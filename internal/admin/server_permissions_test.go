package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go-bot/internal/config"
	"go-bot/internal/db"
)

func TestAdminPermissionsPageListsPermissionScopes(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-list.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100501)
	if _, err := database.EnsureGroup(chatID, "权限群"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}
	if err := database.AddGlobalOwner(90001, "owner"); err != nil {
		t.Fatalf("创建全局权限人失败: %v", err)
	}
	if err := database.AddOperator(0, 90002, "globalop", true); err != nil {
		t.Fatalf("创建全局操作人失败: %v", err)
	}
	if err := database.AddOperator(chatID, 90003, "groupop", false); err != nil {
		t.Fatalf("创建群操作人失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "perm-token"}, database, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/permissions?token=perm-token&chat_id=-100501", nil)
	resp := httptest.NewRecorder()

	server.handlePermissions(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("权限页状态码错误: %d", resp.Code)
	}
	body := resp.Body.String()
	for _, want := range []string{"权限管理", "全局权限人", "全局操作人", "群操作人", "90001", "90002", "90003", "owner", "globalop", "groupop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("权限页缺少 %q: %s", want, body)
		}
	}
}

func TestAdminPermissionsRouteUsesPermissionPage(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-route.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	server := NewServer(config.Config{AdminToken: "perm-token"}, database, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/permissions?token=perm-token", nil)
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("/admin/permissions 状态码错误: %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "权限管理") {
		t.Fatalf("/admin/permissions 未渲染权限页: %s", resp.Body.String())
	}
}

func TestAdminPermissionsRequiresToken(t *testing.T) {
	server := NewServer(config.Config{AdminToken: "perm-token"}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/permissions", nil)
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("权限页缺 token 应返回 401: got=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminNavIncludesPermissions(t *testing.T) {
	server := NewServer(config.Config{AdminToken: "perm-token"}, nil, nil)
	nav := server.navHTML()

	for _, want := range []string{"/admin/permissions?token=perm-token", "权限管理"} {
		if !strings.Contains(nav, want) {
			t.Fatalf("导航缺少权限入口 %q: %s", want, nav)
		}
	}
}

func TestAdminPermissionsPostManagesGlobalOwner(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-owner.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	server := NewServer(config.Config{AdminToken: "perm-token"}, database, nil)

	add := url.Values{}
	add.Set("action", "add_global_owner")
	add.Set("user_id", "91001")
	add.Set("username", "boss")
	req := httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token", strings.NewReader(add.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()
	server.handlePermissions(resp, req)

	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "全局权限人已添加") {
		t.Fatalf("添加全局权限人失败: code=%d body=%s", resp.Code, resp.Body.String())
	}
	owners, err := database.ListGlobalOwners()
	if err != nil || len(owners) != 1 || owners[0].UserID != 91001 {
		t.Fatalf("全局权限人未写入: owners=%+v err=%v", owners, err)
	}

	remove := url.Values{}
	remove.Set("action", "remove_global_owner")
	remove.Set("user_id", "91001")
	req = httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token", strings.NewReader(remove.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp = httptest.NewRecorder()
	server.handlePermissions(resp, req)

	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "全局权限人已删除") {
		t.Fatalf("删除全局权限人失败: code=%d body=%s", resp.Code, resp.Body.String())
	}
	owners, err = database.ListGlobalOwners()
	if err != nil || len(owners) != 0 {
		t.Fatalf("全局权限人未删除: owners=%+v err=%v", owners, err)
	}
}

func TestAdminPermissionsPostRejectsCrossOrigin(t *testing.T) {
	server := NewServer(config.Config{AdminToken: "perm-token"}, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token", strings.NewReader("action=add_global_operator&user_id=92001"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.Host = "autobookbl.site"
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("跨站权限 POST 应被拒绝: got=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminPermissionsPostAllowsMissingOriginAndReferer(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-no-origin.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	server := NewServer(config.Config{AdminToken: "perm-token"}, database, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token", strings.NewReader("action=add_global_operator&user_id=95001"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code == http.StatusForbidden {
		t.Fatalf("缺少 Origin/Referer 但 token 正确时不应被同源检查拒绝: body=%s", resp.Body.String())
	}
}

func TestAdminPermissionsPostManagesGlobalOperator(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-global-operator.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	server := NewServer(config.Config{AdminToken: "perm-token"}, database, nil)

	add := url.Values{}
	add.Set("action", "add_global_operator")
	add.Set("user_id", "92001")
	add.Set("username", "globalop")
	req := httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token", strings.NewReader(add.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()
	server.handlePermissions(resp, req)

	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "全局操作人已添加") {
		t.Fatalf("添加全局操作人失败: code=%d body=%s", resp.Code, resp.Body.String())
	}
	operators, err := database.ListOperators(0, true)
	if err != nil || len(operators) != 1 || operators[0].UserID != 92001 {
		t.Fatalf("全局操作人未写入: operators=%+v err=%v", operators, err)
	}

	remove := url.Values{}
	remove.Set("action", "remove_global_operator")
	remove.Set("user_id", "92001")
	req = httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token", strings.NewReader(remove.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp = httptest.NewRecorder()
	server.handlePermissions(resp, req)

	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "全局操作人已删除") {
		t.Fatalf("删除全局操作人失败: code=%d body=%s", resp.Code, resp.Body.String())
	}
	operators, err = database.ListOperators(0, true)
	if err != nil || len(operators) != 0 {
		t.Fatalf("全局操作人未删除: operators=%+v err=%v", operators, err)
	}
}

func TestAdminPermissionsPostManagesGroupOperator(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-group-operator.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100504)
	if _, err := database.EnsureGroup(chatID, "群操作人测试"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "perm-token"}, database, nil)

	add := url.Values{}
	add.Set("action", "add_group_operator")
	add.Set("chat_id", strconv.FormatInt(chatID, 10))
	add.Set("user_id", "93001")
	add.Set("username", "groupop")
	req := httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token&chat_id=-100504", strings.NewReader(add.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()
	server.handlePermissions(resp, req)

	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "群操作人已添加") {
		t.Fatalf("添加群操作人失败: code=%d body=%s", resp.Code, resp.Body.String())
	}
	operators, err := database.ListOperators(chatID, false)
	if err != nil || len(operators) != 1 || operators[0].UserID != 93001 {
		t.Fatalf("群操作人未写入: operators=%+v err=%v", operators, err)
	}

	remove := url.Values{}
	remove.Set("action", "remove_group_operator")
	remove.Set("chat_id", strconv.FormatInt(chatID, 10))
	remove.Set("user_id", "93001")
	req = httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token&chat_id=-100504", strings.NewReader(remove.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp = httptest.NewRecorder()
	server.handlePermissions(resp, req)

	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "群操作人已删除") {
		t.Fatalf("删除群操作人失败: code=%d body=%s", resp.Code, resp.Body.String())
	}
	operators, err = database.ListOperators(chatID, false)
	if err != nil || len(operators) != 0 {
		t.Fatalf("群操作人未删除: operators=%+v err=%v", operators, err)
	}
}

