package bot

import (
	"context"
	"testing"
	"time"

	"go-bot/internal/services"
)

func TestMemberCanQueryAddressAfterPayoutAddressConfigured(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-address-query.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100558001)
	ownerID := int64(558001)
	memberID := int64(558002)
	payoutAddress := "0x0123456789abcdef0123456789ABCDEF01234567"
	payoutRemark := "222"

	if _, err := database.EnsureGroup(chatID, "地址公开查询测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558101, 558101, chatID, ownerID, "owner_addr", "设置下发地址 "+payoutAddress+" "+payoutRemark, baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558102, 558102, chatID, memberID, "member_addr", "地址", baseDate+1,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558103, 558103, chatID, memberID, "member_addr", "下发地址", baseDate+2,
	))

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.PayoutAddress != payoutAddress {
		t.Fatalf("下发地址未正确写入: want=%q got=%q", payoutAddress, group.PayoutAddress)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 558101, "设置下发地址成功。") {
		t.Fatalf("设置成功回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558102, "下发地址：") || !fourLayerHasReply(snapshot, chatID, 558102, payoutAddress) {
		t.Fatalf("普通成员发送地址未返回下发地址: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558102, "备注信息："+payoutRemark) {
		t.Fatalf("普通成员发送地址未返回备注模板: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558102, "点击上方地址即可复制地址") {
		t.Fatalf("普通成员发送地址未返回复制提示: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558103, "下发地址：") || !fourLayerHasReply(snapshot, chatID, 558103, payoutAddress) {
		t.Fatalf("普通成员发送下发地址未返回地址详情: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558103, "备注信息："+payoutRemark) {
		t.Fatalf("普通成员发送下发地址未返回备注模板: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558103, "点击上方地址即可复制地址") {
		t.Fatalf("普通成员发送下发地址未返回复制提示: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 558102, "权限不足：普通群员仅可使用计算器表达式。") {
		t.Fatalf("普通成员发送地址不应触发权限拦截: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 558103, "权限不足：普通群员仅可使用计算器表达式。") {
		t.Fatalf("普通成员发送下发地址不应触发权限拦截: %+v", snapshot)
	}
}

func TestMemberQueryAddressAfterPayoutAddressCleared(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-address-query-after-clear.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100558011)
	ownerID := int64(558011)
	memberID := int64(558012)
	payoutAddress := "0x0123456789abcdef0123456789ABCDEF01234567"
	payoutRemark := "222"

	if _, err := database.EnsureGroup(chatID, "地址清除后查询测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558111, 558111, chatID, ownerID, "owner_addr", "设置下发地址 "+payoutAddress+" "+payoutRemark, baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558112, 558112, chatID, ownerID, "owner_addr", "清除下发地址", baseDate+1,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558113, 558113, chatID, memberID, "member_addr", "地址", baseDate+2,
	))

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if got := group.PayoutAddress; got != "" {
		t.Fatalf("下发地址应已清空: got=%q", got)
	}
	if got := group.PayoutAddressRemark; got != "" {
		t.Fatalf("下发地址备注应已清空: got=%q", got)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 558112, "下发地址已清除。") {
		t.Fatalf("清除成功回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558113, "下发地址未设置。") {
		t.Fatalf("清除后成员发送地址应提示下发地址未设置: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 558113, "下发地址：") {
		t.Fatalf("清除后成员发送地址不应再返回下发地址: %+v", snapshot)
	}
}

func TestMemberCanQueryExclusiveAddressAfterConfigured(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-exclusive-address-query.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100558101)
	ownerID := int64(558101)
	memberID := int64(558102)
	exclusiveAddress := "0x742d35Cc6634C0532925a3b844Bc454e4438f44e"
	exclusiveOwner := "@alice"
	exclusiveRemark := "VIP通道"

	if _, err := database.EnsureGroup(chatID, "专属地址公开查询测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558201, 558201, chatID, ownerID, "owner_exclusive", "设置专属地址 "+exclusiveAddress+" "+exclusiveOwner+" "+exclusiveRemark, baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558202, 558202, chatID, memberID, "member_exclusive", "专属地址", baseDate+1,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558203, 558203, chatID, memberID, "member_exclusive", "地址", baseDate+2,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558204, 558204, chatID, memberID, "member_exclusive", exclusiveRemark+"地址", baseDate+3,
	))

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.ExclusiveAddress != exclusiveAddress {
		t.Fatalf("专属地址未正确写入: want=%q got=%q", exclusiveAddress, group.ExclusiveAddress)
	}
	if group.ExclusiveAddressOwner != exclusiveOwner {
		t.Fatalf("专属地址所属人未正确写入: want=%q got=%q", exclusiveOwner, group.ExclusiveAddressOwner)
	}
	if group.ExclusiveAddressRemark != exclusiveRemark {
		t.Fatalf("专属地址备注未正确写入: want=%q got=%q", exclusiveRemark, group.ExclusiveAddressRemark)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 558201, "设置专属地址成功。") {
		t.Fatalf("设置专属地址成功回执缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558202, "本群专属地址共 1 个：") {
		t.Fatalf("普通成员发送专属地址未返回列表统计: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558202, "专属地址："+exclusiveAddress) {
		t.Fatalf("普通成员发送专属地址未返回具体地址: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558202, "网络：") {
		t.Fatalf("普通成员发送专属地址未返回网络信息: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 558202, "发送「备注信息+地址」查看详情") {
		t.Fatalf("专属地址列表不应包含旧引导文案: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558203, "下发地址未设置。") {
		t.Fatalf("普通成员发送地址应仅提示下发地址未设置: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558204, "下发地址：") || !fourLayerHasReply(snapshot, chatID, 558204, exclusiveAddress) {
		t.Fatalf("普通成员发送备注关键词地址未返回专属地址详情: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558204, "地址归属："+exclusiveOwner) {
		t.Fatalf("普通成员发送备注关键词地址未返回所属人: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558204, "备注信息："+exclusiveRemark) {
		t.Fatalf("普通成员发送备注关键词地址未返回备注信息: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558204, "验  证  码：") {
		t.Fatalf("普通成员发送备注关键词地址未返回验证码: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 558204, "点击上方地址，👆🏻即可复制地址") {
		t.Fatalf("普通成员发送备注关键词地址未返回复制提示: %+v", snapshot)
	}
}

func TestOwnerCanClearExclusiveAddressByRemarkCommand(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "owner-clear-exclusive-address-by-remark.db")
	app.address = services.NewAddressService(time.Second)

	chatID := int64(-100558201)
	ownerID := int64(558201)
	memberID := int64(558202)
	exclusiveAddress := "0x742d35Cc6634C0532925a3b844Bc454e4438f44e"
	exclusiveOwner := "@a8668123"
	exclusiveRemark := "ma"

	if _, err := database.EnsureGroup(chatID, "专属地址按备注清除测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558301, 558301, chatID, ownerID, "owner_exclusive", "设置专属地址 "+exclusiveAddress+" "+exclusiveOwner+" "+exclusiveRemark, baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558302, 558302, chatID, ownerID, "owner_exclusive", "清除ma地址", baseDate+1,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		558303, 558303, chatID, memberID, "member_exclusive", exclusiveRemark+"地址", baseDate+2,
	))

	rows, err := database.ListExclusiveAddresses(chatID)
	if err != nil {
		t.Fatalf("查询专属地址列表失败: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("按备注清除后专属地址列表应为空: got=%d", len(rows))
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 558302, "清除ma专属地址成功") {
		t.Fatalf("按备注清除成功回执缺失: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 558303, "专属地址：") {
		t.Fatalf("按备注清除后不应再命中该备注地址详情: %+v", snapshot)
	}
}
