package bot

import (
	"context"
	"encoding/json"
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

type capturedLedgerOutboxMessage struct {
	chatID    int64
	replyTo   int
	text      string
	parseMode string
}

func TestLedgerReceiptOutboxWorker(t *testing.T) {
	t.Run("发送成功后标记 sent 并写缓存", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-outbox-success.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		var (
			mu       sync.Mutex
			captured []capturedLedgerOutboxMessage
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
				captured = append(captured, capturedLedgerOutboxMessage{
					chatID:    chatID,
					replyTo:   replyTo,
					text:      strings.TrimSpace(r.PostForm.Get("text")),
					parseMode: strings.TrimSpace(r.PostForm.Get("parse_mode")),
				})
				mu.Unlock()
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":550001}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}))
		defer server.Close()

		api := &tgbotapi.BotAPI{
			Token:  "ledger-receipt-outbox-success-token",
			Client: server.Client(),
		}
		api.SetAPIEndpoint(server.URL + "/bot%s/%s")

		now := time.Now().UTC().Add(time.Second)
		app := &App{
			cfg: config.Config{
				Timezone:          "Asia/Shanghai",
				LargeTxnThreshold: 5000,
				WorkHoursStart:    "09:00",
				WorkHoursEnd:      "22:00",
				BotToken:          "ledger-receipt-outbox-success-token",
			},
			loc:                    time.FixedZone("CST", 8*3600),
			now:                    func() time.Time { return now },
			db:                     database,
			api:                    api,
			requestResultCache:     newRequestResultCache(0),
			ledgerReceiptAsyncOnly: true,
		}

		msg := &tgbotapi.Message{
			MessageID: 55001,
			Chat: &tgbotapi.Chat{
				ID:    -10055001,
				Title: "回执 outbox 成功测试群",
			},
			From: &tgbotapi.User{
				ID:        5500101,
				UserName:  "outbox_success",
				FirstName: "Outbox",
				LastName:  "Success",
			},
		}
		group := db.GroupConfig{
			ChatID:          msg.Chat.ID,
			FeeRate:         0,
			PayoutFeeRate:   0,
			CustomRate:      1,
			PayoutRate:      1,
			DayCutTime:      "04:00:00",
			DisplayNickname: 1,
		}
		if err := app.handleLedger(msg, group, &utils.LedgerParsed{
			EntryType: "deposit",
			Amount:    1200,
			Currency:  "CNY",
			Note:      "worker-success",
		}); err != nil {
			t.Fatalf("记账失败: %v", err)
		}

		delivered, err := app.deliverNextLedgerReceipt(context.Background())
		if err != nil {
			t.Fatalf("发送 outbox 回执失败: %v", err)
		}
		if !delivered {
			t.Fatalf("预期成功消费一条 outbox")
		}

		mu.Lock()
		snapshot := append([]capturedLedgerOutboxMessage(nil), captured...)
		mu.Unlock()
		if len(snapshot) != 1 {
			t.Fatalf("成功发送后应捕获 1 条回执，实际=%d", len(snapshot))
		}
		item := snapshot[0]
		if item.chatID != msg.Chat.ID || item.replyTo != msg.MessageID {
			t.Fatalf("回执路由异常: %+v", item)
		}
		if item.parseMode != telegramParseModeMarkdownV {
			t.Fatalf("回执 ParseMode 异常: %q", item.parseMode)
		}
		if !strings.Contains(item.text, "今日入款") {
			t.Fatalf("回执内容缺少账单卡片: %q", item.text)
		}
		if _, ok := app.cachedBookkeepingResult(msg, "ledger"); !ok {
			t.Fatalf("发送成功后应写入结果缓存")
		}
	})

	t.Run("发送失败最终入死信且不撤账", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-outbox-failure.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/sendMessage") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"description":"forced async receipt failure"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}))
		defer server.Close()

		api := &tgbotapi.BotAPI{
			Token:  "ledger-receipt-outbox-failure-token",
			Client: server.Client(),
		}
		api.SetAPIEndpoint(server.URL + "/bot%s/%s")

		now := time.Now().UTC().Add(time.Second)
		app := &App{
			cfg: config.Config{
				Timezone: "Asia/Shanghai",
				BotToken: "ledger-receipt-outbox-failure-token",
			},
			loc:                    time.FixedZone("CST", 8*3600),
			now:                    func() time.Time { return now },
			db:                     database,
			api:                    api,
			requestResultCache:     newRequestResultCache(0),
			ledgerReceiptAsyncOnly: true,
		}

		msg := &tgbotapi.Message{
			MessageID: 55002,
			Chat: &tgbotapi.Chat{
				ID:    -10055002,
				Title: "回执 outbox 失败测试群",
			},
			From: &tgbotapi.User{
				ID:        5500202,
				UserName:  "outbox_failure",
				FirstName: "Outbox",
				LastName:  "Failure",
			},
		}
		group := db.GroupConfig{
			ChatID:     msg.Chat.ID,
			CustomRate: 1,
			PayoutRate: 1,
			DayCutTime: "04:00:00",
		}
		if err := app.handleLedger(msg, group, &utils.LedgerParsed{
			EntryType: "deposit",
			Amount:    900,
			Currency:  "CNY",
			Note:      "worker-failure",
		}); err != nil {
			t.Fatalf("记账失败: %v", err)
		}

		for attempt := 0; attempt < ledgerReceiptOutboxMaxAttempts; attempt++ {
			delivered, err := app.deliverNextLedgerReceipt(context.Background())
			if err != nil {
				t.Fatalf("第 %d 次消费 outbox 失败: %v", attempt+1, err)
			}
			if !delivered {
				t.Fatalf("第 %d 次应消费到 outbox", attempt+1)
			}
			now = now.Add(2 * time.Minute)
		}

		delivered, err := app.deliverNextLedgerReceipt(context.Background())
		if err != nil {
			t.Fatalf("最终消费检查失败: %v", err)
		}
		if delivered {
			t.Fatalf("达到最大重试次数后不应再存在可消费 outbox")
		}

		depositTotal, payoutTotal, err := database.GetLedgerBalance(msg.Chat.ID)
		if err != nil {
			t.Fatalf("查询余额失败: %v", err)
		}
		if almostZero(depositTotal) || !almostZero(payoutTotal) {
			t.Fatalf("回执失败不应撤账: deposit=%f payout=%f", depositTotal, payoutTotal)
		}
		entries, err := database.ListLedgerSince(msg.Chat.ID, "")
		if err != nil {
			t.Fatalf("查询账目失败: %v", err)
		}
		if len(entries) != 1 || entries[0].Reversed != 0 {
			t.Fatalf("回执失败不应产生反向分录: %+v", entries)
		}

		letters, err := database.ListOutboundDeadLetters(10)
		if err != nil {
			t.Fatalf("读取死信队列失败: %v", err)
		}
		if len(letters) != 1 {
			t.Fatalf("最终失败后应写入 1 条死信，实际=%d", len(letters))
		}
		if !strings.Contains(letters[0].ErrorMessage, "forced async receipt failure") {
			t.Fatalf("死信错误信息异常: %q", letters[0].ErrorMessage)
		}
	})
}

