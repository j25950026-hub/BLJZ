package bot

import (
	"context"
	"testing"
	"time"
)

func TestSetDayCutTimeAllowsOperator(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "day-cut-time-operator-permission.db")

	const (
		chatID     int64 = -1004430101
		ownerID    int64 = 4430101
		operatorID int64 = 4430102
	)
	if _, err := database.EnsureGroup(chatID, "日切时间操作人权限测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.AddOperator(chatID, operatorID, "day_cut_op", false); err != nil {
		t.Fatalf("设置群操作人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		4430103,
		4430103,
		chatID,
		operatorID,
		"day_cut_op",
		"设置日切时间03:30:00",
		baseDate,
	))

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.DayCutTime != "03:30:00" {
		t.Fatalf("操作人应可设置日切时间: got=%q want=%q", group.DayCutTime, "03:30:00")
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 4430103, "日切时间已设置为 03:30:00") {
		t.Fatalf("缺少操作人设置日切时间成功回执: %+v", snapshot)
	}
}

func TestSetDayCutTimeAcceptsHourShortcut(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "day-cut-time-hour-shortcut.db")

	const (
		chatID     int64 = -1004430201
		ownerID    int64 = 4430201
		operatorID int64 = 4430202
	)
	if _, err := database.EnsureGroup(chatID, "日切时间简写测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.AddOperator(chatID, operatorID, "day_cut_shortcut_op", false); err != nil {
		t.Fatalf("设置群操作人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		4430203,
		4430203,
		chatID,
		operatorID,
		"day_cut_shortcut_op",
		"设置日切时间04",
		baseDate,
	))

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.DayCutTime != "04:00:00" {
		t.Fatalf("小时简写应规范化为完整日切时间: got=%q want=%q", group.DayCutTime, "04:00:00")
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 4430203, "日切时间已设置为 04:00:00") {
		t.Fatalf("缺少小时简写设置成功回执: %+v", snapshot)
	}
}
