package system

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestControlPoint523MetricsMonitoring(t *testing.T) {
	t.Run("控制点 5.2.3 功能验证", func(t *testing.T) {
		metrics, err := NewPrometheusMetrics(PrometheusMetricsOptions{
			Namespace:      "cp523",
			Path:           "/monitor/metrics",
			LatencyBuckets: []float64{0.01, 0.1, 0.5, 1},
		})
		if err != nil {
			t.Fatalf("初始化 prometheus 指标采集器失败: %v", err)
		}

		metrics.ObserveUpdate("message", 35*time.Millisecond, true)
		metrics.ObserveUpdate("message", 220*time.Millisecond, false)
		metrics.ObserveUpdate("callback_query", 80*time.Millisecond, true)

		if got := testutil.ToFloat64(metrics.updateTotal.WithLabelValues("message", "success")); got != 1 {
			t.Fatalf("成功计数错误: want=1 got=%v", got)
		}
		if got := testutil.ToFloat64(metrics.updateTotal.WithLabelValues("message", "error")); got != 1 {
			t.Fatalf("失败计数错误: want=1 got=%v", got)
		}
		if got := testutil.ToFloat64(metrics.updateErrors.WithLabelValues("message")); got != 1 {
			t.Fatalf("错误总数计数错误: want=1 got=%v", got)
		}
		if got := testutil.ToFloat64(metrics.goroutineGauge); got <= 0 {
			t.Fatalf("goroutine 指标应大于 0，got=%v", got)
		}

		probe := NewServiceHealthProbe(ServiceHealthProbeOptions{})
		probe.MarkReady()
		probe.Heartbeat("cp523")
		server, err := NewServiceHealthHTTPServer(ServiceHealthHTTPServerOptions{
			Addr:           ":19080",
			Probe:          probe,
			MetricsPath:    metrics.Path(),
			MetricsHandler: metrics.Handler(),
		})
		if err != nil {
			t.Fatalf("创建探针+指标服务失败: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/monitor/metrics", nil)
		resp := httptest.NewRecorder()
		server.Handler().ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("指标端点应返回 200，got=%d body=%s", resp.Code, resp.Body.String())
		}

		body := resp.Body.String()
		required := []string{
			`cp523_update_requests_total{result="success",source="message"} 1`,
			`cp523_update_requests_total{result="error",source="message"} 1`,
			`cp523_update_latency_seconds_count{source="message"} 2`,
			`cp523_update_errors_total{source="message"} 1`,
			"cp523_goroutines",
		}
		for _, fragment := range required {
			if !strings.Contains(body, fragment) {
				t.Fatalf("指标输出缺少关键片段 %q\nbody=%s", fragment, body)
			}
		}
	})
}

func TestNewPrometheusMetricsDefaults(t *testing.T) {
	metrics, err := NewPrometheusMetrics(PrometheusMetricsOptions{
		Path: "/",
	})
	if err != nil {
		t.Fatalf("默认 prometheus 指标初始化失败: %v", err)
	}
	if metrics.Path() != DefaultPrometheusMetricsPath {
		t.Fatalf("默认 metrics path 错误: want=%s got=%s", DefaultPrometheusMetricsPath, metrics.Path())
	}
	if got := testutil.ToFloat64(metrics.goroutineGauge); got <= 0 {
		t.Fatalf("默认 goroutine 指标应大于 0，got=%v", got)
	}
}

func TestPrometheusOutboundMetrics(t *testing.T) {
	metrics, err := NewPrometheusMetrics(PrometheusMetricsOptions{
		Namespace: "outboundtest",
	})
	if err != nil {
		t.Fatalf("初始化 prometheus 指标失败: %v", err)
	}

	metrics.ObserveOutboundQueue(7, 2048, 4)
	metrics.ObserveOutboundQueueSubmission("bookkeeping_receipt", "accepted")
	metrics.ObserveOutboundDispatchFallback("broadcast_ad", "dropped_backpressure")
	metrics.ObserveOutboundDispatchFallback("error_alert", "fallback_backpressure")
	metrics.ObserveOutboundQueueWait("bookkeeping_receipt", 25*time.Millisecond)
	metrics.ObserveOutboundTask("bookkeeping_receipt", "success", 80*time.Millisecond)
	metrics.ObserveOutboundSend("send", "bookkeeping_receipt", "rate_limited")
	metrics.ObserveOutboundSend("send", "normal_chat", "dropped")
	metrics.ObserveTelegramRetryAfter("send", "bookkeeping_receipt", 3*time.Second)
	metrics.ObserveOutboundDeadLetter("send", "bookkeeping_receipt")
	metrics.ObserveLedgerReceiptOutbox("sent", 120*time.Millisecond)
	metrics.ObserveLedgerReceiptOutbox("stale", 5*time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(resp, req)
	body := resp.Body.String()

	required := []string{
		`outboundtest_outbound_queue_depth 7`,
		`outboundtest_outbound_queue_capacity 2048`,
		`outboundtest_outbound_queue_workers 4`,
		`outboundtest_outbound_queue_submissions_total{priority="bookkeeping_receipt",result="accepted"} 1`,
		`outboundtest_outbound_dispatch_fallback_total{priority="broadcast_ad",result="dropped_backpressure"} 1`,
		`outboundtest_outbound_dispatch_fallback_total{priority="error_alert",result="fallback_backpressure"} 1`,
		`outboundtest_outbound_queue_wait_seconds_count{priority="bookkeeping_receipt"} 1`,
		`outboundtest_outbound_task_duration_seconds_count{priority="bookkeeping_receipt",result="success"} 1`,
		`outboundtest_outbound_send_total{operation="send",priority="bookkeeping_receipt",result="rate_limited"} 1`,
		`outboundtest_outbound_send_total{operation="send",priority="normal_chat",result="dropped"} 1`,
		`outboundtest_telegram_retry_after_seconds_count{operation="send",priority="bookkeeping_receipt"} 1`,
		`outboundtest_outbound_dead_letters_total{operation="send",priority="bookkeeping_receipt"} 1`,
		`outboundtest_ledger_receipt_outbox_total{result="sent"} 1`,
		`outboundtest_ledger_receipt_outbox_delivery_seconds_count{result="sent"} 1`,
		`outboundtest_ledger_receipt_outbox_total{result="stale"} 1`,
		`outboundtest_ledger_receipt_outbox_delivery_seconds_count{result="stale"} 1`,
	}
	for _, fragment := range required {
		if !strings.Contains(body, fragment) {
			t.Fatalf("指标输出缺少关键片段 %q\nbody=%s", fragment, body)
		}
	}

	forbidden := []string{"chat_id", "user_id", "message_id", "error_message"}
	for _, fragment := range forbidden {
		if strings.Contains(body, fragment) {
			t.Fatalf("指标输出不应包含高基数 label %q\nbody=%s", fragment, body)
		}
	}
}
