package admin

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go-bot/internal/bot"
	"go-bot/internal/db"
	"go-bot/internal/utils"
)

const adminTemplatePlanTTL = 10 * time.Minute

type adminTemplateVariable struct {
	Token string `json:"token"`
	Label string `json:"label"`
}

type adminWelcomeState struct {
	Enabled  bool   `json:"enabled"`
	Template string `json:"template"`
}

type adminWelcomeSample struct {
	UserID    int64  `json:"userId"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type adminTemplateRequest struct {
	Kind     string             `json:"kind"`
	ChatID   int64              `json:"chatId"`
	Enabled  *bool              `json:"enabled,omitempty"`
	Template string             `json:"template"`
	Sample   adminWelcomeSample `json:"sample"`
}

type adminTemplateApplyRequest struct {
	ApplyToken string `json:"applyToken"`
}

type adminTemplateRenderedPreview struct {
	Text      string `json:"text"`
	ParseMode string `json:"parseMode"`
}

type adminWelcomePlan struct {
	ChatID int64
	Title  string
	Before adminWelcomeState
	After  adminWelcomeState
	Sample adminWelcomeSample
}

type adminTemplatePlan struct {
	Username  string
	Kind      string
	Welcome   adminWelcomePlan
	AutoReply adminAutoReplyPlan
	Closing   adminClosingReminderPlan
	ExpiresAt time.Time
}

type adminTemplatePlanStore struct {
	mu    sync.Mutex
	plans map[string]adminTemplatePlan
	now   func() time.Time
}

func newAdminTemplatePlanStore() *adminTemplatePlanStore {
	return &adminTemplatePlanStore{
		plans: make(map[string]adminTemplatePlan),
		now:   time.Now,
	}
}

func (s *Server) handleAdminTemplatesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	chatID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("chat_id")), 10, 64)
	if err != nil || chatID == 0 {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "chat_id 格式错误"})
		return
	}
	group, err := s.db.GetGroup(chatID)
	if err != nil {
		s.writeAdminJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "群不存在"})
		return
	}
	autoReplies, err := s.db.ListAdminAutoReplies(chatID)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	closingReminder, err := s.db.GetClosingReminderConfig(chatID)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"chatId": chatID,
			"title":  adminSwitchGroupTitle(group),
			"welcome": adminWelcomeState{
				Enabled:  group.WelcomeEnabled == 1,
				Template: group.WelcomeText,
			},
			"autoReply": adminAutoReplyState{
				Enabled: group.AutoReplyEnabled == 1,
				Rules:   adminAutoReplyRules(autoReplies),
			},
			"closingReminder": adminClosingReminderStateFromConfig(closingReminder, group.DayCutTime),
			"variables":       adminWelcomeTemplateVariables(),
		},
	})
}

func (s *Server) handleAdminTemplateRender(w http.ResponseWriter, r *http.Request) {
	request, group, ok := s.readAdminWelcomeTemplateRequest(w, r, false)
	if !ok {
		return
	}
	preview := renderAdminWelcomePreview(group.Title, request.Template, request.Sample)
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"preview": preview,
		},
	})
}

type adminAutoReplyState struct {
	Enabled bool                 `json:"enabled"`
	Rules   []adminAutoReplyRule `json:"rules"`
}

type adminAutoReplyRule struct {
	ID        int64  `json:"id"`
	ChatID    int64  `json:"chatId"`
	Keyword   string `json:"keyword"`
	Content   string `json:"content"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
}

