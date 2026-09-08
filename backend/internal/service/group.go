package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"my-im/internal/apperror"
	"my-im/internal/model"
	"my-im/internal/protocol"
	"my-im/internal/repository"
)

const (
	maxGroupNameRunes      = 50
	maxGroupNoticeRunes    = 300
	defaultGroupMaxMembers = 500

	defaultGroupMemberPageSize = 20
	maxGroupMemberPageSize     = 100
)

type GroupServiceImpl struct {
	repository repository.GroupRepository
	cache      GroupCacheRefresher
	notifier   GroupEventNotifier
}

// GroupCacheRefresher 是群成员事务提交后的 Redis 快速刷新端口。
// MySQL 事务已经写入协调事件，因此这里失败不会把已提交的业务伪装成失败。
type GroupCacheRefresher interface {
	ReconcileGroupMembers(context.Context, int64) error
}

// GroupEventNotifier sends a best-effort online hint after durable state has
// committed. Offline/cross-instance delivery is intentionally not promised by
// this interface; clients always re-read the MySQL-backed HTTP truth.
type GroupEventNotifier interface {
	NotifyGroupEvent(context.Context, int64, string, any) error
}

type GroupServiceOption func(*GroupServiceImpl) error

func WithGroupCache(cache GroupCacheRefresher) GroupServiceOption {
	return func(groupService *GroupServiceImpl) error {
		if cache == nil {
			return errors.New("group cache refresher must not be nil")
		}
		groupService.cache = cache
		return nil
	}
}

func WithGroupEventNotifier(notifier GroupEventNotifier) GroupServiceOption {
	return func(groupService *GroupServiceImpl) error {
		if notifier == nil {
			return errors.New("group event notifier must not be nil")
		}
		groupService.notifier = notifier
		return nil
	}
}

func NewGroupService(groupRepository repository.GroupRepository, options ...GroupServiceOption) (*GroupServiceImpl, error) {
	if groupRepository == nil {
		return nil, errors.New("group repository must not be nil")
	}
	groupService := &GroupServiceImpl{repository: groupRepository}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(groupService); err != nil {
			return nil, err
		}
	}
	return groupService, nil
}

func (s *GroupServiceImpl) Create(ctx context.Context, ownerID int64, rawName, rawNotice string) (int64, error) {
	if ownerID <= 0 {
		return 0, apperror.New(apperror.CodeInvalidParam)
	}
	name, notice, err := normalizeGroupProfile(rawName, rawNotice)
	if err != nil {
		return 0, err
	}
	group := &model.Group{Name: name, Notice: notice, OwnerID: ownerID, MaxMembers: defaultGroupMaxMembers}
	err = s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		exists, err := tx.LockGroupCreator(txCtx, ownerID)
		if err != nil {
			return err
		}
		if !exists {
			return apperror.New(apperror.CodeUserNotFound)
		}
		groupID, err := tx.CreateGroup(txCtx, group)
		if err != nil {
			return err
		}
		group.ID = groupID
		owner := &model.GroupMember{GroupID: groupID, UserID: ownerID, Role: model.GroupRoleOwner}
		if err := tx.AddGroupMember(txCtx, owner); err != nil {
			return err
		}
		return tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceGroupMembers, groupID)
	})
	if err != nil {
		return 0, groupServiceError(err)
	}
	if s.cache != nil {
		s.refreshGroupMemberCache(ctx, group.ID)
	}
	return group.ID, nil
}

