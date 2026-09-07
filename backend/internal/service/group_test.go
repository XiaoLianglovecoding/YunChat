package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"my-im/internal/apperror"
	"my-im/internal/model"
	"my-im/internal/repository"
)

func TestGroupServiceCreateIsAtomicAndRefreshesCache(t *testing.T) {
	t.Parallel()
	repo := newFakeGroupRepository(7)
	cache := &fakeGroupCache{}
	groups, err := NewGroupService(repo, WithGroupCache(cache))
	require.NoError(t, err)

	groupID, err := groups.Create(context.Background(), 7, "  学习小组  ", "  每天进步一点  ")
	require.NoError(t, err)
	require.Positive(t, groupID)

	group := repo.groups[groupID]
	assert.Equal(t, "学习小组", group.Name)
	assert.Equal(t, "每天进步一点", group.Notice)
	assert.Equal(t, int64(7), group.OwnerID)
	assert.Equal(t, 500, group.MaxMembers)
	owner, ok := repo.members[groupMemberKey{groupID: groupID, userID: 7}]
	require.True(t, ok)
	assert.Equal(t, model.GroupRoleOwner, owner.Role)
	assert.False(t, repo.mutationOutsideTransaction)
	require.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, groupID}}, repo.reconcileCalls)
	assert.Equal(t, []int64{groupID}, cache.groupIDs)
}

func TestGroupServiceCreateRollsBackEveryDurableWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		arrange func(*fakeGroupRepository)
	}{
		{name: "owner member insert fails", arrange: func(repo *fakeGroupRepository) {
			repo.addMemberErr = errors.New("insert owner failed")
		}},
		{name: "cache event insert fails", arrange: func(repo *fakeGroupRepository) {
			repo.enqueueErr = errors.New("insert reconcile event failed")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeGroupRepository(1)
			test.arrange(repo)
			cache := &fakeGroupCache{}
			groups, err := NewGroupService(repo, WithGroupCache(cache))
			require.NoError(t, err)

			groupID, err := groups.Create(context.Background(), 1, "rollback", "")
			require.Zero(t, groupID)
			requireAppCode(t, err, apperror.CodeInternalFailure)
			assert.Empty(t, repo.groups, "orphan group must be rolled back")
			assert.Empty(t, repo.members, "orphan owner member must be rolled back")
			assert.Empty(t, repo.reconcileCalls)
			assert.Empty(t, cache.groupIDs, "post-commit fast path must not run after rollback")
		})
	}
}

func TestGroupServiceCreateCacheFailureDoesNotUndoCommittedGroup(t *testing.T) {
	t.Parallel()
	repo := newFakeGroupRepository(1)
	cache := &fakeGroupCache{err: errors.New("redis unavailable")}
	groups, err := NewGroupService(repo, WithGroupCache(cache))
	require.NoError(t, err)

	groupID, err := groups.Create(context.Background(), 1, "durable", "")
	require.NoError(t, err)
	assert.Contains(t, repo.groups, groupID)
	assert.Contains(t, repo.members, groupMemberKey{groupID: groupID, userID: 1})
	require.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, groupID}}, repo.reconcileCalls)
	assert.Equal(t, []int64{groupID}, cache.groupIDs)
}

func TestGroupProfileValidationUsesUnicodeCharacters(t *testing.T) {
	t.Parallel()

	name, notice, err := normalizeGroupProfile("  "+strings.Repeat("群", 50)+"  ", "  第一行\n第二行  ")
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("群", 50), name)
	assert.Equal(t, "第一行\n第二行", notice)

	for _, test := range []struct {
		name   string
		notice string
	}{
		{name: "   "},
		{name: strings.Repeat("群", 51)},
		{name: "bad\nname"},
		{name: "valid", notice: strings.Repeat("公", 301)},
		{name: "valid", notice: "bad\x00notice"},
	} {
		_, _, err := normalizeGroupProfile(test.name, test.notice)
		requireAppCode(t, err, apperror.CodeInvalidParam)
	}

	_, notice, err = normalizeGroupProfile("valid", strings.Repeat("公", 300))
	require.NoError(t, err)
	assert.Equal(t, 300, len([]rune(notice)))
}

