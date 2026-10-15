package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestBotInviteSendsTwoWelcomeMessages(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "bot-invite-welcome.db")

	const chatID int64 = -1004499001
	msg := &tgbotapi.Message{
		MessageID: 4499001,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "机器人入群欢迎测试群",
		},
		From: &tgbotapi.User{
			ID:       4499002,
			UserName: "inviter4499002",
		},
		NewChatMembers: []tgbotapi.User{
			{
				ID:        app.api.Self.ID,
				UserName:  app.api.Self.UserName,
				FirstName: "four_layer_test_bot",
			},
		},
	}
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("处理消息失败: %v", err)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, msg.MessageID, "感谢您把我添加到贵群！下一步设置费率，请发：设置费率xx") {
		t.Fatalf("缺少入群欢迎第一条: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, msg.MessageID, "欢迎加入【机器人入群欢迎测试群】") {
		t.Fatalf("缺少入群欢迎第二条: %+v", snapshot)
	}
	replies := 0
	for _, item := range snapshot {
		if item.chatID == chatID && item.replyTo == msg.MessageID {
			replies++
		}
	}
	if replies != 2 {
		t.Fatalf("机器人入群应发送两条欢迎消息: got=%d snapshot=%+v", replies, snapshot)
	}
}

func TestBotInviteViaMyChatMemberSendsTwoWelcomeMessages(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "bot-invite-my-chat-member-welcome.db")

	const chatID int64 = -1004499003
	upd := tgbotapi.Update{
		UpdateID: 4499003,
		MyChatMember: &tgbotapi.ChatMemberUpdated{
			Chat: tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "机器人入群欢迎测试群-成员状态",
			},
			OldChatMember: tgbotapi.ChatMember{
				Status: "left",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
			NewChatMember: tgbotapi.ChatMember{
				Status: "member",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
		},
	}
	app.dispatchUpdate(context.Background(), upd)

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 0, "感谢您把我添加到贵群！下一步设置费率，请发：设置费率xx") {
		t.Fatalf("缺少my_chat_member入群欢迎第一条: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 0, "欢迎加入【机器人入群欢迎测试群-成员状态】") {
		t.Fatalf("缺少my_chat_member入群欢迎第二条: %+v", snapshot)
	}
	replies := 0
	for _, item := range snapshot {
		if item.chatID == chatID {
			replies++
		}
	}
	if replies != 2 {
		t.Fatalf("my_chat_member入群应发送两条欢迎消息: got=%d snapshot=%+v", replies, snapshot)
	}
}

