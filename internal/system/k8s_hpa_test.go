package system

import (
	"strings"
	"testing"
)

func TestControlPoint512HPA(t *testing.T) {
	t.Run("控制点 5.1.2 功能验证", func(t *testing.T) {
		spec, err := LoadK8sHPASpecFromEnv(func(key string) string {
			switch key {
			case "K8S_HPA_NAME":
				return "ledgerbot-hpa-prod"
			case "K8S_HPA_NAMESPACE":
				return "finance"
			case "K8S_HPA_TARGET_DEPLOYMENT":
				return "ledgerbot"
			case "K8S_HPA_MIN_REPLICAS":
				return "3"
			case "K8S_HPA_MAX_REPLICAS":
				return "12"
			case "K8S_HPA_TARGET_CPU":
				return "65"
			case "K8S_HPA_TARGET_QPS_PER_POD":
				return "180"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 k8s hpa 配置失败: %v", err)
		}
		if spec.MinReplicas != 3 || spec.MaxReplicas != 12 {
			t.Fatalf("副本上下限错误: %+v", spec)
		}
		if spec.TargetCPUUsage != 65 || spec.TargetQPSPerPod != 180 {
			t.Fatalf("扩缩容指标阈值错误: %+v", spec)
		}

		manifest, err := BuildK8sHPAManifest(spec)
		if err != nil {
			t.Fatalf("生成 hpa 清单失败: %v", err)
		}
		required := []string{
			"kind: HorizontalPodAutoscaler",
			"namespace: finance",
			"minReplicas: 3",
			"maxReplicas: 12",
			"averageUtilization: 65",
			"name: requests_per_second",
			"averageValue: \"180\"",
		}
		for _, fragment := range required {
			if !strings.Contains(manifest, fragment) {
				t.Fatalf("hpa 清单缺少关键片段: %q\nmanifest=%s", fragment, manifest)
			}
		}

		desiredReplicas, err := RecommendHPAReplicaCount(spec, 4, 82, 1260)
		if err != nil {
			t.Fatalf("计算 hpa 目标副本失败: %v", err)
		}
		if desiredReplicas != 7 {
			t.Fatalf("副本数计算错误: want=%d got=%d", 7, desiredReplicas)
		}
	})
}

func TestLoadK8sHPASpecFromEnvFallback(t *testing.T) {
	spec, err := LoadK8sHPASpecFromEnv(func(key string) string {
		if key == "K8S_HPA_MIN_REPLICAS" {
			return "0"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("非法最小副本数应返回回退告警")
	}
	if !strings.Contains(err.Error(), "K8S_HPA_MIN_REPLICAS") {
		t.Fatalf("错误信息应包含非法环境变量名: %v", err)
	}
	if spec.MinReplicas != DefaultK8sHPAMinReplicas {
		t.Fatalf("非法最小副本数应回退默认值: want=%d got=%d", DefaultK8sHPAMinReplicas, spec.MinReplicas)
	}
}

func TestNormalizeK8sHPASpecRejectsInvalidReplicaRange(t *testing.T) {
	_, err := NormalizeK8sHPASpec(K8sHPASpec{
		MinReplicas: 5,
		MaxReplicas: 3,
	})
	if err == nil {
		t.Fatalf("max replicas 小于 min replicas 应被拒绝")
	}
	if !strings.Contains(err.Error(), "max replicas") {
		t.Fatalf("错误信息应包含 max replicas: %v", err)
	}
}

func TestRecommendHPAReplicaCountClampByMinMax(t *testing.T) {
	spec := K8sHPASpec{
		MinReplicas:     2,
		MaxReplicas:     6,
		TargetCPUUsage:  70,
		TargetQPSPerPod: 100,
	}

	scaleUpResult, err := RecommendHPAReplicaCount(spec, 4, 200, 2000)
	if err != nil {
		t.Fatalf("扩容场景计算失败: %v", err)
	}
	if scaleUpResult != 6 {
		t.Fatalf("扩容场景应命中最大副本上限: want=%d got=%d", 6, scaleUpResult)
	}

	scaleDownResult, err := RecommendHPAReplicaCount(spec, 6, 10, 0)
	if err != nil {
		t.Fatalf("缩容场景计算失败: %v", err)
	}
	if scaleDownResult != 2 {
		t.Fatalf("缩容场景应命中最小副本下限: want=%d got=%d", 2, scaleDownResult)
	}
}

func TestRecommendHPAReplicaCountLargeCPUUsageClampMax(t *testing.T) {
	spec := K8sHPASpec{
		MinReplicas:     2,
		MaxReplicas:     6,
		TargetCPUUsage:  70,
		TargetQPSPerPod: 100,
	}

	result, err := RecommendHPAReplicaCount(spec, 2000000000, 2000000000, 0)
	if err != nil {
		t.Fatalf("大流量场景计算失败: %v", err)
	}
	if result != 6 {
		t.Fatalf("高 CPU 场景应命中最大副本上限: want=%d got=%d", 6, result)
	}
}
