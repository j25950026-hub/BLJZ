package system

import (
	"strings"
	"testing"
)

func TestControlPoint513ConfigMapSecret(t *testing.T) {
	t.Run("控制点 5.1.3 功能验证", func(t *testing.T) {
		spec, err := LoadK8sConfigMapSecretSpecFromEnv(func(key string) string {
			switch key {
			case "K8S_CONFIGMAP_SECRET_NAMESPACE":
				return "finance"
			case "K8S_CONFIGMAP_NAME":
				return "ledgerbot-config-prod"
			case "K8S_SECRET_NAME":
				return "ledgerbot-secret-prod"
			case "K8S_CONFIGMAP_KEYS":
				return "BOT_TIMEZONE,LOG_LEVEL,ADMIN_ADDR"
			case "K8S_SECRET_KEYS":
				return "BOT_TOKEN,ADMIN_TOKEN"
			case "BOT_TIMEZONE":
				return "Asia/Shanghai"
			case "LOG_LEVEL":
				return "DEBUG"
			case "ADMIN_ADDR":
				return ":9000"
			case "BOT_TOKEN":
				return "123456:token"
			case "ADMIN_TOKEN":
				return "super-secret"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 k8s configmap/secret 配置失败: %v", err)
		}
		if spec.Namespace != "finance" {
			t.Fatalf("命名空间错误: want=%q got=%q", "finance", spec.Namespace)
		}
		if spec.ConfigMapName != "ledgerbot-config-prod" {
			t.Fatalf("configmap 名称错误: %+v", spec)
		}
		if spec.SecretName != "ledgerbot-secret-prod" {
			t.Fatalf("secret 名称错误: %+v", spec)
		}
		if spec.ConfigMapData["LOG_LEVEL"] != "DEBUG" || spec.ConfigMapData["ADMIN_ADDR"] != ":9000" {
			t.Fatalf("configmap 数据错误: %+v", spec.ConfigMapData)
		}
		if spec.SecretData["BOT_TOKEN"] != "123456:token" || spec.SecretData["ADMIN_TOKEN"] != "super-secret" {
			t.Fatalf("secret 数据错误: %+v", spec.SecretData)
		}

		configMapManifest, err := BuildK8sConfigMapManifest(spec)
		if err != nil {
			t.Fatalf("生成 configmap 清单失败: %v", err)
		}
		configMapRequired := []string{
			"kind: ConfigMap",
			"namespace: finance",
			"name: ledgerbot-config-prod",
			"LOG_LEVEL: \"DEBUG\"",
			"ADMIN_ADDR: \":9000\"",
		}
		for _, fragment := range configMapRequired {
			if !strings.Contains(configMapManifest, fragment) {
				t.Fatalf("configmap 清单缺少关键片段: %q\nmanifest=%s", fragment, configMapManifest)
			}
		}

		secretManifest, err := BuildK8sSecretManifest(spec)
		if err != nil {
			t.Fatalf("生成 secret 清单失败: %v", err)
		}
		secretRequired := []string{
			"kind: Secret",
			"type: Opaque",
			"name: ledgerbot-secret-prod",
			"stringData:",
			"BOT_TOKEN: \"123456:token\"",
			"ADMIN_TOKEN: \"super-secret\"",
		}
		for _, fragment := range secretRequired {
			if !strings.Contains(secretManifest, fragment) {
				t.Fatalf("secret 清单缺少关键片段: %q\nmanifest=%s", fragment, secretManifest)
			}
		}

		combinedManifest, err := BuildK8sConfigMapSecretManifests(spec)
		if err != nil {
			t.Fatalf("生成组合清单失败: %v", err)
		}
		if !strings.Contains(combinedManifest, "---\n") {
			t.Fatalf("组合清单应包含资源分隔符: %s", combinedManifest)
		}
	})
}

func TestLoadK8sConfigMapSecretSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadK8sConfigMapSecretSpecFromEnv(func(key string) string {
		switch key {
		case "K8S_CONFIGMAP_KEYS":
			return "BOT_TIMEZONE,BAD-KEY"
		case "BOT_TIMEZONE":
			return "Asia/Tokyo"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法 configmap key 应返回回退告警")
	}
	if !strings.Contains(err.Error(), "K8S_CONFIGMAP_KEYS") {
		t.Fatalf("错误信息应包含非法环境变量名: %v", err)
	}
	if _, exists := spec.ConfigMapData["BAD-KEY"]; exists {
		t.Fatalf("非法配置项不应写入 configmap: %+v", spec.ConfigMapData)
	}
	if spec.ConfigMapData["BOT_TIMEZONE"] != "Asia/Tokyo" {
		t.Fatalf("回退默认 key 时应继续读取环境值: %+v", spec.ConfigMapData)
	}
}

func TestNormalizeK8sConfigMapSecretSpecRejectsOverlappedKeys(t *testing.T) {
	_, err := NormalizeK8sConfigMapSecretSpec(K8sConfigMapSecretSpec{
		ConfigMapData: map[string]string{
			"BOT_TOKEN": "configmap-value",
		},
		SecretData: map[string]string{
			"BOT_TOKEN": "secret-value",
		},
	})
	if err == nil {
		t.Fatalf("ConfigMap 与 Secret 重复 key 应被拒绝")
	}
	if !strings.Contains(err.Error(), "both ConfigMap and Secret") {
		t.Fatalf("错误信息应包含冲突提示: %v", err)
	}
}
