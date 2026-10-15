package bot

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/system"
)

const (
	globalBotAPITPS                     = 30
	globalBotAPIBurst                   = 30
	groupBotMessagesPerMinute           = 20
	defaultTelegramRequestTimeout       = 45 * time.Second
	defaultTelegramSingleAttemptTimeout = 45 * time.Second
	// Address verification photos are larger than 64 KiB; keep probing bounded while
	// allowing normal multipart photo requests to participate in group rate limiting.
	maxChatIDProbeBodyBytes = 256 << 10
)

var errTokenBucketClosed = errors.New("token bucket limiter closed")

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type rateLimitedHTTPClient struct {
	base              httpDoer
	singleAttemptBase httpDoer
	limiter           *tokenBucket
	groupLimiter      *groupLeakyBucketLimiter
	requestTimeout    time.Duration
}

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnCloseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

func (c *rateLimitedHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.base == nil {
		return nil, errors.New("base http client is nil")
	}
	if req == nil {
		return nil, errors.New("request is nil")
	}
	cancel := context.CancelFunc(func() {})
	hasRequestTimeout := c.requestTimeout > 0
	if hasRequestTimeout {
		ctx, timeoutCancel := context.WithTimeout(req.Context(), c.requestTimeout)
		cancel = timeoutCancel
		req = req.WithContext(ctx)
	}
	timing := telegramRequestTimingFromContext(req.Context())
	if c.groupLimiter != nil {
		var err error
		if timing != nil {
			err = c.groupLimiter.WaitByRequestWithTiming(req, timing.recordGroupLimiterWait)
		} else {
			err = c.groupLimiter.WaitByRequest(req)
		}
		if err != nil {
			cancel()
			return nil, err
		}
	}
	if c.limiter != nil {
		if err := c.limiter.Wait(req.Context()); err != nil {
			cancel()
			return nil, err
		}
	}
	resp, err := c.base.Do(req)
	if err != nil {
		cancel()
		return resp, err
	}
	if !hasRequestTimeout {
		return resp, nil
	}
	if resp == nil || resp.Body == nil {
		cancel()
		return resp, nil
	}
	resp.Body = &cancelOnCloseReadCloser{
		ReadCloser: resp.Body,
		cancel:     cancel,
	}
	return resp, nil
}

func (c *rateLimitedHTTPClient) singleAttemptHTTPClient() httpDoer {
	if c == nil {
		return nil
	}
	base := c.singleAttemptBase
	if base == nil {
		base = c.base
	}
	return &rateLimitedHTTPClient{
		base:           base,
		limiter:        c.limiter,
		groupLimiter:   c.groupLimiter,
		requestTimeout: defaultTelegramSingleAttemptTimeout,
	}
}

func singleAttemptTelegramHTTPClient(client tgbotapi.HTTPClient) httpDoer {
	if provider, ok := client.(interface{ singleAttemptHTTPClient() httpDoer }); ok {
		return provider.singleAttemptHTTPClient()
	}
	return client
}

func newRateLimitedBotAPI(token string) (*tgbotapi.BotAPI, *tokenBucket, error) {
	limiter := newTokenBucket(globalBotAPITPS, globalBotAPIBurst)
	groupLimiter := newGroupLeakyBucketLimiter(groupBotMessagesPerMinute)
	transport := http.DefaultTransport
	retryTransport := system.NewRetryPolicyRoundTripper(transport, system.DefaultHTTPRetryPolicySpec())
	retryClient := &http.Client{
		Transport: system.NewTracingRoundTripper(retryTransport, "telegram-bot-api"),
	}
	singleAttemptClient := &http.Client{
		Transport: system.NewTracingRoundTripper(transport, "telegram-bot-api-single-attempt"),
	}
	api, err := tgbotapi.NewBotAPIWithClient(
		token,
		tgbotapi.APIEndpoint,
		newTelegramBotAPIHTTPClient(retryClient, singleAttemptClient, limiter, groupLimiter),
	)
	if err != nil {
		limiter.Close()
		return nil, nil, err
	}
	return api, limiter, nil
}

func newTelegramBotAPIHTTPClient(base httpDoer, singleAttemptBase httpDoer, limiter *tokenBucket, groupLimiter *groupLeakyBucketLimiter) *rateLimitedHTTPClient {
	return newTelegramBotAPIHTTPClientWithTimeout(base, singleAttemptBase, limiter, groupLimiter, defaultTelegramRequestTimeout)
}

func newTelegramBotAPIHTTPClientWithTimeout(base httpDoer, singleAttemptBase httpDoer, limiter *tokenBucket, groupLimiter *groupLeakyBucketLimiter, timeout time.Duration) *rateLimitedHTTPClient {
	return &rateLimitedHTTPClient{
		base:              base,
		singleAttemptBase: singleAttemptBase,
		limiter:           limiter,
		groupLimiter:      groupLimiter,
		requestTimeout:    timeout,
	}
}

type groupLeakyBucketLimiter struct {
	interval   time.Duration
	mu         sync.Mutex
	nextByChat map[int64]time.Time
}

