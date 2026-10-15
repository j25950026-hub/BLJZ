package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestBatchSetGroupTagsSelectionPagingAndApply(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "batch-group-tags-selection.db")
	sourceChatID := int64(-100772001)
	ownerID := int64(772001)

	groupIDs := []int64{
		sourceChatID,
		-100772002,
		-100772003,
		-100772004,
		-100772005,
		-100772006,
		-100772007,
		-100772008,
		-100772009,
		-100772010,
		-100772011,
		-100772012,
		-100772013,
		-100772014,
		-100772015,
	}
	for i, chatID := range groupIDs {
		if _, err := database.EnsureGroup(chatID, fmt.Sprintf("批量标签群-%02d", i+1)); err != nil {
			t.Fatalf("初始化群失败(chat=%d): %v", chatID, err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置权限人失败(chat=%d): %v", chatID, err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{"group_tags": "旧标签"}); err != nil {
			t.Fatalf("初始化群标签失败(chat=%d): %v", chatID, err)
		}
	}

	app.isGroupMemberFn = func(chatID, userID int64) bool {
		return true
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		772101, 772101, sourceChatID, ownerID, "owner_batch", "批量设置群标签", baseDate,
	))

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, sourceChatID, 772101, "批量设置群标签（可设置群列表）") {
		t.Fatalf("缺少批量设置入口回执: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 772101, "页码：1/2（每页10群）") {
		t.Fatalf("分页信息异常: %+v", snapshot)
	}

	session, ok := app.getBatchTagSession(sourceChatID)
	if !ok {
		t.Fatalf("应存在批量标签会话")
	}
	if len(session.Groups) != len(groupIDs) {
		t.Fatalf("会话群数量异常: got=%d want=%d", len(session.Groups), len(groupIDs))
	}
	selectedChatID := session.Groups[11].ChatID // 全局序号12对应的群

	callback := &tgbotapi.CallbackQuery{
		ID:   "batch-bg-1",
		Data: buildBatchTagPageCallbackData(1),
		From: &tgbotapi.User{
			ID:       ownerID,
			UserName: "owner_batch",
		},
		Message: &tgbotapi.Message{
			MessageID: 900101,
			Chat: &tgbotapi.Chat{
				ID:    sourceChatID,
				Type:  "supergroup",
				Title: "批量标签测试群",
			},
		},
	}
	if err := app.handleCallbackQuery(context.Background(), callback); err != nil {
		t.Fatalf("翻页回调失败: %v", err)
	}

	if !fourLayerHasReply(capture.snapshot(), sourceChatID, 900101, "页码：2/2（每页10群）") {
		t.Fatalf("未刷新到第2页: %+v", capture.snapshot())
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		772102, 772102, sourceChatID, ownerID, "owner_batch", "12 38 52 cs2", baseDate+1,
	))

	snapshot = capture.snapshot()
	if !fourLayerHasReply(snapshot, sourceChatID, 772102, "批量设置群标签结果：") {
		t.Fatalf("缺少执行回执: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 772102, "输入序号：3") {
		t.Fatalf("输入序号统计异常: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 772102, "有效序号：1") {
		t.Fatalf("有效序号统计异常: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 772102, "无效序号：2") {
		t.Fatalf("无效序号统计异常: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 772102, "设置成功：1") {
		t.Fatalf("成功数量统计异常: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 772102, "无效项：38、52") {
		t.Fatalf("无效项回执异常: %+v", snapshot)
	}

	selectedGroup, err := database.GetGroup(selectedChatID)
	if err != nil {
		t.Fatalf("读取选中群失败: %v", err)
	}
	if got := strings.TrimSpace(selectedGroup.GroupTags); got != "cs2" {
		t.Fatalf("选中群标签未更新: got=%q", got)
	}

	unchangedChatID := session.Groups[0].ChatID
	unchangedGroup, err := database.GetGroup(unchangedChatID)
	if err != nil {
		t.Fatalf("读取未选中群失败: %v", err)
	}
	if got := strings.TrimSpace(unchangedGroup.GroupTags); got != "旧标签" {
		t.Fatalf("未选中群标签应保持旧值: got=%q", got)
	}
}

func TestBatchSetGroupTagsOnlyIncludeBotMemberGroups(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "batch-group-tags-member-filter.db")
	sourceChatID := int64(-100773001)
	targetA := int64(-100773002)
	targetB := int64(-100773003)
	ownerID := int64(773001)

	for _, chatID := range []int64{sourceChatID, targetA, targetB} {
		if _, err := database.EnsureGroup(chatID, "批量标签过滤群"); err != nil {
			t.Fatalf("初始化群失败(chat=%d): %v", chatID, err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置权限人失败(chat=%d): %v", chatID, err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{"group_tags": "旧标签"}); err != nil {
			t.Fatalf("初始化群标签失败(chat=%d): %v", chatID, err)
		}
	}

	app.isGroupMemberFn = func(chatID, userID int64) bool {
		return chatID == sourceChatID || chatID == targetA
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		773101, 773101, sourceChatID, ownerID, "owner_batch", "批量设置群标签", baseDate,
	))

	session, ok := app.getBatchTagSession(sourceChatID)
	if !ok {
		t.Fatalf("应存在批量标签会话")
	}
	if len(session.Groups) != 2 {
		t.Fatalf("会话应仅包含机器人在群的目标: got=%d", len(session.Groups))
	}

	applyText := "1 2 cs9"
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		773102, 773102, sourceChatID, ownerID, "owner_batch", applyText, baseDate+1,
	))

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, sourceChatID, 773102, "设置成功：2") {
		t.Fatalf("批量设置成功统计异常: %+v", snapshot)
	}

	gA, err := database.GetGroup(targetA)
	if err != nil {
		t.Fatalf("读取targetA失败: %v", err)
	}
	if got := strings.TrimSpace(gA.GroupTags); got != "cs9" {
		t.Fatalf("targetA标签未更新: got=%q", got)
	}
	gB, err := database.GetGroup(targetB)
	if err != nil {
		t.Fatalf("读取targetB失败: %v", err)
	}
	if got := strings.TrimSpace(gB.GroupTags); got != "旧标签" {
		t.Fatalf("targetB应被过滤保持旧值: got=%q", got)
	}
}