func TestGroupServiceListAndGetUseMembershipTruth(t *testing.T) {
	t.Parallel()
	repo := newFakeGroupRepository(1, 2, 3)
	repo.seedGroup(model.Group{ID: 10, Name: "member group", OwnerID: 1, MaxMembers: 500},
		model.GroupMember{GroupID: 10, UserID: 1, Role: model.GroupRoleOwner},
		model.GroupMember{GroupID: 10, UserID: 2, Role: model.GroupRoleMember})
	// owner_id alone is deliberately insufficient: user 3 has no membership row.
	repo.seedGroup(model.Group{ID: 11, Name: "orphan owner expression", OwnerID: 3, MaxMembers: 500})
	groups, err := NewGroupService(repo)
	require.NoError(t, err)

	list, err := groups.ListByUser(context.Background(), 2)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int64(10), list[0].ID)
	assert.Equal(t, int64(2), repo.lastListUserID)

	empty, err := groups.ListByUser(context.Background(), 3)
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)

	group, err := groups.Get(context.Background(), 2, 10)
	require.NoError(t, err)
	assert.Equal(t, "member group", group.Name)
	_, err = groups.Get(context.Background(), 3, 10)
	requireAppCode(t, err, apperror.CodeGroupNotMember)
	_, err = groups.Get(context.Background(), 1, 999)
	requireAppCode(t, err, apperror.CodeGroupNotFound)
}

func TestGroupServiceUpdatePermissionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		operatorID int64
		role       *int
		wantCode   apperror.Code
	}{
		{name: "real owner remains authorized if role projection is stale", operatorID: 1, role: groupRole(model.GroupRoleMember)},
		{name: "administrator is authorized", operatorID: 2, role: groupRole(model.GroupRoleAdmin)},
		{name: "ordinary member is denied", operatorID: 3, role: groupRole(model.GroupRoleMember), wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "stale second owner role is denied", operatorID: 4, role: groupRole(model.GroupRoleOwner), wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "non member is denied", operatorID: 5, wantCode: apperror.CodeNotOwnerOrAdmin},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeGroupRepository(1, 2, 3, 4, 5)
			members := []model.GroupMember{{GroupID: 20, UserID: 1, Role: model.GroupRoleOwner}}
			if test.role != nil && test.operatorID != 1 {
				members = append(members, model.GroupMember{GroupID: 20, UserID: test.operatorID, Role: *test.role})
			} else if test.role != nil {
				members[0].Role = *test.role
			}
			repo.seedGroup(model.Group{ID: 20, Name: "before", OwnerID: 1, MaxMembers: 500}, members...)
			groups, err := NewGroupService(repo)
			require.NoError(t, err)

			err = groups.Update(context.Background(), test.operatorID, 20, "  after  ", "  new notice  ")
			if test.wantCode != 0 {
				requireAppCode(t, err, test.wantCode)
				assert.Equal(t, "before", repo.groups[20].Name)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "after", repo.groups[20].Name)
			assert.Equal(t, "new notice", repo.groups[20].Notice)
			assert.False(t, repo.mutationOutsideTransaction)
		})
	}
}

func TestGroupServiceUpdateMissingGroup(t *testing.T) {
	t.Parallel()
	repo := newFakeGroupRepository(1)
	groups, err := NewGroupService(repo)
	require.NoError(t, err)
	requireAppCode(t, groups.Update(context.Background(), 1, 404, "name", ""), apperror.CodeGroupNotFound)
}

func TestGroupServiceAddMemberPermissionMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		operatorID int64
		role       *int
		wantCode   apperror.Code
	}{
		{name: "real owner with stale member role", operatorID: 1, role: groupRole(model.GroupRoleMember)},
		{name: "administrator", operatorID: 2, role: groupRole(model.GroupRoleAdmin)},
		{name: "ordinary member", operatorID: 3, role: groupRole(model.GroupRoleMember), wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "stale second owner role", operatorID: 4, role: groupRole(model.GroupRoleOwner), wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "outsider", operatorID: 5, wantCode: apperror.CodeNotOwnerOrAdmin},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeGroupRepository(1, 2, 3, 4, 5, 6)
			members := []model.GroupMember{{GroupID: 30, UserID: 1, Role: model.GroupRoleOwner}}
			if test.operatorID == 1 && test.role != nil {
				members[0].Role = *test.role
			} else if test.role != nil {
				members = append(members, model.GroupMember{GroupID: 30, UserID: test.operatorID, Role: *test.role})
			}
			repo.seedGroup(model.Group{ID: 30, OwnerID: 1, MaxMembers: 6}, members...)
			repo.seedFriendship(test.operatorID, 6)
			cache := &fakeGroupCache{}
			groups, err := NewGroupService(repo, WithGroupCache(cache))
			require.NoError(t, err)

			err = groups.AddMember(context.Background(), 30, test.operatorID, 6)
			if test.wantCode != 0 {
				requireAppCode(t, err, test.wantCode)
				assert.NotContains(t, repo.members, groupMemberKey{groupID: 30, userID: 6})
				assert.Empty(t, repo.reconcileCalls)
				assert.Empty(t, cache.groupIDs)
				return
			}
			require.NoError(t, err)
			added := repo.members[groupMemberKey{groupID: 30, userID: 6}]
			assert.Equal(t, model.GroupRoleMember, added.Role)
			assert.False(t, repo.mutationOutsideTransaction)
			assert.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, 30}}, repo.reconcileCalls)
			assert.Equal(t, []int64{30}, cache.groupIDs)
		})
	}
}

func TestGroupServiceAddMemberBusinessRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		arrange  func(*fakeGroupRepository)
		wantCode apperror.Code
	}{
		{name: "group missing", arrange: func(repo *fakeGroupRepository) { delete(repo.groups, 31) }, wantCode: apperror.CodeGroupNotFound},
		{name: "target user missing", arrange: func(repo *fakeGroupRepository) { delete(repo.users, 3) }, wantCode: apperror.CodeUserNotFound},
		{name: "target is not inviter friend", arrange: func(repo *fakeGroupRepository) { delete(repo.friendships, canonicalGroupUserPair(1, 3)) }, wantCode: apperror.CodeMemberNotFriend},
		{name: "target already belongs to group", arrange: func(repo *fakeGroupRepository) {
			repo.seedGroupMember(model.GroupMember{GroupID: 31, UserID: 3, Role: model.GroupRoleMember})
		}, wantCode: apperror.CodeAlreadyMember},
		{name: "dynamic group capacity reached", arrange: func(repo *fakeGroupRepository) {
			group := repo.groups[31]
			group.MaxMembers = 1
			repo.groups[31] = group
		}, wantCode: apperror.CodeGroupFull},
		{name: "unique conflict maps to already member", arrange: func(repo *fakeGroupRepository) {
			repo.addMemberErr = fmt.Errorf("duplicate group member: %w", repository.ErrConflict)
		}, wantCode: apperror.CodeAlreadyMember},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeGroupRepository(1, 3)
			repo.seedGroup(model.Group{ID: 31, OwnerID: 1, MaxMembers: 2},
				model.GroupMember{GroupID: 31, UserID: 1, Role: model.GroupRoleOwner})
			repo.seedFriendship(1, 3)
			test.arrange(repo)
			groups, err := NewGroupService(repo)
			require.NoError(t, err)

			err = groups.AddMember(context.Background(), 31, 1, 3)
			requireAppCode(t, err, test.wantCode)
			assert.Empty(t, repo.reconcileCalls)
		})
	}
}

