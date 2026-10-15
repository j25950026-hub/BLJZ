package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestControlPoint145SettlementGeneration(t *testing.T) {
	t.Run("控制点 1.4.5 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "day-cut-settlement.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const (
			chatWithData int64 = -100145001
			emptyChat    int64 = -100145002
		)
		if _, err := database.EnsureGroup(chatWithData, "日切结算群A"); err != nil {
			t.Fatalf("初始化有数据群失败: %v", err)
		}
		if _, err := database.EnsureGroup(emptyChat, "日切结算群B"); err != nil {
			t.Fatalf("初始化空群失败: %v", err)
		}
		if err := database.UpdateGroupFields(chatWithData, map[string]any{"bookkeeping_enabled": 1}); err != nil {
			t.Fatalf("开启有数据群记账失败: %v", err)
		}
		if err := database.UpdateGroupFields(emptyChat, map[string]any{"bookkeeping_enabled": 1}); err != nil {
			t.Fatalf("开启空群记账失败: %v", err)
		}
		if err := database.UpdateGroupFields(chatWithData, map[string]any{"day_cut_time": "04:00:00"}); err != nil {
			t.Fatalf("设置日切时间失败: %v", err)
		}
		if err := database.UpdateGroupFields(emptyChat, map[string]any{"day_cut_time": "04:00:00"}); err != nil {
			t.Fatalf("设置空群日切时间失败: %v", err)
		}

		applyWithCreatedAt := func(messageID int, entryType string, baseAmount, finalAmount float64, createdAt string) {
			t.Helper()
			apply, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
				ChatID:           chatWithData,
				MessageID:        messageID,
				UserID:           145001,
				Username:         "cp145",
				Nickname:         "CP145",
				EntryType:        entryType,
				Amount:           baseAmount,
				Currency:         "CNY",
				BaseAmount:       baseAmount,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      finalAmount,
				Note:             "day-cut-settlement-test",
				TrackMessageLink: "tg://msg/day-cut-settlement",
			})
			if applyErr != nil {
				t.Fatalf("写入日切结算样本失败: %v", applyErr)
			}
			if apply.Duplicate || apply.EntryID == 0 {
				t.Fatalf("写入日切结算样本结果异常: %+v", apply)
			}
			if _, updateErr := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, createdAt, apply.EntryID); updateErr != nil {
				t.Fatalf("设置样本时间失败: %v", updateErr)
			}
		}

		applyWithCreatedAt(14501, "deposit", 100, 98, "2026-02-18 20:30:00")
		applyWithCreatedAt(14502, "payout", 50, 51, "2026-02-19 12:00:00")
		applyWithCreatedAt(14503, "deposit", 90, 88.2, "2026-02-18 19:59:59")
		applyWithCreatedAt(14504, "deposit", 70, 68.6, "2026-02-19 20:00:00")

		runAt := time.Date(2026, 2, 20, 4, 0, 5, 0, time.FixedZone("CST", 8*3600))
		result, err := database.RunDayCutSettlementBatch(runAt, "Asia/Shanghai")
		if err != nil {
			t.Fatalf("执行日切结算跑批失败: %v", err)
		}
		if result.TotalGroups != 2 || result.GeneratedReports != 1 || result.SkippedGroups != 1 {
			t.Fatalf("日切结算跑批汇总错误: %+v", result)
		}

		report, found, err := database.GetDayCutSettlementReport(chatWithData, "2026-02-19")
		if err != nil {
			t.Fatalf("读取日切结算单失败: %v", err)
		}
		if !found {
			t.Fatalf("应生成有数据群的日切结算单")
		}
		if report.DayCutTime != "04:00:00" {
			t.Fatalf("日切时间错误: %s", report.DayCutTime)
		}
		if report.TotalEntries != 2 || report.DepositCount != 1 || report.PayoutCount != 1 {
			t.Fatalf("日切结算单笔数错误: %+v", report)
		}
		if !floatEqual(report.DepositFeeTotal, 2) || !floatEqual(report.PayoutFeeTotal, 1) || !floatEqual(report.NetFeeTotal, 1) {
			t.Fatalf("日切结算费用统计错误: dep=%v pay=%v net=%v", report.DepositFeeTotal, report.PayoutFeeTotal, report.NetFeeTotal)
		}
		if len(report.CurrencyBreakdown) != 1 {
			t.Fatalf("币种拆分应仅包含 CNY: %+v", report.CurrencyBreakdown)
		}
		cny := report.CurrencyBreakdown[0]
		if cny.Currency != "CNY" || cny.EntryCount != 2 || cny.DepositCount != 1 || cny.PayoutCount != 1 {
			t.Fatalf("CNY 拆分笔数错误: %+v", cny)
		}
		if !floatEqual(cny.DepositFeeTotal, 2) || !floatEqual(cny.PayoutFeeTotal, 1) || !floatEqual(cny.NetFeeTotal, 1) {
			t.Fatalf("CNY 拆分费用错误: %+v", cny)
		}

		if _, found, err := database.GetDayCutSettlementReport(emptyChat, "2026-02-19"); err != nil {
			t.Fatalf("读取空群日切结算单失败: %v", err)
		} else if found {
			t.Fatalf("空群不应生成日切结算单")
		}

		if _, err := database.RunDayCutSettlementBatch(runAt, "Asia/Shanghai"); err != nil {
			t.Fatalf("重复执行日切结算跑批失败: %v", err)
		}
		rowCount := mustCountRows(t, database, `SELECT COUNT(1) FROM day_cut_settlement_reports WHERE chat_id = ? AND report_date = ?`, chatWithData, "2026-02-19")
		if rowCount != 1 {
			t.Fatalf("同一账期日切结算单应幂等更新，期望=1 实际=%d", rowCount)
		}
	})
}

