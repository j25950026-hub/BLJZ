package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAlertManagerConsecutiveFailureThreshold = 5
	DefaultAlertManagerLatencySpikeThreshold       = 2 * time.Second
	DefaultAlertManagerLatencyAlertCooldown        = 5 * time.Minute
	DefaultAlertManagerNotificationTimeout         = 3 * time.Second
	DefaultAlertManagerTelegramAPIBaseURL          = "https://api.telegram.org"
	DefaultAlertManagerPagerDutyEventURL           = "https://events.pagerduty.com/v2/enqueue"
	DefaultAlertManagerPagerDutySource             = "ledgerbot"
)

type AlertKind string

const (
	AlertKindConsecutiveFailures AlertKind = "consecutive_failures"
	AlertKindLatencySpike        AlertKind = "latency_spike"
)

type AlertEvent struct {
	Kind                        AlertKind
	Source                      string
	OccurredAt                  time.Time
	ConsecutiveFailures         int
	ConsecutiveFailureThreshold int
	Latency                     time.Duration
	LatencyThreshold            time.Duration
}

func (e AlertEvent) Summary() string {
	switch e.Kind {
	case AlertKindConsecutiveFailures:
		return fmt.Sprintf(
			"连续失败达到阈值: source=%s failures=%d threshold=%d",
			e.Source,
			e.ConsecutiveFailures,
			e.ConsecutiveFailureThreshold,
		)
	case AlertKindLatencySpike:
		return fmt.Sprintf(
			"延迟飙升超过阈值: source=%s latency=%s threshold=%s",
			e.Source,
			e.Latency.Round(time.Millisecond),
			e.LatencyThreshold,
		)
	default:
		return fmt.Sprintf("未知告警: source=%s", e.Source)
	}
}

type AlertNotifier interface {
	Name() string
	Notify(ctx context.Context, event AlertEvent) error
}

type AlertManagerSpec struct {
	Enabled                     bool
	ConsecutiveFailureThreshold int
	LatencySpikeThreshold       time.Duration
	LatencyAlertCooldown        time.Duration
	NotificationTimeout         time.Duration
	TelegramBotToken            string
	TelegramChatID              int64
	TelegramAPIBaseURL          string
	PagerDutyRoutingKey         string
	PagerDutyEventURL           string
	PagerDutySource             string
}

func DefaultAlertManagerSpec() AlertManagerSpec {
	return AlertManagerSpec{
		Enabled:                     false,
		ConsecutiveFailureThreshold: DefaultAlertManagerConsecutiveFailureThreshold,
		LatencySpikeThreshold:       DefaultAlertManagerLatencySpikeThreshold,
		LatencyAlertCooldown:        DefaultAlertManagerLatencyAlertCooldown,
		NotificationTimeout:         DefaultAlertManagerNotificationTimeout,
		TelegramAPIBaseURL:          DefaultAlertManagerTelegramAPIBaseURL,
		PagerDutyEventURL:           DefaultAlertManagerPagerDutyEventURL,
		PagerDutySource:             DefaultAlertManagerPagerDutySource,
	}
}

func (s AlertManagerSpec) TelegramEnabled() bool {
	return strings.TrimSpace(s.TelegramBotToken) != "" && s.TelegramChatID != 0
}

func (s AlertManagerSpec) PagerDutyEnabled() bool {
	return strings.TrimSpace(s.PagerDutyRoutingKey) != ""
}

func normalizeAlertManagerSpec(spec AlertManagerSpec) AlertManagerSpec {
	normalized := spec
	if normalized.ConsecutiveFailureThreshold <= 0 {
		normalized.ConsecutiveFailureThreshold = DefaultAlertManagerConsecutiveFailureThreshold
	}
	if normalized.LatencySpikeThreshold <= 0 {
		normalized.LatencySpikeThreshold = DefaultAlertManagerLatencySpikeThreshold
	}
	if normalized.LatencyAlertCooldown <= 0 {
		normalized.LatencyAlertCooldown = DefaultAlertManagerLatencyAlertCooldown
	}
	if normalized.NotificationTimeout <= 0 {
		normalized.NotificationTimeout = DefaultAlertManagerNotificationTimeout
	}
	normalized.TelegramBotToken = strings.TrimSpace(normalized.TelegramBotToken)
	normalized.TelegramAPIBaseURL = strings.TrimSpace(normalized.TelegramAPIBaseURL)
	if normalized.TelegramAPIBaseURL == "" {
		normalized.TelegramAPIBaseURL = DefaultAlertManagerTelegramAPIBaseURL
	}
	normalized.PagerDutyRoutingKey = strings.TrimSpace(normalized.PagerDutyRoutingKey)
	normalized.PagerDutyEventURL = strings.TrimSpace(normalized.PagerDutyEventURL)
	if normalized.PagerDutyEventURL == "" {
		normalized.PagerDutyEventURL = DefaultAlertManagerPagerDutyEventURL
	}
	normalized.PagerDutySource = strings.TrimSpace(normalized.PagerDutySource)
	if normalized.PagerDutySource == "" {
		normalized.PagerDutySource = DefaultAlertManagerPagerDutySource
	}
	return normalized
}

