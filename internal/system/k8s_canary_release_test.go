package system

import (
	"strings"
	"testing"
)

func TestControlPoint535CanaryRelease(t *testing.T) {
	t.Run("控制点 5.3.5 功能验证", func(t *testing.T) {
		spec, err := LoadK8sCanaryReleaseSpecFromEnv(func(key string) string {
			switch key {
			case "K8S_CANARY_NAME":
				return "ledgerbot-rollout-prod"
			case "K8S_CANARY_NAMESPACE":
				return "finance"
			case "K8S_CANARY_TARGET_DEPLOYMENT":
				return "ledgerbot"
			case "K8S_CANARY_TOTAL_REPLICAS":
				return "20"
			case "K8S_CANARY_PERCENT":
				return "10"
			case "K8S_CANARY_OBSERVATION_SECONDS":
				return "180"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 k8s canary 配置失败: %v", err)
		}
		if spec.TotalReplicas != 20 {
			t.Fatalf("总副本数错误: want=%d got=%d", 20, spec.TotalReplicas)
		}
		if spec.CanaryPercent != 10 {
			t.Fatalf("灰度比例错误: want=%d got=%d", 10, spec.CanaryPercent)
		}
		if spec.ObserveSeconds != 180 {
			t.Fatalf("观察窗口错误: want=%d got=%d", 180, spec.ObserveSeconds)
		}

		plan, err := BuildK8sCanaryRolloutPlan(spec)
		if err != nil {
			t.Fatalf("生成灰度发布计划失败: %v", err)
		}
		if plan.CanaryReplicas != 2 {
			t.Fatalf("灰度阶段副本数错误: want=%d got=%d", 2, plan.CanaryReplicas)
		}
		if plan.StableReplicas != 18 {
			t.Fatalf("稳定副本数错误: want=%d got=%d", 18, plan.StableReplicas)
		}

		manifest, err := BuildK8sCanaryReleaseManifest(spec)
		if err != nil {
			t.Fatalf("生成 canary 清单失败: %v", err)
		}
		required := []string{
			"kind: Rollout",
			"namespace: finance",
			"name: ledgerbot",
			"setCanaryScale:\n            replicas: 2",
			"setWeight: 10",
			"duration: 180s",
			"setWeight: 100",
		}
		for _, fragment := range required {
			if !strings.Contains(manifest, fragment) {
				t.Fatalf("canary 清单缺少关键片段: %q\nmanifest=%s", fragment, manifest)
			}
		}
	})
}

func TestLoadK8sCanaryReleaseSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadK8sCanaryReleaseSpecFromEnv(func(key string) string {
		if key == "K8S_CANARY_PERCENT" {
			return "invalid"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("非法灰度比例应返回回退告警")
	}
	if !strings.Contains(err.Error(), "K8S_CANARY_PERCENT") {
		t.Fatalf("错误信息应包含非法环境变量名: %v", err)
	}
	if spec.CanaryPercent != DefaultK8sCanaryReleasePercent {
		t.Fatalf("非法灰度比例应回退默认值: want=%d got=%d", DefaultK8sCanaryReleasePercent, spec.CanaryPercent)
	}
}

func TestNormalizeK8sCanaryReleaseSpecRejectsPercentOutOfRange(t *testing.T) {
	_, err := NormalizeK8sCanaryReleaseSpec(K8sCanaryReleaseSpec{CanaryPercent: 101})
	if err == nil {
		t.Fatalf("灰度比例超出范围应被拒绝")
	}
	if !strings.Contains(err.Error(), "canary percent") {
		t.Fatalf("错误信息应包含 canary percent: %v", err)
	}
}

func TestBuildK8sCanaryRolloutPlanRoundUp(t *testing.T) {
	plan, err := BuildK8sCanaryRolloutPlan(K8sCanaryReleaseSpec{
		TotalReplicas: 9,
		CanaryPercent: 10,
	})
	if err != nil {
		t.Fatalf("生成灰度发布计划失败: %v", err)
	}
	if plan.CanaryReplicas != 1 {
		t.Fatalf("向上取整后的灰度副本数错误: want=%d got=%d", 1, plan.CanaryReplicas)
	}
	if plan.StableReplicas != 8 {
		t.Fatalf("稳定副本数错误: want=%d got=%d", 8, plan.StableReplicas)
	}
}
