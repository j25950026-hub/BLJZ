package webhook

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/audit"
	"go-bot/internal/config"
)

type stubUpdateReceiver struct {
	accept bool

	mu      sync.Mutex
	updates []tgbotapi.Update
}

func (s *stubUpdateReceiver) EnqueueWebhookUpdate(update tgbotapi.Update) bool {
	if !s.accept {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, update)
	return true
}

func (s *stubUpdateReceiver) snapshot() []tgbotapi.Update {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([]tgbotapi.Update, len(s.updates))
	copy(copied, s.updates)
	return copied
}

type stubBackpressureReceiver struct {
	accept       bool
	queued       int
	capacity     int
	enqueueCalls int
}

func (s *stubBackpressureReceiver) EnqueueWebhookUpdate(update tgbotapi.Update) bool {
	s.enqueueCalls++
	return s.accept
}

func (s *stubBackpressureReceiver) WebhookQueueUsage() (queued, capacity int) {
	return s.queued, s.capacity
}

func TestControlPoint211WebhookHandler(t *testing.T) {
	t.Run("控制点 2.1.1 功能验证", func(t *testing.T) {
		receiver := &stubUpdateReceiver{accept: true}
		server := NewServer(config.Config{WebhookPath: "/tg/webhook"}, receiver)

		payload := `{"update_id":1001,"message":{"message_id":99,"date":1739930400,"chat":{"id":-100123,"type":"group"},"text":"记账 +100"}}`
		req := httptest.NewRequest(http.MethodPost, "/tg/webhook", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()

		server.Handler().ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("Webhook 接收状态码错误: %d", resp.Code)
		}
		updates := receiver.snapshot()
		if len(updates) != 1 {
			t.Fatalf("Webhook 应写入 1 条更新，got=%d", len(updates))
		}
		if updates[0].UpdateID != 1001 {
			t.Fatalf("UpdateID 解析错误: %d", updates[0].UpdateID)
		}
		if updates[0].Message == nil || updates[0].Message.Text != "记账 +100" {
			t.Fatalf("消息内容解析错误: %+v", updates[0].Message)
		}
	})
}

func TestWebhookHandlerRejectInvalidPayload(t *testing.T) {
	receiver := &stubUpdateReceiver{accept: true}
	server := NewServer(config.Config{WebhookPath: "tg/webhook"}, receiver)

	req := httptest.NewRequest(http.MethodPost, "/tg/webhook", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应返回 400，got=%d", resp.Code)
	}
	if got := len(receiver.snapshot()); got != 0 {
		t.Fatalf("非法请求不应进入业务队列，got=%d", got)
	}
}

