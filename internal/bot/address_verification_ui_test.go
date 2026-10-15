package bot

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
	"go-bot/internal/services"
)

type addressVerificationTimingHTTPClient struct {
	base    tgbotapi.HTTPClient
	delay   time.Duration
	sendErr error
}

func (c *addressVerificationTimingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/sendPhoto") {
		if c.delay > 0 {
			time.Sleep(c.delay)
		}
		if c.sendErr != nil {
			return nil, c.sendErr
		}
	}
	return c.base.Do(req)
}

func captureAddressVerificationTimingLog(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	previousWriter := log.Writer()
	var buffer bytes.Buffer
	log.SetOutput(&buffer)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
	})
	err := fn()
	return buffer.String(), err
}

func installAddressVerificationTimingTransport(t *testing.T, app *App, delay time.Duration, sendErr error) {
	t.Helper()
	if app == nil || app.api == nil || app.api.Client == nil {
		t.Fatal("测试应用缺少 Telegram HTTP 客户端")
	}
	previousClient := app.api.Client
	app.api.Client = &addressVerificationTimingHTTPClient{
		base:    previousClient,
		delay:   delay,
		sendErr: sendErr,
	}
	t.Cleanup(func() {
		app.api.Client = previousClient
	})
}

func addressVerificationTimingLogLine(t *testing.T, output string) string {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "地址验证耗时:") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("地址验证耗时日志应恰好一条，实际=%d output=%q", len(lines), output)
	}
	return lines[0]
}

func addressVerificationTimingField(t *testing.T, line, key string) int64 {
	t.Helper()
	prefix := key + "="
	for _, field := range strings.Fields(line) {
		if strings.HasPrefix(field, prefix) {
			value, err := strconv.ParseInt(strings.TrimPrefix(field, prefix), 10, 64)
			if err != nil {
				t.Fatalf("字段 %q 不是整数: %q: %v", key, field, err)
			}
			return value
		}
	}
	t.Fatalf("耗时日志缺少字段 %q: %s", key, line)
	return 0
}

func TestBuildAddressVerificationCaptionTemplate(t *testing.T) {
	caption := buildAddressVerificationCaption(
		"TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq",
		"4821",
		"2026-02-21 12:34:56",
		3,
		`<a href="tg://user?id=101">@alice</a>`,
		`<a href="tg://user?id=202">@bob</a>`,
	)
	expected := "验证地址：<code>TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq</code>\n" +
		"验  证  码：4821\n" +
		"验证时间：2026-02-21 12:34:56\n" +
		"验证次数：3\n\n" +
		"上次发送人：<a href=\"tg://user?id=101\">@alice</a>\n" +
		"本次发送人：<a href=\"tg://user?id=202\">@bob</a>\n\n" +
		"温馨提示：避免损失，请转账方与收款方多人确认，四位一体，是否一致。\n" +
		"图片中 ✅地址 ✅验证码 ✅验证时间 ✅文字地址 \n\n" +
		"点击上方地址，👆🏻即可复制地址"
	if caption != expected {
		t.Fatalf("caption 模板不符合预期\nwant:\n%s\n\ngot:\n%s", expected, caption)
	}
}

func TestBuildAddressVerificationCaptionAddsFirstVerificationWarning(t *testing.T) {
	caption := buildAddressVerificationCaption(
		"TGR7YWM5sTL4c27KxWnDrHrFeUXMhygduS",
		"7752",
		"2026-07-12 14:46:46",
		1,
		"无",
		"@a8668123",
	)
	expected := "验证地址：<code>TGR7YWM5sTL4c27KxWnDrHrFeUXMhygduS</code>\n" +
		"验  证  码：7752\n" +
		"验证时间：2026-07-12 14:46:46\n" +
		"验证次数：1\n\n" +
		"上次发送人：无\n" +
		"本次发送人：@a8668123\n" +
		"‼️此地址第一次验证，请核实后进行转账。\n" +
		"‼️此地址第一次验证，请核实后进行转账。\n" +
		"‼️此地址第一次验证，请核实后进行转账。\n\n" +
		"温馨提示：避免损失，请转账方与收款方多人确认，四位一体，是否一致。\n" +
		"图片中 ✅地址 ✅验证码 ✅验证时间 ✅文字地址 \n\n" +
		"点击上方地址，👆🏻即可复制地址"
	if caption != expected {
		t.Fatalf("第一次验证 caption 模板不符合预期\nwant:\n%s\n\ngot:\n%s", expected, caption)
	}
}

func TestBuildExclusiveAddressVerificationCaptionTemplate(t *testing.T) {
	caption := buildExclusiveAddressVerificationCaption(
		"TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B",
		97,
		"@未知",
		`<a href="tg://user?id=202">@bob</a>`,
		"@Gong_Xi_Fa_Cai |祝万事胜意",
		"靓仔无语1权威认证",
		"TRC20",
	)
	expected := "【专属验证】专属地址验证通过\n" +
		"验证地址: <code>TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B</code>\n" +
		"验证次数: 97\n" +
		"上次发送人: @未知\n" +
		"本次发送人: <a href=\"tg://user?id=202\">@bob</a>\n" +
		"地址归属: @Gong_Xi_Fa_Cai |祝万事胜意\n" +
		"备注信息: 靓仔无语1权威认证\n" +
		"网络: TRC20\n" +
		"点击上方地址即可复制地址"
	if caption != expected {
		t.Fatalf("专属 caption 模板不符合预期\nwant:\n%s\n\ngot:\n%s", expected, caption)
	}
}

