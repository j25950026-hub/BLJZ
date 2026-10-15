package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/services"
)

const (
	defaultMediaHandlerWorkers    = 2
	defaultMediaHandlerQueueSize  = 256
	defaultMediaHandlerMaxBytes   = 32 * 1024 * 1024
	defaultMediaHandlerKeyPrefix  = "telegram-media"
	defaultMediaHandlerTimeout    = 20 * time.Second
	defaultMediaHandlerStopWait   = 5 * time.Second
	defaultPhotoFileName          = "photo.jpg"
	defaultVideoFileName          = "video.mp4"
	defaultDocumentFileName       = "file.bin"
	mediaObjectNameMaxLength      = 96
	mediaObjectTimestampLayoutUTC = "20060102T150405.000000000"
)

var (
	errMediaHandlerBackpressure = errors.New("media handler backpressure")
	errMediaHandlerClosed       = errors.New("media handler closed")
	mediaObjectNameInvalidChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

type MediaHandler interface {
	EnqueueMessage(msg *tgbotapi.Message) error
	Close(ctx context.Context) error
	Usage() (queued, capacity int)
}

type AsyncMediaHandlerConfig struct {
	Workers       int
	QueueSize     int
	MaxFileBytes  int64
	KeyPrefix     string
	UploadTimeout time.Duration
	Now           func() time.Time
}

type asyncMediaHandler struct {
	resolver      telegramFileURLResolver
	uploader      services.ObjectStorageUploader
	maxFileBytes  int64
	keyPrefix     string
	uploadTimeout time.Duration
	now           func() time.Time

	mu     sync.Mutex
	closed bool
	jobs   chan mediaUploadJob
	wg     sync.WaitGroup
}

type noopMediaHandler struct{}

type mediaUploadJob struct {
	ChatID    int64
	MessageID int
	UserID    int64
	MediaType string
	FileID    string
	FileName  string
	MimeType  string
	Received  time.Time
}

func NewAsyncMediaHandler(resolver telegramFileURLResolver, uploader services.ObjectStorageUploader, cfg AsyncMediaHandlerConfig) (MediaHandler, error) {
	if resolver == nil {
		return nil, errors.New("telegram resolver is required")
	}
	if uploader == nil {
		return nil, errors.New("object storage uploader is required")
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = defaultMediaHandlerWorkers
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = defaultMediaHandlerQueueSize
	}
	maxFileBytes := cfg.MaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = defaultMediaHandlerMaxBytes
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	uploadTimeout := cfg.UploadTimeout
	if uploadTimeout <= 0 {
		uploadTimeout = defaultMediaHandlerTimeout
	}
	keyPrefix := strings.Trim(strings.TrimSpace(cfg.KeyPrefix), "/")
	if keyPrefix == "" {
		keyPrefix = defaultMediaHandlerKeyPrefix
	}

	handler := &asyncMediaHandler{
		resolver:      resolver,
		uploader:      uploader,
		maxFileBytes:  maxFileBytes,
		keyPrefix:     keyPrefix,
		uploadTimeout: uploadTimeout,
		now:           now,
		jobs:          make(chan mediaUploadJob, queueSize),
	}
	for i := 0; i < workers; i++ {
		handler.wg.Add(1)
		go handler.runWorker()
	}
	return handler, nil
}

func (h *asyncMediaHandler) EnqueueMessage(msg *tgbotapi.Message) error {
	if h == nil {
		return nil
	}
	job, ok := extractMediaUploadJob(msg)
	if !ok {
		return nil
	}
	if job.Received.IsZero() {
		job.Received = h.now().UTC()
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errMediaHandlerClosed
	}
	select {
	case h.jobs <- job:
		return nil
	default:
		return errMediaHandlerBackpressure
	}
}

func (h *asyncMediaHandler) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	close(h.jobs)
	h.mu.Unlock()

	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *asyncMediaHandler) Usage() (queued, capacity int) {
	if h == nil {
		return 0, 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.jobs), cap(h.jobs)
}

