package system

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	DefaultBackupVerificationInterval = 6 * time.Hour
	backupVerificationReportName      = "verification.json"
)

type BackupVerificationOptions struct {
	BackupDir      string
	VerifyInterval time.Duration
	Logf           func(format string, args ...any)
	Now            func() time.Time
}

type BackupVerifier struct {
	backupDir      string
	verifyInterval time.Duration
	logf           func(format string, args ...any)
	now            func() time.Time

	mu sync.Mutex
}

type BackupVerificationResult struct {
	Date             string
	ManifestPath     string
	VerificationPath string
	FileCount        int
	Files            []BackupVerificationFile
	TriggeredAt      time.Time
	CompletedAt      time.Time
	Skipped          bool
}

type BackupVerificationFile struct {
	SnapshotPath      string `json:"snapshot_path"`
	RestoredSizeBytes int64  `json:"restored_size_bytes"`
	Integrity         string `json:"integrity"`
}

type backupVerificationReport struct {
	Date         string                   `json:"date"`
	ManifestPath string                   `json:"manifest_path"`
	TriggeredAt  string                   `json:"triggered_at"`
	CompletedAt  string                   `json:"completed_at"`
	Files        []BackupVerificationFile `json:"files"`
}

type backupVerificationManifest struct {
	Date  string                `json:"date"`
	Files []DailyFullBackupFile `json:"files"`
}

func NewBackupVerifier(opts BackupVerificationOptions) (*BackupVerifier, error) {
	backupDir := filepath.Clean(strings.TrimSpace(opts.BackupDir))
	if backupDir == "" || backupDir == "." {
		return nil, fmt.Errorf("missing backup verification dir")
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, fmt.Errorf("create backup verification dir: %w", err)
	}

	verifyInterval := opts.VerifyInterval
	if verifyInterval <= 0 {
		verifyInterval = DefaultBackupVerificationInterval
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return &BackupVerifier{
		backupDir:      backupDir,
		verifyInterval: verifyInterval,
		logf:           logf,
		now:            nowFn,
	}, nil
}

func (v *BackupVerifier) BackupDir() string {
	if v == nil {
		return ""
	}
	return v.backupDir
}

func (v *BackupVerifier) VerifyInterval() time.Duration {
	if v == nil {
		return 0
	}
	return v.verifyInterval
}

func (v *BackupVerifier) Run(ctx context.Context) {
	if v == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	v.runOnceWithLog(v.now())
	ticker := time.NewTicker(v.verifyInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		v.runOnceWithLog(v.now())
	}
}

func (v *BackupVerifier) VerifyOnce(runAt time.Time) (BackupVerificationResult, error) {
	if v == nil {
		return BackupVerificationResult{}, fmt.Errorf("backup verifier is nil")
	}
	if runAt.IsZero() {
		runAt = v.now()
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	result := BackupVerificationResult{
		TriggeredAt: runAt.UTC(),
	}
	dateLabel, manifestPath, found, err := findLatestBackupManifest(v.backupDir)
	if err != nil {
		return result, err
	}
	if !found {
		result.Skipped = true
		return result, nil
	}
	result.Date = dateLabel
	result.ManifestPath = manifestPath
	result.VerificationPath = filepath.Join(filepath.Dir(manifestPath), backupVerificationReportName)

	manifest, err := readBackupVerificationManifest(manifestPath)
	if err != nil {
		return result, err
	}
	if len(manifest.Files) == 0 {
		return result, fmt.Errorf("backup manifest has no files: %s", manifestPath)
	}

	restoreDir, err := os.MkdirTemp("", "backup-restore-verification-")
	if err != nil {
		return result, fmt.Errorf("create backup restore temp dir: %w", err)
	}
	defer os.RemoveAll(restoreDir)

	verifiedFiles := make([]BackupVerificationFile, 0, len(manifest.Files))
	for index, file := range manifest.Files {
		snapshotPath, resolveErr := resolveBackupSnapshotPath(manifestPath, file.SnapshotPath)
		if resolveErr != nil {
			return result, fmt.Errorf("resolve snapshot path: %w", resolveErr)
		}
		restoredPath := filepath.Join(restoreDir, fmt.Sprintf("%02d_%s", index+1, filepath.Base(snapshotPath)))
		restoredSize, restoreErr := restoreSnapshotFile(snapshotPath, restoredPath)
		if restoreErr != nil {
			return result, fmt.Errorf("restore snapshot file %s: %w", snapshotPath, restoreErr)
		}
		integrity, verifyErr := verifySQLiteSnapshot(restoredPath)
		if verifyErr != nil {
			return result, fmt.Errorf("verify snapshot file %s: %w", snapshotPath, verifyErr)
		}
		verifiedFiles = append(verifiedFiles, BackupVerificationFile{
			SnapshotPath:      snapshotPath,
			RestoredSizeBytes: restoredSize,
			Integrity:         integrity,
		})
	}

	result.Files = verifiedFiles
	result.FileCount = len(verifiedFiles)
	result.CompletedAt = v.now().UTC()

	report := backupVerificationReport{
		Date:         dateLabel,
		ManifestPath: manifestPath,
		TriggeredAt:  result.TriggeredAt.Format(time.RFC3339),
		CompletedAt:  result.CompletedAt.Format(time.RFC3339),
		Files:        result.Files,
	}
	if err := writeBackupVerificationReport(result.VerificationPath, report); err != nil {
		return result, err
	}
	return result, nil
}

func (v *BackupVerifier) runOnceWithLog(runAt time.Time) {
	result, err := v.VerifyOnce(runAt)
	if err != nil {
		v.logf(
			"备份有效性验证失败: date=%s backup_dir=%s manifest=%s err=%v",
			result.Date,
			v.backupDir,
			result.ManifestPath,
			err,
		)
		return
	}
	if result.Skipped {
		return
	}
	v.logf(
		"备份有效性验证完成: date=%s files=%d backup_dir=%s manifest=%s report=%s",
		result.Date,
		result.FileCount,
		v.backupDir,
		result.ManifestPath,
		result.VerificationPath,
	)
}

func findLatestBackupManifest(backupDir string) (string, string, bool, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("read backup dir: %w", err)
	}

	latestDate := ""
	latestManifest := ""
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dateLabel := entry.Name()
		if _, err := time.Parse(dailyFullBackupDateLayout, dateLabel); err != nil {
			continue
		}
		manifestPath := filepath.Join(backupDir, dateLabel, dailyFullBackupManifestName)
		manifestStat, err := os.Stat(manifestPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", "", false, fmt.Errorf("stat backup manifest %s: %w", manifestPath, err)
		}
		if !manifestStat.Mode().IsRegular() {
			continue
		}
		if latestDate == "" || dateLabel > latestDate {
			latestDate = dateLabel
			latestManifest = manifestPath
		}
	}
	if latestDate == "" {
		return "", "", false, nil
	}
	return latestDate, latestManifest, true, nil
}

