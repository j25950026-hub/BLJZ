package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlPoint443CrossRegionDR(t *testing.T) {
	t.Run("控制点 4.4.3 功能验证", func(t *testing.T) {
		baseDir := t.TempDir()
		walArchiveDir := filepath.Join(baseDir, "wal_archive")
		fullBackupDir := filepath.Join(baseDir, "full_backups")
		remoteDir := filepath.Join(baseDir, "remote_region")

		if err := os.MkdirAll(walArchiveDir, 0o755); err != nil {
			t.Fatalf("创建 WAL 归档目录失败: %v", err)
		}
		backupDateDir := filepath.Join(fullBackupDir, "2026-02-19")
		if err := os.MkdirAll(backupDateDir, 0o755); err != nil {
			t.Fatalf("创建全量备份目录失败: %v", err)
		}

		walSegmentPath := filepath.Join(walArchiveDir, "0000000100000000000000A1")
		manifestPath := filepath.Join(backupDateDir, "manifest.json")
		snapshotPath := filepath.Join(backupDateDir, "01_ledger.db")
		if err := os.WriteFile(walSegmentPath, []byte("wal-segment-v1"), 0o640); err != nil {
			t.Fatalf("写入 WAL 样本失败: %v", err)
		}
		if err := os.WriteFile(manifestPath, []byte("{\"date\":\"2026-02-19\"}"), 0o640); err != nil {
			t.Fatalf("写入 manifest 样本失败: %v", err)
		}
		if err := os.WriteFile(snapshotPath, []byte("snapshot-v1"), 0o640); err != nil {
			t.Fatalf("写入快照样本失败: %v", err)
		}

		syncer, err := NewCrossRegionDRSyncer(CrossRegionDRSyncOptions{
			SourcePaths:  []string{walArchiveDir, fullBackupDir},
			TargetDir:    remoteDir,
			SyncInterval: time.Second,
		})
		if err != nil {
			t.Fatalf("初始化跨区域容灾同步器失败: %v", err)
		}

		firstResult, err := syncer.SyncOnce(time.Date(2026, 2, 19, 11, 30, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("首次异地同步失败: %v", err)
		}
		if firstResult.SourceCount != 2 {
			t.Fatalf("同步源数量错误: want=%d got=%d", 2, firstResult.SourceCount)
		}
		if firstResult.FileCount != 3 {
			t.Fatalf("首次扫描文件数量错误: want=%d got=%d", 3, firstResult.FileCount)
		}
		if firstResult.CopiedFiles != 3 {
			t.Fatalf("首次同步文件数量错误: want=%d got=%d", 3, firstResult.CopiedFiles)
		}

		mirrors := syncer.SourceMirrors()
		walMirrorDir := mirrors[filepath.Clean(walArchiveDir)]
		backupMirrorDir := mirrors[filepath.Clean(fullBackupDir)]
		if got := mustReadFile(t, filepath.Join(walMirrorDir, "0000000100000000000000A1")); got != "wal-segment-v1" {
			t.Fatalf("WAL 异地副本内容错误: %q", got)
		}
		if got := mustReadFile(t, filepath.Join(backupMirrorDir, "2026-02-19", "manifest.json")); got != "{\"date\":\"2026-02-19\"}" {
			t.Fatalf("manifest 异地副本内容错误: %q", got)
		}
		if got := mustReadFile(t, filepath.Join(backupMirrorDir, "2026-02-19", "01_ledger.db")); got != "snapshot-v1" {
			t.Fatalf("快照异地副本内容错误: %q", got)
		}

		secondResult, err := syncer.SyncOnce(time.Date(2026, 2, 19, 11, 35, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("二次同步不应报错: %v", err)
		}
		if secondResult.CopiedFiles != 0 {
			t.Fatalf("二次同步应命中幂等: copied=%d", secondResult.CopiedFiles)
		}

		if err := os.WriteFile(walSegmentPath, []byte("wal-segment-v2-updated"), 0o640); err != nil {
			t.Fatalf("更新 WAL 样本失败: %v", err)
		}
		thirdResult, err := syncer.SyncOnce(time.Date(2026, 2, 19, 11, 40, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("变更后同步失败: %v", err)
		}
		if thirdResult.CopiedFiles != 1 {
			t.Fatalf("增量同步文件数量错误: want=%d got=%d", 1, thirdResult.CopiedFiles)
		}
		if got := mustReadFile(t, filepath.Join(walMirrorDir, "0000000100000000000000A1")); got != "wal-segment-v2-updated" {
			t.Fatalf("WAL 增量同步内容错误: %q", got)
		}
	})
}

func TestCrossRegionDRRunStartupCatchup(t *testing.T) {
	baseDir := t.TempDir()
	sourceDir := filepath.Join(baseDir, "full_backup")
	targetDir := filepath.Join(baseDir, "remote")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("创建源目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "manifest.json"), []byte("{\"ok\":true}"), 0o640); err != nil {
		t.Fatalf("写入源文件失败: %v", err)
	}

	syncer, err := NewCrossRegionDRSyncer(CrossRegionDRSyncOptions{
		SourcePaths: []string{sourceDir},
		TargetDir:   targetDir,
		Now: func() time.Time {
			return time.Date(2026, 2, 19, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("初始化同步器失败: %v", err)
	}

	mirrors := syncer.SourceMirrors()
	mirrorDir := mirrors[filepath.Clean(sourceDir)]
	targetManifest := filepath.Join(mirrorDir, "manifest.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		syncer.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(targetManifest); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run 启动后应立即执行一次补跑同步")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run 取消后应及时退出")
	}
}

func TestNewCrossRegionDRSyncerRejectsPathOverlap(t *testing.T) {
	baseDir := t.TempDir()
	sourceDir := filepath.Join(baseDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("创建源目录失败: %v", err)
	}

	_, err := NewCrossRegionDRSyncer(CrossRegionDRSyncOptions{
		SourcePaths: []string{sourceDir},
		TargetDir:   filepath.Join(sourceDir, "remote"),
	})
	if err == nil {
		t.Fatalf("源目录与目标目录重叠时应返回错误")
	}
	if !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("重叠路径错误信息不符合预期: %v", err)
	}
}
