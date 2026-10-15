package db

import (
	"path/filepath"
	"testing"
)

func TestControlPoint433CustomRangeQuery(t *testing.T) {
	t.Run("控制点 4.3.3 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "custom-range-query.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const chatID int64 = -1004333001
		if _, err := database.EnsureGroup(chatID, "范围查单测试群"); err != nil {
			t.Fatalf("初始化测试群失败: %v", err)
		}

		insertWithCreatedAt := func(messageID int, amount float64, note string, createdAt string) {
			t.Helper()
			result, applyErr := database.ApplyLedgerEntry(LedgerEntryInput{
				ChatID:           chatID,
				MessageID:        messageID,
				UserID:           433001,
				Username:         "cp433",
				Nickname:         "RangeTester",
				EntryType:        "deposit",
				Amount:           amount,
				Currency:         "CNY",
				BaseAmount:       amount,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      amount,
				Note:             note,
				TrackMessageLink: "tg://msg/cp433",
			})
			if applyErr != nil {
				t.Fatalf("写入查单样本失败: %v", applyErr)
			}
			if result.EntryID == 0 || result.Duplicate {
				t.Fatalf("写入查单样本结果异常: %+v", result)
			}
			if _, updateErr := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, createdAt, result.EntryID); updateErr != nil {
				t.Fatalf("设置样本时间失败: %v", updateErr)
			}
		}

		insertWithCreatedAt(433001, 66.6, "范围前订单", "2026-02-17 23:59:59")
		insertWithCreatedAt(433002, 88.8, "范围内订单A", "2026-02-18 09:15:00")
		insertWithCreatedAt(433003, 99.9, "范围内订单B", "2026-02-19 21:30:00")
		insertWithCreatedAt(433004, 77.7, "范围后订单", "2026-02-20 00:00:00")

		startAt := "2026-02-18 00:00:00"
		endAt := "2026-02-20 00:00:00"

		rows, err := database.SearchOrdersInRange(chatID, "范围内订单", nil, startAt, endAt)
		if err != nil {
			t.Fatalf("按时间范围+关键词查单失败: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("范围内关键词查单应命中 2 条，实际=%d rows=%+v", len(rows), rows)
		}
		if rows[0].MessageID != 433003 || rows[1].MessageID != 433002 {
			t.Fatalf("范围查单结果顺序错误: %+v", rows)
		}

		insideAmount := 88.8
		insideRows, err := database.SearchOrdersInRange(chatID, "", &insideAmount, startAt, endAt)
		if err != nil {
			t.Fatalf("按时间范围+金额查单失败: %v", err)
		}
		if len(insideRows) != 1 || insideRows[0].MessageID != 433002 {
			t.Fatalf("范围内金额查单结果异常: %+v", insideRows)
		}

		afterAmount := 77.7
		afterRows, err := database.SearchOrdersInRange(chatID, "", &afterAmount, startAt, endAt)
		if err != nil {
			t.Fatalf("按范围过滤边界金额失败: %v", err)
		}
		if len(afterRows) != 0 {
			t.Fatalf("结束日期应按开区间处理，期望 0 条，实际=%d rows=%+v", len(afterRows), afterRows)
		}

		fallbackRows, err := database.SearchOrdersInRange(chatID, "范围后订单", nil, startAt, "")
		if err != nil {
			t.Fatalf("无结束日期查单失败: %v", err)
		}
		if len(fallbackRows) != 1 || fallbackRows[0].MessageID != 433004 {
			t.Fatalf("无结束日期应回退原始查单逻辑: %+v", fallbackRows)
		}
	})
}
