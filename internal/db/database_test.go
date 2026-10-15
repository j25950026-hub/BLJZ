package db

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGroupWelcomeEnabledDefaultsOffAndCanBeEnabled(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "group-welcome-enabled.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	group, err := database.EnsureGroup(-10099001, "进群欢迎默认关闭测试群")
	if err != nil {
		t.Fatalf("创建群失败: %v", err)
	}
	if group.WelcomeEnabled != 0 {
		t.Fatalf("新群进群欢迎应默认关闭: %+v", group)
	}

	if err := database.UpdateGroupFields(group.ChatID, map[string]any{"welcome_enabled": 1}); err != nil {
		t.Fatalf("开启进群欢迎失败: %v", err)
	}
	enabled, err := database.GetGroup(group.ChatID)
	if err != nil {
		t.Fatalf("读取群失败: %v", err)
	}
	if enabled.WelcomeEnabled != 1 {
		t.Fatalf("进群欢迎应可被开启: %+v", enabled)
	}
}

func TestControlPoint113ACIDOrchestrator(t *testing.T) {
	t.Run("控制点 1.1.3 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "acid-orchestrator.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		// Force balance writes to fail and verify transaction + balance updates rollback together.
		if _, err := database.raw.Exec(`
CREATE TRIGGER fail_ledger_balance_insert
BEFORE INSERT ON ledger_balances
BEGIN
	SELECT RAISE(FAIL, 'balance write blocked');
END;
`); err != nil {
			t.Fatalf("创建失败注入触发器失败: %v", err)
		}

		input := LedgerEntryInput{
			ChatID:           2001,
			MessageID:        9001,
			UserID:           88,
			Username:         "alice",
			Nickname:         "Alice",
			EntryType:        "deposit",
			Amount:           100,
			Currency:         "CNY",
			BaseAmount:       100,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      100,
			Note:             "acid-check",
			CardNo:           "6222000011112222",
			TrackMessageLink: "tg://msg/9001",
		}

		result, err := database.ApplyLedgerEntry(input)
		if err == nil {
			t.Fatalf("余额更新失败时应整体回滚")
		}
		if result.EntryID != 0 || result.CardSeenCount != 0 || result.Duplicate {
			t.Fatalf("失败写入不应返回成功结果: %+v", result)
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ? AND message_id = ?`, input.ChatID, input.MessageID)
		if ledgerCount != 0 {
			t.Fatalf("事务回滚后 ledger_entries 仍有残留: %d", ledgerCount)
		}
		idempotencyCount := mustCountRows(t, database, `SELECT COUNT(1) FROM message_idempotency WHERE chat_id = ? AND message_id = ? AND command_key = 'ledger'`, input.ChatID, input.MessageID)
		if idempotencyCount != 0 {
			t.Fatalf("事务回滚后 message_idempotency 仍有残留: %d", idempotencyCount)
		}
		balanceCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_balances WHERE chat_id = ?`, input.ChatID)
		if balanceCount != 0 {
			t.Fatalf("事务回滚后 ledger_balances 仍有残留: %d", balanceCount)
		}

		if _, err := database.raw.Exec(`DROP TRIGGER fail_ledger_balance_insert`); err != nil {
			t.Fatalf("删除失败注入触发器失败: %v", err)
		}

		retry, err := database.ApplyLedgerEntry(input)
		if err != nil {
			t.Fatalf("故障恢复后重试应成功: %v", err)
		}
		if retry.Duplicate {
			t.Fatalf("回滚后重试不应被误判为重复")
		}
		if retry.EntryID == 0 {
			t.Fatalf("重试成功应返回有效 entry id")
		}
		if retry.CardSeenCount != 1 {
			t.Fatalf("重试成功后银行卡出现次数应为 1，实际=%d", retry.CardSeenCount)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(input.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 100) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("重试成功后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})

	t.Run("控制点 1.1.3 审计写入失败回滚验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "acid-orchestrator-audit.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		if _, err := database.raw.Exec(`
CREATE TRIGGER fail_ledger_audit_insert
BEFORE INSERT ON audit_logs
BEGIN
	SELECT RAISE(FAIL, 'audit write blocked');
END;
`); err != nil {
			t.Fatalf("创建失败注入触发器失败: %v", err)
		}

		input := LedgerEntryInput{
			ChatID:           2051,
			MessageID:        9151,
			UserID:           96,
			Username:         "audit-checker",
			Nickname:         "AuditChecker",
			EntryType:        "deposit",
			Amount:           120,
			Currency:         "CNY",
			BaseAmount:       120,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      120,
			Note:             "audit-failure-rollback",
			CardNo:           "6222000012340001",
			TrackMessageLink: "tg://msg/9151",
		}

		result, err := database.ApplyLedgerEntry(input)
		if err == nil {
			t.Fatalf("审计写入失败时应整体回滚")
		}
		if result.EntryID != 0 || result.CardSeenCount != 0 || result.Duplicate {
			t.Fatalf("失败写入不应返回成功结果: %+v", result)
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, input.ChatID)
		if ledgerCount != 0 {
			t.Fatalf("事务回滚后 ledger_entries 仍有残留: %d", ledgerCount)
		}
		balanceCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_balances WHERE chat_id = ?`, input.ChatID)
		if balanceCount != 0 {
			t.Fatalf("事务回滚后 ledger_balances 仍有残留: %d", balanceCount)
		}
		cardCount := mustCountRows(t, database, `SELECT COUNT(1) FROM card_history WHERE chat_id = ?`, input.ChatID)
		if cardCount != 0 {
			t.Fatalf("事务回滚后 card_history 仍有残留: %d", cardCount)
		}
		idempotencyCount := mustCountRows(t, database, `SELECT COUNT(1) FROM message_idempotency WHERE chat_id = ? AND message_id = ? AND command_key = 'ledger'`, input.ChatID, input.MessageID)
		if idempotencyCount != 0 {
			t.Fatalf("事务回滚后 message_idempotency 仍有残留: %d", idempotencyCount)
		}
		auditCount := mustCountRows(t, database, `SELECT COUNT(1) FROM audit_logs WHERE chat_id = ? AND message_id = ?`, input.ChatID, input.MessageID)
		if auditCount != 0 {
			t.Fatalf("事务回滚后 audit_logs 仍有残留: %d", auditCount)
		}

		if _, err := database.raw.Exec(`DROP TRIGGER fail_ledger_audit_insert`); err != nil {
			t.Fatalf("删除失败注入触发器失败: %v", err)
		}

		retry, err := database.ApplyLedgerEntry(input)
		if err != nil {
			t.Fatalf("故障恢复后重试应成功: %v", err)
		}
		if retry.Duplicate {
			t.Fatalf("回滚后重试不应被误判为重复")
		}
		if retry.EntryID == 0 {
			t.Fatalf("重试成功应返回有效 entry id")
		}
		if retry.CardSeenCount != 1 {
			t.Fatalf("重试成功后银行卡出现次数应为 1，实际=%d", retry.CardSeenCount)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(input.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 120) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("重试成功后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}

		auditCount = mustCountRows(t, database, `SELECT COUNT(1) FROM audit_logs WHERE chat_id = ? AND message_id = ?`, input.ChatID, input.MessageID)
		if auditCount != 1 {
			t.Fatalf("重试成功后 audit_logs 条数应为 1，实际=%d", auditCount)
		}
	})

	t.Run("控制点 1.1.3 余额更新路径回滚验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "acid-orchestrator-balance-update.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		seed := LedgerEntryInput{
			ChatID:           2101,
			MessageID:        9301,
			UserID:           108,
			Username:         "carol",
			Nickname:         "Carol",
			EntryType:        "deposit",
			Amount:           60,
			Currency:         "CNY",
			BaseAmount:       60,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      60,
			Note:             "seed-balance-row",
			CardNo:           "6222000012345678",
			TrackMessageLink: "tg://msg/9301",
		}
		seedResult, err := database.ApplyLedgerEntry(seed)
		if err != nil {
			t.Fatalf("准备余额快照失败: %v", err)
		}
		if seedResult.EntryID == 0 || seedResult.Duplicate {
			t.Fatalf("准备余额快照结果异常: %+v", seedResult)
		}

		if _, err := database.raw.Exec(`
CREATE TRIGGER fail_apply_ledger_balance_update
BEFORE UPDATE ON ledger_balances
BEGIN
	SELECT RAISE(FAIL, 'balance update blocked during apply');
END;
`); err != nil {
			t.Fatalf("创建失败注入触发器失败: %v", err)
		}

		second := seed
		second.MessageID = 9302
		second.Amount = 25
		second.BaseAmount = 25
		second.FinalAmount = 25
		second.Note = "should-rollback-on-balance-update"
		second.TrackMessageLink = "tg://msg/9302"

		result, err := database.ApplyLedgerEntry(second)
		if err == nil {
			t.Fatalf("余额更新失败时应整体回滚")
		}
		if result.EntryID != 0 || result.CardSeenCount != 0 || result.Duplicate {
			t.Fatalf("失败写入不应返回成功结果: %+v", result)
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, second.ChatID)
		if ledgerCount != 1 {
			t.Fatalf("事务回滚后 ledger_entries 条数应保持 1，实际=%d", ledgerCount)
		}
		idempotencyCount := mustCountRows(t, database, `SELECT COUNT(1) FROM message_idempotency WHERE chat_id = ? AND message_id = ? AND command_key = 'ledger'`, second.ChatID, second.MessageID)
		if idempotencyCount != 0 {
			t.Fatalf("事务回滚后 message_idempotency 仍有残留: %d", idempotencyCount)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(second.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 60) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("回滚后余额应保持不变: deposit=%v payout=%v", depositTotal, payoutTotal)
		}

		if _, err := database.raw.Exec(`DROP TRIGGER fail_apply_ledger_balance_update`); err != nil {
			t.Fatalf("删除失败注入触发器失败: %v", err)
		}

		retry, err := database.ApplyLedgerEntry(second)
		if err != nil {
			t.Fatalf("故障恢复后重试应成功: %v", err)
		}
		if retry.Duplicate || retry.EntryID == 0 {
			t.Fatalf("故障恢复后重试结果异常: %+v", retry)
		}

		depositTotal, payoutTotal, err = database.GetLedgerBalance(second.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 85) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("重试成功后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})

	t.Run("控制点 1.1.3 兼容接口原子提交验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "acid-orchestrator-legacy-api.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		if _, err := database.raw.Exec(`
CREATE TRIGGER fail_legacy_balance_insert
BEFORE INSERT ON ledger_balances
BEGIN
	SELECT RAISE(FAIL, 'legacy balance write blocked');
END;
`); err != nil {
			t.Fatalf("创建失败注入触发器失败: %v", err)
		}

		input := LedgerEntryInput{
			ChatID:           2201,
			MessageID:        9401,
			UserID:           128,
			Username:         "david",
			Nickname:         "David",
			EntryType:        "deposit",
			Amount:           35,
			Currency:         "CNY",
			BaseAmount:       35,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      35,
			Note:             "legacy-api-atomic",
			CardNo:           "6222000019988776",
			TrackMessageLink: "tg://msg/9401",
		}

		if _, err := database.AddLedgerEntry(input); err == nil {
			t.Fatalf("余额写入失败时 AddLedgerEntry 应整体回滚")
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, input.ChatID)
		if ledgerCount != 0 {
			t.Fatalf("事务回滚后 ledger_entries 仍有残留: %d", ledgerCount)
		}
		balanceCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_balances WHERE chat_id = ?`, input.ChatID)
		if balanceCount != 0 {
			t.Fatalf("事务回滚后 ledger_balances 仍有残留: %d", balanceCount)
		}

		if _, err := database.raw.Exec(`DROP TRIGGER fail_legacy_balance_insert`); err != nil {
			t.Fatalf("删除失败注入触发器失败: %v", err)
		}

		entryID, err := database.AddLedgerEntry(input)
		if err != nil {
			t.Fatalf("故障恢复后 AddLedgerEntry 重试应成功: %v", err)
		}
		if entryID == 0 {
			t.Fatalf("AddLedgerEntry 成功后应返回有效 entry id")
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(input.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 35) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("重试成功后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})

	t.Run("控制点 1.1.3 兼容接口余额更新回滚验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "acid-orchestrator-legacy-api-update.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		seed := LedgerEntryInput{
			ChatID:           2231,
			MessageID:        9431,
			UserID:           133,
			Username:         "ella",
			Nickname:         "Ella",
			EntryType:        "deposit",
			Amount:           40,
			Currency:         "CNY",
			BaseAmount:       40,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      40,
			Note:             "legacy-seed-balance-row",
			CardNo:           "6222000022211110",
			TrackMessageLink: "tg://msg/9431",
		}
		seedEntryID, err := database.AddLedgerEntry(seed)
		if err != nil {
			t.Fatalf("准备余额快照失败: %v", err)
		}
		if seedEntryID == 0 {
			t.Fatalf("准备余额快照应返回有效 entry id")
		}

		if _, err := database.raw.Exec(`
CREATE TRIGGER fail_legacy_balance_update
BEFORE UPDATE ON ledger_balances
BEGIN
	SELECT RAISE(FAIL, 'legacy balance update blocked');
END;
`); err != nil {
			t.Fatalf("创建失败注入触发器失败: %v", err)
		}

		second := seed
		second.MessageID = 9432
		second.Amount = 15
		second.BaseAmount = 15
		second.FinalAmount = 15
		second.Note = "legacy-should-rollback-on-balance-update"
		second.TrackMessageLink = "tg://msg/9432"

		if _, err := database.AddLedgerEntry(second); err == nil {
			t.Fatalf("余额更新失败时 AddLedgerEntry 应整体回滚")
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, second.ChatID)
		if ledgerCount != 1 {
			t.Fatalf("事务回滚后 ledger_entries 条数应保持 1，实际=%d", ledgerCount)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(second.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 40) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("回滚后余额应保持不变: deposit=%v payout=%v", depositTotal, payoutTotal)
		}

		if _, err := database.raw.Exec(`DROP TRIGGER fail_legacy_balance_update`); err != nil {
			t.Fatalf("删除失败注入触发器失败: %v", err)
		}

		retryEntryID, err := database.AddLedgerEntry(second)
		if err != nil {
			t.Fatalf("故障恢复后 AddLedgerEntry 重试应成功: %v", err)
		}
		if retryEntryID == 0 {
			t.Fatalf("故障恢复后 AddLedgerEntry 应返回有效 entry id")
		}

		depositTotal, payoutTotal, err = database.GetLedgerBalance(second.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 55) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("重试成功后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})

	t.Run("控制点 1.1.3 兼容撤回标记原子提交验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "acid-orchestrator-legacy-mark-reversed.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		originInput := LedgerEntryInput{
			ChatID:           2251,
			MessageID:        9451,
			UserID:           138,
			Username:         "frank",
			Nickname:         "Frank",
			EntryType:        "deposit",
			Amount:           70,
			Currency:         "CNY",
			BaseAmount:       70,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      70,
			Note:             "legacy-mark-origin",
			CardNo:           "6222000065432109",
			TrackMessageLink: "tg://msg/9451",
		}
		originResult, err := database.ApplyLedgerEntry(originInput)
		if err != nil {
			t.Fatalf("准备原始分录失败: %v", err)
		}
		if originResult.Duplicate || originResult.EntryID == 0 {
			t.Fatalf("准备原始分录结果异常: %+v", originResult)
		}

		reversalOf := originResult.EntryID
		reversalInput := originInput
		reversalInput.MessageID = 9452
		reversalInput.Amount = -70
		reversalInput.BaseAmount = -70
		reversalInput.FinalAmount = -70
		reversalInput.Note = "legacy-mark-reversal"
		reversalInput.TrackMessageLink = "tg://msg/9452"
		reversalInput.ReversalOf = &reversalOf
		reversalID, err := database.AddLedgerEntry(reversalInput)
		if err != nil {
			t.Fatalf("准备冲正分录失败: %v", err)
		}
		if reversalID == 0 {
			t.Fatalf("准备冲正分录应返回有效 entry id")
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(originInput.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 0) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("准备冲正后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}

		if _, err := database.raw.Exec(`
CREATE TRIGGER fail_mark_reversed_balance_update
BEFORE UPDATE ON ledger_balances
BEGIN
	SELECT RAISE(FAIL, 'balance update blocked during mark reversed');
END;
`); err != nil {
			t.Fatalf("创建失败注入触发器失败: %v", err)
		}

		if err := database.MarkReversed(originResult.EntryID, reversalID); err == nil {
			t.Fatalf("余额重建失败时 MarkReversed 应整体回滚")
		}

		originReversed := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE id = ? AND reversed = 1`, originResult.EntryID)
		if originReversed != 0 {
			t.Fatalf("事务回滚后原始分录不应被标记为撤回")
		}

		if _, err := database.raw.Exec(`DROP TRIGGER fail_mark_reversed_balance_update`); err != nil {
			t.Fatalf("删除失败注入触发器失败: %v", err)
		}

		if err := database.MarkReversed(originResult.EntryID, reversalID); err != nil {
			t.Fatalf("故障恢复后 MarkReversed 重试应成功: %v", err)
		}

		originReversed = mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE id = ? AND reversed = 1`, originResult.EntryID)
		if originReversed != 1 {
			t.Fatalf("重试成功后原始分录应标记为撤回")
		}

		depositTotal, payoutTotal, err = database.GetLedgerBalance(originInput.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 0) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("重试成功后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})

	t.Run("控制点 1.1.3 撤回路径回滚验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "acid-orchestrator-reversal.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		input := LedgerEntryInput{
			ChatID:           2002,
			MessageID:        9101,
			UserID:           99,
			Username:         "bob",
			Nickname:         "Bob",
			EntryType:        "deposit",
			Amount:           80,
			Currency:         "CNY",
			BaseAmount:       80,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      80,
			Note:             "acid-reverse-check",
			CardNo:           "6222000099990000",
			TrackMessageLink: "tg://msg/9101",
		}

		applied, err := database.ApplyLedgerEntry(input)
		if err != nil {
			t.Fatalf("准备数据失败: %v", err)
		}
		if applied.EntryID == 0 {
			t.Fatalf("准备数据应写入原始分录")
		}

		// Fail balance update during reversal and verify reversal entry + source flag update rollback together.
		if _, err := database.raw.Exec(`
CREATE TRIGGER fail_ledger_balance_update
BEFORE UPDATE ON ledger_balances
BEGIN
	SELECT RAISE(FAIL, 'balance update blocked');
END;
`); err != nil {
			t.Fatalf("创建失败注入触发器失败: %v", err)
		}

		reversal, err := database.ReverseLastLedger(input.ChatID, 9201, 199, "ops", "Ops", "deposit", "tg://msg/9201")
		if err == nil {
			t.Fatalf("余额更新失败时撤回应整体回滚")
		}
		if reversal.OriginalID != 0 || reversal.ReversalID != 0 || reversal.Duplicate || reversal.NotFound {
			t.Fatalf("失败撤回不应返回成功结果: %+v", reversal)
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, input.ChatID)
		if ledgerCount != 1 {
			t.Fatalf("事务回滚后 ledger_entries 条数应保持 1，实际=%d", ledgerCount)
		}
		originalReversed := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE id = ? AND reversed = 1`, applied.EntryID)
		if originalReversed != 0 {
			t.Fatalf("事务回滚后原始分录不应被标记为撤回")
		}
		idempotencyCount := mustCountRows(t, database, `SELECT COUNT(1) FROM message_idempotency WHERE chat_id = ? AND message_id = ? AND command_key = ?`, input.ChatID, 9201, "reversal:deposit")
		if idempotencyCount != 0 {
			t.Fatalf("事务回滚后 message_idempotency 仍有残留: %d", idempotencyCount)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(input.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 80) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("回滚后余额应保持不变: deposit=%v payout=%v", depositTotal, payoutTotal)
		}

		if _, err := database.raw.Exec(`DROP TRIGGER fail_ledger_balance_update`); err != nil {
			t.Fatalf("删除失败注入触发器失败: %v", err)
		}

		retry, err := database.ReverseLastLedger(input.ChatID, 9201, 199, "ops", "Ops", "deposit", "tg://msg/9201")
		if err != nil {
			t.Fatalf("故障恢复后撤回重试应成功: %v", err)
		}
		if retry.Duplicate || retry.NotFound {
			t.Fatalf("故障恢复后撤回应成功，实际=%+v", retry)
		}
		if retry.OriginalID != applied.EntryID || retry.ReversalID == 0 {
			t.Fatalf("撤回结果异常: %+v", retry)
		}

		depositTotal, payoutTotal, err = database.GetLedgerBalance(input.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 0) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("撤回成功后余额应归零: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})

	t.Run("控制点 1.1.3 清理路径回滚验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "acid-orchestrator-clear.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		input := LedgerEntryInput{
			ChatID:           2301,
			MessageID:        9501,
			UserID:           158,
			Username:         "eve",
			Nickname:         "Eve",
			EntryType:        "deposit",
			Amount:           45,
			Currency:         "CNY",
			BaseAmount:       45,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      45,
			Note:             "clear-acid-check",
			CardNo:           "6222000076543210",
			TrackMessageLink: "tg://msg/9501",
		}
		applied, err := database.ApplyLedgerEntry(input)
		if err != nil {
			t.Fatalf("准备数据失败: %v", err)
		}
		if applied.EntryID == 0 {
			t.Fatalf("准备数据应写入原始分录")
		}

		if _, err := database.raw.Exec(`
CREATE TRIGGER fail_clear_balance_update
BEFORE UPDATE ON ledger_balances
BEGIN
	SELECT RAISE(FAIL, 'balance rebuild blocked during clear');
END;
`); err != nil {
			t.Fatalf("创建失败注入触发器失败: %v", err)
		}

		if _, err := database.ClearLedgerSince(input.ChatID, "1970-01-01 00:00:00"); err == nil {
			t.Fatalf("余额更新失败时清理应整体回滚")
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, input.ChatID)
		if ledgerCount != 1 {
			t.Fatalf("事务回滚后 ledger_entries 条数应保持 1，实际=%d", ledgerCount)
		}
		depositTotal, payoutTotal, err := database.GetLedgerBalance(input.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 45) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("回滚后余额应保持不变: deposit=%v payout=%v", depositTotal, payoutTotal)
		}

		if _, err := database.raw.Exec(`DROP TRIGGER fail_clear_balance_update`); err != nil {
			t.Fatalf("删除失败注入触发器失败: %v", err)
		}

		deleted, err := database.ClearLedgerSince(input.ChatID, "1970-01-01 00:00:00")
		if err != nil {
			t.Fatalf("故障恢复后清理重试应成功: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("清理成功后删除条数异常: %d", deleted)
		}

		ledgerCount = mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, input.ChatID)
		if ledgerCount != 0 {
			t.Fatalf("清理成功后 ledger_entries 应为空，实际=%d", ledgerCount)
		}
		depositTotal, payoutTotal, err = database.GetLedgerBalance(input.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 0) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("清理成功后余额应归零: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})
}

func TestControlPoint114TrialBalanceCheck(t *testing.T) {
	t.Run("控制点 1.1.4 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "trial-balance-check.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		if _, err := database.raw.Exec(`
INSERT INTO ledger_entries (
	chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			2401,
			9501,
			167,
			"legacy",
			"Legacy",
			"manual_adjustment",
			100,
			"CNY",
			100,
			0,
			1,
			100,
			"legacy-bad-entry",
			"",
			"tg://msg/9501",
		); err != nil {
			t.Fatalf("写入历史坏账失败: %v", err)
		}

		input := LedgerEntryInput{
			ChatID:           2401,
			MessageID:        9601,
			UserID:           168,
			Username:         "trial",
			Nickname:         "Trial",
			EntryType:        "deposit",
			Amount:           100,
			Currency:         "CNY",
			BaseAmount:       100,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      100,
			Note:             "trial-balance-pass",
			CardNo:           "6222000098765432",
			TrackMessageLink: "tg://msg/9601",
		}
		result, err := database.ApplyLedgerEntry(input)
		if err != nil {
			t.Fatalf("历史坏账不应阻断当前合法记账: %v", err)
		}
		if result.Duplicate || result.EntryID == 0 {
			t.Fatalf("成功写入结果异常: %+v", result)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(input.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 100) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("当前合法记账后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})

	t.Run("控制点 1.1.4 不平衡回滚验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "trial-balance-rollback.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		bad := LedgerEntryInput{
			ChatID:           2402,
			MessageID:        9602,
			UserID:           169,
			Username:         "trial-bad",
			Nickname:         "TrialBad",
			EntryType:        "manual_adjustment",
			Amount:           88,
			Currency:         "CNY",
			BaseAmount:       88,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      88,
			Note:             "trial-balance-rollback",
			CardNo:           "6222000044448888",
			TrackMessageLink: "tg://msg/9602",
		}
		result, err := database.ApplyLedgerEntry(bad)
		if err == nil {
			t.Fatalf("借贷不平衡时应阻断提交")
		}
		if !strings.Contains(err.Error(), trialBalanceCriticalAlert) {
			t.Fatalf("错误信息应包含关键告警码 %q，实际=%v", trialBalanceCriticalAlert, err)
		}
		if result.EntryID != 0 || result.CardSeenCount != 0 || result.Duplicate {
			t.Fatalf("失败写入不应返回成功结果: %+v", result)
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, bad.ChatID)
		if ledgerCount != 0 {
			t.Fatalf("试算平衡失败后 ledger_entries 仍有残留: %d", ledgerCount)
		}
		balanceCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_balances WHERE chat_id = ?`, bad.ChatID)
		if balanceCount != 0 {
			t.Fatalf("试算平衡失败后 ledger_balances 仍有残留: %d", balanceCount)
		}
		idempotencyCount := mustCountRows(t, database, `SELECT COUNT(1) FROM message_idempotency WHERE chat_id = ? AND message_id = ? AND command_key = 'ledger'`, bad.ChatID, bad.MessageID)
		if idempotencyCount != 0 {
			t.Fatalf("试算平衡失败后 message_idempotency 仍有残留: %d", idempotencyCount)
		}

		retry := bad
		retry.EntryType = "deposit"
		retry.Note = "trial-balance-retry"
		okResult, err := database.ApplyLedgerEntry(retry)
		if err != nil {
			t.Fatalf("修复后重试应成功: %v", err)
		}
		if okResult.Duplicate || okResult.EntryID == 0 {
			t.Fatalf("修复后重试结果异常: %+v", okResult)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(retry.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 88) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("修复后重试余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})
}

func TestLedgerIndexesExist(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "ledger-indexes.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	indexNames := []string{
		"idx_ledger_chat_created_id",
		"idx_ledger_chat_type_created_id",
		"idx_ledger_chat_reversed_id",
	}
	for _, indexName := range indexNames {
		var found string
		err := database.raw.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`,
			indexName,
		).Scan(&found)
		if err != nil {
			t.Fatalf("索引 %s 不存在: %v", indexName, err)
		}
	}
}

func TestControlPoint121AccountMutex(t *testing.T) {
	t.Run("控制点 1.2.1 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "account-mutex.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const (
			chatID      int64 = 2601
			userID      int64 = 188
			workerCount       = 24
		)

		var wg sync.WaitGroup
		errCh := make(chan error, workerCount)
		for i := 0; i < workerCount; i++ {
			wg.Add(1)
			go func(offset int) {
				defer wg.Done()
				input := LedgerEntryInput{
					ChatID:           chatID,
					MessageID:        10000 + offset,
					UserID:           userID,
					Username:         "lock-user",
					Nickname:         "LockUser",
					EntryType:        "deposit",
					Amount:           1,
					Currency:         "USDT",
					BaseAmount:       1,
					FeeRate:          0,
					Rate:             1,
					FinalAmount:      1,
					Note:             "account-mutex-concurrent",
					TrackMessageLink: "tg://msg/account-mutex",
				}
				if _, err := database.ApplyLedgerEntry(input); err != nil {
					errCh <- err
				}
			}(i)
		}
		wg.Wait()
		close(errCh)
		for applyErr := range errCh {
			t.Fatalf("并发记账失败: %v", applyErr)
		}

		usdtBalance, err := database.GetAccountBalance(userID, "usdt")
		if err != nil {
			t.Fatalf("查询账户余额失败: %v", err)
		}
		if !floatEqual(usdtBalance, float64(workerCount)) {
			t.Fatalf("并发记账后余额不正确，期望=%d 实际=%v", workerCount, usdtBalance)
		}

		balanceRowCount := mustCountRows(t, database, `SELECT COUNT(1) FROM account_balances WHERE user_id = ? AND currency = ?`, userID, "USDT")
		if balanceRowCount != 1 {
			t.Fatalf("同一 User_ID + Currency 应只保留一条余额快照，实际=%d", balanceRowCount)
		}

		payout := LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        12001,
			UserID:           userID,
			Username:         "lock-user",
			Nickname:         "LockUser",
			EntryType:        "payout",
			Amount:           5,
			Currency:         "USDT",
			BaseAmount:       5,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      5,
			Note:             "account-mutex-payout",
			TrackMessageLink: "tg://msg/account-mutex-payout",
		}
		if _, err := database.ApplyLedgerEntry(payout); err != nil {
			t.Fatalf("下发记账失败: %v", err)
		}

		usdtBalance, err = database.GetAccountBalance(userID, "USDT")
		if err != nil {
			t.Fatalf("查询账户余额失败: %v", err)
		}
		if !floatEqual(usdtBalance, float64(workerCount-5)) {
			t.Fatalf("下发后余额不正确，期望=%d 实际=%v", workerCount-5, usdtBalance)
		}

		cnyInput := LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        12002,
			UserID:           userID,
			Username:         "lock-user",
			Nickname:         "LockUser",
			EntryType:        "deposit",
			Amount:           30,
			Currency:         "CNY",
			BaseAmount:       30,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      30,
			Note:             "account-mutex-cny",
			TrackMessageLink: "tg://msg/account-mutex-cny",
		}
		if _, err := database.ApplyLedgerEntry(cnyInput); err != nil {
			t.Fatalf("多币种记账失败: %v", err)
		}

		cnyBalance, err := database.GetAccountBalance(userID, "CNY")
		if err != nil {
			t.Fatalf("查询 CNY 余额失败: %v", err)
		}
		if !floatEqual(cnyBalance, 30) {
			t.Fatalf("CNY 余额不正确，期望=30 实际=%v", cnyBalance)
		}

		otherUser := cnyInput
		otherUser.MessageID = 12003
		otherUser.UserID = 199
		otherUser.Amount = 12
		otherUser.BaseAmount = 12
		otherUser.FinalAmount = 12
		otherUser.Note = "account-mutex-other-user"
		if _, err := database.ApplyLedgerEntry(otherUser); err != nil {
			t.Fatalf("其他用户记账失败: %v", err)
		}
		otherUserBalance, err := database.GetAccountBalance(otherUser.UserID, otherUser.Currency)
		if err != nil {
			t.Fatalf("查询其他用户余额失败: %v", err)
		}
		if !floatEqual(otherUserBalance, 12) {
			t.Fatalf("其他用户余额不正确，期望=12 实际=%v", otherUserBalance)
		}
		cnyBalance, err = database.GetAccountBalance(userID, "CNY")
		if err != nil {
			t.Fatalf("复查 CNY 余额失败: %v", err)
		}
		if !floatEqual(cnyBalance, 30) {
			t.Fatalf("其他用户写入不应污染当前用户余额，期望=30 实际=%v", cnyBalance)
		}
	})
}

func TestControlPoint122DeadlockDetectionAndRetry(t *testing.T) {
	t.Run("控制点 1.2.2 功能验证", func(t *testing.T) {
		database, err := NewDatabaseWithOptions(filepath.Join(t.TempDir(), "deadlock-retry.db"), Options{
			WaitTimeout:           1200 * time.Millisecond,
			DeadlockRetryMax:      3,
			DeadlockBackoffBase:   50 * time.Millisecond,
			DeadlockBackoffJitter: 25 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		var busyTimeoutMS int
		if err := database.raw.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeoutMS); err != nil {
			t.Fatalf("读取 busy_timeout 失败: %v", err)
		}
		if busyTimeoutMS != 1200 {
			t.Fatalf("数据库 Wait_Timeout 配置错误，期望=1200ms 实际=%dms", busyTimeoutMS)
		}

		delays := make([]time.Duration, 0, 2)
		database.retryWait = func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		}
		database.randIntn = func(int) int { return 0 }

		attempts := 0
		err = database.withWriteTx(func(_ *sql.Tx) error {
			attempts++
			if attempts < 3 {
				return fmt.Errorf("deadlock found when trying to get lock")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("死锁重试后应成功: %v", err)
		}
		if attempts != 3 {
			t.Fatalf("应执行 3 次事务尝试，实际=%d", attempts)
		}
		if len(delays) != 2 {
			t.Fatalf("应产生 2 次指数退避，实际=%d", len(delays))
		}
		if delays[0] != 50*time.Millisecond || delays[1] != 100*time.Millisecond {
			t.Fatalf("指数退避间隔错误，期望=[50ms 100ms] 实际=%v", delays)
		}
	})

	t.Run("控制点 1.2.2 非死锁错误不重试", func(t *testing.T) {
		database, err := NewDatabaseWithOptions(filepath.Join(t.TempDir(), "deadlock-no-retry.db"), Options{
			WaitTimeout:           1000 * time.Millisecond,
			DeadlockRetryMax:      3,
			DeadlockBackoffBase:   50 * time.Millisecond,
			DeadlockBackoffJitter: 25 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		delayCount := 0
		database.retryWait = func(_ context.Context, delay time.Duration) error {
			delayCount++
			return nil
		}
		database.randIntn = func(int) int { return 0 }

		attempts := 0
		err = database.withWriteTx(func(_ *sql.Tx) error {
			attempts++
			return fmt.Errorf("insufficient balance")
		})
		if err == nil {
			t.Fatalf("非死锁错误应直接返回")
		}
		if !strings.Contains(err.Error(), "insufficient balance") {
			t.Fatalf("错误信息异常: %v", err)
		}
		if attempts != 1 {
			t.Fatalf("非死锁错误不应重试，实际尝试次数=%d", attempts)
		}
		if delayCount != 0 {
			t.Fatalf("非死锁错误不应触发退避等待，实际=%d", delayCount)
		}
	})
}

func TestSQLiteRuntimeOptions(t *testing.T) {
	database, err := NewDatabaseWithOptions(filepath.Join(t.TempDir(), "sqlite-runtime.db"), Options{
		WaitTimeout:           1500 * time.Millisecond,
		SQLiteJournalMode:     "WAL",
		SQLiteSynchronous:     "NORMAL",
		SQLiteOptimizeOnStart: true,
	})
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	var journalMode string
	if err := database.raw.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("读取 journal_mode 失败: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode 应启用 WAL，got=%q", journalMode)
	}

	var synchronous int
	if err := database.raw.QueryRow(`PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatalf("读取 synchronous 失败: %v", err)
	}
	if synchronous != 1 {
		t.Fatalf("synchronous=NORMAL 应为 1，got=%d", synchronous)
	}

	var busyTimeoutMS int
	if err := database.raw.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeoutMS); err != nil {
		t.Fatalf("读取 busy_timeout 失败: %v", err)
	}
	if busyTimeoutMS != 1500 {
		t.Fatalf("busy_timeout 应保持可配置，got=%d", busyTimeoutMS)
	}
}

func TestControlPoint123HotAccountThrottling(t *testing.T) {
	t.Run("控制点 1.2.3 功能验证", func(t *testing.T) {
		const (
			chatID    int64 = 2801
			hotUserID int64 = 66001
		)

		database, err := NewDatabaseWithOptions(filepath.Join(t.TempDir(), "hot-account-throttle.db"), Options{
			HotAccountUserIDs:     []int64{hotUserID},
			HotAccountBatchSize:   5,
			HotAccountFlushWindow: time.Hour,
		})
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		if _, err := database.raw.Exec(`
CREATE TABLE account_balance_write_counter (
	value INTEGER NOT NULL DEFAULT 0
);
INSERT INTO account_balance_write_counter (value) VALUES (0);
CREATE TRIGGER account_balance_insert_counter
AFTER INSERT ON account_balances
BEGIN
	UPDATE account_balance_write_counter SET value = value + 1;
END;
CREATE TRIGGER account_balance_update_counter
AFTER UPDATE ON account_balances
BEGIN
	UPDATE account_balance_write_counter SET value = value + 1;
END;
`); err != nil {
			t.Fatalf("创建账户写入计数器失败: %v", err)
		}

		for i := 0; i < 4; i++ {
			input := LedgerEntryInput{
				ChatID:           chatID,
				MessageID:        13000 + i,
				UserID:           hotUserID,
				Username:         "hot-public-account",
				Nickname:         "HotPublicAccount",
				EntryType:        "deposit",
				Amount:           1,
				Currency:         "USDT",
				BaseAmount:       1,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      1,
				Note:             "hot-account-pre-batch",
				TrackMessageLink: "tg://msg/hot-account-pre-batch",
			}
			if _, err := database.ApplyLedgerEntry(input); err != nil {
				t.Fatalf("热点账户预热写入失败: %v", err)
			}
		}

		writeCount := mustCountRows(t, database, `SELECT value FROM account_balance_write_counter`)
		if writeCount != 0 {
			t.Fatalf("批量阈值未达到前不应写 account_balances，实际写入次数=%d", writeCount)
		}

		balance, err := database.GetAccountBalance(hotUserID, "USDT")
		if err != nil {
			t.Fatalf("读取热点账户余额失败: %v", err)
		}
		if !floatEqual(balance, 4) {
			t.Fatalf("批量刷盘前余额应来自内存合并写，期望=4 实际=%v", balance)
		}

		flushInput := LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        13004,
			UserID:           hotUserID,
			Username:         "hot-public-account",
			Nickname:         "HotPublicAccount",
			EntryType:        "deposit",
			Amount:           1,
			Currency:         "USDT",
			BaseAmount:       1,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      1,
			Note:             "hot-account-trigger-flush",
			TrackMessageLink: "tg://msg/hot-account-trigger-flush",
		}
		if _, err := database.ApplyLedgerEntry(flushInput); err != nil {
			t.Fatalf("触发批量刷盘失败: %v", err)
		}

		writeCount = mustCountRows(t, database, `SELECT value FROM account_balance_write_counter`)
		if writeCount > 2 {
			t.Fatalf("热点账户批量刷盘应显著减少写放大，期望<=2 实际=%d", writeCount)
		}

		balance, err = database.GetAccountBalance(hotUserID, "USDT")
		if err != nil {
			t.Fatalf("读取批量刷盘后余额失败: %v", err)
		}
		if !floatEqual(balance, 5) {
			t.Fatalf("批量刷盘后余额错误，期望=5 实际=%v", balance)
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ? AND user_id = ?`, chatID, hotUserID)
		if ledgerCount != 5 {
			t.Fatalf("热点账户记账条数错误，期望=5 实际=%d", ledgerCount)
		}
	})
}

func TestControlPoint124OptimisticLockingCAS(t *testing.T) {
	t.Run("控制点 1.2.4 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "optimistic-locking-cas.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const (
			userID   int64  = 69001
			currency string = "USDT"
		)

		err = database.withWriteTx(func(tx *sql.Tx) error {
			if err := ensureAccountBalanceRowTx(tx, userID, currency); err != nil {
				return err
			}

			snapshot, found, err := getAccountBalanceSnapshotTx(tx, userID, currency)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("未找到账户余额快照")
			}

			swapped, err := updateAccountBalanceCASTx(tx, userID, currency, snapshot.balance+12, snapshot.version)
			if err != nil {
				return err
			}
			if !swapped {
				return fmt.Errorf("首次 CAS 更新应成功")
			}

			staleSwapped, err := updateAccountBalanceCASTx(tx, userID, currency, 999, snapshot.version)
			if err != nil {
				return err
			}
			if staleSwapped {
				return fmt.Errorf("使用过期版本号的 CAS 更新应被拒绝")
			}

			latest, found, err := getAccountBalanceSnapshotTx(tx, userID, currency)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("CAS 更新后应存在余额快照")
			}
			if !floatEqual(latest.balance, 12) {
				return fmt.Errorf("CAS 更新后余额错误，期望=12 实际=%v", latest.balance)
			}
			if latest.version != snapshot.version+1 {
				return fmt.Errorf("CAS 更新后版本号错误，期望=%d 实际=%d", snapshot.version+1, latest.version)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("CAS 更新验证失败: %v", err)
		}

		input := LedgerEntryInput{
			ChatID:           2901,
			MessageID:        13100,
			UserID:           userID,
			Username:         "cas-user",
			Nickname:         "CASUser",
			EntryType:        "deposit",
			Amount:           3,
			Currency:         currency,
			BaseAmount:       3,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      3,
			Note:             "optimistic-locking-cas",
			TrackMessageLink: "tg://msg/optimistic-locking-cas",
		}
		if _, err := database.ApplyLedgerEntry(input); err != nil {
			t.Fatalf("通过业务链路更新余额失败: %v", err)
		}

		var (
			balance float64
			version int64
		)
		row := database.raw.QueryRow(`SELECT balance, version FROM account_balances WHERE user_id = ? AND currency = ?`, userID, currency)
		if err := row.Scan(&balance, &version); err != nil {
			t.Fatalf("读取余额快照失败: %v", err)
		}
		if !floatEqual(balance, 15) {
			t.Fatalf("业务链路更新后余额错误，期望=15 实际=%v", balance)
		}
		if version != 2 {
			t.Fatalf("业务链路应推进版本号，期望=2 实际=%d", version)
		}
	})
}

func TestControlPoint133ReferenceCheck(t *testing.T) {
	t.Run("控制点 1.3.3 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "reference-check.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		periodStartAt := time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC).Format("2006-01-02T15:04:05")
		settledInput := LedgerEntryInput{
			ChatID:           3301,
			MessageID:        20101,
			UserID:           701,
			Username:         "reference-user",
			Nickname:         "ReferenceUser",
			EntryType:        "deposit",
			Amount:           120,
			Currency:         "CNY",
			BaseAmount:       120,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      120,
			Note:             "settled-entry",
			TrackMessageLink: "tg://msg/20101",
		}
		settledApply, err := database.ApplyLedgerEntry(settledInput)
		if err != nil {
			t.Fatalf("准备已结算分录失败: %v", err)
		}
		if settledApply.Duplicate || settledApply.EntryID == 0 {
			t.Fatalf("准备已结算分录结果异常: %+v", settledApply)
		}

		settledCreatedAt := "2026-01-02 03:59:59"
		if _, err := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, settledCreatedAt, settledApply.EntryID); err != nil {
			t.Fatalf("设置已结算分录时间失败: %v", err)
		}

		blocked, err := database.ReverseLastLedgerInPeriod(
			settledInput.ChatID,
			20102,
			9001,
			"ops",
			"Ops",
			"deposit",
			"tg://msg/20102",
			periodStartAt,
		)
		if err != nil {
			t.Fatalf("账期校验撤回失败: %v", err)
		}
		if !blocked.Settled || blocked.Duplicate || blocked.NotFound {
			t.Fatalf("已结算分录应被阻止撤回: %+v", blocked)
		}
		if blocked.OriginalID != 0 || blocked.ReversalID != 0 {
			t.Fatalf("已结算分录被阻止时不应生成撤回记录: %+v", blocked)
		}

		settledReversed := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE id = ? AND reversed = 1`, settledApply.EntryID)
		if settledReversed != 0 {
			t.Fatalf("已结算分录不应被标记为撤回")
		}

		currentInput := settledInput
		currentInput.MessageID = 20103
		currentInput.Amount = 45
		currentInput.BaseAmount = 45
		currentInput.FinalAmount = 45
		currentInput.Note = "current-period-entry"
		currentInput.TrackMessageLink = "tg://msg/20103"
		currentApply, err := database.ApplyLedgerEntry(currentInput)
		if err != nil {
			t.Fatalf("准备当前账期分录失败: %v", err)
		}
		if currentApply.Duplicate || currentApply.EntryID == 0 {
			t.Fatalf("准备当前账期分录结果异常: %+v", currentApply)
		}

		currentCreatedAt := "2026-01-02 04:00:01"
		if _, err := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, currentCreatedAt, currentApply.EntryID); err != nil {
			t.Fatalf("设置当前账期分录时间失败: %v", err)
		}

		reversal, err := database.ReverseLastLedgerInPeriod(
			settledInput.ChatID,
			20104,
			9001,
			"ops",
			"Ops",
			"deposit",
			"tg://msg/20104",
			periodStartAt,
		)
		if err != nil {
			t.Fatalf("当前账期撤回失败: %v", err)
		}
		if reversal.Settled || reversal.Duplicate || reversal.NotFound {
			t.Fatalf("当前账期撤回状态异常: %+v", reversal)
		}
		if reversal.OriginalID != currentApply.EntryID || reversal.ReversalID == 0 {
			t.Fatalf("当前账期撤回结果异常: %+v", reversal)
		}

		currentReversed := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE id = ? AND reversed = 1`, currentApply.EntryID)
		if currentReversed != 1 {
			t.Fatalf("当前账期原始分录应被标记为撤回")
		}
		settledReversed = mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE id = ? AND reversed = 1`, settledApply.EntryID)
		if settledReversed != 0 {
			t.Fatalf("已结算分录应保持未撤回")
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(settledInput.ChatID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if !floatEqual(depositTotal, 120) || !floatEqual(payoutTotal, 0) {
			t.Fatalf("账期校验后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
		}
	})
}

func TestReverseLastLedgerSkipsReversalEntries(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "reverse-last-ledger-skips-reversal.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = 4401

	applyDeposit := func(messageID int, amount float64, note string) LedgerApplyResult {
		t.Helper()
		result, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        messageID,
			UserID:           901,
			Username:         "reverse-user",
			Nickname:         "ReverseUser",
			EntryType:        "deposit",
			Amount:           amount,
			Currency:         "CNY",
			BaseAmount:       amount,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      amount,
			Note:             note,
			TrackMessageLink: fmt.Sprintf("tg://msg/%d", messageID),
		})
		if applyErr != nil {
			t.Fatalf("写入入款记录失败: %v", applyErr)
		}
		if result.Duplicate || result.EntryID == 0 {
			t.Fatalf("写入入款记录结果异常: %+v", result)
		}
		return result
	}

	first := applyDeposit(44001, 5000, "first")
	second := applyDeposit(44002, 3000, "second")

	reversalOne, err := database.ReverseLastLedger(chatID, 44003, 901, "reverse-user", "ReverseUser", "deposit", "tg://msg/44003")
	if err != nil {
		t.Fatalf("第一次撤回失败: %v", err)
	}
	if reversalOne.Duplicate || reversalOne.NotFound || reversalOne.Settled {
		t.Fatalf("第一次撤回状态异常: %+v", reversalOne)
	}
	if reversalOne.OriginalID != second.EntryID {
		t.Fatalf("第一次撤回应命中最新原始入款，want=%d got=%d", second.EntryID, reversalOne.OriginalID)
	}

	reversalTwo, err := database.ReverseLastLedger(chatID, 44004, 901, "reverse-user", "ReverseUser", "deposit", "tg://msg/44004")
	if err != nil {
		t.Fatalf("第二次撤回失败: %v", err)
	}
	if reversalTwo.Duplicate || reversalTwo.NotFound || reversalTwo.Settled {
		t.Fatalf("第二次撤回状态异常: %+v", reversalTwo)
	}
	if reversalTwo.OriginalID != first.EntryID {
		t.Fatalf("第二次撤回应跳过撤回分录并命中更早的原始入款，want=%d got=%d", first.EntryID, reversalTwo.OriginalID)
	}

	depositTotal, payoutTotal, err := database.GetLedgerBalance(chatID)
	if err != nil {
		t.Fatalf("查询余额失败: %v", err)
	}
	if !floatEqual(depositTotal, 0) || !floatEqual(payoutTotal, 0) {
		t.Fatalf("连续撤回两条原始入款后余额应归零: deposit=%v payout=%v", depositTotal, payoutTotal)
	}
}

func mustCountRows(t *testing.T, database *Database, query string, args ...any) int {
	t.Helper()
	row := database.raw.QueryRow(query, args...)
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("查询计数失败: %v", err)
	}
	return count
}

func floatEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
