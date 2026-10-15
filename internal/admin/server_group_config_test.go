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

func adminValidGroupConfigForm(chatID int64) url.Values {
	form := url.Values{}
	form.Set("action", "save_group_config")
	form.Set("chat_id", strconv.FormatInt(chatID, 10))
	form.Set("title", "新群名")
	form.Set("owner_user_id", "90001")
	form.Set("bookkeeping_enabled", "1")
	form.Set("display_nickname", "1")
	form.Set("show_usdt_conversion", "1")
	form.Set("fee_rate", "11")
	form.Set("payout_fee_rate", "2.5")
	form.Set("custom_rate", "205")
	form.Set("payout_rate", "210")
	form.Set("use_realtime_rate", "1")
	form.Set("rate_source", "okx")
	form.Set("day_cut_time", "03:30:00")
	form.Set("payout_address", "TXpayout")
	form.Set("payout_address_remark", "主下发")
	form.Set("exclusive_address", "TXexclusive")
	form.Set("exclusive_address_owner", "@alice")
	form.Set("exclusive_address_remark", "专属")
	form.Set("group_type", "业务")
	form.Set("group_tags", "夜班,重点")
	form.Set("duplicate_alert_enabled", "1")
	form.Set("at_broadcast_enabled", "1")
	form.Set("auto_reply_enabled", "1")
	form.Set("custom_content_with_time", "1")
	form.Set("class_start_ad", "上课文案")
	form.Set("class_end_ad", "下课文案")
	form.Set("welcome_enabled", "1")
	form.Set("welcome_text", "欢迎加入【群名】")
	return form
}

func adminValidClosingReminderForm(chatID int64) url.Values {
	form := url.Values{}
	form.Set("action", "save_closing_reminder")
	form.Set("chat_id", strconv.FormatInt(chatID, 10))
	form.Set("closing_enabled", "1")
	form.Set("reminder_time", "01:00")
	form.Set("threshold_u", "1.5")
	form.Set("dm_mode", db.ClosingReminderDMModeOwnerOperatorsCustom)
	form.Set("text_normal", "正常下课")
	form.Set("text_pending", "待确认")
	form.Set("text_no_data", "无数据")
	form.Set("text_review", "需复核")
	form.Set("text_failed", "发送失败")
	form.Set("updated_by", "90001")
	return form
}

func TestAdminGroupPageShowsEditableConfigForm(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-group-form.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100301)
	if _, err := database.EnsureGroup(chatID, "配置群"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "group-token"}, database, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/group?token=group-token&chat_id=-100301", nil)
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("群详情状态码错误: %d", resp.Code)
	}
	body := resp.Body.String()
	for _, want := range []string{
		"群配置",
		`name="action" value="save_group_config"`,
		`name="fee_rate"`,
		`name="custom_rate"`,
		`name="payout_address"`,
		`name="day_cut_time"`,
		`name="welcome_enabled"`,
		`name="welcome_text"`,
		"下课时间",
		"/admin/orders?chat_id=-100301",
		"/admin/broadcast?source_chat_id=-100301",
		"/admin/schedules?chat_id=-100301",
		"/admin/automation?chat_id=-100301",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("群配置页缺少 %q: %s", want, body)
		}
	}
}

func TestAdminGroupRouteUsesEditableConfigPage(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-group-route.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100304)
	if _, err := database.EnsureGroup(chatID, "路由群"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "group-token"}, database, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/group?token=group-token&chat_id=-100304", nil)
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("/admin/group 状态码错误: %d body=%s", resp.Code, resp.Body.String())
	}
	for _, want := range []string{"群配置", `name="action" value="save_group_config"`, "路由群"} {
		if !strings.Contains(resp.Body.String(), want) {
			t.Fatalf("/admin/group 未渲染配置页，缺少 %q: %s", want, resp.Body.String())
		}
	}
}

