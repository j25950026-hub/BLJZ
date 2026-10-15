package admin

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-bot/internal/db"
)

type adminGroupWorkbenchFilters struct {
	Query  string `json:"q,omitempty"`
	Tag    string `json:"tag,omitempty"`
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
}

type adminGroupWorkbenchView struct {
	Name    string            `json:"name"`
	Filters map[string]string `json:"filters"`
}

type adminGroupWorkbenchRow struct {
	ChatID             int64  `json:"chatId"`
	Title              string `json:"title"`
	GroupType          string `json:"groupType"`
	GroupTags          string `json:"groupTags"`
	BookkeepingEnabled bool   `json:"bookkeepingEnabled"`
	AutoReplyEnabled   bool   `json:"autoReplyEnabled"`
	WelcomeEnabled     bool   `json:"welcomeEnabled"`
	FeeRate            string `json:"feeRate"`
	CustomRate         string `json:"customRate"`
	PayoutFeeRate      string `json:"payoutFeeRate"`
	PayoutRate         string `json:"payoutRate"`
	PayoutAddress      string `json:"payoutAddress"`
	DayCutTime         string `json:"dayCutTime"`
	HealthScore        int    `json:"healthScore"`
	HealthStatus       string `json:"healthStatus"`
	ActionURL          string `json:"actionUrl"`
}

type adminGroupViewSaveRequest struct {
	Name    string            `json:"name"`
	Filters map[string]string `json:"filters"`
}

type adminGroupBatchPreviewRequest struct {
	Filters map[string]string `json:"filters"`
	ChatIDs []int64           `json:"chatIds,omitempty"`
	Action  struct {
		Field string `json:"field"`
		Value string `json:"value"`
	} `json:"action"`
}

type adminGroupBatchPreviewData struct {
	MatchedCount int                          `json:"matchedCount"`
	Affected     []adminGroupBatchPreviewItem `json:"affected"`
	Skipped      []adminGroupBatchSkipItem    `json:"skipped"`
	ApplyToken   string                       `json:"applyToken"`
}

type adminGroupBatchApplyRequest struct {
	ApplyToken string `json:"applyToken"`
}

type adminGroupBatchApplyReceipt struct {
	MatchedCount int                          `json:"matchedCount"`
	UpdatedCount int                          `json:"updatedCount"`
	SkippedCount int                          `json:"skippedCount"`
	FailedCount  int                          `json:"failedCount"`
	Updated      []adminGroupBatchPreviewItem `json:"updated"`
	Skipped      []adminGroupBatchSkipItem    `json:"skipped"`
	Failed       []adminGroupBatchSkipItem    `json:"failed"`
}

type adminGroupBatchPreviewItem struct {
	ChatID int64  `json:"chatId"`
	Title  string `json:"title"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type adminGroupBatchSkipItem struct {
	ChatID int64  `json:"chatId"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type adminBatchPreviewPlan struct {
	Username  string
	Filters   adminGroupWorkbenchFilters
	ChatIDs   []int64
	Field     string
	Value     string
	ExpiresAt time.Time
}

type adminBatchPreviewStore struct {
	mu       sync.Mutex
	previews map[string]adminBatchPreviewPlan
	now      func() time.Time
}

func newAdminBatchPreviewStore() *adminBatchPreviewStore {
	return &adminBatchPreviewStore{
		previews: make(map[string]adminBatchPreviewPlan),
		now:      time.Now,
	}
}

func (s *Server) handleAdminGroupsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"ok":    false,
			"error": "method not allowed",
		})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"data": map[string]any{
				"groups": []adminGroupWorkbenchRow{},
				"total":  0,
				"views":  []adminGroupWorkbenchView{},
			},
		})
		return
	}

	filters := adminGroupFiltersFromQuery(r)
	rows, err := s.db.ListAllGroups()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	out, err := s.buildAdminGroupWorkbenchRows(rows, filters)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	views, err := s.loadAdminSavedViews(r)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"groups":  out,
			"total":   len(out),
			"filters": filters,
			"views":   views,
		},
	})
}

func (s *Server) handleAdminGroupViewsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"ok":    false,
			"error": "method not allowed",
		})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	var req adminGroupViewSaveRequest
	if err := decodeLimitedAdminJSON(w, r, &req); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 64 {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "视图名称不能为空且不能超过 64 个字符"})
		return
	}
	filters := sanitizeAdminGroupFilterMap(req.Filters)
	username := s.adminSessionUsername(r)
	if err := s.db.SaveAdminSavedView(username, req.Name, filters); err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": adminGroupWorkbenchView{
			Name:    req.Name,
			Filters: filters,
		},
	})
}

func (s *Server) handleAdminGroupBatchPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"ok":    false,
			"error": "method not allowed",
		})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	var req adminGroupBatchPreviewRequest
	if err := decodeLimitedAdminJSON(w, r, &req); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	field, value, err := normalizeAdminBatchAction(req.Action.Field, req.Action.Value)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	chatIDs, err := normalizeAdminBatchChatIDs(req.ChatIDs)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	rows, err := s.db.ListAllGroups()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	filters := adminGroupFiltersFromMap(req.Filters)
	selected := adminBatchChatIDSet(chatIDs)
	preview := adminGroupBatchPreviewData{
		Affected: []adminGroupBatchPreviewItem{},
		Skipped:  []adminGroupBatchSkipItem{},
	}
	for _, row := range rows {
		if !adminGroupMatchesBatchTarget(row, filters, selected) {
			continue
		}
		preview.MatchedCount++
		before := adminBatchFieldValue(row, field)
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = strconv.FormatInt(row.ChatID, 10)
		}
		if before == value {
			preview.Skipped = append(preview.Skipped, adminGroupBatchSkipItem{
				ChatID: row.ChatID,
				Title:  title,
				Reason: "无需变更",
			})
			continue
		}
		preview.Affected = append(preview.Affected, adminGroupBatchPreviewItem{
			ChatID: row.ChatID,
			Title:  title,
			Before: before,
			After:  value,
		})
	}
	preview.ApplyToken = s.saveAdminBatchPreview(r, adminBatchPreviewPlan{
		Username: s.adminSessionUsername(r),
		Filters:  filters,
		ChatIDs:  chatIDs,
		Field:    field,
		Value:    value,
	})
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": preview,
	})
}

func (s *Server) handleAdminGroupBatchApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"ok":    false,
			"error": "method not allowed",
		})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	var req adminGroupBatchApplyRequest
	if err := decodeLimitedAdminJSON(w, r, &req); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	plan, ok := s.takeAdminBatchPreview(s.adminSessionUsername(r), req.ApplyToken)
	if !ok {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "预演已过期，请重新预演"})
		return
	}
	rows, err := s.db.ListAllGroups()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	receipt := adminGroupBatchApplyReceipt{
		Updated: []adminGroupBatchPreviewItem{},
		Skipped: []adminGroupBatchSkipItem{},
		Failed:  []adminGroupBatchSkipItem{},
	}
	retryOperationID := fmt.Sprintf("group_batch:%s:%d", plan.Field, time.Now().UnixNano())
	_, _, actionErr := normalizeAdminBatchAction(plan.Field, plan.Value)
	selected := adminBatchChatIDSet(plan.ChatIDs)
	for _, row := range rows {
		if !adminGroupMatchesBatchTarget(row, plan.Filters, selected) {
			continue
		}
		receipt.MatchedCount++
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = strconv.FormatInt(row.ChatID, 10)
		}
		before := adminBatchFieldValue(row, plan.Field)
		if before == plan.Value {
			receipt.SkippedCount++
			receipt.Skipped = append(receipt.Skipped, adminGroupBatchSkipItem{
				ChatID: row.ChatID,
				Title:  title,
				Reason: "无需变更",
			})
			continue
		}
		if actionErr != nil {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminGroupBatchSkipItem{
				ChatID: row.ChatID,
				Title:  title,
				Reason: actionErr.Error(),
			})
			s.recordAdminRetryFailure(retryOperationID, fmt.Sprintf("%s:%d", plan.Field, row.ChatID), "group_batch", row.ChatID, map[string]any{
				"field":  plan.Field,
				"before": before,
				"after":  plan.Value,
				"chatId": row.ChatID,
			}, actionErr.Error())
			continue
		}
		if err := s.db.UpdateGroupFields(row.ChatID, map[string]any{plan.Field: adminBatchUpdateValue(plan.Field, plan.Value)}); err != nil {
			receipt.FailedCount++
			receipt.Failed = append(receipt.Failed, adminGroupBatchSkipItem{
				ChatID: row.ChatID,
				Title:  title,
				Reason: err.Error(),
			})
			s.recordAdminRetryFailure(retryOperationID, fmt.Sprintf("%s:%d", plan.Field, row.ChatID), "group_batch", row.ChatID, map[string]any{
				"field":  plan.Field,
				"before": before,
				"after":  plan.Value,
				"chatId": row.ChatID,
			}, err.Error())
			continue
		}
		receipt.UpdatedCount++
		receipt.Updated = append(receipt.Updated, adminGroupBatchPreviewItem{
			ChatID: row.ChatID,
			Title:  title,
			Before: before,
			After:  plan.Value,
		})
	}
	if receipt.UpdatedCount > 0 {
		_ = s.db.AddAuditLog(0, 0, 0, "admin_group_batch_apply", fmt.Sprintf("field=%s updated=%d skipped=%d failed=%d", plan.Field, receipt.UpdatedCount, receipt.SkippedCount, receipt.FailedCount))
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": receipt,
	})
}

