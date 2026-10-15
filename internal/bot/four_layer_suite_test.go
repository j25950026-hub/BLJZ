package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestFourLayerSmokeScenario(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "four-layer-smoke.db")

	chatID := int64(-1004401001)
	ownerID := int64(4401001)
	if _, err := database.EnsureGroup(chatID, "四层冒烟测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	updates := []struct {
		upd tgbotapi.Update
	}{
		{upd: fourLayerPrivateTextUpdate(440101, 440101, ownerID, "owner_smoke", "申请试用", baseDate)},
		{upd: fourLayerPrivateTextUpdate(440102, 440102, ownerID, "owner_smoke", "购买使用", baseDate+1)},
		{upd: fourLayerGroupTextUpdate(440103, 440103, chatID, ownerID, "owner_smoke", "开始记账", baseDate+2)},
		{upd: fourLayerGroupTextUpdate(440104, 440104, chatID, ownerID, "owner_smoke", "+88#四层冒烟", baseDate+3)},
		{upd: fourLayerGroupTextUpdate(440105, 440105, chatID, ownerID, "owner_smoke", "一键账单", baseDate+4)},
	}
	for _, item := range updates {
		app.dispatchUpdate(context.Background(), item.upd)
	}

	depositTotal, payoutTotal, err := database.GetLedgerBalance(chatID)
	if err != nil {
		t.Fatalf("查询账务余额失败: %v", err)
	}
	if !almostZero(depositTotal - 88) {
		t.Fatalf("入款总额异常: want=88 got=%v", depositTotal)
	}
	if !almostZero(payoutTotal) {
		t.Fatalf("下发总额异常: want=0 got=%v", payoutTotal)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, ownerID, 440101, "联系管理员") {
		t.Fatalf("关闭使用申请后应提示联系管理员: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, ownerID, 440102, "购买使用") {
		t.Fatalf("缺少私聊购买入口回执: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440103, "已开启记账") {
		t.Fatalf("缺少群内开始记账回执: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440104, "当前入款:") {
		t.Fatalf("缺少群内记账卡片回执: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440105, "一键账单（日切") {
		t.Fatalf("缺少群内一键账单回执: %+v", snapshot)
	}
}

func TestFourLayerPermissionAndExceptionScenario(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "four-layer-permission.db")

	chatID := int64(-1004402001)
	ownerID := int64(4402001)
	localOperatorID := int64(4402002)
	globalOperatorID := int64(4402003)
	memberID := int64(4402099)
	memberBID := int64(4402100)

	if _, err := database.EnsureGroup(chatID, "四层权限测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.AddOperator(chatID, localOperatorID, "local_op", false); err != nil {
		t.Fatalf("设置本群操作人失败: %v", err)
	}
	if err := database.AddOperator(chatID, globalOperatorID, "global_op", true); err != nil {
		t.Fatalf("设置全局操作人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	transferAttemptByOperator := tgbotapi.Update{
		UpdateID: 440210,
		Message: &tgbotapi.Message{
			MessageID: 440210,
			Date:      baseDate + 9,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "四层测试群",
			},
			From: &tgbotapi.User{
				ID:        localOperatorID,
				UserName:  "local_op",
				FirstName: "local_op",
			},
			Text: "转移本群权限人",
			ReplyToMessage: &tgbotapi.Message{
				From: &tgbotapi.User{
					ID:        memberID,
					UserName:  "member_a",
					FirstName: "member_a",
				},
			},
		},
	}
	updates := []tgbotapi.Update{
		fourLayerGroupTextUpdate(440201, 440201, chatID, memberID, "member_a", "开启自动回复", baseDate),
		fourLayerGroupTextUpdate(440202, 440202, chatID, localOperatorID, "local_op", "设置担保规则 操作人尝试", baseDate+1),
		fourLayerGroupTextUpdate(440203, 440203, chatID, globalOperatorID, "global_op", "自动回复列表", baseDate+2),
		fourLayerGroupTextUpdate(440204, 440204, chatID, ownerID, "owner_main", "设置担保规则", baseDate+3),
		fourLayerGroupTextUpdate(440205, 440205, chatID, ownerID, "owner_main", "设置担保规则仅限权限人可改", baseDate+4),
		fourLayerPrivateTextUpdate(440206, 440206, memberID, "member_a", "到期时间", baseDate+5),
		fourLayerGroupTextUpdate(440207, 440207, chatID, ownerID, "owner_main", "查联行号", baseDate+6),
		fourLayerGroupTextUpdate(440208, 440208, chatID, memberID, "member_a", "@所有人", baseDate+7),
		fourLayerGroupTextUpdate(440209, 440209, chatID, memberID, "member_a", "显示全局操作人", baseDate+8),
		transferAttemptByOperator,
		fourLayerGroupTextUpdate(440211, 440211, chatID, ownerID, "owner_main", "显示全局操作人", baseDate+10),
		fourLayerGroupTextUpdate(440212, 440212, chatID, memberID, "member_a", "设置欧易交易所", baseDate+11),
		fourLayerGroupTextUpdate(440213, 440213, chatID, ownerID, "owner_main", "设置币安交易所", baseDate+12),
		fourLayerGroupTextUpdate(440214, 440214, chatID, memberID, "member_a", "查询地址 TRxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", baseDate+13),
		fourLayerGroupTextUpdate(440215, 440215, chatID, memberBID, "member_b", "查卡6222020202020202", baseDate+14),
		fourLayerGroupTextUpdate(440216, 440216, chatID, memberBID, "member_b", "+100#普通成员记账尝试", baseDate+15),
		fourLayerGroupTextUpdate(440217, 440217, chatID, localOperatorID, "local_op", "担保", baseDate+16),
	}
	for _, upd := range updates {
		app.dispatchUpdate(context.Background(), upd)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.GuaranteeRule != "仅限权限人可改" {
		t.Fatalf("担保规则更新失败: want=%q got=%q", "仅限权限人可改", group.GuaranteeRule)
	}
	if group.OwnerUserID != ownerID {
		t.Fatalf("操作人不应越权转移权限人: want=%d got=%d", ownerID, group.OwnerUserID)
	}
	if group.RateSource != "binance" {
		t.Fatalf("交易所应仅允许权限人切换: want=%q got=%q", "binance", group.RateSource)
	}

	snapshot := capture.snapshot()
	if fourLayerHasAnyReply(snapshot, chatID, 440201) {
		t.Fatalf("普通成员越权开启自动回复应静默: %+v", snapshot)
	}
	if fourLayerHasAnyReply(snapshot, chatID, 440202) {
		t.Fatalf("操作人越权设置担保规则应静默: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440203, "当前无自动回复规则。") {
		t.Fatalf("全局操作人查询回执不符合预期: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440204, "格式：设置担保规则 你的规则内容") {
		t.Fatalf("异常输入格式提示缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440205, "担保规则已更新。") {
		t.Fatalf("权限人更新担保规则回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, memberID, 440206, "当前无有效使用权限") {
		t.Fatalf("私聊到期时间异常分支回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440207, "格式：查联行号+银行卡号") {
		t.Fatalf("查联行号格式校验回执缺失: %+v", snapshot)
	}
	if fourLayerHasAnyReply(snapshot, chatID, 440208) {
		t.Fatalf("普通成员@所有人应静默: %+v", snapshot)
	}
	if fourLayerHasAnyReply(snapshot, chatID, 440209) {
		t.Fatalf("普通成员读取全局操作人应静默: %+v", snapshot)
	}
	if fourLayerHasAnyReply(snapshot, chatID, 440210) {
		t.Fatalf("操作人转移权限人应静默: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440211, "全局操作人：") {
		t.Fatalf("权限人查看全局操作人回执缺失: %+v", snapshot)
	}
	if fourLayerHasAnyReply(snapshot, chatID, 440212) {
		t.Fatalf("普通成员设置交易所应静默: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440213, "已切换交易所为 BINANCE") {
		t.Fatalf("权限人设置交易所回执缺失: %+v", snapshot)
	}
	if fourLayerHasAnyReply(snapshot, chatID, 440214) {
		t.Fatalf("普通成员无效地址查询应静默: %+v", snapshot)
	}
	if fourLayerHasAnyReply(snapshot, chatID, 440215) {
		t.Fatalf("普通成员查卡应静默: %+v", snapshot)
	}
	if fourLayerHasAnyReply(snapshot, chatID, 440216) {
		t.Fatalf("普通成员加减分应静默: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440217, "仅限权限人可改") {
		t.Fatalf("操作人触发担保规则回执缺失: %+v", snapshot)
	}
}

func TestFourLayerExpiryDisablesGroupFeatures(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "four-layer-expire-disable.db")

	chatID := int64(-1004403001)
	ownerID := int64(4403001)
	if _, err := database.EnsureGroup(chatID, "四层到期停用测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	expiredAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := database.UpsertPrivateUserProfile(db.PrivateUserProfile{
		UserID:         ownerID,
		TrialStatus:    "expired",
		TrialAppliedAt: expiredAt.Add(-24 * time.Hour).Format(time.RFC3339),
		ExpireAt:       expiredAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("写入到期私聊权限失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		440301, 440301, chatID, ownerID, "owner_expired", "开始记账", baseDate,
	))

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 440301, "当前群功能已禁用：权限人使用权限已于") {
		t.Fatalf("到期停用提示缺失: %+v", snapshot)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 0 {
		t.Fatalf("到期后应强制停用记账: got=%d", group.BookkeepingEnabled)
	}
	if err := app.ExecuteWebAppCommand(ownerID, chatID, "一键账单"); err == nil || !strings.Contains(err.Error(), "当前群功能已禁用") {
		t.Fatalf("WebApp 到期拦截不符合预期: err=%v", err)
	}

	renewedAt := time.Now().UTC().Add(24 * time.Hour)
	if err := database.UpsertPrivateUserProfile(db.PrivateUserProfile{
		UserID:         ownerID,
		TrialStatus:    "active",
		TrialAppliedAt: time.Now().UTC().Format(time.RFC3339),
		ExpireAt:       renewedAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("写入续期私聊权限失败: %v", err)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		440302, 440302, chatID, ownerID, "owner_expired", "开始记账", baseDate+1,
	))
	snapshot = capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 440302, "已开启记账。") {
		t.Fatalf("续期后应恢复群功能: %+v", snapshot)
	}
	group, err = database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取续期后群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 1 {
		t.Fatalf("续期后应允许开启记账: got=%d", group.BookkeepingEnabled)
	}
}

func TestFourLayerOperatorCanTransferOwnerWhenExpired(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "four-layer-expire-transfer.db")

	chatID := int64(-1004404001)
	ownerID := int64(4404001)
	operatorID := int64(4404002)
	newOwnerID := int64(4404003)
	if _, err := database.EnsureGroup(chatID, "四层到期转移权限测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if err := database.AddOperator(chatID, operatorID, "operator_expired", false); err != nil {
		t.Fatalf("设置本群操作人失败: %v", err)
	}
	expiredAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := database.UpsertPrivateUserProfile(db.PrivateUserProfile{
		UserID:         ownerID,
		TrialStatus:    "expired",
		TrialAppliedAt: expiredAt.Add(-24 * time.Hour).Format(time.RFC3339),
		ExpireAt:       expiredAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("写入到期私聊权限失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	transfer := tgbotapi.Update{
		UpdateID: 440401,
		Message: &tgbotapi.Message{
			MessageID: 440401,
			Date:      baseDate,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "四层测试群",
			},
			From: &tgbotapi.User{
				ID:        operatorID,
				UserName:  "operator_expired",
				FirstName: "operator_expired",
			},
			Text: "转移本群权限人",
			ReplyToMessage: &tgbotapi.Message{
				From: &tgbotapi.User{
					ID:        newOwnerID,
					UserName:  "new_owner",
					FirstName: "new_owner",
				},
			},
		},
	}
	app.dispatchUpdate(context.Background(), transfer)

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.OwnerUserID != newOwnerID {
		t.Fatalf("到期场景应允许操作人转移权限人: want=%d got=%d", newOwnerID, group.OwnerUserID)
	}
	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 440401, "当前群功能已禁用：权限人使用权限已于") {
		t.Fatalf("到期转移权限人应提示续费文案: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 440401, "权限人已转移为 @new_owner") {
		t.Fatalf("到期转移权限人回执缺失: %+v", snapshot)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		440402, 440402, chatID, newOwnerID, "new_owner", "开始记账", baseDate+1,
	))
	snapshot = capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 440402, "已开启记账。") {
		t.Fatalf("转移后新权限人应可恢复业务: %+v", snapshot)
	}
}
