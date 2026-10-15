package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestControlPoint432DailySettlement(t *testing.T) {
	t.Run("控制点 4.3.2 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "daily-settlement.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const (
			chatWithData int64 = -100430201
			emptyChat    int64 = -100430202
		)
		if _, err := database.EnsureGroup(chatWithData, "结算群A"); err != nil {
			t.Fatalf("初始化有数据群失败: %v", err)
		}
		if _, err := database.EnsureGroup(emptyChat, "结算群B"); err != nil {
			t.Fatalf("初始化空群失败: %v", err)
		}
		if err := database.UpdateGroupFields(chatWithData, map[string]any{"bookkeeping_enabled": 1}); err != nil {
			t.Fatalf("开启有数据群记账失败: %v", err)
		}
		if err := database.UpdateGroupFields(emptyChat, map[string]any{"bookkeeping_enabled": 1}); err != nil {
			t.Fatalf("开启空群记账失败: %v", err)
		}

		applyWithCreatedAt := func(messageID int, entryType, currency string, finalAmount float64, createdAt string) {
			t.Helper()
			apply, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
				ChatID:           chatWithData,
				MessageID:        messageID,
				UserID:           9527,
				Username:         "settlement-ops",
				Nickname:         "SettlementOps",
				EntryType:        entryType,
				Amount:           finalAmount,
				Currency:         currency,
				BaseAmount:       finalAmount,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      finalAmount,
				Note:             "daily-settlement-test",
				TrackMessageLink: "tg://msg/daily-settlement",
			})
			if applyErr != nil {
				t.Fatalf("写入结算样本失败: %v", applyErr)
			}
			if apply.Duplicate || apply.EntryID == 0 {
				t.Fatalf("写入结算样本结果异常: %+v", apply)
			}
			if _, updateErr := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, createdAt, apply.EntryID); updateErr != nil {
				t.Fatalf("设置分录时间失败: %v", updateErr)
			}
		}

		applyWithCreatedAt(43001, "deposit", "CNY", 100, "2026-02-18 00:30:00")
		applyWithCreatedAt(43002, "payout", "CNY", 40, "2026-02-18 09:15:00")
		applyWithCreatedAt(43003, "deposit", "USDT", 10, "2026-02-18 14:45:00")
		applyWithCreatedAt(43004, "deposit", "CNY", 88, "2026-02-17 23:59:59")
		applyWithCreatedAt(43005, "deposit", "CNY", 66, "2026-02-19 00:00:00")

		periodStart := time.Date(2026, 2, 18, 0, 0, 0, 0, time.UTC)
		periodEnd := time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC)
		result, err := database.RunDailySettlementBatch("2026-02-18", periodStart, periodEnd)
		if err != nil {
			t.Fatalf("执行 T+1 结算跑批失败: %v", err)
		}
		if result.ReportDate != "2026-02-18" {
			t.Fatalf("跑批日期错误: %s", result.ReportDate)
		}
		if result.TotalGroups != 2 || result.GeneratedReports != 1 || result.SkippedGroups != 1 {
			t.Fatalf("跑批汇总错误: %+v", result)
		}

		report, found, err := database.GetDailySettlementReport(chatWithData, "2026-02-18")
		if err != nil {
			t.Fatalf("读取结算报表失败: %v", err)
		}
		if !found {
			t.Fatalf("应生成有数据群的结算报表")
		}
		if report.TotalEntries != 3 {
			t.Fatalf("报表分录总笔数错误: %d", report.TotalEntries)
		}
		if report.DepositCount != 2 || report.PayoutCount != 1 {
			t.Fatalf("报表收支笔数错误: dep=%d pay=%d", report.DepositCount, report.PayoutCount)
		}
		if !floatEqual(report.DepositTotal, 110) || !floatEqual(report.PayoutTotal, 40) || !floatEqual(report.NetProfit, 70) {
			t.Fatalf("报表金额汇总错误: dep=%v pay=%v net=%v", report.DepositTotal, report.PayoutTotal, report.NetProfit)
		}
		if len(report.CurrencyBreakdown) != 2 {
			t.Fatalf("币种拆分应包含 CNY/USDT 两种: %+v", report.CurrencyBreakdown)
		}
		currencyMap := make(map[string]DailySettlementCurrencySummary, len(report.CurrencyBreakdown))
		for _, item := range report.CurrencyBreakdown {
			currencyMap[item.Currency] = item
		}
		cny, ok := currencyMap["CNY"]
		if !ok {
			t.Fatalf("缺少 CNY 拆分明细: %+v", report.CurrencyBreakdown)
		}
		if cny.EntryCount != 2 || cny.DepositCount != 1 || cny.PayoutCount != 1 {
			t.Fatalf("CNY 笔数拆分错误: %+v", cny)
		}
		if !floatEqual(cny.DepositTotal, 100) || !floatEqual(cny.PayoutTotal, 40) || !floatEqual(cny.NetProfit, 60) {
			t.Fatalf("CNY 金额拆分错误: %+v", cny)
		}
		usdt, ok := currencyMap["USDT"]
		if !ok {
			t.Fatalf("缺少 USDT 拆分明细: %+v", report.CurrencyBreakdown)
		}
		if usdt.EntryCount != 1 || usdt.DepositCount != 1 || usdt.PayoutCount != 0 {
			t.Fatalf("USDT 笔数拆分错误: %+v", usdt)
		}
		if !floatEqual(usdt.DepositTotal, 10) || !floatEqual(usdt.PayoutTotal, 0) || !floatEqual(usdt.NetProfit, 10) {
			t.Fatalf("USDT 金额拆分错误: %+v", usdt)
		}

		if _, found, err := database.GetDailySettlementReport(emptyChat, "2026-02-18"); err != nil {
			t.Fatalf("读取空群报表失败: %v", err)
		} else if found {
			t.Fatalf("空群不应生成结算报表")
		}

		if _, err := database.RunDailySettlementBatch("2026-02-18", periodStart, periodEnd); err != nil {
			t.Fatalf("重复执行跑批失败: %v", err)
		}
		rowCount := mustCountRows(t, database, `SELECT COUNT(1) FROM daily_settlement_reports WHERE chat_id = ? AND report_date = ?`, chatWithData, "2026-02-18")
		if rowCount != 1 {
			t.Fatalf("同一账期报表应幂等更新，期望=1 实际=%d", rowCount)
		}
	})
}

