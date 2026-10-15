package system

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const DefaultCrossRegionDRSyncInterval = 30 * time.Second

type CrossRegionDRSyncOptions struct {
	SourcePaths  []string
	TargetDir    string
	SyncInterval time.Duration
	Logf         func(format string, args ...any)
	Now          func() time.Time
}

type CrossRegionDRSyncResult struct {
	SourceCount int
	FileCount   int
	CopiedFiles int
	CopiedBytes int64
	TargetDir   string
	TriggeredAt time.Time
	CompletedAt time.Time
}

type CrossRegionDRSyncer struct {
	sources      []crossRegionDRSource
	targetDir    string
	syncInterval time.Duration
	logf         func(format string, args ...any)
	now          func() time.Time

	mu sync.Mutex
}

type crossRegionDRSource struct {
	path       string
	mirrorPath string
	isDir      bool
}

type crossRegionDRSyncStats struct {
	fileCount   int
	copiedFiles int
	copiedBytes int64
}

func NewCrossRegionDRSyncer(opts CrossRegionDRSyncOptions) (*CrossRegionDRSyncer, error) {
	sourcePaths := normalizeCrossRegionDRSourcePaths(opts.SourcePaths)
	if len(sourcePaths) == 0 {
		return nil, fmt.Errorf("missing cross-region DR source paths")
	}

	targetDir := filepath.Clean(strings.TrimSpace(opts.TargetDir))
	if targetDir == "" || targetDir == "." {
		return nil, fmt.Errorf("missing cross-region DR target dir")
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cross-region DR target dir: %w", err)
	}

	sources := make([]crossRegionDRSource, 0, len(sourcePaths))
	for index, sourcePath := range sourcePaths {
		if crossRegionDRPathsOverlap(sourcePath, targetDir) {
			return nil, fmt.Errorf(
				"cross-region DR source path overlaps target dir: source=%s target=%s",
				sourcePath,
				targetDir,
			)
		}
		sourceStat, err := os.Stat(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("stat cross-region DR source path %s: %w", sourcePath, err)
		}
		if !sourceStat.IsDir() && !sourceStat.Mode().IsRegular() {
			return nil, fmt.Errorf("cross-region DR source path is not file or directory: %s", sourcePath)
		}
		sources = append(sources, crossRegionDRSource{
			path:       sourcePath,
			mirrorPath: filepath.Join(targetDir, buildCrossRegionDRSourceLabel(index, sourcePath)),
			isDir:      sourceStat.IsDir(),
		})
	}

	syncInterval := opts.SyncInterval
	if syncInterval <= 0 {
		syncInterval = DefaultCrossRegionDRSyncInterval
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return &CrossRegionDRSyncer{
		sources:      sources,
		targetDir:    targetDir,
		syncInterval: syncInterval,
		logf:         logf,
		now:          nowFn,
	}, nil
}

func (s *CrossRegionDRSyncer) SourcePaths() []string {
	if s == nil {
		return nil
	}
	paths := make([]string, 0, len(s.sources))
	for _, source := range s.sources {
		paths = append(paths, source.path)
	}
	return paths
}

func (s *CrossRegionDRSyncer) SourceMirrors() map[string]string {
	if s == nil {
		return nil
	}
	out := make(map[string]string, len(s.sources))
	for _, source := range s.sources {
		out[source.path] = source.mirrorPath
	}
	return out
}

func (s *CrossRegionDRSyncer) TargetDir() string {
	if s == nil {
		return ""
	}
	return s.targetDir
}

func (s *CrossRegionDRSyncer) SyncInterval() time.Duration {
	if s == nil {
		return 0
	}
	return s.syncInterval
}

func (s *CrossRegionDRSyncer) Run(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.runOnceWithLog(s.now())

	ticker := time.NewTicker(s.syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnceWithLog(s.now())
		}
	}
}

func (s *CrossRegionDRSyncer) SyncOnce(runAt time.Time) (CrossRegionDRSyncResult, error) {
	if s == nil {
		return CrossRegionDRSyncResult{}, fmt.Errorf("cross-region DR syncer is nil")
	}
	if runAt.IsZero() {
		runAt = s.now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result := CrossRegionDRSyncResult{
		SourceCount: len(s.sources),
		TargetDir:   s.targetDir,
		TriggeredAt: runAt.UTC(),
	}
	var firstErr error
	for _, source := range s.sources {
		stats, err := s.syncSource(source)
		result.FileCount += stats.fileCount
		result.CopiedFiles += stats.copiedFiles
		result.CopiedBytes += stats.copiedBytes
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sync source %s: %w", source.path, err)
		}
	}
	result.CompletedAt = s.now().UTC()
	return result, firstErr
}

func (s *CrossRegionDRSyncer) runOnceWithLog(runAt time.Time) {
	result, err := s.SyncOnce(runAt)
	if err != nil {
		s.logf(
			"跨区域容灾同步失败: sources=%d scanned_files=%d copied_files=%d copied_bytes=%d target=%s err=%v",
			result.SourceCount,
			result.FileCount,
			result.CopiedFiles,
			result.CopiedBytes,
			result.TargetDir,
			err,
		)
		return
	}
	if result.CopiedFiles == 0 {
		return
	}
	s.logf(
		"跨区域容灾同步完成: sources=%d scanned_files=%d copied_files=%d copied_bytes=%d target=%s",
		result.SourceCount,
		result.FileCount,
		result.CopiedFiles,
		result.CopiedBytes,
		result.TargetDir,
	)
}

