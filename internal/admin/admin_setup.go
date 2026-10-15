package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	adminSetupCookieName = "ledgerbot_admin_setup"
	adminSetupTTL        = 10 * time.Minute
	adminSetupQRSize     = 256
)

type adminSetupSession struct {
	Username  string
	CSRFToken string
	ExpiresAt time.Time
}

type adminSetupStore struct {
	mu       sync.Mutex
	sessions map[string]adminSetupSession
}

func newAdminSetupStore() *adminSetupStore {
	return &adminSetupStore{sessions: make(map[string]adminSetupSession)}
}

type adminSetupStartRequest struct {
	SetupToken string `json:"setupToken"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

type adminSetupCompleteRequest struct {
	TOTP string `json:"totp"`
}

func (s *Server) adminSetupConfigured() bool {
	return s != nil && s.db != nil &&
		strings.TrimSpace(s.cfg.AdminSetupToken) != "" &&
		strings.TrimSpace(s.cfg.AdminUsername) != "" &&
		strings.TrimSpace(s.cfg.AdminPasswordHash) != "" &&
		strings.TrimSpace(s.cfg.AdminTOTPSecret) != ""
}

func (s *Server) handleAdminSetupPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	setAdminNoStoreHeaders(w)
	s.handleAdminShell(w, r)
}

func (s *Server) handleAdminSetupStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if !isStrictSameOriginUnsafeRequest(r) {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross origin request denied"})
		return
	}
	if !s.adminSetupConfigured() {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "setup unavailable"})
		return
	}
	var request adminSetupStartRequest
	if err := decodeAdminJSONBody(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid setup request"})
		return
	}
	limitKey := adminSecurityLimitKey(r, request.Username)
	if s.limiter != nil && !s.limiter.allow(limitKey, time.Now()) {
		s.writeAdminJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "setup 尝试过于频繁，请稍后再试"})
		return
	}
	if !s.verifyAdminSetupStart(request) {
		if s.limiter != nil {
			s.limiter.failure(limitKey, time.Now())
		}
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "setup credentials rejected"})
		return
	}
	if s.limiter != nil {
		s.limiter.success(limitKey)
	}
	state, err := s.db.GetAdminSetupState(r.Context())
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "setup state unavailable"})
		return
	}
	if state.ConsumedAt != 0 {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "setup already completed"})
		return
	}
	setupToken, err := randomAdminToken(32)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "setup session unavailable"})
		return
	}
	csrfToken, err := randomAdminToken(32)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "setup session unavailable"})
		return
	}
	if s.setup == nil {
		s.setup = newAdminSetupStore()
	}
	expiresAt := time.Now().Add(adminSetupTTL)
	s.setup.mu.Lock()
	for key, existing := range s.setup.sessions {
		if !existing.ExpiresAt.After(time.Now()) {
			delete(s.setup.sessions, key)
		}
	}
	s.setup.sessions[hashAdminBearer(setupToken)] = adminSetupSession{
		Username:  strings.TrimSpace(request.Username),
		CSRFToken: csrfToken,
		ExpiresAt: expiresAt,
	}
	s.setup.mu.Unlock()
	http.SetCookie(w, newAdminSetupCookie(r, setupToken, adminSetupTTL))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"username":  strings.TrimSpace(request.Username),
			"csrfToken": csrfToken,
			"expiresAt": expiresAt.UTC().Format(time.RFC3339),
		},
	})
}

func (s *Server) verifyAdminSetupStart(request adminSetupStartRequest) bool {
	if s == nil || !s.adminSetupConfigured() {
		return false
	}
	username := strings.TrimSpace(request.Username)
	if !secureStringEqual(username, strings.TrimSpace(s.cfg.AdminUsername)) {
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(strings.TrimSpace(s.cfg.AdminPasswordHash)), []byte(request.Password)) != nil {
		return false
	}
	provided := sha256.Sum256([]byte(request.SetupToken))
	expected := sha256.Sum256([]byte(strings.TrimSpace(s.cfg.AdminSetupToken)))
	return secureStringEqual(hex.EncodeToString(provided[:]), hex.EncodeToString(expected[:]))
}

func (s *Server) handleAdminSetupQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	session, ok := s.adminSetupSession(r)
	if !ok || !s.adminSetupConfigured() {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "setup session required"})
		return
	}
	state, err := s.db.GetAdminSetupState(r.Context())
	if err != nil || state.ConsumedAt != 0 {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "setup unavailable"})
		return
	}
	pngBytes, err := buildAdminTOTPQRCodePNG(s.cfg.AdminTOTPSecret, session.Username)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "二维码生成失败"})
		return
	}
	setAdminQRHeaders(w)
	_, _ = w.Write(pngBytes)
}

func (s *Server) handleAdminSetupComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if !isStrictSameOriginUnsafeRequest(r) {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross origin request denied"})
		return
	}
	session, ok := s.adminSetupSession(r)
	if !ok {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "setup session required"})
		return
	}
	var request adminSetupCompleteRequest
	if err := decodeAdminJSONBody(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid setup request"})
		return
	}
	csrf := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
	if !secureStringEqual(csrf, session.CSRFToken) || !verifyAdminTOTP(s.cfg.AdminTOTPSecret, request.TOTP, time.Now()) {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "验证码或请求令牌无效"})
		return
	}
	consumed, err := s.db.ConsumeAdminSetup(r.Context(), session.Username, time.Now().Unix())
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "setup state unavailable"})
		return
	}
	if !consumed {
		s.removeAdminSetupSession(r, w)
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "setup already completed"})
		return
	}
	s.removeAdminSetupSession(r, w)
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"username": session.Username}})
}

func (s *Server) adminSetupSession(r *http.Request) (adminSetupSession, bool) {
	if s == nil || s.setup == nil || r == nil {
		return adminSetupSession{}, false
	}
	cookie, err := r.Cookie(adminSetupCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return adminSetupSession{}, false
	}
	key := hashAdminBearer(cookie.Value)
	now := time.Now()
	s.setup.mu.Lock()
	defer s.setup.mu.Unlock()
	session, ok := s.setup.sessions[key]
	if !ok || !session.ExpiresAt.After(now) {
		delete(s.setup.sessions, key)
		return adminSetupSession{}, false
	}
	return session, true
}

func (s *Server) removeAdminSetupSession(r *http.Request, w http.ResponseWriter) {
	if s == nil || s.setup == nil || r == nil {
		return
	}
	cookie, err := r.Cookie(adminSetupCookieName)
	if err == nil {
		s.setup.mu.Lock()
		delete(s.setup.sessions, hashAdminBearer(cookie.Value))
		s.setup.mu.Unlock()
	}
	if w != nil {
		http.SetCookie(w, newAdminSetupCookie(r, "", -time.Hour))
	}
}

func decodeAdminJSONBody(w http.ResponseWriter, r *http.Request, destination any) error {
	if r == nil || r.Body == nil {
		return errors.New("missing request body")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminFormBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}

func newAdminSetupCookie(r *http.Request, value string, maxAge time.Duration) *http.Cookie {
	expires := time.Now().Add(maxAge)
	maxAgeSeconds := int(maxAge.Seconds())
	if maxAge < 0 {
		expires = time.Unix(0, 0)
		maxAgeSeconds = -1
	}
	return &http.Cookie{
		Name:     adminSetupCookieName,
		Value:    value,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAgeSeconds,
		Expires:  expires,
	}
}

func setAdminNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
}

func setAdminQRHeaders(w http.ResponseWriter) {
	setAdminNoStoreHeaders(w)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
