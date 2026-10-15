package system

import (
	"fmt"
	"os"
	"strings"
)

type K8sServiceMeshLoadBalancer string

const (
	K8sServiceMeshLoadBalancerLeastRequest K8sServiceMeshLoadBalancer = "LEAST_REQUEST"
	K8sServiceMeshLoadBalancerRoundRobin   K8sServiceMeshLoadBalancer = "ROUND_ROBIN"
)

type K8sServiceMeshMTLSMode string

const (
	K8sServiceMeshMTLSModeStrict     K8sServiceMeshMTLSMode = "STRICT"
	K8sServiceMeshMTLSModePermissive K8sServiceMeshMTLSMode = "PERMISSIVE"
	K8sServiceMeshMTLSModeDisable    K8sServiceMeshMTLSMode = "DISABLE"
)

const (
	DefaultK8sServiceMeshName                  = "ledgerbot-mesh"
	DefaultK8sServiceMeshNamespace             = DefaultK8sDeploymentNamespace
	DefaultK8sServiceMeshHost                  = "ledgerbot.default.svc.cluster.local"
	DefaultK8sServiceMeshGateway               = "mesh"
	DefaultK8sServiceMeshLoadBalancer          = K8sServiceMeshLoadBalancerLeastRequest
	DefaultK8sServiceMeshRequestTimeoutSec     = int32(5)
	DefaultK8sServiceMeshRetryAttempts         = int32(3)
	DefaultK8sServiceMeshRetryPerTryTimeoutSec = int32(2)
	DefaultK8sServiceMeshMaxConnections        = int32(1024)
	DefaultK8sServiceMeshMaxPendingRequests    = int32(128)
	DefaultK8sServiceMeshConsecutive5xxErrors  = int32(5)
	DefaultK8sServiceMeshOutlierIntervalSec    = int32(10)
	DefaultK8sServiceMeshBaseEjectionSec       = int32(30)
	DefaultK8sServiceMeshMaxEjectionPercent    = int32(50)
	DefaultK8sServiceMeshMTLSMode              = K8sServiceMeshMTLSModeStrict
)

type K8sServiceMeshSpec struct {
	Name                  string
	Namespace             string
	Host                  string
	Gateway               string
	LoadBalancerPolicy    K8sServiceMeshLoadBalancer
	RequestTimeoutSec     int32
	RetryAttempts         int32
	RetryPerTryTimeoutSec int32
	MaxConnections        int32
	MaxPendingRequests    int32
	Consecutive5xxErrors  int32
	OutlierIntervalSec    int32
	BaseEjectionSec       int32
	MaxEjectionPercent    int32
	MTLSMode              K8sServiceMeshMTLSMode
}

func DefaultK8sServiceMeshSpec() K8sServiceMeshSpec {
	return K8sServiceMeshSpec{
		Name:                  DefaultK8sServiceMeshName,
		Namespace:             DefaultK8sServiceMeshNamespace,
		Host:                  DefaultK8sServiceMeshHost,
		Gateway:               DefaultK8sServiceMeshGateway,
		LoadBalancerPolicy:    DefaultK8sServiceMeshLoadBalancer,
		RequestTimeoutSec:     DefaultK8sServiceMeshRequestTimeoutSec,
		RetryAttempts:         DefaultK8sServiceMeshRetryAttempts,
		RetryPerTryTimeoutSec: DefaultK8sServiceMeshRetryPerTryTimeoutSec,
		MaxConnections:        DefaultK8sServiceMeshMaxConnections,
		MaxPendingRequests:    DefaultK8sServiceMeshMaxPendingRequests,
		Consecutive5xxErrors:  DefaultK8sServiceMeshConsecutive5xxErrors,
		OutlierIntervalSec:    DefaultK8sServiceMeshOutlierIntervalSec,
		BaseEjectionSec:       DefaultK8sServiceMeshBaseEjectionSec,
		MaxEjectionPercent:    DefaultK8sServiceMeshMaxEjectionPercent,
		MTLSMode:              DefaultK8sServiceMeshMTLSMode,
	}
}

