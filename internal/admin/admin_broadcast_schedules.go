package admin

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go-bot/internal/bot"
	"go-bot/internal/db"
	"go-bot/internal/utils"
)

type adminMessagingService interface {
	AdminBroadcast(bot.AdminBroadcastRequest) (int, error)
	AdminListSchedules(chatID int64, limit int) ([]db.Schedule, error)
	AdminCreateSchedule(bot.AdminScheduleCreateRequest) (int64, error)
	AdminUpdateSchedule(bot.AdminScheduleUpdateRequest) error
	AdminResendSchedule(chatID, scheduleID int64) error
	AdminDisableSchedule(chatID, scheduleID int64) error
	AdminDeleteSchedule(chatID, scheduleID int64) error
}

type adminBroadcastPreviewRequest struct {
	SourceChatID int64    `json:"sourceChatId,omitempty"`
	ChatIDs      []int64  `json:"chatIds,omitempty"`
	TargetTags   []string `json:"targetTags,omitempty"`
	IdentityName string   `json:"identityName,omitempty"`
	MediaType    string   `json:"mediaType,omitempty"`
	MediaFileID  string   `json:"mediaFileId,omitempty"`
	Content      string   `json:"content,omitempty"`
}

type adminMessagingApplyRequest struct {
	ApplyToken string `json:"applyToken"`
}

type adminMessagingTarget struct {
	ChatID int64  `json:"chatId"`
	Title  string `json:"title"`
	Tags   string `json:"tags,omitempty"`
}

type adminBroadcastPreviewData struct {
	MatchedCount int                    `json:"matchedCount"`
	Targets      []adminMessagingTarget `json:"targets"`
	MediaType    string                 `json:"mediaType"`
	Content      string                 `json:"content"`
	ApplyToken   string                 `json:"applyToken"`
}

type adminBroadcastReceipt struct {
	MatchedCount int                    `json:"matchedCount"`
	SentCount    int                    `json:"sentCount"`
	FailedCount  int                    `json:"failedCount"`
	Targets      []adminMessagingTarget `json:"targets"`
}

type adminScheduleActionRequest struct {
	Action          string `json:"action"`
	ChatID          int64  `json:"chatId"`
	ScheduleID      int64  `json:"scheduleId,omitempty"`
	SendAt          string `json:"sendAt,omitempty"`
	Content         string `json:"content,omitempty"`
	SourceChatID    int64  `json:"sourceChatId,omitempty"`
	SourceMessageID int    `json:"sourceMessageId,omitempty"`
	IdentityName    string `json:"identityName,omitempty"`
	TargetTags      string `json:"targetTags,omitempty"`
	CreatorUserID   int64  `json:"creatorUserId,omitempty"`
}

type adminScheduleView struct {
	ID              int64  `json:"id"`
	ChatID          int64  `json:"chatId"`
	GroupTitle      string `json:"groupTitle"`
	SendAt          string `json:"sendAt"`
	Content         string `json:"content"`
	SourceChatID    int64  `json:"sourceChatId,omitempty"`
	SourceMessageID int    `json:"sourceMessageId,omitempty"`
	IdentityName    string `json:"identityName,omitempty"`
	TargetTags      string `json:"targetTags,omitempty"`
	Status          string `json:"status"`
	CreatorUserID   int64  `json:"creatorUserId,omitempty"`
}

type adminSchedulePreviewData struct {
	Action       string                 `json:"action"`
	MatchedCount int                    `json:"matchedCount"`
	Targets      []adminMessagingTarget `json:"targets"`
	Before       *adminScheduleView     `json:"before,omitempty"`
	After        *adminScheduleView     `json:"after,omitempty"`
	ApplyToken   string                 `json:"applyToken"`
}

type adminScheduleReceipt struct {
	Action     string `json:"action"`
	ScheduleID int64  `json:"scheduleId"`
	Status     string `json:"status"`
}

type adminMessagingPlan struct {
	Username       string
	Kind           string
	Broadcast      *bot.AdminBroadcastRequest
	Targets        []adminMessagingTarget
	Schedule       *adminScheduleActionRequest
	ScheduleBefore *adminScheduleView
	ExpiresAt      time.Time
}

type adminMessagingPlanStore struct {
	mu    sync.Mutex
	plans map[string]adminMessagingPlan
	now   func() time.Time
}