func TestBotInviteSendsRenewalMessageWhenEntitlementExpired(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "bot-invite-welcome-expired.db")

	const (
		chatID  int64 = -1004499012
		ownerID int64 = 4499012
	)
	if _, err := database.EnsureGroup(chatID, "机器人入群到期续费提醒测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	expiredAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := database.UpsertPrivateUserProfile(db.PrivateUserProfile{
		UserID:         ownerID,
		TrialStatus:    "expired",
		TrialAppliedAt: expiredAt.Add(-24 * time.Hour).Format(time.RFC3339),
		ExpireAt:       expiredAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("写入到期私聊权限失败: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 4499012,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "机器人入群到期续费提醒测试群",
		},
		From: &tgbotapi.User{
			ID:       4499013,
			UserName: "inviter4499013",
		},
		NewChatMembers: []tgbotapi.User{
			{
				ID:        app.api.Self.ID,
				UserName:  app.api.Self.UserName,
				FirstName: "four_layer_test_bot",
			},
		},
	}
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("处理消息失败: %v", err)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, msg.MessageID, "当前群功能已禁用：权限人使用权限已于") {
		t.Fatalf("机器人入群到期应提示续费文案: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, msg.MessageID, "感谢您把我添加到贵群！下一步设置费率，请发：设置费率xx") {
		t.Fatalf("机器人入群到期不应发送欢迎第一条: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, msg.MessageID, "欢迎加入【") {
		t.Fatalf("机器人入群到期不应发送欢迎第二条: %+v", snapshot)
	}
}

func TestBotInviteViaMyChatMemberSendsRenewalMessageWhenEntitlementExpired(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "bot-invite-my-chat-member-welcome-expired.db")

	const (
		chatID  int64 = -1004499014
		ownerID int64 = 4499014
	)
	if _, err := database.EnsureGroup(chatID, "机器人my_chat_member到期续费提醒测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	expiredAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := database.UpsertPrivateUserProfile(db.PrivateUserProfile{
		UserID:         ownerID,
		TrialStatus:    "expired",
		TrialAppliedAt: expiredAt.Add(-24 * time.Hour).Format(time.RFC3339),
		ExpireAt:       expiredAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("写入到期私聊权限失败: %v", err)
	}

	upd := tgbotapi.Update{
		UpdateID: 4499014,
		MyChatMember: &tgbotapi.ChatMemberUpdated{
			Chat: tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "机器人my_chat_member到期续费提醒测试群",
			},
			OldChatMember: tgbotapi.ChatMember{
				Status: "left",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
			NewChatMember: tgbotapi.ChatMember{
				Status: "member",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
		},
	}
	app.dispatchUpdate(context.Background(), upd)

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 0, "当前群功能已禁用：权限人使用权限已于") {
		t.Fatalf("my_chat_member入群到期应提示续费文案: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 0, "感谢您把我添加到贵群！下一步设置费率，请发：设置费率xx") {
		t.Fatalf("my_chat_member入群到期不应发送欢迎第一条: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 0, "欢迎加入【") {
		t.Fatalf("my_chat_member入群到期不应发送欢迎第二条: %+v", snapshot)
	}
	replies := 0
	for _, item := range snapshot {
		if item.chatID == chatID {
			replies++
		}
	}
	if replies != 1 {
		t.Fatalf("my_chat_member入群到期应仅发送1条续费提醒: got=%d snapshot=%+v", replies, snapshot)
	}
}

func TestBotInviteWelcomeDeduplicatedAcrossUpdateTypes(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "bot-invite-dedup.db")

	const chatID int64 = -1004499004
	msg := &tgbotapi.Message{
		MessageID: 4499004,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "机器人入群欢迎去重测试群",
		},
		From: &tgbotapi.User{
			ID:       4499010,
			UserName: "inviter4499010",
		},
		NewChatMembers: []tgbotapi.User{
			{
				ID:        app.api.Self.ID,
				UserName:  app.api.Self.UserName,
				FirstName: "four_layer_test_bot",
			},
		},
	}
	app.dispatchUpdate(context.Background(), tgbotapi.Update{
		UpdateID: 4499004,
		Message:  msg,
	})
	app.dispatchUpdate(context.Background(), tgbotapi.Update{
		UpdateID: 4499005,
		MyChatMember: &tgbotapi.ChatMemberUpdated{
			Chat: tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "机器人入群欢迎去重测试群",
			},
			OldChatMember: tgbotapi.ChatMember{
				Status: "left",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
			NewChatMember: tgbotapi.ChatMember{
				Status: "member",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
		},
	})

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, msg.MessageID, "感谢您把我添加到贵群！下一步设置费率，请发：设置费率xx") {
		t.Fatalf("缺少去重测试欢迎第一条: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, msg.MessageID, "欢迎加入【机器人入群欢迎去重测试群】") {
		t.Fatalf("缺少去重测试欢迎第二条: %+v", snapshot)
	}
	replies := 0
	for _, item := range snapshot {
		if item.chatID == chatID {
			replies++
		}
	}
	if replies != 2 {
		t.Fatalf("同一次入群跨update类型应只发送两条欢迎消息: got=%d snapshot=%+v", replies, snapshot)
	}
}

func TestBotKickedAndRejoinRefreshesBookkeepingStatus(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "bot-kicked-rejoin-status.db")

	const chatID int64 = -1004499005
	if _, err := database.EnsureGroup(chatID, "机器人退群回群状态刷新测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"bookkeeping_enabled":   1,
		"bookkeeping_cycle_key": "2026-02-24T20:00:00",
	}); err != nil {
		t.Fatalf("初始化记账状态失败: %v", err)
	}

	app.dispatchUpdate(context.Background(), tgbotapi.Update{
		UpdateID: 4499006,
		MyChatMember: &tgbotapi.ChatMemberUpdated{
			Chat: tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "机器人退群回群状态刷新测试群",
			},
			OldChatMember: tgbotapi.ChatMember{
				Status: "member",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
			NewChatMember: tgbotapi.ChatMember{
				Status: "kicked",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
		},
	})

	afterKicked, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取踢出后群配置失败: %v", err)
	}
	if afterKicked.BookkeepingEnabled != 0 {
		t.Fatalf("机器人被踢出后应自动停止记账: got=%d want=0", afterKicked.BookkeepingEnabled)
	}
	if afterKicked.BookkeepingCycleKey != "" {
		t.Fatalf("机器人被踢出后应清空记账周期: got=%q", afterKicked.BookkeepingCycleKey)
	}
	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("机器人被踢出不应发送欢迎消息: %+v", snapshot)
	}

	if err := database.UpdateGroupFields(chatID, map[string]any{
		"bookkeeping_enabled":   1,
		"bookkeeping_cycle_key": "2026-02-25T20:00:00",
	}); err != nil {
		t.Fatalf("模拟回群前旧状态失败: %v", err)
	}

	app.dispatchUpdate(context.Background(), tgbotapi.Update{
		UpdateID: 4499007,
		MyChatMember: &tgbotapi.ChatMemberUpdated{
			Chat: tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "机器人退群回群状态刷新测试群",
			},
			OldChatMember: tgbotapi.ChatMember{
				Status: "kicked",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
			NewChatMember: tgbotapi.ChatMember{
				Status: "member",
				User: &tgbotapi.User{
					ID:       app.api.Self.ID,
					UserName: app.api.Self.UserName,
				},
			},
		},
	})

	afterRejoin, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取回群后群配置失败: %v", err)
	}
	if afterRejoin.BookkeepingEnabled != 0 {
		t.Fatalf("机器人回群后应保持未开始记账状态: got=%d want=0", afterRejoin.BookkeepingEnabled)
	}
	if afterRejoin.BookkeepingCycleKey != "" {
		t.Fatalf("机器人回群后应清空记账周期: got=%q", afterRejoin.BookkeepingCycleKey)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 0, "感谢您把我添加到贵群！下一步设置费率，请发：设置费率xx") {
		t.Fatalf("回群后缺少欢迎第一条: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 0, "欢迎加入【机器人退群回群状态刷新测试群】") {
		t.Fatalf("回群后缺少欢迎第二条: %+v", snapshot)
	}
	replies := 0
	for _, item := range snapshot {
		if item.chatID == chatID {
			replies++
		}
	}
	if replies != 2 {
		t.Fatalf("回群后应仅发送两条欢迎消息: got=%d snapshot=%+v", replies, snapshot)
	}
}

