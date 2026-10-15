package system

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestControlPoint442DailyFullBackup(t *testing.T) {
	t.Run("控制点 4.4.2 功能验证", func(t *testing.T) {
		baseDir := t.TempDir()
		sourcePath := filepath.Join(baseDir, "ledger.db")
		backupDir := filepath.Join(baseDir, "daily_full_backup")

		if err := ensureSQLiteSampleRow(sourcePath, "daily-full-backup-1"); err != nil {
			t.Fatalf("准备备份样本失败: %v", err)
		}

		nowAt := time.Date(2026, 2, 19, 3, 35, 0, 0, time.UTC)
		backup, err := NewDailyFullBackup(DailyFullBackupOptions{
			SourcePaths:   []string{sourcePath},
			BackupDir:     backupDir,
			Timezone:      "UTC",
			LowPeakHour:   3,
			LowPeakMinute: 30,
			Now: func() time.Time {
				return nowAt
			},
		})
		if err != nil {
			t.Fatalf("初始化定时全量备份失败: %v", err)
		}

		firstResult, err := backup.RunOnce(nowAt)
		if err != nil {
			t.Fatalf("执行首次全量备份失败: %v", err)
		}
		if firstResult.Skipped {
			t.Fatalf("低峰期应触发首次备份")
		}
		if len(firstResult.Files) != 1 {
			t.Fatalf("首次备份文件数量错误: %+v", firstResult.Files)
		}
		if _, err := os.Stat(firstResult.ManifestPath); err != nil {
			t.Fatalf("首次备份后应写入 manifest: %v", err)
		}
		if got, err := countSQLiteRows(firstResult.Files[0].SnapshotPath); err != nil {
			t.Fatalf("读取首次备份快照失败: %v", err)
		} else if got != 1 {
			t.Fatalf("首次备份快照行数错误: want=1 got=%d", got)
		}

		secondResult, err := backup.RunOnce(time.Date(2026, 2, 19, 5, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("同日重复备份不应报错: %v", err)
		}
		if !secondResult.Skipped {
			t.Fatalf("同日二次执行应命中幂等跳过")
		}

		beforeWindowResult, err := backup.RunOnce(time.Date(2026, 2, 20, 2, 59, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("低峰期前执行不应报错: %v", err)
		}
		if !beforeWindowResult.Skipped {
			t.Fatalf("低峰期前应跳过备份")
		}

		if err := ensureSQLiteSampleRow(sourcePath, "daily-full-backup-2"); err != nil {
			t.Fatalf("写入次日样本失败: %v", err)
		}
		nowAt = time.Date(2026, 2, 20, 3, 40, 0, 0, time.UTC)
		thirdResult, err := backup.RunOnce(nowAt)
		if err != nil {
			t.Fatalf("执行次日备份失败: %v", err)
		}
		if thirdResult.Skipped {
			t.Fatalf("次日低峰期应执行备份")
		}
		if got, err := countSQLiteRows(thirdResult.Files[0].SnapshotPath); err != nil {
			t.Fatalf("读取次日备份快照失败: %v", err)
		} else if got != 2 {
			t.Fatalf("次日备份快照行数错误: want=2 got=%d", got)
		}
	})
}

func TestDailyFullBackupRunStartupCatchup(t *testing.T) {
	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "ledger.db")
	if err := ensureSQLiteSampleRow(sourcePath, "startup-catchup"); err != nil {
		t.Fatalf("准备补跑样本失败: %v", err)
	}

	nowAt := time.Date(2026, 2, 19, 3, 45, 0, 0, time.UTC)
	logTriggered := make(chan struct{}, 1)
	backup, err := NewDailyFullBackup(DailyFullBackupOptions{
		SourcePaths: []string{sourcePath},
		BackupDir:   filepath.Join(baseDir, "backup"),
		Timezone:    "UTC",
		Logf: func(format string, args ...any) {
			if strings.Contains(format, "定时全量备份完成") {
				select {
				case logTriggered <- struct{}{}:
				default:
				}
			}
		},
		Now: func() time.Time {
			return nowAt
		},
	})
	if err != nil {
		t.Fatalf("初始化备份器失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		backup.Run(ctx)
	}()

	select {
	case <-logTriggered:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run 启动后应触发一次低峰期补跑")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run 取消后应及时退出")
	}
}

func TestDurationUntilNextDailyFullBackup(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 2, 19, 3, 29, 30, 0, loc)
	wait := durationUntilNextDailyFullBackup(now, loc, 3, 30)
	if wait != 30*time.Second {
		t.Fatalf("距低峰期等待时长错误: %s", wait)
	}
}

func ensureSQLiteSampleRow(path string, note string) error {
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer raw.Close()

	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS ledger_entries (
id INTEGER PRIMARY KEY AUTOINCREMENT,
note TEXT NOT NULL
)`); err != nil {
		return err
	}
	_, err = raw.Exec(`INSERT INTO ledger_entries (note) VALUES (?)`, note)
	return err
}

func countSQLiteRows(path string) (int, error) {
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer raw.Close()

	var rowCount int
	if err := raw.QueryRow(`SELECT COUNT(1) FROM ledger_entries`).Scan(&rowCount); err != nil {
		return 0, err
	}
	return rowCount, nil
}