func (h *asyncMediaHandler) runWorker() {
	defer h.wg.Done()
	for job := range h.jobs {
		if err := h.processJob(job); err != nil {
			log.Printf(
				"多媒体处理失败: chat_id=%d message_id=%d user_id=%d media=%s file_id=%s err=%v",
				job.ChatID,
				job.MessageID,
				job.UserID,
				job.MediaType,
				job.FileID,
				err,
			)
		}
	}
}

func (h *asyncMediaHandler) processJob(job mediaUploadJob) error {
	payload, err := downloadTelegramFileByID(h.resolver, job.FileID, h.maxFileBytes)
	if err != nil {
		return err
	}
	objectKey := buildMediaObjectKey(h.keyPrefix, job)
	contentType := normalizeMediaContentType(job.MediaType, job.MimeType, objectKey)

	uploadCtx, cancel := context.WithTimeout(context.Background(), h.uploadTimeout)
	defer cancel()

	location, err := h.uploader.Upload(uploadCtx, services.ObjectStorageUploadRequest{
		Key:         objectKey,
		ContentType: contentType,
		Body:        payload,
	})
	if err != nil {
		return err
	}
	log.Printf(
		"多媒体上传完成: chat_id=%d message_id=%d user_id=%d media=%s size=%d object=%s",
		job.ChatID,
		job.MessageID,
		job.UserID,
		job.MediaType,
		len(payload),
		location,
	)
	return nil
}

func (noopMediaHandler) EnqueueMessage(*tgbotapi.Message) error {
	return nil
}

func (noopMediaHandler) Close(context.Context) error {
	return nil
}

func (noopMediaHandler) Usage() (int, int) {
	return 0, 0
}

func extractMediaUploadJob(msg *tgbotapi.Message) (mediaUploadJob, bool) {
	if msg == nil || msg.Chat == nil {
		return mediaUploadJob{}, false
	}
	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}
	received := time.Time{}
	if msg.Date > 0 {
		received = time.Unix(int64(msg.Date), 0).UTC()
	}

	largestPhoto := selectLargestPhoto(msg.Photo)
	if largestPhoto != nil {
		return mediaUploadJob{
			ChatID:    msg.Chat.ID,
			MessageID: msg.MessageID,
			UserID:    userID,
			MediaType: "photo",
			FileID:    strings.TrimSpace(largestPhoto.FileID),
			FileName:  defaultPhotoFileName,
			MimeType:  "image/jpeg",
			Received:  received,
		}, strings.TrimSpace(largestPhoto.FileID) != ""
	}
	if msg.Video != nil {
		fileID := strings.TrimSpace(msg.Video.FileID)
		if fileID != "" {
			fileName := strings.TrimSpace(msg.Video.FileName)
			if fileName == "" {
				fileName = defaultVideoFileName
			}
			mime := strings.TrimSpace(msg.Video.MimeType)
			if mime == "" {
				mime = "video/mp4"
			}
			return mediaUploadJob{
				ChatID:    msg.Chat.ID,
				MessageID: msg.MessageID,
				UserID:    userID,
				MediaType: "video",
				FileID:    fileID,
				FileName:  fileName,
				MimeType:  mime,
				Received:  received,
			}, true
		}
	}
	if msg.Document != nil {
		fileID := strings.TrimSpace(msg.Document.FileID)
		if fileID != "" {
			fileName := strings.TrimSpace(msg.Document.FileName)
			if fileName == "" {
				fileName = defaultDocumentFileName
			}
			mime := strings.TrimSpace(msg.Document.MimeType)
			if mime == "" {
				mime = "application/octet-stream"
			}
			return mediaUploadJob{
				ChatID:    msg.Chat.ID,
				MessageID: msg.MessageID,
				UserID:    userID,
				MediaType: "document",
				FileID:    fileID,
				FileName:  fileName,
				MimeType:  mime,
				Received:  received,
			}, true
		}
	}
	return mediaUploadJob{}, false
}

func selectLargestPhoto(photos []tgbotapi.PhotoSize) *tgbotapi.PhotoSize {
	if len(photos) == 0 {
		return nil
	}
	index := -1
	for i := range photos {
		if strings.TrimSpace(photos[i].FileID) == "" {
			continue
		}
		if index == -1 || photos[i].FileSize > photos[index].FileSize {
			index = i
		}
	}
	if index < 0 {
		return nil
	}
	return &photos[index]
}

