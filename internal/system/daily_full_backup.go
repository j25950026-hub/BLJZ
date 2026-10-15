package system

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	DefaultDailyFullBackupHour   = 3
	DefaultDailyFullBackupMinute = 30

	dailyFullBackupDateLayout     = "2006-01-02"
	dailyFullBackupManifestName   = "manifest.json"
	dailyFullBackupSQLiteBusyWait = 5 * time.Second
)

type DailyFullBackupOptions struct {
	SourcePaths   []string
	BackupDir     string
	Timezone      string
	LowPeakHour   int
	LowPeakMinute int
	Logf          func(format string, args ...any)
	Now           func() time.Time
}

type DailyFullBackup struct {
	sourcePaths   []string
	backupDir     string
	timezoneName  string
	location      *time.Location
	lowPeakHour   int
	lowPeakMinute int
	logf          func(format string, args ...any)
	now           func() time.Time
}

type DailyFullBackupResult struct {
	Date         string
	BackupDir    string
	ManifestPath string
	Files        []DailyFullBackupFile
	TriggeredAt  time.Time
	CompletedAt  time.Time
	Skipped      bool
}

type DailyFullBackupFile struct {
	SourcePath   string `json:"source_path"`
	SnapshotPath string `json:"snapshot_path"`
	SizeBytes    int64  `json:"size_bytes"`
}

type dailyFullBackupManifest struct {
	Date          string                `json:"date"`
	Timezone      string                `json:"timezone"`
	ScheduledTime string                `json:"scheduled_time"`
	TriggeredAt   string                `json:"triggered_at"`
	CompletedAt   string                `json:"completed_at"`
	Files         []DailyFullBackupFile `json:"files"`
}

func NewDailyFullBackup(opts DailyFullBackupOptions) (*DailyFullBackup, error) {
	sourcePaths := normalizeDailyFullBackupSources(opts.SourcePaths)
	if len(sourcePaths) == 0 {
		return nil, fmt.Errorf("missing daily full backup source paths")
	}
	backupDir := filepath.Clean(strings.TrimSpace(opts.BackupDir))
	if backupDir == "" || backupDir == "." {
		return nil, fmt.Errorf("missing daily full backup dir")
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, fmt.Errorf("create daily full backup dir: %w", err)
	}

	scheduleHour, scheduleMinute := normalizeDailyBackupSchedule(opts.LowPeakHour, opts.LowPeakMinute)
	timezoneName, location := normalizeDailyFullBackupLocation(opts.Timezone)
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &DailyFullBackup{
		sourcePaths:   sourcePaths,
		backupDir:     backupDir,
		timezoneName:  timezoneName,
		location:      location,
		lowPeakHour:   scheduleHour,
		lowPeakMinute: scheduleMinute,
		logf:          logf,
		now:           nowFn,
	}, nil
}

func (b *DailyFullBackup) SourcePaths() []string {
	if b == nil {
		return nil
	}
	out := make([]string, 0, len(b.sourcePaths))
	out = append(out, b.sourcePaths...)
	return out
}

func (b *DailyFullBackup) BackupDir() string {
	if b == nil {
		return ""
	}
	return b.backupDir
}

func (b *DailyFullBackup) Timezone() string {
	if b == nil {
		return ""
	}
	return b.timezoneName
}

func (b *DailyFullBackup) ScheduleTime() string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", b.lowPeakHour, b.lowPeakMinute)
}

func (b *DailyFullBackup) Run(ctx context.Context) {
	if b == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.runOnceWithLog(b.now())
	for {
		wait := durationUntilNextDailyFullBackup(b.now(), b.location, b.lowPeakHour, b.lowPeakMinute)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		b.runOnceWithLog(b.now())
	}
}

func (b *DailyFullBackup) RunOnce(runAt time.Time) (DailyFullBackupResult, error) {
	if b == nil {
		return DailyFullBackupResult{}, fmt.Errorf("daily full backup is nil")
	}
	if runAt.IsZero() {
		runAt = b.now()
	}

	localRunAt := runAt.In(b.location)
	dayLabel := localRunAt.Format(dailyFullBackupDateLayout)
	backupDir := filepath.Join(b.backupDir, dayLabel)
	manifestPath := filepath.Join(backupDir, dailyFullBackupManifestName)

	result := DailyFullBackupResult{
		Date:         dayLabel,
		BackupDir:    backupDir,
		ManifestPath: manifestPath,
		TriggeredAt:  runAt.UTC(),
	}
	if !dailyBackupWindowReached(localRunAt, b.lowPeakHour, b.lowPeakMinute) {
		result.Skipped = true
		return result, nil
	}
	if _, err := os.Stat(manifestPath); err == nil {
		result.Skipped = true
		return result, nil
	} else if !os.IsNotExist(err) {
		return result, fmt.Errorf("stat backup manifest: %w", err)
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return result, fmt.Errorf("create backup snapshot dir: %w", err)
	}

	files := make([]DailyFullBackupFile, 0, len(b.sourcePaths))
	for index, sourcePath := range b.sourcePaths {
		filename := fmt.Sprintf("%02d_%s", index+1, filepath.Base(sourcePath))
		snapshotPath := filepath.Join(backupDir, filename)
		sizeBytes, err := snapshotSQLiteDatabase(sourcePath, snapshotPath)
		if err != nil {
			return result, fmt.Errorf("snapshot source %s: %w", sourcePath, err)
		}
		files = append(files, DailyFullBackupFile{
			SourcePath:   sourcePath,
			SnapshotPath: snapshotPath,
			SizeBytes:    sizeBytes,
		})
	}

	result.Files = files
	result.CompletedAt = b.now().UTC()
	manifest := dailyFullBackupManifest{
		Date:          result.Date,
		Timezone:      b.timezoneName,
		ScheduledTime: b.ScheduleTime(),
		TriggeredAt:   result.TriggeredAt.Format(time.RFC3339),
		CompletedAt:   result.CompletedAt.Format(time.RFC3339),
		Files:         result.Files,
	}
	if err := writeDailyFullBackupManifest(result.ManifestPath, manifest); err != nil {
		return result, err
	}
	return result, nil
}