func TestGroupServiceAddMemberRollsBackDurableWrites(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		arrange func(*fakeGroupRepository)
	}{
		{name: "member insert fails", arrange: func(repo *fakeGroupRepository) { repo.addMemberErr = errors.New("insert failed") }},
		{name: "reconcile event fails", arrange: func(repo *fakeGroupRepository) { repo.enqueueErr = errors.New("event failed") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeGroupRepository(1, 2)
			repo.seedGroup(model.Group{ID: 32, OwnerID: 1, MaxMembers: 2},
				model.GroupMember{GroupID: 32, UserID: 1, Role: model.GroupRoleOwner})
			repo.seedFriendship(1, 2)
			test.arrange(repo)
			cache := &fakeGroupCache{}
			groups, err := NewGroupService(repo, WithGroupCache(cache))
			require.NoError(t, err)

			requireAppCode(t, groups.AddMember(context.Background(), 32, 1, 2), apperror.CodeInternalFailure)
			assert.NotContains(t, repo.members, groupMemberKey{groupID: 32, userID: 2})
			assert.Empty(t, repo.reconcileCalls)
			assert.Empty(t, cache.groupIDs)
		})
	}
}

func TestGroupServiceAddMemberCacheFailureKeepsCommit(t *testing.T) {
	t.Parallel()
	repo := newFakeGroupRepository(1, 2)
	repo.seedGroup(model.Group{ID: 33, OwnerID: 1, MaxMembers: 2},
		model.GroupMember{GroupID: 33, UserID: 1, Role: model.GroupRoleOwner})
	repo.seedFriendship(1, 2)
	cache := &fakeGroupCache{err: errors.New("redis unavailable")}
	groups, err := NewGroupService(repo, WithGroupCache(cache))
	require.NoError(t, err)

	require.NoError(t, groups.AddMember(context.Background(), 33, 1, 2))
	assert.Contains(t, repo.members, groupMemberKey{groupID: 33, userID: 2})
	assert.Len(t, repo.reconcileCalls, 1)
	assert.Equal(t, []int64{33}, cache.groupIDs)
}

func TestGroupServiceRemoveMemberPermissionMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		operatorID int64
		targetID   int64
		wantCode   apperror.Code
	}{
		{name: "owner removes administrator", operatorID: 1, targetID: 2},
		{name: "owner removes ordinary member", operatorID: 1, targetID: 4},
		{name: "owner cannot remove self", operatorID: 1, targetID: 1, wantCode: apperror.CodeCannotRemoveOwner},
		{name: "administrator removes ordinary member", operatorID: 2, targetID: 4},
		{name: "administrator cannot remove peer", operatorID: 2, targetID: 3, wantCode: apperror.CodeCannotRemovePeer},
		{name: "administrator cannot remove owner", operatorID: 2, targetID: 1, wantCode: apperror.CodeCannotRemoveOwner},
		{name: "ordinary member cannot remove", operatorID: 4, targetID: 3, wantCode: apperror.CodeNotOwnerOrAdmin},
		{name: "outsider cannot remove", operatorID: 5, targetID: 4, wantCode: apperror.CodeNotOwnerOrAdmin},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeGroupRepository(1, 2, 3, 4, 5)
			repo.seedGroup(model.Group{ID: 34, OwnerID: 1, MaxMembers: 5},
				model.GroupMember{GroupID: 34, UserID: 1, Role: model.GroupRoleOwner},
				model.GroupMember{GroupID: 34, UserID: 2, Role: model.GroupRoleAdmin},
				model.GroupMember{GroupID: 34, UserID: 3, Role: model.GroupRoleAdmin},
				model.GroupMember{GroupID: 34, UserID: 4, Role: model.GroupRoleMember})
			cache := &fakeGroupCache{}
			groups, err := NewGroupService(repo, WithGroupCache(cache))
			require.NoError(t, err)

			err = groups.RemoveMember(context.Background(), 34, test.operatorID, test.targetID)
			key := groupMemberKey{groupID: 34, userID: test.targetID}
			if test.wantCode != 0 {
				requireAppCode(t, err, test.wantCode)
				assert.Contains(t, repo.members, key)
				assert.Empty(t, repo.reconcileCalls)
				assert.Empty(t, cache.groupIDs)
				return
			}
			require.NoError(t, err)
			assert.NotContains(t, repo.members, key)
			assert.Equal(t, []groupReconcileCall{{repository.CacheResourceGroupMembers, 34}}, repo.reconcileCalls)
			assert.Equal(t, []int64{34}, cache.groupIDs)
		})
	}
}

