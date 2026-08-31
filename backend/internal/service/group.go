package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"my-im/internal/apperror"
	"my-im/internal/model"
	"my-im/internal/repository"
)

const (
	maxGroupNameRunes   = 50
	maxGroupNoticeRunes = 300
)

type GroupServiceImpl struct {
	repository repository.GroupRepository
	cache      GroupCacheRefresher
}

// GroupCacheRefresher 是建群提交后的 Redis 快速刷新端口。
// MySQL 事务已经写入协调事件，因此这里失败不会把成功建群伪装成失败。
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
		_ = s.cache.ReconcileGroupMembers(context.WithoutCancel(ctx), group.ID)
	}
	return group.ID, nil
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

var _ GroupProfileService = (*GroupServiceImpl)(nil)
