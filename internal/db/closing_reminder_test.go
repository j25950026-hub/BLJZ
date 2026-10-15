package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestClosingReminderConfigDefaultsAndUpsert(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "closing-reminder-config.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100714001
	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取默认下课提醒配置失败: %v", err)
	}
	if config.ChatID != chatID {
		t.Fatalf("默认配置 chat_id 错误: %+v", config)
	}
	if config.Enabled != 0 {
		t.Fatalf("默认下课提醒应关闭: %+v", config)
	}
	if config.ReminderTime != "04:00" {
		t.Fatalf("默认提醒时间错误: %q", config.ReminderTime)
	}
	if config.ThresholdU != 1.5 {
		t.Fatalf("默认阈值错误: %v", config.ThresholdU)
	}
	if config.DMMode != ClosingReminderDMModeOwnerOperatorsCustom {
		t.Fatalf("默认私聊模式错误: %q", config.DMMode)
	}

	err = database.UpsertClosingReminderConfig(GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      1,
		ReminderTime: "04:30",
		ThresholdU:   2.5,
		DMMode:       ClosingReminderDMModeOwner,
		TextNormal:   "自定义正常文案",
		UpdatedBy:    7001,
	})
	if err != nil {
		t.Fatalf("保存下课提醒配置失败: %v", err)
	}

	saved, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取已保存下课提醒配置失败: %v", err)
	}
	if saved.Enabled != 1 || saved.ReminderTime != "04:30" || !floatEqual(saved.ThresholdU, 2.5) || saved.DMMode != ClosingReminderDMModeOwner {
		t.Fatalf("保存后的下课提醒配置不正确: %+v", saved)
	}
	if saved.TextNormal != "自定义正常文案" || saved.UpdatedBy != 7001 {
		t.Fatalf("保存后的文案或更新人不正确: %+v", saved)
	}

	enabled, err := database.ListEnabledClosingReminderConfigs()
	if err != nil {
		t.Fatalf("查询启用下课提醒配置失败: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ChatID != chatID {
		t.Fatalf("启用配置列表错误: %+v", enabled)
	}
}

func TestClosingReminderDMRecipientsLifecycle(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "closing-reminder-recipients.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const (
		chatID int64 = -100714002
		userID int64 = 7142001
	)
	if err := database.UpsertClosingDMRecipient(chatID, userID, "old_name", "旧昵称", 7001); err != nil {
		t.Fatalf("新增下课私聊接收人失败: %v", err)
	}
	if err := database.UpsertClosingDMRecipient(chatID, userID, "new_name", "新昵称", 7002); err != nil {
		t.Fatalf("重复保存下课私聊接收人失败: %v", err)
	}

	recipients, err := database.ListClosingDMRecipients(chatID)
	if err != nil {
		t.Fatalf("查询下课私聊接收人失败: %v", err)
	}
	if len(recipients) != 1 {
		t.Fatalf("重复接收人不应产生重复行: %+v", recipients)
	}
	if recipients[0].Username != "new_name" || recipients[0].Nickname != "新昵称" || recipients[0].AddedBy != 7002 {
		t.Fatalf("重复保存应更新用户信息: %+v", recipients[0])
	}

	deleted, err := database.DeleteClosingDMRecipient(chatID, userID)
	if err != nil {
		t.Fatalf("删除下课私聊接收人失败: %v", err)
	}
	if !deleted {
		t.Fatalf("删除已存在接收人应返回 true")
	}
	recipients, err = database.ListClosingDMRecipients(chatID)
	if err != nil {
		t.Fatalf("删除后查询下课私聊接收人失败: %v", err)
	}
	if len(recipients) != 0 {
		t.Fatalf("删除后不应保留接收人: %+v", recipients)
	}

	if err := database.UpsertClosingDMRecipient(chatID, 7142002, "user2", "用户2", 7001); err != nil {
		t.Fatalf("新增第二个接收人失败: %v", err)
	}
	if err := database.UpsertClosingDMRecipient(chatID, 7142003, "user3", "用户3", 7001); err != nil {
		t.Fatalf("新增第三个接收人失败: %v", err)
	}
	if err := database.ClearClosingDMRecipients(chatID); err != nil {
		t.Fatalf("清空下课私聊接收人失败: %v", err)
	}
	recipients, err = database.ListClosingDMRecipients(chatID)
	if err != nil {
		t.Fatalf("清空后查询下课私聊接收人失败: %v", err)
	}
	if len(recipients) != 0 {
		t.Fatalf("清空后不应保留接收人: %+v", recipients)
	}
}

