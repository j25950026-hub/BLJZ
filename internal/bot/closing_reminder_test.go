package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestClosingReminderOperatorConfigCommands(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-operator.db")
	const (
		chatID     int64 = -100714501
		ownerID    int64 = 7145011
		operatorID int64 = 7145012
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)

	for index, command := range []string{
		"开启下课提醒",
		"设置下课时间04:00",
		"查询下课提醒",
		"关闭下课提醒",
	} {
		msg := closingReminderGroupMessage(714510+index, chatID, operatorID, "operator714", command)
		if err := app.handleMessage(context.Background(), msg); err != nil {
			t.Fatalf("执行命令 %q 失败: %v", command, err)
		}
	}

	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败: %v", err)
	}
	if config.Enabled != 0 || config.ReminderTime != "04:00" {
		t.Fatalf("操作人命令保存配置错误: %+v", config)
	}

	texts := closingReminderCapturedTexts(capture.snapshot(), chatID)
	for _, want := range []string{"下课提醒已开启", "下课提醒时间已设置为 04:00", "下课提醒：", "下课提醒已关闭"} {
		if !closingReminderTextsContain(texts, want) {
			t.Fatalf("回执缺少 %q: %+v", want, texts)
		}
	}
}

func TestClosingReminderManualTriggerSendsSafeGroupTextAndDMDetails(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-manual-pending.db")
	app.loc = time.UTC
	app.cfg.Timezone = "UTC"
	app.now = func() time.Time { return time.Now().UTC().Add(24 * time.Hour) }
	const (
		chatID     int64 = -100714505
		ownerID    int64 = 7145051
		operatorID int64 = 7145052
		customID   int64 = 7145053
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)
	if err := database.UpdateGroupFields(chatID, map[string]any{"day_cut_time": "00:00:00"}); err != nil {
		t.Fatalf("设置日切时间失败: %v", err)
	}
	if err := database.UpsertClosingDMRecipient(chatID, customID, "custom714", "指定接收人", ownerID); err != nil {
		t.Fatalf("设置指定私聊接收人失败: %v", err)
	}
	closingReminderApplyLedgerNow(t, database, chatID, 7145501, "deposit", 150)
	closingReminderApplyLedgerNow(t, database, chatID, 7145502, "payout", 100)

	msg := closingReminderGroupMessage(714550, chatID, operatorID, "operator714", "立即下课")
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("立即下课失败: %v", err)
	}

	snapshot := capture.snapshot()
	groupTexts := closingReminderCapturedTexts(snapshot, chatID)
	if !closingReminderTextsContain(groupTexts, "部分收尾事项待确认") {
		t.Fatalf("群内应发送 pending 体面文案: %+v", groupTexts)
	}
	for _, forbidden := range []string{"150", "100", "50", " U", "USDT", "未下发", "忘记下发", "异常", "风险", "错误堆栈", "数据库错误"} {
		if closingReminderTextsContain(groupTexts, forbidden) {
			t.Fatalf("群内文案不应包含 %q: %+v", forbidden, groupTexts)
		}
	}
	for _, userID := range []int64{ownerID, operatorID, customID} {
		dmTexts := closingReminderCapturedTexts(snapshot, userID)
		if !closingReminderTextsContain(dmTexts, "入款：1 笔 / 150 U") ||
			!closingReminderTextsContain(dmTexts, "下发：1 笔 / 100 U") ||
			!closingReminderTextsContain(dmTexts, "待确认：50 U") {
			t.Fatalf("用户 %d 私聊明细不完整: %+v", userID, dmTexts)
		}
	}
}

func TestClosingReminderManualTriggerNoDataSkipsDefaultDM(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-manual-nodata.db")
	app.loc = time.UTC
	app.cfg.Timezone = "UTC"
	app.now = func() time.Time { return time.Now().UTC().Add(24 * time.Hour) }
	const (
		chatID     int64 = -100714506
		ownerID    int64 = 7145061
		operatorID int64 = 7145062
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)
	if err := database.UpdateGroupFields(chatID, map[string]any{"day_cut_time": "00:00:00"}); err != nil {
		t.Fatalf("设置日切时间失败: %v", err)
	}

	msg := closingReminderGroupMessage(714560, chatID, operatorID, "operator714", "立即下课")
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("立即下课失败: %v", err)
	}

	snapshot := capture.snapshot()
	if !closingReminderTextsContain(closingReminderCapturedTexts(snapshot, chatID), "今日暂无账单同步") {
		t.Fatalf("无账单群内文案错误: %+v", snapshot)
	}
	if texts := closingReminderCapturedTexts(snapshot, ownerID); len(texts) != 0 {
		t.Fatalf("no_data 默认不应私聊权限人: %+v", texts)
	}
	if texts := closingReminderCapturedTexts(snapshot, operatorID); len(texts) != 0 {
		t.Fatalf("no_data 默认不应私聊操作人: %+v", texts)
	}
}

