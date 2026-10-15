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

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/config"
	"go-bot/internal/db"
	"go-bot/internal/utils"
)

func TestControlPoint321DuplicateAlert(t *testing.T) {
	t.Run("控制点 3.2.1 功能验证", func(t *testing.T) {
		const (
			testCardNo  = "4539578763621486"
			testAddress = "0x742d35Cc6634C0532925a3b844Bc454e4438f44e"
		)

		cases := []struct {
			name   string
			first  utils.LedgerParsed
			second utils.LedgerParsed
		}{
			{
				name: "短时间内相同金额触发预警",
				first: utils.LedgerParsed{
					EntryType: "deposit",
					Amount:    888,
					Currency:  "CNY",
					Note:      "控制点3.2.1-金额首笔",
				},
				second: utils.LedgerParsed{
					EntryType: "deposit",
					Amount:    888,
					Currency:  "CNY",
					Note:      "控制点3.2.1-金额重复",
				},
			},
			{
				name: "短时间内相同银行卡触发预警",
				first: utils.LedgerParsed{
					EntryType: "deposit",
					Amount:    1001,
					Currency:  "CNY",
					Note:      "控制点3.2.1-银行卡首笔 " + testCardNo,
				},
				second: utils.LedgerParsed{
					EntryType: "deposit",
					Amount:    1999,
					Currency:  "CNY",
					Note:      "控制点3.2.1-银行卡重复 " + testCardNo,
				},
			},
			{
				name: "短时间内相同地址触发预警",
				first: utils.LedgerParsed{
					EntryType: "payout",
					Amount:    3001,
					Currency:  "CNY",
					Note:      "控制点3.2.1-地址首笔 " + testAddress,
				},
				second: utils.LedgerParsed{
					EntryType: "payout",
					Amount:    3555,
					Currency:  "CNY",
					Note:      "控制点3.2.1-地址重复 " + strings.ToLower(testAddress),
				},
			},
		}

		for idx, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "duplicate-alert.db")
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
						_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9321}}`))
						return
					}
					_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
				}))
				defer server.Close()

				api := &tgbotapi.BotAPI{
					Token:  "duplicate-alert-test-token",
					Client: server.Client(),
				}
				api.SetAPIEndpoint(server.URL + "/bot%s/%s")

				chatID := int64(-100321001 - idx)
				buildMsg := func(messageID int) *tgbotapi.Message {
					return &tgbotapi.Message{
						MessageID: messageID,
						Chat: &tgbotapi.Chat{
							ID:    chatID,
							Title: "控制点3.2.1测试群",
						},
						From: &tgbotapi.User{
							ID:        32100101,
							UserName:  "cp321",
							FirstName: "Control",
							LastName:  "Point",
						},
					}
				}

				latestReceipt := func() string {
					mu.Lock()
					defer mu.Unlock()
					for i := len(sentMessages) - 1; i >= 0; i-- {
						if sentMessages[i].chatID == chatID {
							return sentMessages[i].text
						}
					}
					return ""
				}

				groupCfg := db.GroupConfig{
					ChatID:     chatID,
					CustomRate: 1,
					PayoutRate: 1,
				}

				app := &App{
					cfg: config.Config{
						Timezone: "Asia/Shanghai",
					},
					db:  database,
					api: api,
				}

				if err := app.handleLedger(buildMsg(321100+idx*10), groupCfg, &tc.first); err != nil {
					t.Fatalf("首笔记账失败: %v", err)
				}
				firstReceipt := latestReceipt()
				if firstReceipt == "" {
					t.Fatalf("首笔记账未发送回执")
				}
				if !strings.Contains(firstReceipt, "当前入款:") {
					t.Fatalf("首笔回执异常: %q", firstReceipt)
				}
				if strings.Contains(firstReceipt, "⚠️ 重复预警") {
					t.Fatalf("首笔记账不应触发重复预警: %q", firstReceipt)
				}

				if err := app.handleLedger(buildMsg(321101+idx*10), groupCfg, &tc.second); err != nil {
					t.Fatalf("第二笔记账失败: %v", err)
				}
				secondReceipt := latestReceipt()
				if secondReceipt == "" {
					t.Fatalf("第二笔记账未发送回执")
				}
				if !strings.Contains(secondReceipt, "当前入款:") {
					t.Fatalf("第二笔回执异常: %q", secondReceipt)
				}
				if strings.Contains(secondReceipt, "⚠️ 重复预警") {
					t.Fatalf("业务风控关闭后第二笔记账不应触发重复预警提示: %q", secondReceipt)
				}
				if count := queryDuplicateRiskEventCount(t, dbPath, chatID); count != 0 {
					t.Fatalf("业务风控关闭后不应写入重复预警风险事件: got=%d", count)
				}
			})
		}
	})
}

func queryDuplicateRiskEventCount(t *testing.T, dbPath string, chatID int64) int {
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
		`SELECT COUNT(1) FROM risk_events WHERE chat_id = ? AND event_type IN (?, ?, ?)`,
		chatID,
		"duplicate_amount",
		"duplicate_card",
		"duplicate_address",
	).Scan(&count); err != nil {
		t.Fatalf("查询重复预警风险事件数量失败: %v", err)
	}
	return count
}
