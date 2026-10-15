package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/internal/db"
)

func TestAdminTemplateLibraryCanSaveApplyAndVersion(t *testing.T) {
	database := newAdminTemplatesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		resp := httptest.NewRecorder()
		server.handler().ServeHTTP(resp, req)
		return resp
	}
	first := post("/admin/api/template-library", `{"kind":"welcome","name":"日常欢迎","description":"首版","content":{"template":"欢迎加入 {group_name}"}}`)
	if first.Code != http.StatusOK {
		t.Fatalf("保存模板版本 1 失败: got=%d body=%s", first.Code, first.Body.String())
	}
	var firstPayload struct {
		Data struct {
			ID      int64 `json:"id"`
			Version int   `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstPayload); err != nil || firstPayload.Data.ID == 0 || firstPayload.Data.Version != 1 {
		t.Fatalf("模板版本 1 回执错误: err=%v body=%s", err, first.Body.String())
	}
	second := post("/admin/api/template-library", `{"kind":"welcome","name":"日常欢迎","description":"第二版","content":{"template":"欢迎加入本群 {group_name}"}}`)
	var secondPayload struct {
		Data struct {
			ID      int64 `json:"id"`
			Version int   `json:"version"`
		} `json:"data"`
	}
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &secondPayload) != nil || secondPayload.Data.Version != 2 || secondPayload.Data.ID == firstPayload.Data.ID {
		t.Fatalf("模板版本 2 回执错误: got=%d body=%s", second.Code, second.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/admin/api/template-library?kind=welcome", nil)
	for _, cookie := range cookies {
		get.AddCookie(cookie)
	}
	getResp := httptest.NewRecorder()
	server.handler().ServeHTTP(getResp, get)
	if getResp.Code != http.StatusOK || !bytes.Contains(getResp.Body.Bytes(), []byte(`"version":2`)) || !bytes.Contains(getResp.Body.Bytes(), []byte(`"version":1`)) {
		t.Fatalf("模板版本列表错误: got=%d body=%s", getResp.Code, getResp.Body.String())
	}

	before, err := database.GetGroup(-101001)
	if err != nil {
		t.Fatalf("读取预览前群配置失败: %v", err)
	}
	preview := post("/admin/api/template-library/preview", `{"templateId":`+itoa(secondPayload.Data.ID)+`,"chatId":-101001,"sample":{"userId":7788,"firstName":"小明"}}`)
	if preview.Code != http.StatusOK || !bytes.Contains(preview.Body.Bytes(), []byte("欢迎加入本群")) {
		t.Fatalf("模板应用预览失败: got=%d body=%s", preview.Code, preview.Body.String())
	}
	after, err := database.GetGroup(-101001)
	if err != nil {
		t.Fatalf("读取预览后群配置失败: %v", err)
	}
	if before.WelcomeText != after.WelcomeText || before.WelcomeEnabled != after.WelcomeEnabled {
		t.Fatalf("模板预览不应写入目标群: before=%+v after=%+v", before, after)
	}
}

func TestAdminPermissionRiskRadarHighlightsCoverageGaps(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-risk-radar.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	for _, item := range []struct {
		chatID int64
		title  string
	}{
		{chatID: -100701, title: "有操作人群"},
		{chatID: -100702, title: "无操作人群"},
	} {
		if _, err := database.EnsureGroup(item.chatID, item.title); err != nil {
			t.Fatalf("创建风险测试群失败: %v", err)
		}
	}
	if err := database.AddOperator(-100701, 70001, "operator", false); err != nil {
		t.Fatalf("设置群操作人失败: %v", err)
	}
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/risk-radar", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("读取权限风险雷达失败: got=%d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []struct {
				Code       string `json:"code"`
				Severity   string `json:"severity"`
				ChatID     int64  `json:"chatId"`
				Visibility string `json:"visibility"`
				ActionURL  string `json:"actionUrl"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析权限风险雷达失败: %v body=%s", err, resp.Body.String())
	}
	if !payload.OK || len(payload.Data.Items) < 2 {
		t.Fatalf("权限风险雷达应返回覆盖缺口: %+v", payload)
	}
	var missingGlobal, missingGroup bool
	for _, item := range payload.Data.Items {
		if item.Visibility != "admin_only" || item.Severity == "" {
			t.Fatalf("风险项应只对后台可见且有等级: %+v", item)
		}
		if item.Code == "no_global_owner" {
			missingGlobal = true
		}
		if item.Code == "group_missing_operator" && item.ChatID == -100702 && strings.Contains(item.ActionURL, "/admin/group?chat_id=-100702") {
			missingGroup = true
		}
	}
	if !missingGlobal || !missingGroup {
		t.Fatalf("风险项缺少全局权限人或群操作人缺口: %+v", payload.Data.Items)
	}
}

func itoa(value int64) string {
	return fmt.Sprintf("%d", value)
}

func TestAdminSnapshotsRecordBeforeBulkChanges(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	preview := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/preview", `{"feature":"welcome","enabled":false}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("开关预演失败: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var envelope adminSwitchesEnvelopeForTest
	if err := json.Unmarshal(preview.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析开关预演失败: %v", err)
	}
	apply := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/apply", `{"applyToken":"`+envelope.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK {
		t.Fatalf("开关确认失败: got=%d body=%s", apply.Code, apply.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/snapshots?limit=10", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("读取快照 API 失败: got=%d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []struct {
				OwnerUsername string `json:"ownerUsername"`
				Action        string `json:"action"`
				Items         []struct {
					ChatID int64  `json:"chatId"`
					Status string `json:"status"`
				} `json:"items"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析快照 API 失败: %v body=%s", err, resp.Body.String())
	}
	if !payload.OK || len(payload.Data.Items) == 0 {
		t.Fatalf("快照 API 应返回已执行的变更: %+v", payload)
	}
	latest := payload.Data.Items[0]
	if latest.OwnerUsername != "root" || latest.Action != "admin_global_switch_apply" || len(latest.Items) != 2 {
		t.Fatalf("快照元数据或明细错误: %+v", latest)
	}
	for _, item := range latest.Items {
		if item.Status != "updated" || (item.ChatID != -100801 && item.ChatID != -100802) {
			t.Fatalf("快照群明细错误: %+v", item)
		}
	}
}
