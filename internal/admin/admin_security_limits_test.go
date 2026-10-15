package admin

import (
	"testing"
	"time"
)

func TestAdminSecurityLimiterBlocksAfterRepeatedFailures(t *testing.T) {
	limiter := newAdminSecurityLimiter()
	now := time.Unix(100, 0)
	key := "203.0.113.10|admin"
	for i := 0; i < adminSecurityFailureLimit; i++ {
		if !limiter.allow(key, now) {
			t.Fatalf("第 %d 次失败前不应阻止", i+1)
		}
		limiter.failure(key, now)
	}
	if limiter.allow(key, now) {
		t.Fatalf("达到失败阈值后应进入锁定窗口")
	}
	if !limiter.allow(key, now.Add(adminSecurityBlockWindow+time.Second)) {
		t.Fatalf("锁定窗口结束后应允许重试")
	}
	limiter.success(key)
	if !limiter.allow(key, now) {
		t.Fatalf("成功后应清除失败计数")
	}
}
