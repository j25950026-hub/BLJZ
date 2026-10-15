package db

import (
	"path/filepath"
	"testing"
)

func TestAdminTemplateLibraryVersionsAreImmutable(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "admin-template-library.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	first, err := database.SaveAdminTemplateLibrary(AdminTemplateLibrary{
		Kind:          "welcome",
		Name:          "工作日欢迎",
		Description:   "工作日版本",
		ContentJSON:   `{"template":"欢迎加入"}`,
		OwnerUsername: "root",
	})
	if err != nil {
		t.Fatalf("保存模板版本 1 失败: %v", err)
	}
	if first.Version != 1 || first.ID == 0 {
		t.Fatalf("首个模板版本错误: %+v", first)
	}
	second, err := database.SaveAdminTemplateLibrary(AdminTemplateLibrary{
		Kind:          "welcome",
		Name:          "工作日欢迎",
		Description:   "更新版本",
		ContentJSON:   `{"template":"欢迎加入本群"}`,
		OwnerUsername: "root",
	})
	if err != nil {
		t.Fatalf("保存模板版本 2 失败: %v", err)
	}
	if second.Version != 2 || second.ID == first.ID {
		t.Fatalf("模板版本应递增且使用新 ID: first=%+v second=%+v", first, second)
	}

	entries, err := database.ListAdminTemplateLibrary("welcome")
	if err != nil {
		t.Fatalf("读取模板库失败: %v", err)
	}
	if len(entries) != 2 || entries[0].Version != 2 || entries[1].Version != 1 || entries[0].ContentJSON != `{"template":"欢迎加入本群"}` {
		t.Fatalf("模板库版本列表错误: %+v", entries)
	}
}

func TestAdminTemplateLibraryRejectsInvalidPayload(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "admin-template-library-invalid.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	for _, entry := range []AdminTemplateLibrary{
		{Name: "缺类型", ContentJSON: `{}`},
		{Kind: "unknown", Name: "未知类型", ContentJSON: `{}`},
		{Kind: "welcome", Name: "缺内容"},
		{Kind: "welcome", Name: "坏 JSON", ContentJSON: `{"`},
	} {
		if _, err := database.SaveAdminTemplateLibrary(entry); err == nil {
			t.Fatalf("非法模板应被拒绝: %+v", entry)
		}
	}
}