func TestPlainAddressVerificationUsesExclusiveCaptionWhenExclusiveAddressExists(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "plain-address-verification-exclusive-caption.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100746461)
	memberID := int64(746462)
	const address = "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"

	if _, err := database.EnsureGroup(chatID, "裸地址验证模板测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpsertExclusiveAddress(chatID, address, "@owner", "靓仔无语"); err != nil {
		t.Fatalf("预置专属地址失败: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 746462,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "裸地址验证模板测试群",
		},
		From: &tgbotapi.User{
			ID:        memberID,
			UserName:  "a8668123",
			FirstName: "a8668123",
		},
		Text: address,
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if err := app.handleTRC20AddressVerification(msg, group, address); err != nil {
		t.Fatalf("处理裸地址验证失败: %v", err)
	}

	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, chatID, 746462, "【专属验证】专属地址验证通过")
	if !ok {
		t.Fatalf("专属地址裸发时未自动返回专属验证模板: %+v", snapshot)
	}
	if !strings.Contains(reply.text, "验证地址: <code>"+address+"</code>") {
		t.Fatalf("专属验证未返回地址: %+v", reply)
	}
	if !strings.Contains(reply.text, "验证次数: 1") {
		t.Fatalf("专属验证未返回验证次数: %+v", reply)
	}
	if !strings.Contains(reply.text, "上次发送人: 无") {
		t.Fatalf("首次专属验证应显示上次发送人无: %+v", reply)
	}
	if !strings.Contains(reply.text, "地址归属: @owner") {
		t.Fatalf("专属验证应显示地址归属: %+v", reply)
	}
	if !strings.Contains(reply.text, "备注信息: 靓仔无语") {
		t.Fatalf("专属验证应显示备注信息: %+v", reply)
	}
	if strings.Contains(reply.text, "验证地址：") || strings.Contains(reply.text, "验  证  码：") {
		t.Fatalf("专属地址裸发不应返回普通验证模板: %+v", reply)
	}
}

func TestPlainAddressVerificationCaptionIsolatedAcrossChats(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "plain-address-verification-global-count.db")
	app.address = services.NewAddressService(time.Second)

	chatA := int64(-100746471)
	chatB := int64(-100746472)
	const address = "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	for _, chatID := range []int64{chatA, chatB} {
		if _, err := database.EnsureGroup(chatID, "裸地址全局统计测试群"); err != nil {
			t.Fatalf("初始化群配置失败(chat=%d): %v", chatID, err)
		}
	}

	firstMsg := &tgbotapi.Message{
		MessageID: 746471,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: chatA, Type: "supergroup", Title: "A群"},
		From:      &tgbotapi.User{ID: 101, UserName: "alice", FirstName: "alice"},
		Text:      address,
	}
	firstGroup, err := database.GetGroup(chatA)
	if err != nil {
		t.Fatalf("读取 A 群配置失败: %v", err)
	}
	if err := app.handleTRC20AddressVerification(firstMsg, firstGroup, address); err != nil {
		t.Fatalf("处理 A 群裸地址验证失败: %v", err)
	}

	secondMsg := &tgbotapi.Message{
		MessageID: 746472,
		Date:      int(time.Now().Unix()) + 1,
		Chat:      &tgbotapi.Chat{ID: chatB, Type: "supergroup", Title: "B群"},
		From:      &tgbotapi.User{ID: 202, UserName: "bob", FirstName: "bob"},
		Text:      address,
	}
	secondGroup, err := database.GetGroup(chatB)
	if err != nil {
		t.Fatalf("读取 B 群配置失败: %v", err)
	}
	if err := app.handleTRC20AddressVerification(secondMsg, secondGroup, address); err != nil {
		t.Fatalf("处理 B 群裸地址验证失败: %v", err)
	}

	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, chatB, 746472, "验证地址：<code>"+address+"</code>")
	if !ok {
		t.Fatalf("B 群裸地址验证未返回普通验证模板: %+v", snapshot)
	}
	if !strings.Contains(reply.text, "验证次数：1") {
		t.Fatalf("B 群首次验证次数应按群从 1 开始: %+v", reply)
	}
	if !strings.Contains(reply.text, "上次发送人：无") {
		t.Fatalf("B 群首次验证不应显示 A 群发送人为上次发送人: %+v", reply)
	}
	if strings.Contains(reply.text, `tg://user?id=101`) || strings.Contains(reply.text, "@alice") {
		t.Fatalf("B 群验证信息不应包含 A 群发送人: %+v", reply)
	}
}

func TestPlainAddressVerificationUsesCurrentGroupUsernameForPreviousSender(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "plain-address-verification-current-group-username.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100746489)
	const address = "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	if _, err := database.EnsureGroup(chatID, "本群发送人用户名同步测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}

	firstMsg := &tgbotapi.Message{
		MessageID: 746489,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup", Title: "用户名同步群"},
		From:      &tgbotapi.User{ID: 101, UserName: "legacy_sender", FirstName: "legacy_sender"},
		Text:      address,
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if err := app.handleTRC20AddressVerification(firstMsg, group, address); err != nil {
		t.Fatalf("处理首次地址验证失败: %v", err)
	}

	if _, _, err := database.UpsertGroupUser(chatID, 101, "current_sender", "Current Sender"); err != nil {
		t.Fatalf("回填本群当前用户名失败: %v", err)
	}

	secondMsg := &tgbotapi.Message{
		MessageID: 746490,
		Date:      int(time.Now().Unix()) + 1,
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup", Title: "用户名同步群"},
		From:      &tgbotapi.User{ID: 202, UserName: "bob", FirstName: "bob"},
		Text:      address,
	}
	if err := app.handleTRC20AddressVerification(secondMsg, group, address); err != nil {
		t.Fatalf("处理第二次地址验证失败: %v", err)
	}

	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, chatID, 746490, "验证地址：<code>"+address+"</code>")
	if !ok {
		t.Fatalf("第二次地址验证未返回普通验证模板: %+v", snapshot)
	}
	if !strings.Contains(reply.text, "@current_sender") {
		t.Fatalf("上次发送人应显示本群当前用户名: %+v", reply)
	}
	if strings.Contains(reply.text, "@legacy_sender") {
		t.Fatalf("上次发送人不应继续显示本群旧用户名: %+v", reply)
	}
}

