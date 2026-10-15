package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/internal/db"
)

func TestRunAdminCorrectionDryRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "admin-correction-dry-run.db")
	database, err := db.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	defer database.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"-db", dbPath, "-scope", "all"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("dry-run 执行失败: code=%d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"scope": "all"`) {
		t.Fatalf("输出应包含 scope 信息: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"dry_run": true`) {
		t.Fatalf("输出应标记为 dry-run: %s", stdout.String())
	}
}

func TestRunAdminCorrectionInvalidScope(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "admin-correction-invalid-scope.db")
	database, err := db.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	defer database.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"-db", dbPath, "-scope", "unknown"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("非法 scope 应返回失败: code=%d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unsupported correction scope") {
		t.Fatalf("错误信息应包含 scope 校验提示: %s", stderr.String())
	}
}
