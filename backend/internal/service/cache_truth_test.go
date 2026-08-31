package service

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"my-im/internal/model"
	"my-im/internal/repository"
)

type fakeCacheTruthRepository struct {
	mu sync.Mutex

	users   []int64
	groups  []int64
	friends map[int64][]int64
	blocked map[int64][]int64
	members map[int64][]model.GroupMember

	friendReads map[int64]int
	groupReads  map[int64]int
	events      []repository.CacheReconcileEvent
	succeeded   []int64
	failed      []int64
}

func newFakeCacheTruthRepository() *fakeCacheTruthRepository {
	return &fakeCacheTruthRepository{
		friends: make(map[int64][]int64), blocked: make(map[int64][]int64),
		members: make(map[int64][]model.GroupMember), friendReads: make(map[int64]int),
		groupReads: make(map[int64]int),
	}
}

func (f *fakeCacheTruthRepository) EnqueueCacheReconcile(_ context.Context, resource repository.CacheResource, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, repository.CacheReconcileEvent{ID: int64(len(f.events) + 1), ResourceType: resource, ResourceID: id})
	return nil
}

func (f *fakeCacheTruthRepository) WithinCacheSnapshot(
	ctx context.Context,
	_ repository.CacheResource,
	_ int64,
	fn func(context.Context, repository.CacheSnapshotRepository) error,
) error {
	return fn(ctx, f)
}

func pageIDs(ids []int64, after int64, limit int) []int64 {
	result := make([]int64, 0)
	for _, id := range ids {
		if id > after {
			result = append(result, id)
			if len(result) == limit {
				break
			}
		}
	}
	return result
}

func (f *fakeCacheTruthRepository) ListUserIDs(_ context.Context, after int64, limit int) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), pageIDs(f.users, after, limit)...), nil
}

func (f *fakeCacheTruthRepository) ListGroupIDs(_ context.Context, after int64, limit int) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), pageIDs(f.groups, after, limit)...), nil
}

func (f *fakeCacheTruthRepository) ListFriendIDsForCache(_ context.Context, id int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.friendReads[id]++
	return append([]int64(nil), f.friends[id]...), nil
}

func (f *fakeCacheTruthRepository) ListBlockedIDsForCache(_ context.Context, id int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.blocked[id]...), nil
}

func (f *fakeCacheTruthRepository) ListGroupMembersForCache(_ context.Context, id int64) ([]model.GroupMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupReads[id]++
	return append([]model.GroupMember(nil), f.members[id]...), nil
}

func (f *fakeCacheTruthRepository) ClaimCacheReconcileEvents(_ context.Context, _ string, limit int, _ time.Duration) ([]repository.CacheReconcileEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.events) == 0 {
		return nil, nil
	}
	if limit > len(f.events) {
		limit = len(f.events)
	}
	claimed := append([]repository.CacheReconcileEvent(nil), f.events[:limit]...)
	f.events = append([]repository.CacheReconcileEvent(nil), f.events[limit:]...)
	for i := range claimed {
		claimed[i].Attempts++
		claimed[i].LockToken = "claim-token"
	}
	return claimed, nil
}

func (f *fakeCacheTruthRepository) MarkCacheReconcileSuccess(_ context.Context, id int64, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.succeeded = append(f.succeeded, id)
	return nil
}

func (f *fakeCacheTruthRepository) MarkCacheReconcileFailure(_ context.Context, id int64, _ string, _ error, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, id)
	return nil
}

type fakeRelationshipCache struct {
	mu sync.Mutex

	friendLoaded           map[int64]bool
	blockLoaded            map[int64]bool
	groupLoaded            map[int64]bool
	friends                map[int64][]int64
	blocked                map[int64][]int64
	members                map[int64][]model.GroupMember
	missingGroupSet        map[int64][]int64
	missingGroupInfo       map[int64][]int64
	missingGroupReverse    map[int64][]int64
	unexpectedGroupReverse map[int64][]int64
	locks                  map[string]string
	lockAttempts           map[string]int
	invalidations          []string
	replaceGate            chan struct{}
	loadedErr              error
}

