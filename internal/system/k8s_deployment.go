package system

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultK8sDeploymentName                 = "ledgerbot"
	DefaultK8sDeploymentNamespace            = "default"
	DefaultK8sDeploymentContainerName        = "ledgerbot"
	DefaultK8sDeploymentContainerImage       = "ghcr.io/example/ledgerbot:latest"
	DefaultK8sDeploymentContainerPort  int32 = 8080
	DefaultK8sDeploymentReplicas       int32 = 2
	DefaultK8sDeploymentCPURequest           = "250m"
	DefaultK8sDeploymentCPULimit             = "1"
	DefaultK8sDeploymentMemoryRequest        = "256Mi"
	DefaultK8sDeploymentMemoryLimit          = "512Mi"
)

type K8sDeploymentSpec struct {
	Name           string
	Namespace      string
	ContainerName  string
	ContainerImage string
	ContainerPort  int32
	Replicas       int32
	CPURequest     string
	CPULimit       string
	MemoryRequest  string
	MemoryLimit    string
}

func DefaultK8sDeploymentSpec() K8sDeploymentSpec {
	return K8sDeploymentSpec{
		Name:           DefaultK8sDeploymentName,
		Namespace:      DefaultK8sDeploymentNamespace,
		ContainerName:  DefaultK8sDeploymentContainerName,
		ContainerImage: DefaultK8sDeploymentContainerImage,
		ContainerPort:  DefaultK8sDeploymentContainerPort,
		Replicas:       DefaultK8sDeploymentReplicas,
		CPURequest:     DefaultK8sDeploymentCPURequest,
		CPULimit:       DefaultK8sDeploymentCPULimit,
		MemoryRequest:  DefaultK8sDeploymentMemoryRequest,
		MemoryLimit:    DefaultK8sDeploymentMemoryLimit,
	}
}

func NormalizeK8sDeploymentSpec(spec K8sDeploymentSpec) (K8sDeploymentSpec, error) {
	normalized := DefaultK8sDeploymentSpec()

	if name := strings.TrimSpace(spec.Name); name != "" {
		normalized.Name = name
	}
	if namespace := strings.TrimSpace(spec.Namespace); namespace != "" {
		normalized.Namespace = namespace
	}
	if containerName := strings.TrimSpace(spec.ContainerName); containerName != "" {
		normalized.ContainerName = containerName
	}
	if containerImage := strings.TrimSpace(spec.ContainerImage); containerImage != "" {
		normalized.ContainerImage = containerImage
	}

	switch {
	case spec.ContainerPort < 0:
		return K8sDeploymentSpec{}, fmt.Errorf("container port must be greater than 0")
	case spec.ContainerPort > 0:
		normalized.ContainerPort = spec.ContainerPort
	}

	switch {
	case spec.Replicas < 0:
		return K8sDeploymentSpec{}, fmt.Errorf("replicas must be greater than 0")
	case spec.Replicas > 0:
		normalized.Replicas = spec.Replicas
	}

	if cpuRequest := strings.TrimSpace(spec.CPURequest); cpuRequest != "" {
		normalized.CPURequest = cpuRequest
	}
	if cpuLimit := strings.TrimSpace(spec.CPULimit); cpuLimit != "" {
		normalized.CPULimit = cpuLimit
	}
	if memoryRequest := strings.TrimSpace(spec.MemoryRequest); memoryRequest != "" {
		normalized.MemoryRequest = memoryRequest
	}
	if memoryLimit := strings.TrimSpace(spec.MemoryLimit); memoryLimit != "" {
		normalized.MemoryLimit = memoryLimit
	}

	if normalized.Replicas <= 0 {
		return K8sDeploymentSpec{}, fmt.Errorf("replicas must be greater than 0")
	}
	if normalized.ContainerPort <= 0 {
		return K8sDeploymentSpec{}, fmt.Errorf("container port must be greater than 0")
	}
	if strings.TrimSpace(normalized.CPURequest) == "" || strings.TrimSpace(normalized.CPULimit) == "" {
		return K8sDeploymentSpec{}, fmt.Errorf("cpu request/limit must not be empty")
	}
	if strings.TrimSpace(normalized.MemoryRequest) == "" || strings.TrimSpace(normalized.MemoryLimit) == "" {
		return K8sDeploymentSpec{}, fmt.Errorf("memory request/limit must not be empty")
	}
	if strings.TrimSpace(normalized.Name) == "" || strings.TrimSpace(normalized.Namespace) == "" {
		return K8sDeploymentSpec{}, fmt.Errorf("deployment name/namespace must not be empty")
	}
	if strings.TrimSpace(normalized.ContainerName) == "" || strings.TrimSpace(normalized.ContainerImage) == "" {
		return K8sDeploymentSpec{}, fmt.Errorf("container name/image must not be empty")
	}

	return normalized, nil
}

