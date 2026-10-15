package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/internal/db"
)

type adminPermissionsEnvelopeForTest struct {
	OK   bool `json:"ok"`
	Data struct {
		Groups []struct {
			ChatID int64  `json:"chatId"`
			Title  string `json:"title"`
		} `json:"groups"`
		GlobalOwners []struct {
			UserID   int64  `json:"userId"`
			Username string `json:"username"`
			Role     string `json:"role"`
			Scope    string `json:"scope"`
		} `json:"globalOwners"`
		GlobalOperators []struct {
			UserID   int64  `json:"userId"`
			Username string `json:"username"`
			Role     string `json:"role"`
			Scope    string `json:"scope"`
		} `json:"globalOperators"`
		GroupOperators []struct {
			UserID   int64  `json:"userId"`
			Username string `json:"username"`
			Role     string `json:"role"`
			Scope    string `json:"scope"`
			ChatID   int64  `json:"chatId"`
			Group    string `json:"group"`
		} `json:"groupOperators"`
		SelectedChatID int64 `json:"selectedChatId"`
	} `json:"data"`
}

func TestAdminPermissionsAPIListsRolesAndScopes(t *testing.T) {
	database := newAdminPermissionsAPIDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	out := getAdminPermissionsAPI(t, server, cookies, "/admin/api/permissions?chat_id=-100901")

	if out.Data.SelectedChatID != -100901 {
		t.Fatalf("应返回当前筛选群: %+v", out.Data)
	}
	if len(out.Data.Groups) != 2 || out.Data.Groups[0].Title == "" {
		t.Fatalf("应返回群选择列表: %+v", out.Data.Groups)
	}
	if len(out.Data.GlobalOwners) != 1 || out.Data.GlobalOwners[0].UserID != 99001 || out.Data.GlobalOwners[0].Role != "global_owner" || out.Data.GlobalOwners[0].Scope != "all_groups" {
		t.Fatalf("全局权限人数据错误: %+v", out.Data.GlobalOwners)
	}
	if len(out.Data.GlobalOperators) != 1 || out.Data.GlobalOperators[0].UserID != 99002 || out.Data.GlobalOperators[0].Role != "global_operator" || out.Data.GlobalOperators[0].Scope != "all_groups" {
		t.Fatalf("全局操作人数据错误: %+v", out.Data.GlobalOperators)
	}
	if len(out.Data.GroupOperators) != 1 || out.Data.GroupOperators[0].UserID != 99003 || out.Data.GroupOperators[0].Role != "group_operator" || out.Data.GroupOperators[0].Scope != "single_group" || out.Data.GroupOperators[0].ChatID != -100901 || out.Data.GroupOperators[0].Group != "权限群A" {
		t.Fatalf("群操作人数据错误: %+v", out.Data.GroupOperators)
	}
}