func (s *CrossRegionDRSyncer) syncSource(source crossRegionDRSource) (crossRegionDRSyncStats, error) {
	if source.isDir {
		return syncCrossRegionDRDirectory(source.path, source.mirrorPath)
	}
	return syncCrossRegionDRFileSource(source.path, source.mirrorPath)
}

func syncCrossRegionDRDirectory(sourceDir, destinationDir string) (crossRegionDRSyncStats, error) {
	stats := crossRegionDRSyncStats{}
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return stats, fmt.Errorf("create destination dir: %w", err)
	}
	err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return fmt.Errorf("build relative path: %w", err)
		}
		if relativePath == "." {
			return nil
		}
		destinationPath := filepath.Join(destinationDir, relativePath)
		if entry.IsDir() {
			if err := os.MkdirAll(destinationPath, 0o755); err != nil {
				return fmt.Errorf("create destination subdir: %w", err)
			}
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat source file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		stats.fileCount++
		copiedBytes, copied, err := syncCrossRegionDRFile(path, destinationPath, info)
		if err != nil {
			return err
		}
		if copied {
			stats.copiedFiles++
			stats.copiedBytes += copiedBytes
		}
		return nil
	})
	return stats, err
}

func syncCrossRegionDRFileSource(sourcePath, destinationPath string) (crossRegionDRSyncStats, error) {
	sourceStat, err := os.Stat(sourcePath)
	if err != nil {
		return crossRegionDRSyncStats{}, fmt.Errorf("stat source file: %w", err)
	}
	if !sourceStat.Mode().IsRegular() {
		return crossRegionDRSyncStats{}, fmt.Errorf("source path is not regular file: %s", sourcePath)
	}
	stats := crossRegionDRSyncStats{
		fileCount: 1,
	}
	copiedBytes, copied, err := syncCrossRegionDRFile(sourcePath, destinationPath, sourceStat)
	if err != nil {
		return stats, err
	}
	if copied {
		stats.copiedFiles = 1
		stats.copiedBytes = copiedBytes
	}
	return stats, nil
}

func syncCrossRegionDRFile(sourcePath, destinationPath string, sourceStat os.FileInfo) (int64, bool, error) {
	if sourceStat == nil {
		stat, err := os.Stat(sourcePath)
		if err != nil {
			return 0, false, fmt.Errorf("stat source file: %w", err)
		}
		sourceStat = stat
	}
	if !sourceStat.Mode().IsRegular() {
		return 0, false, fmt.Errorf("source path is not regular file: %s", sourcePath)
	}

	if destinationStat, err := os.Stat(destinationPath); err == nil {
		if !destinationStat.Mode().IsRegular() {
			return 0, false, fmt.Errorf("destination path is not regular file: %s", destinationPath)
		}
		if destinationStat.Size() == sourceStat.Size() && !sourceStat.ModTime().After(destinationStat.ModTime()) {
			return 0, false, nil
		}
	} else if !os.IsNotExist(err) {
		return 0, false, fmt.Errorf("stat destination file: %w", err)
	}

	if err := copyCrossRegionDRFileAtomic(sourcePath, destinationPath, sourceStat.Mode().Perm(), sourceStat.ModTime()); err != nil {
		return 0, false, err
	}
	return sourceStat.Size(), true, nil
}

func copyCrossRegionDRFileAtomic(sourcePath, destinationPath string, mode fs.FileMode, modifiedAt time.Time) error {
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return fmt.Errorf("create destination parent dir: %w", err)
	}
	temporaryPath := destinationPath + ".tmp"
	if err := os.Remove(temporaryPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cleanup temporary destination: %w", err)
	}
	defer func() {
		_ = os.Remove(temporaryPath)
	}()

	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open destination file: %w", err)
	}
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		_ = dstFile.Close()
		return fmt.Errorf("copy file content: %w", err)
	}
	if err := dstFile.Sync(); err != nil {
		_ = dstFile.Close()
		return fmt.Errorf("sync destination file: %w", err)
	}
	if err := dstFile.Close(); err != nil {
		return fmt.Errorf("close destination file: %w", err)
	}
	if err := os.Chtimes(temporaryPath, modifiedAt, modifiedAt); err != nil {
		return fmt.Errorf("update destination file timestamp: %w", err)
	}
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		return fmt.Errorf("persist destination file: %w", err)
	}
	return nil
}

func normalizeCrossRegionDRSourcePaths(sourcePaths []string) []string {
	out := make([]string, 0, len(sourcePaths))
	seen := make(map[string]struct{}, len(sourcePaths))
	for _, sourcePath := range sourcePaths {
		trimmed := strings.TrimSpace(sourcePath)
		if trimmed == "" {
			continue
		}
		cleaned := filepath.Clean(trimmed)
		if cleaned == "." {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func buildCrossRegionDRSourceLabel(index int, sourcePath string) string {
	base := strings.TrimSpace(filepath.Base(sourcePath))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "source"
	}
	return fmt.Sprintf("%02d_%s", index+1, base)
}

func crossRegionDRPathsOverlap(sourcePath, targetPath string) bool {
	return crossRegionDRPathContains(sourcePath, targetPath) || crossRegionDRPathContains(targetPath, sourcePath)
}

func crossRegionDRPathContains(parentPath, childPath string) bool {
	relativePath, err := filepath.Rel(parentPath, childPath)
	if err != nil {
		return false
	}
	if relativePath == "." {
		return true
	}
	if relativePath == ".." {
		return false
	}
	return !strings.HasPrefix(relativePath, ".."+string(filepath.Separator))
}