func newGroupLeakyBucketLimiter(messagesPerMinute int) *groupLeakyBucketLimiter {
	if messagesPerMinute <= 0 {
		messagesPerMinute = 1
	}
	interval := time.Minute / time.Duration(messagesPerMinute)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	return &groupLeakyBucketLimiter{
		interval:   interval,
		nextByChat: map[int64]time.Time{},
	}
}

func (l *groupLeakyBucketLimiter) WaitByRequest(req *http.Request) error {
	return l.waitByRequest(req, nil)
}

func (l *groupLeakyBucketLimiter) WaitByRequestWithTiming(req *http.Request, record func(time.Duration)) error {
	return l.waitByRequest(req, record)
}

func (l *groupLeakyBucketLimiter) waitByRequest(req *http.Request, record func(time.Duration)) error {
	if l == nil || req == nil {
		return nil
	}
	method := telegramEndpoint(req)
	if !isMessageSendMethod(method) {
		return nil
	}
	chatID, ok := requestChatID(req)
	if !ok || !isGroupChatID(chatID) {
		return nil
	}
	startedAt := time.Now()
	err := l.Wait(req.Context(), chatID)
	if record != nil {
		record(time.Since(startedAt))
	}
	return err
}

func (l *groupLeakyBucketLimiter) Wait(ctx context.Context, chatID int64) error {
	if l == nil || !isGroupChatID(chatID) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		now := time.Now()

		l.mu.Lock()
		next := l.nextByChat[chatID]
		if !next.After(now) {
			l.nextByChat[chatID] = now.Add(l.interval)
			l.mu.Unlock()
			return nil
		}
		waitFor := next.Sub(now)
		l.mu.Unlock()

		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func telegramEndpoint(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	path := strings.TrimSpace(req.URL.Path)
	if path == "" {
		return ""
	}
	idx := strings.LastIndex(path, "/")
	if idx < 0 || idx == len(path)-1 {
		return strings.ToLower(path)
	}
	return strings.ToLower(path[idx+1:])
}

func isMessageSendMethod(endpoint string) bool {
	switch strings.TrimSpace(strings.ToLower(endpoint)) {
	case "copymessage", "copymessages", "forwardmessage", "forwardmessages":
		return true
	default:
		return strings.HasPrefix(strings.TrimSpace(strings.ToLower(endpoint)), "send")
	}
}

func requestChatID(req *http.Request) (int64, bool) {
	if req == nil {
		return 0, false
	}
	contentType := strings.TrimSpace(req.Header.Get("Content-Type"))
	mediaType := ""
	mediaParams := map[string]string{}
	if contentType != "" {
		parsedType, parsedParams, err := mime.ParseMediaType(contentType)
		if err != nil {
			return 0, false
		}
		mediaType = strings.ToLower(strings.TrimSpace(parsedType))
		mediaParams = parsedParams
	}
	if req.Body == nil {
		return 0, false
	}
	if req.ContentLength < 0 || req.ContentLength > maxChatIDProbeBodyBytes {
		return 0, false
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return 0, false
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	req.ContentLength = int64(len(bodyBytes))

	if len(bodyBytes) == 0 {
		return 0, false
	}
	if len(bodyBytes) > maxChatIDProbeBodyBytes {
		return 0, false
	}

	rawChatID := ""
	switch mediaType {
	case "", "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(bodyBytes))
		if err != nil {
			return 0, false
		}
		rawChatID = strings.TrimSpace(values.Get("chat_id"))
	case "multipart/form-data":
		boundary := strings.TrimSpace(mediaParams["boundary"])
		if boundary == "" {
			return 0, false
		}
		form, err := multipart.NewReader(bytes.NewReader(bodyBytes), boundary).ReadForm(maxChatIDProbeBodyBytes)
		if err != nil {
			return 0, false
		}
		defer form.RemoveAll()
		values := form.Value["chat_id"]
		if len(values) > 0 {
			rawChatID = strings.TrimSpace(values[0])
		}
	default:
		return 0, false
	}
	if rawChatID == "" {
		return 0, false
	}
	chatID, err := strconv.ParseInt(rawChatID, 10, 64)
	if err != nil {
		return 0, false
	}
	return chatID, true
}

func isGroupChatID(chatID int64) bool {
	return chatID < 0
}

type tokenBucket struct {
	tokens    chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func newTokenBucket(rate, burst int) *tokenBucket {
	if rate <= 0 {
		rate = 1
	}
	if burst <= 0 {
		burst = rate
	}

	bucket := &tokenBucket{
		tokens: make(chan struct{}, burst),
		closed: make(chan struct{}),
	}
	for i := 0; i < burst; i++ {
		bucket.tokens <- struct{}{}
	}

	interval := time.Second / time.Duration(rate)
	go bucket.runRefill(interval)
	return bucket
}

func (b *tokenBucket) runRefill(interval time.Duration) {
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-b.closed:
			return
		case <-ticker.C:
			select {
			case b.tokens <- struct{}{}:
			default:
			}
		}
	}
}

func (b *tokenBucket) Wait(ctx context.Context) error {
	if b == nil {
		return errors.New("token bucket is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-b.closed:
		return errTokenBucketClosed
	default:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.closed:
		return errTokenBucketClosed
	case <-b.tokens:
		return nil
	}
}

func (b *tokenBucket) Close() {
	if b == nil {
		return
	}
	b.closeOnce.Do(func() {
		close(b.closed)
	})
}
