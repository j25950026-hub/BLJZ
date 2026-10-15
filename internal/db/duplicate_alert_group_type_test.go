package db

import (
	"path/filepath"
	"testing"
)

func TestDuplicateCardAlertAcrossSameGroupType(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "duplicate-alert-group-type.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	const (
		groupType = "ICF"
		cardNo    = "4539578763621486"
	)
	chatA := int64(-100910001)
	chatB := int64(-100910002)
	chatC := int64(-100910003)

	for _, chatID := range []int64{chatA, chatB, chatC} {
		if _, err := database.EnsureGroup(chatID, "重复预警同类型测试群"); err != nil {
			t.Fatalf("初始化群配置失败: chat=%d err=%v", chatID, err)
		}
	}
	if err := database.UpdateGroupFields(chatA, map[string]any{
		"group_type":              groupType,
		"duplicate_alert_enabled": 1,
	}); err != nil {
		t.Fatalf("更新群A配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatB, map[string]any{
		"group_type":              groupType,
		"duplicate_alert_enabled": 1,
	}); err != nil {
		t.Fatalf("更新群B配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatC, map[string]any{
		"group_type":              groupType,
		"duplicate_alert_enabled": 0,
	}); err != nil {
		t.Fatalf("更新群C配置失败: %v", err)
	}

	buildInput := func(chatID int64, messageID int, amount float64) LedgerEntryInput {
		return LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        messageID,
			UserID:           9100001,
			Username:         "cp_duplicate",
			Nickname:         "cp_duplicate",
			EntryType:        "deposit",
			Amount:           amount,
			Currency:         "CNY",
			BaseAmount:       amount,
			FeeRate:          0,
			Rate:             1,
			FinalAmount:      amount,
			Note:             "同类型跨群卡号重复预警",
			CardNo:           cardNo,
			TrackMessageLink: "tg://msg/test",
		}
	}

	first, err := database.ApplyLedgerEntry(buildInput(chatA, 910101, 100))
	if err != nil {
		t.Fatalf("首笔记账失败: %v", err)
	}
	if first.RecentCardSeenCount != 1 {
		t.Fatalf("首笔卡号统计应为 1，实际=%d", first.RecentCardSeenCount)
	}

	second, err := database.ApplyLedgerEntry(buildInput(chatA, 910102, 200))
	if err != nil {
		t.Fatalf("第二笔记账失败: %v", err)
	}
	if second.RecentCardSeenCount != 2 {
		t.Fatalf("同群第二笔卡号统计应为 2，实际=%d", second.RecentCardSeenCount)
	}

	third, err := database.ApplyLedgerEntry(buildInput(chatB, 910201, 300))
	if err != nil {
		t.Fatalf("跨群第三笔记账失败: %v", err)
	}
	if third.RecentCardSeenCount != 3 {
		t.Fatalf("同类型跨群第三笔卡号统计应为 3，实际=%d", third.RecentCardSeenCount)
	}

	fourth, err := database.ApplyLedgerEntry(buildInput(chatC, 910301, 400))
	if err != nil {
		t.Fatalf("关闭开关群记账失败: %v", err)
	}
	if fourth.RecentCardSeenCount != 1 {
		t.Fatalf("关闭开关群应按本群统计，首次应为 1，实际=%d", fourth.RecentCardSeenCount)
	}
}

func TestTextDuplicateAccountAcrossSameGroupType(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "duplicate-alert-text-group-type.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	const (
		groupType = "ICF"
		accountNo = "110288285406"
	)
	chatA := int64(-100920001)
	chatB := int64(-100920002)
	chatC := int64(-100920003)

	for _, chatID := range []int64{chatA, chatB, chatC} {
		if _, err := database.EnsureGroup(chatID, "纯文本重复预警同类型测试群"); err != nil {
			t.Fatalf("初始化群配置失败: chat=%d err=%v", chatID, err)
		}
	}
	if err := database.UpdateGroupFields(chatA, map[string]any{
		"group_type":              groupType,
		"duplicate_alert_enabled": 1,
	}); err != nil {
		t.Fatalf("更新群A配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatB, map[string]any{
		"group_type":              groupType,
		"duplicate_alert_enabled": 1,
	}); err != nil {
		t.Fatalf("更新群B配置失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatC, map[string]any{
		"group_type":              groupType,
		"duplicate_alert_enabled": 0,
	}); err != nil {
		t.Fatalf("更新群C配置失败: %v", err)
	}

	first, firstScope, err := database.RecordTextDuplicateAccount(chatA, 920101, accountNo)
	if err != nil {
		t.Fatalf("首条纯文本记录失败: %v", err)
	}
	if first != 1 || firstScope != groupType {
		t.Fatalf("首条统计异常: count=%d scope=%q", first, firstScope)
	}

	second, secondScope, err := database.RecordTextDuplicateAccount(chatA, 920102, accountNo)
	if err != nil {
		t.Fatalf("第二条纯文本记录失败: %v", err)
	}
	if second != 2 || secondScope != groupType {
		t.Fatalf("第二条统计异常: count=%d scope=%q", second, secondScope)
	}

	third, thirdScope, err := database.RecordTextDuplicateAccount(chatB, 920201, accountNo)
	if err != nil {
		t.Fatalf("跨群第三条纯文本记录失败: %v", err)
	}
	if third != 3 || thirdScope != groupType {
		t.Fatalf("跨群第三条统计异常: count=%d scope=%q", third, thirdScope)
	}

	fourth, fourthScope, err := database.RecordTextDuplicateAccount(chatC, 920301, accountNo)
	if err != nil {
		t.Fatalf("关闭开关群记录失败: %v", err)
	}
	if fourth != 1 || fourthScope != "" {
		t.Fatalf("关闭开关群应按本群统计且无同类型范围: count=%d scope=%q", fourth, fourthScope)
	}
}

func TestClearDuplicateAlertHistory(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "duplicate-alert-clear-history.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-100920099)
	if _, err := database.EnsureGroup(chatID, "重复预警清理测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if _, _, err := database.RecordTextDuplicateAccount(chatID, 920901, "110288285406"); err != nil {
		t.Fatalf("写入纯文本重复记录失败: %v", err)
	}
	if _, _, err := database.RecordTextDuplicateAccount(chatID, 920902, "110288285406"); err != nil {
		t.Fatalf("写入纯文本重复记录失败: %v", err)
	}

	cleared, err := database.ClearDuplicateAlertHistory(chatID)
	if err != nil {
		t.Fatalf("清理重复预警记录失败: %v", err)
	}
	if cleared < 2 {
		t.Fatalf("清理数量异常: got=%d", cleared)
	}

	count, _, err := database.RecordTextDuplicateAccount(chatID, 920903, "110288285406")
	if err != nil {
		t.Fatalf("清理后写入重复记录失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("清理后计数应重置为 1，实际=%d", count)
	}
}
