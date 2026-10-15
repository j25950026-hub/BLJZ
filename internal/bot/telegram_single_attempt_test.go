package bot

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"go-bot/internal/system"
)

type singleAttemptTimeoutError struct{}

func (singleAttemptTimeoutError) Error() string {
	return "single-attempt timeout"
}

func (singleAttemptTimeoutError) Timeout() bool {
	return true
}

func (singleAttemptTimeoutError) Temporary() bool {
	return true
}

func TestRateLimitedHTTPClientSingleAttemptDoesNotReplayReplayableRequest(t *testing.T) {
	var calls int
	transport := telegramTimingRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, singleAttemptTimeoutError{}
	})
	retryBase := &http.Client{
		Transport: system.NewRetryPolicyRoundTripper(transport, system.DefaultHTTPRetryPolicySpec()),
	}
	singleAttemptBase := &http.Client{Transport: transport}
	client := &rateLimitedHTTPClient{
		base:              retryBase,
		singleAttemptBase: singleAttemptBase,
	}

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://api.telegram.org/bot-token/sendPhoto",
		bytes.NewReader([]byte("replayable")),
	)
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	_, err = client.singleAttemptHTTPClient().Do(req)
	if err == nil || !errors.Is(err, singleAttemptTimeoutError{}) {
		t.Fatalf("single-attempt request should return transport timeout, got=%v", err)
	}
	if calls != 1 {
		t.Fatalf("single-attempt request count=%d want=1", calls)
	}

	calls = 0
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("normal retry-enabled request should return error")
	}
	if calls != system.DefaultHTTPRetryPolicyMaxAttempts {
		t.Fatalf("normal retry request count=%d want=%d", calls, system.DefaultHTTPRetryPolicyMaxAttempts)
	}
}

func TestSingleAttemptHTTPClientHonorsRequestContext(t *testing.T) {
	transport := telegramTimingRoundTripper(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	client := &rateLimitedHTTPClient{
		singleAttemptBase: &http.Client{Transport: transport},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot-token/sendPhoto", io.Reader(bytes.NewReader([]byte("payload"))))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	_, err = client.singleAttemptHTTPClient().Do(req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request error=%v want context canceled", err)
	}
}
