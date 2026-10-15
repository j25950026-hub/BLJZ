package audit

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAuditTrailRedactsSensitiveQueryAndJSONBody(t *testing.T) {
	var sink bytes.Buffer
	trail := NewHTTPAuditTrail(log.New(&sink, "", 0))
	handler := trail.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"sig":"response-secret"}`))
	}))

	body := strings.NewReader(`{"uid":10001,"sig":"request-signature","nested":{"admin_token":"nested-token"}}`)
	req := httptest.NewRequest(http.MethodPost, "/tg/webapp/exec?token=admin-token&sig=url-signature", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	logLine := sink.String()
	for _, leaked := range []string{"admin-token", "url-signature", "request-signature", "nested-token", "response-secret"} {
		if strings.Contains(logLine, leaked) {
			t.Fatalf("审计日志泄露敏感值 %q: %s", leaked, logLine)
		}
	}
	if !strings.Contains(logLine, "[REDACTED]") {
		t.Fatalf("审计日志应包含脱敏标记: %s", logLine)
	}
}

func TestHTTPAuditTrailRedactsAdminAuthenticationFields(t *testing.T) {
	var sink bytes.Buffer
	trail := NewHTTPAuditTrail(log.New(&sink, "", 0))
	handler := trail.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totp":"response-totp","session_token":"response-session"}`))
	}))

	body := strings.NewReader(`{"totp":"request-totp","otp":"request-otp","setup_token":"request-setup","csrf":"request-csrf","cookie":"request-cookie"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/setup/complete?session_token=query-session", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	logLine := sink.String()
	for _, leaked := range []string{"request-totp", "request-otp", "request-setup", "request-csrf", "request-cookie", "query-session", "response-totp", "response-session"} {
		if strings.Contains(logLine, leaked) {
			t.Fatalf("审计日志泄露管理员敏感值 %q: %s", leaked, logLine)
		}
	}
}

func TestHTTPAuditTrailSkipsBinaryAndNoStoreResponseBodies(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		cache       string
		body        string
	}{
		{name: "png", contentType: "image/png", body: "png-secret-payload"},
		{name: "no-store", contentType: "application/json", cache: "no-store, private", body: "no-store-secret-payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sink bytes.Buffer
			trail := NewHTTPAuditTrail(log.New(&sink, "", 0))
			handler := trail.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				if tc.cache != "" {
					w.Header().Set("Cache-Control", tc.cache)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			req := httptest.NewRequest(http.MethodGet, "/admin/api/security/qr", nil)
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if !strings.Contains(resp.Body.String(), tc.body) {
				t.Fatalf("测试响应体未写入: %q", resp.Body.String())
			}
			if strings.Contains(sink.String(), tc.body) {
				t.Fatalf("审计日志不应捕获 %s 响应体: %s", tc.name, sink.String())
			}
		})
	}
}