func TestClosingReminderRejectsGroupTextWithAmountWord(t *testing.T) {
	for index, tc := range []struct {
		name    string
		content string
	}{
		{name: "金额字样", content: "今日金额待确认"},
		{name: "美元符号前置", content: "今日 $100 待确认"},
		{name: "人民币符号前置", content: "今日 ￥100 待确认"},
		{name: "小写u单位", content: "今日 50u 待确认"},
		{name: "币种前置", content: "今日 USDT 100 待确认"},
		{name: "账务词加裸数字", content: "今日差额 50 待确认"},
		{name: "裸数字", content: "今日 100 待确认"},
		{name: "中文数字金额", content: "今日USDT一百待确认"},
		{name: "中文数字账务词", content: "今日差额五十待确认"},
		{name: "圈号数字", content: "今日①⓪⓪U待确认"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, database, capture := newFourLayerTestApp(t, "closing-reminder-amount-word.db")
			chatID := int64(-100714510 - index)
			const (
				ownerID    int64 = 7145101
				operatorID int64 = 7145102
			)
			closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)

			msg := closingReminderGroupMessage(714510+index, chatID, operatorID, "operator714", "设置下课文案正常 "+tc.content)
			if err := app.handleMessage(context.Background(), msg); err != nil {
				t.Fatalf("设置包含金额表达的下课文案不应报错: %v", err)
			}
			config, err := database.GetClosingReminderConfig(chatID)
			if err != nil {
				t.Fatalf("读取下课提醒配置失败: %v", err)
			}
			if strings.TrimSpace(config.TextNormal) != "" {
				t.Fatalf("群内下课文案不应保存金额表达: %+v", config)
			}
			texts := closingReminderCapturedTexts(capture.snapshot(), chatID)
			if !closingReminderTextsContain(texts, "包含敏感词或金额") {
				t.Fatalf("应提示文案包含敏感词或金额: %+v", texts)
			}
		})
	}
}

func TestClosingReminderShouldRunAtRespectsDayCutTime(t *testing.T) {
	loc := time.UTC
	beforeCut := time.Date(2026, 7, 14, 2, 30, 0, 0, loc)
	if closingReminderShouldRunAt("02:00", beforeCut, "04:00:00") {
		t.Fatalf("提醒时间早于日切时，日切前不应发送")
	}
	afterCut := time.Date(2026, 7, 14, 4, 1, 0, 0, loc)
	if !closingReminderShouldRunAt("02:00", afterCut, "04:00:00") {
		t.Fatalf("提醒时间早于日切时，日切后应允许发送一次")
	}
}

func TestClosingReminderAutoScanSkipsExpiredOwnerEntitlement(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-expired-owner.db")
	app.loc = time.UTC
	app.cfg.Timezone = "UTC"
	const (
		chatID     int64 = -100714518
		ownerID    int64 = 7145181
		operatorID int64 = 7145182
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)
	expiredAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := database.UpsertPrivateUserProfile(db.PrivateUserProfile{
		UserID:         ownerID,
		TrialStatus:    "expired",
		TrialAppliedAt: expiredAt.Add(-24 * time.Hour).Format(time.RFC3339),
		ExpireAt:       expiredAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("写入过期权限人失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"day_cut_time": "00:00:00"}); err != nil {
		t.Fatalf("设置日切时间失败: %v", err)
	}
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      1,
		ReminderTime: "00:00",
		DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
	}); err != nil {
		t.Fatalf("保存下课提醒配置失败: %v", err)
	}

	now := time.Now().UTC().Add(24 * time.Hour)
	if err := app.runClosingReminderScanAt(context.Background(), now); err != nil {
		t.Fatalf("过期权限人自动扫描不应报错: %v", err)
	}
	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("权限人过期后不应自动发送下课提醒: %+v", snapshot)
	}
	businessDate, _, _ := resolveClosingReminderWindow(now, time.UTC, "00:00:00")
	inserted, err := database.MarkClosingReminderRun(chatID, businessDate, db.ClosingReminderStatusNoData, false)
	if err != nil {
		t.Fatalf("检查过期群自动去重标记失败: %v", err)
	}
	if !inserted {
		t.Fatalf("权限人过期跳过扫描时不应占用自动去重")
	}
}

func TestClosingReminderDMPeriodUsesLocalTime(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-dm-local-time.db")
	loc := time.FixedZone("ICT", 7*3600)
	app.loc = loc
	const (
		chatID  int64 = -100714514
		ownerID int64 = 7145141
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, 0)
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败: %v", err)
	}
	summary := db.ClosingReminderSummary{
		ChatID:       chatID,
		BusinessDate: "2026-07-14",
		PeriodStart:  "2026-07-13T21:00:00",
		PeriodEnd:    "2026-07-14T21:00:00",
		Status:       db.ClosingReminderStatusPending,
		DepositCount: 1,
		DepositTotal: 100,
		PendingTotal: 100,
	}
	if err := app.notifyClosingReminder(context.Background(), group, config, summary, true); err != nil {
		t.Fatalf("发送下课提醒失败: %v", err)
	}
	dmTexts := closingReminderCapturedTexts(capture.snapshot(), ownerID)
	if !closingReminderTextsContain(dmTexts, "账期：2026-07-14 04:00 - 2026-07-15 04:00") {
		t.Fatalf("私聊账期应按本地时区展示: %+v", dmTexts)
	}
}

