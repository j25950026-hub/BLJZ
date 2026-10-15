package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go-bot/internal/utils"
)

const (
	defaultMaxParamRunes  = 2048
	defaultMaxResultRunes = 1024
)

var sensitiveInlineValuePattern = regexp.MustCompile(`(?i)(token|sig|secret|password|passwd|api[_-]?key|authorization|auth[_-]?token|totp|otp|csrf|cookie|session)=([^&\s"'<>]+)`)

type PrintfLogger interface {
	Printf(format string, v ...any)
}

type HTTPAuditTrail struct {
	logger         PrintfLogger
	maxParamRunes  int
	maxResultRunes int
}

func NewHTTPAuditTrail(logger PrintfLogger) *HTTPAuditTrail {
	if logger == nil {
		logger = log.Default()
	}
	return &HTTPAuditTrail{
		logger:         logger,
		maxParamRunes:  defaultMaxParamRunes,
		maxResultRunes: defaultMaxResultRunes,
	}
}

func (t *HTTPAuditTrail) Wrap(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	if t == nil || t.logger == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		method := "-"
		path := "-"
		if r != nil {
			method = strings.ToUpper(strings.TrimSpace(r.Method))
			if method == "" {
				method = "-"
			}
			if r.URL != nil {
				path = strings.TrimSpace(r.URL.Path)
				if path == "" {
					path = "/"
				}
			}
		}

		sourceIP := requestSourceIP(r)
		params := buildRequestParams(r, t.maxParamRunes)

		recorder := newResponseCaptureWriter(w, t.maxResultRunes)
		defer func() {
			if recovered := recover(); recovered != nil {
				result := buildPanicResult(recorder, recovered, time.Since(start), t.maxResultRunes)
				t.logger.Printf(
					"[api_audit] method=%s path=%s source_ip=%s params=%s result=%s",
					method,
					path,
					sourceIP,
					params,
					result,
				)
				panic(recovered)
			}

			result := buildResult(recorder, time.Since(start), t.maxResultRunes)
			t.logger.Printf(
				"[api_audit] method=%s path=%s source_ip=%s params=%s result=%s",
				method,
				path,
				sourceIP,
				params,
				result,
			)
		}()

		next.ServeHTTP(recorder, r)
	})
}

func buildRequestParams(req *http.Request, maxRunes int) string {
	if req == nil {
		return "-"
	}

	parts := make([]string, 0, 2)
	if req.URL != nil {
		query := strings.TrimSpace(req.URL.RawQuery)
		if query != "" {
			parts = append(parts, "query="+limitText(redactSensitiveQuery(query), maxRunes))
		}
	}
	if body := captureRequestBody(req, maxRunes); body != "" {
		parts = append(parts, "body="+body)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func captureRequestBody(req *http.Request, maxRunes int) string {
	if req == nil || req.Body == nil || maxRunes <= 0 {
		return ""
	}

	captureLimit := int64(maxRunes + 1)
	captured, err := io.ReadAll(io.LimitReader(req.Body, captureLimit))
	req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(captured), req.Body))
	if err != nil {
		return "body_read_error"
	}
	if len(captured) == 0 {
		return ""
	}

	truncated := len(captured) > maxRunes
	previewBytes := captured
	if truncated {
		previewBytes = previewBytes[:maxRunes]
	}
	preview := strings.TrimSpace(string(previewBytes))
	if preview == "" {
		return ""
	}

	contentType := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Type")))
	if strings.Contains(contentType, "application/json") {
		preview = redactSensitiveJSON(preview)
	} else if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		preview = redactSensitiveQuery(preview)
	} else {
		preview = redactSensitiveInlineValues(preview)
	}
	preview = limitText(preview, maxRunes)
	if truncated && !strings.HasSuffix(preview, "...(truncated)") {
		preview += "...(truncated)"
	}
	return preview
}

func requestSourceIP(req *http.Request) string {
	if req == nil {
		return "-"
	}

	candidates := []string{
		req.Header.Get("X-Forwarded-For"),
		req.Header.Get("X-Real-IP"),
		req.RemoteAddr,
	}
	for _, raw := range candidates {
		if ip, ok := parseIPCandidate(raw); ok {
			return ip
		}
	}
	return "-"
}

func parseIPCandidate(raw string) (string, bool) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return "", false
	}
	if comma := strings.Index(candidate, ","); comma >= 0 {
		candidate = strings.TrimSpace(candidate[:comma])
	}
	if candidate == "" {
		return "", false
	}
	if host, _, err := net.SplitHostPort(candidate); err == nil {
		candidate = strings.TrimSpace(host)
	}
	candidate = strings.Trim(candidate, "[]")
	if candidate == "" {
		return "", false
	}
	parsed, err := netip.ParseAddr(candidate)
	if err != nil {
		return "", false
	}
	return parsed.Unmap().String(), true
}

func buildResult(recorder *responseCaptureWriter, duration time.Duration, maxRunes int) string {
	if recorder == nil {
		return "status=0 body=- latency_ms=0"
	}
	return "status=" + strconv.Itoa(recorder.StatusCode()) +
		" body=" + recorder.BodyPreview(maxRunes) +
		" latency_ms=" + strconv.FormatInt(duration.Milliseconds(), 10)
}

func buildPanicResult(recorder *responseCaptureWriter, recovered any, duration time.Duration, maxRunes int) string {
	status := http.StatusInternalServerError
	body := "panic=" + panicValuePreview(recovered, maxRunes)
	if recorder != nil {
		if recorder.statusCode != 0 {
			status = recorder.statusCode
		}
		if capturedBody := recorder.BodyPreview(maxRunes); capturedBody != "-" {
			body = capturedBody + " panic=" + panicValuePreview(recovered, maxRunes)
		}
	}
	return "status=" + strconv.Itoa(status) +
		" body=" + body +
		" latency_ms=" + strconv.FormatInt(duration.Milliseconds(), 10)
}

