package admin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/internal/config"
	"go-bot/internal/db"
)

func TestAdminIndexShowsDashboardStatus(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-dashboard.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	if _, err := database.EnsureGroup(-100101, "测试群A"); err != nil {
		t.Fatalf("创建群A失败: %v", err)
	}
	if _, err := database.EnsureGroup(-100102, "测试群B"); err != nil {
		t.Fatalf("创建群B失败: %v", err)
	}

	server := NewServer(config.Config{
		AdminToken: "dash-token",
		AdminAddr:  "127.0.0.1:8090",
		Timezone:   "Asia/Shanghai",
	}, database, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin?token=dash-token", nil)
	resp := httptest.NewRecorder()

	server.handleDashboard(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("首页应直接返回 200: got=%d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, want := range []string{
		"后台首页",
		"首页状态",
		"127.0.0.1:8090",
		"群数量",
		"2",
		"群组管理",
		"权限管理",
		"定时群发",
		"自动化管理",
		"/admin/groups?token=dash-token",
		"/admin/permissions?token=dash-token",
		"/admin/broadcast?token=dash-token",
		"/admin/schedules?token=dash-token",
		"/admin/automation?token=dash-token",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("首页缺少 %q: %s", want, body)
		}
	}
}

func TestAdminIndexRouteUsesDashboard(t *testing.T) {
	server := newTestSessionAdminServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("/admin 应返回登录页 200: got=%d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, want := range []string{"后台登录", "Google Authenticator", "login-form"} {
		if !strings.Contains(body, want) {
			t.Fatalf("/admin 未渲染登录页 %q: %s", want, body)
		}
	}
}

func TestAdminNavIncludesHome(t *testing.T) {
	server := NewServer(config.Config{AdminToken: "nav-token"}, nil, nil)
	nav := server.navHTML()

	for _, want := range []string{"/admin?token=nav-token", "首页"} {
		if !strings.Contains(nav, want) {
			t.Fatalf("导航缺少 %q: %s", want, nav)
		}
	}
}

func TestAdminGroupsShowsOperationalColumns(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-groups.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100201)
	if _, err := database.EnsureGroup(chatID, "运营群"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"group_type":            "业务",
		"group_tags":            "夜班,重点",
		"fee_rate":              11.0,
		"payout_fee_rate":       2.5,
		"custom_rate":           205.0,
		"payout_rate":           210.0,
		"payout_address":        "TXabc",
		"payout_address_remark": "主下发",
		"exclusive_address":     "TXexclusive",
		"day_cut_time":          "03:00:00",
		"auto_reply_enabled":    1,
		"bookkeeping_enabled":   1,
	}); err != nil {
		t.Fatalf("更新群配置失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "groups-token"}, database, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/groups?token=groups-token", nil)
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("群列表状态码错误: %d", resp.Code)
	}
	body := resp.Body.String()
	for _, want := range []string{"运营群", "业务", "夜班,重点", "11", "2.5", "205", "210", "TXabc", "主下发", "TXexclusive", "03:00:00", "自动回复", "/admin/group?chat_id=-100201"} {
		if !strings.Contains(body, want) {
			t.Fatalf("群列表缺少 %q: %s", want, body)
		}
	}
}