type adminAutoReplyRequest struct {
	ChatID     int64  `json:"chatId"`
	Action     string `json:"action"`
	RuleID     int64  `json:"ruleId"`
	Keyword    string `json:"keyword"`
	Content    string `json:"content"`
	Enabled    *bool  `json:"enabled,omitempty"`
	SampleText string `json:"sampleText"`
	Sample     struct {
		UserID    int64  `json:"userId"`
		Username  string `json:"username"`
		Nickname  string `json:"nickname"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	} `json:"sample"`
}

type adminAutoReplyPlan struct {
	ChatID    int64
	Title     string
	Action    string
	RuleID    int64
	HasBefore bool
	Before    db.AutoReply
	After     db.AutoReply
	Sample    adminAutoReplyRequest
}

func (s *Server) handleAdminAutoReplyRender(w http.ResponseWriter, r *http.Request) {
	request, ok := s.readAdminAutoReplyRequest(w, r)
	if !ok {
		return
	}
	preview, err := renderAdminAutoReplyPreview(request)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "自动回复模板格式错误: " + err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"preview": preview}})
}

func (s *Server) handleAdminAutoReplyPreview(w http.ResponseWriter, r *http.Request) {
	request, ok := s.readAdminAutoReplyRequest(w, r)
	if !ok {
		return
	}
	action := strings.ToLower(strings.TrimSpace(request.Action))
	if action == "" {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "action 不能为空"})
		return
	}
	group, err := s.db.GetGroup(request.ChatID)
	if err != nil {
		s.writeAdminJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "群不存在"})
		return
	}
	plan := adminAutoReplyPlan{ChatID: request.ChatID, Title: adminSwitchGroupTitle(group), Action: action, Sample: request}
	switch action {
	case "create":
		keyword, content, valid := s.normalizeAdminAutoReplyFields(w, request.Keyword, request.Content)
		if !valid {
			return
		}
		plan.After = db.AutoReply{ChatID: request.ChatID, Keyword: keyword, Content: content, Enabled: 1}
	case "update", "toggle", "delete":
		if request.RuleID == 0 {
			s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "ruleId 不能为 0"})
			return
		}
		before, err := s.db.GetAdminAutoReply(request.ChatID, request.RuleID)
		if err != nil {
			s.writeAdminJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "自动回复规则不存在"})
			return
		}
		plan.RuleID = request.RuleID
		plan.HasBefore = true
		plan.Before = before
		plan.After = before
		switch action {
		case "update":
			keyword, content, valid := s.normalizeAdminAutoReplyFields(w, request.Keyword, request.Content)
			if !valid {
				return
			}
			plan.After.Keyword = keyword
			plan.After.Content = content
			if request.Enabled != nil {
				plan.After.Enabled = adminBoolInt(*request.Enabled)
			}
		case "toggle":
			if request.Enabled == nil {
				s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "enabled 不能省略"})
				return
			}
			plan.After.Enabled = adminBoolInt(*request.Enabled)
			request.Content = before.Content
		case "delete":
			request.Content = before.Content
		}
	default:
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "不支持的自动回复操作"})
		return
	}
	preview, err := renderAdminAutoReplyPreview(request)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "自动回复模板格式错误: " + err.Error()})
		return
	}
	applyToken := s.saveAdminTemplatePlan(adminTemplatePlan{
		Username:  s.adminSessionUsername(r),
		Kind:      "auto_reply",
		AutoReply: plan,
	})
	data := map[string]any{
		"kind":         "auto_reply",
		"action":       action,
		"matchedCount": 1,
		"applyToken":   applyToken,
		"preview":      preview,
		"before":       nil,
		"after":        nil,
	}
	if plan.HasBefore {
		data["before"] = adminAutoReplyRuleFromDB(plan.Before)
	}
	if action != "delete" {
		data["after"] = adminAutoReplyRuleFromDB(plan.After)
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func (s *Server) readAdminAutoReplyRequest(w http.ResponseWriter, r *http.Request) (adminAutoReplyRequest, bool) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return adminAutoReplyRequest{}, false
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return adminAutoReplyRequest{}, false
	}
	var request adminAutoReplyRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return adminAutoReplyRequest{}, false
	}
	if request.ChatID == 0 {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "chatId 不能为 0"})
		return adminAutoReplyRequest{}, false
	}
	if _, err := s.db.GetGroup(request.ChatID); err != nil {
		s.writeAdminJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "群不存在"})
		return adminAutoReplyRequest{}, false
	}
	request.Keyword = strings.TrimSpace(request.Keyword)
	request.Content = strings.TrimSpace(request.Content)
	request.SampleText = strings.TrimSpace(request.SampleText)
	if request.SampleText == "" {
		request.SampleText = "示例消息"
	}
	if request.Sample.UserID == 0 {
		request.Sample.UserID = 10001
	}
	if request.Sample.Nickname == "" {
		request.Sample.Nickname = "示例用户"
	}
	return request, true
}

func (s *Server) normalizeAdminAutoReplyFields(w http.ResponseWriter, keyword, content string) (string, string, bool) {
	normalizedKeyword, err := utils.SanitizeAutomationKeyword(keyword, true)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return "", "", false
	}
	normalizedContent, err := utils.SanitizeAutomationContent(content)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return "", "", false
	}
	return normalizedKeyword, normalizedContent, true
}

func renderAdminAutoReplyPreview(request adminAutoReplyRequest) (adminTemplateRenderedPreview, error) {
	preview, err := bot.RenderAdminMessageTemplatePreview(request.Content, bot.AdminMessageTemplateSample{
		ChatID:   request.ChatID,
		UserID:   request.Sample.UserID,
		Username: strings.TrimPrefix(strings.TrimSpace(request.Sample.Username), "@"),
		Nickname: request.Sample.Nickname,
		Text:     request.SampleText,
	})
	if err != nil {
		return adminTemplateRenderedPreview{}, err
	}
	return adminTemplateRenderedPreview{Text: preview.Text, ParseMode: preview.ParseMode}, nil
}

func adminAutoReplyRules(rows []db.AutoReply) []adminAutoReplyRule {
	out := make([]adminAutoReplyRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminAutoReplyRuleFromDB(row))
	}
	return out
}

func adminAutoReplyRuleFromDB(row db.AutoReply) adminAutoReplyRule {
	return adminAutoReplyRule{ID: row.ID, ChatID: row.ChatID, Keyword: row.Keyword, Content: row.Content, Enabled: row.Enabled == 1, CreatedAt: row.CreatedAt}
}

type adminClosingReminderTexts struct {
	Normal  string `json:"normal"`
	Pending string `json:"pending"`
	NoData  string `json:"noData"`
	Review  string `json:"review"`
	Failed  string `json:"failed"`
}

type adminClosingReminderState struct {
	Enabled        bool                      `json:"enabled"`
	ReminderTime   string                    `json:"reminderTime"`
	ThresholdU     float64                   `json:"thresholdU"`
	DMMode         string                    `json:"dmMode"`
	DayCutTime     string                    `json:"dayCutTime"`
	EffectiveRunAt string                    `json:"effectiveRunAt"`
	Texts          adminClosingReminderTexts `json:"texts"`
}

type adminClosingReminderTextsPatch struct {
	Normal  *string `json:"normal,omitempty"`
	Pending *string `json:"pending,omitempty"`
	NoData  *string `json:"noData,omitempty"`
	Review  *string `json:"review,omitempty"`
	Failed  *string `json:"failed,omitempty"`
}

type adminClosingReminderPatch struct {
	Enabled      *bool                          `json:"enabled,omitempty"`
	ReminderTime *string                        `json:"reminderTime,omitempty"`
	ThresholdU   *float64                       `json:"thresholdU,omitempty"`
	Texts        adminClosingReminderTextsPatch `json:"texts"`
}

type adminClosingReminderRequest struct {
	ChatIDs        []int64                   `json:"chatIds,omitempty"`
	ExcludeChatIDs []int64                   `json:"excludeChatIds,omitempty"`
	Patch          adminClosingReminderPatch `json:"patch"`
}

type adminClosingReminderPlanItem struct {
	ChatID        int64
	Title         string
	DayCutTime    string
	ChangedFields []string
	Before        db.GroupClosingReminderConfig
	After         db.GroupClosingReminderConfig
}

type adminClosingReminderPlan struct {
	MatchedCount int
	Patch        adminClosingReminderPatch
	Items        []adminClosingReminderPlanItem
}

type adminClosingReminderPreviewItem struct {
	ChatID               int64                     `json:"chatId"`
	Title                string                    `json:"title"`
	ChangedFields        []string                  `json:"changedFields"`
	Before               adminClosingReminderState `json:"before"`
	After                adminClosingReminderState `json:"after"`
	DayCutTime           string                    `json:"dayCutTime"`
	BeforeEffectiveRunAt string                    `json:"beforeEffectiveRunAt"`
	AfterEffectiveRunAt  string                    `json:"afterEffectiveRunAt"`
}

type adminClosingReminderIssue struct {
	ChatID int64  `json:"chatId"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type adminClosingReminderReceipt struct {
	Kind         string                            `json:"kind"`
	MatchedCount int                               `json:"matchedCount"`
	UpdatedCount int                               `json:"updatedCount"`
	SkippedCount int                               `json:"skippedCount"`
	FailedCount  int                               `json:"failedCount"`
	Updated      []adminClosingReminderPreviewItem `json:"updated"`
	Skipped      []adminClosingReminderIssue       `json:"skipped"`
	Failed       []adminClosingReminderIssue       `json:"failed"`
}

func (s *Server) handleAdminClosingReminderPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	var request adminClosingReminderRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	patch, err := normalizeAdminClosingReminderPatch(request.Patch)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	chatIDs, err := normalizeAdminBatchChatIDs(request.ChatIDs)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	excludeChatIDs, err := normalizeAdminBatchChatIDs(request.ExcludeChatIDs)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	groups, err := s.db.ListAllGroups()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	requested := adminBatchChatIDSet(chatIDs)
	excluded := adminBatchChatIDSet(excludeChatIDs)
	if err := validateAdminClosingReminderTargets(groups, requested); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	matchedCount := 0
	affected := []adminClosingReminderPreviewItem{}
	skipped := []adminClosingReminderIssue{}
	planItems := []adminClosingReminderPlanItem{}
	for _, group := range groups {
		if len(requested) > 0 {
			if _, ok := requested[group.ChatID]; !ok {
				continue
			}
		}
		if _, skip := excluded[group.ChatID]; skip {
			continue
		}
		matchedCount++
		before, err := s.db.GetClosingReminderConfig(group.ChatID)
		if err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		after, changedFields := applyAdminClosingReminderPatch(before, patch)
		title := adminSwitchGroupTitle(group)
		if len(changedFields) == 0 {
			skipped = append(skipped, adminClosingReminderIssue{ChatID: group.ChatID, Title: title, Reason: "无需变更"})
			continue
		}
		item := adminClosingReminderPlanItem{
			ChatID:        group.ChatID,
			Title:         title,
			DayCutTime:    group.DayCutTime,
			ChangedFields: changedFields,
			Before:        before,
			After:         after,
		}
		planItems = append(planItems, item)
		affected = append(affected, adminClosingReminderPreviewItemFromPlan(item))
	}
	if matchedCount == 0 {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "未匹配到可操作的群"})
		return
	}
	applyToken := ""
	if len(planItems) > 0 {
		applyToken = s.saveAdminTemplatePlan(adminTemplatePlan{
			Username: s.adminSessionUsername(r),
			Kind:     "closing_reminder",
			Closing: adminClosingReminderPlan{
				MatchedCount: matchedCount,
				Patch:        patch,
				Items:        planItems,
			},
		})
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"kind":         "closing_reminder",
			"matchedCount": matchedCount,
			"affected":     affected,
			"skipped":      skipped,
			"applyToken":   applyToken,
		},
	})
}

