package system

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultHTTPRetryPolicyMaxAttempts    = 3
	DefaultHTTPRetryPolicyTimeoutBackoff = 200 * time.Millisecond
	DefaultHTTPRetryPolicyRateLimitSleep = 2 * time.Second
)

type HTTPRetryPolicySpec struct {
	MaxAttempts    int
	TimeoutBackoff time.Duration
	RateLimitSleep time.Duration
}

func DefaultHTTPRetryPolicySpec() HTTPRetryPolicySpec {
	return HTTPRetryPolicySpec{
		MaxAttempts:    DefaultHTTPRetryPolicyMaxAttempts,
		TimeoutBackoff: DefaultHTTPRetryPolicyTimeoutBackoff,
		RateLimitSleep: DefaultHTTPRetryPolicyRateLimitSleep,
	}
}

func WrapHTTPClientWithRetryPolicy(client *http.Client, spec HTTPRetryPolicySpec) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	if _, ok := client.Transport.(*retryPolicyRoundTripper); ok {
		return client
	}
	cloned := *client
	cloned.Transport = NewRetryPolicyRoundTripper(client.Transport, spec)
	return &cloned
}

func NewRetryPolicyRoundTripper(base http.RoundTripper, spec HTTPRetryPolicySpec) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &retryPolicyRoundTripper{
		base: base,
		spec: normalizeHTTPRetryPolicySpec(spec),
		sleep: func(ctx context.Context, delay time.Duration) error {
			return sleepWithContext(ctx, delay)
		},
		now: time.Now,
	}
}

type retryPolicyRoundTripper struct {
	base  http.RoundTripper
	spec  HTTPRetryPolicySpec
	sleep func(ctx context.Context, delay time.Duration) error
	now   func() time.Time
}

func (t *retryPolicyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	spec := normalizeHTTPRetryPolicySpec(t.spec)
	sleepFn := t.sleep
	if sleepFn == nil {
		sleepFn = sleepWithContext
	}
	nowFn := t.now
	if nowFn == nil {
		nowFn = time.Now
	}

	attempts := spec.MaxAttempts
	if attempts <= 1 || !isRequestReplayable(req) {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		attemptReq, err := cloneRequestForRetryAttempt(req, attempt)
		if err != nil {
			return nil, err
		}
		resp, err := base.RoundTrip(attemptReq)
		if err != nil {
			if attempt == attempts-1 || !isRetryableTimeoutError(err) {
				return nil, err
			}
			backoff := timeoutRetryBackoff(spec.TimeoutBackoff, attempt)
			if waitErr := sleepFn(req.Context(), backoff); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		if resp.StatusCode != http.StatusTooManyRequests || attempt == attempts-1 {
			return resp, nil
		}
		retryAfter := parseRetryAfterDelay(resp.Header.Get("Retry-After"), nowFn(), spec.RateLimitSleep)
		drainAndCloseResponse(resp)
		if waitErr := sleepFn(req.Context(), retryAfter); waitErr != nil {
			return nil, waitErr
		}
	}
	return nil, fmt.Errorf("retry attempts exhausted: attempts=%d", attempts)
}

func normalizeHTTPRetryPolicySpec(spec HTTPRetryPolicySpec) HTTPRetryPolicySpec {
	normalized := DefaultHTTPRetryPolicySpec()
	if spec.MaxAttempts > 0 {
		normalized.MaxAttempts = spec.MaxAttempts
	}
	if spec.TimeoutBackoff > 0 {
		normalized.TimeoutBackoff = spec.TimeoutBackoff
	}
	if spec.RateLimitSleep > 0 {
		normalized.RateLimitSleep = spec.RateLimitSleep
	}
	return normalized
}

func cloneRequestForRetryAttempt(req *http.Request, attempt int) (*http.Request, error) {
	if attempt == 0 {
		return req, nil
	}
	cloned := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		return cloned, nil
	}
	if req.GetBody == nil {
		return nil, errors.New("request body cannot be replayed for retry")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("rebuild request body for retry: %w", err)
	}
	cloned.Body = body
	return cloned, nil
}

func isRequestReplayable(req *http.Request) bool {
	if req == nil {
		return false
	}
	if req.Body == nil || req.Body == http.NoBody {
		return true
	}
	return req.GetBody != nil
}

func isRetryableTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if os.IsTimeout(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func timeoutRetryBackoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = DefaultHTTPRetryPolicyTimeoutBackoff
	}
	if attempt <= 0 {
		return base
	}
	shift := attempt
	if shift > 6 {
		shift = 6
	}
	return base * time.Duration(1<<shift)
}

func parseRetryAfterDelay(raw string, now time.Time, fallback time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = DefaultHTTPRetryPolicyRateLimitSleep
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	seconds, secErr := strconv.ParseInt(value, 10, 64)
	if secErr == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	retryAt, timeErr := http.ParseTime(value)
	if timeErr == nil {
		if now.IsZero() {
			now = time.Now()
		}
		wait := retryAt.Sub(now)
		if wait > 0 {
			return wait
		}
	}
	return fallback
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func drainAndCloseResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
}
