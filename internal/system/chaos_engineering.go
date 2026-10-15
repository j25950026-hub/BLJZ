package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	DefaultChaosEngineeringEnabled                     = true
	DefaultChaosEngineeringInterval                    = 6 * time.Hour
	DefaultChaosEngineeringDBOutageDuration            = 20 * time.Second
	DefaultChaosEngineeringNetworkInterruptionDuration = 15 * time.Second
)

type ChaosScenario string

const (
	ChaosScenarioDBOutage            ChaosScenario = "db_outage"
	ChaosScenarioNetworkInterruption ChaosScenario = "network_interruption"
)

type ChaosEngineeringSpec struct {
	Enabled                     bool
	Interval                    time.Duration
	DBOutageDuration            time.Duration
	NetworkInterruptionDuration time.Duration
}

type ChaosDrillResult struct {
	Scenario        ChaosScenario
	ScheduledAt     time.Time
	StartedAt       time.Time
	CompletedAt     time.Time
	PlannedDuration time.Duration
	Success         bool
	Error           string
}

type ChaosDrillFunc func(ctx context.Context, plannedDuration time.Duration) error

type ChaosEngineerOptions struct {
	Interval                    time.Duration
	DBOutageDuration            time.Duration
	NetworkInterruptionDuration time.Duration
	RunDBOutage                 ChaosDrillFunc
	RunNetworkInterruption      ChaosDrillFunc
	Logf                        func(format string, args ...any)
	Now                         func() time.Time
}

type ChaosEngineer struct {
	interval                    time.Duration
	dbOutageDuration            time.Duration
	networkInterruptionDuration time.Duration
	runDBOutage                 ChaosDrillFunc
	runNetworkInterruption      ChaosDrillFunc
	logf                        func(format string, args ...any)
	now                         func() time.Time
}

func DefaultChaosEngineeringSpec() ChaosEngineeringSpec {
	return ChaosEngineeringSpec{
		Enabled:                     DefaultChaosEngineeringEnabled,
		Interval:                    DefaultChaosEngineeringInterval,
		DBOutageDuration:            DefaultChaosEngineeringDBOutageDuration,
		NetworkInterruptionDuration: DefaultChaosEngineeringNetworkInterruptionDuration,
	}
}

func NormalizeChaosEngineeringSpec(spec ChaosEngineeringSpec) (ChaosEngineeringSpec, error) {
	normalized := DefaultChaosEngineeringSpec()
	normalized.Enabled = spec.Enabled

	switch {
	case spec.Interval < 0:
		return ChaosEngineeringSpec{}, fmt.Errorf("interval must be greater than 0")
	case spec.Interval > 0:
		normalized.Interval = spec.Interval
	}
	switch {
	case spec.DBOutageDuration < 0:
		return ChaosEngineeringSpec{}, fmt.Errorf("db outage duration must be greater than 0")
	case spec.DBOutageDuration > 0:
		normalized.DBOutageDuration = spec.DBOutageDuration
	}
	switch {
	case spec.NetworkInterruptionDuration < 0:
		return ChaosEngineeringSpec{}, fmt.Errorf("network interruption duration must be greater than 0")
	case spec.NetworkInterruptionDuration > 0:
		normalized.NetworkInterruptionDuration = spec.NetworkInterruptionDuration
	}

	if normalized.Interval <= 0 {
		return ChaosEngineeringSpec{}, fmt.Errorf("interval must be greater than 0")
	}
	if normalized.DBOutageDuration <= 0 {
		return ChaosEngineeringSpec{}, fmt.Errorf("db outage duration must be greater than 0")
	}
	if normalized.NetworkInterruptionDuration <= 0 {
		return ChaosEngineeringSpec{}, fmt.Errorf("network interruption duration must be greater than 0")
	}

	return normalized, nil
}

