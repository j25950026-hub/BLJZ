package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go-bot/internal/db"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminBatchRetryCenterCanRetryOnlyFailedItems(t *testing.T) {
	database := newAdminSwitchesTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)
	item, err := database.SaveAdminRetryItem(db.AdminRetryItem{OperationID: "switch-op-1", ItemKey: "global-switch:-100801", Kind: "global_switch", ChatID: -100801, PayloadJSON: `{"feature":"welcome","before":"1","after":"0","chatId":-100801}`, FailureReason: "配置已变化，请重新预演", Status: "failed"})
	if err != nil {
		t.Fatalf("准备失败重试项失败: %v", err)
	}
	completed, err := database.SaveAdminRetryItem(db.AdminRetryItem{OperationID: "switch-op-1", ItemKey: "global-switch:-100802", Kind: "global_switch", ChatID: -100802, PayloadJSON: `{"feature":"welcome","before":"1","after":"0","chatId":-100802}`, Status: "completed"})
	if err != nil {
		t.Fatalf("准备已完成重试项失败: %v", err)
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		resp := httptest.NewRecorder()
		server.handler().ServeHTTP(resp, req)
		return resp
	}
	listReq := httptest.NewRequest(http.MethodGet, "/admin/api/retries?status=failed", nil)
	for _, cookie := range cookies {
		listReq.AddCookie(cookie)
	}
	listResp := httptest.NewRecorder()
	server.handler().ServeHTTP(listResp, listReq)
	var listPayload struct {
		Data struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("解析失败重试列表失败: %v body=%s", err, listResp.Body.String())
	}
	if listResp.Code != http.StatusOK || len(listPayload.Data.Items) != 1 || listPayload.Data.Items[0].ID != item.ID || listPayload.Data.Items[0].ID == completed.ID {
		t.Fatalf("失败重试列表应只返回 failed 项: got=%d itemID=%d completedID=%d body=%s", listResp.Code, item.ID, completed.ID, listResp.Body.String())
	}
	preview := post("/admin/api/retries/preview", fmt.Sprintf(`{"ids":[%d]}`, item.ID))
	if preview.Code != http.StatusOK {
		t.Fatalf("失败重试预演失败: got=%d body=%s", preview.Code, preview.Body.String())
	}
	var payload struct {
		Data struct {
			ApplyToken string `json:"applyToken"`
			Affected   []struct {
				ID int64 `json:"id"`
			} `json:"affected"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &payload); err != nil || payload.Data.ApplyToken == "" || len(payload.Data.Affected) != 1 || payload.Data.Affected[0].ID != item.ID {
		t.Fatalf("失败重试预演范围错误: err=%v body=%s", err, preview.Body.String())
	}
	apply := post("/admin/api/retries/apply", "{\"applyToken\":\""+payload.Data.ApplyToken+"\"}")
	if apply.Code != http.StatusOK {
		t.Fatalf("失败重试确认失败: got=%d body=%s", apply.Code, apply.Body.String())
	}
	group, err := database.GetGroup(-100801)
	if err != nil || group.WelcomeEnabled != 0 {
		t.Fatalf("重试应重新执行全局开关: group=%+v err=%v", group, err)
	}
	completedItems, err := database.ListAdminRetryItems("completed", 10)
	if err != nil || len(completedItems) != 2 {
		t.Fatalf("重试完成后状态错误: items=%+v err=%v", completedItems, err)
	}
}
