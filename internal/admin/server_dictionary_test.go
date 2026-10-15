package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/internal/db"
)

type adminDictionaryEnvelopeForTest struct {
	OK   bool `json:"ok"`
	Data struct {
		Types []adminDictionaryItemForTest `json:"types"`
		Tags  []adminDictionaryItemForTest `json:"tags"`
	} `json:"data"`
}

type adminDictionaryItemForTest struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int    `json:"sortOrder"`
	GroupCount  int    `json:"groupCount"`
	InCatalog   bool   `json:"inCatalog"`
}

func TestAdminGroupDictionariesListCreateAndDisable(t *testing.T) {
	database := newAdminDictionaryDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	before := getAdminDictionariesAPI(t, server, cookies)
	if got := findAdminDictionaryItem(before.Data.Types, "业务"); got == nil || got.GroupCount != 2 || got.InCatalog {
		t.Fatalf("已使用但未入字典的群类型应被标记出来: %+v", before.Data.Types)
	}
	if got := findAdminDictionaryItem(before.Data.Tags, "夜班"); got == nil || got.GroupCount != 1 || got.InCatalog {
		t.Fatalf("已使用但未入字典的标签应被标记出来: %+v", before.Data.Tags)
	}

	postAdminDictionaryAPI(t, server, cookies, `{"kind":"group_type","name":"业务","description":"核心业务群","sortOrder":20}`)
	postAdminDictionaryAPI(t, server, cookies, `{"kind":"group_tag","name":"夜班","description":"夜班群","sortOrder":10}`)

	afterCreate := getAdminDictionariesAPI(t, server, cookies)
	if got := findAdminDictionaryItem(afterCreate.Data.Types, "业务"); got == nil || got.GroupCount != 2 || !got.InCatalog || got.Description != "核心业务群" || got.SortOrder != 20 {
		t.Fatalf("入字典后的群类型应保留使用统计: %+v", afterCreate.Data.Types)
	}
	if got := findAdminDictionaryItem(afterCreate.Data.Tags, "夜班"); got == nil || got.GroupCount != 1 || !got.InCatalog || got.Description != "夜班群" {
		t.Fatalf("入字典后的标签应保留使用统计: %+v", afterCreate.Data.Tags)
	}

	deleteAdminDictionaryAPI(t, server, cookies, `{"kind":"group_tag","name":"夜班"}`)

	afterDelete := getAdminDictionariesAPI(t, server, cookies)
	if got := findAdminDictionaryItem(afterDelete.Data.Tags, "夜班"); got == nil || got.GroupCount != 1 || got.InCatalog {
		t.Fatalf("删除字典项不应改动已有群标签，应显示为未入字典: %+v", afterDelete.Data.Tags)
	}
	group, err := database.GetGroup(-100811)
	if err != nil {
		t.Fatalf("读取群失败: %v", err)
	}
	if group.GroupTags != "夜班,重点" {
		t.Fatalf("删除字典项不应修改群标签: %+v", group)
	}
}

func TestAdminGroupDictionariesDenyCrossOriginWrites(t *testing.T) {
	database := newAdminDictionaryDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	for _, tc := range []struct {
		name   string
		method string
		body   string
	}{
		{name: "post", method: http.MethodPost, body: `{"kind":"group_type","name":"业务"}`},
		{name: "delete", method: http.MethodDelete, body: `{"kind":"group_tag","name":"夜班"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/admin/api/group-dictionaries", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "https://evil.example")
			req.Host = "admin.example"
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
			resp := httptest.NewRecorder()
			server.handler().ServeHTTP(resp, req)
			if resp.Code != http.StatusForbidden {
				t.Fatalf("跨站字典写操作应返回 403: got=%d body=%s", resp.Code, resp.Body.String())
			}
			if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
				t.Fatalf("跨站字典写操作应返回 JSON: %q", got)
			}
		})
	}
}

func newAdminDictionaryDatabase(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-dictionary-api.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	seed := []struct {
		chatID    int64
		title     string
		groupType string
		tags      string
	}{
		{chatID: -100811, title: "夜班业务群", groupType: "业务", tags: "夜班,重点"},
		{chatID: -100812, title: "白班业务群", groupType: "业务", tags: "白班"},
		{chatID: -100813, title: "测试群", groupType: "测试", tags: "测试"},
	}
	for _, item := range seed {
		if _, err := database.EnsureGroup(item.chatID, item.title); err != nil {
			t.Fatalf("创建群失败: %v", err)
		}
		if err := database.UpdateGroupFields(item.chatID, map[string]any{
			"group_type": item.groupType,
			"group_tags": item.tags,
		}); err != nil {
			t.Fatalf("更新群失败: %v", err)
		}
	}
	return database
}

func getAdminDictionariesAPI(t *testing.T, server *Server, cookies []*http.Cookie) adminDictionaryEnvelopeForTest {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/group-dictionaries", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("字典列表应返回 200: got=%d body=%s", resp.Code, resp.Body.String())
	}
	var out adminDictionaryEnvelopeForTest
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("字典列表应返回 JSON: %v body=%s", err, resp.Body.String())
	}
	if !out.OK {
		t.Fatalf("字典列表应 ok=true: %+v", out)
	}
	return out
}

func postAdminDictionaryAPI(t *testing.T, server *Server, cookies []*http.Cookie, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/api/group-dictionaries", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("保存字典应成功: got=%d body=%s", resp.Code, resp.Body.String())
	}
}

func deleteAdminDictionaryAPI(t *testing.T, server *Server, cookies []*http.Cookie, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/group-dictionaries", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("删除字典应成功: got=%d body=%s", resp.Code, resp.Body.String())
	}
}

func findAdminDictionaryItem(items []adminDictionaryItemForTest, name string) *adminDictionaryItemForTest {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}
