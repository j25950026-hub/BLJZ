package system

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultWALArchiverScanInterval = 2 * time.Second

	PITRTargetActionPromote  = "promote"
	PITRTargetActionPause    = "pause"
	PITRTargetActionShutdown = "shutdown"
)

var (
	walSegmentFilePattern = regexp.MustCompile(`^[0-9A-F]{24}(?:\.partial)?$`)
	walHistoryFilePattern = regexp.MustCompile(`^[0-9A-F]{8}\.history$`)
	walBackupFilePattern  = regexp.MustCompile(`^[0-9A-F]{24}\.[0-9A-F]{8}\.backup$`)
)

type WALArchiverOptions struct {
	SourceDir    string
	ArchiveDir   string
	ScanInterval time.Duration
	Logf         func(format string, args ...any)
	OnArchived   func(WALArchiveResult)
	Now          func() time.Time
}

type WALArchiveResult struct {
	Segment       string
	SourcePath    string
	ArchivePath   string
	SizeBytes     int64
	ArchivedAt    time.Time
	AlreadyExists bool
}

type PITRRecoveryOptions struct {
	ArchiveDir     string
	TargetTime     time.Time
	TargetTimeline string
	TargetAction   string
}

type WALArchiver struct {
	sourceDir    string
	archiveDir   string
	scanInterval time.Duration
	logf         func(format string, args ...any)
	onArchived   func(WALArchiveResult)
	now          func() time.Time

	mu sync.Mutex
}

func NewWALArchiver(opts WALArchiverOptions) (*WALArchiver, error) {
	normalized, err := normalizeWALArchiverOptions(opts)
	if err != nil {
		return nil, err
	}
	return &WALArchiver{
		sourceDir:    normalized.SourceDir,
		archiveDir:   normalized.ArchiveDir,
		scanInterval: normalized.ScanInterval,
		logf:         normalized.Logf,
		onArchived:   normalized.OnArchived,
		now:          normalized.Now,
	}, nil
}

func normalizeWALArchiverOptions(opts WALArchiverOptions) (WALArchiverOptions, error) {
	normalized := opts
	normalized.SourceDir = strings.TrimSpace(normalized.SourceDir)
	normalized.ArchiveDir = strings.TrimSpace(normalized.ArchiveDir)
	if normalized.SourceDir == "" {
		return WALArchiverOptions{}, fmt.Errorf("missing WAL source dir")
	}
	if normalized.ArchiveDir == "" {
		return WALArchiverOptions{}, fmt.Errorf("missing WAL archive dir")
	}
	normalized.SourceDir = filepath.Clean(normalized.SourceDir)
	normalized.ArchiveDir = filepath.Clean(normalized.ArchiveDir)
	if normalized.SourceDir == normalized.ArchiveDir {
		return WALArchiverOptions{}, fmt.Errorf("WAL source dir and archive dir must differ")
	}
	if normalized.ScanInterval <= 0 {
		normalized.ScanInterval = DefaultWALArchiverScanInterval
	}
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	if normalized.Now == nil {
		normalized.Now = time.Now
	}
	sourceStat, err := os.Stat(normalized.SourceDir)
	if err != nil {
		return WALArchiverOptions{}, fmt.Errorf("stat WAL source dir: %w", err)
	}
	if !sourceStat.IsDir() {
		return WALArchiverOptions{}, fmt.Errorf("WAL source dir is not a directory: %s", normalized.SourceDir)
	}
	if err := os.MkdirAll(normalized.ArchiveDir, 0o755); err != nil {
		return WALArchiverOptions{}, fmt.Errorf("create WAL archive dir: %w", err)
	}
	archiveStat, err := os.Stat(normalized.ArchiveDir)
	if err != nil {
		return WALArchiverOptions{}, fmt.Errorf("stat WAL archive dir: %w", err)
	}
	if !archiveStat.IsDir() {
		return WALArchiverOptions{}, fmt.Errorf("WAL archive dir is not a directory: %s", normalized.ArchiveDir)
	}
	return normalized, nil
}

func (w *WALArchiver) SourceDir() string {
	if w == nil {
		return ""
	}
	return w.sourceDir
}

func (w *WALArchiver) ArchiveDir() string {
	if w == nil {
		return ""
	}
	return w.archiveDir
}

func (w *WALArchiver) ScanInterval() time.Duration {
	if w == nil {
		return 0
	}
	return w.scanInterval
}

