package system

import (
	"testing"
	"time"
)

func TestControlPoint355KillSwitch(t *testing.T) {
	t.Run("控制点 3.5.5 功能验证", func(t *testing.T) {
		fixedNow := time.Unix(1739966400, 0)
		stopCalls := 0
		events := make([]KillSwitchEvent, 0, 1)
		switcher := NewKillSwitch(KillSwitchOptions{
			Stop: func() {
				stopCalls++
			},
			OnTrigger: func(event KillSwitchEvent) {
				events = append(events, event)
			},
			Now: func() time.Time {
				return fixedNow
			},
		})

		if ok := switcher.TriggerSevereBug("memory_leak_monitor", "heap_inuse 连续增长"); !ok {
			t.Fatalf("首次触发应成功")
		}
		if ok := switcher.TriggerAttack("api_quota_monitor", "remaining=0 failures=99"); ok {
			t.Fatalf("重复触发应被忽略")
		}

		if stopCalls != 1 {
			t.Fatalf("停机回调应只执行一次: got=%d", stopCalls)
		}
		if len(events) != 1 {
			t.Fatalf("触发事件应只记录一次: got=%d", len(events))
		}
		if !switcher.IsTriggered() {
			t.Fatalf("触发后状态应为 true")
		}

		event, ok := switcher.Snapshot()
		if !ok {
			t.Fatalf("触发后应可读取快照")
		}
		if event.Category != KillSwitchCategorySevereBug {
			t.Fatalf("类别错误: %s", event.Category)
		}
		if event.Source != "memory_leak_monitor" {
			t.Fatalf("来源错误: %s", event.Source)
		}
		if event.Detail != "heap_inuse 连续增长" {
			t.Fatalf("原因错误: %s", event.Detail)
		}
		if !event.TriggeredAt.Equal(fixedNow) {
			t.Fatalf("触发时间错误: %s", event.TriggeredAt)
		}
	})
}

func TestNormalizeKillSwitchEventDefaults(t *testing.T) {
	fixedNow := time.Unix(1739966400, 0)
	event := normalizeKillSwitchEvent(KillSwitchEvent{}, func() time.Time {
		return fixedNow
	})
	if event.Category != killSwitchCategoryUnknown {
		t.Fatalf("默认类别错误: %s", event.Category)
	}
	if event.Source != "unknown" {
		t.Fatalf("默认来源错误: %s", event.Source)
	}
	if event.Detail != "n/a" {
		t.Fatalf("默认原因错误: %s", event.Detail)
	}
	if !event.TriggeredAt.Equal(fixedNow) {
		t.Fatalf("默认时间错误: %s", event.TriggeredAt)
	}
}

func TestKillSwitchStopFirstWhenHookPanics(t *testing.T) {
	stopCalls := 0
	switcher := NewKillSwitch(KillSwitchOptions{
		Stop: func() {
			stopCalls++
		},
		OnTrigger: func(KillSwitchEvent) {
			panic("hook panic")
		},
	})

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatalf("应捕获到 hook panic")
		}
		if stopCalls != 1 {
			t.Fatalf("发生 panic 前应已执行停机: got=%d", stopCalls)
		}
	}()

	switcher.TriggerAttack("unit_test", "panic_hook")
}
