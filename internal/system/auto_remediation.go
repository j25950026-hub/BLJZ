package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAutoRemediationEnabled  = true
	DefaultAutoRemediationTimeout  = 20 * time.Second
	DefaultAutoRemediationCooldown = 5 * time.Minute

	DefaultAutoRemediationWebhookResetScript = "scripts/remediation/webhook_reset.sh"
	DefaultAutoRemediationRedisEvictScript   = "scripts/remediation/redis_evict.sh"
	DefaultAutoRemediationDBTerminateScript  = "scripts/remediation/db_terminate_idle_tx.sh"
	DefaultAutoRemediationQueueRestartScript = "scripts/remediation/restart_consumer.sh"
	DefaultAutoRemediationAlertSilenceScript = "scripts/remediation/alert_silence.sh"
)

type AutoRemediationFault string

const (
	AutoRemediationFaultWebhook404Spike        AutoRemediationFault = "webhook_404_spike"
	AutoRemediationFaultRedisMemoryHigh        AutoRemediationFault = "redis_memory_high"
	AutoRemediationFaultDBIdleInTransaction    AutoRemediationFault = "db_idle_in_transaction"
	AutoRemediationFaultConsumerHeartbeatStall AutoRemediationFault = "consumer_heartbeat_stalled"
	AutoRemediationFaultAlertStorm             AutoRemediationFault = "alert_storm"
)

var autoRemediationFaultOrder = []AutoRemediationFault{
	AutoRemediationFaultWebhook404Spike,
	AutoRemediationFaultRedisMemoryHigh,
	AutoRemediationFaultDBIdleInTransaction,
	AutoRemediationFaultConsumerHeartbeatStall,
	AutoRemediationFaultAlertStorm,
}

type AutoRemediationSpec struct {
	Enabled bool

	Timeout  time.Duration
	Cooldown time.Duration

	WebhookResetScript string
	RedisEvictScript   string
	DBTerminateScript  string
	QueueRestartScript string
	AlertSilenceScript string
}

type AutoRemediationEvent struct {
	Fault       AutoRemediationFault
	TriggeredAt time.Time
	Metadata    map[string]string
}

type AutoRemediationResult struct {
	Fault       AutoRemediationFault
	Script      string
	TriggeredAt time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	Attempted   bool
	Success     bool
	Skipped     bool
	SkipReason  string
	Error       string
}

type AutoRemediationExecutor func(ctx context.Context, script string, event AutoRemediationEvent) error

type AutoRemediatorOptions struct {
	Spec     AutoRemediationSpec
	Executor AutoRemediationExecutor
	Logf     func(format string, args ...any)
	Now      func() time.Time
}

type AutoRemediator struct {
	timeout  time.Duration
	cooldown time.Duration
	scripts  map[AutoRemediationFault]string
	executor AutoRemediationExecutor
	logf     func(format string, args ...any)
	now      func() time.Time

	mu        sync.Mutex
	lastRunAt map[AutoRemediationFault]time.Time
}

func DefaultAutoRemediationSpec() AutoRemediationSpec {
	return AutoRemediationSpec{
		Enabled:            DefaultAutoRemediationEnabled,
		Timeout:            DefaultAutoRemediationTimeout,
		Cooldown:           DefaultAutoRemediationCooldown,
		WebhookResetScript: DefaultAutoRemediationWebhookResetScript,
		RedisEvictScript:   DefaultAutoRemediationRedisEvictScript,
		DBTerminateScript:  DefaultAutoRemediationDBTerminateScript,
		QueueRestartScript: DefaultAutoRemediationQueueRestartScript,
		AlertSilenceScript: DefaultAutoRemediationAlertSilenceScript,
	}
}