func LoadAlertManagerSpecFromEnv(getenv func(string) string) (AlertManagerSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultAlertManagerSpec()
	warnings := make([]string, 0, 8)
	enabledExplicit := false

	if raw := strings.TrimSpace(getenv("ALERT_MANAGER_ENABLED")); raw != "" {
		enabledExplicit = true
		enabled, err := parseAlertManagerBool(raw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf("ALERT_MANAGER_ENABLED=%q 无效，回退默认值 false: %v", raw, err),
			)
		} else {
			spec.Enabled = enabled
		}
	}

	if raw := strings.TrimSpace(getenv("ALERT_CONSECUTIVE_FAILURE_THRESHOLD")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ALERT_CONSECUTIVE_FAILURE_THRESHOLD=%q 无效，回退默认值 %d",
					raw,
					DefaultAlertManagerConsecutiveFailureThreshold,
				),
			)
		} else {
			spec.ConsecutiveFailureThreshold = parsed
		}
	}

	if raw := strings.TrimSpace(getenv("ALERT_LATENCY_SPIKE_THRESHOLD")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ALERT_LATENCY_SPIKE_THRESHOLD=%q 无效，回退默认值 %s",
					raw,
					DefaultAlertManagerLatencySpikeThreshold,
				),
			)
		} else {
			spec.LatencySpikeThreshold = parsed
		}
	} else if raw := strings.TrimSpace(getenv("ALERT_LATENCY_SPIKE_MS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ALERT_LATENCY_SPIKE_MS=%q 无效，回退默认值 %s",
					raw,
					DefaultAlertManagerLatencySpikeThreshold,
				),
			)
		} else {
			spec.LatencySpikeThreshold = time.Duration(parsed) * time.Millisecond
		}
	}

	if raw := strings.TrimSpace(getenv("ALERT_LATENCY_ALERT_COOLDOWN")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ALERT_LATENCY_ALERT_COOLDOWN=%q 无效，回退默认值 %s",
					raw,
					DefaultAlertManagerLatencyAlertCooldown,
				),
			)
		} else {
			spec.LatencyAlertCooldown = parsed
		}
	}

	if raw := strings.TrimSpace(getenv("ALERT_NOTIFICATION_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ALERT_NOTIFICATION_TIMEOUT=%q 无效，回退默认值 %s",
					raw,
					DefaultAlertManagerNotificationTimeout,
				),
			)
		} else {
			spec.NotificationTimeout = parsed
		}
	}

	spec.TelegramBotToken = strings.TrimSpace(getenv("ALERT_TELEGRAM_BOT_TOKEN"))
	if spec.TelegramBotToken == "" {
		spec.TelegramBotToken = strings.TrimSpace(getenv("BOT_TOKEN"))
	}

	chatIDRaw := strings.TrimSpace(getenv("ALERT_TELEGRAM_CHAT_ID"))
	if chatIDRaw == "" {
		chatIDRaw = strings.TrimSpace(getenv("SUPPORT_CHAT_ID"))
	}
	if chatIDRaw != "" {
		chatID, err := strconv.ParseInt(chatIDRaw, 10, 64)
		if err != nil || chatID == 0 {
			warnings = append(warnings, fmt.Sprintf("ALERT_TELEGRAM_CHAT_ID/SUPPORT_CHAT_ID=%q 无效，已禁用 Telegram 告警", chatIDRaw))
		} else {
			spec.TelegramChatID = chatID
		}
	}

	if raw := strings.TrimSpace(getenv("ALERT_TELEGRAM_API_BASE_URL")); raw != "" {
		if _, err := url.ParseRequestURI(raw); err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ALERT_TELEGRAM_API_BASE_URL=%q 无效，回退默认值 %s",
					raw,
					DefaultAlertManagerTelegramAPIBaseURL,
				),
			)
		} else {
			spec.TelegramAPIBaseURL = raw
		}
	}

	spec.PagerDutyRoutingKey = strings.TrimSpace(getenv("ALERT_PAGERDUTY_ROUTING_KEY"))
	if raw := strings.TrimSpace(getenv("ALERT_PAGERDUTY_EVENT_URL")); raw != "" {
		if _, err := url.ParseRequestURI(raw); err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ALERT_PAGERDUTY_EVENT_URL=%q 无效，回退默认值 %s",
					raw,
					DefaultAlertManagerPagerDutyEventURL,
				),
			)
		} else {
			spec.PagerDutyEventURL = raw
		}
	}
	if source := strings.TrimSpace(getenv("ALERT_PAGERDUTY_SOURCE")); source != "" {
		spec.PagerDutySource = source
	}

	spec = normalizeAlertManagerSpec(spec)

	if spec.TelegramBotToken != "" && spec.TelegramChatID == 0 {
		warnings = append(warnings, "已检测到 Telegram Bot Token，但缺少 ALERT_TELEGRAM_CHAT_ID/SUPPORT_CHAT_ID，Telegram 通道不生效")
		spec.TelegramBotToken = ""
	}
	if spec.TelegramBotToken == "" && spec.TelegramChatID != 0 {
		warnings = append(warnings, "已检测到 Telegram ChatID，但缺少 ALERT_TELEGRAM_BOT_TOKEN/BOT_TOKEN，Telegram 通道不生效")
		spec.TelegramChatID = 0
	}

	if !enabledExplicit {
		spec.Enabled = spec.TelegramEnabled() || spec.PagerDutyEnabled()
	}
	if spec.Enabled && !spec.TelegramEnabled() && !spec.PagerDutyEnabled() {
		warnings = append(warnings, "ALERT_MANAGER_ENABLED=true 但未配置 Telegram/PagerDuty 通道，已自动关闭")
		spec.Enabled = false
	}

	if len(warnings) > 0 {
		return spec, errors.New(strings.Join(warnings, "; "))
	}
	return spec, nil
}