func NormalizeK8sServiceMeshSpec(spec K8sServiceMeshSpec) (K8sServiceMeshSpec, error) {
	normalized := DefaultK8sServiceMeshSpec()

	if name := strings.TrimSpace(spec.Name); name != "" {
		normalized.Name = name
	}
	if namespace := strings.TrimSpace(spec.Namespace); namespace != "" {
		normalized.Namespace = namespace
	}
	if host := strings.TrimSpace(spec.Host); host != "" {
		normalized.Host = host
	}
	if gateway := strings.TrimSpace(spec.Gateway); gateway != "" {
		normalized.Gateway = gateway
	}

	loadBalancerRaw := strings.TrimSpace(string(spec.LoadBalancerPolicy))
	if loadBalancerRaw != "" {
		loadBalancer, err := parseK8sServiceMeshLoadBalancer(loadBalancerRaw)
		if err != nil {
			return K8sServiceMeshSpec{}, err
		}
		normalized.LoadBalancerPolicy = loadBalancer
	}

	mtlsRaw := strings.TrimSpace(string(spec.MTLSMode))
	if mtlsRaw != "" {
		mode, err := parseK8sServiceMeshMTLSMode(mtlsRaw)
		if err != nil {
			return K8sServiceMeshSpec{}, err
		}
		normalized.MTLSMode = mode
	}

	switch {
	case spec.RequestTimeoutSec < 0:
		return K8sServiceMeshSpec{}, fmt.Errorf("request timeout must be greater than 0")
	case spec.RequestTimeoutSec > 0:
		normalized.RequestTimeoutSec = spec.RequestTimeoutSec
	}
	switch {
	case spec.RetryAttempts < 0:
		return K8sServiceMeshSpec{}, fmt.Errorf("retry attempts must be in range 1-10")
	case spec.RetryAttempts > 0:
		normalized.RetryAttempts = spec.RetryAttempts
	}
	switch {
	case spec.RetryPerTryTimeoutSec < 0:
		return K8sServiceMeshSpec{}, fmt.Errorf("retry per-try timeout must be greater than 0")
	case spec.RetryPerTryTimeoutSec > 0:
		normalized.RetryPerTryTimeoutSec = spec.RetryPerTryTimeoutSec
	}
	switch {
	case spec.MaxConnections < 0:
		return K8sServiceMeshSpec{}, fmt.Errorf("max connections must be greater than 0")
	case spec.MaxConnections > 0:
		normalized.MaxConnections = spec.MaxConnections
	}
	switch {
	case spec.MaxPendingRequests < 0:
		return K8sServiceMeshSpec{}, fmt.Errorf("max pending requests must be greater than 0")
	case spec.MaxPendingRequests > 0:
		normalized.MaxPendingRequests = spec.MaxPendingRequests
	}
	switch {
	case spec.Consecutive5xxErrors < 0:
		return K8sServiceMeshSpec{}, fmt.Errorf("consecutive 5xx errors must be greater than 0")
	case spec.Consecutive5xxErrors > 0:
		normalized.Consecutive5xxErrors = spec.Consecutive5xxErrors
	}
	switch {
	case spec.OutlierIntervalSec < 0:
		return K8sServiceMeshSpec{}, fmt.Errorf("outlier interval must be greater than 0")
	case spec.OutlierIntervalSec > 0:
		normalized.OutlierIntervalSec = spec.OutlierIntervalSec
	}
	switch {
	case spec.BaseEjectionSec < 0:
		return K8sServiceMeshSpec{}, fmt.Errorf("base ejection time must be greater than 0")
	case spec.BaseEjectionSec > 0:
		normalized.BaseEjectionSec = spec.BaseEjectionSec
	}
	switch {
	case spec.MaxEjectionPercent < 0:
		return K8sServiceMeshSpec{}, fmt.Errorf("max ejection percent must be in range 1-100")
	case spec.MaxEjectionPercent > 0:
		normalized.MaxEjectionPercent = spec.MaxEjectionPercent
	}

	if !k8sDNSLabelPattern.MatchString(normalized.Name) {
		return K8sServiceMeshSpec{}, fmt.Errorf("service mesh name must be a valid k8s dns label")
	}
	if !k8sDNSLabelPattern.MatchString(normalized.Namespace) {
		return K8sServiceMeshSpec{}, fmt.Errorf("service mesh namespace must be a valid k8s dns label")
	}
	if err := validateK8sServiceMeshHost(normalized.Host); err != nil {
		return K8sServiceMeshSpec{}, err
	}
	if strings.TrimSpace(normalized.Gateway) == "" {
		return K8sServiceMeshSpec{}, fmt.Errorf("service mesh gateway must not be empty")
	}
	if normalized.RequestTimeoutSec <= 0 {
		return K8sServiceMeshSpec{}, fmt.Errorf("request timeout must be greater than 0")
	}
	if normalized.RetryAttempts <= 0 || normalized.RetryAttempts > 10 {
		return K8sServiceMeshSpec{}, fmt.Errorf("retry attempts must be in range 1-10")
	}
	if normalized.RetryPerTryTimeoutSec <= 0 {
		return K8sServiceMeshSpec{}, fmt.Errorf("retry per-try timeout must be greater than 0")
	}
	if normalized.RetryPerTryTimeoutSec > normalized.RequestTimeoutSec {
		return K8sServiceMeshSpec{}, fmt.Errorf("retry per-try timeout must be <= request timeout")
	}
	if normalized.MaxConnections <= 0 {
		return K8sServiceMeshSpec{}, fmt.Errorf("max connections must be greater than 0")
	}
	if normalized.MaxPendingRequests <= 0 {
		return K8sServiceMeshSpec{}, fmt.Errorf("max pending requests must be greater than 0")
	}
	if normalized.Consecutive5xxErrors <= 0 {
		return K8sServiceMeshSpec{}, fmt.Errorf("consecutive 5xx errors must be greater than 0")
	}
	if normalized.OutlierIntervalSec <= 0 {
		return K8sServiceMeshSpec{}, fmt.Errorf("outlier interval must be greater than 0")
	}
	if normalized.BaseEjectionSec <= 0 {
		return K8sServiceMeshSpec{}, fmt.Errorf("base ejection time must be greater than 0")
	}
	if normalized.MaxEjectionPercent <= 0 || normalized.MaxEjectionPercent > 100 {
		return K8sServiceMeshSpec{}, fmt.Errorf("max ejection percent must be in range 1-100")
	}
	if normalized.MTLSMode == "" {
		return K8sServiceMeshSpec{}, fmt.Errorf("mTLS mode must not be empty")
	}

	return normalized, nil
}

