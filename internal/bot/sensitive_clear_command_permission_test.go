package bot

import (
	"context"
	"testing"
	"time"

	"go-bot/internal/db"
	"go-bot/internal/utils"
)

func TestSensitiveClearCommandAllowsOperatorWithConfirmation(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "sensitive-clear-command-permission.db")
	chatID := int64(-1004419301)
	ownerID := int64(4419301)
	operatorID := int64(4419302)
	memberID := int64(4419303)

	if _, err := database.EnsureGroup(chatID, "敏感清账权限测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.AddOperator(chatID, operatorID, "clear_op", false); err != nil {
		t.Fatalf("设置群操作人失败: %v", err)
	}

	result, err := database.ApplyLedgerEntry(db.LedgerEntryInput{
		ChatID:           chatID,
		MessageID:        4419300,
		UserID:           ownerID,
		Username:         "owner_clear",
		Nickname:         "OwnerClear",
		EntryType:        "deposit",
		Amount:           3000,
		Currency:         "CNY",
		BaseAmount:       3000,
		FeeRate:          0,
		Rate:             137,
		FinalAmount:      3000.0 / 137.0,
		Note:             "清账前置入款",
		TrackMessageLink: "tg://msg/4419300",
	})
	if err != nil {
		t.Fatalf("写入前置账单失败: %v", err)
	}
	if result.Duplicate || result.EntryID == 0 {
		t.Fatalf("写入前置账单结果异常: %+v", result)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(4419301, 4419301, chatID, memberID, "member_clear", "清理今日数据", baseDate))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(4419302, 4419302, chatID, operatorID, "clear_op", "清理今日数据", baseDate+1))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(4419303, 4419303, chatID, operatorID, "clear_op", "确认清理今日数据", baseDate+2))

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 4419302, "⚠️ 高危操作【清理今日数据】需要二次确认") {
		t.Fatalf("操作人应收到清账二次确认提示: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 4419303, "清理今日数据完成，已删除 1 条数据。") {
		t.Fatalf("操作人确认后应执行清账: %+v", snapshot)
	}

	startAt := utils.GetDayStartISO("00:00:00", app.cfg.Timezone)
	rows, err := database.ListOrderList(chatID, startAt, 10)
	if err != nil {
		t.Fatalf("查询清账后账单失败: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("清理今日数据后应无当日账单残留: %+v", rows)
	}
}