func BuildAlertManagerLog(spec AlertManagerSpec) string {
	normalized := normalizeAlertManagerSpec(spec)
	if !normalized.Enabled {
		return "智能告警未启用：未配置可用告警通道（Telegram/PagerDuty）"
	}

	channels := make([]string, 0, 2)
	if normalized.TelegramEnabled() {
		channels = append(channels, fmt.Sprintf("telegram(chat_id=%d)", normalized.TelegramChatID))
	}
	if normalized.PagerDutyEnabled() {
		channels = append(channels, fmt.Sprintf("pagerduty(source=%s)", normalized.PagerDutySource))
	}
	if len(channels) == 0 {
		return "智能告警未启用：告警开关开启但通道不可用"
	}

	return fmt.Sprintf(
		"智能告警已启用：consecutive_failures>=%d latency_spike>=%s cooldown=%s channels=[%s]",
		normalized.ConsecutiveFailureThreshold,
		normalized.LatencySpikeThreshold,
		normalized.LatencyAlertCooldown,
		strings.Join(channels, ","),
	)
}

type AlertManagerOptions struct {
	ConsecutiveFailureThreshold int
	LatencySpikeThreshold       time.Duration
	LatencyAlertCooldown        time.Duration
	NotificationTimeout         time.Duration
	Logf                        func(format string, args ...any)
	Notifiers                   []AlertNotifier
	Now                         func() time.Time
}

type AlertManager struct {
	consecutiveFailureThreshold int
	latencySpikeThreshold       time.Duration
	latencyAlertCooldown        time.Duration
	notificationTimeout         time.Duration
	logf                        func(format string, args ...any)
	notifiers                   []AlertNotifier
	now                         func() time.Time

	mu     sync.Mutex
	states map[string]alertSourceState
}

type alertSourceState struct {
	consecutiveFailures int
	failureAlerting     bool
	lastLatencyAlertAt  time.Time
}

func NewAlertManager(opts AlertManagerOptions) *AlertManager {
	normalized := normalizeAlertManagerOptions(opts)
	return &AlertManager{
		consecutiveFailureThreshold: normalized.ConsecutiveFailureThreshold,
		latencySpikeThreshold:       normalized.LatencySpikeThreshold,
		latencyAlertCooldown:        normalized.LatencyAlertCooldown,
		notificationTimeout:         normalized.NotificationTimeout,
		logf:                        normalized.Logf,
		notifiers:                   normalized.Notifiers,
		now:                         normalized.Now,
		states:                      make(map[string]alertSourceState),
	}
}

