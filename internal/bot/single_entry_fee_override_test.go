package bot

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestSingleEntryRateOverrideDoesNotAffectGroupRate(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "single-entry-rate-override.db")

	const (
		chatID int64 = -100671001
		userID int64 = 67100101
	)
	if _, err := database.EnsureGroup(chatID, "四层测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}
	baseDate := int(time.Now().Unix())
	updates := []struct {
		updateID  int
		messageID int
		text      string
	}{
		{updateID: 671001, messageID: 671001, text: "开始记账"},
		{updateID: 671002, messageID: 671002, text: "设置费率7"},
		{updateID: 6710021, messageID: 6710021, text: "设置汇率10"},
		{updateID: 671003, messageID: 671003, text: "+500/4#单笔汇率"},
		{updateID: 671004, messageID: 671004, text: "+500#默认费率"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_fee_override",
			item.text,
			baseDate+i,
		))
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if !almostZero(group.FeeRate - 7) {
		t.Fatalf("群默认入款费率不应被单笔覆盖修改: got=%v want=7", group.FeeRate)
	}
	if !almostZero(group.CustomRate - 10) {
		t.Fatalf("群默认入款汇率不应被单笔覆盖修改: got=%v want=10", group.CustomRate)
	}

	rows, err := database.ListLedgerSince(chatID, "")
	if err != nil {
		t.Fatalf("读取账务明细失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("账务明细数量错误: got=%d want=2", len(rows))
	}

	first := rows[0]
	second := rows[1]
	if !almostZero(first.FeeRate - 7) {
		t.Fatalf("首笔应保持群默认费率 7%%: got=%v", first.FeeRate)
	}
	if !almostZero(second.FeeRate - 7) {
		t.Fatalf("第二笔应回退到群默认费率 7%%: got=%v", second.FeeRate)
	}
	if !almostZero(first.Rate - 4) {
		t.Fatalf("首笔应使用单笔汇率 4: got=%v", first.Rate)
	}
	if !almostZero(second.Rate - 10) {
		t.Fatalf("第二笔应回退到群默认汇率 10: got=%v", second.Rate)
	}
	if !almostZero(first.FinalAmount - 465) {
		t.Fatalf("首笔最终金额错误: got=%v want=465", first.FinalAmount)
	}
	if !almostZero(second.FinalAmount - 465) {
		t.Fatalf("第二笔最终金额错误: got=%v want=465", second.FinalAmount)
	}
}

func TestFeeRateCommandsAcceptPercentSuffix(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "fee-rate-percent-suffix.db")

	const (
		chatID int64 = -100671005
		userID int64 = 67100501
	)
	if _, err := database.EnsureGroup(chatID, "百分号费率测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}
	baseDate := int(time.Now().Unix())
	updates := []struct {
		updateID  int
		messageID int
		text      string
	}{
		{updateID: 671005, messageID: 671005, text: "设置费率2%"},
		{updateID: 671006, messageID: 671006, text: "设置下发费率3%"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_fee_percent",
			item.text,
			baseDate+i,
		))
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if !almostZero(group.FeeRate - 2) {
		t.Fatalf("设置费率2%% 应写入 2，got=%v", group.FeeRate)
	}
	if !almostZero(group.PayoutFeeRate - 3) {
		t.Fatalf("设置下发费率3%% 应写入 3，got=%v", group.PayoutFeeRate)
	}
}

func TestFeeRateCommandsAcceptNegativePercentSuffix(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "fee-rate-negative-percent-suffix.db")

	const (
		chatID int64 = -100671007
		userID int64 = 67100701
	)
	if _, err := database.EnsureGroup(chatID, "负百分号费率测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}
	baseDate := int(time.Now().Unix())
	updates := []struct {
		updateID  int
		messageID int
		text      string
	}{
		{updateID: 671007, messageID: 671007, text: "设置费率-2%"},
		{updateID: 671008, messageID: 671008, text: "设置下发费率-3%"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_fee_negative_percent",
			item.text,
			baseDate+i,
		))
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if !almostZero(group.FeeRate + 2) {
		t.Fatalf("设置费率-2%% 应写入 -2，got=%v", group.FeeRate)
	}
	if !almostZero(group.PayoutFeeRate + 3) {
		t.Fatalf("设置下发费率-3%% 应写入 -3，got=%v", group.PayoutFeeRate)
	}
}

func TestCompoundDepositEntrySetsGroupFeeRateAndRate(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "compound-deposit-entry-fee-factor-rate.db")

	const (
		chatID int64 = -100671009
		userID int64 = 67100901
	)
	if _, err := database.EnsureGroup(chatID, "复合入款格式测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	updates := []struct {
		updateID  int
		messageID int
		text      string
	}{
		{updateID: 671009, messageID: 671009, text: "开始记账"},
		{updateID: 671010, messageID: 671010, text: "设置费率0"},
		{updateID: 671011, messageID: 671011, text: "设置汇率10"},
		{updateID: 671012, messageID: 671012, text: "+231480*0.93/124#复合入款"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_compound_deposit",
			item.text,
			baseDate+i,
		))
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if !almostZero(group.FeeRate - 7) {
		t.Fatalf("复合入款应设置群默认入款费率: got=%v want=7", group.FeeRate)
	}
	if !almostZero(group.CustomRate - 124) {
		t.Fatalf("复合入款应设置群默认入款汇率: got=%v want=124", group.CustomRate)
	}
	if group.UseRealtimeRate != 0 {
		t.Fatalf("复合入款设置固定汇率后应关闭实时汇率: got=%v want=0", group.UseRealtimeRate)
	}

	rows, err := database.ListLedgerSince(chatID, "")
	if err != nil {
		t.Fatalf("读取账务明细失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("账务明细数量错误: got=%d want=1", len(rows))
	}

	row := rows[0]
	if row.EntryType != "deposit" {
		t.Fatalf("应写入入款分录: got=%s", row.EntryType)
	}
	if !almostZero(row.Amount - 231480) {
		t.Fatalf("入款金额错误: got=%v want=231480", row.Amount)
	}
	if !almostZero(row.FeeRate - 7) {
		t.Fatalf("单笔费率应由 0.93 反推为 7%%: got=%v", row.FeeRate)
	}
	if !almostZero(row.Rate - 124) {
		t.Fatalf("单笔汇率错误: got=%v want=124", row.Rate)
	}
	if diff := math.Abs(row.FinalAmount - 215276.4); diff > 1e-6 {
		t.Fatalf("最终金额错误: got=%v want=215276.4", row.FinalAmount)
	}
}

func TestSingleEntryPayoutRateOverrideDoesNotAffectGroupPayoutRate(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "single-entry-payout-rate-override.db")

	const (
		chatID int64 = -100671002
		userID int64 = 67100201
	)
	if _, err := database.EnsureGroup(chatID, "四层测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}
	baseDate := int(time.Now().Unix())
	updates := []struct {
		updateID  int
		messageID int
		text      string
	}{
		{updateID: 671101, messageID: 671101, text: "开始记账"},
		{updateID: 671102, messageID: 671102, text: "设置下发费率3"},
		{updateID: 6711021, messageID: 6711021, text: "设置下发汇率20"},
		{updateID: 671103, messageID: 671103, text: "下发6000/6#单笔下发汇率"},
		{updateID: 671104, messageID: 671104, text: "下发6000#默认下发费率"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_payout_fee_override",
			item.text,
			baseDate+i,
		))
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if !almostZero(group.PayoutFeeRate - 3) {
		t.Fatalf("群默认下发费率不应被单笔覆盖修改: got=%v want=3", group.PayoutFeeRate)
	}
	if !almostZero(group.PayoutRate - 20) {
		t.Fatalf("群默认下发汇率不应被单笔覆盖修改: got=%v want=20", group.PayoutRate)
	}

	rows, err := database.ListLedgerSince(chatID, "")
	if err != nil {
		t.Fatalf("读取账务明细失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("账务明细数量错误: got=%d want=2", len(rows))
	}

	first := rows[0]
	second := rows[1]
	if first.EntryType != "payout" || second.EntryType != "payout" {
		t.Fatalf("应写入下发分录: first=%s second=%s", first.EntryType, second.EntryType)
	}
	if !almostZero(first.FeeRate - 3) {
		t.Fatalf("首笔应保持群默认下发费率 3%%: got=%v", first.FeeRate)
	}
	if !almostZero(second.FeeRate - 3) {
		t.Fatalf("第二笔应回退到群默认下发费率 3%%: got=%v", second.FeeRate)
	}
	if !almostZero(first.Rate - 6) {
		t.Fatalf("首笔应使用单笔下发汇率 6: got=%v", first.Rate)
	}
	if !almostZero(second.Rate - 20) {
		t.Fatalf("第二笔应回退到群默认下发汇率 20: got=%v", second.Rate)
	}
	if !almostZero(first.FinalAmount - 5820) {
		t.Fatalf("首笔最终金额错误: got=%v want=5820", first.FinalAmount)
	}
	if !almostZero(second.FinalAmount - 5820) {
		t.Fatalf("第二笔最终金额错误: got=%v want=5820", second.FinalAmount)
	}
}

func TestNegativePayoutFeeRateIncreasesFinalAmount(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "negative-payout-fee-rate.db")

	const (
		chatID int64 = -100671004
		userID int64 = 67100401
	)
	if _, err := database.EnsureGroup(chatID, "负下发费率测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	updates := []struct {
		updateID  int
		messageID int
		text      string
	}{
		{updateID: 671301, messageID: 671301, text: "开始记账"},
		{updateID: 671302, messageID: 671302, text: "设置下发费率-10"},
		{updateID: 671303, messageID: 671303, text: "下发100#负下发费率"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_negative_payout_fee",
			item.text,
			baseDate+i,
		))
	}

	rows, err := database.ListLedgerSince(chatID, "")
	if err != nil {
		t.Fatalf("读取账务明细失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("账务明细数量错误: got=%d want=1", len(rows))
	}

	row := rows[0]
	if row.EntryType != "payout" {
		t.Fatalf("应写入下发分录: got=%s", row.EntryType)
	}
	if !almostZero(row.FeeRate + 10) {
		t.Fatalf("下发费率异常: got=%v want=-10", row.FeeRate)
	}
	if !almostZero(row.FinalAmount - 110) {
		t.Fatalf("负下发费率最终金额错误: got=%v want=110", row.FinalAmount)
	}
}

func TestFixedDepositFeeRateKeepsStableAfterMonthlyVolumeThreshold(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "fixed-fee-rate-stable-after-threshold.db")

	const (
		chatID int64 = -100671003
		userID int64 = 67100301
	)
	if _, err := database.EnsureGroup(chatID, "固定费率阈值测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	updates := []struct {
		updateID  int
		messageID int
		text      string
	}{
		{updateID: 671201, messageID: 671201, text: "开始记账"},
		{updateID: 671202, messageID: 671202, text: "设置费率0"},
		{updateID: 671203, messageID: 671203, text: "入款50000#阈值前"},
		{updateID: 671204, messageID: 671204, text: "入款-50000#阈值后"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_fixed_fee",
			item.text,
			baseDate+i,
		))
	}

	rows, err := database.ListLedgerSince(chatID, "")
	if err != nil {
		t.Fatalf("读取账务明细失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("账务明细数量错误: got=%d want=2", len(rows))
	}

	first := rows[0]
	second := rows[1]
	if first.EntryType != "deposit" || second.EntryType != "deposit" {
		t.Fatalf("应写入两条入款分录: first=%s second=%s", first.EntryType, second.EntryType)
	}
	if !almostZero(first.FeeRate) {
		t.Fatalf("首笔费率应保持群固定费率 0%%: got=%v", first.FeeRate)
	}
	if !almostZero(second.FeeRate) {
		t.Fatalf("第二笔费率应保持群固定费率 0%%: got=%v", second.FeeRate)
	}
	if !almostZero(first.FinalAmount - 50000) {
		t.Fatalf("首笔最终金额错误: got=%v want=50000", first.FinalAmount)
	}
	if !almostZero(second.FinalAmount + 50000) {
		t.Fatalf("第二笔最终金额错误: got=%v want=-50000", second.FinalAmount)
	}
}