func TestDayCutSettlementReportIgnoresLargeUnrelatedHistory(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "day-cut-settlement-history.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100145099
	if _, err := database.EnsureGroup(chatID, "日切历史基线群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}

	applyWithCreatedAt := func(messageID int, entryType string, baseAmount, finalAmount float64, createdAt string) {
		t.Helper()
		apply, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        messageID,
			UserID:           145099,
			Username:         "day-cut-history",
			Nickname:         "DayCutHistory",
			EntryType:        entryType,
			Amount:           baseAmount,
			Currency:         "CNY",
			BaseAmount:       baseAmount,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      finalAmount,
			Note:             "day-cut-history",
			TrackMessageLink: "tg://msg/day-cut-history",
		})
		if applyErr != nil {
			t.Fatalf("写入日切历史样本失败: %v", applyErr)
		}
		if _, updateErr := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, createdAt, apply.EntryID); updateErr != nil {
			t.Fatalf("设置日切历史样本时间失败: %v", updateErr)
		}
	}

	for i := 0; i < 200; i++ {
		applyWithCreatedAt(14599000+i, "deposit", 10, 9.8, "2026-01-01 00:00:00")
	}
	applyWithCreatedAt(14599201, "deposit", 100, 98, "2026-02-18 20:30:00")
	applyWithCreatedAt(14599202, "payout", 50, 51, "2026-02-19 12:00:00")
	applyWithCreatedAt(14599203, "deposit", 80, 78.4, "2026-02-19 20:00:00")

	report, err := database.BuildDayCutSettlementReport(
		chatID,
		"2026-02-19",
		"04:00:00",
		time.Date(2026, 2, 18, 20, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 19, 20, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("构建日切报表失败: %v", err)
	}
	if report.TotalEntries != 2 || report.DepositCount != 1 || report.PayoutCount != 1 {
		t.Fatalf("日切报表不应受窗口外历史影响: %+v", report)
	}
	if !floatEqual(report.DepositFeeTotal, 2) || !floatEqual(report.PayoutFeeTotal, 1) || !floatEqual(report.NetFeeTotal, 1) {
		t.Fatalf("日切费用不应受窗口外历史影响: dep=%v pay=%v net=%v", report.DepositFeeTotal, report.PayoutFeeTotal, report.NetFeeTotal)
	}
}

