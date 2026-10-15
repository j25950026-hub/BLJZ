package system

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultK8sProbeName                            = "ledgerbot-probe"
	DefaultK8sProbeNamespace                       = "default"
	DefaultK8sProbeContainerName                   = "ledgerbot"
	DefaultK8sProbePort                      int32 = 18080
	DefaultK8sProbeBindAddress                     = ":18080"
	DefaultK8sProbeLivenessPath                    = "/healthz"
	DefaultK8sProbeReadinessPath                   = "/readyz"
	DefaultK8sProbeLivenessInitialDelaySec   int32 = 10
	DefaultK8sProbeLivenessPeriodSec         int32 = 10
	DefaultK8sProbeLivenessTimeoutSec        int32 = 2
	DefaultK8sProbeLivenessFailureThreshold  int32 = 3
	DefaultK8sProbeReadinessInitialDelaySec  int32 = 5
	DefaultK8sProbeReadinessPeriodSec        int32 = 5
	DefaultK8sProbeReadinessTimeoutSec       int32 = 2
	DefaultK8sProbeReadinessSuccessThreshold int32 = 1
	DefaultK8sProbeReadinessFailureThreshold int32 = 3
)

type K8sProbeSpec struct {
	Name      string
	Namespace string
	Container string
	Port      int32
	BindAddr  string
	Liveness  K8sHTTPProbeSettings
	Readiness K8sHTTPProbeSettings
}

type K8sHTTPProbeSettings struct {
	Path             string
	InitialDelaySec  int32
	PeriodSec        int32
	TimeoutSec       int32
	SuccessThreshold int32
	FailureThreshold int32
}

func DefaultK8sProbeSpec() K8sProbeSpec {
	return K8sProbeSpec{
		Name:      DefaultK8sProbeName,
		Namespace: DefaultK8sProbeNamespace,
		Container: DefaultK8sProbeContainerName,
		Port:      DefaultK8sProbePort,
		BindAddr:  DefaultK8sProbeBindAddress,
		Liveness: K8sHTTPProbeSettings{
			Path:             DefaultK8sProbeLivenessPath,
			InitialDelaySec:  DefaultK8sProbeLivenessInitialDelaySec,
			PeriodSec:        DefaultK8sProbeLivenessPeriodSec,
			TimeoutSec:       DefaultK8sProbeLivenessTimeoutSec,
			SuccessThreshold: 1,
			FailureThreshold: DefaultK8sProbeLivenessFailureThreshold,
		},
		Readiness: K8sHTTPProbeSettings{
			Path:             DefaultK8sProbeReadinessPath,
			InitialDelaySec:  DefaultK8sProbeReadinessInitialDelaySec,
			PeriodSec:        DefaultK8sProbeReadinessPeriodSec,
			TimeoutSec:       DefaultK8sProbeReadinessTimeoutSec,
			SuccessThreshold: DefaultK8sProbeReadinessSuccessThreshold,
			FailureThreshold: DefaultK8sProbeReadinessFailureThreshold,
		},
	}
}

func NormalizeK8sProbeSpec(spec K8sProbeSpec) (K8sProbeSpec, error) {
	normalized := DefaultK8sProbeSpec()
	if name := strings.TrimSpace(spec.Name); name != "" {
		normalized.Name = name
	}
	if namespace := strings.TrimSpace(spec.Namespace); namespace != "" {
		normalized.Namespace = namespace
	}
	if container := strings.TrimSpace(spec.Container); container != "" {
		normalized.Container = container
	}
	if spec.Port < 0 {
		return K8sProbeSpec{}, fmt.Errorf("probe port must be greater than 0")
	}
	if spec.Port > 0 {
		normalized.Port = spec.Port
	}
	if addr := strings.TrimSpace(spec.BindAddr); addr != "" {
		normalized.BindAddr = addr
	}

	if normalized.BindAddr == "" {
		normalized.BindAddr = fmt.Sprintf(":%d", normalized.Port)
	}
	normalized.Liveness = mergeHTTPProbeSettings(normalized.Liveness, spec.Liveness)
	normalized.Readiness = mergeHTTPProbeSettings(normalized.Readiness, spec.Readiness)

	if strings.TrimSpace(normalized.Name) == "" || strings.TrimSpace(normalized.Namespace) == "" {
		return K8sProbeSpec{}, fmt.Errorf("probe name/namespace must not be empty")
	}
	if strings.TrimSpace(normalized.Container) == "" {
		return K8sProbeSpec{}, fmt.Errorf("probe container name must not be empty")
	}
	if normalized.Port <= 0 || normalized.Port > 65535 {
		return K8sProbeSpec{}, fmt.Errorf("probe port must be in range 1-65535")
	}
	if strings.TrimSpace(normalized.BindAddr) == "" {
		return K8sProbeSpec{}, fmt.Errorf("probe bind address must not be empty")
	}
	if err := validateHTTPProbeSettings("liveness", normalized.Liveness); err != nil {
		return K8sProbeSpec{}, err
	}
	if err := validateHTTPProbeSettings("readiness", normalized.Readiness); err != nil {
		return K8sProbeSpec{}, err
	}
	return normalized, nil
}

