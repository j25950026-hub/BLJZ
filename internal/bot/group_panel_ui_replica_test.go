package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestGroupPanelMarkupIncludesReplicaActions(t *testing.T) {
	app := &App{}
	markup := app.groupPanelMarkup(dbGroupConfigForPanelTest())
	dataSet := flattenInlineCallbackData(markup)
	expected := []string{
		groupPanelCallbackPrefix + "start",
		groupPanelCallbackPrefix + "stop",
		groupPanelCallbackPrefix + "rate_onekey",
		groupPanelCallbackPrefix + "bill",
		groupPanelCallbackPrefix + "broadcast",
		groupPanelCallbackPrefix + "tag_set",
		groupPanelCallbackPrefix + "type_set",
		groupPanelCallbackPrefix + "batch_tag_set",
		groupPanelCallbackPrefix + "batch_type_set",
		groupPanelCallbackPrefix + "closing_enable",
		groupPanelCallbackPrefix + "closing_disable",
		groupPanelCallbackPrefix + "closing_time",
		groupPanelCallbackPrefix + "closing_query",
		groupPanelCallbackPrefix + "type_stats",
		groupPanelCallbackPrefix + "single_stats",
		groupPanelCallbackPrefix + "order_list",
		groupPanelCallbackPrefix + "schedule_list",
		groupPanelCallbackPrefix + "auto_list",
		groupPanelCallbackPrefix + "forward_list",
		groupPanelCallbackPrefix + "custom_list",
		groupPanelCallbackPrefix + "panel",
	}
	for _, item := range expected {
		if !dataSet[item] {
			t.Fatalf("群控面板缺少回调按钮: %s", item)
		}
	}
}

func TestGroupPanelTextShowsPermissionBoundary(t *testing.T) {
	app := &App{}
	text := app.groupPanelText(dbGroupConfigForPanelTest())
	for _, want := range []string{
		"仅机器人系统权限人/操作人可设置",
		"不要求 Telegram 管理员",
		"机器人必须在目标群内",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("群控面板缺少权限边界 %q: %q", want, text)
		}
	}
}

func TestGroupPanelClosingReminderCallbacks(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "group-panel-closing-reminder.db")
	chatID := int64(-1004410005)
	ownerID := int64(4410005)
	if _, err := database.EnsureGroup(chatID, "下课提醒按钮测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	enable := groupPanelTestCallback("closing-enable", groupPanelCallbackPrefix+"closing_enable", chatID, ownerID, 442001)
	if err := app.handleCallbackQuery(context.Background(), enable); err != nil {
		t.Fatalf("开启下课提醒按钮回调失败: %v", err)
	}
	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败: %v", err)
	}
	if config.Enabled != 1 {
		t.Fatalf("开启下课提醒按钮应写入 Enabled=1: %+v", config)
	}
	if !fourLayerHasReply(capture.snapshot(), chatID, 442001, "下课提醒已开启") {
		t.Fatalf("开启下课提醒回执缺失: %+v", capture.snapshot())
	}
	if !fourLayerHasCallbackAnswer(capture.answerSnapshot(), "closing-enable", "已开启下课提醒") {
		t.Fatalf("开启下课提醒 callback 回执缺失: %+v", capture.answerSnapshot())
	}

	setTime := groupPanelTestCallback("closing-time", groupPanelCallbackPrefix+"closing_time", chatID, ownerID, 442002)
	if err := app.handleCallbackQuery(context.Background(), setTime); err != nil {
		t.Fatalf("设置下课时间按钮回调失败: %v", err)
	}
	if !fourLayerHasReply(capture.snapshot(), chatID, 442002, "格式：设置下课时间04:00") {
		t.Fatalf("设置下课时间按钮应提示命令格式: %+v", capture.snapshot())
	}
	if !fourLayerHasCallbackAnswer(capture.answerSnapshot(), "closing-time", "请发送设置下课时间04:00") {
		t.Fatalf("设置下课时间 callback 回执缺失: %+v", capture.answerSnapshot())
	}
}

