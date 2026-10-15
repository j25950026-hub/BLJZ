package system

import (
	"bytes"
	"context"
	"runtime"
	runtimepprof "runtime/pprof"
	"strings"
	"time"
)

const (
	DefaultMemoryLeakMonitorInterval                           = 30 * time.Second
	DefaultMemoryLeakMonitorMinHeapInUseBytes           uint64 = 128 * 1024 * 1024
	DefaultMemoryLeakMonitorGrowthThreshold                    = 0.2
	DefaultMemoryLeakMonitorConsecutiveGCCycles                = 3
	defaultMemoryLeakMonitorHeapProfileSummaryLineLimit        = 6
)

type MemorySnapshot struct {
	CollectedAt  time.Time
	HeapAlloc    uint64
	HeapInUse    uint64
	HeapObjects  uint64
	NumGC        uint32
	LastGCPause  time.Duration
	TotalGCPause time.Duration
}

type MemoryLeakAlert struct {
	Previous            MemorySnapshot
	Current             MemorySnapshot
	HeapInUseGrowth     float64
	HeapObjectsGrowth   float64
	MinHeapInUseBytes   uint64
	GrowthThreshold     float64
	ConsecutiveGCCycles int
	ProfileSummary      string
}

type MemoryStatsProbe func() (MemorySnapshot, error)
type HeapProfileCapture func() (string, error)

type MemoryLeakMonitorOptions struct {
	Interval            time.Duration
	MinHeapInUseBytes   uint64
	GrowthThreshold     float64
	ConsecutiveGCCycles int
	Probe               MemoryStatsProbe
	CaptureProfile      HeapProfileCapture
	Logf                func(format string, args ...any)
	OnAlert             func(MemoryLeakAlert)
}

type MemoryLeakMonitor struct {
	interval            time.Duration
	minHeapInUseBytes   uint64
	growthThreshold     float64
	consecutiveGCCycles int
	probe               MemoryStatsProbe
	captureProfile      HeapProfileCapture
	logf                func(format string, args ...any)
	onAlert             func(MemoryLeakAlert)

	previous        MemorySnapshot
	hasPrevious     bool
	growthStreak    int
	alerting        bool
	lastAlertSample MemorySnapshot
}

func NewMemoryLeakMonitor(opts MemoryLeakMonitorOptions) *MemoryLeakMonitor {
	normalized := normalizeMemoryLeakMonitorOptions(opts)
	return &MemoryLeakMonitor{
		interval:            normalized.Interval,
		minHeapInUseBytes:   normalized.MinHeapInUseBytes,
		growthThreshold:     normalized.GrowthThreshold,
		consecutiveGCCycles: normalized.ConsecutiveGCCycles,
		probe:               normalized.Probe,
		captureProfile:      normalized.CaptureProfile,
		logf:                normalized.Logf,
		onAlert:             normalized.OnAlert,
	}
}

func normalizeMemoryLeakMonitorOptions(opts MemoryLeakMonitorOptions) MemoryLeakMonitorOptions {
	normalized := opts
	if normalized.Interval <= 0 {
		normalized.Interval = DefaultMemoryLeakMonitorInterval
	}
	if normalized.MinHeapInUseBytes == 0 {
		normalized.MinHeapInUseBytes = DefaultMemoryLeakMonitorMinHeapInUseBytes
	}
	if normalized.GrowthThreshold <= 0 {
		normalized.GrowthThreshold = DefaultMemoryLeakMonitorGrowthThreshold
	}
	if normalized.GrowthThreshold > 1 {
		normalized.GrowthThreshold = 1
	}
	if normalized.ConsecutiveGCCycles <= 0 {
		normalized.ConsecutiveGCCycles = DefaultMemoryLeakMonitorConsecutiveGCCycles
	}
	if normalized.Probe == nil {
		normalized.Probe = ReadRuntimeMemorySnapshot
	}
	if normalized.CaptureProfile == nil {
		normalized.CaptureProfile = func() (string, error) {
			return CaptureHeapProfileSummary(defaultMemoryLeakMonitorHeapProfileSummaryLineLimit)
		}
	}
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	return normalized
}

func (m *MemoryLeakMonitor) Run(ctx context.Context) {
	if m == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.scanAndAlert()
		}
	}
}

