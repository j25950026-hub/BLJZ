package db

import (
	"path/filepath"
	"testing"
)

func TestControlPoint451ScheduledReconciliation(t *testing.T) {
	t.Run("控制点 4.5.1 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "scheduled-reconciliation.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const (
			chatConsistent int64 = -100451001
			chatMissing    int64 = -100451002
			chatDisabled   int64 = -100451003
			chatOrphan     int64 = -100451004
		)
		if _, err := database.EnsureGroup(chatConsistent, "对账群A"); err != nil {
			t.Fatalf("初始化对账群A失败: %v", err)
		}
		if _, err := database.EnsureGroup(chatMissing, "对账群B"); err != nil {
			t.Fatalf("初始化对账群B失败: %v", err)
		}
		if _, err := database.EnsureGroup(chatDisabled, "对账群C"); err != nil {
			t.Fatalf("初始化对账群C失败: %v", err)
		}
		if err := database.UpdateGroupFields(chatConsistent, map[string]any{"bookkeeping_enabled": 1}); err != nil {
			t.Fatalf("开启对账群A记账失败: %v", err)
		}
		if err := database.UpdateGroupFields(chatMissing, map[string]any{"bookkeeping_enabled": 1}); err != nil {
			t.Fatalf("开启对账群B记账失败: %v", err)
		}

		applyEntry := func(chatID int64, messageID int, entryType string, finalAmount float64) {
			t.Helper()
			apply, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
				ChatID:           chatID,
				MessageID:        messageID,
				UserID:           4510,
				Username:         "recon",
				Nickname:         "Recon",
				EntryType:        entryType,
				Amount:           finalAmount,
				Currency:         "USDT",
				BaseAmount:       finalAmount,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      finalAmount,
				Note:             "scheduled-reconciliation",
				TrackMessageLink: "tg://msg/recon",
			})
			if applyErr != nil {
				t.Fatalf("写入对账样本失败: %v", applyErr)
			}
			if apply.Duplicate || apply.EntryID == 0 {
				t.Fatalf("写入对账样本结果异常: %+v", apply)
			}
		}

		applyEntry(chatConsistent, 45101, "deposit", 120)
		applyEntry(chatConsistent, 45102, "payout", 35)
		applyEntry(chatMissing, 45103, "deposit", 10)
		applyEntry(chatDisabled, 45104, "deposit", 22)

		baseResult, err := database.RunScheduledReconciliation(DefaultScheduledReconciliationTolerance)
		if err != nil {
			t.Fatalf("基线对账失败: %v", err)
		}
		if baseResult.CheckedChats != 2 {
			t.Fatalf("基线对账仅应覆盖启用群: %+v", baseResult)
		}
		if baseResult.DifferenceCount != 0 || len(baseResult.Differences) != 0 {
			t.Fatalf("基线数据应无差异: %+v", baseResult)
		}

		if _, err := database.raw.Exec(
			`UPDATE ledger_balances SET deposit_total = ?, updated_at = ? WHERE chat_id = ?`,
			100.0,
			nowUTCText(),
			chatConsistent,
		); err != nil {
			t.Fatalf("篡改对账群A余额失败: %v", err)
		}
		if _, err := database.raw.Exec(
			`DELETE FROM ledger_balances WHERE chat_id = ?`,
			chatMissing,
		); err != nil {
			t.Fatalf("删除对账群B余额快照失败: %v", err)
		}
		if _, err := database.raw.Exec(
			`UPDATE ledger_balances SET deposit_total = ?, updated_at = ? WHERE chat_id = ?`,
			0.0,
			nowUTCText(),
			chatDisabled,
		); err != nil {
			t.Fatalf("篡改停用群余额失败: %v", err)
		}
		if _, err := database.raw.Exec(
			`INSERT INTO ledger_balances (chat_id, deposit_total, payout_total, updated_at) VALUES (?, ?, ?, ?)`,
			chatOrphan,
			9.0,
			4.0,
			nowUTCText(),
		); err != nil {
			t.Fatalf("写入孤立余额快照失败: %v", err)
		}

		result, err := database.RunScheduledReconciliation(-1)
		if err != nil {
			t.Fatalf("执行定时对账失败: %v", err)
		}
		if result.Tolerance != DefaultScheduledReconciliationTolerance {
			t.Fatalf("负数容差应回退默认值: %+v", result)
		}
		if result.CheckedChats != 2 {
			t.Fatalf("对账覆盖群数量错误: %+v", result)
		}
		if result.DifferenceCount != 2 || len(result.Differences) != 2 {
			t.Fatalf("差异数量错误: %+v", result)
		}

		diffByChat := make(map[int64]ScheduledReconciliationDifference, len(result.Differences))
		for _, item := range result.Differences {
			diffByChat[item.ChatID] = item
		}

		diffA, ok := diffByChat[chatConsistent]
		if !ok {
			t.Fatalf("应包含对账群A差异: %+v", result.Differences)
		}
		if !floatEqual(diffA.TransactionDepositTotal, 120) || !floatEqual(diffA.BalanceDepositTotal, 100) || !floatEqual(diffA.DepositDifference, 20) {
			t.Fatalf("对账群A入款差异错误: %+v", diffA)
		}
		if !floatEqual(diffA.TransactionPayoutTotal, 35) || !floatEqual(diffA.BalancePayoutTotal, 35) || !floatEqual(diffA.PayoutDifference, 0) {
			t.Fatalf("对账群A下发差异错误: %+v", diffA)
		}

		diffB, ok := diffByChat[chatMissing]
		if !ok {
			t.Fatalf("应包含对账群B差异: %+v", result.Differences)
		}
		if !floatEqual(diffB.TransactionDepositTotal, 10) || !floatEqual(diffB.BalanceDepositTotal, 0) || !floatEqual(diffB.DepositDifference, 10) {
			t.Fatalf("对账群B入款差异错误: %+v", diffB)
		}
		if !floatEqual(diffB.TransactionPayoutTotal, 0) || !floatEqual(diffB.BalancePayoutTotal, 0) || !floatEqual(diffB.PayoutDifference, 0) {
			t.Fatalf("对账群B下发差异错误: %+v", diffB)
		}
		if _, ok := diffByChat[chatDisabled]; ok {
			t.Fatalf("停用群不应进入定时对账差异: %+v", result.Differences)
		}
		if _, ok := diffByChat[chatOrphan]; ok {
			t.Fatalf("孤立余额快照不应进入定时对账差异: %+v", result.Differences)
		}
	})
}