func TestGroupServiceRemoveMemberMissingAndRollback(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		arrange  func(*fakeGroupRepository)
		wantCode apperror.Code
	}{
		{name: "target is not a member", arrange: func(*fakeGroupRepository) {}, wantCode: apperror.CodeGroupMemberNotFound},
		{name: "delete fails", arrange: func(repo *fakeGroupRepository) { repo.removeMemberErr = errors.New("delete failed") }, wantCode: apperror.CodeInternalFailure},
		{name: "event fails after delete", arrange: func(repo *fakeGroupRepository) { repo.enqueueErr = errors.New("event failed") }, wantCode: apperror.CodeInternalFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeGroupRepository(1, 2, 3)
			repo.seedGroup(model.Group{ID: 35, OwnerID: 1, MaxMembers: 3},
				model.GroupMember{GroupID: 35, UserID: 1, Role: model.GroupRoleOwner},
				model.GroupMember{GroupID: 35, UserID: 2, Role: model.GroupRoleMember})
			test.arrange(repo)
			cache := &fakeGroupCache{}
			groups, err := NewGroupService(repo, WithGroupCache(cache))
			require.NoError(t, err)

			targetID := int64(2)
			if test.name == "target is not a member" {
				targetID = 3
			}
			requireAppCode(t, groups.RemoveMember(context.Background(), 35, 1, targetID), test.wantCode)
			if targetID == 2 {
				assert.Contains(t, repo.members, groupMemberKey{groupID: 35, userID: 2}, "failed transaction must restore target")
			}
			assert.Empty(t, repo.reconcileCalls)
			assert.Empty(t, cache.groupIDs)
		})
	}
}

func TestGroupServiceListMembersUsesAuthorizedStableDatabasePage(t *testing.T) {
	t.Parallel()
	repo := newFakeGroupRepository(1, 2, 3, 4, 5)
	repo.usernames[2], repo.avatars[2] = "admin", "/admin.png"
	repo.usernames[3], repo.avatars[3] = "alice", "/alice.png"
	repo.seedGroup(model.Group{ID: 36, OwnerID: 1, MaxMembers: 5},
		model.GroupMember{GroupID: 36, UserID: 1, Role: model.GroupRoleOwner, JoinedAt: time.Unix(4, 0)},
		model.GroupMember{GroupID: 36, UserID: 2, Role: model.GroupRoleAdmin, JoinedAt: time.Unix(3, 0)},
		model.GroupMember{GroupID: 36, UserID: 3, Role: model.GroupRoleMember, JoinedAt: time.Unix(1, 0)},
		model.GroupMember{GroupID: 36, UserID: 4, Role: model.GroupRoleMember, JoinedAt: time.Unix(2, 0)})
	groups, err := NewGroupService(repo)
	require.NoError(t, err)

	page, err := groups.ListMembers(context.Background(), 36, 3, 2, 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, int64(4), page.Total)
	assert.Equal(t, 2, page.Limit)
	assert.Equal(t, 1, page.Offset)
	assert.Equal(t, []int64{2, 3}, []int64{page.Items[0].UserID, page.Items[1].UserID})
	assert.Equal(t, "admin", page.Items[0].Username)
	assert.Equal(t, "/admin.png", page.Items[0].AvatarURL)
	assert.Equal(t, "alice", page.Items[1].Username)
	assert.Equal(t, struct {
		groupID       int64
		limit, offset int
	}{groupID: 36, limit: 2, offset: 1}, repo.lastMemberPage)

	empty, err := groups.ListMembers(context.Background(), 36, 3, 2, 99)
	require.NoError(t, err)
	assert.NotNil(t, empty.Items)
	assert.Empty(t, empty.Items)
	assert.Equal(t, int64(4), empty.Total)
}

