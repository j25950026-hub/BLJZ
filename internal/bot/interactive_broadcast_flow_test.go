package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestInteractiveBroadcastTagFlowRepeatAndClose(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "interactive-broadcast-flow.db")
	sourceChatID := int64(-100661001)
	targetChatID := int64(-100661002)
	ownerID := int64(661001)
	memberID := int64(661099)

	if _, err := database.EnsureGroup(sourceChatID, "群发源群"); err != nil {
		t.Fatalf("初始化来源群失败: %v", err)
	}
	if _, err := database.EnsureGroup(targetChatID, "群发目标群"); err != nil {
		t.Fatalf("初始化目标群失败: %v", err)
	}
	if err := database.SetOwner(sourceChatID, ownerID); err != nil {
		t.Fatalf("设置来源群权限人失败: %v", err)
	}
	if err := database.UpdateGroupFields(sourceChatID, map[string]any{"group_tags": "业务A"}); err != nil {
		t.Fatalf("设置来源群标签失败: %v", err)
	}
	if err := database.UpdateGroupFields(targetChatID, map[string]any{"group_tags": "业务A"}); err != nil {
		t.Fatalf("设置目标群标签失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		661101, 661101, sourceChatID, ownerID, "owner_interactive", "开启群发功能", baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		661102, 661102, sourceChatID, ownerID, "owner_interactive", "今晚八点统一通知", baseDate+1,
	))

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, sourceChatID, 661101, "开始成功，请输入群发内容。") {
		t.Fatalf("开启群发提示缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 661102, "序号对应标签") {
		t.Fatalf("标签选择提示缺失: %+v", snapshot)
	}

	callback := &tgbotapi.CallbackQuery{
		ID:   "interactive-bt-1",
		Data: buildInteractiveBroadcastTagCallbackData(0),
		From: &tgbotapi.User{
			ID:       memberID,
			UserName: "member_clicker",
		},
		Message: &tgbotapi.Message{
			MessageID: 900001,
			Chat: &tgbotapi.Chat{
				ID:    sourceChatID,
				Type:  "supergroup",
				Title: "群发源群",
			},
		},
	}
	if err := app.handleCallbackQuery(context.Background(), callback); err != nil {
		t.Fatalf("首次标签点击失败: %v", err)
	}

	firstSent := waitForExactBroadcastTextCount(t, capture, "今晚八点统一通知", 2)
	if firstSent != 2 {
		t.Fatalf("首次点击后应发送到2个群，实际=%d", firstSent)
	}

	if err := app.handleCallbackQuery(context.Background(), callback); err != nil {
		t.Fatalf("重复标签点击失败: %v", err)
	}
	secondSent := waitForExactBroadcastTextCount(t, capture, "今晚八点统一通知", 4)
	if secondSent != 4 {
		t.Fatalf("重复点击后应累计发送4次，实际=%d", secondSent)
	}

	afterRepeat := waitForReplyContaining(t, capture, sourceChatID, 900001, "标签群发执行结果：")
	if !containsAnyText(afterRepeat, "标签群发执行结果：") {
		t.Fatalf("缺少标签群发结果反馈: %+v", afterRepeat)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		661103, 661103, sourceChatID, ownerID, "owner_interactive", "关闭群发信息", baseDate+2,
	))
	if err := app.handleCallbackQuery(context.Background(), callback); err != nil {
		t.Fatalf("关闭后点击标签不应报错: %v", err)
	}
	afterCloseSent := countExactBroadcastText(capture.snapshot(), "今晚八点统一通知")
	if afterCloseSent != 4 {
		t.Fatalf("关闭后点击标签不应重发，期望=4 实际=%d", afterCloseSent)
	}
}

