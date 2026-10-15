package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestMemberJoinWelcomeTemplateVariables(t *testing.T) {
	group := db.GroupConfig{
		Title:       "欢迎<运营>群",
		WelcomeText: "{user_mention}，欢迎加入 {group_name}；ID={user_id}；姓名={first_name}",
	}
	members := []tgbotapi.User{
		{ID: 7788, FirstName: "小明 & 同学"},
		{ID: 7799, UserName: "alice", FirstName: "Alice"},
	}

	got := buildMemberJoinWelcomeText(group, "事件群名", members...)
	want := `<a href="tg://user?id=7788">小明 &amp; 同学</a> <a href="tg://user?id=7799">@alice</a>，欢迎加入 欢迎&lt;运营&gt;群；ID=7788、7799；姓名=小明 &amp; 同学、Alice`
	if got != want {
		t.Fatalf("欢迎语变量渲染错误:\n got: %q\nwant: %q", got, want)
	}
}
