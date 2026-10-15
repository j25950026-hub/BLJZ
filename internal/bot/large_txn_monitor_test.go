package bot

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "modernc.org/sqlite"

	"go-bot/internal/config"
	"go-bot/internal/db"
	"go-bot/internal/utils"
)

type capturedSendMessage struct {
	chatID int64
	text   string
}

func TestControlPoint323LargeTransactionMonitor(t *testing.T) {
	t.Run("控制点 3.2.3 功能验证", func(t *testing.T) {
		cases := []struct {
			name      string
			entryType string
			amount    float64
			threshold float64
		}{
			{
				name:      "入款超过阈值不触发大额风控",
				entryType: "deposit",
				amount:    60000,
				threshold: 50000,
			},
			{
				name:      "下发超过阈值不触发大额风控",
				entryType: "payout",
				amount:    68000,
				threshold: 50000,
			},
			{
				name:      "阈值内交易不触发大额风控",
				entryType: "deposit",
				amount:    49999,
				threshold: 50000,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "large-txn-monitor.db")
				database, err := db.NewDatabase(dbPath)
				if err != nil {
					t.Fatalf("初始化数据库失败: %v", err)
				}
				defer func() {
					if closeErr := database.Close(); closeErr != nil {
						t.Fatalf("关闭数据库失败: %v", closeErr)
					}
				}()

				var (
					sentMessages []capturedSendMessage
					mu           sync.Mutex
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
						text := strings.TrimSpace(r.PostForm.Get("text"))
						mu.Lock()
						sentMessages = append(sentMessages, capturedSendMessage{
							chatID: chatID,
							text:   text,
						})
						mu.Unlock()
						_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9001}}`))
						return
					}
					_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
				}))
				defer server.Close()

				api := &tgbotapi.BotAPI{
					Token:  "large-txn-monitor-test-token",
					Client: server.Client(),
				}
				api.SetAPIEndpoint(server.URL + "/bot%s/%s")

				loc, err := time.LoadLocation("Asia/Shanghai")
				if err != nil {
					t.Fatalf("加载时区失败: %v", err)
				}

				chatID := int64(-100323001)
				supportChatID := int64(-100323999)
				msg := &tgbotapi.Message{
					MessageID: 323001,
					Chat: &tgbotapi.Chat{
						ID:    chatID,
						Title: "控制点3.2.3测试群",
					},
					From: &tgbotapi.User{
						ID:        32300101,
						UserName:  "cp323",
						FirstName: "Control",
						LastName:  "Point",
					},
				}

				app := &App{
					cfg: config.Config{
						LargeTxnThreshold: tc.threshold,
						SupportChatID:     supportChatID,
					},
					loc: loc,
					db:  database,
					api: api,
				}

				err = app.handleLedger(msg, db.GroupConfig{
					ChatID:     chatID,
					CustomRate: 1,
					PayoutRate: 1,
				}, &utils.LedgerParsed{
					EntryType: tc.entryType,
					Amount:    tc.amount,
					Currency:  "CNY",
					Note:      "控制点3.2.3测试",
				})
				if err != nil {
					t.Fatalf("记账处理失败: %v", err)
				}

				mu.Lock()
				snapshot := append([]capturedSendMessage(nil), sentMessages...)
				mu.Unlock()

				var (
					receiptText string
					alertText   string
				)
				for _, item := range snapshot {
					if item.chatID == chatID {
						receiptText = item.text
					}
					if item.chatID == supportChatID {
						alertText = item.text
					}
				}

				if receiptText == "" {
					t.Fatalf("记账回执未发送到业务群: messages=%d", len(snapshot))
				}
				if alertText != "" {
					t.Fatalf("大额交易风控关闭后不应发送管理员通知: %q", alertText)
				}
				if strings.Contains(receiptText, "⚠️ 大额交易预警") {
					t.Fatalf("群内回执不应包含大额预警: %q", receiptText)
				}
				if count := queryLargeTransactionRiskEventCount(t, dbPath, chatID); count != 0 {
					t.Fatalf("大额交易风控关闭后不应写入风险事件: got=%d", count)
				}
			})
		}
	})
}

func queryLargeTransactionRiskEventCount(t *testing.T, dbPath string, chatID int64) int {
	t.Helper()

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Fatalf("关闭查询连接失败: %v", closeErr)
		}
	}()

	count := 0
	if err := conn.QueryRow(
		`SELECT COUNT(1) FROM risk_events WHERE chat_id = ? AND event_type = ?`,
		chatID,
		"large_transaction",
	).Scan(&count); err != nil {
		t.Fatalf("查询大额交易风险事件数量失败: %v", err)
	}
	return count
}