func LoadChaosEngineeringSpecFromEnv(getenv func(string) string) (ChaosEngineeringSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultChaosEngineeringSpec()
	warnings := make([]string, 0, 4)

	enabledRaw := strings.TrimSpace(getenv("CHAOS_ENGINEERING_ENABLED"))
	if enabledRaw != "" {
		enabled, err := parseChaosEngineeringBool(enabledRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CHAOS_ENGINEERING_ENABLED=%q 无效，回退默认值 %t: %v",
					enabledRaw,
					DefaultChaosEngineeringEnabled,
					err,
				),
			)
		} else {
			spec.Enabled = enabled
		}
	}

	intervalRaw := strings.TrimSpace(getenv("CHAOS_ENGINEERING_INTERVAL"))
	if intervalRaw != "" {
		interval, err := parseChaosEngineeringDuration(intervalRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CHAOS_ENGINEERING_INTERVAL=%q 无效，回退默认值 %s: %v",
					intervalRaw,
					DefaultChaosEngineeringInterval,
					err,
				),
			)
		} else {
			spec.Interval = interval
		}
	}

	dbOutageDurationRaw := strings.TrimSpace(getenv("CHAOS_DB_OUTAGE_DURATION"))
	if dbOutageDurationRaw != "" {
		dbOutageDuration, err := parseChaosEngineeringDuration(dbOutageDurationRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CHAOS_DB_OUTAGE_DURATION=%q 无效，回退默认值 %s: %v",
					dbOutageDurationRaw,
					DefaultChaosEngineeringDBOutageDuration,
					err,
				),
			)
		} else {
			spec.DBOutageDuration = dbOutageDuration
		}
	}

	networkInterruptionDurationRaw := strings.TrimSpace(getenv("CHAOS_NETWORK_INTERRUPTION_DURATION"))
	if networkInterruptionDurationRaw != "" {
		networkInterruptionDuration, err := parseChaosEngineeringDuration(networkInterruptionDurationRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CHAOS_NETWORK_INTERRUPTION_DURATION=%q 无效，回退默认值 %s: %v",
					networkInterruptionDurationRaw,
					DefaultChaosEngineeringNetworkInterruptionDuration,
					err,
				),
			)
		} else {
			spec.NetworkInterruptionDuration = networkInterruptionDuration
		}
	}

	normalized, err := NormalizeChaosEngineeringSpec(spec)
	if err != nil {
		return DefaultChaosEngineeringSpec(), fmt.Errorf("故障演练配置非法，回退默认值: %w", err)
	}
	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func BuildChaosEngineeringLog(spec ChaosEngineeringSpec) string {
	normalized, err := NormalizeChaosEngineeringSpec(spec)
	if err != nil {
		return fmt.Sprintf("故障演练未启用：配置非法 %v", err)
	}
	if !normalized.Enabled {
		return "故障演练未启用：CHAOS_ENGINEERING_ENABLED=false"
	}

	return fmt.Sprintf(
		"故障演练已启用：interval=%s drills=[%s(%s),%s(%s)] strategy=定期模拟故障并验证应急响应",
		normalized.Interval,
		ChaosScenarioDBOutage,
		normalized.DBOutageDuration,
		ChaosScenarioNetworkInterruption,
		normalized.NetworkInterruptionDuration,
	)
}

func NewChaosEngineer(opts ChaosEngineerOptions) *ChaosEngineer {
	normalized := normalizeChaosEngineerOptions(opts)
	return &ChaosEngineer{
		interval:                    normalized.Interval,
		dbOutageDuration:            normalized.DBOutageDuration,
		networkInterruptionDuration: normalized.NetworkInterruptionDuration,
		runDBOutage:                 normalized.RunDBOutage,
		runNetworkInterruption:      normalized.RunNetworkInterruption,
		logf:                        normalized.Logf,
		now:                         normalized.Now,
	}
}