func normalizeAlertManagerOptions(opts AlertManagerOptions) AlertManagerOptions {
	normalized := opts
	if normalized.ConsecutiveFailureThreshold <= 0 {
		normalized.ConsecutiveFailureThreshold = DefaultAlertManagerConsecutiveFailureThreshold
	}
	if normalized.LatencySpikeThreshold <= 0 {
		normalized.LatencySpikeThreshold = DefaultAlertManagerLatencySpikeThreshold
	}
	if normalized.LatencyAlertCooldown <= 0 {
		normalized.LatencyAlertCooldown = DefaultAlertManagerLatencyAlertCooldown
	}
	if normalized.NotificationTimeout <= 0 {
		normalized.NotificationTimeout = DefaultAlertManagerNotificationTimeout
	}
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	if normalized.Now == nil {
		normalized.Now = time.Now
	}
	if len(normalized.Notifiers) == 0 {
		normalized.Notifiers = nil
	} else {
		filtered := make([]AlertNotifier, 0, len(normalized.Notifiers))
		for _, notifier := range normalized.Notifiers {
			if notifier != nil {
				filtered = append(filtered, notifier)
			}
		}
		normalized.Notifiers = filtered
	}
	return normalized
}

func NewAlertManagerFromSpec(spec AlertManagerSpec, logf func(format string, args ...any)) (*AlertManager, error) {
	normalized := normalizeAlertManagerSpec(spec)
	if !normalized.Enabled {
		return nil, nil
	}

	notifiers := make([]AlertNotifier, 0, 2)
	if normalized.TelegramEnabled() {
		telegramNotifier, err := NewTelegramAlertNotifier(TelegramAlertNotifierOptions{
			BotToken:   normalized.TelegramBotToken,
			ChatID:     normalized.TelegramChatID,
			APIBaseURL: normalized.TelegramAPIBaseURL,
			HTTPClient: WrapHTTPClientWithTracing(
				&http.Client{Timeout: normalized.NotificationTimeout},
				"alert-telegram",
			),
		})
		if err != nil {
			return nil, err
		}
		notifiers = append(notifiers, telegramNotifier)
	}
	if normalized.PagerDutyEnabled() {
		pagerDutyNotifier, err := NewPagerDutyAlertNotifier(PagerDutyAlertNotifierOptions{
			RoutingKey: normalized.PagerDutyRoutingKey,
			EventURL:   normalized.PagerDutyEventURL,
			Source:     normalized.PagerDutySource,
			HTTPClient: WrapHTTPClientWithTracing(
				&http.Client{Timeout: normalized.NotificationTimeout},
				"alert-pagerduty",
			),
		})
		if err != nil {
			return nil, err
		}
		notifiers = append(notifiers, pagerDutyNotifier)
	}
	if len(notifiers) == 0 {
		return nil, nil
	}

	return NewAlertManager(AlertManagerOptions{
		ConsecutiveFailureThreshold: normalized.ConsecutiveFailureThreshold,
		LatencySpikeThreshold:       normalized.LatencySpikeThreshold,
		LatencyAlertCooldown:        normalized.LatencyAlertCooldown,
		NotificationTimeout:         normalized.NotificationTimeout,
		Notifiers:                   notifiers,
		Logf:                        logf,
	}), nil
}

func (m *AlertManager) ObserveUpdate(source string, duration time.Duration, success bool) {
	if m == nil || len(m.notifiers) == 0 {
		return
	}

	normalizedSource := normalizeAlertSource(source)
	if duration < 0 {
		duration = 0
	}
	now := m.now()

	events := make([]AlertEvent, 0, 2)
	m.mu.Lock()
	state := m.states[normalizedSource]

	if success {
		state.consecutiveFailures = 0
		state.failureAlerting = false
	} else {
		state.consecutiveFailures++
		if state.consecutiveFailures >= m.consecutiveFailureThreshold && !state.failureAlerting {
			state.failureAlerting = true
			events = append(events, AlertEvent{
				Kind:                        AlertKindConsecutiveFailures,
				Source:                      normalizedSource,
				OccurredAt:                  now,
				ConsecutiveFailures:         state.consecutiveFailures,
				ConsecutiveFailureThreshold: m.consecutiveFailureThreshold,
			})
		}
	}

	if duration >= m.latencySpikeThreshold {
		if state.lastLatencyAlertAt.IsZero() || now.Sub(state.lastLatencyAlertAt) >= m.latencyAlertCooldown {
			state.lastLatencyAlertAt = now
			events = append(events, AlertEvent{
				Kind:             AlertKindLatencySpike,
				Source:           normalizedSource,
				OccurredAt:       now,
				Latency:          duration,
				LatencyThreshold: m.latencySpikeThreshold,
			})
		}
	} else {
		state.lastLatencyAlertAt = time.Time{}
	}

	m.states[normalizedSource] = state
	m.mu.Unlock()

	for _, event := range events {
		m.dispatch(event)
	}
}