func TestGroupServiceListMembersAuthorizationAndPaginationErrors(t *testing.T) {
	t.Parallel()
	repo := newFakeGroupRepository(1, 2)
	repo.seedGroup(model.Group{ID: 37, OwnerID: 1, MaxMembers: 2},
		model.GroupMember{GroupID: 37, UserID: 1, Role: model.GroupRoleOwner})
	groups, err := NewGroupService(repo)
	require.NoError(t, err)

	_, err = groups.ListMembers(context.Background(), 404, 1, 20, 0)
	requireAppCode(t, err, apperror.CodeGroupNotFound)
	_, err = groups.ListMembers(context.Background(), 37, 2, 20, 0)
	requireAppCode(t, err, apperror.CodeGroupNotMember)
	_, err = groups.ListMembers(context.Background(), 37, 1, 101, 0)
	requireAppCode(t, err, apperror.CodeInvalidParam)
	_, err = groups.ListMembers(context.Background(), 37, 1, 20, -1)
	requireAppCode(t, err, apperror.CodeInvalidParam)

	page, err := groups.ListMembers(context.Background(), 37, 1, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, defaultGroupMemberPageSize, page.Limit)
}

func groupRole(role int) *int { return &role }

type groupMemberKey struct {
	groupID int64
	userID  int64
}

type groupReconcileCall struct {
	resource   repository.CacheResource
	resourceID int64
}

type fakeGroupRepository struct {
	users                      map[int64]bool
	usernames                  map[int64]string
	avatars                    map[int64]string
	friendships                map[groupUserPair]bool
	groups                     map[int64]model.Group
	members                    map[groupMemberKey]model.GroupMember
	reconcileCalls             []groupReconcileCall
	nextGroupID                int64
	nextMemberID               int64
	inTransaction              bool
	mutationOutsideTransaction bool
	addMemberErr               error
	removeMemberErr            error
	lockUsersErr               error
	friendshipErr              error
	countMembersErr            error
	listMembersErr             error
	enqueueErr                 error
	lastListUserID             int64
	lastMemberPage             struct {
		groupID       int64
		limit, offset int
	}
}

type groupUserPair struct{ first, second int64 }

func newFakeGroupRepository(userIDs ...int64) *fakeGroupRepository {
	repo := &fakeGroupRepository{
		users: make(map[int64]bool), usernames: make(map[int64]string), avatars: make(map[int64]string),
		friendships: make(map[groupUserPair]bool), groups: make(map[int64]model.Group),
		members: make(map[groupMemberKey]model.GroupMember), nextGroupID: 100, nextMemberID: 1000,
	}
	for _, userID := range userIDs {
		repo.users[userID] = true
		repo.usernames[userID] = fmt.Sprintf("user-%d", userID)
	}
	return repo
}

func (r *fakeGroupRepository) WithinGroupTransaction(ctx context.Context, fn func(context.Context, repository.GroupRepository) error) error {
	groupsBefore := cloneGroups(r.groups)
	membersBefore := cloneGroupMembers(r.members)
	reconcileBefore := append([]groupReconcileCall(nil), r.reconcileCalls...)
	nextGroupBefore, nextMemberBefore := r.nextGroupID, r.nextMemberID
	r.inTransaction = true
	err := fn(ctx, r)
	r.inTransaction = false
	if err != nil {
		r.groups, r.members, r.reconcileCalls = groupsBefore, membersBefore, reconcileBefore
		r.nextGroupID, r.nextMemberID = nextGroupBefore, nextMemberBefore
	}
	return err
}

