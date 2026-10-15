package system

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestControlPoint554AutoRemediation(t *testing.T) {
	t.Run("控制点 5.5.4 功能验证", func(t *testing.T) {
		spec, err := LoadAutoRemediationSpecFromEnv(func(key string) string {
			switch key {
			case "AUTO_REMEDIATION_ENABLED":
				return "true"
			case "AUTO_REMEDIATION_TIMEOUT":
				return "30s"
			case "AUTO_REMEDIATION_COOLDOWN":
				return "90s"
			case "AUTO_REMEDIATION_WEBHOOK_RESET_SCRIPT":
				return "scripts/remediation/custom_webhook_reset.sh"
			case "AUTO_REMEDIATION_REDIS_EVICT_SCRIPT":
				return "scripts/remediation/custom_redis_evict.sh"
			case "AUTO_REMEDIATION_DB_TERMINATE_SCRIPT":
				return "scripts/remediation/custom_db_terminate.sh"
			case "AUTO_REMEDIATION_QUEUE_RESTART_SCRIPT":
				return "scripts/remediation/custom_queue_restart.sh"
			case "AUTO_REMEDIATION_ALERT_SILENCE_SCRIPT":
				return "scripts/remediation/custom_alert_silence.sh"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载自动恢复配置失败: %v", err)
		}
		if !spec.Enabled {
			t.Fatalf("自动恢复脚本应为启用状态")
		}
		if spec.Timeout != 30*time.Second {
			t.Fatalf("自动恢复超时解析错误: want=%s got=%s", 30*time.Second, spec.Timeout)
		}
		if spec.Cooldown != 90*time.Second {
			t.Fatalf("自动恢复冷却时间解析错误: want=%s got=%s", 90*time.Second, spec.Cooldown)
		}
		if spec.QueueRestartScript != "scripts/remediation/custom_queue_restart.sh" {
			t.Fatalf("队列重启脚本解析错误: %s", spec.QueueRestartScript)
		}

		logLine := BuildAutoRemediationLog(spec)
		requiredFragments := []string{
			"自动恢复脚本已启用",
			"timeout=30s",
			"cooldown=1m30s",
			"webhook_404_spike=>scripts/remediation/custom_webhook_reset.sh",
			"consumer_heartbeat_stalled=>scripts/remediation/custom_queue_restart.sh",
			"常见故障自动执行脚本修复",
		}
		for _, fragment := range requiredFragments {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("自动恢复日志缺少关键片段 %q: %s", fragment, logLine)
			}
		}

		executedScripts := make([]string, 0, 2)
		executedFaults := make([]AutoRemediationFault, 0, 2)
		remediator := NewAutoRemediator(AutoRemediatorOptions{
			Spec: spec,
			Executor: func(_ context.Context, script string, event AutoRemediationEvent) error {
				executedScripts = append(executedScripts, script)
				executedFaults = append(executedFaults, event.Fault)
				return nil
			},
			Now: func() time.Time { return time.Date(2026, 2, 20, 12, 0, 0, 0, time.UTC) },
		})

		events := []AutoRemediationEvent{
			{
				Fault:       AutoRemediationFaultWebhook404Spike,
				TriggeredAt: time.Date(2026, 2, 20, 12, 0, 0, 0, time.UTC),
			},
			{
				Fault:       AutoRemediationFaultDBIdleInTransaction,
				TriggeredAt: time.Date(2026, 2, 20, 12, 1, 0, 0, time.UTC),
			},
		}
		results := remediator.RunBatch(context.Background(), events)
		if len(results) != 2 {
			t.Fatalf("自动恢复批量执行结果数量错误: want=%d got=%d", 2, len(results))
		}
		for index, result := range results {
			if !result.Attempted {
				t.Fatalf("第%d条恢复结果应为已执行: %+v", index+1, result)
			}
			if !result.Success {
				t.Fatalf("第%d条恢复结果应成功: %+v", index+1, result)
			}
		}
		if len(executedScripts) != 2 || len(executedFaults) != 2 {
			t.Fatalf("自动恢复执行次数错误: scripts=%d faults=%d", len(executedScripts), len(executedFaults))
		}
		if executedFaults[0] != AutoRemediationFaultWebhook404Spike {
			t.Fatalf("首个恢复故障类型错误: want=%s got=%s", AutoRemediationFaultWebhook404Spike, executedFaults[0])
		}
		if executedScripts[0] != spec.WebhookResetScript {
			t.Fatalf("Webhook 恢复脚本错误: want=%s got=%s", spec.WebhookResetScript, executedScripts[0])
		}
		if executedFaults[1] != AutoRemediationFaultDBIdleInTransaction {
			t.Fatalf("第二个恢复故障类型错误: want=%s got=%s", AutoRemediationFaultDBIdleInTransaction, executedFaults[1])
		}
		if executedScripts[1] != spec.DBTerminateScript {
			t.Fatalf("DB 恢复脚本错误: want=%s got=%s", spec.DBTerminateScript, executedScripts[1])
		}
	})
}

func TestLoadAutoRemediationSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadAutoRemediationSpecFromEnv(func(key string) string {
		switch key {
		case "AUTO_REMEDIATION_ENABLED":
			return "unknown"
		case "AUTO_REMEDIATION_TIMEOUT":
			return "abc"
		case "AUTO_REMEDIATION_COOLDOWN":
			return "0s"
		case "AUTO_REMEDIATION_QUEUE_RESTART_SCRIPT":
			return "   "
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法自动恢复配置应返回回退告警")
	}
	requiredErrorFragments := []string{
		"AUTO_REMEDIATION_ENABLED",
		"AUTO_REMEDIATION_TIMEOUT",
		"AUTO_REMEDIATION_COOLDOWN",
		"AUTO_REMEDIATION_QUEUE_RESTART_SCRIPT",
	}
	for _, fragment := range requiredErrorFragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("错误信息缺少变量名 %q: %v", fragment, err)
		}
	}
	if spec.Timeout != DefaultAutoRemediationTimeout {
		t.Fatalf("非法 timeout 应回退默认值: want=%s got=%s", DefaultAutoRemediationTimeout, spec.Timeout)
	}
	if spec.Cooldown != DefaultAutoRemediationCooldown {
		t.Fatalf("非法 cooldown 应回退默认值: want=%s got=%s", DefaultAutoRemediationCooldown, spec.Cooldown)
	}
	if spec.QueueRestartScript != DefaultAutoRemediationQueueRestartScript {
		t.Fatalf(
			"非法队列脚本应回退默认值: want=%s got=%s",
			DefaultAutoRemediationQueueRestartScript,
			spec.QueueRestartScript,
		)
	}
}

