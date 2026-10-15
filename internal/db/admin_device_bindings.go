package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const adminDeviceBindingTTL = 2 * time.Minute

func (d *Database) StartAdminDeviceBinding(ctx context.Context, bindingID, tokenHash, username, initiatorHandle string, now int64) (AdminDeviceBinding, error) {
	if d == nil || d.raw == nil {
		return AdminDeviceBinding{}, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	bindingID = strings.TrimSpace(bindingID)
	tokenHash = strings.TrimSpace(tokenHash)
	username = strings.TrimSpace(username)
	initiatorHandle = strings.TrimSpace(initiatorHandle)
	if bindingID == "" || tokenHash == "" || username == "" || initiatorHandle == "" {
		return AdminDeviceBinding{}, fmt.Errorf("device binding identity is incomplete")
	}
	var record AdminDeviceBinding
	err := d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
UPDATE admin_device_bindings
SET status = 'expired', status_version = status_version + 1
WHERE initiator_session_handle = ? AND status IN ('pending', 'claimed')`,
			initiatorHandle,
		); err != nil {
			return err
		}
		expiresAt := now + int64(adminDeviceBindingTTL/time.Second)
		result, err := tx.ExecContext(ctx, `
INSERT INTO admin_device_bindings
	(binding_id, token_hash, username, initiator_session_handle, status, status_version, created_at, expires_at)
VALUES (?, ?, ?, ?, 'pending', 1, ?, ?)`,
			bindingID, tokenHash, username, initiatorHandle, now, expiresAt,
		)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		record = AdminDeviceBinding{
			ID: id, BindingID: bindingID, TokenHash: tokenHash, Username: username,
			InitiatorSessionHandle: initiatorHandle, Status: "pending", StatusVersion: 1,
			CreatedAt: now, ExpiresAt: expiresAt,
		}
		return nil
	})
	return record, err
}

func (d *Database) ClaimAdminDeviceBinding(ctx context.Context, tokenHash, targetCookieHash, userAgent, remoteAddr string, now int64) (AdminDeviceBinding, bool, error) {
	if d == nil || d.raw == nil {
		return AdminDeviceBinding{}, false, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	var record AdminDeviceBinding
	err := d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
UPDATE admin_device_bindings
SET status = 'expired', status_version = status_version + 1
WHERE token_hash = ? AND status IN ('pending', 'claimed') AND expires_at <= ?`,
			strings.TrimSpace(tokenHash), now,
		); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
UPDATE admin_device_bindings
SET status = 'claimed', target_cookie_hash = ?, target_user_agent = ?, target_remote_addr = ?,
	claimed_at = ?, status_version = status_version + 1
WHERE token_hash = ? AND status = 'pending' AND expires_at > ?`,
			strings.TrimSpace(targetCookieHash), trimAdminSessionField(userAgent, 512), trimAdminSessionField(remoteAddr, 128),
			now, strings.TrimSpace(tokenHash), now,
		)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
		return scanAdminDeviceBindingRow(tx.QueryRowContext(ctx, `
SELECT id, binding_id, token_hash, username, initiator_session_handle, COALESCE(target_cookie_hash, ''),
	status, status_version, target_user_agent, target_remote_addr, created_at, claimed_at, approved_at, expires_at, completed_at
FROM admin_device_bindings WHERE token_hash = ?`, tokenHash), &record)
	})
	return record, record.BindingID != "", err
}

func (d *Database) GetAdminDeviceBindingByCookie(ctx context.Context, cookieHash string, now int64) (AdminDeviceBinding, error) {
	if d == nil || d.raw == nil {
		return AdminDeviceBinding{}, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	_, _ = d.raw.ExecContext(ctx, `
UPDATE admin_device_bindings
SET status = 'expired', status_version = status_version + 1
WHERE target_cookie_hash = ? AND status IN ('claimed', 'approved') AND expires_at <= ?`,
		strings.TrimSpace(cookieHash), now,
	)
	var record AdminDeviceBinding
	err := scanAdminDeviceBindingRow(d.raw.QueryRowContext(ctx, `
SELECT id, binding_id, token_hash, username, initiator_session_handle, COALESCE(target_cookie_hash, ''),
	status, status_version, target_user_agent, target_remote_addr, created_at, claimed_at, approved_at, expires_at, completed_at
FROM admin_device_bindings WHERE target_cookie_hash = ?`, strings.TrimSpace(cookieHash)), &record)
	return record, err
}

func (d *Database) ListAdminDeviceBindings(ctx context.Context, username, initiatorHandle string, now int64) ([]AdminDeviceBinding, error) {
	if d == nil || d.raw == nil {
		return nil, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	_, _ = d.raw.ExecContext(ctx, `
UPDATE admin_device_bindings
SET status = 'expired', status_version = status_version + 1
WHERE username = ? AND status IN ('pending', 'claimed') AND expires_at <= ?`,
		strings.TrimSpace(username), now,
	)
	rows, err := d.raw.QueryContext(ctx, `
SELECT id, binding_id, token_hash, username, initiator_session_handle, COALESCE(target_cookie_hash, ''),
	status, status_version, target_user_agent, target_remote_addr, created_at, claimed_at, approved_at, expires_at, completed_at
FROM admin_device_bindings
WHERE username = ? AND initiator_session_handle = ?
	AND status IN ('pending', 'claimed', 'approved')
ORDER BY created_at DESC`, strings.TrimSpace(username), strings.TrimSpace(initiatorHandle))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminDeviceBinding{}
	for rows.Next() {
		var item AdminDeviceBinding
		if err := scanAdminDeviceBindingRow(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (d *Database) ApproveAdminDeviceBinding(ctx context.Context, username, initiatorHandle, bindingID string, now int64) (bool, error) {
	return d.updateAdminDeviceBindingStatus(ctx, username, initiatorHandle, bindingID, "approved", "claimed", now)
}

func (d *Database) RejectAdminDeviceBinding(ctx context.Context, username, initiatorHandle, bindingID string, now int64) (bool, error) {
	if d == nil || d.raw == nil {
		return false, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := d.raw.ExecContext(ctx, `
UPDATE admin_device_bindings
SET status = 'rejected', status_version = status_version + 1
WHERE username = ? AND initiator_session_handle = ? AND binding_id = ?
	AND status IN ('pending', 'claimed') AND expires_at > ?`,
		strings.TrimSpace(username), strings.TrimSpace(initiatorHandle), strings.TrimSpace(bindingID), now,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (d *Database) updateAdminDeviceBindingStatus(ctx context.Context, username, initiatorHandle, bindingID, nextStatus, previousStatus string, now int64) (bool, error) {
	if d == nil || d.raw == nil {
		return false, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := d.raw.ExecContext(ctx, `
UPDATE admin_device_bindings
SET status = ?, approved_at = CASE WHEN ? = 'approved' THEN ? ELSE approved_at END,
	status_version = status_version + 1
WHERE username = ? AND initiator_session_handle = ? AND binding_id = ?
	AND status = ? AND expires_at > ?`,
		nextStatus, nextStatus, now, strings.TrimSpace(username), strings.TrimSpace(initiatorHandle),
		strings.TrimSpace(bindingID), previousStatus, now,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (d *Database) CompleteAdminDeviceBinding(ctx context.Context, bindingID, targetCookieHash, sessionHandle, sessionTokenHash, userAgent, remoteAddr string, now int64, secondaryTTL time.Duration) (AdminSessionRecord, bool, error) {
	if d == nil || d.raw == nil {
		return AdminSessionRecord{}, false, fmt.Errorf("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	if secondaryTTL <= 0 {
		secondaryTTL = 12 * time.Hour
	}
	var record AdminSessionRecord
	var completed bool
	err := d.withWriteTxContext(ctx, func(tx *sql.Tx) error {
		var username, initiator string
		var status, expectedCookie string
		var version int
		var expiresAt int64
		if err := tx.QueryRowContext(ctx, `
SELECT username, initiator_session_handle, COALESCE(target_cookie_hash, ''), status, status_version, expires_at
FROM admin_device_bindings WHERE binding_id = ?`, strings.TrimSpace(bindingID)).Scan(
			&username, &initiator, &expectedCookie, &status, &version, &expiresAt,
		); err != nil {
			return err
		}
		if status != "approved" || expiresAt <= now || expectedCookie != strings.TrimSpace(targetCookieHash) {
			return nil
		}
		var initiatorActive int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(1) FROM admin_sessions
WHERE username = ? AND session_handle = ? AND is_primary = 1 AND revoked_at IS NULL
	AND (expires_at IS NULL OR expires_at > ?)`, username, initiator, now).Scan(&initiatorActive); err != nil {
			return err
		}
		if initiatorActive != 1 {
			return nil
		}
		result, err := tx.ExecContext(ctx, `
UPDATE admin_device_bindings
SET status = 'completed', completed_at = ?, status_version = status_version + 1
WHERE binding_id = ? AND status = 'approved' AND status_version = ? AND expires_at > ?`,
			now, bindingID, version, now,
		)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return nil
		}
		var epoch int64
		if err := tx.QueryRowContext(ctx, `SELECT auth_epoch FROM admin_setup_state WHERE singleton_id = 1`).Scan(&epoch); err != nil {
			return err
		}
		expiry := now + int64(secondaryTTL/time.Second)
		result, err = tx.ExecContext(ctx, `
INSERT INTO admin_sessions
	(session_handle, token_hash, username, auth_epoch, is_primary, created_at, last_seen_at, expires_at, user_agent, remote_addr)
VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?, ?)`,
			sessionHandle, sessionTokenHash, username, epoch, now, now, expiry,
			trimAdminSessionField(userAgent, 512), trimAdminSessionField(remoteAddr, 128),
		)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		record = AdminSessionRecord{ID: id, SessionHandle: sessionHandle, TokenHash: sessionTokenHash, Username: username, AuthEpoch: epoch, CreatedAt: now, LastSeenAt: now, ExpiresAt: &expiry, UserAgent: userAgent, RemoteAddr: remoteAddr}
		completed = true
		return nil
	})
	return record, completed, err
}

func scanAdminDeviceBindingRow(row interface{ Scan(...any) error }, item *AdminDeviceBinding) error {
	return row.Scan(
		&item.ID, &item.BindingID, &item.TokenHash, &item.Username, &item.InitiatorSessionHandle,
		&item.TargetCookieHash, &item.Status, &item.StatusVersion, &item.TargetUserAgent, &item.TargetRemoteAddr,
		&item.CreatedAt, &item.ClaimedAt, &item.ApprovedAt, &item.ExpiresAt, &item.CompletedAt,
	)
}
