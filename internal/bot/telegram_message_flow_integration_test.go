package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/config"
	"go-bot/internal/db"
)

type telegramFlowMessageCapture struct {
	chatID  int64
	replyTo int
	text    string
}

func TestControlPoint533IntegrationTesting(t *testing.T) {
	t.Run("控制点 5.3.3 功能验证", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "control-point-5-3-3.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer func() {
			if closeErr := database.Close(); closeErr != nil {
				t.Fatalf("关闭数据库失败: %v", closeErr)
			}
		}()

		var (
			mu       sync.Mutex
			captured []telegramFlowMessageCapture
		)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/sendMessage") {
				if parseErr := r.ParseForm(); parseErr != nil {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
					return
				}
				chatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("chat_id")), 10, 64)
				replyTo, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("reply_to_message_id")))
				text := strings.TrimSpace(r.PostForm.Get("text"))
				mu.Lock()
				captured = append(captured, telegramFlowMessageCapture{
					chatID:  chatID,
					replyTo: replyTo,
					text:    text,
				})
				mu.Unlock()
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":533001}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}))
		defer server.Close()

		api := &tgbotapi.BotAPI{
			Token:  "control-point-5-3-3-token",
			Client: server.Client(),
		}
		api.SetAPIEndpoint(server.URL + "/bot%s/%s")

		app := &App{
			cfg: config.Config{
				Timezone: "Asia/Shanghai",
			},
			loc: time.FixedZone("CST", 8*3600),
			db:  database,
			api: api,
		}

		chatID := int64(-100533001)
		userID := int64(53300101)
		if _, err := database.EnsureGroup(chatID, "控制点5.3.3集成测试群"); err != nil {
			t.Fatalf("初始化群配置失败: %v", err)
		}
		if err := database.SetOwner(chatID, userID); err != nil {
			t.Fatalf("设置权限人失败: %v", err)
		}
		baseDate := int(time.Now().Unix())
		updates := []tgbotapi.Update{
			newGroupTextUpdate(533001, 533001, chatID, userID, "cp533", "控制点", "五三三", "开始记账", baseDate),
			newGroupTextUpdate(533002, 533002, chatID, userID, "cp533", "控制点", "五三三", "+100#首笔入款", baseDate+1),
			newGroupTextUpdate(533003, 533003, chatID, userID, "cp533", "控制点", "五三三", "-20#首笔下发", baseDate+2),
			newGroupTextUpdate(533004, 533004, chatID, userID, "cp533", "控制点", "五三三", "一键账单", baseDate+3),
			newGroupTextUpdate(533005, 533005, chatID, userID, "cp533", "控制点", "五三三", "+0", baseDate+4),
		}
		for _, upd := range updates {
			app.dispatchUpdate(context.Background(), upd)
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(chatID)
		if err != nil {
			t.Fatalf("查询账务余额失败: %v", err)
		}
		if !almostZero(depositTotal - 100) {
			t.Fatalf("总入款金额异常: want=100 got=%v", depositTotal)
		}
		if !almostZero(payoutTotal - 20) {
			t.Fatalf("总下发金额异常: want=20 got=%v", payoutTotal)
		}

		entries, err := database.ListLedgerSince(chatID, "")
		if err != nil {
			t.Fatalf("查询账务明细失败: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("账务明细数量异常: want=2 got=%d", len(entries))
		}
		if entries[0].EntryType != "deposit" || entries[1].EntryType != "payout" {
			t.Fatalf("账务明细类型异常: %+v", entries)
		}

		mu.Lock()
		snapshot := append([]telegramFlowMessageCapture(nil), captured...)
		mu.Unlock()
		if len(snapshot) < 5 {
			t.Fatalf("消息回执数量异常: want>=5 got=%d", len(snapshot))
		}
		if !containsCapturedReply(snapshot, chatID, 533001, "已开启记账。") {
			t.Fatalf("未找到开始记账回执: %+v", snapshot)
		}
		if !containsCapturedReply(snapshot, chatID, 533002, "当前入款:") {
			t.Fatalf("未找到入款记账回执: %+v", snapshot)
		}
		if !containsCapturedReply(snapshot, chatID, 533003, "当前入款:") {
			t.Fatalf("未找到下发记账回执: %+v", snapshot)
		}
		if !containsCapturedReply(snapshot, chatID, 533004, "一键账单（日切") {
			t.Fatalf("未找到账单回执: %+v", snapshot)
		}
		if !containsCapturedReply(snapshot, chatID, 533005, "今日入款") {
			t.Fatalf("未找到+0快捷卡片回执: %+v", snapshot)
		}
		if !containsCapturedText(snapshot, chatID, "入款笔数：1") {
			t.Fatalf("账单缺少入款统计: %+v", snapshot)
		}
		if !containsCapturedText(snapshot, chatID, "下发笔数：1") {
			t.Fatalf("账单缺少下发统计: %+v", snapshot)
		}
	})
}

func newGroupTextUpdate(updateID, messageID int, chatID, userID int64, username, firstName, lastName, text string, date int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "控制点5.3.3集成测试群",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: firstName,
				LastName:  lastName,
			},
			Text: text,
		},
	}
}

func containsCapturedReply(snapshot []telegramFlowMessageCapture, chatID int64, replyTo int, keyword string) bool {
	for _, item := range snapshot {
		if item.chatID != chatID || item.replyTo != replyTo {
			continue
		}
		if strings.Contains(item.text, keyword) {
			return true
		}
	}
	return false
}

func containsCapturedText(snapshot []telegramFlowMessageCapture, chatID int64, keyword string) bool {
	for _, item := range snapshot {
		if item.chatID != chatID {
			continue
		}
		if strings.Contains(item.text, keyword) {
			return true
		}
	}
	return false
}
