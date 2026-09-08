package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"my-im/internal/apperror"
	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/migrate"
	"my-im/internal/model"
	"my-im/internal/protocol"
	redisscripts "my-im/internal/redis"
	"my-im/internal/repository"
)

// Run with local Docker MySQL and Redis:
//
//	MYIM_INTEGRATION=1 go test ./internal/service -run TestGroupDisbandDockerIntegration -v
func TestGroupDisbandDockerIntegration(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker MySQL and Redis running")
	}

	ctx := context.Background()
	db, err := infra.OpenMySQL(ctx, config.MySQLConfig{
		Host: "127.0.0.1", Port: 13306, User: "my_im", Password: "my_im123", DBName: "my_im",
		ConnectTimeoutMS: 3000, QueryTimeoutMS: 5000, MaxOpenConns: 20, MaxIdleConns: 10,
	})
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrate.New(db, "../../scripts/migrations").Up(ctx))

	redisClient, err := infra.OpenRedis(ctx, config.RedisConfig{
		Addr: "127.0.0.1:16379", DialTimeoutMS: 3000, ReadTimeoutMS: 2000,
		WriteTimeoutMS: 2000, PoolSize: 20, HealthTimeoutMS: 1000,
	})
	require.NoError(t, err)
	defer redisClient.Close()

	var userIDs, groupIDs, messageIDs []int64
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for _, groupID := range groupIDs {
			_ = redisClient.Del(cleanupCtx,
				fmt.Sprintf("group_members:%d", groupID),
				fmt.Sprintf("group_member_info:%d", groupID),
				fmt.Sprintf("group_member_loaded:%d", groupID),
				fmt.Sprintf("group_reverse_owner_index:%d", groupID),
				fmt.Sprintf("group_reverse_owner_index_loaded:%d", groupID),
				fmt.Sprintf("cache_warm_lock:group_members:%d", groupID),
				fmt.Sprintf("outbox:%d", groupID),
				fmt.Sprintf("group_seq:%d", groupID),
			).Err()
			_, _ = db.ExecContext(cleanupCtx,
				`DELETE FROM cache_reconcile_events WHERE resource_type = 'group_members' AND resource_id = ?`, groupID)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM group_members WHERE group_id = ?`, groupID)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM group_messages WHERE group_id = ?`, groupID)
			_, _ = db.ExecContext(cleanupCtx, "DELETE FROM `groups` WHERE id = ?", groupID)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM group_tombstones WHERE group_id = ?`, groupID)
		}
		for _, userID := range userIDs {
			_ = redisClient.Del(cleanupCtx, fmt.Sprintf("user_groups:%d", userID)).Err()
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM friendships WHERE user_id = ? OR friend_id = ?`, userID, userID)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM users WHERE id = ?`, userID)
		}
		for _, messageID := range messageIDs {
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM group_messages WHERE id = ?`, messageID)
		}
	})

	stamp := time.Now().UnixNano()
	newUser := func(label string) int64 {
		result, insertErr := db.ExecContext(ctx,
			`INSERT INTO users(username,password_hash,nickname) VALUES(?,?,?)`,
			fmt.Sprintf("group005_%s_%d", label, stamp), "hash", label)
		require.NoError(t, insertErr)
		userID, insertErr := result.LastInsertId()
		require.NoError(t, insertErr)
		userIDs = append(userIDs, userID)
		return userID
	}
	ownerID := newUser("owner")
	adminID := newUser("admin")
	memberID := newUser("member")
	raceCandidateID := newUser("race")
	capacityAID := newUser("capacity_a")
	capacityBID := newUser("capacity_b")
	for _, friendID := range []int64{raceCandidateID, capacityAID, capacityBID} {
		_, err = db.ExecContext(ctx, `INSERT INTO friendships(user_id,friend_id) VALUES(?,?),(?,?)`,
			ownerID, friendID, friendID, ownerID)
		require.NoError(t, err)
	}

	mysqlRepo := repository.NewMySQLRepo(db)
	redisRepo := repository.NewRedisRepo(redisClient)
	cacheTruth := NewCacheTruthService(mysqlRepo, redisRepo, CacheTruthOptions{})
	groups, err := NewGroupService(mysqlRepo, WithGroupCache(cacheTruth))
	require.NoError(t, err)

	// A guessed ID still gets idempotent DELETE semantics, but must not be able
	// to make the server SCAN reverse membership keys or leave permanent Redis
	// markers behind. Only a real dissolution tombstone authorizes cache repair.
	const neverExistingGroupID int64 = 8_000_000_000_000_000_000
	groupIDs = append(groupIDs, neverExistingGroupID)
	require.NoError(t, groups.Disband(ctx, neverExistingGroupID, ownerID))
	require.Zero(t, group005Count(t, ctx, db, "group_tombstones", "group_id = ?", neverExistingGroupID))
	require.Zero(t, countGroup005AllEvents(t, ctx, db, neverExistingGroupID))
	require.Zero(t, redisClient.Exists(ctx,
		fmt.Sprintf("group_member_loaded:%d", neverExistingGroupID),
		fmt.Sprintf("group_reverse_owner_index_loaded:%d", neverExistingGroupID),
	).Val())

	// Main failure-recovery case: the owner disbands a three-person group while
	// MySQL keeps its historical group_messages row.
	groupID, err := groups.Create(ctx, ownerID, "GROUP-005 integration", "retain history")
	require.NoError(t, err)
	groupIDs = append(groupIDs, groupID)
	_, err = db.ExecContext(ctx, `INSERT INTO group_members(group_id,user_id,role) VALUES(?,?,?),(?,?,?)`,
		groupID, adminID, model.GroupRoleAdmin, groupID, memberID, model.GroupRoleMember)
	require.NoError(t, err)
	require.NoError(t, cacheTruth.ReconcileGroupMembers(ctx, groupID))
	require.NoError(t, deleteGroup005Events(ctx, db, groupID))

	messageID := stamp
	messageIDs = append(messageIDs, messageID)
	_, err = db.ExecContext(ctx, `INSERT INTO group_messages
		(id,client_msg_id,group_id,sender_id,content,msg_type,group_seq,created_at)
		VALUES(?,?,?,?,?,?,?,UTC_TIMESTAMP())`,
		messageID, fmt.Sprintf("group005-history-%d", stamp), groupID, ownerID, "retained history", 1, 1)
	require.NoError(t, err)
	require.NoError(t, redisClient.ZAdd(ctx, fmt.Sprintf("outbox:%d", groupID), goredis.Z{
		Score: float64(time.Now().UnixMilli()), Member: "historical-cache-entry",
	}).Err())
	require.NoError(t, redisClient.Set(ctx, fmt.Sprintf("group_seq:%d", groupID), 9, 0).Err())

	notifier := &group005RecordingNotifier{}
	groupsWithFailedFastPath, err := NewGroupService(mysqlRepo,
		WithGroupCache(group005FailingCache{}), WithGroupEventNotifier(notifier))
	require.NoError(t, err)
	requireGroup005Code(t, groupsWithFailedFastPath.Disband(ctx, groupID, adminID), apperror.CodeNotOwnerOrAdmin)
	requireGroup005Code(t, groupsWithFailedFastPath.Disband(ctx, groupID, memberID), apperror.CodeNotOwnerOrAdmin)
	require.Equal(t, 1, group005Count(t, ctx, db, "`groups`", "id = ?", groupID))
	require.Equal(t, 3, group005Count(t, ctx, db, "group_members", "group_id = ?", groupID))
	require.Zero(t, countGroup005PendingEvents(t, ctx, db, groupID))

	require.NoError(t, groupsWithFailedFastPath.Disband(ctx, groupID, ownerID))
	require.Zero(t, group005Count(t, ctx, db, "`groups`", "id = ?", groupID))
	require.Zero(t, group005Count(t, ctx, db, "group_members", "group_id = ?", groupID))
	require.Equal(t, 1, group005Count(t, ctx, db, "group_tombstones", "group_id = ? AND owner_id = ?", groupID, ownerID))
	require.Equal(t, 1, group005Count(t, ctx, db, "group_messages", "id = ?", messageID),
		"hard-disband removes live state but intentionally preserves MySQL history")
	require.Equal(t, 1, countGroup005PendingEvents(t, ctx, db, groupID))
	requireGroup005RedisMembership(t, ctx, redisClient, groupID, ownerID, true)
	requireGroup005RedisMembership(t, ctx, redisClient, groupID, adminID, true)
	require.EqualValues(t, 1, redisClient.ZCard(ctx, fmt.Sprintf("outbox:%d", groupID)).Val())
	require.Equal(t, "9", redisClient.Get(ctx, fmt.Sprintf("group_seq:%d", groupID)).Val())

	calls := notifier.snapshot()
	require.Len(t, calls, 3)
	notified := make([]int64, 0, len(calls))
	for _, call := range calls {
		notified = append(notified, call.userID)
		require.Equal(t, protocol.TypeGroupRemoved, call.eventType)
		require.Equal(t, model.GroupRemovedNotification{
			GroupID: groupID, Reason: model.GroupRemovedReasonDissolved,
		}, call.payload)
	}
	require.ElementsMatch(t, []int64{ownerID, adminID, memberID}, notified)

	reconciler := NewCacheReconciler(mysqlRepo, cacheTruth, fmt.Sprintf("group005-%d", stamp), CacheReconcilerOptions{
		BatchSize: 1000,
	})
	processed, err := reconciler.RunOnce(ctx)
	require.NoError(t, err)
	require.Positive(t, processed)
	require.Zero(t, countGroup005PendingEvents(t, ctx, db, groupID))
	for _, userID := range []int64{ownerID, adminID, memberID} {
		requireGroup005RedisMembership(t, ctx, redisClient, groupID, userID, false)
	}
	require.Equal(t, "0", redisClient.Get(ctx, fmt.Sprintf("group_member_loaded:%d", groupID)).Val(),
		"zero is a deliberate negative-cache marker, not an uncleared member count")
	require.Equal(t, "1", redisClient.Get(ctx, fmt.Sprintf("group_reverse_owner_index_loaded:%d", groupID)).Val())
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("outbox:%d", groupID)).Val())
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("group_seq:%d", groupID)).Val())

	// Simulate restoring Redis from a backup taken before dissolution after the
	// original reconciliation event has already completed. There is no pending
	// event to save us now: the durable tombstone itself must deny authorization
	// before a seemingly self-consistent old positive marker can be trusted.
	restoreGroup005RedisSnapshot(t, ctx, redisClient, groupID, []int64{ownerID, adminID, memberID})
	require.ErrorIs(t, cacheTruth.EnsureGroupAccess(ctx, groupID), ErrGroupDissolved)
	requireGroup005RedisMembership(t, ctx, redisClient, groupID, ownerID, true)

	listedIDs, err := mysqlRepo.ListGroupIDs(ctx, 0, 5000)
	require.NoError(t, err)
	require.Contains(t, listedIDs, groupID, "cache owner enumeration must retain dissolved IDs")
	auditReport, err := cacheTruth.Audit(ctx, CacheScopeGroups)
	require.NoError(t, err)
	require.True(t, group005AuditContains(auditReport, groupID), "audit must inspect restored tombstone state")

	_, err = cacheTruth.Warm(ctx, CacheScopeGroups)
	require.NoError(t, err)
	requireGroup005RedisMembership(t, ctx, redisClient, groupID, ownerID, false)
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("outbox:%d", groupID)).Val())
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("group_seq:%d", groupID)).Val())

	restoreGroup005RedisSnapshot(t, ctx, redisClient, groupID, []int64{ownerID, adminID, memberID})
	_, err = cacheTruth.Rebuild(ctx, CacheScopeGroups)
	require.NoError(t, err)
	requireGroup005RedisMembership(t, ctx, redisClient, groupID, ownerID, false)
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("outbox:%d", groupID)).Val())
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("group_seq:%d", groupID)).Val())

	clientMsgID := fmt.Sprintf("after-disband-%d", stamp)
	result, err := redisscripts.ExecGroupMsgCheck(redisClient, ctx, groupID, ownerID, clientMsgID)
	require.NoError(t, err)
	require.Equal(t, redisscripts.GMErrNotMember, result.ErrCode)
	require.Zero(t, result.GroupSeq)
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("msg_dedup:%d:%s", ownerID, clientMsgID)).Val())
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("group_seq:%d", groupID)).Val())

	// A late old event contains only the group ID. Even when Redis is manually
	// corrupted back to the former membership, the worker re-reads empty MySQL
	// truth instead of replaying an old SADD operation.
	require.NoError(t, redisClient.SAdd(ctx, fmt.Sprintf("group_members:%d", groupID), ownerID).Err())
	require.NoError(t, redisClient.HSet(ctx, fmt.Sprintf("group_member_info:%d", groupID), ownerID, `{"role":2}`).Err())
	require.NoError(t, redisClient.SAdd(ctx, fmt.Sprintf("user_groups:%d", ownerID), groupID).Err())
	require.NoError(t, redisClient.SAdd(ctx, fmt.Sprintf("group_reverse_owner_index:%d", groupID), ownerID).Err())
	require.NoError(t, redisClient.Set(ctx, fmt.Sprintf("group_member_loaded:%d", groupID), 1, 0).Err())
	require.NoError(t, redisClient.Set(ctx, fmt.Sprintf("outbox:%d", groupID), "stale", 0).Err())
	require.NoError(t, redisClient.Set(ctx, fmt.Sprintf("group_seq:%d", groupID), 99, 0).Err())
	require.NoError(t, mysqlRepo.EnqueueCacheReconcile(ctx, repository.CacheResourceGroupMembers, groupID))
	processed, err = reconciler.RunOnce(ctx)
	require.NoError(t, err)
	require.Positive(t, processed)
	requireGroup005RedisMembership(t, ctx, redisClient, groupID, ownerID, false)
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("outbox:%d", groupID)).Val())
	require.Zero(t, redisClient.Exists(ctx, fmt.Sprintf("group_seq:%d", groupID)).Val())

	allEventsBeforeRetry := countGroup005AllEvents(t, ctx, db, groupID)
	require.NoError(t, groupsWithFailedFastPath.Disband(ctx, groupID, ownerID),
		"DELETE retry after a lost response is a successful no-op")
	require.Equal(t, allEventsBeforeRetry, countGroup005AllEvents(t, ctx, db, groupID),
		"an already-absent retry must not create unbounded duplicate events")

	// Two concurrent owner retries against a live group both return the desired
	// final state, but only the transaction that found the row deletes it and
	// writes a durable reconciliation event.
	doubleGroupID, err := groups.Create(ctx, ownerID, "GROUP-005 double disband", "idempotent")
	require.NoError(t, err)
	groupIDs = append(groupIDs, doubleGroupID)
	require.NoError(t, deleteGroup005Events(ctx, db, doubleGroupID))
	startDouble := make(chan struct{})
	doubleResults := make(chan error, 2)
	var doubleWG sync.WaitGroup
	for range 2 {
		doubleWG.Add(1)
		go func() {
			defer doubleWG.Done()
			<-startDouble
			doubleResults <- groupsWithFailedFastPath.Disband(context.Background(), doubleGroupID, ownerID)
		}()
	}
	close(startDouble)
	doubleWG.Wait()
	close(doubleResults)
	for disbandErr := range doubleResults {
		require.NoError(t, disbandErr)
	}
	require.Zero(t, group005Count(t, ctx, db, "`groups`", "id = ?", doubleGroupID))
	require.Zero(t, group005Count(t, ctx, db, "group_members", "group_id = ?", doubleGroupID))
	require.Equal(t, 1, countGroup005PendingEvents(t, ctx, db, doubleGroupID))

	// AddMember and Disband take the same group row lock. Whichever wins first,
	// the final state is fully disbanded: Add either commits before deletion or
	// observes the deletion and returns 1302 afterwards.
	raceGroupID, err := groups.Create(ctx, ownerID, "GROUP-005 add race", "serialized")
	require.NoError(t, err)
	groupIDs = append(groupIDs, raceGroupID)
	require.NoError(t, deleteGroup005Events(ctx, db, raceGroupID))
	startRace := make(chan struct{})
	addResult := make(chan error, 1)
	disbandResult := make(chan error, 1)
	go func() {
		<-startRace
		addResult <- groupsWithFailedFastPath.AddMember(context.Background(), raceGroupID, ownerID, raceCandidateID)
	}()
	go func() {
		<-startRace
		disbandResult <- groupsWithFailedFastPath.Disband(context.Background(), raceGroupID, ownerID)
	}()
	close(startRace)
	addErr := <-addResult
	require.NoError(t, <-disbandResult)
	if addErr != nil {
		requireGroup005Code(t, addErr, apperror.CodeGroupNotFound)
	}
	require.Zero(t, group005Count(t, ctx, db, "`groups`", "id = ?", raceGroupID))
	require.Zero(t, group005Count(t, ctx, db, "group_members", "group_id = ?", raceGroupID))
	requireGroup005Code(t,
		groupsWithFailedFastPath.AddMember(ctx, raceGroupID, ownerID, raceCandidateID),
		apperror.CodeGroupNotFound)

	// Capacity remains a locked MySQL invariant. Two friends compete for the
	// sole remaining slot; exactly one succeeds and the other receives 1304.
	capacityGroupID, err := groups.Create(ctx, ownerID, "GROUP-005 capacity", "one slot")
	require.NoError(t, err)
	groupIDs = append(groupIDs, capacityGroupID)
	_, err = db.ExecContext(ctx, "UPDATE `groups` SET max_members = 2 WHERE id = ?", capacityGroupID)
	require.NoError(t, err)
	require.NoError(t, deleteGroup005Events(ctx, db, capacityGroupID))
	startCapacity := make(chan struct{})
	capacityResults := make(chan error, 2)
	for _, candidateID := range []int64{capacityAID, capacityBID} {
		candidateID := candidateID
		go func() {
			<-startCapacity
			capacityResults <- groupsWithFailedFastPath.AddMember(context.Background(), capacityGroupID, ownerID, candidateID)
		}()
	}
	close(startCapacity)
	successes, fullErrors := 0, 0
	for range 2 {
		capacityErr := <-capacityResults
		if capacityErr == nil {
			successes++
			continue
		}
		requireGroup005Code(t, capacityErr, apperror.CodeGroupFull)
		fullErrors++
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, fullErrors)
	require.Equal(t, 2, group005Count(t, ctx, db, "group_members", "group_id = ?", capacityGroupID))

	// A normal reconciliation of an active group must never delete its message
	// runtime state merely because the cache operation shares GROUP-005 code.
	require.NoError(t, redisClient.Set(ctx, fmt.Sprintf("outbox:%d", capacityGroupID), "active", 0).Err())
	require.NoError(t, redisClient.Set(ctx, fmt.Sprintf("group_seq:%d", capacityGroupID), 7, 0).Err())
	require.NoError(t, cacheTruth.ReconcileGroupMembers(ctx, capacityGroupID))
	require.Equal(t, "active", redisClient.Get(ctx, fmt.Sprintf("outbox:%d", capacityGroupID)).Val())
	require.Equal(t, "7", redisClient.Get(ctx, fmt.Sprintf("group_seq:%d", capacityGroupID)).Val())
	issue, err := cacheTruth.auditGroup(ctx, capacityGroupID)
	require.NoError(t, err)
	require.Nil(t, issue)
}

