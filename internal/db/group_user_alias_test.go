package db

import (
	"path/filepath"
	"testing"
)

func TestGroupUserAliasLookupWithinChat(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "group-user-alias.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-1001001)
	userID := int64(9001)
	if _, err := database.EnsureGroup(chatID, "测试群"); err != nil {
		t.Fatalf("初始化群失败: %v", err)
	}

	if _, _, err := database.UpsertGroupUser(chatID, userID, "new_name", "Nova"); err != nil {
		t.Fatalf("写入当前快照失败: %v", err)
	}
	if err := database.UpsertGroupUserAlias(userID, "old_name", chatID); err != nil {
		t.Fatalf("写入历史 alias 失败: %v", err)
	}

	row, err := database.FindUserByAlias(chatID, "old_name")
	if err != nil {
		t.Fatalf("按群查 alias 失败: %v", err)
	}
	if row == nil || row.UserID != userID || row.Username != "new_name" {
		t.Fatalf("alias 命中异常: %+v", row)
	}
}

func TestGroupUserAliasLookupAcrossChatsReturnsLatestSnapshot(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "group-user-alias-global.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	sourceChatID := int64(-1002001)
	targetChatID := int64(-1002002)
	userID := int64(9002)

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
		t.Fatalf("写入历史 alias 失败: %v", err)
	}

	row, err := database.FindAnyUserByAlias("legacy_name")
	if err != nil {
		t.Fatalf("全局查 alias 失败: %v", err)
	}
	if row == nil || row.UserID != userID || row.ChatID != sourceChatID {
		t.Fatalf("全局 alias 命中异常: %+v", row)
	}
}
