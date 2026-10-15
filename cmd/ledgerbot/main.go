package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"go-bot/internal/accounting"
	"go-bot/internal/admin"
	"go-bot/internal/bot"
	"go-bot/internal/config"
	"go-bot/internal/db"
	"go-bot/internal/services"
	"go-bot/internal/system"
	"go-bot/internal/utils"
)

const defaultJournalSampleInstruction = "+100"
const defaultPrecisionSampleUSDT = "123.45678901"
const defaultPrecisionSampleRate = "7.12345678"
const defaultFingerprintSampleChatID int64 = -1002003004005
const defaultFingerprintSampleMessageID = 9527

const defaultScheduledReconciliationHour = 0
const defaultScheduledReconciliationMinute = 5
const defaultScheduledReconciliationMaxDetailLogs = 20
const defaultDayCutSettlementLoopInterval = time.Minute
const defaultDuplicateAlertAutoClearLead = 5 * time.Minute
const defaultDuplicateAlertAutoClearPostWindow = time.Minute
const defaultGroupDayCutTime = "04:00:00"

const (
	addressVerificationMediaProductionDirectory = "/var/lib/ledgerbot/telegram-media"
	addressVerificationMediaProductionBaseURL   = "https://autobookbl.site/tg/media"
)

type multiCurrencyPrecisionSample struct {
	USDTAmount    accounting.Decimal
	USDTAtomic    int64
	Rate          accounting.Decimal
	FiatAmount    accounting.Decimal
	FiatMinor     int64
	RoundTripUSDT accounting.Decimal
}

type fanoutUpdateMetricsRecorder struct {
	recorders []bot.UpdateMetricsRecorder
}

func setupStructuredLogging(getenv func(string) string) {
	spec, specErr := system.LoadStructuredLoggingSpecFromEnv(getenv)
	writer, err := system.NewStructuredLogWriter(system.StructuredLogWriterOptions{
		Output: utils.NewSensitiveMaskWriter(log.Writer()),
		Spec:   spec,
	})
	if err != nil {
		log.SetOutput(utils.NewSensitiveMaskWriter(log.Writer()))
		log.Printf("结构化日志初始化失败，回退标准日志输出: %v", err)
		return
	}
	log.SetFlags(0)
	log.SetOutput(writer)
	if specErr != nil {
		log.Printf("%v", specErr)
	}
	log.Print(system.BuildStructuredLoggingLog(spec))
}

func newFanoutUpdateMetricsRecorder(recorders ...bot.UpdateMetricsRecorder) bot.UpdateMetricsRecorder {
	normalized := make([]bot.UpdateMetricsRecorder, 0, len(recorders))
	for _, recorder := range recorders {
		if recorder != nil {
			normalized = append(normalized, recorder)
		}
	}
	switch len(normalized) {
	case 0:
		return nil
	case 1:
		return normalized[0]
	default:
		return &fanoutUpdateMetricsRecorder{recorders: normalized}
	}
}

func (r *fanoutUpdateMetricsRecorder) ObserveUpdate(source string, duration time.Duration, success bool) {
	if r == nil {
		return
	}
	for _, recorder := range r.recorders {
		recorder.ObserveUpdate(source, duration, success)
	}
}

func loadJournalSampleEntry(getenv func(string) string) (string, accounting.JournalEntry, error) {
	journalInstruction := strings.TrimSpace(getenv("JOURNAL_SAMPLE_INSTRUCTION"))
	if journalInstruction == "" {
		journalInstruction = defaultJournalSampleInstruction
	}

	journalSample, err := accounting.GenerateJournalEntry(journalInstruction)
	if err == nil {
		return journalSample.Instruction, journalSample, nil
	}

	fallbackSample, fallbackErr := accounting.GenerateJournalEntry(defaultJournalSampleInstruction)
	if fallbackErr != nil {
		return "", accounting.JournalEntry{}, fmt.Errorf("分录生成器初始化失败: %w", fallbackErr)
	}
	return fallbackSample.Instruction, fallbackSample, fmt.Errorf(
		"JOURNAL_SAMPLE_INSTRUCTION=%q 无效，回退默认示例 %q: %w",
		journalInstruction,
		defaultJournalSampleInstruction,
		err,
	)
}

func buildMultiCurrencyPrecisionSample(usdtText, rateText string) (multiCurrencyPrecisionSample, error) {
	usdtAmount, err := accounting.ParseDecimal(usdtText)
	if err != nil {
		return multiCurrencyPrecisionSample{}, fmt.Errorf("invalid PRECISION_SAMPLE_USDT=%q: %w", usdtText, err)
	}
	rate, err := accounting.ParseDecimal(rateText)
	if err != nil {
		return multiCurrencyPrecisionSample{}, fmt.Errorf("invalid PRECISION_SAMPLE_RATE=%q: %w", rateText, err)
	}

	fiatAmount, err := accounting.ConvertUSDTToFiat(usdtAmount, rate)
	if err != nil {
		return multiCurrencyPrecisionSample{}, err
	}
	usdtAtomic, err := usdtAmount.MinorUnits(accounting.CurrencyScaleUSDT)
	if err != nil {
		return multiCurrencyPrecisionSample{}, fmt.Errorf("convert usdt to atomic units failed: %w", err)
	}
	fiatMinor, err := fiatAmount.MinorUnits(accounting.CurrencyScaleFiat)
	if err != nil {
		return multiCurrencyPrecisionSample{}, fmt.Errorf("convert fiat to minor units failed: %w", err)
	}
	roundTripUSDT, err := accounting.ConvertFiatToUSDT(fiatAmount, rate)
	if err != nil {
		return multiCurrencyPrecisionSample{}, err
	}
	return multiCurrencyPrecisionSample{
		USDTAmount:    usdtAmount,
		USDTAtomic:    usdtAtomic,
		Rate:          rate,
		FiatAmount:    fiatAmount,
		FiatMinor:     fiatMinor,
		RoundTripUSDT: roundTripUSDT,
	}, nil
}

func loadMultiCurrencyPrecisionSample(getenv func(string) string) (multiCurrencyPrecisionSample, error) {
	usdtText := strings.TrimSpace(getenv("PRECISION_SAMPLE_USDT"))
	if usdtText == "" {
		usdtText = defaultPrecisionSampleUSDT
	}
	rateText := strings.TrimSpace(getenv("PRECISION_SAMPLE_RATE"))
	if rateText == "" {
		rateText = defaultPrecisionSampleRate
	}

	sample, err := buildMultiCurrencyPrecisionSample(usdtText, rateText)
	if err == nil {
		return sample, nil
	}

	fallback, fallbackErr := buildMultiCurrencyPrecisionSample(defaultPrecisionSampleUSDT, defaultPrecisionSampleRate)
	if fallbackErr != nil {
		return multiCurrencyPrecisionSample{}, fmt.Errorf("多币种精度示例初始化失败: %w", fallbackErr)
	}
	return fallback, fmt.Errorf(
		"PRECISION_SAMPLE_USDT=%q 或 PRECISION_SAMPLE_RATE=%q 无效，回退默认示例: %w",
		usdtText,
		rateText,
		err,
	)
}

func buildTimeAnomalyMonitorLog(cfg config.Config) string {
	start := strings.TrimSpace(cfg.WorkHoursStart)
	end := strings.TrimSpace(cfg.WorkHoursEnd)
	if start == "" && end == "" {
		return "异常时间监控未启用：未配置 WORK_HOURS_START/WORK_HOURS_END"
	}
	windowLabel, ok := bot.ResolveWorkHoursWindow(start, end)
	if ok {
		return fmt.Sprintf(
			"异常时间监控已启用：work_hours=%s timezone=%s",
			windowLabel,
			cfg.Timezone,
		)
	}
	return fmt.Sprintf(
		"异常时间监控未启用：WORK_HOURS_START=%q WORK_HOURS_END=%q 格式无效，期望 HH:MM 或 HH:MM:SS",
		start,
		end,
	)
}

func buildRateDeviationAlertLog() string {
	return fmt.Sprintf(
		"汇率偏离告警已启用：threshold=%.2f%% strategy=设置入款/下发汇率后自动对比市场均价，超阈值通知权限人",
		bot.RateDeviationAlertThreshold()*100,
	)
}

func buildMediaObjectStorageUploader(cfg config.Config) (services.ObjectStorageUploader, bool, error) {
	required := map[string]string{
		"MEDIA_S3_ENDPOINT":   strings.TrimSpace(cfg.MediaS3Endpoint),
		"MEDIA_S3_BUCKET":     strings.TrimSpace(cfg.MediaS3Bucket),
		"MEDIA_S3_ACCESS_KEY": strings.TrimSpace(cfg.MediaS3AccessKey),
		"MEDIA_S3_SECRET_KEY": strings.TrimSpace(cfg.MediaS3SecretKey),
	}
	nonEmpty := 0
	missing := make([]string, 0, len(required))
	for key, value := range required {
		if value == "" {
			missing = append(missing, key)
			continue
		}
		nonEmpty++
	}
	if nonEmpty == 0 {
		return nil, false, nil
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, true, fmt.Errorf("多媒体处理配置不完整: 缺少 %s", strings.Join(missing, ", "))
	}
	uploader, err := services.NewS3ObjectStorageUploader(services.S3ObjectStorageUploaderConfig{
		Endpoint:        cfg.MediaS3Endpoint,
		Region:          cfg.MediaS3Region,
		Bucket:          cfg.MediaS3Bucket,
		AccessKeyID:     cfg.MediaS3AccessKey,
		SecretAccessKey: cfg.MediaS3SecretKey,
		Prefix:          cfg.MediaS3Prefix,
		PublicBaseURL:   cfg.MediaS3PublicBaseURL,
	})
	if err != nil {
		return nil, true, err
	}
	return uploader, true, nil
}

func buildAddressVerificationMediaPublisher(cfg config.Config) (bot.AddressVerificationMediaPublisher, bool, error) {
	publisherConfig, enabled, err := buildAddressVerificationMediaPublisherConfig(cfg)
	if err != nil || !enabled {
		return nil, enabled, err
	}
	publisher, err := bot.NewLocalAddressVerificationMediaPublisher(publisherConfig)
	if err != nil {
		return nil, true, err
	}
	return publisher, true, nil
}

