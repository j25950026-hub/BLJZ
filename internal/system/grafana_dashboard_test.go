package system

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestControlPoint524GrafanaDashboard(t *testing.T) {
	t.Run("控制点 5.2.4 功能验证", func(t *testing.T) {
		spec, err := LoadGrafanaDashboardSpecFromEnv(func(key string) string {
			switch key {
			case "GRAFANA_DASHBOARD_UID":
				return "cp524-core"
			case "GRAFANA_DASHBOARD_TITLE":
				return "控制点 5.2.4 监控大屏"
			case "GRAFANA_DASHBOARD_DATASOURCE":
				return "Prometheus-Core"
			case "GRAFANA_DASHBOARD_NAMESPACE":
				return "cp524"
			case "GRAFANA_DASHBOARD_REFRESH":
				return "15s"
			case "GRAFANA_DASHBOARD_TAGS":
				return "ledgerbot,control-point,cp524"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 grafana dashboard 配置失败: %v", err)
		}
		if spec.MetricsNamespace != "cp524" {
			t.Fatalf("指标命名空间错误: want=cp524 got=%s", spec.MetricsNamespace)
		}

		dashboard, err := BuildGrafanaDashboard(spec)
		if err != nil {
			t.Fatalf("构建 grafana dashboard 失败: %v", err)
		}
		if dashboard.Dashboard.UID != "cp524-core" {
			t.Fatalf("dashboard uid 错误: %s", dashboard.Dashboard.UID)
		}
		if dashboard.Dashboard.Title != "控制点 5.2.4 监控大屏" {
			t.Fatalf("dashboard title 错误: %s", dashboard.Dashboard.Title)
		}
		if got := len(dashboard.Dashboard.Panels); got != DefaultGrafanaDashboardPanelCount {
			t.Fatalf("dashboard panel 数量错误: want=%d got=%d", DefaultGrafanaDashboardPanelCount, got)
		}

		payload, err := BuildGrafanaDashboardJSON(spec)
		if err != nil {
			t.Fatalf("序列化 dashboard json 失败: %v", err)
		}
		required := []string{
			"sum(rate(cp524_update_requests_total[1m]))",
			"sum(rate(cp524_update_errors_total[5m])) / clamp_min(sum(rate(cp524_update_requests_total[5m])), 1)",
			"histogram_quantile(0.95, sum(rate(cp524_update_latency_seconds_bucket[5m])) by (le))",
			"cp524_goroutines",
			"sum by (source) (increase(cp524_update_errors_total[5m]))",
		}
		for _, fragment := range required {
			if !strings.Contains(payload, fragment) {
				t.Fatalf("dashboard promql 缺少关键片段 %q\npayload=%s", fragment, payload)
			}
		}

		var decoded GrafanaDashboardEnvelope
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			t.Fatalf("dashboard json 反序列化失败: %v", err)
		}
		if !decoded.Overwrite {
			t.Fatalf("dashboard import 配置应为 overwrite=true")
		}
		if got := len(decoded.Dashboard.Panels); got != DefaultGrafanaDashboardPanelCount {
			t.Fatalf("dashboard json panel 数量错误: want=%d got=%d", DefaultGrafanaDashboardPanelCount, got)
		}

		logLine, err := BuildGrafanaDashboardLog(spec)
		if err != nil {
			t.Fatalf("生成 dashboard 日志失败: %v", err)
		}
		if !strings.Contains(logLine, "panels=6") {
			t.Fatalf("dashboard 日志应包含 panel 数量: %q", logLine)
		}
	})
}

func TestLoadGrafanaDashboardSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadGrafanaDashboardSpecFromEnv(func(key string) string {
		if key == "GRAFANA_DASHBOARD_NAMESPACE" {
			return "cp-524"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("非法 metrics namespace 应返回回退告警")
	}
	if !strings.Contains(err.Error(), "grafana dashboard config invalid") {
		t.Fatalf("错误信息应包含配置回退提示: %v", err)
	}
	if spec.MetricsNamespace != DefaultPrometheusMetricsNamespace {
		t.Fatalf("非法 namespace 应回退默认值: want=%s got=%s", DefaultPrometheusMetricsNamespace, spec.MetricsNamespace)
	}
}

func TestNormalizeGrafanaDashboardSpecRejectsInvalidRefresh(t *testing.T) {
	_, err := NormalizeGrafanaDashboardSpec(GrafanaDashboardSpec{
		Refresh: "0s",
	})
	if err == nil {
		t.Fatalf("refresh=0s 应被拒绝")
	}
	if !strings.Contains(err.Error(), "refresh") {
		t.Fatalf("错误信息应包含 refresh: %v", err)
	}
}
