package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"my-im/internal/apperror"
	"my-im/internal/model"
	"my-im/internal/protocol"
	"my-im/internal/repository"
)

func TestFriendServiceSendRequestRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fromID  int64
		toID    int64
		message string
		arrange func(*fakeFriendRepository)
		code    apperror.Code
	}{
		{name: "cannot add self", fromID: 1, toID: 1, code: apperror.CodeSelfRequest},
		{name: "target must exist", fromID: 1, toID: 3, code: apperror.CodeUserNotFound},
		{name: "outgoing block prevents request", fromID: 1, toID: 2, arrange: func(repo *fakeFriendRepository) { repo.blocked[pair{1, 2}] = true }, code: apperror.CodeFriendBlocked},
		{name: "incoming block prevents request", fromID: 1, toID: 2, arrange: func(repo *fakeFriendRepository) { repo.blocked[pair{2, 1}] = true }, code: apperror.CodeFriendBlocked},
		{name: "existing one-sided row still means friends", fromID: 1, toID: 2, arrange: func(repo *fakeFriendRepository) { repo.friends[pair{2, 1}] = true }, code: apperror.CodeAlreadyFriends},
		{name: "same direction pending is duplicate", fromID: 1, toID: 2, arrange: func(repo *fakeFriendRepository) { repo.addRequest(1, 2, model.FriendRequestPending) }, code: apperror.CodeDuplicateRequest},
		{name: "reverse pending is duplicate", fromID: 1, toID: 2, arrange: func(repo *fakeFriendRepository) { repo.addRequest(2, 1, model.FriendRequestPending) }, code: apperror.CodeDuplicateRequest},
		{name: "control characters are rejected", fromID: 1, toID: 2, message: "hello\n", code: apperror.CodeInvalidParam},
		{name: "more than 200 unicode characters are rejected", fromID: 1, toID: 2, message: strings.Repeat("好", 201), code: apperror.CodeInvalidParam},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeFriendRepository(1, 2)
			if test.arrange != nil {
				test.arrange(repo)
			}
			service := mustFriendService(t, repo)
			_, err := service.SendRequest(context.Background(), test.fromID, test.toID, test.message)
			requireAppCode(t, err, test.code)
		})
	}
}

func TestFriendServiceSendRequestResetsTerminalAndTrimsMessage(t *testing.T) {
	t.Parallel()
	repo := newFakeFriendRepository(1, 2)
	old := repo.addRequest(1, 2, model.FriendRequestRejected)
	notifier := &fakeFriendNotifier{}
	service := mustFriendService(t, repo, WithFriendEventNotifier(notifier))
	fixedNow := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }

	request, err := service.SendRequest(context.Background(), 1, 2, "  你好  ")
	require.NoError(t, err)
	assert.NotEqual(t, old.ID, request.ID, "a new application generation needs a new request ID")
	assert.Nil(t, repo.requests[old.ID])
	assert.Equal(t, "你好", request.Message)
	assert.Equal(t, model.FriendRequestPending, repo.requests[request.ID].Status)
	assert.Equal(t, fixedNow, repo.requests[request.ID].CreatedAt)
	require.Len(t, notifier.events, 1)
	assert.Equal(t, FriendEventApply, notifier.events[0].eventType)
	assert.Equal(t, int64(2), notifier.events[0].userID)
	assert.IsType(t, protocol.FriendApplyPayload{}, notifier.events[0].payload)
	assert.False(t, repo.mutationOutsideTransaction)
}

