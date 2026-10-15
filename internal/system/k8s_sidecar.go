package system

import (
	"fmt"
	"os"
	"strings"
)

type K8sSidecarMode string

const (
	K8sSidecarModeBoth         K8sSidecarMode = "both"
	K8sSidecarModeLogCollector K8sSidecarMode = "log-collector"
	K8sSidecarModeProxy        K8sSidecarMode = "proxy"
)

const (
	DefaultK8sSidecarName                    = "ledgerbot-sidecar"
	DefaultK8sSidecarNamespace               = "default"
	DefaultK8sSidecarAppContainerName        = "ledgerbot"
	DefaultK8sSidecarAppContainerImage       = "ghcr.io/example/ledgerbot:latest"
	DefaultK8sSidecarAppContainerPort        = DefaultK8sDeploymentContainerPort
	DefaultK8sSidecarMode                    = K8sSidecarModeBoth
	DefaultK8sSidecarLogCollectorName        = "fluent-bit"
	DefaultK8sSidecarLogCollectorImage       = "fluent/fluent-bit:2.2.2"
	DefaultK8sSidecarProxyName               = "mesh-proxy"
	DefaultK8sSidecarProxyImage              = "envoyproxy/envoy:v1.31.2"
	DefaultK8sSidecarProxyPort         int32 = 15001
)

type K8sSidecarSpec struct {
	Name              string
	Namespace         string
	Mode              K8sSidecarMode
	AppContainerName  string
	AppContainerImage string
	AppContainerPort  int32
	LogCollectorName  string
	LogCollectorImage string
	ProxyName         string
	ProxyImage        string
	ProxyPort         int32
}

func DefaultK8sSidecarSpec() K8sSidecarSpec {
	return K8sSidecarSpec{
		Name:              DefaultK8sSidecarName,
		Namespace:         DefaultK8sSidecarNamespace,
		Mode:              DefaultK8sSidecarMode,
		AppContainerName:  DefaultK8sSidecarAppContainerName,
		AppContainerImage: DefaultK8sSidecarAppContainerImage,
		AppContainerPort:  DefaultK8sSidecarAppContainerPort,
		LogCollectorName:  DefaultK8sSidecarLogCollectorName,
		LogCollectorImage: DefaultK8sSidecarLogCollectorImage,
		ProxyName:         DefaultK8sSidecarProxyName,
		ProxyImage:        DefaultK8sSidecarProxyImage,
		ProxyPort:         DefaultK8sSidecarProxyPort,
	}
}

