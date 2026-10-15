package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const adminSessionLastSeenWriteInterval = 5 * time.Minute

func (d *Database) GetAdminSetupState(ctx context.Context) (AdminSetupState, error) {
	if d == nil || d.raw == nil {
		return AdminSetupState{}, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var state AdminSetupState
	err := d.raw.QueryRowContext(ctx, `
SELECT singleton_id, COALESCE(consumed_at, 0), consumed_by,
       credential_fingerprint, auth_epoch, created_at, updated_at
FROM admin_setup_state
WHERE singleton_id = 1`).Scan(
		&state.SingletonID,
		&state.ConsumedAt,
		&state.ConsumedBy,
		&state.CredentialFingerprint,
		&state.AuthEpoch,
		&state.CreatedAt,
		&state.UpdatedAt,
	)
	return state, err
}

func (d *Database) UpdateAdminCredentialFingerprint(ctx context.Context, fingerprint string, now int64) (int64, bool, error) {
	if d == nil || d.raw == nil {
		return 0, false, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	fingerprint = strings.TrimSpace(fingerprint)
	var epoch int64
	changed := false
	err := d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT credential_fingerprint, auth_epoch FROM admin_setup_state WHERE singleton_id = 1`).Scan(&current, &epoch); err != nil {
			return err
		}
		if current == fingerprint {
			return nil
		}
		result, err := tx.ExecContext(ctx, `
UPDATE admin_setup_state
SET credential_fingerprint = ?, auth_epoch = auth_epoch + 1, updated_at = ?
WHERE singleton_id = 1`, fingerprint, now)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected != 1 {
			return fmt.Errorf("admin setup state update affected %d rows", affected)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE admin_sessions SET revoked_at = ?
WHERE revoked_at IS NULL`, now); err != nil {
			return err
		}
		changed = true
		epoch++
		return nil
	})
	return epoch, changed, err
}

func (d *Database) ConsumeAdminSetup(ctx context.Context, consumedBy string, now int64) (bool, error) {
	if d == nil || d.raw == nil {
		return false, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	consumedBy = trimAdminSessionField(consumedBy, 128)
	var consumed bool
	err := d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE admin_setup_state
SET consumed_at = ?, consumed_by = ?, updated_at = ?
WHERE singleton_id = 1 AND consumed_at IS NULL`, now, consumedBy, now)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		consumed = affected == 1
		return nil
	})
	return consumed, err
}

func (d *Database) CreateAdminSession(ctx context.Context, username, sessionHandle, tokenHash, userAgent, remoteAddr string, now int64, secondaryTTL time.Duration) (AdminSessionRecord, error) {
	if d == nil || d.raw == nil {
		return AdminSessionRecord{}, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	username = strings.TrimSpace(username)
	sessionHandle = trimAdminSessionField(sessionHandle, 128)
	tokenHash = trimAdminSessionField(tokenHash, 128)
	userAgent = trimAdminSessionField(userAgent, 512)
	remoteAddr = trimAdminSessionField(remoteAddr, 128)
	if username == "" || sessionHandle == "" || tokenHash == "" {
		return AdminSessionRecord{}, fmt.Errorf("admin session identity is incomplete")
	}
	if secondaryTTL <= 0 {
		secondaryTTL = 12 * time.Hour
	}
	var record AdminSessionRecord
	err := d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM admin_sessions
WHERE revoked_at IS NOT NULL OR (expires_at IS NOT NULL AND expires_at <= ?)`, now); err != nil {
			return err
		}
		var epoch int64
		if err := tx.QueryRowContext(ctx, `SELECT auth_epoch FROM admin_setup_state WHERE singleton_id = 1`).Scan(&epoch); err != nil {
			return err
		}
		var activePrimary int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(1) FROM admin_sessions
WHERE username = ? AND is_primary = 1 AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > ?)`, username, now).Scan(&activePrimary); err != nil {
			return err
		}
		isPrimary := activePrimary == 0
		var expiresAt *int64
		if !isPrimary {
			value := now + int64(secondaryTTL/time.Second)
			expiresAt = &value
		}
		var expiresValue any
		if expiresAt != nil {
			expiresValue = *expiresAt
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO admin_sessions
	(session_handle, token_hash, username, auth_epoch, is_primary, created_at, last_seen_at, expires_at, user_agent, remote_addr)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sessionHandle, tokenHash, username, epoch, boolToInt(isPrimary), now, now, expiresValue, userAgent, remoteAddr)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		record = AdminSessionRecord{
			ID:            id,
			SessionHandle: sessionHandle,
			TokenHash:     tokenHash,
			Username:      username,
			AuthEpoch:     epoch,
			IsPrimary:     isPrimary,
			CreatedAt:     now,
			LastSeenAt:    now,
			ExpiresAt:     expiresAt,
			UserAgent:     userAgent,
			RemoteAddr:    remoteAddr,
		}
		return nil
	})
	return record, err
}

func (d *Database) GetAdminSessionByTokenHash(ctx context.Context, tokenHash string) (AdminSessionRecord, error) {
	if d == nil || d.raw == nil {
		return AdminSessionRecord{}, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var record AdminSessionRecord
	var expiresAt, revokedAt sql.NullInt64
	err := d.raw.QueryRowContext(ctx, `
