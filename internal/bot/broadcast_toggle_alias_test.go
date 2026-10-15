package bot

import (
	"context"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestBroadcastToggleAliasCommand(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "broadcast-toggle-alias.db")
	chatID := int64(-100552001)
	ownerID := int64(552001)
	if _, err := database.EnsureGroup(chatID, "群发开关别名测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	updates := []tgbotapi.Update{
		// 原有命令
		fourLayerGroupTextUpdate(552101, 552101, chatID, ownerID, "owner_alias", "开启@群发信息", baseDate),
		fourLayerGroupTextUpdate(552102, 552102, chatID, ownerID, "owner_alias", "关闭@群发信息", baseDate+1),
		// 新增别名
		fourLayerGroupTextUpdate(552103, 552103, chatID, ownerID, "owner_alias", "开启群发功能", baseDate+2),
		fourLayerGroupTextUpdate(552104, 552104, chatID, ownerID, "owner_alias", "关闭群发功能", baseDate+3),
	}
	for _, upd := range updates {
		app.dispatchUpdate(context.Background(), upd)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 552101, "开始成功，请输入群发内容。") {
		t.Fatalf("原有开启命令回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 552102, "群发信息已关闭。") {
		t.Fatalf("原有关闭命令回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 552103, "开始成功，请输入群发内容。") {
		t.Fatalf("开启群发功能别名回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 552104, "群发信息已关闭。") {
		t.Fatalf("关闭群发功能别名回执缺失: %+v", snapshot)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.AtBroadcastEnabled != 0 {
		t.Fatalf("群发开关应为关闭状态: %+v", group)
	}
}
