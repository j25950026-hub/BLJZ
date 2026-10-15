package system

import (
	"fmt"
	"os"
	"strings"
)

const (
	DefaultK8sHPAName                       = "ledgerbot-hpa"
	DefaultK8sHPANamespace                  = "default"
	DefaultK8sHPATargetDeploymentName       = "ledgerbot"
	DefaultK8sHPAMinReplicas          int32 = 2
	DefaultK8sHPAMaxReplicas          int32 = 10
	DefaultK8sHPATargetCPUUsage       int32 = 70
	DefaultK8sHPATargetQPSPerPod      int32 = 120
)

type K8sHPASpec struct {
	Name                 string
	Namespace            string
	TargetDeploymentName string
	MinReplicas          int32
	MaxReplicas          int32
	TargetCPUUsage       int32
	TargetQPSPerPod      int32
}

func DefaultK8sHPASpec() K8sHPASpec {
	return K8sHPASpec{
		Name:                 DefaultK8sHPAName,
		Namespace:            DefaultK8sHPANamespace,
		TargetDeploymentName: DefaultK8sHPATargetDeploymentName,
		MinReplicas:          DefaultK8sHPAMinReplicas,
		MaxReplicas:          DefaultK8sHPAMaxReplicas,
		TargetCPUUsage:       DefaultK8sHPATargetCPUUsage,
		TargetQPSPerPod:      DefaultK8sHPATargetQPSPerPod,
	}
}

func NormalizeK8sHPASpec(spec K8sHPASpec) (K8sHPASpec, error) {
	normalized := DefaultK8sHPASpec()

	if name := strings.TrimSpace(spec.Name); name != "" {
		normalized.Name = name
	}
	if namespace := strings.TrimSpace(spec.Namespace); namespace != "" {
		normalized.Namespace = namespace
	}
	if targetDeploymentName := strings.TrimSpace(spec.TargetDeploymentName); targetDeploymentName != "" {
		normalized.TargetDeploymentName = targetDeploymentName
	}

	switch {
	case spec.MinReplicas < 0:
		return K8sHPASpec{}, fmt.Errorf("min replicas must be greater than 0")
	case spec.MinReplicas > 0:
		normalized.MinReplicas = spec.MinReplicas
	}
	switch {
	case spec.MaxReplicas < 0:
		return K8sHPASpec{}, fmt.Errorf("max replicas must be greater than 0")
	case spec.MaxReplicas > 0:
		normalized.MaxReplicas = spec.MaxReplicas
	}
	switch {
	case spec.TargetCPUUsage < 0:
		return K8sHPASpec{}, fmt.Errorf("target cpu usage must be greater than 0")
	case spec.TargetCPUUsage > 0:
		normalized.TargetCPUUsage = spec.TargetCPUUsage
	}
	switch {
	case spec.TargetQPSPerPod < 0:
		return K8sHPASpec{}, fmt.Errorf("target qps per pod must be greater than 0")
	case spec.TargetQPSPerPod > 0:
		normalized.TargetQPSPerPod = spec.TargetQPSPerPod
	}

	if normalized.MinReplicas <= 0 {
		return K8sHPASpec{}, fmt.Errorf("min replicas must be greater than 0")
	}
	if normalized.MaxReplicas <= 0 {
		return K8sHPASpec{}, fmt.Errorf("max replicas must be greater than 0")
	}
	if normalized.MaxReplicas < normalized.MinReplicas {
		return K8sHPASpec{}, fmt.Errorf("max replicas must be greater than or equal to min replicas")
	}
	if normalized.TargetCPUUsage <= 0 || normalized.TargetCPUUsage > 100 {
		return K8sHPASpec{}, fmt.Errorf("target cpu usage must be in range 1-100")
	}
	if normalized.TargetQPSPerPod <= 0 {
		return K8sHPASpec{}, fmt.Errorf("target qps per pod must be greater than 0")
	}
	if strings.TrimSpace(normalized.Name) == "" || strings.TrimSpace(normalized.Namespace) == "" {
		return K8sHPASpec{}, fmt.Errorf("hpa name/namespace must not be empty")
	}
	if strings.TrimSpace(normalized.TargetDeploymentName) == "" {
		return K8sHPASpec{}, fmt.Errorf("target deployment name must not be empty")
	}

	return normalized, nil
}

