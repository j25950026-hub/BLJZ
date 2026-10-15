package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-bot/internal/db"
)

type adminGroupsEnvelopeForTest struct {
	OK   bool `json:"ok"`
	Data struct {
		Groups []struct {
			ChatID             int64  `json:"chatId"`
			Title              string `json:"title"`
			GroupType          string `json:"groupType"`
			GroupTags          string `json:"groupTags"`
			BookkeepingEnabled bool   `json:"bookkeepingEnabled"`
			AutoReplyEnabled   bool   `json:"autoReplyEnabled"`
			WelcomeEnabled     bool   `json:"welcomeEnabled"`
			HealthStatus       string `json:"healthStatus"`
		} `json:"groups"`
		Total int `json:"total"`
		Views []struct {
			Name    string            `json:"name"`
			Filters map[string]string `json:"filters"`
		} `json:"views"`
	} `json:"data"`
}

type adminBatchPreviewEnvelopeForTest struct {
	OK   bool `json:"ok"`
	Data struct {
		MatchedCount int `json:"matchedCount"`
		Affected     []struct {
			ChatID int64  `json:"chatId"`
			Title  string `json:"title"`
			Before string `json:"before"`
			After  string `json:"after"`
		} `json:"affected"`
		Skipped []struct {
			ChatID int64  `json:"chatId"`
			Reason string `json:"reason"`
		} `json:"skipped"`
		ApplyToken string `json:"applyToken"`
	} `json:"data"`
}

func TestAdminGroupWorkbenchFiltersAndSavesViews(t *testing.T) {
	database := newAdminGroupWorkbenchDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	groups := getAdminGroupsAPI(t, server, cookies, "/admin/api/groups?tag=夜班&type=业务&status=enabled&q=重点")
	if groups.Data.Total != 1 || len(groups.Data.Groups) != 1 {
		t.Fatalf("筛选后应只返回一个群: %+v", groups.Data)
	}
	got := groups.Data.Groups[0]
	if got.Title != "重点夜班群" || got.GroupType != "业务" || got.GroupTags != "夜班,重点" || !got.BookkeepingEnabled || !got.WelcomeEnabled {
		t.Fatalf("筛选群数据错误: %+v", got)
	}

	payload := []byte(`{"name":"夜班业务群","filters":{"tag":"夜班","type":"业务","status":"enabled","q":"重点"}}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/group-views", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("保存常用视图应成功: got=%d body=%s", resp.Code, resp.Body.String())
	}

	after := getAdminGroupsAPI(t, server, cookies, "/admin/api/groups")
	if len(after.Data.Views) != 1 || after.Data.Views[0].Name != "夜班业务群" || after.Data.Views[0].Filters["tag"] != "夜班" {
		t.Fatalf("常用视图应随群列表返回: %+v", after.Data.Views)
	}
}

func TestAdminGroupBatchPreviewExplainsImpactWithoutWrite(t *testing.T) {
	database := newAdminGroupWorkbenchDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	payload := []byte(`{"filters":{"tag":"夜班"},"action":{"field":"group_type","value":"VIP业务"}}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups/batch-preview", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("批量预演应成功: got=%d body=%s", resp.Code, resp.Body.String())
	}

	var preview adminBatchPreviewEnvelopeForTest
	if err := json.Unmarshal(resp.Body.Bytes(), &preview); err != nil {
		t.Fatalf("预演应返回 JSON: %v body=%s", err, resp.Body.String())
	}
	if !preview.OK || preview.Data.MatchedCount != 2 || len(preview.Data.Affected) != 1 || len(preview.Data.Skipped) != 1 {
		t.Fatalf("预演结果应说明影响和跳过: %+v", preview)
	}
	if preview.Data.Affected[0].ChatID != -100701 || preview.Data.Affected[0].Before != "业务" || preview.Data.Affected[0].After != "VIP业务" {
		t.Fatalf("预演 affected 明细错误: %+v", preview.Data.Affected)
	}
	if preview.Data.Skipped[0].ChatID != -100702 || !strings.Contains(preview.Data.Skipped[0].Reason, "无需变更") {
		t.Fatalf("预演 skipped 明细错误: %+v", preview.Data.Skipped)
	}
	if preview.Data.ApplyToken == "" {
		t.Fatalf("预演应返回 applyToken 供二次确认")
	}

	group, err := database.GetGroup(-100701)
	if err != nil {
		t.Fatalf("读取群失败: %v", err)
	}
	if group.GroupType != "业务" {
		t.Fatalf("批量预演不应写库: %+v", group)
	}
}

