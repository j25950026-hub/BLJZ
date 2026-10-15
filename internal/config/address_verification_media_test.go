package config

import (
	"testing"
	"time"
)

func TestLoadFromEnvDefaultsAddressVerificationMediaConfig(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	t.Setenv("ADDRESS_VERIFY_MEDIA_MODE", "")
	t.Setenv("ADDRESS_VERIFY_MEDIA_DIR", "")
	t.Setenv("ADDRESS_VERIFY_MEDIA_PUBLIC_BASE_URL", "")
	t.Setenv("ADDRESS_VERIFY_MEDIA_TTL", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}
	if cfg.AddressVerifyMediaMode != "multipart" {
		t.Fatalf("默认媒体模式应为 multipart，实际=%q", cfg.AddressVerifyMediaMode)
	}
	if cfg.AddressVerifyMediaTTL != time.Hour {
		t.Fatalf("默认媒体 TTL 应为 1h，实际=%v", cfg.AddressVerifyMediaTTL)
	}
}

func TestLoadFromEnvTrimsAddressVerificationMediaConfig(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	t.Setenv("ADDRESS_VERIFY_MEDIA_MODE", " local_url ")
	t.Setenv("ADDRESS_VERIFY_MEDIA_DIR", " /var/lib/ledgerbot/telegram-media ")
	t.Setenv("ADDRESS_VERIFY_MEDIA_PUBLIC_BASE_URL", " https://autobookbl.site/tg/media ")
	t.Setenv("ADDRESS_VERIFY_MEDIA_TTL", " 45m ")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}
	if cfg.AddressVerifyMediaMode != "local_url" {
		t.Fatalf("媒体模式 trim 异常: %q", cfg.AddressVerifyMediaMode)
	}
	if cfg.AddressVerifyMediaDir != "/var/lib/ledgerbot/telegram-media" {
		t.Fatalf("媒体目录 trim 异常: %q", cfg.AddressVerifyMediaDir)
	}
	if cfg.AddressVerifyMediaPublicBaseURL != "https://autobookbl.site/tg/media" {
		t.Fatalf("媒体 URL trim 异常: %q", cfg.AddressVerifyMediaPublicBaseURL)
	}
	if cfg.AddressVerifyMediaTTL != 45*time.Minute {
		t.Fatalf("媒体 TTL 解析异常: %v", cfg.AddressVerifyMediaTTL)
	}
}

func TestLoadFromEnvInvalidAddressVerificationMediaTTLFallsBack(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	t.Setenv("ADDRESS_VERIFY_MEDIA_MODE", "local_url")
	t.Setenv("ADDRESS_VERIFY_MEDIA_TTL", "not-a-duration")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}
	if cfg.AddressVerifyMediaTTL != time.Hour {
		t.Fatalf("非法媒体 TTL 应回退到 1h，实际=%v", cfg.AddressVerifyMediaTTL)
	}
}

func TestLoadFromEnvKeepsUnknownAddressVerificationMediaModeForDiagnostics(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	t.Setenv("ADDRESS_VERIFY_MEDIA_MODE", " experimental ")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}
	if cfg.AddressVerifyMediaMode != "experimental" {
		t.Fatalf("未知模式应保留 trim 后的原值用于诊断，实际=%q", cfg.AddressVerifyMediaMode)
	}
}
