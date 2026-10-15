package admin

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-bot/internal/db"
)

const adminSwitchPlanTTL = 10 * time.Minute

var adminSwitchTimePattern = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)

type adminSwitchRequest struct {
	Feature        string  `json:"feature"`
	Enabled        *bool   `json:"enabled,omitempty"`
	Value          string  `json:"value,omitempty"`
	ChatIDs        []int64 `json:"chatIds,omitempty"`
	ExcludeChatIDs []int64 `json:"excludeChatIds,omitempty"`
}

type adminSwitchApplyRequest struct {
	ApplyToken string `json:"applyToken"`
}

type adminSwitchRollbackRequest struct {
	RollbackToken string `json:"rollbackToken"`
}

type adminSwitchItem struct {
	ChatID int64  `json:"chatId"`
	Title  string `json:"title"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type adminSwitchSkip struct {
	ChatID int64  `json:"chatId"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type adminSwitchPreview struct {
	Feature      string            `json:"feature"`
	MatchedCount int               `json:"matchedCount"`
	Affected     []adminSwitchItem `json:"affected"`
	Skipped      []adminSwitchSkip `json:"skipped"`
	ApplyToken   string            `json:"applyToken"`
}

type adminSwitchReceipt struct {
	Feature       string            `json:"feature"`
	MatchedCount  int               `json:"matchedCount"`
	UpdatedCount  int               `json:"updatedCount"`
	SkippedCount  int               `json:"skippedCount"`
	FailedCount   int               `json:"failedCount"`
	Updated       []adminSwitchItem `json:"updated"`
	Skipped       []adminSwitchSkip `json:"skipped"`
	Failed        []adminSwitchSkip `json:"failed"`
	RollbackToken string            `json:"rollbackToken,omitempty"`
}

type adminSwitchSummary struct {
	Feature       string `json:"feature"`
	Label         string `json:"label"`
	EnabledCount  int    `json:"enabledCount"`
	DisabledCount int    `json:"disabledCount"`
}

type adminSwitchGroupDetail struct {
	ChatID  int64  `json:"chatId"`
	Title   string `json:"title"`
	Enabled bool   `json:"enabled"`
}

type adminSwitchPlan struct {
	Username      string
	SessionHandle string
	Feature       string
	Items         []adminSwitchItem
	ExpiresAt     time.Time
}

type adminSwitchPlanStore struct {
	mu        sync.Mutex
	previews  map[string]adminSwitchPlan
	rollbacks map[string]adminSwitchPlan
	now       func() time.Time
}

func newAdminSwitchPlanStore() *adminSwitchPlanStore {
	return &adminSwitchPlanStore{
		previews:  make(map[string]adminSwitchPlan),
		rollbacks: make(map[string]adminSwitchPlan),
		now:       time.Now,
	}
}

func (s *Server) handleAdminSwitchFeatureAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	feature := strings.TrimPrefix(r.URL.Path, "/admin/api/switches/")
	if strings.Contains(feature, "/") {
		feature = ""
	}
	feature = strings.ToLower(strings.TrimSpace(feature))
	if !adminSwitchFeatureSupported(feature) {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "不支持的全局功能"})
		return
	}
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	if status == "" {
		status = "all"
	}
	if status != "all" && status != "enabled" && status != "disabled" {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "非法状态筛选"})
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) > 80 {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "搜索条件不能超过 80 个字符"})
		return
	}
	page, pageSize, err := normalizeAdminSwitchPagination(r.URL.Query().Get("page"), r.URL.Query().Get("pageSize"))
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	groups, err := s.db.ListAllGroups()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	type switchRow struct {
		group   db.GroupConfig
		enabled bool
	}
	rows := make([]switchRow, 0, len(groups))
	needle := strings.ToLower(query)
	for _, group := range groups {
		enabled, err := s.adminSwitchEnabled(group, feature)
		if err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		title := adminSwitchGroupTitle(group)
		if needle != "" && !strings.Contains(strings.ToLower(title), needle) && !strings.Contains(strconv.FormatInt(group.ChatID, 10), needle) {
			continue
		}
		if status == "enabled" && !enabled || status == "disabled" && enabled {
			continue
		}
		rows = append(rows, switchRow{group: group, enabled: enabled})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left := strings.ToLower(adminSwitchGroupTitle(rows[i].group))
		right := strings.ToLower(adminSwitchGroupTitle(rows[j].group))
		if left == right {
			return rows[i].group.ChatID < rows[j].group.ChatID
		}
		return left < right
	})
	enabledCount := 0
	for _, row := range rows {
		if row.enabled {
			enabledCount++
		}
	}
	offset := (page - 1) * pageSize
	items := make([]adminSwitchGroupDetail, 0, pageSize)
	if offset < len(rows) {
		end := offset + pageSize
		if end > len(rows) {
			end = len(rows)
		}
		for _, row := range rows[offset:end] {
			items = append(items, adminSwitchGroupDetail{ChatID: row.group.ChatID, Title: adminSwitchGroupTitle(row.group), Enabled: row.enabled})
		}
	}
	label := feature
	for _, summary := range adminSwitchFeatureSummaries() {
		if summary.Feature == feature {
			label = summary.Label
			break
		}
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{
		"feature": feature, "label": label, "total": len(rows),
		"enabledCount": enabledCount, "disabledCount": len(rows) - enabledCount,
		"page": page, "pageSize": pageSize, "readAt": time.Now().UTC().Format(time.RFC3339), "groups": items,
	}})
}