func TestAdminGroupBatchApplyReturnsReceipt(t *testing.T) {
	database := newAdminGroupWorkbenchDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	previewPayload := []byte(`{"filters":{"tag":"夜班"},"action":{"field":"group_type","value":"VIP业务"}}`)
	previewReq := httptest.NewRequest(http.MethodPost, "/admin/api/groups/batch-preview", bytes.NewReader(previewPayload))
	previewReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		previewReq.AddCookie(cookie)
	}
	previewResp := httptest.NewRecorder()
	server.handler().ServeHTTP(previewResp, previewReq)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("批量预演应成功: got=%d body=%s", previewResp.Code, previewResp.Body.String())
	}
	var preview adminBatchPreviewEnvelopeForTest
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatalf("解析预演失败: %v", err)
	}

	applyPayload := []byte(`{"applyToken":"` + preview.Data.ApplyToken + `"}`)
	applyReq := httptest.NewRequest(http.MethodPost, "/admin/api/groups/batch-apply", bytes.NewReader(applyPayload))
	applyReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		applyReq.AddCookie(cookie)
	}
	applyResp := httptest.NewRecorder()
	server.handler().ServeHTTP(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("批量执行应成功: got=%d body=%s", applyResp.Code, applyResp.Body.String())
	}
	body := applyResp.Body.String()
	for _, want := range []string{`"matchedCount":2`, `"updatedCount":1`, `"skippedCount":1`, `"failedCount":0`, `"ok":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("批量回执缺少 %q: %s", want, body)
		}
	}

	group, err := database.GetGroup(-100701)
	if err != nil {
		t.Fatalf("读取群失败: %v", err)
	}
	if group.GroupType != "VIP业务" {
		t.Fatalf("批量执行应写入目标群: %+v", group)
	}
}

func TestAdminGroupBatchPreviewAndApplySupportsSelectedChatIDs(t *testing.T) {
	database := newAdminGroupWorkbenchDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	previewPayload := []byte(`{"filters":{"tag":"夜班"},"chatIds":[-100703],"action":{"field":"group_type","value":"核心业务"}}`)
	previewReq := httptest.NewRequest(http.MethodPost, "/admin/api/groups/batch-preview", bytes.NewReader(previewPayload))
	previewReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		previewReq.AddCookie(cookie)
	}
	previewResp := httptest.NewRecorder()
	server.handler().ServeHTTP(previewResp, previewReq)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("按选中群预演应成功: got=%d body=%s", previewResp.Code, previewResp.Body.String())
	}
	var preview adminBatchPreviewEnvelopeForTest
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatalf("解析预演失败: %v", err)
	}
	if preview.Data.MatchedCount != 1 || len(preview.Data.Affected) != 1 || preview.Data.Affected[0].ChatID != -100703 {
		t.Fatalf("预演应只影响显式选中的群: %+v", preview.Data)
	}

	applyPayload := []byte(`{"applyToken":"` + preview.Data.ApplyToken + `"}`)
	applyReq := httptest.NewRequest(http.MethodPost, "/admin/api/groups/batch-apply", bytes.NewReader(applyPayload))
	applyReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		applyReq.AddCookie(cookie)
	}
	applyResp := httptest.NewRecorder()
	server.handler().ServeHTTP(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("按选中群执行应成功: got=%d body=%s", applyResp.Code, applyResp.Body.String())
	}

	selected, err := database.GetGroup(-100703)
	if err != nil {
		t.Fatalf("读取选中群失败: %v", err)
	}
	if selected.GroupType != "核心业务" {
		t.Fatalf("选中群应被更新: %+v", selected)
	}
	other, err := database.GetGroup(-100701)
	if err != nil {
		t.Fatalf("读取非选中群失败: %v", err)
	}
	if other.GroupType != "业务" {
		t.Fatalf("非选中群不应被更新: %+v", other)
	}
}

func TestAdminGroupBatchApplyCanDisableWelcomeForMatchedGroups(t *testing.T) {
	database := newAdminGroupWorkbenchDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	previewPayload := []byte(`{"filters":{"tag":"夜班"},"action":{"field":"welcome_enabled","value":"0"}}`)
	previewReq := httptest.NewRequest(http.MethodPost, "/admin/api/groups/batch-preview", bytes.NewReader(previewPayload))
	previewReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		previewReq.AddCookie(cookie)
	}
	previewResp := httptest.NewRecorder()
	server.handler().ServeHTTP(previewResp, previewReq)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("批量关闭欢迎预演应成功: got=%d body=%s", previewResp.Code, previewResp.Body.String())
	}
	var preview adminBatchPreviewEnvelopeForTest
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatalf("解析预演失败: %v body=%s", err, previewResp.Body.String())
	}
	if preview.Data.MatchedCount != 2 || len(preview.Data.Affected) != 2 {
		t.Fatalf("夜班群欢迎关闭预演应影响 2 个群: %+v", preview.Data)
	}

	applyPayload := []byte(`{"applyToken":"` + preview.Data.ApplyToken + `"}`)
	applyReq := httptest.NewRequest(http.MethodPost, "/admin/api/groups/batch-apply", bytes.NewReader(applyPayload))
	applyReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		applyReq.AddCookie(cookie)
	}
	applyResp := httptest.NewRecorder()
	server.handler().ServeHTTP(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("批量关闭欢迎执行应成功: got=%d body=%s", applyResp.Code, applyResp.Body.String())
	}

	for _, chatID := range []int64{-100701, -100702} {
		group, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取群失败: %v", err)
		}
		if group.WelcomeEnabled != 0 {
			t.Fatalf("匹配群应关闭进群欢迎 chat_id=%d group=%+v", chatID, group)
		}
	}
	other, err := database.GetGroup(-100703)
	if err != nil {
		t.Fatalf("读取非匹配群失败: %v", err)
	}
	if other.WelcomeEnabled != 0 {
		t.Fatalf("非匹配群原本关闭，仍应保持关闭: %+v", other)
	}
}

func newAdminGroupWorkbenchDatabase(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-groups-api.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	seed := []struct {
		chatID      int64
		title       string
		groupType   string
		tags        string
		bookkeeping int
		autoReply   int
		welcome     int
	}{
		{chatID: -100701, title: "重点夜班群", groupType: "业务", tags: "夜班,重点", bookkeeping: 1, autoReply: 1, welcome: 1},
		{chatID: -100702, title: "夜班已配置群", groupType: "VIP业务", tags: "夜班,重点", bookkeeping: 1, autoReply: 1, welcome: 1},
		{chatID: -100703, title: "白班测试群", groupType: "测试", tags: "白班", bookkeeping: 0, autoReply: 0, welcome: 0},
	}
	for _, item := range seed {
		if _, err := database.EnsureGroup(item.chatID, item.title); err != nil {
			t.Fatalf("创建群失败: %v", err)
		}
		if err := database.UpdateGroupFields(item.chatID, map[string]any{
			"group_type":          item.groupType,
			"group_tags":          item.tags,
			"bookkeeping_enabled": item.bookkeeping,
			"auto_reply_enabled":  item.autoReply,
			"welcome_enabled":     item.welcome,
			"owner_user_id":       90001,
			"payout_address":      "TXworkbench",
			"welcome_text":        "欢迎加入【群名】",
		}); err != nil {
			t.Fatalf("更新群失败: %v", err)
		}
	}
	return database
}

func loginAdminForAPITest(t *testing.T, server *Server) []*http.Cookie {
	t.Helper()
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}
	return loginResp.Result().Cookies()
}

func getAdminGroupsAPI(t *testing.T, server *Server, cookies []*http.Cookie, rawURL string) adminGroupsEnvelopeForTest {
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
	var out adminGroupsEnvelopeForTest
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("群工作台 API 应返回 JSON: %v body=%s", err, resp.Body.String())
	}
	return out
}
