package service

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"my-im/internal/repository"
)

// Run with local Docker MySQL and Redis:
//
//	MYIM_INTEGRATION=1 go test ./internal/service -run TestGroupLifecycleDockerIntegration -v
func TestGroupLifecycleDockerIntegration(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker MySQL and Redis running")
	}

	ctx := context.Background()
	db, err := infra.OpenMySQL(ctx, config.MySQLConfig{
		Host: "127.0.0.1", Port: 13306, User: "my_im", Password: "my_im123", DBName: "my_im",
		ConnectTimeoutMS: 3000, QueryTimeoutMS: 3000, MaxOpenConns: 10, MaxIdleConns: 5,
	})
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrate.New(db, "../../scripts/migrations").Up(ctx))

	redisClient, err := infra.OpenRedis(ctx, config.RedisConfig{
		Addr: "127.0.0.1:16379", DialTimeoutMS: 3000, ReadTimeoutMS: 2000,
		WriteTimeoutMS: 2000, PoolSize: 10, HealthTimeoutMS: 1000,
	})
	require.NoError(t, err)
	defer redisClient.Close()

	var ownerID, newOwnerID, observerID, groupID int64
	defer func() {
		cleanupCtx := context.Background()
		keys := []string{
			fmt.Sprintf("group_members:%d", groupID),
			fmt.Sprintf("group_member_info:%d", groupID),
			fmt.Sprintf("group_member_loaded:%d", groupID),
			fmt.Sprintf("group_reverse_owner_index:%d", groupID),
			fmt.Sprintf("group_reverse_owner_index_loaded:%d", groupID),
			fmt.Sprintf("cache_warm_lock:group_members:%d", groupID),
		}
		for _, userID := range []int64{ownerID, newOwnerID, observerID} {
			if userID > 0 {
				keys = append(keys, fmt.Sprintf("user_groups:%d", userID))
			}
		}
		_ = redisClient.Del(cleanupCtx, keys...).Err()
		_, _ = db.ExecContext(cleanupCtx,
			`DELETE FROM cache_reconcile_events WHERE resource_type = 'group_members' AND resource_id = ?`, groupID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM group_members WHERE group_id = ?`, groupID)
		_, _ = db.ExecContext(cleanupCtx, "DELETE FROM `groups` WHERE id = ?", groupID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM users WHERE id IN (?, ?, ?)`, ownerID, newOwnerID, observerID)
	}()

	stamp := time.Now().UnixNano()
	ownerID = insertGroupLifecycleITUser(t, ctx, db, fmt.Sprintf("group_lifecycle_owner_%d", stamp))
	newOwnerID = insertGroupLifecycleITUser(t, ctx, db, fmt.Sprintf("group_lifecycle_successor_%d", stamp))
	observerID = insertGroupLifecycleITUser(t, ctx, db, fmt.Sprintf("group_lifecycle_observer_%d", stamp))

	mysqlRepo := repository.NewMySQLRepo(db)
	redisRepo := repository.NewRedisRepo(redisClient)
	cacheTruth := NewCacheTruthService(mysqlRepo, redisRepo, CacheTruthOptions{})
	groups, err := NewGroupService(mysqlRepo, WithGroupCache(cacheTruth))
	require.NoError(t, err)

	groupID, err = groups.Create(ctx, ownerID, "GROUP-004 integration", "ownership and leave")
	require.NoError(t, err)
	muteDeadline := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	_, err = db.ExecContext(ctx, `INSERT INTO group_members(group_id, user_id, role, muted_until)
		VALUES (?, ?, ?, ?), (?, ?, ?, NULL)`,
		groupID, newOwnerID, model.GroupRoleAdmin, muteDeadline,
		groupID, observerID, model.GroupRoleMember,
	)
	require.NoError(t, err)
	require.NoError(t, cacheTruth.ReconcileGroupMembers(ctx, groupID))
	require.Equal(t, "3", redisClient.Get(ctx, fmt.Sprintf("group_member_loaded:%d", groupID)).Val())
	require.NoError(t, deleteGroupLifecycleITEvents(ctx, db, groupID))

	// Simulate Redis being unavailable only on the post-commit fast path. The
	// domain transaction must still commit MySQL plus its durable reconcile
	// command; Redis intentionally remains on the old snapshot until the worker.
	groupsWithFailedFastPath, err := NewGroupService(mysqlRepo, WithGroupCache(failingGroupLifecycleCache{}))
	require.NoError(t, err)
	require.NoError(t, groupsWithFailedFastPath.TransferOwnership(ctx, groupID, ownerID, newOwnerID))

	var storedOwnerID int64
	var storedName, storedNotice string
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT owner_id, name, COALESCE(notice, '') FROM `groups` WHERE id = ?", groupID).
		Scan(&storedOwnerID, &storedName, &storedNotice))
	require.Equal(t, newOwnerID, storedOwnerID)
	require.Equal(t, "GROUP-004 integration", storedName, "ownership update must not overwrite the group name")
	require.Equal(t, "ownership and leave", storedNotice, "ownership update must not overwrite the notice")
	oldRole, oldMute := readGroupLifecycleITMySQL(t, ctx, db, groupID, ownerID)
	newRole, newMute := readGroupLifecycleITMySQL(t, ctx, db, groupID, newOwnerID)
	require.Equal(t, model.GroupRoleMember, oldRole)
	require.False(t, oldMute.Valid)
	require.Equal(t, model.GroupRoleOwner, newRole)
	require.False(t, newMute.Valid, "a muted member must be unmuted when becoming owner")

	staleOld := readGroupLifecycleITRedis(t, ctx, redisClient, groupID, ownerID)
	staleNew := readGroupLifecycleITRedis(t, ctx, redisClient, groupID, newOwnerID)
	require.Equal(t, model.GroupRoleOwner, staleOld.Role, "failed fast path should leave the old snapshot for this test")
	require.Equal(t, model.GroupRoleAdmin, staleNew.Role)
	require.NotZero(t, staleNew.MutedUntil)
	require.Equal(t, 1, countGroupLifecycleITEvents(t, ctx, db, groupID))

	reconciler := NewCacheReconciler(mysqlRepo, cacheTruth, fmt.Sprintf("group-lifecycle-%d", stamp), CacheReconcilerOptions{
		BatchSize: 1000,
	})
	processed, err := reconciler.RunOnce(ctx)
	require.NoError(t, err)
	require.Positive(t, processed)
	require.Equal(t, 0, countGroupLifecycleITEvents(t, ctx, db, groupID))

	currentOld := readGroupLifecycleITRedis(t, ctx, redisClient, groupID, ownerID)
	currentNew := readGroupLifecycleITRedis(t, ctx, redisClient, groupID, newOwnerID)
	require.Equal(t, model.GroupRoleMember, currentOld.Role)
	require.Equal(t, model.GroupRoleOwner, currentNew.Role)
	require.Zero(t, currentNew.MutedUntil)
	requireGroupLifecycleITMembership(t, ctx, redisClient, groupID, ownerID, true)
	requireGroupLifecycleITMembership(t, ctx, redisClient, groupID, newOwnerID, true)
	issue, err := cacheTruth.auditGroup(ctx, groupID)
	require.NoError(t, err)
	require.Nil(t, issue)

	// The current owner cannot leave and the denied command creates no repair
	// event. After ownership was transferred, the old owner is an ordinary
	// member and may leave through the same endpoint.
	requireGroupLifecycleITCode(t,
		groupsWithFailedFastPath.Leave(ctx, groupID, newOwnerID), apperror.CodeCannotLeaveAsOwner)
	require.Equal(t, 0, countGroupLifecycleITEvents(t, ctx, db, groupID))
	require.NoError(t, groupsWithFailedFastPath.Leave(ctx, groupID, ownerID))

	var oldRows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM group_members WHERE group_id = ? AND user_id = ?`, groupID, ownerID).Scan(&oldRows))
	require.Zero(t, oldRows)
	// Still stale because the intentionally broken immediate cache refresh
	// cannot remove any of the forward, metadata, reverse or owner-index edges.
	requireGroupLifecycleITMembership(t, ctx, redisClient, groupID, ownerID, true)
	require.Equal(t, 1, countGroupLifecycleITEvents(t, ctx, db, groupID))

	processed, err = reconciler.RunOnce(ctx)
	require.NoError(t, err)
	require.Positive(t, processed)
	require.Equal(t, 0, countGroupLifecycleITEvents(t, ctx, db, groupID))
	requireGroupLifecycleITMembership(t, ctx, redisClient, groupID, ownerID, false)
	requireGroupLifecycleITMembership(t, ctx, redisClient, groupID, newOwnerID, true)
	requireGroupLifecycleITMembership(t, ctx, redisClient, groupID, observerID, true)
	require.Equal(t, "2", redisClient.Get(ctx, fmt.Sprintf("group_member_loaded:%d", groupID)).Val())
	issue, err = cacheTruth.auditGroup(ctx, groupID)
	require.NoError(t, err)
	require.Nil(t, issue, "eventual rebuild must match MySQL after leave")

	// Re-add the former owner as an ordinary member, then race two transfer
	// commands from the same current owner. The group row lock must let exactly
	// one command commit; the waiter re-reads owner_id and loses authority.
	_, err = db.ExecContext(ctx,
		`INSERT INTO group_members(group_id, user_id, role, muted_until) VALUES (?, ?, ?, NULL)`,
		groupID, ownerID, model.GroupRoleMember)
	require.NoError(t, err)
	require.NoError(t, deleteGroupLifecycleITEvents(ctx, db, groupID))

	start := make(chan struct{})
	results := make(chan error, 2)
	var transfers sync.WaitGroup
	for _, targetID := range []int64{ownerID, observerID} {
		targetID := targetID
		transfers.Add(1)
		go func() {
			defer transfers.Done()
			<-start
			results <- groupsWithFailedFastPath.TransferOwnership(ctx, groupID, newOwnerID, targetID)
		}()
	}
	close(start)
	transfers.Wait()
	close(results)

	successes := 0
	for transferErr := range results {
		if transferErr == nil {
			successes++
			continue
		}
		requireGroupLifecycleITCode(t, transferErr, apperror.CodeNotOwnerOrAdmin)
	}
	require.Equal(t, 1, successes, "two concurrent transfers from one owner must have one winner")

	var concurrentOwnerID int64
	var roleTwoRows int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT owner_id FROM `groups` WHERE id = ?", groupID).Scan(&concurrentOwnerID))
	require.Contains(t, []int64{ownerID, observerID}, concurrentOwnerID)
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM group_members WHERE group_id = ? AND role = ?`,
		groupID, model.GroupRoleOwner).Scan(&roleTwoRows))
	require.Equal(t, 1, roleTwoRows)
	formerRole, _ := readGroupLifecycleITMySQL(t, ctx, db, groupID, newOwnerID)
	require.Equal(t, model.GroupRoleMember, formerRole)
	winnerRole, _ := readGroupLifecycleITMySQL(t, ctx, db, groupID, concurrentOwnerID)
	require.Equal(t, model.GroupRoleOwner, winnerRole)
	require.Equal(t, 1, countGroupLifecycleITEvents(t, ctx, db, groupID))

	processed, err = reconciler.RunOnce(ctx)
	require.NoError(t, err)
	require.Positive(t, processed)
	issue, err = cacheTruth.auditGroup(ctx, groupID)
	require.NoError(t, err)
	require.Nil(t, issue, "the concurrent winner must also reconcile to Redis")
}