func NormalizeAutoRemediationSpec(spec AutoRemediationSpec) (AutoRemediationSpec, error) {
	normalized := DefaultAutoRemediationSpec()
	normalized.Enabled = spec.Enabled

	switch {
	case spec.Timeout < 0:
		return AutoRemediationSpec{}, fmt.Errorf("timeout must be greater than 0")
	case spec.Timeout > 0:
		normalized.Timeout = spec.Timeout
	}
	switch {
	case spec.Cooldown < 0:
		return AutoRemediationSpec{}, fmt.Errorf("cooldown must be greater than 0")
	case spec.Cooldown > 0:
		normalized.Cooldown = spec.Cooldown
	}

	if webhookScript := strings.TrimSpace(spec.WebhookResetScript); webhookScript != "" {
		normalized.WebhookResetScript = webhookScript
	}
	if redisScript := strings.TrimSpace(spec.RedisEvictScript); redisScript != "" {
		normalized.RedisEvictScript = redisScript
	}
	if dbScript := strings.TrimSpace(spec.DBTerminateScript); dbScript != "" {
		normalized.DBTerminateScript = dbScript
	}
	if queueScript := strings.TrimSpace(spec.QueueRestartScript); queueScript != "" {
		normalized.QueueRestartScript = queueScript
	}
	if alertScript := strings.TrimSpace(spec.AlertSilenceScript); alertScript != "" {
		normalized.AlertSilenceScript = alertScript
	}

	if normalized.Timeout <= 0 {
		return AutoRemediationSpec{}, fmt.Errorf("timeout must be greater than 0")
	}
	if normalized.Cooldown <= 0 {
		return AutoRemediationSpec{}, fmt.Errorf("cooldown must be greater than 0")
	}

	scripts := autoRemediationScriptsFromSpec(normalized)
	for _, fault := range autoRemediationFaultOrder {
		if strings.TrimSpace(scripts[fault]) == "" {
			return AutoRemediationSpec{}, fmt.Errorf("script must not be empty: fault=%s", fault)
		}
	}
	return normalized, nil
}

func LoadAutoRemediationSpecFromEnv(getenv func(string) string) (AutoRemediationSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultAutoRemediationSpec()
	warnings := make([]string, 0, 8)

	enabledRaw := strings.TrimSpace(getenv("AUTO_REMEDIATION_ENABLED"))
	if enabledRaw != "" {
		enabled, err := parseAutoRemediationBool(enabledRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"AUTO_REMEDIATION_ENABLED=%q 无效，回退默认值 %t: %v",
					enabledRaw,
					DefaultAutoRemediationEnabled,
					err,
				),
			)
		} else {
			spec.Enabled = enabled
		}
	}

	timeoutRaw := strings.TrimSpace(getenv("AUTO_REMEDIATION_TIMEOUT"))
	if timeoutRaw != "" {
		timeout, err := parseAutoRemediationDuration(timeoutRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"AUTO_REMEDIATION_TIMEOUT=%q 无效，回退默认值 %s: %v",
					timeoutRaw,
					DefaultAutoRemediationTimeout,
					err,
				),
			)
		} else {
			spec.Timeout = timeout
		}
	}

	cooldownRaw := strings.TrimSpace(getenv("AUTO_REMEDIATION_COOLDOWN"))
	if cooldownRaw != "" {
		cooldown, err := parseAutoRemediationDuration(cooldownRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"AUTO_REMEDIATION_COOLDOWN=%q 无效，回退默认值 %s: %v",
					cooldownRaw,
					DefaultAutoRemediationCooldown,
					err,
				),
			)
		} else {
			spec.Cooldown = cooldown
		}
	}

	parseScriptEnv := func(envKey string, fallback string, assign func(string)) {
		raw := getenv(envKey)
		if raw == "" {
			return
		}
		script := strings.TrimSpace(raw)
		if script == "" {
			warnings = append(
				warnings,
				fmt.Sprintf("%s=%q 无效，回退默认值 %s: must not be empty", envKey, raw, fallback),
			)
			return
		}
		assign(script)
	}

	parseScriptEnv(
		"AUTO_REMEDIATION_WEBHOOK_RESET_SCRIPT",
		DefaultAutoRemediationWebhookResetScript,
		func(script string) { spec.WebhookResetScript = script },
	)
	parseScriptEnv(
		"AUTO_REMEDIATION_REDIS_EVICT_SCRIPT",
		DefaultAutoRemediationRedisEvictScript,
		func(script string) { spec.RedisEvictScript = script },
	)
	parseScriptEnv(
		"AUTO_REMEDIATION_DB_TERMINATE_SCRIPT",
		DefaultAutoRemediationDBTerminateScript,
		func(script string) { spec.DBTerminateScript = script },
	)
	parseScriptEnv(
		"AUTO_REMEDIATION_QUEUE_RESTART_SCRIPT",
		DefaultAutoRemediationQueueRestartScript,
		func(script string) { spec.QueueRestartScript = script },
	)
	parseScriptEnv(
		"AUTO_REMEDIATION_ALERT_SILENCE_SCRIPT",
		DefaultAutoRemediationAlertSilenceScript,
		func(script string) { spec.AlertSilenceScript = script },
	)

	normalized, err := NormalizeAutoRemediationSpec(spec)
	if err != nil {
		return DefaultAutoRemediationSpec(), fmt.Errorf("自动恢复脚本配置非法，回退默认值: %w", err)
	}
	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func BuildAutoRemediationLog(spec AutoRemediationSpec) string {
	normalized, err := NormalizeAutoRemediationSpec(spec)
	if err != nil {
		return fmt.Sprintf("自动恢复脚本未启用：配置非法 %v", err)
	}
	if !normalized.Enabled {
		return "自动恢复脚本未启用：AUTO_REMEDIATION_ENABLED=false"
	}

	scripts := autoRemediationScriptsFromSpec(normalized)
	pairs := make([]string, 0, len(autoRemediationFaultOrder))
	for _, fault := range autoRemediationFaultOrder {
		pairs = append(pairs, fmt.Sprintf("%s=>%s", fault, scripts[fault]))
	}

	return fmt.Sprintf(
		"自动恢复脚本已启用：timeout=%s cooldown=%s scripts=[%s] strategy=常见故障自动执行脚本修复",
		normalized.Timeout,
		normalized.Cooldown,
		strings.Join(pairs, ","),
	)
}

