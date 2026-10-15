package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestControlPoint413HotColdSeparation(t *testing.T) {
	t.Run("控制点 4.1.3 功能验证", func(t *testing.T) {
		tmpDir := t.TempDir()
		masterPath := filepath.Join(tmpDir, "hot.db")
		archivePath := filepath.Join(tmpDir, "cold", "archive.db")

		database, err := NewDatabaseWithOptions(masterPath, Options{
			ColdArchivePath:      archivePath,
			HotDataRetention:     90 * 24 * time.Hour,
			ColdArchiveSweep:     24 * time.Hour,
			ColdArchiveBatchSize: 16,
		})
		if err != nil {
			t.Fatalf("初始化冷热分离数据库失败: %v", err)
		}
		defer func() {
			if closeErr := database.Close(); closeErr != nil {
				t.Fatalf("关闭数据库失败: %v", closeErr)
			}
		}()

		const chatID int64 = 413001
		now := time.Date(2026, 2, 19, 12, 0, 0, 0, time.UTC)

		historicalEntryID := mustInsertLedgerEntryAt(t, database, LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        8101,
			UserID:           9011,
			Username:         "history_user",
			Nickname:         "HistoryUser",
			EntryType:        "deposit",
			Amount:           100,
			Currency:         "CNY",
			BaseAmount:       100,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      100,
			Note:             "history-entry",
			CardNo:           "6222000041300001",
			TrackMessageLink: "tg://msg/8101",
		}, now.AddDate(0, -4, 0))

		hotEntryID := mustInsertLedgerEntryAt(t, database, LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        8102,
			UserID:           9012,
			Username:         "hot_user",
			Nickname:         "HotUser",
			EntryType:        "deposit",
			Amount:           200,
			Currency:         "CNY",
			BaseAmount:       200,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      200,
			Note:             "hot-entry",
			CardNo:           "6222000041300002",
			TrackMessageLink: "tg://msg/8102",
		}, now.AddDate(0, -1, 0))

		moved, err := database.archiveHistoricalLedgerEntries(now)
		if err != nil {
			t.Fatalf("执行历史归档失败: %v", err)
		}
		if moved != 1 {
			t.Fatalf("归档迁移数量错误: got=%d want=1", moved)
		}

		if got := mustCountRowsByRaw(t, database.raw, `SELECT COUNT(1) FROM ledger_entries WHERE id = ?`, historicalEntryID); got != 0 {
			t.Fatalf("历史流水应已从热库移除: %d", got)
		}
		if got := mustCountRowsByRaw(t, database.raw, `SELECT COUNT(1) FROM ledger_entries WHERE id = ?`, hotEntryID); got != 1 {
			t.Fatalf("近3个月热数据应保留在热库: %d", got)
		}

		if database.coldArchiveRaw == nil {
			t.Fatalf("冷归档库连接未初始化")
		}
		if got := mustCountRowsByRaw(t, database.coldArchiveRaw, `SELECT COUNT(1) FROM ledger_entries_archive WHERE id = ?`, historicalEntryID); got != 1 {
			t.Fatalf("历史流水应已归档至冷库: %d", got)
		}
		if got := mustCountRowsByRaw(t, database.coldArchiveRaw, `SELECT COUNT(1) FROM ledger_entries_archive WHERE id = ?`, hotEntryID); got != 0 {
			t.Fatalf("热数据不应进入冷库: %d", got)
		}

		orders, err := database.ListOrderList(chatID, "2000-01-01 00:00:00", 20)
		if err != nil {
			t.Fatalf("查询热库订单失败: %v", err)
		}
		if len(orders) != 1 || orders[0].ID != hotEntryID {
			t.Fatalf("热库查单应仅返回近3个月数据: %+v", orders)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(chatID)
		if err != nil {
			t.Fatalf("读取余额快照失败: %v", err)
		}
		if !floatEquals(depositTotal, 300) || !floatEquals(payoutTotal, 0) {
			t.Fatalf("归档后余额快照应保持不变: deposit=%v payout=%v", depositTotal, payoutTotal)
		}

		movedAgain, err := database.archiveHistoricalLedgerEntries(now)
		if err != nil {
			t.Fatalf("重复归档执行失败: %v", err)
		}
		if movedAgain != 0 {
			t.Fatalf("重复执行归档不应重复迁移: %d", movedAgain)
		}
	})
}

func mustInsertLedgerEntryAt(t *testing.T, database *Database, input LedgerEntryInput, createdAt time.Time) int64 {
	t.Helper()

	entryID, err := database.AddLedgerEntry(input)
	if err != nil {
		t.Fatalf("写入账单失败: %v", err)
	}
	if _, err := database.raw.Exec(`UPDATE ledger_entries SET created_at = ? WHERE id = ?`, createdAt.UTC().Format(sqliteDateTimeLayout), entryID); err != nil {
		t.Fatalf("写入账单时间失败: %v", err)
	}
	return entryID
}
