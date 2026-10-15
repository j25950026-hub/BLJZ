package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-bot/internal/audit"
	"go-bot/internal/bot"
	"go-bot/internal/config"
	"go-bot/internal/db"
	"go-bot/internal/services"
	"go-bot/internal/system"
	"go-bot/internal/utils"
)

const (
	maxWebAppExecBodyBytes int64 = 64 * 1024
	maxAdminFormBodyBytes  int64 = 1 << 20
)

type Server struct {
	cfg config.Config
	db  *db.Database
	app *bot.App

	auditTrail *audit.HTTPAuditTrail
	killSwitch *system.KillSwitch
	sessions   *adminSessionStore
	setup      *adminSetupStore
	csrf       *adminCSRFStore
	reauth     *adminReauthStore
	limiter    *adminSecurityLimiter

	batchPreviews  *adminBatchPreviewStore
	switchPlans    *adminSwitchPlanStore
	messaging      adminMessagingService
	messagingPlans *adminMessagingPlanStore
	templatePlans  *adminTemplatePlanStore
	retryPlans     *adminRetryPlanStore
}

func NewServer(cfg config.Config, database *db.Database, app *bot.App) *Server {
	server := &Server{
		cfg:        cfg,
		db:         database,
		app:        app,
		auditTrail: audit.NewHTTPAuditTrail(log.Default()),
		sessions:   newAdminSessionStoreWithDatabase(database),
		setup:      newAdminSetupStore(),
		csrf:       newAdminCSRFStore(),
		reauth:     newAdminReauthStore(),
		limiter:    newAdminSecurityLimiter(),

		batchPreviews:  newAdminBatchPreviewStore(),
		switchPlans:    newAdminSwitchPlanStore(),
		messagingPlans: newAdminMessagingPlanStore(),
		templatePlans:  newAdminTemplatePlanStore(),
		retryPlans:     newAdminRetryPlanStore(),
	}
	if app != nil {
		server.messaging = app
	}
	return server
}

func (s *Server) SetKillSwitch(killSwitch *system.KillSwitch) {
	if s == nil {
		return
	}
	s.killSwitch = killSwitch
}

func (s *Server) Run(ctx context.Context) error {
	addr := strings.TrimSpace(s.cfg.AdminAddr)
	if addr == "" {
		return nil
	}
	handler := s.handler()
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return server.ListenAndServe()
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/tg/webapp", s.handleTGWebApp)
	mux.HandleFunc("/tg/webapp/exec", s.handleTGWebAppExec)
	mux.HandleFunc("/tg/webapp/ledger/card", s.handleTGWebAppLedgerCard)
	mux.HandleFunc("/tg/webapp/looklook.php", s.handleTGWebAppLooklook)
	mux.HandleFunc("/tg/webapp/state/ws", s.handleTGWebAppStateWS)
	mux.HandleFunc("/admin/login", s.handleAdminLogin)
	mux.HandleFunc("/admin/setup", s.handleAdminSetupPage)
	mux.HandleFunc("/admin/api/setup/start", s.handleAdminSetupStart)
	mux.HandleFunc("/admin/api/setup/qr", s.handleAdminSetupQR)
	mux.HandleFunc("/admin/api/setup/complete", s.handleAdminSetupComplete)
	mux.HandleFunc("/admin/api/csrf", s.wrapAPI(s.handleAdminCSRF))
	mux.HandleFunc("/admin/api/security/reauth", s.wrapAPI(s.handleAdminReauth))
	mux.HandleFunc("/admin/api/security/qr", s.wrapAPI(s.handleAdminSecurityQR))
	mux.HandleFunc("/admin/api/runtime-status", s.wrapAPI(s.handleAdminRuntimeStatus))
	mux.HandleFunc("/admin/api/recovery/request", s.wrapAPI(s.handleAdminRecoveryRequest))
	mux.HandleFunc("/admin/api/sessions", s.wrapAPI(s.handleAdminSessionsAPI))
	mux.HandleFunc("/admin/api/sessions/expiry", s.wrapAPI(s.handleAdminSessionExpiry))
	mux.HandleFunc("/admin/api/sessions/revoke", s.wrapAPI(s.handleAdminSessionRevoke))
	mux.HandleFunc("/admin/api/sessions/revoke-all-others", s.wrapAPI(s.handleAdminRevokeAllOthers))
	mux.HandleFunc("/admin/api/sessions/revoke-current", s.wrapAPI(s.handleAdminRevokeCurrent))
	mux.HandleFunc("/admin/api/sessions/transfer-primary", s.wrapAPI(s.handleAdminTransferPrimary))
	mux.HandleFunc("/admin/logout", s.wrapAPI(s.handleAdminLogout))
	mux.HandleFunc("/admin/api/me", s.wrapAPI(s.handleAdminMe))
	mux.HandleFunc("/admin/api/console", s.wrapAPI(s.handleAdminConsole))
	mux.HandleFunc("/admin/api/groups", s.wrapAPI(s.handleAdminGroupsAPI))
	mux.HandleFunc("/admin/api/group-views", s.wrapAPI(s.handleAdminGroupViewsAPI))
	mux.HandleFunc("/admin/api/groups/batch-preview", s.wrapAPI(s.handleAdminGroupBatchPreview))
	mux.HandleFunc("/admin/api/groups/batch-apply", s.wrapAPI(s.handleAdminGroupBatchApply))
	mux.HandleFunc("/admin/api/group-dictionaries", s.wrapAPI(s.handleAdminGroupDictionariesAPI))
	mux.HandleFunc("/admin/api/permissions", s.wrapAPI(s.handleAdminPermissionsAPI))
	mux.HandleFunc("/admin/api/switches", s.wrapAPI(s.handleAdminSwitchesAPI))
	mux.HandleFunc("/admin/api/switches/", s.wrapAPI(s.handleAdminSwitchFeatureAPI))
	mux.HandleFunc("/admin/api/switches/preview", s.wrapAPI(s.handleAdminSwitchPreview))
	mux.HandleFunc("/admin/api/switches/apply", s.wrapAPI(s.handleAdminSwitchApply))
	mux.HandleFunc("/admin/api/switches/rollback", s.wrapAPI(s.handleAdminSwitchRollback))
	mux.HandleFunc("/admin/api/device-bindings/start", s.wrapAPI(s.handleAdminDeviceBindingStart))
	mux.HandleFunc("/admin/api/device-bindings/pending", s.wrapAPI(s.handleAdminDeviceBindingPending))
	mux.HandleFunc("/admin/api/device-bindings/approve", s.wrapAPI(s.handleAdminDeviceBindingApprove))
	mux.HandleFunc("/admin/api/device-bindings/claim", s.handleAdminDeviceBindingClaim)
	mux.HandleFunc("/admin/api/device-bindings/status", s.handleAdminDeviceBindingStatus)
	mux.HandleFunc("/admin/api/device-bindings/complete", s.handleAdminDeviceBindingComplete)
	mux.HandleFunc("/admin/api/broadcast/preview", s.wrapAPI(s.handleAdminBroadcastPreview))
	mux.HandleFunc("/admin/api/broadcast/apply", s.wrapAPI(s.handleAdminBroadcastApply))
	mux.HandleFunc("/admin/api/schedules", s.wrapAPI(s.handleAdminSchedulesAPI))
	mux.HandleFunc("/admin/api/schedules/preview", s.wrapAPI(s.handleAdminSchedulePreview))
	mux.HandleFunc("/admin/api/schedules/apply", s.wrapAPI(s.handleAdminScheduleApply))
	mux.HandleFunc("/admin/api/snapshots", s.wrapAPI(s.handleAdminSnapshotsAPI))
	mux.HandleFunc("/admin/api/template-library", s.wrapAPI(s.handleAdminTemplateLibraryAPI))
	mux.HandleFunc("/admin/api/template-library/preview", s.wrapAPI(s.handleAdminTemplateLibraryPreview))
	mux.HandleFunc("/admin/api/risk-radar", s.wrapAPI(s.handleAdminRiskRadarAPI))
	mux.HandleFunc("/admin/api/insights", s.wrapAPI(s.handleAdminInsightsAPI))
	mux.HandleFunc("/admin/api/retries", s.wrapAPI(s.handleAdminRetriesAPI))
	mux.HandleFunc("/admin/api/retries/preview", s.wrapAPI(s.handleAdminRetriesPreview))
	mux.HandleFunc("/admin/api/retries/apply", s.wrapAPI(s.handleAdminRetriesApply))
	mux.HandleFunc("/admin/api/templates", s.wrapAPI(s.handleAdminTemplatesAPI))
	mux.HandleFunc("/admin/api/templates/render", s.wrapAPI(s.handleAdminTemplateRender))
	mux.HandleFunc("/admin/api/templates/preview", s.wrapAPI(s.handleAdminTemplatePreview))
	mux.HandleFunc("/admin/api/templates/apply", s.wrapAPI(s.handleAdminTemplateApply))
	mux.HandleFunc("/admin/api/templates/auto-replies/render", s.wrapAPI(s.handleAdminAutoReplyRender))
	mux.HandleFunc("/admin/api/templates/auto-replies/preview", s.wrapAPI(s.handleAdminAutoReplyPreview))
	mux.HandleFunc("/admin/api/templates/closing-reminders/preview", s.wrapAPI(s.handleAdminClosingReminderPreview))
	mux.HandleFunc("/admin/api/logout", s.wrapAPI(s.handleAdminLogout))
	// 静态前端不包含凭据，setup 向导在建立 session 前也需要加载它们。
	mux.HandleFunc("/admin/assets/app.js", s.handleAdminAppJS)
	mux.HandleFunc("/admin/assets/styles.css", s.handleAdminStylesCSS)
	mux.HandleFunc("/admin", s.handleAdminRoot)
	mux.HandleFunc("/admin/security", s.handleAdminRoot)
	mux.HandleFunc("/admin/device-bind", s.handleAdminDeviceBindPage)
	mux.HandleFunc("/admin/groups", s.handleAdminGroupsRoute)
	mux.HandleFunc("/admin/switches", s.handleAdminRoot)
	mux.HandleFunc("/admin/templates", s.handleAdminRoot)
	mux.HandleFunc("/admin/snapshots", s.handleAdminRoot)
	mux.HandleFunc("/admin/template-library", s.handleAdminRoot)
	mux.HandleFunc("/admin/risk-radar", s.handleAdminRoot)
	mux.HandleFunc("/admin/insights", s.handleAdminRoot)
	mux.HandleFunc("/admin/retries", s.handleAdminRoot)
	mux.HandleFunc("/admin/group", s.wrap(s.handleGroup))
	mux.HandleFunc("/admin/permissions", s.handleAdminPermissionsRoute)
	mux.HandleFunc("/admin/orders", s.wrap(s.handleOrders))
	mux.HandleFunc("/admin/broadcast", s.handleAdminBroadcastRoute)
	mux.HandleFunc("/admin/schedules", s.handleAdminSchedulesRoute)
	mux.HandleFunc("/admin/automation", s.wrap(s.handleAutomation))
	mux.HandleFunc("/admin/kill-switch", s.wrap(s.handleKillSwitch))
	mux.HandleFunc("/admin/bank", s.wrap(s.handleBank))
	mux.HandleFunc("/admin/private-users", s.wrap(s.handlePrivateUsers))
	handler := s.withSecurityHeaders(mux)
	handler = system.WrapHTTPHandlerWithTracing(handler, "admin-server")
	if s == nil || s.auditTrail == nil {
		return handler
	}
	return s.auditTrail.Wrap(handler)
}

func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setHTTPSecurityHeaders(w)
		next.ServeHTTP(w, r)
	})
}

func setHTTPSecurityHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	w.Header().Set("Strict-Transport-Security", "max-age=15552000")
	w.Header().Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self' ws: wss:",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'self' https://web.telegram.org https://*.telegram.org",
	}, "; "))
}

func (s *Server) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthorized(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`<h3>后台鉴权失败</h3><p>请在 URL 添加 token 参数，或设置请求头 X-Admin-Token。</p>`))
			return
		}
		if !isSameOriginUnsafeRequest(r) {
			http.Error(w, "跨站请求被拒绝", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) wrapAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.adminAPISessionUsername(r) == "" {
			s.writeAdminJSON(w, http.StatusUnauthorized, map[string]any{
				"ok":    false,
				"error": "unauthorized",
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
		s.refreshAdminSessionCookie(w, r)
		next(w, r)
	}
}

func (s *Server) isAuthorized(r *http.Request) bool {
	token := strings.TrimSpace(s.cfg.AdminToken)
	loginEnabled := s.adminLoginEnabled()
	if token == "" && !loginEnabled {
		return false
	}
	queryToken := strings.TrimSpace(r.URL.Query().Get("token"))
	headerToken := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if token != "" && (secureTokenEqual(token, queryToken) || secureTokenEqual(token, headerToken)) {
		return true
	}
	return s.adminSessionUsername(r) != ""
}

func secureTokenEqual(expected, candidate string) bool {
	expected = strings.TrimSpace(expected)
	candidate = strings.TrimSpace(candidate)
	if expected == "" || candidate == "" {
		return false
	}
	expectedSum := sha256.Sum256([]byte(expected))
	candidateSum := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(expectedSum[:], candidateSum[:]) == 1
}

func isSameOriginUnsafeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || method == "" {
		return true
	}
	requestOrigin := requestOrigin(r)
	if requestOrigin == "" {
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return requestURLOriginMatches(origin, requestOrigin)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return requestURLOriginMatches(referer, requestOrigin)
	}
	return true
}

func isStrictSameOriginUnsafeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || method == "" {
		return true
	}
	origin := requestOrigin(r)
	if origin == "" {
		return false
	}
	if header := strings.TrimSpace(r.Header.Get("Origin")); header != "" {
		return requestURLOriginMatches(header, origin)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return requestURLOriginMatches(referer, origin)
	}
	return false
}

func requestOrigin(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(strings.Split(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), ",")[0]))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = strings.TrimSpace(r.URL.Host)
	}
	return canonicalOrigin(scheme, host)
}

func requestURLOriginMatches(rawURL, requestOrigin string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil {
		return false
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return canonicalOrigin(parsed.Scheme, parsed.Host) == requestOrigin
}

func canonicalOrigin(scheme, authority string) string {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	authority = strings.ToLower(strings.TrimSpace(authority))
	if scheme == "" || authority == "" {
		return ""
	}
	parsed, err := url.Parse(scheme + "://" + authority)
	if err != nil || parsed == nil || parsed.Hostname() == "" {
		return ""
	}
	port := parsed.Port()
	if port == "" {
		port = defaultPortForScheme(scheme)
	}
	if port == "" {
		return ""
	}
	return scheme + "://" + net.JoinHostPort(parsed.Hostname(), port)
}

func defaultPortForScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func parseLimitedAdminForm(w http.ResponseWriter, r *http.Request) error {
	if r != nil && r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxAdminFormBodyBytes)
	}
	return r.ParseForm()
}

func (s *Server) navHTML() string {
	items := []struct {
		Path string
		Name string
	}{
		{Path: "/admin", Name: "首页"},
		{Path: "/admin/groups", Name: "群组管理"},
		{Path: "/admin/permissions", Name: "权限管理"},
		{Path: "/admin/orders", Name: "查单管理"},
		{Path: "/admin/broadcast", Name: "群发面板"},
		{Path: "/admin/schedules", Name: "定时群发"},
		{Path: "/admin/automation", Name: "自动化管理"},
		{Path: "/admin/private-users", Name: "用户订阅"},
		{Path: "/admin/kill-switch", Name: "紧急停机"},
		{Path: "/admin/bank", Name: "联行号查询"},
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf(`<a href="%s">%s</a>`, s.withToken(item.Path), html.EscapeString(item.Name)))
	}
	return strings.Join(parts, " | ")
}

func (s *Server) writePage(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">`))
	_, _ = w.Write([]byte(`<title>` + html.EscapeString(title) + `</title>`))
	_, _ = w.Write([]byte(`<style>
body{font-family:"Microsoft YaHei","PingFang SC",sans-serif;margin:18px;color:#1f2937}
h1{font-size:24px;margin:0 0 8px} h2{font-size:18px;margin:18px 0 8px}
table{border-collapse:collapse;width:100%;margin-top:10px}
th,td{border:1px solid #e5e7eb;padding:8px;vertical-align:top;font-size:13px}
th{background:#f3f4f6;text-align:left}
input,select,textarea,button{font-size:14px;padding:6px}
textarea{width:100%;min-height:90px}
.top{margin-bottom:12px;padding:10px;background:#f9fafb;border:1px solid #e5e7eb}
.msg{padding:10px;background:#ecfeff;border:1px solid #67e8f9;margin:10px 0}
.err{padding:10px;background:#fef2f2;border:1px solid #fca5a5;margin:10px 0}
.muted{color:#6b7280;font-size:12px}
pre{white-space:pre-wrap;background:#f8fafc;padding:12px;border:1px solid #e5e7eb}
</style></head><body>`))
	_, _ = w.Write([]byte(`<div class="top"><h1>` + html.EscapeString(title) + `</h1><div>` + s.navHTML() + `</div></div>`))
	_, _ = w.Write([]byte(body))
	_, _ = w.Write([]byte(`</body></html>`))
}

func (s *Server) withToken(rawPath string) string {
	token := strings.TrimSpace(s.cfg.AdminToken)
	if token == "" {
		return rawPath
	}
	if strings.Contains(rawPath, "?") {
		return rawPath + "&token=" + url.QueryEscape(token)
	}
	return rawPath + "?token=" + url.QueryEscape(token)
}

func containsFold(raw, key string) bool {
	return strings.Contains(strings.ToLower(raw), strings.ToLower(key))
}

type tgWebAppExecRequest struct {
	UserID       int64  `json:"user_id"`
	Exp          int64  `json:"exp"`
	Sig          string `json:"sig"`
	TargetChatID int64  `json:"target_chat_id"`
	Command      string `json:"command"`
}

type tgWebAppExecResponse struct {
	OK          bool   `json:"ok"`
	Message     string `json:"message"`
	DisplayText string `json:"display_text,omitempty"`
	Mono        bool   `json:"mono,omitempty"`
	OpenURL     string `json:"open_url,omitempty"`
}

type tgWebAppLedgerCardResponse struct {
	OK      bool                  `json:"ok"`
	Message string                `json:"message,omitempty"`
	Data    *tgWebAppLedgerCardVM `json:"data,omitempty"`
}

type tgWebAppLedgerCardVM struct {
	Deposits           []tgWebAppLedgerDeposit `json:"deposits"`
	Payouts            []tgWebAppLedgerPayout  `json:"payouts"`
	FeeRate            float64                 `json:"feeRate"`
	GlobalExchangeRate float64                 `json:"globalExchangeRate"`
	MessageTime        string                  `json:"messageTime"`
}