func decodeLimitedAdminJSON(w http.ResponseWriter, r *http.Request, out any) error {
	if r != nil && r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxAdminFormBodyBytes)
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

func (s *Server) buildAdminGroupWorkbenchRows(rows []db.GroupConfig, filters adminGroupWorkbenchFilters) ([]adminGroupWorkbenchRow, error) {
	out := make([]adminGroupWorkbenchRow, 0, len(rows))
	for _, row := range rows {
		if !adminGroupMatchesFilters(row, filters) {
			continue
		}
		closing, err := s.db.GetClosingReminderConfig(row.ChatID)
		if err != nil {
			return nil, err
		}
		health := buildAdminConsoleGroupHealth(row, closing)
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = strconv.FormatInt(row.ChatID, 10)
		}
		out = append(out, adminGroupWorkbenchRow{
			ChatID:             row.ChatID,
			Title:              title,
			GroupType:          strings.TrimSpace(row.GroupType),
			GroupTags:          strings.TrimSpace(row.GroupTags),
			BookkeepingEnabled: row.BookkeepingEnabled == 1,
			AutoReplyEnabled:   row.AutoReplyEnabled == 1,
			WelcomeEnabled:     row.WelcomeEnabled == 1,
			FeeRate:            strings.TrimSpace(fmt.Sprintf("%g", row.FeeRate)),
			CustomRate:         strings.TrimSpace(fmt.Sprintf("%g", row.CustomRate)),
			PayoutFeeRate:      strings.TrimSpace(fmt.Sprintf("%g", row.PayoutFeeRate)),
			PayoutRate:         strings.TrimSpace(fmt.Sprintf("%g", row.PayoutRate)),
			PayoutAddress:      strings.TrimSpace(row.PayoutAddress),
			DayCutTime:         strings.TrimSpace(row.DayCutTime),
			HealthScore:        health.Score,
			HealthStatus:       health.Status,
			ActionURL:          "/admin/group?chat_id=" + strconv.FormatInt(row.ChatID, 10),
		})
	}
	return out, nil
}

func (s *Server) loadAdminSavedViews(r *http.Request) ([]adminGroupWorkbenchView, error) {
	if s.db == nil {
		return []adminGroupWorkbenchView{}, nil
	}
	rows, err := s.db.ListAdminSavedViews(s.adminSessionUsername(r))
	if err != nil {
		return nil, err
	}
	out := make([]adminGroupWorkbenchView, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminGroupWorkbenchView{
			Name:    row.Name,
			Filters: row.Filters,
		})
	}
	return out, nil
}

func adminGroupFiltersFromQuery(r *http.Request) adminGroupWorkbenchFilters {
	if r == nil || r.URL == nil {
		return adminGroupWorkbenchFilters{}
	}
	query := r.URL.Query()
	return adminGroupWorkbenchFilters{
		Query:  strings.TrimSpace(query.Get("q")),
		Tag:    strings.TrimSpace(query.Get("tag")),
		Type:   strings.TrimSpace(query.Get("type")),
		Status: strings.TrimSpace(query.Get("status")),
	}
}

func adminGroupFiltersFromMap(filters map[string]string) adminGroupWorkbenchFilters {
	clean := sanitizeAdminGroupFilterMap(filters)
	return adminGroupWorkbenchFilters{
		Query:  clean["q"],
		Tag:    clean["tag"],
		Type:   clean["type"],
		Status: clean["status"],
	}
}

func sanitizeAdminGroupFilterMap(filters map[string]string) map[string]string {
	clean := map[string]string{}
	for _, key := range []string{"q", "tag", "type", "status"} {
		value := strings.TrimSpace(filters[key])
		if value != "" {
			clean[key] = value
		}
	}
	return clean
}

func adminGroupMatchesFilters(row db.GroupConfig, filters adminGroupWorkbenchFilters) bool {
	if filters.Query != "" {
		raw := strings.Join([]string{
			row.Title,
			strconv.FormatInt(row.ChatID, 10),
			row.GroupType,
			row.GroupTags,
			row.PayoutAddress,
			row.PayoutAddressRemark,
		}, " ")
		if !containsFold(raw, filters.Query) {
			return false
		}
	}
	if filters.Tag != "" && !adminGroupHasTag(row.GroupTags, filters.Tag) {
		return false
	}
	if filters.Type != "" && !strings.EqualFold(strings.TrimSpace(row.GroupType), filters.Type) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(filters.Status)) {
	case "", "all":
		return true
	case "enabled":
		return row.BookkeepingEnabled == 1
	case "disabled":
		return row.BookkeepingEnabled != 1
	case "missing_address":
		return strings.TrimSpace(row.PayoutAddress) == ""
	case "auto_reply_off":
		return row.AutoReplyEnabled != 1
	default:
		return false
	}
}

