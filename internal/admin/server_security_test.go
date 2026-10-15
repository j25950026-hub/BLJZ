package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-bot/internal/bot"
	"go-bot/internal/config"
)

func TestAdminSecurityHeadersAreSet(t *testing.T) {
	server := NewServer(config.Config{AdminToken: "secure-token"}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin?token=secure-token", nil)
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if got := resp.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("缺少 nosniff 安全头: %q", got)
	}
	if got := resp.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("缺少 no-referrer 安全头: %q", got)
	}
	csp := resp.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "object-src 'none'") || !strings.Contains(csp, "base-uri 'self'") {
		t.Fatalf("CSP 缺少基础 XSS 防护: %q", csp)
	}
}

func TestAdminPostRejectsCrossOrigin(t *testing.T) {
	server := NewServer(config.Config{AdminToken: "secure-token"}, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/kill-switch?token=secure-token", strings.NewReader("reason=test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.Host = "autobookbl.site"
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("跨站 POST 应被拒绝: got=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestTGWebAppExecRejectsOversizedBody(t *testing.T) {
	server := NewServer(config.Config{}, nil, &bot.App{})
	body := strings.NewReader(`{"` + strings.Repeat("x", 80*1024) + `":"y"}`)
	req := httptest.NewRequest(http.MethodPost, "/tg/webapp/exec", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大 WebApp 请求体应返回 413: got=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestTGWebAppPrivateActionUsesContactAdminInsteadOfTrialApply(t *testing.T) {
	if isTGWebAppPrivateAction("申请试用") {
		t.Fatalf("WebApp 不应继续开放申请试用动作")
	}
	if !isTGWebAppPrivateAction("联系管理员") {
		t.Fatalf("WebApp 应开放联系管理员动作")
	}
}
