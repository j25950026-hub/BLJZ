package bot

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultAddressVerificationMediaDirectory = "/var/lib/ledgerbot/telegram-media"
	defaultAddressVerificationMediaBaseURL   = "https://autobookbl.site/tg/media"
	defaultAddressVerificationMediaTTL       = time.Hour
	defaultAddressVerificationMediaInterval  = 15 * time.Minute
	defaultAddressVerificationTempTTL        = time.Hour
	defaultAddressVerificationMediaMaxFiles  = 10000
	defaultAddressVerificationMediaMaxBytes  = 2 << 30
	addressVerificationMediaNameLength       = 32
)

var (
	addressVerificationMediaNamePattern   = regexp.MustCompile(`^[0-9a-f]{32}\.jpg$`)
	addressVerificationMediaTempPattern   = regexp.MustCompile(`^\.tmp-[0-9a-f]{32}$`)
	errAddressVerificationMediaConfig     = errors.New("invalid address verification media configuration")
	errAddressVerificationMediaNotStarted = errors.New("address verification media publisher is not started")
	errAddressVerificationMediaClosed     = errors.New("address verification media publisher is closed")
	errAddressVerificationMediaOverLimit  = errors.New("address verification media directory is over limit")
)

type AddressVerificationMediaPublisher interface {
	Publish(ctx context.Context, imageBytes []byte) (string, error)
}

type addressVerificationMediaLifecycle interface {
	Start(ctx context.Context) error
	Close(ctx context.Context) error
}

type LocalAddressVerificationMediaConfig struct {
	Directory       string
	PublicBaseURL   string
	TTL             time.Duration
	CleanupInterval time.Duration
	CreateDirectory bool
	MaxFiles        int
	MaxBytes        int64
	Now             func() time.Time
}

type LocalAddressVerificationMediaPublisher struct {
	directory       string
	publicBaseURL   string
	ttl             time.Duration
	cleanupInterval time.Duration
	tempTTL         time.Duration
	maxFiles        int
	maxBytes        int64
	now             func() time.Time

	mu        sync.Mutex
	started   bool
	closed    bool
	overLimit bool
	stop      chan struct{}
	done      chan struct{}
}

type addressVerificationMediaCleanupStats struct {
	FileCount   int
	TotalBytes  int64
	Deleted     int
	DeleteError error
}

func NewLocalAddressVerificationMediaPublisher(cfg LocalAddressVerificationMediaConfig) (*LocalAddressVerificationMediaPublisher, error) {
	directory, err := validateAddressVerificationMediaDirectory(cfg.Directory)
	if err != nil {
		return nil, err
	}
	publicBaseURL, err := validateAddressVerificationMediaBaseURL(cfg.PublicBaseURL)
	if err != nil {
		return nil, err
	}
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = defaultAddressVerificationMediaTTL
	}
	if ttl < 30*time.Minute || ttl > 24*time.Hour {
		return nil, fmt.Errorf("%w: ttl must be between 30m and 24h", errAddressVerificationMediaConfig)
	}
	cleanupInterval := cfg.CleanupInterval
	if cleanupInterval <= 0 {
		cleanupInterval = defaultAddressVerificationMediaInterval
	}
	maxFiles := cfg.MaxFiles
	if maxFiles <= 0 {
		maxFiles = defaultAddressVerificationMediaMaxFiles
	}
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultAddressVerificationMediaMaxBytes
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	if cfg.CreateDirectory {
		if err := os.MkdirAll(directory, 0750); err != nil {
			return nil, fmt.Errorf("create address verification media directory: %w", err)
		}
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("stat address verification media directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("%w: media directory must be a real directory", errAddressVerificationMediaConfig)
	}

	return &LocalAddressVerificationMediaPublisher{
		directory:       directory,
		publicBaseURL:   publicBaseURL,
		ttl:             ttl,
		cleanupInterval: cleanupInterval,
		tempTTL:         defaultAddressVerificationTempTTL,
		maxFiles:        maxFiles,
		maxBytes:        maxBytes,
		now:             now,
	}, nil
}

func validateAddressVerificationMediaDirectory(raw string) (string, error) {
	directory := filepath.Clean(strings.TrimSpace(raw))
	if directory == "." || !filepath.IsAbs(directory) || directory == string(filepath.Separator) {
		return "", fmt.Errorf("%w: media directory must be a non-root absolute path", errAddressVerificationMediaConfig)
	}
	workingDirectory, err := os.Getwd()
	if err == nil && directory == filepath.Clean(workingDirectory) {
		return "", fmt.Errorf("%w: media directory cannot be the working directory", errAddressVerificationMediaConfig)
	}
	return directory, nil
}

func validateAddressVerificationMediaBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return "", fmt.Errorf("%w: invalid public base URL", errAddressVerificationMediaConfig)
	}
	if parsed.Scheme != "https" || parsed.Host != "autobookbl.site" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/tg/media" {
		return "", fmt.Errorf("%w: public base URL must be https://autobookbl.site/tg/media", errAddressVerificationMediaConfig)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (p *LocalAddressVerificationMediaPublisher) Start(ctx context.Context) error {
	if p == nil {
		return errAddressVerificationMediaConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errAddressVerificationMediaClosed
	}
	if p.started {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := p.cleanupExpiredLocked(p.now()); err != nil {
		return fmt.Errorf("initial address verification media cleanup: %w", err)
	}
	p.started = true
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	go p.runCleanupLoop(p.stop, p.done)
	return nil
}

func (p *LocalAddressVerificationMediaPublisher) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	if !p.started {
		p.mu.Unlock()
		return nil
	}
	close(p.stop)
	done := p.done
	p.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *LocalAddressVerificationMediaPublisher) Publish(ctx context.Context, imageBytes []byte) (string, error) {
	if p == nil {
		return "", errAddressVerificationMediaConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(imageBytes) == 0 {
		return "", fmt.Errorf("%w: image is empty", errAddressVerificationMediaConfig)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", errAddressVerificationMediaClosed
	}
	if !p.started {
		return "", errAddressVerificationMediaNotStarted
	}
	if p.overLimit {
		return "", errAddressVerificationMediaOverLimit
	}

	name, err := randomAddressVerificationMediaName()
	if err != nil {
		return "", err
	}
	tempPath := filepath.Join(p.directory, ".tmp-"+strings.TrimSuffix(name, ".jpg"))
	finalPath := filepath.Join(p.directory, name)
	file, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("create address verification media temp file: %w", err)
	}
	tempKept := false
	defer func() {
		if !tempKept {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := file.Write(imageBytes); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("write address verification media: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("sync address verification media: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close address verification media: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", fmt.Errorf("publish address verification media: %w", err)
	}
	tempKept = true
	if err := os.Chmod(finalPath, 0640); err != nil {
		_ = os.Remove(finalPath)
		return "", fmt.Errorf("set address verification media permissions: %w", err)
	}
	now := p.now()
	if err := os.Chtimes(finalPath, now, now); err != nil {
		_ = os.Remove(finalPath)
		return "", fmt.Errorf("set address verification media mtime: %w", err)
	}

	stats, err := p.directoryStatsLocked()
	if err != nil {
		_ = os.Remove(finalPath)
		return "", err
	}
	if p.exceedsLimit(stats) {
		p.overLimit = true
		_ = os.Remove(finalPath)
		return "", errAddressVerificationMediaOverLimit
	}
	p.overLimit = false
	return p.publicBaseURL + "/" + name, nil
}

func randomAddressVerificationMediaName() (string, error) {
	var token [addressVerificationMediaNameLength / 2]byte
	if _, err := cryptorand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate address verification media name: %w", err)
	}
	return hex.EncodeToString(token[:]) + ".jpg", nil
}

func (p *LocalAddressVerificationMediaPublisher) runCleanupLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(p.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			stats, err := p.cleanupExpiredLocked(p.now())
			p.mu.Unlock()
			if err != nil {
				log.Printf("地址验证媒体清理失败: dir=%s err=%v", p.directory, err)
				continue
			}
			log.Printf("地址验证媒体清理完成: files=%d bytes=%d deleted=%d", stats.FileCount, stats.TotalBytes, stats.Deleted)
		case <-stop:
			return
		}
	}
}

func (p *LocalAddressVerificationMediaPublisher) cleanupExpired(now time.Time) (addressVerificationMediaCleanupStats, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cleanupExpiredLocked(now)
}

func (p *LocalAddressVerificationMediaPublisher) cleanupExpiredLocked(now time.Time) (addressVerificationMediaCleanupStats, error) {
	entries, err := os.ReadDir(p.directory)
	if err != nil {
		return addressVerificationMediaCleanupStats{}, err
	}
	stats := addressVerificationMediaCleanupStats{}
	var firstDeleteErr error
	for _, entry := range entries {
		name := entry.Name()
		isFinal := addressVerificationMediaNamePattern.MatchString(name)
		isTemp := addressVerificationMediaTempPattern.MatchString(name)
		if !isFinal && !isTemp {
			continue
		}
		path := filepath.Join(p.directory, name)
		info, statErr := os.Lstat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			if firstDeleteErr == nil {
				firstDeleteErr = statErr
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		age := p.ttl
		if isTemp {
			age = p.tempTTL
		}
		if !info.ModTime().Before(now.Add(-age)) {
			stats.FileCount++
			stats.TotalBytes += info.Size()
			continue
		}
		if removeErr := os.Remove(path); removeErr != nil {
			if firstDeleteErr == nil {
				firstDeleteErr = removeErr
			}
			continue
		}
		stats.Deleted++
	}

	current, statErr := p.directoryStatsLocked()
	if statErr != nil {
		return stats, statErr
	}
	stats.FileCount = current.FileCount
	stats.TotalBytes = current.TotalBytes
	p.overLimit = p.exceedsLimit(current)
	stats.DeleteError = firstDeleteErr
	if firstDeleteErr != nil {
		return stats, firstDeleteErr
	}
	return stats, nil
}

func (p *LocalAddressVerificationMediaPublisher) directoryStatsLocked() (addressVerificationMediaCleanupStats, error) {
	entries, err := os.ReadDir(p.directory)
	if err != nil {
		return addressVerificationMediaCleanupStats{}, err
	}
	stats := addressVerificationMediaCleanupStats{}
	for _, entry := range entries {
		if !addressVerificationMediaNamePattern.MatchString(entry.Name()) {
			continue
		}
		info, err := os.Lstat(filepath.Join(p.directory, entry.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return stats, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		stats.FileCount++
		stats.TotalBytes += info.Size()
	}
	return stats, nil
}

func (p *LocalAddressVerificationMediaPublisher) exceedsLimit(stats addressVerificationMediaCleanupStats) bool {
	return stats.FileCount > p.maxFiles || stats.TotalBytes > p.maxBytes
}
