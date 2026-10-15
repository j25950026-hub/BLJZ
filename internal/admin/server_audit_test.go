package admin

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-bot/internal/audit"
	"go-bot/internal/config"
)

func TestAdminHandlerAuditTrailIntegrated(t *testing.T) {
	server := NewServer(config.Config{AdminToken: "audit-token"}, nil, nil)

	var sink bytes.Buffer
	server.auditTrail = audit.NewHTTPAuditTrail(log.New(&sink, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/admin/kill-switch?token=audit-token", nil)
	req.RemoteAddr = "198.51.100.40:9443"
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("后台停机页状态码错误: %d", resp.Code)
	}

	logLine := sink.String()
	checks := []string{
		"[api_audit]",
		"method=GET",
		"path=/admin/kill-switch",
		"source_ip=198.51.100.40",
		"params=query=token=[REDACTED]",
		"result=status=200",
		"紧急停机",
	}
	for _, want := range checks {
		if !strings.Contains(logLine, want) {
			t.Fatalf("后台审计日志缺少字段 %q，got=%s", want, logLine)
		}
	}
}