func normalizeChaosEngineerOptions(opts ChaosEngineerOptions) ChaosEngineerOptions {
	normalized := opts
	spec, err := NormalizeChaosEngineeringSpec(ChaosEngineeringSpec{
		Enabled:                     true,
		Interval:                    opts.Interval,
		DBOutageDuration:            opts.DBOutageDuration,
		NetworkInterruptionDuration: opts.NetworkInterruptionDuration,
	})
	if err != nil {
		spec = DefaultChaosEngineeringSpec()
	}
	normalized.Interval = spec.Interval
	normalized.DBOutageDuration = spec.DBOutageDuration
	normalized.NetworkInterruptionDuration = spec.NetworkInterruptionDuration

	if normalized.RunDBOutage == nil {
		normalized.RunDBOutage = func(context.Context, time.Duration) error { return nil }
	}
	if normalized.RunNetworkInterruption == nil {
		normalized.RunNetworkInterruption = func(context.Context, time.Duration) error { return nil }
	}
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	if normalized.Now == nil {
		normalized.Now = time.Now
	}
	return normalized
}

func (e *ChaosEngineer) Run(ctx context.Context) {
	if e == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-ctx.Done():
		return
	default:
	}
	e.RunOnce(ctx, e.now())

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case tickAt := <-ticker.C:
			e.RunOnce(ctx, tickAt)
		}
	}
}

func (e *ChaosEngineer) RunOnce(ctx context.Context, scheduledAt time.Time) []ChaosDrillResult {
	if e == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if scheduledAt.IsZero() {
		scheduledAt = e.now()
	}

	results := make([]ChaosDrillResult, 0, 2)
	results = append(results, e.runDrill(ctx, ChaosScenarioDBOutage, scheduledAt, e.dbOutageDuration, e.runDBOutage))
	results = append(
		results,
		e.runDrill(
			ctx,
			ChaosScenarioNetworkInterruption,
			scheduledAt,
			e.networkInterruptionDuration,
			e.runNetworkInterruption,
		),
	)
	return results
}

func (e *ChaosEngineer) runDrill(
	ctx context.Context,
	scenario ChaosScenario,
	scheduledAt time.Time,
	plannedDuration time.Duration,
	runner ChaosDrillFunc,
) ChaosDrillResult {
	startedAt := e.now()
	err := runner(ctx, plannedDuration)
	completedAt := e.now()
	if completedAt.Before(startedAt) {
		completedAt = startedAt
	}

	result := ChaosDrillResult{
		Scenario:        scenario,
		ScheduledAt:     scheduledAt,
		StartedAt:       startedAt,
		CompletedAt:     completedAt,
		PlannedDuration: plannedDuration,
		Success:         err == nil,
	}
	if err != nil {
		result.Error = err.Error()
	}
	e.logResult(result)
	return result
}

func (e *ChaosEngineer) logResult(result ChaosDrillResult) {
	if result.Success {
		e.logf(
			"故障演练执行完成: scenario=%s scheduled_at=%s started_at=%s elapsed=%s planned_duration=%s status=success",
			result.Scenario,
			result.ScheduledAt.UTC().Format(time.RFC3339),
			result.StartedAt.UTC().Format(time.RFC3339),
			result.CompletedAt.Sub(result.StartedAt).Round(time.Millisecond),
			result.PlannedDuration,
		)
		return
	}
	e.logf(
		"故障演练执行失败: scenario=%s scheduled_at=%s started_at=%s elapsed=%s planned_duration=%s status=failed err=%q",
		result.Scenario,
		result.ScheduledAt.UTC().Format(time.RFC3339),
		result.StartedAt.UTC().Format(time.RFC3339),
		result.CompletedAt.Sub(result.StartedAt).Round(time.Millisecond),
		result.PlannedDuration,
		result.Error,
	)
}

func parseChaosEngineeringBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on", "enable", "enabled":
		return true, nil
	case "0", "false", "no", "n", "off", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value")
	}
}

func parseChaosEngineeringDuration(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	value, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	return value, nil
}
