package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"my-im/internal/apperror"
	"my-im/internal/model"
	"my-im/internal/protocol"
	"my-im/internal/repository"
)

func TestGroupServiceTransferOwnershipCommitsEveryOwnershipField(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	cache := &fakeGroupCache{}
	notifier := &fakeGroupEventNotifier{}
	groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
	require.NoError(t, err)

	require.NoError(t, groups.TransferOwnership(context.Background(), 50, 1, 2))

	assert.Equal(t, int64(2), repo.groups[50].OwnerID)
	assert.Equal(t, model.GroupRoleMember, repo.members[groupMemberKey{groupID: 50, userID: 1}].Role)
	newOwner := repo.members[groupMemberKey{groupID: 50, userID: 2}]
	assert.Equal(t, model.GroupRoleOwner, newOwner.Role)
	assert.Nil(t, newOwner.MutedUntil, "the new owner must be able to speak and manage immediately")
	assert.Equal(t, model.GroupRoleMember, repo.members[groupMemberKey{groupID: 50, userID: 3}].Role)
	assert.False(t, repo.mutationOutsideTransaction)
	assert.Equal(t, []string{"group:50", "member:50:1", "member:50:2"}, repo.lockCalls)
	assert.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, 50}}, repo.reconcileCalls)
	assert.Equal(t, []int64{50}, cache.groupIDs)

	require.Len(t, notifier.calls, 4)
	assert.Equal(t, []int64{1, 2, 3, 4}, notificationUserIDs(notifier.calls))
	for _, call := range notifier.calls {
		assert.Equal(t, protocol.TypeGroupUpdated, call.eventType)
		payload, ok := call.payload.(model.GroupUpdatedNotification)
		require.True(t, ok)
		assert.Equal(t, model.GroupUpdatedNotification{
			GroupID: 50,
			Reason:  model.GroupUpdatedReasonOwnerTransferred,
		}, payload)
	}
}

func TestGroupServiceTransferOwnershipUsesRealOwnerAndValidTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		groupID    int64
		operatorID int64
		newOwnerID int64
		arrange    func(*fakeGroupRepository)
		wantCode   apperror.Code
	}{
		{name: "invalid group id", groupID: 0, operatorID: 1, newOwnerID: 2, wantCode: apperror.CodeInvalidParam},
		{name: "invalid operator id", groupID: 50, operatorID: 0, newOwnerID: 2, wantCode: apperror.CodeInvalidParam},
		{name: "invalid target id", groupID: 50, operatorID: 1, newOwnerID: 0, wantCode: apperror.CodeInvalidParam},
		{name: "cannot transfer to self", groupID: 50, operatorID: 1, newOwnerID: 1, wantCode: apperror.CodeInvalidParam},
		{name: "group is missing", groupID: 404, operatorID: 1, newOwnerID: 2, wantCode: apperror.CodeGroupNotFound},
		{name: "administrator is not owner", groupID: 50, operatorID: 2, newOwnerID: 3, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "ordinary member is not owner", groupID: 50, operatorID: 3, newOwnerID: 2, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "stray role two is not owner", groupID: 50, operatorID: 4, newOwnerID: 2, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "outsider is not owner", groupID: 50, operatorID: 5, newOwnerID: 2, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "owner id without member row is invalid authority", groupID: 50, operatorID: 1, newOwnerID: 2, arrange: func(repo *fakeGroupRepository) {
			delete(repo.members, groupMemberKey{groupID: 50, userID: 1})
		}, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "target must already belong to group", groupID: 50, operatorID: 1, newOwnerID: 5, wantCode: apperror.CodeGroupMemberNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := lifecycleTestRepository()
			if test.arrange != nil {
				test.arrange(repo)
			}
			cache := &fakeGroupCache{}
			notifier := &fakeGroupEventNotifier{}
			groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
			require.NoError(t, err)

			err = groups.TransferOwnership(context.Background(), test.groupID, test.operatorID, test.newOwnerID)
			requireAppCode(t, err, test.wantCode)
			assert.Equal(t, int64(1), repo.groups[50].OwnerID)
			assert.Empty(t, repo.reconcileCalls)
			assert.Empty(t, cache.groupIDs)
			assert.Empty(t, notifier.calls)
		})
	}
}

func TestGroupServiceTransferOwnershipTrustsOwnerIDEvenWithStaleRole(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	owner := repo.members[groupMemberKey{groupID: 50, userID: 1}]
	owner.Role = model.GroupRoleMember
	repo.members[groupMemberKey{groupID: 50, userID: 1}] = owner
	groups, err := NewGroupService(repo)
	require.NoError(t, err)

	require.NoError(t, groups.TransferOwnership(context.Background(), 50, 1, 3))
	assert.Equal(t, int64(3), repo.groups[50].OwnerID)
	assert.Equal(t, model.GroupRoleMember, repo.members[groupMemberKey{groupID: 50, userID: 1}].Role)
	assert.Equal(t, model.GroupRoleOwner, repo.members[groupMemberKey{groupID: 50, userID: 3}].Role)
}

