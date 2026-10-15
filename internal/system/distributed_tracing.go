package system

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type DistributedTracingExporter string

const (
	DistributedTracingExporterJaeger DistributedTracingExporter = "jaeger"
	DistributedTracingExporterZipkin DistributedTracingExporter = "zipkin"
)

type DistributedTracingPropagator string

const (
	DistributedTracingPropagatorW3C DistributedTracingPropagator = "w3c"
	DistributedTracingPropagatorB3  DistributedTracingPropagator = "b3"
)

const (
	DefaultDistributedTracingServiceName    = "ledgerbot"
	DefaultDistributedTracingServiceVersion = "dev"
	DefaultDistributedTracingEnvironment    = "prod"
	DefaultDistributedTracingSamplingRatio  = 1.0
	DefaultDistributedTracingJaegerEndpoint = "http://localhost:14268/api/traces"
	DefaultDistributedTracingZipkinEndpoint = "http://localhost:9411/api/v2/spans"
)

type DistributedTracingSpec struct {
	Enabled        bool
	ServiceName    string
	ServiceVersion string
	Environment    string
	Exporter       DistributedTracingExporter
	Endpoint       string
	SamplingRatio  float64
	Propagator     DistributedTracingPropagator
}

type DistributedTracingRuntime struct {
	spec     DistributedTracingSpec
	shutdown func(context.Context) error
}

func DefaultDistributedTracingSpec() DistributedTracingSpec {
	return DistributedTracingSpec{
		Enabled:        false,
		ServiceName:    DefaultDistributedTracingServiceName,
		ServiceVersion: DefaultDistributedTracingServiceVersion,
		Environment:    DefaultDistributedTracingEnvironment,
		Exporter:       DistributedTracingExporterJaeger,
		Endpoint:       DefaultDistributedTracingJaegerEndpoint,
		SamplingRatio:  DefaultDistributedTracingSamplingRatio,
		Propagator:     DistributedTracingPropagatorW3C,
	}
}

