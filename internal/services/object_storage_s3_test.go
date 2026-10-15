package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestS3ObjectStorageUploaderUpload(t *testing.T) {
	t.Run("path-style put request with aws4 signature", func(t *testing.T) {
		var (
			gotMethod        string
			gotPath          string
			gotAuthHeader    string
			gotDateHeader    string
			gotPayloadHash   string
			gotContentType   string
			gotRequestBody   string
			gotAuthorization bool
		)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			gotPath = r.URL.EscapedPath()
			gotAuthHeader = strings.TrimSpace(r.Header.Get("Authorization"))
			gotDateHeader = strings.TrimSpace(r.Header.Get("x-amz-date"))
			gotPayloadHash = strings.TrimSpace(r.Header.Get("x-amz-content-sha256"))
			gotContentType = strings.TrimSpace(r.Header.Get("Content-Type"))
			body, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			gotRequestBody = string(body)
			gotAuthorization = strings.HasPrefix(gotAuthHeader, "AWS4-HMAC-SHA256 Credential=AKID/20260220/ap-southeast-1/s3/aws4_request")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		uploader, err := NewS3ObjectStorageUploader(S3ObjectStorageUploaderConfig{
			Endpoint:        server.URL,
			Region:          "ap-southeast-1",
			Bucket:          "cp215-bucket",
			AccessKeyID:     "AKID",
			SecretAccessKey: "SECRET",
			Prefix:          "media",
			HTTPClient:      server.Client(),
			Now: func() time.Time {
				return time.Date(2026, 2, 20, 9, 30, 15, 0, time.UTC)
			},
		})
		if err != nil {
			t.Fatalf("create uploader failed: %v", err)
		}

		location, err := uploader.Upload(context.Background(), ObjectStorageUploadRequest{
			Key:         "chat/100/photo/test.jpg",
			ContentType: "image/jpeg",
			Body:        []byte("cp215-body"),
		})
		if err != nil {
			t.Fatalf("upload failed: %v", err)
		}

		expectedHashRaw := sha256.Sum256([]byte("cp215-body"))
		expectedHash := hex.EncodeToString(expectedHashRaw[:])

		if gotMethod != http.MethodPut {
			t.Fatalf("unexpected method: %s", gotMethod)
		}
		if gotPath != "/cp215-bucket/media/chat/100/photo/test.jpg" {
			t.Fatalf("unexpected path: %s", gotPath)
		}
		if gotDateHeader != "20260220T093015Z" {
			t.Fatalf("unexpected x-amz-date: %s", gotDateHeader)
		}
		if gotPayloadHash != expectedHash {
			t.Fatalf("unexpected payload hash: want=%s got=%s", expectedHash, gotPayloadHash)
		}
		if gotContentType != "image/jpeg" {
			t.Fatalf("unexpected content-type: %s", gotContentType)
		}
		if gotRequestBody != "cp215-body" {
			t.Fatalf("unexpected request body: %s", gotRequestBody)
		}
		if !gotAuthorization {
			t.Fatalf("unexpected authorization header: %s", gotAuthHeader)
		}
		if location != "s3://cp215-bucket/media/chat/100/photo/test.jpg" {
			t.Fatalf("unexpected location: %s", location)
		}
	})
}