func TestAddressVerificationSendsBeforeProfileChangeAlert(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "address-verification-profile-order.db")
	app.address = services.NewAddressService(time.Second)
	chatID := int64(-100746473)
	userID := int64(74647301)
	const address = "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	if _, err := database.EnsureGroup(chatID, "地址验证资料变更顺序测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID+1); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(chatID, userID, "old_user", "old_user"); err != nil {
		t.Fatalf("预置旧成员资料失败: %v", err)
	}
	if err := app.handleMessage(context.Background(), fourLayerGroupTextUpdate(
		746473,
		746473,
		chatID,
		userID,
		"new_user",
		address,
		int(time.Now().Unix()),
	).Message); err != nil {
		t.Fatalf("处理地址消息失败: %v", err)
	}

	events := capture.eventSnapshot()
	messageEvents := make([]fourLayerCapturedEvent, 0, len(events))
	for _, event := range events {
		if event.kind == "message" {
			messageEvents = append(messageEvents, event)
		}
	}
	if len(messageEvents) < 2 {
		t.Fatalf("资料变更和地址验证都应产生消息，实际=%+v", messageEvents)
	}
	if messageEvents[0].source != "send_photo" {
		t.Fatalf("地址验证图片应先于资料变更提醒发送，实际首条=%+v", messageEvents[0])
	}
	if !strings.Contains(messageEvents[1].text, "用户修改用户名、昵称提示") {
		t.Fatalf("资料变更提醒应在地址验证之后发送，实际第二条=%+v", messageEvents[1])
	}
}

func TestFormatAddressVerificationTimingLogIncludesStages(t *testing.T) {
	logLine := formatAddressVerificationTimingLog(
		-100746473,
		746473,
		"success",
		addressVerificationTiming{
			Total:                3200 * time.Millisecond,
			TrackMessage:         12 * time.Millisecond,
			TrackStats:           48 * time.Millisecond,
			ExclusiveLookup:      7 * time.Millisecond,
			ImageRender:          31 * time.Millisecond,
			ImageBytes:           184320,
			MediaMode:            "multipart",
			MediaPublish:         23 * time.Millisecond,
			MediaURLReady:        false,
			SendAttemptCount:     1,
			TelegramResultClass:  "success",
			OutboundSend:         3100 * time.Millisecond,
			GroupLimiterWait:     120 * time.Millisecond,
			HTTPDNSLookup:        11 * time.Millisecond,
			HTTPConnect:          22 * time.Millisecond,
			HTTPTLSHandshake:     33 * time.Millisecond,
			HTTPConnectionWait:   44 * time.Millisecond,
			HTTPConnectionReused: true,
			HTTPRemoteIP:         "198.51.100.7",
			HTTPRequestWrite:     230 * time.Millisecond,
			HTTPResponseWait:     2600 * time.Millisecond,
			HTTPResponseParse:    150 * time.Millisecond,
		},
	)
	expected := "地址验证耗时: chat_id=-100746473 message_id=746473 result=success total_ms=3200 track_message_ms=12 track_stats_ms=48 exclusive_lookup_ms=7 image_render_ms=31 image_bytes=184320 media_mode=multipart media_publish_ms=23 media_url_ready=0 media_publish_error=\"\" send_attempt_count=1 telegram_result_class=success outbound_send_ms=3100 group_limiter_wait_ms=120 dns_lookup_ms=11 tcp_connect_ms=22 tls_handshake_ms=33 connection_wait_ms=44 connection_reused=1 remote_ip=198.51.100.7 http_request_write_ms=230 http_response_wait_ms=2600 http_response_parse_ms=150"
	if logLine != expected {
		t.Fatalf("耗时日志格式不符合固定契约\nwant: %s\n got: %s", expected, logLine)
	}
}

func TestHandleTRC20AddressVerificationTimingSuccess(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "address-verification-timing-success.db")
	app.address = services.NewAddressService(time.Second)
	installAddressVerificationTimingTransport(t, app, 20*time.Millisecond, nil)
	originalClient := app.api.Client

	chatID := int64(-100746490)
	messageID := 746490
	const address = "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	if _, err := database.EnsureGroup(chatID, "地址验证计时成功测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	msg := &tgbotapi.Message{
		MessageID: messageID,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup", Title: "地址验证计时成功测试群"},
		From:      &tgbotapi.User{ID: 7464901, UserName: "timing_success", FirstName: "timing_success"},
		Text:      address,
	}

	output, err := captureAddressVerificationTimingLog(t, func() error {
		return app.handleTRC20AddressVerification(msg, group, address)
	})
	if err != nil {
		t.Fatalf("地址验证成功路径不应返回错误: %v", err)
	}
	line := addressVerificationTimingLogLine(t, output)
	if !strings.Contains(line, "result=success") {
		t.Fatalf("成功路径 result 异常: %s", line)
	}
	imageMS := addressVerificationTimingField(t, line, "image_render_ms")
	totalMS := addressVerificationTimingField(t, line, "total_ms")
	if imageMS < 0 || imageMS > totalMS {
		t.Fatalf("成功路径 image_render_ms 应落在总耗时范围内: image=%d total=%d", imageMS, totalMS)
	}
	if imageBytes := addressVerificationTimingField(t, line, "image_bytes"); imageBytes <= 0 {
		t.Fatalf("成功路径 image_bytes 应为正数: %d", imageBytes)
	}
	if outboundMS := addressVerificationTimingField(t, line, "outbound_send_ms"); outboundMS < 15 {
		t.Fatalf("成功路径 outbound_send_ms 应包含可控发送延迟: %d", outboundMS)
	}
	if app.api.Client != originalClient {
		t.Fatal("地址验证 timing 发送不应替换共享 Telegram 客户端")
	}
	for _, key := range []string{"group_limiter_wait_ms", "http_request_write_ms", "http_response_wait_ms", "http_response_parse_ms"} {
		if got := addressVerificationTimingField(t, line, key); got < 0 {
			t.Fatalf("成功路径字段不得为负数: %s=%d line=%s", key, got, line)
		}
	}
}

