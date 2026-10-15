package bot

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go-bot/internal/db"
	"go-bot/internal/services"
)

const ledgerBotOfflineE2EChatID int64 = -1004450627223

type ledgerBotOfflineE2EUser struct {
	name string
	id   int64
}

var ledgerBotOfflineE2EUsers = []ledgerBotOfflineE2EUser{
	{name: "user01", id: 8806852408},
	{name: "user02", id: 8889560928},
	{name: "user03", id: 8851587445},
	{name: "user04", id: 8924636421},
	{name: "user05", id: 8699993143},
	{name: "user06", id: 8950631624},
	{name: "user07", id: 8717423356},
	{name: "user08", id: 8861985209},
	{name: "user09", id: 8972835879},
	{name: "user10", id: 8896423197},
}

func TestLedgerBotOfflineE2EGroupBootstrapScenario(t *testing.T) {
	app, database, capture := newLedgerBotOfflineE2EApp(t, "ledgerbot-offline-e2e-bootstrap.db")
	baseDate := int(time.Now().Unix())

	steps := []struct {
		msgID   int
		user    ledgerBotOfflineE2EUser
		text    string
		keyword string
	}{
		{msgID: 860001, user: ledgerBotOfflineE2EUsers[0], text: "显示群ID", keyword: fmt.Sprintf("当前群ID：<code>%d</code>", ledgerBotOfflineE2EChatID)},
		{msgID: 860002, user: ledgerBotOfflineE2EUsers[0], text: "群控面板", keyword: "群控面板"},
		{msgID: 860003, user: ledgerBotOfflineE2EUsers[0], text: "开始记账", keyword: "已开启记账"},
	}
	for i, step := range steps {
		dispatchLedgerBotOfflineE2EText(app, step.user, step.msgID, step.text, baseDate+i)
	}

	snapshot := capture.snapshot()
	groupIDReply, ok := fourLayerFindReply(snapshot, ledgerBotOfflineE2EChatID, 0, steps[0].keyword)
	if !ok {
		t.Fatalf("缺少离线 E2E 群ID回执: keyword=%q snapshot=%+v", steps[0].keyword, snapshot)
	}
	if groupIDReply.parseMode != telegramParseModeHTML {
		t.Fatalf("离线 E2E 群ID回执应使用 HTML，got=%q reply=%+v", groupIDReply.parseMode, groupIDReply)
	}
	for _, step := range steps[1:] {
		requireLedgerBotOfflineE2EReply(t, snapshot, step.msgID, step.keyword)
	}

	group, err := database.GetGroup(ledgerBotOfflineE2EChatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 1 {
		t.Fatalf("开始记账后应开启记账: got=%d", group.BookkeepingEnabled)
	}
	if group.OwnerUserID != ledgerBotOfflineE2EUsers[0].id {
		t.Fatalf("权限人异常: want=%d got=%d", ledgerBotOfflineE2EUsers[0].id, group.OwnerUserID)
	}
}

func TestLedgerBotOfflineE2ELedgerCoreScenario(t *testing.T) {
	app, database, capture := newLedgerBotOfflineE2EApp(t, "ledgerbot-offline-e2e-ledger-core.db")
	baseDate := int(time.Now().Unix())

	steps := []struct {
		msgID   int
		user    ledgerBotOfflineE2EUser
		text    string
		keyword string
	}{
		{msgID: 861001, user: ledgerBotOfflineE2EUsers[0], text: "开始记账", keyword: "已开启记账"},
		{msgID: 861002, user: ledgerBotOfflineE2EUsers[0], text: "设置汇率 7.2", keyword: "入款汇率已设置为 7.2"},
		{msgID: 861003, user: ledgerBotOfflineE2EUsers[1], text: "+100#E2E-deposit", keyword: "当前入款:"},
		{msgID: 861004, user: ledgerBotOfflineE2EUsers[1], text: "-30#E2E-payout", keyword: "当前入款:"},
		{msgID: 861005, user: ledgerBotOfflineE2EUsers[1], text: "一键账单", keyword: "一键账单（日切"},
	}
	for i, step := range steps {
		dispatchLedgerBotOfflineE2EText(app, step.user, step.msgID, step.text, baseDate+i)
	}

	depositTotal, payoutTotal, err := database.GetLedgerBalance(ledgerBotOfflineE2EChatID)
	if err != nil {
		t.Fatalf("查询账务余额失败: %v", err)
	}
	if !almostZero(depositTotal - 100) {
		t.Fatalf("入款总额异常: want=100 got=%v", depositTotal)
	}
	if !almostZero(payoutTotal - 30) {
		t.Fatalf("下发总额异常: want=30 got=%v", payoutTotal)
	}

	group, err := database.GetGroup(ledgerBotOfflineE2EChatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if !almostZero(group.CustomRate - 7.2) {
		t.Fatalf("入款汇率异常: want=7.2 got=%v", group.CustomRate)
	}

	snapshot := capture.snapshot()
	for _, step := range steps {
		requireLedgerBotOfflineE2EReply(t, snapshot, step.msgID, step.keyword)
	}
	if !fourLayerHasReply(snapshot, ledgerBotOfflineE2EChatID, 861005, "入款笔数：1") {
		t.Fatalf("账单缺少入款统计: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, ledgerBotOfflineE2EChatID, 861005, "下发笔数：1") {
		t.Fatalf("账单缺少下发统计: %+v", snapshot)
	}
}

func TestLedgerBotOfflineE2EPermissionMatrixScenario(t *testing.T) {
	app, _, capture := newLedgerBotOfflineE2EApp(t, "ledgerbot-offline-e2e-permission.db")
	baseDate := int(time.Now().Unix())

	steps := []struct {
		msgID   int
		user    ledgerBotOfflineE2EUser
		text    string
		keyword string
		silent  bool
	}{
		{msgID: 862001, user: ledgerBotOfflineE2EUsers[0], text: "显示全局操作人", keyword: "全局操作人："},
		{msgID: 862002, user: ledgerBotOfflineE2EUsers[1], text: "自动回复列表", keyword: "当前无自动回复规则。"},
		{msgID: 862003, user: ledgerBotOfflineE2EUsers[1], text: "设置担保规则 操作人越权尝试", silent: true},
		{msgID: 862004, user: ledgerBotOfflineE2EUsers[9], text: "+999#E2E-no-permission", silent: true},
		{msgID: 862005, user: ledgerBotOfflineE2EUsers[9], text: "1+1", keyword: "计算结果：<code>2</code>"},
	}
	for i, step := range steps {
		dispatchLedgerBotOfflineE2EText(app, step.user, step.msgID, step.text, baseDate+i)
	}

	snapshot := capture.snapshot()
	for _, step := range steps {
		if step.silent {
			if fourLayerHasAnyReply(snapshot, ledgerBotOfflineE2EChatID, step.msgID) {
				t.Fatalf("离线 E2E 越权命令应静默: message_id=%d snapshot=%+v", step.msgID, snapshot)
			}
			continue
		}
		requireLedgerBotOfflineE2EReply(t, snapshot, step.msgID, step.keyword)
	}
	requireLedgerBotOfflineE2EHTMLReply(t, snapshot, 862005, "计算结果：<code>2</code>")
}

func TestLedgerBotOfflineE2EMultiUserCommandScenario(t *testing.T) {
	app, _, capture := newLedgerBotOfflineE2EApp(t, "ledgerbot-offline-e2e-multi-user.db")
	baseDate := int(time.Now().Unix())

	steps := []struct {
		msgID   int
		user    ledgerBotOfflineE2EUser
		text    string
		keyword string
		silent  bool
	}{
		{msgID: 863001, user: ledgerBotOfflineE2EUsers[1], text: "北京时间", keyword: "北京时间："},
		{msgID: 863002, user: ledgerBotOfflineE2EUsers[2], text: "菜单", keyword: "群控面板"},
		{msgID: 863003, user: ledgerBotOfflineE2EUsers[3], text: "10+20", keyword: "计算结果：<code>30</code>"},
		{msgID: 863004, user: ledgerBotOfflineE2EUsers[4], text: "地址", keyword: "下发地址未设置。"},
		{msgID: 863005, user: ledgerBotOfflineE2EUsers[5], text: "8*8", keyword: "计算结果：<code>64</code>"},
		{msgID: 863006, user: ledgerBotOfflineE2EUsers[6], text: "下发地址", keyword: "未设置下发地址。"},
		{msgID: 863007, user: ledgerBotOfflineE2EUsers[7], text: "100/4", keyword: "计算结果：<code>25</code>"},
		{msgID: 863008, user: ledgerBotOfflineE2EUsers[8], text: "菜单", silent: true},
	}
	for i, step := range steps {
		dispatchLedgerBotOfflineE2EText(app, step.user, step.msgID, step.text, baseDate+i)
	}

	snapshot := capture.snapshot()
	for _, step := range steps {
		if step.silent {
			if fourLayerHasAnyReply(snapshot, ledgerBotOfflineE2EChatID, step.msgID) {
				t.Fatalf("离线 E2E 普通成员无权限命令应静默: message_id=%d snapshot=%+v", step.msgID, snapshot)
			}
			continue
		}
		requireLedgerBotOfflineE2EReply(t, snapshot, step.msgID, step.keyword)
	}
	requireLedgerBotOfflineE2EHTMLReply(t, snapshot, 863003, "计算结果：<code>30</code>")
	requireLedgerBotOfflineE2EHTMLReply(t, snapshot, 863005, "计算结果：<code>64</code>")
	requireLedgerBotOfflineE2EHTMLReply(t, snapshot, 863007, "计算结果：<code>25</code>")
}

func newLedgerBotOfflineE2EApp(t *testing.T, dbFile string) (*App, *db.Database, *fourLayerCapture) {
	t.Helper()

	app, database, capture := newFourLayerTestApp(t, dbFile)
	app.worldTime = services.NewWorldTimeService()

	if _, err := database.EnsureGroup(ledgerBotOfflineE2EChatID, "3"); err != nil {
		t.Fatalf("初始化离线 E2E 群配置失败: %v", err)
	}
	if err := database.SetOwner(ledgerBotOfflineE2EChatID, ledgerBotOfflineE2EUsers[0].id); err != nil {
		t.Fatalf("设置离线 E2E 权限人失败: %v", err)
	}
	if err := database.AddOperator(ledgerBotOfflineE2EChatID, ledgerBotOfflineE2EUsers[1].id, ledgerBotOfflineE2EUsers[1].name, false); err != nil {
		t.Fatalf("设置离线 E2E 本群操作人失败: %v", err)
	}
	if err := database.AddOperator(ledgerBotOfflineE2EChatID, ledgerBotOfflineE2EUsers[2].id, ledgerBotOfflineE2EUsers[2].name, true); err != nil {
		t.Fatalf("设置离线 E2E 全局操作人失败: %v", err)
	}
	return app, database, capture
}

func dispatchLedgerBotOfflineE2EText(app *App, user ledgerBotOfflineE2EUser, msgID int, text string, date int) {
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		msgID,
		msgID,
		ledgerBotOfflineE2EChatID,
		user.id,
		user.name,
		text,
		date,
	))
}

func requireLedgerBotOfflineE2EReply(t *testing.T, snapshot []fourLayerCapturedMessage, messageID int, keyword string) {
	t.Helper()
	if !fourLayerHasReply(snapshot, ledgerBotOfflineE2EChatID, messageID, keyword) {
		t.Fatalf("缺少离线 E2E 回执: message_id=%d keyword=%q snapshot=%+v", messageID, keyword, snapshot)
	}
}

func requireLedgerBotOfflineE2EHTMLReply(t *testing.T, snapshot []fourLayerCapturedMessage, messageID int, want string) {
	t.Helper()
	reply, ok := fourLayerFindReply(snapshot, ledgerBotOfflineE2EChatID, messageID, want)
	if !ok {
		t.Fatalf("缺少离线 E2E HTML 回执: message_id=%d want=%q snapshot=%+v", messageID, want, snapshot)
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("离线 E2E 计算结果应使用 HTML parse mode: got=%q reply=%+v", reply.parseMode, reply)
	}
}