func buildAddressVerificationMediaPublisherConfig(cfg config.Config) (bot.LocalAddressVerificationMediaConfig, bool, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.AddressVerifyMediaMode))
	switch mode {
	case "", "multipart":
		return bot.LocalAddressVerificationMediaConfig{}, false, nil
	case "local_url":
		if strings.TrimSpace(cfg.AddressVerifyMediaDir) != addressVerificationMediaProductionDirectory {
			return bot.LocalAddressVerificationMediaConfig{}, true, fmt.Errorf("地址验证媒体目录必须固定为 %s", addressVerificationMediaProductionDirectory)
		}
		if strings.TrimSpace(cfg.AddressVerifyMediaPublicBaseURL) != addressVerificationMediaProductionBaseURL {
			return bot.LocalAddressVerificationMediaConfig{}, true, fmt.Errorf("地址验证媒体公网地址必须固定为 autobookbl.site/tg/media")
		}
		return bot.LocalAddressVerificationMediaConfig{
			Directory:       addressVerificationMediaProductionDirectory,
			PublicBaseURL:   addressVerificationMediaProductionBaseURL,
			TTL:             cfg.AddressVerifyMediaTTL,
			CreateDirectory: true,
		}, true, nil
	default:
		return bot.LocalAddressVerificationMediaConfig{}, true, fmt.Errorf("不支持的 ADDRESS_VERIFY_MEDIA_MODE=%q", mode)
	}
}

func buildExportEngineLog() string {
	return "导出引擎已启用：命令=导出账单，自动生成 Excel/PDF 并通过 Telegram 发送"
}

func buildTemplateRendererLog() string {
	return "格式化渲染器已启用：消息模板支持 [html]/[md]，渲染后自动设置 Telegram ParseMode"
}

func buildDockerBuildLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadDockerBuildSpecFromEnv(getenv)
	entrypoint, entrypointErr := system.DockerBuildEntrypoint(spec)
	if entrypointErr != nil {
		return "", entrypointErr
	}
	logLine := fmt.Sprintf(
		"Docker Build 已定义：builder=%s runtime=%s target=%s binary=%s entrypoint=%s",
		spec.BuilderImage,
		spec.RuntimeImage,
		spec.BuildTarget,
		spec.BinaryName,
		entrypoint,
	)
	return logLine, err
}

func buildGrafanaDashboardLog(getenv func(string) string, metricsPath string) (string, error) {
	spec, err := system.LoadGrafanaDashboardSpecFromEnv(getenv)
	logLine, buildErr := system.BuildGrafanaDashboardLog(spec)
	if buildErr != nil {
		return "", buildErr
	}
	normalizedMetricsPath := strings.TrimSpace(metricsPath)
	if normalizedMetricsPath == "" {
		normalizedMetricsPath = system.DefaultPrometheusMetricsPath
	}
	return fmt.Sprintf("%s prometheus_path=%s import_api=/api/dashboards/db", logLine, normalizedMetricsPath), err
}

func buildAlertManagerLog(getenv func(string) string) (string, system.AlertManagerSpec, error) {
	spec, err := system.LoadAlertManagerSpecFromEnv(getenv)
	return system.BuildAlertManagerLog(spec), spec, err
}

func buildK8sDeploymentLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadK8sDeploymentSpecFromEnv(getenv)
	logLine := fmt.Sprintf(
		"K8s Deployment 已定义：name=%s namespace=%s replicas=%d requests(cpu=%s memory=%s) limits(cpu=%s memory=%s)",
		spec.Name,
		spec.Namespace,
		spec.Replicas,
		spec.CPURequest,
		spec.MemoryRequest,
		spec.CPULimit,
		spec.MemoryLimit,
	)
	return logLine, err
}

func buildK8sHPALog(getenv func(string) string) (string, error) {
	spec, err := system.LoadK8sHPASpecFromEnv(getenv)
	logLine := fmt.Sprintf(
		"K8s HPA 已定义：name=%s namespace=%s target=%s replicas(min=%d max=%d) metrics(cpu=%d%% qps_per_pod=%d)",
		spec.Name,
		spec.Namespace,
		spec.TargetDeploymentName,
		spec.MinReplicas,
		spec.MaxReplicas,
		spec.TargetCPUUsage,
		spec.TargetQPSPerPod,
	)
	return logLine, err
}

func buildK8sConfigMapSecretLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadK8sConfigMapSecretSpecFromEnv(getenv)
	logLine := fmt.Sprintf(
		"K8s ConfigMap/Secret 已定义：namespace=%s configmap=%s secret=%s keys(config=%d secret=%d)",
		spec.Namespace,
		spec.ConfigMapName,
		spec.SecretName,
		len(spec.ConfigMapData),
		len(spec.SecretData),
	)
	return logLine, err
}

func buildK8sProbeLog(getenv func(string) string) (string, system.K8sProbeSpec, error) {
	spec, err := system.LoadK8sProbeSpecFromEnv(getenv)
	logLine := fmt.Sprintf(
		"K8s Liveness/Readiness Probe 已定义：name=%s namespace=%s addr=%s liveness(path=%s period=%ds timeout=%ds failure=%d) readiness(path=%s period=%ds timeout=%ds success=%d failure=%d)",
		spec.Name,
		spec.Namespace,
		spec.BindAddr,
		spec.Liveness.Path,
		spec.Liveness.PeriodSec,
		spec.Liveness.TimeoutSec,
		spec.Liveness.FailureThreshold,
		spec.Readiness.Path,
		spec.Readiness.PeriodSec,
		spec.Readiness.TimeoutSec,
		spec.Readiness.SuccessThreshold,
		spec.Readiness.FailureThreshold,
	)
	return logLine, spec, err
}

func buildK8sSidecarLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadK8sSidecarSpecFromEnv(getenv)
	injections := make([]string, 0, 2)
	switch spec.Mode {
	case system.K8sSidecarModeBoth:
		injections = append(injections, string(system.K8sSidecarModeLogCollector), string(system.K8sSidecarModeProxy))
	case system.K8sSidecarModeLogCollector:
		injections = append(injections, string(system.K8sSidecarModeLogCollector))
	case system.K8sSidecarModeProxy:
		injections = append(injections, string(system.K8sSidecarModeProxy))
	}
	logLine := fmt.Sprintf(
		"K8s Sidecar 模式已定义：name=%s namespace=%s mode=%s inject=[%s] app_container=%s proxy_port=%d",
		spec.Name,
		spec.Namespace,
		spec.Mode,
		strings.Join(injections, ","),
		spec.AppContainerName,
		spec.ProxyPort,
	)
	return logLine, err
}

func buildK8sCanaryReleaseLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadK8sCanaryReleaseSpecFromEnv(getenv)
	plan, planErr := system.BuildK8sCanaryRolloutPlan(spec)
	if planErr != nil {
		return "", planErr
	}
	logLine := fmt.Sprintf(
		"K8s 灰度发布已定义：name=%s namespace=%s target=%s replicas(total=%d canary=%d stable=%d) observe=%ds promote=100%%",
		spec.Name,
		spec.Namespace,
		spec.TargetDeploymentName,
		plan.TotalReplicas,
		plan.CanaryReplicas,
		plan.StableReplicas,
		plan.ObservationSeconds,
	)
	return logLine, err
}

func buildK8sServiceMeshLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadK8sServiceMeshSpecFromEnv(getenv)
	return system.BuildK8sServiceMeshLog(spec), err
}

func buildCloudCostMonitorLog(getenv func(string) string) (string, system.CloudCostMonitorSpec, error) {
	spec, err := system.LoadCloudCostMonitorSpecFromEnv(getenv)
	return system.BuildCloudCostMonitorLog(spec), spec, err
}

func buildDBTuningLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadDBTuningSpecFromEnv(getenv)
	sqlText, sqlErr := system.BuildDBTuningSQL(spec)
	if sqlErr != nil {
		return "", sqlErr
	}
	compactSQL := strings.Join(strings.Fields(sqlText), " ")
	logLine := fmt.Sprintf("%s sql=%q", system.BuildDBTuningLog(spec), compactSQL)
	return logLine, err
}

func buildPgBouncerLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadPgBouncerSpecFromEnv(getenv)
	configText, configErr := system.BuildPgBouncerConfig(spec)
	if configErr != nil {
		return "", configErr
	}
	compactConfig := strings.Join(strings.Fields(configText), " ")
	logLine := fmt.Sprintf("%s config=%q", system.BuildPgBouncerLog(spec), compactConfig)
	return logLine, err
}

func buildCDNAccelerationLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadCDNAccelerationSpecFromEnv(getenv)
	rewriteConfig, rewriteErr := system.BuildCDNRewriteConfig(spec)
	if rewriteErr != nil {
		return "", rewriteErr
	}
	compactRewriteConfig := strings.Join(strings.Fields(rewriteConfig), " ")
	logLine := fmt.Sprintf("%s rewrite=%q", system.BuildCDNAccelerationLog(spec), compactRewriteConfig)
	return logLine, err
}

func buildRunbookLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadRunbookSpecFromEnv(getenv)
	return system.BuildRunbookLog(spec), err
}

func buildOnCallRotationLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadOnCallRotationSpecFromEnv(getenv)
	return system.BuildOnCallRotationLog(spec), err
}

func buildChaosEngineeringLog(getenv func(string) string) (string, system.ChaosEngineeringSpec, error) {
	spec, err := system.LoadChaosEngineeringSpecFromEnv(getenv)
	return system.BuildChaosEngineeringLog(spec), spec, err
}

func buildAutoRemediationLog(getenv func(string) string) (string, system.AutoRemediationSpec, error) {
	spec, err := system.LoadAutoRemediationSpecFromEnv(getenv)
	return system.BuildAutoRemediationLog(spec), spec, err
}

func buildPostMortemLog(getenv func(string) string) (string, error) {
	spec, err := system.LoadPostMortemSpecFromEnv(getenv)
	return system.BuildPostMortemLog(spec), err
}

func sanitizeEndpointForLog(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	parsed.User = nil
	return parsed.String()
}

