package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAdminDeviceBindingStateMachineAndCompletion(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "device-binding.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	if _, err := database.CreateAdminSession(ctx, "root", "primary-handle", "primary-token-hash", "Chrome", "127.0.0.1", 99, 12*time.Hour); err != nil {
		t.Fatalf("创建主设备 session 失败: %v", err)
	}
	started, err := database.StartAdminDeviceBinding(ctx, "binding-1", "token-hash-1", "root", "primary-handle", 100)
	if err != nil || started.Status != "pending" {
		t.Fatalf("创建绑定请求失败: %+v err=%v", started, err)
	}
	claimed, ok, err := database.ClaimAdminDeviceBinding(ctx, "token-hash-1", "cookie-hash-1", "Chrome", "127.0.0.2", 101)
	if err != nil || !ok || claimed.Status != "claimed" {
		t.Fatalf("claim 应成功: %+v ok=%v err=%v", claimed, ok, err)
	}
	if changed, err := database.ApproveAdminDeviceBinding(ctx, "root", "primary-handle", "binding-1", 102); err != nil || !changed {
		t.Fatalf("approve 应成功: changed=%v err=%v", changed, err)
	}
	record, err := database.GetAdminDeviceBindingByCookie(ctx, "cookie-hash-1", 103)
	if err != nil || record.Status != "approved" {
		t.Fatalf("approved 状态读取异常: %+v err=%v", record, err)
	}
	created, completed, err := database.CompleteAdminDeviceBinding(ctx, "binding-1", "cookie-hash-1", "secondary-handle", "secondary-token-hash", "Safari", "127.0.0.3", 104, 12*time.Hour)
	if err != nil || !completed || created.Username != "root" || created.IsPrimary {
		t.Fatalf("complete 应创建次设备: %+v completed=%v err=%v", created, completed, err)
	}
	if _, completed, err := database.CompleteAdminDeviceBinding(ctx, "binding-1", "cookie-hash-1", "secondary-handle-2", "secondary-token-hash-2", "Safari", "127.0.0.3", 105, 12*time.Hour); err != nil || completed {
		t.Fatalf("重复 complete 不应创建第二台设备: completed=%v err=%v", completed, err)
	}
}

func TestAdminDeviceBindingStartExpiresPreviousActiveRequest(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "device-binding-replace.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.StartAdminDeviceBinding(ctx, "binding-old", "token-old", "root", "primary-handle", 100); err != nil {
		t.Fatalf("创建旧请求失败: %v", err)
	}
	newBinding, err := database.StartAdminDeviceBinding(ctx, "binding-new", "token-new", "root", "primary-handle", 101)
	if err != nil || newBinding.Status != "pending" {
		t.Fatalf("创建新请求失败: %+v err=%v", newBinding, err)
	}
	if _, ok, err := database.ClaimAdminDeviceBinding(ctx, "token-old", "cookie-old", "Chrome", "127.0.0.2", 102); err != nil || ok {
		t.Fatalf("旧请求不应继续 claim: ok=%v err=%v", ok, err)
	}
}
