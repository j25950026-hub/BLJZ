package bot

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "modernc.org/sqlite"
)

func TestControlPoint322VelocityCheck(t *testing.T) {
	t.Run("控制点 3.2.2 功能验证", func(t *testing.T) {
		checker := newUserCommandVelocityChecker(time.Second, 3)
		base := time.Date(2026, 2, 19, 9, 0, 0, 0, time.UTC)

		for i := 0; i < 3; i++ {
			allowed, count := checker.allowAt(10001, base.Add(200*time.Millisecond))
			if !allowed {
				t.Fatalf("阈值内请求不应被拦截: idx=%d count=%d", i, count)
			}
		}

		allowed, count := checker.allowAt(10001, base.Add(900*time.Millisecond))
		if allowed {
			t.Fatalf("同一秒内第4次请求应被拦截")
		}
		if count != 4 {
			t.Fatalf("拦截计数异常: want=4 got=%d", count)
		}

		allowed, count = checker.allowAt(10001, base.Add(1100*time.Millisecond))
		if !allowed {
			t.Fatalf("进入下一秒后应恢复放行: count=%d", count)
		}
	})
}

func TestVelocityCheckIsolatedByUser(t *testing.T) {
	checker := newUserCommandVelocityChecker(time.Second, 2)
	base := time.Date(2026, 2, 19, 9, 0, 0, 0, time.UTC)

	_, _ = checker.allowAt(10001, base)
	_, _ = checker.allowAt(10001, base.Add(100*time.Millisecond))
	allowed, _ := checker.allowAt(10001, base.Add(200*time.Millisecond))
	if allowed {
		t.Fatalf("同用户超过阈值后应被拦截")
	}

	allowed, count := checker.allowAt(20002, base.Add(300*time.Millisecond))
	if !allowed {
		t.Fatalf("不同用户不应共享限流计数: count=%d", count)
	}
}

func TestGuardCommandVelocity(t *testing.T) {
	base := time.Date(2026, 2, 19, 9, 0, 0, 0, time.UTC)
	now := base
	checker := newUserCommandVelocityChecker(time.Second, 1)
	checker.now = func() time.Time { return now }

	app := &App{commandVelocity: checker}
	msg := &tgbotapi.Message{
		MessageID: 7001,
		Chat:      &tgbotapi.Chat{ID: -10033445566},
		From:      &tgbotapi.User{ID: 889900},
	}

	if !app.guardCommandVelocity(msg, "菜单") {
		t.Fatalf("首条指令不应触发拦截")
	}
	if app.guardCommandVelocity(msg, "菜单") {
		t.Fatalf("同一秒内超过阈值应触发拦截")
	}

	now = base.Add(1200 * time.Millisecond)
	if !app.guardCommandVelocity(msg, "菜单") {
		t.Fatalf("新时间窗口内应恢复放行")
	}
}

func TestGroupPlainTextBroadcastDoesNotTriggerVelocityLimit(t *testing.T) {
	app, database, capture, dbPath := newFourLayerTestAppWithDBPath(t, "group-plain-text-velocity-scope.db")
	base := time.Date(2026, 8, 2, 11, 5, 3, 0, time.FixedZone("CST", 8*3600))
	app.commandVelocity = newUserCommandVelocityChecker(time.Second, 5)
	app.commandVelocity.now = func() time.Time { return base }

	const userID int64 = 6528504333
	const text = "🔛🔛🥹🥹📝📝🀄️📣📣\n\n🀄️🀄️🀄️印度源头直通车🫵🫵\n\n🀄️🀄️🀄️印度一类代收付白资代付🎃\n\n🀄️🀄️🀄️香港源头直通车🎃"
	chatIDs := []int64{
		-1002260253307,
		-1003514822119,
		-1003922958137,
		-4833525610,
		-4609379708,
		-5024599581,
	}

	for index, chatID := range chatIDs {
		if _, err := database.EnsureGroup(chatID, "普通文本限速范围测试群"); err != nil {
			t.Fatalf("初始化群 %d 失败: %v", chatID, err)
		}
		if err := database.SetOwner(chatID, userID); err != nil {
			t.Fatalf("设置群 %d 权限人失败: %v", chatID, err)
		}
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			243723850+index,
			5765+index,
			chatID,
			userID,
			"velocity_plain_text",
			text,
			int(base.Unix()),
		))
	}

	for _, item := range capture.snapshot() {
		if strings.Contains(item.text, "检测到指令发送频率过高") {
			t.Fatalf("普通文本跨群广播不应触发高频指令限速: %+v", capture.snapshot())
		}
	}
	if count := queryVelocityRiskEventCount(t, dbPath, userID); count != 0 {
		t.Fatalf("普通文本跨群广播不应写入 velocity_check_blocked: got=%d", count)
	}
}

