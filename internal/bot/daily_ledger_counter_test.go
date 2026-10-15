package bot

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestControlPoint424AtomicCounters(t *testing.T) {
	t.Run("控制点 4.2.4 功能验证", func(t *testing.T) {
		mini, err := miniredis.Run()
		if err != nil {
			t.Fatalf("启动 miniredis 失败: %v", err)
		}
		defer mini.Close()

		counter, err := NewRedisDailyLedgerCounter(RedisDailyLedgerCounterConfig{
			Addr:      mini.Addr(),
			KeyPrefix: "ledger:daily:test:",
			TTL:       2 * time.Hour,
			Timeout:   time.Second,
		})
		if err != nil {
			t.Fatalf("初始化 Redis 原子计数器失败: %v", err)
		}
		defer counter.Close()

		const (
			chatID    int64  = -1004204001
			periodKey string = "2026-02-19T04:00:00"
		)

		if err := counter.Incr(context.Background(), chatID, periodKey, "deposit", 120.5, 7.2); err != nil {
			t.Fatalf("入款计数器自增失败: %v", err)
		}
		if err := counter.Incr(context.Background(), chatID, periodKey, "deposit", 29.5, 7.2); err != nil {
			t.Fatalf("第二笔入款计数器自增失败: %v", err)
		}
		if err := counter.Incr(context.Background(), chatID, periodKey, "payout", 50, 7.18); err != nil {
			t.Fatalf("下发计数器自增失败: %v", err)
		}

		snapshot, ok, err := counter.Snapshot(context.Background(), chatID, periodKey)
		if err != nil {
			t.Fatalf("读取计数器快照失败: %v", err)
		}
		if !ok {
			t.Fatalf("计数器快照应存在")
		}
		if snapshot.depCount != 2 || snapshot.payCount != 1 {
			t.Fatalf("计数器笔数错误: %+v", snapshot)
		}
		if !almostZero(snapshot.depTotal-150) || !almostZero(snapshot.payTotal-50) {
			t.Fatalf("计数器金额错误: %+v", snapshot)
		}

		depRate, depOK, err := counter.RateStats(context.Background(), chatID, periodKey, "deposit")
		if err != nil {
			t.Fatalf("读取入款汇率统计失败: %v", err)
		}
		if !depOK || len(depRate) != 1 {
			t.Fatalf("入款汇率统计应存在且仅一档，actual=%v", depRate)
		}
		if !almostZero(depRate["7.2"] - 150) {
			t.Fatalf("入款汇率统计金额错误: %v", depRate)
		}

		payRate, payOK, err := counter.RateStats(context.Background(), chatID, periodKey, "payout")
		if err != nil {
			t.Fatalf("读取下发汇率统计失败: %v", err)
		}
		if !payOK || len(payRate) != 1 {
			t.Fatalf("下发汇率统计应存在且仅一档，actual=%v", payRate)
		}
		if !almostZero(payRate["7.18"] - 50) {
			t.Fatalf("下发汇率统计金额错误: %v", payRate)
		}

		err = counter.Replace(
			context.Background(),
			chatID,
			periodKey,
			ledgerSummary{depCount: 1, payCount: 1, depTotal: 88, payTotal: 11},
			map[string]float64{"7.2": 88},
			map[string]float64{"7.18": 11},
		)
		if err != nil {
			t.Fatalf("回填计数器快照失败: %v", err)
		}

		rebuilt, ok, err := counter.Snapshot(context.Background(), chatID, periodKey)
		if err != nil {
			t.Fatalf("读取回填后的计数器快照失败: %v", err)
		}
		if !ok {
			t.Fatalf("回填后计数器快照应存在")
		}
		if rebuilt.depCount != 1 || rebuilt.payCount != 1 || !almostZero(rebuilt.depTotal-88) || !almostZero(rebuilt.payTotal-11) {
			t.Fatalf("回填后计数器快照错误: %+v", rebuilt)
		}

		if err := counter.Replace(context.Background(), chatID, periodKey, ledgerSummary{}, nil, nil); err != nil {
			t.Fatalf("清空计数器快照失败: %v", err)
		}
		cleared, ok, err := counter.Snapshot(context.Background(), chatID, periodKey)
		if err != nil {
			t.Fatalf("读取清空后的计数器快照失败: %v", err)
		}
		if ok {
			t.Fatalf("清空后计数器快照应不存在，actual=%+v", cleared)
		}
	})
}
