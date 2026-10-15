package bot

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/config"
	"go-bot/internal/system"
)

type scriptedTelegramUpdateSource struct {
	updates []tgbotapi.Update
	err     error
	config  tgbotapi.UpdateConfig
}

func (s *scriptedTelegramUpdateSource) GetUpdates(config tgbotapi.UpdateConfig) ([]tgbotapi.Update, error) {
	s.config = config
	return s.updates, s.err
}

func TestLongPollerPollTreatsEmptyResponseAsSuccess(t *testing.T) {
	source := &scriptedTelegramUpdateSource{}
	store := system.NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	poller := NewLongPoller(source, store, LongPollerOptions{
		Now:        func() time.Time { return time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC) },
		ReleaseID:  "release-a",
		ReleaseSHA: "sha-a",
	})

	updates, nextOffset, err := poller.Poll(context.Background(), tgbotapi.UpdateConfig{Offset: 42, Timeout: 30})
	if err != nil {
		t.Fatalf("空响应轮询不应失败: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("空响应 update 数错误: %d", len(updates))
	}
	if nextOffset != 42 {
		t.Fatalf("空响应应保持 offset: want=42 got=%d", nextOffset)
	}
	if source.config.Timeout != 30 || source.config.Offset != 42 {
		t.Fatalf("Telegram 请求配置错误: %#v", source.config)
	}
	status, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("读取轮询状态: %v", err)
	}
	if status.NextOffset != 42 || status.ConsecutiveFailures != 0 || status.ReleaseID != "release-a" {
		t.Fatalf("空成功未正确记录健康状态: %#v", status)
	}
}

func TestLongPollerMarkStartedWritesReleaseIdentity(t *testing.T) {
	startedAt := time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC)
	store := system.NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	poller := NewLongPoller(&scriptedTelegramUpdateSource{}, store, LongPollerOptions{
		Now:        func() time.Time { return startedAt },
		ReleaseID:  "release-a",
		ReleaseSHA: "sha-a",
	})
	if err := poller.MarkStarted(context.Background()); err != nil {
		t.Fatalf("记录轮询启动: %v", err)
	}
	status, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("读取轮询启动状态: %v", err)
	}
	if !status.StartedAt.Equal(startedAt) || status.ReleaseID != "release-a" || status.ReleaseSHA != "sha-a" {
		t.Fatalf("轮询启动状态错误: %#v", status)
	}
}

func TestLongPollerPollAdvancesOffsetFromLargestUpdate(t *testing.T) {
	source := &scriptedTelegramUpdateSource{updates: []tgbotapi.Update{{UpdateID: 102}, {UpdateID: 100}}}
	store := system.NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	poller := NewLongPoller(source, store, LongPollerOptions{Now: time.Now})

	_, nextOffset, err := poller.Poll(context.Background(), tgbotapi.UpdateConfig{Offset: 90, Timeout: 30})
	if err != nil {
		t.Fatalf("轮询失败: %v", err)
	}
	if nextOffset != 103 {
		t.Fatalf("offset 必须取最大 update id 加一: want=103 got=%d", nextOffset)
	}
}

func TestLongPollerPollRecordsClassifiedFailure(t *testing.T) {
	source := &scriptedTelegramUpdateSource{err: errors.New("context canceled")}
	store := system.NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	poller := NewLongPoller(source, store, LongPollerOptions{Now: time.Now})

	_, _, err := poller.Poll(context.Background(), tgbotapi.UpdateConfig{Timeout: 30})
	if err == nil {
		t.Fatal("预期轮询失败")
	}
	status, loadErr := store.Load(context.Background())
	if loadErr != nil {
		t.Fatalf("读取失败状态: %v", loadErr)
	}
	if status.ConsecutiveFailures != 1 || status.LastErrorClass != system.PollErrorClassReleaseRegression {
		t.Fatalf("失败状态分类错误: %#v", status)
	}
}

func TestRunLongPollerLoopDispatchesUpdateThenStopsWithContext(t *testing.T) {
	source := &scriptedTelegramUpdateSource{updates: []tgbotapi.Update{{UpdateID: 7}}}
	store := system.NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	poller := NewLongPoller(source, store, LongPollerOptions{Now: time.Now})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var dispatched []int
	err := runLongPollerLoop(
		ctx,
		poller,
		tgbotapi.UpdateConfig{Timeout: 30},
		func(update tgbotapi.Update) {
			dispatched = append(dispatched, update.UpdateID)
			cancel()
		},
		func(context.Context, time.Duration) bool {
			t.Fatal("成功轮询不应进入重试等待")
			return false
		},
	)
	if err != nil {
		t.Fatalf("上下文取消应正常退出: %v", err)
	}
	if len(dispatched) != 1 || dispatched[0] != 7 {
		t.Fatalf("更新未按预期分发: %#v", dispatched)
	}
}

func TestAppPollReadinessUsesConfiguredPolicy(t *testing.T) {
	startedAt := time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC)
	store := system.NewPollStatusStore(filepath.Join(t.TempDir(), "poll-status.json"))
	if _, err := store.RecordSuccess(context.Background(), system.PollSuccess{At: startedAt.Add(time.Second)}); err != nil {
		t.Fatalf("记录成功轮询: %v", err)
	}
	app := &App{
		cfg: config.Config{
			PollStartupGrace:    90 * time.Second,
			PollMaxSuccessAge:   90 * time.Second,
			PollMinFailureCount: 3,
		},
		longPoller: NewLongPoller(&scriptedTelegramUpdateSource{}, store, LongPollerOptions{}),
	}

	got := app.PollReadiness(startedAt.Add(92 * time.Second))
	if got.Ready || got.Reason != system.PollReadinessReasonStale {
		t.Fatalf("App 必须使用配置的轮询健康策略: %#v", got)
	}
}