func TestGroupServiceTransferOwnershipRollsBackEveryWriteStep(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		arrange func(*fakeGroupRepository)
	}{
		{name: "old owner role update fails", arrange: func(repo *fakeGroupRepository) { repo.updateRoleErr = errors.New("role failed") }},
		{name: "new owner role update fails", arrange: func(repo *fakeGroupRepository) {
			repo.updateRoleErrForUser = map[int64]error{2: errors.New("new owner role failed")}
		}},
		{name: "new owner unmute fails", arrange: func(repo *fakeGroupRepository) { repo.updateMuteErr = errors.New("mute failed") }},
		{name: "group owner update fails", arrange: func(repo *fakeGroupRepository) { repo.updateOwnerErr = errors.New("owner failed") }},
		{name: "reconcile event fails", arrange: func(repo *fakeGroupRepository) { repo.enqueueErr = errors.New("event failed") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := lifecycleTestRepository()
			originalDeadline := *repo.members[groupMemberKey{groupID: 50, userID: 2}].MutedUntil
			test.arrange(repo)
			cache := &fakeGroupCache{}
			notifier := &fakeGroupEventNotifier{}
			groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
			require.NoError(t, err)

			requireAppCode(t, groups.TransferOwnership(context.Background(), 50, 1, 2), apperror.CodeInternalFailure)
			assert.Equal(t, int64(1), repo.groups[50].OwnerID)
			assert.Equal(t, model.GroupRoleOwner, repo.members[groupMemberKey{groupID: 50, userID: 1}].Role)
			newOwner := repo.members[groupMemberKey{groupID: 50, userID: 2}]
			assert.Equal(t, model.GroupRoleAdmin, newOwner.Role)
			require.NotNil(t, newOwner.MutedUntil)
			assert.True(t, newOwner.MutedUntil.Equal(originalDeadline))
			assert.Empty(t, repo.reconcileCalls)
			assert.Empty(t, cache.groupIDs)
			assert.Empty(t, notifier.calls)
		})
	}
}

func TestGroupServiceTransferOwnershipKeepsCommitWhenProjectionsFail(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	repo.getMembersErr = errors.New("recipient query unavailable")
	cache := &fakeGroupCache{err: errors.New("redis unavailable")}
	notifier := &fakeGroupEventNotifier{err: errors.New("socket unavailable")}
	groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
	require.NoError(t, err)

	require.NoError(t, groups.TransferOwnership(context.Background(), 50, 1, 2))
	assert.Equal(t, int64(2), repo.groups[50].OwnerID)
	assert.Len(t, repo.reconcileCalls, 1)
	assert.Equal(t, []int64{50}, cache.groupIDs)
	assert.Equal(t, []int64{1, 2}, notificationUserIDs(notifier.calls), "known old/new owners are the safe fallback recipients")
}

func TestGroupServiceLeavePermissionAndNotifications(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		groupID  int64
		userID   int64
		wantCode apperror.Code
	}{
		{name: "administrator can leave", groupID: 50, userID: 2},
		{name: "ordinary member can leave", groupID: 50, userID: 3},
		{name: "stray role two can leave because owner id is authoritative", groupID: 50, userID: 4},
		{name: "real owner cannot leave even with any role", groupID: 50, userID: 1, wantCode: apperror.CodeCannotLeaveAsOwner},
		{name: "outsider is not a group member", groupID: 50, userID: 5, wantCode: apperror.CodeGroupNotMember},
		{name: "group is missing", groupID: 404, userID: 3, wantCode: apperror.CodeGroupNotFound},
		{name: "invalid group id", groupID: 0, userID: 3, wantCode: apperror.CodeInvalidParam},
		{name: "invalid user id", groupID: 50, userID: 0, wantCode: apperror.CodeInvalidParam},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := lifecycleTestRepository()
			cache := &fakeGroupCache{}
			notifier := &fakeGroupEventNotifier{}
			groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
			require.NoError(t, err)

			err = groups.Leave(context.Background(), test.groupID, test.userID)
			if test.wantCode != 0 {
				requireAppCode(t, err, test.wantCode)
				assert.Empty(t, repo.reconcileCalls)
				assert.Empty(t, cache.groupIDs)
				assert.Empty(t, notifier.calls)
				return
			}

			require.NoError(t, err)
			assert.NotContains(t, repo.members, groupMemberKey{groupID: 50, userID: test.userID})
			assert.False(t, repo.mutationOutsideTransaction)
			assert.Equal(t, []string{"group:50", fmt.Sprintf("member:50:%d", test.userID)}, repo.lockCalls)
			assert.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, 50}}, repo.reconcileCalls)
			assert.Equal(t, []int64{50}, cache.groupIDs)
			require.Len(t, notifier.calls, 4)
			removed := notifier.calls[0]
			assert.Equal(t, test.userID, removed.userID)
			assert.Equal(t, protocol.TypeGroupRemoved, removed.eventType)
			assert.Equal(t, model.GroupRemovedNotification{GroupID: 50, Reason: model.GroupRemovedReasonLeft}, removed.payload)
			for _, call := range notifier.calls[1:] {
				assert.NotEqual(t, test.userID, call.userID)
				assert.Equal(t, protocol.TypeGroupUpdated, call.eventType)
				assert.Equal(t, model.GroupUpdatedNotification{
					GroupID: 50,
					Reason:  model.GroupUpdatedReasonMemberLeft,
				}, call.payload)
			}
		})
	}
}

