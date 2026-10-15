package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlPoint441WALArchiving(t *testing.T) {
	t.Run("控制点 4.4.1 功能验证", func(t *testing.T) {
		baseDir := t.TempDir()
		sourceDir := filepath.Join(baseDir, "pg_wal_ready")
		archiveDir := filepath.Join(baseDir, "wal_archive")
		if err := os.MkdirAll(sourceDir, 0o755); err != nil {
			t.Fatalf("初始化 WAL 源目录失败: %v", err)
		}

		walSegment := "0000000100000000000000A1"
		historyFile := "00000002.history"
		ignoredFile := "README.txt"
		if err := os.WriteFile(filepath.Join(sourceDir, walSegment), []byte("wal-segment-a1"), 0o644); err != nil {
			t.Fatalf("写入 WAL 分段样本失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sourceDir, historyFile), []byte("timeline-history"), 0o644); err != nil {
			t.Fatalf("写入 timeline history 样本失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sourceDir, ignoredFile), []byte("skip"), 0o644); err != nil {
			t.Fatalf("写入非 WAL 样本失败: %v", err)
		}

		archivedSegments := make([]string, 0, 2)
		archiver, err := NewWALArchiver(WALArchiverOptions{
			SourceDir:    sourceDir,
			ArchiveDir:   archiveDir,
			ScanInterval: time.Second,
			OnArchived: func(result WALArchiveResult) {
				archivedSegments = append(archivedSegments, result.Segment)
			},
		})
		if err != nil {
			t.Fatalf("初始化 WAL 归档器失败: %v", err)
		}

		archivedCount, err := archiver.ScanOnce()
		if err != nil {
			t.Fatalf("首次扫描归档失败: %v", err)
		}
		if archivedCount != 2 {
			t.Fatalf("首次扫描归档数量错误: want=%d got=%d", 2, archivedCount)
		}
		if len(archivedSegments) != 2 {
			t.Fatalf("首次扫描回调数量错误: want=%d got=%d", 2, len(archivedSegments))
		}

		if got := mustReadFile(t, filepath.Join(archiveDir, walSegment)); got != "wal-segment-a1" {
			t.Fatalf("WAL 分段归档内容错误: %q", got)
		}
		if got := mustReadFile(t, filepath.Join(archiveDir, historyFile)); got != "timeline-history" {
			t.Fatalf("history 文件归档内容错误: %q", got)
		}
		if _, statErr := os.Stat(filepath.Join(archiveDir, ignoredFile)); !os.IsNotExist(statErr) {
			t.Fatalf("非 WAL 文件不应被归档: err=%v", statErr)
		}

		archivedAgain, err := archiver.ScanOnce()
		if err != nil {
			t.Fatalf("二次扫描不应报错: %v", err)
		}
		if archivedAgain != 0 {
			t.Fatalf("二次扫描应命中幂等: want=%d got=%d", 0, archivedAgain)
		}

		archiveSettings, err := BuildPostgresArchiveSettings(archiveDir)
		if err != nil {
			t.Fatalf("生成 PostgreSQL WAL 归档配置失败: %v", err)
		}
		if !strings.Contains(archiveSettings, "archive_mode = on") {
			t.Fatalf("WAL 归档配置缺少 archive_mode: %s", archiveSettings)
		}
		if !strings.Contains(archiveSettings, "archive_command = 'test ! -f") {
			t.Fatalf("WAL 归档配置缺少 archive_command: %s", archiveSettings)
		}

		targetTime := time.Date(2026, 2, 19, 11, 30, 15, 0, time.FixedZone("CST", 8*3600))
		pitrSettings, err := BuildPITRRecoverySettings(PITRRecoveryOptions{
			ArchiveDir: archiveDir,
			TargetTime: targetTime,
		})
		if err != nil {
			t.Fatalf("生成 PITR 恢复配置失败: %v", err)
		}
		if !strings.Contains(pitrSettings, "restore_command = 'cp") {
			t.Fatalf("PITR 配置缺少 restore_command: %s", pitrSettings)
		}
		if !strings.Contains(pitrSettings, "recovery_target_time = '2026-02-19 11:30:15.000000+08:00'") {
			t.Fatalf("PITR 配置恢复时间错误: %s", pitrSettings)
		}
		if !strings.Contains(pitrSettings, "recovery_target_action = 'promote'") {
			t.Fatalf("PITR 配置缺少恢复动作: %s", pitrSettings)
		}
	})
}

func TestWALArchiverArchiveSizeMismatch(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "src")
	archiveDir := filepath.Join(t.TempDir(), "archive")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("创建源目录失败: %v", err)
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("创建归档目录失败: %v", err)
	}

	segment := "0000000100000000000000B2"
	if err := os.WriteFile(filepath.Join(sourceDir, segment), []byte("source-size"), 0o644); err != nil {
		t.Fatalf("写入源分段失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, segment), []byte("bad"), 0o644); err != nil {
		t.Fatalf("写入归档分段失败: %v", err)
	}

	archiver, err := NewWALArchiver(WALArchiverOptions{
		SourceDir:  sourceDir,
		ArchiveDir: archiveDir,
	})
	if err != nil {
		t.Fatalf("初始化归档器失败: %v", err)
	}

	if _, err := archiver.ArchiveSegment(segment); err == nil {
		t.Fatalf("归档大小不一致应返回错误")
	}
}

func TestIsWALArchivableFile(t *testing.T) {
	cases := map[string]bool{
		"0000000100000000000000A1":                 true,
		"00000002.history":                         true,
		"0000000100000000000000A1.00000028.backup": true,
		"0000000100000000000000a1":                 false,
		"archive_status":                           false,
		"0000000100000000000000A1.tmp":             false,
	}
	for input, want := range cases {
		if got := IsWALArchivableFile(input); got != want {
			t.Fatalf("WAL 文件过滤错误: input=%s want=%v got=%v", input, want, got)
		}
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件失败: path=%s err=%v", path, err)
	}
	return string(data)
}