func TestBuildAutoRemediationDisabled(t *testing.T) {
	spec := DefaultAutoRemediationSpec()
	spec.Enabled = false
	logLine := BuildAutoRemediationLog(spec)
	if !strings.Contains(logLine, "自动恢复脚本未启用") {
		t.Fatalf("关闭自动恢复脚本日志应提示未启用: %s", logLine)
	}
}

func TestAutoRemediatorCooldownAndUnsupportedFault(t *testing.T) {
	spec := DefaultAutoRemediationSpec()
	spec.Timeout = 10 * time.Second
	spec.Cooldown = 2 * time.Minute

	base := time.Date(2026, 2, 20, 14, 0, 0, 0, time.UTC)
	nowValues := []time.Time{
		base,
		base.Add(3 * time.Second),
		base.Add(30 * time.Second),
		base.Add(30 * time.Second),
	}
	nowIndex := 0
	nextNow := func() time.Time {
		if nowIndex >= len(nowValues) {
			return nowValues[len(nowValues)-1]
		}
		value := nowValues[nowIndex]
		nowIndex++
		return value
	}

	executed := 0
	remediator := NewAutoRemediator(AutoRemediatorOptions{
		Spec: spec,
		Executor: func(context.Context, string, AutoRemediationEvent) error {
			executed++
			return nil
		},
		Now: nextNow,
	})

	first := remediator.RunOnce(context.Background(), AutoRemediationEvent{
		Fault:       AutoRemediationFaultWebhook404Spike,
		TriggeredAt: base,
	})
	if !first.Attempted || !first.Success {
		t.Fatalf("首次自动恢复应执行成功: %+v", first)
	}
	if executed != 1 {
		t.Fatalf("首次自动恢复执行次数错误: %d", executed)
	}

	second := remediator.RunOnce(context.Background(), AutoRemediationEvent{
		Fault:       AutoRemediationFaultWebhook404Spike,
		TriggeredAt: base.Add(30 * time.Second),
	})
	if !second.Skipped {
		t.Fatalf("冷却时间内重复故障应跳过: %+v", second)
	}
	if second.SkipReason != "cooldown_not_elapsed" {
		t.Fatalf("跳过原因错误: %s", second.SkipReason)
	}
	if executed != 1 {
		t.Fatalf("冷却时间内不应重复执行脚本: executed=%d", executed)
	}

	unsupported := remediator.RunOnce(context.Background(), AutoRemediationEvent{
		Fault:       AutoRemediationFault("unknown_fault"),
		TriggeredAt: base.Add(5 * time.Minute),
	})
	if unsupported.Attempted {
		t.Fatalf("不支持的故障类型不应执行脚本: %+v", unsupported)
	}
	if !strings.Contains(unsupported.Error, "unsupported fault") {
		t.Fatalf("不支持故障类型的错误信息不正确: %s", unsupported.Error)
	}
}
