package db

import (
	"path/filepath"
	"testing"
)

func TestUpdateGroupTextFieldIfCurrent(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "group-config-cas.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100882901
	if _, err := database.EnsureGroup(chatID, "条件更新测试群"); err != nil {
		t.Fatalf("初始化群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"group_type": "泰国", "group_tags": "VIP,泰国"}); err != nil {
		t.Fatalf("初始化群字段失败: %v", err)
	}
	if _, err := database.GetGroup(chatID); err != nil {
		t.Fatalf("预热群缓存失败: %v", err)
	}

	updated, err := database.UpdateGroupTextFieldIfCurrent(chatID, "group_type", "泰国", "")
	if err != nil {
		t.Fatalf("条件更新群类型失败: %v", err)
	}
	if !updated {
		t.Fatalf("当前类型匹配时应更新成功")
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群失败: %v", err)
	}
	if group.GroupType != "" {
		t.Fatalf("群类型应被清空并刷新缓存: %+v", group)
	}

	if err := database.UpdateGroupFields(chatID, map[string]any{"group_tags": "VIP,泰国,CS2"}); err != nil {
		t.Fatalf("重设群标签失败: %v", err)
	}
	updated, err = database.UpdateGroupTextFieldIfCurrent(chatID, "group_tags", "VIP,泰国", "VIP")
	if err != nil {
		t.Fatalf("条件更新群标签失败: %v", err)
	}
	if updated {
		t.Fatalf("当前标签不匹配时不应更新")
	}
	group, err = database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群失败: %v", err)
	}
	if group.GroupTags != "VIP,泰国,CS2" {
		t.Fatalf("群标签不匹配时不应被旧值覆盖: %+v", group)
	}
}
