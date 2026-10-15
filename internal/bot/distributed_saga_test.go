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

type sagaReceiptCapture struct {
	chatID int64
	text   string
}

func TestControlPoint454DistributedSaga(t *testing.T) {
	t.Run("控制点 4.5.4 功能验证", func(t *testing.T) {
		cases := []struct {
			name               string
			forceNotifyFailure bool
		}{
			{
				name:               "出站可用时记账仅入队回执",
				forceNotifyFailure: false,
			},
			{
				name:               "出站失败时记账仍保留且不触发撤账",
				forceNotifyFailure: true,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				database, err := db.NewDatabase(filepath.Join(t.TempDir(), "distributed-saga.db"))
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
					snapshot []sagaReceiptCapture
				)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/sendMessage") {
						if tc.forceNotifyFailure {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"ok":false,"description":"forced send failure"}`))
							return
						}
						if parseErr := r.ParseForm(); parseErr != nil {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
							return
						}
						chatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("chat_id")), 10, 64)
						text := strings.TrimSpace(r.PostForm.Get("text"))
						mu.Lock()
						snapshot = append(snapshot, sagaReceiptCapture{chatID: chatID, text: text})
						mu.Unlock()
						_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":454001}}`))
						return
					}
					_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
				}))
				defer server.Close()

				api := &tgbotapi.BotAPI{
					Token:  "distributed-saga-test-token",
					Client: server.Client(),
				}
				api.SetAPIEndpoint(server.URL + "/bot%s/%s")

				app := &App{
					cfg:                    config.Config{},
					db:                     database,
					api:                    api,
					ledgerReceiptAsyncOnly: true,
				}

				chatID := int64(-100454001)
				msg := &tgbotapi.Message{
					MessageID: 454001,
					Chat: &tgbotapi.Chat{
						ID:    chatID,
						Title: "控制点4.5.4测试群",
					},
					From: &tgbotapi.User{
						ID:        4541001,
						UserName:  "cp454",
						FirstName: "Control",
						LastName:  "Point",
					},
				}
				err = app.handleLedger(msg, db.GroupConfig{
					ChatID:     chatID,
					CustomRate: 1,
					PayoutRate: 1,
				}, &utils.LedgerParsed{
					EntryType: "deposit",
					Amount:    4567,
					Currency:  "CNY",
					Note:      "控制点4.5.4测试",
				})

				depositTotal, payoutTotal, balErr := database.GetLedgerBalance(chatID)
				if balErr != nil {
					t.Fatalf("查询余额失败: %v", balErr)
				}
				entries, listErr := database.ListLedgerSince(chatID, "")
				if listErr != nil {
					t.Fatalf("查询账目失败: %v", listErr)
				}

				if err != nil {
					t.Fatalf("记账入队回执时不应返回错误: %v", err)
				}
				if almostZero(depositTotal) {
					t.Fatalf("记账成功后余额应被保留: deposit=%f", depositTotal)
				}
				if !almostZero(payoutTotal) {
					t.Fatalf("记账成功时不应产生下发金额: payout=%f", payoutTotal)
				}
				if len(entries) != 1 {
					t.Fatalf("异步回执模式下应只有一条账务记录: got=%d", len(entries))
				}
				if entries[0].Reversed != 0 {
					t.Fatalf("异步回执失败不应触发撤账: %+v", entries[0])
				}

				mu.Lock()
				captured := append([]sagaReceiptCapture(nil), snapshot...)
				mu.Unlock()
				if len(captured) != 0 {
					t.Fatalf("handleLedger 阶段不应同步发送回执: messages=%d", len(captured))
				}
			})
		}
	})
}