func TestClosingReminderSummaryStatuses(t *testing.T) {
	cases := []struct {
		name         string
		entries      []closingReminderLedgerSample
		thresholdU   float64
		wantStatus   string
		wantDeposit  float64
		wantPayout   float64
		wantPending  float64
		wantDepCount int
		wantPayCount int
	}{
		{
			name:       "no data",
			wantStatus: ClosingReminderStatusNoData,
		},
		{
			name: "normal",
			entries: []closingReminderLedgerSample{
				{MessageID: 7143001, EntryType: "deposit", FinalAmount: 100},
				{MessageID: 7143002, EntryType: "payout", FinalAmount: 100},
			},
			wantStatus:   ClosingReminderStatusNormal,
			wantDeposit:  100,
			wantPayout:   100,
			wantDepCount: 1,
			wantPayCount: 1,
		},
		{
			name: "normal when CNY pending is within default 1.5U threshold",
			entries: []closingReminderLedgerSample{
				{MessageID: 7143008, EntryType: "deposit", Amount: 1000, Currency: "CNY", Rate: 10, FinalAmount: 1000},
				{MessageID: 7143009, EntryType: "payout", Amount: 999, Currency: "CNY", Rate: 10, FinalAmount: 999},
			},
			wantStatus:   ClosingReminderStatusNormal,
			wantDeposit:  100,
			wantPayout:   99.9,
			wantPending:  0.1,
			wantDepCount: 1,
			wantPayCount: 1,
		},
		{
			name: "pending",
			entries: []closingReminderLedgerSample{
				{MessageID: 7143003, EntryType: "deposit", FinalAmount: 150},
				{MessageID: 7143004, EntryType: "payout", FinalAmount: 100},
			},
			thresholdU:   10,
			wantStatus:   ClosingReminderStatusPending,
			wantDeposit:  150,
			wantPayout:   100,
			wantPending:  50,
			wantDepCount: 1,
			wantPayCount: 1,
		},
		{
			name: "review when payout exists without deposit",
			entries: []closingReminderLedgerSample{
				{MessageID: 7143005, EntryType: "payout", FinalAmount: 20},
			},
			wantStatus:   ClosingReminderStatusReview,
			wantPayout:   20,
			wantPending:  -20,
			wantPayCount: 1,
		},
		{
			name: "review when payout exceeds deposit plus threshold",
			entries: []closingReminderLedgerSample{
				{MessageID: 7143006, EntryType: "deposit", FinalAmount: 100},
				{MessageID: 7143007, EntryType: "payout", FinalAmount: 120},
			},
			thresholdU:   5,
			wantStatus:   ClosingReminderStatusReview,
			wantDeposit:  100,
			wantPayout:   120,
			wantPending:  -20,
			wantDepCount: 1,
			wantPayCount: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, err := NewDatabase(filepath.Join(t.TempDir(), "closing-reminder-summary.db"))
			if err != nil {
				t.Fatalf("初始化数据库失败: %v", err)
			}
			defer database.Close()

			const chatID int64 = -100714003
			if _, err := database.EnsureGroup(chatID, "收班提醒状态群"); err != nil {
				t.Fatalf("初始化群失败: %v", err)
			}
			for _, entry := range tc.entries {
				closingReminderApplyLedgerSample(t, database, chatID, entry)
			}

			summary, err := database.BuildClosingReminderSummary(
				chatID,
				"2026-07-14",
				time.Date(2026, 7, 14, 4, 0, 0, 0, time.UTC),
				time.Date(2026, 7, 15, 4, 0, 0, 0, time.UTC),
				tc.thresholdU,
			)
			if err != nil {
				t.Fatalf("构建下课提醒汇总失败: %v", err)
			}
			if summary.Status != tc.wantStatus {
				t.Fatalf("状态判定错误: got=%s want=%s summary=%+v", summary.Status, tc.wantStatus, summary)
			}
			if summary.DepositCount != tc.wantDepCount || summary.PayoutCount != tc.wantPayCount {
				t.Fatalf("笔数汇总错误: %+v", summary)
			}
			if !floatEqual(summary.DepositTotal, tc.wantDeposit) || !floatEqual(summary.PayoutTotal, tc.wantPayout) || !floatEqual(summary.PendingTotal, tc.wantPending) {
				t.Fatalf("金额汇总错误: %+v", summary)
			}
		})
	}
}

