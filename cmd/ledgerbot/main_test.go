package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-bot/internal/accounting"
	"go-bot/internal/config"
	"go-bot/internal/db"
	"go-bot/internal/system"
)

type updateMetricsRecorderStub struct {
	calls    int
	lastFrom string
	lastCost time.Duration
	lastOK   bool
}

func (s *updateMetricsRecorderStub) ObserveUpdate(source string, duration time.Duration, success bool) {
	s.calls++
	s.lastFrom = source
	s.lastCost = duration
	s.lastOK = success
}

func TestLoadJournalSampleEntry(t *testing.T) {
	t.Run("default instruction", func(t *testing.T) {
		instruction, entry, err := loadJournalSampleEntry(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认分录示例不应报错: %v", err)
		}
		if instruction != defaultJournalSampleInstruction {
			t.Fatalf("默认指令错误: %q", instruction)
		}
		if entry.EntryType != accounting.JournalEntryTypeDeposit {
			t.Fatalf("默认分录类型错误: %s", entry.EntryType)
		}
		if len(entry.Lines) != 2 {
			t.Fatalf("默认分录拆分行数错误: %d", len(entry.Lines))
		}
		if entry.Lines[0].Direction != accounting.JournalLineDirectionDebit || entry.Lines[0].AccountCode != accounting.JournalAccountCodeMerchantReceivable || entry.Lines[0].AccountName != accounting.JournalAccountNameMerchantReceivable || entry.Lines[0].Amount != 100 {
			t.Fatalf("默认借方分录错误: %+v", entry.Lines[0])
		}
		if entry.Lines[1].Direction != accounting.JournalLineDirectionCredit || entry.Lines[1].AccountCode != accounting.JournalAccountCodeCustomerBalance || entry.Lines[1].AccountName != accounting.JournalAccountNameCustomerBalance || entry.Lines[1].Amount != 100 {
			t.Fatalf("默认贷方分录错误: %+v", entry.Lines[1])
		}
		want := "借：商户应收 100，贷：客户余额 100"
		if entry.SummaryCN() != want {
			t.Fatalf("默认分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})

	t.Run("custom instruction", func(t *testing.T) {
		instruction, entry, err := loadJournalSampleEntry(func(key string) string {
			if key == "JOURNAL_SAMPLE_INSTRUCTION" {
				return "+88.5"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("自定义分录示例不应报错: %v", err)
		}
		if instruction != "+88.5" {
			t.Fatalf("自定义指令错误: %q", instruction)
		}
		want := "借：商户应收 88.5，贷：客户余额 88.5"
		if entry.SummaryCN() != want {
			t.Fatalf("自定义分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})

	t.Run("custom payout instruction", func(t *testing.T) {
		instruction, entry, err := loadJournalSampleEntry(func(key string) string {
			if key == "JOURNAL_SAMPLE_INSTRUCTION" {
				return "-66.6"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("自定义下发分录示例不应报错: %v", err)
		}
		if instruction != "-66.6" {
			t.Fatalf("自定义下发指令错误: %q", instruction)
		}
		if entry.EntryType != accounting.JournalEntryTypePayout {
			t.Fatalf("自定义下发分录类型错误: %s", entry.EntryType)
		}
		if entry.Lines[0].Direction != accounting.JournalLineDirectionDebit || entry.Lines[0].AccountCode != accounting.JournalAccountCodeCustomerBalance {
			t.Fatalf("自定义下发借方分录错误: %+v", entry.Lines[0])
		}
		if entry.Lines[1].Direction != accounting.JournalLineDirectionCredit || entry.Lines[1].AccountCode != accounting.JournalAccountCodeMerchantReceivable {
			t.Fatalf("自定义下发贷方分录错误: %+v", entry.Lines[1])
		}
		want := "借：客户余额 66.6，贷：商户应收 66.6"
		if entry.SummaryCN() != want {
			t.Fatalf("自定义下发分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})

	t.Run("custom instruction should be normalized", func(t *testing.T) {
		instruction, entry, err := loadJournalSampleEntry(func(key string) string {
			if key == "JOURNAL_SAMPLE_INSTRUCTION" {
				return " ＋ 88.50 "
			}
			return ""
		})
		if err != nil {
			t.Fatalf("自定义分录示例不应报错: %v", err)
		}
		if instruction != "+88.5" {
			t.Fatalf("自定义指令应返回归一化结果: %q", instruction)
		}
		if entry.Instruction != "+88.5" {
			t.Fatalf("分录内原始指令应归一化: %q", entry.Instruction)
		}
	})

	t.Run("custom instruction with full-width amount", func(t *testing.T) {
		instruction, entry, err := loadJournalSampleEntry(func(key string) string {
			if key == "JOURNAL_SAMPLE_INSTRUCTION" {
				return "＋１００．５"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("全角金额分录示例不应报错: %v", err)
		}
		if instruction != "+100.5" {
			t.Fatalf("全角金额指令应返回归一化结果: %q", instruction)
		}
		want := "借：商户应收 100.5，贷：客户余额 100.5"
		if entry.SummaryCN() != want {
			t.Fatalf("全角金额分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})

	t.Run("custom instruction with unit suffix", func(t *testing.T) {
		instruction, entry, err := loadJournalSampleEntry(func(key string) string {
			if key == "JOURNAL_SAMPLE_INSTRUCTION" {
				return " ＋１００．５ｕ "
			}
			return ""
		})
		if err != nil {
			t.Fatalf("带单位后缀分录示例不应报错: %v", err)
		}
		if instruction != "+100.5" {
			t.Fatalf("带单位后缀指令应返回归一化结果: %q", instruction)
		}
		want := "借：商户应收 100.5，贷：客户余额 100.5"
		if entry.SummaryCN() != want {
			t.Fatalf("带单位后缀分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})

	t.Run("fallback when invalid instruction", func(t *testing.T) {
		instruction, entry, err := loadJournalSampleEntry(func(key string) string {
			if key == "JOURNAL_SAMPLE_INSTRUCTION" {
				return "invalid"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("非法指令应触发回退错误")
		}
		if !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("回退错误信息应包含非法指令: %v", err)
		}
		if instruction != defaultJournalSampleInstruction {
			t.Fatalf("非法指令应回退默认值: %q", instruction)
		}
		want := "借：商户应收 100，贷：客户余额 100"
		if entry.SummaryCN() != want {
			t.Fatalf("回退默认分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})
}

func TestLoadMultiCurrencyPrecisionSample(t *testing.T) {
	t.Run("default precision sample", func(t *testing.T) {
		sample, err := loadMultiCurrencyPrecisionSample(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认精度示例不应报错: %v", err)
		}
		if got := sample.USDTAmount.StringFixed(accounting.CurrencyScaleUSDT); got != defaultPrecisionSampleUSDT {
			t.Fatalf("默认 USDT 金额错误: %s", got)
		}
		if got := sample.Rate.StringFixed(accounting.CurrencyScaleUSDT); got != defaultPrecisionSampleRate {
			t.Fatalf("默认汇率错误: %s", got)
		}
		if got := sample.FiatAmount.StringFixed(accounting.CurrencyScaleFiat); got != "879.44" {
			t.Fatalf("默认法币金额错误，want=%s got=%s", "879.44", got)
		}
		if got := sample.RoundTripUSDT.StringFixed(accounting.CurrencyScaleUSDT); got != "123.45691525" {
			t.Fatalf("默认回算 USDT 错误，want=%s got=%s", "123.45691525", got)
		}
		if sample.USDTAtomic != 12345678901 {
			t.Fatalf("默认 USDT 原子单位错误，want=%d got=%d", int64(12345678901), sample.USDTAtomic)
		}
		if sample.FiatMinor != 87944 {
			t.Fatalf("默认法币最小单位错误，want=%d got=%d", int64(87944), sample.FiatMinor)
		}
	})

	t.Run("custom precision sample", func(t *testing.T) {
		sample, err := loadMultiCurrencyPrecisionSample(func(key string) string {
			switch key {
			case "PRECISION_SAMPLE_USDT":
				return "100"
			case "PRECISION_SAMPLE_RATE":
				return "7.125"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义精度示例不应报错: %v", err)
		}
		if got := sample.FiatAmount.StringFixed(accounting.CurrencyScaleFiat); got != "712.50" {
			t.Fatalf("自定义法币金额错误，want=%s got=%s", "712.50", got)
		}
		if got := sample.RoundTripUSDT.StringFixed(accounting.CurrencyScaleUSDT); got != "100.00000000" {
			t.Fatalf("自定义回算 USDT 错误，want=%s got=%s", "100.00000000", got)
		}
		if sample.USDTAtomic != 10000000000 {
			t.Fatalf("自定义 USDT 原子单位错误，want=%d got=%d", int64(10000000000), sample.USDTAtomic)
		}
		if sample.FiatMinor != 71250 {
			t.Fatalf("自定义法币最小单位错误，want=%d got=%d", int64(71250), sample.FiatMinor)
		}
	})

	t.Run("fallback when invalid precision sample", func(t *testing.T) {
		sample, err := loadMultiCurrencyPrecisionSample(func(key string) string {
			switch key {
			case "PRECISION_SAMPLE_USDT":
				return "invalid"
			case "PRECISION_SAMPLE_RATE":
				return "0"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法精度参数应触发回退错误")
		}
		if !strings.Contains(err.Error(), "PRECISION_SAMPLE_USDT") {
			t.Fatalf("回退错误信息应包含非法变量名: %v", err)
		}
		if got := sample.USDTAmount.StringFixed(accounting.CurrencyScaleUSDT); got != defaultPrecisionSampleUSDT {
			t.Fatalf("回退后 USDT 金额错误: %s", got)
		}
		if got := sample.FiatAmount.StringFixed(accounting.CurrencyScaleFiat); got != "879.44" {
			t.Fatalf("回退后法币金额错误，want=%s got=%s", "879.44", got)
		}
		if sample.USDTAtomic != 12345678901 {
			t.Fatalf("回退后 USDT 原子单位错误，want=%d got=%d", int64(12345678901), sample.USDTAtomic)
		}
		if sample.FiatMinor != 87944 {
			t.Fatalf("回退后法币最小单位错误，want=%d got=%d", int64(87944), sample.FiatMinor)
		}
	})
}

func TestBuildTimeAnomalyMonitorLog(t *testing.T) {
	t.Run("未配置工作时间", func(t *testing.T) {
		got := buildTimeAnomalyMonitorLog(config.Config{})
		if !strings.Contains(got, "异常时间监控未启用") {
			t.Fatalf("应提示未启用: %q", got)
		}
		if !strings.Contains(got, "WORK_HOURS_START/WORK_HOURS_END") {
			t.Fatalf("应提示配置项: %q", got)
		}
	})

	t.Run("配置有效工作时间", func(t *testing.T) {
		got := buildTimeAnomalyMonitorLog(config.Config{
			Timezone:       "Asia/Shanghai",
			WorkHoursStart: "09:00",
			WorkHoursEnd:   "18:00",
		})
		if !strings.Contains(got, "异常时间监控已启用") {
			t.Fatalf("应提示已启用: %q", got)
		}
		if !strings.Contains(got, "work_hours=09:00:00-18:00:00") {
			t.Fatalf("应输出规范化时间窗口: %q", got)
		}
		if !strings.Contains(got, "timezone=Asia/Shanghai") {
			t.Fatalf("应输出时区: %q", got)
		}
	})

	t.Run("配置无效工作时间", func(t *testing.T) {
		got := buildTimeAnomalyMonitorLog(config.Config{
			WorkHoursStart: "invalid",
			WorkHoursEnd:   "18:00",
		})
		if !strings.Contains(got, "格式无效") {
			t.Fatalf("应提示配置格式无效: %q", got)
		}
		if !strings.Contains(got, "WORK_HOURS_START=\"invalid\"") {
			t.Fatalf("应回显非法配置值: %q", got)
		}
	})
}

func TestBuildAddressVerificationMediaPublisherDefaultsToMultipart(t *testing.T) {
	publisher, configured, err := buildAddressVerificationMediaPublisher(config.Config{})
	if err != nil {
		t.Fatalf("默认 multipart 不应报错: %v", err)
	}
	if configured || publisher != nil {
		t.Fatalf("默认模式应保持 multipart: configured=%t publisher=%v", configured, publisher)
	}
}

func TestBuildAddressVerificationMediaPublisherConfigRequiresFixedProductionValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
	}{
		{
			name: "wrong directory",
			cfg: config.Config{
				AddressVerifyMediaMode:          "local_url",
				AddressVerifyMediaDir:           "/tmp/telegram-media",
				AddressVerifyMediaPublicBaseURL: addressVerificationMediaProductionBaseURL,
			},
		},
		{
			name: "wrong base url",
			cfg: config.Config{
				AddressVerifyMediaMode:          "local_url",
				AddressVerifyMediaDir:           addressVerificationMediaProductionDirectory,
				AddressVerifyMediaPublicBaseURL: "https://example.com/tg/media",
			},
		},
		{
			name: "unknown mode",
			cfg: config.Config{
				AddressVerifyMediaMode: "future_mode",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publisherConfig, configured, err := buildAddressVerificationMediaPublisherConfig(test.cfg)
			if err == nil {
				t.Fatal("invalid local_url configuration should return an error")
			}
			if !configured {
				t.Fatal("invalid requested mode should be reported as configured")
			}
			if publisherConfig.Directory != "" || publisherConfig.PublicBaseURL != "" {
				t.Fatalf("invalid configuration should not produce publisher config: %+v", publisherConfig)
			}
		})
	}
}

func TestBuildAddressVerificationMediaPublisherConfigUsesFixedContract(t *testing.T) {
	publisherConfig, configured, err := buildAddressVerificationMediaPublisherConfig(config.Config{
		AddressVerifyMediaMode:          "LOCAL_URL",
		AddressVerifyMediaDir:           " /var/lib/ledgerbot/telegram-media ",
		AddressVerifyMediaPublicBaseURL: " https://autobookbl.site/tg/media ",
		AddressVerifyMediaTTL:           2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("valid local_url configuration should pass: %v", err)
	}
	if !configured {
		t.Fatal("local_url should be reported as configured")
	}
	if publisherConfig.Directory != addressVerificationMediaProductionDirectory || publisherConfig.PublicBaseURL != addressVerificationMediaProductionBaseURL || publisherConfig.TTL != 2*time.Hour || !publisherConfig.CreateDirectory {
		t.Fatalf("unexpected publisher config: %+v", publisherConfig)
	}
}

func TestAddressVerificationMediaServiceAllowsMediaDirectoryWrites(t *testing.T) {
	servicePath := filepath.Join("..", "..", "scripts", "bilibili-ledgerbot.service.v2")
	contents, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatalf("read service template: %v", err)
	}
	service := string(contents)
	for _, required := range []string{
		"User=ledgerbot",
		"ProtectSystem=full",
		"ReadWritePaths=/opt/bilibili-ledgerbot-20260704/data /opt/bilibili-ledgerbot-20260704/data/recovery /opt/bilibili-ledgerbot-20260704/logs /var/lib/ledgerbot/telegram-media",
	} {
		if !strings.Contains(service, required) {
			t.Fatalf("service template missing %q", required)
		}
	}
}

func TestBuildRateDeviationAlertLog(t *testing.T) {
	logLine := buildRateDeviationAlertLog()
	if !strings.Contains(logLine, "汇率偏离告警已启用") {
		t.Fatalf("汇率偏离告警日志缺少启用标记: %q", logLine)
	}
	if !strings.Contains(logLine, "threshold=10.00%") {
		t.Fatalf("汇率偏离告警日志阈值错误: %q", logLine)
	}
	if !strings.Contains(logLine, "超阈值通知权限人") {
		t.Fatalf("汇率偏离告警日志缺少通知策略: %q", logLine)
	}
}

func TestBuildExportEngineLog(t *testing.T) {
	logLine := buildExportEngineLog()
	if !strings.Contains(logLine, "导出引擎已启用") {
		t.Fatalf("导出引擎日志缺少启用标记: %q", logLine)
	}
	if !strings.Contains(logLine, "导出账单") {
		t.Fatalf("导出引擎日志缺少命令提示: %q", logLine)
	}
	if !strings.Contains(logLine, "Excel/PDF") {
		t.Fatalf("导出引擎日志缺少格式提示: %q", logLine)
	}
}

func TestBuildTemplateRendererLog(t *testing.T) {
	logLine := buildTemplateRendererLog()
	if !strings.Contains(logLine, "格式化渲染器已启用") {
		t.Fatalf("模板渲染器日志缺少启用标记: %q", logLine)
	}
	if !strings.Contains(logLine, "[html]/[md]") {
		t.Fatalf("模板渲染器日志缺少模板前缀说明: %q", logLine)
	}
	if !strings.Contains(logLine, "ParseMode") {
		t.Fatalf("模板渲染器日志缺少 ParseMode 说明: %q", logLine)
	}
}

func TestBuildGrafanaDashboardLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildGrafanaDashboardLog(func(string) string { return "" }, "/metrics")
		if err != nil {
			t.Fatalf("默认 dashboard 配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "Grafana Dashboard 已定义") {
			t.Fatalf("默认 dashboard 日志缺少启用标记: %q", logLine)
		}
		if !strings.Contains(logLine, "namespace=ledgerbot") {
			t.Fatalf("默认 dashboard 日志缺少命名空间: %q", logLine)
		}
		if !strings.Contains(logLine, "panels=6") {
			t.Fatalf("默认 dashboard 日志缺少 panel 数量: %q", logLine)
		}
		if !strings.Contains(logLine, "prometheus_path=/metrics") {
			t.Fatalf("默认 dashboard 日志缺少 metrics path: %q", logLine)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildGrafanaDashboardLog(func(key string) string {
			switch key {
			case "GRAFANA_DASHBOARD_UID":
				return "ledgerbot-prod"
			case "GRAFANA_DASHBOARD_TITLE":
				return "LedgerBot 生产监控"
			case "GRAFANA_DASHBOARD_DATASOURCE":
				return "Prometheus-Prod"
			case "GRAFANA_DASHBOARD_NAMESPACE":
				return "ledgerbotprod"
			case "GRAFANA_DASHBOARD_REFRESH":
				return "10s"
			default:
				return ""
			}
		}, "/monitor/metrics")
		if err != nil {
			t.Fatalf("自定义 dashboard 配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "title=LedgerBot 生产监控") {
			t.Fatalf("自定义 dashboard 日志缺少标题: %q", logLine)
		}
		if !strings.Contains(logLine, "uid=ledgerbot-prod") {
			t.Fatalf("自定义 dashboard 日志缺少 uid: %q", logLine)
		}
		if !strings.Contains(logLine, "datasource=Prometheus-Prod") {
			t.Fatalf("自定义 dashboard 日志缺少 datasource: %q", logLine)
		}
		if !strings.Contains(logLine, "namespace=ledgerbotprod") {
			t.Fatalf("自定义 dashboard 日志缺少 namespace: %q", logLine)
		}
		if !strings.Contains(logLine, "refresh=10s") {
			t.Fatalf("自定义 dashboard 日志缺少 refresh: %q", logLine)
		}
		if !strings.Contains(logLine, "prometheus_path=/monitor/metrics") {
			t.Fatalf("自定义 dashboard 日志缺少 metrics path: %q", logLine)
		}
	})

	t.Run("非法 namespace 触发回退", func(t *testing.T) {
		logLine, err := buildGrafanaDashboardLog(func(key string) string {
			if key == "GRAFANA_DASHBOARD_NAMESPACE" {
				return "ledgerbot-prod"
			}
			return ""
		}, "")
		if err == nil {
			t.Fatalf("非法 namespace 应返回告警")
		}
		if !strings.Contains(err.Error(), "grafana dashboard config invalid") {
			t.Fatalf("错误信息应包含回退提示: %v", err)
		}
		if !strings.Contains(logLine, "namespace=ledgerbot") {
			t.Fatalf("非法 namespace 应回退默认值: %q", logLine)
		}
		if !strings.Contains(logLine, "prometheus_path=/metrics") {
			t.Fatalf("未传入 metrics path 时应回退默认值: %q", logLine)
		}
	})
}

func TestBuildAlertManagerLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, spec, err := buildAlertManagerLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认告警配置不应报错: %v", err)
		}
		if spec.Enabled {
			t.Fatalf("默认告警应关闭")
		}
		if !strings.Contains(logLine, "智能告警未启用") {
			t.Fatalf("默认日志应提示未启用: %q", logLine)
		}
	})

	t.Run("自动启用 telegram 通道", func(t *testing.T) {
		logLine, spec, err := buildAlertManagerLog(func(key string) string {
			switch key {
			case "BOT_TOKEN":
				return "bot-token"
			case "SUPPORT_CHAT_ID":
				return "-10086"
			case "ALERT_CONSECUTIVE_FAILURE_THRESHOLD":
				return "4"
			case "ALERT_LATENCY_SPIKE_THRESHOLD":
				return "1500ms"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("合法配置不应报错: %v", err)
		}
		if !spec.Enabled || !spec.TelegramEnabled() {
			t.Fatalf("应自动启用 telegram 通道: %+v", spec)
		}
		if !strings.Contains(logLine, "智能告警已启用") {
			t.Fatalf("日志应提示已启用: %q", logLine)
		}
		if !strings.Contains(logLine, "consecutive_failures>=4") {
			t.Fatalf("日志缺少连续失败阈值: %q", logLine)
		}
		if !strings.Contains(logLine, "latency_spike>=1.5s") {
			t.Fatalf("日志缺少延迟阈值: %q", logLine)
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, spec, err := buildAlertManagerLog(func(key string) string {
			switch key {
			case "ALERT_MANAGER_ENABLED":
				return "true"
			case "ALERT_CONSECUTIVE_FAILURE_THRESHOLD":
				return "bad"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法配置应返回告警")
		}
		if !strings.Contains(err.Error(), "ALERT_CONSECUTIVE_FAILURE_THRESHOLD") {
			t.Fatalf("回退错误应包含非法配置键: %v", err)
		}
		if spec.ConsecutiveFailureThreshold != system.DefaultAlertManagerConsecutiveFailureThreshold {
			t.Fatalf(
				"非法连续失败阈值应回退默认值: want=%d got=%d",
				system.DefaultAlertManagerConsecutiveFailureThreshold,
				spec.ConsecutiveFailureThreshold,
			)
		}
		if spec.Enabled {
			t.Fatalf("未配置有效通道时应自动关闭告警")
		}
		if !strings.Contains(logLine, "智能告警未启用") {
			t.Fatalf("日志应提示未启用: %q", logLine)
		}
	})
}

func TestNewFanoutUpdateMetricsRecorder(t *testing.T) {
	if got := newFanoutUpdateMetricsRecorder(); got != nil {
		t.Fatalf("空 recorder 列表应返回 nil")
	}

	one := &updateMetricsRecorderStub{}
	if got := newFanoutUpdateMetricsRecorder(one); got != one {
		t.Fatalf("单 recorder 场景应直接复用原实例")
	}

	first := &updateMetricsRecorderStub{}
	second := &updateMetricsRecorderStub{}
	recorder := newFanoutUpdateMetricsRecorder(first, nil, second)
	if recorder == nil {
		t.Fatalf("多 recorder 场景应返回扇出实例")
	}
	recorder.ObserveUpdate("message", 120*time.Millisecond, false)
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("扇出 recorder 未将事件分发到所有下游: first=%d second=%d", first.calls, second.calls)
	}
	if second.lastFrom != "message" || second.lastCost != 120*time.Millisecond || second.lastOK {
		t.Fatalf("扇出 recorder 分发参数错误: %+v", second)
	}
}

func TestBuildDockerBuildLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildDockerBuildLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "builder="+system.DefaultDockerBuildBuilderImage) {
			t.Fatalf("默认构建镜像日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "runtime="+system.DefaultDockerBuildRuntimeImage) {
			t.Fatalf("默认运行时镜像日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "target="+system.DefaultDockerBuildTarget) {
			t.Fatalf("默认构建目标日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "entrypoint=/app/"+system.DefaultDockerBuildBinaryName) {
			t.Fatalf("默认入口日志缺失: %q", logLine)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildDockerBuildLog(func(key string) string {
			switch key {
			case "DOCKER_BUILD_BUILDER_IMAGE":
				return "golang:1.25-alpine3.21"
			case "DOCKER_BUILD_RUNTIME_IMAGE":
				return "alpine:3.21"
			case "DOCKER_BUILD_BINARY_NAME":
				return "ledgerbotd"
			case "DOCKER_BUILD_TARGET":
				return "./cmd/ledgerbot"
			case "DOCKER_BUILD_WORKDIR":
				return "/srv/app"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "builder=golang:1.25-alpine3.21") {
			t.Fatalf("自定义构建镜像日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "runtime=alpine:3.21") {
			t.Fatalf("自定义运行时镜像日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "binary=ledgerbotd") {
			t.Fatalf("自定义二进制名称日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "entrypoint=/srv/app/ledgerbotd") {
			t.Fatalf("自定义入口日志缺失: %q", logLine)
		}
	})

	t.Run("非法 runtime 镜像触发回退", func(t *testing.T) {
		logLine, err := buildDockerBuildLog(func(key string) string {
			if key == "DOCKER_BUILD_RUNTIME_IMAGE" {
				return "ubuntu:24.04"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("非法 runtime 镜像应返回告警")
		}
		if !strings.Contains(err.Error(), "DOCKER_BUILD_RUNTIME_IMAGE") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "runtime="+system.DefaultDockerBuildRuntimeImage) {
			t.Fatalf("非法 runtime 镜像应回退默认值: %q", logLine)
		}
	})
}

func TestBuildK8sDeploymentLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildK8sDeploymentLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "replicas=2") {
			t.Fatalf("默认副本数日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "requests(cpu=250m memory=256Mi)") {
			t.Fatalf("默认 requests 日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "limits(cpu=1 memory=512Mi)") {
			t.Fatalf("默认 limits 日志缺失: %q", logLine)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildK8sDeploymentLog(func(key string) string {
			switch key {
			case "K8S_DEPLOYMENT_NAME":
				return "ledgerbot"
			case "K8S_DEPLOYMENT_NAMESPACE":
				return "finance"
			case "K8S_DEPLOYMENT_REPLICAS":
				return "4"
			case "K8S_DEPLOYMENT_CPU_REQUEST":
				return "600m"
			case "K8S_DEPLOYMENT_CPU_LIMIT":
				return "2"
			case "K8S_DEPLOYMENT_MEM_REQUEST":
				return "768Mi"
			case "K8S_DEPLOYMENT_MEM_LIMIT":
				return "2Gi"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "namespace=finance") {
			t.Fatalf("命名空间日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "replicas=4") {
			t.Fatalf("自定义副本数日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "requests(cpu=600m memory=768Mi)") {
			t.Fatalf("自定义 requests 日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "limits(cpu=2 memory=2Gi)") {
			t.Fatalf("自定义 limits 日志缺失: %q", logLine)
		}
	})

	t.Run("非法副本数触发回退", func(t *testing.T) {
		logLine, err := buildK8sDeploymentLog(func(key string) string {
			if key == "K8S_DEPLOYMENT_REPLICAS" {
				return "invalid"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("非法副本数应返回告警")
		}
		if !strings.Contains(err.Error(), "K8S_DEPLOYMENT_REPLICAS") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "replicas=2") {
			t.Fatalf("非法副本数应回退默认值: %q", logLine)
		}
	})
}

func TestBuildK8sHPALog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildK8sHPALog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "replicas(min=2 max=10)") {
			t.Fatalf("默认副本范围日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "metrics(cpu=70% qps_per_pod=120)") {
			t.Fatalf("默认扩缩容指标日志缺失: %q", logLine)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildK8sHPALog(func(key string) string {
			switch key {
			case "K8S_HPA_NAME":
				return "ledgerbot-hpa-prod"
			case "K8S_HPA_NAMESPACE":
				return "finance"
			case "K8S_HPA_TARGET_DEPLOYMENT":
				return "ledgerbot"
			case "K8S_HPA_MIN_REPLICAS":
				return "3"
			case "K8S_HPA_MAX_REPLICAS":
				return "15"
			case "K8S_HPA_TARGET_CPU":
				return "60"
			case "K8S_HPA_TARGET_QPS_PER_POD":
				return "240"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "name=ledgerbot-hpa-prod") {
			t.Fatalf("HPA 名称日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "namespace=finance") {
			t.Fatalf("命名空间日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "target=ledgerbot") {
			t.Fatalf("目标 deployment 日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "replicas(min=3 max=15)") {
			t.Fatalf("自定义副本范围日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "metrics(cpu=60% qps_per_pod=240)") {
			t.Fatalf("自定义扩缩容指标日志缺失: %q", logLine)
		}
	})

	t.Run("非法最小副本数触发回退", func(t *testing.T) {
		logLine, err := buildK8sHPALog(func(key string) string {
			if key == "K8S_HPA_MIN_REPLICAS" {
				return "invalid"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("非法最小副本数应返回告警")
		}
		if !strings.Contains(err.Error(), "K8S_HPA_MIN_REPLICAS") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "replicas(min=2 max=10)") {
			t.Fatalf("非法最小副本数应回退默认值: %q", logLine)
		}
	})
}

func TestBuildK8sConfigMapSecretLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildK8sConfigMapSecretLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认配置不应报错: %v", err)
		}
		defaultSpec := system.DefaultK8sConfigMapSecretSpec()
		if !strings.Contains(logLine, "namespace=default") {
			t.Fatalf("默认命名空间日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "configmap=ledgerbot-config") {
			t.Fatalf("默认 configmap 名称日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "secret=ledgerbot-secret") {
			t.Fatalf("默认 secret 名称日志缺失: %q", logLine)
		}
		if !strings.Contains(
			logLine,
			"keys(config="+strconv.Itoa(len(defaultSpec.ConfigMapData))+" secret="+strconv.Itoa(len(defaultSpec.SecretData))+")",
		) {
			t.Fatalf("默认键数量日志缺失: %q", logLine)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildK8sConfigMapSecretLog(func(key string) string {
			switch key {
			case "K8S_CONFIGMAP_SECRET_NAMESPACE":
				return "finance"
			case "K8S_CONFIGMAP_NAME":
				return "ledgerbot-config-prod"
			case "K8S_SECRET_NAME":
				return "ledgerbot-secret-prod"
			case "K8S_CONFIGMAP_KEYS":
				return "BOT_TIMEZONE,LOG_LEVEL"
			case "K8S_SECRET_KEYS":
				return "BOT_TOKEN,ADMIN_TOKEN"
			case "BOT_TIMEZONE":
				return "UTC"
			case "LOG_LEVEL":
				return "WARN"
			case "BOT_TOKEN":
				return "token-value"
			case "ADMIN_TOKEN":
				return "admin-token"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "namespace=finance") {
			t.Fatalf("命名空间日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "configmap=ledgerbot-config-prod") {
			t.Fatalf("自定义 configmap 日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "secret=ledgerbot-secret-prod") {
			t.Fatalf("自定义 secret 日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "keys(config=2 secret=2)") {
			t.Fatalf("自定义键数量日志缺失: %q", logLine)
		}
	})

	t.Run("非法 configmap key 触发回退", func(t *testing.T) {
		logLine, err := buildK8sConfigMapSecretLog(func(key string) string {
			if key == "K8S_CONFIGMAP_KEYS" {
				return "BOT_TIMEZONE,BAD-KEY"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("非法 key 应返回告警")
		}
		if !strings.Contains(err.Error(), "K8S_CONFIGMAP_KEYS") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		defaultSpec := system.DefaultK8sConfigMapSecretSpec()
		if !strings.Contains(
			logLine,
			"keys(config="+strconv.Itoa(len(defaultSpec.ConfigMapData))+" secret="+strconv.Itoa(len(defaultSpec.SecretData))+")",
		) {
			t.Fatalf("非法 key 应回退默认键数量: %q", logLine)
		}
	})
}

func TestBuildK8sProbeLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, spec, err := buildK8sProbeLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认配置不应报错: %v", err)
		}
		if spec.BindAddr != system.DefaultK8sProbeBindAddress {
			t.Fatalf("默认探针监听地址错误: want=%q got=%q", system.DefaultK8sProbeBindAddress, spec.BindAddr)
		}
		if !strings.Contains(logLine, "liveness(path=/healthz period=10s timeout=2s failure=3)") {
			t.Fatalf("默认 liveness 日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "readiness(path=/readyz period=5s timeout=2s success=1 failure=3)") {
			t.Fatalf("默认 readiness 日志缺失: %q", logLine)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, spec, err := buildK8sProbeLog(func(key string) string {
			switch key {
			case "K8S_PROBE_NAME":
				return "ledgerbot-probe-prod"
			case "K8S_PROBE_NAMESPACE":
				return "finance"
			case "K8S_PROBE_BIND_ADDR":
				return ":19090"
			case "K8S_LIVENESS_PATH":
				return "/livez"
			case "K8S_LIVENESS_PERIOD_SEC":
				return "7"
			case "K8S_LIVENESS_TIMEOUT_SEC":
				return "3"
			case "K8S_LIVENESS_FAILURE_THRESHOLD":
				return "4"
			case "K8S_READINESS_PATH":
				return "/ready"
			case "K8S_READINESS_PERIOD_SEC":
				return "6"
			case "K8S_READINESS_TIMEOUT_SEC":
				return "3"
			case "K8S_READINESS_SUCCESS_THRESHOLD":
				return "2"
			case "K8S_READINESS_FAILURE_THRESHOLD":
				return "5"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义配置不应报错: %v", err)
		}
		if spec.Name != "ledgerbot-probe-prod" || spec.Namespace != "finance" {
			t.Fatalf("自定义名称/命名空间错误: %+v", spec)
		}
		if spec.BindAddr != ":19090" {
			t.Fatalf("自定义监听地址错误: %+v", spec)
		}
		if !strings.Contains(logLine, "liveness(path=/livez period=7s timeout=3s failure=4)") {
			t.Fatalf("自定义 liveness 日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "readiness(path=/ready period=6s timeout=3s success=2 failure=5)") {
			t.Fatalf("自定义 readiness 日志缺失: %q", logLine)
		}
	})

	t.Run("非法端口触发回退", func(t *testing.T) {
		logLine, spec, err := buildK8sProbeLog(func(key string) string {
			if key == "K8S_PROBE_PORT" {
				return "invalid"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("非法端口应返回告警")
		}
		if !strings.Contains(err.Error(), "K8S_PROBE_PORT") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if spec.Port != system.DefaultK8sProbePort {
			t.Fatalf("非法端口应回退默认值: want=%d got=%d", system.DefaultK8sProbePort, spec.Port)
		}
		if !strings.Contains(logLine, system.DefaultK8sProbeBindAddress) {
			t.Fatalf("非法端口场景应输出默认监听地址: %q", logLine)
		}
	})
}

func TestBuildK8sSidecarLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildK8sSidecarLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "mode=both") {
			t.Fatalf("默认 sidecar 模式日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "inject=[log-collector,proxy]") {
			t.Fatalf("默认 sidecar 注入清单日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "proxy_port=15001") {
			t.Fatalf("默认 sidecar 代理端口日志缺失: %q", logLine)
		}
	})

	t.Run("自定义日志采集 sidecar", func(t *testing.T) {
		logLine, err := buildK8sSidecarLog(func(key string) string {
			switch key {
			case "K8S_SIDECAR_NAME":
				return "ledgerbot-sidecar-prod"
			case "K8S_SIDECAR_NAMESPACE":
				return "finance"
			case "K8S_SIDECAR_MODE":
				return "log-collector"
			case "K8S_SIDECAR_APP_CONTAINER_NAME":
				return "ledgerbot-api"
			case "K8S_SIDECAR_PROXY_PORT":
				return "16001"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "name=ledgerbot-sidecar-prod") {
			t.Fatalf("sidecar 名称日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "namespace=finance") {
			t.Fatalf("sidecar 命名空间日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "mode=log-collector") {
			t.Fatalf("自定义 sidecar 模式日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "inject=[log-collector]") {
			t.Fatalf("sidecar 注入清单日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "app_container=ledgerbot-api") {
			t.Fatalf("sidecar 应用容器日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "proxy_port=16001") {
			t.Fatalf("自定义 sidecar 代理端口日志缺失: %q", logLine)
		}
	})

	t.Run("非法 sidecar 模式触发回退", func(t *testing.T) {
		logLine, err := buildK8sSidecarLog(func(key string) string {
			if key == "K8S_SIDECAR_MODE" {
				return "invalid-mode"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("非法 sidecar 模式应返回告警")
		}
		if !strings.Contains(err.Error(), "K8S_SIDECAR_MODE") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "mode=both") {
			t.Fatalf("非法 sidecar 模式应回退默认值: %q", logLine)
		}
	})
}

func TestBuildK8sCanaryReleaseLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildK8sCanaryReleaseLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "target=ledgerbot") {
			t.Fatalf("默认目标 deployment 日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "replicas(total=2 canary=1 stable=1)") {
			t.Fatalf("默认灰度副本日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "observe=300s") {
			t.Fatalf("默认观察窗口日志缺失: %q", logLine)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildK8sCanaryReleaseLog(func(key string) string {
			switch key {
			case "K8S_CANARY_NAME":
				return "ledgerbot-rollout-prod"
			case "K8S_CANARY_NAMESPACE":
				return "finance"
			case "K8S_CANARY_TARGET_DEPLOYMENT":
				return "ledgerbot-api"
			case "K8S_CANARY_TOTAL_REPLICAS":
				return "30"
			case "K8S_CANARY_PERCENT":
				return "10"
			case "K8S_CANARY_OBSERVATION_SECONDS":
				return "240"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "name=ledgerbot-rollout-prod") {
			t.Fatalf("灰度名称日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "namespace=finance") {
			t.Fatalf("灰度命名空间日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "target=ledgerbot-api") {
			t.Fatalf("灰度目标 deployment 日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "replicas(total=30 canary=3 stable=27)") {
			t.Fatalf("自定义灰度副本日志缺失: %q", logLine)
		}
		if !strings.Contains(logLine, "observe=240s") {
			t.Fatalf("自定义观察窗口日志缺失: %q", logLine)
		}
	})

	t.Run("非法灰度比例触发回退", func(t *testing.T) {
		logLine, err := buildK8sCanaryReleaseLog(func(key string) string {
			if key == "K8S_CANARY_PERCENT" {
				return "invalid"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("非法灰度比例应返回告警")
		}
		if !strings.Contains(err.Error(), "K8S_CANARY_PERCENT") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "replicas(total=2 canary=1 stable=1)") {
			t.Fatalf("非法灰度比例应回退默认值: %q", logLine)
		}
	})
}

func TestBuildK8sServiceMeshLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildK8sServiceMeshLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认 service mesh 配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "K8s Service Mesh 已优化") {
			t.Fatalf("默认日志应提示已优化: %q", logLine)
		}
		if !strings.Contains(logLine, "lb=LEAST_REQUEST") {
			t.Fatalf("默认日志缺少负载均衡策略: %q", logLine)
		}
		if !strings.Contains(logLine, "mTLS=STRICT") {
			t.Fatalf("默认日志缺少 mTLS 策略: %q", logLine)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildK8sServiceMeshLog(func(key string) string {
			switch key {
			case "K8S_SERVICE_MESH_NAME":
				return "ledgerbot-mesh-prod"
			case "K8S_SERVICE_MESH_NAMESPACE":
				return "finance"
			case "K8S_SERVICE_MESH_HOST":
				return "ledgerbot.finance.svc.cluster.local"
			case "K8S_SERVICE_MESH_GATEWAY":
				return "finance-gateway"
			case "K8S_SERVICE_MESH_LB_POLICY":
				return "round-robin"
			case "K8S_SERVICE_MESH_REQUEST_TIMEOUT_SEC":
				return "9"
			case "K8S_SERVICE_MESH_RETRY_ATTEMPTS":
				return "4"
			case "K8S_SERVICE_MESH_RETRY_TIMEOUT_SEC":
				return "3"
			case "K8S_SERVICE_MESH_MAX_CONNECTIONS":
				return "4096"
			case "K8S_SERVICE_MESH_MAX_PENDING_REQUESTS":
				return "512"
			case "K8S_SERVICE_MESH_MTLS_MODE":
				return "permissive"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义 service mesh 配置不应报错: %v", err)
		}
		required := []string{
			"name=ledgerbot-mesh-prod",
			"namespace=finance",
			"host=ledgerbot.finance.svc.cluster.local",
			"gateway=finance-gateway",
			"lb=ROUND_ROBIN",
			"timeout=9s",
			"retries=4",
			"per_try_timeout=3s",
			"mTLS=PERMISSIVE",
			"connections=4096",
			"pending=512",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自定义日志缺少关键字段 %q: %q", fragment, logLine)
			}
		}
	})

	t.Run("非法策略触发回退", func(t *testing.T) {
		logLine, err := buildK8sServiceMeshLog(func(key string) string {
			switch key {
			case "K8S_SERVICE_MESH_LB_POLICY":
				return "invalid-policy"
			case "K8S_SERVICE_MESH_RETRY_ATTEMPTS":
				return "invalid"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法 service mesh 配置应返回告警")
		}
		if !strings.Contains(err.Error(), "K8S_SERVICE_MESH_LB_POLICY") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "lb=LEAST_REQUEST") {
			t.Fatalf("非法策略应回退默认负载均衡: %q", logLine)
		}
		if !strings.Contains(logLine, "retries=3") {
			t.Fatalf("非法重试次数应回退默认值: %q", logLine)
		}
	})
}

func TestBuildCloudCostMonitorLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, spec, err := buildCloudCostMonitorLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认成本监控配置不应报错: %v", err)
		}
		if !spec.Enabled {
			t.Fatalf("默认成本监控应启用")
		}
		if spec.Interval != system.DefaultCloudCostMonitorInterval {
			t.Fatalf(
				"默认采样间隔错误: want=%s got=%s",
				system.DefaultCloudCostMonitorInterval,
				spec.Interval,
			)
		}
		if spec.MonthlyBudget != system.DefaultCloudCostMonitorMonthlyBudget {
			t.Fatalf(
				"默认预算错误: want=%.2f got=%.2f",
				system.DefaultCloudCostMonitorMonthlyBudget,
				spec.MonthlyBudget,
			)
		}
		if !strings.Contains(logLine, "云成本监控已启用") {
			t.Fatalf("默认日志应提示已启用: %q", logLine)
		}
		if !strings.Contains(logLine, "monthly_budget=1000.00USD") {
			t.Fatalf("默认日志缺少预算信息: %q", logLine)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, spec, err := buildCloudCostMonitorLog(func(key string) string {
			switch key {
			case "CLOUD_COST_MONITOR_ENABLED":
				return "true"
			case "CLOUD_COST_MONITOR_INTERVAL":
				return "2m"
			case "CLOUD_COST_MONTHLY_BUDGET":
				return "3200.5"
			case "CLOUD_COST_ALERT_RATIO":
				return "0.7"
			case "CLOUD_COST_GROWTH_RATIO":
				return "0.4"
			case "CLOUD_COST_CURRENCY":
				return "cny"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义成本监控配置不应报错: %v", err)
		}
		if !spec.Enabled {
			t.Fatalf("自定义启用开关解析错误")
		}
		if spec.Interval != 2*time.Minute {
			t.Fatalf("自定义采样间隔解析错误: %s", spec.Interval)
		}
		if spec.MonthlyBudget != 3200.5 {
			t.Fatalf("自定义预算解析错误: %.2f", spec.MonthlyBudget)
		}
		if spec.AlertThresholdRatio != 0.7 || spec.GrowthThresholdRatio != 0.4 {
			t.Fatalf("自定义阈值解析错误: %+v", spec)
		}
		if spec.Currency != "CNY" {
			t.Fatalf("自定义币种解析错误: %s", spec.Currency)
		}
		if !strings.Contains(logLine, "interval=2m0s") {
			t.Fatalf("自定义日志缺少采样间隔: %q", logLine)
		}
		if !strings.Contains(logLine, "monthly_budget=3200.50CNY") {
			t.Fatalf("自定义日志缺少预算信息: %q", logLine)
		}
		if !strings.Contains(logLine, "alert_threshold=70%") {
			t.Fatalf("自定义日志缺少预算阈值: %q", logLine)
		}
		if !strings.Contains(logLine, "growth_threshold=40%") {
			t.Fatalf("自定义日志缺少增长阈值: %q", logLine)
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, spec, err := buildCloudCostMonitorLog(func(key string) string {
			switch key {
			case "CLOUD_COST_MONITOR_INTERVAL":
				return "bad"
			case "CLOUD_COST_MONTHLY_BUDGET":
				return "-10"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法成本监控配置应返回告警")
		}
		if !strings.Contains(err.Error(), "CLOUD_COST_MONITOR_INTERVAL") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if spec.Interval != system.DefaultCloudCostMonitorInterval {
			t.Fatalf("非法间隔应回退默认值: %s", spec.Interval)
		}
		if spec.MonthlyBudget != system.DefaultCloudCostMonitorMonthlyBudget {
			t.Fatalf("非法预算应回退默认值: %.2f", spec.MonthlyBudget)
		}
		if !strings.Contains(logLine, "monthly_budget=1000.00USD") {
			t.Fatalf("非法配置日志应回退默认预算: %q", logLine)
		}
	})

	t.Run("显式关闭", func(t *testing.T) {
		logLine, spec, err := buildCloudCostMonitorLog(func(key string) string {
			if key == "CLOUD_COST_MONITOR_ENABLED" {
				return "false"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("显式关闭配置不应报错: %v", err)
		}
		if spec.Enabled {
			t.Fatalf("显式关闭应生效")
		}
		if !strings.Contains(logLine, "云成本监控未启用") {
			t.Fatalf("关闭日志应提示未启用: %q", logLine)
		}
	})
}

func TestBuildDBTuningLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildDBTuningLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认 DB 调优配置不应报错: %v", err)
		}
		required := []string{
			"PostgreSQL 调优已定义",
			"shared_buffers=" + system.DefaultDBTuningSharedBuffers,
			"work_mem=" + system.DefaultDBTuningWorkMem,
			"ALTER SYSTEM SET shared_buffers",
			"ALTER SYSTEM SET work_mem",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("默认日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildDBTuningLog(func(key string) string {
			switch key {
			case "PG_SHARED_BUFFERS":
				return "2gb"
			case "PG_WORK_MEM":
				return "48mb"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义 DB 调优配置不应报错: %v", err)
		}
		required := []string{
			"shared_buffers=2GB",
			"work_mem=48MB",
			"shared_buffers = '2GB'",
			"work_mem = '48MB'",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自定义日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, err := buildDBTuningLog(func(key string) string {
			switch key {
			case "PG_SHARED_BUFFERS":
				return "1024"
			case "PG_WORK_MEM":
				return "bad"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法 DB 调优配置应返回告警")
		}
		if !strings.Contains(err.Error(), "PG_SHARED_BUFFERS") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "shared_buffers="+system.DefaultDBTuningSharedBuffers) {
			t.Fatalf("非法 shared_buffers 应回退默认值: %s", logLine)
		}
		if !strings.Contains(logLine, "work_mem="+system.DefaultDBTuningWorkMem) {
			t.Fatalf("非法 work_mem 应回退默认值: %s", logLine)
		}
	})
}

func TestBuildPgBouncerLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildPgBouncerLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认 PgBouncer 配置不应报错: %v", err)
		}
		required := []string{
			"PgBouncer 连接池已启用",
			"listen=" + system.DefaultPgBouncerListenAddr + ":" + strconv.Itoa(int(system.DefaultPgBouncerListenPort)),
			"upstream=" + system.DefaultPgBouncerUpstreamHost + ":" + strconv.Itoa(int(system.DefaultPgBouncerUpstreamPort)),
			"mode=" + string(system.DefaultPgBouncerPoolMode),
			"max_client_conn=" + strconv.Itoa(int(system.DefaultPgBouncerMaxClientConn)),
			"pool(default=" + strconv.Itoa(int(system.DefaultPgBouncerDefaultPoolSize)),
			"config=\"[databases]",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("默认日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildPgBouncerLog(func(key string) string {
			switch key {
			case "PGBOUNCER_LISTEN_ADDR":
				return "10.0.0.9"
			case "PGBOUNCER_LISTEN_PORT":
				return "8432"
			case "PGBOUNCER_UPSTREAM_HOST":
				return "postgres.finance.svc.cluster.local"
			case "PGBOUNCER_POOL_MODE":
				return "statement"
			case "PGBOUNCER_DEFAULT_POOL_SIZE":
				return "60"
			case "PGBOUNCER_MIN_POOL_SIZE":
				return "20"
			case "PGBOUNCER_RESERVE_POOL_SIZE":
				return "10"
			case "PGBOUNCER_MAX_CLIENT_CONN":
				return "2000"
			case "PGBOUNCER_AUTH_TYPE":
				return "md5"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义 PgBouncer 配置不应报错: %v", err)
		}
		required := []string{
			"listen=10.0.0.9:8432",
			"upstream=postgres.finance.svc.cluster.local:5432",
			"mode=statement",
			"max_client_conn=2000",
			"pool(default=60 min=20 reserve=10)",
			"auth=md5",
			"pool_mode = statement",
			"default_pool_size = 60",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自定义日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, err := buildPgBouncerLog(func(key string) string {
			switch key {
			case "PGBOUNCER_LISTEN_PORT":
				return "bad"
			case "PGBOUNCER_POOL_MODE":
				return "invalid"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法 PgBouncer 配置应返回告警")
		}
		if !strings.Contains(err.Error(), "PGBOUNCER_LISTEN_PORT") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "listen="+system.DefaultPgBouncerListenAddr+":"+strconv.Itoa(int(system.DefaultPgBouncerListenPort))) {
			t.Fatalf("非法监听端口应回退默认值: %s", logLine)
		}
		if !strings.Contains(logLine, "mode="+string(system.DefaultPgBouncerPoolMode)) {
			t.Fatalf("非法连接池模式应回退默认值: %s", logLine)
		}
	})

	t.Run("显式关闭", func(t *testing.T) {
		logLine, err := buildPgBouncerLog(func(key string) string {
			if key == "PGBOUNCER_ENABLED" {
				return "false"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("显式关闭配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "PgBouncer 连接池未启用") {
			t.Fatalf("关闭日志应提示未启用: %s", logLine)
		}
	})
}

func TestBuildCDNAccelerationLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildCDNAccelerationLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认 CDN 配置不应报错: %v", err)
		}
		required := []string{
			"CDN 加速已启用",
			"cdn=" + system.DefaultCDNAccelerationBaseURL,
			"origin=" + system.DefaultCDNAccelerationOriginBaseURL,
			"image_prefix=" + system.DefaultCDNAccelerationImagePathPrefix,
			"video_prefix=" + system.DefaultCDNAccelerationVideoPathPrefix,
			"cache_max_age=" + strconv.Itoa(int(system.DefaultCDNAccelerationCacheMaxAgeSeconds)) + "s",
			"rewrite=\"location " + system.DefaultCDNAccelerationImagePathPrefix,
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("默认日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildCDNAccelerationLog(func(key string) string {
			switch key {
			case "CDN_ACCEL_ENABLED":
				return "true"
			case "CDN_BASE_URL":
				return "https://cdn.finance.prod.example.com"
			case "CDN_ORIGIN_BASE_URL":
				return "https://assets.finance.prod.example.com"
			case "CDN_IMAGE_PATH_PREFIX":
				return "/assets/images/"
			case "CDN_VIDEO_PATH_PREFIX":
				return "/assets/videos/"
			case "CDN_CACHE_MAX_AGE_SEC":
				return "86400"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义 CDN 配置不应报错: %v", err)
		}
		required := []string{
			"cdn=https://cdn.finance.prod.example.com",
			"origin=https://assets.finance.prod.example.com",
			"image_prefix=/assets/images/",
			"video_prefix=/assets/videos/",
			"cache_max_age=86400s",
			"location /assets/images/",
			"max-age=86400",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自定义日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, err := buildCDNAccelerationLog(func(key string) string {
			switch key {
			case "CDN_BASE_URL":
				return "ftp://cdn.finance.prod.example.com"
			case "CDN_CACHE_MAX_AGE_SEC":
				return "invalid"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法 CDN 配置应返回告警")
		}
		if !strings.Contains(err.Error(), "CDN_BASE_URL") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(err.Error(), "CDN_CACHE_MAX_AGE_SEC") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "cdn="+system.DefaultCDNAccelerationBaseURL) {
			t.Fatalf("非法 CDN 域名应回退默认值: %s", logLine)
		}
		if !strings.Contains(logLine, "cache_max_age="+strconv.Itoa(int(system.DefaultCDNAccelerationCacheMaxAgeSeconds))+"s") {
			t.Fatalf("非法缓存 TTL 应回退默认值: %s", logLine)
		}
	})

	t.Run("显式关闭", func(t *testing.T) {
		logLine, err := buildCDNAccelerationLog(func(key string) string {
			if key == "CDN_ACCEL_ENABLED" {
				return "false"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("显式关闭配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "CDN 加速未启用") {
			t.Fatalf("关闭日志应提示未启用: %s", logLine)
		}
	})
}

func TestBuildRunbookLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildRunbookLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认 runbook 配置不应报错: %v", err)
		}
		required := []string{
			"Runbook(操作手册)已启用",
			"service=" + system.DefaultRunbookServiceName,
			"channel=" + system.DefaultRunbookIncidentChannel,
			"steps=5",
			"ack<=" + strconv.Itoa(int(system.DefaultRunbookAcknowledgeMinutes)) + "min",
			"mitigate<=" + strconv.Itoa(int(system.DefaultRunbookMitigationMinutes)) + "min",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("默认日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildRunbookLog(func(key string) string {
			switch key {
			case "RUNBOOK_ENABLED":
				return "true"
			case "RUNBOOK_SERVICE_NAME":
				return "ledgerbot-prod"
			case "RUNBOOK_INCIDENT_CHANNEL":
				return "#finance-incident"
			case "RUNBOOK_ACK_MINUTES":
				return "3"
			case "RUNBOOK_MITIGATION_MINUTES":
				return "12"
			case "RUNBOOK_STATUS_UPDATE_MINUTES":
				return "6"
			case "RUNBOOK_POSTMORTEM_DEADLINE_HOURS":
				return "36"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义 runbook 配置不应报错: %v", err)
		}
		required := []string{
			"service=ledgerbot-prod",
			"channel=#finance-incident",
			"ack<=3min",
			"mitigate<=12min",
			"status_every=6min",
			"postmortem<=36h",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自定义日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, err := buildRunbookLog(func(key string) string {
			switch key {
			case "RUNBOOK_ACK_MINUTES":
				return "bad"
			case "RUNBOOK_STATUS_UPDATE_MINUTES":
				return "-1"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法 runbook 配置应返回告警")
		}
		if !strings.Contains(err.Error(), "RUNBOOK_ACK_MINUTES") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "ack<=5min") {
			t.Fatalf("非法 ACK 配置应回退默认值: %s", logLine)
		}
		if !strings.Contains(logLine, "status_every=10min") {
			t.Fatalf("非法状态播报配置应回退默认值: %s", logLine)
		}
	})

	t.Run("显式关闭", func(t *testing.T) {
		logLine, err := buildRunbookLog(func(key string) string {
			if key == "RUNBOOK_ENABLED" {
				return "false"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("显式关闭配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "Runbook(操作手册)未启用") {
			t.Fatalf("关闭日志应提示未启用: %s", logLine)
		}
	})
}

func TestBuildOnCallRotationLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildOnCallRotationLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认 on-call 配置不应报错: %v", err)
		}
		required := []string{
			"On-Call 轮值已启用",
			"timezone=" + system.DefaultOnCallRotationTimezone,
			"handoff=00:00",
			"shift=24h",
			"channel=" + system.DefaultOnCallRotationChannel,
			"coverage=24x7",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("默认日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildOnCallRotationLog(func(key string) string {
			switch key {
			case "ONCALL_ROTATION_ENABLED":
				return "true"
			case "ONCALL_TIMEZONE":
				return "UTC"
			case "ONCALL_HANDOFF_HOUR":
				return "8"
			case "ONCALL_ENGINEERS":
				return "ray,mia,zoe"
			case "ONCALL_CHANNEL":
				return "#finance-oncall"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义 on-call 配置不应报错: %v", err)
		}
		required := []string{
			"timezone=UTC",
			"handoff=08:00",
			"engineers=3[ray,mia,zoe]",
			"channel=#finance-oncall",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自定义日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, err := buildOnCallRotationLog(func(key string) string {
			switch key {
			case "ONCALL_HANDOFF_HOUR":
				return "99"
			case "ONCALL_ENGINEERS":
				return "solo"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法 on-call 配置应返回告警")
		}
		if !strings.Contains(err.Error(), "ONCALL_HANDOFF_HOUR") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(err.Error(), "ONCALL_ENGINEERS") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(logLine, "handoff=00:00") {
			t.Fatalf("非法交接班小时应回退默认值: %s", logLine)
		}
		if !strings.Contains(logLine, "engineers=3[alice,bob,carol]") {
			t.Fatalf("非法轮值工程师应回退默认值: %s", logLine)
		}
	})

	t.Run("显式关闭", func(t *testing.T) {
		logLine, err := buildOnCallRotationLog(func(key string) string {
			if key == "ONCALL_ROTATION_ENABLED" {
				return "false"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("显式关闭配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "On-Call 轮值未启用") {
			t.Fatalf("关闭日志应提示未启用: %s", logLine)
		}
	})
}

func TestBuildChaosEngineeringLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, spec, err := buildChaosEngineeringLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认故障演练配置不应报错: %v", err)
		}
		if !spec.Enabled {
			t.Fatalf("默认故障演练应启用")
		}
		required := []string{
			"故障演练已启用",
			"interval=" + system.DefaultChaosEngineeringInterval.String(),
			string(system.ChaosScenarioDBOutage) + "(" + system.DefaultChaosEngineeringDBOutageDuration.String() + ")",
			string(system.ChaosScenarioNetworkInterruption) + "(" + system.DefaultChaosEngineeringNetworkInterruptionDuration.String() + ")",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("默认日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, spec, err := buildChaosEngineeringLog(func(key string) string {
			switch key {
			case "CHAOS_ENGINEERING_ENABLED":
				return "true"
			case "CHAOS_ENGINEERING_INTERVAL":
				return "90m"
			case "CHAOS_DB_OUTAGE_DURATION":
				return "50s"
			case "CHAOS_NETWORK_INTERRUPTION_DURATION":
				return "35s"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义故障演练配置不应报错: %v", err)
		}
		if spec.Interval != 90*time.Minute {
			t.Fatalf("故障演练间隔解析错误: want=%s got=%s", 90*time.Minute, spec.Interval)
		}
		if spec.DBOutageDuration != 50*time.Second {
			t.Fatalf("DB 宕机时长解析错误: want=%s got=%s", 50*time.Second, spec.DBOutageDuration)
		}
		if spec.NetworkInterruptionDuration != 35*time.Second {
			t.Fatalf("网络中断时长解析错误: want=%s got=%s", 35*time.Second, spec.NetworkInterruptionDuration)
		}
		required := []string{"interval=1h30m0s", "db_outage(50s)", "network_interruption(35s)"}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自定义日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, spec, err := buildChaosEngineeringLog(func(key string) string {
			switch key {
			case "CHAOS_ENGINEERING_INTERVAL":
				return "abc"
			case "CHAOS_DB_OUTAGE_DURATION":
				return "-1s"
			case "CHAOS_NETWORK_INTERRUPTION_DURATION":
				return "0s"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法故障演练配置应返回告警")
		}
		if !strings.Contains(err.Error(), "CHAOS_ENGINEERING_INTERVAL") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(err.Error(), "CHAOS_DB_OUTAGE_DURATION") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(err.Error(), "CHAOS_NETWORK_INTERRUPTION_DURATION") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if spec.Interval != system.DefaultChaosEngineeringInterval {
			t.Fatalf("非法间隔应回退默认值: want=%s got=%s", system.DefaultChaosEngineeringInterval, spec.Interval)
		}
		if !strings.Contains(logLine, "interval="+system.DefaultChaosEngineeringInterval.String()) {
			t.Fatalf("日志应回退默认间隔: %s", logLine)
		}
	})

	t.Run("显式关闭", func(t *testing.T) {
		logLine, spec, err := buildChaosEngineeringLog(func(key string) string {
			if key == "CHAOS_ENGINEERING_ENABLED" {
				return "false"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("显式关闭配置不应报错: %v", err)
		}
		if spec.Enabled {
			t.Fatalf("显式关闭后应为禁用状态")
		}
		if !strings.Contains(logLine, "故障演练未启用") {
			t.Fatalf("关闭日志应提示未启用: %s", logLine)
		}
	})
}

func TestBuildAutoRemediationLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, spec, err := buildAutoRemediationLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认自动恢复配置不应报错: %v", err)
		}
		if !spec.Enabled {
			t.Fatalf("默认自动恢复应启用")
		}
		required := []string{
			"自动恢复脚本已启用",
			"timeout=" + system.DefaultAutoRemediationTimeout.String(),
			"cooldown=" + system.DefaultAutoRemediationCooldown.String(),
			string(system.AutoRemediationFaultWebhook404Spike) + "=>" + system.DefaultAutoRemediationWebhookResetScript,
			string(system.AutoRemediationFaultConsumerHeartbeatStall) + "=>" + system.DefaultAutoRemediationQueueRestartScript,
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("默认日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, spec, err := buildAutoRemediationLog(func(key string) string {
			switch key {
			case "AUTO_REMEDIATION_ENABLED":
				return "true"
			case "AUTO_REMEDIATION_TIMEOUT":
				return "45s"
			case "AUTO_REMEDIATION_COOLDOWN":
				return "3m"
			case "AUTO_REMEDIATION_WEBHOOK_RESET_SCRIPT":
				return "/opt/remediation/webhook_reset.sh"
			case "AUTO_REMEDIATION_REDIS_EVICT_SCRIPT":
				return "/opt/remediation/redis_evict.sh"
			case "AUTO_REMEDIATION_DB_TERMINATE_SCRIPT":
				return "/opt/remediation/db_terminate.sh"
			case "AUTO_REMEDIATION_QUEUE_RESTART_SCRIPT":
				return "/opt/remediation/queue_restart.sh"
			case "AUTO_REMEDIATION_ALERT_SILENCE_SCRIPT":
				return "/opt/remediation/alert_silence.sh"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义自动恢复配置不应报错: %v", err)
		}
		if spec.Timeout != 45*time.Second {
			t.Fatalf("自动恢复 timeout 解析错误: want=%s got=%s", 45*time.Second, spec.Timeout)
		}
		if spec.Cooldown != 3*time.Minute {
			t.Fatalf("自动恢复 cooldown 解析错误: want=%s got=%s", 3*time.Minute, spec.Cooldown)
		}
		required := []string{
			"timeout=45s",
			"cooldown=3m0s",
			"webhook_404_spike=>/opt/remediation/webhook_reset.sh",
			"alert_storm=>/opt/remediation/alert_silence.sh",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自定义日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, spec, err := buildAutoRemediationLog(func(key string) string {
			switch key {
			case "AUTO_REMEDIATION_TIMEOUT":
				return "bad"
			case "AUTO_REMEDIATION_COOLDOWN":
				return "-1s"
			case "AUTO_REMEDIATION_REDIS_EVICT_SCRIPT":
				return " "
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法自动恢复配置应返回告警")
		}
		if !strings.Contains(err.Error(), "AUTO_REMEDIATION_TIMEOUT") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(err.Error(), "AUTO_REMEDIATION_COOLDOWN") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if !strings.Contains(err.Error(), "AUTO_REMEDIATION_REDIS_EVICT_SCRIPT") {
			t.Fatalf("错误信息应包含变量名: %v", err)
		}
		if spec.Timeout != system.DefaultAutoRemediationTimeout {
			t.Fatalf("非法 timeout 应回退默认值: want=%s got=%s", system.DefaultAutoRemediationTimeout, spec.Timeout)
		}
		if spec.RedisEvictScript != system.DefaultAutoRemediationRedisEvictScript {
			t.Fatalf(
				"非法 Redis 脚本应回退默认值: want=%s got=%s",
				system.DefaultAutoRemediationRedisEvictScript,
				spec.RedisEvictScript,
			)
		}
		if !strings.Contains(logLine, "cooldown="+system.DefaultAutoRemediationCooldown.String()) {
			t.Fatalf("日志应回退默认 cooldown: %s", logLine)
		}
	})

	t.Run("显式关闭", func(t *testing.T) {
		logLine, spec, err := buildAutoRemediationLog(func(key string) string {
			if key == "AUTO_REMEDIATION_ENABLED" {
				return "false"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("显式关闭配置不应报错: %v", err)
		}
		if spec.Enabled {
			t.Fatalf("显式关闭后应为禁用状态")
		}
		if !strings.Contains(logLine, "自动恢复脚本未启用") {
			t.Fatalf("关闭日志应提示未启用: %s", logLine)
		}
	})
}

func TestBuildPostMortemLog(t *testing.T) {
	t.Run("默认配置", func(t *testing.T) {
		logLine, err := buildPostMortemLog(func(string) string { return "" })
		if err != nil {
			t.Fatalf("默认事后复盘配置不应报错: %v", err)
		}
		required := []string{
			"事后复盘已启用",
			"service=" + system.DefaultPostMortemServiceName,
			"owner=" + system.DefaultPostMortemReviewOwner,
			"channel=" + system.DefaultPostMortemReviewChannel,
			"method=" + system.DefaultPostMortemRCAMethod,
			"deadline<=" + strconv.Itoa(int(system.DefaultPostMortemDeadlineHours)) + "h",
			"min_actions=" + strconv.Itoa(int(system.DefaultPostMortemMinActionItems)),
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("默认日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		logLine, err := buildPostMortemLog(func(key string) string {
			switch key {
			case "POST_MORTEM_ENABLED":
				return "true"
			case "POST_MORTEM_SERVICE_NAME":
				return "ledgerbot-prod"
			case "POST_MORTEM_REVIEW_OWNER":
				return "sre-lead"
			case "POST_MORTEM_REVIEW_CHANNEL":
				return "#finance-incident"
			case "POST_MORTEM_RCA_METHOD":
				return "fishbone"
			case "POST_MORTEM_DEADLINE_HOURS":
				return "36"
			case "POST_MORTEM_MIN_ACTION_ITEMS":
				return "5"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("自定义事后复盘配置不应报错: %v", err)
		}
		required := []string{
			"service=ledgerbot-prod",
			"owner=sre-lead",
			"channel=#finance-incident",
			"method=fishbone",
			"deadline<=36h",
			"min_actions=5",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自定义日志缺少关键字段 %q: %s", fragment, logLine)
			}
		}
	})

	t.Run("非法配置触发回退", func(t *testing.T) {
		logLine, err := buildPostMortemLog(func(key string) string {
			switch key {
			case "POST_MORTEM_ENABLED":
				return "unknown"
			case "POST_MORTEM_RCA_METHOD":
				return "bad"
			case "POST_MORTEM_DEADLINE_HOURS":
				return "oops"
			case "POST_MORTEM_MIN_ACTION_ITEMS":
				return "0"
			default:
				return ""
			}
		})
		if err == nil {
			t.Fatalf("非法事后复盘配置应返回告警")
		}
		requiredErr := []string{
			"POST_MORTEM_ENABLED",
			"POST_MORTEM_RCA_METHOD",
			"POST_MORTEM_DEADLINE_HOURS",
			"POST_MORTEM_MIN_ACTION_ITEMS",
		}
		for _, fragment := range requiredErr {
			if !strings.Contains(err.Error(), fragment) {
				t.Fatalf("错误信息应包含变量名 %q: %v", fragment, err)
			}
		}
		if !strings.Contains(logLine, "method="+system.DefaultPostMortemRCAMethod) {
			t.Fatalf("非法方法配置应回退默认值: %s", logLine)
		}
		if !strings.Contains(logLine, "deadline<="+strconv.Itoa(int(system.DefaultPostMortemDeadlineHours))+"h") {
			t.Fatalf("非法时限配置应回退默认值: %s", logLine)
		}
		if !strings.Contains(logLine, "min_actions="+strconv.Itoa(int(system.DefaultPostMortemMinActionItems))) {
			t.Fatalf("非法行动项配置应回退默认值: %s", logLine)
		}
	})

	t.Run("显式关闭", func(t *testing.T) {
		logLine, err := buildPostMortemLog(func(key string) string {
			if key == "POST_MORTEM_ENABLED" {
				return "false"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("显式关闭配置不应报错: %v", err)
		}
		if !strings.Contains(logLine, "事后复盘未启用") {
			t.Fatalf("关闭日志应提示未启用: %s", logLine)
		}
	})
}

func TestDurationUntilNextLocalMidnight(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 2, 18, 23, 59, 50, 0, loc)
	wait := durationUntilNextLocalMidnight(now, loc)
	if wait != 10*time.Second {
		t.Fatalf("距下个零点等待时长错误: %s", wait)
	}
}

func TestDailySettlementWindowForYesterday(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 2, 19, 0, 3, 0, 0, loc)
	reportDate, periodStart, periodEnd := dailySettlementWindowForYesterday(now, loc)
	if reportDate != "2026-02-18" {
		t.Fatalf("报表日期错误: %s", reportDate)
	}
	if got := periodStart.Format(time.RFC3339); got != "2026-02-17T16:00:00Z" {
		t.Fatalf("报表起始时间错误: %s", got)
	}
	if got := periodEnd.Format(time.RFC3339); got != "2026-02-18T16:00:00Z" {
		t.Fatalf("报表结束时间错误: %s", got)
	}
}

func TestRunDayCutSettlementBatchOnce(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "main-day-cut-settlement.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -10087003
	if _, err := database.EnsureGroup(chatID, "日切结算群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"bookkeeping_enabled": 1,
		"day_cut_time":        "00:00:00",
	}); err != nil {
		t.Fatalf("设置日切时间失败: %v", err)
	}

	apply, err := database.ApplyLedgerEntry(db.LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        87003,
		UserID:           87003,
		Username:         "daycut",
		Nickname:         "DayCut",
		EntryType:        "deposit",
		Amount:           100,
		Currency:         "CNY",
		BaseAmount:       100,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      98,
		Note:             "day cut settlement sample",
		TrackMessageLink: "tg://msg/87003",
	})
	if err != nil {
		t.Fatalf("写入跑批样本失败: %v", err)
	}
	if apply.Duplicate || apply.EntryID == 0 {
		t.Fatalf("写入跑批样本结果异常: %+v", apply)
	}

	runAt := time.Now().UTC().Add(24 * time.Hour)
	result, err := runDayCutSettlementBatchOnce(database, runAt, "UTC")
	if err != nil {
		t.Fatalf("执行日切结算跑批失败: %v", err)
	}
	if result.GeneratedReports != 1 {
		t.Fatalf("跑批结果异常: %+v", result)
	}

	expectedReportDate := time.Date(runAt.Year(), runAt.Month(), runAt.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1).Format("2006-01-02")
	report, found, err := database.GetDayCutSettlementReport(chatID, expectedReportDate)
	if err != nil {
		t.Fatalf("读取日切结算单失败: %v", err)
	}
	if !found {
		t.Fatalf("应生成日切结算单")
	}
	if math.Abs(report.DepositFeeTotal-2) >= 1e-9 || math.Abs(report.PayoutFeeTotal-0) >= 1e-9 || math.Abs(report.NetFeeTotal-2) >= 1e-9 {
		t.Fatalf("日切结算费用统计错误: %+v", report)
	}
}

func TestRunDayCutSettlementLoopStartupCatchup(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "main-day-cut-settlement-loop.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -10087004
	if _, err := database.EnsureGroup(chatID, "日切补跑群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"bookkeeping_enabled": 1,
		"day_cut_time":        "00:00:00",
	}); err != nil {
		t.Fatalf("设置日切时间失败: %v", err)
	}

	apply, err := database.ApplyLedgerEntry(db.LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        87004,
		UserID:           87004,
		Username:         "daycut-catchup",
		Nickname:         "DayCutCatchup",
		EntryType:        "deposit",
		Amount:           88,
		Currency:         "CNY",
		BaseAmount:       88,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      86.24,
		Note:             "startup day cut catchup sample",
		TrackMessageLink: "tg://msg/87004",
	})
	if err != nil {
		t.Fatalf("写入补跑样本失败: %v", err)
	}
	if apply.Duplicate || apply.EntryID == 0 {
		t.Fatalf("写入补跑样本结果异常: %+v", apply)
	}

	runAt := time.Now().UTC().Add(24 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	settled := make(chan struct{}, 1)
	loopStopped := make(chan struct{})
	go func() {
		defer close(loopStopped)
		runDayCutSettlementLoop(
			ctx,
			"UTC",
			database,
			func() time.Time { return runAt },
			func(format string, args ...any) {
				if strings.Contains(format, "日切结算单生成完成") {
					select {
					case settled <- struct{}{}:
					default:
					}
				}
			},
		)
	}()

	select {
	case <-settled:
	case <-time.After(2 * time.Second):
		t.Fatalf("runDayCutSettlementLoop 启动后应立即触发一次补跑")
	}
	cancel()
	select {
	case <-loopStopped:
	case <-time.After(2 * time.Second):
		t.Fatalf("runDayCutSettlementLoop 未在取消后退出")
	}

	expectedReportDate := time.Date(runAt.Year(), runAt.Month(), runAt.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1).Format("2006-01-02")
	report, found, err := database.GetDayCutSettlementReport(chatID, expectedReportDate)
	if err != nil {
		t.Fatalf("读取补跑日切结算单失败: %v", err)
	}
	if !found {
		t.Fatalf("启动补跑后应生成日切结算单")
	}
	if report.TotalEntries != 1 || report.DepositCount != 1 || report.PayoutCount != 0 {
		t.Fatalf("补跑日切结算单统计错误: %+v", report)
	}
}

func TestRunDailySettlementBatchOnce(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "main-daily-settlement.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -10087001
	if _, err := database.EnsureGroup(chatID, "批处理结算群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"bookkeeping_enabled": 1}); err != nil {
		t.Fatalf("开启群记账失败: %v", err)
	}

	apply, err := database.ApplyLedgerEntry(db.LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        87001,
		UserID:           87001,
		Username:         "batch",
		Nickname:         "Batch",
		EntryType:        "deposit",
		Amount:           200,
		Currency:         "CNY",
		BaseAmount:       200,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      200,
		Note:             "batch settlement sample",
		TrackMessageLink: "tg://msg/87001",
	})
	if err != nil {
		t.Fatalf("写入跑批样本失败: %v", err)
	}
	if apply.Duplicate || apply.EntryID == 0 {
		t.Fatalf("写入跑批样本结果异常: %+v", apply)
	}

	loc := time.UTC
	now := time.Now().UTC().Add(24 * time.Hour)
	result, err := runDailySettlementBatchOnce(database, now, loc)
	if err != nil {
		t.Fatalf("执行凌晨跑批失败: %v", err)
	}
	if result.GeneratedReports != 1 {
		t.Fatalf("跑批结果异常: %+v", result)
	}
}

func TestRunDailySettlementLoopStartupCatchup(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "main-daily-settlement-loop.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -10087002
	if _, err := database.EnsureGroup(chatID, "补跑结算群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"bookkeeping_enabled": 1}); err != nil {
		t.Fatalf("开启群记账失败: %v", err)
	}
	if _, err := database.ApplyLedgerEntry(db.LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        87002,
		UserID:           87002,
		Username:         "catchup",
		Nickname:         "Catchup",
		EntryType:        "deposit",
		Amount:           88,
		Currency:         "CNY",
		BaseAmount:       88,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      88,
		Note:             "startup catchup sample",
		TrackMessageLink: "tg://msg/87002",
	}); err != nil {
		t.Fatalf("写入补跑样本失败: %v", err)
	}

	nowBase := time.Now().UTC()
	runAt := nowBase.Add(24 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	settled := make(chan struct{}, 1)
	loopStopped := make(chan struct{})
	go func() {
		defer close(loopStopped)
		runDailySettlementLoop(
			ctx,
			"UTC",
			database,
			func() time.Time { return runAt },
			func(format string, args ...any) {
				if strings.Contains(format, "T+1 结算报表跑批完成") {
					select {
					case settled <- struct{}{}:
					default:
					}
				}
			},
		)
	}()

	select {
	case <-settled:
	case <-time.After(2 * time.Second):
		t.Fatalf("runDailySettlementLoop 启动后应立即触发一次补跑")
	}
	cancel()
	select {
	case <-loopStopped:
	case <-time.After(2 * time.Second):
		t.Fatalf("runDailySettlementLoop 未在取消后退出")
	}

	reportDate := nowBase.Format("2006-01-02")
	report, found, err := database.GetDailySettlementReport(chatID, reportDate)
	if err != nil {
		t.Fatalf("读取补跑报表失败: %v", err)
	}
	if !found {
		t.Fatalf("启动补跑后应生成昨日结算报表")
	}
	if report.TotalEntries != 1 || report.DepositCount != 1 || report.PayoutCount != 0 {
		t.Fatalf("补跑报表统计错误: %+v", report)
	}
}

func TestDurationUntilNextLocalSchedule(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 2, 19, 0, 4, 50, 0, loc)
	wait := durationUntilNextLocalSchedule(now, loc, 0, 5)
	if wait != 10*time.Second {
		t.Fatalf("距下个定时点等待时长错误: %s", wait)
	}

	afterSchedule := time.Date(2026, 2, 19, 0, 5, 0, 0, loc)
	nextWait := durationUntilNextLocalSchedule(afterSchedule, loc, 0, 5)
	if nextWait != 24*time.Hour {
		t.Fatalf("到次日定时点等待时长错误: %s", nextWait)
	}
}

func TestResolveDuplicateAlertAutoClearCycle(t *testing.T) {
	loc := time.UTC
	lead := 5 * time.Minute
	postWindow := time.Minute

	cycleAt, phase, ok := resolveDuplicateAlertAutoClearCycle(
		time.Date(2026, 2, 19, 3, 56, 0, 0, loc),
		"04:00:00",
		lead,
		postWindow,
		loc,
	)
	if !ok || phase != "pre" || !cycleAt.Equal(time.Date(2026, 2, 19, 4, 0, 0, 0, loc)) {
		t.Fatalf("日切前窗口判定错误: ok=%v phase=%s cycle_at=%s", ok, phase, cycleAt.Format(time.RFC3339))
	}

	cycleAt, phase, ok = resolveDuplicateAlertAutoClearCycle(
		time.Date(2026, 2, 19, 4, 0, 30, 0, loc),
		"04:00:00",
		lead,
		postWindow,
		loc,
	)
	if !ok || phase != "post" || !cycleAt.Equal(time.Date(2026, 2, 19, 4, 0, 0, 0, loc)) {
		t.Fatalf("日切后窗口判定错误: ok=%v phase=%s cycle_at=%s", ok, phase, cycleAt.Format(time.RFC3339))
	}

	_, _, ok = resolveDuplicateAlertAutoClearCycle(
		time.Date(2026, 2, 19, 4, 2, 0, 0, loc),
		"04:00:00",
		lead,
		postWindow,
		loc,
	)
	if ok {
		t.Fatalf("窗口外不应触发自动清理")
	}

	cycleAt, phase, ok = resolveDuplicateAlertAutoClearCycle(
		time.Date(2026, 2, 19, 23, 58, 0, 0, loc),
		"00:00:00",
		lead,
		postWindow,
		loc,
	)
	if !ok || phase != "pre" || !cycleAt.Equal(time.Date(2026, 2, 20, 0, 0, 0, 0, loc)) {
		t.Fatalf("跨天窗口判定错误: ok=%v phase=%s cycle_at=%s", ok, phase, cycleAt.Format(time.RFC3339))
	}
}

func TestRunDuplicateAlertAutoClearOnce(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "main-duplicate-alert-auto-clear.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -10087005
	if _, err := database.EnsureGroup(chatID, "重复预警自动清理测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"duplicate_alert_enabled": 1,
		"day_cut_time":            "04:00:00",
	}); err != nil {
		t.Fatalf("更新群配置失败: %v", err)
	}

	if _, err := database.RecordCardSeen(chatID, "622233445566778899", 1); err != nil {
		t.Fatalf("写入卡号缓存失败: %v", err)
	}
	if count, _, err := database.RecordTextDuplicateAccount(chatID, 870501, "110288285406"); err != nil {
		t.Fatalf("写入纯文本重复记录失败: %v", err)
	} else if count != 1 {
		t.Fatalf("首条纯文本重复记录计数异常: %d", count)
	}

	clearedCycles := make(map[int64]string, 1)
	runDuplicateAlertAutoClearOnce(
		database,
		time.Date(2026, 2, 19, 3, 56, 0, 0, time.UTC),
		"UTC",
		5*time.Minute,
		time.Minute,
		clearedCycles,
		nil,
	)

	if count, err := database.RecordCardSeen(chatID, "622233445566778899", 2); err != nil {
		t.Fatalf("自动清理后写入卡号缓存失败: %v", err)
	} else if count != 1 {
		t.Fatalf("自动清理后卡号计数应重置为 1，实际=%d", count)
	}
	if count, _, err := database.RecordTextDuplicateAccount(chatID, 870502, "110288285406"); err != nil {
		t.Fatalf("自动清理后写入纯文本重复记录失败: %v", err)
	} else if count != 1 {
		t.Fatalf("自动清理后纯文本计数应重置为 1，实际=%d", count)
	}

	runDuplicateAlertAutoClearOnce(
		database,
		time.Date(2026, 2, 19, 3, 56, 30, 0, time.UTC),
		"UTC",
		5*time.Minute,
		time.Minute,
		clearedCycles,
		nil,
	)
	if count, _, err := database.RecordTextDuplicateAccount(chatID, 870503, "110288285406"); err != nil {
		t.Fatalf("同阶段重复运行后写入纯文本记录失败: %v", err)
	} else if count != 2 {
		t.Fatalf("同阶段应跳过重复清理，计数应为 2，实际=%d", count)
	}

	runDuplicateAlertAutoClearOnce(
		database,
		time.Date(2026, 2, 19, 4, 0, 10, 0, time.UTC),
		"UTC",
		5*time.Minute,
		time.Minute,
		clearedCycles,
		nil,
	)
	if count, _, err := database.RecordTextDuplicateAccount(chatID, 870504, "110288285406"); err != nil {
		t.Fatalf("日切后补清理后写入纯文本记录失败: %v", err)
	} else if count != 1 {
		t.Fatalf("日切后补清理应重置计数，实际=%d", count)
	}
}

func TestRunScheduledReconciliationOnce(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "main-scheduled-reconciliation.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100451901
	if _, err := database.EnsureGroup(chatID, "主流程对账群"); err != nil {
		t.Fatalf("初始化对账群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"bookkeeping_enabled": 1}); err != nil {
		t.Fatalf("开启对账群记账失败: %v", err)
	}
	if _, err := database.ApplyLedgerEntry(db.LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        451901,
		UserID:           451901,
		Username:         "main-recon",
		Nickname:         "MainRecon",
		EntryType:        "deposit",
		Amount:           30,
		Currency:         "USDT",
		BaseAmount:       30,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      30,
		Note:             "main scheduled reconciliation",
		TrackMessageLink: "tg://msg/451901",
	}); err != nil {
		t.Fatalf("写入对账样本失败: %v", err)
	}

	baseResult, err := runScheduledReconciliationOnce(database, db.DefaultScheduledReconciliationTolerance)
	if err != nil {
		t.Fatalf("执行基线对账失败: %v", err)
	}
	if baseResult.CheckedChats != 1 {
		t.Fatalf("对账覆盖群数量错误: %+v", baseResult)
	}
	if baseResult.DifferenceCount != 0 || len(baseResult.Differences) != 0 {
		t.Fatalf("基线数据应无差异: %+v", baseResult)
	}
}

func TestRunScheduledReconciliationLoopStartupCatchup(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "main-scheduled-reconciliation-loop.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100451902
	if _, err := database.EnsureGroup(chatID, "主流程对账补跑群"); err != nil {
		t.Fatalf("初始化对账补跑群失败: %v", err)
	}
	if _, err := database.ApplyLedgerEntry(db.LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        451902,
		UserID:           451902,
		Username:         "main-recon",
		Nickname:         "MainRecon",
		EntryType:        "deposit",
		Amount:           15,
		Currency:         "USDT",
		BaseAmount:       15,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      15,
		Note:             "main scheduled reconciliation loop",
		TrackMessageLink: "tg://msg/451902",
	}); err != nil {
		t.Fatalf("写入对账补跑样本失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reconciled := make(chan struct{}, 1)
	loopStopped := make(chan struct{})
	go func() {
		defer close(loopStopped)
		runScheduledReconciliationLoop(
			ctx,
			"UTC",
			database,
			func() time.Time {
				return time.Date(2026, 2, 19, 0, 6, 0, 0, time.UTC)
			},
			func(format string, args ...any) {
				if strings.Contains(format, "定时对账完成") {
					select {
					case reconciled <- struct{}{}:
					default:
					}
				}
			},
		)
	}()

	select {
	case <-reconciled:
	case <-time.After(2 * time.Second):
		t.Fatalf("runScheduledReconciliationLoop 启动后应立即触发一次补跑")
	}
	cancel()
	select {
	case <-loopStopped:
	case <-time.After(2 * time.Second):
		t.Fatalf("runScheduledReconciliationLoop 未在取消后退出")
	}
}

func TestBuildDailyFullBackupSourcePaths(t *testing.T) {
	cfg := config.Config{
		DBMasterPath:    filepath.FromSlash("data/bot_go.db"),
		ColdArchivePath: filepath.FromSlash("data/cold_archive.db"),
		DBTenantShards:  3,
	}
	got := buildDailyFullBackupSourcePaths(cfg)
	want := []string{
		filepath.Clean(filepath.FromSlash("data/bot_go.db")),
		filepath.Clean(filepath.FromSlash("data/bot_go.tenant_01.db")),
		filepath.Clean(filepath.FromSlash("data/bot_go.tenant_02.db")),
		filepath.Clean(filepath.FromSlash("data/cold_archive.db")),
		filepath.Clean(filepath.FromSlash("data/cold_archive.tenant_01.db")),
		filepath.Clean(filepath.FromSlash("data/cold_archive.tenant_02.db")),
	}
	if len(got) != len(want) {
		t.Fatalf("全量备份数据源数量错误: want=%d got=%d values=%v", len(want), len(got), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("全量备份数据源路径错误: index=%d want=%s got=%s", index, want[index], got[index])
		}
	}
}

func TestDefaultDailyFullBackupDir(t *testing.T) {
	if got := defaultDailyFullBackupDir(filepath.FromSlash("data/bot_go.db")); got != filepath.Clean(filepath.FromSlash("data/full_backups")) {
		t.Fatalf("默认全量备份目录错误: %s", got)
	}
	if got := defaultDailyFullBackupDir(""); got != filepath.Clean(filepath.FromSlash("data/full_backups")) {
		t.Fatalf("空主库路径时默认备份目录错误: %s", got)
	}
}

func TestBuildCrossRegionDRSourcePaths(t *testing.T) {
	baseDir := t.TempDir()
	walSourceDir := filepath.Join(baseDir, "wal_source")
	walArchiveDir := filepath.Join(baseDir, "wal_archive")
	if err := os.MkdirAll(walSourceDir, 0o755); err != nil {
		t.Fatalf("创建 WAL 源目录失败: %v", err)
	}
	walArchiver, err := system.NewWALArchiver(system.WALArchiverOptions{
		SourceDir:  walSourceDir,
		ArchiveDir: walArchiveDir,
	})
	if err != nil {
		t.Fatalf("初始化 WAL 归档器失败: %v", err)
	}

	dailyFullBackup, err := system.NewDailyFullBackup(system.DailyFullBackupOptions{
		SourcePaths: []string{filepath.Join(baseDir, "ledger.db")},
		BackupDir:   filepath.Join(baseDir, "full_backups"),
		Timezone:    "UTC",
	})
	if err != nil {
		t.Fatalf("初始化全量备份器失败: %v", err)
	}

	got := buildCrossRegionDRSourcePaths(walArchiver, dailyFullBackup)
	want := []string{
		filepath.Clean(walArchiveDir),
		filepath.Clean(dailyFullBackup.BackupDir()),
	}
	if len(got) != len(want) {
		t.Fatalf("跨区域容灾数据源数量错误: want=%d got=%d values=%v", len(want), len(got), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("跨区域容灾数据源错误: index=%d want=%s got=%s", index, want[index], got[index])
		}
	}
}

func TestDefaultCrossRegionDRTargetDir(t *testing.T) {
	if got := defaultCrossRegionDRTargetDir(filepath.FromSlash("data/bot_go.db")); got != filepath.Clean(filepath.FromSlash("data/cross_region_dr")) {
		t.Fatalf("默认跨区域容灾目录错误: %s", got)
	}
	if got := defaultCrossRegionDRTargetDir(""); got != filepath.Clean(filepath.FromSlash("data/cross_region_dr")) {
		t.Fatalf("空主库路径时默认跨区域容灾目录错误: %s", got)
	}
}
