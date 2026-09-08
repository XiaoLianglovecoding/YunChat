package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"my-im/internal/model"
)

const cacheTruthPageLimit = 5000

// WithinCacheSnapshot serializes a MySQL truth read plus its Redis replacement
// against business transactions that lock the same user/group owner row. The
// callback deliberately runs before COMMIT, so an older snapshot cannot land
// after a newer relationship transaction and become the permanent cache state.
func (m *MySQLRepoImpl) WithinCacheSnapshot(
	ctx context.Context,
	resource CacheResource,
	resourceID int64,
	fn func(context.Context, CacheSnapshotRepository) error,
) error {
	if m.root == nil {
		return errors.New("cache snapshot transaction requires a root database handle")
	}
	if !resource.Valid() || resourceID <= 0 || fn == nil {
		return fmt.Errorf("cache snapshot transaction: invalid resource %q:%d", resource, resourceID)
	}
	tx, err := m.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cache snapshot transaction: %w", err)
	}
	child := &MySQLRepoImpl{
		db:           newTimedRunner(tx, m.queryTimeout, m.observer),
		root:         m.root,
		queryTimeout: m.queryTimeout,
		observer:     m.observer,
		tx:           tx,
	}
	table := "users"
	if resource == CacheResourceGroupMembers {
		table = "`groups`"
	}
	var lockedID int64
	lockErr := child.db.QueryRowContext(ctx,
		"SELECT id FROM "+table+" WHERE id = ? FOR UPDATE", resourceID,
	).Scan(&lockedID)
	// A dissolved group has no live owner row. Lock its permanent tombstone so
	// rebuilds serialize on a durable row even after an old Redis restoration.
	if resource == CacheResourceGroupMembers && errors.Is(lockErr, sql.ErrNoRows) {
		lockErr = child.db.QueryRowContext(ctx,
			"SELECT group_id FROM group_tombstones WHERE group_id = ? FOR UPDATE", resourceID,
		).Scan(&lockedID)
	}
	// Never-existing IDs have neither row. Explicit reconciliation may still
	// read an empty projection, but online DELETE avoids creating such work.
	if lockErr != nil && !errors.Is(lockErr, sql.ErrNoRows) {
		_ = tx.Rollback()
		return fmt.Errorf("lock cache owner %s:%d: %w", resource, resourceID, lockErr)
	}
	if err := fn(ctx, child); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback cache snapshot transaction: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cache snapshot transaction: %w", err)
	}
	return nil
}

func normalizeCachePageLimit(limit int) int {
	if limit <= 0 {
		return 500
	}
	if limit > cacheTruthPageLimit {
		return cacheTruthPageLimit
	}
	return limit
}

