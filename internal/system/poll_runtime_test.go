package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPollStatusStoreRecordSuccessResetsFailuresAndPersistsOffset(t *testing.T) {
	store := NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	failedAt := time.Date(2026, time.August, 14, 11, 1, 2, 0, time.UTC)
	if _, err := store.RecordFailure(context.Background(), PollFailure{
		At:    failedAt,
		Class: PollErrorClassReleaseRegression,
		Error: "context canceled",
	}); err != nil {
		t.Fatalf("记录失败轮询: %v", err)
	}

	succeededAt := failedAt.Add(3 * time.Second)
	got, err := store.RecordSuccess(context.Background(), PollSuccess{
		At:         succeededAt,
		NextOffset: 101,
		ReleaseID:  "20260814.1",
		ReleaseSHA: "0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("记录成功轮询: %v", err)
	}
	if !got.LastSuccessfulPollAt.Equal(succeededAt) {
		t.Fatalf("最后成功时间错误: want=%s got=%s", succeededAt, got.LastSuccessfulPollAt)
	}
	if got.ConsecutiveFailures != 0 {
		t.Fatalf("成功后连续失败数必须清零: got=%d", got.ConsecutiveFailures)
	}
	if got.NextOffset != 101 {
		t.Fatalf("下一 update offset 错误: want=101 got=%d", got.NextOffset)
	}
	if got.LastError != "" || got.LastErrorClass != PollErrorClassNone {
		t.Fatalf("成功后不应保留失败原因: %#v", got)
	}

	persisted, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("读取持久化状态: %v", err)
	}
	if persisted != got {
		t.Fatalf("持久化状态不一致: want=%#v got=%#v", got, persisted)
	}
}

func TestPollStatusStoreTracksConsecutiveSuccessfulPolls(t *testing.T) {
	store := NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	first, err := store.RecordSuccess(context.Background(), PollSuccess{At: now, ReleaseID: "release-a", ReleaseSHA: "sha-a"})
	if err != nil {
		t.Fatalf("记录首次成功轮询: %v", err)
	}
	if first.ConsecutiveSuccesses != 1 {
		t.Fatalf("首次成功应计为一次: %#v", first)
	}
	second, err := store.RecordSuccess(context.Background(), PollSuccess{At: now.Add(30 * time.Second), ReleaseID: "release-a", ReleaseSHA: "sha-a"})
	if err != nil {
		t.Fatalf("记录第二次成功轮询: %v", err)
	}
	if second.ConsecutiveSuccesses != 2 {
		t.Fatalf("连续成功计数错误: %#v", second)
	}
	failed, err := store.RecordFailure(context.Background(), PollFailure{At: now.Add(time.Minute), Class: PollErrorClassTransient, Error: "timeout"})
	if err != nil {
		t.Fatalf("记录失败轮询: %v", err)
	}
	if failed.ConsecutiveSuccesses != 0 {
		t.Fatalf("失败后连续成功计数必须清零: %#v", failed)
	}
}

func TestPollStatusStoreMarkStartedPersistsStartupGrace(t *testing.T) {
	store := NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	startedAt := time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC)
	if _, err := store.MarkStarted(context.Background(), startedAt, "release-a", "sha-a"); err != nil {
		t.Fatalf("记录轮询启动: %v", err)
	}
	status, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("读取启动状态: %v", err)
	}
	if !status.StartedAt.Equal(startedAt) || status.ReleaseID != "release-a" {
		t.Fatalf("启动状态错误: %#v", status)
	}
	readiness := store.Readiness(context.Background(), startedAt.Add(30*time.Second), PollReadinessPolicy{
		StartupGrace: 90 * time.Second,
	})
	if !readiness.Ready || readiness.Reason != PollReadinessReasonStarting {
		t.Fatalf("持久化启动宽限应保持 ready: %#v", readiness)
	}
}

