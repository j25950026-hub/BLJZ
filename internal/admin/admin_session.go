package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-bot/internal/db"
	"golang.org/x/crypto/bcrypt"
)

const (
	adminSessionCookieName = "ledgerbot_admin_session"
	adminSessionTTL        = 12 * time.Hour
	adminPrimaryCookieTTL  = 365 * 24 * time.Hour
)

type adminSession struct {
	Username  string
	ExpiresAt time.Time
}

type adminSessionStore struct {
	mu       sync.Mutex
	sessions map[string]adminSession
	now      func() time.Time
	database *db.Database
}

func newAdminSessionStore() *adminSessionStore {
	return &adminSessionStore{
		sessions: make(map[string]adminSession),
		now:      time.Now,
	}
}

func newAdminSessionStoreWithDatabase(database *db.Database) *adminSessionStore {
	store := newAdminSessionStore()
	store.database = database
	return store
}

func (s *Server) adminLoginEnabled() bool {
	if s == nil {
		return false
	}
	return strings.TrimSpace(s.cfg.AdminUsername) != "" &&
		strings.TrimSpace(s.cfg.AdminPasswordHash) != "" &&
		strings.TrimSpace(s.cfg.AdminTOTPSecret) != ""
}

func (s *Server) handleAdminRoot(w http.ResponseWriter, r *http.Request) {
	if !s.adminLoginEnabled() {
		http.Error(w, "后台登录未配置", http.StatusUnauthorized)
		return
	}
	if s.adminAPISessionUsername(r) != "" {
		s.handleAdminShell(w, r)
		return
	}
	s.writeAdminLoginPage(w, http.StatusOK, "")
}

func (s *Server) handleAdminGroupsRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && s.adminLoginEnabled() && !adminLegacyTokenRequested(r) {
		if s.adminAPISessionUsername(r) != "" {
			s.handleAdminShell(w, r)
			return
		}
		s.writeAdminLoginPage(w, http.StatusOK, "")
		return
	}
	s.wrap(s.handleGroups)(w, r)
}

func (s *Server) handleAdminPermissionsRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && s.adminLoginEnabled() && !adminLegacyTokenRequested(r) {
		if s.adminAPISessionUsername(r) != "" {
			s.handleAdminShell(w, r)
			return
		}
		s.writeAdminLoginPage(w, http.StatusOK, "")
		return
	}
	s.wrap(s.handlePermissions)(w, r)
}

func (s *Server) handleAdminBroadcastRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && s.adminLoginEnabled() && !adminLegacyTokenRequested(r) {
		if s.adminAPISessionUsername(r) != "" {
			s.handleAdminShell(w, r)
			return
		}
		s.writeAdminLoginPage(w, http.StatusOK, "")
		return
	}
	s.wrap(s.handleBroadcast)(w, r)
}

func (s *Server) handleAdminSchedulesRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && s.adminLoginEnabled() && !adminLegacyTokenRequested(r) {
		if s.adminAPISessionUsername(r) != "" {
			s.handleAdminShell(w, r)
			return
		}
		s.writeAdminLoginPage(w, http.StatusOK, "")
		return
	}
	s.wrap(s.handleSchedules)(w, r)
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if !s.adminLoginEnabled() {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		s.writeAdminLoginPage(w, http.StatusOK, "")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isSameOriginUnsafeRequest(r) {
		http.Error(w, "跨站请求被拒绝", http.StatusForbidden)
		return
	}
	if err := parseLimitedAdminForm(w, r); err != nil {
		s.writeAdminLoginPage(w, http.StatusUnauthorized, "登录请求无效")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	totp := strings.TrimSpace(r.FormValue("totp"))
	limitKey := adminSecurityLimitKey(r, username)
	if s.limiter != nil && !s.limiter.allow(limitKey, time.Now()) {
		s.writeAdminJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "登录尝试过于频繁，请稍后再试"})
		return
	}
	if !s.verifyAdminLogin(username, password, totp, time.Now()) {
		if s.limiter != nil {
			s.limiter.failure(limitKey, time.Now())
		}
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{
			"ok":    false,
			"error": "账号、密码或验证码错误",
		})
		return
	}
	if s.limiter != nil {
		s.limiter.success(limitKey)
	}
	sessionToken, record, err := s.createAdminSession(username, r.UserAgent(), adminRemoteAddr(r))
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": "创建登录会话失败",
		})
		return
	}
	cookieTTL := adminSessionTTL
	if record.IsPrimary {
		cookieTTL = adminPrimaryCookieTTL
	}
	http.SetCookie(w, s.newAdminSessionCookie(r, sessionToken, cookieTTL))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"username": username,
		},
	})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"ok":    false,
			"error": "method not allowed",
		})
		return
	}
	if !isSameOriginUnsafeRequest(r) {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{
			"ok":    false,
			"error": "cross origin request denied",
		})
		return
	}
	if cookie, err := r.Cookie(adminSessionCookieName); err == nil {
		s.deleteAdminSession(cookie.Value)
	}
	http.SetCookie(w, s.newAdminSessionCookie(r, "", -time.Hour))
	http.SetCookie(w, newAdminReauthCookie(r, "", -time.Hour))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminMe(w http.ResponseWriter, r *http.Request) {
	username := s.adminSessionUsername(r)
	if username == "" {
		username = strings.TrimSpace(s.cfg.AdminUsername)
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"username": username,
		},
	})
}