func (s *GroupServiceImpl) AddMember(ctx context.Context, groupID, operatorID, memberID int64) error {
	if groupID <= 0 || operatorID <= 0 || memberID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	err := s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		group, err := tx.GetGroupForUpdate(txCtx, groupID)
		if err != nil {
			return err
		}
		if group == nil {
			return apperror.New(apperror.CodeGroupNotFound)
		}
		operator, err := tx.GetGroupMemberForUpdate(txCtx, groupID, operatorID)
		if err != nil {
			return err
		}
		if !canManageGroupMembers(group, operator, operatorID) {
			return apperror.New(apperror.CodeNotOwnerOrAdmin)
		}
		existing, err := tx.GetGroupMemberForUpdate(txCtx, groupID, memberID)
		if err != nil {
			return err
		}
		if existing != nil {
			return apperror.New(apperror.CodeAlreadyMember)
		}
		lockedUsers, err := tx.LockGroupUsers(txCtx, operatorID, memberID)
		if err != nil {
			return err
		}
		if lockedUsers != 2 {
			return apperror.New(apperror.CodeUserNotFound)
		}
		friends, err := tx.IsFriendPair(txCtx, operatorID, memberID)
		if err != nil {
			return err
		}
		if !friends {
			return apperror.New(apperror.CodeMemberNotFriend)
		}
		memberCount, err := tx.CountGroupMembers(txCtx, groupID)
		if err != nil {
			return err
		}
		if memberCount >= int64(group.MaxMembers) {
			return apperror.New(apperror.CodeGroupFull)
		}
		if err := tx.AddGroupMember(txCtx, &model.GroupMember{
			GroupID: groupID, UserID: memberID, Role: model.GroupRoleMember,
		}); err != nil {
			return err
		}
		return tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceGroupMembers, groupID)
	})
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return apperror.New(apperror.CodeAlreadyMember)
		}
		return groupServiceError(err)
	}
	s.refreshGroupMemberCache(ctx, groupID)
	return nil
}

func (s *GroupServiceImpl) RemoveMember(ctx context.Context, groupID, operatorID, memberID int64) error {
	if groupID <= 0 || operatorID <= 0 || memberID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	err := s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		group, err := tx.GetGroupForUpdate(txCtx, groupID)
		if err != nil {
			return err
		}
		if group == nil {
			return apperror.New(apperror.CodeGroupNotFound)
		}
		operator, err := tx.GetGroupMemberForUpdate(txCtx, groupID, operatorID)
		if err != nil {
			return err
		}
		if !canManageGroupMembers(group, operator, operatorID) {
			return apperror.New(apperror.CodeNotOwnerOrAdmin)
		}
		target, err := tx.GetGroupMemberForUpdate(txCtx, groupID, memberID)
		if err != nil {
			return err
		}
		if target == nil {
			return apperror.New(apperror.CodeGroupMemberNotFound)
		}
		if target.UserID == group.OwnerID {
			return apperror.New(apperror.CodeCannotRemoveOwner)
		}
		if group.OwnerID != operatorID && target.Role != model.GroupRoleMember {
			return apperror.New(apperror.CodeCannotRemovePeer)
		}
		if err := tx.RemoveGroupMember(txCtx, groupID, memberID); err != nil {
			return err
		}
		return tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceGroupMembers, groupID)
	})
	if err != nil {
		return groupServiceError(err)
	}
	s.refreshGroupMemberCache(ctx, groupID)
	return nil
}

// UpdateRole promotes an ordinary member to administrator or demotes an
// administrator back to ordinary member. Only groups.owner_id is treated as
// the real owner; a stray role=2 row never grants this authority.
func (s *GroupServiceImpl) UpdateRole(ctx context.Context, groupID, operatorID, memberID int64, role int) error {
	if groupID <= 0 || operatorID <= 0 || memberID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	if role != model.GroupRoleMember && role != model.GroupRoleAdmin {
		return apperror.New(apperror.CodeInvalidRole)
	}

	changed := false
	err := s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		group, err := tx.GetGroupForUpdate(txCtx, groupID)
		if err != nil {
			return err
		}
		if group == nil {
			return apperror.New(apperror.CodeGroupNotFound)
		}
		operator, err := tx.GetGroupMemberForUpdate(txCtx, groupID, operatorID)
		if err != nil {
			return err
		}
		if operator == nil || group.OwnerID != operatorID {
			return apperror.New(apperror.CodeNotOwnerOrAdmin)
		}
		target, err := tx.GetGroupMemberForUpdate(txCtx, groupID, memberID)
		if err != nil {
			return err
		}
		if target == nil {
			return apperror.New(apperror.CodeGroupMemberNotFound)
		}
		if target.UserID == group.OwnerID {
			return apperror.New(apperror.CodeNotOwnerOrAdmin)
		}
		// PUT describes the desired final state. Repeating the same command is a
		// successful no-op and need not create another reconciliation event.
		if target.Role == role {
			return nil
		}
		if err := tx.UpdateGroupMemberRole(txCtx, groupID, memberID, role); err != nil {
			return err
		}
		if err := tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceGroupMembers, groupID); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return groupServiceError(err)
	}
	if changed {
		s.refreshGroupMemberCache(ctx, groupID)
	}
	return nil
}