func normalizeAdminClosingReminderPatch(patch adminClosingReminderPatch) (adminClosingReminderPatch, error) {
	changed := patch.Enabled != nil || patch.ReminderTime != nil || patch.ThresholdU != nil
	if patch.ReminderTime != nil {
		value := strings.TrimSpace(*patch.ReminderTime)
		if !adminSwitchTimePattern.MatchString(value) {
			return adminClosingReminderPatch{}, fmt.Errorf("提醒发送时间必须为 HH:MM")
		}
		patch.ReminderTime = &value
	}
	if patch.ThresholdU != nil {
		if math.IsNaN(*patch.ThresholdU) || math.IsInf(*patch.ThresholdU, 0) || *patch.ThresholdU <= 0 {
			return adminClosingReminderPatch{}, fmt.Errorf("下课阈值必须是大于 0 的有限数")
		}
	}
	for _, item := range []struct {
		label string
		value **string
	}{
		{label: "正常文案", value: &patch.Texts.Normal},
		{label: "待确认文案", value: &patch.Texts.Pending},
		{label: "无数据文案", value: &patch.Texts.NoData},
		{label: "复核文案", value: &patch.Texts.Review},
		{label: "失败文案", value: &patch.Texts.Failed},
	} {
		if *item.value == nil {
			continue
		}
		changed = true
		value := strings.TrimSpace(**item.value)
		if utf8.RuneCountInString(value) > 2000 {
			return adminClosingReminderPatch{}, fmt.Errorf("%s长度不能超过 2000 个字符", item.label)
		}
		if value != "" && !bot.IsAdminClosingReminderTextSafe(value) {
			return adminClosingReminderPatch{}, fmt.Errorf("%s包含敏感词或金额，请使用群内体面文案", item.label)
		}
		*item.value = &value
	}
	if !changed {
		return adminClosingReminderPatch{}, fmt.Errorf("下课提醒变更不能为空")
	}
	return patch, nil
}

