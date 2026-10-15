package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultCloudCostMonitorEnabled              = true
	DefaultCloudCostMonitorInterval             = 10 * time.Minute
	DefaultCloudCostMonitorMonthlyBudget        = 1000.0
	DefaultCloudCostMonitorAlertThresholdRatio  = 0.8
	DefaultCloudCostMonitorGrowthThresholdRatio = 0.25
	DefaultCloudCostMonitorCurrency             = "USD"
	maxCloudCostMonitorGrowthRatio              = 10.0
)

type CloudCostMonitorSpec struct {
	Enabled              bool
	Interval             time.Duration
	MonthlyBudget        float64
	AlertThresholdRatio  float64
	GrowthThresholdRatio float64
	Currency             string
}

func DefaultCloudCostMonitorSpec() CloudCostMonitorSpec {
	return CloudCostMonitorSpec{
		Enabled:              DefaultCloudCostMonitorEnabled,
		Interval:             DefaultCloudCostMonitorInterval,
		MonthlyBudget:        DefaultCloudCostMonitorMonthlyBudget,
		AlertThresholdRatio:  DefaultCloudCostMonitorAlertThresholdRatio,
		GrowthThresholdRatio: DefaultCloudCostMonitorGrowthThresholdRatio,
		Currency:             DefaultCloudCostMonitorCurrency,
	}
}

func NormalizeCloudCostMonitorSpec(spec CloudCostMonitorSpec) (CloudCostMonitorSpec, error) {
	normalized := DefaultCloudCostMonitorSpec()
	normalized.Enabled = spec.Enabled

	if spec.Interval > 0 {
		normalized.Interval = spec.Interval
	}
	if spec.MonthlyBudget > 0 {
		normalized.MonthlyBudget = spec.MonthlyBudget
	}
	if spec.AlertThresholdRatio > 0 {
		normalized.AlertThresholdRatio = spec.AlertThresholdRatio
	}
	if spec.GrowthThresholdRatio > 0 {
		normalized.GrowthThresholdRatio = spec.GrowthThresholdRatio
	}
	if currency := strings.ToUpper(strings.TrimSpace(spec.Currency)); currency != "" {
		normalized.Currency = currency
	}

	if normalized.Interval <= 0 {
		return CloudCostMonitorSpec{}, fmt.Errorf("interval must be greater than 0")
	}
	if normalized.MonthlyBudget <= 0 {
		return CloudCostMonitorSpec{}, fmt.Errorf("monthly budget must be greater than 0")
	}
	if normalized.AlertThresholdRatio <= 0 || normalized.AlertThresholdRatio > 1 {
		return CloudCostMonitorSpec{}, fmt.Errorf("alert threshold ratio must be in range (0,1]")
	}
	if normalized.GrowthThresholdRatio <= 0 || normalized.GrowthThresholdRatio > maxCloudCostMonitorGrowthRatio {
		return CloudCostMonitorSpec{}, fmt.Errorf(
			"growth threshold ratio must be in range (0,%.0f]",
			maxCloudCostMonitorGrowthRatio,
		)
	}
	if !isValidCloudCostCurrency(normalized.Currency) {
		return CloudCostMonitorSpec{}, fmt.Errorf("currency must be a 3-letter ISO code")
	}

	return normalized, nil
}

