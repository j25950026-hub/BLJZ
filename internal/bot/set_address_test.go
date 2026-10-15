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
	"go-bot/internal/services"
)

func TestSetAddressWriteFailureReply(t *testing.T) {
	t.Run("写库失败返回明确回执", func(t *testing.T) {
		cases := []struct {
			name             string
			text             string
			kind             string
			expectReplyText  string
			expectReplyCount int
		}{
			{
				name:             "设置下发地址写库失败",
				text:             "设置下发地址 0x742d35Cc6634C0532925a3b844Bc454e4438f44e",
				kind:             "payout",
				expectReplyText:  "下发地址设置失败，请稍后重试。",
				expectReplyCount: 1,
			},
			{
				name:             "设置专属地址写库失败",
				text:             "设置专属地址 0x742d35Cc6634C0532925a3b844Bc454e4438f44e @alice 备用地址",
				kind:             "exclusive",
				expectReplyText:  "专属地址设置失败，请稍后重试。",
				expectReplyCount: 1,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				database, err := db.NewDatabase(filepath.Join(t.TempDir(), "set-address-write-failed.db"))
				if err != nil {
					t.Fatalf("初始化数据库失败: %v", err)
				}
				if err := database.Close(); err != nil {
					t.Fatalf("关闭数据库失败: %v", err)
				}

				var (
					sentMessages []capturedSendMessage
					mu           sync.Mutex
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
						sentMessages = append(sentMessages, capturedSendMessage{
							chatID: chatID,
							text:   text,
						})
						mu.Unlock()
						_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":95501}}`))
						return
					}
					_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
				}))
				defer server.Close()

				api := &tgbotapi.BotAPI{
					Token:  "set-address-write-failed-test-token",
					Client: server.Client(),
				}
				api.SetAPIEndpoint(server.URL + "/bot%s/%s")

				chatID := int64(-100955001)
				app := &App{
					db:  database,
					api: api,
				}
				msg := &tgbotapi.Message{
					MessageID: 955001,
					Chat: &tgbotapi.Chat{
						ID: chatID,
					},
				}

				if err := app.setAddress(msg, tc.text, tc.kind); err != nil {
					t.Fatalf("设置地址失败: %v", err)
				}

				mu.Lock()
				snapshot := append([]capturedSendMessage(nil), sentMessages...)
				mu.Unlock()

				replyCount := 0
				replyText := ""
				for _, item := range snapshot {
					if item.chatID != chatID {
						continue
					}
					replyCount++
					replyText = item.text
				}
				if replyCount != tc.expectReplyCount {
					t.Fatalf("回执数量异常: want=%d got=%d", tc.expectReplyCount, replyCount)
				}
				if !strings.Contains(replyText, tc.expectReplyText) {
					t.Fatalf("回执文本异常: want_contains=%q got=%q", tc.expectReplyText, replyText)
				}
			})
		}
	})
}

