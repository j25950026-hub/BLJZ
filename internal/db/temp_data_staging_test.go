package db

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlPoint224TempDataStaging(t *testing.T) {
	t.Run("控制点 2.2.4 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "temp-data-staging.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const (
			chatID   int64 = -100224001
			userID   int64 = 224001
			stageKey       = "ledger:create"
		)

		initialPayload := map[string]string{
			"step":      "parse_instruction",
			"raw_input": "+100#首笔入款",
			"currency":  "CNY",
		}
		if err := database.StageTempData(chatID, userID, stageKey, initialPayload, 2*time.Minute); err != nil {
			t.Fatalf("写入第一阶段暂存数据失败: %v", err)
		}

		var storedPayload string
		var expireAtUnix int64
		if err := database.raw.QueryRow(
			`SELECT payload, expire_at_unix FROM temp_data_staging WHERE chat_id = ? AND user_id = ? AND stage_key = ?`,
			chatID,
			userID,
			stageKey,
		).Scan(&storedPayload, &expireAtUnix); err != nil {
			t.Fatalf("读取临时暂存表失败: %v", err)
		}
		if !strings.HasPrefix(storedPayload, sensitiveDataCipherPrefix) {
			t.Fatalf("临时暂存数据应加密存储: %s", storedPayload)
		}
		if expireAtUnix <= time.Now().UTC().Unix() {
			t.Fatalf("临时暂存数据过期时间异常: %d", expireAtUnix)
		}

		loadedPayload, ok, err := database.GetTempData(chatID, userID, stageKey)
		if err != nil {
			t.Fatalf("读取第一阶段暂存数据失败: %v", err)
		}
		if !ok {
			t.Fatalf("第一阶段暂存数据不存在")
		}
		if loadedPayload["step"] != "parse_instruction" || loadedPayload["raw_input"] != "+100#首笔入款" {
			t.Fatalf("第一阶段暂存数据内容异常: %+v", loadedPayload)
		}

		secondPayload := map[string]string{
			"step":      "parse_amount",
			"raw_input": "+100#首笔入款",
			"amount":    "100",
		}
		if err := database.StageTempData(chatID, userID, stageKey, secondPayload, 2*time.Minute); err != nil {
			t.Fatalf("写入第二阶段暂存数据失败: %v", err)
		}
		loadedPayload, ok, err = database.GetTempData(chatID, userID, stageKey)
		if err != nil {
			t.Fatalf("读取第二阶段暂存数据失败: %v", err)
		}
		if !ok {
			t.Fatalf("第二阶段暂存数据不存在")
		}
		if loadedPayload["step"] != "parse_amount" || loadedPayload["amount"] != "100" {
			t.Fatalf("第二阶段暂存数据内容异常: %+v", loadedPayload)
		}

		poppedPayload, ok, err := database.PopTempData(chatID, userID, stageKey)
		if err != nil {
			t.Fatalf("消费暂存数据失败: %v", err)
		}
		if !ok {
			t.Fatalf("消费暂存数据时应命中记录")
		}
		if poppedPayload["step"] != "parse_amount" {
			t.Fatalf("消费出的暂存数据异常: %+v", poppedPayload)
		}
		if _, ok, err = database.GetTempData(chatID, userID, stageKey); err != nil {
			t.Fatalf("消费后读取暂存数据失败: %v", err)
		} else if ok {
			t.Fatalf("消费后暂存数据应被清理")
		}

		if err := database.StageTempData(chatID, userID, "expired", map[string]string{"step": "will_expire"}, -time.Second); err != nil {
			t.Fatalf("写入即过期暂存数据失败: %v", err)
		}
		if _, ok, err = database.GetTempData(chatID, userID, "expired"); err != nil {
			t.Fatalf("读取过期暂存数据失败: %v", err)
		} else if ok {
			t.Fatalf("过期暂存数据应在读取时自动失效")
		}
	})
}