func TestClosingReminderAutoScanContinuesAfterGroupSendFailure(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "closing-reminder-scan-failure.db")
	app.loc = time.UTC
	app.cfg.Timezone = "UTC"
	const (
		failChat    int64 = -100714516
		successChat int64 = -100714515
		ownerID     int64 = 7145151
		operatorID  int64 = 7145152
	)
	closingReminderPrepareGroup(t, database, failChat, ownerID, operatorID)
	closingReminderPrepareGroup(t, database, successChat, ownerID, operatorID)
	for _, chatID := range []int64{failChat, successChat} {
		if err := database.UpdateGroupFields(chatID, map[string]any{"day_cut_time": "00:00:00"}); err != nil {
			t.Fatalf("设置日切时间失败: %v", err)
		}
		if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
			ChatID:       chatID,
			Enabled:      1,
			ReminderTime: "00:00",
			DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
		}); err != nil {
			t.Fatalf("保存下课提醒配置失败: %v", err)
		}
		closingReminderApplyLedgerNow(t, database, chatID, int(714515000+(-chatID)%1000), "deposit", 100)
		closingReminderApplyLedgerNow(t, database, chatID, int(714516000+(-chatID)%1000), "payout", 100)
	}
	api, capture := newClosingReminderFailureBotAPI(t, failChat)
	app.api = api

	now := time.Now().UTC().Add(24 * time.Hour)
	err := app.runClosingReminderScanAt(context.Background(), now)
	if err == nil {
		t.Fatalf("失败群发送失败后应返回聚合错误")
	}
	if texts := closingReminderCapturedTexts(capture.snapshot(), successChat); len(texts) != 1 {
		t.Fatalf("一个群发送失败不应阻断后续群: %+v", texts)
	}
	businessDate, _, _ := resolveClosingReminderWindow(now, time.UTC, "00:00:00")
	inserted, markErr := database.MarkClosingReminderRun(failChat, businessDate, db.ClosingReminderStatusNormal, false)
	if markErr != nil {
		t.Fatalf("检查失败群自动去重标记失败: %v", markErr)
	}
	if !inserted {
		t.Fatalf("群消息发送失败后不应占用自动去重")
	}
	inserted, markErr = database.MarkClosingReminderRun(successChat, businessDate, db.ClosingReminderStatusNormal, false)
	if markErr != nil {
		t.Fatalf("检查成功群自动去重标记失败: %v", markErr)
	}
	if inserted {
		t.Fatalf("成功群应已占用自动去重")
	}
}

func TestClosingReminderNotifyDMPolicy(t *testing.T) {
	for _, tc := range []struct {
		status string
		wantDM bool
	}{
		{status: db.ClosingReminderStatusNormal, wantDM: false},
		{status: db.ClosingReminderStatusNoData, wantDM: false},
		{status: db.ClosingReminderStatusPending, wantDM: true},
		{status: db.ClosingReminderStatusReview, wantDM: true},
		{status: db.ClosingReminderStatusFailed, wantDM: true},
	} {
		t.Run(tc.status, func(t *testing.T) {
			app, database, capture := newFourLayerTestApp(t, "closing-reminder-dm-policy.db")
			const (
				chatID     int64 = -100714507
				ownerID    int64 = 7145071
				operatorID int64 = 7145072
				customID   int64 = 7145073
			)
			closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)
			if err := database.UpsertClosingDMRecipient(chatID, customID, "custom714", "指定接收人", ownerID); err != nil {
				t.Fatalf("设置指定私聊接收人失败: %v", err)
			}
			group, err := database.GetGroup(chatID)
			if err != nil {
				t.Fatalf("读取群配置失败: %v", err)
			}
			config, err := database.GetClosingReminderConfig(chatID)
			if err != nil {
				t.Fatalf("读取下课提醒配置失败: %v", err)
			}
			summary := db.ClosingReminderSummary{
				ChatID:       chatID,
				BusinessDate: "2026-07-14",
				PeriodStart:  "2026-07-14T00:00:00",
				PeriodEnd:    "2026-07-15T00:00:00",
				Status:       tc.status,
				DepositCount: 1,
				PayoutCount:  1,
				DepositTotal: 150,
				PayoutTotal:  100,
				PendingTotal: 50,
			}
			if err := app.notifyClosingReminder(context.Background(), group, config, summary, true); err != nil {
				t.Fatalf("发送下课提醒通知失败: %v", err)
			}

			snapshot := capture.snapshot()
			for _, userID := range []int64{ownerID, operatorID, customID} {
				gotDM := len(closingReminderCapturedTexts(snapshot, userID)) > 0
				if gotDM != tc.wantDM {
					t.Fatalf("状态 %s 私聊策略错误 user=%d got=%t want=%t snapshot=%+v", tc.status, userID, gotDM, tc.wantDM, snapshot)
				}
			}
		})
	}
}

