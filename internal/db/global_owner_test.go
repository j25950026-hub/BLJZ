package db

import (
	"path/filepath"
	"testing"
)

func TestGlobalOwnerPrivilegeAcrossGroups(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "global-owner.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatA := int64(-1007701001)
	chatB := int64(-1007701002)
	ownerA := int64(7701001)
	globalOwner := int64(7701099)

	if _, err := database.EnsureGroup(chatA, "全局权限人测试A"); err != nil {
		t.Fatalf("初始化群A失败: %v", err)
	}
	if _, err := database.EnsureGroup(chatB, "全局权限人测试B"); err != nil {
		t.Fatalf("初始化群B失败: %v", err)
	}
	if err := database.SetOwner(chatA, ownerA); err != nil {
		t.Fatalf("设置群A权限人失败: %v", err)
	}

	if database.IsOwner(chatA, globalOwner) {
		t.Fatalf("未设置前不应识别为权限人")
	}
	if err := database.AddGlobalOwner(globalOwner, "global_owner_77"); err != nil {
		t.Fatalf("新增全局权限人失败: %v", err)
	}
	if !database.IsGlobalOwner(globalOwner) {
		t.Fatalf("应识别为全局权限人")
	}
	if !database.IsOwner(chatA, globalOwner) || !database.IsOwner(chatB, globalOwner) {
		t.Fatalf("全局权限人应跨群具备权限人能力")
	}
	if !database.IsPrivileged(chatB, globalOwner) {
		t.Fatalf("全局权限人应具备特权能力")
	}
	rows, err := database.ListGlobalOwners()
	if err != nil {
		t.Fatalf("查询全局权限人失败: %v", err)
	}
	if len(rows) != 1 || rows[0].UserID != globalOwner {
		t.Fatalf("全局权限人列表异常: %+v", rows)
	}

	if err := database.RemoveGlobalOwner(globalOwner); err != nil {
		t.Fatalf("删除全局权限人失败: %v", err)
	}
	if database.IsGlobalOwner(globalOwner) {
		t.Fatalf("删除后不应再识别为全局权限人")
	}
	if database.IsOwner(chatA, globalOwner) || database.IsOwner(chatB, globalOwner) {
		t.Fatalf("删除后不应再具备跨群权限人能力")
	}
}