func LoadDistributedTracingSpecFromEnv(getenv func(string) string) (DistributedTracingSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultDistributedTracingSpec()
	warnings := make([]string, 0, 4)

	enabledRaw := strings.TrimSpace(getenv("TRACING_ENABLED"))
	if enabledRaw != "" {
		enabled, err := parseDistributedTracingBool(enabledRaw)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("TRACING_ENABLED=%q 无效，回退默认值 false: %v", enabledRaw, err))
		} else {
			spec.Enabled = enabled
		}
	}

	if serviceName := strings.TrimSpace(getenv("TRACING_SERVICE_NAME")); serviceName != "" {
		spec.ServiceName = serviceName
	}
	if serviceVersion := strings.TrimSpace(getenv("TRACING_SERVICE_VERSION")); serviceVersion != "" {
		spec.ServiceVersion = serviceVersion
	}
	if environment := strings.TrimSpace(getenv("TRACING_ENVIRONMENT")); environment != "" {
		spec.Environment = environment
	}

	exporterRaw := strings.TrimSpace(getenv("TRACING_EXPORTER"))
	if exporterRaw != "" {
		exporter, err := parseDistributedTracingExporter(exporterRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf("TRACING_EXPORTER=%q 无效，回退默认值 %s: %v", exporterRaw, spec.Exporter, err),
			)
		} else {
			spec.Exporter = exporter
		}
	}
	spec.Endpoint = distributedTracingDefaultEndpoint(spec.Exporter)

	propagatorRaw := strings.TrimSpace(getenv("TRACING_PROPAGATOR"))
	if propagatorRaw != "" {
		propagatorType, err := parseDistributedTracingPropagator(propagatorRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf("TRACING_PROPAGATOR=%q 无效，回退默认值 %s: %v", propagatorRaw, spec.Propagator, err),
			)
		} else {
			spec.Propagator = propagatorType
		}
	}

	samplingRatioRaw := strings.TrimSpace(getenv("TRACING_SAMPLING_RATIO"))
	if samplingRatioRaw != "" {
		samplingRatio, err := strconv.ParseFloat(samplingRatioRaw, 64)
		if err != nil || samplingRatio < 0 || samplingRatio > 1 {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"TRACING_SAMPLING_RATIO=%q 无效，回退默认值 %.2f，取值范围应为 [0,1]",
					samplingRatioRaw,
					DefaultDistributedTracingSamplingRatio,
				),
			)
		} else {
			spec.SamplingRatio = samplingRatio
		}
	}

	endpointRaw := strings.TrimSpace(getenv("TRACING_ENDPOINT"))
	if endpointRaw != "" {
		if _, err := url.ParseRequestURI(endpointRaw); err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf("TRACING_ENDPOINT=%q 无效，回退默认端点: %v", endpointRaw, err),
			)
		} else {
			spec.Endpoint = endpointRaw
		}
	}

	normalized, err := normalizeDistributedTracingSpec(spec)
	if err != nil {
		return DefaultDistributedTracingSpec(), fmt.Errorf("分布式追踪配置非法，回退默认关闭: %w", err)
	}

	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func SetupDistributedTracing(spec DistributedTracingSpec) (*DistributedTracingRuntime, error) {
	normalized, err := normalizeDistributedTracingSpec(spec)
	if err != nil {
		return nil, err
	}
	runtime := &DistributedTracingRuntime{
		spec: normalized,
		shutdown: func(context.Context) error {
			return nil
		},
	}

	if !normalized.Enabled {
		otel.SetTracerProvider(oteltrace.NewNoopTracerProvider())
		otel.SetTextMapPropagator(buildDistributedTracingPropagator(normalized.Propagator))
		return runtime, nil
	}

	exporter, err := buildDistributedTracingExporter(normalized)
	if err != nil {
		return runtime, err
	}

	resource, err := sdkresource.Merge(
		sdkresource.Default(),
		sdkresource.NewSchemaless(
			attribute.String("service.name", normalized.ServiceName),
			attribute.String("service.version", normalized.ServiceVersion),
			attribute.String("deployment.environment", normalized.Environment),
		),
	)
	if err != nil {
		return runtime, fmt.Errorf("构建 tracing resource 失败: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(normalized.SamplingRatio)),
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(buildDistributedTracingPropagator(normalized.Propagator))
	runtime.shutdown = tp.Shutdown
	return runtime, nil
}

func (r *DistributedTracingRuntime) Spec() DistributedTracingSpec {
	if r == nil {
		return DefaultDistributedTracingSpec()
	}
	return r.spec
}

func (r *DistributedTracingRuntime) Shutdown(ctx context.Context) error {
	if r == nil || r.shutdown == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return r.shutdown(ctx)
}

func BuildDistributedTracingLog(spec DistributedTracingSpec) string {
	normalized, err := normalizeDistributedTracingSpec(spec)
	if err != nil {
		return fmt.Sprintf("链路追踪未启用：配置非法 %v", err)
	}
	if !normalized.Enabled {
		return "链路追踪未启用：TRACING_ENABLED=false"
	}
	return fmt.Sprintf(
		"链路追踪已启用：exporter=%s endpoint=%s service=%s sample_ratio=%.2f propagator=%s",
		normalized.Exporter,
		normalized.Endpoint,
		normalized.ServiceName,
		normalized.SamplingRatio,
		normalized.Propagator,
	)
}

func WrapHTTPClientWithTracing(client *http.Client, component string) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	if _, ok := client.Transport.(*tracingRoundTripper); ok {
		return client
	}
	cloned := *client
	cloned.Transport = NewTracingRoundTripper(
		NewRetryPolicyRoundTripper(client.Transport, DefaultHTTPRetryPolicySpec()),
		component,
	)
	return &cloned
}

func NewTracingRoundTripper(base http.RoundTripper, component string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "http-client"
	}
	return &tracingRoundTripper{
		base:   base,
		tracer: otel.Tracer("distributed-tracing/" + component),
	}
}

func WrapHTTPHandlerWithTracing(next http.Handler, component string) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "http-server"
	}
	tracer := otel.Tracer("distributed-tracing/" + component)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r == nil {
			next.ServeHTTP(w, r)
			return
		}
		baseCtx := r.Context()
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		ctx := otel.GetTextMapPropagator().Extract(baseCtx, propagation.HeaderCarrier(r.Header))
		spanName := buildTracingServerSpanName(r)
		spanAttributes := []attribute.KeyValue{
			attribute.String("http.method", strings.ToUpper(strings.TrimSpace(r.Method))),
			attribute.String("http.target", tracingRequestPath(r)),
		}
		if host := strings.TrimSpace(r.Host); host != "" {
			spanAttributes = append(spanAttributes, attribute.String("http.host", host))
		}
		ctx, span := tracer.Start(
			ctx,
			spanName,
			oteltrace.WithSpanKind(oteltrace.SpanKindServer),
			oteltrace.WithAttributes(spanAttributes...),
		)
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type tracingRoundTripper struct {
	base   http.RoundTripper
	tracer oteltrace.Tracer
}

func (t *tracingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	ctx := req.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	spanName := buildTracingClientSpanName(req)
	spanAttributes := []attribute.KeyValue{
		attribute.String("http.method", strings.ToUpper(strings.TrimSpace(req.Method))),
		attribute.String("http.target", tracingRequestPath(req)),
	}
	if req.URL != nil {
		spanAttributes = append(
			spanAttributes,
			attribute.String("http.url", tracingURLForSpan(req.URL)),
			attribute.String("http.host", strings.TrimSpace(req.URL.Host)),
		)
	}
	ctx, span := t.tracer.Start(
		ctx,
		spanName,
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(spanAttributes...),
	)
	defer span.End()

	clonedReq := req.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(clonedReq.Header))

	resp, err := base.RoundTrip(clonedReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	if resp.StatusCode >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	return resp, nil
}

func parseDistributedTracingBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on", "enable", "enabled":
		return true, nil
	case "0", "false", "no", "n", "off", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("仅支持 true/false")
	}
}

func parseDistributedTracingExporter(raw string) (DistributedTracingExporter, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(DistributedTracingExporterJaeger):
		return DistributedTracingExporterJaeger, nil
	case string(DistributedTracingExporterZipkin):
		return DistributedTracingExporterZipkin, nil
	default:
		return "", fmt.Errorf("仅支持 jaeger/zipkin")
	}
}

func parseDistributedTracingPropagator(raw string) (DistributedTracingPropagator, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(DistributedTracingPropagatorW3C), "tracecontext", "trace-context":
		return DistributedTracingPropagatorW3C, nil
	case string(DistributedTracingPropagatorB3):
		return DistributedTracingPropagatorB3, nil
	default:
		return "", fmt.Errorf("仅支持 w3c/b3")
	}
}

func normalizeDistributedTracingSpec(spec DistributedTracingSpec) (DistributedTracingSpec, error) {
	normalized := DefaultDistributedTracingSpec()
	normalized.Enabled = spec.Enabled
	if serviceName := strings.TrimSpace(spec.ServiceName); serviceName != "" {
		normalized.ServiceName = serviceName
	}
	if serviceVersion := strings.TrimSpace(spec.ServiceVersion); serviceVersion != "" {
		normalized.ServiceVersion = serviceVersion
	}
	if environment := strings.TrimSpace(spec.Environment); environment != "" {
		normalized.Environment = environment
	}
	if exporter := strings.TrimSpace(string(spec.Exporter)); exporter != "" {
		parsedExporter, err := parseDistributedTracingExporter(exporter)
		if err != nil {
			return DistributedTracingSpec{}, err
		}
		normalized.Exporter = parsedExporter
	}
	if propagatorType := strings.TrimSpace(string(spec.Propagator)); propagatorType != "" {
		parsedPropagator, err := parseDistributedTracingPropagator(propagatorType)
		if err != nil {
			return DistributedTracingSpec{}, err
		}
		normalized.Propagator = parsedPropagator
	}
	if spec.SamplingRatio < 0 || spec.SamplingRatio > 1 {
		return DistributedTracingSpec{}, fmt.Errorf("sampling ratio must be in range [0,1]")
	}
	normalized.SamplingRatio = spec.SamplingRatio

	endpoint := strings.TrimSpace(spec.Endpoint)
	if endpoint == "" {
		endpoint = distributedTracingDefaultEndpoint(normalized.Exporter)
	}
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return DistributedTracingSpec{}, fmt.Errorf("tracing endpoint 非法: %w", err)
	}
	normalized.Endpoint = endpoint
	return normalized, nil
}

func distributedTracingDefaultEndpoint(exporter DistributedTracingExporter) string {
	if exporter == DistributedTracingExporterZipkin {
		return DefaultDistributedTracingZipkinEndpoint
	}
	return DefaultDistributedTracingJaegerEndpoint
}

func buildDistributedTracingExporter(spec DistributedTracingSpec) (sdktrace.SpanExporter, error) {
	switch spec.Exporter {
	case DistributedTracingExporterJaeger:
		return jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(spec.Endpoint)))
	case DistributedTracingExporterZipkin:
		return zipkin.New(spec.Endpoint)
	default:
		return nil, fmt.Errorf("unsupported tracing exporter: %s", spec.Exporter)
	}
}

func buildDistributedTracingPropagator(propagatorType DistributedTracingPropagator) propagation.TextMapPropagator {
	switch propagatorType {
	case DistributedTracingPropagatorB3:
		return propagation.NewCompositeTextMapPropagator(
			b3.New(),
			propagation.TraceContext{},
			propagation.Baggage{},
		)
	default:
		return propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		)
	}
}

func buildTracingServerSpanName(req *http.Request) string {
	method := "HTTP"
	if req != nil {
		if trimmedMethod := strings.ToUpper(strings.TrimSpace(req.Method)); trimmedMethod != "" {
			method = trimmedMethod
		}
	}
	return method + " " + tracingRequestPath(req)
}

func buildTracingClientSpanName(req *http.Request) string {
	method := "HTTP"
	if req != nil {
		if trimmedMethod := strings.ToUpper(strings.TrimSpace(req.Method)); trimmedMethod != "" {
			method = trimmedMethod
		}
	}
	return method + " " + tracingRequestPath(req)
}

func tracingRequestPath(req *http.Request) string {
	if req == nil || req.URL == nil {
		return "/"
	}
	path := strings.TrimSpace(req.URL.EscapedPath())
	if path == "" {
		path = strings.TrimSpace(req.URL.Path)
	}
	if path == "" {
		return "/"
	}
	return path
}

func tracingURLForSpan(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	cloned := *parsed
	cloned.RawQuery = ""
	cloned.Fragment = ""
	return cloned.String()
}
