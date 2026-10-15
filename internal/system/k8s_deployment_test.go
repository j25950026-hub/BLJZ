package system

import (
	"strings"
	"testing"
)

func TestControlPoint511K8sDeployment(t *testing.T) {
	t.Run("控制点 5.1.1 功能验证", func(t *testing.T) {
		spec, err := LoadK8sDeploymentSpecFromEnv(func(key string) string {
			switch key {
			case "K8S_DEPLOYMENT_NAME":
				return "ledgerbot"
			case "K8S_DEPLOYMENT_NAMESPACE":
				return "finance"
			case "K8S_DEPLOYMENT_CONTAINER_NAME":
				return "ledgerbot-api"
			case "K8S_DEPLOYMENT_IMAGE":
				return "registry.example.com/ledgerbot:v1.2.0"
			case "K8S_DEPLOYMENT_CONTAINER_PORT":
				return "8090"
			case "K8S_DEPLOYMENT_REPLICAS":
				return "3"
			case "K8S_DEPLOYMENT_CPU_REQUEST":
				return "500m"
			case "K8S_DEPLOYMENT_CPU_LIMIT":
				return "2"
			case "K8S_DEPLOYMENT_MEM_REQUEST":
				return "512Mi"
			case "K8S_DEPLOYMENT_MEM_LIMIT":
				return "1Gi"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 k8s deployment 配置失败: %v", err)
		}
		if spec.Replicas != 3 {
			t.Fatalf("副本数错误: want=%d got=%d", 3, spec.Replicas)
		}
		if spec.CPURequest != "500m" || spec.CPULimit != "2" {
			t.Fatalf("CPU 资源限制错误: %+v", spec)
		}
		if spec.MemoryRequest != "512Mi" || spec.MemoryLimit != "1Gi" {
			t.Fatalf("内存资源限制错误: %+v", spec)
		}

		manifest, err := BuildK8sDeploymentManifest(spec)
		if err != nil {
			t.Fatalf("生成 deployment 清单失败: %v", err)
		}
		required := []string{
			"kind: Deployment",
			"namespace: finance",
			"replicas: 3",
			"containerPort: 8090",
			"requests:\n              cpu: \"500m\"\n              memory: \"512Mi\"",
			"limits:\n              cpu: \"2\"\n              memory: \"1Gi\"",
		}
		for _, fragment := range required {
			if !strings.Contains(manifest, fragment) {
				t.Fatalf("deployment 清单缺少关键片段: %q\nmanifest=%s", fragment, manifest)
			}
		}
	})
}

func TestLoadK8sDeploymentSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadK8sDeploymentSpecFromEnv(func(key string) string {
		if key == "K8S_DEPLOYMENT_REPLICAS" {
			return "0"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("非法副本数应返回回退告警")
	}
	if !strings.Contains(err.Error(), "K8S_DEPLOYMENT_REPLICAS") {
		t.Fatalf("错误信息应包含非法环境变量名: %v", err)
	}
	if spec.Replicas != DefaultK8sDeploymentReplicas {
		t.Fatalf("非法副本数应回退默认值: want=%d got=%d", DefaultK8sDeploymentReplicas, spec.Replicas)
	}
}

func TestNormalizeK8sDeploymentSpecRejectsNegativeReplicas(t *testing.T) {
	_, err := NormalizeK8sDeploymentSpec(K8sDeploymentSpec{Replicas: -1})
	if err == nil {
		t.Fatalf("负副本数应被拒绝")
	}
	if !strings.Contains(err.Error(), "replicas") {
		t.Fatalf("错误信息应包含 replicas: %v", err)
	}
}
