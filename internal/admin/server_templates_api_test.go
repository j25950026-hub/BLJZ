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

type adminTemplatesEnvelopeForTest struct {
	OK   bool `json:"ok"`
	Data struct {
		Welcome struct {
			Enabled  bool   `json:"enabled"`
			Template string `json:"template"`
		} `json:"welcome"`
		Variables []struct {
			Token string `json:"token"`
			Label string `json:"label"`
		} `json:"variables"`
		AutoReply struct {
			Enabled bool `json:"enabled"`
			Rules   []struct {
				ID        int64  `json:"id"`
				Keyword   string `json:"keyword"`
				Content   string `json:"content"`
				Enabled   bool   `json:"enabled"`
				CreatedAt string `json:"createdAt"`
			} `json:"rules"`
		} `json:"autoReply"`
		ClosingReminder struct {
			Enabled        bool    `json:"enabled"`
			ReminderTime   string  `json:"reminderTime"`
			ThresholdU     float64 `json:"thresholdU"`
			DMMode         string  `json:"dmMode"`
			DayCutTime     string  `json:"dayCutTime"`
			EffectiveRunAt string  `json:"effectiveRunAt"`
			Texts          struct {
				Normal  string `json:"normal"`
				Pending string `json:"pending"`
				NoData  string `json:"noData"`
				Review  string `json:"review"`
				Failed  string `json:"failed"`
			} `json:"texts"`
		} `json:"closingReminder"`
		Affected []struct {
			ChatID               int64    `json:"chatId"`
			ChangedFields        []string `json:"changedFields"`
			DayCutTime           string   `json:"dayCutTime"`
			BeforeEffectiveRunAt string   `json:"beforeEffectiveRunAt"`
			AfterEffectiveRunAt  string   `json:"afterEffectiveRunAt"`
		} `json:"affected"`
		Failed []struct {
			ChatID int64  `json:"chatId"`
			Reason string `json:"reason"`
		} `json:"failed"`
		Preview struct {
			Text      string `json:"text"`
			ParseMode string `json:"parseMode"`
		} `json:"preview"`
		ApplyToken   string `json:"applyToken"`
		MatchedCount int    `json:"matchedCount"`
		UpdatedCount int    `json:"updatedCount"`
		FailedCount  int    `json:"failedCount"`
	} `json:"data"`
}

