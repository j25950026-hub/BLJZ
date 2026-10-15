package db

import (
	"path/filepath"
	"testing"
)

func TestAdminGroupDictionaryItemsRoundTrip(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "admin-dictionaries.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	if err := database.SaveAdminGroupDictionaryItem("group_type", "业务", "业务群", 20); err != nil {
		t.Fatalf("保存群类型字典失败: %v", err)
	}
	if err := database.SaveAdminGroupDictionaryItem("group_type", "测试", "测试群", 10); err != nil {
		t.Fatalf("保存第二个群类型字典失败: %v", err)
	}
	if err := database.SaveAdminGroupDictionaryItem("group_type", "业务", "核心业务群", 30); err != nil {
		t.Fatalf("更新群类型字典失败: %v", err)
	}

	items, err := database.ListAdminGroupDictionaryItems("group_type")
	if err != nil {
		t.Fatalf("读取群类型字典失败: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应返回两个群类型字典: %+v", items)
	}
	if items[0].Name != "测试" || items[1].Name != "业务" {
		t.Fatalf("字典应按 sort_order/name 排序: %+v", items)
	}
	if items[1].Description != "核心业务群" || items[1].SortOrder != 30 {
		t.Fatalf("重复保存应更新描述和排序: %+v", items[1])
	}

	if err := database.DeleteAdminGroupDictionaryItem("group_type", "测试"); err != nil {
		t.Fatalf("删除群类型字典失败: %v", err)
	}
	items, err = database.ListAdminGroupDictionaryItems("group_type")
	if err != nil {
		t.Fatalf("删除后读取群类型字典失败: %v", err)
	}
	if len(items) != 1 || items[0].Name != "业务" {
		t.Fatalf("删除后应只保留业务字典: %+v", items)
	}
}