func TestSetGroupTagAndTypeRequirePayload(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "group-panel-ui-replica.db")
	chatID := int64(-1004410001)
	ownerID := int64(4410001)
	if _, err := database.EnsureGroup(chatID, "界面复刻测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	updates := []tgbotapi.Update{
		fourLayerGroupTextUpdate(441001, 441001, chatID, ownerID, "owner_ui", "设置群标签", baseDate),
		fourLayerGroupTextUpdate(441002, 441002, chatID, ownerID, "owner_ui", "设置群类型", baseDate+1),
	}
	for _, upd := range updates {
		app.dispatchUpdate(context.Background(), upd)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 441001, "格式：设置群标签 标签1,标签2") {
		t.Fatalf("设置群标签空参数格式提示缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 441002, "格式：设置群类型 资金盘/担保盘/代收代付") {
		t.Fatalf("设置群类型空参数格式提示缺失: %+v", snapshot)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.GroupTags != "" {
		t.Fatalf("空参数不应覆盖群标签: got=%q", group.GroupTags)
	}
	if group.GroupType != "" {
		t.Fatalf("空参数不应覆盖群类型: got=%q", group.GroupType)
	}
}

func TestShowGroupTagsCommand(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "group-panel-show-tags.db")
	chatID := int64(-1004410002)
	ownerID := int64(4410002)
	if _, err := database.EnsureGroup(chatID, "群标签显示测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"group_tags": "ICF,夜班"}); err != nil {
		t.Fatalf("预置群标签失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	updates := []tgbotapi.Update{
		fourLayerGroupTextUpdate(441101, 441101, chatID, ownerID, "owner_ui", "显示群标签", baseDate),
		fourLayerGroupTextUpdate(441102, 441102, chatID, ownerID, "owner_ui", "查看群标签", baseDate+1),
	}
	for _, upd := range updates {
		app.dispatchUpdate(context.Background(), upd)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 441101, "当前群标签：ICF、夜班") {
		t.Fatalf("显示群标签回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 441102, "当前群标签：ICF、夜班") {
		t.Fatalf("查看群标签回执缺失: %+v", snapshot)
	}

	if err := database.UpdateGroupFields(chatID, map[string]any{"group_tags": ""}); err != nil {
		t.Fatalf("清空群标签失败: %v", err)
	}
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(441103, 441103, chatID, ownerID, "owner_ui", "本群标签", baseDate+2))
	snapshot = capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 441103, "当前群标签：未设置") {
		t.Fatalf("本群标签未设置回执缺失: %+v", snapshot)
	}
}

func TestClearGroupTagsCommand(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "group-panel-clear-tags.db")
	chatID := int64(-1004410003)
	ownerID := int64(4410003)
	if _, err := database.EnsureGroup(chatID, "群标签清除测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"group_tags": "ICF,夜班"}); err != nil {
		t.Fatalf("预置群标签失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	updates := []tgbotapi.Update{
		fourLayerGroupTextUpdate(441201, 441201, chatID, ownerID, "owner_ui", "恢复默认群标签", baseDate),
		fourLayerGroupTextUpdate(441202, 441202, chatID, ownerID, "owner_ui", "显示群标签", baseDate+1),
	}
	for _, upd := range updates {
		app.dispatchUpdate(context.Background(), upd)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 441201, "群标签已清除（默认）。") {
		t.Fatalf("清除群标签回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 441202, "当前群标签：未设置") {
		t.Fatalf("清除后显示群标签回执缺失: %+v", snapshot)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.GroupTags != "" {
		t.Fatalf("群标签应已清空: got=%q", group.GroupTags)
	}
}

func TestLegacyClearGroupTagsAliasIgnored(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "group-panel-clear-tags-legacy.db")
	chatID := int64(-1004410004)
	ownerID := int64(4410004)
	if _, err := database.EnsureGroup(chatID, "群标签旧别名测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"group_tags": "ICF,夜班"}); err != nil {
		t.Fatalf("预置群标签失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(441301, 441301, chatID, ownerID, "owner_ui", "清空群标签", baseDate))

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.GroupTags != "ICF,夜班" {
		t.Fatalf("旧别名不应生效: got=%q", group.GroupTags)
	}
}

func flattenInlineCallbackData(markup tgbotapi.InlineKeyboardMarkup) map[string]bool {
	out := make(map[string]bool)
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if button.CallbackData == nil {
				continue
			}
			out[*button.CallbackData] = true
		}
	}
	return out
}

func dbGroupConfigForPanelTest() db.GroupConfig {
	return db.GroupConfig{
		BookkeepingEnabled:  1,
		BookkeepingCycleKey: "2026-02-24T20:00:00",
		ShowUSDTConversion:  1,
		AutoReplyEnabled:    1,
	}
}

func groupPanelTestCallback(queryID, data string, chatID, fromID int64, messageID int) *tgbotapi.CallbackQuery {
	return &tgbotapi.CallbackQuery{
		ID:   queryID,
		Data: data,
		From: &tgbotapi.User{
			ID:        fromID,
			UserName:  "owner_ui",
			FirstName: "owner_ui",
		},
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "下课提醒按钮测试群",
			},
		},
	}
}
