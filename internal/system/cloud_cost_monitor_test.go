package system

import (
	"strings"
	"testing"
	"time"
)

func TestControlPoint541CloudCostMonitor(t *testing.T) {
	t.Run("控制点 5.4.1 功能验证", func(t *testing.T) {
		alerts := make([]CloudCostAlert, 0, 2)
		recovered := make([]CloudCostStat, 0, 1)
		monitor := NewCloudCostMonitor(CloudCostMonitorOptions{
			MonthlyBudget:        1000,
			AlertThresholdRatio:  0.8,
			GrowthThresholdRatio: 0.25,
			OnAlert: func(alert CloudCostAlert) {
				alerts = append(alerts, alert)
			},
			OnRecover: func(stat CloudCostStat) {
				recovered = append(recovered, stat)
			},
		})

		monitor.Observe(CloudCostSample{
			Provider:    "aws",
			Service:     "ec2",
			Currency:    "usd",
			Cost:        500,
			CollectedAt: time.Unix(100, 0),
		})
		monitor.scanAndAlert()

		monitor.Observe(CloudCostSample{
			Provider:    "aws",
			Service:     "ec2",
			Currency:    "usd",
			Cost:        820,
			CollectedAt: time.Unix(130, 0),
		})
		monitor.scanAndAlert()

		monitor.Observe(CloudCostSample{
			Provider:    "aws",
			Service:     "ec2",
			Currency:    "usd",
			Cost:        900,
			CollectedAt: time.Unix(160, 0),
		})
		monitor.scanAndAlert()

		monitor.Observe(CloudCostSample{
			Provider:    "aws",
			Service:     "ec2",
			Currency:    "usd",
			Cost:        600,
			CollectedAt: time.Unix(190, 0),
		})
		monitor.scanAndAlert()

		if len(alerts) != 1 {
			t.Fatalf("高成本阶段应只触发一次告警: got=%d", len(alerts))
		}
		alert := alerts[0]
		if alert.Provider != "aws" || alert.Service != "ec2" {
			t.Fatalf("告警维度错误: %+v", alert)
		}
		if !alert.OverBudget || !alert.RapidGrowth {
			t.Fatalf("告警应同时命中预算与增长阈值: %+v", alert)
		}
		if alert.Cost != 820 || alert.PreviousCost != 500 {
			t.Fatalf("告警成本快照错误: %+v", alert)
		}
		if alert.SamplesTotal != 2 {
			t.Fatalf("告警采样计数错误: %d", alert.SamplesTotal)
		}
		if alert.BudgetUsageRatio < 0.819 || alert.BudgetUsageRatio > 0.821 {
			t.Fatalf("预算占用比例计算错误: %.4f", alert.BudgetUsageRatio)
		}
		if alert.GrowthRatioValue < 0.639 || alert.GrowthRatioValue > 0.641 {
			t.Fatalf("增长比例计算错误: %.4f", alert.GrowthRatioValue)
		}

		if len(recovered) != 1 {
			t.Fatalf("成本恢复后应触发一次恢复事件: got=%d", len(recovered))
		}
		recoverStat := recovered[0]
		if recoverStat.Cost != 600 || recoverStat.PreviousCost != 900 {
			t.Fatalf("恢复成本快照错误: %+v", recoverStat)
		}
		if recoverStat.SamplesTotal != 4 {
			t.Fatalf("恢复采样计数错误: %d", recoverStat.SamplesTotal)
		}
	})
}

func TestLoadCloudCostMonitorSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadCloudCostMonitorSpecFromEnv(func(key string) string {
		switch key {
		case "CLOUD_COST_MONITOR_ENABLED":
			return "yes"
		case "CLOUD_COST_MONITOR_INTERVAL":
			return "bad"
		case "CLOUD_COST_MONTHLY_BUDGET":
			return "invalid"
		case "CLOUD_COST_ALERT_RATIO":
			return "1.5"
		case "CLOUD_COST_GROWTH_RATIO":
			return "-1"
		case "CLOUD_COST_CURRENCY":
			return "USDT"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法成本监控配置应返回回退告警")
	}
	if !strings.Contains(err.Error(), "CLOUD_COST_MONTHLY_BUDGET") {
		t.Fatalf("错误信息应包含非法环境变量名: %v", err)
	}
	if !spec.Enabled {
		t.Fatalf("CLOUD_COST_MONITOR_ENABLED=yes 应生效")
	}
	if spec.Interval != DefaultCloudCostMonitorInterval {
		t.Fatalf("非法间隔应回退默认值: %s", spec.Interval)
	}
	if spec.MonthlyBudget != DefaultCloudCostMonitorMonthlyBudget {
		t.Fatalf("非法预算应回退默认值: %.2f", spec.MonthlyBudget)
	}
	if spec.AlertThresholdRatio != DefaultCloudCostMonitorAlertThresholdRatio {
		t.Fatalf("非法预算阈值应回退默认值: %.2f", spec.AlertThresholdRatio)
	}
	if spec.GrowthThresholdRatio != DefaultCloudCostMonitorGrowthThresholdRatio {
		t.Fatalf("非法增长阈值应回退默认值: %.2f", spec.GrowthThresholdRatio)
	}
	if spec.Currency != DefaultCloudCostMonitorCurrency {
		t.Fatalf("非法币种应回退默认值: %s", spec.Currency)
	}
}

func TestDetectCloudCostAlert(t *testing.T) {
	stat := CloudCostStat{
		Provider:     "gcp",
		Service:      "gke",
		Currency:     "USD",
		Cost:         780,
		PreviousCost: 600,
	}

	alert, high := detectCloudCostAlert(stat, 1000, 0.8, 0.2)
	if !high {
		t.Fatalf("快速增长场景应触发告警")
	}
	if alert.OverBudget {
		t.Fatalf("预算占用未超阈值时不应标记 OverBudget: %+v", alert)
	}
	if !alert.RapidGrowth {
		t.Fatalf("增长占比达阈值时应标记 RapidGrowth: %+v", alert)
	}
	if alert.GrowthRatioValue < 0.299 || alert.GrowthRatioValue > 0.301 {
		t.Fatalf("增长比例错误: %.4f", alert.GrowthRatioValue)
	}

	stat = CloudCostStat{
		Provider:     "gcp",
		Service:      "gke",
		Currency:     "USD",
		Cost:         300,
		PreviousCost: 500,
	}
	if _, high := detectCloudCostAlert(stat, 1000, 0.8, 0.2); high {
		t.Fatalf("成本回落场景不应触发告警")
	}
}

func TestBuildCloudCostMonitorLog(t *testing.T) {
	enabled := BuildCloudCostMonitorLog(CloudCostMonitorSpec{
		Enabled:              true,
		Interval:             5 * time.Minute,
		MonthlyBudget:        1500,
		AlertThresholdRatio:  0.75,
		GrowthThresholdRatio: 0.3,
		Currency:             "usd",
	})
	required := []string{
		"云成本监控已启用",
		"interval=5m0s",
		"monthly_budget=1500.00USD",
		"alert_threshold=75%",
		"growth_threshold=30%",
	}
	for _, fragment := range required {
		if !strings.Contains(enabled, fragment) {
			t.Fatalf("启用日志缺少关键字段 %q: %s", fragment, enabled)
		}
	}

	disabled := BuildCloudCostMonitorLog(CloudCostMonitorSpec{
		Enabled: false,
	})
	if !strings.Contains(disabled, "云成本监控未启用") {
		t.Fatalf("关闭日志应提示未启用: %s", disabled)
	}
}
