package db

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateGroupFieldsRejectsInjectedColumnAndBindsInjectedValue(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "sql-injection-group.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100991
	if _, err := database.EnsureGroup(chatID, "原始群"); err != nil {
		t.Fatalf("创建测试群失败: %v", err)
	}
	value := "普通文本', owner_user_id = 999999 --"
	if err := database.UpdateGroupFields(chatID, map[string]any{"title": value}); err != nil {
		t.Fatalf("合法列更新不应失败: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取更新后的群配置失败: %v", err)
	}
	if group.Title != value || group.OwnerUserID != 0 {
		t.Fatalf("注入值必须作为普通参数保存: group=%+v", group)
	}

	maliciousColumn := "title = '被注入', owner_user_id = 999999 --"
	err = database.UpdateGroupFields(chatID, map[string]any{maliciousColumn: "ignored"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
		t.Fatalf("注入列名必须被拒绝: err=%v", err)
	}
	group, err = database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("拒绝注入列后读取群配置失败: %v", err)
	}
	if group.Title != value || group.OwnerUserID != 0 {
		t.Fatalf("拒绝注入列时不应产生部分更新: group=%+v", group)
	}
}

func TestUpdateScheduleRejectsInjectedColumnAndBindsInjectedValue(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "sql-injection-schedule.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const chatID int64 = -100992
	if _, err := database.EnsureGroup(chatID, "定时群"); err != nil {
		t.Fatalf("创建测试群失败: %v", err)
	}
	scheduleID, err := database.AddSchedule(chatID, time.Now().Add(time.Hour).Format(time.RFC3339), "原始内容", chatID, 1, "", "", 0)
	if err != nil {
		t.Fatalf("创建定时任务失败: %v", err)
	}
	value := "通知内容', status = 'sent' --"
	if err := database.UpdateSchedule(scheduleID, map[string]any{"content": value}); err != nil {
		t.Fatalf("合法列更新不应失败: %v", err)
	}
	schedule, err := database.GetSchedule(scheduleID)
	if err != nil || schedule == nil {
		t.Fatalf("读取更新后的定时任务失败: schedule=%+v err=%v", schedule, err)
	}
	if schedule.Content != value || schedule.Status != "pending" {
		t.Fatalf("注入值必须作为普通参数保存: schedule=%+v", schedule)
	}

	maliciousColumn := "content = '被注入', status = 'sent' --"
	err = database.UpdateSchedule(scheduleID, map[string]any{maliciousColumn: "ignored"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
		t.Fatalf("注入列名必须被拒绝: err=%v", err)
	}
	schedule, err = database.GetSchedule(scheduleID)
	if err != nil || schedule == nil {
		t.Fatalf("拒绝注入列后读取定时任务失败: schedule=%+v err=%v", schedule, err)
	}
	if schedule.Content != value || schedule.Status != "pending" {
		t.Fatalf("拒绝注入列时不应产生部分更新: schedule=%+v", schedule)
	}
}

func TestMigrateChatScopedTableRejectsInjectedTableName(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "sql-injection-migration.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	tx, err := database.raw.Begin()
	if err != nil {
		t.Fatalf("开启迁移测试事务失败: %v", err)
	}
	defer tx.Rollback()

	maliciousTable := `group_config"; DROP TABLE operators; --`
	_, err = migrateChatScopedTableTx(tx, maliciousTable, -100993, -100994)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
		t.Fatalf("注入表名必须在构造 SQL 前被拒绝: err=%v", err)
	}

	var tableCount int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = 'operators'`).Scan(&tableCount); err != nil {
		t.Fatalf("检查 operators 表失败: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("拒绝注入表名后不应删除 operators 表: count=%d", tableCount)
	}
}