func adminSwitchFeatureSummaries() []adminSwitchSummary {
	return []adminSwitchSummary{
		{Feature: "bookkeeping", Label: "记账"},
		{Feature: "ledger_card_hide_reversed", Label: "撤回记录从账单卡片隐藏"},
		{Feature: "welcome", Label: "新成员欢迎"},
		{Feature: "auto_reply", Label: "自动回复"},
		{Feature: "at_broadcast", Label: "@群发"},
		{Feature: "closing_reminder", Label: "下课提醒"},
	}
}

func adminSwitchFeatureSupported(feature string) bool {
	for _, summary := range adminSwitchFeatureSummaries() {
		if summary.Feature == feature {
			return true
		}
	}
	return false
}

func normalizeAdminSwitchPagination(rawPage, rawPageSize string) (int, int, error) {
	page, pageSize := 1, 20
	var err error
	if strings.TrimSpace(rawPage) != "" {
		page, err = strconv.Atoi(rawPage)
		if err != nil || page < 1 {
			return 0, 0, fmt.Errorf("页码必须是正整数")
		}
	}
	if strings.TrimSpace(rawPageSize) != "" {
		pageSize, err = strconv.Atoi(rawPageSize)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return 0, 0, fmt.Errorf("每页数量必须在 1 到 100 之间")
		}
	}
	return page, pageSize, nil
}

func (s *Server) handleAdminSwitchesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"data": map[string]any{"totalGroups": 0, "switches": []adminSwitchSummary{}},
		})
		return
	}
	groups, err := s.db.ListAllGroups()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	summaries := []adminSwitchSummary{
		{Feature: "bookkeeping", Label: "记账"},
		{Feature: "ledger_card_hide_reversed", Label: "撤回记录从账单卡片隐藏"},
		{Feature: "welcome", Label: "新成员欢迎"},
		{Feature: "auto_reply", Label: "自动回复"},
		{Feature: "at_broadcast", Label: "@群发"},
		{Feature: "closing_reminder", Label: "下课提醒"},
	}
	for _, group := range groups {
		for index := range summaries {
			enabled, err := s.adminSwitchEnabled(group, summaries[index].Feature)
			if err != nil {
				s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			if enabled {
				summaries[index].EnabledCount++
			} else {
				summaries[index].DisabledCount++
			}
		}
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": map[string]any{"totalGroups": len(groups), "switches": summaries},
	})
}

