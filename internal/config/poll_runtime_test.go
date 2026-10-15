package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFromEnvPollRuntimeDefaults(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	t.Setenv("DB_PATH", "")
	t.Setenv("POLL_STATUS_PATH", "")
	t.Setenv("POLL_STARTUP_GRACE", "")
	t.Setenv("POLL_MAX_SUCCESS_AGE", "")
	t.Setenv("POLL_MIN_FAILURE_COUNT", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}
	if cfg.PollStatusPath != filepath.FromSlash("data/recovery/poll-status.json") {
		t.Fatalf("默认 poll status path 错误: %q", cfg.PollStatusPath)
	}
	if cfg.PollStartupGrace != 90*time.Second || cfg.PollMaxSuccessAge != 90*time.Second || cfg.PollMinFailureCount != 3 {
		t.Fatalf("默认轮询健康阈值错误: grace=%s age=%s failures=%d", cfg.PollStartupGrace, cfg.PollMaxSuccessAge, cfg.PollMinFailureCount)
	}
}

func TestLoadFromEnvPollRuntimeOverrides(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	t.Setenv("POLL_STATUS_PATH", " /var/lib/ledgerbot/recovery/poll.json ")
	t.Setenv("POLL_STARTUP_GRACE", "45s")
	t.Setenv("POLL_MAX_SUCCESS_AGE", "2m")
	t.Setenv("POLL_MIN_FAILURE_COUNT", "5")
	t.Setenv("RELEASE_ID", "20260814.2")
	t.Setenv("RELEASE_SHA256", "abc123")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}
	if cfg.PollStatusPath != "/var/lib/ledgerbot/recovery/poll.json" {
		t.Fatalf("poll status path 未 trim: %q", cfg.PollStatusPath)
	}
	if cfg.PollStartupGrace != 45*time.Second || cfg.PollMaxSuccessAge != 2*time.Minute || cfg.PollMinFailureCount != 5 {
		t.Fatalf("轮询健康阈值覆盖错误: grace=%s age=%s failures=%d", cfg.PollStartupGrace, cfg.PollMaxSuccessAge, cfg.PollMinFailureCount)
	}
	if cfg.ReleaseID != "20260814.2" || cfg.ReleaseSHA256 != "abc123" {
		t.Fatalf("发布标识覆盖错误: id=%q sha=%q", cfg.ReleaseID, cfg.ReleaseSHA256)
	}
}