func TestInteractiveBroadcastTagFlowIncludesGroupTypeTargets(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "interactive-broadcast-group-type.db")
	sourceChatID := int64(-100661201)
	targetChatID := int64(-100661202)
	otherChatID := int64(-100661203)
	ownerID := int64(661201)
	memberID := int64(661299)

	for _, item := range []struct {
		chatID    int64
		title     string
		groupType string
		groupTags string
	}{
		{sourceChatID, "新加坡源群", "新加坡", ""},
		{targetChatID, "新加坡目标群", "新加坡", ""},
		{otherChatID, "无关CF群", "", "CF"},
	} {
		if _, err := database.EnsureGroup(item.chatID, item.title); err != nil {
			t.Fatalf("初始化群失败(chat=%d): %v", item.chatID, err)
		}
		if err := database.UpdateGroupFields(item.chatID, map[string]any{
			"group_type": item.groupType,
			"group_tags": item.groupTags,
		}); err != nil {
			t.Fatalf("设置群类型/标签失败(chat=%d): %v", item.chatID, err)
		}
	}
	if err := database.SetOwner(sourceChatID, ownerID); err != nil {
		t.Fatalf("设置来源群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		661201, 661201, sourceChatID, ownerID, "owner_interactive", "开启群发功能", baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		661202, 661202, sourceChatID, ownerID, "owner_interactive", "群类型群发测试", baseDate+1,
	))

	session, ok := app.getInteractiveBroadcastSession(sourceChatID)
	if !ok {
		t.Fatalf("群发会话应存在")
	}
	typeIndex := -1
	for i, tag := range session.Tags {
		if tag == "新加坡" {
			typeIndex = i
			break
		}
	}
	if typeIndex < 0 {
		t.Fatalf("群发按钮应包含群类型: tags=%+v snapshot=%+v", session.Tags, capture.snapshot())
	}

	callback := &tgbotapi.CallbackQuery{
		ID:   "interactive-bt-group-type",
		Data: buildInteractiveBroadcastTagCallbackData(typeIndex),
		From: &tgbotapi.User{
			ID:       memberID,
			UserName: "member_clicker",
		},
		Message: &tgbotapi.Message{
			MessageID: 900301,
			Chat: &tgbotapi.Chat{
				ID:    sourceChatID,
				Type:  "supergroup",
				Title: "新加坡源群",
			},
		},
	}
	if err := app.handleCallbackQuery(context.Background(), callback); err != nil {
		t.Fatalf("群类型标签点击失败: %v", err)
	}

	sent := waitForExactBroadcastTextCount(t, capture, "群类型群发测试", 2)
	if sent != 2 {
		t.Fatalf("群类型群发应只发送到同类型2个群，实际=%d snapshot=%+v", sent, capture.snapshot())
	}
}