func TestGroupCommandBroadcastStillTriggersVelocityLimit(t *testing.T) {
	app, database, capture, dbPath := newFourLayerTestAppWithDBPath(t, "group-command-velocity-scope.db")
	base := time.Date(2026, 8, 2, 11, 5, 3, 0, time.FixedZone("CST", 8*3600))
	app.commandVelocity = newUserCommandVelocityChecker(time.Second, 5)
	app.commandVelocity.now = func() time.Time { return base }

	const userID int64 = 6528504333
	chatIDs := []int64{
		-1002260253307,
		-1003514822119,
		-1003922958137,
		-4833525610,
		-4609379708,
		-5024599581,
	}

	for index, chatID := range chatIDs {
		if _, err := database.EnsureGroup(chatID, "命令限速范围测试群"); err != nil {
			t.Fatalf("初始化群 %d 失败: %v", chatID, err)
		}
		if err := database.SetOwner(chatID, userID); err != nil {
			t.Fatalf("设置群 %d 权限人失败: %v", chatID, err)
		}
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			243723950+index,
			6765+index,
			chatID,
			userID,
			"velocity_command",
			"菜单",
			int(base.Unix()),
		))
	}

	snapshot := capture.snapshot()
	for index := 0; index < len(chatIDs)-1; index++ {
		if fourLayerHasReply(snapshot, chatIDs[index], 6765+index, "检测到指令发送频率过高") {
			t.Fatalf("真正命令在第 %d 条时不应提前触发限速: %+v", index+1, snapshot)
		}
	}
	lastIndex := len(chatIDs) - 1
	if !fourLayerHasReply(snapshot, chatIDs[lastIndex], 6765+lastIndex, "检测到指令发送频率过高") {
		t.Fatalf("真正命令第 6 条应触发限速: %+v", snapshot)
	}
	if count := queryVelocityRiskEventCount(t, dbPath, userID); count != 1 {
		t.Fatalf("真正命令第 6 条应写入 1 条 velocity_check_blocked: got=%d", count)
	}
}

func queryVelocityRiskEventCount(t *testing.T, dbPath string, userID int64) int {
	t.Helper()

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开限速审计数据库失败: %v", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Fatalf("关闭限速审计查询连接失败: %v", closeErr)
		}
	}()

	var count int
	if err := conn.QueryRow(
		`SELECT COUNT(1) FROM risk_events WHERE user_id = ? AND event_type = ?`,
		userID,
		"velocity_check_blocked",
	).Scan(&count); err != nil {
		t.Fatalf("查询限速审计事件失败: %v", err)
	}
	return count
}

func TestShouldApplyCommandVelocityLimit(t *testing.T) {
	tests := []struct {
		name string
		msg  *tgbotapi.Message
		text string
		want bool
	}{
		{
			name: "telegram 斜杠命令",
			msg: &tgbotapi.Message{
				Text: "/menu",
				Entities: []tgbotapi.MessageEntity{
					{Type: "bot_command", Offset: 0, Length: 5},
				},
			},
			text: "/menu",
			want: true,
		},
		{name: "菜单口令", msg: &tgbotapi.Message{}, text: "菜单", want: true},
		{name: "账单口令", msg: &tgbotapi.Message{}, text: "一键账单", want: true},
		{name: "设置汇率", msg: &tgbotapi.Message{}, text: "设置汇率7.25", want: true},
		{name: "记账格式", msg: &tgbotapi.Message{}, text: "+100#备注", want: true},
		{name: "世界时间查询", msg: &tgbotapi.Message{}, text: "北京时间", want: true},
		{name: "普通广告文案", msg: &tgbotapi.Message{}, text: "印度源头直通车今晚通车，详情私聊。", want: false},
		{name: "emoji 广告文案", msg: &tgbotapi.Message{}, text: "🔛🔛🥹🥹📝📝🀄️📣📣", want: false},
		{name: "普通聊天句子", msg: &tgbotapi.Message{}, text: "一会通车了给你说老板", want: false},
		{name: "包含开始但不是命令", msg: &tgbotapi.Message{}, text: "开始接单了", want: false},
		{name: "包含关闭但不是命令", msg: &tgbotapi.Message{}, text: "关闭一下话题", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldApplyCommandVelocityLimit(tc.msg, tc.text); got != tc.want {
				t.Fatalf("判定异常: want=%t got=%t text=%q", tc.want, got, tc.text)
			}
		})
	}
}