func validateAdminClosingReminderTargets(groups []db.GroupConfig, requested map[int64]struct{}) error {
	if len(requested) == 0 {
		return nil
	}
	found := make(map[int64]struct{}, len(requested))
	for _, group := range groups {
		if _, ok := requested[group.ChatID]; ok {
			found[group.ChatID] = struct{}{}
		}
	}
	if len(found) != len(requested) {
		return fmt.Errorf("选中群包含不存在的群")
	}
	return nil
}

func applyAdminClosingReminderPatch(config db.GroupClosingReminderConfig, patch adminClosingReminderPatch) (db.GroupClosingReminderConfig, []string) {
	next := config
	changed := []string{}
	if patch.Enabled != nil {
		value := adminBoolInt(*patch.Enabled)
		if next.Enabled != value {
			next.Enabled = value
			changed = append(changed, "enabled")
		}
	}
	if patch.ReminderTime != nil && next.ReminderTime != *patch.ReminderTime {
		next.ReminderTime = *patch.ReminderTime
		changed = append(changed, "reminderTime")
	}
	if patch.ThresholdU != nil && next.ThresholdU != *patch.ThresholdU {
		next.ThresholdU = *patch.ThresholdU
		changed = append(changed, "thresholdU")
	}
	applyText := func(field string, target *string, value *string) {
		if value != nil && *target != *value {
			*target = *value
			changed = append(changed, "texts."+field)
		}
	}
	applyText("normal", &next.TextNormal, patch.Texts.Normal)
	applyText("pending", &next.TextPending, patch.Texts.Pending)
	applyText("noData", &next.TextNoData, patch.Texts.NoData)
	applyText("review", &next.TextReview, patch.Texts.Review)
	applyText("failed", &next.TextFailed, patch.Texts.Failed)
	return next, changed
}

