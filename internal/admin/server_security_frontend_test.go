package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminSecurityFrontendContainsSetupAndDeviceWorkflows(t *testing.T) {
	server := newTestSessionAdminServer(t)
	for _, path := range []string{"/admin/setup", "/admin/security"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		server.handler().ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s 应返回前端入口: status=%d body=%s", path, resp.Code, resp.Body.String())
		}
	}
	login := postAdminLogin(server, adminLoginForm(t))
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("准备静态资源测试的登录失败: status=%d body=%s", login.Code, login.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	req.AddCookie(login.Result().Cookies()[0])
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	body := resp.Body.String()
	for _, want := range []string{
		"/admin/setup",
		"/admin/security",
		"/admin/api/setup/start",
		"/admin/api/setup/qr",
		"/admin/api/setup/complete",
		"/admin/api/sessions",
		"/admin/api/security/reauth",
		"/admin/api/security/qr",
		"/admin/device-bind",
		"/admin/api/device-bindings/start",
		"/admin/api/device-bindings/pending",
		"/admin/api/device-bindings/approve",
		"/admin/api/device-bindings/claim",
		"/admin/api/device-bindings/status",
		"/admin/api/device-bindings/complete",
		"主设备",
		"设备会话",
		"过期时间",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("静态前端缺少 %q: %s", want, body)
		}
	}
	for _, secret := range []string{"otpauth://", "ADMIN_TOTP_SECRET", "passwordHash"} {
		if strings.Contains(body, secret) {
			t.Fatalf("静态前端不应包含敏感配置标识 %q", secret)
		}
	}
}
