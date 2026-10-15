package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

type classModeCapture struct {
	mu              sync.Mutex
	permissionForms []url.Values
	messageTexts    []string
}

func (c *classModeCapture) appendPermission(form url.Values) {
	c.mu.Lock()
	c.permissionForms = append(c.permissionForms, cloneURLValues(form))
	c.mu.Unlock()
}

func (c *classModeCapture) appendMessage(text string) {
	c.mu.Lock()
	c.messageTexts = append(c.messageTexts, text)
	c.mu.Unlock()
}

func (c *classModeCapture) snapshot() ([]url.Values, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	forms := make([]url.Values, len(c.permissionForms))
	for i, form := range c.permissionForms {
		forms[i] = cloneURLValues(form)
	}
	texts := append([]string(nil), c.messageTexts...)
	return forms, texts
}

func cloneURLValues(in url.Values) url.Values {
	out := make(url.Values, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func newClassModeTestApp(t *testing.T) (*App, *classModeCapture) {
	t.Helper()

	capture := &classModeCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
			return
		}
		path := strings.TrimSpace(r.URL.Path)
		switch {
		case strings.HasSuffix(path, "/setChatPermissions"):
			capture.appendPermission(r.PostForm)
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			return
		case strings.HasSuffix(path, "/sendMessage"):
			capture.appendMessage(strings.TrimSpace(r.PostForm.Get("text")))
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9001}}`))
			return
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			return
		}
	}))
	t.Cleanup(server.Close)

	api := &tgbotapi.BotAPI{
		Token:  "class-mode-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")
	api.Self = tgbotapi.User{
		ID:       701000,
		UserName: "class_mode_test_bot",
	}

	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "class-mode-test.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	})

	return &App{api: api, db: database}, capture
}

func TestClassModeCommandMapping(t *testing.T) {
	for _, tc := range []struct {
		name        string
		command     string
		wantAllowed bool
		wantText    string
	}{
		{name: "上课允许发言", command: "上课", wantAllowed: true, wantText: "上课广告测试"},
		{name: "下课禁言", command: "下课", wantAllowed: false, wantText: "下课广告测试"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, capture := newClassModeTestApp(t)
			const (
				chatID  int64 = -100703
				ownerID int64 = 703001
			)
			if _, err := app.db.EnsureGroup(chatID, "上课下课命令测试群"); err != nil {
				t.Fatalf("初始化群失败: %v", err)
			}
			if err := app.db.SetOwner(chatID, ownerID); err != nil {
				t.Fatalf("设置权限人失败: %v", err)
			}
			if err := app.db.UpdateGroupFields(chatID, map[string]any{
				"class_start_ad": "上课广告测试",
				"class_end_ad":   "下课广告测试",
			}); err != nil {
				t.Fatalf("设置广告失败: %v", err)
			}

			msg := &tgbotapi.Message{
				MessageID: 703,
				Chat: &tgbotapi.Chat{
					ID:    chatID,
					Type:  "supergroup",
					Title: "上课下课命令测试群",
				},
				From: &tgbotapi.User{
					ID:        ownerID,
					UserName:  "owner703",
					FirstName: "owner703",
				},
				Text: tc.command,
			}
			if err := app.handleMessage(context.Background(), msg); err != nil {
				t.Fatalf("执行%s命令失败: %v", tc.command, err)
			}

			permissionForms, texts := capture.snapshot()
			if len(permissionForms) != 1 {
				t.Fatalf("setChatPermissions 调用次数错误: got=%d want=1", len(permissionForms))
			}
			var permissions map[string]bool
			if err := json.Unmarshal([]byte(permissionForms[0].Get("permissions")), &permissions); err != nil {
				t.Fatalf("解析 permissions 失败: %v", err)
			}
			if got := permissions["can_send_messages"]; got != tc.wantAllowed {
				t.Fatalf("%s can_send_messages 错误: got=%t want=%t raw=%s", tc.command, got, tc.wantAllowed, permissionForms[0].Get("permissions"))
			}
			if len(texts) != 1 || texts[0] != tc.wantText {
				t.Fatalf("%s 回执错误: got=%+v want=%q", tc.command, texts, tc.wantText)
			}
		})
	}
}

func TestClassModeSetsExplicitPermissions(t *testing.T) {
	t.Run("上课应显式发送 true 权限并触发可发言广告", func(t *testing.T) {
		app, capture := newClassModeTestApp(t)
		msg := &tgbotapi.Message{
			MessageID: 701,
			Chat: &tgbotapi.Chat{
				ID:   -100701,
				Type: "supergroup",
			},
		}
		group := db.GroupConfig{
			ClassStartAd: "上课广告测试",
			ClassEndAd:   "下课广告测试",
		}
		if err := app.handleClassMode(msg, group, false); err != nil {
			t.Fatalf("执行上课模式失败: %v", err)
		}

		permissionForms, texts := capture.snapshot()
		if len(permissionForms) != 1 {
			t.Fatalf("setChatPermissions 调用次数错误: got=%d want=1", len(permissionForms))
		}
		form := permissionForms[0]
		if got := strings.TrimSpace(form.Get("chat_id")); got != strconv.FormatInt(msg.Chat.ID, 10) {
			t.Fatalf("chat_id 参数错误: got=%s want=%d", got, msg.Chat.ID)
		}
		if got := strings.TrimSpace(form.Get("use_independent_chat_permissions")); got != "true" {
			t.Fatalf("独立权限开关应开启: got=%q", got)
		}

		expectedKeys := []string{
			"can_send_messages",
			"can_send_media_messages",
			"can_send_polls",
			"can_send_other_messages",
			"can_add_web_page_previews",
			"can_invite_users",
			"can_pin_messages",
			"can_change_info",
		}
		var permissions map[string]bool
		if err := json.Unmarshal([]byte(form.Get("permissions")), &permissions); err != nil {
			t.Fatalf("解析 permissions 失败: %v", err)
		}
		for _, key := range expectedKeys {
			value, ok := permissions[key]
			if !ok {
				t.Fatalf("permissions 缺少字段 %s: raw=%s", key, form.Get("permissions"))
			}
			if !value {
				t.Fatalf("上课模式字段 %s 应为 true，实际=false", key)
			}
		}

		if len(texts) != 1 || texts[0] != "上课广告测试" {
			t.Fatalf("上课广告回执错误: %+v", texts)
		}
	})

	t.Run("下课应显式发送 false 权限并触发禁言广告", func(t *testing.T) {
		app, capture := newClassModeTestApp(t)
		msg := &tgbotapi.Message{
			MessageID: 702,
			Chat: &tgbotapi.Chat{
				ID:   -100702,
				Type: "supergroup",
			},
		}
		group := db.GroupConfig{
			ClassStartAd: "上课广告测试",
			ClassEndAd:   "下课广告测试",
		}
		if err := app.handleClassMode(msg, group, true); err != nil {
			t.Fatalf("执行下课模式失败: %v", err)
		}

		permissionForms, texts := capture.snapshot()
		if len(permissionForms) != 1 {
			t.Fatalf("setChatPermissions 调用次数错误: got=%d want=1", len(permissionForms))
		}
		form := permissionForms[0]
		if got := strings.TrimSpace(form.Get("use_independent_chat_permissions")); got != "true" {
			t.Fatalf("独立权限开关应开启: got=%q", got)
		}

		expectedKeys := []string{
			"can_send_messages",
			"can_send_media_messages",
			"can_send_polls",
			"can_send_other_messages",
			"can_add_web_page_previews",
			"can_invite_users",
			"can_pin_messages",
			"can_change_info",
		}
		var permissions map[string]bool
		if err := json.Unmarshal([]byte(form.Get("permissions")), &permissions); err != nil {
			t.Fatalf("解析 permissions 失败: %v", err)
		}
		for _, key := range expectedKeys {
			value, ok := permissions[key]
			if !ok {
				t.Fatalf("permissions 缺少字段 %s: raw=%s", key, form.Get("permissions"))
			}
			if value {
				t.Fatalf("下课模式字段 %s 应为 false，实际=true", key)
			}
		}

		if len(texts) != 1 || texts[0] != "下课广告测试" {
			t.Fatalf("下课广告回执错误: %+v", texts)
		}
	})
}

func TestClassModeUsesCurrentDefaultsForLegacyAds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lock     bool
		wantText string
	}{
		{name: "上课旧默认广告应替换为可发言文案", lock: false, wantText: "上课时间到，可恢复正常发言。"},
		{name: "下课旧默认广告应替换为禁言文案", lock: true, wantText: "下课时间到，群聊已禁言。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, capture := newClassModeTestApp(t)
			msg := &tgbotapi.Message{
				MessageID: 704,
				Chat: &tgbotapi.Chat{
					ID:   -100704,
					Type: "supergroup",
				},
			}
			group := db.GroupConfig{
				ClassStartAd: "上课时间到，请专注工作，非必要信息暂缓发送。",
				ClassEndAd:   "下课时间到，可恢复正常交流。",
			}
			if err := app.handleClassMode(msg, group, tc.lock); err != nil {
				t.Fatalf("执行上课/下课模式失败: %v", err)
			}

			_, texts := capture.snapshot()
			if len(texts) != 1 || texts[0] != tc.wantText {
				t.Fatalf("旧默认广告兼容错误: got=%+v want=%q", texts, tc.wantText)
			}
		})
	}
}