func TestWebhookHandlerRejectWhenQueueFull(t *testing.T) {
	receiver := &stubUpdateReceiver{accept: false}
	server := NewServer(config.Config{WebhookPath: "/tg/webhook"}, receiver)

	payload := `{"update_id":2002}`
	req := httptest.NewRequest(http.MethodPost, "/tg/webhook", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("队列满应返回 503，got=%d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "系统繁忙") {
		t.Fatalf("队列满应返回系统繁忙提示，got=%s", resp.Body.String())
	}
}

func TestControlPoint235Backpressure(t *testing.T) {
	t.Run("控制点 2.3.5 功能验证", func(t *testing.T) {
		receiver := &stubBackpressureReceiver{
			accept:   true,
			queued:   8,
			capacity: 10,
		}
		server := NewServer(config.Config{WebhookPath: "/tg/webhook"}, receiver)

		payload := `{"update_id":2350}`
		req := httptest.NewRequest(http.MethodPost, "/tg/webhook", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()

		server.Handler().ServeHTTP(resp, req)

		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("队列堆积应触发背压并返回 503，got=%d", resp.Code)
		}
		if !strings.Contains(resp.Body.String(), "系统繁忙") {
			t.Fatalf("背压返回文案应包含系统繁忙，got=%s", resp.Body.String())
		}
		if receiver.enqueueCalls != 0 {
			t.Fatalf("背压触发后不应继续入队，enqueue_calls=%d", receiver.enqueueCalls)
		}
	})
}

func TestControlPoint233IPBlacklist(t *testing.T) {
	t.Run("控制点 2.3.3 功能验证", func(t *testing.T) {
		payload := `{"update_id":2333}`
		cases := []struct {
			name         string
			blacklist    []string
			remoteAddr   string
			wantCode     int
			wantUpdateID int
		}{
			{
				name:         "命中单个IP黑名单",
				blacklist:    []string{"203.0.113.9"},
				remoteAddr:   "203.0.113.9:9527",
				wantCode:     http.StatusForbidden,
				wantUpdateID: 0,
			},
			{
				name:         "命中CIDR黑名单",
				blacklist:    []string{"198.51.100.0/24"},
				remoteAddr:   "198.51.100.88:80",
				wantCode:     http.StatusForbidden,
				wantUpdateID: 0,
			},
			{
				name:         "未命中黑名单允许入队",
				blacklist:    []string{"203.0.113.9", "198.51.100.0/24"},
				remoteAddr:   "198.51.101.88:80",
				wantCode:     http.StatusOK,
				wantUpdateID: 2333,
			},
			{
				name:         "逗号规则也可被解析",
				blacklist:    []string{"203.0.113.9,198.51.100.0/24"},
				remoteAddr:   "198.51.100.12:443",
				wantCode:     http.StatusForbidden,
				wantUpdateID: 0,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				receiver := &stubUpdateReceiver{accept: true}
				server := NewServer(config.Config{
					WebhookPath:        "/tg/webhook",
					WebhookIPBlacklist: tc.blacklist,
				}, receiver)

				req := httptest.NewRequest(http.MethodPost, "/tg/webhook", strings.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				req.RemoteAddr = tc.remoteAddr
				resp := httptest.NewRecorder()

				server.Handler().ServeHTTP(resp, req)

				if resp.Code != tc.wantCode {
					t.Fatalf("状态码错误: want=%d got=%d", tc.wantCode, resp.Code)
				}
				updates := receiver.snapshot()
				switch tc.wantUpdateID {
				case 0:
					if len(updates) != 0 {
						t.Fatalf("黑名单命中后不应入队，got=%d", len(updates))
					}
				default:
					if len(updates) != 1 || updates[0].UpdateID != tc.wantUpdateID {
						t.Fatalf("请求应成功入队，updates=%+v", updates)
					}
				}
			})
		}
	})
}

func TestControlPoint241WebhookSignatureVerification(t *testing.T) {
	t.Run("控制点 2.4.1 功能验证", func(t *testing.T) {
		payload := `{"update_id":2410}`
		cases := []struct {
			name       string
			secret     string
			header     string
			setHeader  bool
			wantCode   int
			wantUpdate bool
		}{
			{
				name:       "未配置签名密钥时保持兼容",
				secret:     "",
				setHeader:  false,
				wantCode:   http.StatusOK,
				wantUpdate: true,
			},
			{
				name:       "签名匹配时允许请求",
				secret:     "cp241-secret",
				header:     "cp241-secret",
				setHeader:  true,
				wantCode:   http.StatusOK,
				wantUpdate: true,
			},
			{
				name:       "签名缺失时拒绝请求",
				secret:     "cp241-secret",
				setHeader:  false,
				wantCode:   http.StatusUnauthorized,
				wantUpdate: false,
			},
			{
				name:       "签名不匹配时拒绝请求",
				secret:     "cp241-secret",
				header:     "other-secret",
				setHeader:  true,
				wantCode:   http.StatusUnauthorized,
				wantUpdate: false,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				receiver := &stubUpdateReceiver{accept: true}
				server := NewServer(config.Config{
					WebhookPath:        "/tg/webhook",
					WebhookSecretToken: tc.secret,
				}, receiver)

				req := httptest.NewRequest(http.MethodPost, "/tg/webhook", strings.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				if tc.setHeader {
					req.Header.Set(telegramWebhookSecretHeader, tc.header)
				}
				resp := httptest.NewRecorder()

				server.Handler().ServeHTTP(resp, req)

				if resp.Code != tc.wantCode {
					t.Fatalf("状态码错误: want=%d got=%d", tc.wantCode, resp.Code)
				}
				updates := receiver.snapshot()
				if tc.wantUpdate {
					if len(updates) != 1 || updates[0].UpdateID != 2410 {
						t.Fatalf("请求应成功入队，updates=%+v", updates)
					}
					return
				}
				if len(updates) != 0 {
					t.Fatalf("请求不应入队，got=%d", len(updates))
				}
			})
		}
	})
}

func TestWebhookHandlerAuditTrailIntegrated(t *testing.T) {
	receiver := &stubUpdateReceiver{accept: true}
	server := NewServer(config.Config{WebhookPath: "/tg/webhook"}, receiver)

	var sink bytes.Buffer
	server.auditTrail = audit.NewHTTPAuditTrail(log.New(&sink, "", 0))

	req := httptest.NewRequest(http.MethodPost, "/tg/webhook?scene=cp245", strings.NewReader(`{"update_id":2451}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.80:9000"
	resp := httptest.NewRecorder()

	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("Webhook 审计集成场景状态码错误: %d", resp.Code)
	}

	logLine := sink.String()
	checks := []string{
		"[api_audit]",
		"method=POST",
		"path=/tg/webhook",
		"source_ip=203.0.113.80",
		"params=query=scene=cp245 body={\"update_id\":2451}",
		"result=status=200 body={\"ok\":true}",
	}
	for _, want := range checks {
		if !strings.Contains(logLine, want) {
			t.Fatalf("Webhook 审计日志缺少字段 %q，got=%s", want, logLine)
		}
	}
}

func TestNormalizeWebhookPath(t *testing.T) {
	if got := normalizeWebhookPath(""); got != "/tg/webhook" {
		t.Fatalf("默认路径错误: %s", got)
	}
	if got := normalizeWebhookPath("tg/webhook"); got != "/tg/webhook" {
		t.Fatalf("自动补全路径错误: %s", got)
	}
	if got := normalizeWebhookPath("/custom"); got != "/custom" {
		t.Fatalf("自定义路径错误: %s", got)
	}
}

func TestNewIPBlacklistIgnoresInvalidRules(t *testing.T) {
	blacklist := newIPBlacklist([]string{"bad-rule", "203.0.113.8"})
	if blacklist == nil {
		t.Fatalf("黑名单初始化失败")
	}
	req := httptest.NewRequest(http.MethodPost, "/tg/webhook", nil)
	req.RemoteAddr = "203.0.113.8:80"
	addr, ok := requestRemoteAddrIP(req)
	if !ok {
		t.Fatalf("RemoteAddr 解析失败")
	}
	if !blacklist.Contains(addr) {
		t.Fatalf("合法 IP 规则应生效")
	}
}
