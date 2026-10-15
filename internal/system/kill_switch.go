package system

import (
	"strings"
	"sync"
	"time"
)

const (
	KillSwitchCategoryManual    = "manual"
	KillSwitchCategorySevereBug = "severe_bug"
	KillSwitchCategoryAttack    = "attack"
	killSwitchCategoryUnknown   = "unknown"
)

type KillSwitchEvent struct {
	Category    string
	Source      string
	Detail      string
	TriggeredAt time.Time
}

type KillSwitchOptions struct {
	Stop      func()
	Logf      func(format string, args ...any)
	OnTrigger func(KillSwitchEvent)
	Now       func() time.Time
}

type KillSwitch struct {
	stop      func()
	logf      func(format string, args ...any)
	onTrigger func(KillSwitchEvent)
	now       func() time.Time

	once sync.Once

	mu        sync.RWMutex
	triggered bool
	event     KillSwitchEvent
}

func NewKillSwitch(opts KillSwitchOptions) *KillSwitch {
	normalized := normalizeKillSwitchOptions(opts)
	return &KillSwitch{
		stop:      normalized.Stop,
		logf:      normalized.Logf,
		onTrigger: normalized.OnTrigger,
		now:       normalized.Now,
	}
}

func normalizeKillSwitchOptions(opts KillSwitchOptions) KillSwitchOptions {
	normalized := opts
	if normalized.Stop == nil {
		normalized.Stop = func() {}
	}
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	if normalized.Now == nil {
		normalized.Now = time.Now
	}
	return normalized
}

func (k *KillSwitch) Trigger(event KillSwitchEvent) bool {
	if k == nil {
		return false
	}

	normalized := normalizeKillSwitchEvent(event, k.now)
	triggered := false
	k.once.Do(func() {
		triggered = true
		k.mu.Lock()
		k.triggered = true
		k.event = normalized
		k.mu.Unlock()

		k.stop()
		k.logf(
			"紧急停机触发: category=%s source=%s detail=%s triggered_at=%s",
			normalized.Category,
			normalized.Source,
			normalized.Detail,
			normalized.TriggeredAt.Format(time.RFC3339),
		)
		if k.onTrigger != nil {
			k.onTrigger(normalized)
		}
	})
	return triggered
}

func (k *KillSwitch) TriggerManual(source, detail string) bool {
	return k.Trigger(KillSwitchEvent{
		Category: KillSwitchCategoryManual,
		Source:   source,
		Detail:   detail,
	})
}

func (k *KillSwitch) TriggerSevereBug(source, detail string) bool {
	return k.Trigger(KillSwitchEvent{
		Category: KillSwitchCategorySevereBug,
		Source:   source,
		Detail:   detail,
	})
}

func (k *KillSwitch) TriggerAttack(source, detail string) bool {
	return k.Trigger(KillSwitchEvent{
		Category: KillSwitchCategoryAttack,
		Source:   source,
		Detail:   detail,
	})
}

func (k *KillSwitch) IsTriggered() bool {
	if k == nil {
		return false
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.triggered
}

func (k *KillSwitch) Snapshot() (KillSwitchEvent, bool) {
	if k == nil {
		return KillSwitchEvent{}, false
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if !k.triggered {
		return KillSwitchEvent{}, false
	}
	return k.event, true
}

func normalizeKillSwitchEvent(event KillSwitchEvent, now func() time.Time) KillSwitchEvent {
	normalized := event
	normalized.Category = strings.ToLower(strings.TrimSpace(normalized.Category))
	switch normalized.Category {
	case KillSwitchCategoryManual, KillSwitchCategorySevereBug, KillSwitchCategoryAttack:
	default:
		normalized.Category = killSwitchCategoryUnknown
	}
	normalized.Source = strings.TrimSpace(normalized.Source)
	if normalized.Source == "" {
		normalized.Source = "unknown"
	}
	normalized.Detail = strings.TrimSpace(normalized.Detail)
	if normalized.Detail == "" {
		normalized.Detail = "n/a"
	}
	if normalized.TriggeredAt.IsZero() {
		normalized.TriggeredAt = now()
	}
	return normalized
}