func LoadK8sServiceMeshSpecFromEnv(getenv func(string) string) (K8sServiceMeshSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	lbRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_LB_POLICY"))
	loadBalancer, lbErr := parseK8sServiceMeshLoadBalancer(lbRaw)
	mtlsRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_MTLS_MODE"))
	mtlsMode, mtlsErr := parseK8sServiceMeshMTLSMode(mtlsRaw)
	requestTimeoutRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_REQUEST_TIMEOUT_SEC"))
	requestTimeout, requestTimeoutErr := parsePositiveInt32(requestTimeoutRaw)
	retryAttemptsRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_RETRY_ATTEMPTS"))
	retryAttempts, retryAttemptsErr := parsePositiveInt32(retryAttemptsRaw)
	retryPerTryTimeoutRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_RETRY_TIMEOUT_SEC"))
	retryPerTryTimeout, retryPerTryTimeoutErr := parsePositiveInt32(retryPerTryTimeoutRaw)
	maxConnectionsRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_MAX_CONNECTIONS"))
	maxConnections, maxConnectionsErr := parsePositiveInt32(maxConnectionsRaw)
	maxPendingRequestsRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_MAX_PENDING_REQUESTS"))
	maxPendingRequests, maxPendingRequestsErr := parsePositiveInt32(maxPendingRequestsRaw)
	consecutive5xxRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_CONSECUTIVE_5XX_ERRORS"))
	consecutive5xx, consecutive5xxErr := parsePositiveInt32(consecutive5xxRaw)
	outlierIntervalRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_OUTLIER_INTERVAL_SEC"))
	outlierInterval, outlierIntervalErr := parsePositiveInt32(outlierIntervalRaw)
	baseEjectionRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_BASE_EJECTION_SEC"))
	baseEjection, baseEjectionErr := parsePositiveInt32(baseEjectionRaw)
	maxEjectionPercentRaw := strings.TrimSpace(getenv("K8S_SERVICE_MESH_MAX_EJECTION_PERCENT"))
	maxEjectionPercent, maxEjectionPercentErr := parsePositiveInt32(maxEjectionPercentRaw)

	spec := K8sServiceMeshSpec{
		Name:      strings.TrimSpace(getenv("K8S_SERVICE_MESH_NAME")),
		Namespace: strings.TrimSpace(getenv("K8S_SERVICE_MESH_NAMESPACE")),
		Host:      strings.TrimSpace(getenv("K8S_SERVICE_MESH_HOST")),
		Gateway:   strings.TrimSpace(getenv("K8S_SERVICE_MESH_GATEWAY")),
	}
	if loadBalancer != "" {
		spec.LoadBalancerPolicy = loadBalancer
	}
	if mtlsMode != "" {
		spec.MTLSMode = mtlsMode
	}
	if requestTimeout > 0 {
		spec.RequestTimeoutSec = requestTimeout
	}
	if retryAttempts > 0 {
		spec.RetryAttempts = retryAttempts
	}
	if retryPerTryTimeout > 0 {
		spec.RetryPerTryTimeoutSec = retryPerTryTimeout
	}
	if maxConnections > 0 {
		spec.MaxConnections = maxConnections
	}
	if maxPendingRequests > 0 {
		spec.MaxPendingRequests = maxPendingRequests
	}
	if consecutive5xx > 0 {
		spec.Consecutive5xxErrors = consecutive5xx
	}
	if outlierInterval > 0 {
		spec.OutlierIntervalSec = outlierInterval
	}
	if baseEjection > 0 {
		spec.BaseEjectionSec = baseEjection
	}
	if maxEjectionPercent > 0 {
		spec.MaxEjectionPercent = maxEjectionPercent
	}

	normalized, err := NormalizeK8sServiceMeshSpec(spec)
	if err != nil {
		fallback, fallbackErr := NormalizeK8sServiceMeshSpec(K8sServiceMeshSpec{})
		if fallbackErr != nil {
			return K8sServiceMeshSpec{}, fmt.Errorf("normalize k8s service mesh defaults failed: %w", fallbackErr)
		}
		return fallback, fmt.Errorf("k8s service mesh config invalid, fallback to defaults: %w", err)
	}

	if lbErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_LB_POLICY=%q invalid, fallback to %s: %w",
			lbRaw,
			normalized.LoadBalancerPolicy,
			lbErr,
		)
	}
	if mtlsErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_MTLS_MODE=%q invalid, fallback to %s: %w",
			mtlsRaw,
			normalized.MTLSMode,
			mtlsErr,
		)
	}
	if requestTimeoutErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_REQUEST_TIMEOUT_SEC=%q invalid, fallback to %d: %w",
			requestTimeoutRaw,
			normalized.RequestTimeoutSec,
			requestTimeoutErr,
		)
	}
	if retryAttemptsErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_RETRY_ATTEMPTS=%q invalid, fallback to %d: %w",
			retryAttemptsRaw,
			normalized.RetryAttempts,
			retryAttemptsErr,
		)
	}
	if retryPerTryTimeoutErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_RETRY_TIMEOUT_SEC=%q invalid, fallback to %d: %w",
			retryPerTryTimeoutRaw,
			normalized.RetryPerTryTimeoutSec,
			retryPerTryTimeoutErr,
		)
	}
	if maxConnectionsErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_MAX_CONNECTIONS=%q invalid, fallback to %d: %w",
			maxConnectionsRaw,
			normalized.MaxConnections,
			maxConnectionsErr,
		)
	}
	if maxPendingRequestsErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_MAX_PENDING_REQUESTS=%q invalid, fallback to %d: %w",
			maxPendingRequestsRaw,
			normalized.MaxPendingRequests,
			maxPendingRequestsErr,
		)
	}
	if consecutive5xxErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_CONSECUTIVE_5XX_ERRORS=%q invalid, fallback to %d: %w",
			consecutive5xxRaw,
			normalized.Consecutive5xxErrors,
			consecutive5xxErr,
		)
	}
	if outlierIntervalErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_OUTLIER_INTERVAL_SEC=%q invalid, fallback to %d: %w",
			outlierIntervalRaw,
			normalized.OutlierIntervalSec,
			outlierIntervalErr,
		)
	}
	if baseEjectionErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_BASE_EJECTION_SEC=%q invalid, fallback to %d: %w",
			baseEjectionRaw,
			normalized.BaseEjectionSec,
			baseEjectionErr,
		)
	}
	if maxEjectionPercentErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SERVICE_MESH_MAX_EJECTION_PERCENT=%q invalid, fallback to %d: %w",
			maxEjectionPercentRaw,
			normalized.MaxEjectionPercent,
			maxEjectionPercentErr,
		)
	}
	return normalized, nil
}