func durationUntilNextDailyFullBackup(now time.Time, loc *time.Location, scheduleHour, scheduleMinute int) time.Duration {
	if loc == nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), scheduleHour, scheduleMinute, 0, 0, loc)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	wait := next.Sub(localNow)
	if wait <= 0 {
		return time.Second
	}
	return wait
}

func (b *DailyFullBackup) runOnceWithLog(runAt time.Time) {
	result, err := b.RunOnce(runAt)
	if err != nil {
		b.logf(
			"定时全量备份失败: date=%s timezone=%s schedule=%s backup_dir=%s err=%v",
			result.Date,
			b.timezoneName,
			b.ScheduleTime(),
			b.backupDir,
			err,
		)
		return
	}
	if result.Skipped {
		return
	}
	var totalBytes int64
	for _, file := range result.Files {
		totalBytes += file.SizeBytes
	}
	b.logf(
		"定时全量备份完成: date=%s timezone=%s schedule=%s files=%d size_bytes=%d backup_dir=%s manifest=%s",
		result.Date,
		b.timezoneName,
		b.ScheduleTime(),
		len(result.Files),
		totalBytes,
		result.BackupDir,
		result.ManifestPath,
	)
}

func normalizeDailyFullBackupSources(sourcePaths []string) []string {
	out := make([]string, 0, len(sourcePaths))
	seen := make(map[string]struct{}, len(sourcePaths))
	for _, sourcePath := range sourcePaths {
		trimmed := strings.TrimSpace(sourcePath)
		if trimmed == "" {
			continue
		}
		cleaned := filepath.Clean(trimmed)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func normalizeDailyBackupSchedule(hour, minute int) (int, int) {
	if hour < 0 || hour > 23 {
		hour = DefaultDailyFullBackupHour
	}
	if minute < 0 || minute > 59 {
		minute = DefaultDailyFullBackupMinute
	}
	return hour, minute
}

func normalizeDailyFullBackupLocation(timezoneName string) (string, *time.Location) {
	trimmed := strings.TrimSpace(timezoneName)
	if trimmed == "" {
		return "UTC", time.UTC
	}
	location, err := time.LoadLocation(trimmed)
	if err != nil {
		return "UTC", time.UTC
	}
	return trimmed, location
}

func dailyBackupWindowReached(localNow time.Time, scheduleHour, scheduleMinute int) bool {
	if localNow.Hour() != scheduleHour {
		return localNow.Hour() > scheduleHour
	}
	return localNow.Minute() >= scheduleMinute
}

func snapshotSQLiteDatabase(sourcePath string, destinationPath string) (int64, error) {
	sourcePath = filepath.Clean(strings.TrimSpace(sourcePath))
	if sourcePath == "" || sourcePath == "." {
		return 0, fmt.Errorf("missing source path")
	}
	sourceStat, err := os.Stat(sourcePath)
	if err != nil {
		return 0, fmt.Errorf("stat source: %w", err)
	}
	if !sourceStat.Mode().IsRegular() {
		return 0, fmt.Errorf("source is not file: %s", sourcePath)
	}

	destinationPath = filepath.Clean(strings.TrimSpace(destinationPath))
	if destinationPath == "" || destinationPath == "." {
		return 0, fmt.Errorf("missing destination path")
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return 0, fmt.Errorf("create destination dir: %w", err)
	}
	tempPath := destinationPath + ".tmp"
	if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("cleanup temp snapshot: %w", err)
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()

	sqliteDB, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return 0, fmt.Errorf("open source sqlite db: %w", err)
	}
	sqliteDB.SetMaxOpenConns(1)
	defer sqliteDB.Close()

	busyTimeoutMS := int(dailyFullBackupSQLiteBusyWait / time.Millisecond)
	if _, err := sqliteDB.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMS)); err != nil {
		return 0, fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	vacuumSQL := fmt.Sprintf("VACUUM INTO %s", toSQLiteStringLiteral(tempPath))
	if _, err := sqliteDB.Exec(vacuumSQL); err != nil {
		return 0, fmt.Errorf("vacuum into snapshot: %w", err)
	}

	if err := os.Remove(destinationPath); err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("cleanup old snapshot: %w", err)
	}
	if err := os.Rename(tempPath, destinationPath); err != nil {
		return 0, fmt.Errorf("rename snapshot: %w", err)
	}
	info, err := os.Stat(destinationPath)
	if err != nil {
		return 0, fmt.Errorf("stat snapshot: %w", err)
	}
	return info.Size(), nil
}

func writeDailyFullBackupManifest(path string, manifest dailyFullBackupManifest) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup manifest: %w", err)
	}
	payload = append(payload, '\n')
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o640); err != nil {
		return fmt.Errorf("write backup manifest: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("persist backup manifest: %w", err)
	}
	return nil
}

func toSQLiteStringLiteral(raw string) string {
	return "'" + strings.ReplaceAll(raw, "'", "''") + "'"
}