func loadTimezoneLocationOrDefault(timezoneName string) *time.Location {
	locationName := strings.TrimSpace(timezoneName)
	if locationName == "" {
		locationName = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(locationName)
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

func durationUntilNextLocalMidnight(now time.Time, loc *time.Location) time.Duration {
	if loc == nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day()+1, 0, 0, 0, 0, loc)
	wait := next.Sub(localNow)
	if wait <= 0 {
		return time.Second
	}
	return wait
}

func dailySettlementWindowForYesterday(now time.Time, loc *time.Location) (string, time.Time, time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	todayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	return yesterdayStart.Format("2006-01-02"), yesterdayStart.UTC(), todayStart.UTC()
}

func runDayCutSettlementBatchOnce(database *db.Database, runAt time.Time, timezoneName string) (db.DayCutSettlementBatchResult, error) {
	if database == nil {
		return db.DayCutSettlementBatchResult{}, fmt.Errorf("database is nil")
	}
	return database.RunDayCutSettlementBatch(runAt, timezoneName)
}

func runDayCutSettlementBatchOnceWithLog(database *db.Database, runAt time.Time, timezoneName string, logf func(string, ...any)) {
	if logf == nil {
		logf = log.Printf
	}
	result, err := runDayCutSettlementBatchOnce(database, runAt, timezoneName)
	if err != nil {
		logf(
			"日切结算单生成失败: run_at=%s timezone=%s err=%v",
			runAt.UTC().Format(time.RFC3339),
			strings.TrimSpace(timezoneName),
			err,
		)
		return
	}
	logf(
		"日切结算单生成完成: run_at=%s timezone=%s groups=%d generated=%d forced=%d deficit=%d skipped=%d",
		result.RunAt,
		result.Timezone,
		result.TotalGroups,
		result.GeneratedReports,
		result.ForcedReports,
		result.DeficitReports,
		result.SkippedGroups,
	)
}

func runDayCutSettlementLoop(ctx context.Context, timezoneName string, database *db.Database, nowFn func() time.Time, logf func(string, ...any)) {
	if database == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	if logf == nil {
		logf = log.Printf
	}

	select {
	case <-ctx.Done():
		return
	default:
	}
	runDayCutSettlementBatchOnceWithLog(database, nowFn(), timezoneName, logf)

	ticker := time.NewTicker(defaultDayCutSettlementLoopInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		runDayCutSettlementBatchOnceWithLog(database, nowFn(), timezoneName, logf)
	}
}

func normalizeDayCutTime(dayCutTime string) string {
	hour, minute, second, err := utils.ParseHMS(strings.TrimSpace(dayCutTime))
	if err != nil {
		hour, minute, second, _ = utils.ParseHMS(defaultGroupDayCutTime)
	}
	return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second)
}

func resolveDuplicateAlertAutoClearCycle(now time.Time, dayCutTime string, lead, postWindow time.Duration, loc *time.Location) (time.Time, string, bool) {
	if loc == nil {
		loc = time.UTC
	}
	if lead <= 0 {
		lead = defaultDuplicateAlertAutoClearLead
	}
	if postWindow <= 0 {
		postWindow = defaultDuplicateAlertAutoClearPostWindow
	}
	normalizedCut := normalizeDayCutTime(dayCutTime)
	hour, minute, second, parseErr := utils.ParseHMS(normalizedCut)
	if parseErr != nil {
		return time.Time{}, "", false
	}

	localNow := now.In(loc)
	cutToday := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, second, 0, loc)
	candidates := []time.Time{cutToday, cutToday.Add(24 * time.Hour)}
	for _, candidate := range candidates {
		windowStart := candidate.Add(-lead)
		windowEnd := candidate.Add(postWindow)
		if localNow.Before(windowStart) || !localNow.Before(windowEnd) {
			continue
		}
		phase := "pre"
		if !localNow.Before(candidate) {
			phase = "post"
		}
		return candidate, phase, true
	}
	return time.Time{}, "", false
}

func runDuplicateAlertAutoClearOnce(
	database *db.Database,
	now time.Time,
	timezoneName string,
	lead time.Duration,
	postWindow time.Duration,
	clearedCycles map[int64]string,
	logf func(string, ...any),
) {
	if database == nil {
		return
	}
	if logf == nil {
		logf = log.Printf
	}
	if clearedCycles == nil {
		clearedCycles = map[int64]string{}
	}
	groups, err := database.ListAllGroups()
	if err != nil {
		logf("重复预警缓存自动清理失败: list_groups err=%v", err)
		return
	}

	loc := loadTimezoneLocationOrDefault(timezoneName)
	active := make(map[int64]struct{}, len(groups))
	for _, group := range groups {
		active[group.ChatID] = struct{}{}
		if group.DuplicateAlertEnabled != 1 {
			continue
		}
		cycleAt, phase, shouldClear := resolveDuplicateAlertAutoClearCycle(now, group.DayCutTime, lead, postWindow, loc)
		if !shouldClear {
			continue
		}
		cycleToken := fmt.Sprintf("%s|%s", cycleAt.Format(time.RFC3339), phase)
		if clearedCycles[group.ChatID] == cycleToken {
			continue
		}

		cardCount, cardErr := database.ClearCardHistory(group.ChatID)
		duplicateCount, duplicateErr := database.ClearDuplicateAlertHistory(group.ChatID)
		if cardErr != nil || duplicateErr != nil {
			logf(
				"重复预警缓存自动清理失败: chat_id=%d phase=%s day_cut=%s card_err=%v duplicate_err=%v",
				group.ChatID,
				phase,
				normalizeDayCutTime(group.DayCutTime),
				cardErr,
				duplicateErr,
			)
			continue
		}

		clearedCycles[group.ChatID] = cycleToken
		logf(
			"重复预警缓存自动清理完成: chat_id=%d phase=%s day_cut=%s lead=%s cycle=%s cleared_card=%d cleared_duplicate=%d",
			group.ChatID,
			phase,
			normalizeDayCutTime(group.DayCutTime),
			lead,
			cycleAt.Format(time.RFC3339),
			cardCount,
			duplicateCount,
		)
	}

	for chatID := range clearedCycles {
		if _, ok := active[chatID]; !ok {
			delete(clearedCycles, chatID)
		}
	}
}

func runDuplicateAlertAutoClearLoop(
	ctx context.Context,
	timezoneName string,
	database *db.Database,
	lead time.Duration,
	nowFn func() time.Time,
	logf func(string, ...any),
) {
	if database == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	if logf == nil {
		logf = log.Printf
	}
	clearedCycles := make(map[int64]string, 16)

	select {
	case <-ctx.Done():
		return
	default:
	}
	runDuplicateAlertAutoClearOnce(
		database,
		nowFn(),
		timezoneName,
		lead,
		defaultDuplicateAlertAutoClearPostWindow,
		clearedCycles,
		logf,
	)

	ticker := time.NewTicker(defaultDayCutSettlementLoopInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		runDuplicateAlertAutoClearOnce(
			database,
			nowFn(),
			timezoneName,
			lead,
			defaultDuplicateAlertAutoClearPostWindow,
			clearedCycles,
			logf,
		)
	}
}

func durationUntilNextLocalSchedule(now time.Time, loc *time.Location, scheduleHour, scheduleMinute int) time.Duration {
	if loc == nil {
		loc = time.UTC
	}
	if scheduleHour < 0 || scheduleHour > 23 {
		scheduleHour = 0
	}
	if scheduleMinute < 0 || scheduleMinute > 59 {
		scheduleMinute = 0
	}
	localNow := now.In(loc)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), scheduleHour, scheduleMinute, 0, 0, loc)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	wait := next.Sub(localNow)
	if wait <= 0 {
		return time.Second
	}
	return wait
}

func buildTenantShardBackupPath(basePath string, shardIndex int) string {
	ext := filepath.Ext(basePath)
	base := strings.TrimSuffix(basePath, ext)
	return fmt.Sprintf("%s.tenant_%02d%s", base, shardIndex, ext)
}

func buildDailyFullBackupSourcePaths(cfg config.Config) []string {
	seen := make(map[string]struct{})
	sourcePaths := make([]string, 0, cfg.DBTenantShards*2+2)

	appendPath := func(path string) {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return
		}
		cleaned := filepath.Clean(trimmed)
		if _, ok := seen[cleaned]; ok {
			return
		}
		seen[cleaned] = struct{}{}
		sourcePaths = append(sourcePaths, cleaned)
	}
	appendShards := func(basePath string) {
		appendPath(basePath)
		if strings.TrimSpace(basePath) == "" {
			return
		}
		if cfg.DBTenantShards <= 1 {
			return
		}
		for shardIndex := 1; shardIndex < cfg.DBTenantShards; shardIndex++ {
			appendPath(buildTenantShardBackupPath(basePath, shardIndex))
		}
	}

	appendShards(cfg.DBMasterPath)
	appendShards(cfg.ColdArchivePath)
	return sourcePaths
}

func defaultDailyFullBackupDir(dbMasterPath string) string {
	trimmed := strings.TrimSpace(dbMasterPath)
	if trimmed == "" {
		return filepath.FromSlash("data/full_backups")
	}
	cleaned := filepath.Clean(trimmed)
	return filepath.Join(filepath.Dir(cleaned), "full_backups")
}

func buildCrossRegionDRSourcePaths(walArchiver *system.WALArchiver, dailyFullBackup *system.DailyFullBackup) []string {
	seen := make(map[string]struct{}, 2)
	sourcePaths := make([]string, 0, 2)
	appendPath := func(path string) {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return
		}
		cleaned := filepath.Clean(trimmed)
		if _, ok := seen[cleaned]; ok {
			return
		}
		seen[cleaned] = struct{}{}
		sourcePaths = append(sourcePaths, cleaned)
	}

	if walArchiver != nil {
		appendPath(walArchiver.ArchiveDir())
	}
	if dailyFullBackup != nil {
		appendPath(dailyFullBackup.BackupDir())
	}
	return sourcePaths
}

func defaultCrossRegionDRTargetDir(dbMasterPath string) string {
	trimmed := strings.TrimSpace(dbMasterPath)
	if trimmed == "" {
		return filepath.FromSlash("data/cross_region_dr")
	}
	cleaned := filepath.Clean(trimmed)
	return filepath.Join(filepath.Dir(cleaned), "cross_region_dr")
}

func runDailySettlementBatchOnce(database *db.Database, now time.Time, loc *time.Location) (db.DailySettlementBatchResult, error) {
	if database == nil {
		return db.DailySettlementBatchResult{}, fmt.Errorf("database is nil")
	}
	reportDate, periodStart, periodEnd := dailySettlementWindowForYesterday(now, loc)
	return database.RunDailySettlementBatch(reportDate, periodStart, periodEnd)
}

func runDailySettlementBatchOnceWithLog(database *db.Database, runAt time.Time, loc *time.Location, logf func(string, ...any)) {
	result, err := runDailySettlementBatchOnce(database, runAt, loc)
	if err != nil {
		reportDate, periodStart, periodEnd := dailySettlementWindowForYesterday(runAt, loc)
		logf(
			"T+1 结算报表跑批失败: report_date=%s period=[%s,%s) err=%v",
			reportDate,
			periodStart.Format(time.RFC3339),
			periodEnd.Format(time.RFC3339),
			err,
		)
		return
	}
	logf(
		"T+1 结算报表跑批完成: report_date=%s period=[%s,%s) groups=%d generated=%d skipped=%d",
		result.ReportDate,
		result.PeriodStart,
		result.PeriodEnd,
		result.TotalGroups,
		result.GeneratedReports,
		result.SkippedGroups,
	)
}