func BuildK8sServiceMeshManifests(spec K8sServiceMeshSpec) (string, error) {
	normalized, err := NormalizeK8sServiceMeshSpec(spec)
	if err != nil {
		return "", err
	}

	manifests := fmt.Sprintf(`apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: %s
  namespace: %s
spec:
  host: %s
  trafficPolicy:
    tls:
      mode: %s
    loadBalancer:
      simple: %s
    connectionPool:
      tcp:
        maxConnections: %d
      http:
        http1MaxPendingRequests: %d
    outlierDetection:
      consecutive5xxErrors: %d
      interval: %ds
      baseEjectionTime: %ds
      maxEjectionPercent: %d
---
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: %s
  namespace: %s
spec:
  hosts:
    - %s
  gateways:
    - %s
  http:
    - retries:
        attempts: %d
        perTryTimeout: %ds
        retryOn: gateway-error,connect-failure,refused-stream,unavailable,cancelled,5xx
      timeout: %ds
      route:
        - destination:
            host: %s
`,
		normalized.Name,
		normalized.Namespace,
		normalized.Host,
		normalized.MTLSMode,
		normalized.LoadBalancerPolicy,
		normalized.MaxConnections,
		normalized.MaxPendingRequests,
		normalized.Consecutive5xxErrors,
		normalized.OutlierIntervalSec,
		normalized.BaseEjectionSec,
		normalized.MaxEjectionPercent,
		normalized.Name,
		normalized.Namespace,
		normalized.Host,
		normalized.Gateway,
		normalized.RetryAttempts,
		normalized.RetryPerTryTimeoutSec,
		normalized.RequestTimeoutSec,
		normalized.Host,
	)
	return manifests, nil
}