type failingGroupLifecycleCache struct{}

func (failingGroupLifecycleCache) ReconcileGroupMembers(context.Context, int64) error {
	return errors.New("intentional GROUP-004 fast-path cache failure")
}

type groupLifecycleITInfo struct {
	Role       int   `json:"role"`
	MutedUntil int64 `json:"muted_until,omitempty"`
}

func insertGroupLifecycleITUser(t *testing.T, ctx context.Context, db *sql.DB, username string) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, nickname) VALUES (?, ?, ?)`, username, "hash", username)
	require.NoError(t, err)
	userID, err := result.LastInsertId()
	require.NoError(t, err)
	return userID
}

func readGroupLifecycleITMySQL(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	groupID, userID int64,
) (int, sql.NullTime) {
	t.Helper()
	var role int
	var mutedUntil sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT role, muted_until FROM group_members WHERE group_id = ? AND user_id = ?`,
		groupID, userID,
	).Scan(&role, &mutedUntil))
	return role, mutedUntil
}

func readGroupLifecycleITRedis(
	t *testing.T,
	ctx context.Context,
	client *goredis.Client,
	groupID, userID int64,
) groupLifecycleITInfo {
	t.Helper()
	encoded, err := client.HGet(ctx,
		fmt.Sprintf("group_member_info:%d", groupID), fmt.Sprint(userID),
	).Result()
	require.NoError(t, err)
	var info groupLifecycleITInfo
	require.NoError(t, json.Unmarshal([]byte(encoded), &info))
	return info
}

func requireGroupLifecycleITMembership(
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

func requireGroupLifecycleITCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	require.Error(t, err)
	var applicationError *apperror.Error
	require.True(t, errors.As(err, &applicationError))
	require.Equal(t, code, applicationError.Code)
}

func countGroupLifecycleITEvents(t *testing.T, ctx context.Context, db *sql.DB, groupID int64) int {
	t.Helper()
	var total int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cache_reconcile_events
		WHERE resource_type = 'group_members' AND resource_id = ? AND status IN (0, 1)`, groupID).Scan(&total))
	return total
}

func deleteGroupLifecycleITEvents(ctx context.Context, db *sql.DB, groupID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM cache_reconcile_events
		WHERE resource_type = 'group_members' AND resource_id = ?`, groupID)
	return err
}