func LoadK8sHPASpecFromEnv(getenv func(string) string) (K8sHPASpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	minReplicasRaw := strings.TrimSpace(getenv("K8S_HPA_MIN_REPLICAS"))
	minReplicas, minReplicasErr := parsePositiveInt32(minReplicasRaw)
	maxReplicasRaw := strings.TrimSpace(getenv("K8S_HPA_MAX_REPLICAS"))
	maxReplicas, maxReplicasErr := parsePositiveInt32(maxReplicasRaw)
	targetCPURaw := strings.TrimSpace(getenv("K8S_HPA_TARGET_CPU"))
	targetCPU, targetCPUErr := parsePositiveInt32(targetCPURaw)
	targetQPSRaw := strings.TrimSpace(getenv("K8S_HPA_TARGET_QPS_PER_POD"))
	targetQPS, targetQPSErr := parsePositiveInt32(targetQPSRaw)

	spec := K8sHPASpec{
		Name:                 strings.TrimSpace(getenv("K8S_HPA_NAME")),
		Namespace:            strings.TrimSpace(getenv("K8S_HPA_NAMESPACE")),
		TargetDeploymentName: strings.TrimSpace(getenv("K8S_HPA_TARGET_DEPLOYMENT")),
	}
	if minReplicas > 0 {
		spec.MinReplicas = minReplicas
	}
	if maxReplicas > 0 {
		spec.MaxReplicas = maxReplicas
	}
	if targetCPU > 0 {
		spec.TargetCPUUsage = targetCPU
	}
	if targetQPS > 0 {
		spec.TargetQPSPerPod = targetQPS
	}

	normalized, err := NormalizeK8sHPASpec(spec)
	if err != nil {
		fallback, fallbackErr := NormalizeK8sHPASpec(K8sHPASpec{})
		if fallbackErr != nil {
			return K8sHPASpec{}, fmt.Errorf("normalize k8s hpa defaults failed: %w", fallbackErr)
		}
		return fallback, fmt.Errorf("k8s hpa config invalid, fallback to defaults: %w", err)
	}
	if minReplicasErr != nil {
		return normalized, fmt.Errorf(
			"K8S_HPA_MIN_REPLICAS=%q invalid, fallback to %d: %w",
			minReplicasRaw,
			normalized.MinReplicas,
			minReplicasErr,
		)
	}
	if maxReplicasErr != nil {
		return normalized, fmt.Errorf(
			"K8S_HPA_MAX_REPLICAS=%q invalid, fallback to %d: %w",
			maxReplicasRaw,
			normalized.MaxReplicas,
			maxReplicasErr,
		)
	}
	if targetCPUErr != nil {
		return normalized, fmt.Errorf(
			"K8S_HPA_TARGET_CPU=%q invalid, fallback to %d: %w",
			targetCPURaw,
			normalized.TargetCPUUsage,
			targetCPUErr,
		)
	}
	if targetQPSErr != nil {
		return normalized, fmt.Errorf(
			"K8S_HPA_TARGET_QPS_PER_POD=%q invalid, fallback to %d: %w",
			targetQPSRaw,
			normalized.TargetQPSPerPod,
			targetQPSErr,
		)
	}
	return normalized, nil
}

func BuildK8sHPAManifest(spec K8sHPASpec) (string, error) {
	normalized, err := NormalizeK8sHPASpec(spec)
	if err != nil {
		return "", err
	}

	manifest := fmt.Sprintf(`apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: %s
  namespace: %s
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: %s
  minReplicas: %d
  maxReplicas: %d
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: %d
    - type: Pods
      pods:
        metric:
          name: requests_per_second
        target:
          type: AverageValue
          averageValue: "%d"
`,
		normalized.Name,
		normalized.Namespace,
		normalized.TargetDeploymentName,
		normalized.MinReplicas,
		normalized.MaxReplicas,
		normalized.TargetCPUUsage,
		normalized.TargetQPSPerPod,
	)
	return manifest, nil
}

func RecommendHPAReplicaCount(
	spec K8sHPASpec,
	currentReplicas int32,
	averageCPUUsage int32,
	totalQPS int32,
) (int32, error) {
	normalized, err := NormalizeK8sHPASpec(spec)
	if err != nil {
		return 0, err
	}

	if currentReplicas <= 0 {
		return 0, fmt.Errorf("current replicas must be greater than 0")
	}
	if averageCPUUsage < 0 {
		return 0, fmt.Errorf("average cpu usage must be greater than or equal to 0")
	}
	if totalQPS < 0 {
		return 0, fmt.Errorf("total qps must be greater than or equal to 0")
	}

	cpuDesired := ceilDivideInt64(
		int64(currentReplicas)*int64(averageCPUUsage),
		int64(normalized.TargetCPUUsage),
	)
	qpsDesired := ceilDivideInt64(
		int64(totalQPS),
		int64(normalized.TargetQPSPerPod),
	)

	desired := cpuDesired
	if qpsDesired > desired {
		desired = qpsDesired
	}
	if desired < int64(normalized.MinReplicas) {
		desired = int64(normalized.MinReplicas)
	}
	if desired > int64(normalized.MaxReplicas) {
		desired = int64(normalized.MaxReplicas)
	}
	return int32(desired), nil
}

func ceilDivideInt64(numerator, denominator int64) int64 {
	if numerator <= 0 {
		return 0
	}
	return (numerator + denominator - 1) / denominator
}