func TestClosingReminderAutoScanSendsOnce(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-auto-once.db")
	app.loc = time.UTC
	app.cfg.Timezone = "UTC"
	const (
		chatID     int64 = -100714508
		ownerID    int64 = 7145081
		operatorID int64 = 7145082
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)
	if err := database.UpdateGroupFields(chatID, map[string]any{"day_cut_time": "00:00:00"}); err != nil {
		t.Fatalf("设置日切时间失败: %v", err)
	}
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      1,
		ReminderTime: "00:00",
		DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
	}); err != nil {
		t.Fatalf("开启下课提醒失败: %v", err)
	}
	closingReminderApplyLedgerNow(t, database, chatID, 7145801, "deposit", 100)
	closingReminderApplyLedgerNow(t, database, chatID, 7145802, "payout", 100)
	now := time.Now().UTC().Add(24 * time.Hour)

	if err := app.runClosingReminderScanAt(context.Background(), now); err != nil {
		t.Fatalf("第一次自动扫描失败: %v", err)
	}
	if err := app.runClosingReminderScanAt(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("第二次自动扫描失败: %v", err)
	}

	groupTexts := closingReminderCapturedTexts(capture.snapshot(), chatID)
	if len(groupTexts) != 1 {
		t.Fatalf("同业务日自动提醒应只发送一次: %+v", groupTexts)
	}
	if !strings.Contains(groupTexts[0], "收尾完成") {
		t.Fatalf("自动提醒群内文案错误: %q", groupTexts[0])
	}
}

func TestClosingReminderAutoScanSendsWhenOnlyPayoutExists(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-auto-payout-only.db")
	app.loc = time.UTC
	app.cfg.Timezone = "UTC"
	const (
		chatID     int64 = -100714519
		ownerID    int64 = 7145191
		operatorID int64 = 7145192
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)
	if err := database.UpdateGroupFields(chatID, map[string]any{"day_cut_time": "00:00:00"}); err != nil {
		t.Fatalf("设置日切时间失败: %v", err)
	}
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      1,
		ReminderTime: "00:00",
		DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
	}); err != nil {
		t.Fatalf("开启下课提醒失败: %v", err)
	}
	closingReminderApplyLedgerNow(t, database, chatID, 7145901, "payout", 100)
	now := time.Now().UTC().Add(24 * time.Hour)

	if err := app.runClosingReminderScanAt(context.Background(), now); err != nil {
		t.Fatalf("只有下发无入款时自动扫描不应报错: %v", err)
	}
	groupTexts := closingReminderCapturedTexts(capture.snapshot(), chatID)
	if len(groupTexts) != 1 {
		t.Fatalf("只有下发也属于记账行为，应自动发送下课提醒: %+v", groupTexts)
	}
	if !strings.Contains(groupTexts[0], "收支数据待复核") {
		t.Fatalf("只有下发无入款时应提示复核: %q", groupTexts[0])
	}
	businessDate, _, _ := resolveClosingReminderWindow(now, time.UTC, "00:00:00")
	inserted, err := database.MarkClosingReminderRun(chatID, businessDate, db.ClosingReminderStatusReview, false)
	if err != nil {
		t.Fatalf("检查只有下发自动去重标记失败: %v", err)
	}
	if inserted {
		t.Fatalf("只有下发自动发送后应占用自动去重")
	}
}

func TestClosingReminderAutoScanSkipsWithoutLedgerActivity(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-auto-no-ledger.db")
	app.loc = time.UTC
	app.cfg.Timezone = "UTC"
	const (
		chatID     int64 = -100714520
		ownerID    int64 = 7145201
		operatorID int64 = 7145202
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)
	if err := database.UpdateGroupFields(chatID, map[string]any{"day_cut_time": "00:00:00"}); err != nil {
		t.Fatalf("设置日切时间失败: %v", err)
	}
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      1,
		ReminderTime: "00:00",
		DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
	}); err != nil {
		t.Fatalf("开启下课提醒失败: %v", err)
	}
	now := time.Now().UTC().Add(24 * time.Hour)

	if err := app.runClosingReminderScanAt(context.Background(), now); err != nil {
		t.Fatalf("无记账行为时自动扫描不应报错: %v", err)
	}
	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("无记账行为的业务日不应自动发送下课提醒: %+v", snapshot)
	}
	businessDate, _, _ := resolveClosingReminderWindow(now, time.UTC, "00:00:00")
	inserted, err := database.MarkClosingReminderRun(chatID, businessDate, db.ClosingReminderStatusNoData, false)
	if err != nil {
		t.Fatalf("检查无记账行为自动去重标记失败: %v", err)
	}
	if !inserted {
		t.Fatalf("无记账行为的业务日不应占用自动去重")
	}
}

func TestClosingReminderAutoScanSkipsDisabledConfig(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-auto-disabled.db")
	app.loc = time.UTC
	app.cfg.Timezone = "UTC"
	const (
		chatID     int64 = -100714509
		ownerID    int64 = 7145091
		operatorID int64 = 7145092
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      0,
		ReminderTime: "00:00",
		DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
	}); err != nil {
		t.Fatalf("保存关闭配置失败: %v", err)
	}

	if err := app.runClosingReminderScanAt(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("自动扫描关闭配置失败: %v", err)
	}
	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("关闭配置不应发送下课提醒: %+v", snapshot)
	}
}

func TestClosingReminderMemberCommandIsSilent(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-member-silent.db")
	const (
		chatID     int64 = -100714502
		ownerID    int64 = 7145021
		operatorID int64 = 7145022
		memberID   int64 = 7145023
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)

	msg := closingReminderGroupMessage(714520, chatID, memberID, "member714", "开启下课提醒")
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("普通成员发送下课提醒命令不应报错: %v", err)
	}
	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("普通成员下课提醒命令应静默，无回执: %+v", snapshot)
	}

	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败: %v", err)
	}
	if config.Enabled != 0 {
		t.Fatalf("普通成员不应开启下课提醒: %+v", config)
	}
}

