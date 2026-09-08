package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"my-im/internal/apperror"
	"my-im/internal/model"
	"my-im/internal/protocol"
	"my-im/internal/repository"
)

func TestGroupServiceDisbandDeletesLiveStateAndNotifiesFormerMembers(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	repo.seedGroup(model.Group{ID: 60, OwnerID: 5, MaxMembers: 2},
		model.GroupMember{GroupID: 60, UserID: 5, Role: model.GroupRoleOwner})
	cache := &fakeGroupCache{}
	notifier := &fakeGroupEventNotifier{}
	groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
	require.NoError(t, err)

	require.NoError(t, groups.Disband(context.Background(), 50, 1))

	assert.NotContains(t, repo.groups, int64(50))
	assert.Equal(t, int64(1), repo.tombstones[50])
	for key := range repo.members {
		assert.NotEqual(t, int64(50), key.groupID)
	}
	assert.Contains(t, repo.groups, int64(60), "another group must stay untouched")
	assert.False(t, repo.mutationOutsideTransaction)
	assert.Equal(t, []string{"group:50"}, repo.lockCalls)
	assert.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, 50}}, repo.reconcileCalls)
	assert.Equal(t, []int64{50}, cache.groupIDs)

	require.Len(t, notifier.calls, 4)
	assert.Equal(t, []int64{1, 2, 3, 4}, notificationUserIDs(notifier.calls))
	for _, call := range notifier.calls {
		assert.Equal(t, protocol.TypeGroupRemoved, call.eventType)
		assert.Equal(t, model.GroupRemovedNotification{
			GroupID: 50,
			Reason:  model.GroupRemovedReasonDissolved,
		}, call.payload)
	}
}

func TestGroupServiceDisbandUsesOwnerIDAuthority(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		operatorID int64
		arrange    func(*fakeGroupRepository)
		wantCode   apperror.Code
		wantNotice bool
	}{
		{name: "real owner can disband", operatorID: 1, wantNotice: true},
		{name: "administrator cannot disband", operatorID: 2, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "ordinary member cannot disband", operatorID: 3, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "stray role two cannot disband", operatorID: 4, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "outsider cannot disband", operatorID: 5, wantCode: apperror.CodeNotOwnerOrAdmin},
		{
			name: "owner can recover group even when owner member row is missing", operatorID: 1,
			arrange: func(repo *fakeGroupRepository) {
				delete(repo.members, groupMemberKey{groupID: 50, userID: 1})
			},
			wantNotice: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := lifecycleTestRepository()
			if test.arrange != nil {
				test.arrange(repo)
			}
			notifier := &fakeGroupEventNotifier{}
			groups, err := NewGroupService(repo, WithGroupEventNotifier(notifier))
			require.NoError(t, err)

			err = groups.Disband(context.Background(), 50, test.operatorID)
			if test.wantCode != 0 {
				requireAppCode(t, err, test.wantCode)
				assert.Contains(t, repo.groups, int64(50))
				assert.Empty(t, repo.reconcileCalls)
				assert.Empty(t, notifier.calls)
				return
			}
			require.NoError(t, err)
			assert.NotContains(t, repo.groups, int64(50))
			if test.wantNotice {
				assert.Contains(t, notificationUserIDs(notifier.calls), test.operatorID)
			}
		})
	}
}

func TestGroupServiceDisbandIsIdempotentAndRepairsCacheOnRetry(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	cache := &fakeGroupCache{}
	notifier := &fakeGroupEventNotifier{}
	groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
	require.NoError(t, err)

	require.NoError(t, groups.Disband(context.Background(), 50, 1))
	firstEventCount := len(repo.reconcileCalls)
	firstNoticeCount := len(notifier.calls)
	require.NoError(t, groups.Disband(context.Background(), 50, 1))

	assert.Len(t, repo.reconcileCalls, firstEventCount, "retry must not create a fake second deletion event")
	assert.Len(t, notifier.calls, firstNoticeCount, "retry must not notify members twice")
	assert.Equal(t, []int64{50, 50}, cache.groupIDs, "retry should make another best-effort cleanup attempt")
	assert.Equal(t, []string{"group:50", "group:50"}, repo.lockCalls)
}

func TestGroupServiceDisbandNeverExistingIDDoesNotPolluteCacheOrQueue(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	cache := &fakeGroupCache{}
	notifier := &fakeGroupEventNotifier{}
	groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
	require.NoError(t, err)

	require.NoError(t, groups.Disband(context.Background(), 999999, 1))

	assert.Empty(t, repo.reconcileCalls)
	assert.Empty(t, repo.tombstones)
	assert.Empty(t, cache.groupIDs, "a random absent ID must not trigger Redis SCAN/negative keys")
	assert.Empty(t, notifier.calls)
}

