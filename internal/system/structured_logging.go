package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	DefaultStructuredLoggingServiceName = "ledgerbot"
	DefaultStructuredLoggingEnvironment = "prod"
	DefaultStructuredLoggingTraceID     = "00000000000000000000000000000000"
)

var (
	structuredLoggingTraceIDPattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	structuredLoggingTraceIDFindPattern = regexp.MustCompile(
		`(?i)(?:trace[_-]?id|traceid)\s*(?:=|:)\s*"?([0-9a-f]{32})"?`,
	)
)

type StructuredLoggingSpec struct {
	ServiceName    string
	Environment    string
	DefaultTraceID string
}

type StructuredLogWriterOptions struct {
	Output io.Writer
	Spec   StructuredLoggingSpec
	Now    func() time.Time
}

type structuredLogEntry struct {
	Timestamp   string `json:"timestamp"`
	Level       string `json:"level"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	TraceID     string `json:"trace_id"`
	Message     string `json:"message"`
}

type StructuredLogWriter struct {
	output io.Writer
	spec   StructuredLoggingSpec
	now    func() time.Time

	mu      sync.Mutex
	pending []byte
}

func DefaultStructuredLoggingSpec() StructuredLoggingSpec {
	return StructuredLoggingSpec{
		ServiceName:    DefaultStructuredLoggingServiceName,
		Environment:    DefaultStructuredLoggingEnvironment,
		DefaultTraceID: DefaultStructuredLoggingTraceID,
	}
}

func LoadStructuredLoggingSpecFromEnv(getenv func(string) string) (StructuredLoggingSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultStructuredLoggingSpec()
	warnings := make([]string, 0, 1)

	if serviceName := strings.TrimSpace(getenv("STRUCTURED_LOG_SERVICE")); serviceName != "" {
		spec.ServiceName = serviceName
	}
	if environment := strings.TrimSpace(getenv("STRUCTURED_LOG_ENV")); environment != "" {
		spec.Environment = environment
	}
	if traceIDRaw := strings.TrimSpace(getenv("STRUCTURED_LOG_TRACE_ID")); traceIDRaw != "" {
		if normalizedTraceID, ok := normalizeStructuredLoggingTraceID(traceIDRaw); ok {
			spec.DefaultTraceID = normalizedTraceID
		} else {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"STRUCTURED_LOG_TRACE_ID=%q 无效，回退默认 TraceID=%s",
					traceIDRaw,
					DefaultStructuredLoggingTraceID,
				),
			)
		}
	}

	normalized := normalizeStructuredLoggingSpec(spec)
	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func NewStructuredLogWriter(opts StructuredLogWriterOptions) (*StructuredLogWriter, error) {
	if opts.Output == nil {
		return nil, fmt.Errorf("structured log output is nil")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &StructuredLogWriter{
		output: opts.Output,
		spec:   normalizeStructuredLoggingSpec(opts.Spec),
		now:    now,
	}, nil
}

func BuildStructuredLoggingLog(spec StructuredLoggingSpec) string {
	normalized := normalizeStructuredLoggingSpec(spec)
	return fmt.Sprintf(
		"结构化日志已启用：format=json service=%s env=%s trace_id=%s",
		normalized.ServiceName,
		normalized.Environment,
		normalized.DefaultTraceID,
	)
}

func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return DefaultStructuredLoggingTraceID
	}
	spanContext := oteltrace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return DefaultStructuredLoggingTraceID
	}
	traceID := spanContext.TraceID()
	if !traceID.IsValid() {
		return DefaultStructuredLoggingTraceID
	}
	return strings.ToLower(traceID.String())
}

func (w *StructuredLogWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	if len(p) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.pending = append(w.pending, p...)
	for {
		newlineIndex := bytes.IndexByte(w.pending, '\n')
		if newlineIndex < 0 {
			break
		}
		line := string(w.pending[:newlineIndex])
		w.pending = w.pending[newlineIndex+1:]
		line = strings.TrimRight(line, "\r")
		if err := w.writeLine(line); err != nil {
			return 0, err
		}
	}

	return len(p), nil
}

func (w *StructuredLogWriter) writeLine(message string) error {
	if w == nil || w.output == nil {
		return nil
	}
	if strings.TrimSpace(message) == "" {
		return nil
	}

	entry := structuredLogEntry{
		Timestamp:   w.now().UTC().Format(time.RFC3339Nano),
		Level:       "INFO",
		Service:     w.spec.ServiceName,
		Environment: w.spec.Environment,
		TraceID:     w.traceIDFromMessage(message),
		Message:     message,
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	_, err = w.output.Write(payload)
	return err
}

func (w *StructuredLogWriter) traceIDFromMessage(message string) string {
	if traceID, ok := extractStructuredLoggingTraceID(message); ok {
		return traceID
	}
	return w.spec.DefaultTraceID
}

func extractStructuredLoggingTraceID(message string) (string, bool) {
	if strings.TrimSpace(message) == "" {
		return "", false
	}
	matches := structuredLoggingTraceIDFindPattern.FindStringSubmatch(message)
	if len(matches) < 2 {
		return "", false
	}
	return normalizeStructuredLoggingTraceID(matches[1])
}

func normalizeStructuredLoggingSpec(spec StructuredLoggingSpec) StructuredLoggingSpec {
	normalized := DefaultStructuredLoggingSpec()

	if serviceName := strings.TrimSpace(spec.ServiceName); serviceName != "" {
		normalized.ServiceName = serviceName
	}
	if environment := strings.TrimSpace(spec.Environment); environment != "" {
		normalized.Environment = environment
	}
	if traceID, ok := normalizeStructuredLoggingTraceID(spec.DefaultTraceID); ok {
		normalized.DefaultTraceID = traceID
	}

	return normalized
}

func normalizeStructuredLoggingTraceID(raw string) (string, bool) {
	traceID := strings.ToLower(strings.TrimSpace(raw))
	if !structuredLoggingTraceIDPattern.MatchString(traceID) {
		return "", false
	}
	return traceID, true
}
