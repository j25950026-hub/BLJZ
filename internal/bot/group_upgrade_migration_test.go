package bot

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestHandleGroupUpgradeMigrationFromServiceMessage(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "group-upgrade-migration.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	const (
		oldChatID = int64(-5266232385)
		newChatID = int64(-1003758576179)
		ownerID   = int64(8114441184)
	)
	if _, err := database.EnsureGroup(oldChatID, "旧群"); err != nil {
		t.Fatalf("初始化旧群失败: %v", err)
	}
	if _, err := database.EnsureGroup(newChatID, "新群"); err != nil {
		t.Fatalf("初始化新群失败: %v", err)
	}
	if err := database.UpdateGroupFields(oldChatID, map[string]any{
		"owner_user_id":           ownerID,
		"group_type":              "CS",
		"duplicate_alert_enabled": 1,
		"group_tags":              "CS,ICF",
	}); err != nil {
		t.Fatalf("写入旧群配置失败: %v", err)
	}

	app := &App{db: database}
	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{
			ID:    newChatID,
			Type:  "supergroup",
			Title: "The Indian community in Cambodia",
		},
		MigrateFromChatID: oldChatID,
	}

	handled, err := app.handleGroupUpgradeMigration(msg)
	if err != nil {
		t.Fatalf("处理群升级迁移消息失败: %v", err)
	}
	if !handled {
		t.Fatalf("迁移事件应被识别为 handled")
	}

	newGroup, err := database.GetGroup(newChatID)
	if err != nil {
		t.Fatalf("读取新群配置失败: %v", err)
	}
	if newGroup.OwnerUserID != ownerID {
		t.Fatalf("迁移后权限人异常: got=%d want=%d", newGroup.OwnerUserID, ownerID)
	}
	if newGroup.GroupType != "CS" || newGroup.DuplicateAlertEnabled != 1 {
		t.Fatalf("迁移后群配置异常: %+v", newGroup)
	}

	_, err = database.GetGroup(oldChatID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("旧群配置应已迁移并移除，实际 err=%v", err)
	}
}
