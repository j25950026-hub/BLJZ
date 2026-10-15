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

	"go-bot/internal/db"
)

func TestBuildUserProfileChangeAlertText(t *testing.T) {
	diff := db.GroupUserProfileDiff{
		OldUsername: "old_user",
		NewUsername: "new_user",
		OldNickname: "旧昵称",
		NewNickname: "新昵称",
	}
	got := buildUserProfileChangeAlertText(diff)
	want := strings.Join([]string{
		"👱 <b>用户修改用户名、昵称提示：</b>",
		"├原用户名：@old_user",
		"├新用户名：@new_user",
		"├原昵称：旧昵称",
		"├新昵称：新昵称",
		"",
		"请注意规避风险,谨防假冒管理员",
	}, "\n")
	if got != want {
		t.Fatalf("模板不匹配\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestBuildUserProfileChangeAlertTextFallbackAndEscape(t *testing.T) {
	diff := db.GroupUserProfileDiff{
		OldUsername: "",
		NewUsername: "@",
		OldNickname: "",
		NewNickname: "<Admin>",
	}
	got := buildUserProfileChangeAlertText(diff)
	want := strings.Join([]string{
		"👱 <b>用户修改用户名、昵称提示：</b>",
		"├原用户名：无",
		"├新用户名：无",
		"├原昵称：无",
		"├新昵称：&lt;Admin&gt;",
		"",
		"请注意规避风险,谨防假冒管理员",
	}, "\n")
	if got != want {
		t.Fatalf("回退或转义不正确\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestUserProfileChangeAlertMarkup(t *testing.T) {
	markup := userProfileChangeAlertMarkup(9527001)
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("按钮布局错误: %#v", markup.InlineKeyboard)
	}
	btn := markup.InlineKeyboard[0][0]
	if strings.TrimSpace(btn.Text) != "变更用户" {
		t.Fatalf("按钮文案错误: got=%q", btn.Text)
	}
	if btn.URL == nil || strings.TrimSpace(*btn.URL) != "tg://user?id=9527001" {
		got := ""
		if btn.URL != nil {
			got = *btn.URL
		}
		t.Fatalf("按钮链接错误: got=%q", got)
	}
}

type profileAlertSentMessage struct {
	ChatID      int64
	Text        string
	ParseMode   string
	ReplyMarkup string
	ReplyTo     string
}

func TestSyncGroupUserProfileSendsSecurityAlert(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "profile-alert.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	var (
		mu       sync.Mutex
		messages []profileAlertSentMessage
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
			messages = append(messages, profileAlertSentMessage{
				ChatID:      chatID,
				Text:        strings.TrimSpace(r.PostForm.Get("text")),
				ParseMode:   strings.TrimSpace(r.PostForm.Get("parse_mode")),
				ReplyMarkup: strings.TrimSpace(r.PostForm.Get("reply_markup")),
				ReplyTo:     strings.TrimSpace(r.PostForm.Get("reply_to_message_id")),
			})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":10001}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "profile-alert-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	app := &App{
		db:  database,
		api: api,
	}

	chatID := int64(-10099001)
	userID := int64(77889900)
	if err := app.syncGroupUserProfile(chatID, "group", 0, &tgbotapi.User{
		ID:        userID,
		UserName:  "old_user",
		FirstName: "Old",
		LastName:  "Name",
	}); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if err := app.syncGroupUserProfile(chatID, "group", 5566, &tgbotapi.User{
		ID:        userID,
		UserName:  "new_user",
		FirstName: "New",
		LastName:  "Name",
	}); err != nil {
		t.Fatalf("变更同步失败: %v", err)
	}

	mu.Lock()
	snapshot := append([]profileAlertSentMessage(nil), messages...)
	mu.Unlock()
	if len(snapshot) != 1 {
		t.Fatalf("提醒消息数量错误: want=1 got=%d, all=%#v", len(snapshot), snapshot)
	}

	got := snapshot[0]
	if got.ChatID != chatID {
		t.Fatalf("chat_id 错误: want=%d got=%d", chatID, got.ChatID)
	}
	wantText := strings.Join([]string{
		"👱 <b>用户修改用户名、昵称提示：</b>",
		"├原用户名：@old_user",
		"├新用户名：@new_user",
		"├原昵称：Old Name",
		"├新昵称：New Name",
		"",
		"请注意规避风险,谨防假冒管理员",
	}, "\n")
	if got.Text != wantText {
		t.Fatalf("提醒文案错误\nwant:\n%s\n\ngot:\n%s", wantText, got.Text)
	}
	if got.ParseMode != telegramParseModeHTML {
		t.Fatalf("ParseMode 错误: want=%q got=%q", telegramParseModeHTML, got.ParseMode)
	}
	if got.ReplyTo != "5566" {
		t.Fatalf("reply_to_message_id 错误: want=5566 got=%s", got.ReplyTo)
	}
	if !strings.Contains(got.ReplyMarkup, `"text":"变更用户"`) {
		t.Fatalf("缺少按钮文案: %s", got.ReplyMarkup)
	}
	if !strings.Contains(got.ReplyMarkup, `"url":"tg://user?id=77889900"`) {
		t.Fatalf("缺少用户 deep link: %s", got.ReplyMarkup)
	}
}
