package bot

import (
	"path/filepath"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestExtractTargetUsersFallbackToGlobalUsernameCache(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "target-users.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	app := &App{db: database}

	targetChatID := int64(-100770001)
	sourceChatID := int64(-100770002)
	targetUserID := int64(770099)
	targetUsername := "a8668123"

	if _, err := database.EnsureGroup(targetChatID, "目标群"); err != nil {
		t.Fatalf("初始化目标群失败: %v", err)
	}
	if _, err := database.EnsureGroup(sourceChatID, "源群"); err != nil {
		t.Fatalf("初始化源群失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(sourceChatID, targetUserID, targetUsername, "Nova Cashier"); err != nil {
		t.Fatalf("写入源群用户缓存失败: %v", err)
	}
	app.isGroupMemberFn = func(chatID, userID int64) bool {
		return chatID == targetChatID && userID == targetUserID
	}

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{
			ID:   targetChatID,
			Type: "supergroup",
		},
	}

	targets, err := app.extractTargetUsers(msg, "设置操作人 @a8668123")
	if err != nil {
		t.Fatalf("提取目标用户失败: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("目标用户数量异常: want=1 got=%d", len(targets))
	}
	if targets[0].UserID != targetUserID {
		t.Fatalf("目标用户ID异常: want=%d got=%d", targetUserID, targets[0].UserID)
	}
	if targets[0].Username != targetUsername {
		t.Fatalf("目标用户名异常: want=%s got=%s", targetUsername, targets[0].Username)
	}

	row, err := database.FindUserByUsername(targetChatID, targetUsername)
	if err != nil {
		t.Fatalf("查询目标群回填缓存失败: %v", err)
	}
	if row == nil {
		t.Fatalf("目标群用户名缓存未回填")
	}
	if row.UserID != targetUserID {
		t.Fatalf("回填缓存用户ID异常: want=%d got=%d", targetUserID, row.UserID)
	}
}

func TestExtractTargetUsersRejectsGlobalUsernameWhenUserNotInChat(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "target-users-reject.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	targetChatID := int64(-100770003)
	sourceChatID := int64(-100770004)
	targetUserID := int64(770100)
	targetUsername := "a8668124"

	if _, err := database.EnsureGroup(targetChatID, "目标群"); err != nil {
		t.Fatalf("初始化目标群失败: %v", err)
	}
	if _, err := database.EnsureGroup(sourceChatID, "源群"); err != nil {
		t.Fatalf("初始化源群失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(sourceChatID, targetUserID, targetUsername, "Nova Cashier"); err != nil {
		t.Fatalf("写入源群用户缓存失败: %v", err)
	}

	app := &App{db: database}
	app.isGroupMemberFn = func(chatID, userID int64) bool { return false }

	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{
			ID:   targetChatID,
			Type: "supergroup",
		},
	}

	targets, err := app.extractTargetUsers(msg, "设置操作人 @a8668124")
	if err != nil {
		t.Fatalf("提取目标用户失败: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("不应命中已不在群内的全局用户名: %+v", targets)
	}

	row, err := database.FindUserByUsername(targetChatID, targetUsername)
	if err != nil {
		t.Fatalf("查询目标群回填缓存失败: %v", err)
	}
	if row != nil {
		t.Fatalf("不应回填不在群内的成员: %+v", row)
	}
}
