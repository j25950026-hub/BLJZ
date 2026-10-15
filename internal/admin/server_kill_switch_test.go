package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-bot/internal/config"
	"go-bot/internal/system"
)

func TestAdminKillSwitchOneClickStop(t *testing.T) {
	stopCalls := 0
	switcher := system.NewKillSwitch(system.KillSwitchOptions{
		Stop: func() {
			stopCalls++
		},
	})
	server := NewServer(config.Config{AdminToken: "kill-token"}, nil, nil)
	server.SetKillSwitch(switcher)

	req := httptest.NewRequest(http.MethodPost, "/admin/kill-switch?token=kill-token", strings.NewReader("reason=%E6%94%BB%E5%87%BB%E6%A3%80%E6%B5%8B%E8%A7%A6%E5%8F%91"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("紧急停机页面状态码错误: %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "紧急停机已触发") {
		t.Fatalf("页面应反馈触发成功: %s", resp.Body.String())
	}
	if stopCalls != 1 {
		t.Fatalf("停机回调应只执行一次: got=%d", stopCalls)
	}
	event, ok := switcher.Snapshot()
	if !ok {
		t.Fatalf("触发后应记录快照")
	}
	if event.Category != system.KillSwitchCategoryManual {
		t.Fatalf("触发类别错误: %s", event.Category)
	}
	if event.Source != "admin_panel" {
		t.Fatalf("触发来源错误: %s", event.Source)
	}
}
