package audit

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControlPoint245AuditTrail(t *testing.T) {
	t.Run("控制点 2.4.5 功能验证", func(t *testing.T) {
		var sink bytes.Buffer
		trail := NewHTTPAuditTrail(log.New(&sink, "", 0))

		handler := trail.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("读取请求体失败: %v", err)
			}
			if got := strings.TrimSpace(string(body)); got != `{"command":"开始记账"}` {
				t.Fatalf("请求体透传失败: %s", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"ok":true,"message":"accepted"}`))
		}))

		req := httptest.NewRequest(http.MethodPost, "/tg/webapp/exec?chat_id=-10001", strings.NewReader(`{"command":"开始记账"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.25:8443"
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		if resp.Code != http.StatusAccepted {
			t.Fatalf("状态码错误: %d", resp.Code)
		}
		logLine := sink.String()
		checks := []string{
			"[api_audit]",
			"method=POST",
			"path=/tg/webapp/exec",
			"source_ip=203.0.113.25",
			"params=query=chat_id=-10001 body={\"command\":\"开始记账\"}",
			"result=status=202 body={\"ok\":true,\"message\":\"accepted\"}",
		}
		for _, want := range checks {
			if !strings.Contains(logLine, want) {
				t.Fatalf("审计日志缺少字段 %q，got=%s", want, logLine)
			}
		}
	})
}

func TestCaptureRequestBodyDoesNotConsumeBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audit", strings.NewReader("abc123"))
	body := captureRequestBody(req, 32)
	if body != "abc123" {
		t.Fatalf("请求体截取错误: %s", body)
	}
	payload, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取回填请求体失败: %v", err)
	}
	if got := string(payload); got != "abc123" {
		t.Fatalf("请求体未正确回填: %s", got)
	}
}

func TestHTTPAuditTrailLogsOnPanic(t *testing.T) {
	var sink bytes.Buffer
	trail := NewHTTPAuditTrail(log.New(&sink, "", 0))
	handler := trail.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panic?from=test", nil)
	req.RemoteAddr = "198.51.100.11:443"
	resp := httptest.NewRecorder()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("panic 应继续向上抛出，便于上层统一恢复")
		}
		logLine := sink.String()
		checks := []string{
			"[api_audit]",
			"method=GET",
			"path=/panic",
			"source_ip=198.51.100.11",
			"params=query=from=test",
			"result=status=500 body=panic=boom",
		}
		for _, want := range checks {
			if !strings.Contains(logLine, want) {
				t.Fatalf("panic 场景审计缺少字段 %q，got=%s", want, logLine)
			}
		}
	}()

	handler.ServeHTTP(resp, req)
}

func TestControlPoint335DataMasking(t *testing.T) {
	t.Run("控制点 3.3.5 功能验证", func(t *testing.T) {
		const (
			rawCard    = "4539578763621486"
			rawAddress = "0xd90e2f925da726b50c4ed8d0fb90ad053324f31b"
		)

		var sink bytes.Buffer
		trail := NewHTTPAuditTrail(log.New(&sink, "", 0))
		handler := trail.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"card_no":"` + rawCard + `","address":"` + rawAddress + `"}`))
		}))

		req := httptest.NewRequest(
			http.MethodPost,
			"/audit/masking?card_no="+rawCard+"&address="+rawAddress,
			strings.NewReader(`{"card_no":"`+rawCard+`","address":"`+rawAddress+`"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.91:443"
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("状态码错误: %d", resp.Code)
		}
		logLine := sink.String()
		maskedChecks := []string{
			"453957******1486",
			"0xd90e********************************f31b",
		}
		for _, want := range maskedChecks {
			if !strings.Contains(logLine, want) {
				t.Fatalf("审计日志未命中脱敏字段 %q, got=%s", want, logLine)
			}
		}
		leaks := []string{rawCard, rawAddress}
		for _, leak := range leaks {
			if strings.Contains(logLine, leak) {
				t.Fatalf("审计日志泄露敏感信息 %q, got=%s", leak, logLine)
			}
		}
	})
}
