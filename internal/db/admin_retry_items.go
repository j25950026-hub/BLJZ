package db

import (
	"encoding/json"
	"fmt"
	"strings"
)

var adminRetryItemStatuses = map[string]struct{}{
	"pending":   {},
	"running":   {},
	"failed":    {},
	"completed": {},
	"cancelled": {},
}

func (d *Database) SaveAdminRetryItem(item AdminRetryItem) (AdminRetryItem, error) {
	if d == nil || d.raw == nil {
		return AdminRetryItem{}, fmt.Errorf("database is not configured")
	}
	item.OperationID = strings.TrimSpace(item.OperationID)
	item.ItemKey = strings.TrimSpace(item.ItemKey)
	item.Kind = strings.TrimSpace(item.Kind)
	item.PayloadJSON = strings.TrimSpace(item.PayloadJSON)
	item.FailureReason = strings.TrimSpace(item.FailureReason)
	item.Status = strings.TrimSpace(item.Status)
	if item.OperationID == "" || item.ItemKey == "" {
		return AdminRetryItem{}, fmt.Errorf("operation_id and item_key are required")
	}
	if item.PayloadJSON == "" {
		item.PayloadJSON = `{}`
	}
	if !json.Valid([]byte(item.PayloadJSON)) {
		return AdminRetryItem{}, fmt.Errorf("retry payload must be valid json")
	}
	if item.Status == "" {
		item.Status = "failed"
	}
	if _, ok := adminRetryItemStatuses[item.Status]; !ok {
		return AdminRetryItem{}, fmt.Errorf("不支持的重试状态")
	}
	_, err := d.raw.Exec(`
INSERT INTO admin_retry_items (operation_id, item_key, kind, chat_id, payload_json, failure_reason, status, attempt_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(operation_id, item_key) DO UPDATE SET
kind = excluded.kind,
chat_id = excluded.chat_id,
payload_json = excluded.payload_json,
failure_reason = excluded.failure_reason,
status = excluded.status,
updated_at = CURRENT_TIMESTAMP`, item.OperationID, item.ItemKey, item.Kind, item.ChatID, item.PayloadJSON, item.FailureReason, item.Status, item.AttemptCount)
	if err != nil {
		return AdminRetryItem{}, err
	}
	return d.getAdminRetryItemByKey(item.OperationID, item.ItemKey)
}

func (d *Database) getAdminRetryItemByKey(operationID, itemKey string) (AdminRetryItem, error) {
	var item AdminRetryItem
	err := d.raw.QueryRow(`
SELECT id, operation_id, item_key, kind, chat_id, payload_json, failure_reason, status, attempt_count, COALESCE(created_at, ''), COALESCE(updated_at, '')
FROM admin_retry_items
WHERE operation_id = ? AND item_key = ?`, operationID, itemKey).Scan(
		&item.ID, &item.OperationID, &item.ItemKey, &item.Kind, &item.ChatID, &item.PayloadJSON, &item.FailureReason, &item.Status, &item.AttemptCount, &item.CreatedAt, &item.UpdatedAt,
	)
	return item, err
}

func (d *Database) GetAdminRetryItem(id int64) (AdminRetryItem, error) {
	if d == nil || d.raw == nil {
		return AdminRetryItem{}, fmt.Errorf("database is not configured")
	}
	if id == 0 {
		return AdminRetryItem{}, fmt.Errorf("retry item id is required")
	}
	var item AdminRetryItem
	err := d.raw.QueryRow(`
SELECT id, operation_id, item_key, kind, chat_id, payload_json, failure_reason, status, attempt_count, COALESCE(created_at, ''), COALESCE(updated_at, '')
FROM admin_retry_items
WHERE id = ?`, id).Scan(
		&item.ID, &item.OperationID, &item.ItemKey, &item.Kind, &item.ChatID, &item.PayloadJSON, &item.FailureReason, &item.Status, &item.AttemptCount, &item.CreatedAt, &item.UpdatedAt,
	)
	return item, err
}

func (d *Database) ListAdminRetryItems(status string, limit int) ([]AdminRetryItem, error) {
	if d == nil || d.raw == nil {
		return nil, fmt.Errorf("database is not configured")
	}
	status = strings.TrimSpace(status)
	if status != "" {
		if _, ok := adminRetryItemStatuses[status]; !ok {
			return nil, fmt.Errorf("不支持的重试状态")
		}
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := `
SELECT id, operation_id, item_key, kind, chat_id, payload_json, failure_reason, status, attempt_count, COALESCE(created_at, ''), COALESCE(updated_at, '')
FROM admin_retry_items`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.raw.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminRetryItem{}
	for rows.Next() {
		var item AdminRetryItem
		if err := rows.Scan(&item.ID, &item.OperationID, &item.ItemKey, &item.Kind, &item.ChatID, &item.PayloadJSON, &item.FailureReason, &item.Status, &item.AttemptCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (d *Database) UpdateAdminRetryItem(id int64, status, failureReason string, incrementAttempt bool) (bool, error) {
	if d == nil || d.raw == nil {
		return false, fmt.Errorf("database is not configured")
	}
	status = strings.TrimSpace(status)
	if id == 0 {
		return false, fmt.Errorf("retry item id is required")
	}
	if _, ok := adminRetryItemStatuses[status]; !ok {
		return false, fmt.Errorf("不支持的重试状态")
	}
	query := `UPDATE admin_retry_items SET status = ?, failure_reason = ?, updated_at = CURRENT_TIMESTAMP`
	args := []any{status, strings.TrimSpace(failureReason)}
	if incrementAttempt {
		query += `, attempt_count = attempt_count + 1`
	}
	query += ` WHERE id = ?`
	args = append(args, id)
	result, err := d.raw.Exec(query, args...)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}
