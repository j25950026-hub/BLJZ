package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type fakeAddressVerificationMediaPublisher struct {
	mu       sync.Mutex
	url      string
	err      error
	calls    int
	lastBody []byte
}

func (p *fakeAddressVerificationMediaPublisher) Publish(_ context.Context, imageBytes []byte) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.lastBody = append([]byte(nil), imageBytes...)
	if p.err != nil {
		return "", p.err
	}
	return p.url, nil
}

type capturedTelegramPhotoRequest struct {
	photo       string
	caption     string
	parseMode   string
	replyTo     string
	multipart   bool
	requestBody []byte
}

type recordingTelegramHTTPClient struct {
	mu          sync.Mutex
	requests    []capturedTelegramPhotoRequest
	responseErr error
}

func (c *recordingTelegramHTTPClient) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	item := capturedTelegramPhotoRequest{requestBody: append([]byte(nil), body...)}
	contentType := req.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		item.multipart = true
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, nextErr := reader.NextPart()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				return nil, nextErr
			}
			value, readErr := io.ReadAll(part)
			if readErr != nil {
				return nil, readErr
			}
			switch part.FormName() {
			case "photo":
				item.photo = string(value)
			case "caption":
				item.caption = string(value)
			case "parse_mode":
				item.parseMode = string(value)
			case "reply_to_message_id":
				item.replyTo = string(value)
			}
		}
	} else {
		values, parseErr := url.ParseQuery(string(body))
		if parseErr != nil {
			return nil, parseErr
		}
		item.photo = values.Get("photo")
		item.caption = values.Get("caption")
		item.parseMode = values.Get("parse_mode")
		item.replyTo = values.Get("reply_to_message_id")
	}
	c.mu.Lock()
	c.requests = append(c.requests, item)
	c.mu.Unlock()
	if c.responseErr != nil {
		return nil, c.responseErr
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":990001}}`)),
		Request:    req,
	}, nil
}

func (c *recordingTelegramHTTPClient) snapshot() []capturedTelegramPhotoRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := make([]capturedTelegramPhotoRequest, len(c.requests))
	copy(items, c.requests)
	return items
}

func TestAddressVerificationPhotoUsesPublicURL(t *testing.T) {
	app, _, _, _ := newFourLayerTestAppWithDBPath(t, "address-verification-public-url.db")
	publisher := &fakeAddressVerificationMediaPublisher{url: "https://autobookbl.site/tg/media/0123456789abcdef0123456789abcdef.jpg"}
	telegramClient := &recordingTelegramHTTPClient{}
	app.addressVerificationMediaPublisher = publisher
	app.api.Client = telegramClient

	timing, result, err := app.sendAddressVerificationPhoto(
		-100501,
		8801,
		"地址验证 caption",
		[]byte("dynamic-image-bytes"),
	)
	if err != nil {
		t.Fatalf("URL photo send failed: %v", err)
	}
	if result.mediaMode != "local_url" || !result.mediaURLReady {
		t.Fatalf("unexpected media result: %+v", result)
	}
	if result.sendAttemptCount != 1 || result.telegramResultClass != "success" {
		t.Fatalf("unexpected send result: %+v", result)
	}
	if timing.HTTPResponseWait < 0 {
		t.Fatalf("timing should not be negative: %+v", timing)
	}
	requests := telegramClient.snapshot()
	if len(requests) != 1 {
		t.Fatalf("Telegram request count=%d want=1", len(requests))
	}
	request := requests[0]
	if request.multipart {
		t.Fatal("FileURL photo must use urlencoded request, not multipart")
	}
	if request.photo != publisher.url {
		t.Fatalf("photo URL=%q want=%q", request.photo, publisher.url)
	}
	if request.caption != "地址验证 caption" || request.parseMode != telegramParseModeHTML || request.replyTo != "8801" {
		t.Fatalf("photo metadata mismatch: %+v", request)
	}
	if publisher.calls != 1 || !bytes.Equal(publisher.lastBody, []byte("dynamic-image-bytes")) {
		t.Fatalf("publisher input mismatch: calls=%d body=%q", publisher.calls, publisher.lastBody)
	}
}

func TestAddressVerificationPhotoFallsBackToMultipartWhenPublishingFails(t *testing.T) {
	app, _, _, _ := newFourLayerTestAppWithDBPath(t, "address-verification-public-url-fallback.db")
	publisher := &fakeAddressVerificationMediaPublisher{err: errors.New("media directory is unavailable")}
	telegramClient := &recordingTelegramHTTPClient{}
	app.addressVerificationMediaPublisher = publisher
	app.api.Client = telegramClient

	_, result, err := app.sendAddressVerificationPhoto(-100502, 8802, "fallback caption", []byte("dynamic-image-bytes"))
	if err != nil {
		t.Fatalf("multipart fallback failed: %v", err)
	}
	if result.mediaMode != "multipart_fallback" || result.mediaURLReady {
		t.Fatalf("unexpected fallback result: %+v", result)
	}
	requests := telegramClient.snapshot()
	if len(requests) != 1 || !requests[0].multipart {
		t.Fatalf("fallback should make one multipart request: %+v", requests)
	}
	if publisher.calls != 1 {
		t.Fatalf("publisher calls=%d want=1", publisher.calls)
	}
}

func TestAddressVerificationPhotoDoesNotFallbackAfterURLSendError(t *testing.T) {
	app, _, _, _ := newFourLayerTestAppWithDBPath(t, "address-verification-public-url-unknown.db")
	publisher := &fakeAddressVerificationMediaPublisher{url: "https://autobookbl.site/tg/media/abcdefabcdefabcdefabcdefabcdefabcd.jpg"}
	telegramClient := &recordingTelegramHTTPClient{responseErr: errors.New("response connection closed")}
	app.addressVerificationMediaPublisher = publisher
	app.api.Client = telegramClient

	_, result, err := app.sendAddressVerificationPhoto(-100503, 8803, "unknown caption", []byte("dynamic-image-bytes"))
	if err == nil {
		t.Fatal("URL send error should be returned")
	}
	if result.mediaMode != "local_url" || result.telegramResultClass != "unknown" {
		t.Fatalf("unexpected unknown result: %+v", result)
	}
	if len(telegramClient.snapshot()) != 1 {
		t.Fatalf("unknown URL result must not trigger multipart retry: %+v", telegramClient.snapshot())
	}
}

func TestRedactOutboundDeadLetterPayloadRedactsAddressVerificationMediaURL(t *testing.T) {
	fullURL := "https://autobookbl.site/tg/media/0123456789abcdef0123456789abcdef.jpg"
	payloadBytes, err := json.Marshal(map[string]any{
		"File":     fullURL,
		"Caption":  "keep caption",
		"OtherURL": "https://example.com/keep",
		"Nested":   []any{fullURL},
	})
	if err != nil {
		t.Fatalf("marshal fixture failed: %v", err)
	}
	redacted := redactOutboundDeadLetterPayload(string(payloadBytes))
	if strings.Contains(redacted, fullURL) || strings.Contains(redacted, "/tg/media/0123456789abcdef") {
		t.Fatalf("full media URL leaked: %s", redacted)
	}
	if !strings.Contains(redacted, "keep caption") || !strings.Contains(redacted, "https://example.com/keep") {
		t.Fatalf("unrelated payload fields changed: %s", redacted)
	}
}