func LoadK8sProbeSpecFromEnv(getenv func(string) string) (K8sProbeSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	portRaw := strings.TrimSpace(getenv("K8S_PROBE_PORT"))
	port, portErr := parsePositiveInt32ForProbe(portRaw)

	livenessInitialDelayRaw := strings.TrimSpace(getenv("K8S_LIVENESS_INITIAL_DELAY_SEC"))
	livenessInitialDelay, livenessInitialDelayErr := parsePositiveInt32ForProbe(livenessInitialDelayRaw)
	livenessPeriodRaw := strings.TrimSpace(getenv("K8S_LIVENESS_PERIOD_SEC"))
	livenessPeriod, livenessPeriodErr := parsePositiveInt32ForProbe(livenessPeriodRaw)
	livenessTimeoutRaw := strings.TrimSpace(getenv("K8S_LIVENESS_TIMEOUT_SEC"))
	livenessTimeout, livenessTimeoutErr := parsePositiveInt32ForProbe(livenessTimeoutRaw)
	livenessFailureRaw := strings.TrimSpace(getenv("K8S_LIVENESS_FAILURE_THRESHOLD"))
	livenessFailure, livenessFailureErr := parsePositiveInt32ForProbe(livenessFailureRaw)

	readinessInitialDelayRaw := strings.TrimSpace(getenv("K8S_READINESS_INITIAL_DELAY_SEC"))
	readinessInitialDelay, readinessInitialDelayErr := parsePositiveInt32ForProbe(readinessInitialDelayRaw)
	readinessPeriodRaw := strings.TrimSpace(getenv("K8S_READINESS_PERIOD_SEC"))
	readinessPeriod, readinessPeriodErr := parsePositiveInt32ForProbe(readinessPeriodRaw)
	readinessTimeoutRaw := strings.TrimSpace(getenv("K8S_READINESS_TIMEOUT_SEC"))
	readinessTimeout, readinessTimeoutErr := parsePositiveInt32ForProbe(readinessTimeoutRaw)
	readinessSuccessRaw := strings.TrimSpace(getenv("K8S_READINESS_SUCCESS_THRESHOLD"))
	readinessSuccess, readinessSuccessErr := parsePositiveInt32ForProbe(readinessSuccessRaw)
	readinessFailureRaw := strings.TrimSpace(getenv("K8S_READINESS_FAILURE_THRESHOLD"))
	readinessFailure, readinessFailureErr := parsePositiveInt32ForProbe(readinessFailureRaw)

	spec := K8sProbeSpec{
		Name:      strings.TrimSpace(getenv("K8S_PROBE_NAME")),
		Namespace: strings.TrimSpace(getenv("K8S_PROBE_NAMESPACE")),
		Container: strings.TrimSpace(getenv("K8S_PROBE_CONTAINER_NAME")),
		BindAddr:  strings.TrimSpace(getenv("K8S_PROBE_BIND_ADDR")),
		Liveness: K8sHTTPProbeSettings{
			Path: strings.TrimSpace(getenv("K8S_LIVENESS_PATH")),
		},
		Readiness: K8sHTTPProbeSettings{
			Path: strings.TrimSpace(getenv("K8S_READINESS_PATH")),
		},
	}
	if port > 0 {
		spec.Port = port
	}
	if livenessInitialDelay > 0 {
		spec.Liveness.InitialDelaySec = livenessInitialDelay
	}
	if livenessPeriod > 0 {
		spec.Liveness.PeriodSec = livenessPeriod
	}
	if livenessTimeout > 0 {
		spec.Liveness.TimeoutSec = livenessTimeout
	}
	if livenessFailure > 0 {
		spec.Liveness.FailureThreshold = livenessFailure
	}
	if readinessInitialDelay > 0 {
		spec.Readiness.InitialDelaySec = readinessInitialDelay
	}
	if readinessPeriod > 0 {
		spec.Readiness.PeriodSec = readinessPeriod
	}
	if readinessTimeout > 0 {
		spec.Readiness.TimeoutSec = readinessTimeout
	}
	if readinessSuccess > 0 {
		spec.Readiness.SuccessThreshold = readinessSuccess
	}
	if readinessFailure > 0 {
		spec.Readiness.FailureThreshold = readinessFailure
	}

	normalized, err := NormalizeK8sProbeSpec(spec)
	if err != nil {
		fallback, fallbackErr := NormalizeK8sProbeSpec(K8sProbeSpec{})
		if fallbackErr != nil {
			return K8sProbeSpec{}, fmt.Errorf("normalize k8s probe defaults failed: %w", fallbackErr)
		}
		return fallback, fmt.Errorf("k8s probe config invalid, fallback to defaults: %w", err)
	}
	if portErr != nil {
		return normalized, fmt.Errorf("K8S_PROBE_PORT=%q invalid, fallback to %d: %w", portRaw, normalized.Port, portErr)
	}
	if livenessInitialDelayErr != nil {
		return normalized, fmt.Errorf(
			"K8S_LIVENESS_INITIAL_DELAY_SEC=%q invalid, fallback to %d: %w",
			livenessInitialDelayRaw,
			normalized.Liveness.InitialDelaySec,
			livenessInitialDelayErr,
		)
	}
	if livenessPeriodErr != nil {
		return normalized, fmt.Errorf(
			"K8S_LIVENESS_PERIOD_SEC=%q invalid, fallback to %d: %w",
			livenessPeriodRaw,
			normalized.Liveness.PeriodSec,
			livenessPeriodErr,
		)
	}
	if livenessTimeoutErr != nil {
		return normalized, fmt.Errorf(
			"K8S_LIVENESS_TIMEOUT_SEC=%q invalid, fallback to %d: %w",
			livenessTimeoutRaw,
			normalized.Liveness.TimeoutSec,
			livenessTimeoutErr,
		)
	}
	if livenessFailureErr != nil {
		return normalized, fmt.Errorf(
			"K8S_LIVENESS_FAILURE_THRESHOLD=%q invalid, fallback to %d: %w",
			livenessFailureRaw,
			normalized.Liveness.FailureThreshold,
			livenessFailureErr,
		)
	}
	if readinessInitialDelayErr != nil {
		return normalized, fmt.Errorf(
			"K8S_READINESS_INITIAL_DELAY_SEC=%q invalid, fallback to %d: %w",
			readinessInitialDelayRaw,
			normalized.Readiness.InitialDelaySec,
			readinessInitialDelayErr,
		)
	}
	if readinessPeriodErr != nil {
		return normalized, fmt.Errorf(
			"K8S_READINESS_PERIOD_SEC=%q invalid, fallback to %d: %w",
			readinessPeriodRaw,
			normalized.Readiness.PeriodSec,
			readinessPeriodErr,
		)
	}
	if readinessTimeoutErr != nil {
		return normalized, fmt.Errorf(
			"K8S_READINESS_TIMEOUT_SEC=%q invalid, fallback to %d: %w",
			readinessTimeoutRaw,
			normalized.Readiness.TimeoutSec,
			readinessTimeoutErr,
		)
	}
	if readinessSuccessErr != nil {
		return normalized, fmt.Errorf(
			"K8S_READINESS_SUCCESS_THRESHOLD=%q invalid, fallback to %d: %w",
			readinessSuccessRaw,
			normalized.Readiness.SuccessThreshold,
			readinessSuccessErr,
		)
	}
	if readinessFailureErr != nil {
		return normalized, fmt.Errorf(
			"K8S_READINESS_FAILURE_THRESHOLD=%q invalid, fallback to %d: %w",
			readinessFailureRaw,
			normalized.Readiness.FailureThreshold,
			readinessFailureErr,
		)
	}
	return normalized, nil
}

