package db

// UpdateGroupWelcomeIfCurrent atomically updates both welcome fields when the previewed state is still current.
func (d *Database) UpdateGroupWelcomeIfCurrent(chatID int64, beforeEnabled int, beforeText string, afterEnabled int, afterText string) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.UpdateGroupWelcomeIfCurrent(chatID, beforeEnabled, beforeText, afterEnabled, afterText)
	}
	res, err := d.raw.Exec(`
UPDATE group_config
SET welcome_enabled = ?, welcome_text = ?, updated_at = CURRENT_TIMESTAMP
WHERE chat_id = ?
  AND COALESCE(welcome_enabled, 0) = ?
  AND COALESCE(welcome_text, '') = ?`,
		afterEnabled,
		afterText,
		chatID,
		beforeEnabled,
		beforeText,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	d.deleteCachedGroupConfig(chatID)
	return true, nil
}
