package system

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestControlPoint353MemoryLeakCheck(t *testing.T) {
	t.Run("控制点 3.5.3 功能验证", func(t *testing.T) {
		snapshots := []MemorySnapshot{
			{CollectedAt: time.Unix(100, 0), HeapInUse: 100, HeapObjects: 1000, NumGC: 1},
			{CollectedAt: time.Unix(130, 0), HeapInUse: 130, HeapObjects: 1300, NumGC: 2},
			{CollectedAt: time.Unix(160, 0), HeapInUse: 175, HeapObjects: 1700, NumGC: 3},
			{CollectedAt: time.Unix(190, 0), HeapInUse: 220, HeapObjects: 2100, NumGC: 4},
		}
		next := 0
		alerts := make([]MemoryLeakAlert, 0, 2)

		monitor := NewMemoryLeakMonitor(MemoryLeakMonitorOptions{
			MinHeapInUseBytes:   100,
			GrowthThreshold:     0.2,
			ConsecutiveGCCycles: 2,
			Probe: func() (MemorySnapshot, error) {
				if next >= len(snapshots) {
					return snapshots[len(snapshots)-1], nil
				}
				snapshot := snapshots[next]
				next++
				return snapshot, nil
			},
			CaptureProfile: func() (string, error) {
				return "heap profile: runtime.mallocgc", nil
			},
			OnAlert: func(alert MemoryLeakAlert) {
				alerts = append(alerts, alert)
			},
		})

		monitor.scanAndAlert()
		monitor.scanAndAlert()
		monitor.scanAndAlert()
		monitor.scanAndAlert()

		if len(alerts) != 1 {
			t.Fatalf("持续增长场景应只触发一次告警: got=%d", len(alerts))
		}
		alert := alerts[0]
		if alert.Previous.HeapInUse != 130 || alert.Current.HeapInUse != 175 {
			t.Fatalf("告警快照错误: prev=%d current=%d", alert.Previous.HeapInUse, alert.Current.HeapInUse)
		}
		if alert.HeapInUseGrowth < 0.2 {
			t.Fatalf("HeapInUse 增长比例错误: %.2f", alert.HeapInUseGrowth)
		}
		if alert.HeapObjectsGrowth < 0.2 {
			t.Fatalf("HeapObjects 增长比例错误: %.2f", alert.HeapObjectsGrowth)
		}
		if alert.ConsecutiveGCCycles != 2 {
			t.Fatalf("GC 连续增长次数错误: %d", alert.ConsecutiveGCCycles)
		}
		if !strings.Contains(alert.ProfileSummary, "heap profile") {
			t.Fatalf("应包含 pprof 摘要: %q", alert.ProfileSummary)
		}
	})
}

func TestDetectMemoryLeakCandidate(t *testing.T) {
	previous := MemorySnapshot{HeapInUse: 100, HeapObjects: 1000, NumGC: 3}
	current := MemorySnapshot{HeapInUse: 135, HeapObjects: 1400, NumGC: 4}
	heapGrowth, objectGrowth, suspicious := detectMemoryLeakCandidate(previous, current, 80, 0.2)
	if !suspicious {
		t.Fatalf("GC 后内存持续增长应判定为风险")
	}
	if heapGrowth < 0.2 || objectGrowth < 0.2 {
		t.Fatalf("增长比例计算错误: heap=%.2f objects=%.2f", heapGrowth, objectGrowth)
	}

	noGCCurrent := MemorySnapshot{HeapInUse: 200, HeapObjects: 2000, NumGC: 3}
	if _, _, low := detectMemoryLeakCandidate(previous, noGCCurrent, 80, 0.2); low {
		t.Fatalf("未经历 GC 的增长不应直接判定泄漏风险")
	}
}

func TestMemoryLeakMonitorStillAlertsWhenProfileCaptureFails(t *testing.T) {
	snapshots := []MemorySnapshot{
		{CollectedAt: time.Unix(100, 0), HeapInUse: 100, HeapObjects: 1000, NumGC: 1},
		{CollectedAt: time.Unix(130, 0), HeapInUse: 130, HeapObjects: 1300, NumGC: 2},
		{CollectedAt: time.Unix(160, 0), HeapInUse: 170, HeapObjects: 1700, NumGC: 3},
	}
	next := 0
	alerts := make([]MemoryLeakAlert, 0, 1)

	monitor := NewMemoryLeakMonitor(MemoryLeakMonitorOptions{
		MinHeapInUseBytes:   100,
		GrowthThreshold:     0.2,
		ConsecutiveGCCycles: 2,
		Probe: func() (MemorySnapshot, error) {
			if next >= len(snapshots) {
				return snapshots[len(snapshots)-1], nil
			}
			snapshot := snapshots[next]
			next++
			return snapshot, nil
		},
		CaptureProfile: func() (string, error) {
			return "", errors.New("capture failed")
		},
		OnAlert: func(alert MemoryLeakAlert) {
			alerts = append(alerts, alert)
		},
	})

	monitor.scanAndAlert()
	monitor.scanAndAlert()
	monitor.scanAndAlert()

	if len(alerts) != 1 {
		t.Fatalf("pprof 采集失败时仍应触发内存泄漏告警: got=%d", len(alerts))
	}
	if alerts[0].ProfileSummary != "" {
		t.Fatalf("采集失败时应返回空摘要: %q", alerts[0].ProfileSummary)
	}
}

func TestSummarizeHeapProfile(t *testing.T) {
	raw := "\n heap profile\n  1: runtime.mallocgc\n\n  2: runtime.newobject\n tail\n"
	got := summarizeHeapProfile(raw, 3)
	if got != "heap profile | 1: runtime.mallocgc | 2: runtime.newobject" {
		t.Fatalf("heap 摘要格式错误: %q", got)
	}
}

func TestSummarizeHeapProfileMaxLinesNotPositive(t *testing.T) {
	if got := summarizeHeapProfile("line-1\nline-2", 0); got != "" {
		t.Fatalf("maxLines=0 时应返回空串: %q", got)
	}
	if got := summarizeHeapProfile("line-1\nline-2", -1); got != "" {
		t.Fatalf("maxLines<0 时应返回空串: %q", got)
	}
}