func adminClosingReminderConfigMatchesPatch(current, expected db.GroupClosingReminderConfig, patch adminClosingReminderPatch) bool {
	if patch.Enabled != nil && current.Enabled != expected.Enabled {
		return false
	}
	if patch.ReminderTime != nil && current.ReminderTime != expected.ReminderTime {
		return false
	}
	if patch.ThresholdU != nil && current.ThresholdU != expected.ThresholdU {
		return false
	}
	for _, item := range []struct {
		patch   *string
		current string
		expect  string
	}{
		{patch: patch.Texts.Normal, current: current.TextNormal, expect: expected.TextNormal},
		{patch: patch.Texts.Pending, current: current.TextPending, expect: expected.TextPending},
		{patch: patch.Texts.NoData, current: current.TextNoData, expect: expected.TextNoData},
		{patch: patch.Texts.Review, current: current.TextReview, expect: expected.TextReview},
		{patch: patch.Texts.Failed, current: current.TextFailed, expect: expected.TextFailed},
	} {
		if item.patch != nil && item.current != item.expect {
			return false
		}
	}
	return true
}

func adminClosingReminderPreviewItemFromPlan(item adminClosingReminderPlanItem) adminClosingReminderPreviewItem {
	before := adminClosingReminderStateFromConfig(item.Before, item.DayCutTime)
	after := adminClosingReminderStateFromConfig(item.After, item.DayCutTime)
	return adminClosingReminderPreviewItem{
		ChatID:               item.ChatID,
		Title:                item.Title,
		ChangedFields:        item.ChangedFields,
		Before:               before,
		After:                after,
		DayCutTime:           item.DayCutTime,
		BeforeEffectiveRunAt: before.EffectiveRunAt,
		AfterEffectiveRunAt:  after.EffectiveRunAt,
	}
}

func adminClosingReminderStateFromConfig(config db.GroupClosingReminderConfig, dayCutTime string) adminClosingReminderState {
	return adminClosingReminderState{
		Enabled:        config.Enabled == 1,
		ReminderTime:   config.ReminderTime,
		ThresholdU:     config.ThresholdU,
		DMMode:         config.DMMode,
		DayCutTime:     dayCutTime,
		EffectiveRunAt: adminClosingReminderEffectiveRunAt(config.ReminderTime, dayCutTime),
		Texts: adminClosingReminderTexts{
			Normal:  config.TextNormal,
			Pending: config.TextPending,
			NoData:  config.TextNoData,
			Review:  config.TextReview,
			Failed:  config.TextFailed,
		},
	}
}

