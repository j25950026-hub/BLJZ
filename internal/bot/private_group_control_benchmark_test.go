package bot

import (
	"sync/atomic"
	"testing"
	"time"
)

const privateGroupControlBenchmarkGroupCount = 116

func BenchmarkPrivateGroupControlEligibleGroupsCold(b *testing.B) {
	app, database, _ := newFourLayerTestApp(b, "private-group-control-bench-cold.db")
	ownerID := int64(889001)
	groupIDs := setupPrivateGroupControlGroups(b, database, ownerID, privateGroupControlChatIDs(-100889001, privateGroupControlBenchmarkGroupCount))
	baseMemberFn := privateGroupControlBotMemberFn(app, nil)
	var memberChecks int64
	app.isGroupMemberFn = func(chatID, userID int64) bool {
		atomic.AddInt64(&memberChecks, 1)
		return baseMemberFn(chatID, userID)
	}
	fixedNow := time.Now()
	app.now = func() time.Time { return fixedNow }

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.clearPrivateGroupControlCaches(ownerID)
		rows, err := app.listPrivateGroupControlEligibleGroups(ownerID)
		if err != nil {
			b.Fatalf("读取可管理群失败: %v", err)
		}
		if len(rows) != len(groupIDs) {
			b.Fatalf("可管理群数量异常: got=%d want=%d", len(rows), len(groupIDs))
		}
	}
	b.StopTimer()
	if b.N > 0 {
		b.ReportMetric(float64(memberChecks)/float64(b.N), "member_checks/op")
	}
	b.ReportMetric(float64(len(groupIDs)), "groups")
}

func BenchmarkPrivateGroupControlEligibleGroupsHot(b *testing.B) {
	app, database, _ := newFourLayerTestApp(b, "private-group-control-bench-hot.db")
	ownerID := int64(889002)
	groupIDs := setupPrivateGroupControlGroups(b, database, ownerID, privateGroupControlChatIDs(-100889002, privateGroupControlBenchmarkGroupCount))
	baseMemberFn := privateGroupControlBotMemberFn(app, nil)
	var memberChecks int64
	app.isGroupMemberFn = func(chatID, userID int64) bool {
		atomic.AddInt64(&memberChecks, 1)
		return baseMemberFn(chatID, userID)
	}
	fixedNow := time.Now()
	app.now = func() time.Time { return fixedNow }
	if _, err := app.listPrivateGroupControlEligibleGroups(ownerID); err != nil {
		b.Fatalf("预热可管理群缓存失败: %v", err)
	}
	atomic.StoreInt64(&memberChecks, 0)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := app.listPrivateGroupControlEligibleGroups(ownerID)
		if err != nil {
			b.Fatalf("读取可管理群失败: %v", err)
		}
		if len(rows) != len(groupIDs) {
			b.Fatalf("可管理群数量异常: got=%d want=%d", len(rows), len(groupIDs))
		}
	}
	b.StopTimer()
	if b.N > 0 {
		b.ReportMetric(float64(memberChecks)/float64(b.N), "member_checks/op")
	}
	b.ReportMetric(float64(len(groupIDs)), "groups")
}

func BenchmarkPrivateGroupControlConfirmValidation(b *testing.B) {
	app, database, _ := newFourLayerTestApp(b, "private-group-control-bench-confirm.db")
	ownerID := int64(889003)
	groupIDs := setupPrivateGroupControlGroups(b, database, ownerID, privateGroupControlChatIDs(-100889003, privateGroupControlBenchmarkGroupCount))
	baseMemberFn := privateGroupControlBotMemberFn(app, nil)
	var memberChecks int64
	app.isGroupMemberFn = func(chatID, userID int64) bool {
		atomic.AddInt64(&memberChecks, 1)
		return baseMemberFn(chatID, userID)
	}
	fixedNow := time.Now()
	app.now = func() time.Time { return fixedNow }

	rows, err := app.ListPrivilegedGroups(ownerID)
	if err != nil {
		b.Fatalf("预热权限群列表失败: %v", err)
	}
	session := privateGroupControlSession{
		SessionID:       "bench",
		Version:         1,
		UserID:          ownerID,
		PrivateChatID:   ownerID,
		Action:          privateGroupControlActionType,
		Stage:           privateGroupControlStageAwaitingConfirm,
		Groups:          rows,
		SelectedChatIDs: map[int64]bool{},
		UpdatedAt:       fixedNow,
	}
	for i := 0; i < 2 && i < len(session.Groups); i++ {
		session.SelectedChatIDs[session.Groups[i].ChatID] = true
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		valid, invalid, err := app.validatePrivateGroupControlTargets(session)
		if err != nil {
			b.Fatalf("验证确认目标失败: %v", err)
		}
		if len(valid) != 2 {
			b.Fatalf("有效目标数量异常: got=%d want=2", len(valid))
		}
		if len(invalid) != 0 {
			b.Fatalf("确认验证不应产生失效目标: got=%d", len(invalid))
		}
	}
	b.StopTimer()
	if b.N > 0 {
		b.ReportMetric(float64(memberChecks)/float64(b.N), "member_checks/op")
	}
	b.ReportMetric(float64(len(groupIDs)), "groups")
	b.ReportMetric(2, "selected")
}
