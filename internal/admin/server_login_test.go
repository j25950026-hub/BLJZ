package admin

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-bot/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func TestAdminLoginRequiresPasswordAndTOTP(t *testing.T) {
	server := newTestSessionAdminServer(t)

	for _, tc := range []struct {
		name string
		form url.Values
	}{
		{
			name: "缺密码",
			form: url.Values{
				"username": {"root"},
				"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
			},
		},
		{
			name: "缺 TOTP",
			form: url.Values{
				"username": {"root"},
				"password": {"correct-password"},
			},
		},
		{
			name: "错误账号",
			form: url.Values{
				"username": {"other"},
				"password": {"correct-password"},
				"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
			},
		},
		{
			name: "错误 TOTP",
			form: url.Values{
				"username": {"root"},
				"password": {"correct-password"},
				"totp":     {"000000"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := postAdminLogin(server, tc.form)
			if resp.Code != http.StatusUnauthorized {
				t.Fatalf("登录失败应返回 401: got=%d body=%s", resp.Code, resp.Body.String())
			}
			if got := resp.Result().Cookies(); len(got) != 0 {
				t.Fatalf("登录失败不应创建 cookie: %#v", got)
			}
		})
	}
}

func TestAdminSessionAllowsAPIAccessAndLogoutInvalidatesIt(t *testing.T) {
	server := newTestSessionAdminServer(t)
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}
	cookies := loginResp.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("登录成功应返回 session cookie")
	}

	meReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	for _, cookie := range cookies {
		meReq.AddCookie(cookie)
	}
	meResp := httptest.NewRecorder()
	server.handler().ServeHTTP(meResp, meReq)
	if meResp.Code != http.StatusOK {
		t.Fatalf("/admin/api/me 应允许 session 访问: got=%d body=%s", meResp.Code, meResp.Body.String())
	}
	if !strings.Contains(meResp.Body.String(), `"username":"root"`) {
		t.Fatalf("/admin/api/me 应返回当前管理员: %s", meResp.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	for _, cookie := range cookies {
		logoutReq.AddCookie(cookie)
	}
	logoutResp := httptest.NewRecorder()
	server.handler().ServeHTTP(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("/admin/logout 应成功: got=%d body=%s", logoutResp.Code, logoutResp.Body.String())
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	for _, cookie := range cookies {
		afterReq.AddCookie(cookie)
	}
	afterResp := httptest.NewRecorder()
	server.handler().ServeHTTP(afterResp, afterReq)
	if afterResp.Code != http.StatusUnauthorized {
		t.Fatalf("退出后 session 应失效: got=%d body=%s", afterResp.Code, afterResp.Body.String())
	}
}

func TestAdminStaticAppServesShell(t *testing.T) {
	server := newTestSessionAdminServer(t)

	guestReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	guestResp := httptest.NewRecorder()
	server.handler().ServeHTTP(guestResp, guestReq)
	if guestResp.Code != http.StatusOK {
		t.Fatalf("未登录访问 /admin 应显示登录页: got=%d body=%s", guestResp.Code, guestResp.Body.String())
	}
	if !strings.Contains(guestResp.Body.String(), "Google Authenticator") {
		t.Fatalf("未登录 /admin 应显示 TOTP 登录页: %s", guestResp.Body.String())
	}

	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}

	shellReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		shellReq.AddCookie(cookie)
	}
	shellResp := httptest.NewRecorder()
	server.handler().ServeHTTP(shellResp, shellReq)
	if shellResp.Code != http.StatusOK {
		t.Fatalf("登录后 /admin 应返回前端壳: got=%d body=%s", shellResp.Code, shellResp.Body.String())
	}
	if !strings.Contains(shellResp.Body.String(), `<div id="app"`) || !strings.Contains(shellResp.Body.String(), `/admin/assets/app.js`) {
		t.Fatalf("登录后 /admin 应返回静态前端壳: %s", shellResp.Body.String())
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		assetReq.AddCookie(cookie)
	}
	assetResp := httptest.NewRecorder()
	server.handler().ServeHTTP(assetResp, assetReq)
	if assetResp.Code != http.StatusOK {
		t.Fatalf("静态资源应可访问: got=%d body=%s", assetResp.Code, assetResp.Body.String())
	}
	if !strings.Contains(assetResp.Body.String(), "运营总控台") {
		t.Fatalf("静态 JS 应包含后台入口文案: %s", assetResp.Body.String())
	}
}

func TestAdminStaticAppFetchesConsoleAPI(t *testing.T) {
	server := newTestSessionAdminServer(t)
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	body := resp.Body.String()
	for _, want := range []string{
		"/admin/api/console",
		"renderConsole",
		"safeAdminHref",
		`item.visibility && item.visibility !== "admin_only"`,
		"待办中心",
		"群健康状态",
		"下一步建议",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("后台静态 JS 缺少 %q: %s", want, body)
		}
	}
}

func TestAdminStaticAppIncludesGroupWorkbench(t *testing.T) {
	server := newTestSessionAdminServer(t)
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	body := resp.Body.String()
	for _, want := range []string{
		"/admin/api/groups",
		"/admin/api/group-views",
		"/admin/api/groups/batch-preview",
		"/admin/api/groups/batch-apply",
		"renderGroupWorkbench",
		"saveGroupView",
		"previewGroupBatch",
		"applyGroupBatch",
		"beforeunload",
		"批量操作预演",
		"操作结果回执",
		"未保存",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("群组工作台静态 JS 缺少 %q: %s", want, body)
		}
	}
}

