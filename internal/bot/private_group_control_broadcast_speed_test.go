package bot

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/config"
	"go-bot/internal/db"
)

func TestPrivateGroupControlBroadcastUsesBoundedConcurrency(t *testing.T) {
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/sendMessage") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		current := inFlight.Add(1)
		for {
			observed := maxInFlight.Load()
			if current <= observed || maxInFlight.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		inFlight.Add(-1)

		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":440501}}`))
	}))
	t.Cleanup(server.Close)

	app := &App{
		cfg: config.Config{Timezone: "Asia/Shanghai"},
		api: &tgbotapi.BotAPI{
			Token:  "broadcast-speed-test-token",
			Client: server.Client(),
			Self: tgbotapi.User{
				ID:       440500,
				UserName: "broadcast_speed_test_bot",
			},
		},
	}
	app.api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	groups := make([]db.GroupConfig, 12)
	for i := range groups {
		groups[i] = db.GroupConfig{
			ChatID: -100500000 - int64(i),
			Title:  "速度测试群" + strconv.Itoa(i+1),
		}
	}

	result, err := app.broadcastTextToGroupsDetailed(groups, "速度测试", "")
	if err != nil {
		t.Fatalf("批量群发不应返回错误: %v", err)
	}
	if result.Expected != len(groups) || result.Succeeded != len(groups) || result.Failed != 0 {
		t.Fatalf("批量结果统计异常: %+v", result)
	}
	if got := maxInFlight.Load(); got < 2 {
		t.Fatalf("群发应使用并发发送，最大并发数=%d", got)
	}
	if got := maxInFlight.Load(); got > 8 {
		t.Fatalf("群发并发度不应超过 8，最大并发数=%d", got)
	}
}
