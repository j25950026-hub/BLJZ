package admin

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go-bot/internal/audit"
	"go-bot/internal/config"
)

func TestAdminAPIUsesConsistentJSONEnvelope(t *testing.T) {
	server := newTestSessionAdminServer(t)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.handler().ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("未登录 API 应返回 401: got=%d body=%s", unauthorizedResp.Code, unauthorizedResp.Body.String())
	}
	if got := unauthorizedResp.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("未登录 API 应返回 JSON: %q", got)
	}
	if body := unauthorizedResp.Body.String(); !strings.Contains(body, `"ok":false`) || !strings.Contains(body, `"error"`) {
		t.Fatalf("未登录 API 应使用统一错误 envelope: %s", body)
	}

	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	meReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		meReq.AddCookie(cookie)
	}
	meResp := httptest.NewRecorder()
	server.handler().ServeHTTP(meResp, meReq)
	if meResp.Code != http.StatusOK {
		t.Fatalf("已登录 API 应返回 200: got=%d body=%s", meResp.Code, meResp.Body.String())
	}
	if body := meResp.Body.String(); !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"data"`) {
		t.Fatalf("已登录 API 应使用统一成功 envelope: %s", body)
	}
}

func TestAdminAPIDeniesMissingSessionAndCrossOriginPosts(t *testing.T) {
	server := newTestSessionAdminServer(t)

	missingSessionReq := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
	missingSessionResp := httptest.NewRecorder()
	server.handler().ServeHTTP(missingSessionResp, missingSessionReq)
	if missingSessionResp.Code != http.StatusUnauthorized {
		t.Fatalf("缺 session 的 API POST 应返回 401: got=%d body=%s", missingSessionResp.Code, missingSessionResp.Body.String())
	}

	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	crossReq := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
	crossReq.Header.Set("Origin", "https://evil.example")
	crossReq.Host = "admin.example"
	for _, cookie := range loginResp.Result().Cookies() {
		crossReq.AddCookie(cookie)
	}
	crossResp := httptest.NewRecorder()
	server.handler().ServeHTTP(crossResp, crossReq)
	if crossResp.Code != http.StatusForbidden {
		t.Fatalf("跨站 API POST 应返回 403: got=%d body=%s", crossResp.Code, crossResp.Body.String())
	}
	if got := crossResp.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("跨站 API POST 应返回 JSON: %q", got)
	}
	if body := crossResp.Body.String(); !strings.Contains(body, `"ok":false`) || !strings.Contains(body, `"error"`) {
		t.Fatalf("跨站 API POST 应使用统一错误 envelope: %s", body)
	}
}

func TestAdminAPIDeniesDifferentSchemeOrPortOrigins(t *testing.T) {
	server := newTestSessionAdminServer(t)
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	cases := []struct {
		name   string
		origin string
	}{
		{name: "scheme", origin: "https://admin.example"},
		{name: "port", origin: "http://admin.example:444"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
			req.Header.Set("Origin", tc.origin)
			req.Host = "admin.example"
			for _, cookie := range loginResp.Result().Cookies() {
				req.AddCookie(cookie)
			}
			resp := httptest.NewRecorder()
			server.handler().ServeHTTP(resp, req)
			if resp.Code != http.StatusForbidden {
				t.Fatalf("不同源应返回 403: got=%d body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestAdminAPIRequiresSessionEvenWhenLegacyTokenExists(t *testing.T) {
	server := newTestSessionAdminServer(t)
	server.cfg.AdminToken = "legacy-token"

	req := httptest.NewRequest(http.MethodGet, "/admin/api/me?token=legacy-token", nil)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("新后台 API 不应接受 legacy token 绕过 session: got=%d body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, `"ok":false`) || !strings.Contains(body, `"error"`) {
		t.Fatalf("API token 绕过拒绝应返回统一 JSON: %s", body)
	}
}

func TestAdminRootIgnoresLegacyTokenWhenLoginDisabled(t *testing.T) {
	server := NewServer(config.Config{AdminToken: "legacy-token"}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin?token=legacy-token", nil)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("登录未配置时 /admin 不应接受 legacy token: got=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminRootRequiresSessionEvenWhenLegacyTokenExists(t *testing.T) {
	server := newTestSessionAdminServer(t)
	server.cfg.AdminToken = "legacy-token"

	req := httptest.NewRequest(http.MethodGet, "/admin?token=legacy-token", nil)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("未登录访问 /admin 应显示登录页: got=%d body=%s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), `<div id="app"`) {
		t.Fatalf("新后台壳不应被 legacy token 直接打开: %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "Google Authenticator") {
		t.Fatalf("应显示 TOTP 登录页: %s", resp.Body.String())
	}
}

func TestAdminLogoutReturnsJSONEnvelopeForMethodNotAllowed(t *testing.T) {
	server := newTestSessionAdminServer(t)
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/logout", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /admin/api/logout 应返回 405: got=%d body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("GET /admin/api/logout 应返回 JSON: %q", got)
	}
}

func TestAdminAPIFailsClosedWhenLoginIsNotConfigured(t *testing.T) {
	server := NewServer(config.Config{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("登录未配置时新后台 API 应 fail-closed: got=%d body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, `"ok":false`) || !strings.Contains(body, `"error"`) {
		t.Fatalf("登录未配置时 API 应返回统一 JSON: %s", body)
	}
}

func TestAdminRootFailsClosedWhenNoAdminCredentialConfigured(t *testing.T) {
	server := NewServer(config.Config{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 且无登录配置时 /admin 应拒绝访问: got=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminAPIAuditsLoginAndSensitiveChanges(t *testing.T) {
	server := newTestSessionAdminServer(t)
	var sink bytes.Buffer
	server.auditTrail = audit.NewHTTPAuditTrail(log.New(&sink, "", 0))

	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		logoutReq.AddCookie(cookie)
	}
	logoutResp := httptest.NewRecorder()
	server.handler().ServeHTTP(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("退出应成功: got=%d body=%s", logoutResp.Code, logoutResp.Body.String())
	}

	logs := sink.String()
	for _, want := range []string{
		"path=/admin/login",
		"result=status=200",
		"path=/admin/api/logout",
		"method=POST",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("后台 API 审计缺少 %q，got=%s", want, logs)
		}
	}
}