func TestNonBotMemberJoinUsesGroupTitleWelcomeMessage(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-join-welcome.db")

	const chatID int64 = -1004499002
	if _, err := database.EnsureGroup(chatID, "普通成员入群欢迎测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"welcome_enabled": 1}); err != nil {
		t.Fatalf("开启进群欢迎失败: %v", err)
	}
	msg := &tgbotapi.Message{
		MessageID: 4499002,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "普通成员入群欢迎测试群",
		},
		From: &tgbotapi.User{
			ID:       4499003,
			UserName: "inviter4499003",
		},
		NewChatMembers: []tgbotapi.User{
			{
				ID:        4499004,
				UserName:  "member4499004",
				FirstName: "member4499004",
			},
		},
	}
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("处理消息失败: %v", err)
	}

	snapshot := capture.snapshot()
	expected := `<a href="tg://user?id=4499004">@member4499004</a> 欢迎加入【普通成员入群欢迎测试群】`
	reply, ok := fourLayerFindReply(snapshot, chatID, msg.MessageID, expected)
	if !ok {
		t.Fatalf("普通成员入群应走默认欢迎语: %+v", snapshot)
	}
	if reply.text != expected {
		t.Fatalf("普通成员入群欢迎语应按一行艾特格式发送: got %q want %q", reply.text, expected)
	}
	if strings.Contains(reply.text, "新成员：") || strings.Contains(reply.text, "\n") {
		t.Fatalf("普通成员入群欢迎语不应使用旧的换行新成员格式: %+v", reply)
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("普通成员入群欢迎语应使用 HTML parse mode: %+v", reply)
	}
}