func TestAdminTemplatesCanBatchManageClosingReminders(t *testing.T) {
	t.Run("批量修改不影响日切和私聊模式", func(t *testing.T) {
		database := newAdminClosingTemplatesTestDatabase(t)
		server := newTestSessionAdminServer(t)
		server.db = database
		cookies := loginAdminForAPITest(t, server)

		stateResp := getAdminTemplates(t, server, cookies, "/admin/api/templates?chat_id=-101001")
		var state adminTemplatesEnvelopeForTest
		if err := json.Unmarshal(stateResp.Body.Bytes(), &state); err != nil {
			t.Fatalf("解析下课提醒状态失败: %v body=%s", err, stateResp.Body.String())
		}
		if state.Data.ClosingReminder.ReminderTime != "03:00" || state.Data.ClosingReminder.DayCutTime != "04:00:00" || state.Data.ClosingReminder.EffectiveRunAt != "04:00:00" {
			t.Fatalf("下课提醒状态或实际触发时间错误: %+v", state.Data.ClosingReminder)
		}

		preview := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/closing-reminders/preview", `{"chatIds":[-101001,-101002],"patch":{"enabled":true,"reminderTime":"02:30","thresholdU":2.5,"texts":{"normal":"今日工作已完成，辛苦大家。","pending":""}}}`)
		if preview.Code != http.StatusOK {
			t.Fatalf("下课提醒批量预演应成功: got=%d body=%s", preview.Code, preview.Body.String())
		}
		var previewEnvelope adminTemplatesEnvelopeForTest
		if err := json.Unmarshal(preview.Body.Bytes(), &previewEnvelope); err != nil {
			t.Fatalf("解析下课提醒批量预演失败: %v body=%s", err, preview.Body.String())
		}
		if previewEnvelope.Data.MatchedCount != 2 || len(previewEnvelope.Data.Affected) != 2 || previewEnvelope.Data.ApplyToken == "" {
			t.Fatalf("下课提醒批量预演影响范围错误: %+v", previewEnvelope.Data)
		}
		effectiveByChat := map[int64]string{}
		for _, item := range previewEnvelope.Data.Affected {
			effectiveByChat[item.ChatID] = item.AfterEffectiveRunAt
		}
		if effectiveByChat[-101001] != "04:00:00" || effectiveByChat[-101002] != "02:30:00" {
			t.Fatalf("下课提醒实际触发时间应取提醒时间与日切的较晚值: %+v", effectiveByChat)
		}
		assertAdminClosingConfig(t, database, -101001, false, "03:00", 1.5, db.ClosingReminderDMModeOwner, "旧正常文案", "旧待确认文案")
		assertAdminClosingConfig(t, database, -101002, true, "02:00", 1, db.ClosingReminderDMModeCustom, "二群旧文案", "二群待确认")

		apply := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/apply", `{"applyToken":"`+previewEnvelope.Data.ApplyToken+`"}`)
		if apply.Code != http.StatusOK {
			t.Fatalf("下课提醒批量确认失败: got=%d body=%s", apply.Code, apply.Body.String())
		}
		var receipt adminTemplatesEnvelopeForTest
		if err := json.Unmarshal(apply.Body.Bytes(), &receipt); err != nil || receipt.Data.UpdatedCount != 2 || receipt.Data.FailedCount != 0 {
			t.Fatalf("下课提醒批量回执错误: err=%v body=%s", err, apply.Body.String())
		}
		assertAdminClosingConfig(t, database, -101001, true, "02:30", 2.5, db.ClosingReminderDMModeOwner, "今日工作已完成，辛苦大家。", "")
		assertAdminClosingConfig(t, database, -101002, true, "02:30", 2.5, db.ClosingReminderDMModeCustom, "今日工作已完成，辛苦大家。", "")
		assertAdminClosingConfig(t, database, -101003, false, "23:00", 3, db.ClosingReminderDMModeOff, "三群旧文案", "三群待确认")
		for chatID, want := range map[int64]string{-101001: "04:00:00", -101002: "01:00:00", -101003: "06:00:00"} {
			group, err := database.GetGroup(chatID)
			if err != nil || group.DayCutTime != want {
				t.Fatalf("批量下课提醒不应修改日切时间: chat=%d got=%q err=%v", chatID, group.DayCutTime, err)
			}
		}
	})

	t.Run("预演后日切变化则拒绝覆盖", func(t *testing.T) {
		database := newAdminClosingTemplatesTestDatabase(t)
		server := newTestSessionAdminServer(t)
		server.db = database
		cookies := loginAdminForAPITest(t, server)

		preview := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/closing-reminders/preview", `{"chatIds":[-101001],"patch":{"reminderTime":"22:00"}}`)
		var envelope adminTemplatesEnvelopeForTest
		if err := json.Unmarshal(preview.Body.Bytes(), &envelope); err != nil || envelope.Data.ApplyToken == "" {
			t.Fatalf("解析日切并发预演失败: err=%v body=%s", err, preview.Body.String())
		}
		if err := database.UpdateGroupFields(-101001, map[string]any{"day_cut_time": "05:00:00"}); err != nil {
			t.Fatalf("模拟日切并发变化失败: %v", err)
		}
		apply := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/apply", `{"applyToken":"`+envelope.Data.ApplyToken+`"}`)
		var receipt adminTemplatesEnvelopeForTest
		if err := json.Unmarshal(apply.Body.Bytes(), &receipt); err != nil {
			t.Fatalf("解析日切并发回执失败: %v body=%s", err, apply.Body.String())
		}
		if apply.Code != http.StatusOK || receipt.Data.UpdatedCount != 0 || receipt.Data.FailedCount != 1 || len(receipt.Data.Failed) != 1 || !strings.Contains(receipt.Data.Failed[0].Reason, "日切时间已变化") {
			t.Fatalf("日切变化后应跳过过期预演: got=%d data=%+v body=%s", apply.Code, receipt.Data, apply.Body.String())
		}
		assertAdminClosingConfig(t, database, -101001, false, "03:00", 1.5, db.ClosingReminderDMModeOwner, "旧正常文案", "旧待确认文案")
	})

	t.Run("拒绝混入日切和不安全文案", func(t *testing.T) {
		database := newAdminClosingTemplatesTestDatabase(t)
		server := newTestSessionAdminServer(t)
		server.db = database
		cookies := loginAdminForAPITest(t, server)

		for _, body := range []string{
			`{"chatIds":[-101001],"patch":{"reminderTime":"25:99"}}`,
			`{"chatIds":[-101001],"patch":{"thresholdU":0}}`,
			`{"chatIds":[-101001],"patch":{"texts":{"normal":"今日金额 100 U 已完成"}}}`,
			`{"chatIds":[-101001],"patch":{"reminderTime":"22:00"},"dayCutTime":"09:00:00"}`,
		} {
			resp := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/closing-reminders/preview", body)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("非法下课提醒请求应返回 400: body=%s got=%d response=%s", body, resp.Code, resp.Body.String())
			}
		}
		assertAdminClosingConfig(t, database, -101001, false, "03:00", 1.5, db.ClosingReminderDMModeOwner, "旧正常文案", "旧待确认文案")
	})
}