func TestClosingReminderTextTemplateCommands(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-template.db")
	const (
		chatID     int64 = -100714503
		ownerID    int64 = 7145031
		operatorID int64 = 7145032
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)

	custom := "第一行自定义\n第二行自定义"
	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714531, chatID, operatorID, "operator714", "设置下课文案正常 "+custom)); err != nil {
		t.Fatalf("设置下课文案失败: %v", err)
	}
	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714532, chatID, operatorID, "operator714", "预览下课文案正常")); err != nil {
		t.Fatalf("预览自定义下课文案失败: %v", err)
	}
	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714535, chatID, operatorID, "operator714", "查询下课文案")); err != nil {
		t.Fatalf("查询下课文案失败: %v", err)
	}
	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714533, chatID, operatorID, "operator714", "恢复下课文案正常")); err != nil {
		t.Fatalf("恢复下课文案失败: %v", err)
	}
	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714534, chatID, operatorID, "operator714", "预览下课文案正常")); err != nil {
		t.Fatalf("预览默认下课文案失败: %v", err)
	}

	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败: %v", err)
	}
	if strings.TrimSpace(config.TextNormal) != "" {
		t.Fatalf("恢复默认后不应保留自定义正常文案: %+v", config)
	}

	texts := closingReminderCapturedTexts(capture.snapshot(), chatID)
	if !closingReminderTextsContain(texts, custom) {
		t.Fatalf("自定义多行文案预览或查询缺失: %+v", texts)
	}
	if !closingReminderTextsContain(texts, "今日账单已同步，收尾完成。") {
		t.Fatalf("恢复默认后预览应回到默认文案: %+v", texts)
	}
}

func TestClosingReminderDMRecipientCommands(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-dm-recipient.db")
	const (
		chatID     int64 = -100714504
		ownerID    int64 = 7145041
		operatorID int64 = 7145042
		targetID   int64 = 7145043
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, operatorID)
	if _, _, err := database.UpsertGroupUser(chatID, targetID, "target714", "目标用户"); err != nil {
		t.Fatalf("写入目标用户缓存失败: %v", err)
	}

	operatorAdd := closingReminderGroupMessage(714541, chatID, operatorID, "operator714", "添加下课私聊 @target714")
	if err := app.handleMessage(context.Background(), operatorAdd); err != nil {
		t.Fatalf("操作人越权添加下课私聊用户不应报错: %v", err)
	}
	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("操作人越权维护指定私聊用户应静默: %+v", snapshot)
	}

	ownerAdd := closingReminderGroupMessage(714542, chatID, ownerID, "owner714", "添加下课私聊 @target714")
	if err := app.handleMessage(context.Background(), ownerAdd); err != nil {
		t.Fatalf("权限人添加下课私聊用户失败: %v", err)
	}
	recipients, err := database.ListClosingDMRecipients(chatID)
	if err != nil {
		t.Fatalf("查询下课私聊用户失败: %v", err)
	}
	if len(recipients) != 1 || recipients[0].UserID != targetID {
		t.Fatalf("权限人添加下课私聊用户结果错误: %+v", recipients)
	}

	operatorDelete := closingReminderGroupMessage(714546, chatID, operatorID, "operator714", "删除下课私聊 @target714")
	if err := app.handleMessage(context.Background(), operatorDelete); err != nil {
		t.Fatalf("操作人越权删除下课私聊用户不应报错: %v", err)
	}
	recipients, err = database.ListClosingDMRecipients(chatID)
	if err != nil {
		t.Fatalf("操作人越权删除后查询下课私聊用户失败: %v", err)
	}
	if len(recipients) != 1 || recipients[0].UserID != targetID {
		t.Fatalf("操作人不能删除下课私聊用户: %+v", recipients)
	}

	operatorClear := closingReminderGroupMessage(714547, chatID, operatorID, "operator714", "清空下课私聊用户")
	if err := app.handleMessage(context.Background(), operatorClear); err != nil {
		t.Fatalf("操作人越权清空下课私聊用户不应报错: %v", err)
	}
	recipients, err = database.ListClosingDMRecipients(chatID)
	if err != nil {
		t.Fatalf("操作人越权清空后查询下课私聊用户失败: %v", err)
	}
	if len(recipients) != 1 || recipients[0].UserID != targetID {
		t.Fatalf("操作人不能清空下课私聊用户: %+v", recipients)
	}

	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714543, chatID, ownerID, "owner714", "删除下课私聊 @target714")); err != nil {
		t.Fatalf("权限人删除下课私聊用户失败: %v", err)
	}
	recipients, err = database.ListClosingDMRecipients(chatID)
	if err != nil {
		t.Fatalf("删除后查询下课私聊用户失败: %v", err)
	}
	if len(recipients) != 0 {
		t.Fatalf("删除后不应保留下课私聊用户: %+v", recipients)
	}

	if err := app.handleMessage(context.Background(), ownerAdd); err != nil {
		t.Fatalf("权限人再次添加下课私聊用户失败: %v", err)
	}
	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714548, chatID, ownerID, "owner714", "清空下课私聊用户")); err != nil {
		t.Fatalf("权限人清空下课私聊用户失败: %v", err)
	}
	recipients, err = database.ListClosingDMRecipients(chatID)
	if err != nil {
		t.Fatalf("权限人清空后查询下课私聊用户失败: %v", err)
	}
	if len(recipients) != 0 {
		t.Fatalf("权限人清空后不应保留下课私聊用户: %+v", recipients)
	}

	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714544, chatID, ownerID, "owner714", "设置下课私聊权限人和操作人")); err != nil {
		t.Fatalf("设置下课私聊模式失败: %v", err)
	}
	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课私聊配置失败: %v", err)
	}
	if config.DMMode != db.ClosingReminderDMModeOwnerOperators {
		t.Fatalf("下课私聊模式保存错误: %+v", config)
	}
	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714545, chatID, ownerID, "owner714", "查询下课私聊")); err != nil {
		t.Fatalf("查询下课私聊失败: %v", err)
	}
	if !closingReminderTextsContain(closingReminderCapturedTexts(capture.snapshot(), chatID), "权限人和操作人") {
		t.Fatalf("查询下课私聊回执缺少模式说明: %+v", capture.snapshot())
	}
}