func TestHandleTRC20AddressVerificationTimingEarlyDatabaseError(t *testing.T) {
	app, _, _ := newFourLayerTestApp(t, "address-verification-timing-db-error.db")
	app.address = services.NewAddressService(time.Second)
	const address = "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	msg := &tgbotapi.Message{
		MessageID: 746491,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: 0, Type: "supergroup", Title: "地址验证计时数据库错误测试群"},
		From:      &tgbotapi.User{ID: 7464911, UserName: "timing_db_error", FirstName: "timing_db_error"},
		Text:      address,
	}

	output, err := captureAddressVerificationTimingLog(t, func() error {
		return app.handleTRC20AddressVerification(msg, db.GroupConfig{}, address)
	})
	if err == nil {
		t.Fatal("地址验证早期数据库错误路径应返回错误")
	}
	line := addressVerificationTimingLogLine(t, output)
	if !strings.Contains(line, "result=error") {
		t.Fatalf("数据库错误路径 result 异常: %s", line)
	}
	_ = addressVerificationTimingField(t, line, "track_message_ms")
	for _, key := range []string{"track_stats_ms", "exclusive_lookup_ms", "image_render_ms", "image_bytes", "outbound_send_ms", "group_limiter_wait_ms", "http_request_write_ms", "http_response_wait_ms", "http_response_parse_ms"} {
		if got := addressVerificationTimingField(t, line, key); got != 0 {
			t.Fatalf("数据库错误路径后续字段应为 0: %s=%d line=%s", key, got, line)
		}
	}
}

func TestHandleTRC20AddressVerificationTimingSendPhotoError(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "address-verification-timing-send-error.db")
	app.address = services.NewAddressService(time.Second)
	installAddressVerificationTimingTransport(t, app, 20*time.Millisecond, errors.New("sendPhoto timing test error"))

	chatID := int64(-100746491)
	messageID := 746492
	const address = "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	if _, err := database.EnsureGroup(chatID, "地址验证计时发送错误测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	msg := &tgbotapi.Message{
		MessageID: messageID,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup", Title: "地址验证计时发送错误测试群"},
		From:      &tgbotapi.User{ID: 7464921, UserName: "timing_send_error", FirstName: "timing_send_error"},
		Text:      address,
	}

	output, err := captureAddressVerificationTimingLog(t, func() error {
		return app.handleTRC20AddressVerification(msg, group, address)
	})
	if err == nil {
		t.Fatal("sendPhoto 错误路径应返回错误")
	}
	line := addressVerificationTimingLogLine(t, output)
	if !strings.Contains(line, "result=error") {
		t.Fatalf("sendPhoto 错误路径 result 异常: %s", line)
	}
	if outboundMS := addressVerificationTimingField(t, line, "outbound_send_ms"); outboundMS < 15 {
		t.Fatalf("sendPhoto 错误路径 outbound_send_ms 应包含可控发送延迟: %d", outboundMS)
	}
	if imageBytes := addressVerificationTimingField(t, line, "image_bytes"); imageBytes <= 0 {
		t.Fatalf("sendPhoto 错误路径 image_bytes 应为正数: %d", imageBytes)
	}
	for _, key := range []string{"track_message_ms", "track_stats_ms", "exclusive_lookup_ms", "image_render_ms"} {
		if got := addressVerificationTimingField(t, line, key); got < 0 {
			t.Fatalf("sendPhoto 错误路径字段不得为负数: %s=%d line=%s", key, got, line)
		}
	}
	for _, key := range []string{"group_limiter_wait_ms", "http_request_write_ms", "http_response_wait_ms", "http_response_parse_ms"} {
		if got := addressVerificationTimingField(t, line, key); got < 0 {
			t.Fatalf("sendPhoto 错误路径字段不得为负数: %s=%d line=%s", key, got, line)
		}
	}
}

func TestMemberAddressVerificationAllowed(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{
			name: "裸地址",
			text: "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq",
		},
		{
			name: "验证地址命令",
			text: "验证地址 TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq",
		},
	}

	for idx, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			app, database, capture := newFourLayerTestApp(t, "member-address-verification-allowed.db")
			app.address = services.NewAddressService(time.Second)

			chatID := int64(-100746480 - idx)
			ownerID := int64(746480 + idx)
			memberID := int64(746580 + idx)
			messageID := 746680 + idx
			const address = "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"

			if _, err := database.EnsureGroup(chatID, "普通群员地址验证测试群"); err != nil {
				t.Fatalf("初始化群配置失败: %v", err)
			}
			if err := database.SetOwner(chatID, ownerID); err != nil {
				t.Fatalf("设置群权限人失败: %v", err)
			}

			app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
				746780+idx,
				messageID,
				chatID,
				memberID,
				"member_verify",
				tc.text,
				int(time.Now().Unix())+idx,
			))

			snapshot := capture.snapshot()
			reply, ok := fourLayerFindReply(snapshot, chatID, messageID, "验证地址：<code>"+address+"</code>")
			if !ok {
				t.Fatalf("普通群员发送地址应触发地址验证: %+v", snapshot)
			}
			if !strings.Contains(reply.text, "验证次数：1") {
				t.Fatalf("普通群员首次地址验证应记录次数: %+v", reply)
			}
			if !strings.Contains(reply.text, `本次发送人：<a href="tg://user?id=`) {
				t.Fatalf("普通群员地址验证应记录本次发送人: %+v", reply)
			}

			record, ok, err := database.GetAddressVerificationMessage(chatID, messageID)
			if err != nil {
				t.Fatalf("读取地址消息记录失败: %v", err)
			}
			if !ok {
				t.Fatalf("普通群员地址验证应写入地址消息记录")
			}
			if record.SenderUserID != memberID || record.OriginalAddress != address || record.CurrentAddress != address {
				t.Fatalf("普通群员地址验证消息记录不符合预期: %+v", record)
			}
		})
	}
}

