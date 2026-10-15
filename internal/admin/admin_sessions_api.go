package admin

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"go-bot/internal/db"
)

const (
	adminReauthCookieName = "ledgerbot_admin_reauth"
	adminReauthTTL        = 5 * time.Minute
	adminMaxSecondaryAge  = 30 * 24 * time.Hour
)

type adminCSRFStore struct {
	mu     sync.Mutex
	tokens map[string]string
}

type adminReauthRecord struct {
	SessionHandle string
	Username      string
	AuthEpoch     int64
	Scope         string
	ExpiresAt     time.Time
}

type adminReauthStore struct {
	mu     sync.Mutex
	tokens map[string]adminReauthRecord
}

type adminReauthRequest struct {
	Action string `json:"action"`
	TOTP   string `json:"totp"`
}

type adminSessionTargetRequest struct {
	SessionHandle string `json:"sessionHandle"`
	ExpiresAt     string `json:"expiresAt"`
}

func newAdminCSRFStore() *adminCSRFStore {
	return &adminCSRFStore{tokens: make(map[string]string)}
}

func newAdminReauthStore() *adminReauthStore {
	return &adminReauthStore{tokens: make(map[string]adminReauthRecord)}
}

func (s *Server) handleAdminCSRF(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	session, ok := s.currentAdminSession(r)
	if !ok {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}
	if s.csrf == nil {
		s.csrf = newAdminCSRFStore()
	}
	s.csrf.mu.Lock()
	token := s.csrf.tokens[session.SessionHandle]
	if token == "" {
		token, _ = randomAdminToken(32)
		s.csrf.tokens[session.SessionHandle] = token
	}
	s.csrf.mu.Unlock()
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"token": token}})
}

func (s *Server) handleAdminReauth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if !isStrictSameOriginUnsafeRequest(r) {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross origin request denied"})
		return
	}
	session, ok := s.currentAdminSession(r)
	if !ok {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}
	if !s.validAdminCSRF(r, session.SessionHandle) {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "csrf token required"})
		return
	}
	var request adminReauthRequest
	if err := decodeAdminJSONBody(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid reauth request"})
		return
	}
	limitKey := adminSecurityLimitKey(r, session.Username)
	if s.limiter != nil && !s.limiter.allow(limitKey, time.Now()) {
		s.writeAdminJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "重新认证尝试过于频繁，请稍后再试"})
		return
	}
	scope := strings.TrimSpace(request.Action)
	if scope != "qr" && scope != "sessions" && scope != "recovery" {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid reauth action"})
		return
	}
	if !verifyAdminTOTP(s.cfg.AdminTOTPSecret, request.TOTP, time.Now()) {
		if s.limiter != nil {
			s.limiter.failure(limitKey, time.Now())
		}
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "验证码无效"})
		return
	}
	if s.limiter != nil {
		s.limiter.success(limitKey)
	}
	value, err := randomAdminToken(32)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "reauth unavailable"})
		return
	}
	if s.reauth == nil {
		s.reauth = newAdminReauthStore()
	}
	now := time.Now()
	s.reauth.mu.Lock()
	for key, existing := range s.reauth.tokens {
		if !existing.ExpiresAt.After(now) {
			delete(s.reauth.tokens, key)
		}
	}
	s.reauth.tokens[hashAdminBearer(value)] = adminReauthRecord{
		SessionHandle: session.SessionHandle,
		Username:      session.Username,
		AuthEpoch:     session.AuthEpoch,
		Scope:         scope,
		ExpiresAt:     now.Add(adminReauthTTL),
	}
	s.reauth.mu.Unlock()
	http.SetCookie(w, newAdminReauthCookie(r, value, adminReauthTTL))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"action": scope, "expiresIn": int(adminReauthTTL.Seconds())}})
}

func (s *Server) handleAdminSecurityQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	session, ok := s.currentAdminSession(r)
	if !ok || !s.hasAdminReauth(r, session, "qr") {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "reauth required"})
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

