package admin

import (
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

type adminConsoleEnvelopeForTest struct {
	OK   bool `json:"ok"`
	Data struct {
		Summary struct {
			TotalGroups        int `json:"totalGroups"`
			HealthyGroups      int `json:"healthyGroups"`
			IncompleteGroups   int `json:"incompleteGroups"`
			AverageHealthScore int `json:"averageHealthScore"`
			PendingSchedules   int `json:"pendingSchedules"`
			FailedTasks        int `json:"failedTasks"`
		} `json:"summary"`
		Todos []struct {
			Title      string `json:"title"`
			ChatID     int64  `json:"chatId"`
			Severity   string `json:"severity"`
			Visibility string `json:"visibility"`
			ActionURL  string `json:"actionUrl"`
		} `json:"todos"`
		Health []struct {
			ChatID    int64    `json:"chatId"`
			Title     string   `json:"title"`
			Score     int      `json:"score"`
			Status    string   `json:"status"`
			Reasons   []string `json:"reasons"`
			NextSteps []struct {
				Title      string `json:"title"`
				Visibility string `json:"visibility"`
				ActionURL  string `json:"actionUrl"`
			} `json:"nextSteps"`
		} `json:"health"`
		NextSteps []struct {
			Title      string `json:"title"`
			ChatID     int64  `json:"chatId"`
			Visibility string `json:"visibility"`
			ActionURL  string `json:"actionUrl"`
		} `json:"nextSteps"`
	} `json:"data"`
}

func TestAdminConsoleShowsTodosAndHealthSummary(t *testing.T) {
	database := newAdminConsoleTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database

	console := getAdminConsole(t, server)
	if !console.OK {
		t.Fatalf("console API 应返回 ok=true: %+v", console)
	}

	if console.Data.Summary.TotalGroups != 2 {
		t.Fatalf("总群数错误: %+v", console.Data.Summary)
	}
	if console.Data.Summary.HealthyGroups != 1 || console.Data.Summary.IncompleteGroups != 1 {
		t.Fatalf("健康摘要错误: %+v", console.Data.Summary)
	}
	if console.Data.Summary.PendingSchedules != 1 {
		t.Fatalf("待发送定时任务数错误: %+v", console.Data.Summary)
	}
	if console.Data.Summary.FailedTasks != 0 {
		t.Fatalf("v1 总控台不应虚构失败任务: %+v", console.Data.Summary)
	}
	if console.Data.Summary.AverageHealthScore <= 0 || console.Data.Summary.AverageHealthScore >= 100 {
		t.Fatalf("平均健康分应基于健康群和缺配置群计算: %+v", console.Data.Summary)
	}

	if len(console.Data.Todos) == 0 {
		t.Fatalf("缺配置群应产生待办: %+v", console.Data)
	}
	if !adminConsoleTestHasTodo(console, -100602, "设置群权限人") {
		t.Fatalf("待办应提示设置群权限人: %+v", console.Data.Todos)
	}
	if !adminConsoleTestHasTodo(console, -100602, "设置下发地址") {
		t.Fatalf("待办应提示设置下发地址: %+v", console.Data.Todos)
	}
}

func TestAdminConsoleComputesHealthScoreAndNextSteps(t *testing.T) {
	database := newAdminConsoleTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database

	console := getAdminConsole(t, server)
	var incomplete *struct {
		ChatID    int64    `json:"chatId"`
		Title     string   `json:"title"`
		Score     int      `json:"score"`
		Status    string   `json:"status"`
		Reasons   []string `json:"reasons"`
		NextSteps []struct {
			Title      string `json:"title"`
			Visibility string `json:"visibility"`
			ActionURL  string `json:"actionUrl"`
		} `json:"nextSteps"`
	}
	for i := range console.Data.Health {
		if console.Data.Health[i].ChatID == -100602 {
			incomplete = &console.Data.Health[i]
			break
		}
	}
	if incomplete == nil {
		t.Fatalf("健康列表应包含缺配置群: %+v", console.Data.Health)
	}
	if incomplete.Score >= 70 || incomplete.Status != "risk" {
		t.Fatalf("缺配置群应低分且标记风险: %+v", *incomplete)
	}
	for _, want := range []string{"缺少权限人", "缺少群类型", "未配置下发地址", "下课提醒未启用"} {
		if !adminConsoleTestHasReason(incomplete.Reasons, want) {
			t.Fatalf("健康分缺少可解释原因 %q: %+v", want, *incomplete)
		}
	}
	if len(incomplete.NextSteps) == 0 {
		t.Fatalf("缺配置群应有下一步建议: %+v", *incomplete)
	}
	for _, step := range incomplete.NextSteps {
		if step.Visibility != "admin_only" {
			t.Fatalf("下一步建议只能后台可见，不能自动发群: %+v", step)
		}
		if !strings.Contains(step.ActionURL, "/admin/group?chat_id=-100602") {
			t.Fatalf("建议应指向后台群配置页: %+v", step)
		}
	}
	if !adminConsoleTestHasNextStep(console, -100602, "设置下发地址") {
		t.Fatalf("总控台下一步建议应聚合关键动作: %+v", console.Data.NextSteps)
	}
}

func TestAdminConsoleSummaryCountsAttentionGroupsAsIncomplete(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-console-attention.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	chatID := int64(-100603)
	if _, err := database.EnsureGroup(chatID, "轻微缺配置群"); err != nil {
		t.Fatalf("创建群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"owner_user_id":       90001,
		"bookkeeping_enabled": 1,
		"group_type":          "",
		"group_tags":          "重点",
		"custom_rate":         205.0,
		"payout_rate":         210.0,
		"payout_address":      "TXattention",
		"auto_reply_enabled":  1,
		"welcome_text":        "欢迎加入【群名】",
	}); err != nil {
		t.Fatalf("更新轻微缺配置群失败: %v", err)
	}
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      1,
		ReminderTime: "01:00",
		ThresholdU:   1.5,
		DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
	}); err != nil {
		t.Fatalf("写入下课提醒失败: %v", err)
	}

	server := newTestSessionAdminServer(t)
	server.db = database
	console := getAdminConsole(t, server)

	if console.Data.Summary.HealthyGroups != 0 || console.Data.Summary.IncompleteGroups != 1 {
		t.Fatalf("有配置缺口的 attention 群不应算健康群: %+v", console.Data.Summary)
	}
	if len(console.Data.Health) != 1 || console.Data.Health[0].Status != "attention" || console.Data.Health[0].Score != 90 {
		t.Fatalf("轻微缺配置群应为 attention 且 90 分: %+v", console.Data.Health)
	}
}