func TestAdminPermissionsAPIRejectsLegacyTokenWithoutSession(t *testing.T) {
	server := newTestSessionAdminServer(t)
	server.cfg.AdminToken = "legacy-token"

	for _, tc := range []struct {
		name        string
		rawURL      string
		headerToken string
	}{
		{name: "query token", rawURL: "/admin/api/permissions?token=legacy-token"},
		{name: "header token", rawURL: "/admin/api/permissions", headerToken: "legacy-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.rawURL, nil)
			if tc.headerToken != "" {
				req.Header.Set("X-Admin-Token", tc.headerToken)
			}
			resp := httptest.NewRecorder()
			server.handler().ServeHTTP(resp, req)

			if resp.Code != http.StatusUnauthorized {
				t.Fatalf("权限 API 不应接受 legacy token: got=%d body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestAdminPermissionsAPIRejectsUnknownGroup(t *testing.T) {
	database := newAdminPermissionsAPIDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/permissions?chat_id=-100999", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("不存在的群应返回 400: got=%d body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, "群不存在") {
		t.Fatalf("不存在的群应给出明确错误: %s", body)
	}
}

func TestAdminPermissionsAPICanManageGlobalOwner(t *testing.T) {
	database := newAdminPermissionsAPIDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	created := postAdminPermissionsAPIExpectOK(t, server, cookies, `{"action":"upsert_global_owner","userId":99101,"username":"owner_new"}`)
	if !containsPermissionActor(created.Data.GlobalOwners, 99101, "owner_new", "global_owner") {
		t.Fatalf("写入后返回的全局权限人缺少新增项: %+v", created.Data.GlobalOwners)
	}

	owners, err := database.ListGlobalOwners()
	if err != nil {
		t.Fatalf("读取全局权限人失败: %v", err)
	}
	if !containsGlobalOwner(owners, 99101, "owner_new") {
		t.Fatalf("数据库中应存在新增的全局权限人: %+v", owners)
	}

	updated := postAdminPermissionsAPIExpectOK(t, server, cookies, `{"action":"upsert_global_owner","userId":99101,"username":"owner_updated"}`)
	if !containsPermissionActor(updated.Data.GlobalOwners, 99101, "owner_updated", "global_owner") {
		t.Fatalf("更新后返回的全局权限人用户名不正确: %+v", updated.Data.GlobalOwners)
	}

	owners, err = database.ListGlobalOwners()
	if err != nil {
		t.Fatalf("读取全局权限人失败: %v", err)
	}
	if !containsGlobalOwner(owners, 99101, "owner_updated") {
		t.Fatalf("数据库中应更新全局权限人用户名: %+v", owners)
	}

	removed := postAdminPermissionsAPIExpectOK(t, server, cookies, `{"action":"remove_global_owner","userId":99101}`)
	if containsPermissionActor(removed.Data.GlobalOwners, 99101, "owner_updated", "global_owner") {
		t.Fatalf("删除后返回结果不应再包含该全局权限人: %+v", removed.Data.GlobalOwners)
	}

	owners, err = database.ListGlobalOwners()
	if err != nil {
		t.Fatalf("读取全局权限人失败: %v", err)
	}
	if containsGlobalOwner(owners, 99101, "") {
		t.Fatalf("数据库中不应再存在该全局权限人: %+v", owners)
	}
}

func TestAdminPermissionsAPICanManageGlobalOperator(t *testing.T) {
	database := newAdminPermissionsAPIDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	created := postAdminPermissionsAPIExpectOK(t, server, cookies, `{"action":"upsert_global_operator","userId":99201,"username":"global_new"}`)
	if !containsPermissionActor(created.Data.GlobalOperators, 99201, "global_new", "global_operator") {
		t.Fatalf("写入后返回的全局操作人缺少新增项: %+v", created.Data.GlobalOperators)
	}

	operators, err := database.ListOperators(0, true)
	if err != nil {
		t.Fatalf("读取全局操作人失败: %v", err)
	}
	if !containsOperator(operators, 99201, "global_new") {
		t.Fatalf("数据库中应存在新增的全局操作人: %+v", operators)
	}

	updated := postAdminPermissionsAPIExpectOK(t, server, cookies, `{"action":"upsert_global_operator","userId":99201,"username":"global_updated"}`)
	if !containsPermissionActor(updated.Data.GlobalOperators, 99201, "global_updated", "global_operator") {
		t.Fatalf("更新后返回的全局操作人用户名不正确: %+v", updated.Data.GlobalOperators)
	}

	operators, err = database.ListOperators(0, true)
	if err != nil {
		t.Fatalf("读取全局操作人失败: %v", err)
	}
	if !containsOperator(operators, 99201, "global_updated") {
		t.Fatalf("数据库中应更新全局操作人用户名: %+v", operators)
	}

	removed := postAdminPermissionsAPIExpectOK(t, server, cookies, `{"action":"remove_global_operator","userId":99201}`)
	if containsPermissionActor(removed.Data.GlobalOperators, 99201, "global_updated", "global_operator") {
		t.Fatalf("删除后返回结果不应再包含该全局操作人: %+v", removed.Data.GlobalOperators)
	}

	operators, err = database.ListOperators(0, true)
	if err != nil {
		t.Fatalf("读取全局操作人失败: %v", err)
	}
	if containsOperator(operators, 99201, "") {
		t.Fatalf("数据库中不应再存在该全局操作人: %+v", operators)
	}
}

func TestAdminPermissionsAPIRejectsCrossOriginWrites(t *testing.T) {
	database := newAdminPermissionsAPIDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/permissions", strings.NewReader(`{"action":"upsert_global_owner","userId":99101,"username":"owner_new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	req.Host = "admin.example"
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("跨站权限写操作应返回 403: got=%d body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("跨站权限写操作应返回 JSON: %q", got)
	}
}

func TestAdminPermissionsAPIRejectsInvalidIDsWithoutWrite(t *testing.T) {
	database := newAdminPermissionsAPIDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "zero owner id", body: `{"action":"upsert_global_owner","userId":0,"username":"owner_new"}`},
		{name: "negative operator id", body: `{"action":"upsert_global_operator","userId":-1,"username":"global_new"}`},
		{name: "remove owner with zero id", body: `{"action":"remove_global_owner","userId":0}`},
		{name: "remove operator with zero id", body: `{"action":"remove_global_operator","userId":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := postAdminPermissionsAPIResponse(t, server, cookies, tc.body)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("非法 userId 应返回 400: got=%d body=%s", resp.Code, resp.Body.String())
			}
		})
	}

	owners, err := database.ListGlobalOwners()
	if err != nil {
		t.Fatalf("读取全局权限人失败: %v", err)
	}
	if len(owners) != 1 || owners[0].UserID != 99001 {
		t.Fatalf("非法写入不应改动全局权限人: %+v", owners)
	}

	operators, err := database.ListOperators(0, true)
	if err != nil {
		t.Fatalf("读取全局操作人失败: %v", err)
	}
	if len(operators) != 1 || operators[0].UserID != 99002 {
		t.Fatalf("非法写入不应改动全局操作人: %+v", operators)
	}
}

func newAdminPermissionsAPIDatabase(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-permissions-api.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if _, err := database.EnsureGroup(-100901, "权限群A"); err != nil {
		t.Fatalf("创建群A失败: %v", err)
	}
	if _, err := database.EnsureGroup(-100902, "权限群B"); err != nil {
		t.Fatalf("创建群B失败: %v", err)
	}
	if err := database.AddGlobalOwner(99001, "owner_a"); err != nil {
		t.Fatalf("创建全局权限人失败: %v", err)
	}
	if err := database.AddOperator(0, 99002, "global_op", true); err != nil {
		t.Fatalf("创建全局操作人失败: %v", err)
	}
	if err := database.AddOperator(-100901, 99003, "group_op_a", false); err != nil {
		t.Fatalf("创建群操作人失败: %v", err)
	}
	if err := database.AddOperator(-100902, 99004, "group_op_b", false); err != nil {
		t.Fatalf("创建另一个群操作人失败: %v", err)
	}
	return database
}

func getAdminPermissionsAPI(t *testing.T, server *Server, cookies []*http.Cookie, rawURL string) adminPermissionsEnvelopeForTest {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, rawURL, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("%s 应返回 200: got=%d body=%s", rawURL, resp.Code, resp.Body.String())
	}
	var out adminPermissionsEnvelopeForTest
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("权限 API 应返回 JSON: %v body=%s", err, resp.Body.String())
	}
	if !out.OK {
		t.Fatalf("权限 API 应 ok=true: %+v", out)
	}
	return out
}

func postAdminPermissionsAPIExpectOK(t *testing.T, server *Server, cookies []*http.Cookie, body string) adminPermissionsEnvelopeForTest {
	t.Helper()
	resp := postAdminPermissionsAPIResponse(t, server, cookies, body)
	if resp.Code != http.StatusOK {
		t.Fatalf("权限写接口应返回 200: got=%d body=%s", resp.Code, resp.Body.String())
	}
	var out adminPermissionsEnvelopeForTest
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("权限写接口应返回 JSON: %v body=%s", err, resp.Body.String())
	}
	if !out.OK {
		t.Fatalf("权限写接口应 ok=true: %+v", out)
	}
	return out
}

func postAdminPermissionsAPIResponse(t *testing.T, server *Server, cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/api/permissions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAdminTestSameOrigin(req)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	return resp
}

func containsPermissionActor(items []struct {
	UserID   int64  `json:"userId"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Scope    string `json:"scope"`
}, userID int64, username, role string) bool {
	for _, item := range items {
		if item.UserID != userID {
			continue
		}
		if role != "" && item.Role != role {
			continue
		}
		if username != "" && item.Username != username {
			continue
		}
		return true
	}
	return false
}

func containsGlobalOwner(items []db.GlobalOwner, userID int64, username string) bool {
	for _, item := range items {
		if item.UserID != userID {
			continue
		}
		if username != "" && item.Username != username {
			continue
		}
		return true
	}
	return false
}

func containsOperator(items []db.Operator, userID int64, username string) bool {
	for _, item := range items {
		if item.UserID != userID {
			continue
		}
		if username != "" && item.Username != username {
			continue
		}
		return true
	}
	return false
}