func readBackupVerificationManifest(path string) (backupVerificationManifest, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return backupVerificationManifest{}, fmt.Errorf("read backup manifest: %w", err)
	}
	var manifest backupVerificationManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return backupVerificationManifest{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	return manifest, nil
}

func resolveBackupSnapshotPath(manifestPath string, snapshotPath string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(snapshotPath))
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("missing snapshot path")
	}
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}

	if _, err := os.Stat(cleaned); err == nil {
		return cleaned, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat snapshot path %s: %w", cleaned, err)
	}

	resolved := filepath.Join(filepath.Dir(manifestPath), filepath.Base(cleaned))
	return resolved, nil
}

func restoreSnapshotFile(sourcePath string, destinationPath string) (int64, error) {
	sourcePath = filepath.Clean(strings.TrimSpace(sourcePath))
	if sourcePath == "" || sourcePath == "." {
		return 0, fmt.Errorf("missing source snapshot path")
	}
	sourceStat, err := os.Stat(sourcePath)
	if err != nil {
		return 0, fmt.Errorf("stat source snapshot: %w", err)
	}
	if !sourceStat.Mode().IsRegular() {
		return 0, fmt.Errorf("source snapshot is not regular file: %s", sourcePath)
	}

	destinationPath = filepath.Clean(strings.TrimSpace(destinationPath))
	if destinationPath == "" || destinationPath == "." {
		return 0, fmt.Errorf("missing destination snapshot path")
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return 0, fmt.Errorf("create restore dir: %w", err)
	}
	tempPath := destinationPath + ".tmp"
	if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("cleanup restore temp file: %w", err)
	}
	defer os.Remove(tempPath)

	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return 0, fmt.Errorf("open source snapshot: %w", err)
	}
	defer sourceFile.Close()

	restoredFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, fmt.Errorf("open restore snapshot: %w", err)
	}

	copiedBytes, copyErr := io.Copy(restoredFile, sourceFile)
	if closeErr := restoredFile.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return 0, fmt.Errorf("copy snapshot: %w", copyErr)
	}

	if err := os.Rename(tempPath, destinationPath); err != nil {
		return 0, fmt.Errorf("persist restored snapshot: %w", err)
	}
	return copiedBytes, nil
}

func verifySQLiteSnapshot(path string) (string, error) {
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return "", fmt.Errorf("open restored sqlite db: %w", err)
	}
	defer raw.Close()

	raw.SetMaxOpenConns(1)
	busyTimeoutMS := int(dailyFullBackupSQLiteBusyWait / time.Millisecond)
	if _, err := raw.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMS)); err != nil {
		return "", fmt.Errorf("set sqlite busy timeout: %w", err)
	}

	var quickCheck string
	if err := raw.QueryRow("PRAGMA quick_check").Scan(&quickCheck); err != nil {
		return "", fmt.Errorf("run sqlite quick_check: %w", err)
	}
	if !strings.EqualFold(quickCheck, "ok") {
		return quickCheck, fmt.Errorf("sqlite quick_check failed: %s", quickCheck)
	}
	return "ok", nil
}

func writeBackupVerificationReport(path string, report backupVerificationReport) error {
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup verification report: %w", err)
	}
	payload = append(payload, '\n')
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o640); err != nil {
		return fmt.Errorf("write backup verification report: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("persist backup verification report: %w", err)
	}
	return nil
}
