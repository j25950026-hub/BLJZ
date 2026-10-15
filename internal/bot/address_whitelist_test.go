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

func TestControlPoint314WhitelistEnforcement(t *testing.T) {
	t.Run("控制点 3.1.4 功能验证", func(t *testing.T) {
		const (
			whitelistAddress = "0x742d35Cc6634C0532925a3b844Bc454e4438f44e"
			foreignAddress   = "0x1111111111111111111111111111111111111111"
		)

		cases := []struct {
			name                string
			entryType           string
			note                string
			payoutAddress       string
			exclusiveAddress    string
			expectReceiptText   string
			expectDepositAmount float64
			expectPayoutAmount  float64
		}{
			{
				name:                "下发备注命中白名单地址允许入账",
				entryType:           "payout",
				note:                "控制点3.1.4-白名单 0X742D35CC6634C0532925A3B844BC454E4438F44E",
				exclusiveAddress:    whitelistAddress,
				expectReceiptText:   "当前入款:",
				expectDepositAmount: 0,
				expectPayoutAmount:  1000,
			},
			{
				name:                "业务风控关闭后下发备注命中非白名单地址也允许入账",
				entryType:           "payout",
				note:                "控制点3.1.4-拦截 " + foreignAddress,
				exclusiveAddress:    whitelistAddress,
				expectReceiptText:   "当前入款:",
				expectDepositAmount: 0,
				expectPayoutAmount:  1000,
			},
			{
				name:                "业务风控关闭后下发备注缺失地址也允许入账",
				entryType:           "payout",
				note:                "控制点3.1.4-无地址",
				exclusiveAddress:    whitelistAddress,
				expectReceiptText:   "当前入款:",
				expectDepositAmount: 0,
				expectPayoutAmount:  1000,
			},
			{
				name:                "入款流程不受专属下发地址白名单影响",
				entryType:           "deposit",
				note:                "控制点3.1.4-入款 " + foreignAddress,
				exclusiveAddress:    whitelistAddress,
				expectReceiptText:   "当前入款:",
				expectDepositAmount: 1000,
				expectPayoutAmount:  0,
			},
			{
				name:                "下发备注命中下发地址允许入账",
				entryType:           "payout",
				note:                "控制点3.1.4-下发地址 0X742D35CC6634C0532925A3B844BC454E4438F44E",
				payoutAddress:       whitelistAddress,
				expectReceiptText:   "当前入款:",
				expectDepositAmount: 0,
				expectPayoutAmount:  1000,
			},
			{
				name:                "业务风控关闭后下发备注命中非下发地址也允许入账",
				entryType:           "payout",
				note:                "控制点3.1.4-下发地址拦截 " + foreignAddress,
				payoutAddress:       whitelistAddress,
				expectReceiptText:   "当前入款:",
				expectDepositAmount: 0,
				expectPayoutAmount:  1000,
			},
			{
				name:                "业务风控关闭后下发备注缺失下发地址也允许入账",
				entryType:           "payout",
				note:                "控制点3.1.4-下发地址无地址",
				payoutAddress:       whitelistAddress,
				expectReceiptText:   "当前入款:",
				expectDepositAmount: 0,
				expectPayoutAmount:  1000,
			},
			{
				name:                "入款流程不受下发地址白名单影响",
				entryType:           "deposit",
				note:                "控制点3.1.4-入款 " + foreignAddress,
				payoutAddress:       whitelistAddress,
				expectReceiptText:   "当前入款:",
				expectDepositAmount: 1000,
				expectPayoutAmount:  0,
			},
		}

		for idx, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				database, err := db.NewDatabase(filepath.Join(t.TempDir(), "whitelist-enforcement.db"))
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
						_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9314}}`))
						return
					}
					_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
				}))
				defer server.Close()

				api := &tgbotapi.BotAPI{
					Token:  "whitelist-enforcement-test-token",
					Client: server.Client(),
				}
				api.SetAPIEndpoint(server.URL + "/bot%s/%s")

				chatID := int64(-100314001 - idx)
				msg := &tgbotapi.Message{
					MessageID: 314001 + idx,
					Chat: &tgbotapi.Chat{
						ID:    chatID,
						Title: "控制点3.1.4测试群",
					},
					From: &tgbotapi.User{
						ID:        31400101,
						UserName:  "cp314",
						FirstName: "Control",
						LastName:  "Point",
					},
				}

				app := &App{
					cfg: config.Config{
						Timezone: "Asia/Shanghai",
					},
					db:  database,
					api: api,
				}

				err = app.handleLedger(msg, db.GroupConfig{
					ChatID:           chatID,
					CustomRate:       1,
					PayoutRate:       1,
					PayoutAddress:    tc.payoutAddress,
					ExclusiveAddress: tc.exclusiveAddress,
				}, &utils.LedgerParsed{
					EntryType: tc.entryType,
					Amount:    1000,
					Currency:  "CNY",
					Note:      tc.note,
				})
				if err != nil {
					t.Fatalf("记账处理失败: %v", err)
				}

				mu.Lock()
				snapshot := append([]capturedSendMessage(nil), sentMessages...)
				mu.Unlock()

				var receiptText string
				for _, item := range snapshot {
					if item.chatID == chatID {
						receiptText = item.text
					}
				}
				if receiptText == "" {
					t.Fatalf("未收到业务群回执: messages=%d", len(snapshot))
				}
				if !strings.Contains(receiptText, tc.expectReceiptText) {
					t.Fatalf("回执内容异常: want_contains=%q got=%q", tc.expectReceiptText, receiptText)
				}

				depositTotal, payoutTotal, err := database.GetLedgerBalance(chatID)
				if err != nil {
					t.Fatalf("读取余额失败: %v", err)
				}
				if !almostZero(depositTotal - tc.expectDepositAmount) {
					t.Fatalf("入款余额异常: want=%v got=%v", tc.expectDepositAmount, depositTotal)
				}
				if !almostZero(payoutTotal - tc.expectPayoutAmount) {
					t.Fatalf("下发余额异常: want=%v got=%v", tc.expectPayoutAmount, payoutTotal)
				}
			})
		}
	})
}
