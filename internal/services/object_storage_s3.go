package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	defaultS3UploaderRegion  = "us-east-1"
	defaultS3UploaderTimeout = 10 * time.Second
)

type ObjectStorageUploadRequest struct {
	Key         string
	ContentType string
	Body        []byte
}

type ObjectStorageUploader interface {
	Upload(ctx context.Context, req ObjectStorageUploadRequest) (string, error)
}

type S3ObjectStorageUploaderConfig struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Prefix          string
	PublicBaseURL   string
	Timeout         time.Duration
	HTTPClient      *http.Client
	Now             func() time.Time
}

type S3ObjectStorageUploader struct {
	endpointURL     *url.URL
	region          string
	bucket          string
	accessKeyID     string
	secretAccessKey string
	prefix          string
	publicBaseURL   string
	client          *http.Client
	now             func() time.Time
}

func NewS3ObjectStorageUploader(cfg S3ObjectStorageUploaderConfig) (*S3ObjectStorageUploader, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("s3 endpoint is required")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil || strings.TrimSpace(endpointURL.Scheme) == "" || strings.TrimSpace(endpointURL.Host) == "" {
		return nil, fmt.Errorf("invalid s3 endpoint: %q", cfg.Endpoint)
	}
	endpointURL.RawQuery = ""
	endpointURL.Fragment = ""

	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, errors.New("s3 bucket is required")
	}
	accessKeyID := strings.TrimSpace(cfg.AccessKeyID)
	if accessKeyID == "" {
		return nil, errors.New("s3 access key is required")
	}
	secretAccessKey := strings.TrimSpace(cfg.SecretAccessKey)
	if secretAccessKey == "" {
		return nil, errors.New("s3 secret key is required")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = defaultS3UploaderRegion
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultS3UploaderTimeout
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &S3ObjectStorageUploader{
		endpointURL:     endpointURL,
		region:          region,
		bucket:          bucket,
		accessKeyID:     accessKeyID,
		secretAccessKey: secretAccessKey,
		prefix:          normalizeS3Prefix(cfg.Prefix),
		publicBaseURL:   strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/"),
		client:          client,
		now:             now,
	}, nil
}

func (u *S3ObjectStorageUploader) Upload(ctx context.Context, req ObjectStorageUploadRequest) (string, error) {
	if u == nil {
		return "", errors.New("s3 uploader is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	objectKey, err := normalizeS3ObjectKey(req.Key)
	if err != nil {
		return "", err
	}
	if u.prefix != "" {
		objectKey = u.prefix + "/" + objectKey
	}
	targetURL, err := buildS3ObjectURL(u.endpointURL, u.bucket, objectKey)
	if err != nil {
		return "", err
	}
	payload := req.Body
	payloadHash := sha256Hex(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Host = httpReq.URL.Host
	if ct := strings.TrimSpace(req.ContentType); ct != "" {
		httpReq.Header.Set("Content-Type", ct)
	}

	requestTime := u.now().UTC()
	authorization, amzDate, err := u.buildAuthorization(httpReq, payloadHash, requestTime)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("x-amz-content-sha256", payloadHash)
	httpReq.Header.Set("x-amz-date", amzDate)
	httpReq.Header.Set("Authorization", authorization)

	resp, err := u.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("s3 upload failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if u.publicBaseURL != "" {
		return u.publicBaseURL + "/" + escapePathSegments(objectKey), nil
	}
	return fmt.Sprintf("s3://%s/%s", u.bucket, objectKey), nil
}

func (u *S3ObjectStorageUploader) buildAuthorization(req *http.Request, payloadHash string, now time.Time) (string, string, error) {
	if req == nil || req.URL == nil {
		return "", "", errors.New("invalid request for s3 signature")
	}
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	canonicalHeaders := canonicalS3Headers(req.Host, payloadHash, amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		http.MethodPut,
		canonicalPath(req.URL.EscapedPath()),
		canonicalQueryString(req.URL.RawQuery),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, u.region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signature := hex.EncodeToString(signingKeyAWS4(u.secretAccessKey, dateStamp, u.region, "s3", stringToSign))
	authorization := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		u.accessKeyID,
		scope,
		signedHeaders,
		signature,
	)
	return authorization, amzDate, nil
}

func normalizeS3Prefix(raw string) string {
	normalized := strings.TrimSpace(raw)
	normalized = strings.Trim(normalized, "/")
	return normalized
}

func normalizeS3ObjectKey(raw string) (string, error) {
	normalized := strings.TrimSpace(raw)
	normalized = strings.Trim(normalized, "/")
	if normalized == "" {
		return "", errors.New("s3 object key is required")
	}
	if strings.Contains(normalized, "..") {
		return "", errors.New("s3 object key contains invalid path")
	}
	return normalized, nil
}

func buildS3ObjectURL(endpoint *url.URL, bucket, objectKey string) (string, error) {
	if endpoint == nil {
		return "", errors.New("s3 endpoint is required")
	}
	base := strings.TrimRight(endpoint.String(), "/")
	if strings.TrimSpace(base) == "" {
		return "", errors.New("s3 endpoint is required")
	}
	return fmt.Sprintf("%s/%s/%s", base, url.PathEscape(bucket), escapePathSegments(objectKey)), nil
}

func canonicalS3Headers(host, payloadHash, amzDate string) string {
	hostValue := strings.ToLower(strings.TrimSpace(host))
	return strings.Join([]string{
		"host:" + hostValue,
		"x-amz-content-sha256:" + strings.TrimSpace(payloadHash),
		"x-amz-date:" + strings.TrimSpace(amzDate),
	}, "\n") + "\n"
}

func canonicalPath(escapedPath string) string {
	if strings.TrimSpace(escapedPath) == "" {
		return "/"
	}
	if strings.HasPrefix(escapedPath, "/") {
		return escapedPath
	}
	return "/" + escapedPath
}

func canonicalQueryString(rawQuery string) string {
	if strings.TrimSpace(rawQuery) == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil || len(values) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(values))
	for key, items := range values {
		if len(items) == 0 {
			pairs = append(pairs, awsPercentEncode(key)+"=")
			continue
		}
		sort.Strings(items)
		for _, item := range items {
			pairs = append(pairs, awsPercentEncode(key)+"="+awsPercentEncode(item))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func awsPercentEncode(v string) string {
	encoded := url.QueryEscape(v)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func escapePathSegments(raw string) string {
	parts := strings.Split(raw, "/")
	if len(parts) == 0 {
		return ""
	}
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}
	return strings.Join(escaped, "/")
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func signingKeyAWS4(secretAccessKey, date, region, service, stringToSign string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secretAccessKey), date)
	regionKey := hmacSHA256(dateKey, region)
	serviceKey := hmacSHA256(regionKey, service)
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	return hmacSHA256(signingKey, stringToSign)
}

func hmacSHA256(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}
