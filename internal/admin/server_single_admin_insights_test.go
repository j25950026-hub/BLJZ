package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminAdvancedStatsAndRecommendations(t *testing.T) {
	database := newAdminConsoleTestDatabase(t)
	server := newTestSessionAdminServer(t)
	server.db = database
	cookies := loginAdminForAPITest(t, server)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/insights", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("读取单管理员运营洞察失败: got=%d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			Stats struct {
				TotalGroups      int `json:"totalGroups"`
				IncompleteGroups int `json:"incompleteGroups"`
				AverageHealth    int `json:"averageHealthScore"`
				PendingSchedules int `json:"pendingSchedules"`
			} `json:"stats"`
			Recommendations []struct {
				Code       string `json:"code"`
				Visibility string `json:"visibility"`
				ActionURL  string `json:"actionUrl"`
			} `json:"recommendations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析单管理员运营洞察失败: %v body=%s", err, resp.Body.String())
	}
	if !payload.OK || payload.Data.Stats.TotalGroups != 2 || payload.Data.Stats.IncompleteGroups != 1 || payload.Data.Stats.PendingSchedules != 1 || payload.Data.Stats.AverageHealth <= 0 {
		t.Fatalf("运营统计摘要错误: %+v", payload.Data.Stats)
	}
	if len(payload.Data.Recommendations) == 0 {
		t.Fatalf("缺配置群应产生解释性建议")
	}
	for _, recommendation := range payload.Data.Recommendations {
		if recommendation.Visibility != "admin_only" {
			t.Fatalf("建议只能后台可见: %+v", recommendation)
		}
	}
}
