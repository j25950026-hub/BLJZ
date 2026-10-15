package system

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlPoint444BackupVerification(t *testing.T) {
	t.Run("控制点 4.4.4 功能验证", func(t *testing.T) {
		baseDir := t.TempDir()
		sourcePath := filepath.Join(baseDir, "ledger.db")
		backupDir := filepath.Join(baseDir, "full_backups")

		if err := ensureSQLiteSampleRow(sourcePath, "backup-verification-1"); err != nil {
			t.Fatalf("准备备份样本失败: %v", err)
		}

		backup, err := NewDailyFullBackup(DailyFullBackupOptions{
			SourcePaths:   []string{sourcePath},
			BackupDir:     backupDir,
			Timezone:      "UTC",
			LowPeakHour:   3,
			LowPeakMinute: 30,
			Now: func() time.Time {
				return time.Date(2026, 2, 19, 3, 40, 0, 0, time.UTC)
			},
		})
		if err != nil {
			t.Fatalf("初始化定时全量备份失败: %v", err)
		}
		backupResult, err := backup.RunOnce(time.Date(2026, 2, 19, 3, 40, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("执行备份失败: %v", err)
		}
		if backupResult.Skipped {
			t.Fatalf("低峰期备份不应被跳过")
		}

		verifier, err := NewBackupVerifier(BackupVerificationOptions{
			BackupDir:      backupDir,
			VerifyInterval: time.Minute,
			Now: func() time.Time {
				return time.Date(2026, 2, 19, 4, 0, 0, 0, time.UTC)
			},
		})
		if err != nil {
			t.Fatalf("初始化备份有效性验证器失败: %v", err)
		}

		result, err := verifier.VerifyOnce(time.Date(2026, 2, 19, 4, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("执行备份有效性验证失败: %v", err)
		}
		if result.Skipped {
			t.Fatalf("存在可用备份时不应跳过验证")
		}
		if result.Date != backupResult.Date {
			t.Fatalf("验证日期错误: want=%s got=%s", backupResult.Date, result.Date)
		}
		if result.ManifestPath != backupResult.ManifestPath {
			t.Fatalf("验证 manifest 路径错误: want=%s got=%s", backupResult.ManifestPath, result.ManifestPath)
		}
		if result.FileCount != 1 || len(result.Files) != 1 {
			t.Fatalf("验证文件数量错误: count=%d files=%+v", result.FileCount, result.Files)
		}
		if result.Files[0].Integrity != "ok" {
			t.Fatalf("SQLite 完整性校验结果错误: %+v", result.Files[0])
		}
		if result.Files[0].RestoredSizeBytes <= 0 {
			t.Fatalf("恢复快照大小应大于 0: %+v", result.Files[0])
		}

		reportPayload, err := os.ReadFile(result.VerificationPath)
		if err != nil {
			t.Fatalf("读取验证报告失败: %v", err)
		}
		var report backupVerificationReport
		if err := json.Unmarshal(reportPayload, &report); err != nil {
			t.Fatalf("解析验证报告失败: %v", err)
		}
		if report.Date != result.Date {
			t.Fatalf("验证报告日期错误: want=%s got=%s", result.Date, report.Date)
		}
		if report.ManifestPath != result.ManifestPath {
			t.Fatalf("验证报告 manifest 路径错误: want=%s got=%s", result.ManifestPath, report.ManifestPath)
		}
		if len(report.Files) != 1 || report.Files[0].Integrity != "ok" {
			t.Fatalf("验证报告文件详情错误: %+v", report.Files)
		}
	})
}

func TestBackupVerificationRunStartupCatchup(t *testing.T) {
	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "ledger.db")
	backupDir := filepath.Join(baseDir, "full_backups")

	if err := ensureSQLiteSampleRow(sourcePath, "backup-verification-run"); err != nil {
		t.Fatalf("准备备份样本失败: %v", err)
	}
	backup, err := NewDailyFullBackup(DailyFullBackupOptions{
		SourcePaths:   []string{sourcePath},
		BackupDir:     backupDir,
		Timezone:      "UTC",
		LowPeakHour:   3,
		LowPeakMinute: 30,
	})
	if err != nil {
		t.Fatalf("初始化备份器失败: %v", err)
	}
	if _, err := backup.RunOnce(time.Date(2026, 2, 19, 3, 40, 0, 0, time.UTC)); err != nil {
		t.Fatalf("写入验证所需备份失败: %v", err)
	}

	logTriggered := make(chan struct{}, 1)
	verifier, err := NewBackupVerifier(BackupVerificationOptions{
		BackupDir:      backupDir,
		VerifyInterval: time.Hour,
		Logf: func(format string, args ...any) {
			if strings.Contains(format, "备份有效性验证完成") {
				select {
				case logTriggered <- struct{}{}:
				default:
				}
			}
		},
		Now: func() time.Time {
			return time.Date(2026, 2, 19, 4, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("初始化备份验证器失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		verifier.Run(ctx)
	}()

	select {
	case <-logTriggered:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run 启动后应立即触发一次补跑验证")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run 取消后应及时退出")
	}
}

func TestBackupVerificationDetectsCorruptedSnapshot(t *testing.T) {
	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "ledger.db")
	backupDir := filepath.Join(baseDir, "full_backups")

	if err := ensureSQLiteSampleRow(sourcePath, "backup-verification-corrupted"); err != nil {
		t.Fatalf("准备备份样本失败: %v", err)
	}
	backup, err := NewDailyFullBackup(DailyFullBackupOptions{
		SourcePaths:   []string{sourcePath},
		BackupDir:     backupDir,
		Timezone:      "UTC",
		LowPeakHour:   3,
		LowPeakMinute: 30,
	})
	if err != nil {
		t.Fatalf("初始化备份器失败: %v", err)
	}
	backupResult, err := backup.RunOnce(time.Date(2026, 2, 19, 3, 40, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("执行备份失败: %v", err)
	}
	if len(backupResult.Files) == 0 {
		t.Fatalf("备份结果中应包含快照文件")
	}
	if err := os.WriteFile(backupResult.Files[0].SnapshotPath, []byte("not-a-valid-sqlite"), 0o640); err != nil {
		t.Fatalf("写入损坏快照失败: %v", err)
	}

	verifier, err := NewBackupVerifier(BackupVerificationOptions{BackupDir: backupDir})
	if err != nil {
		t.Fatalf("初始化备份验证器失败: %v", err)
	}
	_, err = verifier.VerifyOnce(time.Date(2026, 2, 19, 4, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatalf("损坏快照应导致验证失败")
	}
	if !strings.Contains(err.Error(), "verify snapshot file") {
		t.Fatalf("损坏快照错误信息不符合预期: %v", err)
	}
}

func TestBackupVerificationSkipWhenNoManifest(t *testing.T) {
	verifier, err := NewBackupVerifier(BackupVerificationOptions{BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("初始化备份验证器失败: %v", err)
	}

	result, err := verifier.VerifyOnce(time.Date(2026, 2, 19, 4, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("无备份时验证不应报错: %v", err)
	}
	if !result.Skipped {
		t.Fatalf("无备份数据时应跳过验证")
	}
}