func TestEditedAddressMessageSendsWarningAndRevalidates(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "edited-address-message-warning.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100746473)
	userID := int64(746473)
	messageID := 746473
	originalAddress := "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	editedAddress := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	if _, err := database.EnsureGroup(chatID, "地址编辑检测测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.AddOperator(chatID, userID, "alice", false); err != nil {
		t.Fatalf("设置测试操作人失败: %v", err)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		7464731,
		messageID,
		chatID,
		userID,
		"alice",
		originalAddress,
		int(time.Date(2026, 7, 12, 8, 30, 0, 0, time.UTC).Unix()),
	))
	app.dispatchUpdate(context.Background(), fourLayerEditedGroupTextUpdate(
		7464732,
		messageID,
		chatID,
		userID,
		"alice",
		editedAddress,
		int(time.Date(2026, 7, 12, 8, 31, 0, 0, time.UTC).Unix()),
		int(time.Date(2026, 7, 12, 8, 31, 9, 0, time.UTC).Unix()),
	))

	snapshot := capture.snapshot()
	warning, ok := fourLayerFindReply(snapshot, chatID, messageID, "⚠️ 检测到地址消息已被编辑")
	if !ok {
		t.Fatalf("地址消息编辑后缺少风险警告: %+v", snapshot)
	}
	for _, want := range []string{
		"原始地址：<code>" + originalAddress + "</code>",
		"编辑后地址：<code>" + editedAddress + "</code>",
		"编辑时间：2026-07-12 16:31:09",
		`发送人：<a href="tg://user?id=746473">@alice</a>`,
	} {
		if !strings.Contains(warning.text, want) {
			t.Fatalf("地址编辑警告缺少 %q，实际: %s", want, warning.text)
		}
	}
	if _, ok := fourLayerFindReply(snapshot, chatID, messageID, "验证地址：<code>"+editedAddress+"</code>"); !ok {
		t.Fatalf("编辑后的地址应重新验证: %+v", snapshot)
	}

	record, ok, err := database.GetAddressVerificationMessage(chatID, messageID)
	if err != nil {
		t.Fatalf("读取地址消息记录失败: %v", err)
	}
	if !ok {
		t.Fatalf("缺少地址消息记录")
	}
	if record.OriginalAddress != originalAddress || record.CurrentAddress != editedAddress || record.EditCount != 1 {
		t.Fatalf("地址消息编辑记录不符合预期: %+v", record)
	}
}

func TestEditedNonAddressMessageIsIgnored(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "edited-non-address-message-ignore.db")

	chatID := int64(-100746474)
	messageID := 746474
	if _, err := database.EnsureGroup(chatID, "非地址编辑忽略测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	app.dispatchUpdate(context.Background(), fourLayerEditedGroupTextUpdate(
		7464741,
		messageID,
		chatID,
		746474,
		"bob",
		"今晚八点确认",
		int(time.Date(2026, 7, 12, 16, 35, 0, 0, time.UTC).Unix()),
		int(time.Date(2026, 7, 12, 16, 36, 0, 0, time.UTC).Unix()),
	))

	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("非地址编辑不应发送地址编辑提示: %+v", snapshot)
	}
	if record, ok, err := database.GetAddressVerificationMessage(chatID, messageID); err != nil {
		t.Fatalf("读取地址消息记录失败: %v", err)
	} else if ok {
		t.Fatalf("非地址编辑不应写入地址消息记录: %+v", record)
	}
}

func TestEditedAddressMessageRemovedAddressStillWarns(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "edited-address-message-removed-address.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100746475)
	userID := int64(746475)
	messageID := 746475
	originalAddress := "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	if _, err := database.EnsureGroup(chatID, "地址编辑删除测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.AddOperator(chatID, userID, "carol", false); err != nil {
		t.Fatalf("设置测试操作人失败: %v", err)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		7464751,
		messageID,
		chatID,
		userID,
		"carol",
		originalAddress,
		int(time.Date(2026, 7, 12, 8, 40, 0, 0, time.UTC).Unix()),
	))
	app.dispatchUpdate(context.Background(), fourLayerEditedGroupTextUpdate(
		7464752,
		messageID,
		chatID,
		userID,
		"carol",
		"地址已撤回",
		int(time.Date(2026, 7, 12, 8, 41, 0, 0, time.UTC).Unix()),
		int(time.Date(2026, 7, 12, 8, 41, 9, 0, time.UTC).Unix()),
	))

	snapshot := capture.snapshot()
	warning, ok := fourLayerFindReply(snapshot, chatID, messageID, "⚠️ 检测到地址消息已被编辑")
	if !ok {
		t.Fatalf("地址被编辑删除后缺少风险警告: %+v", snapshot)
	}
	for _, want := range []string{
		"原始地址：<code>" + originalAddress + "</code>",
		"编辑后地址：未检测到有效地址",
		"‼️原地址消息已被编辑，当前未检测到有效地址。",
	} {
		if !strings.Contains(warning.text, want) {
			t.Fatalf("地址删除警告缺少 %q，实际: %s", want, warning.text)
		}
	}
	record, ok, err := database.GetAddressVerificationMessage(chatID, messageID)
	if err != nil {
		t.Fatalf("读取地址消息记录失败: %v", err)
	}
	if !ok {
		t.Fatalf("缺少地址消息记录")
	}
	if record.CurrentAddress != "" || record.EditCount != 1 {
		t.Fatalf("地址删除编辑记录不符合预期: %+v", record)
	}
}

func TestExtractTRC20AddressForVerification(t *testing.T) {
	addr, ok := extractTRC20AddressForVerification("转账地址是 TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq 请核对")
	if !ok {
		t.Fatalf("应识别 TRC20 地址")
	}
	if addr != "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq" {
		t.Fatalf("提取地址错误: %s", addr)
	}
	if _, ok := extractTRC20AddressForVerification("hello world"); ok {
		t.Fatalf("非地址文本不应命中")
	}
}