func TestGroupServiceLeaveRollsBackAndSkipsSideEffects(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		arrange func(*fakeGroupRepository)
	}{
		{name: "member delete fails", arrange: func(repo *fakeGroupRepository) { repo.removeMemberErr = errors.New("delete failed") }},
		{name: "reconcile event fails", arrange: func(repo *fakeGroupRepository) { repo.enqueueErr = errors.New("event failed") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := lifecycleTestRepository()
			test.arrange(repo)
			cache := &fakeGroupCache{}
			notifier := &fakeGroupEventNotifier{}
			groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
			require.NoError(t, err)

			requireAppCode(t, groups.Leave(context.Background(), 50, 3), apperror.CodeInternalFailure)
			assert.Contains(t, repo.members, groupMemberKey{groupID: 50, userID: 3})
			assert.Empty(t, repo.reconcileCalls)
			assert.Empty(t, cache.groupIDs)
			assert.Empty(t, notifier.calls)
		})
	}
}

func TestGroupServiceLeaveKeepsCommitWhenCacheOrNotifierFails(t *testing.T) {
	t.Parallel()
	repo := lifecycleTestRepository()
	cache := &fakeGroupCache{err: errors.New("redis unavailable")}
	notifier := &fakeGroupEventNotifier{err: errors.New("socket unavailable")}
	groups, err := NewGroupService(repo, WithGroupCache(cache), WithGroupEventNotifier(notifier))
	require.NoError(t, err)

	require.NoError(t, groups.Leave(context.Background(), 50, 3))
	assert.NotContains(t, repo.members, groupMemberKey{groupID: 50, userID: 3})
	assert.Len(t, repo.reconcileCalls, 1)
	assert.Equal(t, []int64{50}, cache.groupIDs)
	assert.Len(t, notifier.calls, 4)
}

func TestWithGroupEventNotifierRejectsNil(t *testing.T) {
	t.Parallel()
	_, err := NewGroupService(lifecycleTestRepository(), WithGroupEventNotifier(nil))
	require.EqualError(t, err, "group event notifier must not be nil")
}

func lifecycleTestRepository() *fakeGroupRepository {
	repo := newFakeGroupRepository(1, 2, 3, 4, 5)
	deadline := time.Now().UTC().Add(time.Hour)
	repo.seedGroup(model.Group{ID: 50, Name: "Lifecycle", OwnerID: 1, MaxMembers: 5},
		model.GroupMember{GroupID: 50, UserID: 1, Role: model.GroupRoleOwner},
		model.GroupMember{GroupID: 50, UserID: 2, Role: model.GroupRoleAdmin, MutedUntil: &deadline},
		model.GroupMember{GroupID: 50, UserID: 3, Role: model.GroupRoleMember},
		model.GroupMember{GroupID: 50, UserID: 4, Role: model.GroupRoleOwner},
	)
	return repo
}

type groupNotificationCall struct {
	userID    int64
	eventType string
	payload   any
}

type fakeGroupEventNotifier struct {
	calls []groupNotificationCall
	err   error
}

func (n *fakeGroupEventNotifier) NotifyGroupEvent(_ context.Context, userID int64, eventType string, payload any) error {
	n.calls = append(n.calls, groupNotificationCall{userID: userID, eventType: eventType, payload: payload})
	return n.err
}

func notificationUserIDs(calls []groupNotificationCall) []int64 {
	userIDs := make([]int64, 0, len(calls))
	for _, call := range calls {
		userIDs = append(userIDs, call.userID)
	}
	return userIDs
}

var _ GroupEventNotifier = (*fakeGroupEventNotifier)(nil)
