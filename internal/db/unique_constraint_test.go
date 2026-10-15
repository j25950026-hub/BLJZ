package db

import (
	"path/filepath"
	"testing"
)

func TestControlPoint153DBUniqueConstraint(t *testing.T) {
	t.Run("控制点 1.5.3 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "db-unique-constraint.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		assertLedgerMessageUniqueIndex(t, database)

		input := LedgerEntryInput{
			ChatID:           -10022334455,
			MessageID:        9903,
			UserID:           32003,
			Username:         "unique-constraint-user",
			Nickname:         "UniqueConstraintUser",
			EntryType:        "deposit",
			Amount:           135,
			Currency:         "CNY",
			BaseAmount:       135,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      135,
			Note:             "db-unique-constraint",
			TrackMessageLink: "tg://msg/db-unique-constraint",
		}

		first, err := database.ApplyLedgerEntry(input)
		if err != nil {
			t.Fatalf("首次写入记账失败: %v", err)
		}
		if first.Duplicate || first.EntryID == 0 {
			t.Fatalf("首次写入结果异常: %+v", first)
		}

		if _, err := database.raw.Exec(
			`DELETE FROM message_idempotency WHERE chat_id = ? AND message_id = ? AND command_key = 'ledger'`,
			input.ChatID,
			input.MessageID,
		); err != nil {
			t.Fatalf("模拟幂等记录丢失失败: %v", err)
		}

		retry, err := database.ApplyLedgerEntry(input)
		if err != nil {
			t.Fatalf("idx_msg_id 兜底应返回重复而非错误: %v", err)
		}
		if !retry.Duplicate {
			t.Fatalf("缺失幂等记录时应由 idx_msg_id 阻止重复写入: %+v", retry)
		}

		ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ? AND message_id = ?`, input.ChatID, input.MessageID)
		if ledgerCount != 1 {
			t.Fatalf("重复请求后 ledger_entries 条数应保持 1，实际=%d", ledgerCount)
		}

		idempotencyCount := mustCountRows(t, database, `SELECT COUNT(1) FROM message_idempotency WHERE chat_id = ? AND message_id = ? AND command_key = 'ledger'`, input.ChatID, input.MessageID)
		if idempotencyCount != 1 {
			t.Fatalf("idx_msg_id 兜底后应恢复 message_idempotency，实际=%d", idempotencyCount)
		}
	})
}

func assertLedgerMessageUniqueIndex(t *testing.T, database *Database) {
	t.Helper()

	rows, err := database.raw.Query(`PRAGMA index_list('ledger_entries')`)
	if err != nil {
		t.Fatalf("查询索引列表失败: %v", err)
	}
	defer rows.Close()

	found := false
	unique := false
	for rows.Next() {
		var (
			seq      int
			name     string
			isUnique int
			origin   string
			partial  int
		)
		if err := rows.Scan(&seq, &name, &isUnique, &origin, &partial); err != nil {
			t.Fatalf("读取索引列表失败: %v", err)
		}
		if name == "idx_msg_id" {
			found = true
			unique = isUnique == 1
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历索引列表失败: %v", err)
	}
	if !found {
		t.Fatalf("缺少唯一索引 idx_msg_id")
	}
	if !unique {
		t.Fatalf("idx_msg_id 必须为 UNIQUE 索引")
	}

	columns := make([]string, 0, 2)
	indexRows, err := database.raw.Query(`PRAGMA index_info('idx_msg_id')`)
	if err != nil {
		t.Fatalf("查询 idx_msg_id 列信息失败: %v", err)
	}
	defer indexRows.Close()

	for indexRows.Next() {
		var (
			seqNo int
			cid   int
			name  string
		)
		if err := indexRows.Scan(&seqNo, &cid, &name); err != nil {
			t.Fatalf("读取 idx_msg_id 列信息失败: %v", err)
		}
		columns = append(columns, name)
	}
	if err := indexRows.Err(); err != nil {
		t.Fatalf("遍历 idx_msg_id 列信息失败: %v", err)
	}
	if len(columns) != 2 || columns[0] != "chat_id" || columns[1] != "message_id" {
		t.Fatalf("idx_msg_id 列定义异常: %v", columns)
	}
}
