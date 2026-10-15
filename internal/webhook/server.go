package webhook

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/audit"
	"go-bot/internal/config"
	"go-bot/internal/system"
)

const (
	defaultWebhookPath                  = "/tg/webhook"
	telegramWebhookSecretHeader         = "X-Telegram-Bot-Api-Secret-Token"
	maxRequestBodyBytes                 = 1 << 20
	readTimeout                         = 5 * time.Second
	readHeaderTimeout                   = 5 * time.Second
	writeTimeout                        = 5 * time.Second
	idleTimeout                         = 60 * time.Second
	shutdownTimeout                     = 5 * time.Second
	webhookBackpressureThresholdPercent = 80
	webhookBusyErrorMessage             = "系统繁忙，请稍后重试"
)

type UpdateReceiver interface {
	EnqueueWebhookUpdate(update tgbotapi.Update) bool
}

type UpdateQueueUsageProvider interface {
	WebhookQueueUsage() (queued, capacity int)
}

type Server struct {
	cfg         config.Config
	receiver    UpdateReceiver
	ipBlacklist *ipBlacklist
	auditTrail  *audit.HTTPAuditTrail
}

func NewServer(cfg config.Config, receiver UpdateReceiver) *Server {
	return &Server{
		cfg:         cfg,
		receiver:    receiver,
		ipBlacklist: newIPBlacklist(cfg.WebhookIPBlacklist),
		auditTrail:  audit.NewHTTPAuditTrail(log.Default()),
	}
}

func (s *Server) Run(ctx context.Context) error {
	addr := strings.TrimSpace(s.cfg.WebhookAddr)
	if addr == "" {
		return nil
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	router.POST(
		normalizeWebhookPath(s.cfg.WebhookPath),
		s.rejectBlacklistedIP,
		s.verifySecretToken,
		s.handleTelegramUpdate,
	)
	handler := system.WrapHTTPHandlerWithTracing(router, "webhook-server")
	if s == nil || s.auditTrail == nil {
		return handler
	}
	return s.auditTrail.Wrap(handler)
}

func (s *Server) rejectBlacklistedIP(c *gin.Context) {
	if s == nil || s.ipBlacklist == nil {
		c.Next()
		return
	}
	remoteIP, ok := requestRemoteAddrIP(c.Request)
	if !ok {
		c.Next()
		return
	}
	if s.ipBlacklist.Contains(remoteIP) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"ok": false, "error": "request source ip is blocked"})
		return
	}
	c.Next()
}

func (s *Server) verifySecretToken(c *gin.Context) {
	if s == nil {
		c.Next()
		return
	}
	expectedToken := strings.TrimSpace(s.cfg.WebhookSecretToken)
	if expectedToken == "" {
		c.Next()
		return
	}
	receivedToken := strings.TrimSpace(c.GetHeader(telegramWebhookSecretHeader))
	if len(receivedToken) != len(expectedToken) || subtle.ConstantTimeCompare([]byte(receivedToken), []byte(expectedToken)) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "invalid webhook secret token"})
		return
	}
	c.Next()
}

func (s *Server) handleTelegramUpdate(c *gin.Context) {
	if s.receiver == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "receiver is not configured"})
		return
	}
	if s.isBackpressureTriggered() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": webhookBusyErrorMessage})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
	var update tgbotapi.Update
	if err := c.ShouldBindJSON(&update); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid update payload"})
		return
	}
	if !s.receiver.EnqueueWebhookUpdate(update) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": webhookBusyErrorMessage})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) isBackpressureTriggered() bool {
	if s == nil || s.receiver == nil {
		return false
	}
	reporter, ok := s.receiver.(UpdateQueueUsageProvider)
	if !ok {
		return false
	}
	queued, capacity := reporter.WebhookQueueUsage()
	return reachesLoadThreshold(queued, capacity, webhookBackpressureThresholdPercent)
}

func reachesLoadThreshold(queued, capacity, thresholdPercent int) bool {
	if queued <= 0 || capacity <= 0 {
		return false
	}
	if thresholdPercent <= 0 {
		thresholdPercent = 1
	}
	if thresholdPercent > 100 {
		thresholdPercent = 100
	}
	return queued*100 >= capacity*thresholdPercent
}

func normalizeWebhookPath(rawPath string) string {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return defaultWebhookPath
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

type ipBlacklist struct {
	exactIPs map[netip.Addr]struct{}
	prefixes []netip.Prefix
}

func newIPBlacklist(rawRules []string) *ipBlacklist {
	flattenedRules := flattenRules(rawRules)
	if len(flattenedRules) == 0 {
		return nil
	}

	blacklist := &ipBlacklist{
		exactIPs: make(map[netip.Addr]struct{}, len(flattenedRules)),
		prefixes: make([]netip.Prefix, 0, len(flattenedRules)),
	}

	for _, rule := range flattenedRules {
		if strings.Contains(rule, "/") {
			prefix, err := netip.ParsePrefix(rule)
			if err != nil {
				continue
			}
			blacklist.prefixes = append(blacklist.prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(rule)
		if err != nil {
			continue
		}
		blacklist.exactIPs[addr.Unmap()] = struct{}{}
	}

	if len(blacklist.exactIPs) == 0 && len(blacklist.prefixes) == 0 {
		return nil
	}
	return blacklist
}

func flattenRules(rawRules []string) []string {
	if len(rawRules) == 0 {
		return nil
	}
	flattened := make([]string, 0, len(rawRules))
	for _, raw := range rawRules {
		for _, part := range strings.Split(raw, ",") {
			rule := strings.TrimSpace(part)
			if rule == "" {
				continue
			}
			flattened = append(flattened, rule)
		}
	}
	return flattened
}

func (b *ipBlacklist) Contains(addr netip.Addr) bool {
	if b == nil || !addr.IsValid() {
		return false
	}
	normalized := addr.Unmap()
	if _, ok := b.exactIPs[normalized]; ok {
		return true
	}
	for _, prefix := range b.prefixes {
		if prefix.Contains(normalized) {
			return true
		}
	}
	return false
}

func requestRemoteAddrIP(req *http.Request) (netip.Addr, bool) {
	if req == nil {
		return netip.Addr{}, false
	}
	rawAddr := strings.TrimSpace(req.RemoteAddr)
	if rawAddr == "" {
		return netip.Addr{}, false
	}

	host := rawAddr
	if splitHost, _, err := net.SplitHostPort(rawAddr); err == nil {
		host = splitHost
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
