package system

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	DefaultPrometheusMetricsNamespace = "ledgerbot"
	DefaultPrometheusMetricsPath      = "/metrics"
)

var defaultPrometheusLatencyBuckets = []float64{
	0.005,
	0.01,
	0.025,
	0.05,
	0.1,
	0.25,
	0.5,
	1,
	2.5,
	5,
}

type PrometheusMetricsOptions struct {
	Namespace      string
	Path           string
	LatencyBuckets []float64
}

type PrometheusMetrics struct {
	path                        string
	registry                    *prometheus.Registry
	handler                     http.Handler
	updateTotal                 *prometheus.CounterVec
	updateLatency               *prometheus.HistogramVec
	updateErrors                *prometheus.CounterVec
	goroutineGauge              prometheus.GaugeFunc
	outboundQueueDepth          prometheus.Gauge
	outboundQueueCapacity       prometheus.Gauge
	outboundQueueWorkers        prometheus.Gauge
	outboundQueueSubmissions    *prometheus.CounterVec
	outboundDispatchFallback    *prometheus.CounterVec
	outboundQueueWait           *prometheus.HistogramVec
	outboundTaskDuration        *prometheus.HistogramVec
	outboundSendTotal           *prometheus.CounterVec
	telegramRetryAfter          *prometheus.HistogramVec
	outboundDeadLetters         *prometheus.CounterVec
	ledgerReceiptOutboxTotal    *prometheus.CounterVec
	ledgerReceiptOutboxDelivery *prometheus.HistogramVec
}

func NewPrometheusMetrics(opts PrometheusMetricsOptions) (*PrometheusMetrics, error) {
	normalized := normalizePrometheusMetricsOptions(opts)

	updateTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: normalized.Namespace,
			Name:      "update_requests_total",
			Help:      "Total number of processed bot updates.",
		},
		[]string{"source", "result"},
	)
	updateLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: normalized.Namespace,
			Name:      "update_latency_seconds",
			Help:      "Latency distribution of processed bot updates.",
			Buckets:   normalized.LatencyBuckets,
		},
		[]string{"source"},
	)
	updateErrors := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: normalized.Namespace,
			Name:      "update_errors_total",
			Help:      "Total number of failed bot updates.",
		},
		[]string{"source"},
	)
	goroutineGauge := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: normalized.Namespace,
			Name:      "goroutines",
			Help:      "Current number of goroutines.",
		},
		func() float64 {
			return float64(runtime.NumGoroutine())
		},
	)
	outboundQueueDepth := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: normalized.Namespace,
			Name:      "outbound_queue_depth",
			Help:      "Current number of queued outbound tasks.",
		},
	)
	outboundQueueCapacity := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: normalized.Namespace,
			Name:      "outbound_queue_capacity",
			Help:      "Configured outbound queue capacity.",
		},
	)
	outboundQueueWorkers := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: normalized.Namespace,
			Name:      "outbound_queue_workers",
			Help:      "Configured outbound queue worker count.",
		},
	)
	outboundQueueSubmissions := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: normalized.Namespace,
			Name:      "outbound_queue_submissions_total",
			Help:      "Total number of outbound queue submission outcomes.",
		},
		[]string{"priority", "result"},
	)
	outboundDispatchFallback := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: normalized.Namespace,
			Name:      "outbound_dispatch_fallback_total",
			Help:      "Total number of business fallback outcomes after outbound queue rejection.",
		},
		[]string{"priority", "result"},
	)
	outboundQueueWait := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: normalized.Namespace,
			Name:      "outbound_queue_wait_seconds",
			Help:      "Time outbound tasks spend waiting in the priority queue.",
			Buckets:   normalized.LatencyBuckets,
		},
		[]string{"priority"},
	)
	outboundTaskDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: normalized.Namespace,
			Name:      "outbound_task_duration_seconds",
			Help:      "Outbound task execution duration.",
			Buckets:   normalized.LatencyBuckets,
		},
		[]string{"priority", "result"},
	)
	outboundSendTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: normalized.Namespace,
			Name:      "outbound_send_total",
			Help:      "Total number of Telegram outbound send/request outcomes.",
		},
		[]string{"operation", "priority", "result"},
	)
	telegramRetryAfter := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: normalized.Namespace,
			Name:      "telegram_retry_after_seconds",
			Help:      "Telegram 429 retry_after delay reported by the Bot API.",
			Buckets:   normalized.LatencyBuckets,
		},
		[]string{"operation", "priority"},
	)
	outboundDeadLetters := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: normalized.Namespace,
			Name:      "outbound_dead_letters_total",
			Help:      "Total number of outbound messages written to dead letter storage.",
		},
		[]string{"operation", "priority"},
	)
	ledgerReceiptOutboxTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: normalized.Namespace,
			Name:      "ledger_receipt_outbox_total",
			Help:      "Total number of ledger receipt outbox delivery outcomes.",
		},
		[]string{"result"},
	)
	ledgerReceiptOutboxDelivery := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: normalized.Namespace,
			Name:      "ledger_receipt_outbox_delivery_seconds",
			Help:      "Ledger receipt outbox delivery duration.",
			Buckets:   normalized.LatencyBuckets,
		},
		[]string{"result"},
	)

	registry := prometheus.NewRegistry()
	collectors := []prometheus.Collector{
		updateTotal,
		updateLatency,
		updateErrors,
		goroutineGauge,
		outboundQueueDepth,
		outboundQueueCapacity,
		outboundQueueWorkers,
		outboundQueueSubmissions,
		outboundDispatchFallback,
		outboundQueueWait,
		outboundTaskDuration,
		outboundSendTotal,
		telegramRetryAfter,
		outboundDeadLetters,
		ledgerReceiptOutboxTotal,
		ledgerReceiptOutboxDelivery,
	}
	for _, collector := range collectors {
		if err := registry.Register(collector); err != nil {
			return nil, fmt.Errorf("register prometheus collector failed: %w", err)
		}
	}

	return &PrometheusMetrics{
		path:                        normalized.Path,
		registry:                    registry,
		handler:                     promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		updateTotal:                 updateTotal,
		updateLatency:               updateLatency,
		updateErrors:                updateErrors,
		goroutineGauge:              goroutineGauge,
		outboundQueueDepth:          outboundQueueDepth,
		outboundQueueCapacity:       outboundQueueCapacity,
		outboundQueueWorkers:        outboundQueueWorkers,
		outboundQueueSubmissions:    outboundQueueSubmissions,
		outboundDispatchFallback:    outboundDispatchFallback,
		outboundQueueWait:           outboundQueueWait,
		outboundTaskDuration:        outboundTaskDuration,
		outboundSendTotal:           outboundSendTotal,
		telegramRetryAfter:          telegramRetryAfter,
		outboundDeadLetters:         outboundDeadLetters,
		ledgerReceiptOutboxTotal:    ledgerReceiptOutboxTotal,
		ledgerReceiptOutboxDelivery: ledgerReceiptOutboxDelivery,
	}, nil
}