type tgWebAppLedgerDeposit struct {
	Time           string  `json:"time"`
	Amount         float64 `json:"amount"`
	Rate           float64 `json:"rate"`
	FeeFactor      float64 `json:"feeFactor"`
	Converted      float64 `json:"converted"`
	Currency       string  `json:"currency"`
	SourceCurrency string  `json:"sourceCurrency,omitempty"`
	Track          string  `json:"track,omitempty"`
	Remarks        string  `json:"remarks,omitempty"`
}

type tgWebAppLedgerPayout struct {
	Time      string  `json:"time"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Converted string  `json:"converted,omitempty"`
	PayoutU   float64 `json:"payoutU"`
	Remarks   string  `json:"remarks,omitempty"`
}

type tgWebAppLooklookRow struct {
	OccurredAt       time.Time
	TimeText         string
	EntryType        string
	IsReversal       bool
	Amount           float64
	Currency         string
	FeeFactor        float64
	Rate             float64
	ConvertedU       float64
	MarkerName       string
	MarkerUserID     int64
	MarkerUsername   string
	OperatorName     string
	OperatorUserID   int64
	OperatorUsername string
	Remarks          string
	Track            string
}

type tgWebAppLooklookPartyStat struct {
	Name          string  `json:"name"`
	UserID        int64   `json:"userID,omitempty"`
	Username      string  `json:"username,omitempty"`
	Count         int     `json:"count"`
	DepositAmount float64 `json:"depositAmount"`
	DepositU      float64 `json:"depositU"`
	PayoutAmount  float64 `json:"payoutAmount"`
	PayoutU       float64 `json:"payoutU"`
}

type tgWebAppLooklookDailyStat struct {
	Day      string  `json:"day"`
	DepositU float64 `json:"depositU"`
	PayoutU  float64 `json:"payoutU"`
}

type tgWebAppLooklookRateStat struct {
	Rate  string `json:"rate"`
	Count int    `json:"count"`
}

type tgWebAppLooklookUserStat struct {
	Name     string  `json:"name"`
	Count    int     `json:"count"`
	DepositU float64 `json:"depositU"`
	PayoutU  float64 `json:"payoutU"`
	NetU     float64 `json:"netU"`
}

type tgWebAppLooklookView struct {
	Mode          string
	AllowCZR      bool
	QueryMode     string
	GroupID       int64
	GroupTitle    string
	BillDateText  string
	BeginTimeText string
	EndTimeText   string
	BeginTimeISO  string
	EndTimeISO    string
	DateRangeText string
	FirstName     string
	Rows          []tgWebAppLooklookRow

	TotalDepositAmount float64
	TotalPayoutAmount  float64
	TotalDepositU      float64
	TotalPayoutU       float64

	MarkerStats   []tgWebAppLooklookPartyStat
	OperatorStats []tgWebAppLooklookPartyStat
	DailyData     []tgWebAppLooklookDailyStat
	RateStats     []tgWebAppLooklookRateStat
	UserStats     []tgWebAppLooklookUserStat
}

func isTGWebAppPrivateAction(command string) bool {
	switch strings.TrimSpace(command) {
	case "联系管理员", "获取ID", "邀请进群", "使用说明", "到期时间", "人工客服":
		return true
	default:
		return false
	}
}

func renderTGWebAppRateBoardText(payLabel, average string, quotes []string) string {
	payLabel = strings.TrimSpace(payLabel)
	if payLabel == "" {
		payLabel = "全部"
	}
	average = strings.TrimSpace(average)
	if average == "" {
		average = "0"
	}
	cleanQuotes := make([]string, 0, len(quotes))
	for _, quote := range quotes {
		quote = strings.TrimSpace(quote)
		if quote != "" {
			cleanQuotes = append(cleanQuotes, quote)
		}
	}
	lines := []string{
		"OKX 实时汇率",
		"------------------------------",
		"支付方式：" + payLabel,
		"法币市场：人民币 (CNY)",
		"Top10均价：" + average + " CNY/USDT",
		fmt.Sprintf("样本数量：%d", len(cleanQuotes)),
		"请求交易所：OKX",
		"实际来源：OKX",
		"------------------------------",
		"今日 Okx 商家实时交易汇率 Top10:",
	}
	if len(cleanQuotes) == 0 {
		lines = append(lines, "暂无有效报价")
	} else {
		lines = append(lines, cleanQuotes...)
	}
	return strings.Join(lines, "\n")
}

func (s *Server) handleTGWebApp(w http.ResponseWriter, r *http.Request) {
	setWebAppNoCache(w)
	uid, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("uid")), 10, 64)
	exp, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("exp")), 10, 64)
	sig := strings.TrimSpace(r.URL.Query().Get("sig"))
	if err := s.app.VerifyWebAppAuth(uid, exp, sig); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<h3>WebApp 鉴权失败</h3><p>` + html.EscapeString(err.Error()) + `</p>`))
		return
	}
	groups, _ := s.app.ListWebAppAccessibleGroups(uid)
	selectedChat := strings.TrimSpace(r.URL.Query().Get("chat_id"))
	if selectedChat == "" && len(groups) > 0 {
		selectedChat = strconv.FormatInt(groups[0].ChatID, 10)
	}

	options := make([]string, 0, len(groups))
	for _, row := range groups {
		title := orDefault(strings.TrimSpace(row.Title), strconv.FormatInt(row.ChatID, 10))
		selected := ""
		if selectedChat == strconv.FormatInt(row.ChatID, 10) {
			selected = " selected"
		}
		options = append(options, fmt.Sprintf(`<option value="%d"%s>%s (%d)</option>`,
			row.ChatID, selected, html.EscapeString(title), row.ChatID))
	}
	optionHTML := strings.Join(options, "")
	if optionHTML == "" {
		optionHTML = `<option value="">暂无可操作群</option>`
	}
	rateBoardText := html.EscapeString(renderTGWebAppRateBoardText("全部", "7.35", []string{
		"1) 7.35 CNY/USDT  广深商行",
		"2) 7.35 CNY/USDT  金大福U商",
		"3) 7.35 CNY/USDT  正旺商行",
	}))
	jsSig := strconv.Quote(sig)
	page := fmt.Sprintf(`<!doctype html>
<html lang="zh-CN" class="h-full">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>查记机器人 - Telegram UI Replica</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <style>
    :root {
      --tg-bg: #eef8f1;
      --tg-pattern-a: rgba(255, 255, 255, 0.4);
      --tg-pattern-b: rgba(134, 239, 172, 0.18);
      --tg-bot-bubble: #ffffff;
      --tg-user-bubble: #d8fdd2;
      --tg-text: #1c1e21;
      --tg-muted: #6b7280;
      --tg-reply-zone: #f0f2f5;
      --tg-line: #d9dde3;
      --tg-btn-text: #111827;
      --tg-status: #4b5563;
    }
    html.tg-dark {
      --tg-bg: #0f1722;
      --tg-pattern-a: rgba(255, 255, 255, 0.05);
      --tg-pattern-b: rgba(23, 36, 50, 0.35);
      --tg-bot-bubble: #ffffff;
      --tg-user-bubble: #d8fdd2;
      --tg-text: #e5e7eb;
      --tg-muted: #9ca3af;
      --tg-reply-zone: #1a2430;
      --tg-line: #2d3a48;
      --tg-btn-text: #111827;
      --tg-status: #cbd5e1;
    }
    body {
      color: var(--tg-text);
      background: var(--tg-bg);
    }
    .telegram-wallpaper {
      background-color: var(--tg-bg);
      background-image:
        radial-gradient(circle at 1px 1px, var(--tg-pattern-a) 1px, transparent 0),
        linear-gradient(45deg, var(--tg-pattern-b) 25%%, transparent 25%%, transparent 50%%, var(--tg-pattern-b) 50%%, var(--tg-pattern-b) 75%%, transparent 75%%, transparent);
      background-size: 22px 22px, 44px 44px;
      background-position: 0 0, 0 0;
    }
    .tg-muted { color: var(--tg-muted); }
    .tg-select {
      background: rgba(255, 255, 255, 0.9);
      color: #111827;
      border: 1px solid var(--tg-line);
      border-radius: 10px;
      padding: 8px 10px;
      font-size: 13px;
      outline: none;
      width: 100%%;
    }
    .tg-bubble {
      position: relative;
      border-radius: 16px;
      box-shadow: 0 1px 2px rgba(0, 0, 0, 0.12);
      overflow: hidden;
      width: fit-content;
      max-width: 560px;
    }
    .tg-bot {
      background: var(--tg-bot-bubble);
      color: #111827;
      border-top-left-radius: 6px;
    }
    .tg-user {
      background: var(--tg-user-bubble);
      color: #111827;
      border-top-right-radius: 6px;
    }
    .bubble-left::before {
      content: "";
      position: absolute;
      left: -8px;
      bottom: 10px;
      width: 0;
      height: 0;
      border-top: 8px solid transparent;
      border-bottom: 8px solid transparent;
      border-right: 8px solid var(--tg-bot-bubble);
      filter: drop-shadow(0 1px 1px rgba(0, 0, 0, 0.08));
    }
    .bubble-right::after {
      content: "";
      position: absolute;
      right: -8px;
      bottom: 10px;
      width: 0;
      height: 0;
      border-top: 8px solid transparent;
      border-bottom: 8px solid transparent;
      border-left: 8px solid var(--tg-user-bubble);
      filter: drop-shadow(0 1px 1px rgba(0, 0, 0, 0.08));
    }
    .sys-tip {
      font-size: 12px;
      line-height: 1.2;
      color: var(--tg-muted);
      text-align: center;
      margin: 2px auto 4px;
    }
    .bill-shell {
      overflow: hidden;
      border-radius: 18px;
      background: #fff;
      box-shadow: 0 2px 8px rgba(15, 23, 42, 0.12);
      max-width: 620px;
      width: min(620px, calc(100vw - 70px));
    }
    .bill-body {
      padding: 12px 14px 8px;
      background: #fff;
      color: #1f2937;
      font-size: 13px;
      line-height: 1.46;
    }
    .bill-title {
      font-size: 13px;
      font-weight: 600;
      margin: 0 0 4px;
      color: #111827;
    }
    .bill-line {
      margin: 1px 0;
      word-break: break-word;
    }
    .bill-gap {
      height: 8px;
    }
    .bill-accent {
      color: #3390ec;
      font-weight: 600;
    }
    .bill-mono {
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
    }
    .bill-foot-time {
      margin-top: 8px;
      text-align: right;
      color: #9ca3af;
      font-size: 11px;
      line-height: 1;
    }
    .bill-jump-btn {
      width: 100%%;
      border: 0;
      border-top: 1px solid rgba(17, 24, 39, 0.06);
      background: #86efac;
      color: #ffffff;
      font-size: 14px;
      line-height: 1.15;
      font-weight: 600;
      padding: 11px 12px;
      text-align: center;
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 6px;
    }
    .bill-jump-arrow {
      font-size: 13px;
      line-height: 1;
      opacity: 0.95;
    }
    .inline-mini {
      border: 1px solid var(--tg-line);
      border-radius: 10px;
      background: transparent;
      color: #3390ec;
      font-size: 13px;
      padding: 8px 4px;
      text-align: center;
      line-height: 1.1;
      min-height: 40px;
    }
    .inline-mini.is-active {
      background: rgba(51, 144, 236, 0.12);
      border-color: rgba(51, 144, 236, 0.55);
    }
    .inline-full {
      width: 100%%;
      border: 1px solid var(--tg-line);
      border-radius: 10px;
      background: transparent;
      color: #3390ec;
      font-size: 14px;
      padding: 9px 10px;
      margin-top: 8px;
    }
    .reply-zone {
      background: var(--tg-reply-zone);
      border-top: 1px solid var(--tg-line);
      box-shadow: 0 -3px 12px rgba(0, 0, 0, 0.08);
    }
    .reply-key {
      position: relative;
      overflow: hidden;
      background: #ffffff;
      color: var(--tg-btn-text);
      border: 1px solid rgba(17, 24, 39, 0.08);
      border-radius: 10px;
      font-size: 14px;
      line-height: 1.2;
      text-align: center;
      padding: 10px 8px;
      user-select: none;
    }
    .ripple-dot {
      position: absolute;
      border-radius: 9999px;
      transform: scale(0);
      animation: ripple 520ms linear;
      background: rgba(51, 144, 236, 0.25);
      pointer-events: none;
    }
    @keyframes ripple {
      to {
        transform: scale(4);
        opacity: 0;
      }
    }
    .status-line {
      font-size: 12px;
      line-height: 1.2;
      color: var(--tg-status);
      text-align: center;
      min-height: 16px;
    }
  </style>
</head>
<body class="h-full antialiased">
  <div class="h-full flex flex-col">
    <header class="shrink-0 border-b" style="border-color: var(--tg-line); background: rgba(255,255,255,0.18); backdrop-filter: blur(8px);">
      <div class="max-w-[820px] mx-auto px-3 pt-2 pb-2">
        <div class="flex items-center gap-3">
          <div class="h-9 w-9 rounded-full bg-[#3390ec] text-white flex items-center justify-center text-sm font-semibold">查</div>
          <div class="flex-1 min-w-0">
            <div class="text-[14px] leading-tight font-semibold">查记机器人</div>
            <div class="text-[12px] leading-tight tg-muted">Telegram 原生交互复刻</div>
          </div>
          <button id="themeToggle" class="px-2 py-1 rounded-md text-[12px] border" style="border-color: var(--tg-line);">切换主题</button>
        </div>
        <div class="mt-2">
          <label class="text-[12px] tg-muted">目标群组</label>
          <select id="chatSelect" class="tg-select mt-1">%s</select>
        </div>
      </div>
    </header>

    <main id="chatScroll" class="flex-1 overflow-y-auto telegram-wallpaper">
      <div id="chatBody" class="max-w-[820px] mx-auto px-3 py-4 space-y-3 pb-40">
        <div class="sys-tip">Today</div>

        <div class="flex justify-end">
          <div class="tg-bubble tg-user bubble-right px-3 py-2 text-[14px] leading-[1.3]">开始记账</div>
        </div>

        <div class="flex justify-start">
          <div class="tg-bubble tg-bot bubble-left bill-shell">
            <div id="billCardContent" class="bill-body"></div>
            <button id="billJumpBtn" class="bill-jump-btn" type="button">
              <span>点击跳转完整账单</span>
              <span class="bill-jump-arrow">↗</span>
            </button>
          </div>
        </div>

        <div class="flex justify-start">
          <div class="tg-bubble tg-bot bubble-left px-3 py-3">
            <pre id="rateBoardText" class="text-[13px] leading-[1.45] whitespace-pre-wrap mb-2">%s</pre>
            <div class="grid grid-cols-4 gap-2">
              <button class="inline-mini is-active" type="button" data-rate="all" data-cmd="z0">所有✅</button>
              <button class="inline-mini" type="button" data-rate="bank" data-cmd="lk">银行卡</button>
              <button class="inline-mini" type="button" data-rate="alipay" data-cmd="lz">支付宝</button>
              <button class="inline-mini" type="button" data-rate="wechat" data-cmd="lw">微信</button>
            </div>
            <button id="okxJumpBtn" class="inline-full" type="button">🌐 欧易直达</button>
          </div>
        </div>
      </div>
    </main>

    <footer class="reply-zone shrink-0 fixed inset-x-0 bottom-0">
      <div class="max-w-[820px] mx-auto px-3 py-2">
        <div id="statusLine" class="status-line">等待操作…</div>
        <div class="grid grid-cols-2 gap-2 mt-2">
          <button class="reply-key" type="button" data-action="联系管理员">联系管理员</button>
          <button class="reply-key" type="button" data-action="获取ID">获取ID</button>
          <button class="reply-key" type="button" data-action="邀请进群">邀请进群</button>
          <button class="reply-key" type="button" data-action="使用说明">使用说明</button>
          <button class="reply-key" type="button" data-action="到期时间">到期时间</button>
          <button class="reply-key" type="button" data-action="人工客服">人工客服</button>
        </div>
      </div>
    </footer>
  </div>

  <script>
    const uid = %d;
    const exp = %d;
    const sig = %s;
    const wsScheme = window.location.protocol === "https:" ? "wss" : "ws";
    const chatSelect = document.getElementById("chatSelect");
    const statusLine = document.getElementById("statusLine");
    const chatBody = document.getElementById("chatBody");
    const chatScroll = document.getElementById("chatScroll");
    let stateWS = null;

    const ratePayLabels = {
      all: "全部",
      bank: "银行卡",
      alipay: "支付宝",
      wechat: "微信"
    };
    const rateAverages = {
      all: "7.35",
      bank: "7.34",
      alipay: "7.35",
      wechat: "7.35"
    };
    const rateSamples = {
      all: ["1) 7.35 CNY/USDT  广深商行", "2) 7.35 CNY/USDT  金大福U商", "3) 7.35 CNY/USDT  正旺商行"],
      bank: ["1) 7.34 CNY/USDT  深发商行", "2) 7.34 CNY/USDT  恒通商行", "3) 7.33 CNY/USDT  鑫隆商行"],
      alipay: ["1) 7.35 CNY/USDT  远航支付宝", "2) 7.34 CNY/USDT  聚合商贸", "3) 7.34 CNY/USDT  金鼎商行"],
      wechat: ["1) 7.35 CNY/USDT  万象微信", "2) 7.34 CNY/USDT  安稳商贸", "3) 7.33 CNY/USDT  海纳商行"]
    };
    const billCardEmptyData = {
      deposits: [],
      payouts: [],
      feeRate: 0,
      globalExchangeRate: 1,
      messageTime: "--:--"
    };

    function toSafeNumber(value, fallback) {
      const n = Number(value);
      if (Number.isFinite(n)) {
        return n;
      }
      return fallback;
    }

    function formatFixed2(value) {
      return toSafeNumber(value, 0).toFixed(2);
    }

    function formatRate(value) {
      const n = toSafeNumber(value, 0);
      if (Number.isInteger(n)) {
        return String(n);
      }
      return String(n);
    }

    function formatAmountMaybeInt(value) {
      const n = toSafeNumber(value, 0);
      if (Number.isInteger(n)) {
        return String(n);
      }
      return n.toFixed(2);
    }

    function escapeHTML(value) {
      return String(value || "")
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#39;");
    }

    function computeBillCardView(data) {
      const safe = data || {};
      const deposits = Array.isArray(safe.deposits) ? safe.deposits : [];
      const payouts = Array.isArray(safe.payouts) ? safe.payouts : [];
      const feeRate = toSafeNumber(safe.feeRate, 0);
      const globalExchangeRate = toSafeNumber(safe.globalExchangeRate, 0);

      let totalDepositFiat = 0;
      let totalDepositU = 0;
      const depositLines = deposits.map(function(item) {
        const amount = toSafeNumber(item && item.amount, 0);
        const rate = toSafeNumber(item && item.rate, 1) || 1;
        const sourceCurrency = String((item && item.sourceCurrency) || "").trim().toUpperCase();
        const defaultFeeFactor = 1 - feeRate / 100;
        const feeFactor = toSafeNumber(item && item.feeFactor, defaultFeeFactor);
        const fallbackConverted = sourceCurrency === "USDT"
          ? amount * feeFactor
          : amount * feeFactor / rate;
        const converted = toSafeNumber(item && item.converted, fallbackConverted);
        totalDepositFiat += amount;
        totalDepositU += converted;
        return {
          time: (item && item.time) || "--:--:--",
          amount: formatFixed2(amount),
          rate: formatRate(rate),
          feeFactor: formatRate(feeFactor),
          converted: formatFixed2(converted),
          currency: (item && item.currency) || "U",
          sourceCurrency: sourceCurrency,
          track: String((item && item.track) || "").trim(),
          remarks: (item && item.remarks) || ""
        };
      });

      let totalPayout = 0;
      const payoutLines = payouts.map(function(item) {
        const amount = toSafeNumber(item && item.amount, 0);
        const payoutU = toSafeNumber(item && item.payoutU, amount);
        totalPayout += payoutU;
		return {
			time: (item && item.time) || "--:--:--",
			amount: formatAmountMaybeInt(amount),
			currency: (item && item.currency) || "",
			converted: String((item && item.converted) || "").trim(),
			payoutU: formatFixed2(payoutU),
			remarks: (item && item.remarks) || ""
		};
      });

      const expectedPayout = totalDepositU;
      const remaining = expectedPayout - totalPayout;
      return {
        depositCount: depositLines.length,
        payoutCount: payoutLines.length,
        depositLines: depositLines,
        payoutLines: payoutLines,
        totalDepositFiat: formatFixed2(totalDepositFiat),
        totalDepositU: formatFixed2(totalDepositU),
        feeRate: formatRate(feeRate),
        globalExchangeRate: formatRate(globalExchangeRate),
        expectedPayout: formatFixed2(expectedPayout),
        totalPayout: formatAmountMaybeInt(totalPayout),
        remaining: formatFixed2(remaining),
        messageTime: safe.messageTime || "--:--"
      };
    }

    function renderBillCard(data) {
      const root = document.getElementById("billCardContent");
      if (!root) {
        return;
      }
      const view = computeBillCardView(data);
      const depositHtml = view.depositLines.map(function(item) {
        const trackText = item.track ? " (" + escapeHTML(item.track) + ")" : "";
        const formulaText = item.sourceCurrency === "USDT"
          ? ' * <span class="bill-accent">' + escapeHTML(item.feeFactor) + '</span> = '
          : ' * <span class="bill-accent">' + escapeHTML(item.feeFactor) + '</span> / <span class="bill-accent">' + escapeHTML(item.rate) + '</span> = ';
        return (
          '<div class="bill-line bill-mono">' +
            '<span class="bill-accent">' + escapeHTML(item.time) + '</span> ' +
            '<span class="bill-accent">' + escapeHTML(item.amount) + '</span>' + trackText +
            formulaText +
            '<span class="bill-accent">' + escapeHTML(item.converted) + '</span> ' + escapeHTML(item.currency) +
            (item.remarks ? " " + escapeHTML(item.remarks) : "") +
          '</div>'
        );
      }).join("");
      const payoutHtml = view.payoutLines.map(function(item) {
        const convertedText = item.converted ? ' [' + escapeHTML(item.converted) + ']' : '';
        const remarks = item.remarks ? " " + escapeHTML(item.remarks) : "";
        return (
          '<div class="bill-line bill-mono">' +
            '<span class="bill-accent">' + escapeHTML(item.time) + '</span> ' +
            '<span class="bill-accent">' + escapeHTML(item.amount) + '</span> ' + escapeHTML(item.currency) +
            convertedText + remarks +
          '</div>'
        );
      }).join("");
      root.innerHTML = '' +
        '<div class="bill-title">入款（<span class="bill-accent">' + view.depositCount + '</span>）笔：</div>' +
        (depositHtml || '<div class="bill-line bill-mono">--</div>') +
        '<div class="bill-gap"></div>' +
        '<div class="bill-title">下发（<span class="bill-accent">' + view.payoutCount + '</span>）笔：</div>' +
        (payoutHtml || '<div class="bill-line bill-mono">--</div>') +
        '<div class="bill-gap"></div>' +
        '<div class="bill-line">当前入款：<span class="bill-accent">' + view.totalDepositFiat + '</span> | <span class="bill-accent">' + view.totalDepositU + '</span> USD</div>' +
        '<div class="bill-line">入款费率：<span class="bill-accent">' + view.feeRate + '</span>%%</div>' +
        '<div class="bill-line">入款汇率：<span class="bill-accent">' + view.globalExchangeRate + '</span></div>' +
        '<div class="bill-gap"></div>' +
        '<div class="bill-line">应下发：<span class="bill-accent">' + view.expectedPayout + '</span> USD</div>' +
        '<div class="bill-line">总下发：<span class="bill-accent">' + view.totalPayout + '</span> USD</div>' +
        '<div class="bill-line">未下发：<span class="bill-accent">' + view.remaining + '</span> USD</div>' +
        '<div class="bill-foot-time">' + escapeHTML(view.messageTime) + '</div>';
    }

    async function fetchBillCardData(options) {
      const opts = options || {};
      const target = Number(chatSelect.value || "0");
      if (!target) {
        renderBillCard(billCardEmptyData);
        if (!opts.silentStatus) {
          setStatus("请选择可操作的目标群。");
        }
        return false;
      }
      const query = new URLSearchParams({
        uid: String(uid),
        exp: String(exp),
        sig: sig,
        chat_id: String(target),
        _ts: String(Date.now())
      });
      try {
        const resp = await fetch("/tg/webapp/ledger/card?" + query.toString(), {
          method: "GET"
        });
        const data = await resp.json().catch(function() {
          return { ok: false, message: "账单数据解析失败" };
        });
        if (!resp.ok) {
          data.ok = false;
        }
        if (!data.ok || !data.data) {
          renderBillCard(billCardEmptyData);
          if (!opts.silentStatus) {
            setStatus(data.message || "账单加载失败");
          }
          return false;
        }
        renderBillCard(data.data);
        if (!opts.silentStatus) {
          setStatus("账单卡片已更新。");
        }
        return true;
      } catch (err) {
        renderBillCard(billCardEmptyData);
        if (!opts.silentStatus) {
          setStatus("账单加载失败：" + err);
        }
        return false;
      }
    }

    function setStatus(text) {
      statusLine.textContent = text || "等待操作…";
    }

    function scrollToBottom() {
      chatScroll.scrollTop = chatScroll.scrollHeight;
    }

    function appendSystemTip(text) {
      const tip = document.createElement("div");
      tip.className = "sys-tip";
      tip.textContent = text;
      chatBody.appendChild(tip);
      scrollToBottom();
    }

    function appendUserMessage(text) {
      const row = document.createElement("div");
      row.className = "flex justify-end";
      const bubble = document.createElement("div");
      bubble.className = "tg-bubble tg-user bubble-right px-3 py-2 text-[14px] leading-[1.3]";
      bubble.textContent = text;
      row.appendChild(bubble);
      chatBody.appendChild(row);
      scrollToBottom();
    }

    function appendBotMessage(text, mono) {
      const row = document.createElement("div");
      row.className = "flex justify-start";
      const bubble = document.createElement("div");
      bubble.className = "tg-bubble tg-bot bubble-left px-3 py-2";
      if (mono) {
        const pre = document.createElement("pre");
        pre.className = "text-[13px] leading-[1.45] whitespace-pre-wrap mb-0";
        pre.textContent = text;
        bubble.appendChild(pre);
      } else {
        bubble.className += " text-[14px] leading-[1.35]";
        bubble.textContent = text;
      }
      row.appendChild(bubble);
      chatBody.appendChild(row);
      scrollToBottom();
    }

    function appendBotLink(text, href) {
      const row = document.createElement("div");
      row.className = "flex justify-start";
      const bubble = document.createElement("div");
      bubble.className = "tg-bubble tg-bot bubble-left px-3 py-2 text-[14px] leading-[1.35]";
      const link = document.createElement("a");
      link.href = href;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      link.className = "text-[#3390ec] underline";
      link.textContent = text;
      bubble.appendChild(link);
      row.appendChild(bubble);
      chatBody.appendChild(row);
      scrollToBottom();
    }

    function addRipple(button, event) {
      const rect = button.getBoundingClientRect();
      const ripple = document.createElement("span");
      ripple.className = "ripple-dot";
      const size = Math.max(rect.width, rect.height);
      ripple.style.width = size + "px";
      ripple.style.height = size + "px";
      ripple.style.left = (event.clientX - rect.left - size / 2) + "px";
      ripple.style.top = (event.clientY - rect.top - size / 2) + "px";
      button.appendChild(ripple);
      window.setTimeout(function(){ ripple.remove(); }, 520);
    }

    async function runCommand(command, options) {
      const opts = options || {};
      const cmd = (command || "").trim();
      const target = Number(chatSelect.value || "0");
      const requireTarget = opts.requireTarget !== false;
      const appendTip = opts.appendTip !== false;
      const refreshBillCard = opts.refreshBillCard !== false;
      if (requireTarget && !target) {
        setStatus("请选择可操作的目标群。");
        return null;
      }
      if (!cmd) {
        setStatus("命令不能为空。");
        return null;
      }
      setStatus("执行中：" + cmd);
      try {
        const resp = await fetch("/tg/webapp/exec", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            user_id: uid,
            exp: exp,
            sig: sig,
            target_chat_id: target || 0,
            command: cmd
          })
        });
        const data = await resp.json().catch(function() {
          return { ok: false, message: "服务响应格式错误" };
        });
        if (!resp.ok) {
          data.ok = false;
        }
        setStatus(data.message || (data.ok ? "执行成功" : "执行失败"));
        if (appendTip) {
          appendSystemTip((data.ok ? "✓ " : "✕ ") + (data.message || cmd));
        }
        if (data.ok && requireTarget && refreshBillCard) {
          await fetchBillCardData({ silentStatus: true });
        }
        return data;
      } catch (err) {
        setStatus("请求失败：" + err);
        return { ok: false, message: "请求失败：" + err };
      }
    }

    function setRateView(rateKey) {
      const key = rateSamples[rateKey] ? rateKey : "all";
      const list = rateSamples[key];
      document.getElementById("rateBoardText").textContent =
        [
          "OKX 实时汇率",
          "------------------------------",
          "支付方式：" + ratePayLabels[key],
          "法币市场：人民币 (CNY)",
          "Top10均价：" + rateAverages[key] + " CNY/USDT",
          "样本数量：" + list.length,
          "请求交易所：OKX",
          "实际来源：OKX",
          "------------------------------",
          "今日 Okx 商家实时交易汇率 Top10:"
        ].concat(list).join("\n");
      document.querySelectorAll("button[data-rate]").forEach(function(btn){
        btn.classList.toggle("is-active", btn.getAttribute("data-rate") === key);
      });
    }

    async function handleReplyAction(action) {
      const cmd = (action || "").trim();
      if (!cmd) {
        setStatus("未识别操作");
        return;
      }
      appendUserMessage(cmd);
      const data = await runCommand(cmd, { requireTarget: false, appendTip: false, refreshBillCard: false });
      if (!data) {
        return;
      }
      if (data.display_text) {
        appendBotMessage(data.display_text, !!data.mono);
      } else {
        appendBotMessage(data.message || "操作已完成。", false);
      }
      if (data.open_url) {
        appendBotLink("点击打开直达入口", data.open_url);
      }
      appendSystemTip((data.ok ? "✓ " : "✕ ") + (data.message || cmd));
    }

    function connectStateWS() {
      if (stateWS) {
        stateWS.onclose = null;
        stateWS.close();
        stateWS = null;
      }
      const target = Number(chatSelect.value || "0");
      if (!target) {
        setStatus("请选择可操作的目标群。");
        return;
      }
      const wsURL = wsScheme + "://" + window.location.host +
        "/tg/webapp/state/ws?uid=" + encodeURIComponent(String(uid)) +
        "&exp=" + encodeURIComponent(String(exp)) +
        "&sig=" + encodeURIComponent(sig) +
        "&chat_id=" + encodeURIComponent(String(target));

      stateWS = new WebSocket(wsURL);
      stateWS.onopen = function() {
        setStatus("状态通道已连接。");
      };
      stateWS.onmessage = function(evt) {
        try {
          const payload = JSON.parse(evt.data || "{}");
          if (payload.event === "ledger.reversal") {
            appendSystemTip("撤回同步：原记录#" + payload.original_id + "，反向记录#" + payload.reversal_id);
            setStatus("撤回状态已同步。");
            fetchBillCardData({ silentStatus: true });
          }
        } catch (err) {
          setStatus("状态消息解析失败：" + err);
        }
      };
      stateWS.onclose = function() {
        window.setTimeout(connectStateWS, 1500);
      };
    }

    function applyTheme(mode) {
      const root = document.documentElement;
      const isDark = mode === "dark";
      root.classList.toggle("tg-dark", isDark);
      localStorage.setItem("tg_theme_mode", mode);
      document.getElementById("themeToggle").textContent = isDark ? "切到亮色" : "切到暗色";
    }

    const savedMode = localStorage.getItem("tg_theme_mode");
    const systemDark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
    applyTheme(savedMode || (systemDark ? "dark" : "light"));

    document.getElementById("themeToggle").addEventListener("click", function() {
      const dark = document.documentElement.classList.contains("tg-dark");
      applyTheme(dark ? "light" : "dark");
    });

    document.querySelectorAll(".reply-key").forEach(function(btn) {
      btn.addEventListener("click", function(event) {
        addRipple(btn, event);
        handleReplyAction(btn.getAttribute("data-action") || "");
      });
    });

    document.getElementById("billJumpBtn").addEventListener("click", function() {
      appendUserMessage("点击跳转完整账单");
      const target = Number(chatSelect.value || "0");
      if (!target) {
        setStatus("请选择可操作的目标群。");
        return;
      }
      const billURL = new URL("/tg/webapp/looklook.php", window.location.origin);
      billURL.searchParams.set("uid", String(uid));
      billURL.searchParams.set("exp", String(exp));
      billURL.searchParams.set("sig", sig);
      billURL.searchParams.set("type", "bjr");
      billURL.searchParams.set("groupId", String(target));
      window.open(billURL.toString(), "_blank", "noopener,noreferrer");
      appendBotLink("点击打开网页账单", billURL.toString());
      appendSystemTip("✓ 已打开网页账单");
      setStatus("网页账单已打开。");
    });

    document.querySelectorAll("button[data-rate]").forEach(function(btn) {
      btn.addEventListener("click", function() {
        const rateKey = btn.getAttribute("data-rate") || "all";
        const cmd = btn.getAttribute("data-cmd") || "z0";
        setRateView(rateKey);
        appendUserMessage(btn.textContent.trim());
        runCommand(cmd);
      });
    });

    document.getElementById("okxJumpBtn").addEventListener("click", function() {
      window.open("https://www.okx.com/c2c", "_blank", "noopener,noreferrer");
    });

    chatSelect.addEventListener("change", function() {
      setStatus("目标群已切换：" + (chatSelect.value || "未选择"));
      connectStateWS();
      fetchBillCardData({ silentStatus: true });
    });

    renderBillCard(billCardEmptyData);
    setRateView("all");
    connectStateWS();
    fetchBillCardData({ silentStatus: true });
    scrollToBottom();
  </script>
</body>
</html>`, optionHTML, rateBoardText, uid, exp, jsSig)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func (s *Server) handleTGWebAppExec(w http.ResponseWriter, r *http.Request) {
	setWebAppNoCache(w)
	if r.Method != http.MethodPost {
		s.writeWebAppJSON(w, http.StatusMethodNotAllowed, tgWebAppExecResponse{OK: false, Message: "仅支持 POST"})
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxWebAppExecBodyBytes)
	req := tgWebAppExecRequest{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if _, ok := err.(*http.MaxBytesError); ok {
			s.writeWebAppJSON(w, http.StatusRequestEntityTooLarge, tgWebAppExecResponse{OK: false, Message: "请求体过大"})
			return
		}
		s.writeWebAppJSON(w, http.StatusBadRequest, tgWebAppExecResponse{OK: false, Message: "请求体格式错误"})
		return
	}
	if err := s.app.VerifyWebAppAuth(req.UserID, req.Exp, req.Sig); err != nil {
		s.writeWebAppJSON(w, http.StatusUnauthorized, tgWebAppExecResponse{OK: false, Message: "鉴权失败：" + err.Error()})
		return
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		s.writeWebAppJSON(w, http.StatusBadRequest, tgWebAppExecResponse{OK: false, Message: "命令不能为空"})
		return
	}
	if isTGWebAppPrivateAction(command) {
		result, err := s.app.ExecuteWebAppPrivateAction(req.UserID, req.TargetChatID, command)
		if err != nil {
			s.writeWebAppJSON(w, http.StatusForbidden, tgWebAppExecResponse{OK: false, Message: "执行失败：" + err.Error()})
			return
		}
		s.writeWebAppJSON(w, http.StatusOK, tgWebAppExecResponse{
			OK:          true,
			Message:     orDefault(strings.TrimSpace(result.Message), "执行成功："+command),
			DisplayText: strings.TrimSpace(result.DisplayText),
			Mono:        result.Mono,
			OpenURL:     strings.TrimSpace(result.OpenURL),
		})
		return
	}
	if req.TargetChatID == 0 {
		s.writeWebAppJSON(w, http.StatusBadRequest, tgWebAppExecResponse{OK: false, Message: "目标群ID不能为空"})
		return
	}
	if err := s.app.ExecuteWebAppCommand(req.UserID, req.TargetChatID, command); err != nil {
		s.writeWebAppJSON(w, http.StatusForbidden, tgWebAppExecResponse{OK: false, Message: "执行失败：" + err.Error()})
		return
	}
	s.writeWebAppJSON(w, http.StatusOK, tgWebAppExecResponse{OK: true, Message: "执行成功：" + command})
}

