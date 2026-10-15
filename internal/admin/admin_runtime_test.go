package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-bot/internal/db"
	"go-bot/internal/system"
)

func TestAdminRecoveryRequestRequiresPrimaryRecoveryReauth(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-runtime.db"))
	if err != nil {
		t.Fatalf("初始化数据库: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)
	recoveryDir := t.TempDir()
	server.cfg.PollStatusPath = filepath.Join(recoveryDir, "poll-status.json")
	primaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	csrf := getAdminCSRF(t, server, primaryCookie)

	withoutReauth := postAdminRecoveryRequest(t, server, primaryCookie, nil, csrf, `{"action":"recover"}`)
	if withoutReauth.Code != http.StatusUnauthorized {
		t.Fatalf("缺少 recovery 再认证必须拒绝: status=%d body=%s", withoutReauth.Code, withoutReauth.Body.String())
	}
	reauth := postAdminReauth(t, server, primaryCookie, csrf, "recovery")
	if reauth.Code != http.StatusOK {
		t.Fatalf("recovery 重新认证失败: status=%d body=%s", reauth.Code, reauth.Body.String())
	}
	reauthCookie := cookieNamed(reauth.Result().Cookies(), adminReauthCookieName)
	if reauthCookie == nil {
		t.Fatal("缺少 recovery reauth cookie")
	}
	invalid := postAdminRecoveryRequest(t, server, primaryCookie, reauthCookie, csrf, `{"action":"shell"}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("任意动作必须拒绝: status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	accepted := postAdminRecoveryRequest(t, server, primaryCookie, reauthCookie, csrf, `{"action":"recover"}`)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("合法恢复请求必须接收: status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	requestStore := system.NewRecoveryRequestStore(filepath.Join(recoveryDir, "recovery-request.json"))
	request, ok, err := requestStore.Take(context.Background(), time.Now())
	if err != nil || !ok {
		t.Fatalf("恢复请求未持久化: ok=%t err=%v", ok, err)
	}
	if request.Action != system.RecoveryActionRecover || request.Actor != "root" {
		t.Fatalf("恢复请求内容错误: %#v", request)
	}
}

func TestAdminRecoveryRequestRejectsSecondarySessionAndExtraAuthority(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-runtime-security.db"))
	if err != nil {
		t.Fatalf("初始化数据库: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)
	server.cfg.PollStatusPath = filepath.Join(t.TempDir(), "poll-status.json")
	primaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	secondaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	primaryCSRF := getAdminCSRF(t, server, primaryCookie)
	secondaryCSRF := getAdminCSRF(t, server, secondaryCookie)
	reauth := postAdminReauth(t, server, primaryCookie, primaryCSRF, "recovery")
	if reauth.Code != http.StatusOK {
		t.Fatalf("主设备 recovery 重新认证失败: status=%d body=%s", reauth.Code, reauth.Body.String())
	}
	reauthCookie := cookieNamed(reauth.Result().Cookies(), adminReauthCookieName)
	if reauthCookie == nil {
		t.Fatal("缺少 recovery reauth cookie")
	}
	secondary := postAdminRecoveryRequest(t, server, secondaryCookie, reauthCookie, secondaryCSRF, `{"action":"recover"}`)
	if secondary.Code != http.StatusForbidden {
		t.Fatalf("次设备不得请求恢复: status=%d body=%s", secondary.Code, secondary.Body.String())
	}
	extraAuthority := postAdminRecoveryRequest(t, server, primaryCookie, reauthCookie, primaryCSRF, `{"action":"recover","path":"/tmp/unsafe"}`)
	if extraAuthority.Code != http.StatusBadRequest {
		t.Fatalf("恢复请求不得接受路径等额外权限: status=%d body=%s", extraAuthority.Code, extraAuthority.Body.String())
	}
}

func TestAdminRuntimeStatusReturnsSanitizedSnapshots(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-runtime-status.db"))
	if err != nil {
		t.Fatalf("初始化数据库: %v", err)
	}
	defer database.Close()
	server := newPersistentTestServer(t, database)
	recoveryDir := t.TempDir()
	server.cfg.PollStatusPath = filepath.Join(recoveryDir, "poll-status.json")
	if _, err := system.NewPollStatusStore(server.cfg.PollStatusPath).RecordSuccess(context.Background(), system.PollSuccess{At: time.Now(), ReleaseID: "release-a", ReleaseSHA: "sha-a"}); err != nil {
		t.Fatalf("写入轮询状态: %v", err)
	}
	if err := system.NewGuardianStateStore(filepath.Join(recoveryDir, "guardian-state.json")).Write(context.Background(), system.GuardianState{Version: 1, Phase: system.GuardianPhaseRecovered}); err != nil {
		t.Fatalf("写入 Guardian 状态: %v", err)
	}
	primaryCookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]
	req := httptest.NewRequest(http.MethodGet, "/admin/api/runtime-status", nil)
	req.AddCookie(primaryCookie)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("读取运行状态失败: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			Poll struct {
				ReleaseID string `json:"release_id"`
			} `json:"poll"`
			Guardian struct {
				Phase string `json:"phase"`
			} `json:"guardian"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析运行状态: %v", err)
	}
	if !payload.OK || payload.Data.Poll.ReleaseID != "release-a" || payload.Data.Guardian.Phase != string(system.GuardianPhaseRecovered) {
		t.Fatalf("运行状态响应错误: %s", resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), server.cfg.AdminTOTPSecret) {
		t.Fatalf("运行状态不得泄露后台密钥: %s", resp.Body.String())
	}
}

func TestAdminRuntimeFrontendUsesFixedRecoveryWorkflow(t *testing.T) {
	server := newTestSessionAdminServer(t)
	cookie := postAdminLogin(server, adminLoginForm(t)).Result().Cookies()[0]

	appReq := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	appReq.AddCookie(cookie)
	appResp := httptest.NewRecorder()
	server.handler().ServeHTTP(appResp, appReq)
	if appResp.Code != http.StatusOK {
		t.Fatalf("读取后台前端失败: status=%d body=%s", appResp.Code, appResp.Body.String())
	}
	for _, want := range []string{
		"/admin/api/runtime-status",
		"/admin/api/recovery/request",
		"/admin/api/security/reauth",
		"Telegram 输入状态",
		"受控恢复",
		"试回滚至已验证版本",
		"action: \"recovery\"",
	} {
		if !strings.Contains(appResp.Body.String(), want) {
			t.Fatalf("运行恢复前端缺少 %q", want)
		}
	}
	for _, forbidden := range []string{"systemctl", "ledgerbot-guardian", "recovery-request.json"} {
		if strings.Contains(appResp.Body.String(), forbidden) {
			t.Fatalf("浏览器静态资源不得暴露运维执行细节 %q", forbidden)
		}
	}

	styleReq := httptest.NewRequest(http.MethodGet, "/admin/assets/styles.css", nil)
	styleReq.AddCookie(cookie)
	styleResp := httptest.NewRecorder()
	server.handler().ServeHTTP(styleResp, styleReq)
	if styleResp.Code != http.StatusOK {
		t.Fatalf("读取后台样式失败: status=%d body=%s", styleResp.Code, styleResp.Body.String())
	}
	for _, want := range []string{".runtime-panel", ".runtime-actions", "overflow-x: auto"} {
		if !strings.Contains(styleResp.Body.String(), want) {
			t.Fatalf("运行恢复样式缺少 %q", want)
		}
	}
}

func postAdminRecoveryRequest(t *testing.T, server *Server, sessionCookie, reauthCookie *http.Cookie, csrf, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/api/recovery/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	setAdminTestSameOrigin(req)
	req.AddCookie(sessionCookie)
	if reauthCookie != nil {
		req.AddCookie(reauthCookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	return resp
}
