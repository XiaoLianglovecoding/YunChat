package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

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
	LockGroupUsers(context.Context, int64, int64) (int, error)
	IsFriendPair(context.Context, int64, int64) (bool, error)
	CountGroupMembers(context.Context, int64) (int64, error)
	GetGroupMembers(context.Context, int64) ([]model.GroupMember, error)
	RemoveGroupMember(context.Context, int64, int64) error
	UpdateGroupOwner(context.Context, int64, int64) error
	UpdateGroupMemberRole(context.Context, int64, int64, int) error
	UpdateGroupMemberMute(context.Context, int64, int64, *time.Time) error
	ListGroupMembersPage(context.Context, int64, int, int) ([]GroupMemberProfile, error)
	ListGroupsByUser(context.Context, int64) ([]model.Group, error)
	UpdateGroupProfile(context.Context, int64, string, string) error
}

// UpdateGroupOwner 只更新群主外键，避免复用会连带覆盖群名和公告的旧通用 UpdateGroup。
// 调用方必须把它和新旧群主的成员角色更新放在同一个群事务中。
func (m *MySQLRepoImpl) UpdateGroupOwner(ctx context.Context, groupID, ownerID int64) error {
	if _, err := m.db.ExecContext(ctx,
		"UPDATE `groups` SET owner_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		ownerID, groupID,
	); err != nil {
		return fmt.Errorf("update group owner: %w", err)
	}
	return nil
}

func (m *MySQLRepoImpl) UpdateGroupMemberMute(ctx context.Context, groupID, userID int64, mutedUntil *time.Time) error {
	if _, err := m.db.ExecContext(ctx,
		`UPDATE group_members SET muted_until = ? WHERE group_id = ? AND user_id = ?`,
		mutedUntil, groupID, userID,
	); err != nil {
		return fmt.Errorf("update group member mute deadline: %w", err)
	}
	return nil
}

// GroupMemberProfile 是 MySQL JOIN 得到的成员与公开用户资料投影。
type GroupMemberProfile struct {
	model.GroupMember
	Username  string
	AvatarURL string
}

// WithinGroupTransaction 保证群资料、成员变化与缓存协调事件一起提交或回滚。
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

// LockGroupUsers 按用户 ID 升序锁行，使好友删除与群邀请并发时看到确定结果。
func (m *MySQLRepoImpl) LockGroupUsers(ctx context.Context, firstID, secondID int64) (int, error) {
	ids := []int64{firstID, secondID}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if ids[0] == ids[1] {
		ids = ids[:1]
	}
	locked := 0
	for _, userID := range ids {
		var foundID int64
		err := m.db.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, userID).Scan(&foundID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("lock group operation user %d: %w", userID, err)
		}
		locked++
	}
	return locked, nil
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

func (m *MySQLRepoImpl) CountGroupMembers(ctx context.Context, groupID int64) (int64, error) {
	var total int64
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM group_members WHERE group_id = ?`, groupID,
	).Scan(&total); err != nil {
		return 0, fmt.Errorf("count group members: %w", err)
	}
	return total, nil
}

func (m *MySQLRepoImpl) ListGroupMembersPage(ctx context.Context, groupID int64, limit, offset int) ([]GroupMemberProfile, error) {
	const query = `SELECT gm.id, gm.group_id, gm.user_id, gm.role, gm.muted_until, gm.joined_at,
		u.username, COALESCE(u.avatar_url, '')
		FROM group_members gm
		JOIN users u ON u.id = gm.user_id
		WHERE gm.group_id = ?
		ORDER BY gm.role DESC, gm.joined_at ASC, gm.id ASC
		LIMIT ? OFFSET ?`
	rows, err := m.db.QueryContext(ctx, query, groupID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list group members page: %w", err)
	}
	defer rows.Close()

	members := make([]GroupMemberProfile, 0)
	for rows.Next() {
		var member GroupMemberProfile
		if err := rows.Scan(&member.ID, &member.GroupID, &member.UserID, &member.Role,
			&member.MutedUntil, &member.JoinedAt, &member.Username, &member.AvatarURL); err != nil {
			return nil, fmt.Errorf("scan group member profile: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group member profiles: %w", err)
	}
	return members, nil
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