func (w *WALArchiver) Run(ctx context.Context) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.scanWithLog()

	ticker := time.NewTicker(w.scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanWithLog()
		}
	}
}

func (w *WALArchiver) scanWithLog() {
	archived, err := w.ScanOnce()
	if err != nil {
		w.logf("WAL 归档扫描失败: source=%s archive=%s err=%v", w.sourceDir, w.archiveDir, err)
		return
	}
	if archived > 0 {
		w.logf("WAL 归档完成: archived=%d source=%s archive=%s", archived, w.sourceDir, w.archiveDir)
	}
}

func (w *WALArchiver) ScanOnce() (int, error) {
	if w == nil {
		return 0, nil
	}
	entries, err := os.ReadDir(w.sourceDir)
	if err != nil {
		return 0, err
	}
	candidates := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !IsWALArchivableFile(name) {
			continue
		}
		candidates = append(candidates, name)
	}
	sort.Strings(candidates)

	archivedCount := 0
	var firstErr error
	for _, segment := range candidates {
		result, archiveErr := w.ArchiveSegment(segment)
		if archiveErr != nil {
			if firstErr == nil {
				firstErr = archiveErr
			}
			w.logf("WAL 归档失败: segment=%s err=%v", segment, archiveErr)
			continue
		}
		if result.AlreadyExists {
			continue
		}
		archivedCount++
		w.logf(
			"WAL 分段已归档: segment=%s bytes=%d source=%s archive=%s",
			result.Segment,
			result.SizeBytes,
			result.SourcePath,
			result.ArchivePath,
		)
		if w.onArchived != nil {
			w.onArchived(result)
		}
	}
	return archivedCount, firstErr
}

func (w *WALArchiver) ArchiveSegment(segment string) (WALArchiveResult, error) {
	if w == nil {
		return WALArchiveResult{}, fmt.Errorf("wal archiver is nil")
	}
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" {
		return WALArchiveResult{}, fmt.Errorf("missing WAL segment name")
	}
	if trimmed != filepath.Base(trimmed) {
		return WALArchiveResult{}, fmt.Errorf("invalid WAL segment name: %s", segment)
	}
	if !IsWALArchivableFile(trimmed) {
		return WALArchiveResult{}, fmt.Errorf("unsupported WAL segment file: %s", segment)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	sourcePath := filepath.Join(w.sourceDir, trimmed)
	sourceStat, err := os.Stat(sourcePath)
	if err != nil {
		return WALArchiveResult{}, fmt.Errorf("stat WAL source segment: %w", err)
	}
	if !sourceStat.Mode().IsRegular() {
		return WALArchiveResult{}, fmt.Errorf("WAL source segment is not a regular file: %s", sourcePath)
	}
	archivePath := filepath.Join(w.archiveDir, trimmed)

	if existingStat, existsErr := os.Stat(archivePath); existsErr == nil {
		if existingStat.Size() != sourceStat.Size() {
			return WALArchiveResult{}, fmt.Errorf(
				"WAL archive segment size mismatch: segment=%s source=%d archive=%d",
				trimmed,
				sourceStat.Size(),
				existingStat.Size(),
			)
		}
		return WALArchiveResult{
			Segment:       trimmed,
			SourcePath:    sourcePath,
			ArchivePath:   archivePath,
			SizeBytes:     existingStat.Size(),
			ArchivedAt:    existingStat.ModTime(),
			AlreadyExists: true,
		}, nil
	} else if !errors.Is(existsErr, fs.ErrNotExist) {
		return WALArchiveResult{}, fmt.Errorf("stat WAL archive segment: %w", existsErr)
	}

	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return WALArchiveResult{}, fmt.Errorf("open WAL source segment: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return WALArchiveResult{
				Segment:       trimmed,
				SourcePath:    sourcePath,
				ArchivePath:   archivePath,
				SizeBytes:     sourceStat.Size(),
				ArchivedAt:    w.now(),
				AlreadyExists: true,
			}, nil
		}
		return WALArchiveResult{}, fmt.Errorf("create WAL archive segment: %w", err)
	}

	copied, copyErr := io.Copy(dstFile, srcFile)
	if copyErr != nil {
		_ = dstFile.Close()
		_ = os.Remove(archivePath)
		return WALArchiveResult{}, fmt.Errorf("copy WAL segment: %w", copyErr)
	}
	if syncErr := dstFile.Sync(); syncErr != nil {
		_ = dstFile.Close()
		_ = os.Remove(archivePath)
		return WALArchiveResult{}, fmt.Errorf("sync WAL archive segment: %w", syncErr)
	}
	if closeErr := dstFile.Close(); closeErr != nil {
		_ = os.Remove(archivePath)
		return WALArchiveResult{}, fmt.Errorf("close WAL archive segment: %w", closeErr)
	}
	if copied != sourceStat.Size() {
		_ = os.Remove(archivePath)
		return WALArchiveResult{}, fmt.Errorf(
			"incomplete WAL segment copy: segment=%s source=%d copied=%d",
			trimmed,
			sourceStat.Size(),
			copied,
		)
	}
	return WALArchiveResult{
		Segment:     trimmed,
		SourcePath:  sourcePath,
		ArchivePath: archivePath,
		SizeBytes:   copied,
		ArchivedAt:  w.now(),
	}, nil
}

