package bot

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestControlPoint231GlobalTokenBucket(t *testing.T) {
	t.Run("控制点 2.3.1 功能验证", func(t *testing.T) {
		limiter := newTokenBucket(globalBotAPITPS, globalBotAPIBurst)
		defer limiter.Close()

		base := &countingHTTPDoer{}
		client := &rateLimitedHTTPClient{
			base:    base,
			limiter: limiter,
		}

		const totalRequests = 90
		var wg sync.WaitGroup
		errCh := make(chan error, totalRequests)
		start := time.Now()

		for i := 0; i < totalRequests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.com/bot-api", nil)
				if err != nil {
					errCh <- err
					return
				}
				resp, err := client.Do(req)
				if err != nil {
					errCh <- err
					return
				}
				_ = resp.Body.Close()
			}()
		}

		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				t.Fatalf("全局令牌桶调用失败: %v", err)
			}
		}

		elapsed := time.Since(start)
		if got := base.Calls(); got != totalRequests {
			t.Fatalf("调用总数异常，want=%d got=%d", totalRequests, got)
		}
		if elapsed < 1800*time.Millisecond {
			t.Fatalf("限流未生效，90次请求耗时应接近>=2秒，实际=%v", elapsed)
		}
	})
}

func TestRateLimitedHTTPClientHonorsContextDeadline(t *testing.T) {
	limiter := newTokenBucket(1, 1)
	defer limiter.Close()

	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("预消费首个令牌失败: %v", err)
	}

	base := &countingHTTPDoer{}
	client := &rateLimitedHTTPClient{
		base:    base,
		limiter: limiter,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com/bot-api", nil)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}

	if _, err = client.Do(req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应返回 context deadline exceeded，实际=%v", err)
	}
	if got := base.Calls(); got != 0 {
		t.Fatalf("请求应在限流等待阶段超时，底层 Do 不应被调用，实际=%d", got)
	}
}

func TestNewTelegramBotAPIHTTPClientAppliesRequestTimeout(t *testing.T) {
	client := newTelegramBotAPIHTTPClientWithTimeout(
		&blockingHTTPDoer{},
		&blockingHTTPDoer{},
		nil,
		nil,
		25*time.Millisecond,
	)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/getUpdates", nil)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}

	start := time.Now()
	_, err = client.Do(req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Telegram Bot API client 应注入请求超时，实际=%v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("请求超时应快速返回，实际耗时=%v", elapsed)
	}
}

func TestRateLimitedHTTPClientKeepsTimeoutContextAliveUntilResponseBodyCloses(t *testing.T) {
	base := &contextBoundResponseHTTPDoer{payload: "telegram-response"}
	client := newTelegramBotAPIHTTPClientWithTimeout(
		base,
		base,
		nil,
		nil,
		time.Second,
	)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/getUpdates", nil)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Telegram Bot API 请求失败: %v", err)
	}
	select {
	case <-base.Context().Done():
		t.Fatalf("底层 Do 返回后、响应体读取前不应取消请求上下文: %v", base.Context().Err())
	default:
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 Telegram Bot API 响应体失败: %v", err)
	}
	if got, want := string(body), base.payload; got != want {
		t.Fatalf("响应体异常，want=%q got=%q", want, got)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("关闭 Telegram Bot API 响应体失败: %v", err)
	}
	select {
	case <-base.Context().Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("响应体关闭后应释放请求超时上下文")
	}
}

func TestControlPoint232GroupLeakyBucket(t *testing.T) {
	t.Run("控制点 2.3.2 功能验证", func(t *testing.T) {
		limiter := newGroupLeakyBucketLimiter(groupBotMessagesPerMinute)
		if limiter.interval != 3*time.Second {
			t.Fatalf("群组漏桶间隔异常，want=3s got=%v", limiter.interval)
		}

		if err := limiter.Wait(context.Background(), -1001); err != nil {
			t.Fatalf("首条群消息不应被阻塞: %v", err)
		}

		blockedCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		if err := limiter.Wait(blockedCtx, -1001); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("同群第二条消息应被漏桶限流，实际=%v", err)
		}

		start := time.Now()
		if err := limiter.Wait(context.Background(), -1002); err != nil {
			t.Fatalf("不同群组应独立限流: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 120*time.Millisecond {
			t.Fatalf("不同群组不应串行等待，耗时=%v", elapsed)
		}
	})
}

