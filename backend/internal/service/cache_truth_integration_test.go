package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/migrate"
	redisscripts "my-im/internal/redis"
	"my-im/internal/repository"
)

// Run with Docker MySQL/Redis:
//
//	MYIM_INTEGRATION=1 go test ./internal/service -run TestCacheTruthDockerIntegration -v
func TestCacheTruthDockerIntegration(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker MySQL and Redis running")
	}
	ctx := context.Background()
	db, err := infra.OpenMySQL(ctx, config.MySQLConfig{
		Host: "127.0.0.1", Port: 13306, User: "my_im", Password: "my_im123", DBName: "my_im",
		ConnectTimeoutMS: 3000, QueryTimeoutMS: 3000, MaxOpenConns: 5, MaxIdleConns: 2,
	})
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrate.New(db, "../../scripts/migrations").Up(ctx))

	redisClient, err := infra.OpenRedis(ctx, config.RedisConfig{
		Addr: "127.0.0.1:16379", DialTimeoutMS: 3000, ReadTimeoutMS: 2000,
		WriteTimeoutMS: 2000, PoolSize: 5, HealthTimeoutMS: 1000,
	})
	require.NoError(t, err)
	defer redisClient.Close()

	stamp := time.Now().UnixNano()
	result, err := db.ExecContext(ctx, `INSERT INTO users(username,password_hash,nickname) VALUES(?,?,?)`, fmt.Sprintf("cache_it_a_%d", stamp), "hash", "A")
	require.NoError(t, err)
	userA, err := result.LastInsertId()
	require.NoError(t, err)
	result, err = db.ExecContext(ctx, `INSERT INTO users(username,password_hash,nickname) VALUES(?,?,?)`, fmt.Sprintf("cache_it_b_%d", stamp), "hash", "B")
	require.NoError(t, err)
	userB, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO friendships(user_id,friend_id) VALUES(?,?),(?,?)`, userA, userB, userB, userA)
	require.NoError(t, err)
	result, err = db.ExecContext(ctx, "INSERT INTO `groups`(name,owner_id) VALUES(?,?)", fmt.Sprintf("cache-it-%d", stamp), userA)
	require.NoError(t, err)
	groupID, err := result.LastInsertId()
	require.NoError(t, err)
	extraReverseUserID := userB + 1_000_000_000
	_, err = db.ExecContext(ctx, `INSERT INTO group_members(group_id,user_id,role) VALUES(?,?,2),(?,?,0)`, groupID, userA, groupID, userB)
	require.NoError(t, err)

	cleanupKeys := []string{
		fmt.Sprintf("friend:%d:%d", userA, userB), fmt.Sprintf("friend:%d:%d", userB, userA),
		fmt.Sprintf("friend_loaded:%d", userA), fmt.Sprintf("friend_loaded:%d", userB),
		fmt.Sprintf("friend_owner_index:%d", userA), fmt.Sprintf("friend_owner_index:%d", userB),
		fmt.Sprintf("friend_owner_index_loaded:%d", userA), fmt.Sprintf("friend_owner_index_loaded:%d", userB),
		fmt.Sprintf("blacklist:%d", userA), fmt.Sprintf("blacklist:%d", userB),
		fmt.Sprintf("blacklist_loaded:%d", userA), fmt.Sprintf("blacklist_loaded:%d", userB),
		fmt.Sprintf("group_members:%d", groupID), fmt.Sprintf("group_member_info:%d", groupID),
		fmt.Sprintf("group_member_loaded:%d", groupID), fmt.Sprintf("group_reverse_owner_index:%d", groupID),
		fmt.Sprintf("group_reverse_owner_index_loaded:%d", groupID),
		fmt.Sprintf("user_groups:%d", userA),
		fmt.Sprintf("user_groups:%d", userB),
		fmt.Sprintf("user_groups:%d", extraReverseUserID),
		fmt.Sprintf("cache_warm_lock:friends:%d", userA),
		fmt.Sprintf("cache_warm_lock:friends:%d", userB),
		fmt.Sprintf("cache_warm_lock:blacklist:%d", userA),
		fmt.Sprintf("cache_warm_lock:blacklist:%d", userB),
		fmt.Sprintf("cache_warm_lock:group_members:%d", groupID),
	}
	defer func() {
		_ = redisClient.Del(context.Background(), cleanupKeys...).Err()
		_, _ = db.ExecContext(context.Background(), `DELETE FROM blacklist WHERE user_id IN (?,?) OR blocked_id IN (?,?)`, userA, userB, userA, userB)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM friendships WHERE user_id IN (?,?) OR friend_id IN (?,?)`, userA, userB, userA, userB)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM group_members WHERE group_id=?`, groupID)
		_, _ = db.ExecContext(context.Background(), "DELETE FROM `groups` WHERE id=?", groupID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM cache_reconcile_events
			WHERE (resource_type = 'friends' AND resource_id IN (?,?))
			   OR (resource_type = 'blacklist' AND resource_id IN (?,?))
			   OR (resource_type = 'group_members' AND resource_id = ?)`, userA, userB, userA, userB, groupID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM users WHERE id IN (?,?)`, userA, userB)
	}()
	require.NoError(t, redisClient.Del(ctx, cleanupKeys...).Err()) // simulate relationship-cache loss

	mysqlRepo := repository.NewMySQLRepo(db)
	redisRepo := repository.NewRedisRepo(redisClient, repository.WithMessageIDGenerator(&serviceTestMessageIDs{}))
	cacheTruth := NewCacheTruthService(mysqlRepo, redisRepo, CacheTruthOptions{})
	require.NoError(t, cacheTruth.EnsurePrivateAccess(ctx, userA, userB))
	require.NoError(t, cacheTruth.EnsureGroupAccess(ctx, groupID))
	for _, userID := range []int64{userA, userB} {
		present, err := redisClient.SIsMember(ctx, fmt.Sprintf("user_groups:%d", userID), groupID).Result()
		require.NoError(t, err)
		require.True(t, present, "group rebuild must maintain user_groups reverse projection")
	}
	// Before GROUP-003 the loaded marker was the boolean string "1". A rolling
	// upgrade must notice that it cannot describe this two-member projection and
	// transparently rewrite it to the expected Set/Hash cardinality.
	groupLoadedKey := fmt.Sprintf("group_member_loaded:%d", groupID)
	require.NoError(t, redisClient.Set(ctx, groupLoadedKey, "1", 0).Err())
	loaded, err := redisRepo.GroupMembersLoaded(ctx, groupID)
	require.NoError(t, err)
	require.False(t, loaded)
	require.NoError(t, cacheTruth.EnsureGroupAccess(ctx, groupID))
	require.Equal(t, "2", redisClient.Get(ctx, groupLoadedKey).Val())

	// The Set and Hash are both required by group-message authorization. Audit
	// must detect either half disappearing even when the union of IDs still
	// happens to match MySQL.
	require.NoError(t, redisClient.HDel(ctx, fmt.Sprintf("group_member_info:%d", groupID), fmt.Sprint(userB)).Err())
	issue, err := cacheTruth.auditGroup(ctx, groupID)
	require.NoError(t, err)
	require.NotNil(t, issue)
	require.Equal(t, []int64{userB}, issue.MissingInfoIDs)
	require.NoError(t, cacheTruth.EnsureGroupAccess(ctx, groupID), "missing Hash metadata must invalidate the cardinality marker and reload MySQL")
	restoredInfo, err := redisClient.HExists(ctx, fmt.Sprintf("group_member_info:%d", groupID), fmt.Sprint(userB)).Result()
	require.NoError(t, err)
	require.True(t, restoredInfo)
	require.NoError(t, redisClient.SRem(ctx, fmt.Sprintf("group_members:%d", groupID), userB).Err())
	issue, err = cacheTruth.auditGroup(ctx, groupID)
	require.NoError(t, err)
	require.NotNil(t, issue)
	require.Equal(t, []int64{userB}, issue.MissingSetIDs)
	require.NoError(t, cacheTruth.EnsureGroupAccess(ctx, groupID), "missing Set membership must invalidate the cardinality marker and reload MySQL")

	require.NoError(t, redisClient.SAdd(ctx, fmt.Sprintf("user_groups:%d", extraReverseUserID), groupID).Err())
	require.NoError(t, redisClient.SAdd(ctx, fmt.Sprintf("group_reverse_owner_index:%d", groupID), extraReverseUserID).Err())
	issue, err = cacheTruth.auditGroup(ctx, groupID)
	require.NoError(t, err)
	require.NotNil(t, issue)
	require.Equal(t, []int64{extraReverseUserID}, issue.UnexpectedReverseIDs)
	require.NoError(t, cacheTruth.ReconcileNow(ctx, repository.CacheResourceGroupMembers, groupID))
	extraPresent, err := redisClient.SIsMember(ctx, fmt.Sprintf("user_groups:%d", extraReverseUserID), groupID).Result()
	require.NoError(t, err)
	require.False(t, extraPresent, "rebuild must remove orphan reverse membership")

	// A strict, explicitly requested rebuild must also repair corruption that is
	// outside the bounded owner index. Keep both loaded markers, write only the
	// reverse user_groups edge, and deliberately leave the user out of the
	// group_reverse_owner_index Set. A normal fast-path replacement trusts that
	// index; Rebuild(groups) is the maintenance path that scans for this orphan.
	require.NoError(t, redisClient.SAdd(ctx, fmt.Sprintf("user_groups:%d", extraReverseUserID), groupID).Err())
	require.NoError(t, redisClient.SRem(ctx, fmt.Sprintf("group_reverse_owner_index:%d", groupID), extraReverseUserID).Err())
	for _, markerKey := range []string{
		fmt.Sprintf("group_member_loaded:%d", groupID),
		fmt.Sprintf("group_reverse_owner_index_loaded:%d", groupID),
	} {
		markerExists, markerErr := redisClient.Exists(ctx, markerKey).Result()
		require.NoError(t, markerErr)
		require.EqualValues(t, 1, markerExists, "%s must remain loaded while corruption is injected", markerKey)
	}
	indexed, err := redisClient.SIsMember(ctx, fmt.Sprintf("group_reverse_owner_index:%d", groupID), extraReverseUserID).Result()
	require.NoError(t, err)
	require.False(t, indexed, "the regression requires an index-external orphan")
	_, err = cacheTruth.Rebuild(ctx, CacheScopeGroups)
	require.NoError(t, err)
	extraPresent, err = redisClient.SIsMember(ctx, fmt.Sprintf("user_groups:%d", extraReverseUserID), groupID).Result()
	require.NoError(t, err)
	require.False(t, extraPresent, "explicit group rebuild must remove an orphan outside the owner index")

	allowed, err := redisRepo.ExecPrivateMsgCheck(ctx, userA, userB, fmt.Sprintf("cache-it-ok-%d", stamp))
	require.NoError(t, err)
	require.Positive(t, allowed.MessageID, "a cleared Redis cache must recover from MySQL before Lua")

	// A blocks B. A -> B must also be rejected; this is the reverse direction
	// that the original Lua script forgot to inspect.
	_, err = db.ExecContext(ctx, `INSERT INTO blacklist(user_id,blocked_id) VALUES(?,?)`, userA, userB)
	require.NoError(t, err)
	require.NoError(t, cacheTruth.ReconcileNow(ctx, repository.CacheResourceBlacklist, userA))
	blocked, err := redisRepo.ExecPrivateMsgCheck(ctx, userA, userB, fmt.Sprintf("cache-it-blocked-%d", stamp))
	require.Error(t, err)
	var ruleErr *repository.LuaRuleError
	require.ErrorAs(t, err, &ruleErr)
	require.Equal(t, redisscripts.CodePMBlocked, ruleErr.Code)
	require.Nil(t, blocked)

	// Removing a member and rebuilding removes both directions.
	_, err = db.ExecContext(ctx, `DELETE FROM group_members WHERE group_id=? AND user_id=?`, groupID, userB)
	require.NoError(t, err)
	require.NoError(t, cacheTruth.ReconcileNow(ctx, repository.CacheResourceGroupMembers, groupID))
	present, err := redisClient.SIsMember(ctx, fmt.Sprintf("user_groups:%d", userB), groupID).Result()
	require.NoError(t, err)
	require.False(t, present)

	// Rebuilding owner A may delete only friend:A:*; direction B -> A belongs to
	// B's authoritative row and must survive.
	_, err = db.ExecContext(ctx, `DELETE FROM friendships WHERE user_id=? AND friend_id=?`, userA, userB)
	require.NoError(t, err)
	require.NoError(t, cacheTruth.ReconcileNow(ctx, repository.CacheResourceFriends, userA))

	// As above, retain friend_owner_index_loaded but inject friend:A:B without
	// adding B to friend_owner_index:A. Explicit Rebuild(friends) must take the
	// strict scan path and remove this index-external key using current MySQL.
	orphanFriendKey := fmt.Sprintf("friend:%d:%d", userA, userB)
	require.NoError(t, redisClient.Set(ctx, orphanFriendKey, "1", 0).Err())
	require.NoError(t, redisClient.SRem(ctx, fmt.Sprintf("friend_owner_index:%d", userA), userB).Err())
	indexMarkerExists, err := redisClient.Exists(ctx, fmt.Sprintf("friend_owner_index_loaded:%d", userA)).Result()
	require.NoError(t, err)
	require.EqualValues(t, 1, indexMarkerExists, "the index marker must stay loaded while corruption is injected")
	indexed, err = redisClient.SIsMember(ctx, fmt.Sprintf("friend_owner_index:%d", userA), userB).Result()
	require.NoError(t, err)
	require.False(t, indexed, "the regression requires an index-external friend key")
	_, err = cacheTruth.Rebuild(ctx, CacheScopeFriends)
	require.NoError(t, err)
	orphanExists, err := redisClient.Exists(ctx, orphanFriendKey).Result()
	require.NoError(t, err)
	require.EqualValues(t, 0, orphanExists, "explicit friend rebuild must remove an orphan outside the owner index")

	reverseExists, err := redisClient.Exists(ctx, fmt.Sprintf("friend:%d:%d", userB, userA)).Result()
	require.NoError(t, err)
	require.EqualValues(t, 1, reverseExists)
}