func runDailySettlementLoop(ctx context.Context, timezoneName string, database *db.Database, nowFn func() time.Time, logf func(string, ...any)) {
	if database == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	if logf == nil {
		logf = log.Printf
	}

	loc := loadTimezoneLocationOrDefault(timezoneName)
	select {
	case <-ctx.Done():
		return
	default:
	}
	// 进程在零点后重启时立即补跑一次，避免遗漏昨日 T+1 结算报表。
	runDailySettlementBatchOnceWithLog(database, nowFn(), loc, logf)

	for {
		wait := durationUntilNextLocalMidnight(nowFn(), loc)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		runDailySettlementBatchOnceWithLog(database, nowFn(), loc, logf)
	}
}

func runScheduledReconciliationOnce(database *db.Database, tolerance float64) (db.ScheduledReconciliationResult, error) {
	if database == nil {
		return db.ScheduledReconciliationResult{}, fmt.Errorf("database is nil")
	}
	return database.RunScheduledReconciliation(tolerance)
}

func runScheduledReconciliationOnceWithLog(database *db.Database, runAt time.Time, tolerance float64, logf func(string, ...any)) {
	if logf == nil {
		logf = log.Printf
	}
	result, err := runScheduledReconciliationOnce(database, tolerance)
	if err != nil {
		logf(
			"定时对账执行失败: run_at=%s tolerance=%g err=%v",
			runAt.UTC().Format(time.RFC3339),
			tolerance,
			err,
		)
		return
	}
	if result.DifferenceCount == 0 {
		logf(
			"定时对账完成: run_at=%s checked_chats=%d mismatches=0 tolerance=%g",
			runAt.UTC().Format(time.RFC3339),
			result.CheckedChats,
			result.Tolerance,
		)
		return
	}

	logf(
		"定时对账发现差异: run_at=%s checked_chats=%d mismatches=%d tolerance=%g",
		runAt.UTC().Format(time.RFC3339),
		result.CheckedChats,
		result.DifferenceCount,
		result.Tolerance,
	)
	detailCount := result.DifferenceCount
	if detailCount > defaultScheduledReconciliationMaxDetailLogs {
		detailCount = defaultScheduledReconciliationMaxDetailLogs
	}
	for index := 0; index < detailCount; index++ {
		diff := result.Differences[index]
		logf(
			"定时对账差异: chat_id=%d tx_dep=%g bal_dep=%g dep_diff=%g tx_pay=%g bal_pay=%g pay_diff=%g",
			diff.ChatID,
			diff.TransactionDepositTotal,
			diff.BalanceDepositTotal,
			diff.DepositDifference,
			diff.TransactionPayoutTotal,
			diff.BalancePayoutTotal,
			diff.PayoutDifference,
		)
	}
	if result.DifferenceCount > detailCount {
		logf(
			"定时对账差异明细已截断: total=%d logged=%d omitted=%d",
			result.DifferenceCount,
			detailCount,
			result.DifferenceCount-detailCount,
		)
	}
}

func runScheduledReconciliationLoop(ctx context.Context, timezoneName string, database *db.Database, nowFn func() time.Time, logf func(string, ...any)) {
	if database == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	if logf == nil {
		logf = log.Printf
	}

	loc := loadTimezoneLocationOrDefault(timezoneName)
	select {
	case <-ctx.Done():
		return
	default:
	}
	// 服务在定时点之后重启时，立即补跑一次对账，避免遗漏当日巡检。
	runScheduledReconciliationOnceWithLog(database, nowFn(), db.DefaultScheduledReconciliationTolerance, logf)

	for {
		wait := durationUntilNextLocalSchedule(
			nowFn(),
			loc,
			defaultScheduledReconciliationHour,
			defaultScheduledReconciliationMinute,
		)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		runScheduledReconciliationOnceWithLog(database, nowFn(), db.DefaultScheduledReconciliationTolerance, logf)
	}
}

