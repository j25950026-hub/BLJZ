package system

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultK8sConfigMapName = "ledgerbot-config"
	DefaultK8sSecretName    = "ledgerbot-secret"
)

var defaultK8sConfigMapData = map[string]string{
	"ADMIN_ADDR":                  ":8090",
	"BANK_BRANCH_API_HEADER":      "Authorization",
	"BANK_BRANCH_API_TIMEOUT_SEC": "8",
	"BANK_BRANCH_API_URL":         "",
	"BOT_TIMEZONE":                "Asia/Shanghai",
	"LOG_LEVEL":                   "INFO",
	"TG_WEBAPP_URL":               "",
}

var defaultK8sSecretData = map[string]string{
	"ADMIN_TOKEN":            "",
	"BANK_BRANCH_API_TOKEN":  "",
	"BOT_TOKEN":              "",
	"DB_DATA_ENCRYPTION_KEY": "",
	"TG_WEBAPP_SECRET":       "",
	"WEBHOOK_SECRET_TOKEN":   "",
}

var (
	k8sDNSLabelPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	envVarNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type K8sConfigMapSecretSpec struct {
	Namespace     string
	ConfigMapName string
	SecretName    string
	ConfigMapData map[string]string
	SecretData    map[string]string
}

func DefaultK8sConfigMapSecretSpec() K8sConfigMapSecretSpec {
	return K8sConfigMapSecretSpec{
		Namespace:     DefaultK8sDeploymentNamespace,
		ConfigMapName: DefaultK8sConfigMapName,
		SecretName:    DefaultK8sSecretName,
		ConfigMapData: cloneStringMap(defaultK8sConfigMapData),
		SecretData:    cloneStringMap(defaultK8sSecretData),
	}
}

func NormalizeK8sConfigMapSecretSpec(spec K8sConfigMapSecretSpec) (K8sConfigMapSecretSpec, error) {
	normalized := DefaultK8sConfigMapSecretSpec()

	if namespace := strings.TrimSpace(spec.Namespace); namespace != "" {
		normalized.Namespace = namespace
	}
	if configMapName := strings.TrimSpace(spec.ConfigMapName); configMapName != "" {
		normalized.ConfigMapName = configMapName
	}
	if secretName := strings.TrimSpace(spec.SecretName); secretName != "" {
		normalized.SecretName = secretName
	}

	if len(spec.ConfigMapData) > 0 {
		configMapData, err := normalizeEnvDataMap(spec.ConfigMapData)
		if err != nil {
			return K8sConfigMapSecretSpec{}, fmt.Errorf("invalid configmap data: %w", err)
		}
		normalized.ConfigMapData = configMapData
	}
	if len(spec.SecretData) > 0 {
		secretData, err := normalizeEnvDataMap(spec.SecretData)
		if err != nil {
			return K8sConfigMapSecretSpec{}, fmt.Errorf("invalid secret data: %w", err)
		}
		normalized.SecretData = secretData
	}

	if !k8sDNSLabelPattern.MatchString(normalized.Namespace) {
		return K8sConfigMapSecretSpec{}, fmt.Errorf("namespace must be a valid k8s dns label")
	}
	if !k8sDNSLabelPattern.MatchString(normalized.ConfigMapName) {
		return K8sConfigMapSecretSpec{}, fmt.Errorf("configmap name must be a valid k8s dns label")
	}
	if !k8sDNSLabelPattern.MatchString(normalized.SecretName) {
		return K8sConfigMapSecretSpec{}, fmt.Errorf("secret name must be a valid k8s dns label")
	}
	if len(normalized.ConfigMapData) == 0 {
		return K8sConfigMapSecretSpec{}, fmt.Errorf("configmap data must not be empty")
	}
	if len(normalized.SecretData) == 0 {
		return K8sConfigMapSecretSpec{}, fmt.Errorf("secret data must not be empty")
	}
	for key := range normalized.ConfigMapData {
		if _, exists := normalized.SecretData[key]; exists {
			return K8sConfigMapSecretSpec{}, fmt.Errorf("env key %q must not exist in both ConfigMap and Secret", key)
		}
	}

	return normalized, nil
}

func LoadK8sConfigMapSecretSpecFromEnv(getenv func(string) string) (K8sConfigMapSecretSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	configMapKeysRaw := strings.TrimSpace(getenv("K8S_CONFIGMAP_KEYS"))
	configMapKeys, configMapKeysErr := parseEnvKeyList(configMapKeysRaw)
	if len(configMapKeys) == 0 {
		configMapKeys = sortedMapKeys(defaultK8sConfigMapData)
	}

	secretKeysRaw := strings.TrimSpace(getenv("K8S_SECRET_KEYS"))
	secretKeys, secretKeysErr := parseEnvKeyList(secretKeysRaw)
	if len(secretKeys) == 0 {
		secretKeys = sortedMapKeys(defaultK8sSecretData)
	}

	namespace := strings.TrimSpace(getenv("K8S_CONFIGMAP_SECRET_NAMESPACE"))
	if namespace == "" {
		namespace = strings.TrimSpace(getenv("K8S_DEPLOYMENT_NAMESPACE"))
	}

	spec := K8sConfigMapSecretSpec{
		Namespace:     namespace,
		ConfigMapName: strings.TrimSpace(getenv("K8S_CONFIGMAP_NAME")),
		SecretName:    strings.TrimSpace(getenv("K8S_SECRET_NAME")),
		ConfigMapData: collectEnvData(configMapKeys, getenv, defaultK8sConfigMapData),
		SecretData:    collectEnvData(secretKeys, getenv, defaultK8sSecretData),
	}

	normalized, err := NormalizeK8sConfigMapSecretSpec(spec)
	if err != nil {
		fallback, fallbackErr := NormalizeK8sConfigMapSecretSpec(K8sConfigMapSecretSpec{})
		if fallbackErr != nil {
			return K8sConfigMapSecretSpec{}, fmt.Errorf("normalize k8s configmap/secret defaults failed: %w", fallbackErr)
		}
		return fallback, fmt.Errorf("k8s configmap/secret invalid, fallback to defaults: %w", err)
	}

	if configMapKeysErr != nil {
		return normalized, fmt.Errorf(
			"K8S_CONFIGMAP_KEYS=%q invalid, fallback to default keys: %w",
			configMapKeysRaw,
			configMapKeysErr,
		)
	}
	if secretKeysErr != nil {
		return normalized, fmt.Errorf(
			"K8S_SECRET_KEYS=%q invalid, fallback to default keys: %w",
			secretKeysRaw,
			secretKeysErr,
		)
	}
	return normalized, nil
}

func BuildK8sConfigMapManifest(spec K8sConfigMapSecretSpec) (string, error) {
	normalized, err := NormalizeK8sConfigMapSecretSpec(spec)
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	builder.WriteString("apiVersion: v1\n")
	builder.WriteString("kind: ConfigMap\n")
	builder.WriteString("metadata:\n")
	builder.WriteString(fmt.Sprintf("  name: %s\n", normalized.ConfigMapName))
	builder.WriteString(fmt.Sprintf("  namespace: %s\n", normalized.Namespace))
	builder.WriteString("data:\n")
	for _, key := range sortedMapKeys(normalized.ConfigMapData) {
		builder.WriteString(fmt.Sprintf("  %s: %s\n", key, strconv.Quote(normalized.ConfigMapData[key])))
	}
	return builder.String(), nil
}

func BuildK8sSecretManifest(spec K8sConfigMapSecretSpec) (string, error) {
	normalized, err := NormalizeK8sConfigMapSecretSpec(spec)
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	builder.WriteString("apiVersion: v1\n")
	builder.WriteString("kind: Secret\n")
	builder.WriteString("metadata:\n")
	builder.WriteString(fmt.Sprintf("  name: %s\n", normalized.SecretName))
	builder.WriteString(fmt.Sprintf("  namespace: %s\n", normalized.Namespace))
	builder.WriteString("type: Opaque\n")
	builder.WriteString("stringData:\n")
	for _, key := range sortedMapKeys(normalized.SecretData) {
		builder.WriteString(fmt.Sprintf("  %s: %s\n", key, strconv.Quote(normalized.SecretData[key])))
	}
	return builder.String(), nil
}

func BuildK8sConfigMapSecretManifests(spec K8sConfigMapSecretSpec) (string, error) {
	configMapManifest, err := BuildK8sConfigMapManifest(spec)
	if err != nil {
		return "", err
	}
	secretManifest, err := BuildK8sSecretManifest(spec)
	if err != nil {
		return "", err
	}
	return configMapManifest + "---\n" + secretManifest, nil
}

func parseEnvKeyList(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	keys := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key == "" {
			continue
		}
		if !envVarNamePattern.MatchString(key) {
			return nil, fmt.Errorf("invalid env key %q", key)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("key list must contain at least one valid env key")
	}
	return keys, nil
}

func collectEnvData(keys []string, getenv func(string) string, defaults map[string]string) map[string]string {
	data := make(map[string]string, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			value = strings.TrimSpace(defaults[key])
		}
		data[key] = value
	}
	return data
}

func normalizeEnvDataMap(raw map[string]string) (map[string]string, error) {
	normalized := make(map[string]string, len(raw))
	for key, value := range raw {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return nil, fmt.Errorf("env key must not be empty")
		}
		if !envVarNamePattern.MatchString(trimmedKey) {
			return nil, fmt.Errorf("invalid env key %q", trimmedKey)
		}
		if _, exists := normalized[trimmedKey]; exists {
			return nil, fmt.Errorf("duplicate env key %q", trimmedKey)
		}
		normalized[trimmedKey] = strings.TrimSpace(value)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("env data must not be empty")
	}
	return normalized, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
