package bot

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
)

type telegramRequestTimingContextKey struct{}

type telegramRequestTiming struct {
	mu sync.Mutex

	groupLimiterWait         time.Duration
	requestWriteStartedAt    time.Time
	requestWriteFinishedAt   time.Time
	responseFirstByteAt      time.Time
	responseParseStartedAt   time.Time
	responseParseFinishedAt  time.Time
	dnsLookupStartedAt       time.Time
	dnsLookupFinishedAt      time.Time
	connectStartedAt         time.Time
	connectFinishedAt        time.Time
	tlsHandshakeStartedAt    time.Time
	tlsHandshakeFinishedAt   time.Time
	connectionWaitStartedAt  time.Time
	connectionWaitFinishedAt time.Time
	connectionReused         bool
	connectionSeen           bool
	remoteIP                 string
}

type telegramRequestTimingSnapshot struct {
	GroupLimiterWait     time.Duration
	HTTPRequestWrite     time.Duration
	HTTPResponseWait     time.Duration
	HTTPResponseParse    time.Duration
	HTTPDNSLookup        time.Duration
	HTTPConnect          time.Duration
	HTTPTLSHandshake     time.Duration
	HTTPConnectionWait   time.Duration
	HTTPConnectionReused bool
	HTTPRemoteIP         string
}

func newTelegramRequestTiming() *telegramRequestTiming {
	return &telegramRequestTiming{}
}

func withTelegramRequestTiming(ctx context.Context, timing *telegramRequestTiming) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if timing == nil {
		return ctx
	}
	return context.WithValue(ctx, telegramRequestTimingContextKey{}, timing)
}

func telegramRequestTimingFromContext(ctx context.Context) *telegramRequestTiming {
	if ctx == nil {
		return nil
	}
	timing, _ := ctx.Value(telegramRequestTimingContextKey{}).(*telegramRequestTiming)
	return timing
}