func (m *MemoryLeakMonitor) scanAndAlert() {
	snapshot, err := m.probe()
	if err != nil {
		m.logf("内存泄漏监控采样失败: err=%v", err)
		return
	}
	if snapshot.CollectedAt.IsZero() {
		snapshot.CollectedAt = time.Now()
	}
	if !m.hasPrevious {
		m.previous = snapshot
		m.hasPrevious = true
		return
	}

	previous := m.previous
	heapGrowth, objectGrowth, suspicious := detectMemoryLeakCandidate(previous, snapshot, m.minHeapInUseBytes, m.growthThreshold)
	m.previous = snapshot
	if !suspicious {
		m.growthStreak = 0
		if m.alerting && snapshot.HeapInUse < m.lastAlertSample.HeapInUse {
			m.alerting = false
			m.logf(
				"内存泄漏风险恢复: heap_inuse=%s heap_objects=%d gc_total=%d",
				formatBytes(snapshot.HeapInUse),
				snapshot.HeapObjects,
				snapshot.NumGC,
			)
		}
		return
	}

	m.growthStreak++
	if m.growthStreak < m.consecutiveGCCycles || m.alerting {
		return
	}

	profileSummary, profileErr := m.captureProfile()
	if profileErr != nil {
		m.logf("内存泄漏监控读取 heap pprof 失败: err=%v", profileErr)
		profileSummary = ""
	}
	alert := MemoryLeakAlert{
		Previous:            previous,
		Current:             snapshot,
		HeapInUseGrowth:     heapGrowth,
		HeapObjectsGrowth:   objectGrowth,
		MinHeapInUseBytes:   m.minHeapInUseBytes,
		GrowthThreshold:     m.growthThreshold,
		ConsecutiveGCCycles: m.growthStreak,
		ProfileSummary:      profileSummary,
	}
	m.alerting = true
	m.lastAlertSample = snapshot
	m.logf(
		"内存泄漏疑似告警: heap_inuse=%s growth=%.1f%% heap_objects=%d growth=%.1f%% gc_total=%d streak=%d threshold=%.0f%% min_heap_inuse=%s pprof=\"%s\"",
		formatBytes(alert.Current.HeapInUse),
		alert.HeapInUseGrowth*100,
		alert.Current.HeapObjects,
		alert.HeapObjectsGrowth*100,
		alert.Current.NumGC,
		alert.ConsecutiveGCCycles,
		alert.GrowthThreshold*100,
		formatBytes(alert.MinHeapInUseBytes),
		alert.ProfileSummary,
	)
	if m.onAlert != nil {
		m.onAlert(alert)
	}
}

func detectMemoryLeakCandidate(previous, current MemorySnapshot, minHeapInUseBytes uint64, growthThreshold float64) (float64, float64, bool) {
	if growthThreshold < 0 {
		growthThreshold = 0
	}
	if current.NumGC <= previous.NumGC {
		return 0, 0, false
	}
	if current.HeapInUse < minHeapInUseBytes {
		return 0, 0, false
	}

	heapGrowth := growthRatio(previous.HeapInUse, current.HeapInUse)
	objectGrowth := growthRatio(previous.HeapObjects, current.HeapObjects)
	suspicious := heapGrowth >= growthThreshold && objectGrowth >= growthThreshold
	return heapGrowth, objectGrowth, suspicious
}

func growthRatio(previous, current uint64) float64 {
	if current <= previous {
		return 0
	}
	if previous == 0 {
		return 1
	}
	return float64(current-previous) / float64(previous)
}

func ReadRuntimeMemorySnapshot() (MemorySnapshot, error) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	lastPause := time.Duration(0)
	if stats.NumGC > 0 {
		lastPause = time.Duration(stats.PauseNs[(stats.NumGC-1)%uint32(len(stats.PauseNs))])
	}
	return MemorySnapshot{
		CollectedAt:  time.Now(),
		HeapAlloc:    stats.HeapAlloc,
		HeapInUse:    stats.HeapInuse,
		HeapObjects:  stats.HeapObjects,
		NumGC:        stats.NumGC,
		LastGCPause:  lastPause,
		TotalGCPause: time.Duration(stats.PauseTotalNs),
	}, nil
}

func CaptureHeapProfileSummary(maxLines int) (string, error) {
	if maxLines <= 0 {
		maxLines = defaultMemoryLeakMonitorHeapProfileSummaryLineLimit
	}
	profile := runtimepprof.Lookup("heap")
	if profile == nil {
		return "", nil
	}
	var buffer bytes.Buffer
	if err := profile.WriteTo(&buffer, 1); err != nil {
		return "", err
	}
	return summarizeHeapProfile(buffer.String(), maxLines), nil
}

func summarizeHeapProfile(raw string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(raw, "\n")
	summary := make([]string, 0, maxLines)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		summary = append(summary, trimmed)
		if len(summary) >= maxLines {
			break
		}
	}
	return strings.Join(summary, " | ")
}
