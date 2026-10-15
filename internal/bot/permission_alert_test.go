package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

type permissionAlertMessage struct {
	chatID int64
	text   string
}

func TestControlPoint331PermissionAlert(t *testing.T) {
	t.Run("控制点 3.3.1 功能验证", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "permission-alert.db"))
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
			sentMessages []permissionAlertMessage
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
				sentMessages = append(sentMessages, permissionAlertMessage{
					chatID: chatID,
					text:   text,
				})
				mu.Unlock()
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":33101}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}))
		defer server.Close()

		api := &tgbotapi.BotAPI{
			Token:  "permission-alert-test-token",
			Client: server.Client(),
		}
		api.SetAPIEndpoint(server.URL + "/bot%s/%s")

		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			t.Fatalf("加载时区失败: %v", err)
		}
		fixedNow := time.Date(2026, 2, 19, 20, 6, 1, 0, loc)

		app := &App{
			db:  database,
			api: api,
			loc: loc,
			now: func() time.Time { return fixedNow },
		}

		chatID := int64(-100331001)
		ownerID := int64(33100101)
		targetID := int64(33100199)
		targetName := "target331"
		if _, err := database.EnsureGroup(chatID, "控制点3.3.1测试群"); err != nil {
			t.Fatalf("初始化群配置失败: %v", err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置权限人失败: %v", err)
		}
		group := db.GroupConfig{
			ChatID:      chatID,
			OwnerUserID: ownerID,
		}
		buildCommandMessage := func(messageID int) *tgbotapi.Message {
			return &tgbotapi.Message{
				MessageID: messageID,
				Chat: &tgbotapi.Chat{
					ID:    chatID,
					Title: "控制点3.3.1测试群",
				},
				From: &tgbotapi.User{
					ID:       ownerID,
					UserName: "owner331",
				},
				ReplyToMessage: &tgbotapi.Message{
					From: &tgbotapi.User{
						ID:       targetID,
						UserName: targetName,
					},
				},
			}
		}

		if err := app.handleOperatorCommand(buildCommandMessage(331001), group, "设置操作人"); err != nil {
			t.Fatalf("执行设置操作人失败: %v", err)
		}
		localOperators, err := database.ListOperators(chatID, false)
		if err != nil {
			t.Fatalf("查询本群操作人失败: %v", err)
		}
		if len(localOperators) != 1 {
			t.Fatalf("设置操作人后数量异常: want=1 got=%d", len(localOperators))
		}

		if err := app.handleOperatorCommand(buildCommandMessage(331002), group, "删除操作人"); err != nil {
			t.Fatalf("执行删除操作人失败: %v", err)
		}
		localOperators, err = database.ListOperators(chatID, false)
		if err != nil {
			t.Fatalf("查询本群操作人失败: %v", err)
		}
		if len(localOperators) != 0 {
			t.Fatalf("删除操作人后数量异常: want=0 got=%d", len(localOperators))
		}

		mu.Lock()
		snapshot := append([]permissionAlertMessage(nil), sentMessages...)
		mu.Unlock()

		if len(snapshot) < 4 {
			t.Fatalf("消息数量不足，至少应包含2条群内回执和2条私密通知: got=%d", len(snapshot))
		}

		addReply := ""
		removeReply := ""
		addAlert := ""
		removeAlert := ""
		for _, item := range snapshot {
			if item.chatID == chatID {
				if strings.Contains(item.text, "设置本群操作人完成，共 1 人。") {
					addReply = item.text
				}
				if strings.Contains(item.text, "删除本群操作人完成，共 1 人。") {
					removeReply = item.text
				}
			}
			if item.chatID == ownerID {
				if strings.Contains(item.text, "操作：新增本群操作人") {
					addAlert = item.text
				}
				if strings.Contains(item.text, "操作：删除本群操作人") {
					removeAlert = item.text
				}
			}
		}

		if addReply == "" {
			t.Fatalf("缺少新增操作人群内回执: %#v", snapshot)
		}
		if removeReply == "" {
			t.Fatalf("缺少删除操作人群内回执: %#v", snapshot)
		}
		if addAlert == "" {
			t.Fatalf("缺少新增操作人的群主私密通知: %#v", snapshot)
		}
		if removeAlert == "" {
			t.Fatalf("缺少删除操作人的群主私密通知: %#v", snapshot)
		}

		expectedTarget := "目标：@" + targetName + "(" + strconv.FormatInt(targetID, 10) + ")"
		for _, text := range []string{addAlert, removeAlert} {
			if !strings.Contains(text, "【权限变更通知】") {
				t.Fatalf("私密通知缺少标题: %q", text)
			}
			if !strings.Contains(text, "群组ID："+strconv.FormatInt(chatID, 10)) {
				t.Fatalf("私密通知缺少群组ID: %q", text)
			}
			if !strings.Contains(text, "执行人：@owner331("+strconv.FormatInt(ownerID, 10)+")") {
				t.Fatalf("私密通知缺少执行人信息: %q", text)
			}
			if !strings.Contains(text, expectedTarget) {
				t.Fatalf("私密通知缺少目标用户信息: %q", text)
			}
			if !strings.Contains(text, "时间："+fixedNow.Format("2006-01-02 15:04:05")) {
				t.Fatalf("私密通知缺少时间戳: %q", text)
			}
		}
	})
}

