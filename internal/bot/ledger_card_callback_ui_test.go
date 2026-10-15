package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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

type capturedLedgerCardMessage struct {
	chatID      int64
	text        string
	parseMode   string
	replyMarkup string
}

type capturedLedgerCardCallbackAnswer struct {
	queryID   string
	text      string
	showAlert string
	rawURL    string
}

type capturedLedgerCardEditMarkup struct {
	chatID      int64
	messageID   int
	replyMarkup string
}

func TestLedgerCardReceiptIncludesSingleCallbackButton(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "ledger-card-callback-ui.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	var (
		mu       sync.Mutex
		captured []capturedLedgerCardMessage
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
			mu.Lock()
			captured = append(captured, capturedLedgerCardMessage{
				chatID:      chatID,
				text:        strings.TrimSpace(r.PostForm.Get("text")),
				parseMode:   strings.TrimSpace(r.PostForm.Get("parse_mode")),
				replyMarkup: strings.TrimSpace(r.PostForm.Get("reply_markup")),
			})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9988}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "ledger-card-callback-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	chatID := int64(-100660001)
	app := &App{
		cfg: config.Config{
			Timezone: "Asia/Shanghai",
		},
		db:  database,
		api: api,
	}
	msg := &tgbotapi.Message{
		MessageID: 660001,
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Title: "账单卡片按钮测试群",
		},
		From: &tgbotapi.User{
			ID:        66000101,
			UserName:  "ledger_ui",
			FirstName: "Ledger",
			LastName:  "UI",
		},
	}
	if err := app.handleLedger(msg, db.GroupConfig{
		ChatID:     chatID,
		CustomRate: 10,
		PayoutRate: 10,
	}, &utils.LedgerParsed{
		EntryType: "deposit",
		Amount:    1000,
		Currency:  "CNY",
		Note:      "卡片按钮校验",
	}); err != nil {
		t.Fatalf("记账处理失败: %v", err)
	}

	mu.Lock()
	snapshot := append([]capturedLedgerCardMessage(nil), captured...)
	mu.Unlock()
	if len(snapshot) == 0 {
		t.Fatalf("未捕获到记账回执消息")
	}
	last := snapshot[len(snapshot)-1]
	if last.chatID != chatID {
		t.Fatalf("回执群ID不匹配: want=%d got=%d", chatID, last.chatID)
	}
	if !strings.Contains(last.text, "今日入款") || !strings.Contains(last.text, "`1000`") {
		t.Fatalf("回执未按账单卡片格式输出: %q", last.text)
	}
	if last.parseMode != telegramParseModeMarkdownV {
		t.Fatalf("记账回执 ParseMode 应为 MarkdownV2: got=%q", last.parseMode)
	}
	if !strings.Contains(last.replyMarkup, "💰 查看账单") {
		t.Fatalf("回执按钮文案缺失: %s", last.replyMarkup)
	}
	if !strings.Contains(last.replyMarkup, ledgerCardCallbackPrefix+ledgerCardActionDetail) {
		t.Fatalf("回执按钮回调路由缺失: %s", last.replyMarkup)
	}
}

