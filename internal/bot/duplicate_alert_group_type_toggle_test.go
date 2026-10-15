package bot

import (
	"context"
	"testing"
	"time"
)

func TestDuplicateAlertToggleByGroupType(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "duplicate-alert-toggle-by-type.db")
	app.commandVelocity = newUserCommandVelocityChecker(time.Second, 100)
	const (
		groupType = "ICF"
		ownerID   = int64(771001)
		chatA     = int64(-100771001)
		chatB     = int64(-100771002)
	)
	for _, chatID := range []int64{chatA, chatB} {
		if _, err := database.EnsureGroup(chatID, "重复预警群类型开关测试群"); err != nil {
			t.Fatalf("初始化群配置失败: chat=%d err=%v", chatID, err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置群权限人失败: chat=%d err=%v", chatID, err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{"group_type": groupType}); err != nil {
			t.Fatalf("设置群类型失败: chat=%d err=%v", chatID, err)
		}
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(771101, 771101, chatA, ownerID, "owner_toggle", "开启重复预警", baseDate))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(771102, 771102, chatA, ownerID, "owner_toggle", "关闭重复预警", baseDate+1))

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatA, 771101, "重复预警已开启") {
		t.Fatalf("开启重复预警回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatA, 771102, "重复预警已关闭") {
		t.Fatalf("关闭重复预警回执缺失: %+v", snapshot)
	}

	groupA, err := database.GetGroup(chatA)
	if err != nil {
		t.Fatalf("读取群A配置失败: %v", err)
	}
	groupB, err := database.GetGroup(chatB)
	if err != nil {
		t.Fatalf("读取群B配置失败: %v", err)
	}
	if groupA.DuplicateAlertEnabled != 0 || groupB.DuplicateAlertEnabled != 0 {
		t.Fatalf("关闭后同类型群应全部关闭: groupA=%d groupB=%d", groupA.DuplicateAlertEnabled, groupB.DuplicateAlertEnabled)
	}
}

func TestDuplicateCardWarningAcrossGroupType(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "duplicate-card-warning-by-type.db")
	app.commandVelocity = newUserCommandVelocityChecker(time.Second, 100)
	const (
		groupType = "ICF"
		cardNo    = "4539578763621486"
		ownerID   = int64(772001)
		chatA     = int64(-100772001)
		chatB     = int64(-100772002)
	)
	for _, chatID := range []int64{chatA, chatB} {
		if _, err := database.EnsureGroup(chatID, "重复预警跨群计数测试群"); err != nil {
			t.Fatalf("初始化群配置失败: chat=%d err=%v", chatID, err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置群权限人失败: chat=%d err=%v", chatID, err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{
			"group_type": groupType,
		}); err != nil {
			t.Fatalf("设置群配置失败: chat=%d err=%v", chatID, err)
		}
	}

	baseDate := int(time.Now().Unix())
	updates := []struct {
		updateID  int
		messageID int
		chatID    int64
		text      string
		date      int
	}{
		{772098, 772098, chatA, "开始记账", baseDate},
		{772099, 772099, chatB, "开始记账", baseDate + 1},
		{772100, 772100, chatA, "开启重复预警", baseDate + 2},
		{772101, 772101, chatA, "+100#" + cardNo, baseDate + 3},
		{772102, 772102, chatA, "+200#" + cardNo, baseDate + 4},
		{772103, 772103, chatB, "+300#" + cardNo, baseDate + 5},
	}
	for _, item := range updates {
		app.dispatchUpdate(
			context.Background(),
			fourLayerGroupTextUpdate(item.updateID, item.messageID, item.chatID, ownerID, "owner_duplicate", item.text, item.date),
		)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatA, 772102, "当前入款:") {
		t.Fatalf("业务应继续正常记账: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatB, 772103, "当前入款:") {
		t.Fatalf("跨群业务应继续正常记账: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatA, 772102, "当前信息重复发送") || fourLayerHasReply(snapshot, chatB, 772103, "当前信息重复发送") {
		t.Fatalf("业务风控关闭后不应再提示卡号重复预警: %+v", snapshot)
	}
}

func TestPlainTextDuplicateWarningAcrossGroupType(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "duplicate-plain-warning-by-type.db")
	app.commandVelocity = newUserCommandVelocityChecker(time.Second, 100)
	const (
		groupType = "ICF"
		ownerID   = int64(773001)
		chatA     = int64(-100773001)
		chatB     = int64(-100773002)
	)
	for _, chatID := range []int64{chatA, chatB} {
		if _, err := database.EnsureGroup(chatID, "纯文本重复预警跨群计数测试群"); err != nil {
			t.Fatalf("初始化群配置失败: chat=%d err=%v", chatID, err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置群权限人失败: chat=%d err=%v", chatID, err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{
			"group_type": groupType,
		}); err != nil {
			t.Fatalf("设置群配置失败: chat=%d err=%v", chatID, err)
		}
	}

	sample := "Bank Name :Canara Bank\nAccount :110288285406\nIFSC CODE: CNRB0001258\nName: Birba"
	baseDate := int(time.Now().Unix())
	updates := []struct {
		updateID  int
		messageID int
		chatID    int64
		text      string
		date      int
	}{
		{773100, 773100, chatA, "开启重复预警", baseDate},
		{773101, 773101, chatA, sample, baseDate + 1},
		{773102, 773102, chatA, sample, baseDate + 2},
		{773103, 773103, chatB, sample, baseDate + 3},
	}
	for _, item := range updates {
		app.dispatchUpdate(
			context.Background(),
			fourLayerGroupTextUpdate(item.updateID, item.messageID, item.chatID, ownerID, "owner_plain_duplicate", item.text, item.date),
		)
	}

	snapshot := capture.snapshot()
	if fourLayerHasReply(snapshot, chatA, 773102, "当前信息重复发送") || fourLayerHasReply(snapshot, chatB, 773103, "当前信息重复发送") {
		t.Fatalf("业务风控关闭后不应再提示纯文本重复预警: %+v", snapshot)
	}
}