func newAdminMessagingPlanStore() *adminMessagingPlanStore {
	return &adminMessagingPlanStore{
		plans: make(map[string]adminMessagingPlan),
		now:   time.Now,
	}
}

func (s *Server) handleAdminBroadcastPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	var request adminBroadcastPreviewRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	broadcastRequest, targets, err := s.buildAdminBroadcastPreview(request)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	token := s.saveAdminMessagingPlan(adminMessagingPlan{
		Username:  s.adminSessionUsername(r),
		Kind:      "broadcast",
		Broadcast: &broadcastRequest,
		Targets:   targets,
	})
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": adminBroadcastPreviewData{
			MatchedCount: len(targets),
			Targets:      targets,
			MediaType:    broadcastRequest.MediaType,
			Content:      broadcastRequest.Content,
			ApplyToken:   token,
		},
	})
}

func (s *Server) handleAdminBroadcastApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.messaging == nil {
		s.writeAdminJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "消息服务未配置"})
		return
	}
	var request adminMessagingApplyRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	plan, ok := s.takeAdminMessagingPlan(s.adminSessionUsername(r), request.ApplyToken, "broadcast")
	if !ok || plan.Broadcast == nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "群发预演已过期，请重新预演"})
		return
	}
	retryOperationID := fmt.Sprintf("broadcast:%d", time.Now().UnixNano())
	sent, err := s.messaging.AdminBroadcast(*plan.Broadcast)
	if err != nil {
		for _, target := range plan.Targets {
			s.recordAdminRetryFailure(retryOperationID, fmt.Sprintf("broadcast:%d", target.ChatID), "broadcast", target.ChatID, adminBroadcastRetryPayload(*plan.Broadcast, target.ChatID), err.Error())
		}
		s.writeAdminJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if sent < 0 {
		sent = 0
	}
	if sent > len(plan.Targets) {
		sent = len(plan.Targets)
	}
	receipt := adminBroadcastReceipt{
		MatchedCount: len(plan.Targets),
		SentCount:    sent,
		FailedCount:  len(plan.Targets) - sent,
		Targets:      plan.Targets,
	}
	for index := sent; index < len(plan.Targets); index++ {
		target := plan.Targets[index]
		s.recordAdminRetryFailure(retryOperationID, fmt.Sprintf("broadcast:%d", target.ChatID), "broadcast", target.ChatID, adminBroadcastRetryPayload(*plan.Broadcast, target.ChatID), "群发未成功")
	}
	_ = s.db.AddAuditLog(0, 0, 0, "admin_broadcast_apply", fmt.Sprintf("targets=%d sent=%d failed=%d", receipt.MatchedCount, receipt.SentCount, receipt.FailedCount))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": receipt})
}

func (s *Server) buildAdminBroadcastPreview(request adminBroadcastPreviewRequest) (bot.AdminBroadcastRequest, []adminMessagingTarget, error) {
	mediaType := strings.ToLower(strings.TrimSpace(request.MediaType))
	if mediaType == "" {
		mediaType = "text"
	}
	if mediaType != "text" && mediaType != "photo" && mediaType != "video" && mediaType != "voice" {
		return bot.AdminBroadcastRequest{}, nil, fmt.Errorf("不支持的媒体类型")
	}
	content := strings.TrimSpace(request.Content)
	mediaFileID := strings.TrimSpace(request.MediaFileID)
	if mediaType == "text" && content == "" {
		return bot.AdminBroadcastRequest{}, nil, fmt.Errorf("群发内容不能为空")
	}
	if mediaType != "text" && mediaFileID == "" {
		return bot.AdminBroadcastRequest{}, nil, fmt.Errorf("媒体群发必须提供 FileID")
	}
	if utf8.RuneCountInString(content) > 4096 {
		return bot.AdminBroadcastRequest{}, nil, fmt.Errorf("群发内容不能超过 4096 个字符")
	}
	chatIDs, err := normalizeAdminBatchChatIDs(request.ChatIDs)
	if err != nil {
		return bot.AdminBroadcastRequest{}, nil, err
	}
	tags := utils.SplitTags(strings.Join(request.TargetTags, ","))
	groups, err := s.resolveAdminBroadcastGroups(request.SourceChatID, chatIDs, tags)
	if err != nil {
		return bot.AdminBroadcastRequest{}, nil, err
	}
	targets := make([]adminMessagingTarget, 0, len(groups))
	targetChatIDs := make([]int64, 0, len(groups))
	for _, group := range groups {
		targetChatIDs = append(targetChatIDs, group.ChatID)
		targets = append(targets, adminMessagingTarget{
			ChatID: group.ChatID,
			Title:  adminSwitchGroupTitle(group),
			Tags:   strings.TrimSpace(group.GroupTags),
		})
	}
	return bot.AdminBroadcastRequest{
		SourceChatID:  request.SourceChatID,
		TargetChatIDs: targetChatIDs,
		TargetTags:    tags,
		IdentityName:  strings.TrimSpace(request.IdentityName),
		MediaType:     mediaType,
		MediaFileID:   mediaFileID,
		Content:       content,
	}, targets, nil
}