func TestControlPoint334ForcedCutOff(t *testing.T) {
	t.Run("控制点 3.3.4 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "forced-day-cut.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const chatID int64 = -100334001
		if _, err := database.EnsureGroup(chatID, "强制日切测试群"); err != nil {
			t.Fatalf("初始化群配置失败: %v", err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{"bookkeeping_enabled": 1}); err != nil {
			t.Fatalf("开启强制日切群记账失败: %v", err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{"day_cut_time": "00:00:00"}); err != nil {
			t.Fatalf("设置日切时间失败: %v", err)
		}

		applyWithCreatedAt := func(messageID int, entryType string, baseAmount, finalAmount float64, createdAt string) {
			t.Helper()
			apply, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
				ChatID:           chatID,
				MessageID:        messageID,
				UserID:           334001,
				Username:         "cp334",
				Nickname:         "CP334",
				EntryType:        entryType,
				Amount:           baseAmount,
				Currency:         "CNY",
				BaseAmount:       baseAmount,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      finalAmount,
				Note:             "forced-day-cut-test",
				TrackMessageLink: "tg://msg/forced-day-cut",
			})
			if applyErr != nil {
				t.Fatalf("写入强制日切样本失败: %v", applyErr)
			}
			if apply.Duplicate || apply.EntryID == 0 {
				t.Fatalf("写入强制日切样本结果异常: %+v", apply)
			}
			if _, updateErr := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, createdAt, apply.EntryID); updateErr != nil {
				t.Fatalf("设置强制日切样本时间失败: %v", updateErr)
			}
		}

		applyWithCreatedAt(33401, "deposit", 100, 98, "2026-02-17 08:00:00")
		applyWithCreatedAt(33402, "payout", 80, 84, "2026-02-18 09:00:00")
		applyWithCreatedAt(33403, "deposit", 60, 58.8, "2026-02-19 10:00:00")

		firstRunAt := time.Date(2026, 2, 18, 0, 0, 5, 0, time.UTC)
		firstResult, err := database.RunDayCutSettlementBatch(firstRunAt, "UTC")
		if err != nil {
			t.Fatalf("执行首日切跑批失败: %v", err)
		}
		if firstResult.TotalGroups != 1 || firstResult.GeneratedReports != 1 || firstResult.ForcedReports != 0 || firstResult.DeficitReports != 0 || firstResult.SkippedGroups != 0 {
			t.Fatalf("首日切跑批汇总错误: %+v", firstResult)
		}

		secondRunAt := time.Date(2026, 2, 20, 0, 0, 5, 0, time.UTC)
		secondResult, err := database.RunDayCutSettlementBatch(secondRunAt, "UTC")
		if err != nil {
			t.Fatalf("执行补跑日切失败: %v", err)
		}
		if secondResult.TotalGroups != 1 || secondResult.GeneratedReports != 2 || secondResult.ForcedReports != 1 || secondResult.DeficitReports != 1 || secondResult.SkippedGroups != 0 {
			t.Fatalf("补跑日切汇总错误: %+v", secondResult)
		}

		report, found, err := database.GetDayCutSettlementReport(chatID, "2026-02-18")
		if err != nil {
			t.Fatalf("读取补跑日切结算单失败: %v", err)
		}
		if !found {
			t.Fatalf("应补齐缺失账期的日切结算单")
		}
		if report.TotalEntries != 1 || report.DepositCount != 0 || report.PayoutCount != 1 {
			t.Fatalf("补跑日切结算单笔数错误: %+v", report)
		}
		if !floatEqual(report.NetFeeTotal, -4) {
			t.Fatalf("补跑日切应暴露亏空，期望净手续费=-4，实际=%v", report.NetFeeTotal)
		}

		thirdResult, err := database.RunDayCutSettlementBatch(secondRunAt, "UTC")
		if err != nil {
			t.Fatalf("执行无积压场景跑批失败: %v", err)
		}
		if thirdResult.GeneratedReports != 0 || thirdResult.ForcedReports != 0 || thirdResult.DeficitReports != 0 || thirdResult.SkippedGroups != 1 {
			t.Fatalf("无积压场景汇总错误: %+v", thirdResult)
		}
	})
}

func TestRunDayCutSettlementBatchSkipsDisabledGroups(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "day-cut-disabled-groups.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const (
		enabledChat  int64 = -100145301
		disabledChat int64 = -100145302
	)
	if _, err := database.EnsureGroup(enabledChat, "启用日切群"); err != nil {
		t.Fatalf("初始化启用日切群失败: %v", err)
	}
	if _, err := database.EnsureGroup(disabledChat, "停用日切群"); err != nil {
		t.Fatalf("初始化停用日切群失败: %v", err)
	}
	if err := database.UpdateGroupFields(enabledChat, map[string]any{
		"bookkeeping_enabled": 1,
		"day_cut_time":        "04:00:00",
	}); err != nil {
		t.Fatalf("开启启用日切群记账失败: %v", err)
	}
	if err := database.UpdateGroupFields(disabledChat, map[string]any{"day_cut_time": "04:00:00"}); err != nil {
		t.Fatalf("设置停用日切群日切时间失败: %v", err)
	}

	applyWithCreatedAt := func(chatID int64, messageID int, baseAmount, finalAmount float64) {
		t.Helper()
		apply, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        messageID,
			UserID:           145301,
			Username:         "day-cut-enabled-filter",
			Nickname:         "DayCutEnabledFilter",
			EntryType:        "deposit",
			Amount:           baseAmount,
			Currency:         "CNY",
			BaseAmount:       baseAmount,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      finalAmount,
			Note:             "day-cut-disabled-groups",
			TrackMessageLink: "tg://msg/day-cut-enabled-filter",
		})
		if applyErr != nil {
			t.Fatalf("写入日切样本失败: %v", applyErr)
		}
		if _, updateErr := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, "2026-02-18 20:30:00", apply.EntryID); updateErr != nil {
			t.Fatalf("设置日切样本时间失败: %v", updateErr)
		}
	}

	applyWithCreatedAt(enabledChat, 14530101, 100, 98)
	applyWithCreatedAt(disabledChat, 14530201, 80, 78.4)

	runAt := time.Date(2026, 2, 20, 4, 0, 5, 0, time.FixedZone("CST", 8*3600))
	result, err := database.RunDayCutSettlementBatch(runAt, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("执行日切结算跑批失败: %v", err)
	}
	if result.TotalGroups != 1 || result.GeneratedReports != 1 || result.SkippedGroups != 0 {
		t.Fatalf("停用群不应进入日切扫描口径: %+v", result)
	}

	if _, found, err := database.GetDayCutSettlementReport(enabledChat, "2026-02-19"); err != nil {
		t.Fatalf("读取启用日切群报表失败: %v", err)
	} else if !found {
		t.Fatalf("启用日切群应生成报表")
	}
	if _, found, err := database.GetDayCutSettlementReport(disabledChat, "2026-02-19"); err != nil {
		t.Fatalf("读取停用日切群报表失败: %v", err)
	} else if found {
		t.Fatalf("停用日切群不应生成报表")
	}
}
