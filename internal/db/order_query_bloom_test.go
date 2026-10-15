package db

import (
	"path/filepath"
	"testing"
)

func TestControlPoint422OrderQueryBloomFilter(t *testing.T) {
	t.Run("控制点 4.2.2 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "order-query-bloom.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		if !database.OrderQueryBloomEnabled() {
			t.Fatalf("订单查询布隆过滤器应默认启用")
		}

		const (
			chatID  int64 = -1004222001
			startAt       = "2000-01-01 00:00:00"
		)
		_, err = database.AddLedgerEntry(LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        4222001,
			UserID:           42001,
			Username:         "order-alpha",
			Nickname:         "Alpha",
			EntryType:        "deposit",
			Amount:           188.66,
			Currency:         "CNY",
			BaseAmount:       188.66,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      188.66,
			Note:             "alpha-order-20260219",
			CardNo:           "6222000042220001",
			TrackMessageLink: "tg://msg/4222001",
		})
		if err != nil {
			t.Fatalf("写入订单样本失败: %v", err)
		}

		beforeBlocked := database.orderQueryBloomBlocked.Load()

		missKeywordRows, err := database.SearchOrders(chatID, "zzqvvbnm_no_hit_20260219", nil, startAt)
		if err != nil {
			t.Fatalf("执行不存在关键词查单失败: %v", err)
		}
		if len(missKeywordRows) != 0 {
			t.Fatalf("不存在关键词应返回 0 条，实际=%d", len(missKeywordRows))
		}
		if blocked := database.orderQueryBloomBlocked.Load(); blocked != beforeBlocked+1 {
			t.Fatalf("不存在关键词应被布隆过滤器拦截，want=%d got=%d", beforeBlocked+1, blocked)
		}

		hitRows, err := database.SearchOrders(chatID, "alpha-order", nil, startAt)
		if err != nil {
			t.Fatalf("执行存在关键词查单失败: %v", err)
		}
		if len(hitRows) != 1 {
			t.Fatalf("存在关键词应命中 1 条，实际=%d", len(hitRows))
		}
		if blocked := database.orderQueryBloomBlocked.Load(); blocked != beforeBlocked+1 {
			t.Fatalf("命中查询不应增加拦截计数，want=%d got=%d", beforeBlocked+1, blocked)
		}

		missAmount := 654321.123456
		missAmountRows, err := database.SearchOrders(chatID, "", &missAmount, startAt)
		if err != nil {
			t.Fatalf("执行不存在金额查单失败: %v", err)
		}
		if len(missAmountRows) != 0 {
			t.Fatalf("不存在金额应返回 0 条，实际=%d", len(missAmountRows))
		}
		if blocked := database.orderQueryBloomBlocked.Load(); blocked != beforeBlocked+2 {
			t.Fatalf("不存在金额应被布隆过滤器拦截，want=%d got=%d", beforeBlocked+2, blocked)
		}

		_, err = database.AddLedgerEntry(LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        4222002,
			UserID:           42002,
			Username:         "order-beta",
			Nickname:         "Beta",
			EntryType:        "payout",
			Amount:           321.88,
			Currency:         "CNY",
			BaseAmount:       321.88,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      321.88,
			Note:             "dynamic-order-xyz-20260219",
			CardNo:           "6222000042220002",
			TrackMessageLink: "tg://msg/4222002",
		})
		if err != nil {
			t.Fatalf("写入增量订单样本失败: %v", err)
		}

		dynamicRows, err := database.SearchOrders(chatID, "xyz-20260219", nil, startAt)
		if err != nil {
			t.Fatalf("执行增量关键词查单失败: %v", err)
		}
		if len(dynamicRows) != 1 || dynamicRows[0].MessageID != 4222002 {
			t.Fatalf("新增订单应可被查询命中，actual=%+v", dynamicRows)
		}
	})
}