func TestFriendServiceConcurrentCrossRequestsLeaveAtMostOnePending(t *testing.T) {
	t.Parallel()
	repo := newFakeFriendRepository(1, 2)
	service := mustFriendService(t, repo)
	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for _, direction := range [][2]int64{{1, 2}, {2, 1}} {
		fromID, toID := direction[0], direction[1]
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := service.SendRequest(context.Background(), fromID, toID, "hi")
			errorsFound <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)

	successes, duplicates := 0, 0
	for err := range errorsFound {
		if err == nil {
			successes++
			continue
		}
		var appErr *apperror.Error
		require.ErrorAs(t, err, &appErr)
		if appErr.Code == apperror.CodeDuplicateRequest {
			duplicates++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, duplicates)
	assert.Equal(t, 1, repo.pendingCount())
}

func TestFriendServiceAcceptIsTransactionalAndIdempotent(t *testing.T) {
	t.Parallel()
	repo := newFakeFriendRepository(1, 2)
	request := repo.addRequest(1, 2, model.FriendRequestPending)
	cache := &fakeFriendCache{}
	notifier := &fakeFriendNotifier{}
	service := mustFriendService(t, repo, WithFriendCache(cache), WithFriendEventNotifier(notifier))

	result, err := service.AcceptRequest(context.Background(), 2, request.ID)
	require.NoError(t, err)
	assert.Equal(t, AcceptFriendResult{UserID: 2, FriendID: 1}, result)
	assert.Equal(t, model.FriendRequestAccepted, repo.requests[request.ID].Status)
	assert.True(t, repo.friends[pair{1, 2}])
	assert.True(t, repo.friends[pair{2, 1}])
	assert.False(t, repo.mutationOutsideTransaction)
	assert.Equal(t, 1, cache.setFriendCalls)
	require.Len(t, notifier.events, 1)
	assert.Equal(t, FriendEventAccepted, notifier.events[0].eventType)
	assert.Equal(t, int64(1), notifier.events[0].userID)
	payload, ok := notifier.events[0].payload.(protocol.FriendAcceptedPayload)
	require.True(t, ok)
	assert.Equal(t, request.ID, payload.RequestID)
	assert.Equal(t, int64(1), payload.UserID)
	assert.Equal(t, int64(2), payload.FriendID)

	// Simulate old/partial data, then repeat the already-accepted command.
	delete(repo.friends, pair{2, 1})
	result, err = service.AcceptRequest(context.Background(), 2, request.ID)
	require.NoError(t, err)
	assert.Equal(t, AcceptFriendResult{UserID: 2, FriendID: 1}, result)
	assert.True(t, repo.friends[pair{2, 1}], "retry repairs the missing reverse row")
	assert.Equal(t, 2, cache.setFriendCalls, "retry also repairs the projection")
	assert.Len(t, notifier.events, 1, "retry must not emit a duplicate business event")
}

func TestFriendServiceAcceptAndRejectTerminalRules(t *testing.T) {
	t.Parallel()

	t.Run("only target may act", func(t *testing.T) {
		repo := newFakeFriendRepository(1, 2, 3)
		request := repo.addRequest(1, 2, model.FriendRequestPending)
		service := mustFriendService(t, repo)
		_, err := service.AcceptRequest(context.Background(), 3, request.ID)
		requireAppCode(t, err, apperror.CodeNotRequestTarget)
		requireAppCode(t, service.RejectRequest(context.Background(), 3, request.ID), apperror.CodeNotRequestTarget)
	})

	t.Run("rejected cannot later be accepted", func(t *testing.T) {
		repo := newFakeFriendRepository(1, 2)
		request := repo.addRequest(1, 2, model.FriendRequestPending)
		service := mustFriendService(t, repo)
		require.NoError(t, service.RejectRequest(context.Background(), 2, request.ID))
		require.NoError(t, service.RejectRequest(context.Background(), 2, request.ID), "reject retry is idempotent")
		_, err := service.AcceptRequest(context.Background(), 2, request.ID)
		requireAppCode(t, err, apperror.CodeRequestNotFound)
	})

	t.Run("accepted cannot later be rejected", func(t *testing.T) {
		repo := newFakeFriendRepository(1, 2)
		request := repo.addRequest(1, 2, model.FriendRequestAccepted)
		service := mustFriendService(t, repo)
		requireAppCode(t, service.RejectRequest(context.Background(), 2, request.ID), apperror.CodeRequestNotFound)
	})

	t.Run("block added after application prevents acceptance", func(t *testing.T) {
		repo := newFakeFriendRepository(1, 2)
		request := repo.addRequest(1, 2, model.FriendRequestPending)
		repo.blocked[pair{2, 1}] = true
		service := mustFriendService(t, repo)
		_, err := service.AcceptRequest(context.Background(), 2, request.ID)
		requireAppCode(t, err, apperror.CodeFriendBlocked)
		assert.Equal(t, model.FriendRequestPending, repo.requests[request.ID].Status)
	})
}

func TestFriendServiceListsPaginationProfileAndPresence(t *testing.T) {
	t.Parallel()
	repo := newFakeFriendRepository(1, 2, 3)
	repo.requestsPage = []model.FriendRequest{{ID: 9, FromUserID: 2, ToUserID: 1, Username: "alice", AvatarURL: "/a.png"}}
	repo.requestTotal = 7
	repo.friendsPage = []model.Friendship{
		{ID: 1, UserID: 1, FriendID: 2, Nickname: "Alice", AvatarURL: "/a.png"},
		{ID: 2, UserID: 1, FriendID: 3, Nickname: "Bob", IsBlocked: true},
	}
	repo.friendTotal = 12
	presence := &fakePresence{states: map[int64]bool{2: true, 3: false}}
	service := mustFriendService(t, repo, WithPresenceReader(presence))

	requests, err := service.ListRequests(context.Background(), 1, 100, 4)
	require.NoError(t, err)
	assert.Equal(t, 100, requests.Limit)
	assert.Equal(t, 4, requests.Offset)
	assert.Equal(t, int64(7), requests.Total)
	assert.Equal(t, "alice", requests.Items[0].Username)
	assert.Equal(t, [2]int{100, 4}, repo.lastRequestPage)

	friends, err := service.ListFriends(context.Background(), 1, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 20, friends.Limit)
	assert.Equal(t, int64(12), friends.Total)
	assert.True(t, friends.Items[0].Online)
	assert.False(t, friends.Items[1].Online)
	assert.True(t, friends.Items[1].IsBlocked)
	assert.ElementsMatch(t, []int64{2, 3}, presence.lastIDs)

	_, err = service.ListRequests(context.Background(), 1, 101, 0)
	requireAppCode(t, err, apperror.CodeInvalidParam)
	_, err = service.ListFriends(context.Background(), 1, 20, -1)
	requireAppCode(t, err, apperror.CodeInvalidParam)
}

func TestFriendServicePresenceFailureDoesNotHideMySQLFriends(t *testing.T) {
	t.Parallel()
	repo := newFakeFriendRepository(1, 2)
	repo.friendsPage = []model.Friendship{{ID: 1, UserID: 1, FriendID: 2, Nickname: "Alice"}}
	repo.friendTotal = 1
	service := mustFriendService(t, repo, WithPresenceReader(&fakePresence{err: errors.New("redis unavailable")}))

	page, err := service.ListFriends(context.Background(), 1, 20, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.False(t, page.Items[0].Online)
	assert.Equal(t, int64(1), page.Total)
}

func TestFriendServicePostCommitCacheFailureHasDurableReconcile(t *testing.T) {
	t.Parallel()
	repo := newFakeFriendRepository(1, 2)
	repo.friends[pair{1, 2}] = true
	repo.friends[pair{2, 1}] = true
	cache := &fakeFriendCache{deleteFriendErr: errors.New("redis unavailable")}
	service := mustFriendService(t, repo, WithFriendCache(cache))

	require.NoError(t, service.DeleteFriend(context.Background(), 1, 2))
	assert.False(t, repo.friends[pair{1, 2}])
	assert.False(t, repo.friends[pair{2, 1}])
	require.Equal(t, []cacheReconcileCall{
		{resource: repository.CacheResourceFriends, resourceID: 1},
		{resource: repository.CacheResourceFriends, resourceID: 2},
	}, repo.reconcileCalls)
}

func TestFriendServiceDeleteInvalidatesOldAcceptRetry(t *testing.T) {
	t.Parallel()
	repo := newFakeFriendRepository(1, 2)
	request := repo.addRequest(1, 2, model.FriendRequestAccepted)
	repo.friends[pair{1, 2}] = true
	repo.friends[pair{2, 1}] = true
	service := mustFriendService(t, repo)

	require.NoError(t, service.DeleteFriend(context.Background(), 1, 2))
	assert.Equal(t, model.FriendRequestRejected, repo.requests[request.ID].Status)
	_, err := service.AcceptRequest(context.Background(), 2, request.ID)
	requireAppCode(t, err, apperror.CodeRequestNotFound)
	assert.False(t, repo.friends[pair{1, 2}], "stale accept must not recreate a deleted friendship")
	assert.False(t, repo.friends[pair{2, 1}])
}

func TestFriendServiceBlockRetainsFriendshipAndUnblockIsIdempotent(t *testing.T) {
	t.Parallel()
	repo := newFakeFriendRepository(1, 2)
	repo.friends[pair{1, 2}] = true
	repo.friends[pair{2, 1}] = true
	forwardRequest := repo.addRequest(1, 2, model.FriendRequestPending)
	reverseRequest := repo.addRequest(2, 1, model.FriendRequestPending)
	cache := &fakeFriendCache{}
	service := mustFriendService(t, repo, WithFriendCache(cache))

	require.NoError(t, service.Block(context.Background(), 1, 2))
	assert.True(t, repo.blocked[pair{1, 2}])
	assert.True(t, repo.friends[pair{1, 2}], "blocking policy retains friendship")
	assert.True(t, repo.friends[pair{2, 1}])
	assert.Equal(t, model.FriendRequestRejected, repo.requests[forwardRequest.ID].Status)
	assert.Equal(t, model.FriendRequestRejected, repo.requests[reverseRequest.ID].Status)
	assert.Equal(t, 1, cache.setBlacklistCalls)

	requireAppCode(t, service.Block(context.Background(), 1, 2), apperror.CodeAlreadyBlocked)
	require.NoError(t, service.Unblock(context.Background(), 1, 2))
	require.NoError(t, service.Unblock(context.Background(), 1, 2))
	assert.False(t, repo.blocked[pair{1, 2}])
	assert.Equal(t, 2, cache.deleteBlacklistCalls)
}

func TestFriendServiceCacheRejectsNilWriter(t *testing.T) {
	t.Parallel()
	_, err := NewFriendService(newFakeFriendRepository(1, 2), WithFriendCache(nil))
	require.ErrorContains(t, err, "cache must not be nil")
}

type pair struct{ first, second int64 }

type fakeFriendRepository struct {
	mu                         sync.Mutex
	inTransaction              bool
	mutationOutsideTransaction bool
	users                      map[int64]bool
	requests                   map[int64]*model.FriendRequest
	friends                    map[pair]bool
	blocked                    map[pair]bool
	nextRequestID              int64
	requestsPage               []model.FriendRequest
	requestTotal               int64
	friendsPage                []model.Friendship
	friendTotal                int64
	lastRequestPage            [2]int
	lastFriendPage             [2]int
	reconcileCalls             []cacheReconcileCall
}

type cacheReconcileCall struct {
	resource   repository.CacheResource
	resourceID int64
}

func newFakeFriendRepository(userIDs ...int64) *fakeFriendRepository {
	users := make(map[int64]bool, len(userIDs))
	for _, userID := range userIDs {
		users[userID] = true
	}
	return &fakeFriendRepository{
		users: users, requests: map[int64]*model.FriendRequest{}, friends: map[pair]bool{},
		blocked: map[pair]bool{}, nextRequestID: 1,
	}
}

func (f *fakeFriendRepository) WithinFriendTransaction(ctx context.Context, fn func(context.Context, repository.FriendRepository) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inTransaction = true
	defer func() { f.inTransaction = false }()
	return fn(ctx, f)
}

func (f *fakeFriendRepository) LockFriendUsers(_ context.Context, firstID, secondID int64) (int, error) {
	count := 0
	if f.users[firstID] {
		count++
	}
	if secondID != firstID && f.users[secondID] {
		count++
	}
	return count, nil
}

func (f *fakeFriendRepository) GetFriendUserProfile(_ context.Context, userID int64) (*model.User, error) {
	if !f.users[userID] {
		return nil, nil
	}
	return &model.User{ID: userID, Username: "user" + string(rune('0'+userID)), Nickname: "User", AvatarURL: "/avatar.png"}, nil
}

func (f *fakeFriendRepository) EnqueueCacheReconcile(_ context.Context, resource repository.CacheResource, resourceID int64) error {
	f.markMutation()
	f.reconcileCalls = append(f.reconcileCalls, cacheReconcileCall{resource: resource, resourceID: resourceID})
	return nil
}

func (f *fakeFriendRepository) IsFriendPair(_ context.Context, firstID, secondID int64) (bool, error) {
	return f.friends[pair{firstID, secondID}] || f.friends[pair{secondID, firstID}], nil
}

func (f *fakeFriendRepository) IsEitherBlocked(_ context.Context, firstID, secondID int64) (bool, error) {
	return f.blocked[pair{firstID, secondID}] || f.blocked[pair{secondID, firstID}], nil
}

func (f *fakeFriendRepository) FindPendingFriendRequest(_ context.Context, firstID, secondID int64) (*model.FriendRequest, error) {
	for _, request := range f.requests {
		if request.Status == model.FriendRequestPending &&
			((request.FromUserID == firstID && request.ToUserID == secondID) ||
				(request.FromUserID == secondID && request.ToUserID == firstID)) {
			copy := *request
			return &copy, nil
		}
	}
	return nil, nil
}

func (f *fakeFriendRepository) SaveFriendRequest(_ context.Context, request *model.FriendRequest) error {
	f.markMutation()
	for id, existing := range f.requests {
		if existing.FromUserID == request.FromUserID && existing.ToUserID == request.ToUserID && existing.Status != model.FriendRequestPending {
			delete(f.requests, id)
		}
	}
	request.ID = f.nextRequestID
	f.nextRequestID++
	copy := *request
	f.requests[request.ID] = &copy
	return nil
}

func (f *fakeFriendRepository) GetFriendRequest(_ context.Context, requestID int64) (*model.FriendRequest, error) {
	request := f.requests[requestID]
	if request == nil {
		return nil, nil
	}
	copy := *request
	return &copy, nil
}

func (f *fakeFriendRepository) GetFriendRequestForUpdate(ctx context.Context, requestID int64) (*model.FriendRequest, error) {
	return f.GetFriendRequest(ctx, requestID)
}

func (f *fakeFriendRepository) SetFriendRequestStatus(_ context.Context, requestID int64, status int) error {
	f.markMutation()
	request := f.requests[requestID]
	if request == nil {
		return repository.ErrNotFound
	}
	request.Status = status
	return nil
}

func (f *fakeFriendRepository) ListIncomingFriendRequests(_ context.Context, _ int64, limit, offset int) ([]model.FriendRequest, error) {
	f.lastRequestPage = [2]int{limit, offset}
	return append([]model.FriendRequest(nil), f.requestsPage...), nil
}

func (f *fakeFriendRepository) CountIncomingFriendRequests(context.Context, int64) (int64, error) {
	return f.requestTotal, nil
}

func (f *fakeFriendRepository) EnsureFriendshipPair(_ context.Context, firstID, secondID int64) error {
	f.markMutation()
	f.friends[pair{firstID, secondID}] = true
	f.friends[pair{secondID, firstID}] = true
	return nil
}

func (f *fakeFriendRepository) DeleteFriendshipPair(_ context.Context, firstID, secondID int64) error {
	f.markMutation()
	delete(f.friends, pair{firstID, secondID})
	delete(f.friends, pair{secondID, firstID})
	return nil
}

func (f *fakeFriendRepository) InvalidateAcceptedFriendRequests(_ context.Context, firstID, secondID int64) error {
	f.markMutation()
	for _, request := range f.requests {
		if request.Status == model.FriendRequestAccepted &&
			((request.FromUserID == firstID && request.ToUserID == secondID) ||
				(request.FromUserID == secondID && request.ToUserID == firstID)) {
			request.Status = model.FriendRequestRejected
		}
	}
	return nil
}

func (f *fakeFriendRepository) ListFriendshipsPage(_ context.Context, _ int64, limit, offset int) ([]model.Friendship, error) {
	f.lastFriendPage = [2]int{limit, offset}
	return append([]model.Friendship(nil), f.friendsPage...), nil
}

func (f *fakeFriendRepository) CountFriendships(context.Context, int64) (int64, error) {
	return f.friendTotal, nil
}

func (f *fakeFriendRepository) AddBlacklistEntry(_ context.Context, entry *model.Blacklist) error {
	f.markMutation()
	key := pair{entry.UserID, entry.BlockedID}
	if f.blocked[key] {
		return repository.ErrConflict
	}
	f.blocked[key] = true
	return nil
}

func (f *fakeFriendRepository) RejectPendingFriendRequests(_ context.Context, userID, blockedID int64) error {
	f.markMutation()
	for _, request := range f.requests {
		if request.Status == model.FriendRequestPending &&
			((request.FromUserID == userID && request.ToUserID == blockedID) ||
				(request.FromUserID == blockedID && request.ToUserID == userID)) {
			request.Status = model.FriendRequestRejected
		}
	}
	return nil
}

func (f *fakeFriendRepository) RemoveBlacklistEntry(_ context.Context, userID, blockedID int64) error {
	f.markMutation()
	delete(f.blocked, pair{userID, blockedID})
	return nil
}

func (f *fakeFriendRepository) addRequest(fromID, toID int64, status int) *model.FriendRequest {
	request := &model.FriendRequest{ID: f.nextRequestID, FromUserID: fromID, ToUserID: toID, Status: status, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.nextRequestID++
	f.requests[request.ID] = request
	return request
}

func (f *fakeFriendRepository) pendingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, request := range f.requests {
		if request.Status == model.FriendRequestPending {
			count++
		}
	}
	return count
}

func (f *fakeFriendRepository) markMutation() {
	if !f.inTransaction {
		f.mutationOutsideTransaction = true
	}
}

type fakeFriendCache struct {
	setFriendCalls       int
	deleteFriendCalls    int
	setBlacklistCalls    int
	deleteBlacklistCalls int
	setFriendErr         error
	deleteFriendErr      error
	setBlacklistErr      error
	deleteBlacklistErr   error
}

func (f *fakeFriendCache) SetFriendCache(context.Context, int64, int64) error {
	f.setFriendCalls++
	return f.setFriendErr
}
func (f *fakeFriendCache) DeleteFriendCache(context.Context, int64, int64) error {
	f.deleteFriendCalls++
	return f.deleteFriendErr
}
func (f *fakeFriendCache) SetBlacklistMember(context.Context, int64, int64) error {
	f.setBlacklistCalls++
	return f.setBlacklistErr
}
func (f *fakeFriendCache) DeleteBlacklistMember(context.Context, int64, int64) error {
	f.deleteBlacklistCalls++
	return f.deleteBlacklistErr
}

type friendNotification struct {
	userID    int64
	eventType string
	payload   any
}

type fakeFriendNotifier struct{ events []friendNotification }

func (f *fakeFriendNotifier) NotifyFriendEvent(_ context.Context, userID int64, eventType string, payload any) error {
	f.events = append(f.events, friendNotification{userID: userID, eventType: eventType, payload: payload})
	return nil
}

type fakePresence struct {
	states  map[int64]bool
	lastIDs []int64
	err     error
}

func (f *fakePresence) GetOnlineStates(_ context.Context, userIDs []int64) (map[int64]bool, error) {
	f.lastIDs = append([]int64(nil), userIDs...)
	sort.Slice(f.lastIDs, func(i, j int) bool { return f.lastIDs[i] < f.lastIDs[j] })
	return f.states, f.err
}

func mustFriendService(t *testing.T, repo repository.FriendRepository, options ...FriendServiceOption) *FriendServiceImpl {
	t.Helper()
	service, err := NewFriendService(repo, options...)
	require.NoError(t, err)
	return service
}

func requireAppCode(t *testing.T, err error, expected apperror.Code) {
	t.Helper()
	require.Error(t, err)
	var appErr *apperror.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, expected, appErr.Code)
}
