package bot

import "testing"

func TestListWebAppAccessibleGroupsIncludesMembersWithoutCommandPrivileges(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "webapp-accessible-groups.db")
	userID := int64(771001)
	ownerGroupID := int64(-100771001)
	memberGroupID := int64(-100771002)
	outsiderGroupID := int64(-100771003)

	for _, item := range []struct {
		chatID int64
		title  string
	}{
		{ownerGroupID, "权限群"},
		{memberGroupID, "普通成员群"},
		{outsiderGroupID, "非成员群"},
	} {
		if _, err := database.EnsureGroup(item.chatID, item.title); err != nil {
			t.Fatalf("初始化群失败: chat_id=%d err=%v", item.chatID, err)
		}
	}
	if err := database.SetOwner(ownerGroupID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}
	if err := database.SetOwner(memberGroupID, 880001); err != nil {
		t.Fatalf("设置普通成员群权限人失败: %v", err)
	}
	if err := database.SetOwner(outsiderGroupID, 880002); err != nil {
		t.Fatalf("设置非成员群权限人失败: %v", err)
	}

	app.isGroupMemberFn = func(chatID, checkUserID int64) bool {
		return chatID == memberGroupID && checkUserID == userID
	}

	rows, err := app.ListWebAppAccessibleGroups(userID)
	if err != nil {
		t.Fatalf("读取 WebApp 可访问群失败: %v", err)
	}
	got := map[int64]bool{}
	for _, row := range rows {
		got[row.ChatID] = true
	}
	if !got[ownerGroupID] {
		t.Fatalf("权限群应保留在 WebApp 可访问列表: %+v", rows)
	}
	if !got[memberGroupID] {
		t.Fatalf("普通群成员应能打开 WebApp 并看到所在群: %+v", rows)
	}
	if got[outsiderGroupID] {
		t.Fatalf("非群成员不应看到无关群: %+v", rows)
	}
}
