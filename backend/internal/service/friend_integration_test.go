package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

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
//	MYIM_INTEGRATION=1 go test ./internal/service -run TestFriendDockerIntegration -v
func TestFriendDockerIntegration(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker MySQL and Redis running")
	}
	ctx := context.Background()
	db, err := infra.OpenMySQL(ctx, config.MySQLConfig{
		Host: "127.0.0.1", Port: 13306, User: "my_im", Password: "my_im123", DBName: "my_im",
		ConnectTimeoutMS: 3000, QueryTimeoutMS: 3000, MaxOpenConns: 8, MaxIdleConns: 4,
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
	userA := insertFriendIntegrationUser(t, ctx, db, fmt.Sprintf("friend_it_a_%d", stamp), "Alice")
	userB := insertFriendIntegrationUser(t, ctx, db, fmt.Sprintf("friend_it_b_%d", stamp), "Bob")
	defer func() {
		cleanupCtx := context.Background()
		_, _ = db.ExecContext(cleanupCtx,
			`DELETE FROM blacklist WHERE user_id IN (?, ?) OR blocked_id IN (?, ?)`, userA, userB, userA, userB)
		_, _ = db.ExecContext(cleanupCtx,
			`DELETE FROM friendships WHERE user_id IN (?, ?) OR friend_id IN (?, ?)`, userA, userB, userA, userB)
		_, _ = db.ExecContext(cleanupCtx,
			`DELETE FROM friend_requests WHERE from_user_id IN (?, ?) OR to_user_id IN (?, ?)`, userA, userB, userA, userB)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM cache_reconcile_events
			WHERE resource_type IN ('friends', 'blacklist') AND resource_id IN (?, ?)`, userA, userB)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM users WHERE id IN (?, ?)`, userA, userB)
		_ = redisClient.Del(cleanupCtx,
			fmt.Sprintf("friend:%d:%d", userA, userB), fmt.Sprintf("friend:%d:%d", userB, userA),
			fmt.Sprintf("friend_owner_index:%d", userA), fmt.Sprintf("friend_owner_index:%d", userB),
			fmt.Sprintf("friend_owner_index_loaded:%d", userA), fmt.Sprintf("friend_owner_index_loaded:%d", userB),
			fmt.Sprintf("blacklist:%d", userA), fmt.Sprintf("blacklist:%d", userB),
			fmt.Sprintf("friend_loaded:%d", userA), fmt.Sprintf("friend_loaded:%d", userB),
			fmt.Sprintf("blacklist_loaded:%d", userA), fmt.Sprintf("blacklist_loaded:%d", userB),
			fmt.Sprintf("cache_warm_lock:friends:%d", userA), fmt.Sprintf("cache_warm_lock:friends:%d", userB),
			fmt.Sprintf("cache_warm_lock:blacklist:%d", userA), fmt.Sprintf("cache_warm_lock:blacklist:%d", userB),
		).Err()
	}()

	mysqlRepo := repository.NewMySQLRepo(db)
	redisRepo := repository.NewRedisRepo(redisClient)
	cacheTruth := NewCacheTruthService(mysqlRepo, redisRepo, CacheTruthOptions{})
	friends, err := NewFriendService(mysqlRepo, WithFriendCache(cacheTruth), WithPresenceReader(redisRepo))
	require.NoError(t, err)

	// Start opposite requests together. Deterministic user-row locking must
	// serialize them, regardless of which goroutine reaches MySQL first.
	type sendResult struct {
		request *model.FriendRequest
		err     error
	}
	start := make(chan struct{})
	results := make(chan sendResult, 2)
	for _, direction := range [][2]int64{{userA, userB}, {userB, userA}} {
		fromID, toID := direction[0], direction[1]
		go func() {
			<-start
			request, err := friends.SendRequest(ctx, fromID, toID, " hello ")
			results <- sendResult{request: request, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	all := []sendResult{first, second}
	var acceptedRequest *model.FriendRequest
	duplicateCount := 0
	for _, result := range all {
		if result.err == nil {
			acceptedRequest = result.request
			continue
		}
		var appErr *apperror.Error
		require.True(t, errors.As(result.err, &appErr))
		require.Equal(t, apperror.CodeDuplicateRequest, appErr.Code)
		duplicateCount++
	}
	require.NotNil(t, acceptedRequest)
	require.Equal(t, 1, duplicateCount)
	require.Equal(t, "hello", acceptedRequest.Message)

	var pending int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM friend_requests WHERE status = 0 AND ((from_user_id=? AND to_user_id=?) OR (from_user_id=? AND to_user_id=?))`,
		userA, userB, userB, userA).Scan(&pending))
	require.Equal(t, 1, pending)

	result, err := friends.AcceptRequest(ctx, acceptedRequest.ToUserID, acceptedRequest.ID)
	require.NoError(t, err)
	require.Equal(t, acceptedRequest.FromUserID, result.FriendID)
	_, err = friends.AcceptRequest(ctx, acceptedRequest.ToUserID, acceptedRequest.ID)
	require.NoError(t, err, "accepted retry must be idempotent")

	var friendshipRows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM friendships WHERE (user_id=? AND friend_id=?) OR (user_id=? AND friend_id=?)`,
		userA, userB, userB, userA).Scan(&friendshipRows))
	require.Equal(t, 2, friendshipRows)
	require.Equal(t, int64(2), redisClient.Exists(ctx,
		fmt.Sprintf("friend:%d:%d", userA, userB), fmt.Sprintf("friend:%d:%d", userB, userA)).Val())

	page, err := friends.ListFriends(ctx, acceptedRequest.FromUserID, 20, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Len(t, page.Items, 1)
	require.NotEmpty(t, page.Items[0].Nickname)

	require.NoError(t, friends.Block(ctx, userA, userB))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM friendships WHERE (user_id=? AND friend_id=?) OR (user_id=? AND friend_id=?)`,
		userA, userB, userB, userA).Scan(&friendshipRows))
	require.Equal(t, 2, friendshipRows, "blocking policy keeps the friendship")
	require.True(t, redisClient.SIsMember(ctx, fmt.Sprintf("blacklist:%d", userA), userB).Val())
	require.NoError(t, friends.Unblock(ctx, userA, userB))

	require.NoError(t, friends.DeleteFriend(ctx, userA, userB))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM friendships WHERE (user_id=? AND friend_id=?) OR (user_id=? AND friend_id=?)`,
		userA, userB, userB, userA).Scan(&friendshipRows))
	require.Zero(t, friendshipRows)
	require.Zero(t, redisClient.Exists(ctx,
		fmt.Sprintf("friend:%d:%d", userA, userB), fmt.Sprintf("friend:%d:%d", userB, userA)).Val())
}

func insertFriendIntegrationUser(t *testing.T, ctx context.Context, db *sql.DB, username, nickname string) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx,
		`INSERT INTO users(username,password_hash,nickname) VALUES(?,?,?)`, username, "hash", nickname)
	require.NoError(t, err)
	userID, err := result.LastInsertId()
	require.NoError(t, err)
	return userID
}
