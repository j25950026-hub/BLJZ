package db

import (
	"errors"
	"strings"
)

func (d *Database) ListAdminAutoReplies(chatID int64) ([]AutoReply, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListAdminAutoReplies(chatID)
	}
	if chatID == 0 {
		return nil, errors.New("chat_id is required")
	}
	rows, err := d.raw.Query(`
SELECT id, chat_id, COALESCE(keyword, ''), content, COALESCE(enabled, 1), COALESCE(created_at, '')
FROM auto_replies
WHERE chat_id = ?
ORDER BY id ASC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutoReply{}
	for rows.Next() {
		var item AutoReply
		if err := rows.Scan(&item.ID, &item.ChatID, &item.Keyword, &item.Content, &item.Enabled, &item.CreatedAt); err != nil {
			return nil, err
		}
		if item.Enabled != 0 {
			item.Enabled = 1
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Database) GetAdminAutoReply(chatID, ruleID int64) (AutoReply, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetAdminAutoReply(chatID, ruleID)
	}
	if chatID == 0 || ruleID == 0 {
		return AutoReply{}, errors.New("chat_id and rule_id are required")
	}
	var item AutoReply
	err := d.raw.QueryRow(`
SELECT id, chat_id, COALESCE(keyword, ''), content, COALESCE(enabled, 1), COALESCE(created_at, '')
FROM auto_replies
WHERE chat_id = ? AND id = ?`, chatID, ruleID).Scan(
		&item.ID,
		&item.ChatID,
		&item.Keyword,
		&item.Content,
		&item.Enabled,
		&item.CreatedAt,
	)
	if err != nil {
		return AutoReply{}, err
	}
	if item.Enabled != 0 {
		item.Enabled = 1
	}
	return item, nil
}

func (d *Database) CreateAdminAutoReply(chatID int64, keyword, content string, enabled bool) (AutoReply, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.CreateAdminAutoReply(chatID, keyword, content, enabled)
	}
	if chatID == 0 {
		return AutoReply{}, errors.New("chat_id is required")
	}
	keyword = strings.TrimSpace(keyword)
	content = strings.TrimSpace(content)
	if content == "" {
		return AutoReply{}, errors.New("回复内容不能为空")
	}
	result, err := d.raw.Exec(`
INSERT INTO auto_replies (chat_id, keyword, content, enabled)
VALUES (?, ?, ?, ?)`, chatID, keyword, content, adminAutoReplyEnabledInt(enabled))
	if err != nil {
		return AutoReply{}, err
	}
	ruleID, err := result.LastInsertId()
	if err != nil {
		return AutoReply{}, err
	}
	return d.GetAdminAutoReply(chatID, ruleID)
}

func (d *Database) UpdateAdminAutoReplyIfCurrent(chatID, ruleID int64, before, after AutoReply) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.UpdateAdminAutoReplyIfCurrent(chatID, ruleID, before, after)
	}
	if chatID == 0 || ruleID == 0 {
		return false, errors.New("chat_id and rule_id are required")
	}
	result, err := d.raw.Exec(`
UPDATE auto_replies
SET keyword = ?, content = ?, enabled = ?
WHERE chat_id = ? AND id = ?
  AND COALESCE(keyword, '') = ?
  AND COALESCE(content, '') = ?
  AND COALESCE(enabled, 1) = ?`,
		strings.TrimSpace(after.Keyword),
		strings.TrimSpace(after.Content),
		adminAutoReplyEnabledInt(after.Enabled == 1),
		chatID,
		ruleID,
		strings.TrimSpace(before.Keyword),
		before.Content,
		adminAutoReplyEnabledInt(before.Enabled == 1),
	)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func (d *Database) DeleteAdminAutoReplyIfCurrent(chatID, ruleID int64, before AutoReply) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.DeleteAdminAutoReplyIfCurrent(chatID, ruleID, before)
	}
	if chatID == 0 || ruleID == 0 {
		return false, errors.New("chat_id and rule_id are required")
	}
	result, err := d.raw.Exec(`
DELETE FROM auto_replies
WHERE chat_id = ? AND id = ?
  AND COALESCE(keyword, '') = ?
  AND COALESCE(content, '') = ?
  AND COALESCE(enabled, 1) = ?`,
		chatID,
		ruleID,
		strings.TrimSpace(before.Keyword),
		before.Content,
		adminAutoReplyEnabledInt(before.Enabled == 1),
	)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func adminAutoReplyEnabledInt(enabled bool) int {
	if enabled {
		return 1
	}
	return 0
}