func TestBuildLedgerReceiptMessageRefreshesCurrentReversalVisibility(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-current-visibility.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -10058001
	if _, err := database.EnsureGroup(chatID, "outbox 当前开关测试群"); err != nil {
		t.Fatalf("创建测试群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"ledger_card_hide_reversed": 1}); err != nil {
		t.Fatalf("开启账单卡片隐藏失败: %v", err)
	}
	origin, err := database.ApplyLedgerEntry(db.LedgerEntryInput{
		ChatID:      chatID,
		MessageID:   58001,
		UserID:      5800101,
		EntryType:   "deposit",
		Amount:      100,
		Currency:    "CNY",
		BaseAmount:  100,
		Rate:        1,
		FinalAmount: 100,
		Note:        "outbox-current-origin",
	})
	if err != nil {
		t.Fatalf("创建原始账务失败: %v", err)
	}
	if _, err := database.ReverseLastLedger(chatID, 58002, 5800102, "operator", "Operator", "deposit", "tg://msg/58002"); err != nil {
		t.Fatalf("撤回原始账务失败: %v", err)
	}

	payload, err := json.Marshal(ledgerReceiptOutboxPayload{
		Group: db.GroupConfig{
			ChatID:                 chatID,
			CustomRate:             1,
			PayoutRate:             1,
			DayCutTime:             "04:00:00",
			LedgerCardHideReversed: 0,
		},
		Fallback: ledgerCardFallback{
			EntryType:   "deposit",
			Amount:      100,
			Currency:    "CNY",
			Rate:        1,
			FinalAmount: 100,
			Note:        "outbox-current-origin",
			CreatedAt:   time.Now().UTC(),
		},
		ParseMode: telegramParseModeMarkdownV,
	})
	if err != nil {
		t.Fatalf("构造 outbox payload 失败: %v", err)
	}
	app := &App{db: database, cfg: config.Config{Timezone: "Asia/Shanghai"}, loc: time.FixedZone("CST", 8*3600)}
	_, text, _, err := app.buildLedgerReceiptMessage(&db.LedgerReceiptOutbox{ChatID: chatID, Payload: string(payload)})
	if err != nil {
		t.Fatalf("构造 outbox 账单卡片失败: %v", err)
	}
	if strings.Contains(text, "outbox-current-origin") || strings.Contains(text, "已撤回") {
		t.Fatalf("outbox 投递应读取当前隐藏开关: %s", text)
	}
	if origin.EntryID == 0 {
		t.Fatalf("原始账务 ID 不应为空")
	}
}

func TestLedgerReceiptOutboxDoesNotRetryOnContextCancel(t *testing.T) {
	t.Run("取消前不 claim", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-outbox-cancel-before-claim.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		now := time.Now().UTC().Add(time.Second)
		seedLedgerReceiptOutboxForBotTest(t, database, -10056001, 56001, 5600101, now)
		app := &App{
			db:  database,
			api: newLedgerReceiptOutboxTestAPI(t, nil),
			now: func() time.Time { return now },
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		delivered, err := app.deliverNextLedgerReceiptAt(ctx, now)
		if err == nil || !errorsIsContextDone(err) {
			t.Fatalf("取消上下文应返回取消错误，got delivered=%t err=%v", delivered, err)
		}
		if delivered {
			t.Fatalf("取消前不应 claim 或消费 outbox")
		}

		item, err := database.ClaimLedgerReceiptOutbox(now.Add(time.Second), time.Minute)
		if err != nil {
			t.Fatalf("取消后重新 claim 失败: %v", err)
		}
		if item == nil {
			t.Fatalf("取消前未 claim 的 outbox 应保持可消费")
		}
		if item.AttemptCount != 1 {
			t.Fatalf("取消前不应增加 attempt，重新 claim 后 attempt 应为 1，got=%d", item.AttemptCount)
		}
	})

	t.Run("排队等待取消释放 claim 且不计 retry", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-outbox-cancel-queued.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		now := time.Now().UTC().Add(time.Second)
		seedLedgerReceiptOutboxForBotTest(t, database, -10056002, 56002, 5600202, now)
		queue := newPriorityDispatchQueueWithWorkers(4, 1)
		defer closePriorityQueueForTest(t, queue)

		blockedStarted := make(chan struct{})
		releaseBlocked := make(chan struct{})
		blockedDone, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
			close(blockedStarted)
			<-releaseBlocked
			return nil
		})
		if err != nil {
			t.Fatalf("提交阻塞任务失败: %v", err)
		}
		waitSignal(t, blockedStarted, "阻塞任务未启动")

		app := &App{
			db:            database,
			api:           newLedgerReceiptOutboxTestAPI(t, nil),
			now:           func() time.Time { return now },
			outboundQueue: queue,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		delivered, err := app.deliverNextLedgerReceiptAt(ctx, now)
		close(releaseBlocked)
		waitTaskDone(t, blockedDone, "阻塞任务")
		if err == nil || !errorsIsContextDone(err) {
			t.Fatalf("排队取消应返回取消错误，got delivered=%t err=%v", delivered, err)
		}
		if delivered {
			t.Fatalf("排队取消不应被视为已消费")
		}

		item, err := database.ClaimLedgerReceiptOutbox(now.Add(time.Second), time.Minute)
		if err != nil {
			t.Fatalf("取消释放后重新 claim 失败: %v", err)
		}
		if item == nil {
			t.Fatalf("取消释放后 outbox 应立即可重新消费")
		}
		if item.AttemptCount != 1 {
			t.Fatalf("取消释放不应计入 retry，重新 claim 后 attempt 应为 1，got=%d", item.AttemptCount)
		}
	})
}