func newFakeRelationshipCache() *fakeRelationshipCache {
	return &fakeRelationshipCache{
		friendLoaded: make(map[int64]bool), blockLoaded: make(map[int64]bool),
		groupLoaded: make(map[int64]bool), friends: make(map[int64][]int64),
		blocked: make(map[int64][]int64), members: make(map[int64][]model.GroupMember),
		missingGroupSet: make(map[int64][]int64), missingGroupInfo: make(map[int64][]int64),
		missingGroupReverse: make(map[int64][]int64), unexpectedGroupReverse: make(map[int64][]int64),
		locks: make(map[string]string), lockAttempts: make(map[string]int),
	}
}

func (f *fakeRelationshipCache) FriendsLoaded(_ context.Context, id int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.friendLoaded[id], f.loadedErr
}

func (f *fakeRelationshipCache) BlacklistLoaded(_ context.Context, id int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blockLoaded[id], f.loadedErr
}

func (f *fakeRelationshipCache) GroupMembersLoaded(_ context.Context, id int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.groupLoaded[id], f.loadedErr
}

func (f *fakeRelationshipCache) InvalidateProjectionMarkers(
	_ context.Context,
	resource repository.CacheResource,
	id int64,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch resource {
	case repository.CacheResourceFriends:
		f.friendLoaded[id] = false
	case repository.CacheResourceBlacklist:
		f.blockLoaded[id] = false
	case repository.CacheResourceGroupMembers:
		f.groupLoaded[id] = false
	}
	f.invalidations = append(f.invalidations, lockName(resource, id))
	return nil
}

func lockName(resource repository.CacheResource, id int64) string {
	return string(resource) + ":" + strconv.FormatInt(id, 10)
}

func (f *fakeRelationshipCache) TryCacheWarmLock(_ context.Context, resource repository.CacheResource, id int64, _ time.Duration) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := lockName(resource, id)
	f.lockAttempts[key]++
	if _, held := f.locks[key]; held {
		return "", false, nil
	}
	token := "token"
	f.locks[key] = token
	return token, true, nil
}

func (f *fakeRelationshipCache) ReleaseCacheWarmLock(_ context.Context, resource repository.CacheResource, id int64, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := lockName(resource, id)
	if f.locks[key] == token {
		delete(f.locks, key)
	}
	return nil
}