func TestAdminGroupPostRejectsCrossOrigin(t *testing.T) {
	server := NewServer(config.Config{AdminToken: "group-token"}, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/group?token=group-token&chat_id=-1001", strings.NewReader("action=save_group_config"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.Host = "autobookbl.site"
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("跨站群配置 POST 应被拒绝: got=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminGroupConfigPreservesLegacySummaryFields(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-group-legacy-fields.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100308)
	if _, err := database.EnsureGroup(chatID, "旧字段群"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"owner_user_id":       97001,
		"bookkeeping_enabled": 1,
		"group_type":          "业务",
		"group_tags":          "重点,夜班",
		"day_cut_time":        "03:00:00",
		"rate_source":         "okx",
		"fee_rate":            11.0,
		"payout_fee_rate":     2.5,
		"payout_address":      "TXlegacy",
		"exclusive_address":   "TXexclusive",
	}); err != nil {
		t.Fatalf("写入旧字段失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "legacy-token"}, database, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/group?token=legacy-token&chat_id=-100308", nil)
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	body := resp.Body.String()
	for _, want := range []string{"群ID", "-100308", "旧字段群", "业务", "重点,夜班", "97001", "开启", "03:00:00", "okx", "11", "2.5", "TXlegacy", "TXexclusive"} {
		if !strings.Contains(body, want) {
			t.Fatalf("群详情缺少旧字段 %q: %s", want, body)
		}
	}
}

func TestAdminGroupConfigPostUpdatesGroupFields(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-group-save.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100302)
	if _, err := database.EnsureGroup(chatID, "旧群名"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "group-token"}, database, nil)
	form := adminValidGroupConfigForm(chatID)

	req := httptest.NewRequest(http.MethodPost, "/admin/group?token=group-token&chat_id=-100302", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("保存群配置状态码错误: %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "群配置已保存") {
		t.Fatalf("页面应提示保存成功: %s", resp.Body.String())
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.Title != "新群名" || group.OwnerUserID != 90001 || group.BookkeepingEnabled != 1 {
		t.Fatalf("基础字段未保存: %+v", group)
	}
	if group.FeeRate != 11 || group.PayoutFeeRate != 2.5 || group.CustomRate != 205 || group.PayoutRate != 210 {
		t.Fatalf("费率汇率未保存: %+v", group)
	}
	if group.DayCutTime != "03:30:00" || group.PayoutAddress != "TXpayout" || group.ExclusiveAddress != "TXexclusive" {
		t.Fatalf("时间或地址未保存: %+v", group)
	}
	if group.GroupType != "业务" || group.GroupTags != "夜班,重点" || group.WelcomeText != "欢迎加入【群名】" {
		t.Fatalf("文本配置未保存: %+v", group)
	}
	if group.DuplicateAlertEnabled != 1 || group.AtBroadcastEnabled != 1 || group.AutoReplyEnabled != 1 || group.CustomContentWithTime != 1 || group.WelcomeEnabled != 1 {
		t.Fatalf("开关未保存: %+v", group)
	}
}

func TestAdminGroupConfigPostRejectsInvalidRateWithoutPartialWrite(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-group-invalid.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100303)
	if _, err := database.EnsureGroup(chatID, "原群名"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "group-token"}, database, nil)
	form := adminValidGroupConfigForm(chatID)
	form.Set("title", "不应保存")
	form.Set("custom_rate", "0")

	req := httptest.NewRequest(http.MethodPost, "/admin/group?token=group-token&chat_id=-100303", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("非法表单仍应返回页面: %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "汇率必须大于 0") {
		t.Fatalf("页面应提示汇率错误: %s", resp.Body.String())
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.Title != "原群名" {
		t.Fatalf("非法表单不应部分写入 title: %+v", group)
	}
}

func TestAdminGroupConfigRejectsInvalidFieldMatrixWithoutPartialWrite(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(url.Values)
		wantError string
	}{
		{name: "payout rate zero", mutate: func(v url.Values) { v.Set("payout_rate", "0") }, wantError: "下发汇率必须大于 0"},
		{name: "fee too high", mutate: func(v url.Values) { v.Set("fee_rate", "101") }, wantError: "入款费率必须在 -100 到 100 之间"},
		{name: "fee too low", mutate: func(v url.Values) { v.Set("fee_rate", "-101") }, wantError: "入款费率必须在 -100 到 100 之间"},
		{name: "custom rate nan", mutate: func(v url.Values) { v.Set("custom_rate", "NaN") }, wantError: "入款汇率格式错误"},
		{name: "custom rate inf", mutate: func(v url.Values) { v.Set("custom_rate", "+Inf") }, wantError: "入款汇率格式错误"},
		{name: "custom rate text", mutate: func(v url.Values) { v.Set("custom_rate", "abc") }, wantError: "入款汇率格式错误"},
		{name: "bad day cut", mutate: func(v url.Values) { v.Set("day_cut_time", "25:00") }, wantError: "日切时间格式错误"},
		{name: "bad rate source", mutate: func(v url.Values) { v.Set("rate_source", "evil") }, wantError: "汇率来源不支持"},
		{name: "missing custom rate", mutate: func(v url.Values) { v.Del("custom_rate") }, wantError: "入款汇率不能为空"},
		{name: "long title", mutate: func(v url.Values) { v.Set("title", strings.Repeat("群", 129)) }, wantError: "群名长度不能超过 128"},
		{name: "long tags", mutate: func(v url.Values) { v.Set("group_tags", strings.Repeat("a", 257)) }, wantError: "群标签长度不能超过 256"},
		{name: "long address", mutate: func(v url.Values) { v.Set("payout_address", strings.Repeat("T", 257)) }, wantError: "地址长度不能超过 256"},
		{name: "long welcome", mutate: func(v url.Values) { v.Set("welcome_text", strings.Repeat("欢", 2001)) }, wantError: "欢迎语长度不能超过 2000"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-group-invalid-matrix.db"))
			if err != nil {
				t.Fatalf("初始化数据库失败: %v", err)
			}
			defer database.Close()

			chatID := int64(-100307)
			if _, err := database.EnsureGroup(chatID, "原群名"); err != nil {
				t.Fatalf("创建群失败: %v", err)
			}

			form := adminValidGroupConfigForm(chatID)
			form.Set("title", "不应保存")
			tc.mutate(form)

			server := NewServer(config.Config{AdminToken: "group-token"}, database, nil)
			req := httptest.NewRequest(http.MethodPost, "/admin/group?token=group-token&chat_id=-100307", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp := httptest.NewRecorder()

			server.handleGroupConfig(resp, req)

			if !strings.Contains(resp.Body.String(), tc.wantError) {
				t.Fatalf("应提示 %q: %s", tc.wantError, resp.Body.String())
			}
			group, err := database.GetGroup(chatID)
			if err != nil {
				t.Fatalf("读取群配置失败: %v", err)
			}
			if group.Title != "原群名" {
				t.Fatalf("非法表单不应部分写入: %+v", group)
			}
		})
	}
}

func TestAdminGroupConfigRejectsMismatchedChatID(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-group-chatid.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatA := int64(-100305)
	chatB := int64(-100306)
	if _, err := database.EnsureGroup(chatA, "A群"); err != nil {
		t.Fatalf("创建A群失败: %v", err)
	}
	if _, err := database.EnsureGroup(chatB, "B群"); err != nil {
		t.Fatalf("创建B群失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "group-token"}, database, nil)
	form := adminValidGroupConfigForm(chatB)
	req := httptest.NewRequest(http.MethodPost, "/admin/group?token=group-token&chat_id=-100305", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("错误页面状态码应为 200: %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "chat_id 不一致") {
		t.Fatalf("应提示 chat_id 不一致: %s", resp.Body.String())
	}
	groupB, err := database.GetGroup(chatB)
	if err != nil {
		t.Fatalf("读取B群失败: %v", err)
	}
	if groupB.Title != "B群" {
		t.Fatalf("不应通过隐藏字段写入B群: %+v", groupB)
	}
}

func TestAdminGroupConfigRejectsUnknownGroup(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-group-unknown.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100399)
	server := NewServer(config.Config{AdminToken: "group-token"}, database, nil)
	form := adminValidGroupConfigForm(chatID)
	req := httptest.NewRequest(http.MethodPost, "/admin/group?token=group-token&chat_id=-100399", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("错误页面状态码应为 200: %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "群不存在") {
		t.Fatalf("应提示群不存在: %s", resp.Body.String())
	}
	if _, err := database.GetGroup(chatID); err == nil {
		t.Fatalf("未知群 POST 不应创建群配置")
	}
}

func TestAdminGroupClosingReminderPostUpdatesConfig(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-closing-save.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100401)
	if _, err := database.EnsureGroup(chatID, "下课提醒群"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "closing-token"}, database, nil)
	form := adminValidClosingReminderForm(chatID)

	req := httptest.NewRequest(http.MethodPost, "/admin/group?token=closing-token&chat_id=-100401", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("保存下课配置状态码错误: %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "下课时间已保存") {
		t.Fatalf("页面应提示下课保存成功: %s", resp.Body.String())
	}

	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课配置失败: %v", err)
	}
	if config.Enabled != 1 || config.ReminderTime != "01:00" || config.ThresholdU != 1.5 {
		t.Fatalf("下课基础配置未保存: %+v", config)
	}
	if config.DMMode != db.ClosingReminderDMModeOwnerOperatorsCustom || config.TextNormal != "正常下课" || config.UpdatedBy != 90001 {
		t.Fatalf("下课通知配置未保存: %+v", config)
	}
}

func TestAdminGroupClosingReminderRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name      string
		field     string
		value     string
		wantError string
	}{
		{name: "zero threshold", field: "threshold_u", value: "0", wantError: "下课阈值必须大于 0"},
		{name: "negative threshold", field: "threshold_u", value: "-1", wantError: "下课阈值必须大于 0"},
		{name: "bad time", field: "reminder_time", value: "25:00", wantError: "下课时间格式错误"},
		{name: "bad mode", field: "dm_mode", value: "everyone", wantError: "私聊通知模式不支持"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-closing-invalid.db"))
			if err != nil {
				t.Fatalf("初始化数据库失败: %v", err)
			}
			defer database.Close()

			chatID := int64(-100402)
			if _, err := database.EnsureGroup(chatID, "下课非法群"); err != nil {
				t.Fatalf("创建群失败: %v", err)
			}

			if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
				ChatID:       chatID,
				Enabled:      1,
				ReminderTime: "04:00",
				ThresholdU:   1.5,
				DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
				TextNormal:   "原文案",
			}); err != nil {
				t.Fatalf("写入初始下课配置失败: %v", err)
			}

			form := url.Values{}
			form.Set("action", "save_closing_reminder")
			form.Set("chat_id", strconv.FormatInt(chatID, 10))
			form.Set("closing_enabled", "1")
			form.Set("reminder_time", "01:00")
			form.Set("threshold_u", "1.5")
			form.Set("dm_mode", db.ClosingReminderDMModeOwnerOperatorsCustom)
			form.Set("text_normal", "新文案")
			form.Set("text_pending", "待确认")
			form.Set("text_no_data", "无数据")
			form.Set("text_review", "需复核")
			form.Set("text_failed", "发送失败")
			form.Set(tc.field, tc.value)

			server := NewServer(config.Config{AdminToken: "closing-token"}, database, nil)
			req := httptest.NewRequest(http.MethodPost, "/admin/group?token=closing-token&chat_id=-100402", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp := httptest.NewRecorder()

			server.handleGroupConfig(resp, req)

			if !strings.Contains(resp.Body.String(), tc.wantError) {
				t.Fatalf("应提示 %q: %s", tc.wantError, resp.Body.String())
			}
			got, err := database.GetClosingReminderConfig(chatID)
			if err != nil {
				t.Fatalf("读取下课配置失败: %v", err)
			}
			if got.ReminderTime != "04:00" || got.ThresholdU != 1.5 || got.TextNormal != "原文案" {
				t.Fatalf("非法下课表单不应部分写入: %+v", got)
			}
		})
	}
}

