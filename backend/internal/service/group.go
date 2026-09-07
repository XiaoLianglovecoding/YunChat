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
	"my-im/internal/repository"
)

const (
	maxGroupNameRunes   = 50
	maxGroupNoticeRunes = 300

	defaultGroupMemberPageSize = 20
	maxGroupMemberPageSize     = 100
)

type GroupServiceImpl struct {
	repository repository.GroupRepository
	cache      GroupCacheRefresher
}

// GroupCacheRefresher 是群成员事务提交后的 Redis 快速刷新端口。
// MySQL 事务已经写入协调事件，因此这里失败不会把已提交的业务伪装成失败。
type GroupCacheRefresher interface {
	ReconcileGroupMembers(context.Context, int64) error
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
	group := &model.Group{Name: name, Notice: notice, OwnerID: ownerID, MaxMembers: 500}
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
