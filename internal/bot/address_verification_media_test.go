package bot

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func localAddressVerificationMediaTestConfig(dir string) LocalAddressVerificationMediaConfig {
	return LocalAddressVerificationMediaConfig{
		Directory:       dir,
		PublicBaseURL:   "https://autobookbl.site/tg/media",
		TTL:             time.Hour,
		CleanupInterval: time.Hour,
		CreateDirectory: true,
		MaxFiles:        10000,
		MaxBytes:        2 << 30,
	}
}

func TestLocalAddressVerificationMediaRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*LocalAddressVerificationMediaConfig)
	}{
		{
			name: "relative directory",
			mutate: func(cfg *LocalAddressVerificationMediaConfig) {
				cfg.Directory = "telegram-media"
			},
		},
		{
			name: "root directory",
			mutate: func(cfg *LocalAddressVerificationMediaConfig) {
				cfg.Directory = string(filepath.Separator)
			},
		},
		{
			name: "wrong scheme",
			mutate: func(cfg *LocalAddressVerificationMediaConfig) {
				cfg.PublicBaseURL = "http://autobookbl.site/tg/media"
			},
		},
		{
			name: "wrong host",
			mutate: func(cfg *LocalAddressVerificationMediaConfig) {
				cfg.PublicBaseURL = "https://evil.example/tg/media"
			},
		},
		{
			name: "port",
			mutate: func(cfg *LocalAddressVerificationMediaConfig) {
				cfg.PublicBaseURL = "https://autobookbl.site:443/tg/media"
			},
		},
		{
			name: "query",
			mutate: func(cfg *LocalAddressVerificationMediaConfig) {
				cfg.PublicBaseURL = "https://autobookbl.site/tg/media?x=1"
			},
		},
		{
			name: "fragment",
			mutate: func(cfg *LocalAddressVerificationMediaConfig) {
				cfg.PublicBaseURL = "https://autobookbl.site/tg/media#x"
			},
		},
		{
			name: "wrong path",
			mutate: func(cfg *LocalAddressVerificationMediaConfig) {
				cfg.PublicBaseURL = "https://autobookbl.site/tg/webapp"
			},
		},
		{
			name: "short ttl",
			mutate: func(cfg *LocalAddressVerificationMediaConfig) {
				cfg.TTL = 29 * time.Minute
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := localAddressVerificationMediaTestConfig(t.TempDir())
			tc.mutate(&cfg)
			if _, err := NewLocalAddressVerificationMediaPublisher(cfg); err == nil {
				t.Fatal("invalid media configuration should be rejected")
			}
		})
	}
}

func TestLocalAddressVerificationMediaPublishesAtomicJPEG(t *testing.T) {
	dir := t.TempDir()
	publisher, err := NewLocalAddressVerificationMediaPublisher(localAddressVerificationMediaTestConfig(dir))
	if err != nil {
		t.Fatalf("create publisher failed: %v", err)
	}
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("start publisher failed: %v", err)
	}
	t.Cleanup(func() { _ = publisher.Close(context.Background()) })

	imageBytes := []byte("complete-jpeg-payload")
	url, err := publisher.Publish(context.Background(), imageBytes)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if !regexp.MustCompile(`^https://autobookbl\.site/tg/media/[0-9a-f]{32}\.jpg$`).MatchString(url) {
		t.Fatalf("unexpected public URL: %q", url)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read media directory failed: %v", err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".jpg") {
		t.Fatalf("expected exactly one final jpg, entries=%v", entries)
	}
	got, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read published media failed: %v", err)
	}
	if !bytes.Equal(got, imageBytes) {
		t.Fatalf("published bytes differ: got=%q want=%q", got, imageBytes)
	}
	if entries[0].Name()[0] == '.' {
		t.Fatalf("temporary file must not be exposed as final media: %q", entries[0].Name())
	}
}

func TestLocalAddressVerificationMediaLifecycleAndCleanup(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	cfg := localAddressVerificationMediaTestConfig(dir)
	cfg.TTL = time.Hour
	cfg.Now = func() time.Time { return now }
	cfg.CleanupInterval = time.Hour
	publisher, err := NewLocalAddressVerificationMediaPublisher(cfg)
	if err != nil {
		t.Fatalf("create publisher failed: %v", err)
	}

	if _, err := publisher.Publish(context.Background(), []byte("before-start")); !errors.Is(err, errAddressVerificationMediaNotStarted) {
		t.Fatalf("publish before start should return not-started error, got=%v", err)
	}
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("start publisher failed: %v", err)
	}
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("second start should be idempotent: %v", err)
	}

	oldPath := filepath.Join(dir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg")
	boundaryPath := filepath.Join(dir, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.jpg")
	newPath := filepath.Join(dir, "cccccccccccccccccccccccccccccccc.jpg")
	for _, path := range []string{oldPath, boundaryPath, newPath} {
		if err := os.WriteFile(path, []byte(path), 0640); err != nil {
			t.Fatalf("write cleanup fixture failed: %v", err)
		}
	}
	if err := os.Chtimes(oldPath, now.Add(-time.Hour-time.Second), now.Add(-time.Hour-time.Second)); err != nil {
		t.Fatalf("set old mtime failed: %v", err)
	}
	if err := os.Chtimes(boundaryPath, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("set boundary mtime failed: %v", err)
	}
	if err := os.Chtimes(newPath, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("set new mtime failed: %v", err)
	}
	symlinkPath := filepath.Join(dir, "dddddddddddddddddddddddddddddddd.jpg")
	if err := os.Symlink(oldPath, symlinkPath); err != nil {
		t.Fatalf("create symlink fixture failed: %v", err)
	}

	if _, err := publisher.cleanupExpired(now); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expired file should be removed, err=%v", err)
	}
	if _, err := os.Stat(boundaryPath); err != nil {
		t.Fatalf("file at exact TTL boundary should remain, err=%v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("non-expired file should remain, err=%v", err)
	}
	if _, err := os.Lstat(symlinkPath); err != nil {
		t.Fatalf("symlink should remain untouched, err=%v", err)
	}

	if err := publisher.Close(context.Background()); err != nil {
		t.Fatalf("close publisher failed: %v", err)
	}
	if err := publisher.Close(context.Background()); err != nil {
		t.Fatalf("second close should be idempotent: %v", err)
	}
	if _, err := publisher.Publish(context.Background(), []byte("after-close")); !errors.Is(err, errAddressVerificationMediaClosed) {
		t.Fatalf("publish after close should return closed error, got=%v", err)
	}
}

func TestLocalAddressVerificationMediaPublishesUniqueFilesConcurrently(t *testing.T) {
	dir := t.TempDir()
	publisher, err := NewLocalAddressVerificationMediaPublisher(localAddressVerificationMediaTestConfig(dir))
	if err != nil {
		t.Fatalf("create publisher failed: %v", err)
	}
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("start publisher failed: %v", err)
	}
	t.Cleanup(func() { _ = publisher.Close(context.Background()) })

	const count = 16
	urls := make([]string, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			urls[index], errs[index] = publisher.Publish(context.Background(), []byte("payload"))
		}(i)
	}
	wg.Wait()
	seen := make(map[string]struct{}, count)
	for i := range urls {
		if errs[i] != nil {
			t.Fatalf("concurrent publish %d failed: %v", i, errs[i])
		}
		if _, exists := seen[urls[i]]; exists {
			t.Fatalf("duplicate URL: %s", urls[i])
		}
		seen[urls[i]] = struct{}{}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read media directory failed: %v", err)
	}
	if len(entries) != count {
		t.Fatalf("published file count=%d want=%d", len(entries), count)
	}
}