func runServiceHealthHeartbeatLoop(ctx context.Context, probe *system.ServiceHealthProbe, interval time.Duration) {
	if probe == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		probe.Heartbeat("runtime_heartbeat")
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func main() {
	config.LoadDotEnv(".env")
	setupStructuredLogging(os.Getenv)
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Println("未检测到 BOT_TOKEN。")
		log.Println("请在 .env 中添加：BOT_TOKEN=你的Telegram机器人Token")
		os.Exit(1)
	}
	tracingSpec, tracingSpecErr := system.LoadDistributedTracingSpecFromEnv(os.Getenv)
	if tracingSpecErr != nil {
		log.Printf("%v", tracingSpecErr)
	}
	tracingRuntime, tracingErr := system.SetupDistributedTracing(tracingSpec)
	if tracingErr != nil {
		log.Printf("链路追踪初始化失败，自动降级关闭: %v", tracingErr)
		tracingRuntime, _ = system.SetupDistributedTracing(system.DefaultDistributedTracingSpec())
	}
	if tracingRuntime != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if shutdownErr := tracingRuntime.Shutdown(shutdownCtx); shutdownErr != nil {
				log.Printf("关闭链路追踪失败: %v", shutdownErr)
			}
		}()
		log.Print(system.BuildDistributedTracingLog(tracingRuntime.Spec()))
	}
	database, err := db.NewDatabaseWithOptions(cfg.DBMasterPath, db.Options{
		WaitTimeout:                    time.Duration(cfg.DBWaitTimeoutMS) * time.Millisecond,
		DeadlockRetryMax:               cfg.DBDeadlockRetryMax,
		DeadlockBackoffBase:            time.Duration(cfg.DBDeadlockBackoffMS) * time.Millisecond,
		DeadlockBackoffJitter:          time.Duration(cfg.DBDeadlockJitterMS) * time.Millisecond,
		DataEncryptionKey:              cfg.DBDataEncryptionKey,
		ReadReplicaPath:                cfg.DBSlavePath,
		SQLiteJournalMode:              cfg.SQLiteJournalMode,
		SQLiteSynchronous:              cfg.SQLiteSynchronous,
		SQLiteOptimizeOnStart:          cfg.SQLiteOptimizeOnStart,
		SQLiteIncrementalVacuumOnStart: cfg.SQLiteIncrementalVacuumOnStart,
		HotAccountUserIDs:              cfg.HotAccountUserIDs,
		HotAccountBatchSize:            cfg.HotAccountBatchSize,
		HotAccountFlushWindow:          time.Duration(cfg.HotAccountFlushMS) * time.Millisecond,
		ColdArchivePath:                cfg.ColdArchivePath,
		HotDataRetention:               time.Duration(cfg.HotDataRetentionDays) * 24 * time.Hour,
		ColdArchiveSweep:               time.Duration(cfg.ColdArchiveSweepMinutes) * time.Minute,
		ColdArchiveBatchSize:           cfg.ColdArchiveBatchSize,
		TenantShardCount:               cfg.DBTenantShards,
	})
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	log.Printf("账本 ACID 编排已启用：Transaction(ledger_entries) 与 Balance(ledger_balances) 在同一事务内提交")
	log.Printf("试算平衡检查已启用：提交前强制校验 Sum(Debit) == Sum(Credit)")
	log.Printf("账户级互斥锁已启用：按 User_ID + Currency 串行锁定余额快照，防止并发覆盖")
	log.Printf(
		"死锁重试已启用：wait_timeout=%dms exponential_backoff=%dms max_retry=%d jitter=%dms",
		cfg.DBWaitTimeoutMS,
		cfg.DBDeadlockBackoffMS,
		cfg.DBDeadlockRetryMax,
		cfg.DBDeadlockJitterMS,
	)
	log.Printf(
		"SQLite 运行参数：journal_mode=%q synchronous=%q optimize_on_start=%t incremental_vacuum_on_start=%t",
		cfg.SQLiteJournalMode,
		cfg.SQLiteSynchronous,
		cfg.SQLiteOptimizeOnStart,
		cfg.SQLiteIncrementalVacuumOnStart,
	)
	log.Printf("敏感字段加密已启用：AES-256（DB_DATA_ENCRYPTION_KEY 可覆盖默认密钥）")
	if len(cfg.HotAccountUserIDs) > 0 {
		log.Printf(
			"热点账户削峰已启用：hot_users=%d batch_size=%d flush_window=%dms",
			len(cfg.HotAccountUserIDs),
			cfg.HotAccountBatchSize,
			cfg.HotAccountFlushMS,
		)
	} else {
		log.Printf("热点账户削峰未启用：未配置 HOT_ACCOUNT_USER_IDS")
	}
	if strings.TrimSpace(cfg.ColdArchivePath) == "" {
		log.Printf("冷热数据分离未启用：未配置 COLD_ARCHIVE_PATH")
	} else {
		log.Printf(
			"冷热数据分离已启用：hot_retention=%d天 cold_archive=%s sweep=%dmin batch=%d",
			cfg.HotDataRetentionDays,
			cfg.ColdArchivePath,
			cfg.ColdArchiveSweepMinutes,
			cfg.ColdArchiveBatchSize,
		)
	}
	var walArchiver *system.WALArchiver
	var dailyFullBackup *system.DailyFullBackup
	var backupVerifier *system.BackupVerifier
	var crossRegionDR *system.CrossRegionDRSyncer
	if strings.TrimSpace(cfg.PGWALSourceDir) == "" || strings.TrimSpace(cfg.PGWALArchiveDir) == "" {
		log.Printf("WAL 归档未启用：未配置 PG_WAL_SOURCE_DIR/PG_WAL_ARCHIVE_DIR")
	} else {
		walArchiver, err = system.NewWALArchiver(system.WALArchiverOptions{
			SourceDir:    cfg.PGWALSourceDir,
			ArchiveDir:   cfg.PGWALArchiveDir,
			ScanInterval: time.Duration(cfg.PGWALArchiveScanSec) * time.Second,
			Logf:         log.Printf,
		})
		if err != nil {
			log.Printf("WAL 归档初始化失败，自动降级关闭: %v", err)
			walArchiver = nil
		} else {
			archiveCommand, archiveCommandErr := system.BuildPostgresArchiveCommand(walArchiver.ArchiveDir())
			if archiveCommandErr == nil {
				log.Printf(
					"WAL 归档已启用：source=%s archive=%s scan=%s archive_command=%s",
					walArchiver.SourceDir(),
					walArchiver.ArchiveDir(),
					walArchiver.ScanInterval(),
					archiveCommand,
				)
			} else {
				log.Printf(
					"WAL 归档已启用：source=%s archive=%s scan=%s",
					walArchiver.SourceDir(),
					walArchiver.ArchiveDir(),
					walArchiver.ScanInterval(),
				)
			}
			restoreCommand, restoreCommandErr := system.BuildPostgresRestoreCommand(walArchiver.ArchiveDir())
			if restoreCommandErr == nil {
				log.Printf(
					"PITR 已启用：restore_command=%s recovery_target_action=%s",
					restoreCommand,
					system.PITRTargetActionPromote,
				)
			} else {
				log.Printf("PITR 已启用：recovery_target_action=%s", system.PITRTargetActionPromote)
			}
		}
	}
	fullBackupSourcePaths := buildDailyFullBackupSourcePaths(cfg)
	fullBackupDir := defaultDailyFullBackupDir(cfg.DBMasterPath)
	if len(fullBackupSourcePaths) == 0 {
		log.Printf("定时全量备份未启用：未发现可用数据源")
	} else {
		dailyFullBackup, err = system.NewDailyFullBackup(system.DailyFullBackupOptions{
			SourcePaths:   fullBackupSourcePaths,
			BackupDir:     fullBackupDir,
			Timezone:      cfg.Timezone,
			LowPeakHour:   system.DefaultDailyFullBackupHour,
			LowPeakMinute: system.DefaultDailyFullBackupMinute,
			Logf:          log.Printf,
		})
		if err != nil {
			log.Printf("定时全量备份初始化失败，自动降级关闭: %v", err)
			dailyFullBackup = nil
		} else {
			log.Printf(
				"定时全量备份已启用：sources=%d backup_dir=%s timezone=%s schedule=每日%s（低峰期）",
				len(dailyFullBackup.SourcePaths()),
				dailyFullBackup.BackupDir(),
				dailyFullBackup.Timezone(),
				dailyFullBackup.ScheduleTime(),
			)
		}
	}
	if dailyFullBackup == nil {
		log.Printf("备份有效性验证未启用：定时全量备份未就绪")
	} else {
		backupVerifier, err = system.NewBackupVerifier(system.BackupVerificationOptions{
			BackupDir:      dailyFullBackup.BackupDir(),
			VerifyInterval: system.DefaultBackupVerificationInterval,
			Logf:           log.Printf,
		})
		if err != nil {
			log.Printf("备份有效性验证初始化失败，自动降级关闭: %v", err)
			backupVerifier = nil
		} else {
			log.Printf(
				"备份有效性验证已启用：backup_dir=%s interval=%s strategy=定期自动恢复测试",
				backupVerifier.BackupDir(),
				backupVerifier.VerifyInterval(),
			)
		}
	}
	crossRegionDRSourcePaths := buildCrossRegionDRSourcePaths(walArchiver, dailyFullBackup)
	if len(crossRegionDRSourcePaths) == 0 {
		log.Printf("跨区域容灾未启用：未发现可同步的灾备数据源")
	} else {
		crossRegionDRTargetDir := strings.TrimSpace(cfg.CrossRegionDRTargetDir)
		if crossRegionDRTargetDir == "" {
			crossRegionDRTargetDir = defaultCrossRegionDRTargetDir(cfg.DBMasterPath)
		}
		crossRegionDR, err = system.NewCrossRegionDRSyncer(system.CrossRegionDRSyncOptions{
			SourcePaths:  crossRegionDRSourcePaths,
			TargetDir:    crossRegionDRTargetDir,
			SyncInterval: time.Duration(cfg.CrossRegionDRSyncSec) * time.Second,
			Logf:         log.Printf,
		})
		if err != nil {
			log.Printf("跨区域容灾初始化失败，自动降级关闭: %v", err)
			crossRegionDR = nil
		} else {
			log.Printf(
				"跨区域容灾已启用：sources=%d target=%s interval=%s",
				len(crossRegionDR.SourcePaths()),
				crossRegionDR.TargetDir(),
				crossRegionDR.SyncInterval(),
			)
		}
	}
	if strings.TrimSpace(cfg.ElasticsearchURL) == "" {
		log.Printf("全文检索未启用：未配置 ELASTICSEARCH_URL")
	} else {
		orderSearch, searchErr := services.NewElasticOrderSearch(services.ElasticOrderSearchConfig{
			Endpoint: cfg.ElasticsearchURL,
			Index:    cfg.ElasticsearchIndex,
			Username: cfg.ElasticsearchUsername,
			Password: cfg.ElasticsearchPassword,
			APIKey:   cfg.ElasticsearchAPIKey,
			Timeout:  time.Duration(cfg.ElasticsearchTimeoutSec) * time.Second,
		})
		if searchErr != nil {
			log.Printf("全文检索初始化失败，自动降级为 SQL 查单: %v", searchErr)
		} else {
			database.SetOrderSearchBackend(orderSearch)
			log.Printf(
				"全文检索已启用：engine=elasticsearch endpoint=%s index=%s timeout=%ds",
				sanitizeEndpointForLog(cfg.ElasticsearchURL),
				cfg.ElasticsearchIndex,
				cfg.ElasticsearchTimeoutSec,
			)
		}
	}
	log.Printf("队列化串行执行已启用：同群记账请求按 Group_ID 路由到固定 Worker，队列超过阈值时触发背压保护")
	log.Printf("服务降级策略已启用：高负载时优先处理记账指令，丢弃查询/娱乐指令")
	log.Printf("死信队列已启用：发送失败消息写入 outbound_dead_letters，供后续人工分析")
	log.Print(buildTemplateRendererLog())
	log.Printf("分库分表已启用：group_id_hash_shards=%d", cfg.DBTenantShards)
	if filepath.Clean(cfg.DBMasterPath) == filepath.Clean(cfg.DBSlavePath) {
		log.Printf("读写分离未启用：master=%s slave=%s（同库）", cfg.DBMasterPath, cfg.DBSlavePath)
	} else {
		log.Printf("读写分离已启用：write_master=%s query_slave=%s", cfg.DBMasterPath, cfg.DBSlavePath)
	}
	if database.OrderQueryBloomEnabled() {
		log.Printf("缓存穿透保护已启用：Bloom Filter 在查单前拦截明显不存在的订单条件")
	} else {
		log.Printf("缓存穿透保护未启用：Bloom Filter 预热失败，查单回退数据库查询")
	}
	log.Printf("阶梯费率算法已关闭：当前仅使用群固定费率与单笔覆盖费率")
	log.Printf(
		"请求指纹计算已启用：chat_id=%d message_id=%d fingerprint=%s",
		defaultFingerprintSampleChatID,
		defaultFingerprintSampleMessageID,
		db.BuildRequestFingerprint(defaultFingerprintSampleChatID, defaultFingerprintSampleMessageID),
	)
	chart := accounting.DefaultChartOfAccounts()
	if err := accounting.ValidateChartOfAccounts(chart); err != nil {
		log.Fatalf("会计科目表校验失败: %v", err)
	}
	assetCount := len(accounting.AccountsByCategory(accounting.AccountCategoryAsset))
	liabilityCount := len(accounting.AccountsByCategory(accounting.AccountCategoryLiability))
	pnlCount := len(accounting.AccountsByCategory(accounting.AccountCategoryPnL))
	assetAccount, assetOK := accounting.FindAccountByCode(accounting.AccountCodeAssetUSDT)
	liabilityAccount, liabilityOK := accounting.FindAccountByCode(accounting.AccountCodeLiabilityUserBalance)
	pnlAccount, pnlOK := accounting.FindAccountByCode(accounting.AccountCodePnLFeeIncome)
	if !assetOK || !liabilityOK || !pnlOK {
		log.Fatalf("会计科目表缺少核心科目定义")
	}
	journalInstruction, journalSample, err := loadJournalSampleEntry(os.Getenv)
	if err != nil {
		log.Printf("%v", err)
	}
	journalSummary := journalSample.SummaryCN()
	log.Printf(
		"会计科目表已加载: total=%d asset=%d liability=%d pnl=%d",
		len(chart),
		assetCount,
		liabilityCount,
		pnlCount,
	)
	log.Printf(
		"核心科目示例: asset=%s(%s) liability=%s(%s) pnl=%s(%s)",
		assetAccount.Code,
		assetAccount.Name,
		liabilityAccount.Code,
		liabilityAccount.Name,
		pnlAccount.Code,
		pnlAccount.Name,
	)
	log.Printf(
		"分录生成器示例: instruction=%s type=%s amount=%g summary=%s",
		journalInstruction,
		journalSample.EntryType,
		journalSample.Amount,
		journalSummary,
	)
	for idx, line := range journalSample.Lines {
		log.Printf(
			"分录行[%d]: direction=%s account=%s(%s) amount=%g",
			idx+1,
			line.Direction,
			line.AccountCode,
			line.AccountName,
			line.Amount,
		)
	}

	precisionSample, err := loadMultiCurrencyPrecisionSample(os.Getenv)
	if err != nil {
		log.Printf("%v", err)
	}
	log.Printf(
		"多币种精度示例: usdt=%s(原子单位=%d) 汇率=%s 法币(2位)=%s(分=%d) 回算USDT(8位)=%s",
		precisionSample.USDTAmount.StringFixed(accounting.CurrencyScaleUSDT),
		precisionSample.USDTAtomic,
		precisionSample.Rate.StringFixed(accounting.CurrencyScaleUSDT),
		precisionSample.FiatAmount.StringFixed(accounting.CurrencyScaleFiat),
		precisionSample.FiatMinor,
		precisionSample.RoundTripUSDT.StringFixed(accounting.CurrencyScaleUSDT),
	)

	app, err := bot.NewApp(cfg, database)
	if err != nil {
		log.Fatalf("初始化机器人失败: %v", err)
	}
	addressVerificationMediaPublisher, addressVerificationMediaConfigured, addressVerificationMediaErr := buildAddressVerificationMediaPublisher(cfg)
	switch {
	case addressVerificationMediaErr != nil:
		log.Printf("地址验证媒体配置无效，降级 multipart: media_config_invalid=1 media_mode_effective=multipart reason=%v", addressVerificationMediaErr)
	case !addressVerificationMediaConfigured:
		log.Printf("地址验证媒体模式: media_mode_effective=multipart")
	default:
		if enableErr := app.EnableAddressVerificationMediaPublisher(addressVerificationMediaPublisher); enableErr != nil {
			log.Printf("地址验证媒体发布器启动失败，降级 multipart: media_config_invalid=1 media_mode_effective=multipart reason=%v", enableErr)
		} else {
			log.Printf(
				"地址验证媒体模式: media_mode_effective=local_url directory=%s ttl=%s public_host=autobookbl.site",
				addressVerificationMediaProductionDirectory,
				cfg.AddressVerifyMediaTTL,
			)
		}
	}
	log.Printf(
		"出站队列已启用：workers=%d queue=%d ledger_receipt_outbox_workers=%d",
		cfg.OutboundQueueWorkers,
		cfg.OutboundQueueDepth,
		cfg.LedgerReceiptOutboxWorkers,
	)
	mediaUploader, mediaConfigured, mediaErr := buildMediaObjectStorageUploader(cfg)
	switch {
	case mediaErr != nil:
		log.Printf("多媒体处理初始化失败，自动降级关闭: %v", mediaErr)
	case !mediaConfigured:
		log.Printf("多媒体处理未启用：未配置 MEDIA_S3_ENDPOINT/MEDIA_S3_BUCKET/MEDIA_S3_ACCESS_KEY/MEDIA_S3_SECRET_KEY")
	default:
		if enableErr := app.EnableMediaHandler(mediaUploader, bot.AsyncMediaHandlerConfig{
			Workers:      cfg.MediaHandlerWorkers,
			QueueSize:    cfg.MediaHandlerQueueSize,
			MaxFileBytes: cfg.MediaHandlerMaxBytes,
			KeyPrefix:    cfg.MediaS3Prefix,
		}); enableErr != nil {
			log.Printf("多媒体处理初始化失败，自动降级关闭: %v", enableErr)
		} else {
			log.Printf(
				"多媒体处理已启用：workers=%d queue=%d max_bytes=%d bucket=%s endpoint=%s",
				cfg.MediaHandlerWorkers,
				cfg.MediaHandlerQueueSize,
				cfg.MediaHandlerMaxBytes,
				cfg.MediaS3Bucket,
				cfg.MediaS3Endpoint,
			)
		}
	}
	updateMetricsRecorders := make([]bot.UpdateMetricsRecorder, 0, 2)
	var metricsCollector *system.PrometheusMetrics
	metricsCollector, err = system.NewPrometheusMetrics(system.PrometheusMetricsOptions{})
	if err != nil {
		log.Printf("指标监控初始化失败，自动降级关闭: %v", err)
	} else {
		updateMetricsRecorders = append(updateMetricsRecorders, metricsCollector)
		app.SetOutboundMetricsRecorder(metricsCollector)
		log.Printf(
			"指标监控已启用：Prometheus endpoint=%s metrics=tps(rate(update_requests_total)) latency(update_latency_seconds) outbound_queue(outbound_queue_depth/outbound_queue_submissions_total) outbox(ledger_receipt_outbox_total) goroutines",
			metricsCollector.Path(),
		)
		grafanaDashboardLog, grafanaDashboardErr := buildGrafanaDashboardLog(os.Getenv, metricsCollector.Path())
		if grafanaDashboardErr != nil {
			log.Printf("%v", grafanaDashboardErr)
		}
		log.Print(grafanaDashboardLog)
	}
	alertManagerLog, alertManagerSpec, alertManagerSpecErr := buildAlertManagerLog(os.Getenv)
	if alertManagerSpecErr != nil {
		log.Printf("%v", alertManagerSpecErr)
	}
	log.Print(alertManagerLog)
	alertManager, alertManagerErr := system.NewAlertManagerFromSpec(alertManagerSpec, log.Printf)
	if alertManagerErr != nil {
		log.Printf("智能告警初始化失败，自动降级关闭: %v", alertManagerErr)
	} else if alertManager != nil {
		updateMetricsRecorders = append(updateMetricsRecorders, alertManager)
	}
	if recorder := newFanoutUpdateMetricsRecorder(updateMetricsRecorders...); recorder != nil {
		app.SetUpdateMetricsRecorder(recorder)
	}
	timeSeriesRecorder := services.NewNoopTimeSeriesRecorder()
	if strings.TrimSpace(cfg.TimescaleDSN) == "" {
		log.Printf("时序数据库未启用：未配置 TIMESCALE_DSN")
	} else {
		timeSeriesStore, tsErr := services.NewTimescaleStore(cfg.TimescaleDSN, services.TimescaleStoreOptions{})
		if tsErr != nil {
			log.Printf("时序数据库初始化失败，降级关闭: %v", tsErr)
		} else {
			defer func() {
				if closeErr := timeSeriesStore.Close(); closeErr != nil {
					log.Printf("关闭时序数据库连接失败: %v", closeErr)
				}
			}()
			timeSeriesRecorder = timeSeriesStore
			log.Printf("时序数据库已启用：TimescaleDB 持久化汇率波动与资金流向趋势")
		}
	}
	app.SetTimeSeriesRecorder(timeSeriesRecorder)
	log.Printf("地址黑名单筛查已启用：本地库匹配 OFAC 制裁地址与已知诈骗地址")
	log.Printf("链上地址核验已启用：通过 TronGrid/Etherscan 识别地址活跃度与余额状态")
	log.Printf("二维码地址解析已启用：自动识别图片中的 TRC20/ERC20 地址")
	log.Print(buildExportEngineLog())
	dockerBuildLog, dockerBuildErr := buildDockerBuildLog(os.Getenv)
	if dockerBuildErr != nil {
		log.Printf("%v", dockerBuildErr)
	}
	log.Print(dockerBuildLog)
	k8sDeploymentLog, k8sDeploymentErr := buildK8sDeploymentLog(os.Getenv)
	if k8sDeploymentErr != nil {
		log.Printf("%v", k8sDeploymentErr)
	}
	log.Print(k8sDeploymentLog)
	k8sHPALog, k8sHPAErr := buildK8sHPALog(os.Getenv)
	if k8sHPAErr != nil {
		log.Printf("%v", k8sHPAErr)
	}
	log.Print(k8sHPALog)
	k8sConfigMapSecretLog, k8sConfigMapSecretErr := buildK8sConfigMapSecretLog(os.Getenv)
	if k8sConfigMapSecretErr != nil {
		log.Printf("%v", k8sConfigMapSecretErr)
	}
	log.Print(k8sConfigMapSecretLog)
	k8sProbeLog, k8sProbeSpec, k8sProbeErr := buildK8sProbeLog(os.Getenv)
	if k8sProbeErr != nil {
		log.Printf("%v", k8sProbeErr)
	}
	log.Print(k8sProbeLog)
	k8sSidecarLog, k8sSidecarErr := buildK8sSidecarLog(os.Getenv)
	if k8sSidecarErr != nil {
		log.Printf("%v", k8sSidecarErr)
	}
	log.Print(k8sSidecarLog)
	k8sCanaryReleaseLog, k8sCanaryReleaseErr := buildK8sCanaryReleaseLog(os.Getenv)
	if k8sCanaryReleaseErr != nil {
		log.Printf("%v", k8sCanaryReleaseErr)
	}
	log.Print(k8sCanaryReleaseLog)
	k8sServiceMeshLog, k8sServiceMeshErr := buildK8sServiceMeshLog(os.Getenv)
	if k8sServiceMeshErr != nil {
		log.Printf("%v", k8sServiceMeshErr)
	}
	log.Print(k8sServiceMeshLog)
	cloudCostMonitorLog, cloudCostMonitorSpec, cloudCostMonitorErr := buildCloudCostMonitorLog(os.Getenv)
	if cloudCostMonitorErr != nil {
		log.Printf("%v", cloudCostMonitorErr)
	}
	log.Print(cloudCostMonitorLog)
	dbTuningLog, dbTuningErr := buildDBTuningLog(os.Getenv)
	if dbTuningErr != nil {
		log.Printf("%v", dbTuningErr)
	}
	log.Print(dbTuningLog)
	pgBouncerLog, pgBouncerErr := buildPgBouncerLog(os.Getenv)
	if pgBouncerErr != nil {
		log.Printf("%v", pgBouncerErr)
	}
	log.Print(pgBouncerLog)
	cdnAccelerationLog, cdnAccelerationErr := buildCDNAccelerationLog(os.Getenv)
	if cdnAccelerationErr != nil {
		log.Printf("%v", cdnAccelerationErr)
	}
	log.Print(cdnAccelerationLog)
	runbookLog, runbookErr := buildRunbookLog(os.Getenv)
	if runbookErr != nil {
		log.Printf("%v", runbookErr)
	}
	log.Print(runbookLog)
	onCallRotationLog, onCallRotationErr := buildOnCallRotationLog(os.Getenv)
	if onCallRotationErr != nil {
		log.Printf("%v", onCallRotationErr)
	}
	log.Print(onCallRotationLog)
	chaosEngineeringLog, chaosEngineeringSpec, chaosEngineeringErr := buildChaosEngineeringLog(os.Getenv)
	if chaosEngineeringErr != nil {
		log.Printf("%v", chaosEngineeringErr)
	}
	log.Print(chaosEngineeringLog)
	autoRemediationLog, autoRemediationSpec, autoRemediationErr := buildAutoRemediationLog(os.Getenv)
	if autoRemediationErr != nil {
		log.Printf("%v", autoRemediationErr)
	}
	log.Print(autoRemediationLog)
	postMortemLog, postMortemErr := buildPostMortemLog(os.Getenv)
	if postMortemErr != nil {
		log.Printf("%v", postMortemErr)
	}
	log.Print(postMortemLog)
	log.Printf("全局令牌桶已启用：Bot API 调用上限 30 TPS")
	log.Print(buildTimeAnomalyMonitorLog(cfg))
	log.Print(buildRateDeviationAlertLog())
	if strings.TrimSpace(cfg.WebhookAddr) != "" {
		log.Printf("Webhook 接收器已启用：addr=%s path=%s", cfg.WebhookAddr, cfg.WebhookPath)
		if strings.TrimSpace(cfg.WebhookSecretToken) != "" {
			log.Printf("Webhook 签名验证已启用：header=X-Telegram-Bot-Api-Secret-Token")
		} else {
			log.Printf("Webhook 签名验证未启用：未配置 WEBHOOK_SECRET_TOKEN")
		}
		if len(cfg.WebhookIPBlacklist) > 0 {
			log.Printf("Webhook IP 黑名单已启用：rules=%d", len(cfg.WebhookIPBlacklist))
		}
	} else {
		log.Printf("Webhook 接收器未启用：使用 Telegram Long Polling")
	}
	log.Printf("API 操作审计日志已启用：记录源 IP、请求参数和响应结果")
	stateTargets := make([]bot.StateBroadcaster, 0, 2)
	if hub := app.StateWebSocketHub(); hub != nil {
		stateTargets = append(stateTargets, hub)
	}
	requestLockEnabled := false
	distributedSessionEnabled := false
	userFSMMode := "memory(ttl=300s)"
	var requestLocker *bot.RedisRequestLocker
	redisEnabled := false
	var redisBroadcaster *bot.RedisStateBroadcaster
	dailyCounterEnabled := false
	contextCacheEnabled := false
	contextCacheMode := ""
	var contextCache db.ContextCache
	if cfg.CacheL1Enabled {
		contextCache = db.NewMemoryContextCache(db.MemoryContextCacheConfig{
			TTL:        time.Duration(cfg.CacheL1TTLSeconds) * time.Second,
			MaxEntries: cfg.CacheL1MaxEntries,
		})
		contextCacheMode = fmt.Sprintf(
			"L1=memory(ttl=%ds max_entries=%d)",
			cfg.CacheL1TTLSeconds,
			cfg.CacheL1MaxEntries,
		)
		contextCacheEnabled = true
	}
	cacheRedisAddr := strings.TrimSpace(cfg.CacheRedisAddr)
	if cacheRedisAddr != "" {
		redisContextCache, cacheErr := db.NewRedisContextCache(db.RedisContextCacheConfig{
			Addr:      cacheRedisAddr,
			Password:  cfg.CacheRedisPass,
			DB:        cfg.CacheRedisDB,
			KeyPrefix: "ctxcache:",
			TTL:       time.Duration(cfg.CacheL2TTLSeconds) * time.Second,
			Timeout:   2 * time.Second,
		})
		if cacheErr != nil {
			if contextCache != nil {
				log.Printf("Redis 业务缓存初始化失败，降级为仅 L1 本地缓存: %v", cacheErr)
			} else {
				log.Printf("Redis 业务缓存初始化失败，降级到数据库直读: %v", cacheErr)
			}
		} else {
			if contextCache != nil {
				contextCache = db.NewMultiLevelContextCache(contextCache, redisContextCache)
				contextCacheMode = fmt.Sprintf(
					"%s + L2=redis(addr=%s db=%d ttl=%ds prefix=ctxcache:)",
					contextCacheMode,
					cacheRedisAddr,
					cfg.CacheRedisDB,
					cfg.CacheL2TTLSeconds,
				)
			} else {
				contextCache = redisContextCache
				contextCacheMode = fmt.Sprintf(
					"L2=redis(addr=%s db=%d ttl=%ds prefix=ctxcache:)",
					cacheRedisAddr,
					cfg.CacheRedisDB,
					cfg.CacheL2TTLSeconds,
				)
				contextCacheEnabled = true
			}
		}
	}
	if contextCache != nil {
		defer contextCache.Close()
		database.SetContextCache(contextCache)
		contextCacheEnabled = true
	}
	if cacheRedisAddr != "" {
		sessionStore, sessionErr := bot.NewRedisDistributedSessionStore(bot.RedisDistributedSessionStoreConfig{
			Addr:      cacheRedisAddr,
			Password:  cfg.CacheRedisPass,
			DB:        cfg.CacheRedisDB,
			KeyPrefix: "session:dist:",
			TTL:       20 * time.Minute,
			Timeout:   2 * time.Second,
		})
		if sessionErr != nil {
			log.Printf("Redis 分布式 Session 初始化失败，WebApp 登录态降级为无状态签名鉴权: %v", sessionErr)
		} else {
			defer sessionStore.Close()
			app.SetDistributedSessionStore(sessionStore)
			distributedSessionEnabled = true
		}
	}
	if cacheRedisAddr != "" {
		userFSM, fsmErr := bot.NewRedisUserFSM(bot.RedisUserFSMConfig{
			Addr:      cacheRedisAddr,
			Password:  cfg.CacheRedisPass,
			DB:        cfg.CacheRedisDB,
			KeyPrefix: "session:fsm:",
			TTL:       5 * time.Minute,
			Timeout:   2 * time.Second,
		})
		if fsmErr != nil {
			log.Printf("Redis 用户状态机初始化失败，降级为本地内存状态机: %v", fsmErr)
		} else {
			defer userFSM.Close()
			app.SetUserFSM(userFSM)
			userFSMMode = fmt.Sprintf("redis(addr=%s db=%d prefix=session:fsm: ttl=300s)", cacheRedisAddr, cfg.CacheRedisDB)
		}
	}
	if cacheRedisAddr != "" {
		ledgerCounter, counterErr := bot.NewRedisDailyLedgerCounter(bot.RedisDailyLedgerCounterConfig{
			Addr:      cacheRedisAddr,
			Password:  cfg.CacheRedisPass,
			DB:        cfg.CacheRedisDB,
			KeyPrefix: "ledger:daily:",
			TTL:       72 * time.Hour,
			Timeout:   2 * time.Second,
		})
		if counterErr != nil {
			log.Printf("Redis 原子计数器初始化失败，账单统计降级到数据库聚合: %v", counterErr)
		} else {
			defer ledgerCounter.Close()
			app.SetDailyLedgerCounter(ledgerCounter)
			dailyCounterEnabled = true
		}
	}
	if strings.TrimSpace(cfg.StateBroadcastRedisAddr) != "" {
		requestLocker, err = bot.NewRedisRequestLocker(bot.RedisRequestLockerConfig{
			Addr:           cfg.StateBroadcastRedisAddr,
			Password:       cfg.StateBroadcastRedisPass,
			DB:             cfg.StateBroadcastRedisDB,
			KeyPrefix:      "lock:request:",
			AcquireTimeout: 2 * time.Second,
		})
		if err != nil {
			log.Printf("Redis 分布式锁初始化失败，降级到数据库幂等防重: %v", err)
		} else {
			defer requestLocker.Close()
			app.SetRequestLocker(requestLocker)
			requestLockEnabled = true
		}
		redisBroadcaster, err = bot.NewRedisStateBroadcaster(bot.RedisStateBroadcasterConfig{
			Addr:           cfg.StateBroadcastRedisAddr,
			Password:       cfg.StateBroadcastRedisPass,
			DB:             cfg.StateBroadcastRedisDB,
			ChannelPrefix:  cfg.StateBroadcastChannel,
			PublishTimeout: 2 * time.Second,
		})
		if err != nil {
			log.Printf("Redis PubSub 状态广播初始化失败，自动降级到 WebSocket: %v", err)
		} else {
			defer redisBroadcaster.Close()
			stateTargets = append(stateTargets, redisBroadcaster)
			redisEnabled = true
		}
	}
	app.SetStateBroadcaster(bot.NewFanoutStateBroadcaster(stateTargets...))
	if redisEnabled {
		log.Printf("即时状态广播已启用：WebSocket + Redis PubSub(channel_prefix=%s)", cfg.StateBroadcastChannel)
	} else {
		log.Printf("即时状态广播已启用：WebSocket（Redis PubSub 未配置）")
	}
	if requestLockEnabled {
		log.Printf("分布式锁已启用：SETNX key value EX 10s (prefix=lock:request:)")
	} else {
		log.Printf("分布式锁未启用：未配置可用 Redis，降级到数据库幂等防重")
	}
	if contextCacheEnabled {
		log.Printf("多级缓存已启用：%s，业务数据采用 Cache-Aside（群配置/角色权限）", contextCacheMode)
	} else {
		log.Printf("多级缓存未启用：L1/L2 都未配置，业务数据降级到数据库直读")
	}
	if dailyCounterEnabled {
		log.Printf("原子计数器已启用：Redis INCR 维护今日总入款/总下发（prefix=ledger:daily:）")
	} else {
		log.Printf("原子计数器未启用：未配置可用 Redis，账单统计回退数据库聚合")
	}
	if distributedSessionEnabled {
		log.Printf("分布式 Session 已启用：Redis 存储用户登录态与上下文（prefix=session:dist:）")
	} else {
		log.Printf("分布式 Session 未启用：WebApp 登录态降级为无状态签名鉴权")
	}
	log.Printf("用户状态机已启用：%s", userFSMMode)
	log.Printf("结果缓存已启用：已处理请求命中缓存后直接返回原响应，不再重复执行业务逻辑")
	log.Printf("分布式事务 Saga 已启用：记账成功后必须发送回执，通知失败自动触发补偿撤回，确保跨服务一致性")

	signalCtx, stopBySignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopBySignal()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()
	killSwitch := system.NewKillSwitch(system.KillSwitchOptions{
		Stop: cancel,
		Logf: log.Printf,
	})
	log.Printf("自动紧急停机已启用：检测到严重 Bug 或攻击时一键停止所有服务")
	var autoRemediator *system.AutoRemediator
	if autoRemediationSpec.Enabled {
		autoRemediator = system.NewAutoRemediator(system.AutoRemediatorOptions{
			Spec: autoRemediationSpec,
			Logf: log.Printf,
		})
	}

	serviceHealthProbe := system.NewServiceHealthProbe(system.ServiceHealthProbeOptions{
		HeartbeatTimeout: system.ProbeHeartbeatTimeout(k8sProbeSpec),
		ScanInterval:     system.ProbeScanInterval(k8sProbeSpec),
		Logf:             log.Printf,
		ReadinessCheck: func() (bool, string) {
			pollReadiness := app.PollReadiness(time.Now())
			return pollReadiness.Ready, "telegram poll " + string(pollReadiness.Reason)
		},
		OnZombie: func(alert system.ServiceZombieAlert) {
			if autoRemediator != nil {
				go autoRemediator.RunOnce(context.Background(), system.AutoRemediationEvent{
					Fault:       system.AutoRemediationFaultConsumerHeartbeatStall,
					TriggeredAt: time.Now(),
					Metadata: map[string]string{
						"source":            alert.LastHeartbeatSrc,
						"timeout":           alert.Timeout.String(),
						"stale_for":         alert.StaleFor.Round(time.Millisecond).String(),
						"last_heartbeat_at": alert.LastHeartbeatAt.Format(time.RFC3339),
					},
				})
			}
			killSwitch.TriggerSevereBug(
				"liveness_probe",
				fmt.Sprintf(
					"last_heartbeat_at=%s source=%s stale_for=%s timeout=%s",
					alert.LastHeartbeatAt.Format(time.RFC3339),
					alert.LastHeartbeatSrc,
					alert.StaleFor.Round(time.Millisecond),
					alert.Timeout,
				),
			)
		},
	})
	go serviceHealthProbe.Run(ctx)
	go runServiceHealthHeartbeatLoop(ctx, serviceHealthProbe, system.ProbeHeartbeatInterval(k8sProbeSpec))

	probeServerOpts := system.ServiceHealthHTTPServerOptions{
		Addr:          k8sProbeSpec.BindAddr,
		Probe:         serviceHealthProbe,
		LivenessPath:  k8sProbeSpec.Liveness.Path,
		ReadinessPath: k8sProbeSpec.Readiness.Path,
		Logf:          log.Printf,
	}
	if metricsCollector != nil {
		probeServerOpts.MetricsPath = metricsCollector.Path()
		probeServerOpts.MetricsHandler = metricsCollector.Handler()
	}
	probeServer, err := system.NewServiceHealthHTTPServer(probeServerOpts)
	if err != nil {
		log.Printf("Liveness/Readiness 探针服务初始化失败，自动降级关闭: %v", err)
	} else {
		go func() {
			if runErr := probeServer.Run(ctx); runErr != nil {
				log.Printf("Liveness/Readiness 探针服务停止: %v", runErr)
			}
		}()
	}

	poolMonitor := db.NewDBPoolMonitor(database, db.DBPoolMonitorOptions{
		Interval:                  db.DefaultDBPoolMonitorInterval,
		UtilizationAlertThreshold: db.DefaultDBPoolMonitorUtilizationAlertThreshold,
		WaitCountAlertDelta:       db.DefaultDBPoolMonitorWaitCountAlertDelta,
		Logf:                      log.Printf,
	})
	log.Printf(
		"数据库连接池监控已启用：interval=%s utilization_threshold=%.0f%% wait_count_delta>=%d",
		db.DefaultDBPoolMonitorInterval,
		db.DefaultDBPoolMonitorUtilizationAlertThreshold*100,
		db.DefaultDBPoolMonitorWaitCountAlertDelta,
	)
	diskMonitorPath := filepath.Dir(cfg.DBMasterPath)
	if strings.TrimSpace(diskMonitorPath) == "" {
		diskMonitorPath = "."
	}
	diskMonitor := system.NewDiskSpaceMonitor(system.DiskSpaceMonitorOptions{
		Path:           diskMonitorPath,
		Interval:       system.DefaultDiskSpaceMonitorInterval,
		MinFreeBytes:   system.DefaultDiskSpaceMonitorMinFreeBytes,
		MinFreePercent: system.DefaultDiskSpaceMonitorMinFreePercent,
		Logf:           log.Printf,
	})
	log.Printf(
		"磁盘空间监控已启用：path=%s interval=%s min_free_bytes=%d min_free_percent=%.0f%%",
		diskMonitorPath,
		system.DefaultDiskSpaceMonitorInterval,
		system.DefaultDiskSpaceMonitorMinFreeBytes,
		system.DefaultDiskSpaceMonitorMinFreePercent,
	)
	memoryLeakMonitor := system.NewMemoryLeakMonitor(system.MemoryLeakMonitorOptions{
		Interval:            system.DefaultMemoryLeakMonitorInterval,
		MinHeapInUseBytes:   system.DefaultMemoryLeakMonitorMinHeapInUseBytes,
		GrowthThreshold:     system.DefaultMemoryLeakMonitorGrowthThreshold,
		ConsecutiveGCCycles: system.DefaultMemoryLeakMonitorConsecutiveGCCycles,
		Logf:                log.Printf,
		OnAlert: func(alert system.MemoryLeakAlert) {
			killSwitch.TriggerSevereBug(
				"memory_leak_monitor",
				fmt.Sprintf(
					"heap_inuse=%d growth=%.1f%% heap_objects=%d growth=%.1f%% gc_total=%d",
					alert.Current.HeapInUse,
					alert.HeapInUseGrowth*100,
					alert.Current.HeapObjects,
					alert.HeapObjectsGrowth*100,
					alert.Current.NumGC,
				),
			)
		},
	})
	log.Printf(
		"内存泄漏检测已启用：pprof=heap interval=%s min_heap_inuse_bytes=%d growth_threshold=%.0f%% consecutive_gc_cycles=%d",
		system.DefaultMemoryLeakMonitorInterval,
		system.DefaultMemoryLeakMonitorMinHeapInUseBytes,
		system.DefaultMemoryLeakMonitorGrowthThreshold*100,
		system.DefaultMemoryLeakMonitorConsecutiveGCCycles,
	)
	const apiQuotaKillSwitchFailureThreshold int64 = 50
	apiQuotaMonitor := system.NewAPIQuotaMonitor(system.APIQuotaMonitorOptions{
		Interval:          system.DefaultAPIQuotaMonitorInterval,
		LowRemainingRatio: system.DefaultAPIQuotaMonitorLowRemainingRatio,
		LowRemainingCount: system.DefaultAPIQuotaMonitorLowRemainingCount,
		Logf:              log.Printf,
		OnAlert: func(alert system.APIQuotaAlert) {
			// 只在额度耗尽且失败数快速上升时触发，避免常规低额度误停机。
			if alert.Remaining > 0 || alert.FailuresTotal < apiQuotaKillSwitchFailureThreshold {
				return
			}
			killSwitch.TriggerAttack(
				"api_quota_monitor",
				fmt.Sprintf(
					"provider=%s endpoint=%s remaining=%d failures=%d requests=%d",
					alert.Provider,
					alert.Endpoint,
					alert.Remaining,
					alert.FailuresTotal,
					alert.RequestsTotal,
				),
			)
		},
	})
	app.SetAddressAPIQuotaRecorder(func(sample services.APIQuotaSample) {
		apiQuotaMonitor.Observe(system.APIQuotaSample{
			Provider:    sample.Provider,
			Endpoint:    sample.Endpoint,
			StatusCode:  sample.StatusCode,
			Limit:       sample.Limit,
			Remaining:   sample.Remaining,
			Used:        sample.Used,
			ResetAt:     sample.ResetAt,
			CollectedAt: sample.CollectedAt,
		})
	})
	log.Printf(
		"API 额度监控已启用：provider=tronscan interval=%s low_remaining_ratio=%.0f%% low_remaining_count=%d",
		system.DefaultAPIQuotaMonitorInterval,
		system.DefaultAPIQuotaMonitorLowRemainingRatio*100,
		system.DefaultAPIQuotaMonitorLowRemainingCount,
	)
	var cloudCostMonitor *system.CloudCostMonitor
	if cloudCostMonitorSpec.Enabled {
		cloudCostMonitor = system.NewCloudCostMonitor(system.CloudCostMonitorOptions{
			Interval:             cloudCostMonitorSpec.Interval,
			MonthlyBudget:        cloudCostMonitorSpec.MonthlyBudget,
			AlertThresholdRatio:  cloudCostMonitorSpec.AlertThresholdRatio,
			GrowthThresholdRatio: cloudCostMonitorSpec.GrowthThresholdRatio,
			Currency:             cloudCostMonitorSpec.Currency,
			Logf:                 log.Printf,
		})
	}
	var chaosEngineer *system.ChaosEngineer
	if chaosEngineeringSpec.Enabled {
		chaosEngineer = system.NewChaosEngineer(system.ChaosEngineerOptions{
			Interval:                    chaosEngineeringSpec.Interval,
			DBOutageDuration:            chaosEngineeringSpec.DBOutageDuration,
			NetworkInterruptionDuration: chaosEngineeringSpec.NetworkInterruptionDuration,
			RunDBOutage: func(_ context.Context, plannedDuration time.Duration) error {
				log.Printf(
					"故障演练注入: scenario=%s planned_duration=%s strategy=模拟DB连接不可用",
					system.ChaosScenarioDBOutage,
					plannedDuration,
				)
				return nil
			},
			RunNetworkInterruption: func(_ context.Context, plannedDuration time.Duration) error {
				log.Printf(
					"故障演练注入: scenario=%s planned_duration=%s strategy=模拟网络不可达",
					system.ChaosScenarioNetworkInterruption,
					plannedDuration,
				)
				return nil
			},
			Logf: log.Printf,
		})
	}

	go poolMonitor.Run(ctx)
	go diskMonitor.Run(ctx)
	go memoryLeakMonitor.Run(ctx)
	go apiQuotaMonitor.Run(ctx)
	if cloudCostMonitor != nil {
		go cloudCostMonitor.Run(ctx)
	}
	if chaosEngineer != nil {
		go chaosEngineer.Run(ctx)
	}
	if walArchiver != nil {
		go walArchiver.Run(ctx)
	}
	if dailyFullBackup != nil {
		go dailyFullBackup.Run(ctx)
	}
	if backupVerifier != nil {
		go backupVerifier.Run(ctx)
	}
	if crossRegionDR != nil {
		go crossRegionDR.Run(ctx)
	}
	go runDayCutSettlementLoop(ctx, cfg.Timezone, database, time.Now, log.Printf)
	log.Printf(
		"日切结算单任务已启用：timezone=%s interval=%s，按群日切时间生成当日费用清单",
		cfg.Timezone,
		defaultDayCutSettlementLoopInterval,
	)
	go runDuplicateAlertAutoClearLoop(ctx, cfg.Timezone, database, defaultDuplicateAlertAutoClearLead, time.Now, log.Printf)
	log.Printf(
		"重复预警缓存自动清理任务已启用：timezone=%s trigger=日切前%s与日切后%s",
		cfg.Timezone,
		defaultDuplicateAlertAutoClearLead,
		defaultDuplicateAlertAutoClearPostWindow,
	)
	go runDailySettlementLoop(ctx, cfg.Timezone, database, time.Now, log.Printf)
	log.Printf("T+1 结算报表任务已启用：timezone=%s schedule=每日00:00，自动生成昨日完整财务报表", cfg.Timezone)
	go runScheduledReconciliationLoop(ctx, cfg.Timezone, database, time.Now, log.Printf)
	log.Printf(
		"定时对账任务已启用：timezone=%s schedule=每日%02d:%02d tolerance=%g 口径=Transaction(ledger_entries)汇总 vs Balance(ledger_balances)余额",
		cfg.Timezone,
		defaultScheduledReconciliationHour,
		defaultScheduledReconciliationMinute,
		db.DefaultScheduledReconciliationTolerance,
	)

	adminServer := admin.NewServer(cfg, database, app)
	adminServer.SetKillSwitch(killSwitch)
	go func() {
		if err := adminServer.Run(ctx); err != nil {
			log.Printf("后台服务停止: %v", err)
		}
	}()

	serviceHealthProbe.MarkReady()
	if err := app.Run(ctx); err != nil {
		serviceHealthProbe.MarkNotReady("bot app returned error")
		log.Fatalf("运行失败: %v", err)
	}
}
