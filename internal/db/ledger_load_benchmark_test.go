package db

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const ledgerLoadGroupCount = 100

type ledgerLoadOptions struct {
	Groups            int
	PerGroup          int
	PerGroupPerMinute int
	Duration          time.Duration
	Concurrency       int
	History           int
	Shards            int
	Pace              bool
}

func TestLedgerLoadOptionsFromEnv(t *testing.T) {
	t.Setenv("LEDGER_LOAD_GROUPS", "12")
	t.Setenv("LEDGER_LOAD_PER_GROUP", "7")
	t.Setenv("LEDGER_LOAD_PER_GROUP_PER_MIN", "20")
	t.Setenv("LEDGER_LOAD_DURATION", "2m")
	t.Setenv("LEDGER_LOAD_CONCURRENCY", "8")
	t.Setenv("LEDGER_LOAD_HISTORY", "123")
	t.Setenv("LEDGER_LOAD_SHARDS", "4")
	t.Setenv("LEDGER_LOAD_PACE", "1")

	opts := ledgerLoadOptionsFromEnv()
	if opts.Groups != 12 || opts.PerGroup != 7 || opts.PerGroupPerMinute != 20 || opts.Duration != 2*time.Minute || opts.Concurrency != 8 || opts.History != 123 || opts.Shards != 4 || !opts.Pace {
		t.Fatalf("load options parse mismatch: %+v", opts)
	}
	if got := opts.totalLoadEntries(); got != 480 {
		t.Fatalf("duration/rate mode total mismatch: got=%d want=480", got)
	}
	if got := opts.paceDelay(2); got != 6*time.Second {
		t.Fatalf("pace delay mismatch: got=%s want=6s", got)
	}
}

func BenchmarkApplyLedgerEntry100Groups(b *testing.B) {
	benchmarkApplyLedgerEntry100Groups(b, false)
}

func BenchmarkApplyLedgerEntryWithReceiptOutbox100Groups(b *testing.B) {
	benchmarkApplyLedgerEntry100Groups(b, true)
}

func benchmarkApplyLedgerEntry100Groups(b *testing.B, withOutbox bool) {
	shards := ledgerLoadEnvInt("LEDGER_BENCH_SHARDS", 1)
	database, err := NewDatabaseWithOptions(filepath.Join(b.TempDir(), "ledger-100-groups-bench.db"), Options{
		TenantShardCount: shards,
	})
	if err != nil {
		b.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	history := ledgerLoadEnvInt("LEDGER_BENCH_HISTORY", 0)
	for i := 0; i < history; i++ {
		if _, err := database.ApplyLedgerEntry(ledgerLoadInput(int64(i + 1))); err != nil {
			b.Fatalf("预置历史账单失败: %v", err)
		}
	}

	var seq atomic.Int64
	var failed atomic.Int64
	b.ReportAllocs()
	b.ReportMetric(float64(ledgerLoadGroupCount), "groups")
	b.ReportMetric(float64(history), "history")
	b.ReportMetric(float64(shards), "shards")
	b.ResetTimer()
	startedAt := time.Now()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := seq.Add(1)
			input := ledgerLoadInput(n + int64(history))
			var err error
			if withOutbox {
				_, err = database.ApplyLedgerEntryWithReceiptOutbox(input, func(result LedgerApplyResult) (*LedgerReceiptOutboxInput, error) {
					return &LedgerReceiptOutboxInput{
						ChatID:          input.ChatID,
						SourceMessageID: input.MessageID,
						EntryID:         result.EntryID,
						UserID:          input.UserID,
						CommandKey:      "ledger",
						Payload:         `{"kind":"bench_receipt"}`,
					}, nil
				})
			} else {
				_, err = database.ApplyLedgerEntry(input)
			}
			if err != nil {
				failed.Add(1)
			}
		}
	})
	elapsed := time.Since(startedAt)
	b.StopTimer()

	if failed.Load() != 0 {
		b.Fatalf("benchmark 写账失败次数: %d", failed.Load())
	}
	if elapsed > 0 {
		b.ReportMetric(float64(seq.Load())/elapsed.Seconds(), "ledger_tps")
	}
}

