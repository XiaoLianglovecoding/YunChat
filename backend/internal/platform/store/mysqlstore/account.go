package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
	mysql "github.com/go-sql-driver/mysql"
)

func (store *Store) CreateAccount(ctx context.Context, user domain.User, passwordHash string, settings domain.UserSettings) error {
	return store.WithinTransaction(ctx, func(txContext context.Context) error {
		exec := store.Executor(txContext)
		if _, err := exec.ExecContext(txContext, `
			INSERT INTO users (id, username, email, nickname, avatar_url, bio, status, created_at, updated_at)
			VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?)`,
			user.ID, user.Username, user.Email, user.Nickname, user.AvatarURL, user.Bio, user.Status, user.CreatedAt, user.UpdatedAt); err != nil {
			return mapDatabaseError(err)
		}
		if _, err := exec.ExecContext(txContext, `
			INSERT INTO user_credentials (user_id, password_hash, password_changed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`, user.ID, passwordHash, user.CreatedAt, user.CreatedAt, user.UpdatedAt); err != nil {
			return mapDatabaseError(err)
		}
		_, err := exec.ExecContext(txContext, `
			INSERT INTO user_settings (user_id, locale, theme, notification_enabled, message_preview_enabled, extra, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`, user.ID, settings.Locale, settings.Theme,
			settings.NotificationEnabled, settings.MessagePreviewEnabled, string(settings.Extra), settings.CreatedAt, settings.UpdatedAt)
		return mapDatabaseError(err)
	})
}

