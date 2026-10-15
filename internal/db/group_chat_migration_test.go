package db

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrateGroupChatDataMovesAllChatScopedData(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "group-chat-migration.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	const (
		oldChatID     = int64(-5266232385)
		newChatID     = int64(-1003758576179)
		otherChatID   = int64(-1005123456789)
		ownerUserID   = int64(8114441184)
		oldOperatorID = int64(8114441199)
		newOperatorID = int64(8114441200)
	)

	for _, chatID := range []int64{oldChatID, newChatID, otherChatID} {
		if _, err := database.EnsureGroup(chatID, "迁移测试群"); err != nil {
			t.Fatalf("初始化群配置失败(chat=%d): %v", chatID, err)
		}
	}
	if err := database.UpdateGroupFields(oldChatID, map[string]any{
		"title":                   "旧群标题",
		"owner_user_id":           ownerUserID,
		"group_type":              "CS",
		"duplicate_alert_enabled": 1,
		"group_tags":              "CS,ICF",
		"guarantee_rule":          "恭喜发财担保",
	}); err != nil {
		t.Fatalf("更新旧群配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(newChatID, map[string]any{
		"title":      "新群默认标题",
		"group_type": "NEW",
	}); err != nil {
		t.Fatalf("更新新群配置失败: %v", err)
	}

	if err := database.AddOperator(oldChatID, oldOperatorID, "old_operator", false); err != nil {
		t.Fatalf("写入旧群操作人失败: %v", err)
	}
	if err := database.AddOperator(newChatID, newOperatorID, "new_operator", false); err != nil {
		t.Fatalf("写入新群操作人失败: %v", err)
	}
	if err := database.SetCustomContent(oldChatID, "担保", "恭喜发财担保模板", ownerUserID); err != nil {
		t.Fatalf("写入自定义内容失败: %v", err)
	}
	if err := database.SetAutoReply(oldChatID, "你好", "欢迎咨询"); err != nil {
		t.Fatalf("写入自动回复失败: %v", err)
	}
	if err := database.SetIdentity(oldChatID, "操作人", oldOperatorID, "@old_operator"); err != nil {
		t.Fatalf("写入身份失败: %v", err)
	}
	if err := database.AddEvaluation(oldChatID, ownerUserID, "nova_pay668", 5, "服务稳定", "group", 0); err != nil {
		t.Fatalf("写入评价失败: %v", err)
	}
	if err := database.StartTimer(oldChatID, ownerUserID, time.Now().UTC().Add(-2*time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatalf("写入计时器失败: %v", err)
	}
	sendAt := time.Now().UTC().Add(10 * time.Minute).Format("2006-01-02 15:04:05")
	if _, err := database.AddSchedule(oldChatID, sendAt, "旧群定时群发", oldChatID, 9527, "操作人", "CS", ownerUserID); err != nil {
		t.Fatalf("写入旧群定时群发失败: %v", err)
	}
	if _, err := database.AddSchedule(otherChatID, sendAt, "其他群定时群发", oldChatID, 9528, "操作人", "CS", ownerUserID); err != nil {
		t.Fatalf("写入其他群定时群发失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(oldChatID, ownerUserID, "nova_pay668", "Nova PAY"); err != nil {
		t.Fatalf("写入群成员缓存失败: %v", err)
	}
	if _, err := database.RecordCardSeen(oldChatID, "622233445566778899", 9527); err != nil {
		t.Fatalf("写入卡号缓存失败: %v", err)
	}
	if err := database.SetForwardRule(oldChatID, "账单", fmt.Sprintf("%d,%d", oldChatID, otherChatID)); err != nil {
		t.Fatalf("写入旧群转发规则失败: %v", err)
	}
	if err := database.SetForwardRule(otherChatID, "旧群联动", fmt.Sprintf("%d", oldChatID)); err != nil {
		t.Fatalf("写入其他群转发规则失败: %v", err)
	}
	amount := 188.8
	if _, err := database.AddDelayedQuery(oldChatID, ownerUserID, "关键字", &amount, "延时查单备注"); err != nil {
		t.Fatalf("写入延时查单失败: %v", err)
	}
	if err := database.SetNextPayoutRate(oldChatID, ownerUserID, 7.31); err != nil {
		t.Fatalf("写入用户运行态失败: %v", err)
	}
	if err := database.StageTempData(oldChatID, ownerUserID, "fsm:test", map[string]string{"step": "confirm"}, time.Minute); err != nil {
		t.Fatalf("写入临时暂存失败: %v", err)
	}
	if err := database.SetBranchCodeOverride(oldChatID, "622233445566778899", "123456789012", "Test Branch", "override", ownerUserID); err != nil {
		t.Fatalf("写入联行号覆盖失败: %v", err)
	}
	if _, err := database.TrackAddressVerification(oldChatID, "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B", ownerUserID, "nova_pay668", time.Now().UTC()); err != nil {
		t.Fatalf("写入地址核验统计失败: %v", err)
	}

	ledgerInput := LedgerEntryInput{
		ChatID:           oldChatID,
		MessageID:        7788,
		UserID:           ownerUserID,
		Username:         "nova_pay668",
		Nickname:         "Nova PAY",
		EntryType:        "deposit",
		Amount:           5000,
		Currency:         "USDT",
		BaseAmount:       5000,
		FeeRate:          0,
		Rate:             7,
		FinalAmount:      5000,
		Note:             "迁移测试",
		CardNo:           "622233445566778899",
		TrackMessageLink: "https://t.me/c/123/7788",
	}
	if _, err := database.ApplyLedgerEntry(ledgerInput); err != nil {
		t.Fatalf("写入账本分录失败: %v", err)
	}
	if err := database.AddRiskEvent(oldChatID, 7788, ownerUserID, "duplicate_card", "warn", "migrate_test"); err != nil {
		t.Fatalf("写入风控事件失败: %v", err)
	}
	if _, err := database.AddOutboundDeadLetter(OutboundDeadLetterInput{
		ChatID:       oldChatID,
		Priority:     2,
		Operation:    "send",
		MessageType:  "tgbotapi.MessageConfig",
		Payload:      "{}",
		ErrorMessage: "test",
	}); err != nil {
		t.Fatalf("写入死信队列失败: %v", err)
	}
	if _, err := database.raw.Exec(`
INSERT INTO daily_settlement_reports (
	chat_id, report_date, period_start, period_end, total_entries, deposit_count, payout_count, deposit_total, payout_total, net_profit, currency_breakdown
) VALUES (?, '2026-02-26', '2026-02-25T16:00:00', '2026-02-26T16:00:00', 1, 1, 0, 5000, 0, 5000, '[]')`,
		oldChatID,
	); err != nil {
		t.Fatalf("写入日结报表失败: %v", err)
	}
	if _, err := database.raw.Exec(`
INSERT INTO day_cut_settlement_reports (
	chat_id, report_date, day_cut_time, period_start, period_end, total_entries, deposit_count, payout_count, deposit_fee_total, payout_fee_total, net_fee_total, currency_breakdown
) VALUES (?, '2026-02-26', '04:00:00', '2026-02-25T20:00:00', '2026-02-26T20:00:00', 1, 1, 0, 0, 0, 0, '[]')`,
		oldChatID,
	); err != nil {
		t.Fatalf("写入日切报表失败: %v", err)
	}

	migrated, err := database.MigrateGroupChatData(oldChatID, newChatID, "The Indian community in Cambodia")
	if err != nil {
		t.Fatalf("执行群升级迁移失败: %v", err)
	}
	if !migrated {
		t.Fatalf("应检测到可迁移数据，但返回 migrated=false")
	}
	secondMigrated, err := database.MigrateGroupChatData(oldChatID, newChatID, "The Indian community in Cambodia")
	if err != nil {
		t.Fatalf("重复执行迁移不应报错: %v", err)
	}
	if secondMigrated {
		t.Fatalf("重复执行迁移应为幂等，预期 migrated=false")
	}

	for _, tableName := range groupChatMigrationTables {
		count := mustCountRows(t, database, fmt.Sprintf("SELECT COUNT(1) FROM %s WHERE chat_id = ?", tableName), oldChatID)
		if count != 0 {
			t.Fatalf("迁移后旧群仍有残留数据: table=%s count=%d", tableName, count)
		}
	}

	newGroup, err := database.GetGroup(newChatID)
	if err != nil {
		t.Fatalf("读取新群配置失败: %v", err)
	}
	if newGroup.OwnerUserID != ownerUserID {
		t.Fatalf("新群权限人迁移失败: got=%d want=%d", newGroup.OwnerUserID, ownerUserID)
	}
	if strings.TrimSpace(newGroup.GroupType) != "CS" {
		t.Fatalf("新群群类型迁移失败: got=%q", newGroup.GroupType)
	}
	if newGroup.DuplicateAlertEnabled != 1 {
		t.Fatalf("新群重复预警开关迁移失败: got=%d", newGroup.DuplicateAlertEnabled)
	}
	if strings.TrimSpace(newGroup.Title) != "The Indian community in Cambodia" {
		t.Fatalf("新群标题未按迁移事件更新: got=%q", newGroup.Title)
	}

	operators, err := database.ListOperators(newChatID, false)
	if err != nil {
		t.Fatalf("查询新群操作人失败: %v", err)
	}
	operatorSet := make(map[int64]struct{}, len(operators))
	for _, item := range operators {
		operatorSet[item.UserID] = struct{}{}
	}
	if _, ok := operatorSet[oldOperatorID]; !ok {
		t.Fatalf("旧群操作人未迁移到新群: user=%d", oldOperatorID)
	}
	if _, ok := operatorSet[newOperatorID]; !ok {
		t.Fatalf("新群既有操作人被覆盖丢失: user=%d", newOperatorID)
	}

	customContent, ok, err := database.GetCustomContent(newChatID, "担保")
	if err != nil || !ok {
		t.Fatalf("新群自定义内容查询失败: ok=%v err=%v", ok, err)
	}
	if customContent != "恭喜发财担保模板" {
		t.Fatalf("自定义内容迁移值异常: got=%q", customContent)
	}

	if count := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, newChatID); count == 0 {
		t.Fatalf("账本数据未迁移到新群")
	}
	if count := mustCountRows(t, database, `SELECT COUNT(1) FROM message_idempotency WHERE chat_id = ?`, newChatID); count == 0 {
		t.Fatalf("幂等数据未迁移到新群")
	}
	if count := mustCountRows(t, database, `SELECT COUNT(1) FROM audit_logs WHERE chat_id = ?`, newChatID); count == 0 {
		t.Fatalf("审计日志未迁移到新群")
	}
	if count := mustCountRows(t, database, `SELECT COUNT(1) FROM risk_events WHERE chat_id = ?`, newChatID); count == 0 {
		t.Fatalf("风控事件未迁移到新群")
	}
	if count := mustCountRows(t, database, `SELECT COUNT(1) FROM outbound_dead_letters WHERE chat_id = ?`, newChatID); count == 0 {
		t.Fatalf("死信队列未迁移到新群")
	}
	if count := mustCountRows(t, database, `SELECT COUNT(1) FROM daily_settlement_reports WHERE chat_id = ?`, newChatID); count == 0 {
		t.Fatalf("日结报表未迁移到新群")
	}
	if count := mustCountRows(t, database, `SELECT COUNT(1) FROM day_cut_settlement_reports WHERE chat_id = ?`, newChatID); count == 0 {
		t.Fatalf("日切报表未迁移到新群")
	}

	rate, err := database.PopNextPayoutRate(newChatID, ownerUserID)
	if err != nil {
		t.Fatalf("读取迁移后单笔下发汇率失败: %v", err)
	}
	if rate == nil || *rate != 7.31 {
		t.Fatalf("单笔下发汇率迁移异常: rate=%v", rate)
	}
	tempPayload, exists, err := database.GetTempData(newChatID, ownerUserID, "fsm:test")
	if err != nil {
		t.Fatalf("读取迁移后临时数据失败: %v", err)
	}
	if !exists || tempPayload["step"] != "confirm" {
		t.Fatalf("临时数据迁移异常: exists=%v payload=%v", exists, tempPayload)
	}
	override, err := database.GetBranchCodeOverride(newChatID, "622233445566778899")
	if err != nil {
		t.Fatalf("读取迁移后联行号覆盖失败: %v", err)
	}
	if override == nil || strings.TrimSpace(override.CNAPSCode) != "123456789012" {
		t.Fatalf("联行号覆盖迁移异常: override=%+v", override)
	}

	forwardRules, err := database.ListForwardRules(otherChatID)
	if err != nil {
		t.Fatalf("查询迁移后转发规则失败: %v", err)
	}
	otherRuleTarget := ""
	for _, rule := range forwardRules {
		if rule.Keyword == "旧群联动" {
			otherRuleTarget = rule.TargetChatIDs
			break
		}
	}
	if otherRuleTarget == "" {
		t.Fatalf("其他群转发规则丢失")
	}
	if strings.Contains(otherRuleTarget, fmt.Sprintf("%d", oldChatID)) {
		t.Fatalf("转发目标仍包含旧群ID: %s", otherRuleTarget)
	}
	if !strings.Contains(otherRuleTarget, fmt.Sprintf("%d", newChatID)) {
		t.Fatalf("转发目标未替换为新群ID: %s", otherRuleTarget)
	}

	if count := mustCountRows(t, database, `SELECT COUNT(1) FROM scheduled_broadcasts WHERE source_chat_id = ?`, oldChatID); count != 0 {
		t.Fatalf("定时群发 source_chat_id 仍存在旧群ID: count=%d", count)
	}
	if count := mustCountRows(t, database, `SELECT COUNT(1) FROM scheduled_broadcasts WHERE source_chat_id = ?`, newChatID); count == 0 {
		t.Fatalf("定时群发 source_chat_id 未替换为新群ID")
	}
}
