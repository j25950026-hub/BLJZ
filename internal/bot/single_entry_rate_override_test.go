package bot

import (
	"context"
	"testing"
	"time"
)

func TestSingleEntryDepositRateOverrideUsesSlashValue(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "single-entry-deposit-rate-override.db")

	const (
		chatID int64 = -100671005
		userID int64 = 67100501
	)
	if _, err := database.EnsureGroup(chatID, "单笔入款汇率覆盖测试群"); err != nil {
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
		{updateID: 671401, messageID: 671401, text: "开始记账"},
		{updateID: 671402, messageID: 671402, text: "设置费率0"},
		{updateID: 671403, messageID: 671403, text: "设置汇率137"},
		{updateID: 671404, messageID: 671404, text: "+100/8.8#单笔汇率"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_rate_override",
			item.text,
			baseDate+i,
		))
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if !almostZero(group.CustomRate - 137) {
		t.Fatalf("群默认入款汇率不应被单笔覆盖修改: got=%v want=137", group.CustomRate)
	}

	rows, err := database.ListLedgerSince(chatID, "")
	if err != nil {
		t.Fatalf("读取账务明细失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("账务明细数量错误: got=%d want=1", len(rows))
	}

	row := rows[0]
	if !almostZero(row.Rate - 8.8) {
		t.Fatalf("应使用单笔汇率 8.8: got=%v", row.Rate)
	}
	if !almostZero(row.FeeRate) {
		t.Fatalf("费率应保持群默认 0%%: got=%v", row.FeeRate)
	}
	if !almostZero(row.FinalAmount - 100) {
		t.Fatalf("最终金额应保持原金额: got=%v want=100", row.FinalAmount)
	}
}

func TestSingleEntryPayoutRateOverrideUsesSlashValue(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "single-entry-payout-rate-override.db")

	const (
		chatID int64 = -100671006
		userID int64 = 67100601
	)
	if _, err := database.EnsureGroup(chatID, "单笔下发汇率覆盖测试群"); err != nil {
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
		{updateID: 671501, messageID: 671501, text: "开始记账"},
		{updateID: 671502, messageID: 671502, text: "设置下发费率0"},
		{updateID: 671503, messageID: 671503, text: "设置下发汇率7"},
		{updateID: 671504, messageID: 671504, text: "下发6000/6.2#单笔汇率"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_payout_rate_override",
			item.text,
			baseDate+i,
		))
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if !almostZero(group.PayoutRate - 7) {
		t.Fatalf("群默认下发汇率不应被单笔覆盖修改: got=%v want=7", group.PayoutRate)
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
	if !almostZero(row.Rate - 6.2) {
		t.Fatalf("应使用单笔下发汇率 6.2: got=%v", row.Rate)
	}
	if !almostZero(row.FeeRate) {
		t.Fatalf("费率应保持群默认 0%%: got=%v", row.FeeRate)
	}
	if !almostZero(row.FinalAmount - 6000) {
		t.Fatalf("最终金额应保持原金额: got=%v want=6000", row.FinalAmount)
	}
}

func TestDefaultPayoutRateOneTreatsPlainPayoutAsUSDT(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "default-payout-rate-one-usdt-equivalent.db")

	const (
		chatID int64 = -100671007
		userID int64 = 67100701
	)
	if _, err := database.EnsureGroup(chatID, "默认下发汇率一值测试群"); err != nil {
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
		{updateID: 671601, messageID: 671601, text: "开始记账"},
		{updateID: 671602, messageID: 671602, text: "设置汇率137"},
		{updateID: 671603, messageID: 671603, text: "下发5868#普通下发"},
		{updateID: 671604, messageID: 671604, text: "下发5868U#U下发"},
	}
	for i, item := range updates {
		app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
			item.updateID,
			item.messageID,
			chatID,
			userID,
			"owner_default_payout_rate_one",
			item.text,
			baseDate+i,
		))
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if !almostZero(group.PayoutRate - 1) {
		t.Fatalf("新群默认下发汇率应为 1: got=%v", group.PayoutRate)
	}
	if !almostZero(group.PayoutFeeRate) {
		t.Fatalf("新群默认下发费率应为 0: got=%v", group.PayoutFeeRate)
	}

	rows, err := database.ListLedgerSince(chatID, "")
	if err != nil {
		t.Fatalf("读取账务明细失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("账务明细数量错误: got=%d want=2", len(rows))
	}

	plain := rows[0]
	usdt := rows[1]
	if plain.EntryType != "payout" || usdt.EntryType != "payout" {
		t.Fatalf("两笔都应为下发分录: plain=%s usdt=%s", plain.EntryType, usdt.EntryType)
	}
	if plain.Currency != "" {
		t.Fatalf("普通下发不应带币种: got=%q", plain.Currency)
	}
	if usdt.Currency != "USDT" {
		t.Fatalf("U 下发应识别为 USDT: got=%q", usdt.Currency)
	}
	if !almostZero(plain.Rate - 1) {
		t.Fatalf("默认下发汇率为 1 时，普通下发应按 1:1 U 处理: got=%v", plain.Rate)
	}
	if !almostZero(usdt.Rate - 1) {
		t.Fatalf("U 下发在默认下发汇率为 1 时应保持 1:1 U: got=%v", usdt.Rate)
	}
	if !almostZero(plain.FinalAmount - 5868) || !almostZero(usdt.FinalAmount - 5868) {
		t.Fatalf("默认零费率下最终金额应保持原值: plain=%v usdt=%v", plain.FinalAmount, usdt.FinalAmount)
	}

	plainU := convertLedgerCardAmountToU(normalizeLedgerCardAmount(plain), plain.Currency, plain.Rate)
	usdtU := convertLedgerCardAmountToU(normalizeLedgerCardAmount(usdt), usdt.Currency, usdt.Rate)
	if !almostZero(plainU - 5868) || !almostZero(usdtU - 5868) {
		t.Fatalf("普通下发与 U 下发应折算为相同 U 值: plain=%v usdt=%v", plainU, usdtU)
	}
}