func TestAdminPermissionsRejectsMismatchedChatID(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-mismatch.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatA := int64(-100505)
	chatB := int64(-100506)
	if _, err := database.EnsureGroup(chatA, "A群"); err != nil {
		t.Fatalf("创建A群失败: %v", err)
	}
	if _, err := database.EnsureGroup(chatB, "B群"); err != nil {
		t.Fatalf("创建B群失败: %v", err)
	}

	form := url.Values{}
	form.Set("action", "add_group_operator")
	form.Set("chat_id", strconv.FormatInt(chatB, 10))
	form.Set("user_id", "94001")
	form.Set("username", "hidden")
	server := NewServer(config.Config{AdminToken: "perm-token"}, database, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token&chat_id=-100505", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handlePermissions(resp, req)

	if !strings.Contains(resp.Body.String(), "chat_id 不一致") {
		t.Fatalf("应提示 chat_id 不一致: %s", resp.Body.String())
	}
	ops, err := database.ListOperators(chatB, false)
	if err != nil {
		t.Fatalf("读取B群操作人失败: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("不应通过隐藏字段写入B群操作人: %+v", ops)
	}
}

func TestAdminPermissionsRejectsUnknownGroupOperator(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-unknown-group.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	form := url.Values{}
	form.Set("action", "add_group_operator")
	form.Set("chat_id", "-100507")
	form.Set("user_id", "94002")
	form.Set("username", "ghost")
	server := NewServer(config.Config{AdminToken: "perm-token"}, database, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token&chat_id=-100507", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handlePermissions(resp, req)

	if !strings.Contains(resp.Body.String(), "群不存在") {
		t.Fatalf("应提示群不存在: %s", resp.Body.String())
	}
	ops, err := database.ListOperators(-100507, false)
	if err != nil {
		t.Fatalf("读取未知群操作人失败: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("未知群不应写入操作人: %+v", ops)
	}
}

func TestAdminPermissionsRejectsInvalidIDsWithoutWrite(t *testing.T) {
	cases := []struct {
		name      string
		form      url.Values
		wantError string
	}{
		{
			name: "invalid user id",
			form: url.Values{
				"action":  {"add_global_operator"},
				"user_id": {"abc"},
			},
			wantError: "用户ID格式错误",
		},
		{
			name: "invalid group chat id",
			form: url.Values{
				"action":  {"add_group_operator"},
				"chat_id": {"abc"},
				"user_id": {"94003"},
			},
			wantError: "群ID格式错误",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-invalid-id.db"))
			if err != nil {
				t.Fatalf("初始化数据库失败: %v", err)
			}
			defer database.Close()

			server := NewServer(config.Config{AdminToken: "perm-token"}, database, nil)
			req := httptest.NewRequest(http.MethodPost, "/admin/permissions?token=perm-token", strings.NewReader(tc.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp := httptest.NewRecorder()

			server.handlePermissions(resp, req)

			if !strings.Contains(resp.Body.String(), tc.wantError) {
				t.Fatalf("应提示 %q: %s", tc.wantError, resp.Body.String())
			}
			globalOps, err := database.ListOperators(0, true)
			if err != nil {
				t.Fatalf("读取全局操作人失败: %v", err)
			}
			if len(globalOps) != 0 {
				t.Fatalf("非法ID不应写入全局操作人: %+v", globalOps)
			}
		})
	}
}

func TestAdminPermissionsEscapesHTMLInput(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-escape.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	if err := database.AddGlobalOwner(96001, `<img src=x onerror=alert(1)>`); err != nil {
		t.Fatalf("创建危险用户名失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "escape-token"}, database, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/permissions?token=escape-token", nil)
	resp := httptest.NewRecorder()

	server.handlePermissions(resp, req)

	body := resp.Body.String()
	if strings.Contains(body, `<img src=x onerror=alert(1)>`) {
		t.Fatalf("权限页包含未转义用户名: %s", body)
	}
	if !strings.Contains(body, `&lt;img src=x onerror=alert(1)&gt;`) {
		t.Fatalf("权限页缺少转义用户名: %s", body)
	}
}