func (f *fakeRelationshipCache) ReplaceFriendOwner(_ context.Context, id int64, ids []int64) error {
	if f.replaceGate != nil {
		<-f.replaceGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.friends[id] = append([]int64(nil), ids...)
	f.friendLoaded[id] = true
	return nil
}

func (f *fakeRelationshipCache) ReplaceBlacklistOwner(_ context.Context, id int64, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocked[id] = append([]int64(nil), ids...)
	f.blockLoaded[id] = true
	return nil
}

func (f *fakeRelationshipCache) ReplaceGroupMembersOwner(_ context.Context, id int64, members []model.GroupMember) error {
	if f.replaceGate != nil {
		<-f.replaceGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[id] = append([]model.GroupMember(nil), members...)
	f.missingGroupSet[id] = nil
	f.missingGroupInfo[id] = nil
	f.missingGroupReverse[id] = nil
	f.unexpectedGroupReverse[id] = nil
	f.groupLoaded[id] = true
	return nil
}

func (f *fakeRelationshipCache) ReadFriendSnapshot(_ context.Context, id int64) (repository.RelationshipCacheSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return repository.RelationshipCacheSnapshot{Loaded: f.friendLoaded[id], IDs: append([]int64(nil), f.friends[id]...)}, nil
}

func (f *fakeRelationshipCache) ReadBlacklistSnapshot(_ context.Context, id int64) (repository.RelationshipCacheSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return repository.RelationshipCacheSnapshot{Loaded: f.blockLoaded[id], IDs: append([]int64(nil), f.blocked[id]...)}, nil
}

func (f *fakeRelationshipCache) ReadGroupMemberSnapshot(_ context.Context, id int64) (repository.GroupMemberCacheSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return repository.GroupMemberCacheSnapshot{
		Loaded: f.groupLoaded[id], Members: append([]model.GroupMember(nil), f.members[id]...),
		MissingSetIDs:        append([]int64(nil), f.missingGroupSet[id]...),
		MissingInfoIDs:       append([]int64(nil), f.missingGroupInfo[id]...),
		MissingReverseIDs:    append([]int64(nil), f.missingGroupReverse[id]...),
		UnexpectedReverseIDs: append([]int64(nil), f.unexpectedGroupReverse[id]...),
	}, nil
}

func (f *fakeRelationshipCache) GetOnlineStates(_ context.Context, ids []int64) (map[int64]bool, error) {
	return make(map[int64]bool, len(ids)), nil
}

func TestEnsurePrivateAccessRestoresClearedCacheAndMarksEmptyCollectionsLoaded(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	truth.friends[1] = []int64{2}
	truth.friends[2] = []int64{1}
	cache := newFakeRelationshipCache() // models FLUSHDB: no data and no loaded markers
	service := NewCacheTruthService(truth, cache, CacheTruthOptions{})

	require.NoError(t, service.EnsurePrivateAccess(context.Background(), 1, 2))

	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Equal(t, []int64{2}, cache.friends[1])
	require.Equal(t, []int64{1}, cache.friends[2])
	require.True(t, cache.friendLoaded[1])
	require.True(t, cache.friendLoaded[2])
	// Empty blacklists still get markers, otherwise every message would hit MySQL.
	require.True(t, cache.blockLoaded[1])
	require.True(t, cache.blockLoaded[2])
	require.Empty(t, cache.blocked[1])
}

func TestEnsureGroupAccessPreventsConcurrentCacheBreakdown(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	truth.members[7] = []model.GroupMember{{GroupID: 7, UserID: 11}}
	cache := newFakeRelationshipCache()
	cache.replaceGate = make(chan struct{})
	service := NewCacheTruthService(truth, cache, CacheTruthOptions{
		WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})

	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- service.EnsureGroupAccess(context.Background(), 7) }()
	require.Eventually(t, func() bool {
		truth.mu.Lock()
		defer truth.mu.Unlock()
		return truth.groupReads[7] == 1
	}, time.Second, time.Millisecond)
	go func() { errorsChannel <- service.EnsureGroupAccess(context.Background(), 7) }()
	time.Sleep(10 * time.Millisecond)
	close(cache.replaceGate)

	require.NoError(t, <-errorsChannel)
	require.NoError(t, <-errorsChannel)
	truth.mu.Lock()
	defer truth.mu.Unlock()
	require.Equal(t, 1, truth.groupReads[7], "only the lock owner may read MySQL")
}

func TestFriendFastPathWaitsForOldSnapshotThenRereadsLatestTruth(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	truth.friends[1] = []int64{2}
	truth.friends[2] = []int64{1}

	cache := newFakeRelationshipCache()
	gate := make(chan struct{})
	cache.replaceGate = gate
	var closeGate sync.Once
	defer closeGate.Do(func() { close(gate) })

	cacheTruth := NewCacheTruthService(truth, cache, CacheTruthOptions{
		WaitTimeout:  time.Second,
		PollInterval: time.Millisecond,
	})

	// The background repair reads the old friendship snapshot, then stalls just
	// before replacing Redis. This is the dangerous "old snapshot writes late"
	// window that used to be able to overwrite a newer post-commit fast path.
	oldRepairDone := make(chan error, 1)
	go func() {
		oldRepairDone <- cacheTruth.ReconcileNow(context.Background(), repository.CacheResourceFriends, 1)
	}()
	require.Eventually(t, func() bool {
		truth.mu.Lock()
		defer truth.mu.Unlock()
		return truth.friendReads[1] == 1
	}, time.Second, time.Millisecond)

	// Simulate the durable friend-delete transaction committing while that old
	// Redis replacement is delayed. The fast path must wait for the same resource
	// lock; after the old write lands, it opens a fresh WithinCacheSnapshot and
	// reloads the now-empty MySQL truth instead of reusing the old payload.
	truth.mu.Lock()
	truth.friends[1] = nil
	truth.friends[2] = nil
	truth.mu.Unlock()

	fastPathDone := make(chan error, 1)
	go func() {
		fastPathDone <- cacheTruth.DeleteFriendCache(context.Background(), 1, 2)
	}()
	require.Eventually(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return cache.lockAttempts[lockName(repository.CacheResourceFriends, 1)] >= 2
	}, time.Second, time.Millisecond, "the fast path should contend on the old repair's resource lock")

	truth.mu.Lock()
	require.Equal(t, 1, truth.friendReads[1], "a waiting fast path must not read a competing snapshot early")
	truth.mu.Unlock()

	closeGate.Do(func() { close(gate) })
	require.NoError(t, <-oldRepairDone)
	require.NoError(t, <-fastPathDone)

	cache.mu.Lock()
	require.True(t, cache.friendLoaded[1])
	require.Empty(t, cache.friends[1], "the second authoritative reload must remove the stale friendship")
	require.Empty(t, cache.friends[2])
	cache.mu.Unlock()
	truth.mu.Lock()
	require.Equal(t, 2, truth.friendReads[1], "the fast path must reread MySQL after acquiring the lock")
	truth.mu.Unlock()
}