func adminClosingReminderEffectiveRunAt(reminderTime, dayCutTime string) string {
	reminderSeconds := adminClockSeconds(reminderTime, 4*60*60)
	dayCutSeconds := adminClockSeconds(dayCutTime, 4*60*60)
	if dayCutSeconds > reminderSeconds {
		reminderSeconds = dayCutSeconds
	}
	return fmt.Sprintf("%02d:%02d:%02d", reminderSeconds/3600, reminderSeconds%3600/60, reminderSeconds%60)
}

func adminClockSeconds(raw string, fallback int) int {
	for _, layout := range []string{"15:04:05", "15:04"} {
		parsed, err := time.Parse(layout, strings.TrimSpace(raw))
		if err == nil {
			return parsed.Hour()*3600 + parsed.Minute()*60 + parsed.Second()
		}
	}
	return fallback
}

func (s *Server) handleAdminTemplatePreview(w http.ResponseWriter, r *http.Request) {
	request, group, ok := s.readAdminWelcomeTemplateRequest(w, r, true)
	if !ok {
		return
	}
	before := adminWelcomeState{Enabled: group.WelcomeEnabled == 1, Template: group.WelcomeText}
	after := adminWelcomeState{Enabled: *request.Enabled, Template: request.Template}
	preview := renderAdminWelcomePreview(group.Title, request.Template, request.Sample)
	applyToken := ""
	if before != after {
		applyToken = s.saveAdminTemplatePlan(adminTemplatePlan{
			Username: s.adminSessionUsername(r),
			Kind:     "welcome",
			Welcome: adminWelcomePlan{
				ChatID: request.ChatID,
				Title:  adminSwitchGroupTitle(group),
				Before: before,
				After:  after,
				Sample: request.Sample,
			},
		})
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"kind":         "welcome",
			"matchedCount": 1,
			"before":       before,
			"after":        after,
			"preview":      preview,
			"applyToken":   applyToken,
		},
	})
}

func (s *Server) handleAdminTemplateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	var request adminTemplateApplyRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	plan, ok := s.takeAdminTemplatePlan(s.adminSessionUsername(r), request.ApplyToken)
	if !ok {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "预演已过期，请重新预演"})
		return
	}
	if plan.Kind == "welcome" {
		updated, err := s.db.UpdateGroupWelcomeIfCurrent(
			plan.Welcome.ChatID,
			adminBoolInt(plan.Welcome.Before.Enabled),
			plan.Welcome.Before.Template,
			adminBoolInt(plan.Welcome.After.Enabled),
			plan.Welcome.After.Template,
		)
		if err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if !updated {
			s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "欢迎语配置已变化，请重新预演"})
			return
		}
		_ = s.db.AddAuditLog(plan.Welcome.ChatID, 0, 0, "admin_welcome_template_apply", fmt.Sprintf("username=%s enabled=%t", plan.Username, plan.Welcome.After.Enabled))
		s.writeAdminJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"data": map[string]any{
				"kind":         "welcome",
				"matchedCount": 1,
				"updatedCount": 1,
			},
		})
		return
	}
	if plan.Kind == "closing_reminder" {
		s.applyAdminClosingReminderPlan(w, plan)
		return
	}
	if plan.Kind != "auto_reply" {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "预演类型不支持"})
		return
	}
	s.applyAdminAutoReplyPlan(w, plan)
}