SELECT id, session_handle, token_hash, username, auth_epoch, is_primary,
       created_at, last_seen_at, expires_at, user_agent, remote_addr, revoked_at
FROM admin_sessions WHERE token_hash = ?`, strings.TrimSpace(tokenHash)).Scan(
		&record.ID, &record.SessionHandle, &record.TokenHash, &record.Username,
		&record.AuthEpoch, &record.IsPrimary, &record.CreatedAt, &record.LastSeenAt,
		&expiresAt, &record.UserAgent, &record.RemoteAddr, &revokedAt,
	)
	if err != nil {
		return AdminSessionRecord{}, err
	}
	record.IsPrimary = record.IsPrimary
	if expiresAt.Valid {
		value := expiresAt.Int64
		record.ExpiresAt = &value
	}
	if revokedAt.Valid {
		value := revokedAt.Int64
		record.RevokedAt = &value
	}
	return record, nil
}

func (d *Database) GetAdminSessionByHandle(ctx context.Context, username, sessionHandle string) (AdminSessionRecord, error) {
	if d == nil || d.raw == nil {
		return AdminSessionRecord{}, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var record AdminSessionRecord
	var expiresAt, revokedAt sql.NullInt64
	err := d.raw.QueryRowContext(ctx, `
SELECT id, session_handle, token_hash, username, auth_epoch, is_primary,
       created_at, last_seen_at, expires_at, user_agent, remote_addr, revoked_at
FROM admin_sessions WHERE username = ? AND session_handle = ?`, strings.TrimSpace(username), strings.TrimSpace(sessionHandle)).Scan(
		&record.ID, &record.SessionHandle, &record.TokenHash, &record.Username,
		&record.AuthEpoch, &record.IsPrimary, &record.CreatedAt, &record.LastSeenAt,
		&expiresAt, &record.UserAgent, &record.RemoteAddr, &revokedAt,
	)
	if err != nil {
		return AdminSessionRecord{}, err
	}
	if expiresAt.Valid {
		value := expiresAt.Int64
		record.ExpiresAt = &value
	}
	if revokedAt.Valid {
		value := revokedAt.Int64
		record.RevokedAt = &value
	}
	return record, nil
}

func (d *Database) TouchAdminSession(ctx context.Context, sessionHandle string, now int64) error {
	if d == nil || d.raw == nil {
		return fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	err := d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
UPDATE admin_sessions SET last_seen_at = ?
WHERE session_handle = ? AND revoked_at IS NULL AND last_seen_at <= ?`,
			now, strings.TrimSpace(sessionHandle), now-int64(adminSessionLastSeenWriteInterval/time.Second))
		return err
	})
	return err
}