func (r *fakeGroupRepository) LockGroupCreator(_ context.Context, userID int64) (bool, error) {
	return r.users[userID], nil
}

func (r *fakeGroupRepository) CreateGroup(_ context.Context, group *model.Group) (int64, error) {
	r.markMutation()
	r.nextGroupID++
	group.ID = r.nextGroupID
	if group.MaxMembers == 0 {
		group.MaxMembers = 500
	}
	group.CreatedAt = time.Now()
	group.UpdatedAt = group.CreatedAt
	r.groups[group.ID] = *group
	return group.ID, nil
}

func (r *fakeGroupRepository) AddGroupMember(_ context.Context, member *model.GroupMember) error {
	r.markMutation()
	if r.addMemberErr != nil {
		return r.addMemberErr
	}
	key := groupMemberKey{groupID: member.GroupID, userID: member.UserID}
	if _, exists := r.members[key]; exists {
		return repository.ErrConflict
	}
	r.nextMemberID++
	member.ID = r.nextMemberID
	member.JoinedAt = time.Now()
	r.members[key] = *member
	return nil
}

func (r *fakeGroupRepository) GetGroupByID(_ context.Context, groupID int64) (*model.Group, error) {
	group, ok := r.groups[groupID]
	if !ok {
		return nil, nil
	}
	copy := group
	return &copy, nil
}

func (r *fakeGroupRepository) GetGroupForUpdate(ctx context.Context, groupID int64) (*model.Group, error) {
	return r.GetGroupByID(ctx, groupID)
}

func (r *fakeGroupRepository) GetGroupMember(_ context.Context, groupID, userID int64) (*model.GroupMember, error) {
	member, ok := r.members[groupMemberKey{groupID: groupID, userID: userID}]
	if !ok {
		return nil, nil
	}
	copy := member
	return &copy, nil
}

func (r *fakeGroupRepository) GetGroupMemberForUpdate(ctx context.Context, groupID, userID int64) (*model.GroupMember, error) {
	return r.GetGroupMember(ctx, groupID, userID)
}

func (r *fakeGroupRepository) LockGroupUsers(_ context.Context, firstID, secondID int64) (int, error) {
	if r.lockUsersErr != nil {
		return 0, r.lockUsersErr
	}
	if firstID == secondID {
		if r.users[firstID] {
			return 1, nil
		}
		return 0, nil
	}
	locked := 0
	if r.users[firstID] {
		locked++
	}
	if r.users[secondID] {
		locked++
	}
	return locked, nil
}

func (r *fakeGroupRepository) IsFriendPair(_ context.Context, firstID, secondID int64) (bool, error) {
	if r.friendshipErr != nil {
		return false, r.friendshipErr
	}
	return r.friendships[canonicalGroupUserPair(firstID, secondID)], nil
}

func (r *fakeGroupRepository) CountGroupMembers(_ context.Context, groupID int64) (int64, error) {
	if r.countMembersErr != nil {
		return 0, r.countMembersErr
	}
	var total int64
	for key := range r.members {
		if key.groupID == groupID {
			total++
		}
	}
	return total, nil
}

func (r *fakeGroupRepository) RemoveGroupMember(_ context.Context, groupID, userID int64) error {
	r.markMutation()
	if r.removeMemberErr != nil {
		return r.removeMemberErr
	}
	delete(r.members, groupMemberKey{groupID: groupID, userID: userID})
	return nil
}

