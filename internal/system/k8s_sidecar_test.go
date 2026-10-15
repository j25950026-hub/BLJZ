package system

import (
	"strings"
	"testing"
)

func TestControlPoint515SidecarModeInjection(t *testing.T) {
	t.Run("控制点 5.1.5 功能验证", func(t *testing.T) {
		spec, err := LoadK8sSidecarSpecFromEnv(func(key string) string {
			switch key {
			case "K8S_SIDECAR_NAME":
				return "ledgerbot-sidecar-prod"
			case "K8S_SIDECAR_NAMESPACE":
				return "finance"
			case "K8S_SIDECAR_MODE":
				return "both"
			case "K8S_SIDECAR_APP_CONTAINER_NAME":
				return "ledgerbot-api"
			case "K8S_SIDECAR_APP_IMAGE":
				return "registry.example.com/ledgerbot:v2.0.1"
			case "K8S_SIDECAR_APP_PORT":
				return "8090"
			case "K8S_SIDECAR_LOG_COLLECTOR_NAME":
				return "fluent-bit"
			case "K8S_SIDECAR_LOG_COLLECTOR_IMAGE":
				return "fluent/fluent-bit:2.2.2"
			case "K8S_SIDECAR_PROXY_NAME":
				return "envoy-proxy"
			case "K8S_SIDECAR_PROXY_IMAGE":
				return "envoyproxy/envoy:v1.31.2"
			case "K8S_SIDECAR_PROXY_PORT":
				return "15021"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 k8s sidecar 配置失败: %v", err)
		}
		if spec.Mode != K8sSidecarModeBoth {
			t.Fatalf("sidecar 模式错误: want=%s got=%s", K8sSidecarModeBoth, spec.Mode)
		}
		if spec.AppContainerPort != 8090 || spec.ProxyPort != 15021 {
			t.Fatalf("sidecar 端口错误: %+v", spec)
		}

		manifest, err := BuildK8sSidecarManifest(spec)
		if err != nil {
			t.Fatalf("生成 sidecar 清单失败: %v", err)
		}
		required := []string{
			"kind: Deployment",
			"namespace: finance",
			"- name: ledgerbot-api",
			"- name: fluent-bit",
			"image: fluent/fluent-bit:2.2.2",
			"- name: envoy-proxy",
			"containerPort: 15021",
		}
		for _, fragment := range required {
			if !strings.Contains(manifest, fragment) {
				t.Fatalf("sidecar 清单缺少关键片段: %q\nmanifest=%s", fragment, manifest)
			}
		}
	})
}

func TestLoadK8sSidecarSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadK8sSidecarSpecFromEnv(func(key string) string {
		if key == "K8S_SIDECAR_MODE" {
			return "invalid-mode"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("非法 sidecar 模式应返回回退告警")
	}
	if !strings.Contains(err.Error(), "K8S_SIDECAR_MODE") {
		t.Fatalf("错误信息应包含非法环境变量名: %v", err)
	}
	if spec.Mode != DefaultK8sSidecarMode {
		t.Fatalf("非法 sidecar 模式应回退默认值: want=%s got=%s", DefaultK8sSidecarMode, spec.Mode)
	}
}

func TestNormalizeK8sSidecarSpecRejectsDuplicateContainerName(t *testing.T) {
	_, err := NormalizeK8sSidecarSpec(K8sSidecarSpec{
		Mode:             K8sSidecarModeBoth,
		AppContainerName: "ledgerbot",
		LogCollectorName: "ledgerbot",
	})
	if err == nil {
		t.Fatalf("重复容器名应被拒绝")
	}
	if !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("错误信息应包含 duplicated: %v", err)
	}
}