func TestInteractiveBroadcastTagCallbackSubmitsBeforeBroadcastCompletes(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "interactive-broadcast-async-submit.db")
	sourceChatID := int64(-100661401)
	targetChatID := int64(-100661402)
	ownerID := int64(661401)
	memberID := int64(661499)

	for _, item := range []struct {
		chatID int64
		title  string
	}{
		{sourceChatID, "异步群发源群"},
		{targetChatID, "异步群发目标群"},
	} {
		if _, err := database.EnsureGroup(item.chatID, item.title); err != nil {
			t.Fatalf("初始化群失败(chat=%d): %v", item.chatID, err)
		}
		if err := database.UpdateGroupFields(item.chatID, map[string]any{"group_tags": "业务A"}); err != nil {
			t.Fatalf("设置群标签失败(chat=%d): %v", item.chatID, err)
		}
	}
	if err := database.SetOwner(sourceChatID, ownerID); err != nil {
		t.Fatalf("设置来源群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		661401, 661401, sourceChatID, ownerID, "owner_interactive", "开启群发功能", baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		661402, 661402, sourceChatID, ownerID, "owner_interactive", "异步群发测试", baseDate+1,
	))

	sourceBlock := &blockingSend{entered: make(chan struct{}), release: make(chan struct{})}
	targetBlock := &blockingSend{entered: make(chan struct{}), release: make(chan struct{})}
	capture := installBlockingSendBotAPI(t, app, map[int64]*blockingSend{
		sourceChatID: sourceBlock,
		targetChatID: targetBlock,
	})
	callback := &tgbotapi.CallbackQuery{
		ID:   "interactive-bt-async",
		Data: buildInteractiveBroadcastTagCallbackData(0),
		From: &tgbotapi.User{
			ID:       memberID,
			UserName: "member_clicker",
		},
		Message: &tgbotapi.Message{
			MessageID: 900401,
			Chat: &tgbotapi.Chat{
				ID:    sourceChatID,
				Type:  "supergroup",
				Title: "异步群发源群",
			},
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- app.handleCallbackQuery(context.Background(), callback)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("异步群发点击不应返回错误: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		releaseBlockingSend(sourceBlock)
		releaseBlockingSend(targetBlock)
		t.Fatalf("点击群发按钮不应等待目标群发送完成")
	}
	if containsAnyText(capture.snapshot(), "标签群发执行结果：") {
		t.Fatalf("目标群发送未完成前不应出现最终回执: %+v", capture.snapshot())
	}
	select {
	case <-sourceBlock.entered:
	case <-targetBlock.entered:
	case <-time.After(time.Second):
		t.Fatalf("后台群发任务未开始")
	}
	releaseBlockingSend(sourceBlock)
	releaseBlockingSend(targetBlock)

	deadline := time.After(2 * time.Second)
	for {
		if containsAnyText(capture.snapshot(), "标签群发执行结果：") {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("后台群发完成后缺少最终回执: %+v", capture.snapshot())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type blockingSend struct {
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func releaseBlockingSend(block *blockingSend) {
	if block == nil {
		return
	}
	block.releaseOnce.Do(func() { close(block.release) })
}

func installBlockingSendBotAPI(t *testing.T, app *App, blocks map[int64]*blockingSend) *fourLayerCapture {
	t.Helper()
	capture := &fourLayerCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimSpace(r.URL.Path)
		if strings.HasSuffix(path, "/sendMessage") {
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
				return
			}
			chatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("chat_id")), 10, 64)
			if block := blocks[chatID]; block != nil {
				block.enteredOnce.Do(func() { close(block.entered) })
				<-block.release
			}
			replyTo, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("reply_to_message_id")))
			capture.append(fourLayerCapturedMessage{
				chatID:  chatID,
				replyTo: replyTo,
				text:    strings.TrimSpace(r.PostForm.Get("text")),
			})
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":440401}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	t.Cleanup(server.Close)

	app.api = &tgbotapi.BotAPI{
		Token:  "four-layer-test-token",
		Client: server.Client(),
		Self: tgbotapi.User{
			ID:       440000,
			UserName: "four_layer_test_bot",
		},
	}
	app.api.SetAPIEndpoint(server.URL + "/bot%s/%s")
	return capture
}

func countExactBroadcastText(snapshot []fourLayerCapturedMessage, target string) int {
	count := 0
	for _, item := range snapshot {
		if strings.TrimSpace(item.text) == strings.TrimSpace(target) {
			count++
		}
	}
	return count
}

func waitForExactBroadcastTextCount(t *testing.T, capture *fourLayerCapture, target string, want int) int {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		got := countExactBroadcastText(capture.snapshot(), target)
		if got == want {
			return got
		}
		select {
		case <-deadline:
			return got
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForReplyContaining(t *testing.T, capture *fourLayerCapture, chatID int64, replyTo int, keyword string) []fourLayerCapturedMessage {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		snapshot := capture.snapshot()
		if fourLayerHasReply(snapshot, chatID, replyTo, keyword) {
			return snapshot
		}
		select {
		case <-deadline:
			return snapshot
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func containsAnyText(snapshot []fourLayerCapturedMessage, keyword string) bool {
	for _, item := range snapshot {
		if strings.Contains(item.text, keyword) {
			return true
		}
	}
	return false
}