func TestScheduledReconciliationTolerance(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "scheduled-reconciliation-tolerance.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100451101
	if _, err := database.EnsureGroup(chatID, "容差对账群"); err != nil {
		t.Fatalf("初始化容差对账群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"bookkeeping_enabled": 1}); err != nil {
		t.Fatalf("开启容差对账群记账失败: %v", err)
	}
	apply, err := database.ApplyLedgerEntry(LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        45111,
		UserID:           4511,
		Username:         "recon",
		Nickname:         "Recon",
		EntryType:        "deposit",
		Amount:           10,
		Currency:         "USDT",
		BaseAmount:       10,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      10,
		Note:             "scheduled-reconciliation-tolerance",
		TrackMessageLink: "tg://msg/recon-tolerance",
	})
	if err != nil {
		t.Fatalf("写入容差样本失败: %v", err)
	}
	if apply.Duplicate || apply.EntryID == 0 {
		t.Fatalf("写入容差样本结果异常: %+v", apply)
	}
	if _, err := database.raw.Exec(
		`UPDATE ledger_balances SET deposit_total = ?, updated_at = ? WHERE chat_id = ?`,
		10.0000005,
		nowUTCText(),
		chatID,
	); err != nil {
		t.Fatalf("设置容差样本余额失败: %v", err)
	}

	withDefaultTolerance, err := database.RunScheduledReconciliation(DefaultScheduledReconciliationTolerance)
	if err != nil {
		t.Fatalf("默认容差对账失败: %v", err)
	}
	if withDefaultTolerance.DifferenceCount != 0 {
		t.Fatalf("默认容差下不应记录差异: %+v", withDefaultTolerance)
	}

	withStrictTolerance, err := database.RunScheduledReconciliation(1e-8)
	if err != nil {
		t.Fatalf("严格容差对账失败: %v", err)
	}
	if withStrictTolerance.DifferenceCount != 1 {
		t.Fatalf("严格容差下应检测到差异: %+v", withStrictTolerance)
	}
}