func (m *MySQLRepoImpl) ListUserIDs(ctx context.Context, afterID int64, limit int) ([]int64, error) {
	return m.listCacheOwnerIDs(ctx, `SELECT id FROM users WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
}

func (m *MySQLRepoImpl) ListGroupIDs(ctx context.Context, afterID int64, limit int) ([]int64, error) {
	const query = "SELECT owner_id FROM (" +
		"SELECT id AS owner_id FROM `groups` WHERE id > ? " +
		"UNION " +
		"SELECT group_id AS owner_id FROM group_tombstones WHERE group_id > ?" +
		") AS group_cache_owners ORDER BY owner_id LIMIT ?"
	rows, err := m.db.QueryContext(ctx, query, afterID, afterID, normalizeCachePageLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("list live and dissolved group cache owners: %w", err)
	}
	defer rows.Close()
	return scanInt64Rows(rows, "group cache owner IDs")
}

func (m *MySQLRepoImpl) listCacheOwnerIDs(ctx context.Context, query string, afterID int64, limit int) ([]int64, error) {
	rows, err := m.db.QueryContext(ctx, query, afterID, normalizeCachePageLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("list cache owners: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan cache owner: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cache owners: %w", err)
	}
	return ids, nil
}

func (m *MySQLRepoImpl) ListFriendIDsForCache(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT friend_id FROM friendships WHERE user_id = ? ORDER BY friend_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list authoritative friends for user %d: %w", userID, err)
	}
	defer rows.Close()
	return scanInt64Rows(rows, "friend IDs")
}

func (m *MySQLRepoImpl) ListBlockedIDsForCache(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT blocked_id FROM blacklist WHERE user_id = ? ORDER BY blocked_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list authoritative blacklist for user %d: %w", userID, err)
	}
	defer rows.Close()
	return scanInt64Rows(rows, "blocked IDs")
}

// GroupExistsForCache is also used as a read-only cold-ID guard. Cleanup
// decisions call it through WithinCacheSnapshot, whose group/tombstone lock
// prevents a rebuild racing disband from deleting a still-active runtime.
func (m *MySQLRepoImpl) GroupExistsForCache(ctx context.Context, groupID int64) (bool, error) {
	var exists bool
	if err := m.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM `groups` WHERE id = ?)", groupID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check authoritative group %d existence: %w", groupID, err)
	}
	return exists, nil
}

func scanInt64Rows(rows *sql.Rows, label string) ([]int64, error) {
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan %s: %w", label, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", label, err)
	}
	return ids, nil
}

func (m *MySQLRepoImpl) ListGroupMembersForCache(ctx context.Context, groupID int64) ([]model.GroupMember, error) {
	const query = `SELECT id, group_id, user_id, role, muted_until, joined_at
		FROM group_members WHERE group_id = ? ORDER BY user_id`
	rows, err := m.db.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("list authoritative members for group %d: %w", groupID, err)
	}
	defer rows.Close()

	members := make([]model.GroupMember, 0)
	for rows.Next() {
		var member model.GroupMember
		if err := rows.Scan(&member.ID, &member.GroupID, &member.UserID, &member.Role, &member.MutedUntil, &member.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan authoritative group member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authoritative group members: %w", err)
	}
	return members, nil
}

func (m *MySQLRepoImpl) EnqueueCacheReconcile(ctx context.Context, resource CacheResource, resourceID int64) error {
	if !resource.Valid() {
		return fmt.Errorf("enqueue cache reconcile: invalid resource type %q", resource)
	}
	if resourceID <= 0 {
		return errors.New("enqueue cache reconcile: resource ID must be positive")
	}
	const query = `INSERT INTO cache_reconcile_events(resource_type, resource_id) VALUES(?, ?)`
	if _, err := m.db.ExecContext(ctx, query, resource, resourceID); err != nil {
		return fmt.Errorf("enqueue cache reconcile %s:%d: %w", resource, resourceID, err)
	}
	return nil
}

func (m *MySQLRepoImpl) ClaimCacheReconcileEvents(
	ctx context.Context,
	workerID string,
	limit int,
	lease time.Duration,
) ([]CacheReconcileEvent, error) {
	if m.root == nil || m.tx != nil {
		return nil, errors.New("claim cache reconcile events requires the root repository")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, errors.New("claim cache reconcile events: worker ID is empty")
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	limit = normalizeCachePageLimit(limit)
	token, err := newCacheClaimToken(workerID)
	if err != nil {
		return nil, err
	}

	tx, err := m.root.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin cache event claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	staleBefore := time.Now().UTC().Add(-lease)
	const selectQuery = `SELECT id, resource_type, resource_id, attempts
		FROM cache_reconcile_events
		WHERE (status = 0 AND available_at <= UTC_TIMESTAMP(6))
		   OR (status = 1 AND locked_at < ?)
		ORDER BY id
		LIMIT ?
		FOR UPDATE SKIP LOCKED`
	rows, err := tx.QueryContext(ctx, selectQuery, staleBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("select cache events to claim: %w", err)
	}
	events := make([]CacheReconcileEvent, 0, limit)
	for rows.Next() {
		var event CacheReconcileEvent
		if err := rows.Scan(&event.ID, &event.ResourceType, &event.ResourceID, &event.Attempts); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan cache event claim: %w", err)
		}
		event.Attempts++
		event.LockToken = token
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate cache event claim: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close cache event claim rows: %w", err)
	}

	if len(events) > 0 {
		placeholders := make([]string, len(events))
		args := make([]any, 0, len(events)+1)
		args = append(args, token)
		for i, event := range events {
			placeholders[i] = "?"
			args = append(args, event.ID)
		}
		query := `UPDATE cache_reconcile_events
			SET status = 1, attempts = attempts + 1, locked_at = UTC_TIMESTAMP(6), lock_token = ?
			WHERE id IN (` + strings.Join(placeholders, ",") + `)`
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return nil, fmt.Errorf("mark cache events claimed: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit cache event claim: %w", err)
	}
	committed = true
	return events, nil
}

func newCacheClaimToken(workerID string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("create cache claim token: %w", err)
	}
	// The DB column is 64 bytes; reserve room for the random suffix.
	if len(workerID) > 30 {
		workerID = workerID[:30]
	}
	return workerID + ":" + hex.EncodeToString(random), nil
}

func (m *MySQLRepoImpl) MarkCacheReconcileSuccess(ctx context.Context, eventID int64, lockToken string) error {
	const query = `UPDATE cache_reconcile_events
		SET status = 2, locked_at = NULL, lock_token = '', last_error = ''
		WHERE id = ? AND status = 1 AND lock_token = ?`
	result, err := m.db.ExecContext(ctx, query, eventID, lockToken)
	if err != nil {
		return fmt.Errorf("mark cache event %d successful: %w", eventID, err)
	}
	return requireOneAffected(result, "mark cache event successful")
}

func (m *MySQLRepoImpl) MarkCacheReconcileFailure(
	ctx context.Context,
	eventID int64,
	lockToken string,
	cause error,
	retryAfter time.Duration,
) error {
	if retryAfter < 0 {
		retryAfter = 0
	}
	message := "unknown reconciliation failure"
	if cause != nil {
		message = truncateUTF8(cause.Error(), 500)
	}
	const query = `UPDATE cache_reconcile_events
		SET status = 0, available_at = ?, locked_at = NULL, lock_token = '', last_error = ?
		WHERE id = ? AND status = 1 AND lock_token = ?`
	result, err := m.db.ExecContext(ctx, query, time.Now().UTC().Add(retryAfter), message, eventID, lockToken)
	if err != nil {
		return fmt.Errorf("mark cache event %d failed: %w", eventID, err)
	}
	return requireOneAffected(result, "mark cache event failed")
}

func requireOneAffected(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: read affected rows: %w", operation, err)
	}
	if affected != 1 {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	return nil
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && (value[maxBytes]&0xc0) == 0x80 {
		maxBytes--
	}
	return value[:maxBytes]
}

var _ CacheTruthRepository = (*MySQLRepoImpl)(nil)
