package system

import (
	"fmt"
	"os"
	"strings"
)

const (
	DefaultK8sCanaryReleaseName                   = "ledgerbot-rollout"
	DefaultK8sCanaryReleaseNamespace              = DefaultK8sDeploymentNamespace
	DefaultK8sCanaryReleaseTargetDeployment       = DefaultK8sDeploymentName
	DefaultK8sCanaryReleaseTotalReplicas    int32 = DefaultK8sDeploymentReplicas
	DefaultK8sCanaryReleasePercent          int32 = 10
	DefaultK8sCanaryReleaseObserveSec       int32 = 300
)

type K8sCanaryReleaseSpec struct {
	Name                 string
	Namespace            string
	TargetDeploymentName string
	TotalReplicas        int32
	CanaryPercent        int32
	ObserveSeconds       int32
}

type K8sCanaryRolloutPlan struct {
	TotalReplicas      int32
	CanaryReplicas     int32
	StableReplicas     int32
	CanaryPercent      int32
	ObservationSeconds int32
}

func DefaultK8sCanaryReleaseSpec() K8sCanaryReleaseSpec {
	return K8sCanaryReleaseSpec{
		Name:                 DefaultK8sCanaryReleaseName,
		Namespace:            DefaultK8sCanaryReleaseNamespace,
		TargetDeploymentName: DefaultK8sCanaryReleaseTargetDeployment,
		TotalReplicas:        DefaultK8sCanaryReleaseTotalReplicas,
		CanaryPercent:        DefaultK8sCanaryReleasePercent,
		ObserveSeconds:       DefaultK8sCanaryReleaseObserveSec,
	}
}

func NormalizeK8sCanaryReleaseSpec(spec K8sCanaryReleaseSpec) (K8sCanaryReleaseSpec, error) {
	normalized := DefaultK8sCanaryReleaseSpec()

	if name := strings.TrimSpace(spec.Name); name != "" {
		normalized.Name = name
	}
	if namespace := strings.TrimSpace(spec.Namespace); namespace != "" {
		normalized.Namespace = namespace
	}
	if target := strings.TrimSpace(spec.TargetDeploymentName); target != "" {
		normalized.TargetDeploymentName = target
	}

	switch {
	case spec.TotalReplicas < 0:
		return K8sCanaryReleaseSpec{}, fmt.Errorf("total replicas must be greater than 0")
	case spec.TotalReplicas > 0:
		normalized.TotalReplicas = spec.TotalReplicas
	}
	switch {
	case spec.CanaryPercent < 0:
		return K8sCanaryReleaseSpec{}, fmt.Errorf("canary percent must be in range 1-100")
	case spec.CanaryPercent > 0:
		normalized.CanaryPercent = spec.CanaryPercent
	}
	switch {
	case spec.ObserveSeconds < 0:
		return K8sCanaryReleaseSpec{}, fmt.Errorf("observe seconds must be greater than 0")
	case spec.ObserveSeconds > 0:
		normalized.ObserveSeconds = spec.ObserveSeconds
	}

	if !k8sDNSLabelPattern.MatchString(normalized.Name) {
		return K8sCanaryReleaseSpec{}, fmt.Errorf("canary release name must be a valid k8s dns label")
	}
	if !k8sDNSLabelPattern.MatchString(normalized.Namespace) {
		return K8sCanaryReleaseSpec{}, fmt.Errorf("canary release namespace must be a valid k8s dns label")
	}
	if !k8sDNSLabelPattern.MatchString(normalized.TargetDeploymentName) {
		return K8sCanaryReleaseSpec{}, fmt.Errorf("canary target deployment name must be a valid k8s dns label")
	}
	if normalized.TotalReplicas <= 0 {
		return K8sCanaryReleaseSpec{}, fmt.Errorf("total replicas must be greater than 0")
	}
	if normalized.CanaryPercent <= 0 || normalized.CanaryPercent > 100 {
		return K8sCanaryReleaseSpec{}, fmt.Errorf("canary percent must be in range 1-100")
	}
	if normalized.ObserveSeconds <= 0 {
		return K8sCanaryReleaseSpec{}, fmt.Errorf("observe seconds must be greater than 0")
	}

	return normalized, nil
}