func (s *Server) handleTGWebAppLedgerCard(w http.ResponseWriter, r *http.Request) {
	setWebAppNoCache(w)
	if r.Method != http.MethodGet {
		s.writeWebAppLedgerJSON(w, http.StatusMethodNotAllowed, tgWebAppLedgerCardResponse{OK: false, Message: "仅支持 GET"})
		return
	}
	if s == nil || s.app == nil || s.db == nil {
		s.writeWebAppLedgerJSON(w, http.StatusServiceUnavailable, tgWebAppLedgerCardResponse{OK: false, Message: "服务未初始化"})
		return
	}
	uid, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("uid")), 10, 64)
	exp, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("exp")), 10, 64)
	sig := strings.TrimSpace(r.URL.Query().Get("sig"))
	if err := s.app.VerifyWebAppAuth(uid, exp, sig); err != nil {
		s.writeWebAppLedgerJSON(w, http.StatusUnauthorized, tgWebAppLedgerCardResponse{OK: false, Message: "鉴权失败：" + err.Error()})
		return
	}
	chatIDText := strings.TrimSpace(r.URL.Query().Get("chat_id"))
	chatID, err := strconv.ParseInt(chatIDText, 10, 64)
	if err != nil || chatID == 0 {
		s.writeWebAppLedgerJSON(w, http.StatusBadRequest, tgWebAppLedgerCardResponse{OK: false, Message: "chat_id 不能为空"})
		return
	}
	if !s.app.CanAccessWebAppGroup(chatID, uid) {
		s.writeWebAppLedgerJSON(w, http.StatusForbidden, tgWebAppLedgerCardResponse{OK: false, Message: "无权限读取该群账单"})
		return
	}
	group, err := s.db.GetGroup(chatID)
	if err != nil {
		s.writeWebAppLedgerJSON(w, http.StatusNotFound, tgWebAppLedgerCardResponse{OK: false, Message: "群配置不存在"})
		return
	}
	startAt := utils.GetDayStartISO(group.DayCutTime, s.cfg.Timezone)
	rows, err := s.db.ListLedgerSince(chatID, startAt)
	if err != nil {
		s.writeWebAppLedgerJSON(w, http.StatusInternalServerError, tgWebAppLedgerCardResponse{OK: false, Message: "读取账单失败：" + err.Error()})
		return
	}
	card := buildTGWebAppLedgerCardVM(rows, group, s.cfg.Timezone, time.Now())
	s.writeWebAppLedgerJSON(w, http.StatusOK, tgWebAppLedgerCardResponse{
		OK:   true,
		Data: card,
	})
}

