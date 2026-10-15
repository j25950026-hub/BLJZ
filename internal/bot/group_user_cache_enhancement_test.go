package bot

import (
	"path/filepath"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestExtractTargetUsersFallbackToGlobalAliasCacheAndBackfillsSnapshot(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "target-users-alias.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	sourceChatID := int64(-1003001)
	targetChatID := int64(-1003002)
	userID := int64(88001)

	if _, err := database.EnsureGroup(sourceChatID, "源群"); err != nil {
		t.Fatalf("初始化源群失败: %v", err)
	}
	if _, err := database.EnsureGroup(targetChatID, "目标群"); err != nil {
		t.Fatalf("初始化目标群失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(sourceChatID, userID, "current_name", "Nova"); err != nil {
		t.Fatalf("写入当前快照失败: %v", err)
	}
	if err := database.UpsertGroupUserAlias(userID, "legacy_name", sourceChatID); err != nil {
		t.Fatalf("写入 alias 失败: %v", err)
	}

	app := &App{db: database}
	app.isGroupMemberFn = func(chatID, uid int64) bool {
		return chatID == targetChatID && uid == userID
	}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{
			ID:   targetChatID,
			Type: "supergroup",
		},
	}
	targets, err := app.extractTargetUsers(msg, "设置身份合作伙伴 @legacy_name")
	if err != nil {
		t.Fatalf("提取目标失败: %v", err)
	}
	if len(targets) != 1 || targets[0].UserID != userID {
		t.Fatalf("命中目标异常: %+v", targets)
	}
	if targets[0].Username != "current_name" {
		t.Fatalf("命中用户名异常: want=current_name got=%s", targets[0].Username)
	}

	row, err := database.FindUserByUsername(targetChatID, "current_name")
	if err != nil {
		t.Fatalf("查询回填失败: %v", err)
	}
	if row == nil || row.UserID != userID {
		t.Fatalf("目标群快照未回填: %+v", row)
	}
}

func TestExtractTargetUsersResolvesAliasWithinChatAndBackfillsSnapshot(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "target-users-alias-within-chat.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-1003005)
	userID := int64(88003)

	if _, err := database.EnsureGroup(chatID, "测试群"); err != nil {
		t.Fatalf("初始化测试群失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(chatID, userID, "current_name", "Nova"); err != nil {
		t.Fatalf("写入当前快照失败: %v", err)
	}
	if err := database.UpsertGroupUserAlias(userID, "legacy_name", chatID); err != nil {
		t.Fatalf("写入 alias 失败: %v", err)
	}

	app := &App{db: database}
	app.isGroupMemberFn = func(checkChatID, checkUserID int64) bool {
		return checkChatID == chatID && checkUserID == userID
	}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{
			ID:   chatID,
			Type: "supergroup",
		},
	}
	targets, err := app.extractTargetUsers(msg, "设置身份合作伙伴 @legacy_name")
	if err != nil {
		t.Fatalf("提取目标失败: %v", err)
	}
	if len(targets) != 1 || targets[0].UserID != userID {
		t.Fatalf("命中目标异常: %+v", targets)
	}
	if targets[0].Username != "current_name" {
		t.Fatalf("命中用户名异常: want=current_name got=%s", targets[0].Username)
	}
}

func TestExtractTargetUsersRejectsGlobalAliasWhenUserNotInChat(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "target-users-alias-reject.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	sourceChatID := int64(-1003003)
	targetChatID := int64(-1003004)
	userID := int64(88002)

	if _, err := database.EnsureGroup(sourceChatID, "源群"); err != nil {
		t.Fatalf("初始化源群失败: %v", err)
	}
	if _, err := database.EnsureGroup(targetChatID, "目标群"); err != nil {
		t.Fatalf("初始化目标群失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(sourceChatID, userID, "current_name", "Atlas"); err != nil {
		t.Fatalf("写入当前快照失败: %v", err)
	}
	if err := database.UpsertGroupUserAlias(userID, "legacy_name", sourceChatID); err != nil {
		t.Fatalf("写入 alias 失败: %v", err)
	}

	app := &App{db: database}
	app.isGroupMemberFn = func(chatID, uid int64) bool { return false }

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{
			ID:   targetChatID,
			Type: "supergroup",
		},
	}
	targets, err := app.extractTargetUsers(msg, "设置操作人 @legacy_name")
	if err != nil {
		t.Fatalf("提取目标失败: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("不应命中已不在群内的历史 alias: %+v", targets)
	}

	row, err := database.FindUserByUsername(targetChatID, "current_name")
	if err != nil {
		t.Fatalf("查询目标群回填缓存失败: %v", err)
	}
	if row != nil {
		t.Fatalf("不应回填不在群内的成员: %+v", row)
	}
}