func LoadK8sCanaryReleaseSpecFromEnv(getenv func(string) string) (K8sCanaryReleaseSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	totalReplicasRaw := strings.TrimSpace(getenv("K8S_CANARY_TOTAL_REPLICAS"))
	totalReplicas, totalReplicasErr := parsePositiveInt32(totalReplicasRaw)
	canaryPercentRaw := strings.TrimSpace(getenv("K8S_CANARY_PERCENT"))
	canaryPercent, canaryPercentErr := parseK8sCanaryPercent(canaryPercentRaw)
	observeSecRaw := strings.TrimSpace(getenv("K8S_CANARY_OBSERVATION_SECONDS"))
	observeSec, observeSecErr := parsePositiveInt32(observeSecRaw)

	spec := K8sCanaryReleaseSpec{
		Name:                 strings.TrimSpace(getenv("K8S_CANARY_NAME")),
		Namespace:            strings.TrimSpace(getenv("K8S_CANARY_NAMESPACE")),
		TargetDeploymentName: strings.TrimSpace(getenv("K8S_CANARY_TARGET_DEPLOYMENT")),
	}
	if totalReplicas > 0 {
		spec.TotalReplicas = totalReplicas
	}
	if canaryPercent > 0 {
		spec.CanaryPercent = canaryPercent
	}
	if observeSec > 0 {
		spec.ObserveSeconds = observeSec
	}

	normalized, err := NormalizeK8sCanaryReleaseSpec(spec)
	if err != nil {
		fallback, fallbackErr := NormalizeK8sCanaryReleaseSpec(K8sCanaryReleaseSpec{})
		if fallbackErr != nil {
			return K8sCanaryReleaseSpec{}, fmt.Errorf("normalize k8s canary release defaults failed: %w", fallbackErr)
		}
		return fallback, fmt.Errorf("k8s canary release config invalid, fallback to defaults: %w", err)
	}
	if totalReplicasErr != nil {
		return normalized, fmt.Errorf(
			"K8S_CANARY_TOTAL_REPLICAS=%q invalid, fallback to %d: %w",
			totalReplicasRaw,
			normalized.TotalReplicas,
			totalReplicasErr,
		)
	}
	if canaryPercentErr != nil {
		return normalized, fmt.Errorf(
			"K8S_CANARY_PERCENT=%q invalid, fallback to %d: %w",
			canaryPercentRaw,
			normalized.CanaryPercent,
			canaryPercentErr,
		)
	}
	if observeSecErr != nil {
		return normalized, fmt.Errorf(
			"K8S_CANARY_OBSERVATION_SECONDS=%q invalid, fallback to %d: %w",
			observeSecRaw,
			normalized.ObserveSeconds,
			observeSecErr,
		)
	}
	return normalized, nil
}

func BuildK8sCanaryRolloutPlan(spec K8sCanaryReleaseSpec) (K8sCanaryRolloutPlan, error) {
	normalized, err := NormalizeK8sCanaryReleaseSpec(spec)
	if err != nil {
		return K8sCanaryRolloutPlan{}, err
	}

	canaryReplicas := computeCanaryReplicas(normalized.TotalReplicas, normalized.CanaryPercent)
	return K8sCanaryRolloutPlan{
		TotalReplicas:      normalized.TotalReplicas,
		CanaryReplicas:     canaryReplicas,
		StableReplicas:     normalized.TotalReplicas - canaryReplicas,
		CanaryPercent:      normalized.CanaryPercent,
		ObservationSeconds: normalized.ObserveSeconds,
	}, nil
}

func BuildK8sCanaryReleaseManifest(spec K8sCanaryReleaseSpec) (string, error) {
	normalized, err := NormalizeK8sCanaryReleaseSpec(spec)
	if err != nil {
		return "", err
	}
	plan, err := BuildK8sCanaryRolloutPlan(normalized)
	if err != nil {
		return "", err
	}

	manifest := fmt.Sprintf(`apiVersion: argoproj.io/v1alpha1
kind: Rollout
metadata:
  name: %s
  namespace: %s
spec:
  replicas: %d
  workloadRef:
    apiVersion: apps/v1
    kind: Deployment
    name: %s
  strategy:
    canary:
      canaryService: %s-canary
      stableService: %s-stable
      maxSurge: 0
      maxUnavailable: 0
      steps:
        - setCanaryScale:
            replicas: %d
        - setWeight: %d
        - pause:
            duration: %ds
        - setWeight: 100
`,
		normalized.Name,
		normalized.Namespace,
		plan.TotalReplicas,
		normalized.TargetDeploymentName,
		normalized.TargetDeploymentName,
		normalized.TargetDeploymentName,
		plan.CanaryReplicas,
		plan.CanaryPercent,
		plan.ObservationSeconds,
	)
	return manifest, nil
}

func parseK8sCanaryPercent(raw string) (int32, error) {
	value, err := parsePositiveInt32(raw)
	if err != nil || value == 0 {
		return value, err
	}
	if value > 100 {
		return 0, fmt.Errorf("must be in range 1-100")
	}
	return value, nil
}

func computeCanaryReplicas(totalReplicas, canaryPercent int32) int32 {
	desired := (int64(totalReplicas)*int64(canaryPercent) + 99) / 100
	if desired < 1 {
		desired = 1
	}
	if desired > int64(totalReplicas) {
		desired = int64(totalReplicas)
	}
	return int32(desired)
}