func (s *Server) handleAdminShell(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if content, err := readAdminAppFile("index.html"); err == nil && len(content) > 0 {
		_, _ = w.Write(content)
		return
	}
	_, _ = w.Write([]byte(adminAppIndexFallback))
}

func (s *Server) handleAdminAppJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	if content, err := readAdminAppFile("app.js"); err == nil && len(content) > 0 {
		_, _ = w.Write(content)
		return
	}
	_, _ = w.Write([]byte(adminAppJSFallback))
}

func (s *Server) handleAdminStylesCSS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if content, err := readAdminAppFile("styles.css"); err == nil && len(content) > 0 {
		_, _ = w.Write(content)
		return
	}
	_, _ = w.Write([]byte(adminAppCSSFallback))
}

func readAdminAppFile(name string) ([]byte, error) {
	if content, err := os.ReadFile(filepath.Join("web", "admin-app", name)); err == nil {
		return content, nil
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "..", "..", "web", "admin-app", name))
}

func (s *Server) writeAdminLoginPage(w http.ResponseWriter, status int, errorText string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	message := ""
	if strings.TrimSpace(errorText) != "" {
		message = `<div class="error">` + html.EscapeString(errorText) + `</div>`
	}
	_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>后台登录</title><style>body{margin:0;font-family:"Microsoft YaHei","PingFang SC",Arial,sans-serif;color:#1f2937;background:#f6f7f9}.login-page{min-height:100vh;display:grid;place-items:center}.login-box{width:min(420px,calc(100vw - 32px));background:#fff;border:1px solid #e5e7eb;padding:24px}.login-box h1{margin:0 0 8px;font-size:22px}.login-box p{margin:0 0 18px;color:#4b5563}.login-box label{display:block;margin:12px 0;font-size:13px}.login-box input,.login-box button{box-sizing:border-box;width:100%;padding:10px;font-size:14px}.login-box button{border:0;color:#fff;background:#111827;cursor:pointer}.error{padding:10px;border:1px solid #fca5a5;background:#fef2f2}.muted{margin-top:8px;color:#6b7280;font-size:12px}</style></head><body class="login-page"><main class="login-box"><h1>后台登录</h1><p>请输入管理员账号、密码和 Google Authenticator 6 位验证码。</p><div id="login-error">` + message + `</div><form id="login-form" method="post" action="/admin/login"><label>账号<input name="username" autocomplete="username" required></label><label>密码<input type="password" name="password" autocomplete="current-password" required></label><label>Google Authenticator<input name="totp" inputmode="numeric" maxlength="6" autocomplete="one-time-code" required></label><button type="submit">登录</button></form><div class="muted">提交后会自动进入后台。</div></main><script>(function(){var form=document.getElementById("login-form");var box=document.getElementById("login-error");if(!form||!box){return;}form.addEventListener("submit",async function(event){event.preventDefault();box.textContent="";var data=new URLSearchParams(new FormData(form));var resp=await fetch("/admin/login",{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/x-www-form-urlencoded"},body:data.toString()});var payload=null;try{payload=await resp.json();}catch(err){}if(resp.ok&&payload&&payload.ok){window.location.href = "/admin";return;}box.textContent=(payload&&payload.error)||"登录失败";});})();</script></body></html>`))
}

func (s *Server) writeAdminJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) verifyAdminLogin(username, password, totp string, at time.Time) bool {
	expectedUsername := strings.TrimSpace(s.cfg.AdminUsername)
	if !secureStringEqual(username, expectedUsername) {
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(strings.TrimSpace(s.cfg.AdminPasswordHash)), []byte(password)) != nil {
		return false
	}
	return verifyAdminTOTP(strings.TrimSpace(s.cfg.AdminTOTPSecret), totp, at)
}

func secureStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func verifyAdminTOTP(secret, code string, at time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(strings.ToUpper(strings.TrimSpace(secret)), "="))
	if err != nil || len(key) == 0 {
		return false
	}
	counter := at.Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		if secureTokenEqual(generateAdminTOTPCode(key, uint64(counter+offset)), code) {
			return true
		}
	}
	return false
}

func generateAdminTOTPCode(key []byte, counter uint64) string {
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
	raw := code % 1000000
	text := "000000" + strconv.Itoa(raw)
	return text[len(text)-6:]
}

func (s *Server) createAdminSession(username, userAgent, remoteAddr string) (string, db.AdminSessionRecord, error) {
	if s == nil {
		return "", db.AdminSessionRecord{}, os.ErrInvalid
	}
	if s.sessions == nil {
		s.sessions = newAdminSessionStoreWithDatabase(s.db)
	}
	if s.sessions.database != nil {
		if err := s.syncAdminCredentialState(); err != nil {
			return "", db.AdminSessionRecord{}, err
		}
		handle, err := randomAdminToken(32)
		if err != nil {
			return "", db.AdminSessionRecord{}, err
		}
		bearer, err := randomAdminToken(48)
		if err != nil {
			return "", db.AdminSessionRecord{}, err
		}
		record, err := s.sessions.database.CreateAdminSession(
			context.Background(), username, handle, hashAdminBearer(bearer), userAgent, remoteAddr, time.Now().Unix(), adminSessionTTL,
		)
		return bearer, record, err
	}

	bearer, err := randomAdminToken(48)
	if err != nil {
		return "", db.AdminSessionRecord{}, err
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	primary := len(s.sessions.sessions) == 0
	expiresAt := s.sessions.now().Add(adminSessionTTL)
	if primary {
		expiresAt = s.sessions.now().Add(adminPrimaryCookieTTL)
	}
	s.sessions.sessions[bearer] = adminSession{
		Username:  username,
		ExpiresAt: expiresAt,
	}
	return bearer, db.AdminSessionRecord{Username: username, IsPrimary: primary}, nil
}

func adminRemoteAddr(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (s *Server) deleteAdminSession(sessionID string) {
	if s == nil || s.sessions == nil {
		return
	}
	if s.sessions.database != nil {
		record, err := s.sessions.database.GetAdminSessionByTokenHash(context.Background(), hashAdminBearer(sessionID))
		if err != nil {
			return
		}
		_, _ = s.sessions.database.RevokeCurrentAdminSession(context.Background(), record.Username, record.SessionHandle, time.Now().Unix())
		return
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	delete(s.sessions.sessions, sessionID)
}

func (s *Server) adminSessionUsername(r *http.Request) string {
	if s == nil || s.sessions == nil || r == nil {
		return ""
	}
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return ""
	}
	if s.sessions.database != nil {
		if err := s.syncAdminCredentialState(); err != nil {
			return ""
		}
		record, err := s.sessions.database.GetAdminSessionByTokenHash(r.Context(), hashAdminBearer(cookie.Value))
		if err != nil {
			return ""
		}
		state, err := s.sessions.database.GetAdminSetupState(r.Context())
		if err != nil || record.Username != strings.TrimSpace(s.cfg.AdminUsername) || record.AuthEpoch != state.AuthEpoch {
			return ""
		}
		now := time.Now().Unix()
		if record.RevokedAt != nil || (record.ExpiresAt != nil && *record.ExpiresAt <= now) {
			return ""
		}
		_ = s.sessions.database.TouchAdminSession(r.Context(), record.SessionHandle, now)
		return record.Username
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	session, ok := s.sessions.sessions[cookie.Value]
	if !ok {
		return ""
	}
	now := s.sessions.now()
	if !session.ExpiresAt.After(now) {
		delete(s.sessions.sessions, cookie.Value)
		return ""
	}
	return session.Username
}

func (s *Server) adminSessionHandle(r *http.Request) string {
	record, ok := s.currentAdminSession(r)
	if !ok {
		return ""
	}
	return strings.TrimSpace(record.SessionHandle)
}

func (s *Server) syncAdminCredentialState() error {
	if s == nil || s.sessions == nil || s.sessions.database == nil {
		return nil
	}
	fingerprint := adminCredentialFingerprint(s.cfg.AdminUsername, s.cfg.AdminPasswordHash, s.cfg.AdminTOTPSecret)
	_, _, err := s.sessions.database.UpdateAdminCredentialFingerprint(context.Background(), fingerprint, time.Now().Unix())
	return err
}

func adminCredentialFingerprint(username, passwordHash, totpSecret string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.TrimSpace(username)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strings.TrimSpace(passwordHash)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strings.TrimSpace(totpSecret)))
	return hex.EncodeToString(hash.Sum(nil))
}

func randomAdminToken(size int) (string, error) {
	if size <= 0 {
		return "", os.ErrInvalid
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashAdminBearer(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (s *Server) currentAdminSession(r *http.Request) (db.AdminSessionRecord, bool) {
	if s == nil || s.sessions == nil || r == nil {
		return db.AdminSessionRecord{}, false
	}
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return db.AdminSessionRecord{}, false
	}
	if s.sessions.database != nil {
		if err := s.syncAdminCredentialState(); err != nil {
			return db.AdminSessionRecord{}, false
		}
		record, err := s.sessions.database.GetAdminSessionByTokenHash(r.Context(), hashAdminBearer(cookie.Value))
		if err != nil {
			return db.AdminSessionRecord{}, false
		}
		state, err := s.sessions.database.GetAdminSetupState(r.Context())
		if err != nil || record.Username != strings.TrimSpace(s.cfg.AdminUsername) || record.AuthEpoch != state.AuthEpoch {
			return db.AdminSessionRecord{}, false
		}
		now := time.Now().Unix()
		if record.RevokedAt != nil || (record.ExpiresAt != nil && *record.ExpiresAt <= now) {
			return db.AdminSessionRecord{}, false
		}
		return record, true
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	session, ok := s.sessions.sessions[cookie.Value]
	if !ok || !session.ExpiresAt.After(s.sessions.now()) {
		delete(s.sessions.sessions, cookie.Value)
		return db.AdminSessionRecord{}, false
	}
	return db.AdminSessionRecord{SessionHandle: cookie.Value, Username: session.Username, IsPrimary: true}, true
}

func (s *Server) refreshAdminSessionCookie(w http.ResponseWriter, r *http.Request) {
	if s == nil || w == nil || r == nil {
		return
	}
	record, ok := s.currentAdminSession(r)
	if !ok {
		return
	}
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return
	}
	maxAge := adminSessionTTL
	if record.IsPrimary {
		maxAge = adminPrimaryCookieTTL
	}
	http.SetCookie(w, s.newAdminSessionCookie(r, cookie.Value, maxAge))
}

func (s *Server) adminAPISessionUsername(r *http.Request) string {
	if !s.adminLoginEnabled() {
		return ""
	}
	return s.adminSessionUsername(r)
}

func adminLegacyTokenRequested(r *http.Request) bool {
	if r == nil {
		return false
	}
	if strings.TrimSpace(r.Header.Get("X-Admin-Token")) != "" {
		return true
	}
	if r.URL != nil && strings.TrimSpace(r.URL.Query().Get("token")) != "" {
		return true
	}
	return false
}

func (s *Server) newAdminSessionCookie(r *http.Request, value string, maxAge time.Duration) *http.Cookie {
	expires := time.Now().Add(maxAge)
	maxAgeSeconds := int(maxAge.Seconds())
	if maxAge < 0 {
		expires = time.Unix(0, 0)
		maxAgeSeconds = -1
	}
	return &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    value,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   maxAgeSeconds,
	}
}

func isSecureRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if proto := strings.TrimSpace(strings.Split(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), ",")[0]); proto != "" {
		return strings.EqualFold(proto, "https")
	}
	return r.TLS != nil
}

