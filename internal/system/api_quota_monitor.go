package system

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAPIQuotaMonitorInterval                = 30 * time.Second
	DefaultAPIQuotaMonitorLowRemainingRatio       = 0.2
	DefaultAPIQuotaMonitorLowRemainingCount int64 = 100
)

type APIQuotaSample struct {
	Provider    string
	Endpoint    string
	StatusCode  int
	Limit       int64
	Remaining   int64
	Used        int64
	ResetAt     time.Time
	CollectedAt time.Time
}

type APIQuotaStat struct {
	Provider        string
	Endpoint        string
	RequestsTotal   int64
	FailuresTotal   int64
	Limit           int64
	Remaining       int64
	Used            int64
	LastStatusCode  int
	LastCollectedAt time.Time
	ResetAt         time.Time
}

func (s APIQuotaStat) RemainingRatio() float64 {
	if s.Limit <= 0 || s.Remaining < 0 {
		return 0
	}
	if s.Remaining >= s.Limit {
		return 1
	}
	return float64(s.Remaining) / float64(s.Limit)
}

type APIQuotaAlert struct {
	APIQuotaStat
	RemainingRatio    float64
	LowByRatio        bool
	LowByCount        bool
	LowRemainingRatio float64
	LowRemainingCount int64
}

type APIQuotaMonitorOptions struct {
	Interval          time.Duration
	LowRemainingRatio float64
	LowRemainingCount int64
	Logf              func(format string, args ...any)
	OnAlert           func(APIQuotaAlert)
	OnRecover         func(APIQuotaStat)
}

type APIQuotaMonitor struct {
	interval          time.Duration
	lowRemainingRatio float64
	lowRemainingCount int64
	logf              func(format string, args ...any)
	onAlert           func(APIQuotaAlert)
	onRecover         func(APIQuotaStat)

	mu     sync.Mutex
	states map[string]apiQuotaState
}

type apiQuotaState struct {
	stat     APIQuotaStat
	alerting bool
}

func NewAPIQuotaMonitor(opts APIQuotaMonitorOptions) *APIQuotaMonitor {
	normalized := normalizeAPIQuotaMonitorOptions(opts)
	return &APIQuotaMonitor{
		interval:          normalized.Interval,
		lowRemainingRatio: normalized.LowRemainingRatio,
		lowRemainingCount: normalized.LowRemainingCount,
		logf:              normalized.Logf,
		onAlert:           normalized.OnAlert,
		onRecover:         normalized.OnRecover,
		states:            make(map[string]apiQuotaState),
	}
}

func normalizeAPIQuotaMonitorOptions(opts APIQuotaMonitorOptions) APIQuotaMonitorOptions {
	normalized := opts
	if normalized.Interval <= 0 {
		normalized.Interval = DefaultAPIQuotaMonitorInterval
	}
	if normalized.LowRemainingRatio <= 0 {
		normalized.LowRemainingRatio = DefaultAPIQuotaMonitorLowRemainingRatio
	}
	if normalized.LowRemainingRatio > 1 {
		normalized.LowRemainingRatio = 1
	}
	if normalized.LowRemainingCount <= 0 {
		normalized.LowRemainingCount = DefaultAPIQuotaMonitorLowRemainingCount
	}
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	return normalized
}

func (m *APIQuotaMonitor) Run(ctx context.Context) {
	if m == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.scanAndAlert()
		}
	}
}

func (m *APIQuotaMonitor) Observe(sample APIQuotaSample) {
	if m == nil {
		return
	}
	normalized := normalizeAPIQuotaSample(sample)
	key := buildAPIQuotaStateKey(normalized.Provider, normalized.Endpoint)

	m.mu.Lock()
	state, ok := m.states[key]
	if !ok {
		state = apiQuotaState{
			stat: APIQuotaStat{
				Provider:  normalized.Provider,
				Endpoint:  normalized.Endpoint,
				Limit:     -1,
				Remaining: -1,
				Used:      -1,
			},
		}
	}

	state.stat.RequestsTotal++
	if normalized.StatusCode == 0 || normalized.StatusCode >= 400 {
		state.stat.FailuresTotal++
	}
	if normalized.StatusCode > 0 {
		state.stat.LastStatusCode = normalized.StatusCode
	}
	state.stat.LastCollectedAt = normalized.CollectedAt
	if !normalized.ResetAt.IsZero() {
		state.stat.ResetAt = normalized.ResetAt
	}
	if normalized.Limit >= 0 {
		state.stat.Limit = normalized.Limit
	}
	if normalized.Remaining >= 0 {
		state.stat.Remaining = normalized.Remaining
	}
	if normalized.Used >= 0 {
		state.stat.Used = normalized.Used
	} else if state.stat.Limit >= 0 && state.stat.Remaining >= 0 && state.stat.Remaining <= state.stat.Limit {
		state.stat.Used = state.stat.Limit - state.stat.Remaining
	}

	m.states[key] = state
	m.mu.Unlock()
}

