package main

import (
	"testing"
	"time"
)

func TestLoadGuardianConfigUsesRecoveryPathsWithoutBotToken(t *testing.T) {
	values := map[string]string{
		"POLL_STATUS_PATH":        "/var/lib/ledgerbot/recovery/poll-status.json",
		"LEDGERBOT_RELEASE_ROOT":  "/opt/ledgerbot",
		"LEDGERBOT_SERVICE_NAME":  "bilibili-ledgerbot.service",
		"GUARDIAN_COOLDOWN":       "15m",
		"GUARDIAN_VERIFY_TIMEOUT": "90s",
	}
	config, err := loadGuardianConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("加载 Guardian 配置: %v", err)
	}
	if config.PollStatusPath != values["POLL_STATUS_PATH"] || config.ReleaseRoot != values["LEDGERBOT_RELEASE_ROOT"] || config.ServiceName != values["LEDGERBOT_SERVICE_NAME"] {
		t.Fatalf("Guardian 路径配置错误: %#v", config)
	}
	if config.StatePath != "/var/lib/ledgerbot/recovery/guardian-state.json" || config.RequestPath != "/var/lib/ledgerbot/recovery/recovery-request.json" {
		t.Fatalf("Guardian 默认恢复路径错误: %#v", config)
	}
	if config.Cooldown != 15*time.Minute || config.VerifyTimeout != 90*time.Second {
		t.Fatalf("Guardian 时间配置错误: %#v", config)
	}
}

func TestLoadGuardianConfigRejectsInvalidDuration(t *testing.T) {
	_, err := loadGuardianConfig(func(key string) string {
		if key == "GUARDIAN_COOLDOWN" {
			return "not-a-duration"
		}
		return ""
	})
	if err == nil {
		t.Fatal("非法 Guardian duration 必须报错")
	}
}

func TestLoadGuardianConfigDefaultsToRestartOnly(t *testing.T) {
	config, err := loadGuardianConfig(func(string) string { return "" })
	if err != nil {
		t.Fatalf("加载默认 Guardian 配置: %v", err)
	}
	if config.EnableAutoRollback {
		t.Fatal("Guardian 默认必须为 restart-only，不应自动回滚")
	}
}

func TestLoadGuardianConfigCanEnableAutoRollbackExplicitly(t *testing.T) {
	config, err := loadGuardianConfig(func(key string) string {
		if key == "GUARDIAN_AUTO_ROLLBACK_ENABLED" {
			return "true"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("加载显式自动回滚配置: %v", err)
	}
	if !config.EnableAutoRollback {
		t.Fatal("显式开启后 Guardian 应允许自动回滚")
	}
}
