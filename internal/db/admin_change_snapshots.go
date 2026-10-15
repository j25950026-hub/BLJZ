package db

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (d *Database) SaveAdminChangeSnapshot(snapshot AdminChangeSnapshot) (int64, error) {
	if d == nil || d.raw == nil {
		return 0, fmt.Errorf("database is not configured")
	}
	snapshot.OwnerUsername = strings.TrimSpace(snapshot.OwnerUsername)
	snapshot.Action = strings.TrimSpace(snapshot.Action)
	snapshot.Status = strings.TrimSpace(snapshot.Status)
	if snapshot.Action == "" {
		return 0, fmt.Errorf("snapshot action is required")
	}
	if snapshot.Status == "" {
		snapshot.Status = "completed"
	}
	if snapshot.SummaryJSON == "" {
		snapshot.SummaryJSON = `{}`
	}
	if !json.Valid([]byte(snapshot.SummaryJSON)) {
		return 0, fmt.Errorf("snapshot summary must be valid json")
	}
	for i := range snapshot.Items {
		if snapshot.Items[i].BeforeJSON == "" {
			snapshot.Items[i].BeforeJSON = `{}`
		}
		if snapshot.Items[i].AfterJSON == "" {
			snapshot.Items[i].AfterJSON = `{}`
		}
		if !json.Valid([]byte(snapshot.Items[i].BeforeJSON)) || !json.Valid([]byte(snapshot.Items[i].AfterJSON)) {
			return 0, fmt.Errorf("snapshot item json is invalid")
		}
		snapshot.Items[i].Status = strings.TrimSpace(snapshot.Items[i].Status)
		snapshot.Items[i].FailureReason = strings.TrimSpace(snapshot.Items[i].FailureReason)
	}

	tx, err := d.raw.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.Exec(`
INSERT INTO admin_change_snapshots (owner_username, action, summary_json, status)
VALUES (?, ?, ?, ?)`, snapshot.OwnerUsername, snapshot.Action, snapshot.SummaryJSON, snapshot.Status)
	if err != nil {
		return 0, err
	}
	snapshotID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, item := range snapshot.Items {
		if _, err := tx.Exec(`
INSERT INTO admin_change_snapshot_items (snapshot_id, chat_id, before_json, after_json, status, failure_reason)
VALUES (?, ?, ?, ?, ?, ?)`, snapshotID, item.ChatID, item.BeforeJSON, item.AfterJSON, item.Status, item.FailureReason); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return snapshotID, nil
}

func (d *Database) ListAdminChangeSnapshots(ownerUsername string, limit int) ([]AdminChangeSnapshot, error) {
	if d == nil || d.raw == nil {
		return nil, fmt.Errorf("database is not configured")
	}
	ownerUsername = strings.TrimSpace(ownerUsername)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := d.raw.Query(`
SELECT id, owner_username, action, summary_json, status, COALESCE(created_at, '')
FROM admin_change_snapshots
WHERE owner_username = ?
ORDER BY id DESC
LIMIT ?`, ownerUsername, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AdminChangeSnapshot{}
	for rows.Next() {
		var item AdminChangeSnapshot
		if err := rows.Scan(&item.ID, &item.OwnerUsername, &item.Action, &item.SummaryJSON, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Items, err = d.listAdminChangeSnapshotItems(out[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (d *Database) listAdminChangeSnapshotItems(snapshotID int64) ([]AdminChangeSnapshotItem, error) {
	rows, err := d.raw.Query(`
SELECT id, snapshot_id, chat_id, before_json, after_json, status, failure_reason, COALESCE(created_at, '')
FROM admin_change_snapshot_items
WHERE snapshot_id = ?
ORDER BY id ASC`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []AdminChangeSnapshotItem{}
	for rows.Next() {
		var item AdminChangeSnapshotItem
		if err := rows.Scan(&item.ID, &item.SnapshotID, &item.ChatID, &item.BeforeJSON, &item.AfterJSON, &item.Status, &item.FailureReason, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