func BuildK8sServiceMeshLog(spec K8sServiceMeshSpec) string {
	normalized, err := NormalizeK8sServiceMeshSpec(spec)
	if err != nil {
		return fmt.Sprintf("K8s Service Mesh 未优化：配置非法 %v", err)
	}
	return fmt.Sprintf(
		"K8s Service Mesh 已优化：name=%s namespace=%s host=%s gateway=%s lb=%s timeout=%ds retries=%d per_try_timeout=%ds mTLS=%s pool(connections=%d pending=%d) outlier(5xx=%d interval=%ds ejection=%ds/%d%%)",
		normalized.Name,
		normalized.Namespace,
		normalized.Host,
		normalized.Gateway,
		normalized.LoadBalancerPolicy,
		normalized.RequestTimeoutSec,
		normalized.RetryAttempts,
		normalized.RetryPerTryTimeoutSec,
		normalized.MTLSMode,
		normalized.MaxConnections,
		normalized.MaxPendingRequests,
		normalized.Consecutive5xxErrors,
		normalized.OutlierIntervalSec,
		normalized.BaseEjectionSec,
		normalized.MaxEjectionPercent,
	)
}

func parseK8sServiceMeshLoadBalancer(raw string) (K8sServiceMeshLoadBalancer, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "least_request", "least-request", "leastrequest", "least":
		return K8sServiceMeshLoadBalancerLeastRequest, nil
	case "round_robin", "round-robin", "roundrobin", "rr":
		return K8sServiceMeshLoadBalancerRoundRobin, nil
	default:
		return "", fmt.Errorf("unsupported load balancer policy %q", raw)
	}
}

func parseK8sServiceMeshMTLSMode(raw string) (K8sServiceMeshMTLSMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "strict":
		return K8sServiceMeshMTLSModeStrict, nil
	case "permissive", "permit":
		return K8sServiceMeshMTLSModePermissive, nil
	case "disable", "disabled", "off":
		return K8sServiceMeshMTLSModeDisable, nil
	default:
		return "", fmt.Errorf("unsupported mTLS mode %q", raw)
	}
}

func validateK8sServiceMeshHost(host string) error {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return fmt.Errorf("service mesh host must not be empty")
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return fmt.Errorf("service mesh host must not contain spaces")
	}
	return nil
}