func TestExtractTRC20AddressForVerificationLooseFallback(t *testing.T) {
	raw := "地址 T000000000000000000000000000000000 请核实"
	addr, ok := extractTRC20AddressForVerification(raw)
	if !ok {
		t.Fatalf("宽松模式应识别地址")
	}
	if addr != "T000000000000000000000000000000000" {
		t.Fatalf("宽松地址提取错误: %s", addr)
	}
}

func TestExtractTRC20AddressForVerificationERC20(t *testing.T) {
	addr, ok := extractTRC20AddressForVerification("0X0123456789abcdef0123456789ABCDEF01234567")
	if !ok {
		t.Fatalf("应识别 ERC20 地址")
	}
	if addr != "0x0123456789abcdef0123456789ABCDEF01234567" {
		t.Fatalf("ERC20 地址提取错误: %s", addr)
	}
}

func TestIsAddressManagementCommand(t *testing.T) {
	if !isAddressManagementCommand("设置下发地址 0x0123456789abcdef0123456789ABCDEF01234567") {
		t.Fatalf("设置下发地址命令应识别为地址管理命令")
	}
	if !isAddressManagementCommand("查询地址 0x0123456789abcdef0123456789ABCDEF01234567") {
		t.Fatalf("查询地址命令应识别为地址管理命令")
	}
	if isAddressManagementCommand("请核对地址 0x0123456789abcdef0123456789ABCDEF01234567") {
		t.Fatalf("普通文本不应识别为地址管理命令")
	}
}

func TestParseAddressFromSetAddressCommand(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{
			name: "标准格式",
			text: "设置下发地址 0x0123456789abcdef0123456789ABCDEF01234567",
			want: "0x0123456789abcdef0123456789ABCDEF01234567",
			ok:   true,
		},
		{
			name: "中文冒号",
			text: "设置下发地址：0x0123456789abcdef0123456789ABCDEF01234567",
			want: "0x0123456789abcdef0123456789ABCDEF01234567",
			ok:   true,
		},
		{
			name: "带引导词与注释",
			text: "设置专属地址 为 0X0123456789abcdef0123456789ABCDEF01234567 （备用）",
			want: "0x0123456789abcdef0123456789ABCDEF01234567",
			ok:   true,
		},
		{
			name: "空参数",
			text: "设置下发地址：",
			want: "",
			ok:   false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseAddressFromSetAddressCommand(tc.text)
			if ok != tc.ok {
				t.Fatalf("解析状态异常: want=%v got=%v text=%q", tc.ok, ok, tc.text)
			}
			if got != tc.want {
				t.Fatalf("解析地址异常: want=%q got=%q", tc.want, got)
			}
		})
	}
}

func TestParseExclusiveAddressOwnerAndRemarkFromSetAddressCommand(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		wantAddr   string
		wantOwner  string
		wantRemark string
		ok         bool
	}{
		{
			name:       "标准格式",
			text:       "设置专属地址 0x742d35Cc6634C0532925a3b844Bc454e4438f44e @alice 备用通道",
			wantAddr:   "0x742d35Cc6634C0532925a3b844Bc454e4438f44e",
			wantOwner:  "@alice",
			wantRemark: "备用通道",
			ok:         true,
		},
		{
			name:       "所属人无@自动补齐",
			text:       "设置专属地址 0x742d35Cc6634C0532925a3b844Bc454e4438f44e alice 备用通道",
			wantAddr:   "0x742d35Cc6634C0532925a3b844Bc454e4438f44e",
			wantOwner:  "@alice",
			wantRemark: "备用通道",
			ok:         true,
		},
		{
			name: "缺少备注",
			text: "设置专属地址 0x742d35Cc6634C0532925a3b844Bc454e4438f44e @alice",
			ok:   false,
		},
		{
			name: "缺少所属人",
			text: "设置专属地址 0x742d35Cc6634C0532925a3b844Bc454e4438f44e 仅备注",
			ok:   false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			addr, owner, remark, ok := parseExclusiveAddressOwnerAndRemarkFromSetAddressCommand(tc.text)
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

func TestParseExclusiveAddressRemarkQuery(t *testing.T) {
	remark, ok := parseExclusiveAddressRemarkQuery("靓仔无语地址")
	if !ok {
		t.Fatalf("应识别备注地址查询命令")
	}
	if remark != "靓仔无语" {
		t.Fatalf("备注解析异常: want=%q got=%q", "靓仔无语", remark)
	}
	if _, ok := parseExclusiveAddressRemarkQuery("查询地址"); ok {
		t.Fatalf("查询地址不应识别为备注地址查询命令")
	}
	if _, ok := parseExclusiveAddressRemarkQuery("设置专属地址"); ok {
		t.Fatalf("设置专属地址不应识别为备注地址查询命令")
	}
	if _, ok := parseExclusiveAddressRemarkQuery("查询链上地址"); ok {
		t.Fatalf("查询链上地址不应识别为备注地址查询命令")
	}
	if _, ok := parseExclusiveAddressRemarkQuery("查询TRC20地址"); ok {
		t.Fatalf("查询TRC20地址不应识别为备注地址查询命令")
	}
}

func TestIsMemberAddressQueryCommand(t *testing.T) {
	if !isMemberAddressQueryCommand("地址") {
		t.Fatalf("地址应识别为成员可查询命令")
	}
	if !isMemberAddressQueryCommand("专属地址") {
		t.Fatalf("专属地址应识别为成员可查询命令")
	}
	if isMemberAddressQueryCommand("靓仔无语地址") {
		t.Fatalf("备注地址查询不应作为通用成员地址命令")
	}
}

func TestParseClearExclusiveAddressByRemarkCommand(t *testing.T) {
	remark, ok := parseClearExclusiveAddressByRemarkCommand("清除ma地址")
	if !ok {
		t.Fatalf("应识别清除备注地址命令")
	}
	if remark != "ma" {
		t.Fatalf("清除备注解析异常: want=%q got=%q", "ma", remark)
	}
	if _, ok := parseClearExclusiveAddressByRemarkCommand("清除下发地址"); ok {
		t.Fatalf("清除下发地址不应识别为按备注清除专属地址命令")
	}
	if _, ok := parseClearExclusiveAddressByRemarkCommand("清空专属地址"); ok {
		t.Fatalf("清空专属地址不应识别为按备注清除专属地址命令")
	}
}

func TestParseAddressFromQueryAddressCommand(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{
			name: "紧贴地址",
			text: "查询地址TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq",
			want: "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq",
			ok:   true,
		},
		{
			name: "地址后附加说明",
			text: "查询地址TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq】可查询当前地址近5笔交易与基本信息",
			want: "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq",
			ok:   true,
		},
		{
			name: "无地址参数",
			text: "查询地址",
			want: "",
			ok:   false,
		},
		{
			name: "非查询地址命令",
			text: "验证地址 TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq",
			want: "",
			ok:   false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseAddressFromQueryAddressCommand(tc.text)
			if ok != tc.ok {
				t.Fatalf("解析状态异常: want=%v got=%v text=%q", tc.ok, ok, tc.text)
			}
			if got != tc.want {
				t.Fatalf("解析地址异常: want=%q got=%q", tc.want, got)
			}
		})
	}
}