func (s *Server) handleAdminSwitchPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	var request adminSwitchRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	feature, value, err := normalizeAdminSwitchRequest(request)
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
	selected := adminBatchChatIDSet(chatIDs)
	excluded := adminBatchChatIDSet(excludeChatIDs)
	preview := adminSwitchPreview{
		Feature:  feature,
		Affected: []adminSwitchItem{},
		Skipped:  []adminSwitchSkip{},
	}
	for _, group := range groups {
		if len(selected) > 0 {
			if _, ok := selected[group.ChatID]; !ok {
				continue
			}
		}
		if _, skip := excluded[group.ChatID]; skip {
			continue
		}
		preview.MatchedCount++
		before, err := s.adminSwitchValue(group, feature)
		if err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		title := adminSwitchGroupTitle(group)
		if before == value {
			preview.Skipped = append(preview.Skipped, adminSwitchSkip{ChatID: group.ChatID, Title: title, Reason: "无需变更"})
			continue
		}
		preview.Affected = append(preview.Affected, adminSwitchItem{
			ChatID: group.ChatID,
			Title:  title,
			Before: before,
			After:  value,
		})
	}
	preview.ApplyToken = s.saveAdminSwitchPreview(adminSwitchPlan{
		Username:      s.adminSessionUsername(r),
		SessionHandle: s.adminSessionHandle(r),
		Feature:       feature,
		Items:         preview.Affected,
	})
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": preview})
}

func (s *Server) handleAdminSwitchApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	var request adminSwitchApplyRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	plan, ok := s.takeAdminSwitchPlan(s.adminSessionUsername(r), s.adminSessionHandle(r), request.ApplyToken, false)
	if !ok {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "预演已过期，请重新预演"})
		return
	}
	receipt := s.applyAdminSwitchPlan(plan, false)
	s.recordAdminSwitchRetryItems(plan, receipt)
	s.recordAdminSwitchSnapshot(plan, receipt, "admin_global_switch_apply")
	if receipt.UpdatedCount > 0 {
		rollbackItems := make([]adminSwitchItem, 0, len(receipt.Updated))
		for _, item := range receipt.Updated {
			rollbackItems = append(rollbackItems, adminSwitchItem{
				ChatID: item.ChatID,
				Title:  item.Title,
				Before: item.After,
				After:  item.Before,
			})
		}
		receipt.RollbackToken = s.saveAdminSwitchRollback(adminSwitchPlan{
			Username:      plan.Username,
			SessionHandle: plan.SessionHandle,
			Feature:       plan.Feature,
			Items:         rollbackItems,
		})
		_ = s.db.AddAuditLog(0, 0, 0, "admin_global_switch_apply", fmt.Sprintf("feature=%s updated=%d skipped=%d failed=%d", plan.Feature, receipt.UpdatedCount, receipt.SkippedCount, receipt.FailedCount))
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": receipt})
}

func (s *Server) handleAdminSwitchRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	var request adminSwitchRollbackRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	plan, ok := s.takeAdminSwitchPlan(s.adminSessionUsername(r), s.adminSessionHandle(r), request.RollbackToken, true)
	if !ok {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "恢复令牌已过期或已使用"})
		return
	}
	receipt := s.applyAdminSwitchPlan(plan, true)
	s.recordAdminSwitchSnapshot(plan, receipt, "admin_global_switch_rollback")
	if receipt.UpdatedCount > 0 {
		_ = s.db.AddAuditLog(0, 0, 0, "admin_global_switch_rollback", fmt.Sprintf("feature=%s updated=%d failed=%d", plan.Feature, receipt.UpdatedCount, receipt.FailedCount))
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": receipt})
}

