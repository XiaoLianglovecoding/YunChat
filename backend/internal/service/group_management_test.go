package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"my-im/internal/apperror"
	"my-im/internal/model"
	"my-im/internal/repository"
)

func TestGroupServiceUpdateRolePermissionMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		operatorID     int64
		targetID       int64
		role           int
		staleOwnerRole bool
		wantCode       apperror.Code
	}{
		{name: "owner promotes member", operatorID: 1, targetID: 4, role: model.GroupRoleAdmin},
		{name: "owner demotes administrator", operatorID: 1, targetID: 2, role: model.GroupRoleMember},
		{name: "real owner remains authorized with stale role", operatorID: 1, targetID: 4, role: model.GroupRoleAdmin, staleOwnerRole: true},
		{name: "administrator cannot change roles", operatorID: 2, targetID: 4, role: model.GroupRoleAdmin, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "member cannot change roles", operatorID: 4, targetID: 2, role: model.GroupRoleMember, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "stray owner role grants no authority", operatorID: 5, targetID: 4, role: model.GroupRoleAdmin, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "outsider cannot change roles", operatorID: 6, targetID: 4, role: model.GroupRoleAdmin, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "owner role cannot be changed here", operatorID: 1, targetID: 1, role: model.GroupRoleMember, wantCode: apperror.CodeNotOwnerOrAdmin},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := managementTestRepository()
			if test.staleOwnerRole {
				owner := repo.members[groupMemberKey{groupID: 40, userID: 1}]
				owner.Role = model.GroupRoleMember
				repo.members[groupMemberKey{groupID: 40, userID: 1}] = owner
			}
			cache := &fakeGroupCache{}
			groups, err := NewGroupService(repo, WithGroupCache(cache))
			require.NoError(t, err)

			err = groups.UpdateRole(context.Background(), 40, test.operatorID, test.targetID, test.role)
			if test.wantCode != 0 {
				requireAppCode(t, err, test.wantCode)
				assert.Empty(t, repo.reconcileCalls)
				assert.Empty(t, cache.groupIDs)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.role, repo.members[groupMemberKey{groupID: 40, userID: test.targetID}].Role)
			assert.False(t, repo.mutationOutsideTransaction)
			assert.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, 40}}, repo.reconcileCalls)
			assert.Equal(t, []int64{40}, cache.groupIDs)
		})
	}
}

func TestGroupServiceMuteMemberPermissionMatrix(t *testing.T) {
	t.Parallel()
	deadline := time.Now().UTC().Add(2 * time.Hour).Round(time.Second)
	tests := []struct {
		name       string
		operatorID int64
		targetID   int64
		wantCode   apperror.Code
	}{
		{name: "owner mutes administrator", operatorID: 1, targetID: 2},
		{name: "owner mutes member", operatorID: 1, targetID: 4},
		{name: "administrator mutes member", operatorID: 2, targetID: 4},
		{name: "owner cannot mute self", operatorID: 1, targetID: 1, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "administrator cannot mute owner", operatorID: 2, targetID: 1, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "administrator cannot mute peer", operatorID: 2, targetID: 3, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "administrator cannot mute self", operatorID: 2, targetID: 2, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "member cannot mute", operatorID: 4, targetID: 3, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "stray owner role grants no authority", operatorID: 5, targetID: 4, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "outsider cannot mute", operatorID: 6, targetID: 4, wantCode: apperror.CodeNotOwnerOrAdmin},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := managementTestRepository()
			cache := &fakeGroupCache{}
			groups, err := NewGroupService(repo, WithGroupCache(cache))
			require.NoError(t, err)

			err = groups.MuteMember(context.Background(), 40, test.operatorID, test.targetID, &deadline)
			if test.wantCode != 0 {
				requireAppCode(t, err, test.wantCode)
				assert.Empty(t, repo.reconcileCalls)
				assert.Empty(t, cache.groupIDs)
				return
			}
			require.NoError(t, err)
			stored := repo.members[groupMemberKey{groupID: 40, userID: test.targetID}].MutedUntil
			require.NotNil(t, stored)
			assert.True(t, stored.Equal(deadline))
			assert.False(t, repo.mutationOutsideTransaction)
			assert.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, 40}}, repo.reconcileCalls)
			assert.Equal(t, []int64{40}, cache.groupIDs)
		})
	}
}

