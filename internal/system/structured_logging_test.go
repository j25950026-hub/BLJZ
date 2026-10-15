package system

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestControlPoint521StructuredLogging(t *testing.T) {
	t.Run("控制点 5.2.1 功能验证", func(t *testing.T) {
		const (
			defaultTraceID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			requestTraceID = "1234567890abcdef1234567890abcdef"
		)
		fixedNow := time.Date(2026, time.February, 20, 12, 34, 56, 789000000, time.UTC)

		var sink bytes.Buffer
		writer, err := NewStructuredLogWriter(StructuredLogWriterOptions{
			Output: &sink,
			Spec: StructuredLoggingSpec{
				ServiceName:    "cp521",
				Environment:    "test",
				DefaultTraceID: defaultTraceID,
			},
			Now: func() time.Time { return fixedNow },
		})
		if err != nil {
			t.Fatalf("创建结构化日志 writer 失败: %v", err)
		}

		logger := log.New(writer, "", 0)
		logger.Printf("结构化日志验证：event=boot trace_id=%s", requestTraceID)
		logger.Print("结构化日志验证：event=heartbeat")

		lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("日志行数错误，want=2 got=%d body=%q", len(lines), sink.String())
		}

		first := mustDecodeStructuredLogLine(t, lines[0])
		if first.Level != "INFO" {
			t.Fatalf("日志级别错误: %s", first.Level)
		}
		if first.Service != "cp521" || first.Environment != "test" {
			t.Fatalf("日志元信息错误: %+v", first)
		}
		if first.TraceID != requestTraceID {
			t.Fatalf("请求 TraceID 未透传，want=%s got=%s", requestTraceID, first.TraceID)
		}
		if first.Timestamp != fixedNow.Format(time.RFC3339Nano) {
			t.Fatalf("时间戳错误，want=%s got=%s", fixedNow.Format(time.RFC3339Nano), first.Timestamp)
		}

		second := mustDecodeStructuredLogLine(t, lines[1])
		if second.TraceID != defaultTraceID {
			t.Fatalf("默认 TraceID 错误，want=%s got=%s", defaultTraceID, second.TraceID)
		}
		if second.Message != "结构化日志验证：event=heartbeat" {
			t.Fatalf("日志消息错误: %s", second.Message)
		}
	})
}

func TestTraceIDFromContext(t *testing.T) {
	if got := TraceIDFromContext(nil); got != DefaultStructuredLoggingTraceID {
		t.Fatalf("nil context 应回退默认 TraceID: %s", got)
	}
	if got := TraceIDFromContext(context.Background()); got != DefaultStructuredLoggingTraceID {
		t.Fatalf("空 context 应回退默认 TraceID: %s", got)
	}

	traceID, err := oteltrace.TraceIDFromHex("89abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatalf("构造 trace id 失败: %v", err)
	}
	spanContext := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     oteltrace.SpanID{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	ctx := oteltrace.ContextWithSpanContext(context.Background(), spanContext)
	if got := TraceIDFromContext(ctx); got != traceID.String() {
		t.Fatalf("context TraceID 提取错误，want=%s got=%s", traceID.String(), got)
	}
}

func TestLoadStructuredLoggingSpecFromEnv(t *testing.T) {
	spec, err := LoadStructuredLoggingSpecFromEnv(func(key string) string {
		switch key {
		case "STRUCTURED_LOG_SERVICE":
			return "ledgerbot-cp"
		case "STRUCTURED_LOG_ENV":
			return "staging"
		case "STRUCTURED_LOG_TRACE_ID":
			return "invalid-trace-id"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法 trace id 应返回告警错误")
	}
	if spec.ServiceName != "ledgerbot-cp" || spec.Environment != "staging" {
		t.Fatalf("环境变量配置未生效: %+v", spec)
	}
	if spec.DefaultTraceID != DefaultStructuredLoggingTraceID {
		t.Fatalf("非法 trace id 应回退默认值: %s", spec.DefaultTraceID)
	}
}

type structuredLogLine struct {
	Timestamp   string `json:"timestamp"`
	Level       string `json:"level"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	TraceID     string `json:"trace_id"`
	Message     string `json:"message"`
}

func mustDecodeStructuredLogLine(t *testing.T, line string) structuredLogLine {
	t.Helper()
	var payload structuredLogLine
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("结构化日志 JSON 解析失败: %v line=%q", err, line)
	}
	return payload
}
