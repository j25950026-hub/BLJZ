package system

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultGrafanaDashboardUID           = "ledgerbot-core-overview"
	DefaultGrafanaDashboardTitle         = "LedgerBot 核心指标大屏"
	DefaultGrafanaDashboardDatasource    = "Prometheus"
	DefaultGrafanaDashboardRefresh       = "30s"
	DefaultGrafanaDashboardPanelCount    = 6
	defaultGrafanaDashboardSchemaVersion = 39
	defaultGrafanaDashboardVersion       = 1
	defaultGrafanaDashboardTimeFrom      = "now-6h"
	defaultGrafanaDashboardTimeTo        = "now"
	defaultGrafanaDashboardTimezone      = "browser"
)

var (
	defaultGrafanaDashboardTags           = []string{"ledgerbot", "observability", "automation"}
	grafanaDashboardUIDPattern            = regexp.MustCompile(`^[A-Za-z0-9_-]{3,40}$`)
	prometheusMetricsNamespaceNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type GrafanaDashboardSpec struct {
	UID              string
	Title            string
	Datasource       string
	MetricsNamespace string
	Refresh          string
	Tags             []string
}

type GrafanaDashboardEnvelope struct {
	Dashboard GrafanaDashboard `json:"dashboard"`
	Overwrite bool             `json:"overwrite"`
}

type GrafanaDashboard struct {
	UID           string                  `json:"uid"`
	Title         string                  `json:"title"`
	Tags          []string                `json:"tags"`
	Timezone      string                  `json:"timezone"`
	SchemaVersion int                     `json:"schemaVersion"`
	Version       int                     `json:"version"`
	Refresh       string                  `json:"refresh"`
	Time          GrafanaDashboardTime    `json:"time"`
	Panels        []GrafanaDashboardPanel `json:"panels"`
}

type GrafanaDashboardTime struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type GrafanaDashboardPanel struct {
	ID          int                     `json:"id"`
	Title       string                  `json:"title"`
	Type        string                  `json:"type"`
	Description string                  `json:"description,omitempty"`
	Datasource  string                  `json:"datasource"`
	GridPos     GrafanaDashboardGridPos `json:"gridPos"`
	Targets     []GrafanaDashboardQuery `json:"targets"`
	FieldConfig map[string]any          `json:"fieldConfig,omitempty"`
}

type GrafanaDashboardGridPos struct {
	H int `json:"h"`
	W int `json:"w"`
	X int `json:"x"`
	Y int `json:"y"`
}

type GrafanaDashboardQuery struct {
	RefID        string `json:"refId"`
	Expr         string `json:"expr"`
	LegendFormat string `json:"legendFormat,omitempty"`
}

func DefaultGrafanaDashboardSpec() GrafanaDashboardSpec {
	return GrafanaDashboardSpec{
		UID:              DefaultGrafanaDashboardUID,
		Title:            DefaultGrafanaDashboardTitle,
		Datasource:       DefaultGrafanaDashboardDatasource,
		MetricsNamespace: DefaultPrometheusMetricsNamespace,
		Refresh:          DefaultGrafanaDashboardRefresh,
		Tags:             append([]string(nil), defaultGrafanaDashboardTags...),
	}
}

func NormalizeGrafanaDashboardSpec(spec GrafanaDashboardSpec) (GrafanaDashboardSpec, error) {
	normalized := DefaultGrafanaDashboardSpec()

	if uid := strings.TrimSpace(spec.UID); uid != "" {
		normalized.UID = uid
	}
	if title := strings.TrimSpace(spec.Title); title != "" {
		normalized.Title = title
	}
	if datasource := strings.TrimSpace(spec.Datasource); datasource != "" {
		normalized.Datasource = datasource
	}
	if namespace := strings.TrimSpace(spec.MetricsNamespace); namespace != "" {
		normalized.MetricsNamespace = namespace
	}
	if refresh := strings.TrimSpace(spec.Refresh); refresh != "" {
		normalized.Refresh = refresh
	}
	if tags := normalizeGrafanaDashboardTags(spec.Tags); len(tags) > 0 {
		normalized.Tags = tags
	}

	if !grafanaDashboardUIDPattern.MatchString(normalized.UID) {
		return GrafanaDashboardSpec{}, fmt.Errorf("dashboard uid must match pattern %s", grafanaDashboardUIDPattern.String())
	}
	if strings.TrimSpace(normalized.Title) == "" {
		return GrafanaDashboardSpec{}, fmt.Errorf("dashboard title must not be empty")
	}
	if strings.TrimSpace(normalized.Datasource) == "" {
		return GrafanaDashboardSpec{}, fmt.Errorf("dashboard datasource must not be empty")
	}
	if !prometheusMetricsNamespaceNamePattern.MatchString(normalized.MetricsNamespace) {
		return GrafanaDashboardSpec{}, fmt.Errorf(
			"metrics namespace must match pattern %s",
			prometheusMetricsNamespaceNamePattern.String(),
		)
	}

	refreshDuration, err := time.ParseDuration(normalized.Refresh)
	if err != nil || refreshDuration <= 0 {
		return GrafanaDashboardSpec{}, fmt.Errorf("dashboard refresh must be a valid positive duration")
	}
	if len(normalized.Tags) == 0 {
		normalized.Tags = append([]string(nil), defaultGrafanaDashboardTags...)
	}

	return normalized, nil
}

func LoadGrafanaDashboardSpecFromEnv(getenv func(string) string) (GrafanaDashboardSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := GrafanaDashboardSpec{
		UID:              strings.TrimSpace(getenv("GRAFANA_DASHBOARD_UID")),
		Title:            strings.TrimSpace(getenv("GRAFANA_DASHBOARD_TITLE")),
		Datasource:       strings.TrimSpace(getenv("GRAFANA_DASHBOARD_DATASOURCE")),
		MetricsNamespace: strings.TrimSpace(getenv("GRAFANA_DASHBOARD_NAMESPACE")),
		Refresh:          strings.TrimSpace(getenv("GRAFANA_DASHBOARD_REFRESH")),
		Tags:             parseGrafanaDashboardTags(getenv("GRAFANA_DASHBOARD_TAGS")),
	}

	normalized, err := NormalizeGrafanaDashboardSpec(spec)
	if err != nil {
		fallback, fallbackErr := NormalizeGrafanaDashboardSpec(GrafanaDashboardSpec{})
		if fallbackErr != nil {
			return GrafanaDashboardSpec{}, fmt.Errorf("normalize grafana dashboard defaults failed: %w", fallbackErr)
		}
		return fallback, fmt.Errorf("grafana dashboard config invalid, fallback to defaults: %w", err)
	}
	return normalized, nil
}

func BuildGrafanaDashboard(spec GrafanaDashboardSpec) (GrafanaDashboardEnvelope, error) {
	normalized, err := NormalizeGrafanaDashboardSpec(spec)
	if err != nil {
		return GrafanaDashboardEnvelope{}, err
	}

	requestsTotalMetric := fmt.Sprintf("%s_update_requests_total", normalized.MetricsNamespace)
	latencyBucketMetric := fmt.Sprintf("%s_update_latency_seconds_bucket", normalized.MetricsNamespace)
	errorsTotalMetric := fmt.Sprintf("%s_update_errors_total", normalized.MetricsNamespace)
	goroutinesMetric := fmt.Sprintf("%s_goroutines", normalized.MetricsNamespace)

	panels := []GrafanaDashboardPanel{
		buildGrafanaDashboardPanel(
			1,
			"TPS (1m)",
			"stat",
			"业务吞吐核心指标，展示机器人每秒处理请求数。",
			normalized.Datasource,
			GrafanaDashboardGridPos{H: 8, W: 6, X: 0, Y: 0},
			fmt.Sprintf("sum(rate(%s[1m]))", requestsTotalMetric),
			"",
			"reqps",
		),
		buildGrafanaDashboardPanel(
			2,
			"错误率 (5m)",
			"stat",
			"业务稳定性核心指标，展示失败请求占比。",
			normalized.Datasource,
			GrafanaDashboardGridPos{H: 8, W: 6, X: 6, Y: 0},
			fmt.Sprintf(
				"sum(rate(%s[5m])) / clamp_min(sum(rate(%s[5m])), 1)",
				errorsTotalMetric,
				requestsTotalMetric,
			),
			"",
			"percentunit",
		),
		buildGrafanaDashboardPanel(
			3,
			"P95 延迟 (5m)",
			"stat",
			"业务时延核心指标，展示 P95 请求处理延迟。",
			normalized.Datasource,
			GrafanaDashboardGridPos{H: 8, W: 6, X: 12, Y: 0},
			fmt.Sprintf(
				"histogram_quantile(0.95, sum(rate(%s[5m])) by (le))",
				latencyBucketMetric,
			),
			"",
			"s",
		),
		buildGrafanaDashboardPanel(
			4,
			"Goroutines",
			"stat",
			"系统运行核心指标，展示当前 goroutine 数量。",
			normalized.Datasource,
			GrafanaDashboardGridPos{H: 8, W: 6, X: 18, Y: 0},
			goroutinesMetric,
			"",
			"short",
		),
		buildGrafanaDashboardPanel(
			5,
			"请求来源吞吐 (5m)",
			"timeseries",
			"按请求来源拆解吞吐趋势，用于识别流量结构变化。",
			normalized.Datasource,
			GrafanaDashboardGridPos{H: 8, W: 12, X: 0, Y: 8},
			fmt.Sprintf("sum by (source) (rate(%s[5m]))", requestsTotalMetric),
			"{{source}}",
			"reqps",
		),
		buildGrafanaDashboardPanel(
			6,
			"错误来源增量 (5m)",
			"bargauge",
			"按来源展示最近 5 分钟错误增量，用于快速定位异常入口。",
			normalized.Datasource,
			GrafanaDashboardGridPos{H: 8, W: 12, X: 12, Y: 8},
			fmt.Sprintf("sum by (source) (increase(%s[5m]))", errorsTotalMetric),
			"{{source}}",
			"short",
		),
	}

	return GrafanaDashboardEnvelope{
		Dashboard: GrafanaDashboard{
			UID:           normalized.UID,
			Title:         normalized.Title,
			Tags:          append([]string(nil), normalized.Tags...),
			Timezone:      defaultGrafanaDashboardTimezone,
			SchemaVersion: defaultGrafanaDashboardSchemaVersion,
			Version:       defaultGrafanaDashboardVersion,
			Refresh:       normalized.Refresh,
			Time: GrafanaDashboardTime{
				From: defaultGrafanaDashboardTimeFrom,
				To:   defaultGrafanaDashboardTimeTo,
			},
			Panels: panels,
		},
		Overwrite: true,
	}, nil
}

func BuildGrafanaDashboardJSON(spec GrafanaDashboardSpec) (string, error) {
	dashboard, err := BuildGrafanaDashboard(spec)
	if err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(dashboard, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal grafana dashboard failed: %w", err)
	}
	return string(payload), nil
}

func BuildGrafanaDashboardLog(spec GrafanaDashboardSpec) (string, error) {
	normalized, err := NormalizeGrafanaDashboardSpec(spec)
	if err != nil {
		return "", err
	}
	dashboard, err := BuildGrafanaDashboard(normalized)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"Grafana Dashboard 已定义：title=%s uid=%s datasource=%s namespace=%s refresh=%s panels=%d metrics=tps,p95_latency,error_rate,goroutines",
		normalized.Title,
		normalized.UID,
		normalized.Datasource,
		normalized.MetricsNamespace,
		normalized.Refresh,
		len(dashboard.Dashboard.Panels),
	), nil
}

func buildGrafanaDashboardPanel(
	id int,
	title string,
	panelType string,
	description string,
	datasource string,
	gridPos GrafanaDashboardGridPos,
	expr string,
	legendFormat string,
	unit string,
) GrafanaDashboardPanel {
	panel := GrafanaDashboardPanel{
		ID:          id,
		Title:       title,
		Type:        panelType,
		Description: description,
		Datasource:  datasource,
		GridPos:     gridPos,
		Targets: []GrafanaDashboardQuery{
			{
				RefID:        "A",
				Expr:         expr,
				LegendFormat: legendFormat,
			},
		},
	}
	if strings.TrimSpace(unit) != "" {
		panel.FieldConfig = map[string]any{
			"defaults": map[string]any{
				"unit": unit,
			},
			"overrides": []any{},
		}
	}
	return panel
}

func parseGrafanaDashboardTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		if normalized := strings.TrimSpace(part); normalized != "" {
			tags = append(tags, normalized)
		}
	}
	return tags
}

func normalizeGrafanaDashboardTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		cleaned := strings.TrimSpace(tag)
		if cleaned == "" {
			continue
		}
		key := strings.ToLower(cleaned)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	return normalized
}
