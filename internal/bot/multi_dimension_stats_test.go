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
)

type multiDimensionStatsMessage struct {
	chatID int64
	text   string
}

func TestControlPoint434MultiDimensionStats(t *testing.T) {
	t.Run("控制点 4.3.4 功能验证", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "multi-dimension-stats.db"))
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
			messages []multiDimensionStatsMessage
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
				messages = append(messages, multiDimensionStatsMessage{chatID: chatID, text: text})
				mu.Unlock()
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":43401}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}))
		defer server.Close()

		api := &tgbotapi.BotAPI{
			Token:  "multi-dimension-stats-test-token",
			Client: server.Client(),
		}
		api.SetAPIEndpoint(server.URL + "/bot%s/%s")

		app := &App{
			cfg: config.Config{Timezone: "Asia/Shanghai"},
			db:  database,
			api: api,
		}

		chatIDA := int64(-100434001)
		chatIDB := int64(-100434002)
		groupType := "cp434"
		dayCutTime := "00:00:00"

		if _, err := database.EnsureGroup(chatIDA, "控制点4.3.4-A群"); err != nil {
			t.Fatalf("初始化A群失败: %v", err)
		}
		if _, err := database.EnsureGroup(chatIDB, "控制点4.3.4-B群"); err != nil {
			t.Fatalf("初始化B群失败: %v", err)
		}
		if err := database.UpdateGroupFields(chatIDA, map[string]any{"group_type": groupType, "day_cut_time": dayCutTime}); err != nil {
			t.Fatalf("更新A群配置失败: %v", err)
		}
		if err := database.UpdateGroupFields(chatIDB, map[string]any{"group_type": groupType, "day_cut_time": dayCutTime}); err != nil {
			t.Fatalf("更新B群配置失败: %v", err)
		}

		insertStatsLedgerEntry(t, database, db.LedgerEntryInput{
			ChatID:      chatIDA,
			MessageID:   434001,
			UserID:      4341001,
			Username:    "alice",
			Nickname:    "Alice",
			EntryType:   "deposit",
			Amount:      100,
			Currency:    "CNY",
			BaseAmount:  100,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 100,
			Note:        "银行卡入款",
			CardNo:      "6222000000000000",
		})
		insertStatsLedgerEntry(t, database, db.LedgerEntryInput{
			ChatID:      chatIDA,
			MessageID:   434002,
			UserID:      4341001,
			Username:    "alice",
			Nickname:    "Alice",
			EntryType:   "payout",
			Amount:      20,
			Currency:    "USDT",
			BaseAmount:  20,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 20,
			Note:        "wechat 下发",
		})
		insertStatsLedgerEntry(t, database, db.LedgerEntryInput{
			ChatID:      chatIDA,
			MessageID:   434003,
			UserID:      4341002,
			Username:    "bob",
			Nickname:    "Bob",
			EntryType:   "deposit",
			Amount:      50,
			Currency:    "USDT",
			BaseAmount:  50,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 50,
			Note:        "支付宝转入",
		})
		insertStatsLedgerEntry(t, database, db.LedgerEntryInput{
			ChatID:      chatIDA,
			MessageID:   434004,
			UserID:      4341002,
			Username:    "bob",
			Nickname:    "Bob",
			EntryType:   "payout",
			Amount:      10,
			Currency:    "CNY",
			BaseAmount:  10,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 10,
			Note:        "现金下发",
		})
		insertStatsLedgerEntry(t, database, db.LedgerEntryInput{
			ChatID:      chatIDB,
			MessageID:   434101,
			UserID:      4341001,
			Username:    "alice",
			Nickname:    "Alice",
			EntryType:   "deposit",
			Amount:      30,
			Currency:    "CNY",
			BaseAmount:  30,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 30,
			Note:        "bank transfer",
			CardNo:      "6223000000000000",
		})
		insertStatsLedgerEntry(t, database, db.LedgerEntryInput{
			ChatID:      chatIDB,
			MessageID:   434102,
			UserID:      4341003,
			Username:    "charlie",
			Nickname:    "Charlie",
			EntryType:   "payout",
			Amount:      5,
			Currency:    "CNY",
			BaseAmount:  5,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 5,
			Note:        "alipay 下发",
		})

		groupA, err := database.GetGroup(chatIDA)
		if err != nil {
			t.Fatalf("读取A群配置失败: %v", err)
		}

		msg := &tgbotapi.Message{
			MessageID: 434201,
			Chat: &tgbotapi.Chat{
				ID:    chatIDA,
				Title: groupA.Title,
			},
		}

		if err := app.showSingleGroupStats(msg, groupA); err != nil {
			t.Fatalf("执行单独群数据统计失败: %v", err)
		}
		singleText := latestStatsMessageText(messagesSnapshot(&mu, messages), chatIDA)
		requireContainsAll(t, singleText,
			"按币种统计：",
			"CNY：入款 1 笔 / 100，下发 1 笔 / 10，利润 90",
			"USDT：入款 1 笔 / 50，下发 1 笔 / 20，利润 30",
			"按操作人统计：",
			"@alice(4341001)：入款 1 笔 / 100，下发 1 笔 / 20，利润 80",
			"@bob(4341002)：入款 1 笔 / 50，下发 1 笔 / 10，利润 40",
			"按支付方式统计：",
			"银行卡：入款 1 笔 / 100，下发 0 笔 / 0，利润 100",
			"支付宝：入款 1 笔 / 50，下发 0 笔 / 0，利润 50",
			"微信：入款 0 笔 / 0，下发 1 笔 / 20，利润 -20",
			"其他：入款 0 笔 / 0，下发 1 笔 / 10，利润 -10",
		)

		if err := app.showSameTypeStats(msg, groupA); err != nil {
			t.Fatalf("执行同类型群数据统计失败: %v", err)
		}
		sameTypeText := latestStatsMessageText(messagesSnapshot(&mu, messages), chatIDA)
		requireContainsAll(t, sameTypeText,
			"同类型群数据统计（类型：cp434）",
			"群数量：2",
			"总入款：3 笔 / 180",
			"总下发：3 笔 / 35",
			"CNY：入款 2 笔 / 130，下发 2 笔 / 15，利润 115",
			"USDT：入款 1 笔 / 50，下发 1 笔 / 20，利润 30",
			"@alice(4341001)：入款 2 笔 / 130，下发 1 笔 / 20，利润 110",
			"@charlie(4341003)：入款 0 笔 / 0，下发 1 笔 / 5，利润 -5",
			"银行卡：入款 2 笔 / 130，下发 0 笔 / 0，利润 130",
			"支付宝：入款 1 笔 / 50，下发 1 笔 / 5，利润 45",
		)
	})
}

func insertStatsLedgerEntry(t *testing.T, database *db.Database, input db.LedgerEntryInput) {
	t.Helper()
	if _, err := database.AddLedgerEntry(input); err != nil {
		t.Fatalf("写入账单失败: %v, input=%+v", err, input)
	}
}

func messagesSnapshot(mu *sync.Mutex, messages []multiDimensionStatsMessage) []multiDimensionStatsMessage {
	mu.Lock()
	defer mu.Unlock()
	return append([]multiDimensionStatsMessage(nil), messages...)
}

func latestStatsMessageText(messages []multiDimensionStatsMessage, chatID int64) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].chatID == chatID {
			return messages[i].text
		}
	}
	return ""
}

func requireContainsAll(t *testing.T, got string, wants ...string) {
	t.Helper()
	if strings.TrimSpace(got) == "" {
		t.Fatalf("消息内容为空")
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("消息缺少预期片段: %q\n完整消息:\n%s", want, got)
		}
	}
}