// MuteMember sets a future mute deadline. Passing nil clears the deadline and
// therefore unmutes the member. Owners may manage administrators and members;
// administrators may manage ordinary members only.
func (s *GroupServiceImpl) MuteMember(ctx context.Context, groupID, operatorID, memberID int64, mutedUntil *time.Time) error {
	if groupID <= 0 || operatorID <= 0 || memberID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	deadline, err := normalizeGroupMuteDeadline(mutedUntil)
	if err != nil {
		return err
	}

	changed := false
	err = s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		group, err := tx.GetGroupForUpdate(txCtx, groupID)
		if err != nil {
			return err
		}
		if group == nil {
			return apperror.New(apperror.CodeGroupNotFound)
		}
		operator, err := tx.GetGroupMemberForUpdate(txCtx, groupID, operatorID)
		if err != nil {
			return err
		}
		if !canManageGroupMembers(group, operator, operatorID) {
			return apperror.New(apperror.CodeNotOwnerOrAdmin)
		}
		target, err := tx.GetGroupMemberForUpdate(txCtx, groupID, memberID)
		if err != nil {
			return err
		}
		if target == nil {
			return apperror.New(apperror.CodeGroupMemberNotFound)
		}
		if target.UserID == group.OwnerID ||
			(group.OwnerID != operatorID && target.Role != model.GroupRoleMember) {
			return apperror.New(apperror.CodeNotOwnerOrAdmin)
		}
		// The request may have waited for a row lock. Recheck here so a deadline
		// cannot already be expired by the time the transaction writes it.
		if deadline != nil && !deadline.After(time.Now().UTC()) {
			return apperror.WithMessage(apperror.CodeInvalidParam, "muted_until must be in the future")
		}
		// Mute and unmute are desired-state operations too, so an identical
		// deadline (or clearing an already empty deadline) skips the write/event.
		if sameOptionalGroupTime(target.MutedUntil, deadline) {
			return nil
		}
		if err := tx.UpdateGroupMemberMute(txCtx, groupID, memberID, deadline); err != nil {
			return err
		}
		if err := tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceGroupMembers, groupID); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return groupServiceError(err)
	}
	if changed {
		s.refreshGroupMemberCache(ctx, groupID)
	}
	return nil
}

func normalizeGroupMuteDeadline(deadline *time.Time) (*time.Time, error) {
	if deadline == nil {
		return nil, nil
	}
	// group_members.muted_until is a MySQL DATETIME without fractional seconds.
	// Normalize to the column's precision before both validation and comparison,
	// otherwise a browser's millisecond timestamp would never equal the stored
	// value on an idempotent retry.
	normalized := deadline.UTC().Truncate(time.Second)
	if normalized.Year() < 1000 || normalized.Year() > 9999 || !normalized.After(time.Now().UTC()) {
		return nil, apperror.WithMessage(apperror.CodeInvalidParam, "muted_until must be in the future")
	}
	return &normalized, nil
}

func sameOptionalGroupTime(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Equal(*second)
}

