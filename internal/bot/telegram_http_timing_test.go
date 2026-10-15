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
	"testing"
	"time"
)

type telegramTimingRoundTripper func(*http.Request) (*http.Response, error)

func (f telegramTimingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type telegramTimingTestAddr string

func (a telegramTimingTestAddr) Network() string {
	return "tcp"
}

func (a telegramTimingTestAddr) String() string {
	return string(a)
}

type telegramTimingTestConn struct{}

func (telegramTimingTestConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (telegramTimingTestConn) Write(p []byte) (int, error) {
	return len(p), nil
}

func (telegramTimingTestConn) Close() error {
	return nil
}

func (telegramTimingTestConn) LocalAddr() net.Addr {
	return telegramTimingTestAddr("10.0.0.2:52100")
}

func (telegramTimingTestConn) RemoteAddr() net.Addr {
	return telegramTimingTestAddr("198.51.100.7:443")
}

func (telegramTimingTestConn) SetDeadline(time.Time) error {
	return nil
}

func (telegramTimingTestConn) SetReadDeadline(time.Time) error {
	return nil
}

func (telegramTimingTestConn) SetWriteDeadline(time.Time) error {
	return nil
}

func TestTelegramTimingHTTPClientRecordsConnectionPhases(t *testing.T) {
	timing := newTelegramRequestTiming()
	client := &telegramTimingHTTPClient{
		base: &http.Client{Transport: telegramTimingRoundTripper(func(req *http.Request) (*http.Response, error) {
			trace := httptrace.ContextClientTrace(req.Context())
			if trace == nil {
				t.Fatal("请求 context 缺少 httptrace")
			}
			trace.GetConn("api.telegram.org:443")
			trace.DNSStart(httptrace.DNSStartInfo{Host: "api.telegram.org"})
			time.Sleep(2 * time.Millisecond)
			trace.DNSDone(httptrace.DNSDoneInfo{})
			trace.ConnectStart("tcp", "198.51.100.7:443")
			time.Sleep(2 * time.Millisecond)
			trace.ConnectDone("tcp", "198.51.100.7:443", nil)
			trace.TLSHandshakeStart()
			time.Sleep(2 * time.Millisecond)
			trace.TLSHandshakeDone(tls.ConnectionState{}, nil)
			time.Sleep(2 * time.Millisecond)
			trace.GotConn(httptrace.GotConnInfo{
				Conn:     telegramTimingTestConn{},
				Reused:   true,
				WasIdle:  true,
				IdleTime: 25 * time.Millisecond,
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Request:    req,
			}, nil
		})},
		timing: timing,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.com/bot-token/sendPhoto", nil)
	if err != nil {
		t.Fatalf("构建测试请求失败: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("timing HTTP client 请求失败: %v", err)
	}
	_ = resp.Body.Close()

	snapshot := timing.snapshot()
	if snapshot.HTTPDNSLookup < time.Millisecond {
		t.Fatalf("DNS 耗时未记录: %v", snapshot.HTTPDNSLookup)
	}
	if snapshot.HTTPConnect < time.Millisecond {
		t.Fatalf("TCP 连接耗时未记录: %v", snapshot.HTTPConnect)
	}
	if snapshot.HTTPTLSHandshake < time.Millisecond {
		t.Fatalf("TLS 握手耗时未记录: %v", snapshot.HTTPTLSHandshake)
	}
	if snapshot.HTTPConnectionWait < time.Millisecond {
		t.Fatalf("连接池等待耗时未记录: %v", snapshot.HTTPConnectionWait)
	}
	if !snapshot.HTTPConnectionReused {
		t.Fatal("连接复用标记未记录")
	}
	if snapshot.HTTPRemoteIP != "198.51.100.7" {
		t.Fatalf("远端 IP 异常: %q", snapshot.HTTPRemoteIP)
	}
}

func TestTelegramTimingHTTPClientRecordsHTTPPhases(t *testing.T) {
	timing := newTelegramRequestTiming()
	client := &telegramTimingHTTPClient{
		base: &http.Client{Transport: telegramTimingRoundTripper(func(req *http.Request) (*http.Response, error) {
			trace := httptrace.ContextClientTrace(req.Context())
			if trace == nil {
				t.Fatal("请求 context 缺少 httptrace")
			}
			trace.WroteHeaders()
			time.Sleep(3 * time.Millisecond)
			trace.WroteRequest(httptrace.WroteRequestInfo{})
			time.Sleep(3 * time.Millisecond)
			trace.GotFirstResponseByte()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Request:    req,
			}, nil
		})},
		timing: timing,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.com/bot-token/sendPhoto", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("构建测试请求失败: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("timing HTTP client 请求失败: %v", err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("读取测试响应失败: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("关闭测试响应失败: %v", err)
	}

	snapshot := timing.snapshot()
	if snapshot.HTTPRequestWrite < time.Millisecond {
		t.Fatalf("请求写入耗时未记录: %v", snapshot.HTTPRequestWrite)
	}
	if snapshot.HTTPResponseWait < time.Millisecond {
		t.Fatalf("响应等待耗时未记录: %v", snapshot.HTTPResponseWait)
	}
	if snapshot.HTTPResponseParse <= 0 {
		t.Fatalf("响应解析耗时未记录: %v", snapshot.HTTPResponseParse)
	}
}

func TestTelegramTimingHTTPClientLeavesUnavailableTracePhasesAtZero(t *testing.T) {
	timing := newTelegramRequestTiming()
	client := &telegramTimingHTTPClient{
		base: &http.Client{Transport: telegramTimingRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Request:    req,
			}, nil
		})},
		timing: timing,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.com/bot-token/sendPhoto", nil)
	if err != nil {
		t.Fatalf("构建测试请求失败: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("timing HTTP client 请求失败: %v", err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("读取测试响应失败: %v", err)
	}
	_ = resp.Body.Close()

	snapshot := timing.snapshot()
	if snapshot.HTTPDNSLookup != 0 || snapshot.HTTPConnect != 0 || snapshot.HTTPTLSHandshake != 0 || snapshot.HTTPConnectionWait != 0 {
		t.Fatalf("缺失连接 trace 事件不应推导连接阶段耗时: %+v", snapshot)
	}
	if snapshot.HTTPConnectionReused || snapshot.HTTPRemoteIP != "" {
		t.Fatalf("缺失 GotConn 事件不应推导连接复用和远端地址: %+v", snapshot)
	}
	if snapshot.HTTPRequestWrite != 0 || snapshot.HTTPResponseWait != 0 {
		t.Fatalf("缺失 trace 事件不应推导请求/响应耗时: %+v", snapshot)
	}
	if snapshot.HTTPResponseParse <= 0 {
		t.Fatalf("响应体读取仍应记录 parse 耗时: %v", snapshot.HTTPResponseParse)
	}
}

type telegramTimingErrorBody struct{}

func (telegramTimingErrorBody) Read([]byte) (int, error) {
	return 0, errors.New("response body read failed")
}

func (telegramTimingErrorBody) Close() error {
	return errors.New("response body close failed")
}

func TestTelegramTimingResponseBodyPreservesErrorsAndFinishesOnce(t *testing.T) {
	timing := newTelegramRequestTiming()
	body := &telegramTimingResponseBody{
		ReadCloser: telegramTimingErrorBody{},
		timing:     timing,
	}
	if _, err := body.Read(make([]byte, 8)); err == nil || err.Error() != "response body read failed" {
		t.Fatalf("应保留响应体读取错误，实际=%v", err)
	}
	first := timing.snapshot().HTTPResponseParse
	if first <= 0 {
		t.Fatalf("读取错误路径应完成 parse 计时: %v", first)
	}
	if err := body.Close(); err == nil || err.Error() != "response body close failed" {
		t.Fatalf("应保留响应体关闭错误，实际=%v", err)
	}
	second := timing.snapshot().HTTPResponseParse
	if second != first {
		t.Fatalf("重复完成不应覆盖首次 parse 计时，first=%v second=%v", first, second)
	}
	if err := body.Close(); err == nil || err.Error() != "response body close failed" {
		t.Fatalf("重复关闭仍应保留底层关闭错误，实际=%v", err)
	}
}

func TestTelegramRequestTimingIsolatedAcrossConcurrentRequests(t *testing.T) {
	const requests = 2
	timings := make([]*telegramRequestTiming, requests)
	clients := make([]*telegramTimingHTTPClient, requests)
	for i := 0; i < requests; i++ {
		timings[i] = newTelegramRequestTiming()
		delay := time.Duration(i+1) * 3 * time.Millisecond
		clients[i] = &telegramTimingHTTPClient{
			base: &http.Client{Transport: telegramTimingRoundTripper(func(req *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(req.Context())
				trace.WroteHeaders()
				time.Sleep(delay)
				trace.WroteRequest(httptrace.WroteRequestInfo{})
				trace.GotFirstResponseByte()
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
					Request:    req,
				}, nil
			})},
			timing: timings[i],
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.com/bot-token/sendPhoto", nil)
			if err != nil {
				t.Errorf("构建并发测试请求失败: %v", err)
				return
			}
			resp, err := clients[index].Do(req)
			if err != nil {
				t.Errorf("并发 timing 请求失败: %v", err)
				return
			}
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}(i)
	}
	wg.Wait()

	first := timings[0].snapshot().HTTPRequestWrite
	second := timings[1].snapshot().HTTPRequestWrite
	if first <= 0 || second <= 0 || first == second {
		t.Fatalf("并发请求 timing 未保持独立，first=%v second=%v", first, second)
	}
}

func TestRateLimitedHTTPClientRecordsGroupLimiterWait(t *testing.T) {
	timing := newTelegramRequestTiming()
	client := &rateLimitedHTTPClient{
		base:         &countingHTTPDoer{},
		groupLimiter: newGroupLeakyBucketLimiter(600),
	}
	first := newTelegramFormRequest(t, "sendMessage", "chat_id=-2005&text=first")
	if _, err := client.Do(first); err != nil {
		t.Fatalf("首个测试请求失败: %v", err)
	}

	second := newTelegramFormRequest(t, "sendMessage", "chat_id=-2005&text=second")
	second = second.WithContext(withTelegramRequestTiming(second.Context(), timing))
	if _, err := client.Do(second); err != nil {
		t.Fatalf("第二个测试请求失败: %v", err)
	}

	if got := timing.snapshot().GroupLimiterWait; got < 80*time.Millisecond {
		t.Fatalf("群限流等待未被记录，want>=80ms got=%v", got)
	}
}
