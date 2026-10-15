package bot

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestControlPoint252DeadLetterQueue(t *testing.T) {
	t.Run("控制点 2.5.2 功能验证", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "dead-letter-queue.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer func() {
			if closeErr := database.Close(); closeErr != nil {
				t.Fatalf("关闭数据库失败: %v", closeErr)
			}
		}()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/sendMessage") {
				_, _ = w.Write([]byte(`{"ok":false,"error_code":500,"description":"mock send failure for dlq"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		}))
		defer server.Close()

		api := &tgbotapi.BotAPI{
			Token:  "dlq-test-token",
			Client: server.Client(),
		}
		api.SetAPIEndpoint(server.URL + "/bot%s/%s")

		app := &App{
			api: api,
			db:  database,
		}
		chatID := int64(-10025201)
		if err := app.sendWithPriority(outboundPriorityNormalChat, tgbotapi.NewMessage(chatID, "DLQ-CONTROL-POINT")); err == nil {
			t.Fatalf("模拟发送失败时应返回错误")
		}

		letters, err := database.ListOutboundDeadLetters(10)
		if err != nil {
			t.Fatalf("读取死信队列失败: %v", err)
		}
		if len(letters) != 1 {
			t.Fatalf("死信条目数量异常: want=1 got=%d", len(letters))
		}
		item := letters[0]
		if item.ChatID != chatID {
			t.Fatalf("死信 chat_id 异常: want=%d got=%d", chatID, item.ChatID)
		}
		if item.Operation != "send" {
			t.Fatalf("死信操作类型异常: want=send got=%q", item.Operation)
		}
		if item.Priority != int(outboundPriorityNormalChat) {
			t.Fatalf("死信优先级异常: want=%d got=%d", outboundPriorityNormalChat, item.Priority)
		}
		if !strings.Contains(item.ErrorMessage, "mock send failure for dlq") {
			t.Fatalf("死信错误信息异常: %q", item.ErrorMessage)
		}
		if !strings.Contains(item.Payload, "DLQ-CONTROL-POINT") {
			t.Fatalf("死信载荷应包含原消息内容: %q", item.Payload)
		}
	})
}