func (d *Database) ListAdminSessionSummaries(ctx context.Context, username, currentHandle string, now int64) ([]AdminSessionSummary, error) {
	if d == nil || d.raw == nil {
		return nil, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	rows, err := d.raw.QueryContext(ctx, `
SELECT session_handle, username, auth_epoch, is_primary, created_at, last_seen_at,
       expires_at, user_agent, remote_addr
FROM admin_sessions
WHERE username = ? AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > ?)
ORDER BY is_primary DESC, created_at ASC`, strings.TrimSpace(username), now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminSessionSummary{}
	for rows.Next() {
		var item AdminSessionSummary
		var expiresAt sql.NullInt64
		if err := rows.Scan(&item.SessionHandle, &item.Username, &item.AuthEpoch, &item.IsPrimary, &item.CreatedAt, &item.LastSeenAt, &expiresAt, &item.UserAgent, &item.RemoteAddr); err != nil {
			return nil, err
		}
		item.IsCurrent = item.SessionHandle == strings.TrimSpace(currentHandle)
		if expiresAt.Valid {
			value := expiresAt.Int64
			item.ExpiresAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (d *Database) UpdateAdminSessionExpiry(ctx context.Context, username, sessionHandle string, expiresAt int64, now int64) (bool, error) {
	if now <= 0 {
		now = time.Now().Unix()
	}
	return d.updateAdminSession(ctx, `
UPDATE admin_sessions SET expires_at = ?
WHERE username = ? AND session_handle = ? AND is_primary = 0
  AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
		expiresAt, strings.TrimSpace(username), strings.TrimSpace(sessionHandle), now)
}

func (d *Database) RevokeAdminSession(ctx context.Context, username, sessionHandle string, now int64) (bool, error) {
	if now <= 0 {
		now = time.Now().Unix()
	}
	return d.updateAdminSession(ctx, `
UPDATE admin_sessions SET revoked_at = ?
WHERE username = ? AND session_handle = ? AND revoked_at IS NULL`,
		now, strings.TrimSpace(username), strings.TrimSpace(sessionHandle))
}

func (d *Database) RevokeOtherAdminSessions(ctx context.Context, username, currentHandle string, now int64) (int64, error) {
	if d == nil || d.raw == nil {
		return 0, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	var count int64
	err := d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE admin_sessions SET revoked_at = ?
WHERE username = ? AND session_handle <> ? AND revoked_at IS NULL`, now, strings.TrimSpace(username), strings.TrimSpace(currentHandle))
		if err != nil {
			return err
		}
		count, err = result.RowsAffected()
		return err
	})
	return count, err
}

func (d *Database) RevokeCurrentAdminSession(ctx context.Context, username, sessionHandle string, now int64) (bool, error) {
	return d.RevokeAdminSession(ctx, username, sessionHandle, now)
}

func (d *Database) TransferAdminPrimary(ctx context.Context, username, currentHandle string, now int64) error {
	if d == nil || d.raw == nil {
		return fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	return d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
UPDATE admin_sessions SET revoked_at = ?
WHERE username = ? AND is_primary = 1 AND revoked_at IS NULL`, now, strings.TrimSpace(username)); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
UPDATE admin_sessions SET is_primary = 1, expires_at = NULL
WHERE username = ? AND session_handle = ? AND is_primary = 0 AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > ?)`, strings.TrimSpace(username), strings.TrimSpace(currentHandle), now)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("primary transfer target is not active")
		}
		return nil
	})
}

func (d *Database) updateAdminSession(ctx context.Context, query string, args ...any) (bool, error) {
	if d == nil || d.raw == nil {
		return false, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var changed bool
	err := d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		changed = affected == 1
		return nil
	})
	return changed, err
}

func trimAdminSessionField(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes]
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
