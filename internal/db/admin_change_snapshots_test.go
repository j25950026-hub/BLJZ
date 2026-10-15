package db

import (
	"path/filepath"
	"testing"
)

func TestAdminChangeSnapshotsRoundTrip(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "admin-snapshots.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	snapshotID, err := database.SaveAdminChangeSnapshot(AdminChangeSnapshot{
		OwnerUsername: "preview",
		Action:        "admin_global_switch_apply",
		SummaryJSON:   `{"updatedCount":1,"failedCount":1}`,
		Status:        "partial",
		Items: []AdminChangeSnapshotItem{
			{ChatID: -1001, BeforeJSON: `{"enabled":true}`, AfterJSON: `{"enabled":false}`, Status: "updated"},
			{ChatID: -1002, BeforeJSON: `{"enabled":true}`, AfterJSON: `{"enabled":false}`, Status: "failed", FailureReason: "配置已变化"},
		},
	})
	if err != nil {
		t.Fatalf("保存管理员变更快照失败: %v", err)
	}
	if snapshotID == 0 {
		t.Fatalf("快照应返回稳定 ID")
	}

	items, err := database.ListAdminChangeSnapshots("preview", 10)
	if err != nil {
		t.Fatalf("查询管理员变更快照失败: %v", err)
	}
	if len(items) != 1 || items[0].ID != snapshotID || items[0].OwnerUsername != "preview" || items[0].Action != "admin_global_switch_apply" {
		t.Fatalf("快照主记录错误: %+v", items)
	}
	if len(items[0].Items) != 2 || items[0].Items[1].FailureReason != "配置已变化" {
		t.Fatalf("快照明细错误: %+v", items[0].Items)
	}
}

func TestAdminChangeSnapshotsRejectInvalidAction(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "admin-snapshots-invalid.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	if _, err := database.SaveAdminChangeSnapshot(AdminChangeSnapshot{OwnerUsername: "preview", Action: ""}); err == nil {
		t.Fatalf("空操作名应被拒绝")
	}
}