func (m *AlertManager) dispatch(event AlertEvent) {
	m.logf("智能告警触发: type=%s source=%s summary=%s", event.Kind, event.Source, event.Summary())
	for _, notifier := range m.notifiers {
		notifyCtx, cancel := context.WithTimeout(context.Background(), m.notificationTimeout)
		err := notifier.Notify(notifyCtx, event)
		cancel()
		if err != nil {
			m.logf("智能告警通知失败: channel=%s type=%s source=%s err=%v", notifier.Name(), event.Kind, event.Source, err)
			continue
		}
		m.logf("智能告警通知成功: channel=%s type=%s source=%s", notifier.Name(), event.Kind, event.Source)
	}
}

func normalizeAlertSource(source string) string {
	normalized := strings.TrimSpace(strings.ToLower(source))
	if normalized == "" {
		return "unknown"
	}
	return normalized
}

type TelegramAlertNotifierOptions struct {
	BotToken   string
	ChatID     int64
	APIBaseURL string
	HTTPClient *http.Client
}

type TelegramAlertNotifier struct {
	endpoint string
	chatID   int64
	client   *http.Client
}

func NewTelegramAlertNotifier(opts TelegramAlertNotifierOptions) (*TelegramAlertNotifier, error) {
	botToken := strings.TrimSpace(opts.BotToken)
	if botToken == "" {
		return nil, errors.New("telegram bot token 不能为空")
	}
	if opts.ChatID == 0 {
		return nil, errors.New("telegram chat id 不能为空")
	}
	apiBaseURL := strings.TrimSpace(opts.APIBaseURL)
	if apiBaseURL == "" {
		apiBaseURL = DefaultAlertManagerTelegramAPIBaseURL
	}
	if _, err := url.ParseRequestURI(apiBaseURL); err != nil {
		return nil, fmt.Errorf("telegram api base url 非法: %w", err)
	}

	client := opts.HTTPClient
	if client == nil {
		client = WrapHTTPClientWithTracing(
			&http.Client{Timeout: DefaultAlertManagerNotificationTimeout},
			"alert-telegram",
		)
	}

	return &TelegramAlertNotifier{
		endpoint: strings.TrimRight(apiBaseURL, "/") + "/bot" + botToken + "/sendMessage",
		chatID:   opts.ChatID,
		client:   client,
	}, nil
}

func (n *TelegramAlertNotifier) Name() string {
	return "telegram"
}

func (n *TelegramAlertNotifier) Notify(ctx context.Context, event AlertEvent) error {
	if n == nil {
		return errors.New("telegram notifier 未初始化")
	}
	payload := struct {
		ChatID                int64  `json:"chat_id"`
		Text                  string `json:"text"`
		DisableWebPagePreview bool   `json:"disable_web_page_preview"`
	}{
		ChatID:                n.chatID,
		Text:                  renderTelegramAlertText(event),
		DisableWebPagePreview: true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram alert payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telegram alert request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram alert failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"telegram alert response status=%d body=%s",
			resp.StatusCode,
			strings.TrimSpace(string(respBody)),
		)
	}

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if len(respBody) > 0 && json.Unmarshal(respBody, &result) == nil && !result.OK {
		return fmt.Errorf("telegram alert rejected: %s", strings.TrimSpace(result.Description))
	}
	return nil
}

type PagerDutyAlertNotifierOptions struct {
	RoutingKey string
	EventURL   string
	Source     string
	HTTPClient *http.Client
}

type PagerDutyAlertNotifier struct {
	routingKey string
	eventURL   string
	source     string
	client     *http.Client
}

