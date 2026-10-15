package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestControlPoint445DataEncryption(t *testing.T) {
	t.Run("控制点 4.4.5 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "data-encryption.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		input := LedgerEntryInput{
			ChatID:           -1004455001,
			MessageID:        4455001,
			UserID:           4455001,
			Username:         "encrypt-user",
			Nickname:         "EncryptUser",
			EntryType:        "deposit",
			Amount:           188.8,
			Currency:         "CNY",
			BaseAmount:       188.8,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      188.8,
			Note:             "敏感备注-4455",
			CardNo:           "6222000044550001",
			TrackMessageLink: "tg://msg/4455001",
		}
		result, err := database.ApplyLedgerEntry(input)
		if err != nil {
			t.Fatalf("写入账单失败: %v", err)
		}
		if result.EntryID == 0 {
			t.Fatalf("应返回有效账单ID")
		}

		var (
			storedNote   string
			storedCardNo string
		)
		if err := database.raw.QueryRow(`SELECT note, card_no FROM ledger_entries WHERE id = ?`, result.EntryID).Scan(&storedNote, &storedCardNo); err != nil {
			t.Fatalf("读取数据库密文失败: %v", err)
		}
		if storedNote == input.Note || storedCardNo == input.CardNo {
			t.Fatalf("敏感字段应加密存储: note=%q card_no=%q", storedNote, storedCardNo)
		}
		if !strings.HasPrefix(storedNote, sensitiveDataCipherPrefix) {
			t.Fatalf("备注字段缺少密文标识: %q", storedNote)
		}
		if !strings.HasPrefix(storedCardNo, sensitiveDataCipherPrefix) {
			t.Fatalf("卡号字段缺少密文标识: %q", storedCardNo)
		}

		last, err := database.GetLastLedger(input.ChatID, "")
		if err != nil {
			t.Fatalf("查询账单失败: %v", err)
		}
		if last.Note != input.Note || last.CardNo != input.CardNo {
			t.Fatalf("读取账单应自动解密: %+v", last)
		}

		rows, err := database.SearchOrders(input.ChatID, "敏感备注-4455", nil, "2000-01-01 00:00:00")
		if err != nil {
			t.Fatalf("按备注查单失败: %v", err)
		}
		if len(rows) != 1 || rows[0].ID != result.EntryID {
			t.Fatalf("按备注查单应命中新记录: %+v", rows)
		}

		delayedID, err := database.AddDelayedQuery(input.ChatID, input.UserID, "enc-keyword", nil, input.Note)
		if err != nil {
			t.Fatalf("写入延迟查单失败: %v", err)
		}
		if delayedID == 0 {
			t.Fatalf("延迟查单ID无效")
		}
		var delayedStoredNote string
		if err := database.raw.QueryRow(`SELECT note FROM delayed_queries WHERE id = ?`, delayedID).Scan(&delayedStoredNote); err != nil {
			t.Fatalf("读取延迟查单备注失败: %v", err)
		}
		if delayedStoredNote == input.Note || !strings.HasPrefix(delayedStoredNote, sensitiveDataCipherPrefix) {
			t.Fatalf("延迟查单备注未加密存储: %q", delayedStoredNote)
		}
		delayedRows, err := database.ListDelayedQueries(input.ChatID, "pending")
		if err != nil {
			t.Fatalf("读取延迟查单列表失败: %v", err)
		}
		if len(delayedRows) != 1 || delayedRows[0].Note != input.Note {
			t.Fatalf("延迟查单备注应自动解密: %+v", delayedRows)
		}

		if err := database.SetBranchCodeOverride(input.ChatID, input.CardNo, "102100099996", "中国工商银行深圳南山支行", input.Note, input.UserID); err != nil {
			t.Fatalf("设置联行号覆盖失败: %v", err)
		}
		var branchStoredNote string
		if err := database.raw.QueryRow(`SELECT note FROM branch_code_overrides WHERE chat_id = ? AND card_no = ?`, input.ChatID, input.CardNo).Scan(&branchStoredNote); err != nil {
			t.Fatalf("读取联行号备注失败: %v", err)
		}
		if branchStoredNote == input.Note || !strings.HasPrefix(branchStoredNote, sensitiveDataCipherPrefix) {
			t.Fatalf("联行号备注未加密存储: %q", branchStoredNote)
		}
		override, err := database.GetBranchCodeOverride(input.ChatID, input.CardNo)
		if err != nil {
			t.Fatalf("读取联行号覆盖失败: %v", err)
		}
		if override == nil || override.Note != input.Note {
			t.Fatalf("联行号备注应自动解密: %+v", override)
		}
	})
}
