package bot

import (
	"context"
	"testing"
	"time"
)

func TestMemberOnlyCalculatorCommandAllowed(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-calculator-only.db")

	const (
		chatID   int64 = -1004488801
		ownerID  int64 = 4488801
		memberID int64 = 4488899
	)
	if _, err := database.EnsureGroup(chatID, "普通成员计算器权限测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		4488801, 4488801, chatID, memberID, "member_calc", "1+2*3", baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		4488802, 4488802, chatID, memberID, "member_calc", "自动回复列表", baseDate+1,
	))

	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, chatID, 4488801, "计算结果：")
	if !ok {
		t.Fatalf("普通成员计算器指令未通过: %+v", snapshot)
	}
	if reply.text != "计算结果：<code>7</code>" {
		t.Fatalf("普通成员计算器结果文案错误: got=%q reply=%+v", reply.text, reply)
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("普通成员计算器结果应使用 HTML parse mode: got=%q reply=%+v", reply.parseMode, reply)
	}
	if fourLayerHasAnyReply(snapshot, chatID, 4488802) {
		t.Fatalf("普通成员无权限命令应静默，不应回执: %+v", snapshot)
	}
}

func TestPrivilegedCalculatorCommandUsesHTMLCodeReply(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "privileged-calculator-html.db")

	const (
		chatID  int64 = -1004488821
		ownerID int64 = 4488821
		msgID         = 4488822
	)
	if _, err := database.EnsureGroup(chatID, "权限人计算器样式测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		msgID, msgID, chatID, ownerID, "owner_calc", "10+20", baseDate,
	))

	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, chatID, msgID, "计算结果：")
	if !ok {
		t.Fatalf("权限人计算器指令未返回结果: %+v", snapshot)
	}
	if reply.text != "计算结果：<code>30</code>" {
		t.Fatalf("权限人计算器结果文案错误: got=%q reply=%+v", reply.text, reply)
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("权限人计算器结果应使用 HTML parse mode: got=%q reply=%+v", reply.parseMode, reply)
	}
}

func TestPrivilegedSignedDivisionUsesCalculatorBeforeLedger(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "privileged-signed-division-calculator.db")

	const (
		chatID  int64 = -1004488861
		ownerID int64 = 4488861
		msgID         = 4488863
	)
	if _, err := database.EnsureGroup(chatID, "权限人负数除法计算器测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		4488862, 4488862, chatID, ownerID, "owner_calc", "开始记账", baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		msgID, msgID, chatID, ownerID, "owner_calc", "-1/26000", baseDate+1,
	))

	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, chatID, msgID, "计算结果：")
	if !ok {
		t.Fatalf("权限人负数除法应返回计算结果: %+v", snapshot)
	}
	if reply.text != "计算结果：<code>-0.000038</code>" {
		t.Fatalf("权限人负数除法计算结果文案错误: got=%q reply=%+v", reply.text, reply)
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("权限人负数除法计算结果应使用 HTML parse mode: got=%q reply=%+v", reply.parseMode, reply)
	}
	rows, err := database.ListLedgerSince(chatID, "")
	if err != nil {
		t.Fatalf("查询账单失败: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("负数除法不应写入账单: %+v", rows)
	}
}

func TestPrivilegedPlainNumberDoesNotTriggerCalculator(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "privileged-plain-number-no-calculator.db")

	const (
		chatID  int64 = -1004488841
		ownerID int64 = 4488841
		msgID         = 4488842
	)
	if _, err := database.EnsureGroup(chatID, "权限人纯数字静默测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		msgID, msgID, chatID, ownerID, "owner_plain_number", "1", baseDate,
	))

	snapshot := capture.snapshot()
	if fourLayerHasReply(snapshot, chatID, msgID, "计算结果：") {
		t.Fatalf("权限人纯数字不应触发计算器: %+v", snapshot)
	}
}

func TestMemberPlainTextSilence(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-plain-text-silence.db")

	const (
		chatID   int64 = -1004488811
		ownerID  int64 = 4488811
		memberID int64 = 4488899
		msgID          = 4488812
	)
	if _, err := database.EnsureGroup(chatID, "普通成员静默测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		msgID, msgID, chatID, memberID, "member_plain", "Today commission already paid out.", baseDate,
	))

	snapshot := capture.snapshot()
	if fourLayerHasAnyReply(snapshot, chatID, msgID) {
		t.Fatalf("普通文案应静默，不应回执: %+v", snapshot)
	}
}

func TestMemberPlainNumberDoesNotTriggerCalculator(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-plain-number-no-calculator.db")

	const (
		chatID   int64 = -1004488831
		ownerID  int64 = 4488831
		memberID int64 = 4488899
		msgID          = 4488832
	)
	if _, err := database.EnsureGroup(chatID, "普通成员纯数字静默测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		msgID, msgID, chatID, memberID, "member_plain_number", "120", baseDate,
	))

	snapshot := capture.snapshot()
	if fourLayerHasAnyReply(snapshot, chatID, msgID) {
		t.Fatalf("普通成员纯数字应静默，不应回执: %+v", snapshot)
	}
}
