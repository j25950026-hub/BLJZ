package system

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestControlPoint522DistributedTracing(t *testing.T) {
	t.Run("控制点 5.2.2 功能验证", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		prevProvider := otel.GetTracerProvider()
		prevPropagator := otel.GetTextMapPropagator()
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(
			propagation.NewCompositeTextMapPropagator(
				propagation.TraceContext{},
				propagation.Baggage{},
			),
		)
		t.Cleanup(func() {
			_ = tp.Shutdown(context.Background())
			otel.SetTracerProvider(prevProvider)
			otel.SetTextMapPropagator(prevPropagator)
		})

		var downstreamTraceparent string
		downstreamServer := httptest.NewServer(
			WrapHTTPHandlerWithTracing(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downstreamTraceparent = strings.TrimSpace(r.Header.Get("Traceparent"))
				w.WriteHeader(http.StatusNoContent)
			}), "cp522-downstream"),
		)
		defer downstreamServer.Close()

		upstreamServer := httptest.NewServer(
			WrapHTTPHandlerWithTracing(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				client := WrapHTTPClientWithTracing(&http.Client{Timeout: time.Second}, "cp522-upstream-client")
				req, err := http.NewRequestWithContext(
					r.Context(),
					http.MethodGet,
					downstreamServer.URL+"/service-b",
					nil,
				)
				if err != nil {
					t.Fatalf("构建下游请求失败: %v", err)
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("调用下游服务失败: %v", err)
				}
				_ = resp.Body.Close()
				w.WriteHeader(http.StatusOK)
			}), "cp522-upstream"),
		)
		defer upstreamServer.Close()

		caller := WrapHTTPClientWithTracing(&http.Client{Timeout: time.Second}, "cp522-caller")
		reqCtx, rootSpan := otel.Tracer("cp522-root").Start(context.Background(), "cp522-root-span")
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, upstreamServer.URL+"/service-a", nil)
		if err != nil {
			t.Fatalf("构建入口请求失败: %v", err)
		}
		resp, err := caller.Do(req)
		if err != nil {
			t.Fatalf("调用上游服务失败: %v", err)
		}
		_ = resp.Body.Close()
		rootSpan.End()

		if downstreamTraceparent == "" {
			t.Fatalf("下游请求应携带 traceparent 头")
		}

		spans := recorder.Ended()
		if len(spans) < 5 {
			t.Fatalf("应至少记录 5 个 span，got=%d", len(spans))
		}
		traceID := spans[0].SpanContext().TraceID()
		if !traceID.IsValid() {
			t.Fatalf("trace id 应合法")
		}
		for _, span := range spans {
			if span.SpanContext().TraceID() != traceID {
				t.Fatalf("调用链 span 应属于同一条 trace")
			}
		}

		var upstreamClientSpanID oteltrace.SpanID
		var downstreamServerParentID oteltrace.SpanID
		for _, span := range spans {
			httpTarget, _ := findTracingAttr(span.Attributes(), "http.target")
			if span.SpanKind() == oteltrace.SpanKindClient && httpTarget == "/service-b" {
				upstreamClientSpanID = span.SpanContext().SpanID()
			}
			if span.SpanKind() == oteltrace.SpanKindServer && httpTarget == "/service-b" {
				downstreamServerParentID = span.Parent().SpanID()
			}
		}
		if !upstreamClientSpanID.IsValid() {
			t.Fatalf("应存在上游调用下游服务的 client span")
		}
		if downstreamServerParentID != upstreamClientSpanID {
			t.Fatalf(
				"下游 server span 的父 span 应是上游 client span: parent=%s client=%s",
				downstreamServerParentID,
				upstreamClientSpanID,
			)
		}
	})
}

func TestLoadDistributedTracingSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadDistributedTracingSpecFromEnv(func(key string) string {
		switch key {
		case "TRACING_ENABLED":
			return "enabled"
		case "TRACING_EXPORTER":
			return "zipkin"
		case "TRACING_PROPAGATOR":
			return "invalid"
		case "TRACING_SAMPLING_RATIO":
			return "2"
		case "TRACING_ENDPOINT":
			return "://bad-endpoint"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法 tracing 环境变量应返回回退告警")
	}
	if !spec.Enabled {
		t.Fatalf("TRACING_ENABLED=enabled 应生效")
	}
	if spec.Exporter != DistributedTracingExporterZipkin {
		t.Fatalf("tracing exporter 解析错误: %s", spec.Exporter)
	}
	if spec.Propagator != DistributedTracingPropagatorW3C {
		t.Fatalf("非法 propagator 应回退为默认值")
	}
	if spec.SamplingRatio != DefaultDistributedTracingSamplingRatio {
		t.Fatalf("非法采样率应回退为默认值: %.2f", spec.SamplingRatio)
	}
	if spec.Endpoint != DefaultDistributedTracingZipkinEndpoint {
		t.Fatalf("非法 endpoint 应回退为 zipkin 默认端点: %s", spec.Endpoint)
	}
}

func TestBuildDistributedTracingLog(t *testing.T) {
	logLine := BuildDistributedTracingLog(DistributedTracingSpec{
		Enabled:       true,
		Exporter:      DistributedTracingExporterZipkin,
		ServiceName:   "ledgerbot-observe",
		Endpoint:      "http://127.0.0.1:9411/api/v2/spans",
		SamplingRatio: 0.5,
		Propagator:    DistributedTracingPropagatorB3,
	})
	required := []string{
		"链路追踪已启用",
		"exporter=zipkin",
		"service=ledgerbot-observe",
		"sample_ratio=0.50",
		"propagator=b3",
	}
	for _, fragment := range required {
		if !strings.Contains(logLine, fragment) {
			t.Fatalf("日志应包含 %q，got=%s", fragment, logLine)
		}
	}
}

func findTracingAttr(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, kv := range attrs {
		if string(kv.Key) != key {
			continue
		}
		if kv.Value.Type() == attribute.STRING {
			return kv.Value.AsString(), true
		}
		return kv.Value.Emit(), true
	}
	return "", false
}