func (t *telegramRequestTiming) recordGroupLimiterWait(duration time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.groupLimiterWait = duration
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markWroteHeaders(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.requestWriteStartedAt.IsZero() {
		t.requestWriteStartedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markWroteRequest(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.requestWriteFinishedAt.IsZero() {
		t.requestWriteFinishedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markGotFirstResponseByte(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.responseFirstByteAt.IsZero() {
		t.responseFirstByteAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markResponseParseStarted(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.responseParseStartedAt.IsZero() {
		t.responseParseStartedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markResponseParseFinished(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.responseParseFinishedAt.IsZero() {
		t.responseParseFinishedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markDNSStart(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.dnsLookupStartedAt.IsZero() {
		t.dnsLookupStartedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markDNSDone(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.dnsLookupFinishedAt.IsZero() {
		t.dnsLookupFinishedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markConnectStart(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.connectStartedAt.IsZero() {
		t.connectStartedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markConnectDone(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.connectFinishedAt.IsZero() {
		t.connectFinishedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markTLSHandshakeStart(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.tlsHandshakeStartedAt.IsZero() {
		t.tlsHandshakeStartedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markTLSHandshakeDone(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.tlsHandshakeFinishedAt.IsZero() {
		t.tlsHandshakeFinishedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markGetConn(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.connectionWaitStartedAt.IsZero() {
		t.connectionWaitStartedAt = at
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) markGotConn(at time.Time, reused bool, remoteIP string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.connectionWaitFinishedAt.IsZero() {
		t.connectionWaitFinishedAt = at
	}
	if !t.connectionSeen {
		t.connectionReused = reused
		t.remoteIP = remoteIP
		t.connectionSeen = true
	}
	t.mu.Unlock()
}

func (t *telegramRequestTiming) snapshot() telegramRequestTimingSnapshot {
	if t == nil {
		return telegramRequestTimingSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return telegramRequestTimingSnapshot{
		GroupLimiterWait:     t.groupLimiterWait,
		HTTPRequestWrite:     elapsedTiming(t.requestWriteStartedAt, t.requestWriteFinishedAt),
		HTTPResponseWait:     elapsedTiming(t.requestWriteFinishedAt, t.responseFirstByteAt),
		HTTPResponseParse:    elapsedTiming(t.responseParseStartedAt, t.responseParseFinishedAt),
		HTTPDNSLookup:        elapsedTiming(t.dnsLookupStartedAt, t.dnsLookupFinishedAt),
		HTTPConnect:          elapsedTiming(t.connectStartedAt, t.connectFinishedAt),
		HTTPTLSHandshake:     elapsedTiming(t.tlsHandshakeStartedAt, t.tlsHandshakeFinishedAt),
		HTTPConnectionWait:   elapsedTiming(t.connectionWaitStartedAt, t.connectionWaitFinishedAt),
		HTTPConnectionReused: t.connectionReused,
		HTTPRemoteIP:         t.remoteIP,
	}
}

func elapsedTiming(start, end time.Time) time.Duration {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

func remoteIPFromConn(conn net.Conn) string {
	if conn == nil || conn.RemoteAddr() == nil {
		return ""
	}
	address := strings.TrimSpace(conn.RemoteAddr().String())
	if address == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		return strings.TrimSpace(host)
	}
	return address
}

type telegramTimingHTTPClient struct {
	base   httpDoer
	timing *telegramRequestTiming
}

func (c *telegramTimingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.base == nil {
		return nil, errors.New("base http client is nil")
	}
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if c.timing == nil {
		return c.base.Do(req)
	}

	trace := &httptrace.ClientTrace{
		GetConn: func(string) {
			c.timing.markGetConn(time.Now())
		},
		GotConn: func(info httptrace.GotConnInfo) {
			c.timing.markGotConn(time.Now(), info.Reused, remoteIPFromConn(info.Conn))
		},
		DNSStart: func(httptrace.DNSStartInfo) {
			c.timing.markDNSStart(time.Now())
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			c.timing.markDNSDone(time.Now())
		},
		ConnectStart: func(string, string) {
			c.timing.markConnectStart(time.Now())
		},
		ConnectDone: func(string, string, error) {
			c.timing.markConnectDone(time.Now())
		},
		TLSHandshakeStart: func() {
			c.timing.markTLSHandshakeStart(time.Now())
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			c.timing.markTLSHandshakeDone(time.Now())
		},
		WroteHeaders: func() {
			c.timing.markWroteHeaders(time.Now())
		},
		WroteRequest: func(httptrace.WroteRequestInfo) {
			c.timing.markWroteRequest(time.Now())
		},
		GotFirstResponseByte: func() {
			c.timing.markGotFirstResponseByte(time.Now())
		},
	}
	ctx := withTelegramRequestTiming(req.Context(), c.timing)
	ctx = httptrace.WithClientTrace(ctx, trace)
	resp, err := c.base.Do(req.WithContext(ctx))
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = &telegramTimingResponseBody{
		ReadCloser: resp.Body,
		timing:     c.timing,
	}
	return resp, nil
}

type telegramTimingResponseBody struct {
	io.ReadCloser
	timing *telegramRequestTiming

	mu         sync.Mutex
	startedAt  time.Time
	finishOnce sync.Once
}

func (b *telegramTimingResponseBody) Read(p []byte) (int, error) {
	if b == nil || b.ReadCloser == nil {
		return 0, io.EOF
	}
	b.mu.Lock()
	if b.startedAt.IsZero() {
		b.startedAt = time.Now()
		b.timing.markResponseParseStarted(b.startedAt)
	}
	b.mu.Unlock()
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b *telegramTimingResponseBody) Close() error {
	if b == nil || b.ReadCloser == nil {
		return nil
	}
	b.finish()
	return b.ReadCloser.Close()
}

func (b *telegramTimingResponseBody) finish() {
	if b == nil || b.timing == nil {
		return
	}
	b.finishOnce.Do(func() {
		b.timing.markResponseParseFinished(time.Now())
	})
}