// TransferOwnership atomically moves the two representations of ownership:
// groups.owner_id and the old/new group_members.role rows. The new owner is
// also unmuted so ownership can never be transferred into an unusable state.
func (s *GroupServiceImpl) TransferOwnership(ctx context.Context, groupID, operatorID, newOwnerID int64) error {
	if groupID <= 0 || operatorID <= 0 || newOwnerID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	if operatorID == newOwnerID {
		return apperror.WithMessage(apperror.CodeInvalidParam, "new owner must differ from current owner")
	}

	err := s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		// Every group-member mutation uses this order: group first, members next.
		// The group lock serializes transfer against add/remove/role/mute/leave.
		group, err := tx.GetGroupForUpdate(txCtx, groupID)
		if err != nil {
			return err
		}
		if group == nil {
			return apperror.New(apperror.CodeGroupNotFound)
		}

		oldOwner, err := tx.GetGroupMemberForUpdate(txCtx, groupID, operatorID)
		if err != nil {
			return err
		}
		// A stray role=2 row never grants transfer authority. Conversely, a
		// stale role on the real owner does not revoke authority from owner_id.
		if oldOwner == nil || group.OwnerID != operatorID {
			return apperror.New(apperror.CodeNotOwnerOrAdmin)
		}

		newOwner, err := tx.GetGroupMemberForUpdate(txCtx, groupID, newOwnerID)
		if err != nil {
			return err
		}
		if newOwner == nil {
			return apperror.New(apperror.CodeGroupMemberNotFound)
		}

		if err := tx.UpdateGroupMemberRole(txCtx, groupID, operatorID, model.GroupRoleMember); err != nil {
			return err
		}
		if err := tx.UpdateGroupMemberRole(txCtx, groupID, newOwnerID, model.GroupRoleOwner); err != nil {
			return err
		}
		if newOwner.MutedUntil != nil {
			if err := tx.UpdateGroupMemberMute(txCtx, groupID, newOwnerID, nil); err != nil {
				return err
			}
		}
		if err := tx.UpdateGroupOwner(txCtx, groupID, newOwnerID); err != nil {
			return err
		}
		return tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceGroupMembers, groupID)
	})
	if err != nil {
		return groupServiceError(err)
	}

	// Redis and WebSocket are projections/hints. Their failures never turn a
	// committed ownership transfer into an HTTP failure; the durable reconcile
	// event and later HTTP reads recover from either failure.
	postCommitCtx := context.WithoutCancel(ctx)
	s.refreshGroupMemberCache(postCommitCtx, groupID)
	s.notifyCurrentGroupMembers(postCommitCtx, groupID, protocol.TypeGroupUpdated, model.GroupUpdatedNotification{
		GroupID: groupID,
		Reason:  model.GroupUpdatedReasonOwnerTransferred,
	}, operatorID, newOwnerID)
	return nil
}

// Leave removes the authenticated user's own membership. A real owner must
// transfer ownership first; role=2 on any other row does not block self-leave.
func (s *GroupServiceImpl) Leave(ctx context.Context, groupID, userID int64) error {
	if groupID <= 0 || userID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}

	err := s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		group, err := tx.GetGroupForUpdate(txCtx, groupID)
		if err != nil {
			return err
		}
		if group == nil {
			return apperror.New(apperror.CodeGroupNotFound)
		}
		member, err := tx.GetGroupMemberForUpdate(txCtx, groupID, userID)
		if err != nil {
			return err
		}
		if member == nil {
			return apperror.New(apperror.CodeGroupNotMember)
		}
		if group.OwnerID == userID {
			return apperror.New(apperror.CodeCannotLeaveAsOwner)
		}
		if err := tx.RemoveGroupMember(txCtx, groupID, userID); err != nil {
			return err
		}
		return tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceGroupMembers, groupID)
	})
	if err != nil {
		return groupServiceError(err)
	}

	postCommitCtx := context.WithoutCancel(ctx)
	s.refreshGroupMemberCache(postCommitCtx, groupID)
	s.notifyGroupUser(postCommitCtx, userID, protocol.TypeGroupRemoved, model.GroupRemovedNotification{
		GroupID: groupID,
		Reason:  model.GroupRemovedReasonLeft,
	})
	s.notifyCurrentGroupMembers(postCommitCtx, groupID, protocol.TypeGroupUpdated, model.GroupUpdatedNotification{
		GroupID: groupID,
		Reason:  model.GroupUpdatedReasonMemberLeft,
	})
	return nil
}