func TestHandleLedgerCardCallbackResetsInputContext(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "ledger-card-callback-reset.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	var (
		mu                sync.Mutex
		captured          []capturedLedgerCardMessage
		capturedCallbacks []capturedLedgerCardCallbackAnswer
		capturedEdits     []capturedLedgerCardEditMarkup
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/sendMessage"):
			if parseErr := r.ParseForm(); parseErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
				return
			}
			chatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("chat_id")), 10, 64)
			mu.Lock()
			captured = append(captured, capturedLedgerCardMessage{
				chatID:      chatID,
				text:        strings.TrimSpace(r.PostForm.Get("text")),
				parseMode:   strings.TrimSpace(r.PostForm.Get("parse_mode")),
				replyMarkup: strings.TrimSpace(r.PostForm.Get("reply_markup")),
			})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9989}}`))
			return
		case strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/answerCallbackQuery"):
			if parseErr := r.ParseForm(); parseErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
				return
			}
			mu.Lock()
			capturedCallbacks = append(capturedCallbacks, capturedLedgerCardCallbackAnswer{
				queryID:   strings.TrimSpace(r.PostForm.Get("callback_query_id")),
				text:      strings.TrimSpace(r.PostForm.Get("text")),
				showAlert: strings.TrimSpace(r.PostForm.Get("show_alert")),
				rawURL:    strings.TrimSpace(r.PostForm.Get("url")),
			})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			return
		case strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/editMessageReplyMarkup"):
			if parseErr := r.ParseForm(); parseErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
				return
			}
			chatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("chat_id")), 10, 64)
			messageID, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("message_id")))
			mu.Lock()
			capturedEdits = append(capturedEdits, capturedLedgerCardEditMarkup{
				chatID:      chatID,
				messageID:   messageID,
				replyMarkup: strings.TrimSpace(r.PostForm.Get("reply_markup")),
			})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":660099}}`))
			return
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			return
		}
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "ledger-card-callback-reset-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	chatID := int64(-100660099)
	userID := int64(66009901)
	if _, err := database.EnsureGroup(chatID, "账单按钮回滚群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	app := &App{
		cfg: config.Config{
			Timezone:       "Asia/Shanghai",
			TGWebAppURL:    "https://example.com/tg/webapp",
			TGWebAppSecret: "ledger-card-callback-secret",
		},
		db:      database,
		api:     api,
		userFSM: NewMemoryUserFSM(MemoryUserFSMConfig{TTL: 5 * time.Minute}),
		loc:     time.FixedZone("CST", 8*3600),
		now: func() time.Time {
			return time.Date(2026, 2, 21, 14, 0, 0, 0, time.UTC)
		},
	}
	if _, _, err := app.getUserFSM().Transition(context.Background(), chatID, userID, UserInteractionContext{
		State: UserInteractionStateAwaitCustomRate,
		Scene: "正在设置汇率中",
	}); err != nil {
		t.Fatalf("写入用户状态失败: %v", err)
	}
	if err := database.StageTempData(chatID, userID, rateSetupTempDataKey, map[string]string{
		"mode":  "custom",
		"scene": "正在设置汇率中",
	}, 5*time.Minute); err != nil {
		t.Fatalf("写入暂存状态失败: %v", err)
	}

	if err := app.handleLedgerCardCallback(context.Background(), &tgbotapi.CallbackQuery{
		ID:   "callback-reset-1",
		Data: ledgerCardCallbackPrefix + ledgerCardActionDetail,
		From: &tgbotapi.User{
			ID:       userID,
			UserName: "owner_reset",
		},
		Message: &tgbotapi.Message{
			MessageID: 660099,
			Chat: &tgbotapi.Chat{
				ID: chatID,
			},
		},
	}); err != nil {
		t.Fatalf("处理账单按钮回调失败: %v", err)
	}

	_, ok, err := app.getUserFSM().Get(context.Background(), chatID, userID)
	if err != nil {
		t.Fatalf("读取用户状态失败: %v", err)
	}
	if ok {
		t.Fatalf("点击账单按钮后应清理用户状态")
	}

	_, staged, err := database.GetTempData(chatID, userID, rateSetupTempDataKey)
	if err != nil {
		t.Fatalf("读取暂存状态失败: %v", err)
	}
	if staged {
		t.Fatalf("点击账单按钮后应清理暂存状态")
	}

	mu.Lock()
	snapshot := append([]capturedLedgerCardMessage(nil), captured...)
	mu.Unlock()
	if len(snapshot) != 0 {
		t.Fatalf("点击查看账单不应额外发送确认消息: %+v", snapshot)
	}
	_, ledgerOpenStaged, err := database.GetTempData(chatID, userID, ledgerOpenTempDataKey)
	if err != nil {
		t.Fatalf("读取网页账单确认态失败: %v", err)
	}
	if ledgerOpenStaged {
		t.Fatalf("点击账单按钮后不应写入网页账单确认态")
	}

	mu.Lock()
	callbackSnapshot := append([]capturedLedgerCardCallbackAnswer(nil), capturedCallbacks...)
	editSnapshot := append([]capturedLedgerCardEditMarkup(nil), capturedEdits...)
	mu.Unlock()
	if len(editSnapshot) == 0 {
		t.Fatalf("点击查看账单应更新当前消息按钮为 URL 按钮")
	}
	lastEdit := editSnapshot[len(editSnapshot)-1]
	if lastEdit.chatID != chatID || lastEdit.messageID != 660099 {
		t.Fatalf("按钮更新目标消息异常: %+v", lastEdit)
	}
	if !strings.Contains(lastEdit.replyMarkup, "\"url\":\"https://example.com/tg/webapp/looklook.php") {
		t.Fatalf("按钮更新未写入账单 URL: %+v", lastEdit)
	}
	if !strings.Contains(lastEdit.replyMarkup, ledgerCardCallbackPrefix+ledgerCardActionDetail) {
		t.Fatalf("按钮更新后应保留刷新回调: %+v", lastEdit)
	}
	var openAnswer capturedLedgerCardCallbackAnswer
	for _, answer := range callbackSnapshot {
		if answer.queryID == "callback-reset-1" {
			openAnswer = answer
			break
		}
	}
	if strings.TrimSpace(openAnswer.queryID) == "" {
		t.Fatalf("未捕获到查看账单回调响应: %+v", callbackSnapshot)
	}
	if !strings.Contains(openAnswer.text, "正在生成账单链接") {
		t.Fatalf("查看账单回调提示异常: %+v", openAnswer)
	}
	if strings.TrimSpace(openAnswer.rawURL) != "" {
		t.Fatalf("查看账单回调不应直接携带 URL: %+v", openAnswer)
	}
}

func TestBuildWebAppLooklookURLIncludesRequiredParams(t *testing.T) {
	app := &App{
		cfg: config.Config{
			Timezone:       "Asia/Shanghai",
			TGWebAppURL:    "https://example.com/tg/webapp",
			TGWebAppSecret: "looklook-secret",
		},
		loc: time.FixedZone("CST", 8*3600),
		now: func() time.Time {
			return time.Date(2026, 2, 21, 12, 34, 56, 0, time.UTC)
		},
	}
	raw := app.BuildWebAppLooklookURL(9001, -1001234567890, "06:00:00")
	if strings.TrimSpace(raw) == "" {
		t.Fatalf("应生成网页账单 URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("解析 URL 失败: %v", err)
	}
	if parsed.Path != "/tg/webapp/looklook.php" {
		t.Fatalf("looklook path 异常: %s", parsed.Path)
	}
	query := parsed.Query()
	required := map[string]string{
		"type":    "bjr",
		"groupId": "-1001234567890",
	}
	for key, want := range required {
		if got := strings.TrimSpace(query.Get(key)); got != want {
			t.Fatalf("query[%s] 异常: got=%q want=%q", key, got, want)
		}
	}
	if strings.TrimSpace(query.Get("uid")) == "" || strings.TrimSpace(query.Get("exp")) == "" || strings.TrimSpace(query.Get("sig")) == "" {
		t.Fatalf("缺少 webapp 鉴权参数: %s", raw)
	}
	if strings.TrimSpace(query.Get("begintime")) == "" || strings.TrimSpace(query.Get("endtime")) == "" || strings.TrimSpace(query.Get("daterange")) == "" {
		t.Fatalf("缺少 looklook 时间参数: %s", raw)
	}
}
