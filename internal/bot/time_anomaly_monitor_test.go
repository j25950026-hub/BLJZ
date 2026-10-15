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

	"go-bot/internal/config"
	"go-bot/internal/db"
	"go-bot/internal/utils"
)

func TestControlPoint324TimeAnomalyMonitor(t *testing.T) {
	t.Run("控制点 3.2.4 功能验证", func(t *testing.T) {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			t.Fatalf("加载时区失败: %v", err)
		}

		cases := []struct {
			name           string
			now            time.Time
			workHoursStart string
			workHoursEnd   string
		}{
			{
				name:           "业务风控关闭后工作时间外也不触发预警",
				now:            time.Date(2026, 2, 19, 3, 15, 0, 0, loc),
				workHoursStart: "09:00:00",
				workHoursEnd:   "18:00:00",
			},
			{
				name:           "常规工作时间内不触发预警",
				now:            time.Date(2026, 2, 19, 10, 20, 0, 0, loc),
				workHoursStart: "09:00:00",
				workHoursEnd:   "18:00:00",
			},
			{
				name:           "跨日班次在窗口内不触发预警",
				now:            time.Date(2026, 2, 19, 23, 40, 0, 0, loc),
				workHoursStart: "22:00:00",
				workHoursEnd:   "06:00:00",
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "time-anomaly-monitor.db")
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
						_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9059}}`))
						return
					}
					_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
				}))
				defer server.Close()

				api := &tgbotapi.BotAPI{
					Token:  "time-anomaly-monitor-test-token",
					Client: server.Client(),
				}
				api.SetAPIEndpoint(server.URL + "/bot%s/%s")

				chatID := int64(-100324001)
				supportChatID := int64(-100324999)
				msg := &tgbotapi.Message{
					MessageID: 324001,
					Chat: &tgbotapi.Chat{
						ID:    chatID,
						Title: "控制点3.2.4测试群",
					},
					From: &tgbotapi.User{
						ID:        32400101,
						UserName:  "cp324",
						FirstName: "Control",
						LastName:  "Point",
					},
				}

				app := &App{
					cfg: config.Config{
						SupportChatID:     supportChatID,
						Timezone:          "Asia/Shanghai",
						WorkHoursStart:    tc.workHoursStart,
						WorkHoursEnd:      tc.workHoursEnd,
						LargeTxnThreshold: 0,
					},
					loc: loc,
					now: func() time.Time { return tc.now },
					db:  database,
					api: api,
				}

				err = app.handleLedger(msg, db.GroupConfig{
					ChatID:     chatID,
					CustomRate: 1,
					PayoutRate: 1,
				}, &utils.LedgerParsed{
					EntryType: "deposit",
					Amount:    1000,
					Currency:  "CNY",
					Note:      "控制点3.2.4测试",
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
					t.Fatalf("业务风控关闭后不应发送管理员通知: %q", alertText)
				}
				if strings.Contains(receiptText, "⚠️ 异常时间预警") {
					t.Fatalf("业务风控关闭后交易回执不应包含异常时间预警: %q", receiptText)
				}
				if count := queryTimeAnomalyRiskEventCount(t, dbPath, chatID); count != 0 {
					t.Fatalf("业务风控关闭后不应写入异常时间风险事件: got=%d", count)
				}
			})
		}
	})
}

func queryTimeAnomalyRiskEventCount(t *testing.T, dbPath string, chatID int64) int {
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
		"time_anomaly",
	).Scan(&count); err != nil {
		t.Fatalf("查询异常时间风险事件数量失败: %v", err)
	}
	return count
}