func TestMessageTextForDispatchUsesCaption(t *testing.T) {
	msg := &tgbotapi.Message{
		Text:    "",
		Caption: "  TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq  ",
	}
	if got := messageTextForDispatch(msg); got != "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq" {
		t.Fatalf("caption 提取失败: %q", got)
	}
}

func TestNormalizeGroupTextForDispatch(t *testing.T) {
	cases := []struct {
		name        string
		text        string
		botUsername string
		want        string
	}{
		{
			name:        "匹配机器人前缀并剥离",
			text:        "@demo_bot 设置下发地址 0x0123456789abcdef0123456789ABCDEF01234567",
			botUsername: "demo_bot",
			want:        "设置下发地址 0x0123456789abcdef0123456789ABCDEF01234567",
		},
		{
			name:        "支持中文标点分隔",
			text:        "@demo_bot，设置下发地址 0x0123456789abcdef0123456789ABCDEF01234567",
			botUsername: "demo_bot",
			want:        "设置下发地址 0x0123456789abcdef0123456789ABCDEF01234567",
		},
		{
			name:        "其他用户名不处理",
			text:        "@other_bot 设置下发地址 0x0123456789abcdef0123456789ABCDEF01234567",
			botUsername: "demo_bot",
			want:        "@other_bot 设置下发地址 0x0123456789abcdef0123456789ABCDEF01234567",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeGroupTextForDispatch(tc.text, tc.botUsername); got != tc.want {
				t.Fatalf("normalize 结果异常: want=%q got=%q", tc.want, got)
			}
		})
	}
}

func TestHandleMessageMentionSetPayoutAddressPermissionDenied(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "set-address-mention-permission.db")

	chatID := int64(-100551001)
	ownerID := int64(551001)
	memberID := int64(551002)
	if _, err := database.EnsureGroup(chatID, "地址权限测试群"); err != nil {
		t.Fatalf("初始化群失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 551201,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "地址权限测试群",
		},
		From: &tgbotapi.User{
			ID:        memberID,
			UserName:  "member_setter",
			FirstName: "member_setter",
		},
		Text: "@four_layer_test_bot 设置下发地址 0x0123456789abcdef0123456789ABCDEF01234567",
	}
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("处理消息失败: %v", err)
	}

	snapshot := capture.snapshot()
	if fourLayerHasAnyReply(snapshot, chatID, msg.MessageID) {
		t.Fatalf("无权限用户设置下发地址应静默，不应回执: %+v", snapshot)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.PayoutAddress != "" {
		t.Fatalf("无权限用户不应改写下发地址: %q", group.PayoutAddress)
	}
}

func TestGenerateAddressVerificationImage(t *testing.T) {
	imgBytes, err := generateAddressVerificationImage(
		"TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq",
		"7290",
		"2026-02-21 12:34:56",
	)
	if err != nil {
		t.Fatalf("生成图片失败: %v", err)
	}
	if len(imgBytes) == 0 {
		t.Fatalf("生成图片为空")
	}
	if _, err := jpeg.Decode(bytes.NewReader(imgBytes)); err != nil {
		t.Fatalf("生成图片格式异常: %v", err)
	}
}