// Disband permanently removes the group's live metadata and memberships while
// deliberately retaining group_messages as history. It is a desired-state
// DELETE: retrying after a timeout succeeds even when the first request already
// committed. Every live group mutation locks the same groups row first, so an
// invitation/transfer cannot commit halfway through disbanding.
func (s *GroupServiceImpl) Disband(ctx context.Context, groupID, operatorID int64) error {
	if groupID <= 0 || operatorID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}

	recipients := make([]int64, 0)
	cleanupRequired := false
	err := s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		group, err := tx.GetGroupForUpdate(txCtx, groupID)
		if err != nil {
			return err
		}
		if group == nil {
			// Both a retry and a never-existing positive ID satisfy DELETE's HTTP
			// desired state, but only a durable tombstone proves that Redis cleanup
			// belongs to a real old group. This prevents random IDs from forcing
			// reverse-key scans and permanent negative-cache keys.
			tombstoned, err := tx.IsGroupTombstoned(txCtx, groupID)
			if err != nil {
				return err
			}
			cleanupRequired = tombstoned
			return nil
		}
		// groups.owner_id is authoritative. A stale/missing owner member row
		// must not make an anomalous group impossible to clean up.
		if group.OwnerID != operatorID {
			return apperror.New(apperror.CodeNotOwnerOrAdmin)
		}
		members, err := tx.GetGroupMembers(txCtx, groupID)
		if err != nil {
			return err
		}
		recipients = make([]int64, 0, len(members))
		for _, member := range members {
			if member.UserID > 0 {
				recipients = append(recipients, member.UserID)
			}
		}
		// Keep the authenticated owner as a fallback recipient when recovering
		// an anomalous group whose owner membership row is already missing.
		recipients = append(recipients, operatorID)
		if err := tx.UpsertGroupTombstone(txCtx, groupID, group.OwnerID); err != nil {
			return err
		}
		if err := tx.DeleteGroupMembers(txCtx, groupID); err != nil {
			return err
		}
		if err := tx.DeleteGroup(txCtx, groupID); err != nil {
			return err
		}
		// The owner row may now be absent. Cache reconciliation explicitly
		// supports a deleted owner and replaces the projection with empty state.
		if err := tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceGroupMembers, groupID); err != nil {
			return err
		}
		cleanupRequired = true
		return nil
	})
	if err != nil {
		return groupServiceError(err)
	}

	if !cleanupRequired {
		return nil
	}
	postCommitCtx := context.WithoutCancel(ctx)
	// A tombstoned retry makes another best-effort cleanup attempt. A random ID
	// returns above without touching Redis or creating a durable event.
	s.refreshGroupMemberCache(postCommitCtx, groupID)
	seen := make(map[int64]struct{}, len(recipients))
	for _, userID := range recipients {
		if _, duplicate := seen[userID]; duplicate {
			continue
		}
		seen[userID] = struct{}{}
		s.notifyGroupUser(postCommitCtx, userID, protocol.TypeGroupRemoved, model.GroupRemovedNotification{
			GroupID: groupID,
			Reason:  model.GroupRemovedReasonDissolved,
		})
	}
	return nil
}

func (s *GroupServiceImpl) notifyCurrentGroupMembers(
	ctx context.Context,
	groupID int64,
	eventType string,
	payload any,
	fallbackUserIDs ...int64,
) {
	if s.notifier == nil {
		return
	}
	members, err := s.repository.GetGroupMembers(ctx, groupID)
	userIDs := fallbackUserIDs
	if err == nil {
		userIDs = make([]int64, 0, len(members))
		for _, member := range members {
			userIDs = append(userIDs, member.UserID)
		}
	}
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		s.notifyGroupUser(ctx, userID, eventType, payload)
	}
}

func (s *GroupServiceImpl) notifyGroupUser(ctx context.Context, userID int64, eventType string, payload any) {
	if s.notifier != nil {
		_ = s.notifier.NotifyGroupEvent(ctx, userID, eventType, payload)
	}
}

func (s *GroupServiceImpl) ListMembers(ctx context.Context, groupID, viewerID int64, limit, offset int) (Page[GroupMemberListItem], error) {
	if groupID <= 0 || viewerID <= 0 {
		return Page[GroupMemberListItem]{}, apperror.New(apperror.CodeInvalidParam)
	}
	limit, offset, err := normalizeGroupMemberPage(limit, offset)
	if err != nil {
		return Page[GroupMemberListItem]{}, err
	}
	page := Page[GroupMemberListItem]{Limit: limit, Offset: offset, Items: make([]GroupMemberListItem, 0)}
	err = s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		group, err := tx.GetGroupForUpdate(txCtx, groupID)
		if err != nil {
			return err
		}
		if group == nil {
			return apperror.New(apperror.CodeGroupNotFound)
		}
		viewer, err := tx.GetGroupMemberForUpdate(txCtx, groupID, viewerID)
		if err != nil {
			return err
		}
		if viewer == nil {
			return apperror.New(apperror.CodeGroupNotMember)
		}
		rows, err := tx.ListGroupMembersPage(txCtx, groupID, limit, offset)
		if err != nil {
			return err
		}
		page.Items = make([]GroupMemberListItem, 0, len(rows))
		for _, row := range rows {
			page.Items = append(page.Items, GroupMemberListItem{
				GroupMember: row.GroupMember, Username: row.Username, AvatarURL: row.AvatarURL,
			})
		}
		page.Total, err = tx.CountGroupMembers(txCtx, groupID)
		return err
	})
	if err != nil {
		return Page[GroupMemberListItem]{}, groupServiceError(err)
	}
	return page, nil
}

