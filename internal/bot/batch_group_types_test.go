package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBatchSetGroupTypesSelectionAndApply(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "batch-group-types-selection.db")
	sourceChatID := int64(-100782001)
	ownerID := int64(782001)

	groupIDs := []int64{
		sourceChatID,
		-100782002,
		-100782003,
		-100782004,
		-100782005,
		-100782006,
		-100782007,
		-100782008,
		-100782009,
		-100782010,
		-100782011,
		-100782012,
		-100782013,
		-100782014,
		-100782015,
	}
	for i, chatID := range groupIDs {
		if _, err := database.EnsureGroup(chatID, fmt.Sprintf("批量类型群-%02d", i+1)); err != nil {
			t.Fatalf("初始化群失败(chat=%d): %v", chatID, err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置权限人失败(chat=%d): %v", chatID, err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{"group_type": "旧类型"}); err != nil {
			t.Fatalf("初始化群类型失败(chat=%d): %v", chatID, err)
		}
	}

	app.isGroupMemberFn = func(chatID, userID int64) bool {
		return true
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		782101, 782101, sourceChatID, ownerID, "owner_batch", "批量设置群类型", baseDate,
	))

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, sourceChatID, 782101, "批量设置群类型（可设置群列表）") {
		t.Fatalf("缺少批量设置群类型入口回执: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 782101, "页码：1/2（每页10群）") {
		t.Fatalf("批量设置群类型分页信息异常: %+v", snapshot)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		782102, 782102, sourceChatID, ownerID, "owner_batch", "1 2 3 代收", baseDate+1,
	))

	snapshot = capture.snapshot()
	if !fourLayerHasReply(snapshot, sourceChatID, 782102, "批量设置群类型结果：") {
		t.Fatalf("缺少批量设置群类型执行回执: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 782102, "目标类型：代收") {
		t.Fatalf("目标类型回执异常: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 782102, "输入序号：3") {
		t.Fatalf("输入序号统计异常: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 782102, "有效序号：3") {
		t.Fatalf("有效序号统计异常: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 782102, "设置成功：3") {
		t.Fatalf("成功数量统计异常: %+v", snapshot)
	}

	for _, chatID := range []int64{-100782015, -100782014, -100782013} {
		group, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取目标群失败(chat=%d): %v", chatID, err)
		}
		if got := strings.TrimSpace(group.GroupType); got != "代收" {
			t.Fatalf("目标群类型未更新(chat=%d): got=%q", chatID, got)
		}
	}

	untouched, err := database.GetGroup(-100782012)
	if err != nil {
		t.Fatalf("读取未选中群失败: %v", err)
	}
	if got := strings.TrimSpace(untouched.GroupType); got != "旧类型" {
		t.Fatalf("未选中群类型不应变化: got=%q", got)
	}
}
