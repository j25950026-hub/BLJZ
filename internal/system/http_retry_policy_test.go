package system

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestControlPoint255RetryPolicy(t *testing.T) {
	t.Run("控制点 2.5.5 功能验证", func(t *testing.T) {
		timeoutSleeps := make([]time.Duration, 0, 2)
		timeoutCalls := 0
		timeoutTransport := &retryPolicyRoundTripper{
			base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				timeoutCalls++
				if timeoutCalls <= 2 {
					return nil, mockTimeoutError{}
				}
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       io.NopCloser(strings.NewReader("ok")),
					Header:     make(http.Header),
				}, nil
			}),
			spec: HTTPRetryPolicySpec{
				MaxAttempts:    3,
				TimeoutBackoff: 50 * time.Millisecond,
				RateLimitSleep: time.Second,
			},
			sleep: func(_ context.Context, delay time.Duration) error {
				timeoutSleeps = append(timeoutSleeps, delay)
				return nil
			},
			now: time.Now,
		}

		timeoutReq, err := http.NewRequest(http.MethodGet, "https://example.com/timeout", nil)
		if err != nil {
			t.Fatalf("构建 timeout 请求失败: %v", err)
		}
		timeoutResp, err := timeoutTransport.RoundTrip(timeoutReq)
		if err != nil {
			t.Fatalf("timeout 重试应成功: %v", err)
		}
		if timeoutResp.StatusCode != http.StatusNoContent {
			t.Fatalf("timeout 重试成功后状态码错误: %d", timeoutResp.StatusCode)
		}
		_ = timeoutResp.Body.Close()
		if timeoutCalls != 3 {
			t.Fatalf("timeout 重试次数错误: want=3 got=%d", timeoutCalls)
		}
		if len(timeoutSleeps) != 2 || timeoutSleeps[0] != 50*time.Millisecond || timeoutSleeps[1] != 100*time.Millisecond {
			t.Fatalf("timeout 重试退避错误: %+v", timeoutSleeps)
		}

		rateLimitSleeps := make([]time.Duration, 0, 1)
		rateLimitCalls := 0
		receivedBodies := make([]string, 0, 2)
		first429Body := &mockCloseBody{reader: strings.NewReader("limited")}
		rateLimitTransport := &retryPolicyRoundTripper{
			base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				rateLimitCalls++
				payload, readErr := io.ReadAll(req.Body)
				if readErr != nil {
					t.Fatalf("读取请求体失败: %v", readErr)
				}
				receivedBodies = append(receivedBodies, string(payload))
				_ = req.Body.Close()

				if rateLimitCalls == 1 {
					return &http.Response{
						StatusCode: http.StatusTooManyRequests,
						Header: http.Header{
							"Retry-After": []string{"3"},
						},
						Body: first429Body,
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusCreated,
					Body:       io.NopCloser(strings.NewReader("created")),
					Header:     make(http.Header),
				}, nil
			}),
			spec: HTTPRetryPolicySpec{
				MaxAttempts:    3,
				TimeoutBackoff: 50 * time.Millisecond,
				RateLimitSleep: 2 * time.Second,
			},
			sleep: func(_ context.Context, delay time.Duration) error {
				rateLimitSleeps = append(rateLimitSleeps, delay)
				return nil
			},
			now: time.Now,
		}

		postBody := []byte(`{"message":"retry-on-429"}`)
		rateLimitReq, err := http.NewRequest(
			http.MethodPost,
			"https://example.com/ratelimit",
			bytes.NewReader(postBody),
		)
		if err != nil {
			t.Fatalf("构建 429 请求失败: %v", err)
		}
		rateLimitResp, err := rateLimitTransport.RoundTrip(rateLimitReq)
		if err != nil {
			t.Fatalf("429 自动休眠后应重试成功: %v", err)
		}
		if rateLimitResp.StatusCode != http.StatusCreated {
			t.Fatalf("429 重试后状态码错误: %d", rateLimitResp.StatusCode)
		}
		_ = rateLimitResp.Body.Close()
		if rateLimitCalls != 2 {
			t.Fatalf("429 请求应触发 1 次重试: got=%d", rateLimitCalls)
		}
		if len(rateLimitSleeps) != 1 || rateLimitSleeps[0] != 3*time.Second {
			t.Fatalf("429 休眠时长错误: %+v", rateLimitSleeps)
		}
		if !first429Body.closed {
			t.Fatalf("429 首次响应体应被关闭，避免连接泄漏")
		}
		if len(receivedBodies) != 2 || receivedBodies[0] != string(postBody) || receivedBodies[1] != string(postBody) {
			t.Fatalf("重试请求体应保持一致: %+v", receivedBodies)
		}
	})
}

func TestWrapHTTPClientWithTracingInjectsRetryPolicy(t *testing.T) {
	client := WrapHTTPClientWithTracing(&http.Client{}, "cp255-retry")
	tracingTransport, ok := client.Transport.(*tracingRoundTripper)
	if !ok {
		t.Fatalf("HTTP client transport 应为 tracingRoundTripper")
	}
	if _, ok := tracingTransport.base.(*retryPolicyRoundTripper); !ok {
		t.Fatalf("tracing transport 底层应挂载重试中间件")
	}
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type mockTimeoutError struct{}

func (mockTimeoutError) Error() string {
	return "timeout"
}

func (mockTimeoutError) Timeout() bool {
	return true
}

func (mockTimeoutError) Temporary() bool {
	return true
}

type mockCloseBody struct {
	reader io.Reader
	closed bool
}

func (m *mockCloseBody) Read(p []byte) (int, error) {
	if m == nil || m.reader == nil {
		return 0, io.EOF
	}
	return m.reader.Read(p)
}

func (m *mockCloseBody) Close() error {
	if m != nil {
		m.closed = true
	}
	return nil
}