func TestDailySettlementReportIgnoresLargeUnrelatedHistory(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "daily-settlement-history.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100430299
	if _, err := database.EnsureGroup(chatID, "日报历史基线群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}

	applyWithCreatedAt := func(messageID int, entryType string, finalAmount float64, createdAt string) {
		t.Helper()
		apply, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        messageID,
			UserID:           430299,
			Username:         "daily-history",
			Nickname:         "DailyHistory",
			EntryType:        entryType,
			Amount:           finalAmount,
			Currency:         "CNY",
			BaseAmount:       finalAmount,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      finalAmount,
			Note:             "daily-settlement-history",
			TrackMessageLink: "tg://msg/daily-history",
		})
		if applyErr != nil {
			t.Fatalf("写入日报历史样本失败: %v", applyErr)
		}
		if _, updateErr := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, createdAt, apply.EntryID); updateErr != nil {
			t.Fatalf("设置日报历史样本时间失败: %v", updateErr)
		}
	}

	for i := 0; i < 200; i++ {
		applyWithCreatedAt(43299000+i, "deposit", float64(10+i%5), "2026-01-01 00:00:00")
	}
	applyWithCreatedAt(43299201, "deposit", 100, "2026-02-18 01:00:00")
	applyWithCreatedAt(43299202, "payout", 30, "2026-02-18 12:00:00")
	applyWithCreatedAt(43299203, "deposit", 70, "2026-03-01 00:00:00")

	report, err := database.BuildDailySettlementReport(
		chatID,
		"2026-02-18",
		time.Date(2026, 2, 18, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("构建日报失败: %v", err)
	}
	if report.TotalEntries != 2 || report.DepositCount != 1 || report.PayoutCount != 1 {
		t.Fatalf("日报不应受窗口外历史影响: %+v", report)
	}
	if !floatEqual(report.DepositTotal, 100) || !floatEqual(report.PayoutTotal, 30) || !floatEqual(report.NetProfit, 70) {
		t.Fatalf("日报金额不应受窗口外历史影响: dep=%v pay=%v net=%v", report.DepositTotal, report.PayoutTotal, report.NetProfit)
	}
}

func TestRunDailySettlementBatchSkipsDisabledGroups(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "daily-settlement-disabled-groups.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const (
		enabledChat  int64 = -100430301
		disabledChat int64 = -100430302
	)
	if _, err := database.EnsureGroup(enabledChat, "启用日报群"); err != nil {
		t.Fatalf("初始化启用日报群失败: %v", err)
	}
	if _, err := database.EnsureGroup(disabledChat, "停用日报群"); err != nil {
		t.Fatalf("初始化停用日报群失败: %v", err)
	}
	if err := database.UpdateGroupFields(enabledChat, map[string]any{"bookkeeping_enabled": 1}); err != nil {
		t.Fatalf("开启启用日报群记账失败: %v", err)
	}

	applyWithCreatedAt := func(chatID int64, messageID int, finalAmount float64) {
		t.Helper()
		apply, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        messageID,
			UserID:           430301,
			Username:         "daily-enabled-filter",
			Nickname:         "DailyEnabledFilter",
			EntryType:        "deposit",
			Amount:           finalAmount,
			Currency:         "CNY",
			BaseAmount:       finalAmount,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      finalAmount,
			Note:             "daily-settlement-disabled-groups",
			TrackMessageLink: "tg://msg/daily-enabled-filter",
		})
		if applyErr != nil {
			t.Fatalf("写入日报样本失败: %v", applyErr)
		}
		if _, updateErr := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, "2026-02-18 08:00:00", apply.EntryID); updateErr != nil {
			t.Fatalf("设置日报样本时间失败: %v", updateErr)
		}
	}

	applyWithCreatedAt(enabledChat, 43030101, 100)
	applyWithCreatedAt(disabledChat, 43030201, 88)

	periodStart := time.Date(2026, 2, 18, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC)
	result, err := database.RunDailySettlementBatch("2026-02-18", periodStart, periodEnd)
	if err != nil {
		t.Fatalf("执行 T+1 结算跑批失败: %v", err)
	}
	if result.TotalGroups != 1 || result.GeneratedReports != 1 || result.SkippedGroups != 0 {
		t.Fatalf("停用群不应进入 T+1 扫描口径: %+v", result)
	}

	if _, found, err := database.GetDailySettlementReport(enabledChat, "2026-02-18"); err != nil {
		t.Fatalf("读取启用日报群报表失败: %v", err)
	} else if !found {
		t.Fatalf("启用日报群应生成报表")
	}
	if _, found, err := database.GetDailySettlementReport(disabledChat, "2026-02-18"); err != nil {
		t.Fatalf("读取停用日报群报表失败: %v", err)
	} else if found {
		t.Fatalf("停用日报群不应生成报表")
	}
}