func TestAdminConsoleRequiresSessionAndHandlesNilDB(t *testing.T) {
	server := newTestSessionAdminServer(t)
	server.cfg.AdminToken = "legacy-token"

	for _, rawURL := range []string{"/admin/api/console", "/admin/api/console?token=legacy-token"} {
		req := httptest.NewRequest(http.MethodGet, rawURL, nil)
		resp := httptest.NewRecorder()
		server.handler().ServeHTTP(resp, req)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("%s 未登录应返回 401: got=%d body=%s", rawURL, resp.Code, resp.Body.String())
		}
	}

	console := getAdminConsole(t, server)
	if !console.OK {
		t.Fatalf("nil DB console API 应返回 ok=true: %+v", console)
	}
	if console.Data.Summary.TotalGroups != 0 || len(console.Data.Todos) != 0 || len(console.Data.Health) != 0 {
		t.Fatalf("nil DB console API 应返回空数据: %+v", console.Data)
	}
}

func newAdminConsoleTestDatabase(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-console.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	healthyChatID := int64(-100601)
	if _, err := database.EnsureGroup(healthyChatID, "健康群"); err != nil {
		t.Fatalf("创建健康群失败: %v", err)
	}
	if err := database.UpdateGroupFields(healthyChatID, map[string]any{
		"owner_user_id":           90001,
		"bookkeeping_enabled":     1,
		"group_type":              "业务",
		"group_tags":              "重点,夜班",
		"custom_rate":             205.0,
		"payout_rate":             210.0,
		"payout_address":          "TXhealthy",
		"exclusive_address":       "TXexclusive",
		"day_cut_time":            "03:00:00",
		"auto_reply_enabled":      1,
		"welcome_text":            "欢迎加入【群名】\n新成员：@username",
		"at_broadcast_enabled":    1,
		"duplicate_alert_enabled": 1,
	}); err != nil {
		t.Fatalf("更新健康群失败: %v", err)
	}
	if err := database.UpsertClosingReminderConfig(db.GroupClosingReminderConfig{
		ChatID:       healthyChatID,
		Enabled:      1,
		ReminderTime: "01:00",
		ThresholdU:   1.5,
		DMMode:       db.ClosingReminderDMModeOwnerOperatorsCustom,
	}); err != nil {
		t.Fatalf("写入健康群下课提醒失败: %v", err)
	}

	incompleteChatID := int64(-100602)
	if _, err := database.EnsureGroup(incompleteChatID, "缺配置群"); err != nil {
		t.Fatalf("创建缺配置群失败: %v", err)
	}
	if err := database.UpdateGroupFields(incompleteChatID, map[string]any{
		"bookkeeping_enabled": 1,
		"auto_reply_enabled":  0,
		"welcome_text":        "",
	}); err != nil {
		t.Fatalf("更新缺配置群失败: %v", err)
	}

	if _, err := database.AddSchedule(healthyChatID, "2026-08-02 03:00:00", "明日群发", 0, 0, "", "", 90001); err != nil {
		t.Fatalf("写入定时任务失败: %v", err)
	}

	return database
}

func getAdminConsole(t *testing.T, server *Server) adminConsoleEnvelopeForTest {
	t.Helper()
	loginResp := postAdminLogin(server, url.Values{
		"username": {"root"},
		"password": {"correct-password"},
		"totp":     {testTOTPCode(t, testAdminTOTPSecret, time.Now())},
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("登录应成功: got=%d body=%s", loginResp.Code, loginResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/console", nil)
	for _, cookie := range loginResp.Result().Cookies() {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("/admin/api/console 应返回 200: got=%d body=%s", resp.Code, resp.Body.String())
	}

	var out adminConsoleEnvelopeForTest
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("console API 应返回 JSON: %v body=%s", err, resp.Body.String())
	}
	return out
}

func adminConsoleTestHasTodo(console adminConsoleEnvelopeForTest, chatID int64, title string) bool {
	for _, todo := range console.Data.Todos {
		if todo.ChatID == chatID && strings.Contains(todo.Title, title) && todo.Visibility == "admin_only" {
			return true
		}
	}
	return false
}

func adminConsoleTestHasNextStep(console adminConsoleEnvelopeForTest, chatID int64, title string) bool {
	for _, step := range console.Data.NextSteps {
		if step.ChatID == chatID && strings.Contains(step.Title, title) && step.Visibility == "admin_only" {
			return true
		}
	}
	return false
}

func adminConsoleTestHasReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}