func TestGroupServiceUnmuteMemberClearsDeadline(t *testing.T) {
	t.Parallel()
	repo := managementTestRepository()
	deadline := time.Now().UTC().Add(time.Hour)
	member := repo.members[groupMemberKey{groupID: 40, userID: 4}]
	member.MutedUntil = &deadline
	repo.members[groupMemberKey{groupID: 40, userID: 4}] = member
	cache := &fakeGroupCache{}
	groups, err := NewGroupService(repo, WithGroupCache(cache))
	require.NoError(t, err)

	require.NoError(t, groups.MuteMember(context.Background(), 40, 2, 4, nil))
	assert.Nil(t, repo.members[groupMemberKey{groupID: 40, userID: 4}].MutedUntil)
	assert.Len(t, repo.reconcileCalls, 1)
	assert.Equal(t, []int64{40}, cache.groupIDs)
}

func TestGroupServiceManagementValidationAndMissingResources(t *testing.T) {
	t.Parallel()
	repo := managementTestRepository()
	groups, err := NewGroupService(repo)
	require.NoError(t, err)

	for _, role := range []int{-1, model.GroupRoleOwner, 99} {
		requireAppCode(t, groups.UpdateRole(context.Background(), 40, 1, 4, role), apperror.CodeInvalidRole)
	}
	requireAppCode(t, groups.UpdateRole(context.Background(), 0, 1, 4, model.GroupRoleAdmin), apperror.CodeInvalidParam)
	requireAppCode(t, groups.UpdateRole(context.Background(), 404, 1, 4, model.GroupRoleAdmin), apperror.CodeGroupNotFound)
	requireAppCode(t, groups.UpdateRole(context.Background(), 40, 1, 99, model.GroupRoleAdmin), apperror.CodeGroupMemberNotFound)

	expired := time.Now().UTC().Add(-time.Minute)
	zero := time.Time{}
	requireAppCode(t, groups.MuteMember(context.Background(), 40, 1, 4, &expired), apperror.CodeInvalidParam)
	requireAppCode(t, groups.MuteMember(context.Background(), 40, 1, 4, &zero), apperror.CodeInvalidParam)
	requireAppCode(t, groups.MuteMember(context.Background(), 0, 1, 4, nil), apperror.CodeInvalidParam)
	requireAppCode(t, groups.MuteMember(context.Background(), 404, 1, 4, nil), apperror.CodeGroupNotFound)
	requireAppCode(t, groups.MuteMember(context.Background(), 40, 1, 99, nil), apperror.CodeGroupMemberNotFound)
	assert.Empty(t, repo.reconcileCalls)
}

func TestGroupServiceManagementIdempotencySkipsWrites(t *testing.T) {
	t.Parallel()
	repo := managementTestRepository()
	requestDeadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second).Add(789 * time.Millisecond)
	storedDeadline := requestDeadline.Truncate(time.Second)
	member := repo.members[groupMemberKey{groupID: 40, userID: 4}]
	member.MutedUntil = &storedDeadline
	repo.members[groupMemberKey{groupID: 40, userID: 4}] = member
	cache := &fakeGroupCache{}
	groups, err := NewGroupService(repo, WithGroupCache(cache))
	require.NoError(t, err)

	require.NoError(t, groups.UpdateRole(context.Background(), 40, 1, 2, model.GroupRoleAdmin))
	require.NoError(t, groups.MuteMember(context.Background(), 40, 1, 4, &requestDeadline))
	assert.Empty(t, repo.reconcileCalls)
	assert.Empty(t, cache.groupIDs)
}

func TestGroupServiceMuteNormalizesToDatabasePrecision(t *testing.T) {
	t.Parallel()
	repo := managementTestRepository()
	groups, err := NewGroupService(repo)
	require.NoError(t, err)
	requestDeadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second).Add(789 * time.Millisecond)

	require.NoError(t, groups.MuteMember(context.Background(), 40, 1, 4, &requestDeadline))
	stored := repo.members[groupMemberKey{groupID: 40, userID: 4}].MutedUntil
	require.NotNil(t, stored)
	assert.Equal(t, requestDeadline.Truncate(time.Second), *stored)
}

