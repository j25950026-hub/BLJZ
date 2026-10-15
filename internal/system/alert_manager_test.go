package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type captureAlertNotifier struct {
	name   string
	mu     sync.Mutex
	events []AlertEvent
	err    error
}

func (n *captureAlertNotifier) Name() string {
	return n.name
}

func (n *captureAlertNotifier) Notify(_ context.Context, event AlertEvent) error {
	n.mu.Lock()
	n.events = append(n.events, event)
	n.mu.Unlock()
	return n.err
}

func (n *captureAlertNotifier) Events() []AlertEvent {
	n.mu.Lock()
	defer n.mu.Unlock()
	copied := make([]AlertEvent, len(n.events))
	copy(copied, n.events)
	return copied
}

func TestLoadAlertManagerSpecFromEnv(t *testing.T) {
	t.Run("默认关闭", func(t *testing.T) {
		spec, err := LoadAlertManagerSpecFromEnv(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认配置不应报错: %v", err)
		}
		if spec.Enabled {
			t.Fatalf("默认配置应关闭告警")
		}
		if spec.ConsecutiveFailureThreshold != DefaultAlertManagerConsecutiveFailureThreshold {
			t.Fatalf("默认连续失败阈值错误: %d", spec.ConsecutiveFailureThreshold)
		}
		if spec.LatencySpikeThreshold != DefaultAlertManagerLatencySpikeThreshold {
			t.Fatalf("默认延迟阈值错误: %s", spec.LatencySpikeThreshold)
		}
		if spec.TelegramEnabled() || spec.PagerDutyEnabled() {
			t.Fatalf("默认配置不应启用任何通道")
		}
	})

	t.Run("自动启用 telegram 通道", func(t *testing.T) {
		spec, err := LoadAlertManagerSpecFromEnv(func(key string) string {
			switch key {
			case "BOT_TOKEN":
				return "bot-token"
			case "SUPPORT_CHAT_ID":
				return "-100200"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("合法 telegram 配置不应报错: %v", err)
		}
		if !spec.Enabled {
			t.Fatalf("检测到有效通道后应自动启用")
		}
		if !spec.TelegramEnabled() {
			t.Fatalf("telegram 通道应启用")
		}
		if spec.TelegramBotToken != "bot-token" {
			t.Fatalf("telegram token 解析错误: %q", spec.TelegramBotToken)
		}
		if spec.TelegramChatID != -100200 {
			t.Fatalf("telegram chat id 解析错误: %d", spec.TelegramChatID)
		}
	})

	t.Run("非法配置触发回退告警", func(t *testing.T) {
		spec, err := LoadAlertManagerSpecFromEnv(func(key string) string {
			switch key {
			case "ALERT_MANAGER_ENABLED":
				return "maybe"
			case "ALERT_CONSECUTIVE_FAILURE_THRESHOLD":
				return "bad"
			case "ALERT_LATENCY_SPIKE_THRESHOLD":
				return "bad"
			case "ALERT_TELEGRAM_CHAT_ID":
				return "not-number"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法配置应返回告警")
		}
		if !strings.Contains(err.Error(), "ALERT_MANAGER_ENABLED") {
			t.Fatalf("错误信息应包含非法变量: %v", err)
		}
		if spec.ConsecutiveFailureThreshold != DefaultAlertManagerConsecutiveFailureThreshold {
			t.Fatalf("连续失败阈值应回退默认值: %d", spec.ConsecutiveFailureThreshold)
		}
		if spec.LatencySpikeThreshold != DefaultAlertManagerLatencySpikeThreshold {
			t.Fatalf("延迟阈值应回退默认值: %s", spec.LatencySpikeThreshold)
		}
		if spec.Enabled {
			t.Fatalf("配置非法且无可用通道时应保持关闭")
		}
	})
}

func TestBuildAlertManagerLog(t *testing.T) {
	disabled := BuildAlertManagerLog(AlertManagerSpec{})
	if !strings.Contains(disabled, "智能告警未启用") {
		t.Fatalf("关闭时日志错误: %q", disabled)
	}

	enabled := BuildAlertManagerLog(AlertManagerSpec{
		Enabled:                     true,
		ConsecutiveFailureThreshold: 4,
		LatencySpikeThreshold:       1500 * time.Millisecond,
		LatencyAlertCooldown:        2 * time.Minute,
		TelegramBotToken:            "bot-token",
		TelegramChatID:              -100100,
		PagerDutyRoutingKey:         "pd-key",
		PagerDutySource:             "ledgerbot-prod",
	})
	if !strings.Contains(enabled, "智能告警已启用") {
		t.Fatalf("启用日志缺少标记: %q", enabled)
	}
	if !strings.Contains(enabled, "consecutive_failures>=4") {
		t.Fatalf("启用日志缺少连续失败阈值: %q", enabled)
	}
	if !strings.Contains(enabled, "telegram(chat_id=-100100)") {
		t.Fatalf("启用日志缺少 telegram 通道: %q", enabled)
	}
	if !strings.Contains(enabled, "pagerduty(source=ledgerbot-prod)") {
		t.Fatalf("启用日志缺少 pagerduty 通道: %q", enabled)
	}
}

func TestControlPoint525AlertManager(t *testing.T) {
	notifier := &captureAlertNotifier{name: "capture"}
	now := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	manager := NewAlertManager(AlertManagerOptions{
		ConsecutiveFailureThreshold: 3,
		LatencySpikeThreshold:       200 * time.Millisecond,
		LatencyAlertCooldown:        time.Minute,
		NotificationTimeout:         time.Second,
		Notifiers:                   []AlertNotifier{notifier},
		Now: func() time.Time {
			return now
		},
		Logf: func(string, ...any) {},
	})

	// 连续失败达到阈值后只触发一次，成功后可重新计数。
	manager.ObserveUpdate("message", 120*time.Millisecond, false)
	manager.ObserveUpdate("message", 120*time.Millisecond, false)
	manager.ObserveUpdate("message", 120*time.Millisecond, false)
	manager.ObserveUpdate("message", 120*time.Millisecond, false)
	manager.ObserveUpdate("message", 80*time.Millisecond, true)
	manager.ObserveUpdate("message", 120*time.Millisecond, false)
	manager.ObserveUpdate("message", 120*time.Millisecond, false)
	manager.ObserveUpdate("message", 120*time.Millisecond, false)

	now = now.Add(10 * time.Second)
	manager.ObserveUpdate("message", 250*time.Millisecond, true)
	now = now.Add(30 * time.Second)
	manager.ObserveUpdate("message", 320*time.Millisecond, true)
	now = now.Add(31 * time.Second)
	manager.ObserveUpdate("message", 350*time.Millisecond, true)
	now = now.Add(2 * time.Second)
	manager.ObserveUpdate("message", 100*time.Millisecond, true)
	now = now.Add(2 * time.Second)
	manager.ObserveUpdate("message", 260*time.Millisecond, true)

	events := notifier.Events()
	if len(events) != 5 {
		t.Fatalf("控制点 5.2.5 功能验证失败：告警数量错误 want=5 got=%d events=%+v", len(events), events)
	}

	if events[0].Kind != AlertKindConsecutiveFailures || events[1].Kind != AlertKindConsecutiveFailures {
		t.Fatalf("前两个告警应为连续失败: %+v", events)
	}
	if events[2].Kind != AlertKindLatencySpike || events[3].Kind != AlertKindLatencySpike || events[4].Kind != AlertKindLatencySpike {
		t.Fatalf("后三个告警应为延迟飙升: %+v", events)
	}
	if events[0].ConsecutiveFailures != 3 || events[0].ConsecutiveFailureThreshold != 3 {
		t.Fatalf("连续失败告警字段错误: %+v", events[0])
	}
	if events[2].LatencyThreshold != 200*time.Millisecond {
		t.Fatalf("延迟阈值字段错误: %+v", events[2])
	}
}

func TestTelegramAlertNotifier(t *testing.T) {
	var requestPath string
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("解析 telegram 请求失败: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	notifier, err := NewTelegramAlertNotifier(TelegramAlertNotifierOptions{
		BotToken:   "test-token",
		ChatID:     -10001,
		APIBaseURL: server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("初始化 telegram notifier 失败: %v", err)
	}

	err = notifier.Notify(context.Background(), AlertEvent{
		Kind:                        AlertKindConsecutiveFailures,
		Source:                      "message",
		OccurredAt:                  time.Date(2026, 2, 20, 12, 30, 0, 0, time.UTC),
		ConsecutiveFailures:         5,
		ConsecutiveFailureThreshold: 5,
	})
	if err != nil {
		t.Fatalf("发送 telegram 告警失败: %v", err)
	}
	if requestPath != "/bottest-token/sendMessage" {
		t.Fatalf("telegram 请求路径错误: %q", requestPath)
	}
	chatID, ok := payload["chat_id"].(float64)
	if !ok || int64(chatID) != -10001 {
		t.Fatalf("telegram chat_id 错误: %v", payload["chat_id"])
	}
	text, _ := payload["text"].(string)
	if !strings.Contains(text, "连续失败") || !strings.Contains(text, "message") {
		t.Fatalf("telegram 告警正文错误: %q", text)
	}
}

func TestPagerDutyAlertNotifier(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("解析 pagerduty 请求失败: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	notifier, err := NewPagerDutyAlertNotifier(PagerDutyAlertNotifierOptions{
		RoutingKey: "routing-key",
		EventURL:   server.URL,
		Source:     "ledgerbot-prod",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("初始化 pagerduty notifier 失败: %v", err)
	}

	err = notifier.Notify(context.Background(), AlertEvent{
		Kind:                        AlertKindConsecutiveFailures,
		Source:                      "callback_query",
		OccurredAt:                  time.Date(2026, 2, 20, 12, 40, 0, 0, time.UTC),
		ConsecutiveFailures:         6,
		ConsecutiveFailureThreshold: 5,
	})
	if err != nil {
		t.Fatalf("发送 pagerduty 告警失败: %v", err)
	}

	if payload["routing_key"] != "routing-key" {
		t.Fatalf("pagerduty routing_key 错误: %v", payload["routing_key"])
	}
	if payload["event_action"] != "trigger" {
		t.Fatalf("pagerduty event_action 错误: %v", payload["event_action"])
	}
	dedupKey, _ := payload["dedup_key"].(string)
	if !strings.Contains(dedupKey, string(AlertKindConsecutiveFailures)) {
		t.Fatalf("pagerduty dedup_key 错误: %q", dedupKey)
	}
	payloadData, ok := payload["payload"].(map[string]any)
	if !ok {
		t.Fatalf("pagerduty payload 结构错误: %T", payload["payload"])
	}
	if payloadData["severity"] != "critical" {
		t.Fatalf("pagerduty 严重级别错误: %v", payloadData["severity"])
	}
	summary, _ := payloadData["summary"].(string)
	if !strings.Contains(summary, "连续失败") {
		t.Fatalf("pagerduty 摘要错误: %q", summary)
	}
	customDetails, ok := payloadData["custom_details"].(map[string]any)
	if !ok {
		t.Fatalf("pagerduty custom_details 结构错误: %T", payloadData["custom_details"])
	}
	if got := customDetails["failure_threshold"]; got != float64(5) {
		t.Fatalf("pagerduty threshold 错误: %v", got)
	}
}

func TestNewAlertManagerFromSpec(t *testing.T) {
	notEnabled, err := NewAlertManagerFromSpec(AlertManagerSpec{}, func(string, ...any) {})
	if err != nil {
		t.Fatalf("默认 spec 不应报错: %v", err)
	}
	if notEnabled != nil {
		t.Fatalf("默认 spec 应返回 nil manager")
	}

	manager, err := NewAlertManagerFromSpec(AlertManagerSpec{
		Enabled:                     true,
		ConsecutiveFailureThreshold: 3,
		LatencySpikeThreshold:       time.Second,
		LatencyAlertCooldown:        time.Minute,
		NotificationTimeout:         time.Second,
		TelegramBotToken:            "bot-token",
		TelegramChatID:              -1001,
		TelegramAPIBaseURL:          "https://api.telegram.org",
	}, func(string, ...any) {})
	if err != nil {
		t.Fatalf("合法 spec 初始化失败: %v", err)
	}
	if manager == nil {
		t.Fatalf("合法 spec 应初始化 manager")
	}
}
