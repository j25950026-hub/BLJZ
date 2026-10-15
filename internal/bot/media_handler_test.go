package bot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/services"
)

type capturedMediaObject struct {
	key         string
	contentType string
	payload     []byte
}

type delayedObjectStorageUploader struct {
	delay    time.Duration
	expected int

	mu      sync.Mutex
	objects []capturedMediaObject
	done    chan struct{}
	once    sync.Once
}

func newDelayedObjectStorageUploader(delay time.Duration, expected int) *delayedObjectStorageUploader {
	return &delayedObjectStorageUploader{
		delay:    delay,
		expected: expected,
		done:     make(chan struct{}),
	}
}

func (u *delayedObjectStorageUploader) Upload(ctx context.Context, req services.ObjectStorageUploadRequest) (string, error) {
	if u == nil {
		return "", errors.New("uploader is nil")
	}
	if u.delay > 0 {
		timer := time.NewTimer(u.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
		}
	}

	u.mu.Lock()
	u.objects = append(u.objects, capturedMediaObject{
		key:         req.Key,
		contentType: req.ContentType,
		payload:     append([]byte(nil), req.Body...),
	})
	if len(u.objects) >= u.expected {
		u.once.Do(func() { close(u.done) })
	}
	u.mu.Unlock()
	return "s3://cp215/" + req.Key, nil
}

func (u *delayedObjectStorageUploader) wait(timeout time.Duration) bool {
	if u == nil {
		return false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-u.done:
		return true
	case <-timer.C:
		return false
	}
}

func (u *delayedObjectStorageUploader) snapshot() []capturedMediaObject {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]capturedMediaObject, len(u.objects))
	copy(out, u.objects)
	return out
}

type staticTelegramFileResolver struct {
	baseURL string
}

func (r staticTelegramFileResolver) GetFileDirectURL(fileID string) (string, error) {
	normalized := strings.TrimSpace(fileID)
	if normalized == "" {
		return "", errors.New("file id is empty")
	}
	return strings.TrimRight(r.baseURL, "/") + "/files/" + url.PathEscape(normalized), nil
}

func TestControlPoint215MediaHandler(t *testing.T) {
	t.Run("控制点 2.1.5 功能验证", func(t *testing.T) {
		payloads := map[string][]byte{
			"photo-large":   []byte("cp215-photo"),
			"video-main":    []byte("cp215-video"),
			"document-main": []byte("cp215-document"),
		}
		fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fileID := strings.TrimPrefix(strings.TrimSpace(r.URL.Path), "/files/")
			fileID, _ = url.PathUnescape(fileID)
			payload, ok := payloads[fileID]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("missing"))
				return
			}
			_, _ = w.Write(payload)
		}))
		defer fileServer.Close()

		uploader := newDelayedObjectStorageUploader(120*time.Millisecond, 3)
		handler, err := NewAsyncMediaHandler(
			staticTelegramFileResolver{baseURL: fileServer.URL},
			uploader,
			AsyncMediaHandlerConfig{
				Workers:       1,
				QueueSize:     8,
				MaxFileBytes:  2 * 1024 * 1024,
				KeyPrefix:     "cp215",
				UploadTimeout: time.Second,
				Now: func() time.Time {
					return time.Date(2026, 2, 20, 10, 30, 0, 0, time.UTC)
				},
			},
		)
		if err != nil {
			t.Fatalf("创建多媒体处理器失败: %v", err)
		}
		defer handler.Close(context.Background())

		messages := []*tgbotapi.Message{
			{
				MessageID: 215001,
				Chat:      &tgbotapi.Chat{ID: -100215001, Type: "supergroup"},
				From:      &tgbotapi.User{ID: 2150001},
				Photo: []tgbotapi.PhotoSize{
					{FileID: "photo-small", FileSize: 10},
					{FileID: "photo-large", FileSize: 100},
				},
			},
			{
				MessageID: 215002,
				Chat:      &tgbotapi.Chat{ID: -100215001, Type: "supergroup"},
				From:      &tgbotapi.User{ID: 2150002},
				Video: &tgbotapi.Video{
					FileID:   "video-main",
					MimeType: "video/mp4",
					FileName: "promo.mp4",
				},
			},
			{
				MessageID: 215003,
				Chat:      &tgbotapi.Chat{ID: -100215001, Type: "supergroup"},
				From:      &tgbotapi.User{ID: 2150003},
				Document: &tgbotapi.Document{
					FileID:   "document-main",
					MimeType: "application/pdf",
					FileName: "账单#2.pdf",
				},
			},
		}

		start := time.Now()
		for _, msg := range messages {
			if err := handler.EnqueueMessage(msg); err != nil {
				t.Fatalf("消息入队失败: %v", err)
			}
		}
		enqueueCost := time.Since(start)
		if enqueueCost >= 80*time.Millisecond {
			t.Fatalf("异步入队耗时异常，疑似阻塞上传: cost=%s", enqueueCost)
		}

		if !uploader.wait(3 * time.Second) {
			t.Fatalf("等待异步上传超时")
		}

		objects := uploader.snapshot()
		if len(objects) != 3 {
			t.Fatalf("上传数量错误: want=3 got=%d", len(objects))
		}

		assertContains := func(label, key, expected string) {
			t.Helper()
			if !strings.Contains(key, expected) {
				t.Fatalf("%s key 异常: want_contains=%q got=%q", label, expected, key)
			}
		}

		assertContains("photo", objects[0].key, "/photo/")
		assertContains("video", objects[1].key, "/video/")
		assertContains("document", objects[2].key, "/document/")
		if objects[0].contentType != "image/jpeg" {
			t.Fatalf("photo content-type 错误: %s", objects[0].contentType)
		}
		if objects[1].contentType != "video/mp4" {
			t.Fatalf("video content-type 错误: %s", objects[1].contentType)
		}
		if objects[2].contentType != "application/pdf" {
			t.Fatalf("document content-type 错误: %s", objects[2].contentType)
		}
		if string(objects[0].payload) != "cp215-photo" {
			t.Fatalf("photo payload 异常: %q", string(objects[0].payload))
		}
		if string(objects[1].payload) != "cp215-video" {
			t.Fatalf("video payload 异常: %q", string(objects[1].payload))
		}
		if string(objects[2].payload) != "cp215-document" {
			t.Fatalf("document payload 异常: %q", string(objects[2].payload))
		}
	})
}
