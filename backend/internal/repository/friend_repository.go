package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"my-im/internal/model"
)

// FriendRepository is the smallest database port needed by the friend use cases.
// The callback passed to WithinFriendTransaction receives a transaction-bound
// implementation of this same narrow interface, which keeps service tests small.
type FriendRepository interface {
	CacheEventWriter
	WithinFriendTransaction(context.Context, func(context.Context, FriendRepository) error) error

	// LockFriendUsers locks both user rows in ascending ID order. The returned
	// count is normally two; a smaller count means at least one user is missing.
	LockFriendUsers(context.Context, int64, int64) (int, error)
	GetFriendUserProfile(context.Context, int64) (*model.User, error)
	IsFriendPair(context.Context, int64, int64) (bool, error)
	IsEitherBlocked(context.Context, int64, int64) (bool, error)
	FindPendingFriendRequest(context.Context, int64, int64) (*model.FriendRequest, error)
	SaveFriendRequest(context.Context, *model.FriendRequest) error
	GetFriendRequest(context.Context, int64) (*model.FriendRequest, error)
	GetFriendRequestForUpdate(context.Context, int64) (*model.FriendRequest, error)
	SetFriendRequestStatus(context.Context, int64, int) error
	ListIncomingFriendRequests(context.Context, int64, int, int) ([]model.FriendRequest, error)
	CountIncomingFriendRequests(context.Context, int64) (int64, error)

	EnsureFriendshipPair(context.Context, int64, int64) error
	DeleteFriendshipPair(context.Context, int64, int64) error
	InvalidateAcceptedFriendRequests(context.Context, int64, int64) error
	ListFriendshipsPage(context.Context, int64, int, int) ([]model.Friendship, error)
	CountFriendships(context.Context, int64) (int64, error)

	AddBlacklistEntry(context.Context, *model.Blacklist) error
	RejectPendingFriendRequests(context.Context, int64, int64) error
	RemoveBlacklistEntry(context.Context, int64, int64) error
}

// WithinFriendTransaction binds all repository calls in fn to one sql.Tx.
func (m *MySQLRepoImpl) WithinFriendTransaction(ctx context.Context, fn func(context.Context, FriendRepository) error) error {
	if m.root == nil {
		return errors.New("friend transaction requires a root database handle")
	}
	tx, err := m.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin friend transaction: %w", err)
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
			return errors.Join(err, fmt.Errorf("rollback friend transaction: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit friend transaction: %w", err)
	}
	return nil
}

func (m *MySQLRepoImpl) LockFriendUsers(ctx context.Context, firstID, secondID int64) (int, error) {
	ids := []int64{firstID, secondID}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if ids[0] == ids[1] {
		ids = ids[:1]
	}
	locked := 0
	for _, id := range ids {
		var found int64
		err := m.db.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("lock friend user %d: %w", id, err)
		}
		locked++
	}
	return locked, nil
}

func (m *MySQLRepoImpl) GetFriendUserProfile(ctx context.Context, userID int64) (*model.User, error) {
	const query = `SELECT id, username, nickname, avatar_url FROM users WHERE id = ?`
	var user model.User
	err := m.db.QueryRowContext(ctx, query, userID).Scan(&user.ID, &user.Username, &user.Nickname, &user.AvatarURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get friend user profile: %w", err)
	}
	return &user, nil
}

func (m *MySQLRepoImpl) IsFriendPair(ctx context.Context, userID, peerID int64) (bool, error) {
	const query = `SELECT EXISTS(
		SELECT 1 FROM friendships
		WHERE (user_id = ? AND friend_id = ?) OR (user_id = ? AND friend_id = ?)
	)`
	var exists bool
	if err := m.db.QueryRowContext(ctx, query, userID, peerID, peerID, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check friendship pair: %w", err)
	}
	return exists, nil
}

func (m *MySQLRepoImpl) IsEitherBlocked(ctx context.Context, userID, peerID int64) (bool, error) {
	const query = `SELECT EXISTS(
		SELECT 1 FROM blacklist
		WHERE (user_id = ? AND blocked_id = ?) OR (user_id = ? AND blocked_id = ?)
	)`
	var exists bool
	if err := m.db.QueryRowContext(ctx, query, userID, peerID, peerID, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check blacklist pair: %w", err)
	}
	return exists, nil
}