func NewPagerDutyAlertNotifier(opts PagerDutyAlertNotifierOptions) (*PagerDutyAlertNotifier, error) {
	routingKey := strings.TrimSpace(opts.RoutingKey)
	if routingKey == "" {
		return nil, errors.New("pagerduty routing key 不能为空")
	}
	eventURL := strings.TrimSpace(opts.EventURL)
	if eventURL == "" {
		eventURL = DefaultAlertManagerPagerDutyEventURL
	}
	if _, err := url.ParseRequestURI(eventURL); err != nil {
		return nil, fmt.Errorf("pagerduty event url 非法: %w", err)
	}
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		source = DefaultAlertManagerPagerDutySource
	}
	client := opts.HTTPClient
	if client == nil {
		client = WrapHTTPClientWithTracing(
			&http.Client{Timeout: DefaultAlertManagerNotificationTimeout},
			"alert-pagerduty",
		)
	}
	return &PagerDutyAlertNotifier{
		routingKey: routingKey,
		eventURL:   eventURL,
		source:     source,
		client:     client,
	}, nil
}

func (n *PagerDutyAlertNotifier) Name() string {
	return "pagerduty"
}

func (n *PagerDutyAlertNotifier) Notify(ctx context.Context, event AlertEvent) error {
	if n == nil {
		return errors.New("pagerduty notifier 未初始化")
	}

	payload := map[string]any{
		"routing_key":  n.routingKey,
		"event_action": "trigger",
		"dedup_key":    buildPagerDutyDedupKey(event),
		"payload": map[string]any{
			"summary": renderPagerDutySummary(event),
			"source":  n.source,
			"severity": func() string {
				if event.Kind == AlertKindConsecutiveFailures {
					return "critical"
				}
				return "warning"
			}(),
			"component": event.Source,
			"group":     "alert-manager",
			"class":     string(event.Kind),
			"custom_details": map[string]any{
				"kind":        string(event.Kind),
				"source":      event.Source,
				"occurred_at": event.OccurredAt.Format(time.RFC3339),
				"summary":     event.Summary(),
			},
		},
	}
	if event.Kind == AlertKindConsecutiveFailures {
		payload["payload"].(map[string]any)["custom_details"].(map[string]any)["consecutive_failures"] = event.ConsecutiveFailures
		payload["payload"].(map[string]any)["custom_details"].(map[string]any)["failure_threshold"] = event.ConsecutiveFailureThreshold
	}
	if event.Kind == AlertKindLatencySpike {
		payload["payload"].(map[string]any)["custom_details"].(map[string]any)["latency_ms"] = event.Latency.Milliseconds()
		payload["payload"].(map[string]any)["custom_details"].(map[string]any)["latency_threshold_ms"] = event.LatencyThreshold.Milliseconds()
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal pagerduty alert payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.eventURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build pagerduty alert request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("send pagerduty alert failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"pagerduty alert response status=%d body=%s",
			resp.StatusCode,
			strings.TrimSpace(string(respBody)),
		)
	}
	return nil
}

func renderTelegramAlertText(event AlertEvent) string {
	lines := []string{
		"🚨【智能告警】",
		"类型：" + alertKindLabel(event.Kind),
		"来源：" + event.Source,
	}
	switch event.Kind {
	case AlertKindConsecutiveFailures:
		lines = append(
			lines,
			fmt.Sprintf("连续失败：%d（阈值 >= %d）", event.ConsecutiveFailures, event.ConsecutiveFailureThreshold),
		)
	case AlertKindLatencySpike:
		lines = append(
			lines,
			fmt.Sprintf(
				"当前延迟：%s（阈值 >= %s）",
				event.Latency.Round(time.Millisecond),
				event.LatencyThreshold,
			),
		)
	}
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	lines = append(lines, "时间："+occurredAt.Format(time.RFC3339))
	return strings.Join(lines, "\n")
}

func renderPagerDutySummary(event AlertEvent) string {
	return "[ledgerbot] " + event.Summary()
}

func buildPagerDutyDedupKey(event AlertEvent) string {
	source := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(event.Source)), " ", "-")
	if source == "" {
		source = "unknown"
	}
	return fmt.Sprintf("ledgerbot-%s-%s", event.Kind, source)
}

func alertKindLabel(kind AlertKind) string {
	switch kind {
	case AlertKindConsecutiveFailures:
		return "连续失败"
	case AlertKindLatencySpike:
		return "延迟飙升"
	default:
		return "未知"
	}
}

func parseAlertManagerBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on", "enable", "enabled":
		return true, nil
	case "0", "false", "no", "n", "off", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value")
	}
}