func (m *APIQuotaMonitor) scanAndAlert() {
	type alertEvent struct {
		alert APIQuotaAlert
	}
	type recoverEvent struct {
		stat APIQuotaStat
	}

	alerts := make([]alertEvent, 0, len(m.states))
	recovered := make([]recoverEvent, 0, len(m.states))

	m.mu.Lock()
	for key, state := range m.states {
		alert, low := detectAPIQuotaAlert(state.stat, m.lowRemainingRatio, m.lowRemainingCount)
		if low {
			if state.alerting {
				m.states[key] = state
				continue
			}
			state.alerting = true
			m.states[key] = state
			alerts = append(alerts, alertEvent{alert: alert})
			continue
		}
		if state.alerting {
			state.alerting = false
			m.states[key] = state
			recovered = append(recovered, recoverEvent{stat: state.stat})
		}
	}
	m.mu.Unlock()

	for _, event := range alerts {
		alert := event.alert
		m.logf(
			"API 额度预警: provider=%s endpoint=%s remaining=%s limit=%s used=%s ratio=%.2f%% requests=%d failures=%d threshold=%.0f%%/%d reason=%s",
			alert.Provider,
			alert.Endpoint,
			formatQuotaValue(alert.Remaining),
			formatQuotaValue(alert.Limit),
			formatQuotaValue(alert.Used),
			alert.RemainingRatio*100,
			alert.RequestsTotal,
			alert.FailuresTotal,
			alert.LowRemainingRatio*100,
			alert.LowRemainingCount,
			apiQuotaAlertReason(alert),
		)
		if m.onAlert != nil {
			m.onAlert(alert)
		}
	}
	for _, event := range recovered {
		stat := event.stat
		m.logf(
			"API 额度恢复: provider=%s endpoint=%s remaining=%s limit=%s used=%s ratio=%.2f%% requests=%d failures=%d",
			stat.Provider,
			stat.Endpoint,
			formatQuotaValue(stat.Remaining),
			formatQuotaValue(stat.Limit),
			formatQuotaValue(stat.Used),
			stat.RemainingRatio()*100,
			stat.RequestsTotal,
			stat.FailuresTotal,
		)
		if m.onRecover != nil {
			m.onRecover(stat)
		}
	}
}

func detectAPIQuotaAlert(stat APIQuotaStat, lowRemainingRatio float64, lowRemainingCount int64) (APIQuotaAlert, bool) {
	if stat.Remaining < 0 {
		return APIQuotaAlert{}, false
	}
	remainingRatio := stat.RemainingRatio()
	lowByRatio := stat.Limit > 0 && remainingRatio <= lowRemainingRatio
	lowByCount := stat.Remaining <= lowRemainingCount
	if !lowByRatio && !lowByCount {
		return APIQuotaAlert{}, false
	}
	return APIQuotaAlert{
		APIQuotaStat:      stat,
		RemainingRatio:    remainingRatio,
		LowByRatio:        lowByRatio,
		LowByCount:        lowByCount,
		LowRemainingRatio: lowRemainingRatio,
		LowRemainingCount: lowRemainingCount,
	}, true
}

func normalizeAPIQuotaSample(sample APIQuotaSample) APIQuotaSample {
	normalized := sample
	normalized.Provider = strings.ToLower(strings.TrimSpace(normalized.Provider))
	if normalized.Provider == "" {
		normalized.Provider = "unknown"
	}
	normalized.Endpoint = strings.TrimSpace(normalized.Endpoint)
	if normalized.CollectedAt.IsZero() {
		normalized.CollectedAt = time.Now()
	}
	if normalized.Limit < 0 {
		normalized.Limit = -1
	}
	if normalized.Remaining < 0 {
		normalized.Remaining = -1
	}
	if normalized.Used < 0 {
		normalized.Used = -1
	}
	if normalized.Used == 0 && normalized.Limit > 0 && normalized.Remaining >= 0 && normalized.Remaining < normalized.Limit {
		normalized.Used = normalized.Limit - normalized.Remaining
	}
	if normalized.Used < 0 && normalized.Limit >= 0 && normalized.Remaining >= 0 && normalized.Remaining <= normalized.Limit {
		normalized.Used = normalized.Limit - normalized.Remaining
	}
	return normalized
}

func buildAPIQuotaStateKey(provider, endpoint string) string {
	if endpoint == "" {
		return provider
	}
	return fmt.Sprintf("%s|%s", provider, endpoint)
}

func formatQuotaValue(value int64) string {
	if value < 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d", value)
}

func apiQuotaAlertReason(alert APIQuotaAlert) string {
	reasons := make([]string, 0, 2)
	if alert.LowByCount {
		reasons = append(reasons, "remaining_count")
	}
	if alert.LowByRatio {
		reasons = append(reasons, "remaining_ratio")
	}
	if len(reasons) == 0 {
		return "unknown"
	}
	return strings.Join(reasons, "+")
}