func TestAdminStaticAppIncludesPermissionsCenter(t *testing.T) {
	server := newTestSessionAdminServer(t)
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	body := resp.Body.String()
	for _, want := range []string{
		"/admin/api/permissions",
		"renderPermissionsCenter",
		"loadPermissionsCenter",
		"人员与权限中心",
		"全局权限人",
		"全局操作人",
		"群操作人",
		"upsert_global_owner",
		"remove_global_owner",
		"upsert_global_operator",
		"remove_global_operator",
		"添加全局权限人",
		"添加全局操作人",
		"删除",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("权限中心静态 JS 缺少 %q: %s", want, body)
		}
	}
}

func TestAdminSessionGroupRouteServesStaticAppShell(t *testing.T) {
	server := newTestSessionAdminServer(t)
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/groups", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("登录后 /admin/groups 应返回前端壳: got=%d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, `<div id="app"`) || !strings.Contains(body, `/admin/assets/app.js`) {
		t.Fatalf("登录后 /admin/groups 应返回静态前端壳: %s", body)
	}
}

func TestAdminSessionPermissionsRouteServesStaticAppShell(t *testing.T) {
	server := newTestSessionAdminServer(t)
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/permissions", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("登录后 /admin/permissions 应返回前端壳: got=%d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, `<div id="app"`) || !strings.Contains(body, `/admin/assets/app.js`) {
		t.Fatalf("登录后 /admin/permissions 应返回静态前端壳: %s", body)
	}
}

func TestAdminGuestGroupRouteShowsLoginPage(t *testing.T) {
	server := newTestSessionAdminServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/groups", nil)
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("未登录访问 /admin/groups 应显示登录页: got=%d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, want := range []string{"后台登录", "Google Authenticator", "login-form"} {
		if !strings.Contains(body, want) {
			t.Fatalf("/admin/groups 未登录页缺少 %q: %s", want, body)
		}
	}
}

func TestAdminGroupRouteKeepsLegacyTokenAccessWhenLoginEnabled(t *testing.T) {
	database := newAdminGroupWorkbenchDatabase(t)
	server := newTestSessionAdminServer(t)
	server.cfg.AdminToken = "legacy-token"
	server.db = database

	req := httptest.NewRequest(http.MethodGet, "/admin/groups?token=legacy-token", nil)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("legacy token 访问 /admin/groups 应保留旧群管理页: got=%d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, want := range []string{"群组管理", "重点夜班群", "夜班,重点"} {
		if !strings.Contains(body, want) {
			t.Fatalf("legacy token 旧群管理页缺少 %q: %s", want, body)
		}
	}
	if strings.Contains(body, `/admin/assets/app.js`) {
		t.Fatalf("legacy token 访问 /admin/groups 不应返回新 SPA 壳: %s", body)
	}
}

func TestAdminPermissionsRouteKeepsLegacyTokenAccessWhenLoginEnabled(t *testing.T) {
	database := newAdminPermissionsAPIDatabase(t)
	server := newTestSessionAdminServer(t)
	server.cfg.AdminToken = "legacy-token"
	server.db = database

	req := httptest.NewRequest(http.MethodGet, "/admin/permissions?token=legacy-token&chat_id=-100901", nil)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("legacy token 访问 /admin/permissions 应保留旧权限页: got=%d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, want := range []string{"权限管理", "全局权限人", "全局操作人", "群操作人", "owner_a", "global_op", "group_op_a"} {
		if !strings.Contains(body, want) {
			t.Fatalf("legacy token 旧权限页缺少 %q: %s", want, body)
		}
	}
	if strings.Contains(body, `/admin/assets/app.js`) {
		t.Fatalf("legacy token 访问 /admin/permissions 不应返回新 SPA 壳: %s", body)
	}
}

func TestAdminLoginPageUsesFetchRedirect(t *testing.T) {
	server := newTestSessionAdminServer(t)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	body := resp.Body.String()
	for _, want := range []string{
		`fetch("/admin/login"`,
		`window.location.href = "/admin"`,
		`login-form`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("登录页缺少浏览器跳转逻辑 %q: %s", want, body)
		}
	}
}

func TestAdminSessionCookieSecureDependsOnRequestScheme(t *testing.T) {
	cases := []struct {
		name         string
		forwardProto string
		wantSecure   bool
	}{
		{name: "http", wantSecure: false},
		{name: "https", forwardProto: "https", wantSecure: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newTestSessionAdminServer(t)
			form := url.Values{
				"username": {"root"},
				"password": {"correct-password"},
				"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
			}
			req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.forwardProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwardProto)
			}
			resp := httptest.NewRecorder()
			server.handler().ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("登录应成功: got=%d body=%s", resp.Code, resp.Body.String())
			}
			cookies := resp.Result().Cookies()
			if len(cookies) == 0 {
				t.Fatalf("登录应返回 cookie")
			}
			if got := cookies[0].Secure; got != tc.wantSecure {
				t.Fatalf("cookie Secure 错误: got=%v want=%v", got, tc.wantSecure)
			}
		})
	}
}

const testAdminTOTPSecret = "JBSWY3DPEHPK3PXP"

func newTestSessionAdminServer(t *testing.T) *Server {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成测试密码哈希失败: %v", err)
	}
	return NewServer(config.Config{
		AdminUsername:     "root",
		AdminPasswordHash: string(hash),
		AdminTOTPSecret:   testAdminTOTPSecret,
	}, nil, nil)
}

func postAdminLogin(server *Server, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	return resp
}

func testTOTPCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		t.Fatalf("解码 TOTP secret 失败: %v", err)
	}
	counter := uint64(at.Unix() / 30)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (int(sum[offset])&0x7f)<<24 |
		(int(sum[offset+1])&0xff)<<16 |
		(int(sum[offset+2])&0xff)<<8 |
		(int(sum[offset+3]) & 0xff)
	return "000000"[len(strconv.Itoa(code%1000000)):] + strconv.Itoa(code%1000000)
}