func LoadCloudCostMonitorSpecFromEnv(getenv func(string) string) (CloudCostMonitorSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultCloudCostMonitorSpec()
	warnings := make([]string, 0, 6)

	enabledRaw := strings.TrimSpace(getenv("CLOUD_COST_MONITOR_ENABLED"))
	if enabledRaw != "" {
		enabled, err := parseCloudCostMonitorBool(enabledRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CLOUD_COST_MONITOR_ENABLED=%q 无效，回退默认值 %t: %v",
					enabledRaw,
					DefaultCloudCostMonitorEnabled,
					err,
				),
			)
		} else {
			spec.Enabled = enabled
		}
	}

	intervalRaw := strings.TrimSpace(getenv("CLOUD_COST_MONITOR_INTERVAL"))
	if intervalRaw != "" {
		interval, err := parseCloudCostMonitorInterval(intervalRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CLOUD_COST_MONITOR_INTERVAL=%q 无效，回退默认值 %s: %v",
					intervalRaw,
					DefaultCloudCostMonitorInterval,
					err,
				),
			)
		} else {
			spec.Interval = interval
		}
	}

	budgetRaw := strings.TrimSpace(getenv("CLOUD_COST_MONTHLY_BUDGET"))
	if budgetRaw != "" {
		budget, err := parseCloudCostMonitorPositiveFloat(budgetRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CLOUD_COST_MONTHLY_BUDGET=%q 无效，回退默认值 %.2f: %v",
					budgetRaw,
					DefaultCloudCostMonitorMonthlyBudget,
					err,
				),
			)
		} else {
			spec.MonthlyBudget = budget
		}
	}

	alertRatioRaw := strings.TrimSpace(getenv("CLOUD_COST_ALERT_RATIO"))
	if alertRatioRaw != "" {
		alertRatio, err := parseCloudCostMonitorRatio(alertRatioRaw, 1)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CLOUD_COST_ALERT_RATIO=%q 无效，回退默认值 %.2f: %v",
					alertRatioRaw,
					DefaultCloudCostMonitorAlertThresholdRatio,
					err,
				),
			)
		} else {
			spec.AlertThresholdRatio = alertRatio
		}
	}

	growthRatioRaw := strings.TrimSpace(getenv("CLOUD_COST_GROWTH_RATIO"))
	if growthRatioRaw != "" {
		growthRatio, err := parseCloudCostMonitorRatio(growthRatioRaw, maxCloudCostMonitorGrowthRatio)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CLOUD_COST_GROWTH_RATIO=%q 无效，回退默认值 %.2f: %v",
					growthRatioRaw,
					DefaultCloudCostMonitorGrowthThresholdRatio,
					err,
				),
			)
		} else {
			spec.GrowthThresholdRatio = growthRatio
		}
	}

	currencyRaw := strings.TrimSpace(getenv("CLOUD_COST_CURRENCY"))
	if currencyRaw != "" {
		currency := strings.ToUpper(currencyRaw)
		if !isValidCloudCostCurrency(currency) {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CLOUD_COST_CURRENCY=%q 无效，回退默认值 %s: currency must be a 3-letter ISO code",
					currencyRaw,
					DefaultCloudCostMonitorCurrency,
				),
			)
		} else {
			spec.Currency = currency
		}
	}

	normalized, err := NormalizeCloudCostMonitorSpec(spec)
	if err != nil {
		return DefaultCloudCostMonitorSpec(), fmt.Errorf("云成本监控配置非法，回退默认值: %w", err)
	}

	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func BuildCloudCostMonitorLog(spec CloudCostMonitorSpec) string {
	normalized, err := NormalizeCloudCostMonitorSpec(spec)
	if err != nil {
		return fmt.Sprintf("云成本监控未启用：配置非法 %v", err)
	}
	if !normalized.Enabled {
		return "云成本监控未启用：CLOUD_COST_MONITOR_ENABLED=false"
	}
	return fmt.Sprintf(
		"云成本监控已启用：interval=%s monthly_budget=%.2f%s alert_threshold=%.0f%% growth_threshold=%.0f%%",
		normalized.Interval,
		normalized.MonthlyBudget,
		normalized.Currency,
		normalized.AlertThresholdRatio*100,
		normalized.GrowthThresholdRatio*100,
	)
}

type CloudCostSample struct {
	Provider    string
	Service     string
	Currency    string
	Cost        float64
	CollectedAt time.Time
}

type CloudCostStat struct {
	Provider     string
	Service      string
	Currency     string
	Cost         float64
	PreviousCost float64
	SamplesTotal int64
	CollectedAt  time.Time
}

func (s CloudCostStat) UsageRatio(monthlyBudget float64) float64 {
	if monthlyBudget <= 0 {
		return 0
	}
	return s.Cost / monthlyBudget
}

func (s CloudCostStat) GrowthRatio() float64 {
	if s.PreviousCost <= 0 {
		return 0
	}
	return (s.Cost - s.PreviousCost) / s.PreviousCost
}

type CloudCostAlert struct {
	CloudCostStat
	MonthlyBudget        float64
	BudgetUsageRatio     float64
	GrowthRatioValue     float64
	AlertThresholdRatio  float64
	GrowthThresholdRatio float64
	OverBudget           bool
	RapidGrowth          bool
}

