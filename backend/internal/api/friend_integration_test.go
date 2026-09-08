package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	authtoken "my-im/internal/auth"
	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/migrate"
	"my-im/internal/model"
	redisscripts "my-im/internal/redis"
	"my-im/internal/repository"
	"my-im/internal/service"
)

// Run with the project Docker MySQL and Redis:
//
//	MYIM_INTEGRATION=1 go test ./internal/api -run TestFriendHTTPIntegration -v
//
// The test never flushes Redis or truncates shared tables. Every durable row and
// Redis key it removes belongs to the unique users created below.
func TestFriendHTTPIntegration(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker MySQL and Redis running")
	}

	ctx := context.Background()
	db, err := infra.OpenMySQL(ctx, config.MySQLConfig{
		Host: "127.0.0.1", Port: 13306, User: "my_im", Password: "my_im123", DBName: "my_im",
		ConnectTimeoutMS: 3000, QueryTimeoutMS: 3000, MaxOpenConns: 10, MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate.New(db, "../../scripts/migrations").Up(ctx); err != nil {
		t.Fatal(err)
	}

	redisClient, err := infra.OpenRedis(ctx, config.RedisConfig{
		Addr: "127.0.0.1:16379", DialTimeoutMS: 3000, ReadTimeoutMS: 2000,
		WriteTimeoutMS: 2000, PoolSize: 10, HealthTimeoutMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer redisClient.Close()

	mysqlRepo := repository.NewMySQLRepository(db, 3*time.Second, nil)
	redisRepo := repository.NewRedisRepo(redisClient, repository.WithMessageIDGenerator(&apiTestMessageIDs{}))
	tokens, err := authtoken.NewManager(
		"0123456789abcdef0123456789abcdef", "my-im-friend-integration", time.Hour, 24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	authService, err := service.NewAuthService(mysqlRepo, redisRepo, tokens, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	cacheTruth := service.NewCacheTruthService(mysqlRepo, redisRepo, service.CacheTruthOptions{})
	friendService, err := service.NewFriendService(
		mysqlRepo,
		service.WithFriendCache(cacheTruth),
		service.WithPresenceReader(redisRepo),
	)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(RouterOptions{
		ServiceName: "my-im-friend-integration", Auth: authService,
		TokenVerifier: tokens, Friend: friendService,
	})

	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	var userIDs []int64
	var dedupKeys []string
	defer func() {
		friendIntegrationCleanup(db, redisClient, redisRepo, userIDs, dedupKeys)
	}()

	alice := registerFriendIntegrationUser(t, router, "fit_a_"+stamp)
	userIDs = append(userIDs, alice.ID)
	bob := registerFriendIntegrationUser(t, router, "fit_b_"+stamp)
	userIDs = append(userIDs, bob.ID)
	carol := registerFriendIntegrationUser(t, router, "fit_c_"+stamp)
	userIDs = append(userIDs, carol.ID)
	alice.AvatarURL = "/uploads/alice-integration.png"
	bob.AvatarURL = "/uploads/bob-integration.png"
	carol.AvatarURL = "/uploads/carol-integration.png"

	profiles := []struct {
		id       int64
		nickname string
		avatar   string
	}{
		{alice.ID, "Alice Integration", alice.AvatarURL},
		{bob.ID, "Bob Integration", bob.AvatarURL},
		{carol.ID, "Carol Integration", carol.AvatarURL},
	}
	for _, profile := range profiles {
		if _, err := db.ExecContext(ctx,
			`UPDATE users SET nickname = ?, avatar_url = ? WHERE id = ?`,
			profile.nickname, profile.avatar, profile.id,
		); err != nil {
			t.Fatal(err)
		}
	}

	// Two senders make the target's request list genuinely paginated. The
	// response must contain the sender profile joined from users, not just IDs.
	carolRequest := sendFriendIntegrationRequest(t, router, carol, bob.ID, "hello from carol")
	if carolRequest.RequestID <= 0 {
		t.Fatalf("carol request has invalid ID: %+v", carolRequest)
	}
	aliceRequest := sendFriendIntegrationRequest(t, router, alice, bob.ID, "hello from alice")

	status, body := integrationJSONRequest(t, router, http.MethodGet,
		"/api/v1/friend/requests?limit=1&offset=0", "", bob.AccessToken)
	var firstPage integrationEnvelope[PageResponse[model.FriendRequest]]
	if status != http.StatusOK || json.Unmarshal(body, &firstPage) != nil || firstPage.Code != 0 {
		t.Fatalf("list first request page status=%d body=%s", status, body)
	}
	if len(firstPage.Data.Items) != 1 || firstPage.Data.Pagination.Total != 2 ||
		firstPage.Data.Pagination.Offset != 0 || firstPage.Data.Pagination.Limit != 1 ||
		!firstPage.Data.Pagination.HasMore {
		t.Fatalf("unexpected first request page: %+v", firstPage.Data)
	}
	status, body = integrationJSONRequest(t, router, http.MethodGet,
		"/api/v1/friend/requests?limit=1&offset=1", "", bob.AccessToken)
	var secondPage integrationEnvelope[PageResponse[model.FriendRequest]]
	if status != http.StatusOK || json.Unmarshal(body, &secondPage) != nil || secondPage.Code != 0 ||
		len(secondPage.Data.Items) != 1 || secondPage.Data.Pagination.Total != 2 ||
		secondPage.Data.Pagination.Offset != 1 || secondPage.Data.Pagination.Limit != 1 ||
		secondPage.Data.Pagination.HasMore {
		t.Fatalf("unexpected second request page status=%d body=%s", status, body)
	}

	status, body = integrationJSONRequest(t, router, http.MethodGet,
		"/api/v1/friend/requests?limit=2&offset=0", "", bob.AccessToken)
	var allRequests integrationEnvelope[PageResponse[model.FriendRequest]]
	if status != http.StatusOK || json.Unmarshal(body, &allRequests) != nil || allRequests.Code != 0 {
		t.Fatalf("list all requests status=%d body=%s", status, body)
	}
	if len(allRequests.Data.Items) != 2 || allRequests.Data.Pagination.HasMore {
		t.Fatalf("unexpected complete request page: %+v", allRequests.Data)
	}
	assertFriendRequestProfiles(t, allRequests.Data.Items, map[int64]friendIntegrationUser{
		alice.ID: alice,
		carol.ID: carol,
	})

	// Two simultaneous accepts exercise SELECT ... FOR UPDATE and idempotency.
	// Both calls must succeed, while the table still contains exactly two
	// directed rows (A->B and B->A), never four.
	type acceptHTTPResult struct {
		status int
		body   []byte
	}
	acceptResults := make(chan acceptHTTPResult, 2)
	acceptBody := fmt.Sprintf(`{"request_id":%d}`, aliceRequest.RequestID)
	for range 2 {
		go func() {
			status, body := integrationJSONRequest(t, router, http.MethodPost,
				"/api/v1/friend/accept", acceptBody, bob.AccessToken)
			acceptResults <- acceptHTTPResult{status: status, body: body}
		}()
	}
	for range 2 {
		result := <-acceptResults
		var accepted integrationEnvelope[service.AcceptFriendResult]
		if result.status != http.StatusOK || json.Unmarshal(result.body, &accepted) != nil ||
			accepted.Code != 0 || accepted.Data.UserID != bob.ID || accepted.Data.FriendID != alice.ID {
			t.Fatalf("concurrent accept status=%d body=%s", result.status, result.body)
		}
	}
	status, body = integrationJSONRequest(t, router, http.MethodPost,
		"/api/v1/friend/accept", acceptBody, bob.AccessToken)
	if status != http.StatusOK {
		t.Fatalf("idempotent accept retry status=%d body=%s", status, body)
	}
	assertFriendPairRowCount(t, db, alice.ID, bob.ID, 2)
	var requestStatus int
	if err := db.QueryRowContext(ctx,
		`SELECT status FROM friend_requests WHERE id = ?`, aliceRequest.RequestID,
	).Scan(&requestStatus); err != nil || requestStatus != model.FriendRequestAccepted {
		t.Fatalf("accepted request status=%d err=%v", requestStatus, err)
	}

	assertFriendIntegrationList(t, router, alice, bob.ID, "Bob Integration", "/uploads/bob-integration.png", false)
	assertFriendIntegrationList(t, router, bob, alice.ID, "Alice Integration", "/uploads/alice-integration.png", false)

	// Product policy: block is a communication barrier, not an implicit
	// friendship deletion. The list exposes the caller's is_blocked state.
	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/friend/block",
		fmt.Sprintf(`{"blocked_id":%d}`, bob.ID), alice.AccessToken)
	if status != http.StatusOK {
		t.Fatalf("block status=%d body=%s", status, body)
	}
	assertFriendPairRowCount(t, db, alice.ID, bob.ID, 2)
	assertFriendIntegrationList(t, router, alice, bob.ID, "Bob Integration", "/uploads/bob-integration.png", true)

	if err := cacheTruth.EnsurePrivateAccess(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	assertPrivateMessageBlocked(t, redisRepo, alice.ID, bob.ID, "fit-block-a-"+stamp)
	assertPrivateMessageBlocked(t, redisRepo, bob.ID, alice.ID, "fit-block-b-"+stamp)

	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/friend/unblock",
		fmt.Sprintf(`{"blocked_id":%d}`, bob.ID), alice.AccessToken)
	if status != http.StatusOK {
		t.Fatalf("unblock status=%d body=%s", status, body)
	}
	var blacklistRows int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blacklist WHERE user_id = ? AND blocked_id = ?`, alice.ID, bob.ID,
	).Scan(&blacklistRows); err != nil || blacklistRows != 0 {
		t.Fatalf("blacklist rows=%d err=%v after unblock", blacklistRows, err)
	}
	if err := cacheTruth.EnsurePrivateAccess(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	unblockedClientID := "fit-unblocked-" + stamp
	if _, err := redisRepo.ExecPrivateMsgCheck(ctx, alice.ID, bob.ID, unblockedClientID); err != nil {
		t.Fatalf("private message after unblock: %v", err)
	}
	dedupKeys = append(dedupKeys, fmt.Sprintf("msg_dedup:%d:%s", alice.ID, unblockedClientID))

	// Simulate loss of every relationship projection owned by these two users.
	// EnsurePrivateAccess must rebuild from MySQL before the Lua authorization
	// check runs, including valid empty blacklist collections.
	cacheKeys := friendIntegrationRelationshipKeys(alice.ID, bob.ID)
	if err := redisClient.Del(ctx, cacheKeys...).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cacheTruth.EnsurePrivateAccess(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	aliceFriends, err := redisRepo.ReadFriendSnapshot(ctx, alice.ID)
	if err != nil || !aliceFriends.Loaded || !equalFriendIntegrationIDs(aliceFriends.IDs, []int64{bob.ID}) {
		t.Fatalf("alice friend cache was not rebuilt: snapshot=%+v err=%v", aliceFriends, err)
	}
	bobFriends, err := redisRepo.ReadFriendSnapshot(ctx, bob.ID)
	if err != nil || !bobFriends.Loaded || !equalFriendIntegrationIDs(bobFriends.IDs, []int64{alice.ID}) {
		t.Fatalf("bob friend cache was not rebuilt: snapshot=%+v err=%v", bobFriends, err)
	}
	aliceBlacklist, err := redisRepo.ReadBlacklistSnapshot(ctx, alice.ID)
	if err != nil || !aliceBlacklist.Loaded || len(aliceBlacklist.IDs) != 0 {
		t.Fatalf("empty blacklist truth was not cached: snapshot=%+v err=%v", aliceBlacklist, err)
	}
	recoveredClientID := "fit-cache-recovered-" + stamp
	if _, err := redisRepo.ExecPrivateMsgCheck(ctx, alice.ID, bob.ID, recoveredClientID); err != nil {
		t.Fatalf("private Lua after Redis loss and MySQL reload: %v", err)
	}
	dedupKeys = append(dedupKeys, fmt.Sprintf("msg_dedup:%d:%s", alice.ID, recoveredClientID))

	// The maintenance audit must see a deliberately corrupted projection, and
	// ReconcileNow must repair only that owner's cache from current MySQL truth.
	if err := redisClient.Del(ctx, fmt.Sprintf("friend:%d:%d", alice.ID, bob.ID)).Err(); err != nil {
		t.Fatal(err)
	}
	audit, err := cacheTruth.Audit(ctx, service.CacheScopeFriends)
	if err != nil {
		t.Fatal(err)
	}
	if !friendIntegrationAuditHasIssue(audit, repository.CacheResourceFriends, alice.ID) {
		t.Fatalf("cache audit missed corrupted friend projection for user %d: %+v", alice.ID, audit)
	}
	if err := cacheTruth.ReconcileNow(ctx, repository.CacheResourceFriends, alice.ID); err != nil {
		t.Fatal(err)
	}
	audit, err = cacheTruth.Audit(ctx, service.CacheScopeFriends)
	if err != nil {
		t.Fatal(err)
	}
	if friendIntegrationAuditHasIssue(audit, repository.CacheResourceFriends, alice.ID) {
		t.Fatalf("cache reconcile did not repair user %d: %+v", alice.ID, audit)
	}

	status, body = integrationJSONRequest(t, router, http.MethodDelete,
		fmt.Sprintf("/api/v1/friend/%d", bob.ID), "", alice.AccessToken)
	if status != http.StatusOK {
		t.Fatalf("delete friend status=%d body=%s", status, body)
	}
	assertFriendPairRowCount(t, db, alice.ID, bob.ID, 0)

	// A delayed retry of the old accepted request must not recreate a
	// friendship that the user has explicitly deleted.
	status, body = integrationJSONRequest(t, router, http.MethodPost,
		"/api/v1/friend/accept", acceptBody, bob.AccessToken)
	var staleAccept integrationEnvelope[service.AcceptFriendResult]
	if status != http.StatusNotFound || json.Unmarshal(body, &staleAccept) != nil ||
		staleAccept.Code != 1205 {
		t.Fatalf("stale accept after delete status=%d body=%s", status, body)
	}
	assertFriendPairRowCount(t, db, alice.ID, bob.ID, 0)

	if err := cacheTruth.EnsurePrivateAccess(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	_, err = redisRepo.ExecPrivateMsgCheck(ctx, alice.ID, bob.ID, "fit-after-delete-"+stamp)
	var ruleErr *repository.LuaRuleError
	if !errors.As(err, &ruleErr) || ruleErr.Code != redisscripts.CodePMNotFriend {
		t.Fatalf("private message after delete err=%v, want code=%d", err, redisscripts.CodePMNotFriend)
	}
}

type friendIntegrationUser struct {
	ID          int64
	Username    string
	AccessToken string
	AvatarURL   string
}

func registerFriendIntegrationUser(t *testing.T, handler http.Handler, username string) friendIntegrationUser {
	t.Helper()
	status, body := integrationJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/register",
		fmt.Sprintf(`{"username":%q,"password":"secret123"}`, username), "")
	var registered integrationEnvelope[RegisterResponse]
	if status != http.StatusCreated || json.Unmarshal(body, &registered) != nil ||
		registered.Code != 0 || registered.Data.UserID <= 0 {
		t.Fatalf("register %q status=%d body=%s", username, status, body)
	}
	status, body = integrationJSONRequest(t, handler, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"username":%q,"password":"secret123"}`, username), "")
	var loggedIn integrationEnvelope[LoginResponse]
	if status != http.StatusOK || json.Unmarshal(body, &loggedIn) != nil || loggedIn.Data.AccessToken == "" {
		t.Fatalf("login %q status=%d body=%s", username, status, body)
	}
	return friendIntegrationUser{ID: registered.Data.UserID, Username: username, AccessToken: loggedIn.Data.AccessToken}
}

func sendFriendIntegrationRequest(
	t *testing.T,
	handler http.Handler,
	sender friendIntegrationUser,
	targetID int64,
	message string,
) SendFriendResponse {
	t.Helper()
	status, body := integrationJSONRequest(t, handler, http.MethodPost, "/api/v1/friend/request",
		fmt.Sprintf(`{"to_user_id":%d,"message":%q}`, targetID, message), sender.AccessToken)
	var response integrationEnvelope[SendFriendResponse]
	if status != http.StatusCreated || json.Unmarshal(body, &response) != nil || response.Code != 0 ||
		response.Data.FromUserID != sender.ID || response.Data.ToUserID != targetID {
		t.Fatalf("send friend request status=%d body=%s", status, body)
	}
	return response.Data
}

func assertFriendRequestProfiles(
	t *testing.T,
	requests []model.FriendRequest,
	users map[int64]friendIntegrationUser,
) {
	t.Helper()
	seen := make(map[int64]bool, len(requests))
	for _, request := range requests {
		user, ok := users[request.FromUserID]
		if !ok {
			t.Fatalf("unexpected request sender: %+v", request)
		}
		if request.Username != user.Username || request.AvatarURL != user.AvatarURL || request.Status != model.FriendRequestPending {
			t.Fatalf("request does not include sender profile/status: %+v", request)
		}
		seen[request.FromUserID] = true
	}
	if len(seen) != len(users) {
		t.Fatalf("request senders=%v, want=%v", seen, users)
	}
}

func assertFriendIntegrationList(
	t *testing.T,
	handler http.Handler,
	viewer friendIntegrationUser,
	wantFriendID int64,
	wantNickname string,
	wantAvatar string,
	wantBlocked bool,
) {
	t.Helper()
	status, body := integrationJSONRequest(t, handler, http.MethodGet,
		"/api/v1/friend/list?limit=10&offset=0", "", viewer.AccessToken)
	var response integrationEnvelope[PageResponse[model.Friendship]]
	if status != http.StatusOK || json.Unmarshal(body, &response) != nil || response.Code != 0 {
		t.Fatalf("list friends status=%d body=%s", status, body)
	}
	if response.Data.Pagination.Total != 1 || len(response.Data.Items) != 1 {
		t.Fatalf("unexpected friend page: %+v", response.Data)
	}
	friend := response.Data.Items[0]
	if friend.UserID != viewer.ID || friend.FriendID != wantFriendID || friend.Nickname != wantNickname ||
		friend.AvatarURL != wantAvatar || friend.IsBlocked != wantBlocked {
		t.Fatalf("unexpected friend view: %+v", friend)
	}
}

func assertPrivateMessageBlocked(
	t *testing.T,
	redisRepo *repository.RedisRepoImpl,
	senderID int64,
	receiverID int64,
	clientMsgID string,
) {
	t.Helper()
	result, err := redisRepo.ExecPrivateMsgCheck(context.Background(), senderID, receiverID, clientMsgID)
	var ruleErr *repository.LuaRuleError
	if result != nil || !errors.As(err, &ruleErr) || ruleErr.Code != redisscripts.CodePMBlocked {
		t.Fatalf("private block %d->%d result=%+v err=%v, want code=%d",
			senderID, receiverID, result, err, redisscripts.CodePMBlocked)
	}
}

func assertFriendPairRowCount(t *testing.T, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, firstID, secondID int64, want int) {
	t.Helper()
	var count int
	err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM friendships
		WHERE (user_id = ? AND friend_id = ?) OR (user_id = ? AND friend_id = ?)`,
		firstID, secondID, secondID, firstID).Scan(&count)
	if err != nil || count != want {
		t.Fatalf("friendship row count=%d err=%v, want=%d", count, err, want)
	}
}

func friendIntegrationRelationshipKeys(firstID, secondID int64) []string {
	return []string{
		fmt.Sprintf("friend:%d:%d", firstID, secondID),
		fmt.Sprintf("friend:%d:%d", secondID, firstID),
		fmt.Sprintf("friend_loaded:%d", firstID),
		fmt.Sprintf("friend_loaded:%d", secondID),
		fmt.Sprintf("friend_owner_index:%d", firstID),
		fmt.Sprintf("friend_owner_index:%d", secondID),
		fmt.Sprintf("friend_owner_index_loaded:%d", firstID),
		fmt.Sprintf("friend_owner_index_loaded:%d", secondID),
		fmt.Sprintf("blacklist:%d", firstID),
		fmt.Sprintf("blacklist:%d", secondID),
		fmt.Sprintf("blacklist_loaded:%d", firstID),
		fmt.Sprintf("blacklist_loaded:%d", secondID),
		fmt.Sprintf("cache_warm_lock:friends:%d", firstID),
		fmt.Sprintf("cache_warm_lock:friends:%d", secondID),
		fmt.Sprintf("cache_warm_lock:blacklist:%d", firstID),
		fmt.Sprintf("cache_warm_lock:blacklist:%d", secondID),
	}
}

func friendIntegrationAuditHasIssue(
	report service.CacheAuditReport,
	resource repository.CacheResource,
	ownerID int64,
) bool {
	for _, issue := range report.Issues {
		if issue.Resource == resource && issue.OwnerID == ownerID {
			return true
		}
	}
	return false
}

func equalFriendIntegrationIDs(left, right []int64) bool {
	left = append([]int64(nil), left...)
	right = append([]int64(nil), right...)
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func friendIntegrationCleanup(
	db *sql.DB,
	redisClient *goredis.Client,
	redisRepo *repository.RedisRepoImpl,
	userIDs []int64,
	dedupKeys []string,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keys := append([]string(nil), dedupKeys...)
	for _, ownerID := range userIDs {
		keys = append(keys,
			fmt.Sprintf("friend_loaded:%d", ownerID),
			fmt.Sprintf("friend_owner_index:%d", ownerID),
			fmt.Sprintf("friend_owner_index_loaded:%d", ownerID),
			fmt.Sprintf("blacklist:%d", ownerID),
			fmt.Sprintf("blacklist_loaded:%d", ownerID),
			fmt.Sprintf("cache_warm_lock:friends:%d", ownerID),
			fmt.Sprintf("cache_warm_lock:blacklist:%d", ownerID),
			fmt.Sprintf("conn:%d", ownerID),
			fmt.Sprintf("online:%d", ownerID),
		)
		_ = redisRepo.RevokeUserRefreshSessions(ctx, ownerID)
	}
	for _, ownerID := range userIDs {
		for _, friendID := range userIDs {
			if ownerID != friendID {
				keys = append(keys, fmt.Sprintf("friend:%d:%d", ownerID, friendID))
			}
		}
	}
	if len(keys) > 0 {
		_ = redisClient.Del(ctx, keys...).Err()
	}

	for _, userID := range userIDs {
		for _, resourceType := range []repository.CacheResource{
			repository.CacheResourceFriends,
			repository.CacheResourceBlacklist,
		} {
			_, _ = db.ExecContext(ctx, `DELETE FROM cache_reconcile_events
				WHERE resource_type = ? AND resource_id = ?`, resourceType, userID)
		}
		_, _ = db.ExecContext(ctx, `DELETE FROM blacklist WHERE user_id = ? OR blocked_id = ?`, userID, userID)
		_, _ = db.ExecContext(ctx, `DELETE FROM friendships WHERE user_id = ? OR friend_id = ?`, userID, userID)
		_, _ = db.ExecContext(ctx, `DELETE FROM friend_requests WHERE from_user_id = ? OR to_user_id = ?`, userID, userID)
	}
	for index := len(userIDs) - 1; index >= 0; index-- {
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userIDs[index])
	}
}
