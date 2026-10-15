package db

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// BuildRequestFingerprint 根据 Telegram MessageID + ChatID 生成稳定请求指纹。
func BuildRequestFingerprint(chatID int64, messageID int) string {
	payload := fmt.Sprintf("%d:%d", messageID, chatID)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