func TestSetAddressAcceptsDecoratedInput(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "set-address-decorated.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-100955101)
	if _, err := database.EnsureGroup(chatID, "地址设置测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}

	app := &App{
		db:      database,
		address: services.NewAddressService(time.Second),
	}
	msg := &tgbotapi.Message{
		MessageID: 955101,
		Chat: &tgbotapi.Chat{
			ID: chatID,
		},
	}

	text := "设置下发地址：0X0123456789abcdef0123456789ABCDEF01234567 （备用地址）"
	if err := app.setAddress(msg, text, "payout"); err != nil {
		t.Fatalf("设置下发地址失败: %v", err)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	want := "0x0123456789abcdef0123456789ABCDEF01234567"
	if group.PayoutAddress != want {
		t.Fatalf("下发地址写入异常: want=%q got=%q", want, group.PayoutAddress)
	}
	wantRemark := "（备用地址）"
	if group.PayoutAddressRemark != wantRemark {
		t.Fatalf("下发地址备注写入异常: want=%q got=%q", wantRemark, group.PayoutAddressRemark)
	}
}

func TestSetExclusiveAddressWithOwnerAndRemark(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "set-exclusive-address.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-100955201)
	if _, err := database.EnsureGroup(chatID, "专属地址设置测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}

	app := &App{
		db:      database,
		address: services.NewAddressService(time.Second),
	}
	msg := &tgbotapi.Message{
		MessageID: 955201,
		Chat: &tgbotapi.Chat{
			ID: chatID,
		},
	}

	text := "设置专属地址 0x742d35Cc6634C0532925a3b844Bc454e4438f44e @alice 备用地址"
	if err := app.setAddress(msg, text, "exclusive"); err != nil {
		t.Fatalf("设置专属地址失败: %v", err)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.ExclusiveAddress != "0x742d35Cc6634C0532925a3b844Bc454e4438f44e" {
		t.Fatalf("专属地址写入异常: got=%q", group.ExclusiveAddress)
	}
	if group.ExclusiveAddressOwner != "@alice" {
		t.Fatalf("专属地址所属人写入异常: got=%q", group.ExclusiveAddressOwner)
	}
	if group.ExclusiveAddressRemark != "备用地址" {
		t.Fatalf("专属地址备注写入异常: got=%q", group.ExclusiveAddressRemark)
	}
}

func TestParseCompactExclusiveAddressShortcut(t *testing.T) {
	const address = "TRMvQWPruApYCY29iKnvrZHYwsDktECc1t"
	cases := []struct {
		name       string
		text       string
		wantAddr   string
		wantOwner  string
		wantRemark string
		ok         bool
	}{
		{
			name:       "无空格快捷格式",
			text:       "恭喜发财地址" + address + "@a8668123靓仔无语",
			wantAddr:   address,
			wantOwner:  "@a8668123",
			wantRemark: "恭喜发财 靓仔无语",
			ok:         true,
		},
		{
			name:       "有空格快捷格式",
			text:       "恭喜发财地址 " + address + " @a8668123 靓仔无语",
			wantAddr:   address,
			wantOwner:  "@a8668123",
			wantRemark: "恭喜发财 靓仔无语",
			ok:         true,
		},
		{
			name: "缺少所属人",
			text: "恭喜发财地址 " + address + " 靓仔无语",
			ok:   false,
		},
		{
			name: "缺少后置备注",
			text: "恭喜发财地址 " + address + " @a8668123",
			ok:   false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			addr, owner, remark, ok := parseCompactExclusiveAddressShortcut(tc.text)
			if ok != tc.ok {
				t.Fatalf("解析状态异常: want=%v got=%v text=%q", tc.ok, ok, tc.text)
			}
			if addr != tc.wantAddr {
				t.Fatalf("解析地址异常: want=%q got=%q", tc.wantAddr, addr)
			}
			if owner != tc.wantOwner {
				t.Fatalf("解析所属人异常: want=%q got=%q", tc.wantOwner, owner)
			}
			if remark != tc.wantRemark {
				t.Fatalf("解析备注异常: want=%q got=%q", tc.wantRemark, remark)
			}
		})
	}
}

func TestHandleMessageCompactExclusiveAddressShortcut(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "compact-exclusive-address-shortcut.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100955301)
	ownerID := int64(955301)
	operatorID := int64(955302)
	const address = "TRMvQWPruApYCY29iKnvrZHYwsDktECc1t"

	if _, err := database.EnsureGroup(chatID, "快捷专属地址设置测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}
	if err := database.AddOperator(chatID, operatorID, "operator_shortcut", false); err != nil {
		t.Fatalf("设置操作人失败: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 955301,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "快捷专属地址设置测试群",
		},
		From: &tgbotapi.User{
			ID:        operatorID,
			UserName:  "operator_shortcut",
			FirstName: "operator_shortcut",
		},
		Text: "恭喜发财地址" + address + " @a8668123靓仔无语",
	}
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("处理快捷专属地址消息失败: %v", err)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, msg.MessageID, "恭喜发财地址设置成功。") {
		t.Fatalf("快捷专属地址未返回成功回执: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, msg.MessageID, "验证地址:") {
		t.Fatalf("快捷专属地址不应落入地址验证分支: %+v", snapshot)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.ExclusiveAddress != address {
		t.Fatalf("专属地址写入异常: want=%q got=%q", address, group.ExclusiveAddress)
	}
	if group.ExclusiveAddressOwner != "@a8668123" {
		t.Fatalf("专属地址所属人写入异常: want=%q got=%q", "@a8668123", group.ExclusiveAddressOwner)
	}
	if group.ExclusiveAddressRemark != "恭喜发财 靓仔无语" {
		t.Fatalf("专属地址备注写入异常: want=%q got=%q", "恭喜发财 靓仔无语", group.ExclusiveAddressRemark)
	}
}

func TestMemberCanQueryCompactExclusiveAddressByPrefixRemark(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "compact-exclusive-address-prefix-query.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100955401)
	ownerID := int64(955401)
	memberID := int64(955402)
	const address = "TRMvQWPruApYCY29iKnvrZHYwsDktECc1t"

	if _, err := database.EnsureGroup(chatID, "快捷专属地址前缀查询测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		955401, 955401, chatID, ownerID, "owner_shortcut",
		"恭喜发财地址"+address+" @Gong_Xi_Fa_Cai靓仔无语",
		baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		955402, 955402, chatID, memberID, "member_shortcut",
		"恭喜发财地址",
		baseDate+1,
	))

	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, chatID, 955402, "下发地址：<code>"+address+"</code>")
	if !ok {
		t.Fatalf("普通成员发送快捷关键词地址未返回图片验证格式地址详情: %+v", snapshot)
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("图片验证格式 ParseMode 异常: want=%q got=%q reply=%+v", telegramParseModeHTML, reply.parseMode, reply)
	}
	if !strings.Contains(reply.text, "验  证  码：") {
		t.Fatalf("普通成员发送快捷关键词地址未返回验证码: %+v", reply)
	}
	if !strings.Contains(reply.text, "验证时间：") {
		t.Fatalf("普通成员发送快捷关键词地址未返回验证时间: %+v", reply)
	}
	if !fourLayerHasReply(snapshot, chatID, 955402, "地址归属：@Gong_Xi_Fa_Cai") {
		t.Fatalf("普通成员发送快捷关键词地址未返回地址归属: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 955402, "备注信息：靓仔无语") {
		t.Fatalf("普通成员发送快捷关键词地址未返回去前缀后的备注信息: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 955402, "备注信息：恭喜发财 靓仔无语") {
		t.Fatalf("快捷关键词查询不应展示用于触发的前缀备注: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 955402, "点击上方地址，👆🏻即可复制地址") {
		t.Fatalf("普通成员发送快捷关键词地址未返回复制提示: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 955402, "温馨提示：避免损失，请转账方与收款方多人确认，四位一体，是否一致。") {
		t.Fatalf("普通成员发送快捷关键词地址未返回确认提示: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 955402, "图片中 ✅地址 ✅验证码 ✅验证时间 ✅文字地址") {
		t.Fatalf("普通成员发送快捷关键词地址未返回图片校验提示: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 955402, "权限不足") {
		t.Fatalf("普通成员发送快捷关键词地址不应被权限拦截: %+v", snapshot)
	}
}

func TestOwnerCanClearCompactExclusiveAddressByPrefixRemark(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "compact-exclusive-address-prefix-clear.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100955501)
	ownerID := int64(955501)
	memberID := int64(955502)
	const address = "TRMvQWPruApYCY29iKnvrZHYwsDktECc1t"

	if _, err := database.EnsureGroup(chatID, "快捷专属地址前缀清除测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		955501, 955501, chatID, ownerID, "owner_shortcut",
		"恭喜发财地址"+address+" @a8668123靓仔无语",
		baseDate,
	))

	rows, err := database.ListExclusiveAddresses(chatID)
	if err != nil {
		t.Fatalf("查询专属地址列表失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("快捷专属地址应写入一条记录: got=%d", len(rows))
	}
	if rows[0].Remark != "恭喜发财 靓仔无语" {
		t.Fatalf("快捷专属地址内部备注异常: want=%q got=%q", "恭喜发财 靓仔无语", rows[0].Remark)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		955502, 955502, chatID, ownerID, "owner_shortcut",
		"清除恭喜发财地址",
		baseDate+1,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		955503, 955503, chatID, memberID, "member_shortcut",
		"恭喜发财地址",
		baseDate+2,
	))

	rows, err = database.ListExclusiveAddresses(chatID)
	if err != nil {
		t.Fatalf("查询清除后的专属地址列表失败: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("按名称清除后专属地址列表应为空: got=%d", len(rows))
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 955502, "清除恭喜发财专属地址成功") {
		t.Fatalf("按快捷名称清除成功回执缺失: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 955503, "下发地址：<code>"+address+"</code>") {
		t.Fatalf("按快捷名称清除后不应再命中专属地址详情: %+v", snapshot)
	}
}

func TestSetExclusiveAddressInvalidFormatWillNotPersist(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "set-exclusive-address-invalid.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-100955202)
	if _, err := database.EnsureGroup(chatID, "专属地址非法格式测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}

	app := &App{
		db:      database,
		address: services.NewAddressService(time.Second),
	}
	msg := &tgbotapi.Message{
		MessageID: 955202,
		Chat: &tgbotapi.Chat{
			ID: chatID,
		},
	}

	text := "设置专属地址 0x742d35Cc6634C0532925a3b844Bc454e4438f44e 仅备注"
	if err := app.setAddress(msg, text, "exclusive"); err != nil {
		t.Fatalf("设置专属地址失败: %v", err)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if strings.TrimSpace(group.ExclusiveAddress) != "" || strings.TrimSpace(group.ExclusiveAddressOwner) != "" || strings.TrimSpace(group.ExclusiveAddressRemark) != "" {
		t.Fatalf("非法格式不应写入专属地址信息: %+v", group)
	}
}