func (s *Server) handleTGWebAppLooklook(w http.ResponseWriter, r *http.Request) {
	setWebAppNoCache(w)
	if r.Method != http.MethodGet {
		http.Error(w, "仅支持 GET", http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.app == nil || s.db == nil {
		http.Error(w, "服务未初始化", http.StatusServiceUnavailable)
		return
	}

	uid, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("uid")), 10, 64)
	exp, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("exp")), 10, 64)
	sig := strings.TrimSpace(r.URL.Query().Get("sig"))
	if err := s.app.VerifyWebAppAuth(uid, exp, sig); err != nil {
		http.Error(w, "鉴权失败："+err.Error(), http.StatusUnauthorized)
		return
	}

	requestedModeRaw := r.URL.Query().Get("type")
	mode := "bjr"
	queryMode := normalizeTGWebAppLooklookQueryMode(r.URL.Query().Get("query_mode"))
	if queryMode == "" {
		if strings.TrimSpace(r.URL.Query().Get("bill_date")) != "" {
			queryMode = "day"
		} else {
			queryMode = "range"
		}
	}
	groupText := strings.TrimSpace(r.URL.Query().Get("groupId"))
	if groupText == "" {
		groupText = strings.TrimSpace(r.URL.Query().Get("chat_id"))
	}
	groupID, _ := strconv.ParseInt(groupText, 10, 64)
	if groupID == 0 {
		view := tgWebAppLooklookView{
			Mode:          mode,
			AllowCZR:      false,
			QueryMode:     queryMode,
			GroupID:       0,
			GroupTitle:    "未选择群组",
			BillDateText:  strings.TrimSpace(r.URL.Query().Get("bill_date")),
			BeginTimeText: strings.TrimSpace(r.URL.Query().Get("begintime")),
			EndTimeText:   strings.TrimSpace(r.URL.Query().Get("endtime")),
			BeginTimeISO:  strings.TrimSpace(r.URL.Query().Get("begintime")),
			EndTimeISO:    strings.TrimSpace(r.URL.Query().Get("endtime")),
			DateRangeText: strings.TrimSpace(r.URL.Query().Get("daterange")),
			FirstName:     strings.TrimSpace(r.URL.Query().Get("firstname")),
		}
		exportCSVQuery := buildTGWebAppLooklookExportQuery(r.URL.Query(), "csv")
		exportCSVQuery.Set("type", view.Mode)
		exportCSVURL := "/tg/webapp/looklook.php?" + exportCSVQuery.Encode()
		exportExcelQuery := buildTGWebAppLooklookExportQuery(r.URL.Query(), "excel")
		exportExcelQuery.Set("type", view.Mode)
		exportExcelURL := "/tg/webapp/looklook.php?" + exportExcelQuery.Encode()
		exportPDFQuery := buildTGWebAppLooklookExportQuery(r.URL.Query(), "pdf")
		exportPDFQuery.Set("type", view.Mode)
		exportPDFURL := "/tg/webapp/looklook.php?" + exportPDFQuery.Encode()
		page := renderTGWebAppLooklookHTML(view, uid, exp, sig, exportCSVURL, exportExcelURL, exportPDFURL)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
		return
	}
	canView, allowCZR := s.resolveTGWebAppLooklookAccess(groupID, uid)
	if !canView {
		http.Error(w, "无权限读取该群账单", http.StatusForbidden)
		return
	}
	mode, modeAllowed := resolveTGWebAppLooklookMode(requestedModeRaw, allowCZR)
	if !modeAllowed {
		http.Error(w, "无权限访问该模式", http.StatusForbidden)
		return
	}
	group, err := s.db.GetGroup(groupID)
	if err != nil {
		http.Error(w, "群配置不存在", http.StatusNotFound)
		return
	}

	loc := resolveTGWebAppLocation(s.cfg.Timezone)
	nowAt := time.Now().In(loc)
	beginAt := nowAt.Add(-24 * time.Hour)
	if beginUTC := strings.TrimSpace(utils.GetDayStartISO(group.DayCutTime, s.cfg.Timezone)); beginUTC != "" {
		if parsedBegin, parseErr := time.ParseInLocation("2006-01-02T15:04:05", beginUTC, time.UTC); parseErr == nil {
			beginAt = parsedBegin.In(loc)
		}
	}
	endAt := nowAt
	if rawBegin := strings.TrimSpace(r.URL.Query().Get("begintime")); rawBegin != "" {
		if parsedBegin, parseErr := parseTGWebAppLooklookTime(rawBegin, loc); parseErr == nil {
			beginAt = parsedBegin
		}
	}
	if rawEnd := strings.TrimSpace(r.URL.Query().Get("endtime")); rawEnd != "" {
		if parsedEnd, parseErr := parseTGWebAppLooklookTime(rawEnd, loc); parseErr == nil {
			endAt = parsedEnd
		}
	}
	billDateText := strings.TrimSpace(r.URL.Query().Get("bill_date"))
	parsedBillDate, billDateErr := parseTGWebAppLooklookDate(billDateText, loc)
	switch queryMode {
	case "day":
		if billDateErr != nil {
			parsedBillDate = time.Date(nowAt.Year(), nowAt.Month(), nowAt.Day(), 0, 0, 0, 0, loc)
		}
		beginAt = parsedBillDate
		endAt = parsedBillDate.Add(24*time.Hour - time.Second)
		billDateText = parsedBillDate.Format("2006-01-02")
	default:
		queryMode = "range"
		if billDateErr == nil {
			billDateText = parsedBillDate.Format("2006-01-02")
		} else if billDateText != "" {
			billDateText = ""
		}
	}
	if endAt.Before(beginAt) {
		beginAt, endAt = endAt, beginAt
	}

	startAt := beginAt.UTC().Format("2006-01-02T15:04:05")
	rows, err := s.db.ListLedgerSince(groupID, startAt)
	if err != nil {
		http.Error(w, "读取账单失败："+err.Error(), http.StatusInternalServerError)
		return
	}

	dateRange := strings.TrimSpace(r.URL.Query().Get("daterange"))
	if dateRange == "" {
		dateRange = beginAt.Format("2006-01-02 15:04:05") + " - " + endAt.Format("2006-01-02 15:04:05")
	}
	view := buildTGWebAppLooklookView(
		rows,
		group,
		loc,
		beginAt,
		endAt,
		mode,
		strings.TrimSpace(r.URL.Query().Get("firstname")),
		dateRange,
	)
	view.QueryMode = queryMode
	view.BillDateText = billDateText
	view.AllowCZR = allowCZR

	switch normalizeTGWebAppLooklookExportType(r.URL.Query().Get("export")) {
	case "csv":
		if err := writeTGWebAppLooklookCSV(w, view); err != nil {
			http.Error(w, "导出失败："+err.Error(), http.StatusInternalServerError)
		}
		return
	case "excel":
		if err := writeTGWebAppLooklookExcel(w, view); err != nil {
			http.Error(w, "导出失败："+err.Error(), http.StatusInternalServerError)
		}
		return
	case "pdf":
		if err := writeTGWebAppLooklookPDF(w, view); err != nil {
			http.Error(w, "导出失败："+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	exportCSVQuery := buildTGWebAppLooklookExportQuery(r.URL.Query(), "csv")
	exportCSVQuery.Set("type", view.Mode)
	exportCSVURL := "/tg/webapp/looklook.php?" + exportCSVQuery.Encode()
	exportExcelQuery := buildTGWebAppLooklookExportQuery(r.URL.Query(), "excel")
	exportExcelQuery.Set("type", view.Mode)
	exportExcelURL := "/tg/webapp/looklook.php?" + exportExcelQuery.Encode()
	exportPDFQuery := buildTGWebAppLooklookExportQuery(r.URL.Query(), "pdf")
	exportPDFQuery.Set("type", view.Mode)
	exportPDFURL := "/tg/webapp/looklook.php?" + exportPDFQuery.Encode()
	page := renderTGWebAppLooklookHTML(view, uid, exp, sig, exportCSVURL, exportExcelURL, exportPDFURL)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func normalizeTGWebAppLooklookMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "czr":
		return "czr"
	default:
		return "bjr"
	}
}

func normalizeTGWebAppLooklookQueryMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "day":
		return "day"
	case "range":
		return "range"
	default:
		return ""
	}
}

func resolveTGWebAppLooklookMode(raw string, allowCZR bool) (string, bool) {
	mode := normalizeTGWebAppLooklookMode(raw)
	if mode == "czr" && !allowCZR {
		return mode, false
	}
	return mode, true
}

func (s *Server) resolveTGWebAppLooklookAccess(chatID, userID int64) (bool, bool) {
	if s == nil || s.db == nil || chatID == 0 || userID == 0 {
		return false, false
	}
	allowCZR := s.db.IsPrivileged(chatID, userID)
	if allowCZR {
		return true, true
	}
	if s.app != nil && s.app.IsGroupMember(chatID, userID) {
		return true, false
	}
	return false, false
}

func buildTGWebAppLooklookExportQuery(query url.Values, exportType string) url.Values {
	out := make(url.Values, len(query))
	for key, values := range query {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	normalized := normalizeTGWebAppLooklookExportType(exportType)
	if normalized == "" {
		normalized = "csv"
	}
	out.Set("export", normalized)
	return out
}

func normalizeTGWebAppLooklookExportType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "csv":
		return "csv"
	case "excel", "xlsx", "xls":
		return "excel"
	case "pdf":
		return "pdf"
	default:
		return ""
	}
}

func parseTGWebAppLooklookTime(raw string, loc *time.Location) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, trimmed, loc); err == nil {
			return parsed.In(loc), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time: %s", raw)
}

func parseTGWebAppLooklookDate(raw string, loc *time.Location) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	parsed, err := time.ParseInLocation("2006-01-02", trimmed, loc)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func parseTGWebAppLedgerOccurredAt(raw string, loc *time.Location) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	layoutsWithUTC := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layoutsWithUTC {
		if t, err := time.ParseInLocation(layout, trimmed, time.UTC); err == nil {
			return t.In(loc), true
		}
	}
	if len(trimmed) >= 19 {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", trimmed[:19], time.UTC); err == nil {
			return t.In(loc), true
		}
	}
	return time.Time{}, false
}

func buildTGWebAppLooklookView(
	rows []db.LedgerEntry,
	group db.GroupConfig,
	loc *time.Location,
	beginAt, endAt time.Time,
	mode, firstName, dateRange string,
) tgWebAppLooklookView {
	view := tgWebAppLooklookView{
		Mode:          mode,
		GroupID:       group.ChatID,
		GroupTitle:    strings.TrimSpace(group.Title),
		BillDateText:  beginAt.Format("2006-01-02"),
		BeginTimeText: beginAt.Format("2006-01-02 15:04:05"),
		EndTimeText:   endAt.Format("2006-01-02 15:04:05"),
		BeginTimeISO:  beginAt.Format("2006-01-02T15:04:05"),
		EndTimeISO:    endAt.Format("2006-01-02T15:04:05"),
		DateRangeText: strings.TrimSpace(dateRange),
		FirstName:     strings.TrimSpace(firstName),
	}
	if view.GroupTitle == "" {
		view.GroupTitle = strconv.FormatInt(group.ChatID, 10)
	}
	if view.DateRangeText == "" {
		view.DateRangeText = view.BeginTimeText + " - " + view.EndTimeText
	}

	fallbackRate := normalizeTGWebAppRate(group.CustomRate)
	nameFilter := strings.ToLower(strings.TrimSpace(firstName))

	markerMap := map[string]tgWebAppLooklookPartyStat{}
	operatorMap := map[string]tgWebAppLooklookPartyStat{}
	dailyMap := map[string]tgWebAppLooklookDailyStat{}
	rateMap := map[string]int{}
	userMap := map[string]tgWebAppLooklookUserStat{}

	for _, row := range rows {
		occurredAt, ok := parseTGWebAppLedgerOccurredAt(row.CreatedAt, loc)
		if !ok {
			continue
		}
		if occurredAt.Before(beginAt) || occurredAt.After(endAt) {
			continue
		}
		markerName := strings.TrimSpace(row.Nickname)
		if markerName == "" {
			markerName = strings.TrimSpace(row.Username)
		}
		if markerName == "" {
			markerName = "未知"
		}
		operatorName := strings.TrimSpace(row.Username)
		if operatorName == "" {
			operatorName = strings.TrimSpace(row.Nickname)
		}
		if operatorName == "" {
			operatorName = "未知"
		}
		markerUsername := normalizeTGWebAppUsername(row.Username)
		operatorUsername := normalizeTGWebAppUsername(row.Username)
		markerUserID := row.UserID
		operatorUserID := row.UserID
		remarks := normalizeTGWebAppLedgerRemark(row.Note)
		isReversal := isTGWebAppReversalNote(row.Note)
		if nameFilter != "" {
			if !strings.Contains(strings.ToLower(markerName), nameFilter) && !strings.Contains(strings.ToLower(operatorName), nameFilter) {
				continue
			}
		}

		entryType := strings.ToLower(strings.TrimSpace(row.EntryType))
		if entryType != "deposit" && entryType != "payout" {
			continue
		}
		amount := normalizeTGWebAppLedgerAmount(row)
		rate := normalizeTGWebAppRateWithFallback(row.Rate, fallbackRate)
		convertedU := 0.0
		feeFactor := 1.0
		currency := strings.ToUpper(strings.TrimSpace(row.Currency))
		if currency == "CNY" {
			currency = ""
		}
		if entryType == "deposit" {
			convertedU, feeFactor = computeTGWebAppDepositConvertedAndFactor(row, amount, rate, group.FeeRate)
			view.TotalDepositAmount += amount
			view.TotalDepositU += convertedU
		} else {
			convertedU = convertTGWebAppAmountToU(amount, row.Currency, rate)
			view.TotalPayoutAmount += amount
			view.TotalPayoutU += convertedU
		}
		view.Rows = append(view.Rows, tgWebAppLooklookRow{
			OccurredAt:       occurredAt,
			TimeText:         occurredAt.Format("2006-01-02 15:04:05"),
			EntryType:        entryType,
			IsReversal:       isReversal,
			Amount:           amount,
			Currency:         currency,
			FeeFactor:        feeFactor,
			Rate:             rate,
			ConvertedU:       convertedU,
			MarkerName:       markerName,
			MarkerUserID:     markerUserID,
			MarkerUsername:   markerUsername,
			OperatorName:     operatorName,
			OperatorUserID:   operatorUserID,
			OperatorUsername: operatorUsername,
			Remarks:          remarks,
			Track:            strings.TrimSpace(row.TrackLink),
		})

		marker := markerMap[markerName]
		marker.Name = markerName
		if marker.UserID == 0 && markerUserID > 0 {
			marker.UserID = markerUserID
		}
		if marker.Username == "" && markerUsername != "" {
			marker.Username = markerUsername
		}
		marker.Count++
		operator := operatorMap[operatorName]
		operator.Name = operatorName
		if operator.UserID == 0 && operatorUserID > 0 {
			operator.UserID = operatorUserID
		}
		if operator.Username == "" && operatorUsername != "" {
			operator.Username = operatorUsername
		}
		operator.Count++
		user := userMap[operatorName]
		user.Name = operatorName
		user.Count++
		if entryType == "deposit" {
			marker.DepositAmount += amount
			marker.DepositU += convertedU
			operator.DepositAmount += amount
			operator.DepositU += convertedU
			user.DepositU += convertedU
		} else {
			marker.PayoutAmount += amount
			marker.PayoutU += convertedU
			operator.PayoutAmount += amount
			operator.PayoutU += convertedU
			user.PayoutU += convertedU
		}
		markerMap[markerName] = marker
		operatorMap[operatorName] = operator
		user.NetU = user.DepositU - user.PayoutU
		userMap[operatorName] = user

		dayKey := occurredAt.Format("2006-01-02")
		daily := dailyMap[dayKey]
		daily.Day = dayKey
		if entryType == "deposit" {
			daily.DepositU += convertedU
		} else {
			daily.PayoutU += convertedU
		}
		dailyMap[dayKey] = daily

		rateKey := utils.FormatAmount(rate)
		rateMap[rateKey]++
	}

	sort.Slice(view.Rows, func(i, j int) bool {
		return view.Rows[i].OccurredAt.Before(view.Rows[j].OccurredAt)
	})

	view.MarkerStats = flattenTGWebAppLooklookPartyStats(markerMap)
	view.OperatorStats = flattenTGWebAppLooklookPartyStats(operatorMap)
	view.DailyData = flattenTGWebAppLooklookDailyStats(dailyMap)
	view.RateStats = flattenTGWebAppLooklookRateStats(rateMap)
	view.UserStats = flattenTGWebAppLooklookUserStats(userMap)
	return view
}

func flattenTGWebAppLooklookPartyStats(stats map[string]tgWebAppLooklookPartyStat) []tgWebAppLooklookPartyStat {
	out := make([]tgWebAppLooklookPartyStat, 0, len(stats))
	for _, item := range stats {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i].DepositU + out[i].PayoutU
		right := out[j].DepositU + out[j].PayoutU
		if left == right {
			return out[i].Name < out[j].Name
		}
		return left > right
	})
	return out
}

func flattenTGWebAppLooklookDailyStats(stats map[string]tgWebAppLooklookDailyStat) []tgWebAppLooklookDailyStat {
	out := make([]tgWebAppLooklookDailyStat, 0, len(stats))
	for _, item := range stats {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Day < out[j].Day
	})
	return out
}