func buildMediaObjectKey(prefix string, job mediaUploadJob) string {
	fileName := normalizeMediaObjectName(job.FileName, job.MediaType, job.MimeType)
	ts := job.Received
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	parts := []string{
		strings.Trim(strings.TrimSpace(prefix), "/"),
		strconv.FormatInt(job.ChatID, 10),
		strconv.Itoa(job.MessageID),
		job.MediaType,
		ts.Format(mediaObjectTimestampLayoutUTC) + "_" + fileName,
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, "/")
}

func normalizeMediaObjectName(rawName, mediaType, mimeType string) string {
	name := strings.TrimSpace(rawName)
	if name == "" {
		switch mediaType {
		case "photo":
			name = defaultPhotoFileName
		case "video":
			name = defaultVideoFileName
		default:
			name = defaultDocumentFileName
		}
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(name)))
	if ext == "" {
		ext = mediaDefaultExtension(mediaType, mimeType)
	}
	base := strings.TrimSpace(strings.TrimSuffix(name, filepath.Ext(name)))
	base = mediaObjectNameInvalidChars.ReplaceAllString(base, "-")
	base = strings.Trim(base, "._-")
	if base == "" {
		base = "blob"
	}
	if len(base) > mediaObjectNameMaxLength {
		base = base[:mediaObjectNameMaxLength]
		base = strings.TrimRight(base, "-_.")
		if base == "" {
			base = "blob"
		}
	}
	if ext == "" {
		return base
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return base + ext
}

func normalizeMediaContentType(mediaType, mimeType, objectKey string) string {
	normalized := strings.TrimSpace(strings.ToLower(mimeType))
	if normalized != "" {
		return normalized
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(objectKey)))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	}
	switch mediaType {
	case "photo":
		return "image/jpeg"
	case "video":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

func mediaDefaultExtension(mediaType, mimeType string) string {
	normalized := strings.TrimSpace(strings.ToLower(mimeType))
	switch normalized {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/x-matroska":
		return ".mkv"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	}
	switch mediaType {
	case "photo":
		return ".jpg"
	case "video":
		return ".mp4"
	default:
		return ".bin"
	}
}

func (a *App) enqueueMediaUpload(msg *tgbotapi.Message) {
	if a == nil || a.mediaHandler == nil || msg == nil {
		return
	}
	if err := a.mediaHandler.EnqueueMessage(msg); err != nil {
		if errors.Is(err, errMediaHandlerBackpressure) {
			queued, capacity := a.mediaHandler.Usage()
			log.Printf(
				"多媒体处理触发背压: chat_id=%d message_id=%d queued=%d capacity=%d",
				msg.Chat.ID,
				msg.MessageID,
				queued,
				capacity,
			)
			return
		}
		if errors.Is(err, errMediaHandlerClosed) {
			return
		}
		log.Printf(
			"多媒体处理入队失败: chat_id=%d message_id=%d err=%v",
			msg.Chat.ID,
			msg.MessageID,
			err,
		)
	}
}

func (a *App) shutdownMediaHandler() {
	if a == nil || a.mediaHandler == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultMediaHandlerStopWait)
	defer cancel()
	if err := a.mediaHandler.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("关闭多媒体处理器失败: %v", err)
	}
}

func (a *App) SetMediaHandler(handler MediaHandler) {
	if a == nil {
		return
	}
	if handler == nil {
		handler = noopMediaHandler{}
	}
	a.mediaHandler = handler
}

func (a *App) EnableMediaHandler(uploader services.ObjectStorageUploader, cfg AsyncMediaHandlerConfig) error {
	if a == nil {
		return errors.New("app is nil")
	}
	handler, err := NewAsyncMediaHandler(a.api, uploader, cfg)
	if err != nil {
		return fmt.Errorf("create media handler: %w", err)
	}
	a.SetMediaHandler(handler)
	return nil
}