func (s *Server) applyAdminAutoReplyPlan(w http.ResponseWriter, plan adminTemplatePlan) {
	item := plan.AutoReply
	var updatedRule *adminAutoReplyRule
	switch item.Action {
	case "create":
		created, err := s.db.CreateAdminAutoReply(item.ChatID, item.After.Keyword, item.After.Content, item.After.Enabled == 1)
		if err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		rule := adminAutoReplyRuleFromDB(created)
		updatedRule = &rule
	case "update", "toggle":
		updated, err := s.db.UpdateAdminAutoReplyIfCurrent(item.ChatID, item.RuleID, item.Before, item.After)
		if err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if !updated {
			s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "自动回复规则已变化，请重新预演"})
			return
		}
		rule := adminAutoReplyRuleFromDB(item.After)
		rule.ID = item.RuleID
		rule.ChatID = item.ChatID
		updatedRule = &rule
	case "delete":
		deleted, err := s.db.DeleteAdminAutoReplyIfCurrent(item.ChatID, item.RuleID, item.Before)
		if err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if !deleted {
			s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "自动回复规则已变化，请重新预演"})
			return
		}
	default:
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "自动回复操作不支持"})
		return
	}
	_ = s.db.AddAuditLog(item.ChatID, 0, 0, "admin_auto_reply_apply", fmt.Sprintf("username=%s action=%s rule=%d", plan.Username, item.Action, item.RuleID))
	data := map[string]any{"kind": "auto_reply", "action": item.Action, "matchedCount": 1, "updatedCount": 1}
	if updatedRule != nil {
		data["rule"] = updatedRule
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func (s *Server) applyAdminClosingReminderPlan(w http.ResponseWriter, plan adminTemplatePlan) {
	item := plan.Closing
	receipt := adminClosingReminderReceipt{
		Kind:         "closing_reminder",
		MatchedCount: item.MatchedCount,
		Updated:      []adminClosingReminderPreviewItem{},
		Skipped:      []adminClosingReminderIssue{},
		Failed:       []adminClosingReminderIssue{},
	}
	for _, planned := range item.Items {
		group, err := s.db.GetGroup(planned.ChatID)
		if err != nil {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminClosingReminderIssue{ChatID: planned.ChatID, Title: planned.Title, Reason: err.Error()})
			continue
		}
		if strings.TrimSpace(group.DayCutTime) != strings.TrimSpace(planned.DayCutTime) {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminClosingReminderIssue{ChatID: planned.ChatID, Title: planned.Title, Reason: "日切时间已变化，请重新预演"})
			continue
		}
		current, err := s.db.GetClosingReminderConfig(planned.ChatID)
		if err != nil {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminClosingReminderIssue{ChatID: planned.ChatID, Title: planned.Title, Reason: err.Error()})
			continue
		}
		if adminClosingReminderConfigMatchesPatch(current, planned.After, item.Patch) {
			receipt.SkippedCount++
			receipt.Skipped = append(receipt.Skipped, adminClosingReminderIssue{ChatID: planned.ChatID, Title: planned.Title, Reason: "已是目标值"})
			continue
		}
		if !adminClosingReminderConfigMatchesPatch(current, planned.Before, item.Patch) {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminClosingReminderIssue{ChatID: planned.ChatID, Title: planned.Title, Reason: "配置已变化，请重新预演"})
			continue
		}
		next, changedFields := applyAdminClosingReminderPatch(current, item.Patch)
		if len(changedFields) == 0 {
			receipt.SkippedCount++
			receipt.Skipped = append(receipt.Skipped, adminClosingReminderIssue{ChatID: planned.ChatID, Title: planned.Title, Reason: "无需变更"})
			continue
		}
		if err := s.db.UpsertClosingReminderConfig(next); err != nil {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminClosingReminderIssue{ChatID: planned.ChatID, Title: planned.Title, Reason: err.Error()})
			continue
		}
		receipt.UpdatedCount++
		receipt.Updated = append(receipt.Updated, adminClosingReminderPreviewItemFromPlan(adminClosingReminderPlanItem{
			ChatID:        planned.ChatID,
			Title:         planned.Title,
			DayCutTime:    group.DayCutTime,
			ChangedFields: changedFields,
			Before:        current,
			After:         next,
		}))
	}
	if receipt.FailedCount > 0 {
		retryOperationID := fmt.Sprintf("closing_reminder:%d", time.Now().UnixNano())
		for _, failed := range receipt.Failed {
			for _, planned := range item.Items {
				if planned.ChatID != failed.ChatID {
					continue
				}
				s.recordAdminRetryFailure(retryOperationID, fmt.Sprintf("closing_reminder:%d", planned.ChatID), "closing_reminder", planned.ChatID, map[string]any{
					"chatId":     planned.ChatID,
					"dayCutTime": planned.DayCutTime,
					"patch":      item.Patch,
					"before":     planned.Before,
					"after":      planned.After,
				}, failed.Reason)
				break
			}
		}
	}
	if receipt.UpdatedCount > 0 {
		_ = s.db.AddAuditLog(0, 0, 0, "admin_closing_reminder_apply", fmt.Sprintf("username=%s updated=%d skipped=%d failed=%d", plan.Username, receipt.UpdatedCount, receipt.SkippedCount, receipt.FailedCount))
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": receipt})
}