func TestClosingReminderAddDMRecipientReturnsUpsertError(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-dm-recipient-error.db")
	const (
		chatID  int64 = -100714517
		ownerID int64 = 7145171
	)
	closingReminderPrepareGroup(t, database, chatID, ownerID, 0)

	msg := closingReminderGroupMessage(714571, chatID, ownerID, "owner714", "添加下课私聊")
	msg.ReplyToMessage = &tgbotapi.Message{
		From: &tgbotapi.User{ID: 0, UserName: "invalid_zero_user"},
	}
	err := app.handleAddClosingDMRecipients(msg, msg.Text)
	if err == nil {
		t.Fatalf("添加下课私聊用户时应返回 DAO 写入错误")
	}
	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("写入失败时不应回复添加成功: %+v", snapshot)
	}
}

func TestClosingReminderBatchByGroupType(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-batch-type.db")
	const (
		sourceChatID int64 = -100714601
		targetA      int64 = -100714602
		targetB      int64 = -100714603
		otherChatID  int64 = -100714604
		localOwnerID int64 = 7146011
		globalOwner  int64 = 7146012
	)
	closingReminderPrepareBatchGroup(t, database, sourceChatID, localOwnerID, "批量来源群", "", "")
	closingReminderPrepareBatchGroup(t, database, targetA, localOwnerID, "代收一群", "代收", "")
	closingReminderPrepareBatchGroup(t, database, targetB, localOwnerID, "代收二群", "代收", "夜班")
	closingReminderPrepareBatchGroup(t, database, otherChatID, localOwnerID, "担保群", "担保", "")
	if err := database.AddGlobalOwner(globalOwner, "global714"); err != nil {
		t.Fatalf("设置全局权限人失败: %v", err)
	}

	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714601, sourceChatID, globalOwner, "global714", "批量开启下课提醒 群类型 代收")); err != nil {
		t.Fatalf("批量开启下课提醒失败: %v", err)
	}
	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714602, sourceChatID, globalOwner, "global714", "批量设置下课时间 群类型 代收 21:49")); err != nil {
		t.Fatalf("批量设置下课时间失败: %v", err)
	}

	for _, chatID := range []int64{targetA, targetB} {
		config, err := database.GetClosingReminderConfig(chatID)
		if err != nil {
			t.Fatalf("读取匹配群配置失败(chat=%d): %v", chatID, err)
		}
		if config.Enabled != 1 || config.ReminderTime != "21:49" {
			t.Fatalf("匹配群配置未更新(chat=%d): %+v", chatID, config)
		}
	}
	otherConfig, err := database.GetClosingReminderConfig(otherChatID)
	if err != nil {
		t.Fatalf("读取非匹配群配置失败: %v", err)
	}
	if otherConfig.Enabled != 0 || otherConfig.ReminderTime == "21:49" {
		t.Fatalf("非匹配群不应被批量更新: %+v", otherConfig)
	}

	texts := closingReminderCapturedTexts(capture.snapshot(), sourceChatID)
	for _, want := range []string{"批量开启下课提醒完成：", "范围：群类型【代收】", "目标群：2 个", "成功：2 个", "时间：21:49"} {
		if !closingReminderTextsContain(texts, want) {
			t.Fatalf("批量类型回执缺少 %q: %+v", want, texts)
		}
	}

	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714603, sourceChatID, globalOwner, "global714", "批量关闭下课提醒 群类型 代收")); err != nil {
		t.Fatalf("批量关闭下课提醒失败: %v", err)
	}
	for _, chatID := range []int64{targetA, targetB} {
		config, err := database.GetClosingReminderConfig(chatID)
		if err != nil {
			t.Fatalf("读取批量关闭后配置失败(chat=%d): %v", chatID, err)
		}
		if config.Enabled != 0 || config.ReminderTime != "21:49" {
			t.Fatalf("批量关闭应只关闭提醒并保留时间(chat=%d): %+v", chatID, config)
		}
	}
	if !closingReminderTextsContain(closingReminderCapturedTexts(capture.snapshot(), sourceChatID), "批量关闭下课提醒完成：") {
		t.Fatalf("批量关闭回执缺失: %+v", capture.snapshot())
	}
}

