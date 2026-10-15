package db

import (
	"path/filepath"
	"testing"
)

func TestGroupLedgerCardHideReversedDefaultsOnAndCanToggle(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "ledger-card-visibility.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -10099501
	group, err := database.EnsureGroup(chatID, "账单卡片隐藏测试群")
	if err != nil {
		t.Fatalf("创建群失败: %v", err)
	}
	if group.LedgerCardHideReversed != 1 {
		t.Fatalf("新群默认应隐藏已撤回账单行: %+v", group)
	}

	if err := database.UpdateGroupFields(chatID, map[string]any{"ledger_card_hide_reversed": 0}); err != nil {
		t.Fatalf("关闭账单隐藏开关失败: %v", err)
	}
	group, err = database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取关闭后的群配置失败: %v", err)
	}
	if group.LedgerCardHideReversed != 0 {
		t.Fatalf("账单隐藏开关应可关闭: %+v", group)
	}

	if err := database.UpdateGroupFields(chatID, map[string]any{
		"ledger_card_hide_reversed": 1,
		"bookkeeping_enabled":       1,
		"group_type":                "账单测试",
		"group_tags":                "账单,回归",
	}); err != nil {
		t.Fatalf("准备群查询回归数据失败: %v", err)
	}
	queries := []struct {
		name string
		rows []GroupConfig
	}{
		{name: "all", rows: mustListGroups(t, database.ListAllGroups)},
		{name: "enabled", rows: mustListGroups(t, database.ListEnabledGroups)},
		{name: "type", rows: mustListGroupsWithString(t, database.ListGroupsByType, "账单测试")},
		{name: "tags", rows: mustListGroupsWithTags(t, database.ListGroupsByTags, []string{"回归"})},
	}
	for _, query := range queries {
		found := false
		for _, item := range query.rows {
			if item.ChatID == chatID {
				found = true
				if item.LedgerCardHideReversed != 1 {
					t.Fatalf("%s 查询未传播账单隐藏字段: %+v", query.name, item)
				}
			}
		}
		if !found {
			t.Fatalf("%s 查询未返回测试群: %+v", query.name, query.rows)
		}
	}
}

func mustListGroups(t *testing.T, fn func() ([]GroupConfig, error)) []GroupConfig {
	t.Helper()
	rows, err := fn()
	if err != nil {
		t.Fatalf("群查询失败: %v", err)
	}
	return rows
}

func mustListGroupsWithString(t *testing.T, fn func(string) ([]GroupConfig, error), value string) []GroupConfig {
	t.Helper()
	rows, err := fn(value)
	if err != nil {
		t.Fatalf("群类型查询失败: %v", err)
	}
	return rows
}

func mustListGroupsWithTags(t *testing.T, fn func([]string) ([]GroupConfig, error), values []string) []GroupConfig {
	t.Helper()
	rows, err := fn(values)
	if err != nil {
		t.Fatalf("群标签查询失败: %v", err)
	}
	return rows
}