func TestAdminTemplatesCanManageAutoRepliesAndPreview(t *testing.T) {
	database := newAdminTemplatesTestDatabase(t)
	if err := database.SetAutoReply(-101001, "到账", "[html]已收到 {{.Text}}"); err != nil {
		t.Fatalf("初始化关键词自动回复失败: %v", err)
	}
	if err := database.SetAutoReply(-101001, "", "默认回复"); err != nil {
		t.Fatalf("初始化兜底自动回复失败: %v", err)
	}
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	stateResp := getAdminTemplates(t, server, cookies, "/admin/api/templates?chat_id=-101001")
	var state adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(stateResp.Body.Bytes(), &state); err != nil {
		t.Fatalf("解析自动回复列表失败: %v body=%s", err, stateResp.Body.String())
	}
	if !state.Data.AutoReply.Enabled || len(state.Data.AutoReply.Rules) != 2 {
		t.Fatalf("自动回复列表应包含启用和兜底规则: %+v", state.Data.AutoReply)
	}
	var keywordRule, fallbackRule int64
	for _, rule := range state.Data.AutoReply.Rules {
		if rule.Keyword == "到账" {
			keywordRule = rule.ID
		}
		if rule.Keyword == "" {
			fallbackRule = rule.ID
		}
		if rule.ID == 0 || !rule.Enabled {
			t.Fatalf("自动回复规则应返回稳定 ID 且默认启用: %+v", rule)
		}
	}
	if keywordRule == 0 || fallbackRule == 0 {
		t.Fatalf("测试数据未返回关键词和兜底规则: %+v", state.Data.AutoReply.Rules)
	}

	render := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/auto-replies/render", `{"chatId":-101001,"content":"[html]<b>{{.Text}}</b>","sampleText":"到账"}`)
	var renderEnvelope adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(render.Body.Bytes(), &renderEnvelope); err != nil {
		t.Fatalf("解析自动回复实时预览失败: %v body=%s", err, render.Body.String())
	}
	if render.Code != http.StatusOK || renderEnvelope.Data.Preview.ParseMode != "HTML" || renderEnvelope.Data.Preview.Text != "<b>到账</b>" {
		t.Fatalf("自动回复实时预览错误: got=%d body=%s", render.Code, render.Body.String())
	}

	updatePreview := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/auto-replies/preview", fmt.Sprintf(`{"action":"update","chatId":-101001,"ruleId":%d,"keyword":"到账新标记","content":"已更新"}`, keywordRule))
	if updatePreview.Code != http.StatusOK {
		t.Fatalf("自动回复更新预演应成功: got=%d body=%s", updatePreview.Code, updatePreview.Body.String())
	}
	var updateEnvelope adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(updatePreview.Body.Bytes(), &updateEnvelope); err != nil || updateEnvelope.Data.ApplyToken == "" {
		t.Fatalf("自动回复更新预演令牌错误: err=%v body=%s", err, updatePreview.Body.String())
	}
	stateResp = getAdminTemplates(t, server, cookies, "/admin/api/templates?chat_id=-101001")
	if bytes.Contains(stateResp.Body.Bytes(), []byte("已更新")) {
		t.Fatalf("自动回复预演不应写入数据库: %s", stateResp.Body.String())
	}
	apply := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/apply", `{"applyToken":"`+updateEnvelope.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK || !bytes.Contains(apply.Body.Bytes(), []byte(`"updatedCount":1`)) {
		t.Fatalf("自动回复更新确认失败: got=%d body=%s", apply.Code, apply.Body.String())
	}

	togglePreview := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/auto-replies/preview", fmt.Sprintf(`{"action":"toggle","chatId":-101001,"ruleId":%d,"enabled":false}`, fallbackRule))
	if togglePreview.Code != http.StatusOK {
		t.Fatalf("自动回复单条停用预演失败: got=%d body=%s", togglePreview.Code, togglePreview.Body.String())
	}
	var toggleEnvelope adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(togglePreview.Body.Bytes(), &toggleEnvelope); err != nil {
		t.Fatalf("解析停用预演失败: %v", err)
	}
	if postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/apply", `{"applyToken":"`+toggleEnvelope.Data.ApplyToken+`"}`).Code != http.StatusOK {
		t.Fatalf("自动回复单条停用确认失败")
	}
	stateResp = getAdminTemplates(t, server, cookies, "/admin/api/templates?chat_id=-101001")
	if !bytes.Contains(stateResp.Body.Bytes(), []byte(`"keyword":"","content":"默认回复","enabled":false`)) {
		t.Fatalf("停用规则仍应在管理列表中且标记为关闭: %s", stateResp.Body.String())
	}
	runtimeRules, err := database.ListAutoReplies(-101001)
	if err != nil {
		t.Fatalf("读取 Bot 运行时自动回复失败: %v", err)
	}
	for _, rule := range runtimeRules {
		if rule.Keyword == "" {
			t.Fatalf("停用兜底规则不应还在 Bot 运行时列表中: %+v", runtimeRules)
		}
	}

	createPreview := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/auto-replies/preview", `{"action":"create","chatId":-101001,"keyword":"新关键词","content":"新回复"}`)
	if createPreview.Code != http.StatusOK {
		t.Fatalf("自动回复新增预演失败: got=%d body=%s", createPreview.Code, createPreview.Body.String())
	}
	var createEnvelope adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(createPreview.Body.Bytes(), &createEnvelope); err != nil {
		t.Fatalf("解析新增预演失败: %v", err)
	}
	createApply := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/apply", `{"applyToken":"`+createEnvelope.Data.ApplyToken+`"}`)
	if createApply.Code != http.StatusOK {
		t.Fatalf("自动回复新增确认失败: got=%d body=%s", createApply.Code, createApply.Body.String())
	}
	var createReceipt struct {
		Data struct {
			Rule struct {
				ID int64 `json:"id"`
			} `json:"rule"`
		} `json:"data"`
	}
	if err := json.Unmarshal(createApply.Body.Bytes(), &createReceipt); err != nil || createReceipt.Data.Rule.ID == 0 {
		t.Fatalf("新增回执应返回稳定规则 ID: err=%v body=%s", err, createApply.Body.String())
	}
	stateResp = getAdminTemplates(t, server, cookies, "/admin/api/templates?chat_id=-101001")
	if !bytes.Contains(stateResp.Body.Bytes(), []byte("新关键词")) {
		t.Fatalf("自动回复新增后列表缺少新规则: %s", stateResp.Body.String())
	}
	deletePreview := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/auto-replies/preview", fmt.Sprintf(`{"action":"delete","chatId":-101001,"ruleId":%d}`, createReceipt.Data.Rule.ID))
	if deletePreview.Code != http.StatusOK {
		t.Fatalf("自动回复删除预演失败: got=%d body=%s", deletePreview.Code, deletePreview.Body.String())
	}
	var deleteEnvelope adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(deletePreview.Body.Bytes(), &deleteEnvelope); err != nil || deleteEnvelope.Data.ApplyToken == "" {
		t.Fatalf("解析自动回复删除预演失败: err=%v body=%s", err, deletePreview.Body.String())
	}
	deleteApply := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/apply", `{"applyToken":"`+deleteEnvelope.Data.ApplyToken+`"}`)
	if deleteApply.Code != http.StatusOK {
		t.Fatalf("自动回复删除确认失败: got=%d body=%s", deleteApply.Code, deleteApply.Body.String())
	}
	stateResp = getAdminTemplates(t, server, cookies, "/admin/api/templates?chat_id=-101001")
	if bytes.Contains(stateResp.Body.Bytes(), []byte("新关键词")) {
		t.Fatalf("自动回复删除后不应仍在管理列表: %s", stateResp.Body.String())
	}
}

func TestAdminTemplatesCanToggleWelcomeAndPreview(t *testing.T) {
	database := newAdminTemplatesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	stateResp := getAdminTemplates(t, server, cookies, "/admin/api/templates?chat_id=-101001")
	var state adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(stateResp.Body.Bytes(), &state); err != nil {
		t.Fatalf("解析欢迎语状态失败: %v body=%s", err, stateResp.Body.String())
	}
	if !state.OK || state.Data.Welcome.Enabled || state.Data.Welcome.Template != "原欢迎语" {
		t.Fatalf("欢迎语初始状态错误: %+v", state)
	}
	wantVariables := map[string]bool{
		"{group_name}":   false,
		"{user_mention}": false,
		"{user_id}":      false,
		"{first_name}":   false,
	}
	for _, variable := range state.Data.Variables {
		if _, ok := wantVariables[variable.Token]; ok {
			wantVariables[variable.Token] = true
		}
	}
	for token, found := range wantVariables {
		if !found {
			t.Fatalf("欢迎语变量列表缺少 %s: %+v", token, state.Data.Variables)
		}
	}

	template := "{user_mention}，欢迎加入 {group_name}；ID={user_id}；姓名={first_name}"
	renderBody := fmt.Sprintf(`{"kind":"welcome","chatId":-101001,"template":%q,"sample":{"userId":7788,"firstName":"小明 & 同学"}}`, template)
	renderResp := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/render", renderBody)
	if renderResp.Code != http.StatusOK {
		t.Fatalf("欢迎语实时预览应成功: got=%d body=%s", renderResp.Code, renderResp.Body.String())
	}
	var rendered adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(renderResp.Body.Bytes(), &rendered); err != nil {
		t.Fatalf("解析欢迎语实时预览失败: %v body=%s", err, renderResp.Body.String())
	}
	for _, want := range []string{
		`<a href="tg://user?id=7788">小明 &amp; 同学</a>`,
		"欢迎加入 欢迎&lt;运营&gt;群",
		"ID=7788",
		"姓名=小明 &amp; 同学",
	} {
		if !bytes.Contains([]byte(rendered.Data.Preview.Text), []byte(want)) {
			t.Fatalf("欢迎语实时预览缺少 %q: %+v", want, rendered.Data.Preview)
		}
	}
	if rendered.Data.Preview.ParseMode != "HTML" || bytes.Contains([]byte(rendered.Data.Preview.Text), []byte("@")) {
		t.Fatalf("无 username 时应使用可点击姓名，不应硬拼 @username: %+v", rendered.Data.Preview)
	}
	assertAdminWelcomeConfig(t, database, -101001, false, "原欢迎语")

	previewBody := fmt.Sprintf(`{"kind":"welcome","chatId":-101001,"enabled":true,"template":%q,"sample":{"userId":7788,"firstName":"小明 & 同学"}}`, template)
	previewResp := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/preview", previewBody)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("欢迎语变更预演应成功: got=%d body=%s", previewResp.Code, previewResp.Body.String())
	}
	var preview adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatalf("解析欢迎语变更预演失败: %v body=%s", err, previewResp.Body.String())
	}
	if !preview.OK || preview.Data.ApplyToken == "" || preview.Data.MatchedCount != 1 || preview.Data.Preview.Text != rendered.Data.Preview.Text {
		t.Fatalf("欢迎语变更预演结果错误: %+v", preview)
	}
	assertAdminWelcomeConfig(t, database, -101001, false, "原欢迎语")

	applyResp := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/apply", `{"applyToken":"`+preview.Data.ApplyToken+`"}`)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("确认欢迎语变更应成功: got=%d body=%s", applyResp.Code, applyResp.Body.String())
	}
	var receipt adminTemplatesEnvelopeForTest
	if err := json.Unmarshal(applyResp.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("解析欢迎语变更回执失败: %v body=%s", err, applyResp.Body.String())
	}
	if !receipt.OK || receipt.Data.UpdatedCount != 1 {
		t.Fatalf("欢迎语变更回执错误: %+v", receipt)
	}
	assertAdminWelcomeConfig(t, database, -101001, true, template)

	reused := postAdminTemplatesJSON(t, server, cookies, "/admin/api/templates/apply", `{"applyToken":"`+preview.Data.ApplyToken+`"}`)
	if reused.Code != http.StatusBadRequest {
		t.Fatalf("欢迎语确认令牌必须一次性使用: got=%d body=%s", reused.Code, reused.Body.String())
	}
}

func TestAdminStaticAppIncludesTemplatesCenter(t *testing.T) {
	server := newTestSessionAdminServer(t)
	cookies := loginAdminForAPITest(t, server)
	shellReq := httptest.NewRequest(http.MethodGet, "/admin/templates", nil)
	for _, cookie := range cookies {
		shellReq.AddCookie(cookie)
	}
	shellResp := httptest.NewRecorder()
	server.handler().ServeHTTP(shellResp, shellReq)
	if shellResp.Code != http.StatusOK || !strings.Contains(shellResp.Body.String(), `<div id="app"`) {
		t.Fatalf("模板中心路由应返回前端壳: got=%d body=%s", shellResp.Code, shellResp.Body.String())
	}
	assetReq := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	for _, cookie := range cookies {
		assetReq.AddCookie(cookie)
	}
	assetResp := httptest.NewRecorder()
	server.handler().ServeHTTP(assetResp, assetReq)
	if assetResp.Code != http.StatusOK {
		t.Fatalf("读取前端资源失败: got=%d body=%s", assetResp.Code, assetResp.Body.String())
	}
	for _, want := range []string{
		"/admin/templates",
		"/admin/api/templates",
		"/admin/api/templates/auto-replies/preview",
		"/admin/api/templates/closing-reminders/preview",
		"renderTemplatesCenter",
		"变量",
		"实时预览",
		"未保存",
	} {
		if !strings.Contains(assetResp.Body.String(), want) {
			t.Fatalf("自动化中心前端缺少 %q", want)
		}
	}
}

func newAdminTemplatesTestDatabase(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-templates.db"))
	if err != nil {
		t.Fatalf("初始化模板测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.EnsureGroup(-101001, "欢迎<运营>群"); err != nil {
		t.Fatalf("创建模板测试群失败: %v", err)
	}
	if err := database.UpdateGroupFields(-101001, map[string]any{
		"welcome_enabled": 0,
		"welcome_text":    "原欢迎语",
	}); err != nil {
		t.Fatalf("初始化欢迎语配置失败: %v", err)
	}
	return database
}

func newAdminClosingTemplatesTestDatabase(t *testing.T) *db.Database {
	t.Helper()
	database := newAdminTemplatesTestDatabase(t)
	for _, item := range []struct {
		chatID    int64
		title     string
		dayCut    string
		enabled   int
		time      string
		threshold float64
		dmMode    string
		normal    string
		pending   string
	}{
		{chatID: -101001, dayCut: "04:00:00", enabled: 0, time: "03:00", threshold: 1.5, dmMode: db.ClosingReminderDMModeOwner, normal: "旧正常文案", pending: "旧待确认文案"},
		{chatID: -101002, title: "提醒测试群2", dayCut: "01:00:00", enabled: 1, time: "02:00", threshold: 1, dmMode: db.ClosingReminderDMModeCustom, normal: "二群旧文案", pending: "二群待确认"},
		{chatID: -101003, title: "提醒测试群3", dayCut: "06:00:00", enabled: 0, time: "23:00", threshold: 3, dmMode: db.ClosingReminderDMModeOff, normal: "三群旧文案", pending: "三群待确认"},
	} {
		if item.chatID != -101001 {
			if _, err := database.EnsureGroup(item.chatID, item.title); err != nil {
				t.Fatalf("创建下课提醒测试群失败: %v", err)
			}
		}
		if err := database.UpdateGroupFields(item.chatID, map[string]any{"day_cut_time": item.dayCut}); err != nil {
			t.Fatalf("设置测试群日切失败: %v", err)
		}
		if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
			ChatID:       item.chatID,
			Enabled:      item.enabled,
			ReminderTime: item.time,
			ThresholdU:   item.threshold,
			DMMode:       item.dmMode,
			TextNormal:   item.normal,
			TextPending:  item.pending,
			UpdatedBy:    90001,
		}); err != nil {
			t.Fatalf("初始化下课提醒配置失败: %v", err)
		}
	}
	return database
}

func assertAdminClosingConfig(t *testing.T, database *db.Database, chatID int64, enabled bool, reminderTime string, threshold float64, dmMode, normal, pending string) {
	t.Helper()
	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败: chat=%d err=%v", chatID, err)
	}
	if (config.Enabled == 1) != enabled || config.ReminderTime != reminderTime || config.ThresholdU != threshold || config.DMMode != dmMode || config.TextNormal != normal || config.TextPending != pending {
		t.Fatalf("下课提醒配置错误: chat=%d config=%+v", chatID, config)
	}
}

func getAdminTemplates(t *testing.T, server *Server, cookies []*http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("%s 应返回 200: got=%d body=%s", path, resp.Code, resp.Body.String())
	}
	return resp
}

func postAdminTemplatesJSON(t *testing.T, server *Server, cookies []*http.Cookie, path, body string) *httptest.ResponseRecorder {
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

func assertAdminWelcomeConfig(t *testing.T, database *db.Database, chatID int64, enabled bool, template string) {
	t.Helper()
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取欢迎语配置失败: %v", err)
	}
	if (group.WelcomeEnabled == 1) != enabled || group.WelcomeText != template {
		t.Fatalf("欢迎语配置错误: got enabled=%v template=%q", group.WelcomeEnabled == 1, group.WelcomeText)
	}
}