func (s *Server) applyAdminSwitchPlan(plan adminSwitchPlan, rollback bool) adminSwitchReceipt {
	receipt := adminSwitchReceipt{
		Feature:      plan.Feature,
		MatchedCount: len(plan.Items),
		Updated:      []adminSwitchItem{},
		Skipped:      []adminSwitchSkip{},
		Failed:       []adminSwitchSkip{},
	}
	for _, item := range plan.Items {
		group, err := s.db.GetGroup(item.ChatID)
		if err != nil {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminSwitchSkip{ChatID: item.ChatID, Title: item.Title, Reason: err.Error()})
			continue
		}
		current, err := s.adminSwitchValue(group, plan.Feature)
		if err != nil {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminSwitchSkip{ChatID: item.ChatID, Title: item.Title, Reason: err.Error()})
			continue
		}
		if current == item.After {
			receipt.SkippedCount++
			receipt.Skipped = append(receipt.Skipped, adminSwitchSkip{ChatID: item.ChatID, Title: item.Title, Reason: "无需变更"})
			continue
		}
		if current != item.Before {
			receipt.FailedCount++
			reason := "配置已变化，请重新预演"
			if rollback {
				reason = "配置已在恢复前被修改，已跳过"
			}
			receipt.Failed = append(receipt.Failed, adminSwitchSkip{ChatID: item.ChatID, Title: item.Title, Reason: reason})
			continue
		}
		if err := s.writeAdminSwitchValue(group, plan.Feature, item.After); err != nil {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminSwitchSkip{ChatID: item.ChatID, Title: item.Title, Reason: err.Error()})
			continue
		}
		if plan.Feature == "ledger_card_hide_reversed" && s.app != nil {
			s.app.ClearBookkeepingResultCache(item.ChatID)
		}
		receipt.UpdatedCount++
		receipt.Updated = append(receipt.Updated, item)
	}
	return receipt
}

func normalizeAdminSwitchRequest(request adminSwitchRequest) (string, string, error) {
	feature := strings.ToLower(strings.TrimSpace(request.Feature))
	switch feature {
	case "welcome", "welcome_enabled":
		feature = "welcome"
	case "auto_reply", "auto_reply_enabled":
		feature = "auto_reply"
	case "bookkeeping", "bookkeeping_enabled":
		feature = "bookkeeping"
	case "ledger_card_hide_reversed":
		feature = "ledger_card_hide_reversed"
	case "at_broadcast", "at_broadcast_enabled":
		feature = "at_broadcast"
	case "closing_reminder", "closing_reminder_enabled":
		feature = "closing_reminder"
	case "closing_reminder_time":
		value := strings.TrimSpace(request.Value)
		if !adminSwitchTimePattern.MatchString(value) {
			return "", "", fmt.Errorf("下课提醒时间格式必须为 HH:MM")
		}
		return feature, value, nil
	case "welcome_text":
		value := strings.TrimSpace(request.Value)
		if value == "" || len([]rune(value)) > 2000 {
			return "", "", fmt.Errorf("欢迎模板不能为空且不能超过 2000 个字符")
		}
		return feature, value, nil
	default:
		return "", "", fmt.Errorf("不支持的全局功能")
	}
	if request.Enabled == nil {
		return "", "", fmt.Errorf("开关操作必须提供 enabled")
	}
	if *request.Enabled {
		return feature, "1", nil
	}
	return feature, "0", nil
}

func (s *Server) adminSwitchEnabled(group db.GroupConfig, feature string) (bool, error) {
	value, err := s.adminSwitchValue(group, feature)
	return value == "1", err
}

func (s *Server) adminSwitchValue(group db.GroupConfig, feature string) (string, error) {
	switch feature {
	case "welcome":
		return strconv.Itoa(normalizeAdminSwitchBit(group.WelcomeEnabled)), nil
	case "auto_reply":
		return strconv.Itoa(normalizeAdminSwitchBit(group.AutoReplyEnabled)), nil
	case "bookkeeping":
		return strconv.Itoa(normalizeAdminSwitchBit(group.BookkeepingEnabled)), nil
	case "ledger_card_hide_reversed":
		return strconv.Itoa(normalizeAdminSwitchBit(group.LedgerCardHideReversed)), nil
	case "at_broadcast":
		return strconv.Itoa(normalizeAdminSwitchBit(group.AtBroadcastEnabled)), nil
	case "welcome_text":
		return strings.TrimSpace(group.WelcomeText), nil
	case "closing_reminder", "closing_reminder_time":
		config, err := s.db.GetClosingReminderConfig(group.ChatID)
		if err != nil {
			return "", err
		}
		if feature == "closing_reminder" {
			return strconv.Itoa(normalizeAdminSwitchBit(config.Enabled)), nil
		}
		return strings.TrimSpace(config.ReminderTime), nil
	default:
		return "", fmt.Errorf("不支持的全局功能")
	}
}

