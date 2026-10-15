package system

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestControlPoint514LivenessReadinessProbe(t *testing.T) {
	t.Run("控制点 5.1.4 功能验证", func(t *testing.T) {
		spec, err := LoadK8sProbeSpecFromEnv(func(key string) string {
			switch key {
			case "K8S_PROBE_NAME":
				return "ledgerbot-probe-prod"
			case "K8S_PROBE_NAMESPACE":
				return "finance"
			case "K8S_PROBE_CONTAINER_NAME":
				return "ledgerbot-api"
			case "K8S_PROBE_PORT":
				return "19090"
			case "K8S_PROBE_BIND_ADDR":
				return ":19090"
			case "K8S_LIVENESS_PATH":
				return "/healthz"
			case "K8S_LIVENESS_INITIAL_DELAY_SEC":
				return "15"
			case "K8S_LIVENESS_PERIOD_SEC":
				return "6"
			case "K8S_LIVENESS_TIMEOUT_SEC":
				return "3"
			case "K8S_LIVENESS_FAILURE_THRESHOLD":
				return "4"
			case "K8S_READINESS_PATH":
				return "/readyz"
			case "K8S_READINESS_INITIAL_DELAY_SEC":
				return "3"
			case "K8S_READINESS_PERIOD_SEC":
				return "4"
			case "K8S_READINESS_TIMEOUT_SEC":
				return "2"
			case "K8S_READINESS_SUCCESS_THRESHOLD":
				return "2"
			case "K8S_READINESS_FAILURE_THRESHOLD":
				return "3"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 k8s probe 配置失败: %v", err)
		}
		if spec.Port != 19090 {
			t.Fatalf("探针端口错误: want=%d got=%d", 19090, spec.Port)
		}
		if spec.Liveness.Path != "/healthz" || spec.Readiness.Path != "/readyz" {
			t.Fatalf("探针路径错误: %+v", spec)
		}
		if spec.Liveness.FailureThreshold != 4 || spec.Readiness.SuccessThreshold != 2 {
			t.Fatalf("探针阈值错误: %+v", spec)
		}

		manifest, err := BuildK8sProbeManifest(spec)
		if err != nil {
			t.Fatalf("生成 probe 清单失败: %v", err)
		}
		required := []string{
			"kind: Deployment",
			"namespace: finance",
			"name: ledgerbot-api",
			"livenessProbe:",
			"path: /healthz",
			"periodSeconds: 6",
			"failureThreshold: 4",
			"readinessProbe:",
			"path: /readyz",
			"successThreshold: 2",
		}
		for _, fragment := range required {
			if !strings.Contains(manifest, fragment) {
				t.Fatalf("probe 清单缺少关键片段: %q\nmanifest=%s", fragment, manifest)
			}
		}

		now := time.Date(2026, 2, 20, 9, 0, 0, 0, time.UTC)
		restartTriggered := false
		probe := NewServiceHealthProbe(ServiceHealthProbeOptions{
			HeartbeatTimeout: 5 * time.Second,
			ScanInterval:     time.Second,
			Now: func() time.Time {
				return now
			},
			OnZombie: func(ServiceZombieAlert) {
				restartTriggered = true
			},
		})
		probe.MarkReady()
		probe.Heartbeat("control_point_5_1_4")

		if live, _ := probe.LivenessOK(); !live {
			t.Fatalf("心跳正常时 liveness 应为 healthy")
		}
		if ready, _ := probe.ReadinessOK(); !ready {
			t.Fatalf("服务就绪后 readiness 应为 healthy")
		}

		now = now.Add(6 * time.Second)
		probe.scanAndAlert()

		if !restartTriggered {
			t.Fatalf("心跳超时应触发自动重启回调")
		}
		if live, reason := probe.LivenessOK(); live {
			t.Fatalf("心跳超时后 liveness 应失败，reason=%s", reason)
		}
		if ready, reason := probe.ReadinessOK(); ready {
			t.Fatalf("心跳超时后 readiness 应失败，reason=%s", reason)
		}
	})
}

func TestLoadK8sProbeSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadK8sProbeSpecFromEnv(func(key string) string {
		if key == "K8S_PROBE_PORT" {
			return "0"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("非法端口应返回回退告警")
	}
	if !strings.Contains(err.Error(), "K8S_PROBE_PORT") {
		t.Fatalf("错误信息应包含非法环境变量名: %v", err)
	}
	if spec.Port != DefaultK8sProbePort {
		t.Fatalf("非法端口应回退默认值: want=%d got=%d", DefaultK8sProbePort, spec.Port)
	}
}

func TestServiceHealthHTTPServerHandler(t *testing.T) {
	now := time.Date(2026, 2, 20, 12, 0, 0, 0, time.UTC)
	probe := NewServiceHealthProbe(ServiceHealthProbeOptions{
		HeartbeatTimeout: 3 * time.Second,
		ScanInterval:     time.Second,
		Now: func() time.Time {
			return now
		},
	})
	probe.MarkReady()
	probe.Heartbeat("handler_test")

	server, err := NewServiceHealthHTTPServer(ServiceHealthHTTPServerOptions{
		Addr:  ":19191",
		Probe: probe,
	})
	if err != nil {
		t.Fatalf("创建 probe server 失败: %v", err)
	}

	t.Run("healthy", func(t *testing.T) {
		livenessReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		livenessResp := httptest.NewRecorder()
		server.Handler().ServeHTTP(livenessResp, livenessReq)
		if livenessResp.Code != http.StatusOK {
			t.Fatalf("健康状态下 /healthz 应返回 200，got=%d body=%s", livenessResp.Code, livenessResp.Body.String())
		}

		readinessReq := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		readinessResp := httptest.NewRecorder()
		server.Handler().ServeHTTP(readinessResp, readinessReq)
		if readinessResp.Code != http.StatusOK {
			t.Fatalf("健康状态下 /readyz 应返回 200，got=%d body=%s", readinessResp.Code, readinessResp.Body.String())
		}
	})

	t.Run("heartbeat timeout", func(t *testing.T) {
		now = now.Add(4 * time.Second)
		probe.scanAndAlert()

		livenessReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		livenessResp := httptest.NewRecorder()
		server.Handler().ServeHTTP(livenessResp, livenessReq)
		if livenessResp.Code != http.StatusServiceUnavailable {
			t.Fatalf("心跳超时后 /healthz 应返回 503，got=%d body=%s", livenessResp.Code, livenessResp.Body.String())
		}
		if !strings.Contains(livenessResp.Body.String(), "heartbeat timeout") {
			t.Fatalf("心跳超时响应应包含失败原因，body=%s", livenessResp.Body.String())
		}
	})
}