func TestRateLimitedHTTPClientAppliesGroupLeakyBucket(t *testing.T) {
	groupLimiter := newGroupLeakyBucketLimiter(600)
	base := &countingHTTPDoer{}
	client := &rateLimitedHTTPClient{
		base:         base,
		groupLimiter: groupLimiter,
	}

	first := newTelegramFormRequest(t, "sendMessage", "chat_id=-2001&text=hello")
	second := newTelegramFormRequest(t, "sendMessage", "chat_id=-2001&text=world")

	start := time.Now()
	if _, err := client.Do(first); err != nil {
		t.Fatalf("首条请求失败: %v", err)
	}
	if _, err := client.Do(second); err != nil {
		t.Fatalf("第二条请求失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Fatalf("同群发送应触发漏桶延迟，实际耗时=%v", elapsed)
	}

	otherGroup := newTelegramFormRequest(t, "sendMessage", "chat_id=-2002&text=independent")
	anotherStart := time.Now()
	if _, err := client.Do(otherGroup); err != nil {
		t.Fatalf("不同群组请求失败: %v", err)
	}
	if elapsed := time.Since(anotherStart); elapsed > 80*time.Millisecond {
		t.Fatalf("不同群组不应被同群漏桶阻塞，耗时=%v", elapsed)
	}

	privateChat := newTelegramFormRequest(t, "sendMessage", "chat_id=9527&text=private")
	privateStart := time.Now()
	if _, err := client.Do(privateChat); err != nil {
		t.Fatalf("私聊消息请求失败: %v", err)
	}
	if elapsed := time.Since(privateStart); elapsed > 80*time.Millisecond {
		t.Fatalf("私聊不应受群组漏桶影响，耗时=%v", elapsed)
	}

	if got := base.Calls(); got != 4 {
		t.Fatalf("底层请求计数异常，want=4 got=%d", got)
	}
}

func TestRateLimitedHTTPClientAppliesGroupLeakyBucketToMultipart(t *testing.T) {
	groupLimiter := newGroupLeakyBucketLimiter(600)
	base := &countingHTTPDoer{}
	client := &rateLimitedHTTPClient{
		base:         base,
		groupLimiter: groupLimiter,
	}

	first := newTelegramFormRequest(t, "sendMessage", "chat_id=-3001&text=hello")
	second := newTelegramMultipartRequest(t, "sendPhoto", "-3001")

	start := time.Now()
	if _, err := client.Do(first); err != nil {
		t.Fatalf("首条请求失败: %v", err)
	}
	if _, err := client.Do(second); err != nil {
		t.Fatalf("multipart 请求失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Fatalf("同群 multipart 发送应触发漏桶延迟，实际耗时=%v", elapsed)
	}
	if got := base.Calls(); got != 2 {
		t.Fatalf("底层请求计数异常，want=2 got=%d", got)
	}
}

func TestRequestChatIDSkipsLargeMultipartWithoutReadingBody(t *testing.T) {
	req := newTelegramMultipartRequest(t, "sendPhoto", "-4001")
	req.ContentLength = 100 << 20

	if chatID, ok := requestChatID(req); ok {
		t.Fatalf("声明过大的 multipart body 不应为提取 chat_id 而被读取解析，got chat_id=%d", chatID)
	}
}

func TestRequestChatIDReadsMediumMultipartBody(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", "-4002"); err != nil {
		t.Fatalf("写入 chat_id 失败: %v", err)
	}
	if err := writer.WriteField("payload", strings.Repeat("x", 128<<10)); err != nil {
		t.Fatalf("写入 multipart payload 失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart writer 失败: %v", err)
	}
	const legacyChatIDProbeBodyBytes = 64 << 10
	if body.Len() <= legacyChatIDProbeBodyBytes || body.Len() >= maxChatIDProbeBodyBytes {
		t.Fatalf("测试请求应位于旧阈值和新阈值之间，实际=%d old=%d new=%d", body.Len(), legacyChatIDProbeBodyBytes, maxChatIDProbeBodyBytes)
	}

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://api.telegram.org/bot-token/sendPhoto",
		bytes.NewReader(body.Bytes()),
	)
	if err != nil {
		t.Fatalf("构建 multipart 请求失败: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.ContentLength = int64(body.Len())

	chatID, ok := requestChatID(req)
	if !ok || chatID != -4002 {
		t.Fatalf("中等大小 multipart body 应提取 chat_id，got chat_id=%d ok=%v body=%d", chatID, ok, body.Len())
	}
}

func newTelegramFormRequest(t *testing.T, endpoint, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://api.telegram.org/bot-token/"+endpoint,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func newTelegramMultipartRequest(t *testing.T, endpoint, chatID string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", chatID); err != nil {
		t.Fatalf("写入 chat_id 失败: %v", err)
	}
	if err := writer.WriteField("caption", "photo"); err != nil {
		t.Fatalf("写入 caption 失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart writer 失败: %v", err)
	}
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://api.telegram.org/bot-token/"+endpoint,
		&body,
	)
	if err != nil {
		t.Fatalf("构建 multipart 请求失败: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

type countingHTTPDoer struct {
	calls atomic.Int32
}

func (d *countingHTTPDoer) Do(*http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}, nil
}

func (d *countingHTTPDoer) Calls() int {
	return int(d.calls.Load())
}

type blockingHTTPDoer struct{}

func (d *blockingHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

type contextBoundResponseHTTPDoer struct {
	ctx     context.Context
	payload string
}

func (d *contextBoundResponseHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	d.ctx = req.Context()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: &contextBoundResponseBody{
			ctx:    req.Context(),
			reader: strings.NewReader(d.payload),
		},
	}, nil
}

func (d *contextBoundResponseHTTPDoer) Context() context.Context {
	return d.ctx
}

type contextBoundResponseBody struct {
	ctx    context.Context
	reader *strings.Reader
}

func (b *contextBoundResponseBody) Read(p []byte) (int, error) {
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	default:
		return b.reader.Read(p)
	}
}

func (b *contextBoundResponseBody) Close() error {
	return nil
}