func NewAutoRemediator(opts AutoRemediatorOptions) *AutoRemediator {
	candidate := opts.Spec
	candidate.Enabled = true
	normalized, err := NormalizeAutoRemediationSpec(candidate)
	if err != nil {
		normalized = DefaultAutoRemediationSpec()
	}

	executor := opts.Executor
	if executor == nil {
		executor = executeAutoRemediationScript
	}

	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return &AutoRemediator{
		timeout:   normalized.Timeout,
		cooldown:  normalized.Cooldown,
		scripts:   autoRemediationScriptsFromSpec(normalized),
		executor:  executor,
		logf:      logf,
		now:       nowFn,
		lastRunAt: make(map[AutoRemediationFault]time.Time, len(autoRemediationFaultOrder)),
	}
}

func (r *AutoRemediator) RunBatch(ctx context.Context, events []AutoRemediationEvent) []AutoRemediationResult {
	if r == nil || len(events) == 0 {
		return nil
	}

	results := make([]AutoRemediationResult, 0, len(events))
	for _, event := range events {
		results = append(results, r.RunOnce(ctx, event))
	}
	return results
}

func (r *AutoRemediator) RunOnce(ctx context.Context, event AutoRemediationEvent) AutoRemediationResult {
	if r == nil {
		return AutoRemediationResult{}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	triggeredAt := event.TriggeredAt
	if triggeredAt.IsZero() {
		triggeredAt = r.now()
	}
	if triggeredAt.IsZero() {
		triggeredAt = time.Now()
	}

	result := AutoRemediationResult{
		Fault:       event.Fault,
		TriggeredAt: triggeredAt.UTC(),
	}

	if strings.TrimSpace(string(event.Fault)) == "" {
		result.Error = "fault must not be empty"
		r.logResult(result)
		return result
	}

	script, err := r.scriptForFault(event.Fault)
	if err != nil {
		result.Error = err.Error()
		r.logResult(result)
		return result
	}
	result.Script = script

	startedAt := r.now()
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	result.StartedAt = startedAt.UTC()

	if remaining, skipped := r.shouldSkipByCooldown(event.Fault, startedAt); skipped {
		completedAt := r.now()
		if completedAt.IsZero() || completedAt.Before(startedAt) {
			completedAt = startedAt
		}
		result.CompletedAt = completedAt.UTC()
		result.Skipped = true
		result.SkipReason = "cooldown_not_elapsed"
		result.Error = fmt.Sprintf("remaining=%s", remaining.Round(time.Second))
		r.logResult(result)
		return result
	}

	runCtx := ctx
	cancel := func() {}
	if r.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()

	result.Attempted = true
	if err := r.executor(runCtx, script, event); err != nil {
		result.Error = err.Error()
	} else {
		result.Success = true
	}

	completedAt := r.now()
	if completedAt.IsZero() || completedAt.Before(startedAt) {
		completedAt = startedAt
	}
	result.CompletedAt = completedAt.UTC()

	r.logResult(result)
	return result
}

func (r *AutoRemediator) shouldSkipByCooldown(
	fault AutoRemediationFault,
	startedAt time.Time,
) (remaining time.Duration, skipped bool) {
	if r.cooldown <= 0 {
		return 0, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	lastRunAt, exists := r.lastRunAt[fault]
	if !exists {
		r.lastRunAt[fault] = startedAt
		return 0, false
	}

	elapsed := startedAt.Sub(lastRunAt)
	if elapsed >= r.cooldown {
		r.lastRunAt[fault] = startedAt
		return 0, false
	}
	if elapsed < 0 {
		return r.cooldown, true
	}
	return r.cooldown - elapsed, true
}

func (r *AutoRemediator) scriptForFault(fault AutoRemediationFault) (string, error) {
	script, ok := r.scripts[fault]
	if !ok {
		return "", fmt.Errorf("unsupported fault: %s", fault)
	}
	if strings.TrimSpace(script) == "" {
		return "", fmt.Errorf("script is empty: fault=%s", fault)
	}
	return script, nil
}

func (r *AutoRemediator) logResult(result AutoRemediationResult) {
	elapsed := result.CompletedAt.Sub(result.StartedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	if result.Skipped {
		r.logf(
			"自动恢复脚本跳过: fault=%s script=%s triggered_at=%s started_at=%s elapsed=%s status=skipped reason=%s detail=%q",
			result.Fault,
			result.Script,
			result.TriggeredAt.UTC().Format(time.RFC3339),
			result.StartedAt.UTC().Format(time.RFC3339),
			elapsed.Round(time.Millisecond),
			result.SkipReason,
			result.Error,
		)
		return
	}
	if result.Success {
		r.logf(
			"自动恢复脚本执行完成: fault=%s script=%s triggered_at=%s started_at=%s elapsed=%s status=success",
			result.Fault,
			result.Script,
			result.TriggeredAt.UTC().Format(time.RFC3339),
			result.StartedAt.UTC().Format(time.RFC3339),
			elapsed.Round(time.Millisecond),
		)
		return
	}
	r.logf(
		"自动恢复脚本执行失败: fault=%s script=%s triggered_at=%s started_at=%s elapsed=%s status=failed err=%q",
		result.Fault,
		result.Script,
		result.TriggeredAt.UTC().Format(time.RFC3339),
		result.StartedAt.UTC().Format(time.RFC3339),
		elapsed.Round(time.Millisecond),
		result.Error,
	)
}

func autoRemediationScriptsFromSpec(spec AutoRemediationSpec) map[AutoRemediationFault]string {
	return map[AutoRemediationFault]string{
		AutoRemediationFaultWebhook404Spike:        spec.WebhookResetScript,
		AutoRemediationFaultRedisMemoryHigh:        spec.RedisEvictScript,
		AutoRemediationFaultDBIdleInTransaction:    spec.DBTerminateScript,
		AutoRemediationFaultConsumerHeartbeatStall: spec.QueueRestartScript,
		AutoRemediationFaultAlertStorm:             spec.AlertSilenceScript,
	}
}

func executeAutoRemediationScript(ctx context.Context, script string, event AutoRemediationEvent) error {
	script = strings.TrimSpace(script)
	if script == "" {
		return fmt.Errorf("script must not be empty")
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Env = buildAutoRemediationScriptEnv(event)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	preview := strings.TrimSpace(string(output))
	if preview == "" {
		return err
	}
	const maxOutputPreviewBytes = 256
	if len(preview) > maxOutputPreviewBytes {
		preview = preview[:maxOutputPreviewBytes] + "..."
	}
	return fmt.Errorf("%w: output=%s", err, preview)
}

func buildAutoRemediationScriptEnv(event AutoRemediationEvent) []string {
	env := append([]string{}, os.Environ()...)
	triggeredAt := event.TriggeredAt
	if triggeredAt.IsZero() {
		triggeredAt = time.Now()
	}
	env = append(
		env,
		fmt.Sprintf("AUTO_REMEDIATION_FAULT=%s", event.Fault),
		fmt.Sprintf("AUTO_REMEDIATION_TRIGGERED_AT=%s", triggeredAt.UTC().Format(time.RFC3339)),
	)

	keys := make([]string, 0, len(event.Metadata))
	for key := range event.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		envKey := normalizeAutoRemediationEnvKey(key)
		if envKey == "" {
			continue
		}
		env = append(env, fmt.Sprintf("AUTO_REMEDIATION_%s=%s", envKey, event.Metadata[key]))
	}
	return env
}

func normalizeAutoRemediationEnvKey(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	var builder strings.Builder
	for _, char := range trimmed {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char - ('a' - 'A'))
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		default:
			builder.WriteByte('_')
		}
	}
	normalized := strings.Trim(builder.String(), "_")
	normalized = strings.TrimSpace(normalized)
	return normalized
}

func parseAutoRemediationBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on", "enable", "enabled":
		return true, nil
	case "0", "false", "f", "no", "n", "off", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("must be a boolean")
	}
}

func parseAutoRemediationDuration(raw string) (time.Duration, error) {
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
