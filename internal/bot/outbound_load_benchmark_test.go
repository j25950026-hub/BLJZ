package bot

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type outboundBenchOptions struct {
	Workers        int
	Depth          int
	RTT            time.Duration
	Mode           string
	RateLimitEvery int
	Groups         int
	PerGroupPerMin int
}

func TestOutboundBenchOptionsFromEnv(t *testing.T) {
	t.Setenv("OUTBOUND_BENCH_WORKERS", "8")
	t.Setenv("OUTBOUND_BENCH_DEPTH", "4096")
	t.Setenv("OUTBOUND_BENCH_RTT_MS", "120")
	t.Setenv("OUTBOUND_BENCH_MODE", "rate_limit")
	t.Setenv("OUTBOUND_BENCH_RATE_LIMIT_EVERY", "5")
	t.Setenv("OUTBOUND_BENCH_GROUPS", "200")
	t.Setenv("OUTBOUND_BENCH_PER_GROUP_PER_MIN", "12")

	opts := outboundBenchOptionsFromEnv()
	if opts.Workers != 8 || opts.Depth != 4096 || opts.RTT != 120*time.Millisecond || opts.Mode != "rate_limit" || opts.RateLimitEvery != 5 || opts.Groups != 200 || opts.PerGroupPerMin != 12 {
		t.Fatalf("outbound bench options parse mismatch: %+v", opts)
	}
	t.Setenv("OUTBOUND_BENCH_GROUPS", "0")
	t.Setenv("OUTBOUND_BENCH_PER_GROUP_PER_MIN", "-1")
	opts = outboundBenchOptionsFromEnv()
	if opts.Groups != 100 || opts.PerGroupPerMin != 10 {
		t.Fatalf("outbound bench load shape fallback mismatch: %+v", opts)
	}
	if err := outboundBenchResultError(opts, 4); err != nil {
		t.Fatalf("rate limit every=5 不应在第 4 次触发: %v", err)
	}
	err := outboundBenchResultError(opts, 5)
	var tgErr *tgbotapi.Error
	if !errors.As(err, &tgErr) || tgErr.Code != 429 || tgErr.RetryAfter != 2 {
		t.Fatalf("rate limit every=5 第 5 次应生成 retry_after=2 的 429: %#v", err)
	}
}

func BenchmarkOutboundDispatch(b *testing.B) {
	opts := outboundBenchOptionsFromEnv()
	queue := newPriorityDispatchQueueWithWorkers(opts.Depth, opts.Workers)
	defer closePriorityQueueForBenchmark(b, queue)

	var seq atomic.Int64
	var failed atomic.Int64
	var rateLimited atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	startedAt := time.Now()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := seq.Add(1)
			priority := outboundPriorityBookkeepingReceipt
			if n%10 == 0 {
				priority = outboundPriorityBroadcastAd
			}
			if err := queue.Submit(context.Background(), priority, func(ctx context.Context) error {
				if err := outboundBenchWait(ctx, opts.RTT); err != nil {
					return err
				}
				return outboundBenchResultError(opts, n)
			}); err != nil {
				var tgErr *tgbotapi.Error
				if errors.As(err, &tgErr) && tgErr.Code == 429 {
					rateLimited.Add(1)
				} else {
					failed.Add(1)
				}
			}
		}
	})
	elapsed := time.Since(startedAt)
	b.StopTimer()
	b.ReportMetric(float64(opts.Workers), "workers")
	b.ReportMetric(float64(opts.Depth), "queue_depth")
	b.ReportMetric(float64(opts.RTT.Milliseconds()), "rtt_ms")
	b.ReportMetric(float64(opts.RateLimitEvery), "rate_limit_every")
	b.ReportMetric(float64(opts.Groups), "groups")
	b.ReportMetric(float64(opts.PerGroupPerMin), "per_group_per_min")
	b.ReportMetric(float64(opts.Groups*opts.PerGroupPerMin), "offered_per_min")

	if failed.Load() != 0 {
		b.Fatalf("出站 dispatch 失败次数: %d", failed.Load())
	}
	if elapsed > 0 {
		b.ReportMetric(float64(seq.Load())/elapsed.Seconds(), "dispatch_tps")
	}
	b.ReportMetric(float64(rateLimited.Load()), "rate_limited")
}

func closePriorityQueueForBenchmark(b *testing.B, queue *priorityDispatchQueue) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := queue.Close(ctx); err != nil {
		b.Fatalf("关闭出站队列失败: %v", err)
	}
}

func outboundBenchEnvInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func outboundBenchOptionsFromEnv() outboundBenchOptions {
	opts := outboundBenchOptions{
		Workers:        outboundBenchEnvInt("OUTBOUND_BENCH_WORKERS", outboundQueueWorkers),
		Depth:          outboundBenchEnvInt("OUTBOUND_BENCH_DEPTH", outboundQueueDepth),
		RTT:            time.Duration(outboundBenchEnvInt("OUTBOUND_BENCH_RTT_MS", 0)) * time.Millisecond,
		Mode:           outboundBenchEnvString("OUTBOUND_BENCH_MODE", "success"),
		RateLimitEvery: outboundBenchEnvInt("OUTBOUND_BENCH_RATE_LIMIT_EVERY", 0),
		Groups:         outboundBenchEnvInt("OUTBOUND_BENCH_GROUPS", 100),
		PerGroupPerMin: outboundBenchEnvInt("OUTBOUND_BENCH_PER_GROUP_PER_MIN", 10),
	}
	if opts.Workers <= 0 {
		opts.Workers = outboundQueueWorkers
	}
	if opts.Depth <= 0 {
		opts.Depth = outboundQueueDepth
	}
	if opts.RateLimitEvery < 0 {
		opts.RateLimitEvery = 0
	}
	if opts.Groups <= 0 {
		opts.Groups = 100
	}
	if opts.PerGroupPerMin <= 0 {
		opts.PerGroupPerMin = 10
	}
	switch opts.Mode {
	case "success", "rate_limit", "error":
	default:
		opts.Mode = "success"
	}
	return opts
}

func outboundBenchEnvString(key, fallback string) string {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	return raw
}

func outboundBenchWait(ctx context.Context, rtt time.Duration) error {
	if rtt <= 0 {
		return nil
	}
	timer := time.NewTimer(rtt)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func outboundBenchResultError(opts outboundBenchOptions, seq int64) error {
	switch opts.Mode {
	case "rate_limit":
		if opts.RateLimitEvery <= 0 || seq%int64(opts.RateLimitEvery) != 0 {
			return nil
		}
		return &tgbotapi.Error{
			Code:    429,
			Message: "Too Many Requests",
			ResponseParameters: tgbotapi.ResponseParameters{
				RetryAfter: 2,
			},
		}
	case "error":
		return errors.New("outbound benchmark send error")
	default:
		return nil
	}
}
