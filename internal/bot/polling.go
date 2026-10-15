package bot

import (
	"context"
	"errors"
	"log"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/system"
)

type telegramUpdateSource interface {
	GetUpdates(tgbotapi.UpdateConfig) ([]tgbotapi.Update, error)
}

type LongPollerOptions struct {
	Now        func() time.Time
	ReleaseID  string
	ReleaseSHA string
}

type LongPoller struct {
	source     telegramUpdateSource
	status     *system.PollStatusStore
	now        func() time.Time
	releaseID  string
	releaseSHA string
}

func NewLongPoller(source telegramUpdateSource, status *system.PollStatusStore, opts LongPollerOptions) *LongPoller {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &LongPoller{
		source:     source,
		status:     status,
		now:        now,
		releaseID:  opts.ReleaseID,
		releaseSHA: opts.ReleaseSHA,
	}
}

func (p *LongPoller) MarkStarted(ctx context.Context) error {
	if p == nil || p.status == nil {
		return errors.New("poll status store is nil")
	}
	_, err := p.status.MarkStarted(ctx, p.now(), p.releaseID, p.releaseSHA)
	return err
}

func (p *LongPoller) Poll(ctx context.Context, config tgbotapi.UpdateConfig) ([]tgbotapi.Update, int, error) {
	if p == nil || p.source == nil {
		return nil, config.Offset, errors.New("telegram update source is nil")
	}
	if p.status == nil {
		return nil, config.Offset, errors.New("poll status store is nil")
	}
	updates, err := p.source.GetUpdates(config)
	at := p.now()
	if err != nil {
		if _, recordErr := p.status.RecordFailure(ctx, system.PollFailure{
			At:    at,
			Class: system.ClassifyPollError(err),
			Error: err.Error(),
		}); recordErr != nil {
			return nil, config.Offset, errors.Join(err, recordErr)
		}
		return nil, config.Offset, err
	}

	nextOffset := config.Offset
	for _, update := range updates {
		if update.UpdateID >= nextOffset {
			nextOffset = update.UpdateID + 1
		}
	}
	if _, err := p.status.RecordSuccess(ctx, system.PollSuccess{
		At:         at,
		NextOffset: nextOffset,
		ReleaseID:  p.releaseID,
		ReleaseSHA: p.releaseSHA,
	}); err != nil {
		return nil, config.Offset, err
	}
	return updates, nextOffset, nil
}

func (p *LongPoller) Readiness(ctx context.Context, now time.Time, policy system.PollReadinessPolicy) system.PollReadiness {
	if p == nil || p.status == nil {
		return system.PollReadiness{Reason: system.PollReadinessReasonMissing}
	}
	return p.status.Readiness(ctx, now, policy)
}

func runLongPollerLoop(
	ctx context.Context,
	poller *LongPoller,
	config tgbotapi.UpdateConfig,
	dispatch func(tgbotapi.Update),
	wait func(context.Context, time.Duration) bool,
) error {
	if poller == nil {
		return errors.New("long poller is nil")
	}
	if dispatch == nil {
		return errors.New("long polling dispatch is nil")
	}
	if wait == nil {
		wait = waitForLongPollRetry
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		updates, nextOffset, err := poller.Poll(ctx, config)
		if err != nil {
			log.Printf("Telegram Long Polling 失败: class=%s error=%s", system.ClassifyPollError(err), system.SanitizeTelegramError(err.Error()))
			log.Printf("Failed to get updates, retrying in 3 seconds...")
			if !wait(ctx, 3*time.Second) {
				return nil
			}
			continue
		}
		config.Offset = nextOffset
		for _, update := range updates {
			dispatch(update)
		}
	}
}

func waitForLongPollRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