func TestLedgerReceiptOutboxRetryAfter(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-outbox-retry-after.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	now := time.Now().UTC().Add(time.Second)
	seedLedgerReceiptOutboxForBotTest(t, database, -10057001, 57001, 5700101, now)

	metrics := &recordingOutboundMetrics{}
	app := &App{
		db: database,
		api: newLedgerReceiptOutboxTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":9}}`))
		}),
		outboundQueue:   newPriorityDispatchQueueWithWorkers(4, 1),
		outboundMetrics: metrics,
		now:             func() time.Time { return now },
	}
	defer closePriorityQueueForTest(t, app.outboundQueue)

	delivered, err := app.deliverNextLedgerReceiptAt(context.Background(), now)
	if err != nil {
		t.Fatalf("429 retry_after 不应向 worker 返回错误: %v", err)
	}
	if !delivered {
		t.Fatalf("429 retry_after 应被视为已消费并进入 retry")
	}
	if got := metrics.sendResults(); len(got) != 1 || got[0] != "send:bookkeeping_receipt:rate_limited" {
		t.Fatalf("429 发送结果指标错误: got=%v", got)
	}
	if got := metrics.retryAfterResults(); len(got) != 1 || got[0] != "send:bookkeeping_receipt:9s" {
		t.Fatalf("429 retry_after 指标错误: got=%v", got)
	}

	item, err := database.ClaimLedgerReceiptOutbox(now.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("retry_after 延后前 claim 失败: %v", err)
	}
	if item != nil {
		t.Fatalf("429 retry 后 next_attempt_at 应延后，立即不应可重新 claim: %+v", item)
	}

	retryItem, err := database.ClaimLedgerReceiptOutbox(now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("retry_after 延后后重新 claim 失败: %v", err)
	}
	if retryItem == nil {
		t.Fatalf("429 retry 应保持非 final，延后后应可重新 claim")
	}
	if retryItem.AttemptCount != 2 {
		t.Fatalf("retry 后重新 claim 应进入第 2 次尝试，got=%d", retryItem.AttemptCount)
	}
	if !strings.Contains(retryItem.LastError, "Too Many Requests") {
		t.Fatalf("retry 应记录 Telegram 429 错误，got=%q", retryItem.LastError)
	}
}

