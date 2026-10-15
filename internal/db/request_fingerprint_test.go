package db

import (
	"encoding/hex"
	"path/filepath"
	"testing"
)

func TestControlPoint151RequestFingerprint(t *testing.T) {
	t.Run("控制点 1.5.1 功能验证", func(t *testing.T) {
		const (
			chatID    int64 = -10022334455
			messageID       = 9901
		)

		fingerprint := BuildRequestFingerprint(chatID, messageID)
		if fingerprint == "" {
			t.Fatalf("请求指纹不能为空")
		}
		if got := BuildRequestFingerprint(chatID, messageID); got != fingerprint {
			t.Fatalf("相同输入应生成相同指纹: want=%s got=%s", fingerprint, got)
		}
		if len(fingerprint) != 64 {
			t.Fatalf("请求指纹长度错误: %d", len(fingerprint))
		}
		if _, err := hex.DecodeString(fingerprint); err != nil {
			t.Fatalf("请求指纹应为十六进制编码: %v", err)
		}
		if other := BuildRequestFingerprint(chatID, messageID+1); other == fingerprint {
			t.Fatalf("不同 MessageID 不应生成相同指纹: %s", other)
		}
		if other := BuildRequestFingerprint(chatID+1, messageID); other == fingerprint {
			t.Fatalf("不同 ChatID 不应生成相同指纹: %s", other)
		}

		database, err := NewDatabase(filepath.Join(t.TempDir(), "request-fingerprint.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		input := LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        messageID,
			UserID:           32001,
			Username:         "fingerprint-user",
			Nickname:         "FingerprintUser",
			EntryType:        "deposit",
			Amount:           100,
			Currency:         "CNY",
			BaseAmount:       100,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      100,
			Note:             "fingerprint-test",
			TrackMessageLink: "tg://msg/request-fingerprint",
		}
		result, err := database.ApplyLedgerEntry(input)
		if err != nil {
			t.Fatalf("写入记账失败: %v", err)
		}
		if result.Duplicate || result.EntryID == 0 {
			t.Fatalf("写入记账结果异常: %+v", result)
		}

		var storedFingerprint string
		err = database.raw.QueryRow(
			`SELECT request_fingerprint FROM message_idempotency WHERE chat_id = ? AND message_id = ? AND command_key = 'ledger'`,
			chatID,
			messageID,
		).Scan(&storedFingerprint)
		if err != nil {
			t.Fatalf("读取幂等表指纹失败: %v", err)
		}
		if storedFingerprint != fingerprint {
			t.Fatalf("幂等表请求指纹不匹配: want=%s got=%s", fingerprint, storedFingerprint)
		}
	})
}