func (s *Server) handleAdminSessionsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	session, ok := s.currentAdminSession(r)
	if !ok || s.db == nil {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}
	items, err := s.db.ListAdminSessionSummaries(r.Context(), session.Username, session.SessionHandle, time.Now().Unix())
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "session list unavailable"})
		return
	}
	if !session.IsPrimary || !s.hasAdminReauth(r, session, "sessions") {
		filtered := make([]any, 0, 1)
		for _, item := range items {
			if item.IsCurrent {
				filtered = append(filtered, adminSessionSummaryJSON(item))
			}
		}
		s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": filtered}})
		return
	}
	all := make([]any, 0, len(items))
	for _, item := range items {
		all = append(all, adminSessionSummaryJSON(item))
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": all}})
}

func (s *Server) handleAdminSessionExpiry(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requirePrimarySessionManagement(w, r)
	if !ok {
		return
	}
	var request adminSessionTargetRequest
	if err := decodeAdminJSONBody(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid expiry request"})
		return
	}
	target := strings.TrimSpace(request.SessionHandle)
	if target == "" || target == session.SessionHandle {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "secondary session required"})
		return
	}
	expiresAt, err := parseAdminSecondaryExpiry(request.ExpiresAt, time.Now())
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid expiry"})
		return
	}
	changed, err := s.db.UpdateAdminSessionExpiry(r.Context(), session.Username, target, expiresAt, time.Now().Unix())
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "expiry update failed"})
		return
	}
	if !changed {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "session state changed"})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminSessionRevoke(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requirePrimarySessionManagement(w, r)
	if !ok {
		return
	}
	var request adminSessionTargetRequest
	if err := decodeAdminJSONBody(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid revoke request"})
		return
	}
	target := strings.TrimSpace(request.SessionHandle)
	if target == "" || target == session.SessionHandle {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "secondary session required"})
		return
	}
	record, err := s.db.GetAdminSessionByHandle(r.Context(), session.Username, target)
	if err != nil || record.IsPrimary {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "session state changed"})
		return
	}
	changed, err := s.db.RevokeAdminSession(r.Context(), session.Username, target, time.Now().Unix())
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "revoke failed"})
		return
	}
	if !changed {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "session state changed"})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminRevokeAllOthers(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requirePrimarySessionManagement(w, r)
	if !ok {
		return
	}
	count, err := s.db.RevokeOtherAdminSessions(r.Context(), session.Username, session.SessionHandle, time.Now().Unix())
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "revoke failed"})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"revoked": count}})
}

func (s *Server) handleAdminRevokeCurrent(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAdminSessionWrite(w, r)
	if !ok {
		return
	}
	changed, err := s.db.RevokeCurrentAdminSession(r.Context(), session.Username, session.SessionHandle, time.Now().Unix())
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "revoke failed"})
		return
	}
	if !changed {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "session state changed"})
		return
	}
	http.SetCookie(w, s.newAdminSessionCookie(r, "", -time.Hour))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminTransferPrimary(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requirePrimarySessionManagement(w, r)
	if !ok {
		return
	}
	var request adminSessionTargetRequest
	if err := decodeAdminJSONBody(w, r, &request); err != nil || strings.TrimSpace(request.SessionHandle) == "" {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "transfer target required"})
		return
	}
	if err := s.db.TransferAdminPrimary(r.Context(), session.Username, strings.TrimSpace(request.SessionHandle), time.Now().Unix()); err != nil {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "session state changed"})
		return
	}
	http.SetCookie(w, s.newAdminSessionCookie(r, "", -time.Hour))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) requireAdminSessionWrite(w http.ResponseWriter, r *http.Request) (record db.AdminSessionRecord, ok bool) {
	record, ok = s.currentAdminSession(r)
	if !ok || s.db == nil {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return record, false
	}
	if !s.validAdminCSRF(r, record.SessionHandle) {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "csrf token required"})
		return record, false
	}
	if !isStrictSameOriginUnsafeRequest(r) {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross origin request denied"})
		return record, false
	}
	return record, true
}

func (s *Server) requirePrimarySessionManagement(w http.ResponseWriter, r *http.Request) (record db.AdminSessionRecord, ok bool) {
	record, ok = s.requireAdminSessionWrite(w, r)
	if !ok {
		return record, false
	}
	if !record.IsPrimary {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "primary session required"})
		return record, false
	}
	if !s.hasAdminReauth(r, record, "sessions") {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "reauth required"})
		return record, false
	}
	return record, true
}

