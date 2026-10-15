package db

import (
	"path/filepath"
	"testing"
)

func TestExclusiveAddressBookCRUD(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "exclusive-address-book.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-100881001)
	if _, err := database.EnsureGroup(chatID, "专属地址名单测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}

	if err := database.UpsertExclusiveAddress(chatID, "TTuMtmC7SariHaJAfCqc1YkLRWrtCsksCN", "@alice", "靓仔无语"); err != nil {
		t.Fatalf("写入专属地址1失败: %v", err)
	}
	if err := database.UpsertExclusiveAddress(chatID, "TAc2evKh6XQh9A3T9aQX2mA4h7Y3P7n2nZ", "@bob", "财务主通道"); err != nil {
		t.Fatalf("写入专属地址2失败: %v", err)
	}
	if err := database.UpsertExclusiveAddress(chatID, "TTuMtmC7SariHaJAfCqc1YkLRWrtCsksCN", "@neo", "靓仔无语-新备注"); err != nil {
		t.Fatalf("同地址覆盖写入失败: %v", err)
	}

	rows, err := database.ListExclusiveAddresses(chatID)
	if err != nil {
		t.Fatalf("查询专属地址名单失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("专属地址数量异常: want=2 got=%d", len(rows))
	}

	item, ok, err := database.GetExclusiveAddressByRemark(chatID, "靓仔无语-新备注")
	if err != nil {
		t.Fatalf("按备注查询专属地址失败: %v", err)
	}
	if !ok {
		t.Fatalf("应命中最新地址备注")
	}
	if item.Owner != "@neo" {
		t.Fatalf("所属人不正确: got=%q", item.Owner)
	}
	if item.Address != "TTuMtmC7SariHaJAfCqc1YkLRWrtCsksCN" {
		t.Fatalf("地址不正确: got=%q", item.Address)
	}
	if _, ok, err := database.GetExclusiveAddressByRemark(chatID, "靓仔无语"); err != nil {
		t.Fatalf("查询旧备注失败: %v", err)
	} else if ok {
		t.Fatalf("旧备注不应继续命中")
	}

	if err := database.ClearExclusiveAddresses(chatID); err != nil {
		t.Fatalf("清空专属地址名单失败: %v", err)
	}
	cleared, err := database.ListExclusiveAddresses(chatID)
	if err != nil {
		t.Fatalf("清空后查询专属地址名单失败: %v", err)
	}
	if len(cleared) != 0 {
		t.Fatalf("清空后专属地址仍存在: got=%d", len(cleared))
	}
}

func TestDeleteExclusiveAddressesByRemark(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "exclusive-address-delete-by-remark.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-100881101)
	if _, err := database.EnsureGroup(chatID, "专属地址按备注删除测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}

	if err := database.UpsertExclusiveAddress(chatID, "TTuMtmC7SariHaJAfCqc1YkLRWrtCsksCN", "@alice", "ma"); err != nil {
		t.Fatalf("写入专属地址失败: %v", err)
	}
	if err := database.UpsertExclusiveAddress(chatID, "TAc2evKh6XQh9A3T9aQX2mA4h7Y3P7n2nZ", "@bob", "mb"); err != nil {
		t.Fatalf("写入专属地址失败: %v", err)
	}

	removed, err := database.DeleteExclusiveAddressesByRemark(chatID, "ma")
	if err != nil {
		t.Fatalf("按备注删除失败: %v", err)
	}
	if !removed {
		t.Fatalf("应删除成功")
	}
	if _, ok, err := database.GetExclusiveAddressByRemark(chatID, "ma"); err != nil {
		t.Fatalf("查询备注失败: %v", err)
	} else if ok {
		t.Fatalf("被删除备注不应再命中")
	}
	if _, ok, err := database.GetExclusiveAddressByRemark(chatID, "mb"); err != nil {
		t.Fatalf("查询备注失败: %v", err)
	} else if !ok {
		t.Fatalf("未删除备注应继续命中")
	}
}