func TestHumanMemberJoinDoesNotSendWelcomeWhenWelcomeDisabled(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "human-member-join-welcome-disabled.db")

	const chatID int64 = -1004499005
	if _, err := database.EnsureGroup(chatID, "成员入群关闭欢迎测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"welcome_enabled": 0,
		"welcome_text":    "不应发送的欢迎语",
	}); err != nil {
		t.Fatalf("关闭进群欢迎失败: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 4499005,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "成员入群关闭欢迎测试群",
		},
		From: &tgbotapi.User{
			ID:       4499006,
			UserName: "inviter4499006",
		},
		NewChatMembers: []tgbotapi.User{
			{
				ID:        4499007,
				UserName:  "member4499007",
				FirstName: "member4499007",
			},
			{
				ID:        4499008,
				UserName:  "bot4499008",
				FirstName: "bot4499008",
				IsBot:     true,
			},
		},
	}
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("处理消息失败: %v", err)
	}

	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("进群欢迎关闭后应保持静默: %+v", snapshot)
	}
}

func TestHumanMemberJoinWelcomeExcludesBotsWhenEnabled(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "human-member-join-welcome-excludes-bots.db")

	const chatID int64 = -1004499009
	if _, err := database.EnsureGroup(chatID, "成员入群排除机器人测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"welcome_enabled": 1}); err != nil {
		t.Fatalf("开启进群欢迎失败: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 4499009,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "成员入群排除机器人测试群",
		},
		From: &tgbotapi.User{
			ID:       4499010,
			UserName: "inviter4499010",
		},
		NewChatMembers: []tgbotapi.User{
			{
				ID:        4499011,
				UserName:  "human4499011",
				FirstName: "human4499011",
			},
			{
				ID:        4499012,
				UserName:  "bot4499012",
				FirstName: "bot4499012",
				IsBot:     true,
			},
		},
	}
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("处理消息失败: %v", err)
	}

	snapshot := capture.snapshot()
	expected := `<a href="tg://user?id=4499011">@human4499011</a> 欢迎加入【成员入群排除机器人测试群】`
	reply, ok := fourLayerFindReply(snapshot, chatID, msg.MessageID, expected)
	if !ok {
		t.Fatalf("开启进群欢迎后应欢迎真人成员: %+v", snapshot)
	}
	if strings.Contains(reply.text, "bot4499012") || strings.Contains(reply.text, "4499012") {
		t.Fatalf("成员欢迎不应包含机器人: %+v", reply)
	}
}

func TestNonBotMemberJoinViaChatMemberSendsWelcomeMessage(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-join-chat-member-welcome.db")

	const chatID int64 = -1004499021
	if _, err := database.EnsureGroup(chatID, "普通成员chat_member欢迎测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"welcome_enabled": 1}); err != nil {
		t.Fatalf("开启进群欢迎失败: %v", err)
	}
	app.dispatchUpdate(context.Background(), tgbotapi.Update{
		UpdateID: 4499021,
		ChatMember: &tgbotapi.ChatMemberUpdated{
			Chat: tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "普通成员chat_member欢迎测试群",
			},
			OldChatMember: tgbotapi.ChatMember{
				Status: "left",
				User: &tgbotapi.User{
					ID:        4499022,
					UserName:  "member4499022",
					FirstName: "member4499022",
				},
			},
			NewChatMember: tgbotapi.ChatMember{
				Status: "member",
				User: &tgbotapi.User{
					ID:        4499022,
					UserName:  "member4499022",
					FirstName: "member4499022",
				},
			},
		},
	})

	snapshot := capture.snapshot()
	expected := `<a href="tg://user?id=4499022">@member4499022</a> 欢迎加入【普通成员chat_member欢迎测试群】`
	reply, ok := fourLayerFindReply(snapshot, chatID, 0, expected)
	if !ok {
		t.Fatalf("普通成员chat_member入群应走默认欢迎语: %+v", snapshot)
	}
	if reply.text != expected {
		t.Fatalf("普通成员chat_member入群欢迎语应按一行艾特格式发送: got %q want %q", reply.text, expected)
	}
	if strings.Contains(reply.text, "新成员：") || strings.Contains(reply.text, "\n") {
		t.Fatalf("普通成员chat_member入群欢迎语不应使用旧的换行新成员格式: %+v", reply)
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("普通成员chat_member入群欢迎语应使用 HTML parse mode: %+v", reply)
	}
}

