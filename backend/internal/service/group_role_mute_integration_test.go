package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"my-im/internal/apperror"
	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/migrate"
	"my-im/internal/model"
	redisscripts "my-im/internal/redis"
	"my-im/internal/repository"
)

// Run with local Docker MySQL and Redis:
//
//	MYIM_INTEGRATION=1 go test ./internal/service -run TestGroupRoleMuteDockerIntegration -v
func TestGroupRoleMuteDockerIntegration(t *testing.T) {
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

	var (
		ownerID, targetID, ordinaryID, peerAdminID int64
		groupID                                    int64
	)
	stamp := time.Now().UnixNano()
	mutedClientMsgID := fmt.Sprintf("group-role-mute-rejected-%d", stamp)
	expiredClientMsgID := fmt.Sprintf("group-role-mute-expired-%d", stamp)
	lostCacheClientMsgID := fmt.Sprintf("group-role-mute-cache-loss-%d", stamp)

	defer func() {
		cleanupCtx := context.Background()
		keys := []string{
			fmt.Sprintf("group_members:%d", groupID),
			fmt.Sprintf("group_member_info:%d", groupID),
			fmt.Sprintf("group_member_loaded:%d", groupID),
			fmt.Sprintf("group_reverse_owner_index:%d", groupID),
			fmt.Sprintf("group_reverse_owner_index_loaded:%d", groupID),
			fmt.Sprintf("cache_warm_lock:group_members:%d", groupID),
			fmt.Sprintf("group_seq:%d", groupID),
			fmt.Sprintf("msg_dedup:%d:%s", targetID, mutedClientMsgID),
			fmt.Sprintf("msg_dedup:%d:%s", ordinaryID, expiredClientMsgID),
			fmt.Sprintf("msg_dedup:%d:%s", ordinaryID, lostCacheClientMsgID),
		}
		for _, userID := range []int64{ownerID, targetID, ordinaryID, peerAdminID} {
			if userID > 0 {
				keys = append(keys, fmt.Sprintf("user_groups:%d", userID))
			}
		}
		if groupID > 0 {
			_ = redisClient.Del(cleanupCtx, keys...).Err()
			_, _ = db.ExecContext(cleanupCtx,
				`DELETE FROM cache_reconcile_events WHERE resource_type = 'group_members' AND resource_id = ?`, groupID)
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM group_members WHERE group_id = ?`, groupID)
			_, _ = db.ExecContext(cleanupCtx, "DELETE FROM `groups` WHERE id = ?", groupID)
		}
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM users WHERE id IN (?, ?, ?, ?)`,
			ownerID, targetID, ordinaryID, peerAdminID)
	}()

	ownerID = insertGroupRoleMuteITUser(t, ctx, db, fmt.Sprintf("group_role_owner_%d", stamp))
	targetID = insertGroupRoleMuteITUser(t, ctx, db, fmt.Sprintf("group_role_target_%d", stamp))
	ordinaryID = insertGroupRoleMuteITUser(t, ctx, db, fmt.Sprintf("group_role_ordinary_%d", stamp))
	peerAdminID = insertGroupRoleMuteITUser(t, ctx, db, fmt.Sprintf("group_role_peer_admin_%d", stamp))

	mysqlRepo := repository.NewMySQLRepo(db)
	redisRepo := repository.NewRedisRepo(redisClient)
	cacheTruth := NewCacheTruthService(mysqlRepo, redisRepo, CacheTruthOptions{})
	groups, err := NewGroupService(mysqlRepo, WithGroupCache(cacheTruth))
	require.NoError(t, err)

	groupID, err = groups.Create(ctx, ownerID, "GROUP-003 integration", "role and mute")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO group_members(group_id, user_id, role)
		VALUES (?, ?, ?), (?, ?, ?), (?, ?, ?)`,
		groupID, targetID, model.GroupRoleMember,
		groupID, ordinaryID, model.GroupRoleMember,
		groupID, peerAdminID, model.GroupRoleAdmin,
	)
	require.NoError(t, err)
	require.NoError(t, cacheTruth.ReconcileGroupMembers(ctx, groupID))
	require.Equal(t, "4", redisClient.Get(ctx, fmt.Sprintf("group_member_loaded:%d", groupID)).Val())

	// 1. Mute role=0 first. The Redis field must contain both independent
	// dimensions; a partial HSET that stores only muted_until would lose role.
	deadline := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second).Add(789 * time.Millisecond)
	wantStoredDeadline := deadline.Truncate(time.Second)
	require.NoError(t, groups.MuteMember(ctx, groupID, ownerID, targetID, &deadline))
	dbRole, dbMutedUntil := readGroupRoleMuteITMySQL(t, ctx, db, groupID, targetID)
	require.Equal(t, model.GroupRoleMember, dbRole)
	require.True(t, dbMutedUntil.Valid)
	require.Equal(t, wantStoredDeadline, dbMutedUntil.Time)
	info := readGroupRoleMuteITRedis(t, ctx, redisClient, groupID, targetID)
	require.Equal(t, model.GroupRoleMember, info.Role)
	require.NotNil(t, info.MutedUntil)
	require.Equal(t, dbMutedUntil.Time.UnixMilli(), *info.MutedUntil)
	eventCountBeforeRetry := countGroupRoleMuteITEvents(t, ctx, db, groupID)
	require.NoError(t, groups.MuteMember(ctx, groupID, ownerID, targetID, &deadline))
	require.Equal(t, eventCountBeforeRetry, countGroupRoleMuteITEvents(t, ctx, db, groupID),
		"a millisecond-bearing retry must normalize to the stored second and remain a no-op")

	// 2. Promote the same muted member. Rebuilding from complete MySQL truth
	// must update role while preserving the existing mute deadline.
	require.NoError(t, groups.UpdateRole(ctx, groupID, ownerID, targetID, model.GroupRoleAdmin))
	dbRole, promotedMutedUntil := readGroupRoleMuteITMySQL(t, ctx, db, groupID, targetID)
	require.Equal(t, model.GroupRoleAdmin, dbRole)
	require.True(t, promotedMutedUntil.Valid)
	require.Equal(t, dbMutedUntil.Time.UnixMilli(), promotedMutedUntil.Time.UnixMilli())
	info = readGroupRoleMuteITRedis(t, ctx, redisClient, groupID, targetID)
	require.Equal(t, model.GroupRoleAdmin, info.Role)
	require.NotNil(t, info.MutedUntil)
	require.Equal(t, promotedMutedUntil.Time.UnixMilli(), *info.MutedUntil)

	// The raw Lua result is 2, while the repository contract maps that rule to
	// public code 5002. Rejection happens before dedup and sequence allocation.
	rawResult, err := redisscripts.ExecGroupMsgCheck(redisClient, ctx, groupID, targetID, mutedClientMsgID)
	require.NoError(t, err)
	require.Equal(t, redisscripts.GMErrMuted, rawResult.ErrCode)
	wrappedResult, err := redisRepo.ExecGroupMsgCheck(ctx, groupID, targetID, mutedClientMsgID)
	require.Nil(t, wrappedResult)
	requireGroupRoleMuteITLuaCode(t, err, redisscripts.CodeGMMuted)
	requireGroupRoleMuteITKeyMissing(t, ctx, redisClient, fmt.Sprintf("msg_dedup:%d:%s", targetID, mutedClientMsgID))
	requireGroupRoleMuteITKeyMissing(t, ctx, redisClient, fmt.Sprintf("group_seq:%d", groupID))

	// 3. Permission boundaries: an admin may mute an ordinary member, but may
	// not touch the owner/admin peer or change roles; ordinary members manage no
	// one. Denied requests must leave authoritative rows unchanged.
	requireGroupRoleMuteITAppCode(t,
		groups.MuteMember(ctx, groupID, targetID, ownerID, &deadline), apperror.CodeNotOwnerOrAdmin)
	requireGroupRoleMuteITAppCode(t,
		groups.MuteMember(ctx, groupID, targetID, peerAdminID, &deadline), apperror.CodeNotOwnerOrAdmin)
	requireGroupRoleMuteITAppCode(t,
		groups.MuteMember(ctx, groupID, ordinaryID, targetID, &deadline), apperror.CodeNotOwnerOrAdmin)
	requireGroupRoleMuteITAppCode(t,
		groups.UpdateRole(ctx, groupID, targetID, peerAdminID, model.GroupRoleMember), apperror.CodeNotOwnerOrAdmin)
	requireGroupRoleMuteITAppCode(t,
		groups.UpdateRole(ctx, groupID, ownerID, ownerID, model.GroupRoleMember), apperror.CodeNotOwnerOrAdmin)
	peerRole, peerMute := readGroupRoleMuteITMySQL(t, ctx, db, groupID, peerAdminID)
	require.Equal(t, model.GroupRoleAdmin, peerRole)
	require.False(t, peerMute.Valid)
	require.NoError(t, groups.MuteMember(ctx, groupID, targetID, ordinaryID, &deadline))
	require.NoError(t, groups.MuteMember(ctx, groupID, targetID, ordinaryID, nil))

	// 4. Unmute must remove only muted_until and preserve role=1. The very same
	// client_msg_id rejected above is now accepted, proving rejection did not
	// consume dedup state.
	require.NoError(t, groups.MuteMember(ctx, groupID, ownerID, targetID, nil))
	dbRole, dbMutedUntil = readGroupRoleMuteITMySQL(t, ctx, db, groupID, targetID)
	require.Equal(t, model.GroupRoleAdmin, dbRole)
	require.False(t, dbMutedUntil.Valid)
	info = readGroupRoleMuteITRedis(t, ctx, redisClient, groupID, targetID)
	require.Equal(t, model.GroupRoleAdmin, info.Role)
	require.Nil(t, info.MutedUntil)
	allowed, err := redisRepo.ExecGroupMsgCheck(ctx, groupID, targetID, mutedClientMsgID)
	require.NoError(t, err)
	require.NotNil(t, allowed)
	require.EqualValues(t, 1, allowed.GroupSeq)

	// 5. An expired absolute deadline stays useful audit information in the
	// Hash, but Lua compares it with Redis TIME and permits sending immediately.
	expiredAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	_, err = db.ExecContext(ctx,
		`UPDATE group_members SET muted_until = ? WHERE group_id = ? AND user_id = ?`,
		expiredAt, groupID, ordinaryID)
	require.NoError(t, err)
	require.NoError(t, cacheTruth.ReconcileGroupMembers(ctx, groupID))
	info = readGroupRoleMuteITRedis(t, ctx, redisClient, groupID, ordinaryID)
	require.NotNil(t, info.MutedUntil)
	expiredAllowed, err := redisRepo.ExecGroupMsgCheck(ctx, groupID, ordinaryID, expiredClientMsgID)
	require.NoError(t, err)
	require.NotNil(t, expiredAllowed)

	// 6. If Redis independently loses Set/Hash data, the cardinality marker is
	// no longer trusted. EnsureGroupAccess reloads the complete current row from
	// MySQL, including a future mute, before Lua executes.
	lostCacheDeadline := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Second)
	require.NoError(t, groups.MuteMember(ctx, groupID, targetID, ordinaryID, &lostCacheDeadline))
	require.NoError(t, redisClient.Del(ctx,
		fmt.Sprintf("group_members:%d", groupID),
		fmt.Sprintf("group_member_info:%d", groupID),
	).Err())
	require.NoError(t, cacheTruth.EnsureGroupAccess(ctx, groupID))
	info = readGroupRoleMuteITRedis(t, ctx, redisClient, groupID, ordinaryID)
	require.Equal(t, model.GroupRoleMember, info.Role)
	require.NotNil(t, info.MutedUntil)
	lostCacheResult, err := redisRepo.ExecGroupMsgCheck(ctx, groupID, ordinaryID, lostCacheClientMsgID)
	require.Nil(t, lostCacheResult)
	requireGroupRoleMuteITLuaCode(t, err, redisscripts.CodeGMMuted)

	// 7. Durable events contain only groupID, not an old role/mute value. Even if
	// stale data lands in Redis late, processing any pending event rereads current
	// MySQL truth, so it cannot resurrect role=0 or the old mute deadline.
	staleDeadline := time.Now().UTC().Add(time.Hour).UnixMilli()
	staleJSON := fmt.Sprintf(`{"role":0,"muted_until":%d}`, staleDeadline)
	require.NoError(t, redisClient.HSet(ctx,
		fmt.Sprintf("group_member_info:%d", groupID), targetID, staleJSON).Err())
	issue, err := cacheTruth.auditGroup(ctx, groupID)
	require.NoError(t, err)
	require.NotNil(t, issue)
	reconciler := NewCacheReconciler(mysqlRepo, cacheTruth, fmt.Sprintf("group-role-mute-%d", stamp), CacheReconcilerOptions{
		BatchSize: 1000,
	})
	processed, err := reconciler.RunOnce(ctx)
	require.NoError(t, err)
	require.Positive(t, processed)
	info = readGroupRoleMuteITRedis(t, ctx, redisClient, groupID, targetID)
	require.Equal(t, model.GroupRoleAdmin, info.Role)
	require.Nil(t, info.MutedUntil)
	issue, err = cacheTruth.auditGroup(ctx, groupID)
	require.NoError(t, err)
	require.Nil(t, issue)
}

type groupRoleMuteITInfo struct {
	Role       int    `json:"role"`
	MutedUntil *int64 `json:"muted_until,omitempty"`
}

func insertGroupRoleMuteITUser(t *testing.T, ctx context.Context, db *sql.DB, username string) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, nickname) VALUES (?, ?, ?)`, username, "hash", username)
	require.NoError(t, err)
	userID, err := result.LastInsertId()
	require.NoError(t, err)
	return userID
}