func panicValuePreview(recovered any, maxRunes int) string {
	panicText := limitText(fmt.Sprint(recovered), maxRunes)
	if panicText == "" {
		return "panic"
	}
	return panicText
}

func limitText(raw string, maxRunes int) string {
	sanitized := sanitize(raw)
	if sanitized == "" {
		return ""
	}
	if maxRunes <= 0 {
		return sanitized
	}
	runes := []rune(sanitized)
	if len(runes) <= maxRunes {
		return sanitized
	}
	return string(runes[:maxRunes]) + "...(truncated)"
}

func sanitize(raw string) string {
	replacer := strings.NewReplacer("\n", "\\n", "\r", "\\r", "\t", " ")
	return strings.TrimSpace(utils.MaskSensitiveText(redactSensitiveInlineValues(replacer.Replace(raw))))
}

func redactSensitiveQuery(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	values, err := url.ParseQuery(trimmed)
	if err != nil {
		return redactSensitiveInlineValues(trimmed)
	}
	for key := range values {
		if isSensitiveAuditKey(key) {
			values[key] = []string{"[REDACTED]"}
		}
	}
	return values.Encode()
}

func redactSensitiveJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(trimmed)); err == nil {
			return redactSensitiveInlineValues(compact.String())
		}
		return redactSensitiveInlineValues(trimmed)
	}
	redacted, changed := redactSensitiveJSONValue(payload)
	if !changed {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(trimmed)); err == nil {
			return compact.String()
		}
		return trimmed
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return redactSensitiveInlineValues(trimmed)
	}
	return string(encoded)
}

func redactSensitiveJSONValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for key, nested := range typed {
			if isSensitiveAuditKey(key) {
				typed[key] = "[REDACTED]"
				changed = true
				continue
			}
			redacted, nestedChanged := redactSensitiveJSONValue(nested)
			typed[key] = redacted
			if nestedChanged {
				changed = true
			}
		}
		return typed, changed
	case []any:
		changed := false
		for i, nested := range typed {
			redacted, nestedChanged := redactSensitiveJSONValue(nested)
			typed[i] = redacted
			if nestedChanged {
				changed = true
			}
		}
		return typed, changed
	default:
		return value, false
	}
}

func redactSensitiveInlineValues(raw string) string {
	return sensitiveInlineValuePattern.ReplaceAllString(raw, "$1=[REDACTED]")
}

func isSensitiveAuditKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if normalized == "" {
		return false
	}
	if normalized == "sig" || normalized == "signature" || normalized == "authorization" {
		return true
	}
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "passwd") ||
		strings.Contains(normalized, "api_key") ||
		strings.Contains(normalized, "totp") ||
		strings.Contains(normalized, "otp") ||
		strings.Contains(normalized, "csrf") ||
		strings.Contains(normalized, "cookie") ||
		strings.Contains(normalized, "session")
}

type responseCaptureWriter struct {
	http.ResponseWriter
	statusCode int
	body       strings.Builder
	bodyRunes  int
	truncated  bool
	maxRunes   int
}

func newResponseCaptureWriter(base http.ResponseWriter, maxRunes int) *responseCaptureWriter {
	return &responseCaptureWriter{ResponseWriter: base, maxRunes: maxRunes}
}

func (w *responseCaptureWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *responseCaptureWriter) WriteHeader(statusCode int) {
	if w.statusCode == 0 {
		w.statusCode = statusCode
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseCaptureWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	if !w.skipBodyCapture() {
		w.captureBody(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseCaptureWriter) skipBodyCapture() bool {
	if w == nil || w.ResponseWriter == nil {
		return true
	}
	contentType := strings.ToLower(strings.TrimSpace(w.Header().Get("Content-Type")))
	if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "audio/") || strings.HasPrefix(contentType, "video/") || strings.Contains(contentType, "application/octet-stream") {
		return true
	}
	cacheControl := strings.ToLower(w.Header().Get("Cache-Control"))
	return strings.Contains(cacheControl, "no-store")
}

func (w *responseCaptureWriter) captureBody(p []byte) {
	if len(p) == 0 || w.maxRunes <= 0 || w.truncated {
		return
	}
	remaining := w.maxRunes - w.bodyRunes
	if remaining <= 0 {
		w.truncated = true
		return
	}
	chunk := []rune(string(p))
	if len(chunk) > remaining {
		w.body.WriteString(string(chunk[:remaining]))
		w.bodyRunes += remaining
		w.truncated = true
		return
	}
	w.body.WriteString(string(chunk))
	w.bodyRunes += len(chunk)
}

func (w *responseCaptureWriter) StatusCode() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

func (w *responseCaptureWriter) BodyPreview(maxRunes int) string {
	body := sanitize(redactSensitiveJSON(w.body.String()))
	if body == "" {
		return "-"
	}
	body = limitText(body, maxRunes)
	if w.truncated && !strings.HasSuffix(body, "...(truncated)") {
		body += "...(truncated)"
	}
	return body
}

func (w *responseCaptureWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseCaptureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacker is not supported")
	}
	return hijacker.Hijack()
}

func (w *responseCaptureWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (w *responseCaptureWriter) CloseNotify() <-chan bool {
	notifier, ok := w.ResponseWriter.(http.CloseNotifier)
	if !ok {
		ch := make(chan bool)
		close(ch)
		return ch
	}
	return notifier.CloseNotify()
}

func (w *responseCaptureWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