func (s *Server) resolveAdminBroadcastGroups(sourceChatID int64, chatIDs []int64, tags []string) ([]db.GroupConfig, error) {
	if len(chatIDs) > 0 {
		groups := make([]db.GroupConfig, 0, len(chatIDs))
		for _, chatID := range chatIDs {
			group, err := s.db.GetGroup(chatID)
			if err != nil {
				return nil, fmt.Errorf("目标群 %d 不存在", chatID)
			}
			groups = append(groups, group)
		}
		return groups, nil
	}
	if len(tags) > 0 {
		groups, err := s.db.ListGroupsByTags(tags)
		if err != nil {
			return nil, err
		}
		if len(groups) == 0 {
			return nil, fmt.Errorf("按标签未找到目标群")
		}
		return groups, nil
	}
	if sourceChatID == 0 {
		return nil, fmt.Errorf("请选择目标群、目标标签或来源群")
	}
	source, err := s.db.GetGroup(sourceChatID)
	if err != nil {
		return nil, fmt.Errorf("来源群不存在")
	}
	sourceTags := utils.SplitTags(source.GroupTags)
	if len(sourceTags) == 0 {
		return []db.GroupConfig{source}, nil
	}
	groups, err := s.db.ListGroupsByTags(sourceTags)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return []db.GroupConfig{source}, nil
	}
	return groups, nil
}

func (s *Server) handleAdminSchedulesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.messaging == nil || s.db == nil {
		s.writeAdminJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "定时任务服务未配置"})
		return
	}
	chatID, err := adminOptionalChatID(r.URL.Query().Get("chat_id"))
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	rows, err := s.listAdminSchedules(chatID)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"schedules": rows, "total": len(rows)}})
}

func (s *Server) handleAdminSchedulePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.messaging == nil || s.db == nil {
		s.writeAdminJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "定时任务服务未配置"})
		return
	}
	var request adminScheduleActionRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	request, before, after, target, err := s.normalizeAdminScheduleAction(request)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	token := s.saveAdminMessagingPlan(adminMessagingPlan{
		Username:       s.adminSessionUsername(r),
		Kind:           "schedule",
		Targets:        []adminMessagingTarget{target},
		Schedule:       &request,
		ScheduleBefore: before,
	})
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": adminSchedulePreviewData{
			Action:       request.Action,
			MatchedCount: 1,
			Targets:      []adminMessagingTarget{target},
			Before:       before,
			After:        after,
			ApplyToken:   token,
		},
	})
}

func (s *Server) handleAdminScheduleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.messaging == nil {
		s.writeAdminJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "定时任务服务未配置"})
		return
	}
	var request adminMessagingApplyRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	plan, ok := s.takeAdminMessagingPlan(s.adminSessionUsername(r), request.ApplyToken, "schedule")
	if !ok || plan.Schedule == nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "定时任务预演已过期，请重新预演"})
		return
	}
	action := *plan.Schedule
	if plan.ScheduleBefore != nil {
		current, findErr := s.findAdminSchedule(action.ChatID, action.ScheduleID)
		if findErr != nil {
			s.recordAdminScheduleRetryFailure(plan, "任务已变化，请重新预演")
			s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "任务已变化，请重新预演"})
			return
		}
		currentView := adminScheduleToView(current, plan.ScheduleBefore.GroupTitle)
		if !sameAdminScheduleView(currentView, *plan.ScheduleBefore) {
			s.recordAdminScheduleRetryFailure(plan, "任务已变化，请重新预演")
			s.writeAdminJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "任务已变化，请重新预演"})
			return
		}
	}
	scheduleID := action.ScheduleID
	status := "pending"
	var err error
	switch action.Action {
	case "create":
		scheduleID, err = s.messaging.AdminCreateSchedule(bot.AdminScheduleCreateRequest{
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
		status = "canceled"
		err = s.messaging.AdminDisableSchedule(action.ChatID, action.ScheduleID)
	case "resume":
		err = s.messaging.AdminResendSchedule(action.ChatID, action.ScheduleID)
	case "delete":
		status = "deleted"
		err = s.messaging.AdminDeleteSchedule(action.ChatID, action.ScheduleID)
	}
	if err != nil {
		s.recordAdminScheduleRetryFailure(plan, err.Error())
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = s.db.AddAuditLog(action.ChatID, 0, 0, "admin_schedule_"+action.Action, fmt.Sprintf("schedule_id=%d", scheduleID))
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": adminScheduleReceipt{Action: action.Action, ScheduleID: scheduleID, Status: status},
	})
}

