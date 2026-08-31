package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"my-im/internal/model"
)

// GroupRepository 是群业务所需的最小 MySQL 端口。
//
// Service 只依赖这组方法，因此单元测试不需要伪造整个 MySQLRepository。
// WithinGroupTransaction 回调收到的是绑定到同一个 sql.Tx 的实现。
type GroupRepository interface {
	CacheEventWriter
	WithinGroupTransaction(context.Context, func(context.Context, GroupRepository) error) error
	LockGroupCreator(context.Context, int64) (bool, error)
	CreateGroup(context.Context, *model.Group) (int64, error)
	AddGroupMember(context.Context, *model.GroupMember) error
	GetGroupByID(context.Context, int64) (*model.Group, error)
	GetGroupForUpdate(context.Context, int64) (*model.Group, error)
	GetGroupMember(context.Context, int64, int64) (*model.GroupMember, error)
	GetGroupMemberForUpdate(context.Context, int64, int64) (*model.GroupMember, error)
	ListGroupsByUser(context.Context, int64) ([]model.Group, error)
	UpdateGroupProfile(context.Context, int64, string, string) error
}

// WithinGroupTransaction 保证建群时群资料和群主成员要么一起成功，要么一起回滚。
func (m *MySQLRepoImpl) WithinGroupTransaction(ctx context.Context, fn func(context.Context, GroupRepository) error) error {
	if m.root == nil {
		return errors.New("group transaction requires a root database handle")
	}
	if fn == nil {
		return errors.New("group transaction callback must not be nil")
	}
	tx, err := m.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group transaction: %w", err)
	}
	child := &MySQLRepoImpl{
		db:           newTimedRunner(tx, m.queryTimeout, m.observer),
		root:         m.root,
		queryTimeout: m.queryTimeout,
		observer:     m.observer,
		tx:           tx,
	}
	if err := fn(ctx, child); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback group transaction: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit group transaction: %w", err)
	}
	return nil
}

// LockGroupCreator 锁住创建者用户行，并验证 access token 指向的用户仍然存在。
func (m *MySQLRepoImpl) LockGroupCreator(ctx context.Context, userID int64) (bool, error) {
	var lockedID int64
	err := m.db.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, userID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock group creator: %w", err)
	}
	return true, nil
}

func (m *MySQLRepoImpl) GetGroupForUpdate(ctx context.Context, groupID int64) (*model.Group, error) {
	const query = "SELECT id, name, COALESCE(notice, ''), owner_id, max_members, created_at, updated_at " +
		"FROM `groups` WHERE id = ? FOR UPDATE"
	return m.scanGroup(m.db.QueryRowContext(ctx, query, groupID))
}

func (m *MySQLRepoImpl) GetGroupMember(ctx context.Context, groupID, userID int64) (*model.GroupMember, error) {
	return scanGroupMember(m.db.QueryRowContext(ctx, `SELECT id, group_id, user_id, role, muted_until, joined_at
		FROM group_members WHERE group_id = ? AND user_id = ?`, groupID, userID))
}

func (m *MySQLRepoImpl) GetGroupMemberForUpdate(ctx context.Context, groupID, userID int64) (*model.GroupMember, error) {
	return scanGroupMember(m.db.QueryRowContext(ctx, `SELECT id, group_id, user_id, role, muted_until, joined_at
		FROM group_members WHERE group_id = ? AND user_id = ? FOR UPDATE`, groupID, userID))
}

func scanGroupMember(row *sql.Row) (*model.GroupMember, error) {
	var member model.GroupMember
	err := row.Scan(&member.ID, &member.GroupID, &member.UserID, &member.Role, &member.MutedUntil, &member.JoinedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan group member: %w", err)
	}
	return &member, nil
}

func (m *MySQLRepoImpl) ListGroupsByUser(ctx context.Context, userID int64) ([]model.Group, error) {
	const query = "SELECT g.id, g.name, COALESCE(g.notice, ''), g.owner_id, " +
		"g.max_members, g.created_at, g.updated_at " +
		"FROM group_members gm JOIN `groups` g ON g.id = gm.group_id " +
		"WHERE gm.user_id = ? ORDER BY gm.joined_at DESC, g.id DESC"
	rows, err := m.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list groups by user: %w", err)
	}
	defer rows.Close()

	groups := make([]model.Group, 0)
	for rows.Next() {
		var group model.Group
		if err := rows.Scan(&group.ID, &group.Name, &group.Notice, &group.OwnerID,
			&group.MaxMembers, &group.CreatedAt, &group.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user groups: %w", err)
	}
	return groups, nil
}

func (m *MySQLRepoImpl) UpdateGroupProfile(ctx context.Context, groupID int64, name, notice string) error {
	_, err := m.db.ExecContext(ctx,
		"UPDATE `groups` SET name = ?, notice = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		name, notice, groupID)
	if err != nil {
		return fmt.Errorf("update group profile: %w", err)
	}
	return nil
}

func (m *MySQLRepoImpl) scanGroup(row *sql.Row) (*model.Group, error) {
	var group model.Group
	err := row.Scan(&group.ID, &group.Name, &group.Notice, &group.OwnerID,
		&group.MaxMembers, &group.CreatedAt, &group.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan group: %w", err)
	}
	return &group, nil
}

var _ GroupRepository = (*MySQLRepoImpl)(nil)
