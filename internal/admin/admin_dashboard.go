package admin

import (
	"html"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"go-bot/internal/db"
)

type adminConsoleData struct {
	Summary   adminConsoleSummary  `json:"summary"`
	Todos     []adminConsoleAction `json:"todos"`
	Health    []adminConsoleGroup  `json:"health"`
	NextSteps []adminConsoleAction `json:"nextSteps"`
}

type adminConsoleSummary struct {
	TotalGroups        int `json:"totalGroups"`
	HealthyGroups      int `json:"healthyGroups"`
	IncompleteGroups   int `json:"incompleteGroups"`
	AverageHealthScore int `json:"averageHealthScore"`
	PendingSchedules   int `json:"pendingSchedules"`
	FailedTasks        int `json:"failedTasks"`
}

type adminConsoleAction struct {
	ID          string `json:"id,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Severity    string `json:"severity,omitempty"`
	Visibility  string `json:"visibility"`
	ChatID      int64  `json:"chatId,omitempty"`
	ActionURL   string `json:"actionUrl,omitempty"`
}

type adminConsoleGroup struct {
	ChatID    int64                `json:"chatId"`
	Title     string               `json:"title"`
	Score     int                  `json:"score"`
	Status    string               `json:"status"`
	Reasons   []string             `json:"reasons"`
	NextSteps []adminConsoleAction `json:"nextSteps"`
}

func (s *Server) handleAdminConsole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"ok":    false,
			"error": "method not allowed",
		})
		return
	}
	data, err := s.buildAdminConsoleData()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": data,
	})
}

func (s *Server) buildAdminConsoleData() (adminConsoleData, error) {
	data := adminConsoleData{
		Todos:     []adminConsoleAction{},
		Health:    []adminConsoleGroup{},
		NextSteps: []adminConsoleAction{},
	}
	if s == nil || s.db == nil {
		return data, nil
	}

	groups, err := s.db.ListAllGroups()
	if err != nil {
		return data, err
	}
	pendingSchedules, err := s.db.ListPendingSchedules()
	if err != nil {
		return data, err
	}

	data.Summary.TotalGroups = len(groups)
	data.Summary.PendingSchedules = len(pendingSchedules)
	if len(groups) == 0 {
		return data, nil
	}

	totalScore := 0
	healthRows := make([]adminConsoleGroup, 0, len(groups))
	for _, group := range groups {
		closing, err := s.db.GetClosingReminderConfig(group.ChatID)
		if err != nil {
			return data, err
		}
		row := buildAdminConsoleGroupHealth(group, closing)
		totalScore += row.Score
		healthRows = append(healthRows, row)
	}

	for i := 0; i < len(healthRows); i++ {
		if healthRows[i].Status == "healthy" {
			data.Summary.HealthyGroups++
		} else {
			data.Summary.IncompleteGroups++
		}
	}
	sort.SliceStable(healthRows, func(i, j int) bool {
		if healthRows[i].Score == healthRows[j].Score {
			return healthRows[i].ChatID < healthRows[j].ChatID
		}
		return healthRows[i].Score < healthRows[j].Score
	})
	if len(healthRows) > 0 {
		data.Summary.AverageHealthScore = totalScore / len(healthRows)
	}

	for _, row := range healthRows {
		for _, step := range row.NextSteps {
			if step.Severity != "info" && len(data.Todos) < 12 {
				data.Todos = append(data.Todos, step)
			}
			if step.Severity != "info" && len(data.NextSteps) < 8 {
				data.NextSteps = append(data.NextSteps, step)
			}
		}
	}
	data.Health = healthRows
	return data, nil
}

func buildAdminConsoleGroupHealth(group db.GroupConfig, closing db.GroupClosingReminderConfig) adminConsoleGroup {
	title := strings.TrimSpace(group.Title)
	if title == "" {
		title = strconv.FormatInt(group.ChatID, 10)
	}

	row := adminConsoleGroup{
		ChatID:  group.ChatID,
		Title:   title,
		Score:   100,
		Status:  "healthy",
		Reasons: []string{},
	}

	addIssue := func(penalty int, reason, title, severity string) {
		if penalty <= 0 {
			return
		}
		row.Score -= penalty
		if row.Score < 0 {
			row.Score = 0
		}
		row.Reasons = append(row.Reasons, reason)
		row.NextSteps = append(row.NextSteps, adminConsoleAction{
			ID:          strconv.FormatInt(group.ChatID, 10) + ":" + strings.ReplaceAll(title, " ", "_"),
			Title:       title,
			Description: reason,
			Severity:    severity,
			Visibility:  "admin_only",
			ChatID:      group.ChatID,
			ActionURL:   "/admin/group?chat_id=" + strconv.FormatInt(group.ChatID, 10),
		})
	}

	if group.OwnerUserID <= 0 {
		addIssue(20, "缺少权限人", "设置群权限人", "high")
	}
	if group.BookkeepingEnabled != 1 {
		addIssue(10, "记账未开启", "开启记账", "normal")
	}
	if strings.TrimSpace(group.GroupType) == "" {
		addIssue(10, "缺少群类型", "设置群类型", "normal")
	}
	if strings.TrimSpace(group.GroupTags) == "" {
		addIssue(10, "缺少群标签", "设置群标签", "normal")
	}
	if group.CustomRate <= 0 || group.PayoutRate <= 0 {
		addIssue(15, "汇率配置异常", "检查汇率/费率", "high")
	}
	if strings.TrimSpace(group.PayoutAddress) == "" {
		addIssue(15, "未配置下发地址", "设置下发地址", "high")
	}
	if group.WelcomeEnabled == 1 && strings.TrimSpace(group.WelcomeText) == "" {
		addIssue(10, "欢迎语未配置", "设置新成员欢迎语", "normal")
	}
	if group.AutoReplyEnabled != 1 {
		addIssue(8, "自动回复未开启", "开启自动回复", "normal")
	}
	if closing.Enabled != 1 {
		addIssue(10, "下课提醒未启用", "开启下课提醒", "normal")
	}
	if row.Score < 0 {
		row.Score = 0
	}
	if len(row.Reasons) == 0 {
		row.Status = "healthy"
		row.Reasons = []string{"配置完整"}
		row.NextSteps = append(row.NextSteps, adminConsoleAction{
			ID:          strconv.FormatInt(group.ChatID, 10) + ":monitor",
			Title:       "继续观察",
			Description: "当前群配置完整，保持巡检即可。",
			Severity:    "info",
			Visibility:  "admin_only",
			ChatID:      group.ChatID,
			ActionURL:   "/admin/group?chat_id=" + strconv.FormatInt(group.ChatID, 10),
		})
	} else if row.Score >= 70 {
		row.Status = "attention"
	} else {
		row.Status = "risk"
	}
	return row
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	groupCount := 0
	groupErr := ""
	if s.db != nil {
		groups, err := s.db.ListAllGroups()
		if err != nil {
			groupErr = err.Error()
		} else {
			groupCount = len(groups)
		}
	}

	adminAddr := strings.TrimSpace(s.cfg.AdminAddr)
	if adminAddr == "" {
		adminAddr = "-"
	}
	timezone := strings.TrimSpace(s.cfg.Timezone)
	if timezone == "" {
		timezone = "-"
	}

	links := []struct {
		path  string
		label string
	}{
		{path: "/admin/groups", label: "群组管理"},
		{path: "/admin/permissions", label: "权限管理"},
		{path: "/admin/broadcast", label: "群发面板"},
		{path: "/admin/schedules", label: "定时群发"},
		{path: "/admin/automation", label: "自动化管理"},
		{path: "/admin/kill-switch", label: "紧急停机"},
	}

	var b strings.Builder
	if groupErr != "" {
		b.WriteString(`<div class="err">读取群数量失败：` + html.EscapeString(groupErr) + `</div>`)
	}
	b.WriteString(`<h2>首页状态</h2>`)
	b.WriteString(`<table><tbody>`)
	b.WriteString(`<tr><th style="width:180px">监听地址</th><td>` + html.EscapeString(adminAddr) + `</td></tr>`)
	b.WriteString(`<tr><th>时区</th><td>` + html.EscapeString(timezone) + `</td></tr>`)
	b.WriteString(`<tr><th>群数量</th><td>` + strconv.Itoa(groupCount) + `</td></tr>`)
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<h2>管理入口</h2>`)
	b.WriteString(`<table><thead><tr><th>模块</th><th>入口</th></tr></thead><tbody>`)
	for _, link := range links {
		href := s.withToken(link.path)
		b.WriteString(`<tr><td>` + html.EscapeString(link.label) + `</td><td><a href="` + html.EscapeString(href) + `">进入</a></td></tr>`)
	}
	b.WriteString(`</tbody></table>`)

	s.writePage(w, "后台首页", b.String())
}