func TestGenerateAddressVerificationImageUsesBlueWhiteLayout(t *testing.T) {
	imgBytes, err := generateAddressVerificationImageWithRemark(
		"TGR7YWM5sTL4c27KxWnDrHrFeUXMhygduS",
		"靓仔无语",
		"9770",
		"2026-07-12 14:25:36",
	)
	if err != nil {
		t.Fatalf("生成图片失败: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		t.Fatalf("生成图片格式异常: %v", err)
	}
	if img.Bounds().Dx() != 1280 || img.Bounds().Dy() != 720 {
		t.Fatalf("图片画布尺寸应为 1280x720，实际: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	topR, _, topB, _ := img.At(32, 90).RGBA()
	if topR>>8 > 90 || topB>>8 < 90 {
		t.Fatalf("地址区应为深蓝背景，实际 RGB: %d,%d", topR>>8, topB>>8)
	}
	lowR, lowG, lowB, _ := img.At(32, 320).RGBA()
	if lowR>>8 < 190 || lowG>>8 < 205 || lowB>>8 < 220 {
		t.Fatalf("内容区应为浅蓝白背景，实际 RGB: %d,%d,%d", lowR>>8, lowG>>8, lowB>>8)
	}
}

func TestAddressVerificationBackgroundHasNoTextureLines(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	drawAddressVerificationBlueWhiteBackground(canvas)

	lineR, lineG, lineB, _ := canvas.At(120, 220).RGBA()
	nextR, nextG, nextB, _ := canvas.At(120, 221).RGBA()
	diff := absInt(int(lineR>>8)-int(nextR>>8)) +
		absInt(int(lineG>>8)-int(nextG>>8)) +
		absInt(int(lineB>>8)-int(nextB>>8))
	if diff > 3 {
		t.Fatalf("浅色内容区不应有明显纹理横线，相邻行颜色差=%d", diff)
	}
}

func TestAddressVerificationCanvasUsesCachedBackgroundTemplate(t *testing.T) {
	template := addressVerificationBackgroundTemplate()
	if template == nil {
		t.Fatal("地址验证背景模板不应为空")
	}
	if addressVerificationBackgroundTemplate() != template {
		t.Fatal("地址验证背景模板应复用同一个缓存对象")
	}

	first := newAddressVerificationCanvas(template.Bounds())
	second := newAddressVerificationCanvas(template.Bounds())
	if first == nil || second == nil {
		t.Fatal("地址验证画布不应为空")
	}
	if first == second {
		t.Fatal("每次生成图片都必须使用独立画布，不能直接修改共享模板")
	}

	original := second.RGBAAt(120, 220)
	first.SetRGBA(120, 220, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	if got := second.RGBAAt(120, 220); got != original {
		t.Fatalf("修改一张图片不应污染另一张图片的背景: want=%v got=%v", original, got)
	}
}

func TestAddressVerificationImageLayoutGivesAddressMoreSpace(t *testing.T) {
	layout := buildAddressVerificationImageLayout(
		"TGR7YWM5sTL4c27KxWnDrHrFeUXMhygduS",
		"靓仔无语",
		"9770",
		"2026-07-12 14:25:36",
	)
	if layout.Bounds.Dx() != 1280 || layout.Bounds.Dy() != 720 {
		t.Fatalf("图片画布尺寸应为 1280x720，实际: %dx%d", layout.Bounds.Dx(), layout.Bounds.Dy())
	}
	if len(layout.Sections) != 4 {
		t.Fatalf("图片应为四个信息区，实际: %d", len(layout.Sections))
	}
	expectedLabels := []string{"本次验证地址", "备注信息", "本次验证码", "本次验证时间"}
	expectedRects := []image.Rectangle{
		image.Rect(0, 0, 1280, 270),
		image.Rect(0, 270, 1280, 405),
		image.Rect(0, 405, 1280, 540),
		image.Rect(0, 540, 1280, 720),
	}
	for i, section := range layout.Sections {
		if section.Label != expectedLabels[i] {
			t.Fatalf("第 %d 个信息区标题不符合预期: want %q got %q", i+1, expectedLabels[i], section.Label)
		}
		if section.Rect != expectedRects[i] {
			t.Fatalf("第 %d 个信息区尺寸不符合地址放大布局: want=%v got=%v", i+1, expectedRects[i], section.Rect)
		}
	}
	if !layout.Sections[0].DarkBackground {
		t.Fatalf("地址信息区应使用深蓝底反衬白字")
	}
	if layout.Sections[0].PreferredValueSize != 60 {
		t.Fatalf("地址文字最大字号应放大到 60，实际: %.2f", layout.Sections[0].PreferredValueSize)
	}
}

func TestFitAddressVerifyTextSizeKeepsAddressOnSingleLine(t *testing.T) {
	address := "TGR7YWM5sTL4c27KxWnDrHrFeUXMhygduS"
	size, err := fitAddressVerifyTextSize(address, 1208, 60, 24)
	if err != nil {
		t.Fatalf("计算地址单行字号失败: %v", err)
	}
	if size < 24 || size > 60 {
		t.Fatalf("地址字号应在动态范围内，实际: %.2f", size)
	}
	if size < 46 {
		t.Fatalf("地址字号应明显大于旧版，实际: %.2f", size)
	}
	face, err := newAddressVerifyFace(size)
	if err != nil {
		t.Fatalf("创建地址字体失败: %v", err)
	}
	defer closeFontFace(face)
	if width := measureTextWidth(face, address); width > 1208 {
		t.Fatalf("地址单行宽度超出可用区域: width=%d limit=1208", width)
	}
}

func TestBuildAddressVerificationImageLinesIncludesRemark(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	lines := buildAddressVerificationImageLines(
		address,
		"靓仔无语",
		"8953",
		"2026-07-12 13:40:51",
	)
	joined := strings.Join(lines, "\n")
	compact := strings.ReplaceAll(joined, "\n", "")

	for _, want := range []string{
		"本次验证地址：",
		"备注信息：靓仔无语",
		"本次验证码：8953",
		"本次验证时间：2026-07-12 13:40:51",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("图片文本行缺少 %q，实际内容:\n%s", want, joined)
		}
	}
	if !strings.Contains(compact, address) {
		t.Fatalf("图片文本行缺少完整地址 %q，实际内容:\n%s", address, joined)
	}
	if strings.Contains(joined, "本次验证地址:") ||
		strings.Contains(joined, "本次验证码:") ||
		strings.Contains(joined, "本次验证时间:") {
		t.Fatalf("图片文本行应使用中文全角冒号，实际内容:\n%s", joined)
	}
}

func TestLoadAddressVerifyFontSupportsVerificationImageText(t *testing.T) {
	tt, err := loadAddressVerifyFont()
	if err != nil {
		t.Fatalf("加载地址验证图片字体失败: %v", err)
	}
	for _, r := range "本次验证地址备注信息码时间靓仔无语TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B89532026-07-12 13:40:51：" {
		glyph, err := tt.GlyphIndex(nil, r)
		if err != nil {
			t.Fatalf("读取字形 %q 失败: %v", r, err)
		}
		if glyph == 0 {
			t.Fatalf("地址验证图片字体缺少中文字形 %q", r)
		}
	}
}

func TestLoadAddressVerifyFontCachesParsedFont(t *testing.T) {
	first, err := loadAddressVerifyFont()
	if err != nil {
		t.Fatalf("首次加载地址验证图片字体失败: %v", err)
	}
	second, err := loadAddressVerifyFont()
	if err != nil {
		t.Fatalf("第二次加载地址验证图片字体失败: %v", err)
	}
	if first != second {
		t.Fatalf("地址验证图片字体应复用已解析对象，避免每次生成图片重复解析 TTC")
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
