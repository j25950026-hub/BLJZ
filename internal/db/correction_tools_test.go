package db

import (
	"path/filepath"
	"testing"
)

func TestControlPoint455CorrectionTools(t *testing.T) {
	t.Run("控制点 4.5.5 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "correction-tools.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const (
			chatDirty  int64 = -100455001
			chatOrphan int64 = -100455002
			userID     int64 = 45501
		)
		if _, err := database.EnsureGroup(chatDirty, "控制点4.5.5脏数据群"); err != nil {
			t.Fatalf("初始化群组失败: %v", err)
		}

		applyLedger := func(messageID int, entryType string, finalAmount float64) {
			t.Helper()
			result, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
				ChatID:           chatDirty,
				MessageID:        messageID,
				UserID:           userID,
				Username:         "cp455",
				Nickname:         "ControlPoint455",
				EntryType:        entryType,
				Amount:           finalAmount,
				Currency:         "USDT",
				BaseAmount:       finalAmount,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      finalAmount,
				Note:             "control-point-455",
				TrackMessageLink: "tg://msg/control-point-455",
			})
			if applyErr != nil {
				t.Fatalf("写入样本分录失败: %v", applyErr)
			}
			if result.Duplicate || result.EntryID == 0 {
				t.Fatalf("写入样本分录结果异常: %+v", result)
			}
		}

		applyLedger(45501, "deposit", 100)
		applyLedger(45502, "payout", 40)

		if err := database.flushHotAccountPending(); err != nil {
			t.Fatalf("刷新热点余额失败: %v", err)
		}
		baseline, err := database.RunCorrectionTool(CorrectionToolInput{
			Scope:  CorrectionScopeAll,
			DryRun: true,
		})
		if err != nil {
			t.Fatalf("基线巡检失败: %v", err)
		}
		if baseline.DirtyLedgerChats != 0 || len(baseline.LedgerBalanceIssues) != 0 {
			t.Fatalf("基线数据不应存在账务脏数据: %+v", baseline)
		}
		if baseline.DirtyAccountBalances != 0 || len(baseline.AccountBalanceIssues) != 0 {
			t.Fatalf("基线数据不应存在账户余额脏数据: %+v", baseline)
		}

		if _, err := database.raw.Exec(
			`UPDATE ledger_balances SET deposit_total = ?, payout_total = ?, updated_at = ? WHERE chat_id = ?`,
			1.0,
			2.0,
			nowUTCText(),
			chatDirty,
		); err != nil {
			t.Fatalf("篡改脏账样本失败: %v", err)
		}
		if _, err := database.raw.Exec(
			`INSERT INTO ledger_balances (chat_id, deposit_total, payout_total, updated_at) VALUES (?, ?, ?, ?)`,
			chatOrphan,
			9.0,
			3.0,
			nowUTCText(),
		); err != nil {
			t.Fatalf("写入孤立余额样本失败: %v", err)
		}
		if _, err := database.raw.Exec(
			`UPDATE account_balances SET balance = ?, version = version + 1, updated_at = ? WHERE user_id = ? AND currency = ?`,
			999.0,
			nowUTCText(),
			userID,
			"USDT",
		); err != nil {
			t.Fatalf("篡改账户余额样本失败: %v", err)
		}

		preview, err := database.RunCorrectionTool(CorrectionToolInput{
			Scope:  CorrectionScopeLedgerBalances,
			ChatID: chatDirty,
			DryRun: true,
		})
		if err != nil {
			t.Fatalf("定向脏数据预检失败: %v", err)
		}
		if preview.CheckedChats != 1 {
			t.Fatalf("定向预检覆盖群数量错误: %+v", preview)
		}
		if preview.DirtyLedgerChats != 1 || len(preview.LedgerBalanceIssues) != 1 {
			t.Fatalf("定向预检脏数据数量错误: %+v", preview)
		}
		if preview.DirtyAccountBalances != 0 || len(preview.AccountBalanceIssues) != 0 {
			t.Fatalf("定向账务预检不应扫描账户余额脏数据: %+v", preview)
		}
		issue := preview.LedgerBalanceIssues[0]
		if issue.ChatID != chatDirty {
			t.Fatalf("定向预检群组错误: %+v", issue)
		}
		if !floatEqual(issue.CurrentDepositTotal, 1) || !floatEqual(issue.CurrentPayoutTotal, 2) {
			t.Fatalf("定向预检当前余额错误: %+v", issue)
		}
		if !floatEqual(issue.ExpectedDepositTotal, 100) || !floatEqual(issue.ExpectedPayoutTotal, 40) {
			t.Fatalf("定向预检期望余额错误: %+v", issue)
		}

		fixResult, err := database.RunCorrectionTool(CorrectionToolInput{
			Scope:  CorrectionScopeAll,
			DryRun: false,
		})
		if err != nil {
			t.Fatalf("执行修正失败: %v", err)
		}
		if fixResult.DirtyLedgerChats != 2 || fixResult.FixedLedgerChats != 2 {
			t.Fatalf("修正账务脏数据数量错误: %+v", fixResult)
		}
		if fixResult.DirtyAccountBalances != 1 || fixResult.FixedAccountBalances != 1 {
			t.Fatalf("修正账户余额脏数据数量错误: %+v", fixResult)
		}
		if !fixResult.AccountBalancesRebuilt {
			t.Fatalf("执行修正时应重建账户余额快照: %+v", fixResult)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(chatDirty)
		if err != nil {
			t.Fatalf("查询修正后群余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 100) || !floatEqual(payoutTotal, 40) {
			t.Fatalf("修正后群余额错误: deposit=%v payout=%v", depositTotal, payoutTotal)
		}

		orphanDeposit, orphanPayout, err := database.GetLedgerBalance(chatOrphan)
		if err != nil {
			t.Fatalf("查询修正后孤立余额失败: %v", err)
		}
		if !floatEqual(orphanDeposit, 0) || !floatEqual(orphanPayout, 0) {
			t.Fatalf("孤立余额应被归零: deposit=%v payout=%v", orphanDeposit, orphanPayout)
		}

		accountBalance, err := database.GetAccountBalance(userID, "USDT")
		if err != nil {
			t.Fatalf("查询修正后账户余额失败: %v", err)
		}
		if !floatEqual(accountBalance, 60) {
			t.Fatalf("修正后账户余额错误: %v", accountBalance)
		}

		afterFix, err := database.RunCorrectionTool(CorrectionToolInput{
			Scope:  CorrectionScopeAll,
			DryRun: true,
		})
		if err != nil {
			t.Fatalf("修正后复检失败: %v", err)
		}
		if afterFix.DirtyLedgerChats != 0 || len(afterFix.LedgerBalanceIssues) != 0 {
			t.Fatalf("修正后不应残留账务脏数据: %+v", afterFix)
		}
		if afterFix.DirtyAccountBalances != 0 || len(afterFix.AccountBalanceIssues) != 0 {
			t.Fatalf("修正后不应残留账户余额脏数据: %+v", afterFix)
		}
	})
}
