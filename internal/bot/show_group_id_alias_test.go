package bot

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestShowGroupIDAliasNotFallbackToIdentity(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "show-group-id-alias.db")
	chatID := int64(-1004410901)
	ownerID := int64(4410901)
	if _, err := database.EnsureGroup(chatID, "群ID别名测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(441901, 441901, chatID, ownerID, "owner_alias", "显示群id", baseDate))

	snapshot := capture.snapshot()
	want := fmt.Sprintf("当前群ID：<code>%d</code>", chatID)
	reply, ok := fourLayerFindReply(snapshot, chatID, 0, want)
	if !ok {
		t.Fatalf("显示群id 未返回群ID: %+v", snapshot)
	}
	if reply.replyTo != 0 {
		t.Fatalf("显示群id 不应回复用户消息，实际 replyTo=%d，消息=%+v", reply.replyTo, reply)
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("显示群id 应返回 HTML parse mode，实际为 %q，消息=%+v", reply.parseMode, reply)
	}
	if fourLayerHasReply(snapshot, chatID, 441901, want) {
		t.Fatalf("显示群id 不应以 reply 形式返回: %+v", snapshot)
	}
	for _, item := range snapshot {
		if item.chatID != chatID {
			continue
		}
		if item.text == "该身份暂无成员。" {
			t.Fatalf("显示群id 不应回落到身份分支: %+v", snapshot)
		}
	}
}

func TestShowGroupIDAliasAccessibleToMembers(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "show-group-id-member-access.db")
	chatID := int64(-1004410902)
	ownerID := int64(4410902)
	memberID := int64(4410999)
	if _, err := database.EnsureGroup(chatID, "群ID普通成员测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	cases := []struct {
		messageID int
		text      string
	}{
		{messageID: 441902, text: "显示群ID"},
		{messageID: 441903, text: "群id"},
	}

	for idx, tc := range cases {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			441902+idx,
			tc.messageID,
			chatID,
			memberID,
			"member_show_group_id",
			tc.text,
			baseDate+idx,
		))
	}

	snapshot := capture.snapshot()
	want := fmt.Sprintf("当前群ID：<code>%d</code>", chatID)
	matchCount := 0
	for _, item := range snapshot {
		if item.chatID != chatID || item.text != want {
			continue
		}
		if item.replyTo != 0 {
			t.Fatalf("群ID消息不应回复用户消息，实际 replyTo=%d，消息=%+v", item.replyTo, item)
		}
		if item.parseMode != telegramParseModeHTML {
			t.Fatalf("群ID消息应返回 HTML parse mode，实际为 %q，消息=%+v", item.parseMode, item)
		}
		matchCount++
	}
	if matchCount != len(cases) {
		t.Fatalf("普通成员发送别名后返回群ID次数不正确，want=%d got=%d snapshot=%+v", len(cases), matchCount, snapshot)
	}
	for _, tc := range cases {
		if fourLayerHasReply(snapshot, chatID, tc.messageID, want) {
			t.Fatalf("普通成员发送 %q 不应以 reply 形式返回: %+v", tc.text, snapshot)
		}
		if fourLayerHasReply(snapshot, chatID, tc.messageID, "权限不足：普通群员仅可使用计算器表达式。") {
			t.Fatalf("普通成员发送 %q 不应再被普通成员权限拦截: %+v", tc.text, snapshot)
		}
	}
}
