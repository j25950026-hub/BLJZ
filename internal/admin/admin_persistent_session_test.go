package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-bot/internal/db"
)

func TestAdminPersistentSessionsUsePrimaryAndSecondaryPolicies(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "persistent-session.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	server := newPersistentTestServer(t, database)
	first := postAdminLogin(server, adminLoginForm(t))
	if first.Code != http.StatusOK {
		t.Fatalf("首次登录失败: status=%d body=%s", first.Code, first.Body.String())
	}
	firstCookies := first.Result().Cookies()
	if len(firstCookies) != 1 {
		t.Fatalf("首次登录应返回一个 session cookie，实际=%d", len(firstCookies))
	}
	primaryCookie := firstCookies[0]
	if primaryCookie.SameSite != http.SameSiteStrictMode || !primaryCookie.HttpOnly {
		t.Fatalf("主设备 cookie 安全属性异常: %+v", primaryCookie)
	}
	if primaryCookie.MaxAge <= 24*60*60 {
		t.Fatalf("主设备 cookie 应使用较长持久 Max-Age: %d", primaryCookie.MaxAge)
	}

	second := postAdminLogin(server, adminLoginForm(t))
	if second.Code != http.StatusOK {
		t.Fatalf("第二台设备登录失败: status=%d body=%s", second.Code, second.Body.String())
	}
	secondaryCookies := second.Result().Cookies()
	if len(secondaryCookies) != 1 {
		t.Fatalf("第二次登录应返回一个 session cookie，实际=%d", len(secondaryCookies))
	}
	if secondaryCookies[0].Value == primaryCookie.Value {
		t.Fatalf("不同设备不得复用相同 bearer cookie")
	}
	if secondaryCookies[0].MaxAge <= 0 || secondaryCookies[0].MaxAge > int((30*24*time.Hour).Seconds()) {
		t.Fatalf("次设备 cookie 过期策略异常: %d", secondaryCookies[0].MaxAge)
	}

	primaryHash := sha256.Sum256([]byte(primaryCookie.Value))
	primaryRecord, err := database.GetAdminSessionByTokenHash(context.Background(), hex.EncodeToString(primaryHash[:]))
	if err != nil {
		t.Fatalf("读取主设备 session 失败: %v", err)
	}
	secondaryHash := sha256.Sum256([]byte(secondaryCookies[0].Value))
	secondaryRecord, err := database.GetAdminSessionByTokenHash(context.Background(), hex.EncodeToString(secondaryHash[:]))
	if err != nil {
		t.Fatalf("读取次设备 session 失败: %v", err)
	}
	if primaryRecord.ExpiresAt != nil {
		t.Fatalf("主设备服务端 expires_at 应为 NULL，实际=%v", primaryRecord.ExpiresAt)
	}
	if primaryRecord.SessionHandle == primaryCookie.Value || primaryRecord.SessionHandle == "" {
		t.Fatalf("公开 session handle 必须与 bearer cookie 分离: handle=%q", primaryRecord.SessionHandle)
	}
	if primaryRecord.RemoteAddr == "" {
		t.Fatalf("设备摘要应记录 IP: %+v", primaryRecord)
	}
	if secondaryRecord.ExpiresAt == nil {
		t.Fatalf("次设备服务端 expires_at 不应为 NULL")
	}

	primaryMe := getAdminMeWithCookie(server, primaryCookie)
	if primaryMe.Code != http.StatusOK || !strings.Contains(primaryMe.Body.String(), `"username":"root"`) {
		t.Fatalf("主设备持久 session 访问失败: status=%d body=%s", primaryMe.Code, primaryMe.Body.String())
	}
}

func TestAdminPersistentPrimarySurvivesDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen-session.db")
	database, err := db.NewDatabase(path)
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	server := newPersistentTestServer(t, database)
	serverConfig := server.cfg
	login := postAdminLogin(server, adminLoginForm(t))
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("登录失败: status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	if err := database.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}

	reopened, err := db.NewDatabase(path)
	if err != nil {
		t.Fatalf("重新打开数据库失败: %v", err)
	}
	defer reopened.Close()
	reopenedServer := NewServer(serverConfig, reopened, nil)
	me := getAdminMeWithCookie(reopenedServer, cookie)
	if me.Code != http.StatusOK {
		t.Fatalf("数据库重开后主设备 session 应保持有效: status=%d body=%s", me.Code, me.Body.String())
	}
}

func TestAdminCredentialFingerprintChangeInvalidatesPersistentSession(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "epoch-session.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)
	login := postAdminLogin(server, adminLoginForm(t))
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("登录失败: status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]

	if _, changed, err := database.UpdateAdminCredentialFingerprint(context.Background(), "rotated-by-test", time.Now().Unix()); err != nil || !changed {
		t.Fatalf("模拟凭据轮换失败: changed=%v err=%v", changed, err)
	}
	me := getAdminMeWithCookie(server, cookie)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("凭据 fingerprint 变化后旧 session 必须失效: status=%d body=%s", me.Code, me.Body.String())
	}
}

func TestAdminPersistentLogoutRevokesDatabaseSession(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "logout-session.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)
	login := postAdminLogin(server, adminLoginForm(t))
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("登录失败: status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	logoutReq := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutResp := httptest.NewRecorder()
	server.handler().ServeHTTP(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("退出失败: status=%d body=%s", logoutResp.Code, logoutResp.Body.String())
	}
	me := getAdminMeWithCookie(server, cookie)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("退出后持久 session 必须失效: status=%d body=%s", me.Code, me.Body.String())
	}
}

func newPersistentTestServer(t *testing.T, database *db.Database) *Server {
	t.Helper()
	server := newTestSessionAdminServer(t)
	server.db = database
	server.sessions = newAdminSessionStoreWithDatabase(database)
	return server
}

func adminLoginForm(t *testing.T) map[string][]string {
	t.Helper()
	return map[string][]string{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	}
}

func getAdminMeWithCookie(server *Server, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	req.AddCookie(cookie)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	return resp
}