func TestClosingReminderRunDedup(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "closing-reminder-run.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100714004
	inserted, err := database.MarkClosingReminderRun(chatID, "2026-07-14", ClosingReminderStatusNormal, false)
	if err != nil {
		t.Fatalf("标记自动下课提醒发送失败: %v", err)
	}
	if !inserted {
		t.Fatalf("第一次自动发送应成功插入")
	}
	inserted, err = database.MarkClosingReminderRun(chatID, "2026-07-14", ClosingReminderStatusNormal, false)
	if err != nil {
		t.Fatalf("重复标记自动下课提醒发送失败: %v", err)
	}
	if inserted {
		t.Fatalf("同一群同一业务日自动发送不应重复插入")
	}
	runCount := mustCountRows(t, database, `SELECT COUNT(1) FROM group_closing_reminder_runs WHERE chat_id = ? AND business_date = ?`, chatID, "2026-07-14")
	if runCount != 1 {
		t.Fatalf("自动发送去重行数错误: %d", runCount)
	}

	inserted, err = database.MarkClosingReminderRun(chatID, "2026-07-15", ClosingReminderStatusPending, true)
	if err != nil {
		t.Fatalf("标记手动下课提醒失败: %v", err)
	}
	if !inserted {
		t.Fatalf("手动发送应返回成功且不占用自动去重")
	}
	inserted, err = database.MarkClosingReminderRun(chatID, "2026-07-15", ClosingReminderStatusPending, false)
	if err != nil {
		t.Fatalf("手动后标记自动下课提醒失败: %v", err)
	}
	if !inserted {
		t.Fatalf("手动发送不应阻止同业务日自动发送")
	}
}

type closingReminderLedgerSample struct {
	MessageID   int
	EntryType   string
	Amount      float64
	Currency    string
	Rate        float64
	FinalAmount float64
}

func closingReminderApplyLedgerSample(t *testing.T, database *Database, chatID int64, sample closingReminderLedgerSample) {
	t.Helper()
	amount := sample.Amount
	if amount == 0 {
		amount = sample.FinalAmount
	}
	currency := sample.Currency
	if currency == "" {
		currency = "USDT"
	}
	rate := sample.Rate
	if rate == 0 {
		rate = 1
	}
	result, err := database.ApplyLedgerEntry(LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        sample.MessageID,
		UserID:           7143999,
		Username:         "closing-reminder",
		Nickname:         "ClosingReminder",
		EntryType:        sample.EntryType,
		Amount:           amount,
		Currency:         currency,
		BaseAmount:       amount,
		FeeRate:          0,
		Rate:             rate,
		FinalAmount:      sample.FinalAmount,
		Note:             "closing-reminder-test",
		TrackMessageLink: "tg://msg/closing-reminder",
	})
	if err != nil {
		t.Fatalf("写入下课提醒测试分录失败: %v", err)
	}
	if result.EntryID == 0 || result.Duplicate {
		t.Fatalf("下课提醒测试分录写入结果异常: %+v", result)
	}
	if _, err := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, "2026-07-14 12:00:00", result.EntryID); err != nil {
		t.Fatalf("设置下课提醒测试分录时间失败: %v", err)
	}
}