type CloudCostMonitorOptions struct {
	Interval             time.Duration
	MonthlyBudget        float64
	AlertThresholdRatio  float64
	GrowthThresholdRatio float64
	Currency             string
	Logf                 func(format string, args ...any)
	OnAlert              func(CloudCostAlert)
	OnRecover            func(CloudCostStat)
}

type CloudCostMonitor struct {
	interval             time.Duration
	monthlyBudget        float64
	alertThresholdRatio  float64
	growthThresholdRatio float64
	currency             string
	logf                 func(format string, args ...any)
	onAlert              func(CloudCostAlert)
	onRecover            func(CloudCostStat)

	mu     sync.Mutex
	states map[string]cloudCostState
}

type cloudCostState struct {
	stat     CloudCostStat
	alerting bool
}

func NewCloudCostMonitor(opts CloudCostMonitorOptions) *CloudCostMonitor {
	normalized := normalizeCloudCostMonitorOptions(opts)
	return &CloudCostMonitor{
		interval:             normalized.Interval,
		monthlyBudget:        normalized.MonthlyBudget,
		alertThresholdRatio:  normalized.AlertThresholdRatio,
		growthThresholdRatio: normalized.GrowthThresholdRatio,
		currency:             normalized.Currency,
		logf:                 normalized.Logf,
		onAlert:              normalized.OnAlert,
		onRecover:            normalized.OnRecover,
		states:               make(map[string]cloudCostState),
	}
}

func normalizeCloudCostMonitorOptions(opts CloudCostMonitorOptions) CloudCostMonitorOptions {
	spec, err := NormalizeCloudCostMonitorSpec(CloudCostMonitorSpec{
		Enabled:              true,
		Interval:             opts.Interval,
		MonthlyBudget:        opts.MonthlyBudget,
		AlertThresholdRatio:  opts.AlertThresholdRatio,
		GrowthThresholdRatio: opts.GrowthThresholdRatio,
		Currency:             opts.Currency,
	})
	if err != nil {
		spec = DefaultCloudCostMonitorSpec()
	}
	normalized := opts
	normalized.Interval = spec.Interval
	normalized.MonthlyBudget = spec.MonthlyBudget
	normalized.AlertThresholdRatio = spec.AlertThresholdRatio
	normalized.GrowthThresholdRatio = spec.GrowthThresholdRatio
	normalized.Currency = spec.Currency
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	return normalized
}

