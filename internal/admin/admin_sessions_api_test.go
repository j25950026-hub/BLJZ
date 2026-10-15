package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-bot/internal/db"
)

func TestAdminSessionsAPIPermissionsAndExpiry(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "sessions-api.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)

	primaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	secondaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]

	csrf := getAdminCSRF(t, server, primaryCookie)
	reauth := postAdminReauth(t, server, primaryCookie, csrf, "sessions")
	if reauth.Code != http.StatusOK {
		t.Fatalf("主设备重新认证失败: status=%d body=%s", reauth.Code, reauth.Body.String())
	}
	reauthCookie := cookieNamed(reauth.Result().Cookies(), adminReauthCookieName)
	if reauthCookie == nil {
		t.Fatalf("重新认证响应缺少 reauth cookie: %+v", reauth.Result().Cookies())
	}
	if reauthCookie.Name != adminReauthCookieName || reauthCookie.Path != "/admin" || !reauthCookie.HttpOnly || reauthCookie.SameSite != http.SameSiteStrictMode || reauthCookie.MaxAge <= 0 || reauthCookie.MaxAge > int(adminReauthTTL.Seconds()) {
		t.Fatalf("reauth cookie 属性异常: %+v", reauthCookie)
	}

	list := getAdminSessions(t, server, primaryCookie, reauthCookie)
	if list.Code != http.StatusOK {
		t.Fatalf("主设备设备列表失败: status=%d body=%s", list.Code, list.Body.String())
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []struct {
				SessionHandle string `json:"sessionHandle"`
				IsPrimary     bool   `json:"isPrimary"`
				IsCurrent     bool   `json:"isCurrent"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析设备列表失败: %v", err)
	}
	if !envelope.OK || len(envelope.Data.Items) != 2 {
		t.Fatalf("主设备应看到两台设备: %s", list.Body.String())
	}
	secondaryHandle := ""
	for _, item := range envelope.Data.Items {
		if item.IsPrimary && !item.IsCurrent {
			t.Fatalf("主设备列表中的 primary 必须是当前设备: %+v", item)
		}
		if !item.IsPrimary {
			secondaryHandle = item.SessionHandle
		}
	}
	if secondaryHandle == "" {
		t.Fatalf("未找到次设备 handle: %s", list.Body.String())
	}
	for _, forbidden := range []string{"tokenHash", "TokenHash", server.cfg.AdminTOTPSecret} {
		if strings.Contains(list.Body.String(), forbidden) {
			t.Fatalf("设备列表泄露敏感字段 %q: %s", forbidden, list.Body.String())
		}
	}

	secondaryList := getAdminSessions(t, server, secondaryCookie)
	if secondaryList.Code != http.StatusOK {
		t.Fatalf("次设备读取自身列表失败: status=%d body=%s", secondaryList.Code, secondaryList.Body.String())
	}
	var secondaryEnvelope struct {
		Data struct {
			Items []struct {
				SessionHandle string `json:"sessionHandle"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(secondaryList.Body.Bytes(), &secondaryEnvelope); err != nil {
		t.Fatalf("解析次设备列表失败: %v", err)
	}
	if len(secondaryEnvelope.Data.Items) != 1 {
		t.Fatalf("次设备只能看到自己: %s", secondaryList.Body.String())
	}

	tooFar := time.Now().UTC().Add(31 * 24 * time.Hour).Format(time.RFC3339)
	tooFarResp := postAdminSessionJSON(t, server, "/admin/api/sessions/expiry", primaryCookie, reauthCookie, csrf, `{"sessionHandle":"`+secondaryHandle+`","expiresAt":"`+tooFar+`"}`)
	if tooFarResp.Code != http.StatusBadRequest {
		t.Fatalf("超过 30 天的过期时间应拒绝: status=%d body=%s", tooFarResp.Code, tooFarResp.Body.String())
	}

	validExpiry := time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	validResp := postAdminSessionJSON(t, server, "/admin/api/sessions/expiry", primaryCookie, reauthCookie, csrf, `{"sessionHandle":"`+secondaryHandle+`","expiresAt":"`+validExpiry+`"}`)
	if validResp.Code != http.StatusOK {
		t.Fatalf("合法过期时间应成功: status=%d body=%s", validResp.Code, validResp.Body.String())
	}

	missingCSRF := postAdminSessionJSON(t, server, "/admin/api/sessions/revoke-all-others", primaryCookie, reauthCookie, "", `{}`)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF 应拒绝写操作: status=%d body=%s", missingCSRF.Code, missingCSRF.Body.String())
	}

	revokeAll := postAdminSessionJSON(t, server, "/admin/api/sessions/revoke-all-others", primaryCookie, reauthCookie, csrf, `{}`)
	if revokeAll.Code != http.StatusOK {
		t.Fatalf("主设备退出其他设备失败: status=%d body=%s", revokeAll.Code, revokeAll.Body.String())
	}
	secondaryMe := getAdminMeWithCookie(server, secondaryCookie)
	if secondaryMe.Code != http.StatusUnauthorized {
		t.Fatalf("退出其他设备后次设备应立即失效: status=%d body=%s", secondaryMe.Code, secondaryMe.Body.String())
	}
}