func (s *Server) writeAdminSwitchValue(group db.GroupConfig, feature, value string) error {
	switch feature {
	case "welcome":
		return s.db.UpdateGroupFields(group.ChatID, map[string]any{"welcome_enabled": adminSwitchIntValue(value)})
	case "auto_reply":
		return s.db.UpdateGroupFields(group.ChatID, map[string]any{"auto_reply_enabled": adminSwitchIntValue(value)})
	case "bookkeeping":
		return s.db.UpdateGroupFields(group.ChatID, map[string]any{"bookkeeping_enabled": adminSwitchIntValue(value)})
	case "ledger_card_hide_reversed":
		return s.db.UpdateGroupFields(group.ChatID, map[string]any{"ledger_card_hide_reversed": adminSwitchIntValue(value)})
	case "at_broadcast":
		return s.db.UpdateGroupFields(group.ChatID, map[string]any{"at_broadcast_enabled": adminSwitchIntValue(value)})
	case "welcome_text":
		return s.db.UpdateGroupFields(group.ChatID, map[string]any{"welcome_text": value})
	case "closing_reminder", "closing_reminder_time":
		config, err := s.db.GetClosingReminderConfig(group.ChatID)
		if err != nil {
			return err
		}
		if feature == "closing_reminder" {
			config.Enabled = adminSwitchIntValue(value)
		} else {
			config.ReminderTime = value
		}
		return s.db.UpsertClosingReminderConfig(config)
	default:
		return fmt.Errorf("不支持的全局功能")
	}
}

func normalizeAdminSwitchBit(value int) int {
	if value == 1 {
		return 1
	}
	return 0
}

func adminSwitchIntValue(value string) int {
	if value == "1" {
		return 1
	}
	return 0
}

func adminSwitchGroupTitle(group db.GroupConfig) string {
	title := strings.TrimSpace(group.Title)
	if title == "" {
		return strconv.FormatInt(group.ChatID, 10)
	}
	return title
}

func (s *Server) saveAdminSwitchPreview(plan adminSwitchPlan) string {
	store := s.ensureAdminSwitchPlanStore()
	token := newAdminSwitchToken()
	store.mu.Lock()
	defer store.mu.Unlock()
	plan.ExpiresAt = store.now().Add(adminSwitchPlanTTL)
	store.previews[token] = plan
	return token
}

func (s *Server) saveAdminSwitchRollback(plan adminSwitchPlan) string {
	store := s.ensureAdminSwitchPlanStore()
	token := newAdminSwitchToken()
	store.mu.Lock()
	defer store.mu.Unlock()
	plan.ExpiresAt = store.now().Add(adminSwitchPlanTTL)
	store.rollbacks[token] = plan
	return token
}

func (s *Server) takeAdminSwitchPlan(username, sessionHandle, token string, rollback bool) (adminSwitchPlan, bool) {
	store := s.ensureAdminSwitchPlanStore()
	username = strings.TrimSpace(username)
	token = strings.TrimSpace(token)
	if token == "" {
		return adminSwitchPlan{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now()
	adminSwitchDeleteExpiredPlans(store.previews, now)
	adminSwitchDeleteExpiredPlans(store.rollbacks, now)
	plans := store.previews
	if rollback {
		plans = store.rollbacks
	}
	plan, ok := plans[token]
	if !ok {
		return adminSwitchPlan{}, false
	}
	delete(plans, token)
	if strings.TrimSpace(plan.Username) != username || strings.TrimSpace(plan.SessionHandle) != strings.TrimSpace(sessionHandle) || !plan.ExpiresAt.After(now) {
		return adminSwitchPlan{}, false
	}
	return plan, true
}

func (s *Server) ensureAdminSwitchPlanStore() *adminSwitchPlanStore {
	if s.switchPlans == nil {
		s.switchPlans = newAdminSwitchPlanStore()
	}
	return s.switchPlans
}

func adminSwitchDeleteExpiredPlans(plans map[string]adminSwitchPlan, now time.Time) {
	for token, plan := range plans {
		if !plan.ExpiresAt.After(now) {
			delete(plans, token)
		}
	}
}

func newAdminSwitchToken() string {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
