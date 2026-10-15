package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminCloseoutRoutesServeStaticAppShell(t *testing.T) {
	server := newTestSessionAdminServer(t)
	cookies := loginAdminForAPITest(t, server)
	for _, path := range []string{"/admin/snapshots", "/admin/template-library", "/admin/risk-radar", "/admin/insights", "/admin/retries"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		resp := httptest.NewRecorder()
		server.handler().ServeHTTP(resp, req)
		if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `<div id="app"`) {
			t.Fatalf("收尾路由应返回 SPA 壳: path=%s got=%d body=%s", path, resp.Code, resp.Body.String())
		}
	}
}
