package bot

import "testing"

func TestAdminBroadcastTargetsOnlySelectedGroups(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "admin-broadcast-selection.db")
	for index, chatID := range []int64{-1009101, -1009102, -1009103} {
		if _, err := database.EnsureGroup(chatID, "选群测试"); err != nil {
			t.Fatalf("创建测试群 %d 失败: %v", index+1, err)
		}
	}

	sent, err := app.AdminBroadcast(AdminBroadcastRequest{
		TargetChatIDs: []int64{-1009103, -1009101, -1009103},
		MediaType:     "text",
		Content:       "仅发送到已选群",
	})
	if err != nil {
		t.Fatalf("选群群发失败: %v", err)
	}
	if sent != 2 {
		t.Fatalf("重复目标应去重并发送到两个群: got=%d", sent)
	}
	messages := capture.snapshot()
	if len(messages) != 2 || messages[0].chatID != -1009103 || messages[1].chatID != -1009101 {
		t.Fatalf("群发应只按选择顺序发送到目标群: %+v", messages)
	}
}