func NormalizeK8sSidecarSpec(spec K8sSidecarSpec) (K8sSidecarSpec, error) {
	normalized := DefaultK8sSidecarSpec()

	if name := strings.TrimSpace(spec.Name); name != "" {
		normalized.Name = name
	}
	if namespace := strings.TrimSpace(spec.Namespace); namespace != "" {
		normalized.Namespace = namespace
	}
	if appContainerName := strings.TrimSpace(spec.AppContainerName); appContainerName != "" {
		normalized.AppContainerName = appContainerName
	}
	if appContainerImage := strings.TrimSpace(spec.AppContainerImage); appContainerImage != "" {
		normalized.AppContainerImage = appContainerImage
	}
	if logCollectorName := strings.TrimSpace(spec.LogCollectorName); logCollectorName != "" {
		normalized.LogCollectorName = logCollectorName
	}
	if logCollectorImage := strings.TrimSpace(spec.LogCollectorImage); logCollectorImage != "" {
		normalized.LogCollectorImage = logCollectorImage
	}
	if proxyName := strings.TrimSpace(spec.ProxyName); proxyName != "" {
		normalized.ProxyName = proxyName
	}
	if proxyImage := strings.TrimSpace(spec.ProxyImage); proxyImage != "" {
		normalized.ProxyImage = proxyImage
	}

	modeRaw := strings.TrimSpace(string(spec.Mode))
	if modeRaw != "" {
		mode, err := parseK8sSidecarMode(modeRaw)
		if err != nil {
			return K8sSidecarSpec{}, err
		}
		normalized.Mode = mode
	}

	switch {
	case spec.AppContainerPort < 0:
		return K8sSidecarSpec{}, fmt.Errorf("sidecar app container port must be greater than 0")
	case spec.AppContainerPort > 0:
		normalized.AppContainerPort = spec.AppContainerPort
	}
	switch {
	case spec.ProxyPort < 0:
		return K8sSidecarSpec{}, fmt.Errorf("sidecar proxy port must be greater than 0")
	case spec.ProxyPort > 0:
		normalized.ProxyPort = spec.ProxyPort
	}

	if !k8sDNSLabelPattern.MatchString(normalized.Name) {
		return K8sSidecarSpec{}, fmt.Errorf("sidecar deployment name must be a valid k8s dns label")
	}
	if !k8sDNSLabelPattern.MatchString(normalized.Namespace) {
		return K8sSidecarSpec{}, fmt.Errorf("sidecar namespace must be a valid k8s dns label")
	}
	if !k8sDNSLabelPattern.MatchString(normalized.AppContainerName) {
		return K8sSidecarSpec{}, fmt.Errorf("app container name must be a valid k8s dns label")
	}
	if strings.TrimSpace(normalized.AppContainerImage) == "" {
		return K8sSidecarSpec{}, fmt.Errorf("app container image must not be empty")
	}
	if normalized.AppContainerPort <= 0 || normalized.AppContainerPort > 65535 {
		return K8sSidecarSpec{}, fmt.Errorf("sidecar app container port must be in range 1-65535")
	}
	if sidecarModeInjectsLogCollector(normalized.Mode) {
		if !k8sDNSLabelPattern.MatchString(normalized.LogCollectorName) {
			return K8sSidecarSpec{}, fmt.Errorf("log collector name must be a valid k8s dns label")
		}
		if strings.TrimSpace(normalized.LogCollectorImage) == "" {
			return K8sSidecarSpec{}, fmt.Errorf("log collector image must not be empty")
		}
	}
	if sidecarModeInjectsProxy(normalized.Mode) {
		if !k8sDNSLabelPattern.MatchString(normalized.ProxyName) {
			return K8sSidecarSpec{}, fmt.Errorf("proxy name must be a valid k8s dns label")
		}
		if strings.TrimSpace(normalized.ProxyImage) == "" {
			return K8sSidecarSpec{}, fmt.Errorf("proxy image must not be empty")
		}
		if normalized.ProxyPort <= 0 || normalized.ProxyPort > 65535 {
			return K8sSidecarSpec{}, fmt.Errorf("sidecar proxy port must be in range 1-65535")
		}
	}
	if err := validateDistinctSidecarContainerNames(normalized); err != nil {
		return K8sSidecarSpec{}, err
	}

	return normalized, nil
}

func LoadK8sSidecarSpecFromEnv(getenv func(string) string) (K8sSidecarSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	modeRaw := strings.TrimSpace(getenv("K8S_SIDECAR_MODE"))
	mode, modeErr := parseK8sSidecarMode(modeRaw)
	appPortRaw := strings.TrimSpace(getenv("K8S_SIDECAR_APP_PORT"))
	appPort, appPortErr := parsePositiveInt32(appPortRaw)
	proxyPortRaw := strings.TrimSpace(getenv("K8S_SIDECAR_PROXY_PORT"))
	proxyPort, proxyPortErr := parsePositiveInt32(proxyPortRaw)

	spec := K8sSidecarSpec{
		Name:              strings.TrimSpace(getenv("K8S_SIDECAR_NAME")),
		Namespace:         strings.TrimSpace(getenv("K8S_SIDECAR_NAMESPACE")),
		AppContainerName:  strings.TrimSpace(getenv("K8S_SIDECAR_APP_CONTAINER_NAME")),
		AppContainerImage: strings.TrimSpace(getenv("K8S_SIDECAR_APP_IMAGE")),
		LogCollectorName:  strings.TrimSpace(getenv("K8S_SIDECAR_LOG_COLLECTOR_NAME")),
		LogCollectorImage: strings.TrimSpace(getenv("K8S_SIDECAR_LOG_COLLECTOR_IMAGE")),
		ProxyName:         strings.TrimSpace(getenv("K8S_SIDECAR_PROXY_NAME")),
		ProxyImage:        strings.TrimSpace(getenv("K8S_SIDECAR_PROXY_IMAGE")),
	}
	if mode != "" {
		spec.Mode = mode
	}
	if appPort > 0 {
		spec.AppContainerPort = appPort
	}
	if proxyPort > 0 {
		spec.ProxyPort = proxyPort
	}

	normalized, err := NormalizeK8sSidecarSpec(spec)
	if err != nil {
		fallback, fallbackErr := NormalizeK8sSidecarSpec(K8sSidecarSpec{})
		if fallbackErr != nil {
			return K8sSidecarSpec{}, fmt.Errorf("normalize k8s sidecar defaults failed: %w", fallbackErr)
		}
		return fallback, fmt.Errorf("k8s sidecar config invalid, fallback to defaults: %w", err)
	}
	if modeErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SIDECAR_MODE=%q invalid, fallback to %s: %w",
			modeRaw,
			normalized.Mode,
			modeErr,
		)
	}
	if appPortErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SIDECAR_APP_PORT=%q invalid, fallback to %d: %w",
			appPortRaw,
			normalized.AppContainerPort,
			appPortErr,
		)
	}
	if proxyPortErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SIDECAR_PROXY_PORT=%q invalid, fallback to %d: %w",
			proxyPortRaw,
			normalized.ProxyPort,
			proxyPortErr,
		)
	}
	return normalized, nil
}

