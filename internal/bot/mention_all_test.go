package bot

import (
	"path/filepath"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestMentionAllSkipsBotsAndAnonymousServiceUsers(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "mention_all.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	})

	chatID := int64(-100668001)
	users := []struct {
		id       int64
		username string
		nickname string
	}{
		{id: 66800101, username: "a8668123", nickname: "A"},
		{id: 66800102, username: "bl66688", nickname: "B"},
		{id: 66800103, username: "bl66888_bot", nickname: "Business Bot"},
		{id: 1087968824, username: "groupanonymousbot", nickname: "Group Anonymous"},
	}
	for _, user := range users {
		if _, _, err := database.UpsertGroupUser(chatID, user.id, user.username, user.nickname); err != nil {
			t.Fatalf("写入群成员缓存失败: %v", err)
		}
	}

	api, capture := newFourLayerMockBotAPI(t)
	app := &App{db: database, api: api}
	msg := &tgbotapi.Message{
		MessageID: 668001,
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
	}

	if err := app.mentionAll(msg); err != nil {
		t.Fatalf("@所有人失败: %v", err)
	}

	snapshot := capture.snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("应只发送一条@所有人消息: %+v", snapshot)
	}
	reply := snapshot[0]
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("@所有人应使用 HTML parse mode: got=%q", reply.parseMode)
	}
	for _, want := range []string{"@a8668123", "@bl66688"} {
		if !strings.Contains(reply.text, want) {
			t.Fatalf("@所有人缺少真实用户 %q: %s", want, reply.text)
		}
	}
	for _, forbidden := range []string{"@bl66888_bot", "@groupanonymousbot"} {
		if strings.Contains(strings.ToLower(reply.text), forbidden) {
			t.Fatalf("@所有人不应包含 bot/匿名服务号 %q: %s", forbidden, reply.text)
		}
	}
}