func TestHandleOperatorCommandShowsReplyHintWhenUserNotCached(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "operator-missing-cache-hint.db"))
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
		sentMessages []permissionAlertMessage
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
			sentMessages = append(sentMessages, permissionAlertMessage{
				chatID: chatID,
				text:   text,
			})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":33103}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "operator-missing-cache-hint-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	chatID := int64(-100331050)
	ownerID := int64(33105001)
	if _, err := database.EnsureGroup(chatID, "操作人提示测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	app := &App{
		db:  database,
		api: api,
	}
	group := db.GroupConfig{
		ChatID:      chatID,
		OwnerUserID: ownerID,
	}
	msg := &tgbotapi.Message{
		MessageID: 33105001,
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Title: "操作人提示测试群",
		},
		From: &tgbotapi.User{
			ID:       ownerID,
			UserName: "owner331050",
		},
	}

	if err := app.handleOperatorCommand(msg, group, "设置操作人 @missing_user"); err != nil {
		t.Fatalf("执行设置操作人失败: %v", err)
	}

	mu.Lock()
	snapshot := append([]permissionAlertMessage(nil), sentMessages...)
	mu.Unlock()
	if len(snapshot) == 0 {
		t.Fatalf("缺少找不到用户时的群内提示")
	}

	got := snapshot[len(snapshot)-1].text
	want := "没有缓存该用户，请直接回复对方消息后发送【设置操作人】或使用 @用户名 指定用户。"
	if got != want {
		t.Fatalf("找不到用户提示异常: want=%q got=%q", want, got)
	}
}

func TestDispatchGroupMessageShowLocalOperator(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "show-local-operator.db"))
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
		sentMessages []permissionAlertMessage
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
			sentMessages = append(sentMessages, permissionAlertMessage{
				chatID: chatID,
				text:   text,
			})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":33102}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "show-local-operator-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载时区失败: %v", err)
	}
	app := &App{
		db:  database,
		api: api,
		loc: loc,
		now: time.Now,
	}

	chatID := int64(-100331099)
	ownerID := int64(33109901)
	operatorID := int64(33109988)
	operatorUsername := "local_operator_331"

	if _, err := database.EnsureGroup(chatID, "显示本群操作人测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}
	if err := database.AddOperator(chatID, operatorID, operatorUsername, false); err != nil {
		t.Fatalf("设置本群操作人失败: %v", err)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	msg := &tgbotapi.Message{
		MessageID: 331099,
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Title: "显示本群操作人测试群",
		},
		From: &tgbotapi.User{
			ID:       ownerID,
			UserName: "owner_show_local",
		},
	}
	if err := app.dispatchGroupMessage(context.Background(), msg, group, "显示操作人"); err != nil {
		t.Fatalf("分发显示操作人命令失败: %v", err)
	}

	mu.Lock()
	snapshot := append([]permissionAlertMessage(nil), sentMessages...)
	mu.Unlock()

	foundLocalOperatorReply := false
	for _, item := range snapshot {
		if item.chatID != chatID {
			continue
		}
		if strings.Contains(item.text, "该身份暂无成员。") {
			t.Fatalf("显示操作人不应回落到身份分支: %q", item.text)
		}
		if strings.Contains(item.text, "本群操作人：") && strings.Contains(item.text, "@"+operatorUsername) {
			foundLocalOperatorReply = true
		}
	}
	if !foundLocalOperatorReply {
		t.Fatalf("缺少本群操作人回执: %#v", snapshot)
	}
}