func TestExplicitRebuildInvalidatesProjectionMarkersButWarmDoesNot(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	truth.users = []int64{1}
	truth.friends[1] = []int64{2}
	cache := newFakeRelationshipCache()
	cache.friendLoaded[1] = true
	cache.friends[1] = []int64{99}
	cacheTruth := NewCacheTruthService(truth, cache, CacheTruthOptions{})

	_, err := cacheTruth.Warm(context.Background(), CacheScopeFriends)
	require.NoError(t, err)
	cache.mu.Lock()
	require.Empty(t, cache.invalidations, "startup warm-up must stay on the bounded fast path")
	require.Equal(t, []int64{2}, cache.friends[1])
	cache.mu.Unlock()

	_, err = cacheTruth.Rebuild(context.Background(), CacheScopeFriends)
	require.NoError(t, err)
	cache.mu.Lock()
	require.Equal(t, []string{"friends:1"}, cache.invalidations,
		"operator rebuild must invalidate markers before reading and replacing the projection")
	require.True(t, cache.friendLoaded[1], "replacement must restore the business loaded marker")
	require.Equal(t, []int64{2}, cache.friends[1])
	cache.mu.Unlock()
}

func TestAuditReportsLoadedCacheMismatch(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	truth.users = []int64{1}
	truth.friends[1] = []int64{2}
	cache := newFakeRelationshipCache()
	cache.friendLoaded[1] = true
	cache.friends[1] = []int64{3}
	service := NewCacheTruthService(truth, cache, CacheTruthOptions{})

	report, err := service.Audit(context.Background(), CacheScopeFriends)
	require.NoError(t, err)
	require.Equal(t, 1, report.Checked)
	require.Equal(t, 1, report.Mismatches)
	require.Equal(t, []int64{2}, report.Issues[0].MySQLIDs)
	require.Equal(t, []int64{3}, report.Issues[0].RedisIDs)
}

func TestAuditReportsMissingUserGroupsReverseMembership(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	truth.groups = []int64{7}
	truth.members[7] = []model.GroupMember{{GroupID: 7, UserID: 11, Role: 0}}
	cache := newFakeRelationshipCache()
	cache.groupLoaded[7] = true
	cache.members[7] = append([]model.GroupMember(nil), truth.members[7]...)
	cache.missingGroupReverse[7] = []int64{11}
	service := NewCacheTruthService(truth, cache, CacheTruthOptions{})

	report, err := service.Audit(context.Background(), CacheScopeGroups)
	require.NoError(t, err)
	require.Equal(t, 1, report.Checked)
	require.Equal(t, 1, report.Mismatches)
	require.Equal(t, []int64{11}, report.Issues[0].MissingReverseIDs)
}