func BuildK8sSidecarManifest(spec K8sSidecarSpec) (string, error) {
	normalized, err := NormalizeK8sSidecarSpec(spec)
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	builder.WriteString("apiVersion: apps/v1\n")
	builder.WriteString("kind: Deployment\n")
	builder.WriteString("metadata:\n")
	builder.WriteString(fmt.Sprintf("  name: %s\n", normalized.Name))
	builder.WriteString(fmt.Sprintf("  namespace: %s\n", normalized.Namespace))
	builder.WriteString("spec:\n")
	builder.WriteString("  selector:\n")
	builder.WriteString("    matchLabels:\n")
	builder.WriteString(fmt.Sprintf("      app: %s\n", normalized.Name))
	builder.WriteString("  template:\n")
	builder.WriteString("    metadata:\n")
	builder.WriteString("      labels:\n")
	builder.WriteString(fmt.Sprintf("        app: %s\n", normalized.Name))
	builder.WriteString("    spec:\n")
	builder.WriteString("      containers:\n")
	builder.WriteString(fmt.Sprintf("        - name: %s\n", normalized.AppContainerName))
	builder.WriteString(fmt.Sprintf("          image: %s\n", normalized.AppContainerImage))
	builder.WriteString("          ports:\n")
	builder.WriteString(fmt.Sprintf("            - containerPort: %d\n", normalized.AppContainerPort))
	if sidecarModeInjectsLogCollector(normalized.Mode) {
		builder.WriteString(fmt.Sprintf("        - name: %s\n", normalized.LogCollectorName))
		builder.WriteString(fmt.Sprintf("          image: %s\n", normalized.LogCollectorImage))
		builder.WriteString("          args:\n")
		builder.WriteString("            - -c\n")
		builder.WriteString("            - /fluent-bit/etc/fluent-bit.conf\n")
	}
	if sidecarModeInjectsProxy(normalized.Mode) {
		builder.WriteString(fmt.Sprintf("        - name: %s\n", normalized.ProxyName))
		builder.WriteString(fmt.Sprintf("          image: %s\n", normalized.ProxyImage))
		builder.WriteString("          ports:\n")
		builder.WriteString(fmt.Sprintf("            - containerPort: %d\n", normalized.ProxyPort))
	}
	return builder.String(), nil
}

func sidecarModeInjectsLogCollector(mode K8sSidecarMode) bool {
	return mode == K8sSidecarModeBoth || mode == K8sSidecarModeLogCollector
}

func sidecarModeInjectsProxy(mode K8sSidecarMode) bool {
	return mode == K8sSidecarModeBoth || mode == K8sSidecarModeProxy
}

func parseK8sSidecarMode(raw string) (K8sSidecarMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "both", "all":
		return K8sSidecarModeBoth, nil
	case "log", "logs", "log-collector", "collector":
		return K8sSidecarModeLogCollector, nil
	case "proxy":
		return K8sSidecarModeProxy, nil
	default:
		return "", fmt.Errorf("unsupported sidecar mode %q", raw)
	}
}

func validateDistinctSidecarContainerNames(spec K8sSidecarSpec) error {
	containerOwners := map[string]string{
		spec.AppContainerName: "app container",
	}
	if sidecarModeInjectsLogCollector(spec.Mode) {
		if owner, exists := containerOwners[spec.LogCollectorName]; exists {
			return fmt.Errorf("container name %q duplicated between %s and log collector", spec.LogCollectorName, owner)
		}
		containerOwners[spec.LogCollectorName] = "log collector"
	}
	if sidecarModeInjectsProxy(spec.Mode) {
		if owner, exists := containerOwners[spec.ProxyName]; exists {
			return fmt.Errorf("container name %q duplicated between %s and proxy", spec.ProxyName, owner)
		}
	}
	return nil
}