func TestAdminGroupClosingReminderRejectsChatIDMismatchAndUnknownGroup(t *testing.T) {
	t.Run("mismatched chat id", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-closing-mismatch.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		chatA := int64(-100403)
		chatB := int64(-100404)
		if _, err := database.EnsureGroup(chatA, "A群"); err != nil {
			t.Fatalf("创建A群失败: %v", err)
		}
		if _, err := database.EnsureGroup(chatB, "B群"); err != nil {
			t.Fatalf("创建B群失败: %v", err)
		}

		form := adminValidClosingReminderForm(chatB)
		server := NewServer(config.Config{AdminToken: "closing-token"}, database, nil)
		req := httptest.NewRequest(http.MethodPost, "/admin/group?token=closing-token&chat_id=-100403", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp := httptest.NewRecorder()

		server.handleGroupConfig(resp, req)

		if !strings.Contains(resp.Body.String(), "chat_id 不一致") {
			t.Fatalf("应提示 chat_id 不一致: %s", resp.Body.String())
		}
		config, err := database.GetClosingReminderConfig(chatB)
		if err != nil {
			t.Fatalf("读取B群下课配置失败: %v", err)
		}
		if config.Enabled != 0 {
			t.Fatalf("不应通过隐藏字段写入B群下课配置: %+v", config)
		}
	})

	t.Run("unknown group", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-closing-unknown.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		chatID := int64(-100405)
		form := adminValidClosingReminderForm(chatID)
		server := NewServer(config.Config{AdminToken: "closing-token"}, database, nil)
		req := httptest.NewRequest(http.MethodPost, "/admin/group?token=closing-token&chat_id=-100405", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp := httptest.NewRecorder()

		server.handleGroupConfig(resp, req)

		if !strings.Contains(resp.Body.String(), "群不存在") {
			t.Fatalf("应提示群不存在: %s", resp.Body.String())
		}
		config, err := database.GetClosingReminderConfig(chatID)
		if err != nil {
			t.Fatalf("读取默认下课配置失败: %v", err)
		}
		if config.Enabled != 0 {
			t.Fatalf("未知群不应创建启用的下课配置: %+v", config)
		}
	})
}