func IsWALArchivableFile(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	return walSegmentFilePattern.MatchString(trimmed) ||
		walHistoryFilePattern.MatchString(trimmed) ||
		walBackupFilePattern.MatchString(trimmed)
}

func BuildPostgresArchiveSettings(archiveDir string) (string, error) {
	archiveCommand, err := BuildPostgresArchiveCommand(archiveDir)
	if err != nil {
		return "", err
	}
	lines := []string{
		"wal_level = replica",
		"archive_mode = on",
		"archive_timeout = 60s",
		fmt.Sprintf("archive_command = %s", toPostgresConfStringLiteral(archiveCommand)),
	}
	return strings.Join(lines, "\n"), nil
}

func BuildPostgresArchiveCommand(archiveDir string) (string, error) {
	archivePath, err := normalizeWALArchiveDir(archiveDir)
	if err != nil {
		return "", err
	}
	dest := shellDoubleQuote(filepath.ToSlash(filepath.Join(archivePath, "%f")))
	return fmt.Sprintf(`test ! -f %s && cp "%%p" %s`, dest, dest), nil
}

func BuildPostgresRestoreCommand(archiveDir string) (string, error) {
	archivePath, err := normalizeWALArchiveDir(archiveDir)
	if err != nil {
		return "", err
	}
	source := shellDoubleQuote(filepath.ToSlash(filepath.Join(archivePath, "%f")))
	return fmt.Sprintf(`cp %s "%%p"`, source), nil
}

func BuildPITRRecoverySettings(opts PITRRecoveryOptions) (string, error) {
	restoreCommand, err := BuildPostgresRestoreCommand(opts.ArchiveDir)
	if err != nil {
		return "", err
	}
	if opts.TargetTime.IsZero() {
		return "", fmt.Errorf("missing PITR target time")
	}
	action, err := normalizePITRTargetAction(opts.TargetAction)
	if err != nil {
		return "", err
	}
	timeline := strings.TrimSpace(opts.TargetTimeline)
	if timeline == "" {
		timeline = "latest"
	}
	lines := []string{
		fmt.Sprintf("restore_command = %s", toPostgresConfStringLiteral(restoreCommand)),
		fmt.Sprintf("recovery_target_time = %s", toPostgresConfStringLiteral(opts.TargetTime.Format("2006-01-02 15:04:05.000000Z07:00"))),
		fmt.Sprintf("recovery_target_timeline = %s", toPostgresConfStringLiteral(timeline)),
		fmt.Sprintf("recovery_target_action = %s", toPostgresConfStringLiteral(action)),
	}
	return strings.Join(lines, "\n"), nil
}

func normalizeWALArchiveDir(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("missing WAL archive dir")
	}
	return filepath.Clean(trimmed), nil
}

func normalizePITRTargetAction(raw string) (string, error) {
	action := strings.ToLower(strings.TrimSpace(raw))
	if action == "" {
		return PITRTargetActionPromote, nil
	}
	switch action {
	case PITRTargetActionPromote, PITRTargetActionPause, PITRTargetActionShutdown:
		return action, nil
	default:
		return "", fmt.Errorf("invalid PITR target action: %s", raw)
	}
}

func shellDoubleQuote(raw string) string {
	escaped := strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"$", "\\$",
		"`", "\\`",
	).Replace(raw)
	return `"` + escaped + `"`
}

func toPostgresConfStringLiteral(raw string) string {
	return "'" + strings.ReplaceAll(raw, "'", "''") + "'"
}
