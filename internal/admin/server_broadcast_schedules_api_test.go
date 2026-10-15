package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"go-bot/internal/bot"
	"go-bot/internal/db"
)

type fakeAdminMessagingService struct {
	broadcasts []bot.AdminBroadcastRequest
	schedules  map[int64]db.Schedule
	nextID     int64
}

func newFakeAdminMessagingService() *fakeAdminMessagingService {
	return &fakeAdminMessagingService{schedules: map[int64]db.Schedule{}, nextID: 100}
}

func (f *fakeAdminMessagingService) AdminBroadcast(request bot.AdminBroadcastRequest) (int, error) {
	f.broadcasts = append(f.broadcasts, request)
	return len(request.TargetChatIDs), nil
}

func (f *fakeAdminMessagingService) AdminListSchedules(chatID int64, limit int) ([]db.Schedule, error) {
	rows := []db.Schedule{}
	for _, row := range f.schedules {
		if chatID == 0 || row.ChatID == chatID {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (f *fakeAdminMessagingService) AdminCreateSchedule(request bot.AdminScheduleCreateRequest) (int64, error) {
	f.nextID++
	f.schedules[f.nextID] = db.Schedule{
		ID:              f.nextID,
		ChatID:          request.ChatID,
		SendAt:          request.SendAt,
		Content:         request.Content,
		SourceChatID:    request.SourceChatID,
		SourceMessageID: request.SourceMessageID,
		IdentityName:    request.IdentityName,
		TargetTags:      request.TargetTags,
		Status:          "pending",
		CreatorUserID:   request.CreatorUserID,
	}
	return f.nextID, nil
}

func (f *fakeAdminMessagingService) AdminUpdateSchedule(request bot.AdminScheduleUpdateRequest) error {
	row, ok := f.schedules[request.ScheduleID]
	if !ok || row.ChatID != request.ChatID {
		return fmt.Errorf("任务不存在")
	}
	row.SendAt = request.SendAt
	row.Content = request.Content
	row.SourceChatID = request.SourceChatID
	row.SourceMessageID = request.SourceMessageID
	row.IdentityName = request.IdentityName
	row.TargetTags = request.TargetTags
	row.Status = "pending"
	f.schedules[row.ID] = row
	return nil
}

func (f *fakeAdminMessagingService) AdminResendSchedule(chatID, scheduleID int64) error {
	row, ok := f.schedules[scheduleID]
	if !ok || row.ChatID != chatID {
		return fmt.Errorf("任务不存在")
	}
	row.Status = "pending"
	f.schedules[scheduleID] = row
	return nil
}

func (f *fakeAdminMessagingService) AdminDisableSchedule(chatID, scheduleID int64) error {
	row, ok := f.schedules[scheduleID]
	if !ok || row.ChatID != chatID {
		return fmt.Errorf("任务不存在")
	}
	row.Status = "canceled"
	f.schedules[scheduleID] = row
	return nil
}

func (f *fakeAdminMessagingService) AdminDeleteSchedule(chatID, scheduleID int64) error {
	row, ok := f.schedules[scheduleID]
	if !ok || row.ChatID != chatID {
		return fmt.Errorf("任务不存在")
	}
	delete(f.schedules, scheduleID)
	return nil
}

type adminPreviewEnvelopeForTest struct {
	OK   bool `json:"ok"`
	Data struct {
		ApplyToken   string `json:"applyToken"`
		MatchedCount int    `json:"matchedCount"`
		Targets      []struct {
			ChatID int64  `json:"chatId"`
			Title  string `json:"title"`
		} `json:"targets"`
	} `json:"data"`
}

func TestAdminBroadcastCanSelectTargetsPreviewAndSend(t *testing.T) {
	database := newAdminMessagingTestDatabase(t)
	service := newFakeAdminMessagingService()
	server := newTestSessionAdminServer(t)
	server.db = database
	server.messaging = service
	cookies := loginAdminForAPITest(t, server)

	preview := postAdminMessagingJSON(t, server, cookies, "/admin/api/broadcast/preview", `{"sourceChatId":-100901,"chatIds":[-100901,-100902],"mediaType":"text","content":"系统维护通知"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("群发预演应成功: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var envelope adminPreviewEnvelopeForTest
	if err := json.Unmarshal(preview.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析群发预演失败: %v", err)
	}
	if !envelope.OK || envelope.Data.MatchedCount != 2 || len(envelope.Data.Targets) != 2 || envelope.Data.ApplyToken == "" {
		t.Fatalf("群发预演目标错误: %+v", envelope)
	}
	if len(service.broadcasts) != 0 {
		t.Fatalf("群发预演不应调用发送服务")
	}

	apply := postAdminMessagingJSON(t, server, cookies, "/admin/api/broadcast/apply", `{"applyToken":"`+envelope.Data.ApplyToken+`"}`)
	if apply.Code != http.StatusOK {
		t.Fatalf("确认群发应成功: got=%d body=%s", apply.Code, apply.Body.String())
	}
	if len(service.broadcasts) != 1 {
		t.Fatalf("确认后应且仅应调用一次发送服务: %d", len(service.broadcasts))
	}
	request := service.broadcasts[0]
	if len(request.TargetChatIDs) != 2 || request.TargetChatIDs[0] != -100901 || request.TargetChatIDs[1] != -100902 || request.Content != "系统维护通知" {
		t.Fatalf("发送服务收到的目标或文案错误: %+v", request)
	}
	for _, want := range []string{`"matchedCount":2`, `"sentCount":2`, `"failedCount":0`} {
		if !bytes.Contains(apply.Body.Bytes(), []byte(want)) {
			t.Fatalf("群发回执缺少 %s: %s", want, apply.Body.String())
		}
	}
}

func TestAdminSchedulesCanCreateUpdatePauseAndResume(t *testing.T) {
	database := newAdminMessagingTestDatabase(t)
	service := newFakeAdminMessagingService()
	server := newTestSessionAdminServer(t)
	server.db = database
	server.messaging = service
	cookies := loginAdminForAPITest(t, server)
	sendAt := time.Now().Add(2 * time.Hour).Format(time.RFC3339)

	createToken := previewScheduleAction(t, server, cookies, `{"action":"create","chatId":-100901,"sendAt":"`+sendAt+`","content":"定时通知"}`)
	create := applyScheduleAction(t, server, cookies, createToken)
	var createBody struct {
		Data struct {
			ScheduleID int64 `json:"scheduleId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &createBody); err != nil || createBody.Data.ScheduleID == 0 {
		t.Fatalf("创建回执应返回任务 ID: err=%v body=%s", err, create.Body.String())
	}
	scheduleID := createBody.Data.ScheduleID

	updatedAt := time.Now().Add(3 * time.Hour).Format(time.RFC3339)
	updateToken := previewScheduleAction(t, server, cookies, fmt.Sprintf(`{"action":"update","chatId":-100901,"scheduleId":%d,"sendAt":"%s","content":"更新后的通知"}`, scheduleID, updatedAt))
	applyScheduleAction(t, server, cookies, updateToken)
	if row := service.schedules[scheduleID]; row.Content != "更新后的通知" || row.SendAt != updatedAt {
		t.Fatalf("定时任务未更新: %+v", row)
	}

	pauseToken := previewScheduleAction(t, server, cookies, fmt.Sprintf(`{"action":"pause","chatId":-100901,"scheduleId":%d}`, scheduleID))
	applyScheduleAction(t, server, cookies, pauseToken)
	if service.schedules[scheduleID].Status != "canceled" {
		t.Fatalf("定时任务未暂停: %+v", service.schedules[scheduleID])
	}

	resumeToken := previewScheduleAction(t, server, cookies, fmt.Sprintf(`{"action":"resume","chatId":-100901,"scheduleId":%d}`, scheduleID))
	applyScheduleAction(t, server, cookies, resumeToken)
	if service.schedules[scheduleID].Status != "pending" {
		t.Fatalf("定时任务未恢复: %+v", service.schedules[scheduleID])
	}

	listReq := httptest.NewRequest(http.MethodGet, "/admin/api/schedules?chat_id=-100901", nil)
	for _, cookie := range cookies {
		listReq.AddCookie(cookie)
	}
	listResp := httptest.NewRecorder()
	server.handler().ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK || !bytes.Contains(listResp.Body.Bytes(), []byte(`"content":"更新后的通知"`)) {
		t.Fatalf("定时任务列表错误: got=%d body=%s", listResp.Code, listResp.Body.String())
	}

	deleteToken := previewScheduleAction(t, server, cookies, fmt.Sprintf(`{"action":"delete","chatId":-100901,"scheduleId":%d}`, scheduleID))
	applyScheduleAction(t, server, cookies, deleteToken)
	if _, ok := service.schedules[scheduleID]; ok {
		t.Fatalf("定时任务应已删除")
	}
}

func TestAdminStaticAppIncludesBroadcastAndSchedules(t *testing.T) {
	server := newTestSessionAdminServer(t)
	cookies := loginAdminForAPITest(t, server)
	req := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("读取后台前端资源失败: got=%d body=%s", resp.Code, resp.Body.String())
	}
	for _, want := range []string{
		"/admin/api/broadcast/preview",
		"/admin/api/broadcast/apply",
		"/admin/api/schedules/preview",
		"/admin/api/schedules/apply",
		"renderBroadcastCenter",
		"renderScheduleCenter",
		"选择目标群",
		"群发预演",
		"定时任务预演",
		"暂停",
		"恢复",
	} {
		if !bytes.Contains(resp.Body.Bytes(), []byte(want)) {
			t.Fatalf("群发/定时前端缺少 %q", want)
		}
	}
}

func TestAdminScheduleApplyRejectsConcurrentChanges(t *testing.T) {
	database := newAdminMessagingTestDatabase(t)
	service := newFakeAdminMessagingService()
	service.schedules[77] = db.Schedule{
		ID:      77,
		ChatID:  -100901,
		SendAt:  time.Now().Add(2 * time.Hour).Format(time.RFC3339),
		Content: "原内容",
		Status:  "pending",
	}
	server := newTestSessionAdminServer(t)
	server.db = database
	server.messaging = service
	cookies := loginAdminForAPITest(t, server)

	token := previewScheduleAction(t, server, cookies, fmt.Sprintf(`{"action":"update","chatId":-100901,"scheduleId":77,"sendAt":"%s","content":"预演内容"}`, time.Now().Add(3*time.Hour).Format(time.RFC3339)))
	row := service.schedules[77]
	row.Content = "并发修改内容"
	service.schedules[77] = row

	apply := postAdminMessagingJSON(t, server, cookies, "/admin/api/schedules/apply", `{"applyToken":"`+token+`"}`)
	if apply.Code != http.StatusConflict || !bytes.Contains(apply.Body.Bytes(), []byte("重新预演")) {
		t.Fatalf("并发变化后执行应返回 409: got=%d body=%s", apply.Code, apply.Body.String())
	}
	if service.schedules[77].Content != "并发修改内容" {
		t.Fatalf("并发修改内容不应被旧预演覆盖: %+v", service.schedules[77])
	}
}

func previewScheduleAction(t *testing.T, server *Server, cookies []*http.Cookie, body string) string {
	t.Helper()
	resp := postAdminMessagingJSON(t, server, cookies, "/admin/api/schedules/preview", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("定时任务预演失败: got=%d body=%s", resp.Code, resp.Body.String())
	}
	var envelope adminPreviewEnvelopeForTest
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil || envelope.Data.ApplyToken == "" {
		t.Fatalf("定时任务预演缺少令牌: err=%v body=%s", err, resp.Body.String())
	}
	return envelope.Data.ApplyToken
}

func applyScheduleAction(t *testing.T, server *Server, cookies []*http.Cookie, token string) *httptest.ResponseRecorder {
	t.Helper()
	resp := postAdminMessagingJSON(t, server, cookies, "/admin/api/schedules/apply", `{"applyToken":"`+token+`"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("定时任务执行失败: got=%d body=%s", resp.Code, resp.Body.String())
	}
	return resp
}

func postAdminMessagingJSON(t *testing.T, server *Server, cookies []*http.Cookie, path, body string) *httptest.ResponseRecorder {
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

func newAdminMessagingTestDatabase(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-messaging.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for index, chatID := range []int64{-100901, -100902, -100903} {
		if _, err := database.EnsureGroup(chatID, fmt.Sprintf("消息测试群 %d", index+1)); err != nil {
			t.Fatalf("创建测试群失败: %v", err)
		}
	}
	return database
}