func canManageGroupMembers(group *model.Group, operator *model.GroupMember, operatorID int64) bool {
	return group != nil && operator != nil &&
		(group.OwnerID == operatorID || operator.Role == model.GroupRoleAdmin)
}

func normalizeGroupMemberPage(limit, offset int) (int, int, error) {
	if offset < 0 {
		return 0, 0, apperror.WithMessage(apperror.CodeInvalidParam, "offset must not be negative")
	}
	if limit <= 0 {
		limit = defaultGroupMemberPageSize
	}
	if limit > maxGroupMemberPageSize {
		return 0, 0, apperror.WithMessage(apperror.CodeInvalidParam, "limit must not exceed 100")
	}
	return limit, offset, nil
}

func (s *GroupServiceImpl) refreshGroupMemberCache(ctx context.Context, groupID int64) {
	if s.cache != nil {
		_ = s.cache.ReconcileGroupMembers(context.WithoutCancel(ctx), groupID)
	}
}

func (s *GroupServiceImpl) ListByUser(ctx context.Context, userID int64) ([]model.Group, error) {
	if userID <= 0 {
		return nil, apperror.New(apperror.CodeInvalidParam)
	}
	groups, err := s.repository.ListGroupsByUser(ctx, userID)
	if err != nil {
		return nil, groupServiceError(err)
	}
	if groups == nil {
		groups = make([]model.Group, 0)
	}
	return groups, nil
}

func (s *GroupServiceImpl) Get(ctx context.Context, userID, groupID int64) (*model.Group, error) {
	if userID <= 0 || groupID <= 0 {
		return nil, apperror.New(apperror.CodeInvalidParam)
	}
	group, err := s.repository.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, groupServiceError(err)
	}
	if group == nil {
		return nil, apperror.New(apperror.CodeGroupNotFound)
	}
	member, err := s.repository.GetGroupMember(ctx, groupID, userID)
	if err != nil {
		return nil, groupServiceError(err)
	}
	if member == nil {
		return nil, apperror.New(apperror.CodeGroupNotMember)
	}
	return group, nil
}

func (s *GroupServiceImpl) Update(ctx context.Context, operatorID, groupID int64, rawName, rawNotice string) error {
	if operatorID <= 0 || groupID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	name, notice, err := normalizeGroupProfile(rawName, rawNotice)
	if err != nil {
		return err
	}
	err = s.repository.WithinGroupTransaction(ctx, func(txCtx context.Context, tx repository.GroupRepository) error {
		group, err := tx.GetGroupForUpdate(txCtx, groupID)
		if err != nil {
			return err
		}
		if group == nil {
			return apperror.New(apperror.CodeGroupNotFound)
		}
		member, err := tx.GetGroupMemberForUpdate(txCtx, groupID, operatorID)
		if err != nil {
			return err
		}
		if member == nil || (group.OwnerID != operatorID && member.Role != model.GroupRoleAdmin) {
			return apperror.New(apperror.CodeNotOwnerOrAdmin)
		}
		return tx.UpdateGroupProfile(txCtx, groupID, name, notice)
	})
	return groupServiceError(err)
}

func normalizeGroupProfile(rawName, rawNotice string) (string, string, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "", "", apperror.WithMessage(apperror.CodeInvalidParam, "group name must not be empty")
	}
	if utf8.RuneCountInString(name) > maxGroupNameRunes {
		return "", "", apperror.WithMessage(apperror.CodeInvalidParam, "group name must not exceed 50 characters")
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return "", "", apperror.WithMessage(apperror.CodeInvalidParam, "group name must not contain control characters")
		}
	}

	notice := strings.TrimSpace(rawNotice)
	if utf8.RuneCountInString(notice) > maxGroupNoticeRunes {
		return "", "", apperror.WithMessage(apperror.CodeInvalidParam, "group notice must not exceed 300 characters")
	}
	for _, character := range notice {
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			return "", "", apperror.WithMessage(apperror.CodeInvalidParam, "group notice contains an unsupported control character")
		}
	}
	return name, notice, nil
}

func groupServiceError(err error) error {
	if err == nil {
		return nil
	}
	var applicationError *apperror.Error
	if errors.As(err, &applicationError) {
		return applicationError
	}
	return apperror.Wrap(apperror.CodeInternalFailure, fmt.Errorf("group service: %w", err))
}

var _ GroupCoreService = (*GroupServiceImpl)(nil)