func TestClosingReminderBatchByGroupTagUsesExactTag(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "closing-reminder-batch-tag.db")
	const (
		sourceChatID int64 = -100714611
		targetA      int64 = -100714612
		targetB      int64 = -100714613
		targetC      int64 = -100714614
		localOwnerID int64 = 7146111
		globalOwner  int64 = 7146112
	)
	closingReminderPrepareBatchGroup(t, database, sourceChatID, localOwnerID, "批量来源群", "", "")
	closingReminderPrepareBatchGroup(t, database, targetA, localOwnerID, "夜班一群", "", "夜班,代收")
	closingReminderPrepareBatchGroup(t, database, targetB, localOwnerID, "夜班二号群", "", "夜班2,代收")
	closingReminderPrepareBatchGroup(t, database, targetC, localOwnerID, "夜班三群", "", "白班 夜班")
	if err := database.AddGlobalOwner(globalOwner, "global714"); err != nil {
		t.Fatalf("设置全局权限人失败: %v", err)
	}

	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714611, sourceChatID, globalOwner, "global714", "批量开启下课提醒 群标签 夜班")); err != nil {
		t.Fatalf("批量按标签开启失败: %v", err)
	}

	for _, chatID := range []int64{targetA, targetC} {
		config, err := database.GetClosingReminderConfig(chatID)
		if err != nil {
			t.Fatalf("读取精确匹配群配置失败(chat=%d): %v", chatID, err)
		}
		if config.Enabled != 1 {
			t.Fatalf("精确匹配标签群应开启(chat=%d): %+v", chatID, config)
		}
	}
	configB, err := database.GetClosingReminderConfig(targetB)
	if err != nil {
		t.Fatalf("读取近似标签群配置失败: %v", err)
	}
	if configB.Enabled != 0 {
		t.Fatalf("标签【夜班】不应命中【夜班2】: %+v", configB)
	}
}

func TestClosingReminderBatchRejectsOperator(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-batch-reject-operator.db")
	const (
		sourceChatID int64 = -100714621
		targetChatID int64 = -100714622
		ownerID      int64 = 7146211
		operatorID   int64 = 7146212
	)
	closingReminderPrepareBatchGroup(t, database, sourceChatID, ownerID, "批量来源群", "", "")
	if err := database.AddOperator(sourceChatID, operatorID, "operator714", false); err != nil {
		t.Fatalf("设置本群操作人失败: %v", err)
	}
	closingReminderPrepareBatchGroup(t, database, targetChatID, ownerID, "代收群", "代收", "")

	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714621, sourceChatID, operatorID, "operator714", "批量开启下课提醒 群类型 代收")); err != nil {
		t.Fatalf("操作人批量命令不应报错: %v", err)
	}

	config, err := database.GetClosingReminderConfig(targetChatID)
	if err != nil {
		t.Fatalf("读取目标群配置失败: %v", err)
	}
	if config.Enabled != 0 {
		t.Fatalf("普通操作人不能批量开启下课提醒: %+v", config)
	}
	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("普通操作人批量命令应静默: %+v", snapshot)
	}
}

func TestClosingReminderBatchQueryByType(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-batch-query-type.db")
	const (
		sourceChatID int64 = -100714631
		targetA      int64 = -100714632
		targetB      int64 = -100714633
		targetC      int64 = -100714634
		localOwnerID int64 = 7146311
		globalOwner  int64 = 7146312
	)
	closingReminderPrepareBatchGroup(t, database, sourceChatID, localOwnerID, "批量来源群", "", "")
	closingReminderPrepareBatchGroup(t, database, targetA, localOwnerID, "代收一群", "代收", "")
	closingReminderPrepareBatchGroup(t, database, targetB, localOwnerID, "代收二群", "代收", "")
	closingReminderPrepareBatchGroup(t, database, targetC, localOwnerID, "代收三群", "代收", "")
	if err := database.AddGlobalOwner(globalOwner, "global714"); err != nil {
		t.Fatalf("设置全局权限人失败: %v", err)
	}
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{ChatID: targetA, Enabled: 1, ReminderTime: "21:49"}); err != nil {
		t.Fatalf("写入目标A配置失败: %v", err)
	}
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{ChatID: targetB, Enabled: 1, ReminderTime: "04:00"}); err != nil {
		t.Fatalf("写入目标B配置失败: %v", err)
	}

	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714631, sourceChatID, globalOwner, "global714", "查询下课提醒群类型 代收")); err != nil {
		t.Fatalf("查询批量下课提醒失败: %v", err)
	}

	texts := closingReminderCapturedTexts(capture.snapshot(), sourceChatID)
	for _, want := range []string{
		"下课提醒批量查询：",
		"范围：群类型【代收】",
		"目标群：3 个",
		"已开启：2 个",
		"已关闭：1 个",
		"21:49：1 个",
		"04:00：2 个",
	} {
		if !closingReminderTextsContain(texts, want) {
			t.Fatalf("批量查询回执缺少 %q: %+v", want, texts)
		}
	}
}

