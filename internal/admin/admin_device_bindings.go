package admin

import (
	"net/http"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"go-bot/internal/db"
)

const adminDeviceBindingCookieName = "ledgerbot_admin_device_bind"

type adminDeviceBindingRequest struct {
	BindingID string `json:"bindingId"`
	Approved  bool   `json:"approved"`
}

type adminDeviceClaimRequest struct {
	Token string `json:"token"`
}

func (s *Server) handleAdminDeviceBindPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	setAdminNoStoreHeaders(w)
	s.handleAdminShell(w, r)
}

func (s *Server) handleAdminDeviceBindingStart(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requirePrimarySessionManagement(w, r)
	if !ok {
		return
	}
	rawToken, err := randomAdminToken(32)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "binding unavailable"})
		return
	}
	bindingID, err := randomAdminToken(18)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "binding unavailable"})
		return
	}
	now := time.Now().Unix()
	record, err := s.db.StartAdminDeviceBinding(r.Context(), bindingID, hashAdminBearer(rawToken), session.Username, session.SessionHandle, now)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "binding unavailable"})
		return
	}
	origin := requestOrigin(r)
	if origin == "" {
		origin = "https://" + r.Host
	}
	payload := strings.TrimRight(origin, "/") + "/admin/device-bind#token=" + rawToken
	pngBytes, err := qrcode.Encode(payload, qrcode.Medium, 256)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "二维码生成失败"})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Admin-Binding-ID", record.BindingID)
	w.Header().Set("X-Admin-Binding-Expires-At", time.Unix(record.ExpiresAt, 0).UTC().Format(time.RFC3339))
	_, _ = w.Write(pngBytes)
}

func (s *Server) requirePrimarySessionRead(w http.ResponseWriter, r *http.Request) (db.AdminSessionRecord, bool) {
	session, ok := s.currentAdminSession(r)
	if !ok || s.db == nil {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return session, false
	}
	if !session.IsPrimary {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "primary session required"})
		return session, false
	}
	if !s.hasAdminReauth(r, session, "sessions") {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "reauth required"})
		return session, false
	}
	return session, true
}

func (s *Server) handleAdminDeviceBindingPending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	session, ok := s.requirePrimarySessionRead(w, r)
	if !ok {
		return
	}
	items, err := s.db.ListAdminDeviceBindings(r.Context(), session.Username, session.SessionHandle, time.Now().Unix())
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "binding list unavailable"})
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"bindingId":  item.BindingID,
			"status":     item.Status,
			"device":     item.TargetUserAgent,
			"remoteAddr": item.TargetRemoteAddr,
			"claimedAt":  formatAdminUnixTime(item.ClaimedAt),
			"expiresAt":  time.Unix(item.ExpiresAt, 0).UTC().Format(time.RFC3339),
		})
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": out}})
}

func (s *Server) handleAdminDeviceBindingApprove(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requirePrimarySessionManagement(w, r)
	if !ok {
		return
	}
	var request adminDeviceBindingRequest
	if err := decodeAdminJSONBody(w, r, &request); err != nil || strings.TrimSpace(request.BindingID) == "" {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bindingId required"})
		return
	}
	var changed bool
	var err error
	if request.Approved {
		changed, err = s.db.ApproveAdminDeviceBinding(r.Context(), session.Username, session.SessionHandle, request.BindingID, time.Now().Unix())
	} else {
		changed, err = s.db.RejectAdminDeviceBinding(r.Context(), session.Username, session.SessionHandle, request.BindingID, time.Now().Unix())
	}
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "binding state update failed"})
		return
	}
	if !changed {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "binding state changed"})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminDeviceBindingClaim(w http.ResponseWriter, r *http.Request) {
	setAdminNoStoreHeaders(w)
	if r.Method != http.MethodPost || !isStrictSameOriginUnsafeRequest(r) {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross origin request denied"})
		return
	}
	var request adminDeviceClaimRequest
	if err := decodeAdminJSONBody(w, r, &request); err != nil || strings.TrimSpace(request.Token) == "" {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "binding token required"})
		return
	}
	rawCookie, err := randomAdminToken(32)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "binding unavailable"})
		return
	}
	record, claimed, err := s.db.ClaimAdminDeviceBinding(r.Context(), hashAdminBearer(request.Token), hashAdminBearer(rawCookie), r.UserAgent(), adminRemoteAddr(r), time.Now().Unix())
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "binding unavailable"})
		return
	}
	if !claimed {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "binding request invalid or expired"})
		return
	}
	http.SetCookie(w, newAdminDeviceBindingCookie(r, rawCookie, 180))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"bindingId": record.BindingID, "status": record.Status}})
}

func (s *Server) handleAdminDeviceBindingStatus(w http.ResponseWriter, r *http.Request) {
	setAdminNoStoreHeaders(w)
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	cookie, err := r.Cookie(adminDeviceBindingCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "binding session required"})
		return
	}
	record, err := s.db.GetAdminDeviceBindingByCookie(r.Context(), hashAdminBearer(cookie.Value), time.Now().Unix())
	if err != nil {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "binding request invalid or expired"})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"bindingId": record.BindingID, "status": record.Status, "device": record.TargetUserAgent, "expiresAt": time.Unix(record.ExpiresAt, 0).UTC().Format(time.RFC3339)}})
}

func (s *Server) handleAdminDeviceBindingComplete(w http.ResponseWriter, r *http.Request) {
	setAdminNoStoreHeaders(w)
	if r.Method != http.MethodPost || !isStrictSameOriginUnsafeRequest(r) {
		s.writeAdminJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross origin request denied"})
		return
	}
	cookie, err := r.Cookie(adminDeviceBindingCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "binding session required"})
		return
	}
	binding, err := s.db.GetAdminDeviceBindingByCookie(r.Context(), hashAdminBearer(cookie.Value), time.Now().Unix())
	if err != nil || binding.BindingID == "" {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "binding request invalid or expired"})
		return
	}
	sessionHandle, err := randomAdminToken(32)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "session unavailable"})
		return
	}
	sessionBearer, err := randomAdminToken(48)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "session unavailable"})
		return
	}
	record, completed, err := s.db.CompleteAdminDeviceBinding(r.Context(), binding.BindingID, hashAdminBearer(cookie.Value), sessionHandle, hashAdminBearer(sessionBearer), r.UserAgent(), adminRemoteAddr(r), time.Now().Unix(), adminSessionTTL)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "session unavailable"})
		return
	}
	if !completed {
		s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "binding is not approved"})
		return
	}
	http.SetCookie(w, s.newAdminSessionCookie(r, sessionBearer, adminSessionTTL))
	http.SetCookie(w, newAdminDeviceBindingCookie(r, "", -1))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"username": record.Username}})
}

func newAdminDeviceBindingCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	expires := time.Now().Add(time.Duration(maxAge) * time.Second)
	if maxAge < 0 {
		expires = time.Unix(0, 0)
	}
	return &http.Cookie{Name: adminDeviceBindingCookieName, Value: value, Path: "/admin", HttpOnly: true, Secure: isSecureRequest(r), SameSite: http.SameSiteStrictMode, MaxAge: maxAge, Expires: expires}
}

func formatAdminUnixTime(value *int64) string {
	if value == nil || *value <= 0 {
		return ""
	}
	return time.Unix(*value, 0).UTC().Format(time.RFC3339)
}
