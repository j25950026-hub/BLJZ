package bot

import (
	"context"
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

	"go-bot/internal/db"
	"go-bot/internal/services"
)

type stubRateDeviationExchange struct {
	rows []services.RateResult
	err  error
}

func (s *stubRateDeviationExchange) GetRate(provider, payType, fiat string) (*services.RateResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.rows) == 0 {
		return nil, nil
	}
	row := s.rows[0]
	return &row, nil
}

func (s *stubRateDeviationExchange) CompareRates(payType, fiat string) ([]services.RateResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]services.RateResult, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

func TestControlPoint332RateDeviationAlert(t *testing.T) {
	t.Run("控制点 3.3.2 功能验证", func(t *testing.T) {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			t.Fatalf("加载时区失败: %v", err)
		}
		fixedNow := time.Date(2026, 2, 19, 20, 22, 0, 0, loc)
		marketRows := []services.RateResult{
			{Provider: "okx", Price: 7.20},
			{Provider: "huobi", Price: 7.22},
			{Provider: "binance", Price: 7.18},
		}

		cases := []struct {
			name         string
			command      string
			expectedRate float64
		}{
			{
				name:         "业务风控关闭后偏离超阈值不再告警",
				command:      "设置汇率6.2",
				expectedRate: 6.2,
			},
			{
				name:         "阈值内本来也不告警",
				command:      "设置汇率7.15",
				expectedRate: 7.15,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "rate-deviation-alert.db")
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
					mu           sync.Mutex
					sentMessages []capturedSendMessage
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
						sentMessages = append(sentMessages, capturedSendMessage{chatID: chatID, text: text})
						mu.Unlock()
						_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":33201}}`))
						return
					}
					_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
				}))
				defer server.Close()

				api := &tgbotapi.BotAPI{
					Token:  "rate-deviation-alert-test-token",
					Client: server.Client(),
				}
				api.SetAPIEndpoint(server.URL + "/bot%s/%s")

				const (
					chatID  int64 = -100332001
					ownerID int64 = 33200101
				)
				if _, err := database.EnsureGroup(chatID, "控制点3.3.2测试群"); err != nil {
					t.Fatalf("初始化群配置失败: %v", err)
				}
				if err := database.SetOwner(chatID, ownerID); err != nil {
					t.Fatalf("设置权限人失败: %v", err)
				}
				group, err := database.GetGroup(chatID)
				if err != nil {
					t.Fatalf("读取群配置失败: %v", err)
				}

				app := &App{
					db:       database,
					api:      api,
					loc:      loc,
					now:      func() time.Time { return fixedNow },
					exchange: &stubRateDeviationExchange{rows: marketRows},
					userFSM:  NewMemoryUserFSM(MemoryUserFSMConfig{TTL: 5 * time.Minute}),
				}
				msg := &tgbotapi.Message{
					MessageID: 332001,
					Date:      int(fixedNow.Unix()),
					Chat: &tgbotapi.Chat{
						ID:    chatID,
						Type:  "supergroup",
						Title: "控制点3.3.2测试群",
					},
					From: &tgbotapi.User{
						ID:        ownerID,
						UserName:  "cp332",
						FirstName: "Control",
						LastName:  "Point",
					},
					Text: tc.command,
				}

				if err := app.dispatchGroupMessage(context.Background(), msg, group, tc.command); err != nil {
					t.Fatalf("执行设置汇率命令失败: %v", err)
				}

				updated, err := database.GetGroup(chatID)
				if err != nil {
					t.Fatalf("读取更新后的群配置失败: %v", err)
				}
				if !almostZero(updated.CustomRate - tc.expectedRate) {
					t.Fatalf("入款汇率写入错误: want=%.2f got=%.8f", tc.expectedRate, updated.CustomRate)
				}
				if updated.UseRealtimeRate != 0 {
					t.Fatalf("设置自定义汇率后应关闭实时汇率: got=%d", updated.UseRealtimeRate)
				}

				mu.Lock()
				snapshot := append([]capturedSendMessage(nil), sentMessages...)
				mu.Unlock()

				groupReply := ""
				ownerAlert := ""
				for _, item := range snapshot {
					if item.chatID == chatID && strings.Contains(item.text, "入款汇率已设置为") {
						groupReply = item.text
					}
					if item.chatID == ownerID && strings.Contains(item.text, "【汇率偏离告警】") {
						ownerAlert = item.text
					}
				}
				if groupReply == "" {
					t.Fatalf("未收到群内设置回执: %+v", snapshot)
				}
				if strings.Contains(groupReply, "⚠️ 汇率偏离预警") {
					t.Fatalf("业务风控关闭后群内回执不应出现汇率偏离提示: %q", groupReply)
				}
				if ownerAlert != "" {
					t.Fatalf("业务风控关闭后不应发送权限人汇率告警: %q", ownerAlert)
				}

				riskCount, severity, detail := queryRateDeviationRiskEvent(t, dbPath, chatID)
				if riskCount != 0 {
					t.Fatalf("业务风控关闭后不应写入汇率偏离风险事件: got=%d severity=%q detail=%q", riskCount, severity, detail)
				}
			})
		}
	})
}

func queryRateDeviationRiskEvent(t *testing.T, dbPath string, chatID int64) (int, string, string) {
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
		rateDeviationEventType,
	).Scan(&count); err != nil {
		t.Fatalf("查询风险事件数量失败: %v", err)
	}
	if count == 0 {
		return 0, "", ""
	}

	var (
		severity string
		detail   string
	)
	if err := conn.QueryRow(
		`SELECT severity, detail FROM risk_events WHERE chat_id = ? AND event_type = ? ORDER BY id DESC LIMIT 1`,
		chatID,
		rateDeviationEventType,
	).Scan(&severity, &detail); err != nil {
		t.Fatalf("查询风险事件详情失败: %v", err)
	}
	return count, severity, detail
}

func TestResolveRateDeviationSignalUsesFairMarketPrice(t *testing.T) {
	app := &App{
		exchange: &stubRateDeviationExchange{
			rows: []services.RateResult{
				{Provider: "okx", Price: 6.90},
				{Provider: "huobi", Price: 7.20},
				{Provider: "binance", Price: 7.65},
			},
		},
	}

	marketRate, deviation, snapshot, ok := app.resolveRateDeviationSignal(7.18)
	if !ok {
		t.Fatalf("应成功计算市场公允价")
	}
	if !almostZero(marketRate - 7.20) {
		t.Fatalf("市场公允价错误: want=7.20 got=%.8f", marketRate)
	}
	if deviation <= 0 || deviation >= 0.01 {
		t.Fatalf("偏离比例应接近去极值后的结果: got=%.8f", deviation)
	}
	for _, providerQuote := range []string{"BINANCE=7.65", "HUOBI=7.2", "OKX=6.9"} {
		if !strings.Contains(snapshot, providerQuote) {
			t.Fatalf("市场快照缺少报价 %q, got=%q", providerQuote, snapshot)
		}
	}
}
