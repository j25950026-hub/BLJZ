package bot

import "go-bot/internal/db"

// IsAdminClosingReminderTextSafe shares the production group-message safety policy.
func IsAdminClosingReminderTextSafe(text string) bool {
	return closingReminderGroupTextIsSafe(text)
}

// RenderAdminClosingReminderText resolves a custom or default group message without sending it.
func RenderAdminClosingReminderText(config db.GroupClosingReminderConfig, status string) string {
	return closingReminderGroupTextForStatus(config, status)
}
