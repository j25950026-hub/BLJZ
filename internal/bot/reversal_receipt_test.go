package bot

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/config"
	"go-bot/internal/db"
	"go-bot/internal/utils"
)

type capturedReversalReceiptMessage struct {
	chatID    int64
	replyTo   int
	text      string
	parseMode string
}

func TestReversalSuccessReceiptContainsStatusAndAccountingCard(t *testing.T) {
	tests := []struct {
		name             string
		hideReversed     int
		wantDetailedText bool
	}{
		{
			name:             "隐藏开关开启时仅保留撤回成功状态",
			hideReversed:     1,
			wantDetailedText: false,
		},
		{
			name:             "隐藏开关关闭时保留详细撤回状态",
			hideReversed:     0,
			wantDetailedText: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, err := db.NewDatabase(filepath.Join(t.TempDir(), "reversal-receipt.db"))
			if err != nil {
				t.Fatalf("初始化数据库失败: %v", err)
			}
			defer database.Close()

			var (
				mu       sync.Mutex
				captured []capturedReversalReceiptMessage
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
					mu.Lock()
					captured = append(captured, capturedReversalReceiptMessage{
						chatID:    chatID,
						replyTo:   replyTo,
						text:      strings.TrimSpace(r.PostForm.Get("text")),
						parseMode: strings.TrimSpace(r.PostForm.Get("parse_mode")),
					})
					mu.Unlock()
					_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":4499}}`))
					return
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			}))
			defer server.Close()

			api := &tgbotapi.BotAPI{
				Token:  "reversal-receipt-test-token",
				Client: server.Client(),
			}
			api.SetAPIEndpoint(server.URL + "/bot%s/%s")

			chatID := int64(-100449001)
			operatorID := int64(44900101)
			group := db.GroupConfig{
				ChatID:                 chatID,
				CustomRate:             10,
				PayoutRate:             10,
				LedgerCardHideReversed: tt.hideReversed,
			}
			app := &App{
				cfg: config.Config{
					Timezone: "Asia/Shanghai",
				},
				db:  database,
				api: api,
			}

			ledgerMsg := &tgbotapi.Message{
				MessageID: 449001,
				Chat: &tgbotapi.Chat{
					ID:    chatID,
					Title: "撤回回执测试群",
				},
				From: &tgbotapi.User{
					ID:        operatorID,
					UserName:  "cp449",
					FirstName: "Control",
					LastName:  "Point",
				},
			}
			if err := app.handleLedger(ledgerMsg, group, &utils.LedgerParsed{
				EntryType: "deposit",
				Amount:    1000,
				Currency:  "CNY",
				Note:      "撤回回执前置入款",
			}); err != nil {
				t.Fatalf("准备入款失败: %v", err)
			}

			reversalMsg := &tgbotapi.Message{
				MessageID: 449002,
				Chat: &tgbotapi.Chat{
					ID:    chatID,
					Title: "撤回回执测试群",
				},
				From: &tgbotapi.User{
					ID:        operatorID,
					UserName:  "cp449",
					FirstName: "Control",
					LastName:  "Point",
				},
			}
			if err := app.reversalLast(reversalMsg, group, "撤回入款"); err != nil {
				t.Fatalf("撤回失败: %v", err)
			}

			mu.Lock()
			snapshot := append([]capturedReversalReceiptMessage(nil), captured...)
			mu.Unlock()

			var reversalReceipt *capturedReversalReceiptMessage
			for i := range snapshot {
				item := &snapshot[i]
				if item.chatID != chatID || item.replyTo != reversalMsg.MessageID {
					continue
				}
				reversalReceipt = item
			}
			if reversalReceipt == nil {
				t.Fatalf("未捕获到撤回回执: messages=%d", len(snapshot))
			}
			if !strings.Contains(reversalReceipt.text, "撤回成功") {
				t.Fatalf("撤回回执缺少状态文案: %q", reversalReceipt.text)
			}
			const (
				escapedOriginalRef = `原记录 \#`
				escapedReversalRef = `反向记录 \#`
			)
			if tt.wantDetailedText {
				if !strings.Contains(reversalReceipt.text, escapedOriginalRef) || !strings.Contains(reversalReceipt.text, escapedReversalRef) {
					t.Fatalf("关闭隐藏开关后应保留详细撤回状态: %q", reversalReceipt.text)
				}
			} else {
				if strings.Contains(reversalReceipt.text, escapedOriginalRef) || strings.Contains(reversalReceipt.text, escapedReversalRef) {
					t.Fatalf("开启隐藏开关后不应显示已隐藏记录编号: %q", reversalReceipt.text)
				}
			}
			if !strings.Contains(reversalReceipt.text, "今日入款") || !strings.Contains(reversalReceipt.text, "今日下发") {
				t.Fatalf("撤回回执缺少账单卡片: %q", reversalReceipt.text)
			}
			if reversalReceipt.parseMode != telegramParseModeMarkdownV {
				t.Fatalf("撤回回执 ParseMode 应为 MarkdownV2: got=%q", reversalReceipt.parseMode)
			}
		})
	}
}