func (s *Server) requirePrimarySessionRecovery(w http.ResponseWriter, r *http.Request) (record db.AdminSessionRecord, ok bool) {
	record, ok = s.requireAdminSessionWrite(w, r)
	if !ok {
		return record, false
	}
	if !record.IsPrimary {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "primary session required"})
		return record, false
	}
	if !s.hasAdminReauth(r, record, "recovery") {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "reauth required"})
		return record, false
	}
	return record, true
}

func (s *Server) validAdminCSRF(r *http.Request, sessionHandle string) bool {
	if s == nil || s.csrf == nil || r == nil {
		return false
	}
	provided := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
	if provided == "" {
		return false
	}
	s.csrf.mu.Lock()
	expected := s.csrf.tokens[strings.TrimSpace(sessionHandle)]
	s.csrf.mu.Unlock()
	return secureStringEqual(provided, expected)
}

func (s *Server) hasAdminReauth(r *http.Request, session db.AdminSessionRecord, scope string) bool {
	if s == nil || s.reauth == nil || r == nil {
		return false
	}
	cookie, err := r.Cookie(adminReauthCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}
	key := hashAdminBearer(cookie.Value)
	s.reauth.mu.Lock()
	defer s.reauth.mu.Unlock()
	record, ok := s.reauth.tokens[key]
	if !ok || !record.ExpiresAt.After(time.Now()) || record.Scope != scope || record.SessionHandle != session.SessionHandle || record.Username != session.Username || record.AuthEpoch != session.AuthEpoch {
		if !ok || !record.ExpiresAt.After(time.Now()) {
			delete(s.reauth.tokens, key)
		}
		return false
	}
	return true
}

func parseAdminSecondaryExpiry(raw string, now time.Time) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, errInvalidAdminExpiry
	}
	var target time.Time
	if duration, err := time.ParseDuration(value); err == nil {
		target = now.Add(duration)
	} else if strings.HasSuffix(value, "d") {
		days := strings.TrimSuffix(value, "d")
		if parsed, parseErr := time.ParseDuration(days + "24h"); parseErr == nil {
			target = now.Add(parsed)
		}
	} else {
		parsed, parseErr := time.Parse(time.RFC3339, value)
		if parseErr == nil {
			target = parsed
		}
	}
	if target.IsZero() {
		return 0, errInvalidAdminExpiry
	}
	if !target.After(now) || target.After(now.Add(adminMaxSecondaryAge)) {
		return 0, errInvalidAdminExpiry
	}
	return target.Unix(), nil
}

var errInvalidAdminExpiry = &adminExpiryError{}

type adminExpiryError struct{}

func (*adminExpiryError) Error() string { return "invalid admin expiry" }

func adminSessionSummaryJSON(item db.AdminSessionSummary) map[string]any {
	result := map[string]any{
		"sessionHandle": item.SessionHandle,
		"username":      item.Username,
		"isPrimary":     item.IsPrimary,
		"isCurrent":     item.IsCurrent,
		"userAgent":     item.UserAgent,
		"remoteAddr":    item.RemoteAddr,
		"createdAt":     time.Unix(item.CreatedAt, 0).UTC().Format(time.RFC3339),
		"lastSeenAt":    time.Unix(item.LastSeenAt, 0).UTC().Format(time.RFC3339),
	}
	if item.ExpiresAt != nil {
		result["expiresAt"] = time.Unix(*item.ExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	return result
}

func newAdminReauthCookie(r *http.Request, value string, maxAge time.Duration) *http.Cookie {
	expires := time.Now().Add(maxAge)
	maxAgeSeconds := int(maxAge.Seconds())
	if maxAge < 0 {
		expires = time.Unix(0, 0)
		maxAgeSeconds = -1
	}
	return &http.Cookie{
		Name:     adminReauthCookieName,
		Value:    value,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAgeSeconds,
		Expires:  expires,
	}
}

func buildAdminTOTPQRCodePNG(secret, username string) ([]byte, error) {
	label := url.PathEscape("LedgerBot:" + strings.TrimSpace(username))
	values := url.Values{}
	values.Set("secret", strings.TrimRight(strings.ToUpper(strings.TrimSpace(secret)), "="))
	values.Set("issuer", "LedgerBot")
	payload := "otpauth://totp/" + label + "?" + values.Encode()
	return qrcode.Encode(payload, qrcode.Medium, adminSetupQRSize)
}
