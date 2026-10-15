package db

import (
	"path/filepath"
	"testing"
)

func TestAdminRetryItemsAreIdempotent(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "admin-retry-items.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	first, err := database.SaveAdminRetryItem(AdminRetryItem{
		OperationID:   "op-1",
		ItemKey:       "group:-1001",
		Kind:          "group_batch",
		ChatID:        -1001,
		PayloadJSON:   `{"field":"welcome_enabled","value":false}`,
		FailureReason: "配置已变化",
		Status:        "failed",
	})
	if err != nil {
		t.Fatalf("保存重试项失败: %v", err)
	}
	second, err := database.SaveAdminRetryItem(AdminRetryItem{
		OperationID:   "op-1",
		ItemKey:       "group:-1001",
		Kind:          "group_batch",
		ChatID:        -1001,
		PayloadJSON:   `{"field":"welcome_enabled","value":true}`,
		FailureReason: "最新配置仍不一致",
		Status:        "failed",
	})
	if err != nil {
		t.Fatalf("重复保存重试项失败: %v", err)
	}
	if first.ID == 0 || second.ID != first.ID {
		t.Fatalf("同一操作项应幂等复用 ID: first=%+v second=%+v", first, second)
	}

	items, err := database.ListAdminRetryItems("failed", 10)
	if err != nil {
		t.Fatalf("读取失败重试项失败: %v", err)
	}
	if len(items) != 1 || items[0].FailureReason != "最新配置仍不一致" || items[0].PayloadJSON != `{"field":"welcome_enabled","value":true}` {
		t.Fatalf("重试项更新错误: %+v", items)
	}
	updated, err := database.UpdateAdminRetryItem(first.ID, "completed", "", true)
	if err != nil || !updated {
		t.Fatalf("标记重试项完成失败: updated=%v err=%v", updated, err)
	}
	items, err = database.ListAdminRetryItems("failed", 10)
	if err != nil {
		t.Fatalf("读取完成后的失败重试项失败: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("完成项不应继续出现在失败列表: %+v", items)
	}
	completed, err := database.ListAdminRetryItems("completed", 10)
	if err != nil || len(completed) != 1 || completed[0].AttemptCount != 1 {
		t.Fatalf("完成项状态或尝试次数错误: items=%+v err=%v", completed, err)
	}
}

func TestAdminRetryItemsRejectInvalidPayload(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "admin-retry-items-invalid.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()
	if _, err := database.SaveAdminRetryItem(AdminRetryItem{OperationID: "op", ItemKey: "item", PayloadJSON: `{"`}); err == nil {
		t.Fatalf("非法重试 payload 应被拒绝")
	}
}
