package system

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestControlPoint553ChaosEngineering(t *testing.T) {
	t.Run("控制点 5.5.3 功能验证", func(t *testing.T) {
		spec, err := LoadChaosEngineeringSpecFromEnv(func(key string) string {
			switch key {
			case "CHAOS_ENGINEERING_ENABLED":
				return "true"
			case "CHAOS_ENGINEERING_INTERVAL":
				return "2h"
			case "CHAOS_DB_OUTAGE_DURATION":
				return "45s"
			case "CHAOS_NETWORK_INTERRUPTION_DURATION":
				return "30s"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载故障演练配置失败: %v", err)
		}
		if !spec.Enabled {
			t.Fatalf("故障演练应为启用状态")
		}
		if spec.Interval != 2*time.Hour {
			t.Fatalf("故障演练间隔解析错误: want=%s got=%s", 2*time.Hour, spec.Interval)
		}
		if spec.DBOutageDuration != 45*time.Second {
			t.Fatalf("DB 宕机演练时长解析错误: want=%s got=%s", 45*time.Second, spec.DBOutageDuration)
		}
		if spec.NetworkInterruptionDuration != 30*time.Second {
			t.Fatalf("网络中断演练时长解析错误: want=%s got=%s", 30*time.Second, spec.NetworkInterruptionDuration)
		}

		logLine := BuildChaosEngineeringLog(spec)
		requiredFragments := []string{
			"故障演练已启用",
			"interval=2h0m0s",
			"db_outage(45s)",
			"network_interruption(30s)",
			"定期模拟故障并验证应急响应",
		}
		for _, fragment := range requiredFragments {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("故障演练日志缺少关键片段 %q: %s", fragment, logLine)
			}
		}

		dbCalls := 0
		networkCalls := 0
		runner := NewChaosEngineer(ChaosEngineerOptions{
			Interval:                    spec.Interval,
			DBOutageDuration:            spec.DBOutageDuration,
			NetworkInterruptionDuration: spec.NetworkInterruptionDuration,
			RunDBOutage: func(_ context.Context, plannedDuration time.Duration) error {
				dbCalls++
				if plannedDuration != spec.DBOutageDuration {
					return errors.New("db outage planned duration mismatch")
				}
				return nil
			},
			RunNetworkInterruption: func(_ context.Context, plannedDuration time.Duration) error {
				networkCalls++
				if plannedDuration != spec.NetworkInterruptionDuration {
					return errors.New("network interruption planned duration mismatch")
				}
				return nil
			},
		})

		scheduledAt := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
		results := runner.RunOnce(context.Background(), scheduledAt)
		if len(results) != 2 {
			t.Fatalf("单次故障演练应产出两条结果（DB+Network）: got=%d", len(results))
		}
		if results[0].Scenario != ChaosScenarioDBOutage {
			t.Fatalf("首条演练类型错误: want=%s got=%s", ChaosScenarioDBOutage, results[0].Scenario)
		}
		if results[1].Scenario != ChaosScenarioNetworkInterruption {
			t.Fatalf("第二条演练类型错误: want=%s got=%s", ChaosScenarioNetworkInterruption, results[1].Scenario)
		}
		if !results[0].Success || !results[1].Success {
			t.Fatalf("演练应成功: %+v", results)
		}
		if results[0].PlannedDuration != spec.DBOutageDuration {
			t.Fatalf("DB 演练计划时长错误: want=%s got=%s", spec.DBOutageDuration, results[0].PlannedDuration)
		}
		if results[1].PlannedDuration != spec.NetworkInterruptionDuration {
			t.Fatalf("网络演练计划时长错误: want=%s got=%s", spec.NetworkInterruptionDuration, results[1].PlannedDuration)
		}
		if dbCalls != 1 || networkCalls != 1 {
			t.Fatalf("单次演练应各触发一次: db=%d network=%d", dbCalls, networkCalls)
		}
	})
}

func TestLoadChaosEngineeringSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadChaosEngineeringSpecFromEnv(func(key string) string {
		switch key {
		case "CHAOS_ENGINEERING_INTERVAL":
			return "invalid"
		case "CHAOS_DB_OUTAGE_DURATION":
			return "-1s"
		case "CHAOS_NETWORK_INTERRUPTION_DURATION":
			return "0s"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法故障演练配置应返回回退告警")
	}
	if !strings.Contains(err.Error(), "CHAOS_ENGINEERING_INTERVAL") {
		t.Fatalf("错误信息应包含非法变量名: %v", err)
	}
	if !strings.Contains(err.Error(), "CHAOS_DB_OUTAGE_DURATION") {
		t.Fatalf("错误信息应包含非法变量名: %v", err)
	}
	if !strings.Contains(err.Error(), "CHAOS_NETWORK_INTERRUPTION_DURATION") {
		t.Fatalf("错误信息应包含非法变量名: %v", err)
	}
	if spec.Interval != DefaultChaosEngineeringInterval {
		t.Fatalf("非法间隔应回退默认值: want=%s got=%s", DefaultChaosEngineeringInterval, spec.Interval)
	}
	if spec.DBOutageDuration != DefaultChaosEngineeringDBOutageDuration {
		t.Fatalf(
			"非法 DB 宕机演练时长应回退默认值: want=%s got=%s",
			DefaultChaosEngineeringDBOutageDuration,
			spec.DBOutageDuration,
		)
	}
	if spec.NetworkInterruptionDuration != DefaultChaosEngineeringNetworkInterruptionDuration {
		t.Fatalf(
			"非法网络中断演练时长应回退默认值: want=%s got=%s",
			DefaultChaosEngineeringNetworkInterruptionDuration,
			spec.NetworkInterruptionDuration,
		)
	}
}

func TestBuildChaosEngineeringDisabled(t *testing.T) {
	spec := DefaultChaosEngineeringSpec()
	spec.Enabled = false
	logLine := BuildChaosEngineeringLog(spec)
	if !strings.Contains(logLine, "故障演练未启用") {
		t.Fatalf("关闭故障演练日志应提示未启用: %s", logLine)
	}
}

func TestChaosEngineerRunOnceStillExecutesNextScenarioAfterFailure(t *testing.T) {
	runner := NewChaosEngineer(ChaosEngineerOptions{
		RunDBOutage: func(context.Context, time.Duration) error {
			return errors.New("db down")
		},
		RunNetworkInterruption: func(context.Context, time.Duration) error {
			return nil
		},
	})

	results := runner.RunOnce(context.Background(), time.Now())
	if len(results) != 2 {
		t.Fatalf("单次演练应包含两个场景: got=%d", len(results))
	}
	if results[0].Success {
		t.Fatalf("DB 演练失败时应标记 success=false")
	}
	if results[0].Error == "" {
		t.Fatalf("DB 演练失败时应记录错误原因")
	}
	if !results[1].Success {
		t.Fatalf("后续网络演练仍应继续执行并成功: %+v", results[1])
	}
}