func seedLedgerReceiptOutboxForBotTest(t *testing.T, database *db.Database, chatID int64, messageID int, userID int64, now time.Time) int64 {
	t.Helper()
	payload, err := json.Marshal(ledgerReceiptOutboxPayload{
		Group: db.GroupConfig{
			ChatID:          chatID,
			CustomRate:      1,
			PayoutRate:      1,
			DayCutTime:      "04:00:00",
			DisplayNickname: 1,
		},
		Fallback: ledgerCardFallback{
			EntryType:   "deposit",
			Amount:      100,
			Currency:    "CNY",
			Rate:        1,
			FinalAmount: 100,
			Note:        "receipt-outbox-cancel",
			CreatedAt:   now,
		},
		ParseMode: telegramParseModeMarkdownV,
		Cacheable: true,
	})
	if err != nil {
		t.Fatalf("构造 outbox payload 失败: %v", err)
	}
	result, err := database.ApplyLedgerEntryWithReceiptOutbox(db.LedgerEntryInput{
		ChatID:      chatID,
		MessageID:   messageID,
		UserID:      userID,
		Username:    "outbox_cancel",
		Nickname:    "OutboxCancel",
		EntryType:   "deposit",
		Amount:      100,
		Currency:    "CNY",
		BaseAmount:  100,
		Rate:        1,
		FinalAmount: 100,
		Note:        "receipt-outbox-cancel",
	}, func(result db.LedgerApplyResult) (*db.LedgerReceiptOutboxInput, error) {
		return &db.LedgerReceiptOutboxInput{
			ChatID:          chatID,
			SourceMessageID: messageID,
			EntryID:         result.EntryID,
			UserID:          userID,
			CommandKey:      "ledger",
			Payload:         string(payload),
		}, nil
	})
	if err != nil {
		t.Fatalf("写入测试 outbox 失败: %v", err)
	}
	return result.EntryID
}

func newLedgerReceiptOutboxTestAPI(t *testing.T, handler http.HandlerFunc) *tgbotapi.BotAPI {
	t.Helper()
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":560000}}`))
		}
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	api := &tgbotapi.BotAPI{
		Token:  "ledger-receipt-outbox-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")
	return api
}