func (s *Server) readAdminWelcomeTemplateRequest(w http.ResponseWriter, r *http.Request, requireEnabled bool) (adminTemplateRequest, db.GroupConfig, bool) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return adminTemplateRequest{}, db.GroupConfig{}, false
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return adminTemplateRequest{}, db.GroupConfig{}, false
	}
	var request adminTemplateRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return adminTemplateRequest{}, db.GroupConfig{}, false
	}
	if strings.ToLower(strings.TrimSpace(request.Kind)) != "welcome" {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "模板类型不支持"})
		return adminTemplateRequest{}, db.GroupConfig{}, false
	}
	request.Template = strings.TrimSpace(request.Template)
	if request.ChatID == 0 {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "chatId 不能为 0"})
		return adminTemplateRequest{}, db.GroupConfig{}, false
	}
	if request.Template == "" {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "欢迎语模板不能为空"})
		return adminTemplateRequest{}, db.GroupConfig{}, false
	}
	if utf8.RuneCountInString(request.Template) > 2000 {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "欢迎语模板长度不能超过 2000 个字符"})
		return adminTemplateRequest{}, db.GroupConfig{}, false
	}
	if requireEnabled && request.Enabled == nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "enabled 不能省略"})
		return adminTemplateRequest{}, db.GroupConfig{}, false
	}
	group, err := s.db.GetGroup(request.ChatID)
	if err != nil {
		s.writeAdminJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "群不存在"})
		return adminTemplateRequest{}, db.GroupConfig{}, false
	}
	request.Sample.Username = strings.TrimSpace(strings.TrimPrefix(request.Sample.Username, "@"))
	request.Sample.FirstName = strings.TrimSpace(request.Sample.FirstName)
	request.Sample.LastName = strings.TrimSpace(request.Sample.LastName)
	if request.Sample.UserID == 0 {
		request.Sample.UserID = 10001
	}
	if request.Sample.Username == "" && request.Sample.FirstName == "" && request.Sample.LastName == "" {
		request.Sample.FirstName = "新成员"
	}
	return request, group, true
}

func renderAdminWelcomePreview(groupName, template string, sample adminWelcomeSample) adminTemplateRenderedPreview {
	return adminTemplateRenderedPreview{
		Text: bot.RenderWelcomeTemplatePreview(groupName, template, bot.WelcomeTemplateSample{
			UserID:    sample.UserID,
			Username:  sample.Username,
			FirstName: sample.FirstName,
			LastName:  sample.LastName,
		}),
		ParseMode: "HTML",
	}
}

func adminWelcomeTemplateVariables() []adminTemplateVariable {
	return []adminTemplateVariable{
		{Token: "{group_name}", Label: "群名"},
		{Token: "{user_mention}", Label: "可点击成员"},
		{Token: "{user_id}", Label: "成员 ID"},
		{Token: "{first_name}", Label: "成员名字"},
	}
}

func adminBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Server) saveAdminTemplatePlan(plan adminTemplatePlan) string {
	store := s.ensureAdminTemplatePlanStore()
	token := newAdminSwitchToken()
	store.mu.Lock()
	defer store.mu.Unlock()
	plan.ExpiresAt = store.now().Add(adminTemplatePlanTTL)
	store.plans[token] = plan
	return token
}

func (s *Server) takeAdminTemplatePlan(username, token string) (adminTemplatePlan, bool) {
	store := s.ensureAdminTemplatePlanStore()
	username = strings.TrimSpace(username)
	token = strings.TrimSpace(token)
	if token == "" {
		return adminTemplatePlan{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now()
	for key, plan := range store.plans {
		if !plan.ExpiresAt.After(now) {
			delete(store.plans, key)
		}
	}
	plan, ok := store.plans[token]
	if !ok {
		return adminTemplatePlan{}, false
	}
	delete(store.plans, token)
	if strings.TrimSpace(plan.Username) != username || !plan.ExpiresAt.After(now) {
		return adminTemplatePlan{}, false
	}
	return plan, true
}

func (s *Server) ensureAdminTemplatePlanStore() *adminTemplatePlanStore {
	if s.templatePlans == nil {
		s.templatePlans = newAdminTemplatePlanStore()
	}
	return s.templatePlans
}