func flattenTGWebAppLooklookRateStats(stats map[string]int) []tgWebAppLooklookRateStat {
	out := make([]tgWebAppLooklookRateStat, 0, len(stats))
	for rate, count := range stats {
		out = append(out, tgWebAppLooklookRateStat{
			Rate:  rate,
			Count: count,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		left, leftErr := strconv.ParseFloat(out[i].Rate, 64)
		right, rightErr := strconv.ParseFloat(out[j].Rate, 64)
		if leftErr == nil && rightErr == nil {
			return left < right
		}
		return out[i].Rate < out[j].Rate
	})
	return out
}

func flattenTGWebAppLooklookUserStats(stats map[string]tgWebAppLooklookUserStat) []tgWebAppLooklookUserStat {
	out := make([]tgWebAppLooklookUserStat, 0, len(stats))
	for _, item := range stats {
		item.NetU = item.DepositU - item.PayoutU
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func tgWebAppLooklookExportFilename(view tgWebAppLooklookView, ext string) string {
	return fmt.Sprintf(
		"looklook_%s_%d_%s.%s",
		strings.TrimSpace(view.Mode),
		view.GroupID,
		time.Now().Format("20060102_150405"),
		strings.TrimSpace(ext),
	)
}

func tgWebAppLooklookExportHeader() []string {
	return []string{"时间", "类型", "原始金额", "币种", "费率因子", "汇率", "折算U", "标记人", "操作人", "备注", "跟踪链接"}
}

func tgWebAppLooklookExportRecords(view tgWebAppLooklookView) [][]string {
	records := make([][]string, 0, len(view.Rows))
	for _, row := range view.Rows {
		entryType := tgWebAppLooklookEntryTypeLabel(row)
		records = append(records, []string{
			row.TimeText,
			entryType,
			utils.FormatAmount(row.Amount),
			row.Currency,
			utils.FormatAmount(row.FeeFactor),
			utils.FormatAmount(row.Rate),
			utils.FormatAmount(row.ConvertedU),
			row.MarkerName,
			row.OperatorName,
			row.Remarks,
			row.Track,
		})
	}
	return records
}

func writeTGWebAppLooklookCSV(w http.ResponseWriter, view tgWebAppLooklookView) error {
	filename := tgWebAppLooklookExportFilename(view, "csv")
	w.Header().Set("Content-Type", "application/vnd.ms-excel; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(w)
	header := tgWebAppLooklookExportHeader()
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, record := range tgWebAppLooklookExportRecords(view) {
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func writeTGWebAppLooklookExcel(w http.ResponseWriter, view tgWebAppLooklookView) error {
	rows := [][]string{
		{"模式", strings.TrimSpace(view.Mode)},
		{"群组ID", strconv.FormatInt(view.GroupID, 10)},
		{"群组名称", strings.TrimSpace(view.GroupTitle)},
		{"时间范围", strings.TrimSpace(view.DateRangeText)},
		{"总入账金额", utils.FormatAmount(view.TotalDepositAmount)},
		{"总下发金额", utils.FormatAmount(view.TotalPayoutAmount)},
		{"总入账U", utils.FormatAmount(view.TotalDepositU)},
		{"总下发U", utils.FormatAmount(view.TotalPayoutU)},
		{},
		tgWebAppLooklookExportHeader(),
	}
	rows = append(rows, tgWebAppLooklookExportRecords(view)...)
	payload, err := services.BuildTabularXLSX(rows)
	if err != nil {
		return err
	}
	filename := tgWebAppLooklookExportFilename(view, "xlsx")
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	_, err = w.Write(payload)
	return err
}

func writeTGWebAppLooklookPDF(w http.ResponseWriter, view tgWebAppLooklookView) error {
	lines := []string{
		"Looklook Export",
		fmt.Sprintf("Mode: %s", strings.TrimSpace(view.Mode)),
		fmt.Sprintf("Group ID: %d", view.GroupID),
		fmt.Sprintf("Group Title: %s", strings.TrimSpace(view.GroupTitle)),
		fmt.Sprintf("Range: %s", strings.TrimSpace(view.DateRangeText)),
		fmt.Sprintf("Total Deposit Amount: %s", utils.FormatAmount(view.TotalDepositAmount)),
		fmt.Sprintf("Total Payout Amount: %s", utils.FormatAmount(view.TotalPayoutAmount)),
		fmt.Sprintf("Total Deposit U: %s", utils.FormatAmount(view.TotalDepositU)),
		fmt.Sprintf("Total Payout U: %s", utils.FormatAmount(view.TotalPayoutU)),
		"",
		"Details:",
	}
	for idx, row := range view.Rows {
		entryType := "DEPOSIT"
		if row.EntryType == "payout" {
			entryType = "PAYOUT"
		}
		if row.IsReversal {
			entryType = "REVERSAL"
		}
		lines = append(lines, fmt.Sprintf(
			"#%d %s %s amount=%s currency=%s fee_factor=%s rate=%s converted_u=%s marker=%s operator=%s note=%s track=%s",
			idx+1,
			strings.TrimSpace(row.TimeText),
			entryType,
			utils.FormatAmount(row.Amount),
			strings.TrimSpace(row.Currency),
			utils.FormatAmount(row.FeeFactor),
			utils.FormatAmount(row.Rate),
			utils.FormatAmount(row.ConvertedU),
			strings.TrimSpace(row.MarkerName),
			strings.TrimSpace(row.OperatorName),
			strings.TrimSpace(row.Remarks),
			strings.TrimSpace(row.Track),
		))
	}
	payload, err := services.BuildPlainTextPDF(lines)
	if err != nil {
		return err
	}
	filename := tgWebAppLooklookExportFilename(view, "pdf")
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	_, err = w.Write(payload)
	return err
}

func tgWebAppLooklookEntryTypeLabel(row tgWebAppLooklookRow) string {
	if row.IsReversal {
		return "撤回"
	}
	if row.EntryType == "payout" {
		return "下发"
	}
	return "入款"
}

func renderTGWebAppLooklookPerson(name, username string, userID int64) string {
	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName = "未知"
	}
	normalizedUsername := normalizeTGWebAppUsername(username)
	if normalizedUsername != "" {
		label := "@" + normalizedUsername
		href := "https://t.me/" + normalizedUsername
		return `<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">` + html.EscapeString(label) + `</a>`
	}
	if userID > 0 {
		href := fmt.Sprintf("tg://user?id=%d", userID)
		return `<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">` + html.EscapeString(displayName) + `</a>`
	}
	return html.EscapeString(displayName)
}

func normalizeTGWebAppUsername(raw string) string {
	username := strings.TrimSpace(raw)
	username = strings.TrimPrefix(username, "@")
	if username == "" {
		return ""
	}
	for _, ch := range username {
		isDigit := ch >= '0' && ch <= '9'
		isLower := ch >= 'a' && ch <= 'z'
		isUpper := ch >= 'A' && ch <= 'Z'
		if ch != '_' && !isDigit && !isLower && !isUpper {
			return ""
		}
	}
	return username
}

func renderTGWebAppLooklookModeField(view tgWebAppLooklookView) string {
	mode := normalizeTGWebAppLooklookMode(view.Mode)
	if !view.AllowCZR {
		return `<div><label>type</label><input value="bjr" disabled><input type="hidden" name="type" value="bjr"></div>`
	}
	return fmt.Sprintf(
		`<div><label>type</label><select name="type"><option value="bjr"%s>bjr</option><option value="czr"%s>czr</option></select></div>`,
		map[bool]string{true: " selected", false: ""}[mode == "bjr"],
		map[bool]string{true: " selected", false: ""}[mode == "czr"],
	)
}

func renderTGWebAppLooklookHTML(view tgWebAppLooklookView, uid, exp int64, sig, exportCSVURL, exportExcelURL, exportPDFURL string) string {
	var detailRows strings.Builder
	if len(view.Rows) == 0 {
		detailRows.WriteString(`<tr><td colspan="11" class="muted">暂无数据</td></tr>`)
	} else {
		for _, row := range view.Rows {
			entryType := tgWebAppLooklookEntryTypeLabel(row)
			formula := fmt.Sprintf("%s * %s / %s = %s U", utils.FormatAmount(row.Amount), utils.FormatAmount(row.FeeFactor), utils.FormatAmount(row.Rate), utils.FormatAmount(row.ConvertedU))
			if row.EntryType == "payout" {
				formula = "≈" + utils.FormatAmount(row.ConvertedU) + " U"
			}
			track := "-"
			if strings.TrimSpace(row.Track) != "" {
				track = `<a href="` + html.EscapeString(strings.TrimSpace(row.Track)) + `" target="_blank" rel="noopener noreferrer">跳转</a>`
			}
			detailRows.WriteString("<tr>")
			detailRows.WriteString("<td>" + html.EscapeString(row.TimeText) + "</td>")
			detailRows.WriteString("<td>" + html.EscapeString(entryType) + "</td>")
			detailRows.WriteString("<td>" + html.EscapeString(utils.FormatAmount(row.Amount)) + "</td>")
			detailRows.WriteString("<td>" + html.EscapeString(row.Currency) + "</td>")
			detailRows.WriteString("<td>" + html.EscapeString(utils.FormatAmount(row.FeeFactor)) + "</td>")
			detailRows.WriteString("<td>" + html.EscapeString(utils.FormatAmount(row.Rate)) + "</td>")
			detailRows.WriteString("<td>" + html.EscapeString(formula) + "</td>")
			detailRows.WriteString("<td>" + renderTGWebAppLooklookPerson(row.MarkerName, row.MarkerUsername, row.MarkerUserID) + "</td>")
			detailRows.WriteString("<td>" + renderTGWebAppLooklookPerson(row.OperatorName, row.OperatorUsername, row.OperatorUserID) + "</td>")
			detailRows.WriteString("<td>" + html.EscapeString(row.Remarks) + "</td>")
			detailRows.WriteString("<td>" + track + "</td>")
			detailRows.WriteString("</tr>")
		}
	}

	renderPartyRows := func(items []tgWebAppLooklookPartyStat) string {
		if len(items) == 0 {
			return `<tr><td colspan="6" class="muted">暂无数据</td></tr>`
		}
		var b strings.Builder
		for _, item := range items {
			b.WriteString("<tr>")
			b.WriteString("<td>" + renderTGWebAppLooklookPerson(item.Name, item.Username, item.UserID) + "</td>")
			b.WriteString("<td>" + strconv.Itoa(item.Count) + "</td>")
			b.WriteString("<td>" + html.EscapeString(utils.FormatAmount(item.DepositAmount)) + "</td>")
			b.WriteString("<td>" + html.EscapeString(utils.FormatAmount(item.DepositU)) + "</td>")
			b.WriteString("<td>" + html.EscapeString(utils.FormatAmount(item.PayoutAmount)) + "</td>")
			b.WriteString("<td>" + html.EscapeString(utils.FormatAmount(item.PayoutU)) + "</td>")
			b.WriteString("</tr>")
		}
		return b.String()
	}

	dailyDataJSON, _ := json.Marshal(view.DailyData)
	exchangeRateJSON, _ := json.Marshal(view.RateStats)
	userStatsJSON, _ := json.Marshal(view.UserStats)
	modeField := renderTGWebAppLooklookModeField(view)

	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>账单详情</title>
  <style>
    body{font-family:"PingFang SC","Microsoft YaHei",sans-serif;margin:16px;color:#111827;background:#f8fafc}
    .wrap{max-width:1180px;margin:0 auto}
    .panel{background:#fff;border:1px solid #e5e7eb;border-radius:12px;padding:12px;margin-bottom:12px}
    .title{font-size:20px;font-weight:700;margin:0 0 6px}
    .muted{color:#6b7280}
    .grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}
    label{font-size:12px;color:#6b7280;display:block;margin-bottom:4px}
    input,select{width:100%%;box-sizing:border-box;padding:8px;border:1px solid #d1d5db;border-radius:8px}
    button,a.btn{display:inline-block;padding:8px 12px;border-radius:8px;border:1px solid #16a34a;background:#16a34a;color:#fff;text-decoration:none;cursor:pointer}
    a.btn.alt{background:#fff;color:#16a34a}
    table{width:100%%;border-collapse:collapse}
    th,td{border:1px solid #e5e7eb;padding:8px;font-size:12px;text-align:left;vertical-align:top}
    th{background:#f3f4f6}
    .summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:8px}
    .summary .item{background:#f9fafb;border:1px solid #e5e7eb;border-radius:10px;padding:10px}
    .summary .key{font-size:12px;color:#6b7280}
    .summary .val{font-size:16px;font-weight:700;color:#2563eb}
    @media (max-width:860px){.grid,.summary{grid-template-columns:repeat(2,minmax(0,1fr));}}
  </style>
</head>
<body>
<div class="wrap">
  <div class="panel">
    <h1 class="title">账单详情</h1>
    <div class="muted">群组：%s（%d） | 模式：%s</div>
    <form method="get" action="/tg/webapp/looklook.php" style="margin-top:10px">
      <input type="hidden" name="uid" value="%d">
      <input type="hidden" name="exp" value="%d">
      <input type="hidden" name="sig" value="%s">
      <div class="grid">
	        %s
	        <div><label>groupId</label><input name="groupId" value="%d"></div>
	        <div><label>查询模式</label>
	          <select name="query_mode">
	            <option value="day"%s>单日查询</option>
	            <option value="range"%s>时间区间查询</option>
	          </select>
	        </div>
        <div><label>账单日期（年月日）</label><input type="date" name="bill_date" value="%s"></div>
        <div><label>开始时间</label><input type="datetime-local" step="1" name="begintime" value="%s"></div>
        <div><label>结束时间</label><input type="datetime-local" step="1" name="endtime" value="%s"></div>
        <div><label>firstname</label><input name="firstname" value="%s"></div>
      </div>
      <div class="muted" style="margin-top:8px">当前时间范围：%s - %s</div>
      <div style="margin-top:10px;display:flex;gap:8px;align-items:center;">
        <button type="submit">查询</button>
        <a class="btn alt" href="%s">导出 CSV</a>
        <a class="btn alt" href="%s">导出 Excel</a>
        <a class="btn alt" href="%s">导出 PDF</a>
      </div>
    </form>
  </div>

  <div class="panel summary">
    <div class="item"><div class="key">总入账金额</div><div class="val">%s</div></div>
    <div class="item"><div class="key">总下发金额</div><div class="val">%s</div></div>
    <div class="item"><div class="key">总入账U</div><div class="val">%s</div></div>
    <div class="item"><div class="key">总下发U</div><div class="val">%s</div></div>
  </div>

  <div class="panel">
    <h2 class="title" style="font-size:18px">账单明细</h2>
    <table>
      <thead>
        <tr>
          <th>时间</th><th>类型</th><th>金额</th><th>币种</th><th>费率因子</th><th>汇率</th><th>计算</th><th>标记人</th><th>操作人</th><th>备注</th><th>跟踪</th>
        </tr>
      </thead>
      <tbody>%s</tbody>
    </table>
  </div>

  <div class="panel">
    <h2 class="title" style="font-size:18px">统计（按标记人）</h2>
    <table>
      <thead><tr><th>标记人</th><th>笔数</th><th>入款金额</th><th>入款U</th><th>下发金额</th><th>下发U</th></tr></thead>
      <tbody>%s</tbody>
    </table>
  </div>

  <div class="panel">
    <h2 class="title" style="font-size:18px">统计（按操作人）</h2>
    <table>
      <thead><tr><th>操作人</th><th>笔数</th><th>入款金额</th><th>入款U</th><th>下发金额</th><th>下发U</th></tr></thead>
      <tbody>%s</tbody>
    </table>
  </div>
</div>

<script>
const dailyData = %s;
const exchangeRateStats = %s;
const userStats = %s;
window.__LOOKLOOK_BOOTSTRAP__ = { dailyData, exchangeRateStats, userStats };
</script>
</body>
</html>`,
		html.EscapeString(view.GroupTitle),
		view.GroupID,
		html.EscapeString(view.Mode),
		uid,
		exp,
		html.EscapeString(sig),
		modeField,
		view.GroupID,
		map[bool]string{true: " selected", false: ""}[view.QueryMode == "day"],
		map[bool]string{true: " selected", false: ""}[view.QueryMode == "range"],
		html.EscapeString(view.BillDateText),
		html.EscapeString(view.BeginTimeISO),
		html.EscapeString(view.EndTimeISO),
		html.EscapeString(view.FirstName),
		html.EscapeString(view.BeginTimeText),
		html.EscapeString(view.EndTimeText),
		html.EscapeString(exportCSVURL),
		html.EscapeString(exportExcelURL),
		html.EscapeString(exportPDFURL),
		html.EscapeString(utils.FormatAmount(view.TotalDepositAmount)),
		html.EscapeString(utils.FormatAmount(view.TotalPayoutAmount)),
		html.EscapeString(utils.FormatAmount(view.TotalDepositU)),
		html.EscapeString(utils.FormatAmount(view.TotalPayoutU)),
		detailRows.String(),
		renderPartyRows(view.MarkerStats),
		renderPartyRows(view.OperatorStats),
		string(dailyDataJSON),
		string(exchangeRateJSON),
		string(userStatsJSON),
	)
}

func buildTGWebAppLedgerCardVM(rows []db.LedgerEntry, group db.GroupConfig, timezone string, now time.Time) *tgWebAppLedgerCardVM {
	loc := resolveTGWebAppLocation(timezone)
	fallbackRate := normalizeTGWebAppRate(group.CustomRate)
	deposits := make([]tgWebAppLedgerDeposit, 0, len(rows))
	payouts := make([]tgWebAppLedgerPayout, 0, len(rows))
	for _, row := range rows {
		entryType := strings.ToLower(strings.TrimSpace(row.EntryType))
		switch entryType {
		case "deposit":
			rate := normalizeTGWebAppRateWithFallback(row.Rate, fallbackRate)
			amount := normalizeTGWebAppLedgerAmount(row)
			converted, feeFactor := computeTGWebAppDepositConvertedAndFactor(row, amount, rate, group.FeeRate)
			sourceCurrency := strings.ToUpper(strings.TrimSpace(row.Currency))
			if sourceCurrency == "" {
				sourceCurrency = "CNY"
			}
			deposits = append(deposits, tgWebAppLedgerDeposit{
				Time:           formatTGWebAppLedgerClock(row.CreatedAt, loc),
				Amount:         amount,
				Rate:           rate,
				FeeFactor:      feeFactor,
				Converted:      converted,
				Currency:       "U",
				SourceCurrency: sourceCurrency,
				Track:          strings.TrimSpace(row.TrackLink),
				Remarks:        normalizeTGWebAppLedgerRemark(row.Note),
			})
		case "payout":
			rate := normalizeTGWebAppRateWithFallback(row.Rate, fallbackRate)
			amount := normalizeTGWebAppLedgerAmount(row)
			payoutU := convertTGWebAppAmountToU(amount, row.Currency, rate)
			currency := strings.ToUpper(strings.TrimSpace(row.Currency))
			if currency == "CNY" {
				currency = ""
			}
			converted := "≈" + utils.FormatAmount(payoutU) + " U"
			if isTGWebAppUSDT(row.Currency) {
				converted = "≈" + utils.FormatAmount(amount*normalizeTGWebAppRate(rate))
			}
			payouts = append(payouts, tgWebAppLedgerPayout{
				Time:      formatTGWebAppLedgerClock(row.CreatedAt, loc),
				Amount:    amount,
				Currency:  currency,
				Converted: converted,
				PayoutU:   payoutU,
				Remarks:   normalizeTGWebAppLedgerRemark(row.Note),
			})
		}
	}
	return &tgWebAppLedgerCardVM{
		Deposits:           deposits,
		Payouts:            payouts,
		FeeRate:            group.FeeRate,
		GlobalExchangeRate: fallbackRate,
		MessageTime:        now.In(loc).Format("15:04"),
	}
}

func normalizeTGWebAppRate(rate float64) float64 {
	if isTGWebAppNearZero(rate) {
		return 1
	}
	return rate
}

func normalizeTGWebAppRateWithFallback(rate, fallback float64) float64 {
	if !isTGWebAppNearZero(rate) {
		return rate
	}
	return normalizeTGWebAppRate(fallback)
}

func normalizeTGWebAppLedgerAmount(row db.LedgerEntry) float64 {
	if !isTGWebAppNearZero(row.Amount) {
		return row.Amount
	}
	if !isTGWebAppNearZero(row.FinalAmount) {
		return row.FinalAmount
	}
	return row.BaseAmount
}

func convertTGWebAppAmountToU(amount float64, currency string, rate float64) float64 {
	if isTGWebAppUSDT(currency) {
		return amount
	}
	return amount / normalizeTGWebAppRate(rate)
}

func computeTGWebAppDepositConvertedAndFactor(row db.LedgerEntry, amount, rate, fallbackFeeRate float64) (float64, float64) {
	baseConverted := convertTGWebAppAmountToU(amount, row.Currency, rate)
	if !isTGWebAppNearZero(row.FinalAmount) {
		finalConverted := convertTGWebAppAmountToU(row.FinalAmount, row.Currency, rate)
		if isTGWebAppNearZero(baseConverted) {
			return finalConverted, 1
		}
		return finalConverted, finalConverted / baseConverted
	}
	effectiveFeeRate := row.FeeRate
	if isTGWebAppNearZero(effectiveFeeRate) {
		effectiveFeeRate = fallbackFeeRate
	}
	feeFactor := 1 - effectiveFeeRate/100.0
	return baseConverted * feeFactor, feeFactor
}

func isTGWebAppUSDT(currency string) bool {
	return strings.EqualFold(strings.TrimSpace(currency), "USDT")
}

func normalizeTGWebAppLedgerRemark(note string) string {
	trimmed := strings.TrimSpace(note)
	tail, isReversal := splitTGWebAppReversalRemark(trimmed)
	if !isReversal {
		return trimmed
	}
	if tail == "" {
		return "撤回"
	}
	return "撤回 " + tail
}

func isTGWebAppReversalNote(note string) bool {
	_, isReversal := splitTGWebAppReversalRemark(note)
	return isReversal
}

func splitTGWebAppReversalRemark(note string) (string, bool) {
	trimmed := strings.TrimSpace(note)
	if len(trimmed) < len("REV:1") {
		return "", false
	}
	if !strings.HasPrefix(strings.ToUpper(trimmed), "REV:") {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[len("REV:"):])
	digitEnd := 0
	for digitEnd < len(rest) {
		ch := rest[digitEnd]
		if ch < '0' || ch > '9' {
			break
		}
		digitEnd++
	}
	if digitEnd == 0 {
		return "", false
	}
	return strings.TrimSpace(rest[digitEnd:]), true
}

func formatTGWebAppLedgerClock(raw string, loc *time.Location) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "--:--:--"
	}
	layoutsWithUTC := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layoutsWithUTC {
		if t, err := time.ParseInLocation(layout, trimmed, time.UTC); err == nil {
			return t.In(loc).Format("15:04:05")
		}
	}
	if len(trimmed) >= 19 {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", trimmed[:19], time.UTC); err == nil {
			return t.In(loc).Format("15:04:05")
		}
	}
	return "--:--:--"
}

func resolveTGWebAppLocation(timezone string) *time.Location {
	loc, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil || loc == nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

func isTGWebAppNearZero(value float64) bool {
	if value < 0 {
		value = -value
	}
	return value < 1e-9
}

func (s *Server) handleTGWebAppStateWS(w http.ResponseWriter, r *http.Request) {
	setWebAppNoCache(w)
	if r.Method != http.MethodGet {
		http.Error(w, "仅支持 GET", http.StatusMethodNotAllowed)
		return
	}
	uid, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("uid")), 10, 64)
	exp, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("exp")), 10, 64)
	sig := strings.TrimSpace(r.URL.Query().Get("sig"))
	if err := s.app.VerifyWebAppAuth(uid, exp, sig); err != nil {
		http.Error(w, "鉴权失败", http.StatusUnauthorized)
		return
	}
	chatID := int64(0)
	chatText := strings.TrimSpace(r.URL.Query().Get("chat_id"))
	if chatText != "" {
		parsed, err := strconv.ParseInt(chatText, 10, 64)
		if err != nil {
			http.Error(w, "chat_id 格式错误", http.StatusBadRequest)
			return
		}
		chatID = parsed
	}
	if chatID == 0 {
		http.Error(w, "chat_id 不能为空", http.StatusBadRequest)
		return
	}
	if !s.db.IsPrivileged(chatID, uid) {
		http.Error(w, "无权限订阅该群状态", http.StatusForbidden)
		return
	}
	hub := s.app.StateWebSocketHub()
	if hub == nil {
		http.Error(w, "状态广播未启用", http.StatusServiceUnavailable)
		return
	}
	if err := hub.ServeWS(w, r, chatID); err != nil && !isNormalWSClose(err) {
		log.Printf("WebApp 状态通道关闭: %v", err)
	}
}

func isNormalWSClose(err error) bool {
	if err == nil {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "close 1000") ||
		strings.Contains(lower, "going away") ||
		strings.Contains(lower, "closed network connection")
}

func (s *Server) writeWebAppJSON(w http.ResponseWriter, status int, payload tgWebAppExecResponse) {
	setWebAppNoCache(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeWebAppLedgerJSON(w http.ResponseWriter, status int, payload tgWebAppLedgerCardResponse) {
	setWebAppNoCache(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func setWebAppNoCache(w http.ResponseWriter) {
	if w == nil {
		return
	}
	header := w.Header()
	header.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.handleDashboard(w, r)
}

func (s *Server) handleKillSwitch(w http.ResponseWriter, r *http.Request) {
	resultMsg := ""
	resultErr := ""
	reasonInput := "检测到严重 Bug 或攻击风险，立即停机排查。"

	if r.Method == http.MethodPost {
		if err := parseLimitedAdminForm(w, r); err != nil {
			resultErr = "表单解析失败：" + err.Error()
		} else {
			reasonInput = strings.TrimSpace(r.FormValue("reason"))
			if reasonInput == "" {
				reasonInput = "管理员手动触发紧急停机"
			}
			if s.killSwitch == nil {
				resultErr = "紧急停机未启用：服务未注入 Kill Switch"
			} else if s.killSwitch.TriggerManual("admin_panel", reasonInput) {
				resultMsg = "紧急停机已触发，所有服务正在停止。"
			} else {
				resultMsg = "紧急停机已在此前触发，忽略重复请求。"
			}
		}
	}

	statusHTML := `<div class="muted">当前状态：未触发</div>`
	if s.killSwitch == nil {
		statusHTML = `<div class="err">当前状态：未启用（未注入 Kill Switch）</div>`
	} else if event, ok := s.killSwitch.Snapshot(); ok {
		statusHTML = `<div class="err">当前状态：已触发</div><div class="muted">` +
			`触发时间：` + html.EscapeString(event.TriggeredAt.Format(time.RFC3339)) +
			`<br>触发类别：` + html.EscapeString(event.Category) +
			`<br>触发来源：` + html.EscapeString(event.Source) +
			`<br>触发原因：` + html.EscapeString(event.Detail) +
			`</div>`
	}

	var b strings.Builder
	if resultMsg != "" {
		b.WriteString(`<div class="msg">` + html.EscapeString(resultMsg) + `</div>`)
	}
	if resultErr != "" {
		b.WriteString(`<div class="err">` + html.EscapeString(resultErr) + `</div>`)
	}
	b.WriteString(`<h2>一键紧急停机</h2>`)
	b.WriteString(`<div class="err">仅在确认系统出现严重 Bug 或攻击时使用。触发后将取消主上下文并停止全部服务。</div>`)
	b.WriteString(statusHTML)
	b.WriteString(`<form method="post" action="` + s.withToken("/admin/kill-switch") + `">`)
	b.WriteString(`<p>停机原因：</p>`)
	b.WriteString(`<p><textarea name="reason" required placeholder="例如：检测到异常流量攻击，立即停机保护账本。">` + html.EscapeString(reasonInput) + `</textarea></p>`)
	b.WriteString(`<p><button type="submit" style="background:#b91c1c;color:#fff;border:1px solid #991b1b;">立即停机</button></p>`)
	b.WriteString(`</form>`)
	s.writePage(w, "紧急停机", b.String())
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.ListAllGroups()
	if err != nil {
		s.writePage(w, "群组管理", `<div class="err">读取群组失败：`+html.EscapeString(err.Error())+`</div>`)
		return
	}
	keyword := strings.TrimSpace(r.URL.Query().Get("q"))
	filtered := make([]db.GroupConfig, 0, len(rows))
	for _, row := range rows {
		if keyword == "" {
			filtered = append(filtered, row)
			continue
		}
		raw := strings.Join([]string{
			row.Title,
			strconv.FormatInt(row.ChatID, 10),
			row.GroupType,
			row.GroupTags,
			row.RateSource,
		}, " ")
		if containsFold(raw, keyword) {
			filtered = append(filtered, row)
		}
	}

	var b strings.Builder
	b.WriteString(`<form method="get" action="/admin/groups">`)
	if token := strings.TrimSpace(s.cfg.AdminToken); token != "" {
		b.WriteString(`<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`)
	}
	b.WriteString(`搜索：<input name="q" value="` + html.EscapeString(keyword) + `" placeholder="群名/群ID/标签/类型">`)
	b.WriteString(`<button type="submit">查询</button>`)
	b.WriteString(`<span class="muted"> 当前共 ` + strconv.Itoa(len(filtered)) + ` 个群</span></form>`)

	b.WriteString(`<table><thead><tr><th>群ID</th><th>群名</th><th>群类型</th><th>标签</th><th>记账状态</th><th>入款费率</th><th>入款汇率</th><th>下发费率</th><th>下发汇率</th><th>下发地址</th><th>下发地址备注</th><th>专属地址</th><th>日切时间</th><th>自动回复</th><th>操作</th></tr></thead><tbody>`)
	for _, row := range filtered {
		state := "开启"
		if row.BookkeepingEnabled == 0 {
			state = "停止"
		}
		autoReplyState := "关闭"
		if row.AutoReplyEnabled == 1 {
			autoReplyState = "开启"
		}
		displayText := func(raw string) string {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				return "-"
			}
			return html.EscapeString(trimmed)
		}
		groupURL := s.withToken("/admin/group?chat_id=" + strconv.FormatInt(row.ChatID, 10))
		orderURL := s.withToken("/admin/orders?chat_id=" + strconv.FormatInt(row.ChatID, 10))
		broadcastURL := s.withToken("/admin/broadcast?source_chat_id=" + strconv.FormatInt(row.ChatID, 10))
		scheduleURL := s.withToken("/admin/schedules?chat_id=" + strconv.FormatInt(row.ChatID, 10))
		b.WriteString(`<tr>`)
		b.WriteString(`<td>` + strconv.FormatInt(row.ChatID, 10) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.Title) + `</td>`)
		b.WriteString(`<td>` + displayText(row.GroupType) + `</td>`)
		b.WriteString(`<td>` + displayText(row.GroupTags) + `</td>`)
		b.WriteString(`<td>` + state + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(utils.FormatAmount(row.FeeRate)) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(utils.FormatAmount(row.CustomRate)) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(utils.FormatAmount(row.PayoutFeeRate)) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(utils.FormatAmount(row.PayoutRate)) + `</td>`)
		b.WriteString(`<td>` + displayText(row.PayoutAddress) + `</td>`)
		b.WriteString(`<td>` + displayText(row.PayoutAddressRemark) + `</td>`)
		b.WriteString(`<td>` + displayText(row.ExclusiveAddress) + `</td>`)
		b.WriteString(`<td>` + displayText(row.DayCutTime) + `</td>`)
		b.WriteString(`<td>` + autoReplyState + `</td>`)
		b.WriteString(`<td><a href="` + groupURL + `">详情</a> | <a href="` + orderURL + `">查单</a> | <a href="` + broadcastURL + `">群发</a> | <a href="` + scheduleURL + `">定时</a></td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	s.writePage(w, "群组管理", b.String())
}

func (s *Server) handleGroup(w http.ResponseWriter, r *http.Request) {
	s.handleGroupConfig(w, r)
}

func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	chatID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("chat_id")), 10, 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit <= 0 || limit > 500 {
		limit = 120
	}
	if chatID == 0 {
		groups, _ := s.db.ListAllGroups()
		var b strings.Builder
		b.WriteString(`<div class="muted">请选择群后查看查单列表：</div>`)
		b.WriteString(`<table><thead><tr><th>群ID</th><th>群名</th><th>操作</th></tr></thead><tbody>`)
		for _, row := range groups {
			link := s.withToken("/admin/orders?chat_id=" + strconv.FormatInt(row.ChatID, 10))
			b.WriteString(`<tr><td>` + strconv.FormatInt(row.ChatID, 10) + `</td><td>` + html.EscapeString(row.Title) + `</td><td><a href="` + link + `">查看账单</a></td></tr>`)
		}
		b.WriteString(`</tbody></table>`)
		s.writePage(w, "查单管理", b.String())
		return
	}
	group, err := s.db.GetGroup(chatID)
	if err != nil {
		s.writePage(w, "查单管理", `<div class="err">读取群配置失败：`+html.EscapeString(err.Error())+`</div>`)
		return
	}
	startAt := utils.GetDayStartISO(group.DayCutTime, s.cfg.Timezone)
	rows, err := s.db.ListOrderList(chatID, startAt, limit)
	if err != nil {
		s.writePage(w, "查单管理", `<div class="err">读取账单失败：`+html.EscapeString(err.Error())+`</div>`)
		return
	}
	var b strings.Builder
	b.WriteString(`<p>群：` + html.EscapeString(group.Title) + `（` + strconv.FormatInt(chatID, 10) + `）</p>`)
	b.WriteString(`<p class="muted">账期起点（UTC）：` + html.EscapeString(startAt) + `，展示 ` + strconv.Itoa(len(rows)) + ` 条。</p>`)
	b.WriteString(`<table><thead><tr><th>ID</th><th>时间</th><th>类型</th><th>金额</th><th>币种</th><th>汇率</th><th>费率</th><th>记账金额</th><th>用户</th><th>备注</th></tr></thead><tbody>`)
	for _, row := range rows {
		entryType := "下发"
		if row.EntryType == "deposit" {
			entryType = "入款"
		}
		userLabel := row.Username
		if strings.TrimSpace(userLabel) == "" {
			userLabel = strconv.FormatInt(row.UserID, 10)
		}
		b.WriteString(`<tr>`)
		b.WriteString(`<td>` + strconv.FormatInt(row.ID, 10) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.CreatedAt) + `</td>`)
		b.WriteString(`<td>` + entryType + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(utils.FormatAmount(row.Amount)) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.Currency) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(utils.FormatAmount(row.Rate)) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(utils.FormatAmount(row.FeeRate)) + `%</td>`)
		b.WriteString(`<td>` + html.EscapeString(utils.FormatAmount(row.FinalAmount)) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(userLabel) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.Note) + `</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	s.writePage(w, "查单管理", b.String())
}

func (s *Server) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	groups, _ := s.db.ListAllGroups()
	resultMsg := ""
	resultErr := ""
	if r.Method == http.MethodPost {
		if err := parseLimitedAdminForm(w, r); err != nil {
			resultErr = "表单解析失败：" + err.Error()
		} else {
			sourceChatID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("source_chat_id")), 10, 64)
			req := bot.AdminBroadcastRequest{
				SourceChatID: sourceChatID,
				TargetTags:   utils.SplitTags(r.FormValue("target_tags")),
				IdentityName: strings.TrimSpace(r.FormValue("identity_name")),
				MediaType:    strings.TrimSpace(strings.ToLower(r.FormValue("media_type"))),
				MediaFileID:  strings.TrimSpace(r.FormValue("media_file_id")),
				Content:      strings.TrimSpace(r.FormValue("content")),
			}
			sent, err := s.app.AdminBroadcast(req)
			if err != nil {
				resultErr = err.Error()
			} else {
				resultMsg = fmt.Sprintf("群发已完成，成功发送到 %d 个群。", sent)
			}
		}
	}

	sourceValue := strings.TrimSpace(r.URL.Query().Get("source_chat_id"))
	var b strings.Builder
	if resultMsg != "" {
		b.WriteString(`<div class="msg">` + html.EscapeString(resultMsg) + `</div>`)
	}
	if resultErr != "" {
		b.WriteString(`<div class="err">` + html.EscapeString(resultErr) + `</div>`)
	}
	b.WriteString(`<form method="post" action="` + s.withToken("/admin/broadcast") + `">`)
	b.WriteString(`<h2>群发配置</h2>`)
	b.WriteString(`<p>来源群ID：<input name="source_chat_id" required value="` + html.EscapeString(sourceValue) + `" placeholder="-100xxxxxxxxxx"></p>`)
	b.WriteString(`<p class="muted">若来源群设置了群标签，不填目标标签时将按来源群标签路由。</p>`)
	b.WriteString(`<p>目标标签（可选，逗号分隔）：<input name="target_tags" placeholder="标签A,标签B" style="width:360px"></p>`)
	b.WriteString(`<p>指定@身份（可选）：<input name="identity_name" placeholder="合作伙伴"></p>`)
	b.WriteString(`<p>媒体类型：<select name="media_type">`)
	b.WriteString(`<option value="text">文字</option><option value="photo">图片</option><option value="video">视频</option><option value="voice">语音</option>`)
	b.WriteString(`</select></p>`)
	b.WriteString(`<p>媒体 FileID（媒体模式必填）：<input name="media_file_id" placeholder="AgACAg..." style="width:520px"></p>`)
	b.WriteString(`<p>文本内容（可为空）：<textarea name="content" placeholder="输入群发文案"></textarea></p>`)
	b.WriteString(`<p><button type="submit">立即群发</button></p>`)
	b.WriteString(`</form>`)

	b.WriteString(`<h2>可选来源群</h2><table><thead><tr><th>群ID</th><th>群名</th><th>标签</th><th>快捷</th></tr></thead><tbody>`)
	for _, row := range groups {
		link := s.withToken("/admin/broadcast?source_chat_id=" + strconv.FormatInt(row.ChatID, 10))
		b.WriteString(`<tr>`)
		b.WriteString(`<td>` + strconv.FormatInt(row.ChatID, 10) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.Title) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.GroupTags) + `</td>`)
		b.WriteString(`<td><a href="` + link + `">填充来源群</a></td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	s.writePage(w, "群发面板", b.String())
}

func (s *Server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	chatID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("chat_id")), 10, 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit <= 0 || limit > 500 {
		limit = 120
	}
	statusFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))

	parseInt64 := func(raw string) int64 {
		v, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		return v
	}
	parseInt := func(raw string) int {
		v, _ := strconv.Atoi(strings.TrimSpace(raw))
		return v
	}

	msg := ""
	errMsg := ""
	if r.Method == http.MethodPost {
		if err := parseLimitedAdminForm(w, r); err != nil {
			errMsg = "表单解析失败: " + err.Error()
		} else {
			if v := strings.TrimSpace(r.FormValue("chat_id")); v != "" {
				chatID, _ = strconv.ParseInt(v, 10, 64)
			}
			action := strings.TrimSpace(r.FormValue("action"))
			switch action {
			case "create":
				req := bot.AdminScheduleCreateRequest{
					ChatID:          chatID,
					SendAt:          strings.TrimSpace(r.FormValue("send_at")),
					Content:         strings.TrimSpace(r.FormValue("content")),
					SourceChatID:    parseInt64(r.FormValue("source_chat_id")),
					SourceMessageID: parseInt(r.FormValue("source_message_id")),
					IdentityName:    strings.TrimSpace(r.FormValue("identity_name")),
					TargetTags:      strings.TrimSpace(r.FormValue("target_tags")),
					CreatorUserID:   parseInt64(r.FormValue("creator_user_id")),
				}
				scheduleID, err := s.app.AdminCreateSchedule(req)
				if err != nil {
					errMsg = err.Error()
				} else {
					msg = fmt.Sprintf("定时任务已创建，任务ID: %d", scheduleID)
				}
			case "edit":
				req := bot.AdminScheduleUpdateRequest{
					ChatID:          chatID,
					ScheduleID:      parseInt64(r.FormValue("schedule_id")),
					SendAt:          strings.TrimSpace(r.FormValue("send_at")),
					Content:         strings.TrimSpace(r.FormValue("content")),
					SourceChatID:    parseInt64(r.FormValue("source_chat_id")),
					SourceMessageID: parseInt(r.FormValue("source_message_id")),
					IdentityName:    strings.TrimSpace(r.FormValue("identity_name")),
					TargetTags:      strings.TrimSpace(r.FormValue("target_tags")),
				}
				if err := s.app.AdminUpdateSchedule(req); err != nil {
					errMsg = err.Error()
				} else {
					msg = fmt.Sprintf("任务 #%d 已更新并重新排程", req.ScheduleID)
				}
			case "resend":
				scheduleID := parseInt64(r.FormValue("schedule_id"))
				if err := s.app.AdminResendSchedule(chatID, scheduleID); err != nil {
					errMsg = err.Error()
				} else {
					msg = fmt.Sprintf("任务 #%d 已加入重发队列", scheduleID)
				}
			case "disable":
				scheduleID := parseInt64(r.FormValue("schedule_id"))
				if err := s.app.AdminDisableSchedule(chatID, scheduleID); err != nil {
					errMsg = err.Error()
				} else {
					msg = fmt.Sprintf("任务 #%d 已停用", scheduleID)
				}
			case "delete":
				scheduleID := parseInt64(r.FormValue("schedule_id"))
				if err := s.app.AdminDeleteSchedule(chatID, scheduleID); err != nil {
					errMsg = err.Error()
				} else {
					msg = fmt.Sprintf("任务 #%d 已删除", scheduleID)
				}
			default:
				errMsg = "未知操作"
			}
		}
	}

	loc := time.Local
	if tz := strings.TrimSpace(s.cfg.Timezone); tz != "" {
		if loaded, err := time.LoadLocation(tz); err == nil {
			loc = loaded
		}
	}
	formatDisplayTime := func(raw string) string {
		dt, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return raw
		}
		return dt.In(loc).Format("2006-01-02 15:04:05")
	}
	formatInputTime := func(raw string) string {
		dt, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return ""
		}
		return dt.In(loc).Format("2006-01-02T15:04")
	}
	newScheduleDefaultTime := time.Now().In(loc).Add(10 * time.Minute).Format("2006-01-02T15:04")

	tokenInput := ""
	if token := strings.TrimSpace(s.cfg.AdminToken); token != "" {
		tokenInput = `<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`
	}
	chatHidden := `<input type="hidden" name="chat_id" value="` + strconv.FormatInt(chatID, 10) + `">`
	pageURL := func(id int64) string {
		raw := "/admin/schedules?chat_id=" + strconv.FormatInt(id, 10) + "&limit=" + strconv.Itoa(limit)
		if statusFilter != "" {
			raw += "&status=" + url.QueryEscape(statusFilter)
		}
		return s.withToken(raw)
	}

	groups, _ := s.db.ListAllGroups()
	var selected *db.GroupConfig
	if chatID != 0 {
		if row, err := s.db.GetGroup(chatID); err == nil {
			selected = &row
		}
	}

	var b strings.Builder
	if msg != "" {
		b.WriteString(`<div class="msg">` + html.EscapeString(msg) + `</div>`)
	}
	if errMsg != "" {
		b.WriteString(`<div class="err">` + html.EscapeString(errMsg) + `</div>`)
	}

	b.WriteString(`<form method="get" action="/admin/schedules">`)
	if token := strings.TrimSpace(s.cfg.AdminToken); token != "" {
		b.WriteString(`<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`)
	}
	b.WriteString(`群ID: <input name="chat_id" value="`)
	if chatID != 0 {
		b.WriteString(strconv.FormatInt(chatID, 10))
	}
	b.WriteString(`" placeholder="-100xxxxxxxxxx">`)
	b.WriteString(` 状态: <select name="status">`)
	statuses := []string{"", "pending", "done", "canceled", "expired"}
	for _, item := range statuses {
		label := "全部"
		if item != "" {
			label = item
		}
		selectedAttr := ""
		if statusFilter == item {
			selectedAttr = ` selected`
		}
		b.WriteString(`<option value="` + html.EscapeString(item) + `"` + selectedAttr + `>` + html.EscapeString(label) + `</option>`)
	}
	b.WriteString(`</select>`)
	b.WriteString(` 数量: <input name="limit" value="` + strconv.Itoa(limit) + `" style="width:80px">`)
	b.WriteString(` <button type="submit">加载任务</button></form>`)

	if selected == nil {
		b.WriteString(`<h2>可选群</h2><table><thead><tr><th>群ID</th><th>群名</th><th>标签</th><th>操作</th></tr></thead><tbody>`)
		for _, row := range groups {
			link := s.withToken("/admin/schedules?chat_id=" + strconv.FormatInt(row.ChatID, 10) + "&limit=" + strconv.Itoa(limit))
			b.WriteString(`<tr><td>` + strconv.FormatInt(row.ChatID, 10) + `</td><td>` + html.EscapeString(row.Title) + `</td><td>` + html.EscapeString(row.GroupTags) + `</td><td><a href="` + link + `">进入定时群发</a></td></tr>`)
		}
		b.WriteString(`</tbody></table>`)
		s.writePage(w, "定时群发管理", b.String())
		return
	}

	rows, err := s.app.AdminListSchedules(chatID, limit)
	if err != nil {
		b.WriteString(`<div class="err">读取任务失败: ` + html.EscapeString(err.Error()) + `</div>`)
		rows = []db.Schedule{}
	}
	filtered := make([]db.Schedule, 0, len(rows))
	for _, row := range rows {
		if statusFilter == "" || strings.EqualFold(row.Status, statusFilter) {
			filtered = append(filtered, row)
		}
	}

	b.WriteString(`<h2>当前群: ` + html.EscapeString(orDefault(selected.Title, strconv.FormatInt(chatID, 10))) + ` (` + strconv.FormatInt(chatID, 10) + `)</h2>`)
	b.WriteString(`<p class="muted">共 ` + strconv.Itoa(len(filtered)) + ` 条任务（原始 ` + strconv.Itoa(len(rows)) + ` 条）</p>`)

	b.WriteString(`<h2>新建任务</h2>`)
	b.WriteString(`<form method="post" action="` + pageURL(chatID) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="create">`)
	b.WriteString(`<p>发送时间: <input type="datetime-local" name="send_at" value="` + html.EscapeString(newScheduleDefaultTime) + `" required></p>`)
	b.WriteString(`<p>文本内容: <textarea name="content" placeholder="输入群发文案；若发媒体可留空"></textarea></p>`)
	b.WriteString(`<p>来源群ID(媒体可选): <input name="source_chat_id" placeholder="默认当前群" value="` + strconv.FormatInt(chatID, 10) + `">`)
	b.WriteString(` 来源消息ID(媒体可选): <input name="source_message_id" placeholder="12345"></p>`)
	b.WriteString(`<p>@身份(可选): <input name="identity_name" placeholder="合作伙伴"> 标签(可选): <input name="target_tags" placeholder="标签A,标签B" style="width:280px"></p>`)
	b.WriteString(`<p>创建人用户ID(可选): <input name="creator_user_id" placeholder="123456"></p>`)
	b.WriteString(`<p><button type="submit">创建定时任务</button></p></form>`)

	b.WriteString(`<h2>任务列表</h2>`)
	if len(filtered) == 0 {
		b.WriteString(`<div class="muted">暂无匹配任务</div>`)
		s.writePage(w, "定时群发管理", b.String())
		return
	}
	b.WriteString(`<table><thead><tr><th>ID</th><th>状态</th><th>发送时间</th><th>类型</th><th>内容</th><th>身份/标签</th><th>来源消息</th><th>创建人</th><th>编辑</th><th>操作</th></tr></thead><tbody>`)
	for _, row := range filtered {
		kind := "文本"
		sourceText := "-"
		if row.SourceMessageID > 0 {
			kind = "媒体"
			sourceChatID := row.SourceChatID
			if sourceChatID == 0 {
				sourceChatID = row.ChatID
			}
			sourceText = strconv.FormatInt(sourceChatID, 10) + "/" + strconv.Itoa(row.SourceMessageID)
		}
		editTime := formatInputTime(row.SendAt)
		b.WriteString(`<tr>`)
		b.WriteString(`<td>#` + strconv.FormatInt(row.ID, 10) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.Status) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(formatDisplayTime(row.SendAt)) + `</td>`)
		b.WriteString(`<td>` + kind + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.Content) + `</td>`)
		b.WriteString(`<td>身份:` + html.EscapeString(orDefault(row.IdentityName, "-")) + `<br>标签:` + html.EscapeString(orDefault(row.TargetTags, "-")) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(sourceText) + `</td>`)
		b.WriteString(`<td>` + strconv.FormatInt(row.CreatorUserID, 10) + `</td>`)

		b.WriteString(`<td><form method="post" action="` + pageURL(chatID) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="edit"><input type="hidden" name="schedule_id" value="` + strconv.FormatInt(row.ID, 10) + `">`)
		b.WriteString(`时间: <input type="datetime-local" name="send_at" value="` + html.EscapeString(editTime) + `" required><br>`)
		b.WriteString(`内容: <input name="content" value="` + html.EscapeString(row.Content) + `" style="width:220px"><br>`)
		sourceChatValue := ""
		if row.SourceChatID != 0 {
			sourceChatValue = strconv.FormatInt(row.SourceChatID, 10)
		}
		b.WriteString(`来源群ID: <input name="source_chat_id" value="` + html.EscapeString(sourceChatValue) + `" style="width:120px"> `)
		sourceMidValue := ""
		if row.SourceMessageID > 0 {
			sourceMidValue = strconv.Itoa(row.SourceMessageID)
		}
		b.WriteString(`来源消息ID: <input name="source_message_id" value="` + html.EscapeString(sourceMidValue) + `" style="width:100px"><br>`)
		b.WriteString(`@身份: <input name="identity_name" value="` + html.EscapeString(row.IdentityName) + `" style="width:120px"> `)
		b.WriteString(`标签: <input name="target_tags" value="` + html.EscapeString(row.TargetTags) + `" style="width:160px"><br>`)
		b.WriteString(`<button type="submit">保存</button></form></td>`)

		b.WriteString(`<td>`)
		b.WriteString(`<form method="post" action="` + pageURL(chatID) + `" style="display:inline-block;margin-right:6px">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="resend"><input type="hidden" name="schedule_id" value="` + strconv.FormatInt(row.ID, 10) + `"><button type="submit">重发</button></form>`)
		b.WriteString(`<form method="post" action="` + pageURL(chatID) + `" style="display:inline-block;margin-right:6px">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="disable"><input type="hidden" name="schedule_id" value="` + strconv.FormatInt(row.ID, 10) + `"><button type="submit">停用</button></form>`)
		b.WriteString(`<form method="post" action="` + pageURL(chatID) + `" style="display:inline-block">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="delete"><input type="hidden" name="schedule_id" value="` + strconv.FormatInt(row.ID, 10) + `"><button type="submit">删除</button></form>`)
		b.WriteString(`</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)

	s.writePage(w, "定时群发管理", b.String())
}

func (s *Server) handleAutomation(w http.ResponseWriter, r *http.Request) {
	chatID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("chat_id")), 10, 64)
	msg := ""
	errMsg := ""
	if r.Method == http.MethodPost {
		if err := parseLimitedAdminForm(w, r); err != nil {
			errMsg = "表单解析失败：" + err.Error()
		} else {
			if v := strings.TrimSpace(r.FormValue("chat_id")); v != "" {
				chatID, _ = strconv.ParseInt(v, 10, 64)
			}
			action := strings.TrimSpace(r.FormValue("action"))
			switch action {
			case "toggle_auto_reply":
				flag := 0
				if strings.TrimSpace(r.FormValue("auto_reply_enabled")) == "1" {
					flag = 1
				}
				if chatID == 0 {
					errMsg = "请填写 chat_id。"
				} else if err := s.db.UpdateGroupFields(chatID, map[string]any{"auto_reply_enabled": flag}); err != nil {
					errMsg = err.Error()
				} else {
					msg = fmt.Sprintf("自动回复总开关已%s。", map[bool]string{true: "开启", false: "关闭"}[flag == 1])
				}
			case "toggle_custom_time":
				flag := 0
				if strings.TrimSpace(r.FormValue("custom_content_with_time")) == "1" {
					flag = 1
				}
				if chatID == 0 {
					errMsg = "请填写 chat_id。"
				} else if err := s.db.UpdateGroupFields(chatID, map[string]any{"custom_content_with_time": flag}); err != nil {
					errMsg = err.Error()
				} else {
					msg = fmt.Sprintf("自定义内容时间显示已%s。", map[bool]string{true: "开启", false: "关闭"}[flag == 1])
				}
			case "set_auto_reply":
				if chatID == 0 {
					errMsg = "请填写 chat_id 与回复内容。"
					break
				}
				keyword, err := utils.SanitizeAutomationKeyword(r.FormValue("keyword"), true)
				if err != nil {
					errMsg = err.Error()
					break
				}
				content, err := utils.SanitizeAutomationContent(r.FormValue("content"))
				if err != nil {
					errMsg = err.Error()
					break
				}
				if err := s.db.SetAutoReply(chatID, keyword, content); err != nil {
					errMsg = err.Error()
				} else {
					msg = "自动回复已保存。"
				}
			case "delete_auto_reply":
				if chatID == 0 {
					errMsg = "请填写 chat_id 与关键词。"
					break
				}
				keyword, err := utils.SanitizeAutomationKeyword(r.FormValue("keyword"), false)
				if err != nil {
					errMsg = err.Error()
					break
				}
				if _, err := s.db.DeleteAutoReply(chatID, keyword); err != nil {
					errMsg = err.Error()
				} else {
					msg = "自动回复已删除。"
				}
			case "set_forward_rule":
				if chatID == 0 {
					errMsg = "请填写 chat_id、关键词、目标群ID。"
					break
				}
				keyword, err := utils.SanitizeAutomationKeyword(r.FormValue("keyword"), false)
				if err != nil {
					errMsg = err.Error()
					break
				}
				targets, err := utils.SanitizeForwardTargets(r.FormValue("targets"))
				if err != nil {
					errMsg = err.Error()
					break
				}
				if err := s.db.SetForwardRule(chatID, keyword, targets); err != nil {
					errMsg = err.Error()
				} else {
					msg = "关键词转发规则已保存。"
				}
			case "delete_forward_rule":
				if chatID == 0 {
					errMsg = "请填写 chat_id 与关键词。"
					break
				}
				keyword, err := utils.SanitizeAutomationKeyword(r.FormValue("keyword"), false)
				if err != nil {
					errMsg = err.Error()
					break
				}
				if _, err := s.db.DeleteForwardRule(chatID, keyword); err != nil {
					errMsg = err.Error()
				} else {
					msg = "关键词转发规则已删除。"
				}
			case "set_custom_content":
				if chatID == 0 {
					errMsg = "请填写 chat_id、触发词、内容。"
					break
				}
				key, err := utils.SanitizeAutomationTriggerKey(r.FormValue("key"))
				if err != nil {
					errMsg = err.Error()
					break
				}
				content, err := utils.SanitizeAutomationContent(r.FormValue("content"))
				if err != nil {
					errMsg = err.Error()
					break
				}
				if err := s.db.SetCustomContent(chatID, key, content, 0); err != nil {
					errMsg = err.Error()
				} else {
					msg = "自定义内容已保存。"
				}
			case "delete_custom_content":
				if chatID == 0 {
					errMsg = "请填写 chat_id 与触发词。"
					break
				}
				key, err := utils.SanitizeAutomationTriggerKey(r.FormValue("key"))
				if err != nil {
					errMsg = err.Error()
					break
				}
				if _, err := s.db.DeleteCustomContent(chatID, key); err != nil {
					errMsg = err.Error()
				} else {
					msg = "自定义内容已删除。"
				}
			case "set_identity":
				identity := strings.TrimSpace(r.FormValue("identity"))
				userID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("user_id")), 10, 64)
				username := strings.TrimSpace(r.FormValue("username"))
				if chatID == 0 || identity == "" || userID == 0 {
					errMsg = "请填写 chat_id、身份名、用户ID。"
				} else if err := s.db.SetIdentity(chatID, identity, userID, username); err != nil {
					errMsg = err.Error()
				} else {
					msg = "身份已设置。"
				}
			case "delete_identity":
				identity := strings.TrimSpace(r.FormValue("identity"))
				userID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("user_id")), 10, 64)
				if chatID == 0 || userID == 0 {
					errMsg = "请填写 chat_id 与用户ID。"
				} else {
					var err error
					if identity == "" {
						err = s.db.RemoveIdentity(chatID, userID)
					} else {
						err = s.db.RemoveIdentityByName(chatID, identity, userID)
					}
					if err != nil {
						errMsg = err.Error()
					} else {
						msg = "身份已删除。"
					}
				}
			default:
				errMsg = "未知操作。"
			}
		}
	}

	groups, _ := s.db.ListAllGroups()
	var selected *db.GroupConfig
	if chatID != 0 {
		if row, err := s.db.GetGroup(chatID); err == nil {
			selected = &row
		}
	}

	autoReplies := []db.AutoReply{}
	forwardRules := []db.ForwardRule{}
	customContents := []db.CustomContent{}
	identities := []db.IdentityAssignment{}
	groupEvals := []map[string]any{}
	salesEvals := []map[string]any{}
	if selected != nil {
		autoReplies, _ = s.db.ListAutoReplies(chatID)
		forwardRules, _ = s.db.ListForwardRules(chatID)
		customContents, _ = s.db.ListCustomContents(chatID)
		identities, _ = s.db.ListIdentityAssignments(chatID)
		groupEvals, _ = s.db.ListEvaluations(chatID, "group")
		salesEvals, _ = s.db.ListEvaluations(chatID, "sales")
	}

	var b strings.Builder
	if msg != "" {
		b.WriteString(`<div class="msg">` + html.EscapeString(msg) + `</div>`)
	}
	if errMsg != "" {
		b.WriteString(`<div class="err">` + html.EscapeString(errMsg) + `</div>`)
	}

	b.WriteString(`<form method="get" action="/admin/automation">`)
	if token := strings.TrimSpace(s.cfg.AdminToken); token != "" {
		b.WriteString(`<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`)
	}
	b.WriteString(`群ID：<input name="chat_id" value="` + func() string {
		if chatID == 0 {
			return ""
		}
		return strconv.FormatInt(chatID, 10)
	}() + `" placeholder="-100xxxxxxxxxx">`)
	b.WriteString(`<button type="submit">加载配置</button></form>`)

	if selected == nil {
		b.WriteString(`<h2>可选群</h2><table><thead><tr><th>群ID</th><th>群名</th><th>操作</th></tr></thead><tbody>`)
		for _, row := range groups {
			link := s.withToken("/admin/automation?chat_id=" + strconv.FormatInt(row.ChatID, 10))
			b.WriteString(`<tr><td>` + strconv.FormatInt(row.ChatID, 10) + `</td><td>` + html.EscapeString(row.Title) + `</td><td><a href="` + link + `">进入自动化管理</a></td></tr>`)
		}
		b.WriteString(`</tbody></table>`)
		s.writePage(w, "自动化管理", b.String())
		return
	}

	tokenInput := ""
	if token := strings.TrimSpace(s.cfg.AdminToken); token != "" {
		tokenInput = `<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`
	}
	chatHidden := `<input type="hidden" name="chat_id" value="` + strconv.FormatInt(chatID, 10) + `">`

	b.WriteString(`<h2>当前群：` + html.EscapeString(orDefault(selected.Title, strconv.FormatInt(chatID, 10))) + `（` + strconv.FormatInt(chatID, 10) + `）</h2>`)

	autoEnabled := selected.AutoReplyEnabled == 1
	customTimeEnabled := selected.CustomContentWithTime == 1
	autoOnSelected := ""
	autoOffSelected := ""
	if autoEnabled {
		autoOnSelected = ` selected`
	} else {
		autoOffSelected = ` selected`
	}
	customOnSelected := ""
	customOffSelected := ""
	if customTimeEnabled {
		customOnSelected = ` selected`
	} else {
		customOffSelected = ` selected`
	}

	b.WriteString(`<h2>功能开关</h2>`)
	b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="toggle_auto_reply">`)
	b.WriteString(`自动回复总开关：<select name="auto_reply_enabled"><option value="1"` + autoOnSelected + `>开启</option><option value="0"` + autoOffSelected + `>关闭</option></select> <button type="submit">保存</button></form>`)
	b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `" style="margin-top:6px">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="toggle_custom_time">`)
	b.WriteString(`自定义内容时间显示：<select name="custom_content_with_time"><option value="1"` + customOnSelected + `>开启</option><option value="0"` + customOffSelected + `>关闭</option></select> <button type="submit">保存</button></form>`)
	b.WriteString(`<p class="muted">当前状态：自动回复=` + map[bool]string{true: "开启", false: "关闭"}[autoEnabled] + `，自定义内容时间=` + map[bool]string{true: "开启", false: "关闭"}[customTimeEnabled] + `</p>`)

	b.WriteString(`<h2>自动回复</h2>`)
	b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="set_auto_reply">`)
	b.WriteString(`关键词（可空）：<input name="keyword" placeholder="到账"> 内容：<input name="content" style="width:420px" placeholder="已收到，请稍候"> <button type="submit">新增/覆盖</button></form>`)
	b.WriteString(`<table><thead><tr><th>关键词</th><th>内容</th><th>删除</th></tr></thead><tbody>`)
	for _, row := range autoReplies {
		b.WriteString(`<tr><td>` + html.EscapeString(orDefault(row.Keyword, "（无关键词）")) + `</td><td>` + html.EscapeString(row.Content) + `</td><td>`)
		b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="delete_auto_reply"><input type="hidden" name="keyword" value="` + html.EscapeString(row.Keyword) + `"><button type="submit">删除</button></form>`)
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)

	b.WriteString(`<h2>关键词转发</h2>`)
	b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="set_forward_rule">`)
	b.WriteString(`关键词：<input name="keyword" placeholder="USDT"> 目标群ID（逗号分隔）：<input name="targets" style="width:320px" placeholder="-1001,-1002"> <button type="submit">新增/覆盖</button></form>`)
	b.WriteString(`<table><thead><tr><th>关键词</th><th>目标群</th><th>删除</th></tr></thead><tbody>`)
	for _, row := range forwardRules {
		b.WriteString(`<tr><td>` + html.EscapeString(row.Keyword) + `</td><td>` + html.EscapeString(row.TargetChatIDs) + `</td><td>`)
		b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="delete_forward_rule"><input type="hidden" name="keyword" value="` + html.EscapeString(row.Keyword) + `"><button type="submit">删除</button></form>`)
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)

	b.WriteString(`<h2>自定义内容</h2>`)
	b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="set_custom_content">`)
	b.WriteString(`触发词：<input name="key" placeholder="合作话术"> 内容：<input name="content" style="width:420px" placeholder="请输入发送内容"> <button type="submit">新增/覆盖</button></form>`)
	b.WriteString(`<table><thead><tr><th>触发词</th><th>内容</th><th>创建人</th><th>删除</th></tr></thead><tbody>`)
	for _, row := range customContents {
		b.WriteString(`<tr><td>` + html.EscapeString(row.TriggerKey) + `</td><td>` + html.EscapeString(row.Content) + `</td><td>` + strconv.FormatInt(row.CreatedBy, 10) + `</td><td>`)
		b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="delete_custom_content"><input type="hidden" name="key" value="` + html.EscapeString(row.TriggerKey) + `"><button type="submit">删除</button></form>`)
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)

	b.WriteString(`<h2>身份管理</h2>`)
	b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="set_identity">`)
	b.WriteString(`身份名：<input name="identity" placeholder="合作伙伴"> 用户ID：<input name="user_id" placeholder="123456"> 用户名：<input name="username" placeholder="username"> <button type="submit">设置身份</button></form>`)
	b.WriteString(`<form method="post" action="` + s.withToken("/admin/automation?chat_id="+strconv.FormatInt(chatID, 10)) + `" style="margin-top:6px">` + tokenInput + chatHidden + `<input type="hidden" name="action" value="delete_identity">`)
	b.WriteString(`删除身份（可选）：<input name="identity" placeholder="留空则删除该用户全部身份"> 用户ID：<input name="user_id" placeholder="123456"> <button type="submit">删除身份</button></form>`)
	b.WriteString(`<table><thead><tr><th>身份名</th><th>用户ID</th><th>用户名</th><th>创建时间</th></tr></thead><tbody>`)
	for _, row := range identities {
		b.WriteString(`<tr><td>` + html.EscapeString(row.IdentityName) + `</td><td>` + strconv.FormatInt(row.UserID, 10) + `</td><td>` + html.EscapeString(row.Username) + `</td><td>` + html.EscapeString(row.CreatedAt) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)

	b.WriteString(`<h2>评价服务</h2>`)
	b.WriteString(`<h3>群评价</h3><table><thead><tr><th>用户</th><th>评分</th><th>评价内容</th></tr></thead><tbody>`)
	for _, row := range groupEvals {
		user := fmt.Sprintf("%v", row["username"])
		if strings.TrimSpace(user) == "" || user == "<nil>" {
			user = fmt.Sprintf("%v", row["user_id"])
		}
		score := fmt.Sprintf("%v", row["score"])
		comment := fmt.Sprintf("%v", row["comment"])
		b.WriteString(`<tr><td>` + html.EscapeString(user) + `</td><td>` + html.EscapeString(score) + `</td><td>` + html.EscapeString(comment) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<h3>业务员评价</h3><table><thead><tr><th>用户</th><th>评分</th><th>评价内容</th></tr></thead><tbody>`)
	for _, row := range salesEvals {
		user := fmt.Sprintf("%v", row["username"])
		if strings.TrimSpace(user) == "" || user == "<nil>" {
			user = fmt.Sprintf("%v", row["user_id"])
		}
		score := fmt.Sprintf("%v", row["score"])
		comment := fmt.Sprintf("%v", row["comment"])
		b.WriteString(`<tr><td>` + html.EscapeString(user) + `</td><td>` + html.EscapeString(score) + `</td><td>` + html.EscapeString(comment) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)

	s.writePage(w, "自动化管理", b.String())
}

