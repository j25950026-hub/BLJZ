package system

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	DefaultDiskSpaceMonitorInterval              = 30 * time.Second
	DefaultDiskSpaceMonitorMinFreeBytes   uint64 = 2 * 1024 * 1024 * 1024
	DefaultDiskSpaceMonitorMinFreePercent        = 10.0
)

type DiskUsageStat struct {
	Path           string
	TotalBytes     uint64
	AvailableBytes uint64
	FreePercent    float64
}

type DiskSpaceAlert struct {
	DiskUsageStat
	MinFreeBytes   uint64
	MinFreePercent float64
	LowByBytes     bool
	LowByPercent   bool
}

type DiskUsageProbe func(path string) (DiskUsageStat, error)

type DiskSpaceMonitorOptions struct {
	Path           string
	Interval       time.Duration
	MinFreeBytes   uint64
	MinFreePercent float64
	Probe          DiskUsageProbe
	Logf           func(format string, args ...any)
	OnAlert        func(DiskSpaceAlert)
	OnRecover      func(DiskUsageStat)
}

type DiskSpaceMonitor struct {
	path           string
	interval       time.Duration
	minFreeBytes   uint64
	minFreePercent float64
	probe          DiskUsageProbe
	logf           func(format string, args ...any)
	onAlert        func(DiskSpaceAlert)
	onRecover      func(DiskUsageStat)
	alerting       bool
}

func NewDiskSpaceMonitor(opts DiskSpaceMonitorOptions) *DiskSpaceMonitor {
	normalized := normalizeDiskSpaceMonitorOptions(opts)
	return &DiskSpaceMonitor{
		path:           normalized.Path,
		interval:       normalized.Interval,
		minFreeBytes:   normalized.MinFreeBytes,
		minFreePercent: normalized.MinFreePercent,
		probe:          normalized.Probe,
		logf:           normalized.Logf,
		onAlert:        normalized.OnAlert,
		onRecover:      normalized.OnRecover,
	}
}

func normalizeDiskSpaceMonitorOptions(opts DiskSpaceMonitorOptions) DiskSpaceMonitorOptions {
	normalized := opts
	normalized.Path = strings.TrimSpace(normalized.Path)
	if normalized.Path == "" {
		normalized.Path = "."
	}
	if normalized.Interval <= 0 {
		normalized.Interval = DefaultDiskSpaceMonitorInterval
	}
	if normalized.MinFreeBytes == 0 {
		normalized.MinFreeBytes = DefaultDiskSpaceMonitorMinFreeBytes
	}
	if normalized.MinFreePercent <= 0 {
		normalized.MinFreePercent = DefaultDiskSpaceMonitorMinFreePercent
	}
	if normalized.MinFreePercent > 100 {
		normalized.MinFreePercent = 100
	}
	if normalized.Probe == nil {
		normalized.Probe = ReadDiskUsage
	}
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	return normalized
}

func (m *DiskSpaceMonitor) Run(ctx context.Context) {
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

func (m *DiskSpaceMonitor) scanAndAlert() {
	stat, err := m.probe(m.path)
	if err != nil {
		m.logf("磁盘空间监控失败: path=%s err=%v", m.path, err)
		return
	}
	stat = normalizeDiskUsageStat(stat)
	if stat.Path == "" {
		stat.Path = m.path
	}

	alert, low := detectDiskSpaceAlert(stat, m.minFreeBytes, m.minFreePercent)
	if low {
		if m.alerting {
			return
		}
		m.alerting = true
		m.logf(
			"磁盘空间预警: path=%s available=%s(%.2f%%) threshold=%s/%.2f%% reason=%s",
			alert.Path,
			formatBytes(alert.AvailableBytes),
			alert.FreePercent,
			formatBytes(alert.MinFreeBytes),
			alert.MinFreePercent,
			alertReason(alert),
		)
		if m.onAlert != nil {
			m.onAlert(alert)
		}
		return
	}

	if m.alerting {
		m.alerting = false
		m.logf(
			"磁盘空间恢复: path=%s available=%s(%.2f%%)",
			stat.Path,
			formatBytes(stat.AvailableBytes),
			stat.FreePercent,
		)
		if m.onRecover != nil {
			m.onRecover(stat)
		}
	}
}

func detectDiskSpaceAlert(stat DiskUsageStat, minFreeBytes uint64, minFreePercent float64) (DiskSpaceAlert, bool) {
	if minFreePercent < 0 {
		minFreePercent = 0
	}
	if minFreePercent > 100 {
		minFreePercent = 100
	}

	stat = normalizeDiskUsageStat(stat)
	lowByBytes := stat.AvailableBytes <= minFreeBytes
	lowByPercent := stat.TotalBytes > 0 && stat.FreePercent <= minFreePercent
	if !lowByBytes && !lowByPercent {
		return DiskSpaceAlert{}, false
	}

	return DiskSpaceAlert{
		DiskUsageStat:  stat,
		MinFreeBytes:   minFreeBytes,
		MinFreePercent: minFreePercent,
		LowByBytes:     lowByBytes,
		LowByPercent:   lowByPercent,
	}, true
}

func ReadDiskUsage(path string) (DiskUsageStat, error) {
	target := strings.TrimSpace(path)
	if target == "" {
		target = "."
	}
	target = filepath.Clean(target)

	var fsStat syscall.Statfs_t
	if err := syscall.Statfs(target, &fsStat); err != nil {
		return DiskUsageStat{}, err
	}
	blockSize := uint64(fsStat.Bsize)
	total := fsStat.Blocks * blockSize
	available := fsStat.Bavail * blockSize
	return DiskUsageStat{
		Path:           target,
		TotalBytes:     total,
		AvailableBytes: available,
		FreePercent:    calculateFreePercent(total, available),
	}, nil
}

func normalizeDiskUsageStat(stat DiskUsageStat) DiskUsageStat {
	if stat.Path != "" {
		stat.Path = filepath.Clean(stat.Path)
	}
	stat.FreePercent = calculateFreePercent(stat.TotalBytes, stat.AvailableBytes)
	return stat
}

func calculateFreePercent(total, available uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(available) * 100 / float64(total)
}

func alertReason(alert DiskSpaceAlert) string {
	reasons := make([]string, 0, 2)
	if alert.LowByBytes {
		reasons = append(reasons, "available_bytes")
	}
	if alert.LowByPercent {
		reasons = append(reasons, "free_percent")
	}
	if len(reasons) == 0 {
		return "unknown"
	}
	return strings.Join(reasons, "+")
}

func formatBytes(bytes uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(bytes)
	unit := units[0]
	for index := 1; index < len(units) && value >= 1024; index++ {
		value /= 1024
		unit = units[index]
	}
	if unit == "B" {
		return fmt.Sprintf("%d%s", bytes, unit)
	}
	return fmt.Sprintf("%.2f%s", value, unit)
}