func BuildK8sProbeManifest(spec K8sProbeSpec) (string, error) {
	normalized, err := NormalizeK8sProbeSpec(spec)
	if err != nil {
		return "", err
	}

	manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  template:
    spec:
      containers:
        - name: %s
          ports:
            - containerPort: %d
          livenessProbe:
            httpGet:
              path: %s
              port: %d
            initialDelaySeconds: %d
            periodSeconds: %d
            timeoutSeconds: %d
            failureThreshold: %d
          readinessProbe:
            httpGet:
              path: %s
              port: %d
            initialDelaySeconds: %d
            periodSeconds: %d
            timeoutSeconds: %d
            successThreshold: %d
            failureThreshold: %d
`,
		normalized.Name,
		normalized.Namespace,
		normalized.Container,
		normalized.Port,
		normalized.Liveness.Path,
		normalized.Port,
		normalized.Liveness.InitialDelaySec,
		normalized.Liveness.PeriodSec,
		normalized.Liveness.TimeoutSec,
		normalized.Liveness.FailureThreshold,
		normalized.Readiness.Path,
		normalized.Port,
		normalized.Readiness.InitialDelaySec,
		normalized.Readiness.PeriodSec,
		normalized.Readiness.TimeoutSec,
		normalized.Readiness.SuccessThreshold,
		normalized.Readiness.FailureThreshold,
	)
	return manifest, nil
}

func ProbeHeartbeatTimeout(spec K8sProbeSpec) time.Duration {
	normalized, err := NormalizeK8sProbeSpec(spec)
	if err != nil {
		normalized = DefaultK8sProbeSpec()
	}
	seconds := int64(normalized.Liveness.PeriodSec) * int64(normalized.Liveness.FailureThreshold)
	if seconds <= 0 {
		seconds = int64(DefaultK8sProbeLivenessPeriodSec * DefaultK8sProbeLivenessFailureThreshold)
	}
	return time.Duration(seconds) * time.Second
}

func ProbeScanInterval(spec K8sProbeSpec) time.Duration {
	normalized, err := NormalizeK8sProbeSpec(spec)
	if err != nil {
		normalized = DefaultK8sProbeSpec()
	}
	period := time.Duration(normalized.Liveness.PeriodSec) * time.Second
	if period <= 0 {
		period = time.Duration(DefaultK8sProbeLivenessPeriodSec) * time.Second
	}
	if period > 5*time.Second {
		return 5 * time.Second
	}
	return period
}

func ProbeHeartbeatInterval(spec K8sProbeSpec) time.Duration {
	timeout := ProbeHeartbeatTimeout(spec)
	interval := timeout / 3
	if interval < time.Second {
		return time.Second
	}
	if interval > 10*time.Second {
		return 10 * time.Second
	}
	return interval
}

func mergeHTTPProbeSettings(base, incoming K8sHTTPProbeSettings) K8sHTTPProbeSettings {
	merged := base
	if path := strings.TrimSpace(incoming.Path); path != "" {
		merged.Path = ensureHTTPPath(path)
	}
	switch {
	case incoming.InitialDelaySec < 0:
		merged.InitialDelaySec = incoming.InitialDelaySec
	case incoming.InitialDelaySec > 0:
		merged.InitialDelaySec = incoming.InitialDelaySec
	}
	switch {
	case incoming.PeriodSec < 0:
		merged.PeriodSec = incoming.PeriodSec
	case incoming.PeriodSec > 0:
		merged.PeriodSec = incoming.PeriodSec
	}
	switch {
	case incoming.TimeoutSec < 0:
		merged.TimeoutSec = incoming.TimeoutSec
	case incoming.TimeoutSec > 0:
		merged.TimeoutSec = incoming.TimeoutSec
	}
	switch {
	case incoming.SuccessThreshold < 0:
		merged.SuccessThreshold = incoming.SuccessThreshold
	case incoming.SuccessThreshold > 0:
		merged.SuccessThreshold = incoming.SuccessThreshold
	}
	switch {
	case incoming.FailureThreshold < 0:
		merged.FailureThreshold = incoming.FailureThreshold
	case incoming.FailureThreshold > 0:
		merged.FailureThreshold = incoming.FailureThreshold
	}
	return merged
}

func validateHTTPProbeSettings(kind string, settings K8sHTTPProbeSettings) error {
	if strings.TrimSpace(settings.Path) == "" {
		return fmt.Errorf("%s probe path must not be empty", kind)
	}
	if settings.InitialDelaySec <= 0 {
		return fmt.Errorf("%s probe initial delay must be greater than 0", kind)
	}
	if settings.PeriodSec <= 0 {
		return fmt.Errorf("%s probe period must be greater than 0", kind)
	}
	if settings.TimeoutSec <= 0 {
		return fmt.Errorf("%s probe timeout must be greater than 0", kind)
	}
	if settings.SuccessThreshold <= 0 {
		return fmt.Errorf("%s probe success threshold must be greater than 0", kind)
	}
	if settings.FailureThreshold <= 0 {
		return fmt.Errorf("%s probe failure threshold must be greater than 0", kind)
	}
	if settings.TimeoutSec > settings.PeriodSec {
		return fmt.Errorf("%s probe timeout must be less than or equal to period", kind)
	}
	if kind == "liveness" && settings.SuccessThreshold != 1 {
		return fmt.Errorf("liveness probe success threshold must be 1")
	}
	return nil
}

func ensureHTTPPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "/"
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

func parsePositiveInt32ForProbe(raw string) (int32, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	if value > math.MaxInt32 {
		return 0, fmt.Errorf("value exceeds int32")
	}
	return int32(value), nil
}
