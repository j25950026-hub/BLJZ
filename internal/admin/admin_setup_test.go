package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-bot/internal/config"
	"go-bot/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func TestAdminSetupStartQRAndComplete(t *testing.T) {
	database, server := newSetupTestServer(t)
	defer database.Close()

	pageReq := httptest.NewRequest(http.MethodGet, "/admin/setup", nil)
	pageResp := httptest.NewRecorder()
	server.handler().ServeHTTP(pageResp, pageReq)
	if pageResp.Code != http.StatusOK {
		t.Fatalf("setup 页面应可访问: status=%d body=%s", pageResp.Code, pageResp.Body.String())
	}
	for _, secret := range []string{server.cfg.AdminTOTPSecret, server.cfg.AdminSetupToken, server.cfg.AdminPasswordHash} {
		if strings.Contains(pageResp.Body.String(), secret) {
			t.Fatalf("setup 页面不应泄露预置敏感值 %q", secret)
		}
	}

	startBody := `{"setupToken":"setup-token","username":"root","password":"correct-password"}`
	startReq := httptest.NewRequest(http.MethodPost, "/admin/api/setup/start", strings.NewReader(startBody))
	startReq.Header.Set("Content-Type", "application/json")
	setAdminTestSameOrigin(startReq)
	startResp := httptest.NewRecorder()
	server.handler().ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusOK {
		t.Fatalf("setup start 应成功: status=%d body=%s", startResp.Code, startResp.Body.String())
	}
	var startPayload struct {
		OK   bool `json:"ok"`
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("解析 setup start 响应失败: %v", err)
	}
	if !startPayload.OK || startPayload.Data.CSRFToken == "" {
		t.Fatalf("setup start 应返回 csrf token: %s", startResp.Body.String())
	}
	cookies := startResp.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("setup start 应返回一个 cookie，实际=%d", len(cookies))
	}
	setupCookie := cookies[0]
	if setupCookie.Name != adminSetupCookieName || setupCookie.Path != "/admin" || !setupCookie.HttpOnly || setupCookie.SameSite != http.SameSiteStrictMode || setupCookie.MaxAge <= 0 || setupCookie.MaxAge > 10*60 {
		t.Fatalf("setup cookie 属性异常: %+v", setupCookie)
	}
	if strings.Contains(startResp.Body.String(), server.cfg.AdminTOTPSecret) || strings.Contains(startResp.Body.String(), server.cfg.AdminSetupToken) {
		t.Fatalf("setup start 响应不应包含 TOTP/setup token")
	}

	qrReq := httptest.NewRequest(http.MethodGet, "/admin/api/setup/qr", nil)
	qrReq.AddCookie(setupCookie)
	qrResp := httptest.NewRecorder()
	server.handler().ServeHTTP(qrResp, qrReq)
	if qrResp.Code != http.StatusOK {
		t.Fatalf("合法 setup session 获取二维码失败: status=%d body=%s", qrResp.Code, qrResp.Body.String())
	}
	if got := qrResp.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/png") {
		t.Fatalf("二维码 content type 错误: %q", got)
	}
	if got := qrResp.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("二维码缓存头错误: %q", got)
	}
	if qrResp.Header().Get("Pragma") != "no-cache" || qrResp.Header().Get("X-Robots-Tag") != "noindex, nofollow, noarchive" || qrResp.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("二维码安全响应头缺失: %+v", qrResp.Header())
	}
	if strings.Contains(qrResp.Body.String(), "otpauth://") || strings.Contains(qrResp.Body.String(), server.cfg.AdminTOTPSecret) {
		t.Fatalf("二维码响应不应包含明文 TOTP URI")
	}
	if _, err := png.Decode(bytes.NewReader(qrResp.Body.Bytes())); err != nil {
		t.Fatalf("二维码响应不是有效 PNG: %v", err)
	}

	completeReq := httptest.NewRequest(http.MethodPost, "/admin/api/setup/complete", strings.NewReader(`{"totp":"`+testTOTPCode(t, testAdminTOTPSecret, time.Now())+`"}`))
	completeReq.Header.Set("Content-Type", "application/json")
	completeReq.Header.Set("X-CSRF-Token", startPayload.Data.CSRFToken)
	setAdminTestSameOrigin(completeReq)
	completeReq.AddCookie(setupCookie)
	completeResp := httptest.NewRecorder()
	server.handler().ServeHTTP(completeResp, completeReq)
	if completeResp.Code != http.StatusOK || !strings.Contains(completeResp.Body.String(), `"ok":true`) {
		t.Fatalf("setup complete 应成功: status=%d body=%s", completeResp.Code, completeResp.Body.String())
	}
	state, err := database.GetAdminSetupState(context.Background())
	if err != nil || state.ConsumedAt == 0 {
		t.Fatalf("setup complete 后状态应持久化为 consumed: state=%+v err=%v", state, err)
	}

	qrAgainReq := httptest.NewRequest(http.MethodGet, "/admin/api/setup/qr", nil)
	qrAgainReq.AddCookie(setupCookie)
	qrAgainResp := httptest.NewRecorder()
	server.handler().ServeHTTP(qrAgainResp, qrAgainReq)
	if qrAgainResp.Code != http.StatusUnauthorized {
		t.Fatalf("setup consumed 后二维码应拒绝: status=%d body=%s", qrAgainResp.Code, qrAgainResp.Body.String())
	}
}

