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

type adminSwitchesEnvelopeForTest struct {
	OK   bool `json:"ok"`
	Data struct {
		MatchedCount int `json:"matchedCount"`
		Affected     []struct {
			ChatID int64  `json:"chatId"`
			Before string `json:"before"`
			After  string `json:"after"`
		} `json:"affected"`
		Skipped []struct {
			ChatID int64  `json:"chatId"`
			Reason string `json:"reason"`
		} `json:"skipped"`
		ApplyToken    string `json:"applyToken"`
		RollbackToken string `json:"rollbackToken"`
		UpdatedCount  int    `json:"updatedCount"`
		SkippedCount  int    `json:"skippedCount"`
		FailedCount   int    `json:"failedCount"`
	} `json:"data"`
}

func TestAdminGlobalSwitchesCanPauseAndResumeFleetWideFeatures(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	preview := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/preview", `{"feature":"welcome","enabled":false}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("全局暂停预演应成功: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewEnvelope adminSwitchesEnvelopeForTest
	if err := json.Unmarshal(preview.Body.Bytes(), &previewEnvelope); err != nil {
		t.Fatalf("解析暂停预演失败: %v", err)
	}
	if !previewEnvelope.OK || previewEnvelope.Data.MatchedCount != 2 || len(previewEnvelope.Data.Affected) != 2 || previewEnvelope.Data.ApplyToken == "" {
		t.Fatalf("暂停预演应覆盖全部群并返回确认令牌: %+v", previewEnvelope)
	}

	apply := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/apply", `{"applyToken":"`+previewEnvelope.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK {
		t.Fatalf("全局暂停执行应成功: got=%d body=%s", apply.Code, apply.Body.String())
	}
	var receipt adminSwitchesEnvelopeForTest
	if err := json.Unmarshal(apply.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("解析暂停回执失败: %v", err)
	}
	if receipt.Data.UpdatedCount != 2 || receipt.Data.FailedCount != 0 || receipt.Data.RollbackToken == "" {
		t.Fatalf("暂停回执应包含更新数和恢复令牌: %+v", receipt)
	}

	for _, chatID := range []int64{-100801, -100802} {
		group, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取群 %d 失败: %v", chatID, err)
		}
		if group.WelcomeEnabled != 0 {
			t.Fatalf("群 %d 欢迎开关应已暂停: %+v", chatID, group)
		}
	}

	rollback := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/rollback", `{"rollbackToken":"`+receipt.Data.RollbackToken+`"}`)
	if rollback.Code != http.StatusOK {
		t.Fatalf("全局恢复执行应成功: got=%d body=%s", rollback.Code, rollback.Body.String())
	}
	for _, chatID := range []int64{-100801, -100802} {
		group, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("恢复后读取群 %d 失败: %v", chatID, err)
		}
		if group.WelcomeEnabled != 1 {
			t.Fatalf("群 %d 欢迎开关应恢复: %+v", chatID, group)
		}
	}
}

func TestAdminGlobalSwitchesCanApplyCommonValuesAcrossSelectedGroups(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	preview := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/preview", `{"feature":"closing_reminder_time","value":"23:30","chatIds":[-100801]}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("批量改下课时间预演应成功: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var envelope adminSwitchesEnvelopeForTest
	if err := json.Unmarshal(preview.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析批量改值预演失败: %v", err)
	}
	if envelope.Data.MatchedCount != 1 || len(envelope.Data.Affected) != 1 || envelope.Data.Affected[0].Before != "04:00" || envelope.Data.Affected[0].After != "23:30" {
		t.Fatalf("批量改值影响范围错误: %+v", envelope.Data)
	}

	apply := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/apply", `{"applyToken":"`+envelope.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK {
		t.Fatalf("批量改值执行应成功: got=%d body=%s", apply.Code, apply.Body.String())
	}
	group, err := database.GetClosingReminderConfig(-100801)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败: %v", err)
	}
	if group.ReminderTime != "23:30" {
		t.Fatalf("批量改值未更新下课时间: %+v", group)
	}
	untouched, err := database.GetClosingReminderConfig(-100802)
	if err != nil {
		t.Fatalf("读取未选群配置失败: %v", err)
	}
	if untouched.ReminderTime != "04:00" {
		t.Fatalf("未选群不应被批量改值影响: %+v", untouched)
	}
}

func TestAdminLedgerCardHideReversedSwitchCanApplyAndRollback(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	preview := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/preview", `{"feature":"ledger_card_hide_reversed","enabled":false,"chatIds":[-100801]}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("账单卡片撤回隐藏开关预演应成功: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewEnvelope adminSwitchesEnvelopeForTest
	if err := json.Unmarshal(preview.Body.Bytes(), &previewEnvelope); err != nil {
		t.Fatalf("解析账单卡片撤回隐藏预演失败: %v", err)
	}
	if previewEnvelope.Data.MatchedCount != 1 || len(previewEnvelope.Data.Affected) != 1 || previewEnvelope.Data.Affected[0].Before != "1" || previewEnvelope.Data.Affected[0].After != "0" {
		t.Fatalf("账单卡片撤回隐藏预演前后值异常: %+v", previewEnvelope.Data)
	}

	apply := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/apply", `{"applyToken":"`+previewEnvelope.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK {
		t.Fatalf("账单卡片撤回隐藏开关执行应成功: got=%d body=%s", apply.Code, apply.Body.String())
	}
	group, err := database.GetGroup(-100801)
	if err != nil {
		t.Fatalf("读取关闭后的账单卡片开关失败: %v", err)
	}
	if group.LedgerCardHideReversed != 0 {
		t.Fatalf("账单卡片撤回隐藏开关应关闭: %+v", group)
	}

	var receipt adminSwitchesEnvelopeForTest
	if err := json.Unmarshal(apply.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("解析账单卡片撤回隐藏回执失败: %v", err)
	}
	if receipt.Data.RollbackToken == "" {
		t.Fatalf("账单卡片撤回隐藏回执应返回恢复令牌: %+v", receipt.Data)
	}
	rollback := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/rollback", `{"rollbackToken":"`+receipt.Data.RollbackToken+`"}`)
	if rollback.Code != http.StatusOK {
		t.Fatalf("账单卡片撤回隐藏恢复应成功: got=%d body=%s", rollback.Code, rollback.Body.String())
	}
	group, err = database.GetGroup(-100801)
	if err != nil {
		t.Fatalf("读取恢复后的账单卡片开关失败: %v", err)
	}
	if group.LedgerCardHideReversed != 1 {
		t.Fatalf("账单卡片撤回隐藏开关应恢复开启: %+v", group)
	}
}

func TestAdminStaticAppIncludesGlobalSwitchCenter(t *testing.T) {
	server := newTestSessionAdminServer(t)
	cookies := loginAdminForAPITest(t, server)
	shellReq := httptest.NewRequest(http.MethodGet, "/admin/switches", nil)
	for _, cookie := range cookies {
		shellReq.AddCookie(cookie)
	}
	shellResp := httptest.NewRecorder()
	server.handler().ServeHTTP(shellResp, shellReq)
	if shellResp.Code != http.StatusOK || !strings.Contains(shellResp.Body.String(), `<div id="app"`) {
		t.Fatalf("登录后开关中心路由应返回前端壳: got=%d body=%s", shellResp.Code, shellResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("读取后台前端资源失败: got=%d body=%s", resp.Code, resp.Body.String())
	}
	for _, want := range []string{
		"/admin/switches",
		"/admin/api/switches",
		"/admin/api/switches/",
		"/admin/api/switches/preview",
		"/admin/api/switches/apply",
		"/admin/api/switches/rollback",
		"ledger_card_hide_reversed",
		"撤回记录从账单卡片隐藏",
		"renderSwitchCenter",
		"switch-summary-card",
		"switch-detail-filter",
		"loadSwitchFeature",
		"全局功能开关中心",
		"操作前预演",
		"恢复上次变更",
		"未保存",
	} {
		if !strings.Contains(resp.Body.String(), want) {
			t.Fatalf("全局开关前端缺少 %q", want)
		}
	}
}

func TestAdminSwitchesRejectInvalidValuesWithoutWrite(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	resp := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/preview", `{"feature":"closing_reminder_time","value":"25:99"}`)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "HH:MM") {
		t.Fatalf("非法下课时间应返回可解释的 400: got=%d body=%s", resp.Code, resp.Body.String())
	}
	config, err := database.GetClosingReminderConfig(-100801)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败: %v", err)
	}
	if config.ReminderTime != "04:00" {
		t.Fatalf("非法预演不应修改下课时间: %+v", config)
	}
}

func TestAdminSwitchApplySkipsConcurrentChanges(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	preview := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/preview", `{"feature":"closing_reminder_time","value":"23:30"}`)
	var previewEnvelope adminSwitchesEnvelopeForTest
	if err := json.Unmarshal(preview.Body.Bytes(), &previewEnvelope); err != nil {
		t.Fatalf("解析预演失败: %v", err)
	}
	concurrent, err := database.GetClosingReminderConfig(-100801)
	if err != nil {
		t.Fatalf("读取并发变更前配置失败: %v", err)
	}
	concurrent.ReminderTime = "22:00"
	if err := database.UpsertClosingReminderConfig(concurrent); err != nil {
		t.Fatalf("模拟并发配置变更失败: %v", err)
	}
	apply := postAdminSwitchesJSON(t, server, cookies, "/admin/api/switches/apply", `{"applyToken":"`+previewEnvelope.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK {
		t.Fatalf("批量执行应返回结构化回执: got=%d body=%s", apply.Code, apply.Body.String())
	}
	var receipt adminSwitchesEnvelopeForTest
	if err := json.Unmarshal(apply.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("解析并发回执失败: %v", err)
	}
	if receipt.Data.FailedCount != 1 || receipt.Data.UpdatedCount != 1 {
		t.Fatalf("并发变更群应跳过，其余群仍应更新: %+v", receipt.Data)
	}
}

func TestAdminSwitchFeatureDetailReturnsConcreteGroups(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/switches/welcome?status=enabled&q=开关测试群&page=1&pageSize=1", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("开关详情 API 应成功: got=%d body=%s", resp.Code, resp.Body.String())
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Feature       string `json:"feature"`
			Total         int    `json:"total"`
			EnabledCount  int    `json:"enabledCount"`
			DisabledCount int    `json:"disabledCount"`
			Groups        []struct {
				ChatID  int64  `json:"chatId"`
				Title   string `json:"title"`
				Enabled bool   `json:"enabled"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析开关详情响应失败: %v", err)
	}
	if !envelope.OK || envelope.Data.Feature != "welcome" || envelope.Data.Total != 2 || envelope.Data.EnabledCount != 2 || envelope.Data.DisabledCount != 0 || len(envelope.Data.Groups) != 1 || !envelope.Data.Groups[0].Enabled {
		t.Fatalf("开关详情应返回具体群和分页状态: %+v", envelope)
	}

	bad := httptest.NewRequest(http.MethodGet, "/admin/api/switches/unknown", nil)
	for _, cookie := range cookies {
		bad.AddCookie(cookie)
	}
	badResp := httptest.NewRecorder()
	server.handler().ServeHTTP(badResp, bad)
	if badResp.Code != http.StatusBadRequest || !strings.Contains(badResp.Body.String(), "不支持的全局功能") {
		t.Fatalf("未知功能应返回可解释的 400: got=%d body=%s", badResp.Code, badResp.Body.String())
	}
}

func TestAdminSwitchPlanCannotBeConsumedByAnotherSession(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	primaryCookies := loginAdminForAPITest(t, server)
	secondaryCookies := loginAdminForAPITest(t, server)

	preview := postAdminSwitchesJSON(t, server, primaryCookies, "/admin/api/switches/preview", `{"feature":"welcome","enabled":false,"chatIds":[-100801]}`)
	var previewEnvelope adminSwitchesEnvelopeForTest
	if err := json.Unmarshal(preview.Body.Bytes(), &previewEnvelope); err != nil {
		t.Fatalf("解析预演失败: %v", err)
	}
	apply := postAdminSwitchesJSON(t, server, secondaryCookies, "/admin/api/switches/apply", `{"applyToken":"`+previewEnvelope.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusBadRequest {
		t.Fatalf("其他 session 消费预演令牌应被拒绝: got=%d body=%s", apply.Code, apply.Body.String())
	}
	group, err := database.GetGroup(-100801)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.WelcomeEnabled != 1 {
		t.Fatalf("跨 session 消费失败时不应写库: %+v", group)
	}
}

func postAdminSwitchesJSON(t *testing.T, server *Server, cookies []*http.Cookie, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	return resp
}

func newAdminSwitchesTestDatabase(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-switches.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, chatID := range []int64{-100801, -100802} {
		if _, err := database.EnsureGroup(chatID, fmt.Sprintf("开关测试群 %d", chatID)); err != nil {
			t.Fatalf("创建测试群 %d 失败: %v", chatID, err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{
			"welcome_enabled":     1,
			"auto_reply_enabled":  1,
			"bookkeeping_enabled": 1,
		}); err != nil {
			t.Fatalf("设置测试群 %d 开关失败: %v", chatID, err)
		}
		if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
			ChatID:       chatID,
			Enabled:      1,
			ReminderTime: "04:00",
			ThresholdU:   1.5,
			DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
		}); err != nil {
			t.Fatalf("设置测试群 %d 下课提醒失败: %v", chatID, err)
		}
	}
	return database
}
