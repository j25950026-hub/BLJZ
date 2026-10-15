package db

import (
	"database/sql"
	"math"
	"path/filepath"
	"testing"
)

func TestControlPoint411ReadWriteSplit(t *testing.T) {
	t.Run("控制点 4.1.1 功能验证", func(t *testing.T) {
		tmpDir := t.TempDir()
		masterPath := filepath.Join(tmpDir, "master.db")
		slavePath := filepath.Join(tmpDir, "slave.db")
		replicaBootstrap, err := NewDatabase(slavePath)
		if err != nil {
			t.Fatalf("初始化 Slave 模式库失败: %v", err)
		}
		if closeErr := replicaBootstrap.Close(); closeErr != nil {
			t.Fatalf("关闭 Slave 模式库失败: %v", closeErr)
		}

		database, err := NewDatabaseWithOptions(masterPath, Options{
			ReadReplicaPath: slavePath,
		})
		if err != nil {
			t.Fatalf("初始化读写分离数据库失败: %v", err)
		}
		defer func() {
			if closeErr := database.Close(); closeErr != nil {
				t.Fatalf("关闭数据库失败: %v", closeErr)
			}
		}()

		if database.readRaw == nil || database.readRaw == database.raw {
			t.Fatalf("应初始化独立 Slave 连接")
		}

		entryID, err := database.AddLedgerEntry(LedgerEntryInput{
			ChatID:           410100,
			MessageID:        7001,
			UserID:           9001,
			Username:         "master_user",
			Nickname:         "MasterUser",
			EntryType:        "deposit",
			Amount:           100,
			Currency:         "CNY",
			BaseAmount:       100,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      100,
			Note:             "master-only-entry",
			CardNo:           "6222000012345678",
			TrackMessageLink: "tg://msg/7001",
		})
		if err != nil {
			t.Fatalf("Master 写入失败: %v", err)
		}
		if entryID == 0 {
			t.Fatalf("Master 写入应返回有效主键")
		}

		if got := mustCountRowsByRaw(t, database.raw, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, int64(410100)); got != 1 {
			t.Fatalf("Master 账单条数错误: %d", got)
		}
		if got := mustCountRowsByRaw(t, database.readRaw, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, int64(410100)); got != 0 {
			t.Fatalf("Slave 不应收到写入: %d", got)
		}

		startAt := "2000-01-01 00:00:00"
		rows, err := database.ListOrderList(410100, startAt, 10)
		if err != nil {
			t.Fatalf("查单列表读取失败: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("Slave 为空时查单应返回 0 条，实际=%d", len(rows))
		}

		if _, err := database.readRaw.Exec(`
INSERT INTO ledger_entries (
	chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
			int64(410100),
			7101,
			int64(9002),
			"slave_user",
			"SlaveUser",
			"deposit",
			88.0,
			"CNY",
			88.0,
			0.0,
			1.0,
			88.0,
			"slave-only-entry",
			"6222000099991111",
			"tg://msg/7101",
			"2026-02-19 00:00:00",
		); err != nil {
			t.Fatalf("写入 Slave 账单样本失败: %v", err)
		}
		if _, err := database.readRaw.Exec(`
INSERT INTO ledger_balances (chat_id, deposit_total, payout_total, updated_at)
VALUES (?, ?, ?, ?)`,
			int64(410100),
			888.0,
			123.0,
			"2026-02-19 00:00:00",
		); err != nil {
			t.Fatalf("写入 Slave 汇总样本失败: %v", err)
		}

		rows, err = database.ListOrderList(410100, startAt, 10)
		if err != nil {
			t.Fatalf("查单列表读取失败: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("查单列表应从 Slave 读取 1 条，实际=%d", len(rows))
		}
		if rows[0].MessageID != 7101 || rows[0].Note != "slave-only-entry" {
			t.Fatalf("查单列表应命中 Slave 样本: %+v", rows[0])
		}

		matched, err := database.SearchOrders(410100, "slave-only", nil, startAt)
		if err != nil {
			t.Fatalf("自助查单读取失败: %v", err)
		}
		if len(matched) != 1 || matched[0].MessageID != 7101 {
			t.Fatalf("自助查单应命中 Slave 样本: %+v", matched)
		}

		deposit, payout, err := database.GetLedgerBalance(410100)
		if err != nil {
			t.Fatalf("账单汇总读取失败: %v", err)
		}
		if !floatEquals(deposit, 888) || !floatEquals(payout, 123) {
			t.Fatalf("账单汇总应读取 Slave 快照，deposit=%v payout=%v", deposit, payout)
		}
	})
}

func mustCountRowsByRaw(t *testing.T, raw *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := raw.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("统计行数失败: %v", err)
	}
	return count
}

func floatEquals(left, right float64) bool {
	return math.Abs(left-right) <= 1e-9
}