func readGroupRoleMuteITMySQL(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	groupID, userID int64,
) (int, sql.NullTime) {
	t.Helper()
	var (
		role       int
		mutedUntil sql.NullTime
	)
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT role, muted_until FROM group_members WHERE group_id = ? AND user_id = ?`,
		groupID, userID,
	).Scan(&role, &mutedUntil))
	return role, mutedUntil
}

func readGroupRoleMuteITRedis(
	t *testing.T,
	ctx context.Context,
	client *goredis.Client,
	groupID, userID int64,
) groupRoleMuteITInfo {
	t.Helper()
	encoded, err := client.HGet(ctx,
		fmt.Sprintf("group_member_info:%d", groupID), fmt.Sprint(userID),
	).Result()
	require.NoError(t, err)
	var info groupRoleMuteITInfo
	require.NoError(t, json.Unmarshal([]byte(encoded), &info))
	return info
}

func requireGroupRoleMuteITAppCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	require.Error(t, err)
	var applicationError *apperror.Error
	require.True(t, errors.As(err, &applicationError))
	require.Equal(t, code, applicationError.Code)
}

func requireGroupRoleMuteITLuaCode(t *testing.T, err error, code int) {
	t.Helper()
	require.Error(t, err)
	var ruleError *repository.LuaRuleError
	require.True(t, errors.As(err, &ruleError))
	require.Equal(t, code, ruleError.Code)
}

func requireGroupRoleMuteITKeyMissing(t *testing.T, ctx context.Context, client *goredis.Client, key string) {
	t.Helper()
	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	require.Zero(t, exists, "%s must not exist", key)
}

func countGroupRoleMuteITEvents(t *testing.T, ctx context.Context, db *sql.DB, groupID int64) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cache_reconcile_events WHERE resource_type = 'group_members' AND resource_id = ?`,
		groupID,
	).Scan(&count))
	return count
}
