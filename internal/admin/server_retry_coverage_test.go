package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-bot/internal/bot"
	"go-bot/internal/db"
)

func TestAdminRetryCoverageRecordsGroupBatchFailures(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	preview := postRetryCoverageJSON(t, server, cookies, "/admin/api/groups/batch-preview", `{"chatIds":[-100801],"action":{"field":"group_type","value":"核心业务"}}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("群组批量预演失败: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewPayload struct {
		Data struct {
			ApplyToken string `json:"applyToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewPayload); err != nil || previewPayload.Data.ApplyToken == "" {
		t.Fatalf("群组批量预演缺少令牌: err=%v body=%s", err, preview.Body.String())
	}
	server.batchPreviews.mu.Lock()
	plan := server.batchPreviews.previews[previewPayload.Data.ApplyToken]
	plan.Field = "unsupported_field"
	server.batchPreviews.previews[previewPayload.Data.ApplyToken] = plan
	server.batchPreviews.mu.Unlock()

	apply := postRetryCoverageJSON(t, server, cookies, "/admin/api/groups/batch-apply", `{"applyToken":"`+previewPayload.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK || !strings.Contains(apply.Body.String(), `"failedCount":1`) {
		t.Fatalf("群组批量失败应返回结构化失败回执: got=%d body=%s", apply.Code, apply.Body.String())
	}
	assertRetryCoverageItem(t, database, "group_batch", -100801)
}

func TestAdminRetryCoverageRecordsClosingReminderFailures(t *testing.T) {
	database := newAdminClosingTemplatesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)

	preview := postRetryCoverageJSON(t, server, cookies, "/admin/api/templates/closing-reminders/preview", `{"chatIds":[-101001],"patch":{"reminderTime":"22:00"}}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("下课提醒预演失败: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewPayload struct {
		Data struct {
			ApplyToken string `json:"applyToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewPayload); err != nil || previewPayload.Data.ApplyToken == "" {
		t.Fatalf("下课提醒预演缺少令牌: err=%v body=%s", err, preview.Body.String())
	}
	server.templatePlans.mu.Lock()
	plan := server.templatePlans.plans[previewPayload.Data.ApplyToken]
	plan.Closing.Items[0].DayCutTime = "05:00:00"
	server.templatePlans.plans[previewPayload.Data.ApplyToken] = plan
	server.templatePlans.mu.Unlock()

	apply := postRetryCoverageJSON(t, server, cookies, "/admin/api/templates/apply", `{"applyToken":"`+previewPayload.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK || !strings.Contains(apply.Body.String(), `"failedCount":1`) {
		t.Fatalf("下课提醒失败应返回结构化失败回执: got=%d body=%s", apply.Code, apply.Body.String())
	}
	assertRetryCoverageItem(t, database, "closing_reminder", -101001)
}

func TestAdminRetryCoverageRecordsBroadcastPartialFailures(t *testing.T) {
	database := newAdminMessagingTestDatabase(t)
	base := newFakeAdminMessagingService()
	service := &retryCoverageMessagingService{fakeAdminMessagingService: base, broadcastSent: 1}
	server := newTestSessionAdminServer(t)
	server.db = database
	server.messaging = service
	cookies := loginAdminForAPITest(t, server)

	preview := postRetryCoverageJSON(t, server, cookies, "/admin/api/broadcast/preview", `{"chatIds":[-100901,-100902],"mediaType":"text","content":"部分失败通知"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("群发预演失败: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewPayload struct {
		Data struct {
			ApplyToken string `json:"applyToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewPayload); err != nil || previewPayload.Data.ApplyToken == "" {
		t.Fatalf("群发预演缺少令牌: err=%v body=%s", err, preview.Body.String())
	}
	apply := postRetryCoverageJSON(t, server, cookies, "/admin/api/broadcast/apply", `{"applyToken":"`+previewPayload.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK || !strings.Contains(apply.Body.String(), `"failedCount":1`) {
		t.Fatalf("群发部分失败应返回失败回执: got=%d body=%s", apply.Code, apply.Body.String())
	}
	assertRetryCoverageItem(t, database, "broadcast", -100902)
}

func TestAdminRetryCoverageRecordsScheduleFailures(t *testing.T) {
	database := newAdminMessagingTestDatabase(t)
	base := newFakeAdminMessagingService()
	service := &retryCoverageMessagingService{fakeAdminMessagingService: base, scheduleErr: errors.New("定时任务服务暂时不可用")}
	server := newTestSessionAdminServer(t)
	server.db = database
	server.messaging = service
	cookies := loginAdminForAPITest(t, server)

	preview := postRetryCoverageJSON(t, server, cookies, "/admin/api/schedules/preview", `{"action":"create","chatId":-100901,"sendAt":"2030-01-01T09:00:00+08:00","content":"定时失败通知"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("定时任务预演失败: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewPayload struct {
		Data struct {
			ApplyToken string `json:"applyToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewPayload); err != nil || previewPayload.Data.ApplyToken == "" {
		t.Fatalf("定时任务预演缺少令牌: err=%v body=%s", err, preview.Body.String())
	}
	apply := postRetryCoverageJSON(t, server, cookies, "/admin/api/schedules/apply", `{"applyToken":"`+previewPayload.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusBadRequest || !strings.Contains(apply.Body.String(), "暂时不可用") {
		t.Fatalf("定时任务失败应保留底层错误: got=%d body=%s", apply.Code, apply.Body.String())
	}
	assertRetryCoverageItem(t, database, "schedule", -100901)
}

type retryCoverageMessagingService struct {
	*fakeAdminMessagingService
	broadcastSent int
	scheduleErr   error
}

func (f *retryCoverageMessagingService) AdminBroadcast(request bot.AdminBroadcastRequest) (int, error) {
	f.broadcasts = append(f.broadcasts, request)
	return f.broadcastSent, nil
}

func (f *retryCoverageMessagingService) AdminCreateSchedule(request bot.AdminScheduleCreateRequest) (int64, error) {
	if f.scheduleErr != nil {
		return 0, f.scheduleErr
	}
	return f.fakeAdminMessagingService.AdminCreateSchedule(request)
}

func postRetryCoverageJSON(t *testing.T, server *Server, cookies []*http.Cookie, path, body string) *httptest.ResponseRecorder {
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

func assertRetryCoverageItem(t *testing.T, database interface {
	ListAdminRetryItems(string, int) ([]db.AdminRetryItem, error)
}, kind string, chatID int64) {
	t.Helper()
	items, err := database.ListAdminRetryItems("failed", 50)
	if err != nil {
		t.Fatalf("读取失败重试项失败: %v", err)
	}
	for _, item := range items {
		if item.Kind == kind && item.ChatID == chatID && strings.TrimSpace(item.FailureReason) != "" {
			return
		}
	}
	t.Fatalf("未登记带原因的失败重试项: kind=%s chatID=%d items=%+v", kind, chatID, items)
}
