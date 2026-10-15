package bot

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBookkeepingRequiresRestartForNewCycle(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "daily-bookkeeping-gate.db")

	const (
		chatID  int64 = -1005522001
		ownerID int64 = 5522001
	)
	if _, err := database.EnsureGroup(chatID, "每日开始记账门禁测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		5522001, 5522001, chatID, ownerID, "owner_daily_gate", "开始记账", baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		5522002, 5522002, chatID, ownerID, "owner_daily_gate", "+100#首笔", baseDate+1,
	))

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 1 {
		t.Fatalf("开始记账后开关应为开启: got=%d", group.BookkeepingEnabled)
	}
	if group.BookkeepingCycleKey == "" {
		t.Fatalf("开始记账后周期键不应为空")
	}
	expectedCycle := app.currentBookkeepingCycleKey(group.DayCutTime)
	if group.BookkeepingCycleKey != expectedCycle {
		t.Fatalf("开始记账应写入当前周期: want=%q got=%q", expectedCycle, group.BookkeepingCycleKey)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 5522002, "当前入款:") {
		t.Fatalf("同周期应允许记账: %+v", snapshot)
	}

	staleCycle, err := shiftBookkeepingCycleKey(group.BookkeepingCycleKey, -24*time.Hour)
	if err != nil {
		t.Fatalf("构造过期周期失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"bookkeeping_enabled":   1,
		"bookkeeping_cycle_key": staleCycle,
	}); err != nil {
		t.Fatalf("写入过期周期失败: %v", err)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		5522003, 5522003, chatID, ownerID, "owner_daily_gate", "+200#跨日未开始", baseDate+2,
	))

	snapshot = capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 5522003, bookkeepingNotStartedTip) {
		t.Fatalf("跨周期未开始记账应被拦截: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 5522003, "当前入款:") {
		t.Fatalf("跨周期未开始记账不应继续记账并发送账单: %+v", snapshot)
	}

	group, err = database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取跨周期拦截后群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 0 {
		t.Fatalf("跨周期拦截后应回收记账开关: got=%d", group.BookkeepingEnabled)
	}
	if group.BookkeepingCycleKey != "" {
		t.Fatalf("跨周期拦截后应清空周期键: got=%q", group.BookkeepingCycleKey)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		5522004, 5522004, chatID, ownerID, "owner_daily_gate", "开始记账", baseDate+3,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		5522005, 5522005, chatID, ownerID, "owner_daily_gate", "+300#重新开始", baseDate+4,
	))

	snapshot = capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 5522004, "已开启记账。") {
		t.Fatalf("重新开始记账回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 5522005, "当前入款:") {
		t.Fatalf("重新开始后应恢复记账: %+v", snapshot)
	}
}

func shiftBookkeepingCycleKey(key string, offset time.Duration) (string, error) {
	const layout = "2006-01-02T15:04:05"
	ts, err := time.ParseInLocation(layout, key, time.UTC)
	if err != nil {
		return "", err
	}
	return ts.Add(offset).Format(layout), nil
}

func TestStartBookkeepingReplyUsesBeautifiedCard(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "start-bookkeeping-template.db")

	const (
		chatID  int64 = -1005522101
		ownerID int64 = 5522101
	)
	if _, err := database.EnsureGroup(chatID, "开始记账模板测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"fee_rate":        7.0,
		"custom_rate":     117.0,
		"payout_fee_rate": 0.0,
		"payout_rate":     0.0,
		"day_cut_time":    "04:00:00",
	}); err != nil {
		t.Fatalf("写入群账单设置失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		5522101, 5522101, chatID, ownerID, "owner_start_tpl", "开始记账", baseDate,
	))

	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, chatID, 5522101, "已开启记账。")
	if !ok {
		t.Fatalf("开始记账卡片回执缺失: %+v", snapshot)
	}
	if reply.parseMode != telegramParseModeMarkdownV {
		t.Fatalf("开始记账应返回 MarkdownV2 卡片，实际 parseMode=%q，消息=%+v", reply.parseMode, reply)
	}
	wantFragments := []string{
		"已开启记账。",
		"账单已经开始了，请继续记账：",
		"今日入款 \\(`0`\\) 笔:",
		"今日下发 \\(`0`\\) 笔:",
		"入款费率: `7`%",
		"入款汇率: `117`",
		"应下发: `0.00` U",
		"未下发: `0.00` U",
	}
	for _, fragment := range wantFragments {
		if !strings.Contains(reply.text, fragment) {
			t.Fatalf("开始记账卡片缺少片段 %q: %+v", fragment, reply)
		}
	}
	for _, fragment := range []string{"下发费率", "下发汇率"} {
		if strings.Contains(reply.text, fragment) {
			t.Fatalf("开始记账卡片不应展示片段 %q: %+v", fragment, reply)
		}
	}
	if strings.Contains(reply.text, "当前账单设置信息：") {
		t.Fatalf("开始记账不应回退纯文本模板: %+v", reply)
	}
}