func (s *Server) normalizeAdminScheduleAction(request adminScheduleActionRequest) (adminScheduleActionRequest, *adminScheduleView, *adminScheduleView, adminMessagingTarget, error) {
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	switch request.Action {
	case "create", "update", "pause", "resume", "delete":
	default:
		return request, nil, nil, adminMessagingTarget{}, fmt.Errorf("不支持的定时任务操作")
	}
	if request.ChatID == 0 {
		return request, nil, nil, adminMessagingTarget{}, fmt.Errorf("群 ID 不能为空")
	}
	group, err := s.db.GetGroup(request.ChatID)
	if err != nil {
		return request, nil, nil, adminMessagingTarget{}, fmt.Errorf("群不存在")
	}
	target := adminMessagingTarget{ChatID: group.ChatID, Title: adminSwitchGroupTitle(group), Tags: strings.TrimSpace(group.GroupTags)}
	var before *adminScheduleView
	if request.Action != "create" {
		if request.ScheduleID == 0 {
			return request, nil, nil, target, fmt.Errorf("任务 ID 不能为空")
		}
		row, err := s.findAdminSchedule(request.ChatID, request.ScheduleID)
		if err != nil {
			return request, nil, nil, target, err
		}
		view := adminScheduleToView(row, group.Title)
		before = &view
	}
	request.Content = strings.TrimSpace(request.Content)
	request.IdentityName = strings.TrimSpace(request.IdentityName)
	request.TargetTags = strings.TrimSpace(request.TargetTags)
	if request.Action == "create" || request.Action == "update" {
		if request.Action == "update" && before != nil && request.SourceChatID == 0 && request.SourceMessageID == 0 {
			request.SourceChatID = before.SourceChatID
			request.SourceMessageID = before.SourceMessageID
		}
		sendAt, err := s.normalizeAdminScheduleTime(request.SendAt)
		if err != nil {
			return request, before, nil, target, err
		}
		request.SendAt = sendAt
		if request.Content == "" && request.SourceMessageID <= 0 {
			return request, before, nil, target, fmt.Errorf("定时内容不能为空")
		}
		if utf8.RuneCountInString(request.Content) > 4096 {
			return request, before, nil, target, fmt.Errorf("定时内容不能超过 4096 个字符")
		}
	}
	after := adminScheduleView{
		ID:              request.ScheduleID,
		ChatID:          request.ChatID,
		GroupTitle:      target.Title,
		SendAt:          request.SendAt,
		Content:         request.Content,
		SourceChatID:    request.SourceChatID,
		SourceMessageID: request.SourceMessageID,
		IdentityName:    request.IdentityName,
		TargetTags:      request.TargetTags,
		Status:          "pending",
		CreatorUserID:   request.CreatorUserID,
	}
	if before != nil && request.Action != "update" {
		after = *before
		switch request.Action {
		case "pause":
			after.Status = "canceled"
		case "resume":
			after.Status = "pending"
		case "delete":
			after.Status = "deleted"
		}
	}
	return request, before, &after, target, nil
}