func normalizePrometheusMetricsOptions(opts PrometheusMetricsOptions) PrometheusMetricsOptions {
	normalized := opts
	normalized.Namespace = strings.TrimSpace(normalized.Namespace)
	if normalized.Namespace == "" {
		normalized.Namespace = DefaultPrometheusMetricsNamespace
	}
	normalized.Path = ensureHTTPPath(strings.TrimSpace(normalized.Path))
	if normalized.Path == "/" {
		normalized.Path = DefaultPrometheusMetricsPath
	}
	if len(normalized.LatencyBuckets) == 0 {
		normalized.LatencyBuckets = append([]float64(nil), defaultPrometheusLatencyBuckets...)
	}
	return normalized
}

func (m *PrometheusMetrics) Path() string {
	if m == nil {
		return DefaultPrometheusMetricsPath
	}
	return m.path
}

func (m *PrometheusMetrics) Handler() http.Handler {
	if m == nil || m.handler == nil {
		return http.NotFoundHandler()
	}
	return m.handler
}

func (m *PrometheusMetrics) ObserveUpdate(source string, duration time.Duration, success bool) {
	if m == nil {
		return
	}
	normalizedSource := normalizeMetricsSource(source)
	if duration < 0 {
		duration = 0
	}

	result := "success"
	if !success {
		result = "error"
	}

	m.updateTotal.WithLabelValues(normalizedSource, result).Inc()
	m.updateLatency.WithLabelValues(normalizedSource).Observe(duration.Seconds())
	if !success {
		m.updateErrors.WithLabelValues(normalizedSource).Inc()
	}
}