func (store *Store) CreateAccountAndSession(ctx context.Context, user domain.User, passwordHash string, settings domain.UserSettings, device domain.Device, session domain.Session) (domain.Session, error) {
	err := store.WithinTransaction(ctx, func(txContext context.Context) error {
		exec := store.Executor(txContext)
		if _, err := exec.ExecContext(txContext, `
			INSERT INTO users (id, username, email, nickname, avatar_url, bio, status, created_at, updated_at)
			VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?)`, user.ID, user.Username, user.Email, user.Nickname,
			user.AvatarURL, user.Bio, user.Status, user.CreatedAt, user.UpdatedAt); err != nil {
			return mapDatabaseError(err)
		}
		if _, err := exec.ExecContext(txContext, `
			INSERT INTO user_credentials (user_id, password_hash, password_changed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`, user.ID, passwordHash, user.CreatedAt, user.CreatedAt, user.UpdatedAt); err != nil {
			return mapDatabaseError(err)
		}
		if _, err := exec.ExecContext(txContext, `
			INSERT INTO user_settings (user_id, locale, theme, notification_enabled, message_preview_enabled, extra, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`, user.ID, settings.Locale, settings.Theme, settings.NotificationEnabled,
			settings.MessagePreviewEnabled, string(settings.Extra), settings.CreatedAt, settings.UpdatedAt); err != nil {
			return mapDatabaseError(err)
		}
		if _, err := exec.ExecContext(txContext, `
			INSERT INTO user_devices (id, user_id, device_key, device_name, platform, last_active_at, revoked_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`, device.ID, user.ID, device.DeviceKey, device.DeviceName, device.Platform, device.LastActiveAt, device.LastActiveAt); err != nil {
			return mapDatabaseError(err)
		}
		if _, err := exec.ExecContext(txContext, `
			INSERT INTO refresh_tokens (id, user_id, device_id, token_hash, family_id, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, session.TokenID, user.ID, device.ID, session.TokenHash, session.FamilyID, session.ExpiresAt, device.LastActiveAt); err != nil {
			return mapDatabaseError(err)
		}
		session.UserID, session.DeviceID = user.ID, device.ID
		return nil
	})
	return session, err
}

func (store *Store) FindByID(ctx context.Context, userID domain.UserID) (domain.User, error) {
	return scanUser(store.Executor(ctx).QueryRowContext(ctx, `
		SELECT id, username, COALESCE(email, ''), COALESCE(phone, ''), nickname, avatar_url, bio, status, last_seen_at, created_at, updated_at
		FROM users WHERE id = ?`, userID))
}

func (store *Store) FindByLogin(ctx context.Context, login string) (domain.User, string, error) {
	var user domain.User
	var hash string
	var lastSeen sql.NullTime
	row := store.Executor(ctx).QueryRowContext(ctx, `
		SELECT u.id, u.username, COALESCE(u.email, ''), COALESCE(u.phone, ''), u.nickname, u.avatar_url, u.bio,
		       u.status, u.last_seen_at, u.created_at, u.updated_at, c.password_hash
		FROM users u JOIN user_credentials c ON c.user_id = u.id
		WHERE u.username = ? OR u.email = ? OR u.phone = ?
		LIMIT 1`, login, login, login)
	if err := row.Scan(&user.ID, &user.Username, &user.Email, &user.Phone, &user.Nickname, &user.AvatarURL,
		&user.Bio, &user.Status, &lastSeen, &user.CreatedAt, &user.UpdatedAt, &hash); err != nil {
		return domain.User{}, "", mapQueryError(err)
	}
	if lastSeen.Valid {
		user.LastSeenAt = &lastSeen.Time
	}
	return user, hash, nil
}

func (store *Store) FindPasswordHash(ctx context.Context, userID domain.UserID) (string, error) {
	var hash string
	if err := store.Executor(ctx).QueryRowContext(ctx, `SELECT password_hash FROM user_credentials WHERE user_id = ?`, userID).Scan(&hash); err != nil {
		return "", mapQueryError(err)
	}
	return hash, nil
}

func (store *Store) UpdateProfile(ctx context.Context, user domain.User) error {
	result, err := store.Executor(ctx).ExecContext(ctx, `
		UPDATE users SET email = NULLIF(?, ''), nickname = ?, avatar_url = ?, bio = ?, updated_at = ? WHERE id = ?`,
		user.Email, user.Nickname, user.AvatarURL, user.Bio, user.UpdatedAt, user.ID)
	if err != nil {
		return mapDatabaseError(err)
	}
	return requireAffected(result, domain.ErrNotFound)
}

func (store *Store) GetSettings(ctx context.Context, userID domain.UserID) (domain.UserSettings, error) {
	var settings domain.UserSettings
	var extra []byte
	if err := store.Executor(ctx).QueryRowContext(ctx, `
		SELECT user_id, locale, theme, notification_enabled, message_preview_enabled, COALESCE(extra, JSON_OBJECT()), created_at, updated_at
		FROM user_settings WHERE user_id = ?`, userID).Scan(&settings.UserID, &settings.Locale, &settings.Theme,
		&settings.NotificationEnabled, &settings.MessagePreviewEnabled, &extra, &settings.CreatedAt, &settings.UpdatedAt); err != nil {
		return domain.UserSettings{}, mapQueryError(err)
	}
	settings.Extra = extra
	return settings, nil
}

func (store *Store) UpdateSettings(ctx context.Context, settings domain.UserSettings) error {
	result, err := store.Executor(ctx).ExecContext(ctx, `
		UPDATE user_settings SET locale = ?, theme = ?, notification_enabled = ?, message_preview_enabled = ?, extra = ?, updated_at = ?
		WHERE user_id = ?`, settings.Locale, settings.Theme, settings.NotificationEnabled, settings.MessagePreviewEnabled,
		nullJSON(settings.Extra), settings.UpdatedAt, settings.UserID)
	if err != nil {
		return mapDatabaseError(err)
	}
	return requireAffected(result, domain.ErrNotFound)
}

func (store *Store) ChangePassword(ctx context.Context, userID domain.UserID, passwordHash string) error {
	result, err := store.Executor(ctx).ExecContext(ctx, `
		UPDATE user_credentials SET password_hash = ?, password_version = password_version + 1, password_changed_at = UTC_TIMESTAMP(3), updated_at = UTC_TIMESTAMP(3)
		WHERE user_id = ?`, passwordHash, userID)
	if err != nil {
		return mapDatabaseError(err)
	}
	if err := requireAffected(result, domain.ErrNotFound); err != nil {
		return err
	}
	_, err = store.Executor(ctx).ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = UTC_TIMESTAMP(3) WHERE user_id = ? AND revoked_at IS NULL`, userID)
	return mapDatabaseError(err)
}

func (store *Store) StartSingleDeviceSession(ctx context.Context, userID domain.UserID, device domain.Device, session domain.Session) (domain.Session, error) {
	var resultSession domain.Session
	err := store.WithinTransaction(ctx, func(txContext context.Context) error {
		exec := store.Executor(txContext)
		if _, err := exec.ExecContext(txContext, `UPDATE refresh_tokens SET revoked_at = UTC_TIMESTAMP(3) WHERE user_id = ? AND revoked_at IS NULL`, userID); err != nil {
			return mapDatabaseError(err)
		}
		if _, err := exec.ExecContext(txContext, `UPDATE user_devices SET revoked_at = UTC_TIMESTAMP(3) WHERE user_id = ? AND revoked_at IS NULL`, userID); err != nil {
			return mapDatabaseError(err)
		}

		var existingID domain.DeviceID
		err := exec.QueryRowContext(txContext, `SELECT id FROM user_devices WHERE user_id = ? AND device_key = ? FOR UPDATE`, userID, device.DeviceKey).Scan(&existingID)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := exec.ExecContext(txContext, `INSERT INTO user_devices (id, user_id, device_key, device_name, platform, last_active_at, revoked_at, created_at) VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`, device.ID, userID, device.DeviceKey, device.DeviceName, device.Platform, device.LastActiveAt, device.LastActiveAt); err != nil {
				return mapDatabaseError(err)
			}
		} else if err != nil {
			return mapQueryError(err)
		} else {
			device.ID = existingID
			if _, err := exec.ExecContext(txContext, `UPDATE user_devices SET device_name = ?, platform = ?, last_active_at = ?, revoked_at = NULL WHERE id = ?`, device.DeviceName, device.Platform, device.LastActiveAt, device.ID); err != nil {
				return mapDatabaseError(err)
			}
		}
		if _, err := exec.ExecContext(txContext, `INSERT INTO refresh_tokens (id, user_id, device_id, token_hash, family_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, session.TokenID, userID, device.ID, session.TokenHash, session.FamilyID, session.ExpiresAt, device.LastActiveAt); err != nil {
			return mapDatabaseError(err)
		}
		resultSession = session
		resultSession.UserID, resultSession.DeviceID = userID, device.ID
		return nil
	})
	return resultSession, err
}

func (store *Store) RotateRefreshToken(ctx context.Context, oldHash []byte, replacement domain.Session, now time.Time) (domain.Session, error) {
	var resultSession domain.Session
	replayed := false
	err := store.WithinTransaction(ctx, func(txContext context.Context) error {
		exec := store.Executor(txContext)
		var old domain.Session
		var usedAt, revokedAt sql.NullTime
		if err := exec.QueryRowContext(txContext, `SELECT id, user_id, device_id, family_id, expires_at, used_at, revoked_at FROM refresh_tokens WHERE token_hash = ? FOR UPDATE`, oldHash).Scan(&old.TokenID, &old.UserID, &old.DeviceID, &old.FamilyID, &old.ExpiresAt, &usedAt, &revokedAt); err != nil {
			return mapQueryError(err)
		}
		if usedAt.Valid {
			if _, err := exec.ExecContext(txContext, `UPDATE refresh_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL`, now, old.FamilyID); err != nil {
				return mapDatabaseError(err)
			}
			replayed = true
			return nil
		}
		if revokedAt.Valid || !old.ExpiresAt.After(now) {
			return domain.ErrUnauthorized
		}
		replacement.UserID, replacement.DeviceID, replacement.FamilyID = old.UserID, old.DeviceID, old.FamilyID
		if _, err := exec.ExecContext(txContext, `UPDATE refresh_tokens SET used_at = ?, replaced_by_id = ? WHERE id = ?`, now, replacement.TokenID, old.TokenID); err != nil {
			return mapDatabaseError(err)
		}
		if _, err := exec.ExecContext(txContext, `INSERT INTO refresh_tokens (id, user_id, device_id, token_hash, family_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, replacement.TokenID, replacement.UserID, replacement.DeviceID, replacement.TokenHash, replacement.FamilyID, replacement.ExpiresAt, now); err != nil {
			return mapDatabaseError(err)
		}
		if _, err := exec.ExecContext(txContext, `UPDATE user_devices SET last_active_at = ? WHERE id = ? AND revoked_at IS NULL`, now, replacement.DeviceID); err != nil {
			return mapDatabaseError(err)
		}
		resultSession = replacement
		return nil
	})
	if err == nil && replayed {
		return domain.Session{}, domain.ErrUnauthorized
	}
	return resultSession, err
}

func (store *Store) RevokeDeviceSession(ctx context.Context, userID domain.UserID, deviceID domain.DeviceID, now time.Time) error {
	return store.WithinTransaction(ctx, func(txContext context.Context) error {
		exec := store.Executor(txContext)
		if _, err := exec.ExecContext(txContext, `UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND device_id = ? AND revoked_at IS NULL`, now, userID, deviceID); err != nil {
			return mapDatabaseError(err)
		}
		result, err := exec.ExecContext(txContext, `UPDATE user_devices SET revoked_at = ? WHERE user_id = ? AND id = ? AND revoked_at IS NULL`, now, userID, deviceID)
		if err != nil {
			return mapDatabaseError(err)
		}
		return requireAffected(result, domain.ErrNotFound)
	})
}

func (store *Store) IsSessionActive(ctx context.Context, userID domain.UserID, deviceID domain.DeviceID, tokenID uint64, now time.Time) (bool, error) {
	var exists bool
	err := store.Executor(ctx).QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM refresh_tokens rt
			JOIN user_devices d ON d.id = rt.device_id
			WHERE rt.id = ? AND rt.user_id = ? AND rt.device_id = ?
			  AND rt.used_at IS NULL AND rt.revoked_at IS NULL AND rt.expires_at > ?
			  AND d.revoked_at IS NULL
		)`, tokenID, userID, deviceID, now).Scan(&exists)
	return exists, err
}

func scanUser(row *sql.Row) (domain.User, error) {
	var user domain.User
	var lastSeen sql.NullTime
	if err := row.Scan(&user.ID, &user.Username, &user.Email, &user.Phone, &user.Nickname, &user.AvatarURL, &user.Bio, &user.Status, &lastSeen, &user.CreatedAt, &user.UpdatedAt); err != nil {
		return domain.User{}, mapQueryError(err)
	}
	if lastSeen.Valid {
		user.LastSeenAt = &lastSeen.Time
	}
	return user, nil
}

func nullJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func requireAffected(result sql.Result, notFound error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound
	}
	return nil
}

func mapQueryError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

func mapDatabaseError(err error) error {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return domain.ErrConflict
	}
	return err
}

var _ domain.AccountRepository = (*Store)(nil)
