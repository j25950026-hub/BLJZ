package integration_test

import (
	"math"
	"path/filepath"
	"testing"

	"go-bot/internal/db"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestLedgerControls(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	input := db.LedgerEntryInput{
		ChatID:           1001,
		MessageID:        5001,
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
		Note:             "首笔",
		CardNo:           "6222000011112222",
		TrackMessageLink: "tg://msg/5001",
	}
	first, err := database.ApplyLedgerEntry(input)
	if err != nil {
		t.Fatalf("首次记账失败: %v", err)
	}
	if first.Duplicate {
		t.Fatalf("首次记账被错误识别为重复")
	}
	if first.EntryID == 0 {
		t.Fatalf("首次记账未返回有效ID")
	}
	if first.CardSeenCount != 1 {
		t.Fatalf("首次卡号计数异常: %d", first.CardSeenCount)
	}

	dup, err := database.ApplyLedgerEntry(input)
	if err != nil {
		t.Fatalf("重复记账返回错误: %v", err)
	}
	if !dup.Duplicate {
		t.Fatalf("重复记账未触发幂等")
	}

	input.MessageID = 5003
	input.Amount = 50
	input.BaseAmount = 50
	input.FinalAmount = 50
	input.Note = "第二笔"
	second, err := database.ApplyLedgerEntry(input)
	if err != nil {
		t.Fatalf("第二笔记账失败: %v", err)
	}
	if second.Duplicate {
		t.Fatalf("第二笔记账被误判为重复")
	}
	if second.CardSeenCount != 2 {
		t.Fatalf("银行卡重复计数异常: %d", second.CardSeenCount)
	}

	depositTotal, payoutTotal, err := database.GetLedgerBalance(1001)
	if err != nil {
		t.Fatalf("读取余额快照失败: %v", err)
	}
	if !almostEqual(depositTotal, 150) || !almostEqual(payoutTotal, 0) {
		t.Fatalf("落账后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
	}

	reversal, err := database.ReverseLastLedger(1001, 5004, 88, "alice", "Alice", "deposit", "tg://msg/5004")
	if err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	if reversal.NotFound || reversal.Duplicate {
		t.Fatalf("撤回状态异常: %+v", reversal)
	}
	if reversal.OriginalID != second.EntryID {
		t.Fatalf("撤回应命中最新入款记录，期望=%d 实际=%d", second.EntryID, reversal.OriginalID)
	}

	depositTotal, payoutTotal, err = database.GetLedgerBalance(1001)
	if err != nil {
		t.Fatalf("读取撤回后余额失败: %v", err)
	}
	if !almostEqual(depositTotal, 100) || !almostEqual(payoutTotal, 0) {
		t.Fatalf("撤回后余额异常: deposit=%v payout=%v", depositTotal, payoutTotal)
	}

	reversalDup, err := database.ReverseLastLedger(1001, 5004, 88, "alice", "Alice", "deposit", "tg://msg/5004")
	if err != nil {
		t.Fatalf("重复撤回返回错误: %v", err)
	}
	if !reversalDup.Duplicate {
		t.Fatalf("重复撤回未触发幂等")
	}

	if err := database.AddRiskEvent(1001, 5004, 88, "replay_blocked", "warn", "test"); err != nil {
		t.Fatalf("写入风险事件失败: %v", err)
	}
}