func TestEvaluatePollReadinessHonorsStartupGraceAndStaleSuccess(t *testing.T) {
	startedAt := time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC)
	policy := PollReadinessPolicy{
		StartedAt:       startedAt,
		StartupGrace:    90 * time.Second,
		MaxSuccessAge:   90 * time.Second,
		MinFailureCount: 3,
	}

	duringGrace := EvaluatePollReadiness(PollStatus{}, startedAt.Add(30*time.Second), policy)
	if !duringGrace.Ready || duringGrace.Reason != PollReadinessReasonStarting {
		t.Fatalf("启动宽限期应保持 ready: %#v", duringGrace)
	}

	status := PollStatus{
		LastSuccessfulPollAt: startedAt.Add(10 * time.Second),
		LastAttemptAt:        startedAt.Add(10 * time.Second),
		ConsecutiveFailures:  3,
		LastErrorClass:       PollErrorClassReleaseRegression,
		LastError:            "context canceled",
	}
	stillFresh := EvaluatePollReadiness(status, startedAt.Add(100*time.Second), policy)
	if !stillFresh.Ready {
		t.Fatalf("成功轮询尚未陈旧时应保持 ready: %#v", stillFresh)
	}

	stale := EvaluatePollReadiness(status, startedAt.Add(101*time.Second), policy)
	if stale.Ready || stale.Reason != PollReadinessReasonStale {
		t.Fatalf("成功轮询陈旧且失败达到阈值时必须 not ready: %#v", stale)
	}
}

func TestPollStatusStoreRejectsCorruptSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poll-status.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("写入损坏状态: %v", err)
	}
	_, err := NewPollStatusStore(path).Load(context.Background())
	if err == nil {
		t.Fatal("损坏状态必须返回错误")
	}
}

func TestPollStatusStoreReadinessTreatsMissingSnapshotAsUnreadyAfterGrace(t *testing.T) {
	store := NewPollStatusStore(filepath.Join(t.TempDir(), "missing-poll-status.json"))
	startedAt := time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC)
	got := store.Readiness(context.Background(), startedAt.Add(91*time.Second), PollReadinessPolicy{
		StartedAt:    startedAt,
		StartupGrace: 90 * time.Second,
	})
	if got.Ready || got.Reason != PollReadinessReasonMissing {
		t.Fatalf("缺少状态文件且超过启动宽限必须 unready: %#v", got)
	}
}

func TestClassifyPollError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want PollErrorClass
	}{
		{name: "提前取消属于发布回归候选", err: errors.New("context canceled"), want: PollErrorClassReleaseRegression},
		{name: "未知错误保守归类", err: errors.New("unexpected response"), want: PollErrorClassUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyPollError(tt.err); got != tt.want {
				t.Fatalf("错误分类错误: want=%s got=%s", tt.want, got)
			}
		})
	}
}

func TestSanitizeTelegramErrorRedactsBotTokenFromAPIURL(t *testing.T) {
	raw := `Post "https://api.telegram.org/bot123456789:AAExampleSecretToken_1234567890/getUpdates": context deadline exceeded`
	got := SanitizeTelegramError(raw)
	if strings.Contains(got, "AAExampleSecretToken") || strings.Contains(got, "123456789:") {
		t.Fatalf("Telegram Token 不得出现在脱敏错误中: %q", got)
	}
	if !strings.Contains(got, "https://api.telegram.org/bot[REDACTED]/getUpdates") {
		t.Fatalf("API URL 脱敏结果不符合预期: %q", got)
	}
}

func TestPollStatusStoreRecordFailurePersistsSanitizedTelegramError(t *testing.T) {
	store := NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	raw := `Post "https://api.telegram.org/bot123456789:AAExampleSecretToken_1234567890/getUpdates": context deadline exceeded`
	status, err := store.RecordFailure(context.Background(), PollFailure{
		At:    time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC),
		Class: PollErrorClassTransient,
		Error: raw,
	})
	if err != nil {
		t.Fatalf("记录失败轮询: %v", err)
	}
	if strings.Contains(status.LastError, "AAExampleSecretToken") || strings.Contains(status.LastError, "123456789:") {
		t.Fatalf("持久化状态不应保留 Telegram Token: %q", status.LastError)
	}
	persisted, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("读取持久化状态: %v", err)
	}
	if persisted.LastError != status.LastError {
		t.Fatalf("持久化脱敏错误不一致: want=%q got=%q", status.LastError, persisted.LastError)
	}
}