func (m *MySQLRepoImpl) FindPendingFriendRequest(ctx context.Context, userID, peerID int64) (*model.FriendRequest, error) {
	const query = `SELECT id, from_user_id, to_user_id, COALESCE(message, ''), status, created_at, updated_at
		FROM friend_requests
		WHERE status = ? AND ((from_user_id = ? AND to_user_id = ?) OR (from_user_id = ? AND to_user_id = ?))
		ORDER BY created_at DESC, id DESC LIMIT 1`
	return scanFriendRequest(m.db.QueryRowContext(ctx, query,
		model.FriendRequestPending, userID, peerID, peerID, userID))
}

func (m *MySQLRepoImpl) SaveFriendRequest(ctx context.Context, request *model.FriendRequest) error {
	// A new application gets a new ID. Reusing a terminal row's ID would let a
	// delayed accept command from the previous application act on the new one.
	if _, err := m.db.ExecContext(ctx, `DELETE FROM friend_requests
		WHERE from_user_id = ? AND to_user_id = ? AND status <> ?`,
		request.FromUserID, request.ToUserID, model.FriendRequestPending); err != nil {
		return fmt.Errorf("remove terminal friend request: %w", err)
	}
	const query = `INSERT INTO friend_requests
		(from_user_id, to_user_id, message, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	result, err := m.db.ExecContext(ctx, query, request.FromUserID, request.ToUserID,
		request.Message, request.Status, request.CreatedAt, request.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save friend request: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read friend request insert id: %w", err)
	}
	request.ID = id
	return nil
}

func (m *MySQLRepoImpl) GetFriendRequest(ctx context.Context, requestID int64) (*model.FriendRequest, error) {
	const query = `SELECT id, from_user_id, to_user_id, COALESCE(message, ''), status, created_at, updated_at
		FROM friend_requests WHERE id = ?`
	return scanFriendRequest(m.db.QueryRowContext(ctx, query, requestID))
}

func (m *MySQLRepoImpl) GetFriendRequestForUpdate(ctx context.Context, requestID int64) (*model.FriendRequest, error) {
	const query = `SELECT id, from_user_id, to_user_id, COALESCE(message, ''), status, created_at, updated_at
		FROM friend_requests WHERE id = ? FOR UPDATE`
	return scanFriendRequest(m.db.QueryRowContext(ctx, query, requestID))
}

func scanFriendRequest(row *sql.Row) (*model.FriendRequest, error) {
	var request model.FriendRequest
	err := row.Scan(&request.ID, &request.FromUserID, &request.ToUserID, &request.Message,
		&request.Status, &request.CreatedAt, &request.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan friend request: %w", err)
	}
	return &request, nil
}

func (m *MySQLRepoImpl) SetFriendRequestStatus(ctx context.Context, requestID int64, status int) error {
	result, err := m.db.ExecContext(ctx,
		`UPDATE friend_requests SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, requestID)
	if err != nil {
		return fmt.Errorf("set friend request status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read friend request affected rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *MySQLRepoImpl) ListIncomingFriendRequests(ctx context.Context, userID int64, limit, offset int) ([]model.FriendRequest, error) {
	const query = `SELECT r.id, r.from_user_id, r.to_user_id, u.username, u.avatar_url,
		COALESCE(r.message, ''), r.status, r.created_at, r.updated_at
		FROM friend_requests r
		JOIN users u ON u.id = r.from_user_id
		WHERE r.to_user_id = ? AND r.status = ?
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT ? OFFSET ?`
	rows, err := m.db.QueryContext(ctx, query, userID, model.FriendRequestPending, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list incoming friend requests: %w", err)
	}
	defer rows.Close()
	requests := make([]model.FriendRequest, 0)
	for rows.Next() {
		var request model.FriendRequest
		if err := rows.Scan(&request.ID, &request.FromUserID, &request.ToUserID,
			&request.Username, &request.AvatarURL, &request.Message, &request.Status,
			&request.CreatedAt, &request.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan incoming friend request: %w", err)
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incoming friend requests: %w", err)
	}
	return requests, nil
}

func (m *MySQLRepoImpl) CountIncomingFriendRequests(ctx context.Context, userID int64) (int64, error) {
	var total int64
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM friend_requests WHERE to_user_id = ? AND status = ?`,
		userID, model.FriendRequestPending).Scan(&total); err != nil {
		return 0, fmt.Errorf("count incoming friend requests: %w", err)
	}
	return total, nil
}

func (m *MySQLRepoImpl) EnsureFriendshipPair(ctx context.Context, userID, friendID int64) error {
	const query = `INSERT IGNORE INTO friendships (user_id, friend_id) VALUES (?, ?), (?, ?)`
	if _, err := m.db.ExecContext(ctx, query, userID, friendID, friendID, userID); err != nil {
		return fmt.Errorf("ensure friendship pair: %w", err)
	}
	return nil
}

func (m *MySQLRepoImpl) DeleteFriendshipPair(ctx context.Context, userID, friendID int64) error {
	const query = `DELETE FROM friendships
		WHERE (user_id = ? AND friend_id = ?) OR (user_id = ? AND friend_id = ?)`
	if _, err := m.db.ExecContext(ctx, query, userID, friendID, friendID, userID); err != nil {
		return fmt.Errorf("delete friendship pair: %w", err)
	}
	return nil
}

// InvalidateAcceptedFriendRequests prevents a delayed retry of an old accept
// command from recreating a friendship that was explicitly deleted later.
func (m *MySQLRepoImpl) InvalidateAcceptedFriendRequests(ctx context.Context, userID, friendID int64) error {
	const query = `UPDATE friend_requests SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE status = ? AND ((from_user_id = ? AND to_user_id = ?) OR (from_user_id = ? AND to_user_id = ?))`
	if _, err := m.db.ExecContext(ctx, query,
		model.FriendRequestRejected, model.FriendRequestAccepted,
		userID, friendID, friendID, userID); err != nil {
		return fmt.Errorf("invalidate accepted friend requests: %w", err)
	}
	return nil
}

func (m *MySQLRepoImpl) ListFriendshipsPage(ctx context.Context, userID int64, limit, offset int) ([]model.Friendship, error) {
	const query = `SELECT f.id, f.user_id, f.friend_id,
		COALESCE(NULLIF(u.nickname, ''), u.username), u.avatar_url,
		EXISTS(SELECT 1 FROM blacklist b WHERE b.user_id = f.user_id AND b.blocked_id = f.friend_id),
		f.created_at
		FROM friendships f
		JOIN users u ON u.id = f.friend_id
		WHERE f.user_id = ?
		ORDER BY f.created_at DESC, f.id DESC
		LIMIT ? OFFSET ?`
	rows, err := m.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list friendships: %w", err)
	}
	defer rows.Close()
	friends := make([]model.Friendship, 0)
	for rows.Next() {
		var friendship model.Friendship
		if err := rows.Scan(&friendship.ID, &friendship.UserID, &friendship.FriendID,
			&friendship.Nickname, &friendship.AvatarURL, &friendship.IsBlocked,
			&friendship.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan friendship: %w", err)
		}
		friends = append(friends, friendship)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate friendships: %w", err)
	}
	return friends, nil
}

func (m *MySQLRepoImpl) CountFriendships(ctx context.Context, userID int64) (int64, error) {
	var total int64
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM friendships WHERE user_id = ?`, userID).Scan(&total); err != nil {
		return 0, fmt.Errorf("count friendships: %w", err)
	}
	return total, nil
}

func (m *MySQLRepoImpl) AddBlacklistEntry(ctx context.Context, entry *model.Blacklist) error {
	result, err := m.db.ExecContext(ctx,
		`INSERT INTO blacklist (user_id, blocked_id, created_at) VALUES (?, ?, ?)`,
		entry.UserID, entry.BlockedID, entry.CreatedAt)
	if err != nil {
		return fmt.Errorf("add blacklist entry: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read blacklist insert id: %w", err)
	}
	entry.ID = id
	return nil
}

func (m *MySQLRepoImpl) RejectPendingFriendRequests(ctx context.Context, userID, blockedID int64) error {
	const query = `UPDATE friend_requests SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE status = ? AND ((from_user_id = ? AND to_user_id = ?) OR (from_user_id = ? AND to_user_id = ?))`
	if _, err := m.db.ExecContext(ctx, query,
		model.FriendRequestRejected, model.FriendRequestPending,
		userID, blockedID, blockedID, userID); err != nil {
		return fmt.Errorf("reject pending blocked friend requests: %w", err)
	}
	return nil
}

func (m *MySQLRepoImpl) RemoveBlacklistEntry(ctx context.Context, userID, blockedID int64) error {
	if _, err := m.db.ExecContext(ctx,
		`DELETE FROM blacklist WHERE user_id = ? AND blocked_id = ?`, userID, blockedID); err != nil {
		return fmt.Errorf("remove blacklist entry: %w", err)
	}
	return nil
}

var _ FriendRepository = (*MySQLRepoImpl)(nil)