func LoadK8sDeploymentSpecFromEnv(getenv func(string) string) (K8sDeploymentSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	replicasRaw := strings.TrimSpace(getenv("K8S_DEPLOYMENT_REPLICAS"))
	replicas, replicasErr := parsePositiveInt32(replicasRaw)
	containerPortRaw := strings.TrimSpace(getenv("K8S_DEPLOYMENT_CONTAINER_PORT"))
	containerPort, containerPortErr := parsePositiveInt32(containerPortRaw)

	spec := K8sDeploymentSpec{
		Name:           strings.TrimSpace(getenv("K8S_DEPLOYMENT_NAME")),
		Namespace:      strings.TrimSpace(getenv("K8S_DEPLOYMENT_NAMESPACE")),
		ContainerName:  strings.TrimSpace(getenv("K8S_DEPLOYMENT_CONTAINER_NAME")),
		ContainerImage: strings.TrimSpace(getenv("K8S_DEPLOYMENT_IMAGE")),
		CPURequest:     strings.TrimSpace(getenv("K8S_DEPLOYMENT_CPU_REQUEST")),
		CPULimit:       strings.TrimSpace(getenv("K8S_DEPLOYMENT_CPU_LIMIT")),
		MemoryRequest:  strings.TrimSpace(getenv("K8S_DEPLOYMENT_MEM_REQUEST")),
		MemoryLimit:    strings.TrimSpace(getenv("K8S_DEPLOYMENT_MEM_LIMIT")),
	}
	if replicas > 0 {
		spec.Replicas = replicas
	}
	if containerPort > 0 {
		spec.ContainerPort = containerPort
	}

	normalized, err := NormalizeK8sDeploymentSpec(spec)
	if err != nil {
		fallback, fallbackErr := NormalizeK8sDeploymentSpec(K8sDeploymentSpec{})
		if fallbackErr != nil {
			return K8sDeploymentSpec{}, fmt.Errorf("normalize k8s deployment defaults failed: %w", fallbackErr)
		}
		return fallback, fmt.Errorf("k8s deployment config invalid, fallback to defaults: %w", err)
	}
	if replicasErr != nil {
		return normalized, fmt.Errorf(
			"K8S_DEPLOYMENT_REPLICAS=%q invalid, fallback to %d: %w",
			replicasRaw,
			normalized.Replicas,
			replicasErr,
		)
	}
	if containerPortErr != nil {
		return normalized, fmt.Errorf(
			"K8S_DEPLOYMENT_CONTAINER_PORT=%q invalid, fallback to %d: %w",
			containerPortRaw,
			normalized.ContainerPort,
			containerPortErr,
		)
	}
	return normalized, nil
}

func BuildK8sDeploymentManifest(spec K8sDeploymentSpec) (string, error) {
	normalized, err := NormalizeK8sDeploymentSpec(spec)
	if err != nil {
		return "", err
	}

	manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
        - name: %s
          image: %s
          ports:
            - containerPort: %d
          resources:
            requests:
              cpu: "%s"
              memory: "%s"
            limits:
              cpu: "%s"
              memory: "%s"
`,
		normalized.Name,
		normalized.Namespace,
		normalized.Replicas,
		normalized.Name,
		normalized.Name,
		normalized.ContainerName,
		normalized.ContainerImage,
		normalized.ContainerPort,
		normalized.CPURequest,
		normalized.MemoryRequest,
		normalized.CPULimit,
		normalized.MemoryLimit,
	)
	return manifest, nil
}

func parsePositiveInt32(raw string) (int32, error) {
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
