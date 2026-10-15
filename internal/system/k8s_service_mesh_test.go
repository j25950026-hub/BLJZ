package system

import (
	"strings"
	"testing"
)

func TestControlPoint542K8sServiceMesh(t *testing.T) {
	t.Run("控制点 5.4.2 功能验证", func(t *testing.T) {
		spec, err := LoadK8sServiceMeshSpecFromEnv(func(key string) string {
			switch key {
			case "K8S_SERVICE_MESH_NAME":
				return "ledgerbot-mesh-prod"
			case "K8S_SERVICE_MESH_NAMESPACE":
				return "finance"
			case "K8S_SERVICE_MESH_HOST":
				return "ledgerbot.finance.svc.cluster.local"
			case "K8S_SERVICE_MESH_GATEWAY":
				return "finance-gateway"
			case "K8S_SERVICE_MESH_LB_POLICY":
				return "round-robin"
			case "K8S_SERVICE_MESH_REQUEST_TIMEOUT_SEC":
				return "8"
			case "K8S_SERVICE_MESH_RETRY_ATTEMPTS":
				return "4"
			case "K8S_SERVICE_MESH_RETRY_TIMEOUT_SEC":
				return "3"
			case "K8S_SERVICE_MESH_MAX_CONNECTIONS":
				return "2048"
			case "K8S_SERVICE_MESH_MAX_PENDING_REQUESTS":
				return "256"
			case "K8S_SERVICE_MESH_CONSECUTIVE_5XX_ERRORS":
				return "7"
			case "K8S_SERVICE_MESH_OUTLIER_INTERVAL_SEC":
				return "12"
			case "K8S_SERVICE_MESH_BASE_EJECTION_SEC":
				return "45"
			case "K8S_SERVICE_MESH_MAX_EJECTION_PERCENT":
				return "35"
			case "K8S_SERVICE_MESH_MTLS_MODE":
				return "permissive"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 k8s service mesh 配置失败: %v", err)
		}
		if spec.LoadBalancerPolicy != K8sServiceMeshLoadBalancerRoundRobin {
			t.Fatalf("负载均衡策略错误: want=%s got=%s", K8sServiceMeshLoadBalancerRoundRobin, spec.LoadBalancerPolicy)
		}
		if spec.MTLSMode != K8sServiceMeshMTLSModePermissive {
			t.Fatalf("mTLS 模式错误: want=%s got=%s", K8sServiceMeshMTLSModePermissive, spec.MTLSMode)
		}
		if spec.RetryAttempts != 4 || spec.RetryPerTryTimeoutSec != 3 || spec.RequestTimeoutSec != 8 {
			t.Fatalf("重试/超时参数错误: %+v", spec)
		}
		if spec.MaxConnections != 2048 || spec.MaxPendingRequests != 256 {
			t.Fatalf("连接池参数错误: %+v", spec)
		}
		if spec.Consecutive5xxErrors != 7 || spec.OutlierIntervalSec != 12 || spec.BaseEjectionSec != 45 {
			t.Fatalf("异常剔除参数错误: %+v", spec)
		}
		if spec.MaxEjectionPercent != 35 {
			t.Fatalf("最大剔除比例错误: %+v", spec)
		}

		manifests, err := BuildK8sServiceMeshManifests(spec)
		if err != nil {
			t.Fatalf("生成 service mesh 清单失败: %v", err)
		}
		required := []string{
			"kind: DestinationRule",
			"namespace: finance",
			"host: ledgerbot.finance.svc.cluster.local",
			"mode: PERMISSIVE",
			"simple: ROUND_ROBIN",
			"maxConnections: 2048",
			"http1MaxPendingRequests: 256",
			"consecutive5xxErrors: 7",
			"interval: 12s",
			"baseEjectionTime: 45s",
			"maxEjectionPercent: 35",
			"kind: VirtualService",
			"- finance-gateway",
			"attempts: 4",
			"perTryTimeout: 3s",
			"timeout: 8s",
		}
		for _, fragment := range required {
			if !strings.Contains(manifests, fragment) {
				t.Fatalf("service mesh 清单缺少关键片段: %q\nmanifest=%s", fragment, manifests)
			}
		}
	})
}

func TestLoadK8sServiceMeshSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadK8sServiceMeshSpecFromEnv(func(key string) string {
		switch key {
		case "K8S_SERVICE_MESH_RETRY_ATTEMPTS":
			return "invalid"
		case "K8S_SERVICE_MESH_LB_POLICY":
			return "bad-policy"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法 service mesh 配置应返回回退告警")
	}
	if !strings.Contains(err.Error(), "K8S_SERVICE_MESH_LB_POLICY") {
		t.Fatalf("错误信息应包含非法环境变量名: %v", err)
	}
	if spec.LoadBalancerPolicy != DefaultK8sServiceMeshLoadBalancer {
		t.Fatalf(
			"非法负载均衡策略应回退默认值: want=%s got=%s",
			DefaultK8sServiceMeshLoadBalancer,
			spec.LoadBalancerPolicy,
		)
	}
	if spec.RetryAttempts != DefaultK8sServiceMeshRetryAttempts {
		t.Fatalf(
			"非法重试次数应回退默认值: want=%d got=%d",
			DefaultK8sServiceMeshRetryAttempts,
			spec.RetryAttempts,
		)
	}
}

func TestNormalizeK8sServiceMeshSpecRejectsInvalidTimeout(t *testing.T) {
	_, err := NormalizeK8sServiceMeshSpec(K8sServiceMeshSpec{
		RequestTimeoutSec:     2,
		RetryPerTryTimeoutSec: 3,
	})
	if err == nil {
		t.Fatalf("per-try timeout 大于 request timeout 应被拒绝")
	}
	if !strings.Contains(err.Error(), "per-try timeout") {
		t.Fatalf("错误信息应包含 per-try timeout: %v", err)
	}
}

func TestBuildK8sServiceMeshLog(t *testing.T) {
	logLine := BuildK8sServiceMeshLog(DefaultK8sServiceMeshSpec())
	required := []string{
		"K8s Service Mesh 已优化",
		"lb=LEAST_REQUEST",
		"mTLS=STRICT",
		"retries=3",
		"timeout=5s",
	}
	for _, fragment := range required {
		if !strings.Contains(logLine, fragment) {
			t.Fatalf("service mesh 日志缺少关键字段 %q: %s", fragment, logLine)
		}
	}
}