func TestLedger100GroupsHighFrequencyLoad(t *testing.T) {
	if os.Getenv("RUN_LEDGER_LOAD_TEST") != "1" {
		t.Skip("设置 RUN_LEDGER_LOAD_TEST=1 后执行 100 群高频写账 load test")
	}

	opts := ledgerLoadOptionsFromEnv()

	database, err := NewDatabaseWithOptions(filepath.Join(t.TempDir(), "ledger-100-groups-load.db"), Options{
		TenantShardCount: opts.Shards,
	})
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	for i := 0; i < opts.History; i++ {
		if _, err := database.ApplyLedgerEntry(ledgerLoadInputForGroups(int64(i+1), opts.Groups)); err != nil {
			t.Fatalf("预置历史账单失败: %v", err)
		}
	}

	total := opts.totalLoadEntries()
	durations := make([]time.Duration, 0, total)
	var durationMu sync.Mutex
	var failed atomic.Int64
	sem := make(chan struct{}, opts.Concurrency)
	var scheduleWG sync.WaitGroup
	var applyWG sync.WaitGroup
	startedAt := time.Now()

	perGroup := total / opts.Groups
	for groupIdx := 0; groupIdx < opts.Groups; groupIdx++ {
		groupIdx := groupIdx
		scheduleWG.Add(1)
		go func() {
			defer scheduleWG.Done()
			for itemIdx := 0; itemIdx < perGroup; itemIdx++ {
				if delay := opts.paceDelay(itemIdx); delay > 0 {
					if wait := time.Until(startedAt.Add(delay)); wait > 0 {
						time.Sleep(wait)
					}
				}
				n := int64(groupIdx*perGroup + itemIdx + opts.History + 1)
				sem <- struct{}{}
				applyWG.Add(1)
				go func(seq int64, groupIdx int) {
					defer applyWG.Done()
					defer func() { <-sem }()
					input := ledgerLoadInputForGroup(seq, groupIdx)
					applyStartedAt := time.Now()
					if _, err := database.ApplyLedgerEntryWithReceiptOutbox(input, func(result LedgerApplyResult) (*LedgerReceiptOutboxInput, error) {
						return &LedgerReceiptOutboxInput{
							ChatID:          input.ChatID,
							SourceMessageID: input.MessageID,
							EntryID:         result.EntryID,
							UserID:          input.UserID,
							CommandKey:      "ledger",
							Payload:         `{"kind":"load_receipt"}`,
						}, nil
					}); err != nil {
						failed.Add(1)
						return
					}
					durationMu.Lock()
					durations = append(durations, time.Since(applyStartedAt))
					durationMu.Unlock()
				}(n, groupIdx)
			}
		}()
	}
	scheduleWG.Wait()
	applyWG.Wait()
	elapsed := time.Since(startedAt)

	if failed.Load() != 0 {
		t.Fatalf("load 写账失败次数: %d", failed.Load())
	}
	if len(durations) != total {
		t.Fatalf("写账耗时样本数异常: got=%d want=%d", len(durations), total)
	}
	ledgerCount := ledgerLoadCountRowsAllShards(t, database, `SELECT COUNT(1) FROM ledger_entries`)
	outboxCount := ledgerLoadCountRowsAllShards(t, database, `SELECT COUNT(1) FROM ledger_receipt_outbox`)
	if ledgerCount != opts.History+total || outboxCount != total {
		t.Fatalf("写入行数异常: ledger=%d outbox=%d want_ledger=%d want_outbox=%d", ledgerCount, outboxCount, opts.History+total, total)
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf(
		"ledger_100_groups_load groups=%d per_group=%d total=%d concurrency=%d history=%d shards=%d duration=%s per_group_per_min=%d pace=%t elapsed=%s ledger_tps=%.2f apply_p50=%s apply_p95=%s apply_p99=%s",
		opts.Groups,
		perGroup,
		total,
		opts.Concurrency,
		opts.History,
		opts.Shards,
		opts.Duration,
		opts.PerGroupPerMinute,
		opts.Pace,
		elapsed,
		float64(total)/elapsed.Seconds(),
		ledgerLoadPercentile(durations, 0.50),
		ledgerLoadPercentile(durations, 0.95),
		ledgerLoadPercentile(durations, 0.99),
	)
}

func ledgerLoadInput(seq int64) LedgerEntryInput {
	return ledgerLoadInputForGroups(seq, ledgerLoadGroupCount)
}

func ledgerLoadInputForGroups(seq int64, groups int) LedgerEntryInput {
	if groups <= 0 {
		groups = ledgerLoadGroupCount
	}
	groupIdx := seq % int64(groups)
	return ledgerLoadInputForGroup(seq, int(groupIdx))
}

func ledgerLoadInputForGroup(seq int64, groupIdx int) LedgerEntryInput {
	amount := 100 + float64(seq%100)
	return LedgerEntryInput{
		ChatID:           -1000000000 - int64(groupIdx),
		MessageID:        int(1000000 + seq),
		UserID:           900000 + int64(groupIdx),
		Username:         "load_user",
		Nickname:         "LoadUser",
		EntryType:        "deposit",
		Amount:           amount,
		Currency:         "CNY",
		BaseAmount:       amount,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      amount,
		Note:             "ledger-load",
		CardNo:           "",
		TrackMessageLink: "tg://load/" + strconv.FormatInt(seq, 10),
	}
}

func ledgerLoadOptionsFromEnv() ledgerLoadOptions {
	opts := ledgerLoadOptions{
		Groups:      ledgerLoadEnvInt("LEDGER_LOAD_GROUPS", ledgerLoadGroupCount),
		PerGroup:    ledgerLoadEnvInt("LEDGER_LOAD_PER_GROUP", 50),
		Concurrency: ledgerLoadEnvInt("LEDGER_LOAD_CONCURRENCY", 100),
		History:     ledgerLoadEnvInt("LEDGER_LOAD_HISTORY", 0),
		Shards:      ledgerLoadEnvInt("LEDGER_LOAD_SHARDS", 1),
	}
	opts.PerGroupPerMinute = ledgerLoadEnvInt("LEDGER_LOAD_PER_GROUP_PER_MIN", 0)
	opts.Duration = ledgerLoadEnvDuration("LEDGER_LOAD_DURATION", 0)
	opts.Pace = ledgerLoadEnvBool("LEDGER_LOAD_PACE", false)
	if opts.Groups <= 0 {
		opts.Groups = ledgerLoadGroupCount
	}
	if opts.PerGroup <= 0 {
		opts.PerGroup = 50
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 100
	}
	if opts.History < 0 {
		opts.History = 0
	}
	if opts.Shards <= 0 {
		opts.Shards = 1
	}
	return opts
}

func (o ledgerLoadOptions) paceDelay(itemIdx int) time.Duration {
	interval := o.paceInterval()
	if interval <= 0 || itemIdx <= 0 {
		return 0
	}
	return time.Duration(itemIdx) * interval
}

func (o ledgerLoadOptions) paceInterval() time.Duration {
	if !o.Pace || o.PerGroupPerMinute <= 0 {
		return 0
	}
	return time.Minute / time.Duration(o.PerGroupPerMinute)
}

func (o ledgerLoadOptions) totalLoadEntries() int {
	perGroup := o.PerGroup
	if o.Duration > 0 && o.PerGroupPerMinute > 0 {
		perGroup = int(math.Ceil(o.Duration.Minutes() * float64(o.PerGroupPerMinute)))
		if perGroup <= 0 {
			perGroup = 1
		}
	}
	return o.Groups * perGroup
}

func ledgerLoadEnvInt(key string, fallback int) int {
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

func ledgerLoadEnvBool(key string, fallback bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func ledgerLoadEnvDuration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func ledgerLoadCountRows(t *testing.T, database *Database, query string) int {
	t.Helper()
	var count int
	if err := database.raw.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("统计行数失败: %v", err)
	}
	return count
}

func ledgerLoadCountRowsAllShards(t *testing.T, database *Database, query string) int {
	t.Helper()
	if database == nil || database.tenantRouter == nil {
		return ledgerLoadCountRows(t, database, query)
	}
	total := 0
	for _, shard := range database.tenantRouter.shards {
		if shard == nil {
			continue
		}
		total += ledgerLoadCountRows(t, shard, query)
	}
	return total
}

func ledgerLoadPercentile(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		return values[0]
	}
	idx := int(float64(len(values)-1) * percentile)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return values[idx]
}
