package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminFrontendWarnsOnUnsavedChanges(t *testing.T) {
	server := newTestSessionAdminServer(t)
	cookies := loginAdminForAPITest(t, server)
	req := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("读取前端资源失败: got=%d body=%s", resp.Code, resp.Body.String())
	}
	for _, want := range []string{"beforeunload", "popstate", "有未保存的后台操作草稿", "确认离开"} {
		if !strings.Contains(resp.Body.String(), want) {
			t.Fatalf("前端缺少未保存保护标记 %q", want)
		}
	}
}

func TestAdminStaticAppIncludesCloseoutCenter(t *testing.T) {
	server := newTestSessionAdminServer(t)
	cookies := loginAdminForAPITest(t, server)
	req := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("读取前端资源失败: got=%d body=%s", resp.Code, resp.Body.String())
	}
	for _, want := range []string{
		"/admin/snapshots",
		"/admin/template-library",
		"/admin/risk-radar",
		"/admin/insights",
		"/admin/retries",
		"/admin/api/snapshots",
		"/admin/api/template-library",
		"/admin/api/risk-radar",
		"/admin/api/insights",
		"/admin/api/retries",
		"renderSnapshotsCenter",
		"renderTemplateLibraryCenter",
		"renderRiskRadarCenter",
		"renderInsightsCenter",
		"renderRetryCenter",
		"item.description || item.reason",
	} {
		if !bytes.Contains(resp.Body.Bytes(), []byte(want)) {
			t.Fatalf("收尾中心前端缺少 %q", want)
		}
	}
}
