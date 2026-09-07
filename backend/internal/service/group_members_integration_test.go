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
	"my-im/internal/repository"
)

// Run with local Docker MySQL and Redis:
//
//	MYIM_INTEGRATION=1 go test ./internal/service -run TestGroupMembersDockerIntegration -v
func TestGroupMembersDockerIntegration(t *testing.T) {
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
		ownerID, adminID, memberID, adminInviteeID        int64
		nonFriendID, outsiderID, capacityAID, capacityBID int64
		groupID, capacityGroupID                          int64
	)
	defer func() {
		cleanupCtx := context.Background()
		groupIDs := []int64{groupID, capacityGroupID}
		userIDs := []int64{
			ownerID, adminID, memberID, adminInviteeID,
			nonFriendID, outsiderID, capacityAID, capacityBID,
		}
		keys := make([]string, 0, len(groupIDs)*6+len(userIDs))
		for _, id := range groupIDs {
			if id <= 0 {
				continue
			}
			keys = append(keys,
				fmt.Sprintf("group_members:%d", id),
				fmt.Sprintf("group_member_info:%d", id),
				fmt.Sprintf("group_member_loaded:%d", id),
				fmt.Sprintf("group_reverse_owner_index:%d", id),
				fmt.Sprintf("group_reverse_owner_index_loaded:%d", id),
				fmt.Sprintf("cache_warm_lock:group_members:%d", id),
			)
		}
		for _, id := range userIDs {
			if id > 0 {
				keys = append(keys, fmt.Sprintf("user_groups:%d", id))
			}
		}
		if len(keys) > 0 {
			_ = redisClient.Del(cleanupCtx, keys...).Err()
		}

		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM cache_reconcile_events
			WHERE resource_type = 'group_members' AND resource_id IN (?, ?)`, groupID, capacityGroupID)
		_, _ = db.ExecContext(cleanupCtx,
			`DELETE FROM group_members WHERE group_id IN (?, ?)`, groupID, capacityGroupID)
		_, _ = db.ExecContext(cleanupCtx,
			"DELETE FROM `groups` WHERE id IN (?, ?)", groupID, capacityGroupID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM friendships
			WHERE user_id IN (?, ?, ?, ?, ?, ?, ?, ?)
			   OR friend_id IN (?, ?, ?, ?, ?, ?, ?, ?)`,
			ownerID, adminID, memberID, adminInviteeID, nonFriendID, outsiderID, capacityAID, capacityBID,
			ownerID, adminID, memberID, adminInviteeID, nonFriendID, outsiderID, capacityAID, capacityBID,
		)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM users
			WHERE id IN (?, ?, ?, ?, ?, ?, ?, ?)`,
			ownerID, adminID, memberID, adminInviteeID,
			nonFriendID, outsiderID, capacityAID, capacityBID,
		)
	}()

	stamp := time.Now().UnixNano()
	ownerID = insertGroupMembersITUser(t, ctx, db, fmt.Sprintf("group_members_owner_%d", stamp))
	adminID = insertGroupMembersITUser(t, ctx, db, fmt.Sprintf("group_members_admin_%d", stamp))
	memberID = insertGroupMembersITUser(t, ctx, db, fmt.Sprintf("group_members_member_%d", stamp))
	adminInviteeID = insertGroupMembersITUser(t, ctx, db, fmt.Sprintf("group_members_admin_invitee_%d", stamp))
	nonFriendID = insertGroupMembersITUser(t, ctx, db, fmt.Sprintf("group_members_non_friend_%d", stamp))
	outsiderID = insertGroupMembersITUser(t, ctx, db, fmt.Sprintf("group_members_outsider_%d", stamp))
	capacityAID = insertGroupMembersITUser(t, ctx, db, fmt.Sprintf("group_members_capacity_a_%d", stamp))
	capacityBID = insertGroupMembersITUser(t, ctx, db, fmt.Sprintf("group_members_capacity_b_%d", stamp))

	insertGroupMembersITFriendPair(t, ctx, db, ownerID, adminID)
	insertGroupMembersITFriendPair(t, ctx, db, ownerID, memberID)
	insertGroupMembersITFriendPair(t, ctx, db, adminID, adminInviteeID)
	insertGroupMembersITFriendPair(t, ctx, db, ownerID, capacityAID)
	insertGroupMembersITFriendPair(t, ctx, db, ownerID, capacityBID)

	mysqlRepo := repository.NewMySQLRepo(db)
	redisRepo := repository.NewRedisRepo(redisClient)
	cacheTruth := NewCacheTruthService(mysqlRepo, redisRepo, CacheTruthOptions{})
	groups, err := NewGroupService(mysqlRepo, WithGroupCache(cacheTruth))
	require.NoError(t, err)

	groupID, err = groups.Create(ctx, ownerID, "GROUP-002 integration", "members")
	require.NoError(t, err)
	requireGroupMembersITProjection(t, ctx, redisClient, groupID, ownerID, model.GroupRoleOwner, true)

	// Owner and administrators may invite, but the invitee must be the
	// operator's MySQL friend. Redis friendship state is deliberately irrelevant.
	require.NoError(t, groups.AddMember(ctx, groupID, ownerID, adminID))
	requireGroupMembersITProjection(t, ctx, redisClient, groupID, adminID, model.GroupRoleMember, true)
	requireGroupMembersITCode(t, groups.AddMember(ctx, groupID, ownerID, adminID), apperror.CodeAlreadyMember)

	// This GROUP-002 integration test seeds the administrator role directly so
	// it stays focused on member removal instead of depending on another use case.
	// and rebuild the complete projection before exercising the GROUP-002 matrix.
	_, err = db.ExecContext(ctx, `UPDATE group_members SET role = ? WHERE group_id = ? AND user_id = ?`,
		model.GroupRoleAdmin, groupID, adminID)
	require.NoError(t, err)
	require.NoError(t, cacheTruth.ReconcileGroupMembers(ctx, groupID))
	requireGroupMembersITProjection(t, ctx, redisClient, groupID, adminID, model.GroupRoleAdmin, true)

	require.NoError(t, groups.AddMember(ctx, groupID, ownerID, memberID))
	requireGroupMembersITProjection(t, ctx, redisClient, groupID, memberID, model.GroupRoleMember, true)
	requireGroupMembersITCode(t,
		groups.AddMember(ctx, groupID, memberID, nonFriendID), apperror.CodeNotOwnerOrAdmin)
	requireGroupMembersITCode(t,
		groups.AddMember(ctx, groupID, outsiderID, nonFriendID), apperror.CodeNotOwnerOrAdmin)
	requireGroupMembersITCode(t,
		groups.AddMember(ctx, groupID, ownerID, nonFriendID), apperror.CodeMemberNotFriend)

	require.NoError(t, groups.AddMember(ctx, groupID, adminID, adminInviteeID))
	requireGroupMembersITProjection(t, ctx, redisClient, groupID, adminInviteeID, model.GroupRoleMember, true)

	// Paging is authoritative MySQL data. Role and stable join/id ordering put
	// owner and administrator on the first page and return public user profiles.
	firstPage, err := groups.ListMembers(ctx, groupID, memberID, 2, 0)
	require.NoError(t, err)
	require.EqualValues(t, 4, firstPage.Total)
	require.Equal(t, 2, firstPage.Limit)
	require.Zero(t, firstPage.Offset)
	require.Len(t, firstPage.Items, 2)
	require.Equal(t, ownerID, firstPage.Items[0].UserID)
	require.Equal(t, model.GroupRoleOwner, firstPage.Items[0].Role)
	require.NotEmpty(t, firstPage.Items[0].Username)
	require.Equal(t, adminID, firstPage.Items[1].UserID)
	require.Equal(t, model.GroupRoleAdmin, firstPage.Items[1].Role)
	require.NotEmpty(t, firstPage.Items[1].Username)

	secondPage, err := groups.ListMembers(ctx, groupID, memberID, 2, 2)
	require.NoError(t, err)
	require.EqualValues(t, 4, secondPage.Total)
	require.Equal(t, 2, secondPage.Limit)
	require.Equal(t, 2, secondPage.Offset)
	require.Len(t, secondPage.Items, 2)
	require.ElementsMatch(t, []int64{memberID, adminInviteeID}, []int64{
		secondPage.Items[0].UserID, secondPage.Items[1].UserID,
	})
	_, err = groups.ListMembers(ctx, groupID, outsiderID, 20, 0)
	requireGroupMembersITCode(t, err, apperror.CodeGroupNotMember)

	// Ordinary members cannot remove others. Administrators can remove ordinary
	// members, but cannot remove the owner or an administrator peer.
	requireGroupMembersITCode(t,
		groups.RemoveMember(ctx, groupID, memberID, adminInviteeID), apperror.CodeNotOwnerOrAdmin)
	requireGroupMembersITCode(t,
		groups.RemoveMember(ctx, groupID, adminID, ownerID), apperror.CodeCannotRemoveOwner)
	require.NoError(t, groups.RemoveMember(ctx, groupID, adminID, memberID))
	requireGroupMembersITProjection(t, ctx, redisClient, groupID, memberID, model.GroupRoleMember, false)

	_, err = db.ExecContext(ctx, `UPDATE group_members SET role = ? WHERE group_id = ? AND user_id = ?`,
		model.GroupRoleAdmin, groupID, adminInviteeID)
	require.NoError(t, err)
	require.NoError(t, cacheTruth.ReconcileGroupMembers(ctx, groupID))
	requireGroupMembersITCode(t,
		groups.RemoveMember(ctx, groupID, adminID, adminInviteeID), apperror.CodeCannotRemovePeer)
	require.NoError(t, groups.RemoveMember(ctx, groupID, ownerID, adminInviteeID))
	requireGroupMembersITProjection(t, ctx, redisClient, groupID, adminInviteeID, model.GroupRoleAdmin, false)

	issue, err := cacheTruth.auditGroup(ctx, groupID)
	require.NoError(t, err)
	require.Nil(t, issue, "GROUP-002 writes must leave the full Redis projection equal to MySQL")

	var reconcileEvents int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cache_reconcile_events
		WHERE resource_type = 'group_members' AND resource_id = ?`, groupID).Scan(&reconcileEvents))
	require.GreaterOrEqual(t, reconcileEvents, 5, "every committed membership mutation must leave a durable repair event")

	// Two concurrent invitations compete for the only remaining slot. Locking
	// the groups row must serialize count+insert, so exactly one can commit.
	capacityGroupID, err = groups.Create(ctx, ownerID, "GROUP-002 capacity", "one slot")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE `groups` SET max_members = 2 WHERE id = ?", capacityGroupID)
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan struct {
		memberID int64
		err      error
	}, 2)
	for _, candidateID := range []int64{capacityAID, capacityBID} {
		candidateID := candidateID
		go func() {
			<-start
			results <- struct {
				memberID int64
				err      error
			}{candidateID, groups.AddMember(context.Background(), capacityGroupID, ownerID, candidateID)}
		}()
	}
	close(start)

	var winnerID, loserID int64
	for range 2 {
		result := <-results
		if result.err == nil {
			require.Zero(t, winnerID, "only one concurrent invitation may succeed")
			winnerID = result.memberID
			continue
		}
		requireGroupMembersITCode(t, result.err, apperror.CodeGroupFull)
		loserID = result.memberID
	}
	require.NotZero(t, winnerID)
	require.NotZero(t, loserID)

	var capacityCount int64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM group_members WHERE group_id = ?`, capacityGroupID).Scan(&capacityCount))
	require.EqualValues(t, 2, capacityCount)
	requireGroupMembersITProjection(t, ctx, redisClient, capacityGroupID, winnerID, model.GroupRoleMember, true)
	requireGroupMembersITProjection(t, ctx, redisClient, capacityGroupID, loserID, model.GroupRoleMember, false)
	issue, err = cacheTruth.auditGroup(ctx, capacityGroupID)
	require.NoError(t, err)
	require.Nil(t, issue)
}

func insertGroupMembersITUser(t *testing.T, ctx context.Context, db *sql.DB, username string) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx,
		`INSERT INTO users(username,password_hash,nickname) VALUES(?,?,?)`, username, "hash", username)
	require.NoError(t, err)
	userID, err := result.LastInsertId()
	require.NoError(t, err)
	return userID
}

func insertGroupMembersITFriendPair(t *testing.T, ctx context.Context, db *sql.DB, firstID, secondID int64) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO friendships(user_id,friend_id) VALUES(?,?),(?,?)`,
		firstID, secondID, secondID, firstID)
	require.NoError(t, err)
}

func requireGroupMembersITCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	require.Error(t, err)
	var applicationError *apperror.Error
	require.True(t, errors.As(err, &applicationError))
	require.Equal(t, code, applicationError.Code)
}

func requireGroupMembersITProjection(
	t *testing.T,
	ctx context.Context,
	redisClient *goredis.Client,
	groupID, userID int64,
	wantRole int,
	wantPresent bool,
) {
	t.Helper()
	groupKey := fmt.Sprintf("group_members:%d", groupID)
	reverseKey := fmt.Sprintf("user_groups:%d", userID)
	infoKey := fmt.Sprintf("group_member_info:%d", groupID)
	ownerIndexKey := fmt.Sprintf("group_reverse_owner_index:%d", groupID)

	forward, err := redisClient.SIsMember(ctx, groupKey, userID).Result()
	require.NoError(t, err)
	require.Equal(t, wantPresent, forward, "forward group membership")
	reverse, err := redisClient.SIsMember(ctx, reverseKey, groupID).Result()
	require.NoError(t, err)
	require.Equal(t, wantPresent, reverse, "reverse user membership")
	indexed, err := redisClient.SIsMember(ctx, ownerIndexKey, userID).Result()
	require.NoError(t, err)
	require.Equal(t, wantPresent, indexed, "bounded reverse-owner index")

	encoded, err := redisClient.HGet(ctx, infoKey, fmt.Sprint(userID)).Result()
	if !wantPresent {
		require.ErrorIs(t, err, goredis.Nil)
	} else {
		require.NoError(t, err)
		var info struct {
			Role       int   `json:"role"`
			MutedUntil int64 `json:"muted_until,omitempty"`
		}
		require.NoError(t, json.Unmarshal([]byte(encoded), &info))
		require.Equal(t, wantRole, info.Role)
		require.Zero(t, info.MutedUntil)
	}

	for _, marker := range []string{
		fmt.Sprintf("group_member_loaded:%d", groupID),
		fmt.Sprintf("group_reverse_owner_index_loaded:%d", groupID),
	} {
		exists, markerErr := redisClient.Exists(ctx, marker).Result()
		require.NoError(t, markerErr)
		require.EqualValues(t, 1, exists, "%s", marker)
	}
}