func TestNonBotMemberJoinWelcomeDeduplicatedAcrossUpdateTypes(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-join-dedup.db")

	const (
		chatID   int64 = -1004499023
		memberID int64 = 4499024
	)
	if _, err := database.EnsureGroup(chatID, "普通成员入群欢迎去重测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"welcome_enabled": 1}); err != nil {
		t.Fatalf("开启进群欢迎失败: %v", err)
	}
	msg := &tgbotapi.Message{
		MessageID: 4499023,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "普通成员入群欢迎去重测试群",
		},
		From: &tgbotapi.User{
			ID:       4499025,
			UserName: "inviter4499025",
		},
		NewChatMembers: []tgbotapi.User{
			{
				ID:        memberID,
				UserName:  "member4499024",
				FirstName: "member4499024",
			},
		},
	}
	app.dispatchUpdate(context.Background(), tgbotapi.Update{
		UpdateID: 4499023,
		Message:  msg,
	})
	app.dispatchUpdate(context.Background(), tgbotapi.Update{
		UpdateID: 4499024,
		ChatMember: &tgbotapi.ChatMemberUpdated{
			Chat: tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "普通成员入群欢迎去重测试群",
			},
			OldChatMember: tgbotapi.ChatMember{
				Status: "left",
				User: &tgbotapi.User{
					ID:        memberID,
					UserName:  "member4499024",
					FirstName: "member4499024",
				},
			},
			NewChatMember: tgbotapi.ChatMember{
				Status: "member",
				User: &tgbotapi.User{
					ID:        memberID,
					UserName:  "member4499024",
					FirstName: "member4499024",
				},
			},
		},
	})

	snapshot := capture.snapshot()
	replies := 0
	for _, item := range snapshot {
		if item.chatID == chatID && strings.Contains(item.text, "欢迎加入【普通成员入群欢迎去重测试群】") {
			replies++
		}
	}
	if replies != 1 {
		t.Fatalf("同一普通成员跨update类型入群应只发送一条欢迎消息: got=%d snapshot=%+v", replies, snapshot)
	}
}

func TestNonBotMemberJoinDoesNotSendWelcomeWhenEntitlementExpired(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-join-welcome-expired.db")

	const (
		chatID  int64 = -1004499011
		ownerID int64 = 4499011
	)
	if _, err := database.EnsureGroup(chatID, "普通成员入群到期静默测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"welcome_enabled": 1}); err != nil {
		t.Fatalf("开启进群欢迎失败: %v", err)
	}
	expiredAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := database.UpsertPrivateUserProfile(db.PrivateUserProfile{
		UserID:         ownerID,
		TrialStatus:    "expired",
		TrialAppliedAt: expiredAt.Add(-24 * time.Hour).Format(time.RFC3339),
		ExpireAt:       expiredAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("写入到期私聊权限失败: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 4499011,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "普通成员入群到期静默测试群",
		},
		From: &tgbotapi.User{
			ID:       4499012,
			UserName: "inviter4499012",
		},
		NewChatMembers: []tgbotapi.User{
			{
				ID:        4499013,
				UserName:  "member4499013",
				FirstName: "member4499013",
			},
		},
	}
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("处理消息失败: %v", err)
	}

	snapshot := capture.snapshot()
	if fourLayerHasReply(snapshot, chatID, msg.MessageID, "欢迎加入本群，请联系操作人了解群规则。") {
		t.Fatalf("群到期后普通成员入群不应发送欢迎语: %+v", snapshot)
	}
	if len(snapshot) != 0 {
		t.Fatalf("群到期后普通成员入群应保持静默: %+v", snapshot)
	}
}