func TestGroupServiceManagementRollsBackMutationAndEventTogether(t *testing.T) {
	t.Parallel()
	deadline := time.Now().UTC().Add(2 * time.Hour)
	for _, test := range []struct {
		name    string
		arrange func(*fakeGroupRepository)
		invoke  func(*GroupServiceImpl) error
	}{
		{name: "role update failure", arrange: func(repo *fakeGroupRepository) { repo.updateRoleErr = errors.New("role write failed") }, invoke: func(groups *GroupServiceImpl) error {
			return groups.UpdateRole(context.Background(), 40, 1, 4, model.GroupRoleAdmin)
		}},
		{name: "role event failure", arrange: func(repo *fakeGroupRepository) { repo.enqueueErr = errors.New("event write failed") }, invoke: func(groups *GroupServiceImpl) error {
			return groups.UpdateRole(context.Background(), 40, 1, 4, model.GroupRoleAdmin)
		}},
		{name: "mute update failure", arrange: func(repo *fakeGroupRepository) { repo.updateMuteErr = errors.New("mute write failed") }, invoke: func(groups *GroupServiceImpl) error {
			return groups.MuteMember(context.Background(), 40, 1, 4, &deadline)
		}},
		{name: "mute event failure", arrange: func(repo *fakeGroupRepository) { repo.enqueueErr = errors.New("event write failed") }, invoke: func(groups *GroupServiceImpl) error {
			return groups.MuteMember(context.Background(), 40, 1, 4, &deadline)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := managementTestRepository()
			test.arrange(repo)
			cache := &fakeGroupCache{}
			groups, err := NewGroupService(repo, WithGroupCache(cache))
			require.NoError(t, err)

			requireAppCode(t, test.invoke(groups), apperror.CodeInternalFailure)
			member := repo.members[groupMemberKey{groupID: 40, userID: 4}]
			assert.Equal(t, model.GroupRoleMember, member.Role)
			assert.Nil(t, member.MutedUntil)
			assert.Empty(t, repo.reconcileCalls)
			assert.Empty(t, cache.groupIDs)
		})
	}
}

func TestGroupServiceManagementCacheFailureKeepsCommittedTruth(t *testing.T) {
	t.Parallel()
	t.Run("role", func(t *testing.T) {
		repo := managementTestRepository()
		cache := &fakeGroupCache{err: errors.New("redis unavailable")}
		groups, err := NewGroupService(repo, WithGroupCache(cache))
		require.NoError(t, err)

		require.NoError(t, groups.UpdateRole(context.Background(), 40, 1, 4, model.GroupRoleAdmin))
		assert.Equal(t, model.GroupRoleAdmin, repo.members[groupMemberKey{groupID: 40, userID: 4}].Role)
		assert.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, 40}}, repo.reconcileCalls)
		assert.Equal(t, []int64{40}, cache.groupIDs)
	})

	t.Run("mute", func(t *testing.T) {
		repo := managementTestRepository()
		cache := &fakeGroupCache{err: errors.New("redis unavailable")}
		groups, err := NewGroupService(repo, WithGroupCache(cache))
		require.NoError(t, err)
		deadline := time.Now().UTC().Add(2 * time.Hour).Round(time.Second)

		require.NoError(t, groups.MuteMember(context.Background(), 40, 1, 4, &deadline))
		stored := repo.members[groupMemberKey{groupID: 40, userID: 4}].MutedUntil
		require.NotNil(t, stored)
		assert.True(t, stored.Equal(deadline))
		assert.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, 40}}, repo.reconcileCalls)
		assert.Equal(t, []int64{40}, cache.groupIDs)
	})
}

func managementTestRepository() *fakeGroupRepository {
	repo := newFakeGroupRepository(1, 2, 3, 4, 5, 6)
	repo.seedGroup(model.Group{ID: 40, OwnerID: 1, MaxMembers: 6},
		model.GroupMember{GroupID: 40, UserID: 1, Role: model.GroupRoleOwner},
		model.GroupMember{GroupID: 40, UserID: 2, Role: model.GroupRoleAdmin},
		model.GroupMember{GroupID: 40, UserID: 3, Role: model.GroupRoleAdmin},
		model.GroupMember{GroupID: 40, UserID: 4, Role: model.GroupRoleMember},
		model.GroupMember{GroupID: 40, UserID: 5, Role: model.GroupRoleOwner})
	return repo
}