func (m *PrometheusMetrics) ObserveOutboundQueue(queued, capacity, workers int) {
	if m == nil {
		return
	}
	if queued < 0 {
		queued = 0
	}
	if capacity < 0 {
		capacity = 0
	}
	if workers < 0 {
		workers = 0
	}
	m.outboundQueueDepth.Set(float64(queued))
	m.outboundQueueCapacity.Set(float64(capacity))
	m.outboundQueueWorkers.Set(float64(workers))
}

func (m *PrometheusMetrics) ObserveOutboundQueueSubmission(priority, result string) {
	if m == nil {
		return
	}
	m.outboundQueueSubmissions.WithLabelValues(
		normalizeOutboundPriorityLabel(priority),
		normalizeOutboundResultLabel(result),
	).Inc()
}

func (m *PrometheusMetrics) ObserveOutboundDispatchFallback(priority, result string) {
	if m == nil {
		return
	}
	m.outboundDispatchFallback.WithLabelValues(
		normalizeOutboundPriorityLabel(priority),
		normalizeOutboundResultLabel(result),
	).Inc()
}

func (m *PrometheusMetrics) ObserveOutboundQueueWait(priority string, duration time.Duration) {
	if m == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	m.outboundQueueWait.WithLabelValues(normalizeOutboundPriorityLabel(priority)).Observe(duration.Seconds())
}

func (m *PrometheusMetrics) ObserveOutboundTask(priority, result string, duration time.Duration) {
	if m == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	m.outboundTaskDuration.WithLabelValues(
		normalizeOutboundPriorityLabel(priority),
		normalizeOutboundResultLabel(result),
	).Observe(duration.Seconds())
}

func (m *PrometheusMetrics) ObserveOutboundSend(operation, priority, result string) {
	if m == nil {
		return
	}
	m.outboundSendTotal.WithLabelValues(
		normalizeOutboundOperationLabel(operation),
		normalizeOutboundPriorityLabel(priority),
		normalizeOutboundResultLabel(result),
	).Inc()
}

func (m *PrometheusMetrics) ObserveTelegramRetryAfter(operation, priority string, duration time.Duration) {
	if m == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	m.telegramRetryAfter.WithLabelValues(
		normalizeOutboundOperationLabel(operation),
		normalizeOutboundPriorityLabel(priority),
	).Observe(duration.Seconds())
}

func (m *PrometheusMetrics) ObserveOutboundDeadLetter(operation, priority string) {
	if m == nil {
		return
	}
	m.outboundDeadLetters.WithLabelValues(
		normalizeOutboundOperationLabel(operation),
		normalizeOutboundPriorityLabel(priority),
	).Inc()
}

func (m *PrometheusMetrics) ObserveLedgerReceiptOutbox(result string, duration time.Duration) {
	if m == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	normalizedResult := normalizeOutboundResultLabel(result)
	m.ledgerReceiptOutboxTotal.WithLabelValues(normalizedResult).Inc()
	m.ledgerReceiptOutboxDelivery.WithLabelValues(normalizedResult).Observe(duration.Seconds())
}

func normalizeMetricsSource(source string) string {
	normalized := strings.TrimSpace(strings.ToLower(source))
	if normalized == "" {
		return "unknown"
	}
	return normalized
}

func normalizeOutboundPriorityLabel(priority string) string {
	switch normalizeMetricToken(priority) {
	case "bookkeeping_receipt", "error_alert", "normal_chat", "broadcast_ad":
		return normalizeMetricToken(priority)
	default:
		return "unknown"
	}
}

func normalizeOutboundOperationLabel(operation string) string {
	switch normalizeMetricToken(operation) {
	case "send", "request":
		return normalizeMetricToken(operation)
	default:
		return "other"
	}
}

func normalizeOutboundResultLabel(result string) string {
	switch normalizeMetricToken(result) {
	case "accepted",
		"dropped",
		"dropped_backpressure",
		"fallback_backpressure",
		"fallback_closed",
		"rejected",
		"success",
		"error",
		"canceled",
		"rate_limited",
		"retry",
		"failed",
		"stale",
		"sent":
		return normalizeMetricToken(result)
	default:
		return "unknown"
	}
}

func normalizeMetricToken(raw string) string {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
	if normalized == "" {
		return "unknown"
	}
	return normalized
}
