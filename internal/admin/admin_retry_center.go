package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-bot/internal/bot"
	"go-bot/internal/db"
)

const adminRetryPlanTTL = 10 * time.Minute

type adminRetryPlanEntry struct {
	ID          int64
	ChatID      int64
	Kind        string
	OperationID string
	ItemKey     string
	Payload     db.AdminRetryItem
}

type adminRetryPlan struct {
	Username  string
	Items     []adminRetryPlanEntry
	ExpiresAt time.Time
}

type adminRetryPlanStore struct {
	mu    sync.Mutex
	plans map[string]adminRetryPlan
	now   func() time.Time
}

func newAdminRetryPlanStore() *adminRetryPlanStore {
	return &adminRetryPlanStore{plans: make(map[string]adminRetryPlan), now: time.Now}
}

type adminRetryResponse struct {
	ID            int64  `json:"id"`
	OperationID   string `json:"operationId"`
	ItemKey       string `json:"itemKey"`
	Kind          string `json:"kind"`
	ChatID        int64  `json:"chatId"`
	PayloadJSON   string `json:"payloadJson"`
	FailureReason string `json:"failureReason"`
	Status        string `json:"status"`
	AttemptCount  int    `json:"attemptCount"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type adminRetryIssue struct {
	ID     int64  `json:"id"`
	ChatID int64  `json:"chatId"`
	Reason string `json:"reason"`
}

type adminRetryPreviewRequest struct {
	IDs []int64 `json:"ids"`
}

type adminRetryApplyRequest struct {
	ApplyToken string `json:"applyToken"`
}

func (s *Server) handleAdminRetriesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": []adminRetryResponse{}}})
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "limit 格式错误"})
			return
		}
		limit = parsed
	}
	items, err := s.db.ListAdminRetryItems(strings.TrimSpace(r.URL.Query().Get("status")), limit)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	rows := make([]adminRetryResponse, 0, len(items))
	for _, item := range items {
		rows = append(rows, adminRetryResponseFromDB(item))
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": rows}})
}

func (s *Server) handleAdminRetriesPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	var request adminRetryPreviewRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	ids := normalizeAdminRetryIDs(request.IDs)
	if len(ids) == 0 {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "请选择至少一个失败项"})
		return
	}
	affected := []adminRetryPlanEntry{}
	skipped := []adminRetryIssue{}
	failed := []adminRetryIssue{}
	for _, id := range ids {
		item, err := s.db.GetAdminRetryItem(id)
		if err != nil {
			failed = append(failed, adminRetryIssue{ID: id, Reason: "重试项不存在"})
			continue
		}
		if item.Status != "failed" {
			skipped = append(skipped, adminRetryIssue{ID: id, ChatID: item.ChatID, Reason: "仅失败项可重试"})
			continue
		}
		entry, reason := s.previewAdminRetryItem(item)
		if reason != "" {
			failed = append(failed, adminRetryIssue{ID: id, ChatID: item.ChatID, Reason: reason})
			continue
		}
		affected = append(affected, entry)
	}
	applyToken := ""
	if len(affected) > 0 {
		applyToken = s.saveAdminRetryPlan(adminRetryPlan{Username: s.adminSessionUsername(r), Items: affected})
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{
		"affected":     adminRetryPlanEntriesResponse(affected),
		"skipped":      skipped,
		"failed":       failed,
		"applyToken":   applyToken,
		"matchedCount": len(ids),
	}})
}

func (s *Server) handleAdminRetriesApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	var request adminRetryApplyRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	plan, ok := s.takeAdminRetryPlan(s.adminSessionUsername(r), request.ApplyToken)
	if !ok {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "重试预演已过期，请重新预演"})
		return
	}
	updated := 0
	skipped := []adminRetryIssue{}
	failed := []adminRetryIssue{}
	for _, entry := range plan.Items {
		item, err := s.db.GetAdminRetryItem(entry.ID)
		if err != nil {
			failed = append(failed, adminRetryIssue{ID: entry.ID, ChatID: entry.ChatID, Reason: "重试项不存在"})
			continue
		}
		if item.Status != "failed" {
			skipped = append(skipped, adminRetryIssue{ID: entry.ID, ChatID: entry.ChatID, Reason: "重试项已处理"})
			continue
		}
		if err := s.applyAdminRetryItem(entry); err != nil {
			_, _ = s.db.UpdateAdminRetryItem(entry.ID, "failed", err.Error(), true)
			failed = append(failed, adminRetryIssue{ID: entry.ID, ChatID: entry.ChatID, Reason: err.Error()})
			continue
		}
		_, _ = s.db.UpdateAdminRetryItem(entry.ID, "completed", "", true)
		updated++
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{
		"matchedCount": len(plan.Items),
		"updatedCount": updated,
		"skippedCount": len(skipped),
		"failedCount":  len(failed),
		"skipped":      skipped,
		"failed":       failed,
	}})
}

func (s *Server) previewAdminRetryItem(item db.AdminRetryItem) (adminRetryPlanEntry, string) {
	var chatID int64
	switch item.Kind {
	case "global_switch":
		var payload struct {
			Feature string `json:"feature"`
			Before  string `json:"before"`
			After   string `json:"after"`
			ChatID  int64  `json:"chatId"`
		}
		if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil || payload.Feature == "" || payload.ChatID == 0 {
			return adminRetryPlanEntry{}, "重试参数格式错误"
		}
		group, err := s.db.GetGroup(payload.ChatID)
		if err != nil {
			return adminRetryPlanEntry{}, "目标群不存在"
		}
		current, err := s.adminSwitchValue(group, payload.Feature)
		if err != nil {
			return adminRetryPlanEntry{}, err.Error()
		}
		if current == payload.After {
			return adminRetryPlanEntry{}, "目标已经是目标值"
		}
		if current != payload.Before {
			return adminRetryPlanEntry{}, "目标配置已变化，请重新生成失败项"
		}
		chatID = payload.ChatID
	case "group_batch":
		var payload adminGroupBatchRetryPayload
		if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil || payload.Field == "" || payload.ChatID == 0 {
			return adminRetryPlanEntry{}, "重试参数格式错误"
		}
		if _, _, err := normalizeAdminBatchAction(payload.Field, payload.After); err != nil {
			return adminRetryPlanEntry{}, err.Error()
		}
		group, err := s.db.GetGroup(payload.ChatID)
		if err != nil {
			return adminRetryPlanEntry{}, "目标群不存在"
		}
		current := adminBatchFieldValue(group, payload.Field)
		if current == payload.After {
			return adminRetryPlanEntry{}, "目标已经是目标值"
		}
		if current != payload.Before {
			return adminRetryPlanEntry{}, "目标配置已变化，请重新生成失败项"
		}
		chatID = payload.ChatID
	case "closing_reminder":
		var payload adminClosingReminderRetryPayload
		if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil || payload.ChatID == 0 {
			return adminRetryPlanEntry{}, "重试参数格式错误"
		}
		group, err := s.db.GetGroup(payload.ChatID)
		if err != nil {
			return adminRetryPlanEntry{}, "目标群不存在"
		}
		if strings.TrimSpace(group.DayCutTime) != strings.TrimSpace(payload.DayCutTime) {
			return adminRetryPlanEntry{}, "日切时间已变化，请重新预演"
		}
		current, err := s.db.GetClosingReminderConfig(payload.ChatID)
		if err != nil {
			return adminRetryPlanEntry{}, err.Error()
		}
		if adminClosingReminderConfigMatchesPatch(current, payload.After, payload.Patch) {
			return adminRetryPlanEntry{}, "目标已经是目标值"
		}
		if !adminClosingReminderConfigMatchesPatch(current, payload.Before, payload.Patch) {
			return adminRetryPlanEntry{}, "目标配置已变化，请重新生成失败项"
		}
		chatID = payload.ChatID
	case "broadcast":
		var payload adminBroadcastRetryPayloadData
		if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil || len(payload.TargetChatIDs) != 1 || payload.TargetChatIDs[0] == 0 {
			return adminRetryPlanEntry{}, "重试参数格式错误"
		}
		if s.messaging == nil {
			return adminRetryPlanEntry{}, "消息服务未配置"
		}
		if _, err := s.db.GetGroup(payload.TargetChatIDs[0]); err != nil {
			return adminRetryPlanEntry{}, "目标群不存在"
		}
		chatID = payload.TargetChatIDs[0]
	case "schedule":
		var payload adminScheduleRetryPayload
		if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil || payload.Action.ChatID == 0 {
			return adminRetryPlanEntry{}, "重试参数格式错误"
		}
		if s.messaging == nil {
			return adminRetryPlanEntry{}, "定时任务服务未配置"
		}
		if _, err := s.db.GetGroup(payload.Action.ChatID); err != nil {
			return adminRetryPlanEntry{}, "目标群不存在"
		}
		if payload.Before != nil && payload.Action.Action != "create" {
			current, err := s.findAdminSchedule(payload.Action.ChatID, payload.Action.ScheduleID)
			if err != nil || !sameAdminScheduleView(adminScheduleToView(current, payload.Before.GroupTitle), *payload.Before) {
				return adminRetryPlanEntry{}, "任务已变化，请重新预演"
			}
		}
		chatID = payload.Action.ChatID
	default:
		return adminRetryPlanEntry{}, "当前重试中心暂不支持该失败类型"
	}
	return adminRetryPlanEntry{ID: item.ID, ChatID: chatID, Kind: item.Kind, OperationID: item.OperationID, ItemKey: item.ItemKey, Payload: item}, ""
}

func (s *Server) applyAdminRetryItem(entry adminRetryPlanEntry) error {
	var payload struct {
		Feature string `json:"feature"`
		Before  string `json:"before"`
		After   string `json:"after"`
		ChatID  int64  `json:"chatId"`
	}
	switch entry.Kind {
	case "global_switch":
		if err := json.Unmarshal([]byte(entry.Payload.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("重试参数格式错误")
		}
		group, err := s.db.GetGroup(payload.ChatID)
		if err != nil {
			return fmt.Errorf("目标群不存在")
		}
		current, err := s.adminSwitchValue(group, payload.Feature)
		if err != nil {
			return err
		}
		if current == payload.After {
			return nil
		}
		if current != payload.Before {
			return fmt.Errorf("目标配置已变化，请重新生成失败项")
		}
		return s.writeAdminSwitchValue(group, payload.Feature, payload.After)
	case "group_batch":
		return s.applyAdminGroupBatchRetry(entry.Payload.PayloadJSON)
	case "closing_reminder":
		return s.applyAdminClosingReminderRetry(entry.Payload.PayloadJSON)
	case "broadcast":
		return s.applyAdminBroadcastRetry(entry.Payload.PayloadJSON)
	case "schedule":
		return s.applyAdminScheduleRetry(entry.Payload.PayloadJSON)
	default:
		return fmt.Errorf("当前重试中心暂不支持该失败类型")
	}
}

func (s *Server) saveAdminRetryPlan(plan adminRetryPlan) string {
	store := s.ensureAdminRetryPlanStore()
	token := newAdminSwitchToken()
	store.mu.Lock()
	defer store.mu.Unlock()
	plan.ExpiresAt = store.now().Add(adminRetryPlanTTL)
	store.plans[token] = plan
	return token
}

func (s *Server) takeAdminRetryPlan(username, token string) (adminRetryPlan, bool) {
	store := s.ensureAdminRetryPlanStore()
	username = strings.TrimSpace(username)
	token = strings.TrimSpace(token)
	if token == "" {
		return adminRetryPlan{}, false
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
		return adminRetryPlan{}, false
	}
	delete(store.plans, token)
	if plan.Username != username || !plan.ExpiresAt.After(now) {
		return adminRetryPlan{}, false
	}
	return plan, true
}

func (s *Server) ensureAdminRetryPlanStore() *adminRetryPlanStore {
	if s.retryPlans == nil {
		s.retryPlans = newAdminRetryPlanStore()
	}
	return s.retryPlans
}

func normalizeAdminRetryIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) >= 200 {
			break
		}
	}
	return out
}

func adminRetryResponseFromDB(item db.AdminRetryItem) adminRetryResponse {
	return adminRetryResponse{ID: item.ID, OperationID: item.OperationID, ItemKey: item.ItemKey, Kind: item.Kind, ChatID: item.ChatID, PayloadJSON: item.PayloadJSON, FailureReason: item.FailureReason, Status: item.Status, AttemptCount: item.AttemptCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func adminRetryPlanEntriesResponse(items []adminRetryPlanEntry) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{"id": item.ID, "chatId": item.ChatID, "kind": item.Kind, "operationId": item.OperationID, "itemKey": item.ItemKey})
	}
	return out
}

func (s *Server) recordAdminSwitchRetryItems(plan adminSwitchPlan, receipt adminSwitchReceipt) {
	if s == nil || s.db == nil || len(receipt.Failed) == 0 {
		return
	}
	operationID := fmt.Sprintf("switch:%s:%d", plan.Feature, time.Now().UnixNano())
	for _, failed := range receipt.Failed {
		for _, planned := range plan.Items {
			if planned.ChatID != failed.ChatID {
				continue
			}
			s.recordAdminRetryFailure(operationID, fmt.Sprintf("%s:%d", plan.Feature, planned.ChatID), "global_switch", planned.ChatID, map[string]any{
				"feature": plan.Feature,
				"before":  planned.Before,
				"after":   planned.After,
				"chatId":  planned.ChatID,
			}, failed.Reason)
			break
		}
	}
}

func (s *Server) recordAdminRetryFailure(operationID, itemKey, kind string, chatID int64, payload any, reason string) {
	if s == nil || s.db == nil {
		return
	}
	operationID = strings.TrimSpace(operationID)
	itemKey = strings.TrimSpace(itemKey)
	kind = strings.TrimSpace(kind)
	reason = strings.TrimSpace(reason)
	if operationID == "" || itemKey == "" || kind == "" || reason == "" {
		return
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = s.db.SaveAdminRetryItem(db.AdminRetryItem{
		OperationID:   operationID,
		ItemKey:       itemKey,
		Kind:          kind,
		ChatID:        chatID,
		PayloadJSON:   string(payloadJSON),
		FailureReason: reason,
		Status:        "failed",
	})
}

type adminGroupBatchRetryPayload struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
	ChatID int64  `json:"chatId"`
}

type adminClosingReminderRetryPayload struct {
	ChatID     int64                         `json:"chatId"`
	DayCutTime string                        `json:"dayCutTime"`
	Patch      adminClosingReminderPatch     `json:"patch"`
	Before     db.GroupClosingReminderConfig `json:"before"`
	After      db.GroupClosingReminderConfig `json:"after"`
}

type adminBroadcastRetryPayloadData struct {
	SourceChatID  int64    `json:"sourceChatId"`
	TargetChatIDs []int64  `json:"targetChatIds"`
	TargetTags    []string `json:"targetTags,omitempty"`
	IdentityName  string   `json:"identityName,omitempty"`
	MediaType     string   `json:"mediaType,omitempty"`
	MediaFileID   string   `json:"mediaFileId,omitempty"`
	Content       string   `json:"content,omitempty"`
}

type adminScheduleRetryPayload struct {
	Action adminScheduleActionRequest `json:"action"`
	Before *adminScheduleView         `json:"before,omitempty"`
}

func adminBroadcastRetryPayload(request bot.AdminBroadcastRequest, chatID int64) adminBroadcastRetryPayloadData {
	return adminBroadcastRetryPayloadData{
		SourceChatID:  request.SourceChatID,
		TargetChatIDs: []int64{chatID},
		TargetTags:    append([]string(nil), request.TargetTags...),
		IdentityName:  request.IdentityName,
		MediaType:     request.MediaType,
		MediaFileID:   request.MediaFileID,
		Content:       request.Content,
	}
}

func (s *Server) applyAdminGroupBatchRetry(payloadJSON string) error {
	var payload adminGroupBatchRetryPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil || payload.Field == "" || payload.ChatID == 0 {
		return fmt.Errorf("重试参数格式错误")
	}
	if _, _, err := normalizeAdminBatchAction(payload.Field, payload.After); err != nil {
		return err
	}
	group, err := s.db.GetGroup(payload.ChatID)
	if err != nil {
		return fmt.Errorf("目标群不存在")
	}
	current := adminBatchFieldValue(group, payload.Field)
	if current == payload.After {
		return nil
	}
	if current != payload.Before {
		return fmt.Errorf("目标配置已变化，请重新生成失败项")
	}
	return s.db.UpdateGroupFields(payload.ChatID, map[string]any{payload.Field: adminBatchUpdateValue(payload.Field, payload.After)})
}

func (s *Server) applyAdminClosingReminderRetry(payloadJSON string) error {
	var payload adminClosingReminderRetryPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil || payload.ChatID == 0 {
		return fmt.Errorf("重试参数格式错误")
	}
	group, err := s.db.GetGroup(payload.ChatID)
	if err != nil {
		return fmt.Errorf("目标群不存在")
	}
	if strings.TrimSpace(group.DayCutTime) != strings.TrimSpace(payload.DayCutTime) {
		return fmt.Errorf("日切时间已变化，请重新预演")
	}
	current, err := s.db.GetClosingReminderConfig(payload.ChatID)
	if err != nil {
		return err
	}
	if adminClosingReminderConfigMatchesPatch(current, payload.After, payload.Patch) {
		return nil
	}
	if !adminClosingReminderConfigMatchesPatch(current, payload.Before, payload.Patch) {
		return fmt.Errorf("目标配置已变化，请重新生成失败项")
	}
	next, changedFields := applyAdminClosingReminderPatch(current, payload.Patch)
	if len(changedFields) == 0 {
		return nil
	}
	return s.db.UpsertClosingReminderConfig(next)
}

func (s *Server) applyAdminBroadcastRetry(payloadJSON string) error {
	var payload adminBroadcastRetryPayloadData
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil || len(payload.TargetChatIDs) != 1 || payload.TargetChatIDs[0] == 0 {
		return fmt.Errorf("重试参数格式错误")
	}
	if s.messaging == nil {
		return fmt.Errorf("消息服务未配置")
	}
	if _, err := s.db.GetGroup(payload.TargetChatIDs[0]); err != nil {
		return fmt.Errorf("目标群不存在")
	}
	sent, err := s.messaging.AdminBroadcast(bot.AdminBroadcastRequest{
		SourceChatID:  payload.SourceChatID,
		TargetChatIDs: payload.TargetChatIDs,
		TargetTags:    payload.TargetTags,
		IdentityName:  payload.IdentityName,
		MediaType:     payload.MediaType,
		MediaFileID:   payload.MediaFileID,
		Content:       payload.Content,
	})
	if err != nil {
		return err
	}
	if sent < 1 {
		return fmt.Errorf("群发未成功")
	}
	return nil
}

func (s *Server) applyAdminScheduleRetry(payloadJSON string) error {
	var payload adminScheduleRetryPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil || payload.Action.ChatID == 0 {
		return fmt.Errorf("重试参数格式错误")
	}
	if s.messaging == nil {
		return fmt.Errorf("定时任务服务未配置")
	}
	action := payload.Action
	if payload.Before != nil && action.Action != "create" {
		current, err := s.findAdminSchedule(action.ChatID, action.ScheduleID)
		if err != nil || !sameAdminScheduleView(adminScheduleToView(current, payload.Before.GroupTitle), *payload.Before) {
			return fmt.Errorf("任务已变化，请重新预演")
		}
	}
	var err error
	switch action.Action {
	case "create":
		_, err = s.messaging.AdminCreateSchedule(bot.AdminScheduleCreateRequest{
			ChatID:          action.ChatID,
			SendAt:          action.SendAt,
			Content:         action.Content,
			SourceChatID:    action.SourceChatID,
			SourceMessageID: action.SourceMessageID,
			IdentityName:    action.IdentityName,
			TargetTags:      action.TargetTags,
			CreatorUserID:   action.CreatorUserID,
		})
	case "update":
		err = s.messaging.AdminUpdateSchedule(bot.AdminScheduleUpdateRequest{
			ChatID:          action.ChatID,
			ScheduleID:      action.ScheduleID,
			SendAt:          action.SendAt,
			Content:         action.Content,
			SourceChatID:    action.SourceChatID,
			SourceMessageID: action.SourceMessageID,
			IdentityName:    action.IdentityName,
			TargetTags:      action.TargetTags,
		})
	case "pause":
		err = s.messaging.AdminDisableSchedule(action.ChatID, action.ScheduleID)
	case "resume":
		err = s.messaging.AdminResendSchedule(action.ChatID, action.ScheduleID)
	case "delete":
		err = s.messaging.AdminDeleteSchedule(action.ChatID, action.ScheduleID)
	default:
		return fmt.Errorf("不支持的定时任务操作")
	}
	return err
}

func (s *Server) recordAdminScheduleRetryFailure(plan adminMessagingPlan, reason string) {
	if plan.Schedule == nil {
		return
	}
	action := *plan.Schedule
	itemKey := fmt.Sprintf("schedule:%s:%d:%d", action.Action, action.ChatID, action.ScheduleID)
	s.recordAdminRetryFailure(fmt.Sprintf("schedule:%d", time.Now().UnixNano()), itemKey, "schedule", action.ChatID, adminScheduleRetryPayload{Action: action, Before: plan.ScheduleBefore}, reason)
}