func (s *Server) handlePrivateUsers(w http.ResponseWriter, r *http.Request) {
	statusFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	msg := ""
	errMsg := ""

	if r.Method == http.MethodPost {
		if err := parseLimitedAdminForm(w, r); err != nil {
			errMsg = "表单解析失败: " + err.Error()
		} else {
			action := strings.TrimSpace(r.FormValue("action"))
			switch action {
			case "review_order":
				orderID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("order_id")), 10, 64)
				reviewStatus := strings.TrimSpace(strings.ToLower(r.FormValue("review_status")))
				reviewNote := strings.TrimSpace(r.FormValue("review_note"))
				reviewedBy, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("reviewed_by")), 10, 64)
				overrideDays, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("override_days")))

				if orderID == 0 {
					errMsg = "缺少订单ID。"
					break
				}
				if reviewStatus != "approved" && reviewStatus != "rejected" && reviewStatus != "canceled" {
					errMsg = "审核状态仅支持 approved/rejected/canceled。"
					break
				}
				order, err := s.db.GetPrivatePurchaseOrder(orderID)
				if err != nil {
					errMsg = "读取订单失败: " + err.Error()
					break
				}
				expiresAt := ""
				if reviewStatus == "approved" {
					days := order.DurationDays
					if overrideDays > 0 {
						days = overrideDays
					}
					if days <= 0 {
						days = 30
					}
					profile, profileErr := s.db.GetPrivateUserProfile(order.UserID)
					if profileErr != nil {
						errMsg = "读取用户档案失败: " + profileErr.Error()
						break
					}
					now := time.Now().UTC()
					base := now
					if oldExp, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(profile.ExpireAt)); parseErr == nil && oldExp.After(now) {
						base = oldExp
					}
					newExpire := base.Add(time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
					profile.UserID = order.UserID
					profile.TrialStatus = "active"
					if strings.TrimSpace(profile.TrialAppliedAt) == "" {
						profile.TrialAppliedAt = now.Format(time.RFC3339)
					}
					profile.ExpireAt = newExpire
					if profileErr = s.db.UpsertPrivateUserProfile(profile); profileErr != nil {
						errMsg = "写入用户档案失败: " + profileErr.Error()
						break
					}
					expiresAt = newExpire
				}
				if err := s.db.ReviewPrivatePurchaseOrder(orderID, reviewStatus, reviewNote, reviewedBy, expiresAt); err != nil {
					errMsg = "更新订单失败: " + err.Error()
					break
				}
				msg = fmt.Sprintf("订单 #%d 已更新为 %s。", orderID, reviewStatus)
			default:
				errMsg = "未知操作。"
			}
		}
	}

	queryStatus := statusFilter
	if queryStatus == "all" {
		queryStatus = ""
	}
	rows, err := s.db.ListPrivatePurchaseOrders(queryStatus, limit)
	if err != nil {
		errMsg = "读取订单列表失败: " + err.Error()
		rows = nil
	}

	tokenInput := ""
	if token := strings.TrimSpace(s.cfg.AdminToken); token != "" {
		tokenInput = `<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`
	}

	statusOptions := []string{"all", "pending", "approved", "rejected", "canceled"}
	if statusFilter == "" {
		statusFilter = "all"
	}
	pageURL := "/admin/private-users?status=" + url.QueryEscape(statusFilter) + "&limit=" + strconv.Itoa(limit)

	var b strings.Builder
	if msg != "" {
		b.WriteString(`<div class="msg">` + html.EscapeString(msg) + `</div>`)
	}
	if errMsg != "" {
		b.WriteString(`<div class="err">` + html.EscapeString(errMsg) + `</div>`)
	}

	b.WriteString(`<form method="get" action="/admin/private-users">`)
	if token := strings.TrimSpace(s.cfg.AdminToken); token != "" {
		b.WriteString(`<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`)
	}
	b.WriteString(`状态：<select name="status">`)
	for _, item := range statusOptions {
		label := item
		if item == "all" {
			label = "全部"
		}
		selectedAttr := ""
		if statusFilter == item {
			selectedAttr = ` selected`
		}
		b.WriteString(`<option value="` + html.EscapeString(item) + `"` + selectedAttr + `>` + html.EscapeString(label) + `</option>`)
	}
	b.WriteString(`</select>`)
	b.WriteString(` 数量：<input name="limit" value="` + strconv.Itoa(limit) + `" style="width:90px">`)
	b.WriteString(` <button type="submit">加载订单</button></form>`)
	b.WriteString(`<p class="muted">审批通过后将自动续期到 ` + html.EscapeString("private_user_profiles.expire_at") + `。</p>`)

	if len(rows) == 0 {
		b.WriteString(`<div class="muted">暂无订单记录。</div>`)
		s.writePage(w, "用户订阅", b.String())
		return
	}

	b.WriteString(`<table><thead><tr><th>订单ID</th><th>用户ID</th><th>套餐</th><th>金额</th><th>状态</th><th>申请时间</th><th>当前到期</th><th>订单到期</th><th>备注</th><th>审批</th></tr></thead><tbody>`)
	for _, row := range rows {
		profileExpire := "-"
		if profile, profileErr := s.db.GetPrivateUserProfile(row.UserID); profileErr == nil && strings.TrimSpace(profile.ExpireAt) != "" {
			profileExpire = profile.ExpireAt
		}
		orderExpire := "-"
		if strings.TrimSpace(row.ExpiresAt) != "" {
			orderExpire = row.ExpiresAt
		}
		b.WriteString(`<tr>`)
		b.WriteString(`<td>#` + strconv.FormatInt(row.ID, 10) + `</td>`)
		b.WriteString(`<td>` + strconv.FormatInt(row.UserID, 10) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.PlanName) + ` (` + strconv.Itoa(row.DurationDays) + `天)</td>`)
		b.WriteString(`<td>` + html.EscapeString(fmt.Sprintf("%.2f %s", row.Amount, row.Currency)) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.Status) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.CreatedAt) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(profileExpire) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(orderExpire) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(orDefault(row.RequestNote, "-")) + `</td>`)
		b.WriteString(`<td>`)
		b.WriteString(`<form method="post" action="` + s.withToken(pageURL) + `">` + tokenInput + `<input type="hidden" name="action" value="review_order"><input type="hidden" name="order_id" value="` + strconv.FormatInt(row.ID, 10) + `">`)
		b.WriteString(`状态：<select name="review_status"><option value="approved">通过</option><option value="rejected">拒绝</option><option value="canceled">取消</option></select><br>`)
		b.WriteString(`续期天数(可选)：<input name="override_days" placeholder="默认订单天数" style="width:120px"><br>`)
		b.WriteString(`审核人ID(可选)：<input name="reviewed_by" placeholder="0" style="width:120px"><br>`)
		b.WriteString(`备注：<input name="review_note" style="width:220px" value="` + html.EscapeString(row.ReviewNote) + `"><br>`)
		b.WriteString(`<button type="submit">提交</button></form>`)
		b.WriteString(`</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	s.writePage(w, "用户订阅", b.String())
}

func (s *Server) handleBank(w http.ResponseWriter, r *http.Request) {
	cardNo := strings.TrimSpace(r.URL.Query().Get("card_no"))
	result := ""
	infoBlock := ""
	if cardNo != "" {
		info := s.app.AdminQueryCard(cardNo)
		lines := []string{
			"卡号: " + info.CardNo,
			"卡号校验: " + services.CardValidationStatus(info),
			"银行: " + orDefault(info.BankName, "未知"),
		}
		if location := services.CardLocationLabel(info); location != "" {
			lines = append(lines, "归属地: "+location)
		}
		if strings.TrimSpace(info.CardName) != "" {
			lines = append(lines, "银行卡名: "+strings.TrimSpace(info.CardName))
		}
		lines = append(lines, "银行卡种: "+orDefault(info.CardType, "未知"))
		if strings.TrimSpace(info.BankPhone) != "" {
			lines = append(lines, "银行客服电话: "+strings.TrimSpace(info.BankPhone))
		}
		if strings.TrimSpace(info.BankURL) != "" {
			lines = append(lines, "银行官网: "+strings.TrimSpace(info.BankURL))
		}
		if strings.TrimSpace(info.BankCode) != "" {
			lines = append(lines, "银行代码: "+strings.TrimSpace(info.BankCode))
		}
		if strings.TrimSpace(info.Brand) != "" {
			lines = append(lines, "卡组织: "+strings.TrimSpace(info.Brand))
		}
		if strings.TrimSpace(info.Country) != "" {
			lines = append(lines, "国家: "+strings.TrimSpace(info.Country))
		}
		lines = append(lines, "数据来源: "+services.CardSourceLabel(info))
		infoBlock = strings.Join(lines, "\n")
		result = s.app.AdminQueryBranch(cardNo)
	}

	var b strings.Builder
	b.WriteString(`<form method="get" action="/admin/bank">`)
	if token := strings.TrimSpace(s.cfg.AdminToken); token != "" {
		b.WriteString(`<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`)
	}
	b.WriteString(`银行卡号：<input name="card_no" value="` + html.EscapeString(cardNo) + `" placeholder="6222..." style="width:280px">`)
	b.WriteString(`<button type="submit">查询联行号</button></form>`)
	if infoBlock != "" {
		b.WriteString(`<h2>银行卡明细</h2><pre>` + html.EscapeString(infoBlock) + `</pre>`)
	}
	if result != "" {
		b.WriteString(`<h2>联行号结果</h2><pre>` + html.EscapeString(result) + `</pre>`)
	}
	s.writePage(w, "联行号查询", b.String())
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