func TestAdminGroupClosingReminderRejectsInvalidUpdatedBy(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-closing-updated-by.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100406)
	if _, err := database.EnsureGroup(chatID, "下课更新人群"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}

	form := adminValidClosingReminderForm(chatID)
	form.Set("updated_by", "abc")
	server := NewServer(config.Config{AdminToken: "closing-token"}, database, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/group?token=closing-token&chat_id=-100406", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	if !strings.Contains(resp.Body.String(), "更新人ID格式错误") {
		t.Fatalf("应提示 updated_by 错误: %s", resp.Body.String())
	}
	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课配置失败: %v", err)
	}
	if config.Enabled != 0 || config.UpdatedBy != 0 {
		t.Fatalf("非法 updated_by 不应写入: %+v", config)
	}
}

func TestAdminGroupPageLabelsDayCutAndClosingTimeSeparately(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-time-labels.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100407)
	if _, err := database.EnsureGroup(chatID, "时间配置群"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"day_cut_time": "03:00:00"}); err != nil {
		t.Fatalf("写入日切时间失败: %v", err)
	}
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      1,
		ReminderTime: "01:00",
		ThresholdU:   1.5,
		DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
	}); err != nil {
		t.Fatalf("写入下课配置失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "time-token"}, database, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/group?token=time-token&chat_id=-100407", nil)
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	body := resp.Body.String()
	for _, want := range []string{"账期设置", "日切时间", `name="day_cut_time"`, "03:00:00", "下课提醒", "下课时间", `name="reminder_time"`, "01:00"} {
		if !strings.Contains(body, want) {
			t.Fatalf("时间配置页缺少 %q: %s", want, body)
		}
	}
}

func TestAdminGroupConfigEscapesHTMLInput(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-group-escape.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	chatID := int64(-100601)
	if _, err := database.EnsureGroup(chatID, `<script>alert(1)</script>`); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"payout_address_remark": `" onfocus="alert(1)`,
		"welcome_text":          `<b>欢迎</b>`,
	}); err != nil {
		t.Fatalf("写入危险文本失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "escape-token"}, database, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/group?token=escape-token&chat_id=-100601", nil)
	resp := httptest.NewRecorder()

	server.handleGroupConfig(resp, req)

	body := resp.Body.String()
	for _, bad := range []string{`<script>alert(1)</script>`, `" onfocus="alert(1)`, `<b>欢迎</b>`} {
		if strings.Contains(body, bad) {
			t.Fatalf("页面包含未转义危险文本 %q: %s", bad, body)
		}
	}
	for _, want := range []string{`&lt;script&gt;alert(1)&lt;/script&gt;`, `&#34; onfocus=&#34;alert(1)`, `&lt;b&gt;欢迎&lt;/b&gt;`} {
		if !strings.Contains(body, want) {
			t.Fatalf("页面缺少转义文本 %q: %s", want, body)
		}
	}
}