func TestAuditReportsGroupSetAndInfoDivergence(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	truth.groups = []int64{7}
	truth.members[7] = []model.GroupMember{
		{GroupID: 7, UserID: 11, Role: 0},
		{GroupID: 7, UserID: 12, Role: 0},
	}
	cache := newFakeRelationshipCache()
	cache.groupLoaded[7] = true
	cache.members[7] = append([]model.GroupMember(nil), truth.members[7]...)
	cache.missingGroupSet[7] = []int64{11}
	cache.missingGroupInfo[7] = []int64{12}
	service := NewCacheTruthService(truth, cache, CacheTruthOptions{})

	report, err := service.Audit(context.Background(), CacheScopeGroups)
	require.NoError(t, err)
	require.Equal(t, 1, report.Mismatches)
	require.Equal(t, []int64{11}, report.Issues[0].MissingSetIDs)
	require.Equal(t, []int64{12}, report.Issues[0].MissingInfoIDs)
}

func TestAuditReportsUnexpectedUserGroupsReverseMembership(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	truth.groups = []int64{7}
	truth.members[7] = []model.GroupMember{{GroupID: 7, UserID: 11}}
	cache := newFakeRelationshipCache()
	cache.groupLoaded[7] = true
	cache.members[7] = append([]model.GroupMember(nil), truth.members[7]...)
	cache.unexpectedGroupReverse[7] = []int64{99}
	service := NewCacheTruthService(truth, cache, CacheTruthOptions{})

	report, err := service.Audit(context.Background(), CacheScopeGroups)
	require.NoError(t, err)
	require.Equal(t, 1, report.Mismatches)
	require.Equal(t, []int64{99}, report.Issues[0].UnexpectedReverseIDs)
}

func TestNewCacheTruthServiceRejectsNilDependenciesImmediately(t *testing.T) {
	cache := newFakeRelationshipCache()
	require.PanicsWithValue(t, "NewCacheTruthService: nil MySQL truth repository", func() {
		NewCacheTruthService(nil, cache, CacheTruthOptions{})
	})
	truth := newFakeCacheTruthRepository()
	require.PanicsWithValue(t, "NewCacheTruthService: nil Redis relationship cache repository", func() {
		NewCacheTruthService(truth, nil, CacheTruthOptions{})
	})
}

func TestReconcilerUsesCurrentMySQLTruthInsteadOfOldEventPayload(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	truth.friends[1] = []int64{} // friendship was deleted after this old event was inserted
	truth.events = []repository.CacheReconcileEvent{{
		ID: 9, ResourceType: repository.CacheResourceFriends, ResourceID: 1,
	}}
	cache := newFakeRelationshipCache()
	cache.friendLoaded[1] = true
	cache.friends[1] = []int64{2} // stale Redis relation must be removed, not resurrected
	cacheTruth := NewCacheTruthService(truth, cache, CacheTruthOptions{})
	reconciler := NewCacheReconciler(truth, cacheTruth, "test-worker", CacheReconcilerOptions{})

	processed, err := reconciler.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	cache.mu.Lock()
	require.Empty(t, cache.friends[1])
	require.True(t, cache.friendLoaded[1])
	cache.mu.Unlock()
	truth.mu.Lock()
	require.Equal(t, []int64{9}, truth.succeeded)
	truth.mu.Unlock()
}

func TestRedisFailureIsAnErrorNotNegativeRelationshipTruth(t *testing.T) {
	truth := newFakeCacheTruthRepository()
	cache := newFakeRelationshipCache()
	cache.loadedErr = errors.New("redis unavailable")
	service := NewCacheTruthService(truth, cache, CacheTruthOptions{})

	err := service.EnsurePrivateAccess(context.Background(), 1, 2)
	require.Error(t, err)
	var cacheErr *CacheTruthError
	require.ErrorAs(t, err, &cacheErr)
	require.Equal(t, "redis", cacheErr.Layer)
}