type group005FailingCache struct{}

func (group005FailingCache) ReconcileGroupMembers(context.Context, int64) error {
	return errors.New("intentional GROUP-005 fast-path cache failure")
}

type group005NotificationCall struct {
	userID    int64
	eventType string
	payload   any
}

type group005RecordingNotifier struct {
	mu    sync.Mutex
	calls []group005NotificationCall
}

func (n *group005RecordingNotifier) NotifyGroupEvent(_ context.Context, userID int64, eventType string, payload any) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, group005NotificationCall{userID: userID, eventType: eventType, payload: payload})
	return nil
}

func (n *group005RecordingNotifier) snapshot() []group005NotificationCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]group005NotificationCall(nil), n.calls...)
}

func deleteGroup005Events(ctx context.Context, db *sql.DB, groupID int64) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM cache_reconcile_events WHERE resource_type = 'group_members' AND resource_id = ?`, groupID)
	return err
}

func countGroup005PendingEvents(t *testing.T, ctx context.Context, db *sql.DB, groupID int64) int {
	t.Helper()
	var total int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cache_reconcile_events
		WHERE resource_type = 'group_members' AND resource_id = ? AND status IN (0,1)`, groupID).Scan(&total))
	return total
}

func countGroup005AllEvents(t *testing.T, ctx context.Context, db *sql.DB, groupID int64) int {
	t.Helper()
	var total int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cache_reconcile_events
		WHERE resource_type = 'group_members' AND resource_id = ?`, groupID).Scan(&total))
	return total
}

func group005Count(t *testing.T, ctx context.Context, db *sql.DB, table, condition string, args ...any) int {
	t.Helper()
	// table/condition are test-owned constants above; values remain parameters.
	var total int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+table+" WHERE "+condition, args...).Scan(&total))
	return total
}

func requireGroup005Code(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	require.Error(t, err)
	var applicationError *apperror.Error
	require.True(t, errors.As(err, &applicationError))
	require.Equal(t, code, applicationError.Code)
}

func requireGroup005RedisMembership(
	t *testing.T,
	ctx context.Context,
	client *goredis.Client,
	groupID, userID int64,
	want bool,
) {
	t.Helper()
	checks := []*goredis.BoolCmd{
		client.SIsMember(ctx, fmt.Sprintf("group_members:%d", groupID), userID),
		client.HExists(ctx, fmt.Sprintf("group_member_info:%d", groupID), fmt.Sprint(userID)),
		client.SIsMember(ctx, fmt.Sprintf("user_groups:%d", userID), groupID),
		client.SIsMember(ctx, fmt.Sprintf("group_reverse_owner_index:%d", groupID), userID),
	}
	for _, check := range checks {
		present, err := check.Result()
		require.NoError(t, err)
		require.Equal(t, want, present)
	}
}

func restoreGroup005RedisSnapshot(
	t *testing.T,
	ctx context.Context,
	client *goredis.Client,
	groupID int64,
	userIDs []int64,
) {
	t.Helper()
	pipe := client.TxPipeline()
	memberKey := fmt.Sprintf("group_members:%d", groupID)
	infoKey := fmt.Sprintf("group_member_info:%d", groupID)
	reverseOwnerKey := fmt.Sprintf("group_reverse_owner_index:%d", groupID)
	pipe.Del(ctx, memberKey, infoKey, reverseOwnerKey)
	for index, userID := range userIDs {
		role := model.GroupRoleMember
		if index == 0 {
			role = model.GroupRoleOwner
		}
		pipe.SAdd(ctx, memberKey, userID)
		pipe.HSet(ctx, infoKey, userID, fmt.Sprintf(`{"role":%d,"muted_until":0}`, role))
		pipe.SAdd(ctx, fmt.Sprintf("user_groups:%d", userID), groupID)
		pipe.SAdd(ctx, reverseOwnerKey, userID)
	}
	pipe.Set(ctx, fmt.Sprintf("group_member_loaded:%d", groupID), len(userIDs), 0)
	pipe.Set(ctx, fmt.Sprintf("group_reverse_owner_index_loaded:%d", groupID), 1, 0)
	pipe.Set(ctx, fmt.Sprintf("outbox:%d", groupID), "restored-old-outbox", 0)
	pipe.Set(ctx, fmt.Sprintf("group_seq:%d", groupID), 99, 0)
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)
}

func group005AuditContains(report CacheAuditReport, groupID int64) bool {
	for _, issue := range report.Issues {
		if issue.Resource == repository.CacheResourceGroupMembers && issue.OwnerID == groupID {
			return true
		}
	}
	return false
}