func TestAdminReauthCannotReplayAcrossSessionsAndQRIsProtected(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "reauth-api.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)
	primaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	secondaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]

	csrf := getAdminCSRF(t, server, primaryCookie)
	reauth := postAdminReauth(t, server, primaryCookie, csrf, "qr")
	if reauth.Code != http.StatusOK {
		t.Fatalf("QR 重新认证失败: status=%d body=%s", reauth.Code, reauth.Body.String())
	}
	reauthCookie := cookieNamed(reauth.Result().Cookies(), adminReauthCookieName)
	if reauthCookie == nil {
		t.Fatalf("重新认证响应缺少 reauth cookie: %+v", reauth.Result().Cookies())
	}

	qr := getAdminSecurityQR(t, server, primaryCookie, reauthCookie)
	if qr.Code != http.StatusOK || !strings.HasPrefix(qr.Header().Get("Content-Type"), "image/png") {
		t.Fatalf("合法重新认证后 QR 获取失败: status=%d headers=%v", qr.Code, qr.Header())
	}
	if strings.Contains(qr.Body.String(), server.cfg.AdminTOTPSecret) || strings.Contains(qr.Body.String(), "otpauth://") {
		t.Fatalf("安全设置 QR 不应返回明文 URI")
	}

	replayed := getAdminSecurityQR(t, server, secondaryCookie, reauthCookie)
	if replayed.Code != http.StatusUnauthorized {
		t.Fatalf("重新认证 cookie 不能跨 session 重放: status=%d body=%s", replayed.Code, replayed.Body.String())
	}
}

func TestAdminTransferPrimaryRevokesOldPrimary(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "transfer-api.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)
	primaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	targetCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	csrf := getAdminCSRF(t, server, primaryCookie)
	reauth := postAdminReauth(t, server, primaryCookie, csrf, "sessions")
	if reauth.Code != http.StatusOK {
		t.Fatalf("主设备重新认证失败: %s", reauth.Body.String())
	}
	reauthCookie := cookieNamed(reauth.Result().Cookies(), adminReauthCookieName)
	if reauthCookie == nil {
		t.Fatalf("重新认证响应缺少 reauth cookie: %+v", reauth.Result().Cookies())
	}
	list := getAdminSessions(t, server, primaryCookie, reauthCookie)
	var payload struct {
		Data struct {
			Items []struct {
				SessionHandle string `json:"sessionHandle"`
				IsPrimary     bool   `json:"isPrimary"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析设备列表失败: %v", err)
	}
	targetHandle := ""
	for _, item := range payload.Data.Items {
		if !item.IsPrimary {
			targetHandle = item.SessionHandle
		}
	}
	if targetHandle == "" {
		t.Fatalf("找不到转移目标")
	}
	transfer := postAdminSessionJSON(t, server, "/admin/api/sessions/transfer-primary", primaryCookie, reauthCookie, csrf, `{"sessionHandle":"`+targetHandle+`"}`)
	if transfer.Code != http.StatusOK {
		t.Fatalf("转移主设备失败: status=%d body=%s", transfer.Code, transfer.Body.String())
	}
	if got := getAdminMeWithCookie(server, primaryCookie).Code; got != http.StatusUnauthorized {
		t.Fatalf("转移后旧主设备应失效: status=%d", got)
	}
	if got := getAdminMeWithCookie(server, targetCookie).Code; got != http.StatusOK {
		t.Fatalf("转移后目标设备应保持有效: status=%d", got)
	}
}

func getAdminCSRF(t *testing.T, server *Server, sessionCookie *http.Cookie) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/csrf", nil)
	req.AddCookie(sessionCookie)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("获取 CSRF 失败: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil || payload.Data.Token == "" {
		t.Fatalf("CSRF 响应异常: %s", resp.Body.String())
	}
	return payload.Data.Token
}

func setAdminTestSameOrigin(req *http.Request) {
	if req == nil {
		return
	}
	req.Host = "admin.example"
	req.Header.Set("Origin", "http://admin.example")
}

func postAdminReauth(t *testing.T, server *Server, sessionCookie *http.Cookie, csrf, action string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/api/security/reauth", strings.NewReader(`{"action":"`+action+`","totp":"`+testTOTPCode(t, testAdminTOTPSecret, time.Now())+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	setAdminTestSameOrigin(req)
	req.AddCookie(sessionCookie)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	return resp
}

func getAdminSessions(t *testing.T, server *Server, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/sessions", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	return resp
}

func postAdminSessionJSON(t *testing.T, server *Server, path string, sessionCookie, reauthCookie *http.Cookie, csrf, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	setAdminTestSameOrigin(req)
	req.AddCookie(sessionCookie)
	if reauthCookie != nil {
		req.AddCookie(reauthCookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	return resp
}

func getAdminSecurityQR(t *testing.T, server *Server, sessionCookie, reauthCookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/security/qr", nil)
	req.AddCookie(sessionCookie)
	req.AddCookie(reauthCookie)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	return resp
}

func cookieNamed(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == name {
			return cookie
		}
	}
	return nil
}