func TestAdminSetupCompleteConcurrentConsumption(t *testing.T) {
	database, server := newSetupTestServer(t)
	defer database.Close()

	startReq := httptest.NewRequest(http.MethodPost, "/admin/api/setup/start", strings.NewReader(`{"setupToken":"setup-token","username":"root","password":"correct-password"}`))
	startReq.Header.Set("Content-Type", "application/json")
	setAdminTestSameOrigin(startReq)
	startResp := httptest.NewRecorder()
	server.handler().ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusOK {
		t.Fatalf("setup start 失败: status=%d body=%s", startResp.Code, startResp.Body.String())
	}
	var payload struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析 setup start 失败: %v", err)
	}
	cookie := startResp.Result().Cookies()[0]
	totp := testTOTPCode(t, testAdminTOTPSecret, time.Now())

	const workers = 6
	statuses := make(chan int, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			req := httptest.NewRequest(http.MethodPost, "/admin/api/setup/complete", strings.NewReader(`{"totp":"`+totp+`"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", payload.Data.CSRFToken)
			setAdminTestSameOrigin(req)
			req.AddCookie(cookie)
			resp := httptest.NewRecorder()
			server.handler().ServeHTTP(resp, req)
			statuses <- resp.Code
		}()
	}
	wait.Wait()
	close(statuses)
	successes := 0
	for status := range statuses {
		if status == http.StatusOK {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("并发 setup complete 必须恰好成功一次，实际=%d", successes)
	}
}

func TestAdminSetupFailsClosedWhenPreconfiguredCredentialMissing(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "setup-missing.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	server := NewServer(config.Config{AdminSetupToken: "setup-token", AdminUsername: "root", AdminPasswordHash: ""}, database, nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/setup/start", strings.NewReader(`{"setupToken":"setup-token","username":"root","password":"correct-password"}`))
	req.Header.Set("Content-Type", "application/json")
	setAdminTestSameOrigin(req)
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("缺少预置 TOTP/password 时 setup 必须 fail-closed: status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func newSetupTestServer(t *testing.T) (*db.Database, *Server) {
	t.Helper()
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "setup.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		database.Close()
		t.Fatalf("生成测试密码哈希失败: %v", err)
	}
	server := NewServer(config.Config{
		AdminUsername:     "root",
		AdminPasswordHash: string(hash),
		AdminTOTPSecret:   testAdminTOTPSecret,
		AdminSetupToken:   "setup-token",
	}, database, nil)
	return database, server
}