func adminGroupMatchesBatchTarget(row db.GroupConfig, filters adminGroupWorkbenchFilters, selected map[int64]struct{}) bool {
	if len(selected) > 0 {
		_, ok := selected[row.ChatID]
		return ok
	}
	return adminGroupMatchesFilters(row, filters)
}

func adminGroupHasTag(rawTags, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return true
	}
	for _, part := range strings.Split(rawTags, ",") {
		if strings.EqualFold(strings.TrimSpace(part), want) {
			return true
		}
	}
	return false
}

func normalizeAdminBatchChatIDs(raw []int64) ([]int64, error) {
	if len(raw) > 500 {
		return nil, fmt.Errorf("一次最多选择 500 个群")
	}
	out := make([]int64, 0, len(raw))
	seen := map[int64]struct{}{}
	for _, chatID := range raw {
		if chatID == 0 {
			continue
		}
		if _, ok := seen[chatID]; ok {
			continue
		}
		seen[chatID] = struct{}{}
		out = append(out, chatID)
	}
	if len(raw) > 0 && len(out) == 0 {
		return nil, fmt.Errorf("选中群无效")
	}
	return out, nil
}

func adminBatchChatIDSet(chatIDs []int64) map[int64]struct{} {
	if len(chatIDs) == 0 {
		return nil
	}
	out := make(map[int64]struct{}, len(chatIDs))
	for _, chatID := range chatIDs {
		out[chatID] = struct{}{}
	}
	return out
}

func normalizeAdminBatchAction(field, value string) (string, string, error) {
	field = strings.TrimSpace(field)
	value = strings.TrimSpace(value)
	switch field {
	case "group_type", "group_tags":
		if value == "" || len([]rune(value)) > 256 {
			return "", "", fmt.Errorf("批量值不能为空且不能超过 256 个字符")
		}
		return field, value, nil
	case "auto_reply_enabled", "bookkeeping_enabled", "welcome_enabled":
		switch strings.ToLower(value) {
		case "1", "true", "on", "开启":
			return field, "1", nil
		case "0", "false", "off", "关闭":
			return field, "0", nil
		default:
			return "", "", fmt.Errorf("开关值必须是 1/0")
		}
	default:
		return "", "", fmt.Errorf("不支持的批量字段")
	}
}

func adminBatchFieldValue(row db.GroupConfig, field string) string {
	switch field {
	case "group_type":
		return strings.TrimSpace(row.GroupType)
	case "group_tags":
		return strings.TrimSpace(row.GroupTags)
	case "auto_reply_enabled":
		if row.AutoReplyEnabled == 1 {
			return "1"
		}
		return "0"
	case "welcome_enabled":
		if row.WelcomeEnabled == 1 {
			return "1"
		}
		return "0"
	case "bookkeeping_enabled":
		if row.BookkeepingEnabled == 1 {
			return "1"
		}
		return "0"
	default:
		return ""
	}
}

func newAdminBatchApplyToken() string {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func (s *Server) saveAdminBatchPreview(r *http.Request, plan adminBatchPreviewPlan) string {
	if s.batchPreviews == nil {
		s.batchPreviews = newAdminBatchPreviewStore()
	}
	token := newAdminBatchApplyToken()
	store := s.batchPreviews
	store.mu.Lock()
	defer store.mu.Unlock()
	plan.ExpiresAt = store.now().Add(10 * time.Minute)
	if strings.TrimSpace(plan.Username) == "" {
		plan.Username = s.adminSessionUsername(r)
	}
	store.previews[token] = plan
	return token
}

func (s *Server) takeAdminBatchPreview(username, token string) (adminBatchPreviewPlan, bool) {
	if s.batchPreviews == nil {
		return adminBatchPreviewPlan{}, false
	}
	username = strings.TrimSpace(username)
	token = strings.TrimSpace(token)
	if token == "" {
		return adminBatchPreviewPlan{}, false
	}
	store := s.batchPreviews
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now()
	for key, plan := range store.previews {
		if !plan.ExpiresAt.After(now) {
			delete(store.previews, key)
		}
	}
	plan, ok := store.previews[token]
	if !ok {
		return adminBatchPreviewPlan{}, false
	}
	delete(store.previews, token)
	if strings.TrimSpace(plan.Username) != username {
		return adminBatchPreviewPlan{}, false
	}
	if !plan.ExpiresAt.After(now) {
		return adminBatchPreviewPlan{}, false
	}
	return plan, true
}

func adminBatchUpdateValue(field, value string) any {
	switch field {
	case "auto_reply_enabled", "bookkeeping_enabled", "welcome_enabled":
		if value == "1" {
			return 1
		}
		return 0
	default:
		return value
	}
}
