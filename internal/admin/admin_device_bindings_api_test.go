package admin

import (
	"bytes"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/internal/db"
)

func TestAdminDeviceBindingStartReturnsProtectedPNG(t *testing.T) {
	database, err := newAdminDeviceBindingTestDatabase(t)
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)
	primaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	csrf := getAdminCSRF(t, server, primaryCookie)
	reauth := postAdminReauth(t, server, primaryCookie, csrf, "sessions")
	if reauth.Code != http.StatusOK {
		t.Fatalf("主设备重新认证失败: %s", reauth.Body.String())
	}
	reauthCookie := cookieNamed(reauth.Result().Cookies(), adminReauthCookieName)
	if reauthCookie == nil {
		t.Fatalf("缺少 sessions reauth cookie")
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/device-bindings/start", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	setAdminTestSameOrigin(req)
	req.AddCookie(primaryCookie)
	req.AddCookie(reauthCookie)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || resp.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("绑定 start 应返回 PNG: status=%d headers=%v body=%s", resp.Code, resp.Header(), resp.Body.String())
	}
	if _, err := png.Decode(bytes.NewReader(resp.Body.Bytes())); err != nil {
		t.Fatalf("绑定二维码不是有效 PNG: %v", err)
	}
	for _, header := range []string{"no-store", "private"} {
		if !strings.Contains(resp.Header().Get("Cache-Control"), header) {
			t.Fatalf("绑定二维码缺少 Cache-Control %q: %v", header, resp.Header())
		}
	}
	if resp.Header().Get("X-Admin-Binding-ID") == "" || resp.Header().Get("X-Admin-Binding-Expires-At") == "" {
		t.Fatalf("绑定二维码缺少元数据响应头: %v", resp.Header())
	}
}

func TestAdminDeviceBindingRequiresPrimaryReauthAndRejectsInvalidClaim(t *testing.T) {
	database, err := newAdminDeviceBindingTestDatabase(t)
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)
	primary := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	secondary := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	primaryCSRF := getAdminCSRF(t, server, primary)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/device-bindings/start", nil)
	req.Header.Set("X-CSRF-Token", primaryCSRF)
	setAdminTestSameOrigin(req)
	req.AddCookie(secondary)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("次设备不能 start: status=%d body=%s", resp.Code, resp.Body.String())
	}
	claim := httptest.NewRequest(http.MethodPost, "/admin/api/device-bindings/claim", strings.NewReader("{\"token\":\"invalid\"}"))
	claim.Header.Set("Content-Type", "application/json")
	setAdminTestSameOrigin(claim)
	claimResp := httptest.NewRecorder()
	server.handler().ServeHTTP(claimResp, claim)
	if claimResp.Code != http.StatusConflict {
		t.Fatalf("非法绑定令牌应返回 409: status=%d body=%s", claimResp.Code, claimResp.Body.String())
	}
}

func newAdminDeviceBindingTestDatabase(t *testing.T) (*db.Database, error) {
	t.Helper()
	return db.NewDatabase(filepath.Join(t.TempDir(), "device-binding-api.db"))
}