func (s *Server) normalizeAdminScheduleTime(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("请填写定时发送时间")
	}
	location := time.Local
	if name := strings.TrimSpace(s.cfg.Timezone); name != "" {
		if loaded, err := time.LoadLocation(name); err == nil {
			location = loaded
		}
	}
	layouts := []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		var parsed time.Time
		var err error
		if layout == time.RFC3339 {
			parsed, err = time.Parse(layout, text)
		} else {
			parsed, err = time.ParseInLocation(layout, text, location)
		}
		if err != nil {
			continue
		}
		if !parsed.After(time.Now()) {
			return "", fmt.Errorf("定时发送时间必须晚于当前时间")
		}
		return parsed.Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("时间格式错误")
}

func (s *Server) listAdminSchedules(chatID int64) ([]adminScheduleView, error) {
	groups, err := s.db.ListAllGroups()
	if err != nil {
		return nil, err
	}
	groupNames := make(map[int64]string, len(groups))
	for _, group := range groups {
		groupNames[group.ChatID] = adminSwitchGroupTitle(group)
	}
	chatIDs := []int64{chatID}
	if chatID == 0 {
		chatIDs = make([]int64, 0, len(groups))
		for _, group := range groups {
			chatIDs = append(chatIDs, group.ChatID)
		}
	} else if _, ok := groupNames[chatID]; !ok {
		return nil, fmt.Errorf("群不存在")
	}
	views := []adminScheduleView{}
	for _, targetChatID := range chatIDs {
		rows, err := s.messaging.AdminListSchedules(targetChatID, 500)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			views = append(views, adminScheduleToView(row, groupNames[row.ChatID]))
		}
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].SendAt == views[j].SendAt {
			return views[i].ID > views[j].ID
		}
		return views[i].SendAt > views[j].SendAt
	})
	return views, nil
}

func (s *Server) findAdminSchedule(chatID, scheduleID int64) (db.Schedule, error) {
	rows, err := s.messaging.AdminListSchedules(chatID, 500)
	if err != nil {
		return db.Schedule{}, err
	}
	for _, row := range rows {
		if row.ID == scheduleID {
			return row, nil
		}
	}
	return db.Schedule{}, fmt.Errorf("任务不存在")
}

func adminScheduleToView(row db.Schedule, groupTitle string) adminScheduleView {
	return adminScheduleView{
		ID:              row.ID,
		ChatID:          row.ChatID,
		GroupTitle:      strings.TrimSpace(groupTitle),
		SendAt:          row.SendAt,
		Content:         row.Content,
		SourceChatID:    row.SourceChatID,
		SourceMessageID: row.SourceMessageID,
		IdentityName:    row.IdentityName,
		TargetTags:      row.TargetTags,
		Status:          row.Status,
		CreatorUserID:   row.CreatorUserID,
	}
}

func sameAdminScheduleView(left, right adminScheduleView) bool {
	return left.ID == right.ID &&
		left.ChatID == right.ChatID &&
		left.SendAt == right.SendAt &&
		left.Content == right.Content &&
		left.SourceChatID == right.SourceChatID &&
		left.SourceMessageID == right.SourceMessageID &&
		left.IdentityName == right.IdentityName &&
		left.TargetTags == right.TargetTags &&
		left.Status == right.Status &&
		left.CreatorUserID == right.CreatorUserID
}

func adminOptionalChatID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	chatID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || chatID == 0 {
		return 0, fmt.Errorf("群 ID 格式错误")
	}
	return chatID, nil
}

func (s *Server) saveAdminMessagingPlan(plan adminMessagingPlan) string {
	store := s.ensureAdminMessagingPlanStore()
	token := newAdminBatchApplyToken()
	store.mu.Lock()
	defer store.mu.Unlock()
	plan.ExpiresAt = store.now().Add(10 * time.Minute)
	store.plans[token] = plan
	return token
}

func (s *Server) takeAdminMessagingPlan(username, token, kind string) (adminMessagingPlan, bool) {
	store := s.ensureAdminMessagingPlanStore()
	username = strings.TrimSpace(username)
	token = strings.TrimSpace(token)
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
		return adminMessagingPlan{}, false
	}
	delete(store.plans, token)
	if strings.TrimSpace(plan.Username) != username || plan.Kind != kind || !plan.ExpiresAt.After(now) {
		return adminMessagingPlan{}, false
	}
	return plan, true
}

func (s *Server) ensureAdminMessagingPlanStore() *adminMessagingPlanStore {
	if s.messagingPlans == nil {
		s.messagingPlans = newAdminMessagingPlanStore()
	}
	return s.messagingPlans
}