func TestGroupServiceDisbandFailsClosedWhenAbsentStateCannotBeVerified(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	delete(repo.groups, 50)
	for key := range repo.members {
		if key.groupID == 50 {
			delete(repo.members, key)
		}
	}
	repo.tombstoneReadErr = errors.New("mysql unavailable")
	cache := &fakeGroupCache{}
	notifier := &fakeGroupEventNotifier{}
	groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
	require.NoError(t, err)

	requireAppCode(t, groups.Disband(context.Background(), 50, 1), apperror.CodeInternalFailure)

	assert.Empty(t, repo.reconcileCalls)
	assert.Empty(t, repo.tombstones)
	assert.Empty(t, cache.groupIDs, "an unverifiable ID must not touch Redis")
	assert.Empty(t, notifier.calls)
}

func TestGroupServiceDisbandTombstoneRetryCleansWithoutDuplicateEffects(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	delete(repo.groups, 50)
	for key := range repo.members {
		if key.groupID == 50 {
			delete(repo.members, key)
		}
	}
	repo.tombstones[50] = 1
	cache := &fakeGroupCache{}
	notifier := &fakeGroupEventNotifier{}
	groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
	require.NoError(t, err)

	require.NoError(t, groups.Disband(context.Background(), 50, 1))

	assert.Empty(t, repo.reconcileCalls, "retry reuses the original durable fact")
	assert.Equal(t, []int64{50}, cache.groupIDs)
	assert.Empty(t, notifier.calls)
}

func TestGroupServiceDisbandValidatesIDs(t *testing.T) {
	t.Parallel()
	groups, err := NewGroupService(lifecycleTestRepository())
	require.NoError(t, err)
	for _, input := range []struct{ groupID, operatorID int64 }{{0, 1}, {-1, 1}, {50, 0}, {50, -1}} {
		requireAppCode(t, groups.Disband(context.Background(), input.groupID, input.operatorID), apperror.CodeInvalidParam)
	}
}

func TestGroupServiceDisbandRollsBackEveryWriteStep(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		arrange func(*fakeGroupRepository)
	}{
		{name: "read recipients fails", arrange: func(repo *fakeGroupRepository) { repo.getMembersErr = errors.New("read failed") }},
		{name: "write tombstone fails", arrange: func(repo *fakeGroupRepository) { repo.upsertTombstoneErr = errors.New("tombstone failed") }},
		{name: "delete members fails", arrange: func(repo *fakeGroupRepository) { repo.deleteMembersErr = errors.New("members failed") }},
		{name: "delete group fails", arrange: func(repo *fakeGroupRepository) { repo.deleteGroupErr = errors.New("group failed") }},
		{name: "enqueue repair event fails", arrange: func(repo *fakeGroupRepository) { repo.enqueueErr = errors.New("event failed") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := lifecycleTestRepository()
			test.arrange(repo)
			cache := &fakeGroupCache{}
			notifier := &fakeGroupEventNotifier{}
			groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
			require.NoError(t, err)

			requireAppCode(t, groups.Disband(context.Background(), 50, 1), apperror.CodeInternalFailure)
			assert.Contains(t, repo.groups, int64(50))
			assert.Len(t, membersForFakeGroup(repo, 50), 4)
			assert.NotContains(t, repo.tombstones, int64(50))
			assert.Empty(t, repo.reconcileCalls)
			assert.Empty(t, cache.groupIDs)
			assert.Empty(t, notifier.calls)
		})
	}
}

func TestGroupServiceDisbandKeepsCommitWhenPostCommitEffectsFail(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	cache := &fakeGroupCache{err: errors.New("redis unavailable")}
	notifier := &fakeGroupEventNotifier{err: errors.New("socket unavailable")}
	groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
	require.NoError(t, err)

	require.NoError(t, groups.Disband(context.Background(), 50, 1))
	assert.NotContains(t, repo.groups, int64(50))
	assert.Empty(t, membersForFakeGroup(repo, 50))
	assert.Len(t, repo.reconcileCalls, 1, "durable recovery survives Redis failure")
	assert.Equal(t, []int64{50}, cache.groupIDs)
	assert.Len(t, notifier.calls, 4)
}

func membersForFakeGroup(repo *fakeGroupRepository, groupID int64) []model.GroupMember {
	members := make([]model.GroupMember, 0)
	for key, member := range repo.members {
		if key.groupID == groupID {
			members = append(members, member)
		}
	}
	return members
}