func (m *CloudCostMonitor) Run(ctx context.Context) {
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

func (m *CloudCostMonitor) Observe(sample CloudCostSample) {
	if m == nil {
		return
	}
	normalized := normalizeCloudCostSample(sample, m.currency)
	key := buildCloudCostStateKey(normalized.Provider, normalized.Service)

	m.mu.Lock()
	state := m.states[key]
	current := state.stat.Cost
	if current < 0 {
		current = 0
	}
	state.stat = CloudCostStat{
		Provider:     normalized.Provider,
		Service:      normalized.Service,
		Currency:     normalized.Currency,
		Cost:         normalized.Cost,
		PreviousCost: current,
		SamplesTotal: state.stat.SamplesTotal + 1,
		CollectedAt:  normalized.CollectedAt,
	}
	m.states[key] = state
	m.mu.Unlock()
}

func (m *CloudCostMonitor) scanAndAlert() {
	type recoverEvent struct {
		stat CloudCostStat
	}

	alerts := make([]CloudCostAlert, 0, len(m.states))
	recovered := make([]recoverEvent, 0, len(m.states))

	m.mu.Lock()
	for key, state := range m.states {
		alert, high := detectCloudCostAlert(
			state.stat,
			m.monthlyBudget,
			m.alertThresholdRatio,
			m.growthThresholdRatio,
		)
		if high {
			if state.alerting {
				m.states[key] = state
				continue
			}
			state.alerting = true
			m.states[key] = state
			alerts = append(alerts, alert)
			continue
		}
		if state.alerting {
			state.alerting = false
			m.states[key] = state
			recovered = append(recovered, recoverEvent{stat: state.stat})
		}
	}
	m.mu.Unlock()

	for _, alert := range alerts {
		m.logf(
			"云成本预警: provider=%s service=%s current=%.2f%s previous=%.2f%s usage=%.2f%% growth=%.2f%% threshold=%.0f%%/%.0f%% reason=%s",
			alert.Provider,
			alert.Service,
			alert.Cost,
			alert.Currency,
			alert.PreviousCost,
			alert.Currency,
			alert.BudgetUsageRatio*100,
			alert.GrowthRatioValue*100,
			alert.AlertThresholdRatio*100,
			alert.GrowthThresholdRatio*100,
			cloudCostAlertReason(alert),
		)
		if m.onAlert != nil {
			m.onAlert(alert)
		}
	}
	for _, event := range recovered {
		stat := event.stat
		m.logf(
			"云成本恢复: provider=%s service=%s current=%.2f%s previous=%.2f%s usage=%.2f%% growth=%.2f%%",
			stat.Provider,
			stat.Service,
			stat.Cost,
			stat.Currency,
			stat.PreviousCost,
			stat.Currency,
			stat.UsageRatio(m.monthlyBudget)*100,
			stat.GrowthRatio()*100,
		)
		if m.onRecover != nil {
			m.onRecover(stat)
		}
	}
}

func detectCloudCostAlert(
	stat CloudCostStat,
	monthlyBudget float64,
	alertThresholdRatio float64,
	growthThresholdRatio float64,
) (CloudCostAlert, bool) {
	if stat.Cost < 0 || monthlyBudget <= 0 {
		return CloudCostAlert{}, false
	}
	usageRatio := stat.UsageRatio(monthlyBudget)
	growthRatio := stat.GrowthRatio()
	overBudget := usageRatio >= alertThresholdRatio
	rapidGrowth := stat.PreviousCost > 0 && growthRatio >= growthThresholdRatio
	if !overBudget && !rapidGrowth {
		return CloudCostAlert{}, false
	}
	return CloudCostAlert{
		CloudCostStat:        stat,
		MonthlyBudget:        monthlyBudget,
		BudgetUsageRatio:     usageRatio,
		GrowthRatioValue:     growthRatio,
		AlertThresholdRatio:  alertThresholdRatio,
		GrowthThresholdRatio: growthThresholdRatio,
		OverBudget:           overBudget,
		RapidGrowth:          rapidGrowth,
	}, true
}

func normalizeCloudCostSample(sample CloudCostSample, defaultCurrency string) CloudCostSample {
	normalized := sample
	normalized.Provider = strings.ToLower(strings.TrimSpace(normalized.Provider))
	if normalized.Provider == "" {
		normalized.Provider = "unknown"
	}
	normalized.Service = strings.ToLower(strings.TrimSpace(normalized.Service))
	if normalized.Service == "" {
		normalized.Service = "all"
	}
	normalized.Currency = strings.ToUpper(strings.TrimSpace(normalized.Currency))
	if !isValidCloudCostCurrency(normalized.Currency) {
		normalized.Currency = strings.ToUpper(strings.TrimSpace(defaultCurrency))
	}
	if !isValidCloudCostCurrency(normalized.Currency) {
		normalized.Currency = DefaultCloudCostMonitorCurrency
	}
	if normalized.Cost < 0 {
		normalized.Cost = 0
	}
	if normalized.CollectedAt.IsZero() {
		normalized.CollectedAt = time.Now()
	}
	return normalized
}

func buildCloudCostStateKey(provider, service string) string {
	if service == "" {
		return provider
	}
	return fmt.Sprintf("%s|%s", provider, service)
}

func cloudCostAlertReason(alert CloudCostAlert) string {
	reasons := make([]string, 0, 2)
	if alert.OverBudget {
		reasons = append(reasons, "budget")
	}
	if alert.RapidGrowth {
		reasons = append(reasons, "growth")
	}
	if len(reasons) == 0 {
		return "unknown"
	}
	return strings.Join(reasons, "+")
}

func isValidCloudCostCurrency(raw string) bool {
	if len(raw) != 3 {
		return false
	}
	for _, r := range raw {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func parseCloudCostMonitorBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on", "enable", "enabled":
		return true, nil
	case "0", "false", "no", "n", "off", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value")
	}
}

func parseCloudCostMonitorInterval(raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	return value, nil
}

func parseCloudCostMonitorPositiveFloat(raw string) (float64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	return value, nil
}

func parseCloudCostMonitorRatio(raw string, max float64) (float64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if value <= 0 || value > max {
		return 0, fmt.Errorf("must be in range (0,%.2f]", max)
	}
	return value, nil
}
