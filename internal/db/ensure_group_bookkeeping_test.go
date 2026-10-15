package db

import (
	"path/filepath"
	"testing"
)

func TestEnsureGroupDefaultsBookkeepingDisabled(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "ensure-group-bookkeeping.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100778899
	group, err := database.EnsureGroup(chatID, "默认记账开关测试群")
	if err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 0 {
		t.Fatalf("新建群默认应关闭记账: got=%d want=0", group.BookkeepingEnabled)
	}
	if group.FeeRate != 0 {
		t.Fatalf("新建群默认入款费率应为 0: got=%v", group.FeeRate)
	}
	if group.PayoutFeeRate != 0 {
		t.Fatalf("新建群默认下发费率应为 0: got=%v", group.PayoutFeeRate)
	}
	if group.CustomRate != 1 {
		t.Fatalf("新建群默认入款汇率应为 1: got=%v", group.CustomRate)
	}
	if group.PayoutRate != 1 {
		t.Fatalf("新建群默认下发汇率应为 1: got=%v", group.PayoutRate)
	}

	if err := database.UpdateGroupFields(chatID, map[string]any{"bookkeeping_enabled": 1}); err != nil {
		t.Fatalf("更新记账开关失败: %v", err)
	}
	group, err = database.EnsureGroup(chatID, "默认记账开关测试群")
	if err != nil {
		t.Fatalf("读取已存在群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 1 {
		t.Fatalf("已存在群配置不应被 EnsureGroup 重置: got=%d want=1", group.BookkeepingEnabled)
	}
}