const adminAppIndexFallback = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>运营总控台</title><link rel="stylesheet" href="/admin/assets/styles.css"></head><body><div id="app"></div><script src="/admin/assets/app.js"></script></body></html>`

const adminAppJSFallback = `(function(){var root=document.getElementById("app");if(!root){return;}function esc(v){return String(v==null?"":v).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;").replace(/'/g,"&#39;");}function safeAdminHref(v){var href=String(v==null?"":v).trim();return href==="/admin"||href.indexOf("/admin/")===0?href:"";}function renderList(items,empty){items=(items||[]).filter(function(x){return !x.visibility||x.visibility==="admin_only";});if(!items.length){return "<div>"+esc(empty)+"</div>";}return "<ul>"+items.map(function(x){var href=safeAdminHref(x.actionUrl);return "<li><b>"+esc(x.title)+"</b><div>"+esc(x.description||"")+"</div>"+(href?"<a href=\""+esc(href)+"\">进入处理</a>":"")+"</li>";}).join("")+"</ul>";}function renderConsole(data){data=data||{};var s=data.summary||{};root.innerHTML="<main class=\"admin-shell\"><aside class=\"admin-nav\"><div class=\"brand\">运营后台</div><a class=\"nav-link nav-link-active\" href=\"/admin\">运营总控台</a><a class=\"nav-link\" href=\"/admin/groups\">群组工作台</a><a class=\"nav-link\" href=\"/admin/permissions\">人员与权限中心</a></aside><section class=\"admin-main\"><header class=\"page-head\"><h1>运营总控台</h1><button id=\"logout-btn\">退出</button></header><section class=\"metric-strip\"><article class=\"metric-card\"><div>群总数</div><strong>"+esc(s.totalGroups||0)+"</strong></article><article class=\"metric-card\"><div>待办</div><strong>"+esc((data.todos||[]).length)+"</strong></article><article class=\"metric-card\"><div>平均健康分</div><strong>"+esc(s.averageHealthScore||0)+"</strong></article></section><section class=\"panel-row\"><article class=\"panel\"><h2>待办中心</h2>"+renderList(data.todos,\"暂无待处理事项\")+"</article><article class=\"panel\"><h2>下一步建议</h2>"+renderList(data.nextSteps,\"暂无建议\")+"</article></section><section class=\"panel\"><h2>群健康状态</h2>"+renderList(data.health,\"暂无群数据\")+"</section></section></main>";var logout=document.getElementById("logout-btn");if(logout){logout.addEventListener("click",async function(){await fetch("/admin/api/logout",{method:"POST",credentials:"same-origin"});window.location.href="/admin";});}}fetch("/admin/api/console",{credentials:"same-origin",headers:{Accept:"application/json"}}).then(function(resp){if(resp.status===401){window.location.href="/admin";return null;}return resp.json();}).then(function(payload){if(payload&&payload.ok){renderConsole(payload.data||{});}}).catch(function(err){root.innerHTML="<main class=\"admin-shell\"><section class=\"admin-main\"><h1>运营总控台</h1><div class=\"state-error\">"+esc(err&&err.message?err.message:"加载失败")+"</div></section></main>";});})();`

const adminAppCSSFallback = `body{font-family:"Microsoft YaHei","PingFang SC",sans-serif;margin:0;color:#1f2937;background:#f8fafc}.login-page{display:grid;min-height:100vh;place-items:center}.login-box{width:min(420px,calc(100vw - 32px));background:#fff;border:1px solid #e5e7eb;padding:24px}label{display:block;margin:12px 0}input,button{box-sizing:border-box;width:100%;padding:10px;font-size:14px}button{background:#111827;color:#fff;border:0}.error,.state-error{background:#fef2f2;border:1px solid #fca5a5;padding:10px}.admin-shell{min-height:100vh;display:grid;grid-template-columns:240px minmax(0,1fr)}.admin-nav{padding:18px;border-right:1px solid #e5e7eb;background:#fff}.nav-link{display:block;padding:10px;color:#1f2937;text-decoration:none}.nav-link-active{background:#f3f4f6;border:1px solid #d1d5db}.admin-main{padding:24px}.page-head{display:flex;justify-content:space-between;align-items:center}.metric-strip,.panel-row{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}.metric-card,.panel{border:1px solid #e5e7eb;background:#fff;padding:16px;margin-top:12px}@media(max-width:760px){.admin-shell,.metric-strip,.panel-row{grid-template-columns:1fr}}`