func TestClosingReminderBatchInvalidTimeDoesNotChangeGroups(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "closing-reminder-batch-invalid-time.db")
	const (
		sourceChatID int64 = -100714641
		targetChatID int64 = -100714642
		localOwnerID int64 = 7146411
		globalOwner  int64 = 7146412
	)
	closingReminderPrepareBatchGroup(t, database, sourceChatID, localOwnerID, "批量来源群", "", "")
	closingReminderPrepareBatchGroup(t, database, targetChatID, localOwnerID, "代收群", "代收", "")
	if err := database.AddGlobalOwner(globalOwner, "global714"); err != nil {
		t.Fatalf("设置全局权限人失败: %v", err)
	}

	if err := app.handleMessage(context.Background(), closingReminderGroupMessage(714641, sourceChatID, globalOwner, "global714", "批量设置下课时间 群类型 代收 25:99")); err != nil {
		t.Fatalf("非法时间批量命令不应报错: %v", err)
	}

	config, err := database.GetClosingReminderConfig(targetChatID)
	if err != nil {
		t.Fatalf("读取目标群配置失败: %v", err)
	}
	if config.ReminderTime != "04:00" {
		t.Fatalf("非法时间不应修改目标群配置: %+v", config)
	}
	texts := closingReminderCapturedTexts(capture.snapshot(), sourceChatID)
	if !closingReminderTextsContain(texts, "格式：批量设置下课时间 群类型 代收 21:49") {
		t.Fatalf("非法时间应提示批量时间格式: %+v", texts)
	}
}

func closingReminderPrepareGroup(t *testing.T, database *db.Database, chatID int64, ownerID int64, operatorID int64) {
	t.Helper()
	if _, err := database.EnsureGroup(chatID, "下课提醒测试群"); err != nil {
		t.Fatalf("初始化群失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}
	if operatorID != 0 {
		if err := database.AddOperator(chatID, operatorID, "operator714", false); err != nil {
			t.Fatalf("设置操作人失败: %v", err)
		}
	}
}

func closingReminderPrepareBatchGroup(t *testing.T, database *db.Database, chatID int64, ownerID int64, title string, groupType string, tags string) {
	t.Helper()
	if _, err := database.EnsureGroup(chatID, title); err != nil {
		t.Fatalf("初始化批量群失败(chat=%d): %v", chatID, err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置批量群权限人失败(chat=%d): %v", chatID, err)
	}
	fields := map[string]any{}
	if groupType != "" {
		fields["group_type"] = groupType
	}
	if tags != "" {
		fields["group_tags"] = tags
	}
	if len(fields) > 0 {
		if err := database.UpdateGroupFields(chatID, fields); err != nil {
			t.Fatalf("设置批量群属性失败(chat=%d): %v", chatID, err)
		}
	}
}

func closingReminderApplyLedgerNow(t *testing.T, database *db.Database, chatID int64, messageID int, entryType string, finalAmount float64) {
	t.Helper()
	result, err := database.ApplyLedgerEntry(db.LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        messageID,
		UserID:           7145999,
		Username:         "closing-reminder-ledger",
		Nickname:         "ClosingReminderLedger",
		EntryType:        entryType,
		Amount:           finalAmount,
		Currency:         "USDT",
		BaseAmount:       finalAmount,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      finalAmount,
		Note:             "closing-reminder-bot-test",
		TrackMessageLink: "tg://msg/closing-reminder",
	})
	if err != nil {
		t.Fatalf("写入下课提醒账单样本失败: %v", err)
	}
	if result.EntryID == 0 || result.Duplicate {
		t.Fatalf("下课提醒账单样本写入结果异常: %+v", result)
	}
}

func newClosingReminderFailureBotAPI(t *testing.T, failChatID int64) (*tgbotapi.BotAPI, *fourLayerCapture) {
	t.Helper()
	capture := &fourLayerCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
			return
		}
		if !strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/sendMessage") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			return
		}
		chatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("chat_id")), 10, 64)
		if chatID == failChatID {
			_, _ = w.Write([]byte(`{"ok":false,"description":"forced send failure"}`))
			return
		}
		replyTo, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("reply_to_message_id")))
		capture.append(fourLayerCapturedMessage{
			chatID:  chatID,
			replyTo: replyTo,
			text:    strings.TrimSpace(r.PostForm.Get("text")),
		})
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":714900}}`))
	}))
	t.Cleanup(server.Close)

	api := &tgbotapi.BotAPI{
		Token:  "closing-reminder-failure-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")
	api.Self = tgbotapi.User{ID: 714000, UserName: "closing_reminder_failure_bot"}
	return api, capture
}

func closingReminderGroupMessage(messageID int, chatID int64, userID int64, username string, text string) *tgbotapi.Message {
	return &tgbotapi.Message{
		MessageID: messageID,
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "下课提醒测试群",
		},
		From: &tgbotapi.User{
			ID:        userID,
			UserName:  username,
			FirstName: username,
		},
		Text: text,
	}
}

func closingReminderCapturedTexts(snapshot []fourLayerCapturedMessage, chatID int64) []string {
	texts := []string{}
	for _, item := range snapshot {
		if item.chatID == chatID {
			texts = append(texts, item.text)
		}
	}
	return texts
}

func closingReminderTextsContain(texts []string, want string) bool {
	for _, text := range texts {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}
