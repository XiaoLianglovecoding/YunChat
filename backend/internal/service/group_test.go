package service

import (
	"context"
	"errors"
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
	groups                     map[int64]model.Group
	members                    map[groupMemberKey]model.GroupMember
	reconcileCalls             []groupReconcileCall
	nextGroupID                int64
	nextMemberID               int64
	inTransaction              bool
	mutationOutsideTransaction bool
	addMemberErr               error
	enqueueErr                 error
	lastListUserID             int64
}

func newFakeGroupRepository(userIDs ...int64) *fakeGroupRepository {
	repo := &fakeGroupRepository{
		users: make(map[int64]bool), groups: make(map[int64]model.Group),
		members: make(map[groupMemberKey]model.GroupMember), nextGroupID: 100, nextMemberID: 1000,
	}
	for _, userID := range userIDs {
		repo.users[userID] = true
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
	r.nextMemberID++
	member.ID = r.nextMemberID
	member.JoinedAt = time.Now()
	r.members[groupMemberKey{groupID: member.GroupID, userID: member.UserID}] = *member
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
		r.nextMemberID++
		member.ID = r.nextMemberID
		r.members[groupMemberKey{groupID: member.GroupID, userID: member.UserID}] = member
	}
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
