package system

import (
	"testing"
	"time"
)

func TestServiceHealthProbeReadinessIncludesFunctionalCheck(t *testing.T) {
	now := time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC)
	probe := NewServiceHealthProbe(ServiceHealthProbeOptions{
		Now:              func() time.Time { return now },
		HeartbeatTimeout:  time.Minute,
		ReadinessCheck: func() (bool, string) {
			return false, "telegram poll stale"
		},
	})
	probe.Heartbeat("runtime")
	probe.MarkReady()

	live, livenessReason := probe.LivenessOK()
	if !live || livenessReason != "ok" {
		t.Fatalf("运行时心跳正常时 liveness 应保持通过: live=%t reason=%q", live, livenessReason)
	}
	ready, readinessReason := probe.ReadinessOK()
	if ready || readinessReason != "telegram poll stale" {
		t.Fatalf("功能检查失败必须只影响 readiness: ready=%t reason=%q", ready, readinessReason)
	}
}