func (r *fakeGroupRepository) ListGroupMembersPage(_ context.Context, groupID int64, limit, offset int) ([]repository.GroupMemberProfile, error) {
	if r.listMembersErr != nil {
		return nil, r.listMembersErr
	}
	r.lastMemberPage = struct {
		groupID       int64
		limit, offset int
	}{groupID: groupID, limit: limit, offset: offset}
	items := make([]repository.GroupMemberProfile, 0)
	for key, member := range r.members {
		if key.groupID != groupID {
			continue
		}
		items = append(items, repository.GroupMemberProfile{
			GroupMember: member, Username: r.usernames[member.UserID], AvatarURL: r.avatars[member.UserID],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Role != items[j].Role {
			return items[i].Role > items[j].Role
		}
		if !items[i].JoinedAt.Equal(items[j].JoinedAt) {
			return items[i].JoinedAt.Before(items[j].JoinedAt)
		}
		return items[i].ID < items[j].ID
	})
	if offset >= len(items) {
		return make([]repository.GroupMemberProfile, 0), nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return append([]repository.GroupMemberProfile(nil), items[offset:end]...), nil
}

func (r *fakeGroupRepository) ListGroupsByUser(_ context.Context, userID int64) ([]model.Group, error) {
	r.lastListUserID = userID
	groups := make([]model.Group, 0)
	for key := range r.members {
		if key.userID == userID {
			groups = append(groups, r.groups[key.groupID])
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID > groups[j].ID })
	return groups, nil
}

func (r *fakeGroupRepository) UpdateGroupProfile(_ context.Context, groupID int64, name, notice string) error {
	r.markMutation()
	group := r.groups[groupID]
	group.Name, group.Notice, group.UpdatedAt = name, notice, time.Now()
	r.groups[groupID] = group
	return nil
}

func (r *fakeGroupRepository) EnqueueCacheReconcile(_ context.Context, resource repository.CacheResource, resourceID int64) error {
	r.markMutation()
	if r.enqueueErr != nil {
		return r.enqueueErr
	}
	r.reconcileCalls = append(r.reconcileCalls, groupReconcileCall{resource, resourceID})
	return nil
}

func (r *fakeGroupRepository) seedGroup(group model.Group, members ...model.GroupMember) {
	r.groups[group.ID] = group
	for _, member := range members {
		r.seedGroupMember(member)
	}
}

func (r *fakeGroupRepository) seedGroupMember(member model.GroupMember) {
	r.nextMemberID++
	member.ID = r.nextMemberID
	if member.JoinedAt.IsZero() {
		member.JoinedAt = time.Unix(r.nextMemberID, 0).UTC()
	}
	r.members[groupMemberKey{groupID: member.GroupID, userID: member.UserID}] = member
}

func (r *fakeGroupRepository) seedFriendship(firstID, secondID int64) {
	r.friendships[canonicalGroupUserPair(firstID, secondID)] = true
}

func canonicalGroupUserPair(firstID, secondID int64) groupUserPair {
	if firstID > secondID {
		firstID, secondID = secondID, firstID
	}
	return groupUserPair{first: firstID, second: secondID}
}

func (r *fakeGroupRepository) markMutation() {
	if !r.inTransaction {
		r.mutationOutsideTransaction = true
	}
}

func cloneGroups(source map[int64]model.Group) map[int64]model.Group {
	result := make(map[int64]model.Group, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneGroupMembers(source map[groupMemberKey]model.GroupMember) map[groupMemberKey]model.GroupMember {
	result := make(map[groupMemberKey]model.GroupMember, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

type fakeGroupCache struct {
	groupIDs []int64
	err      error
}

func (c *fakeGroupCache) ReconcileGroupMembers(_ context.Context, groupID int64) error {
	c.groupIDs = append(c.groupIDs, groupID)
	return c.err
}

var _ repository.GroupRepository = (*fakeGroupRepository)(nil)
var _ GroupCacheRefresher = (*fakeGroupCache)(nil)
