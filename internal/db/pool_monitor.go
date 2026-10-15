package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	DefaultDBPoolMonitorInterval                        = 3 * time.Second
	DefaultDBPoolMonitorUtilizationAlertThreshold       = 0.8
	DefaultDBPoolMonitorWaitCountAlertDelta       int64 = 1
)

type DBPoolStat struct {
	Name               string
	MaxOpenConnections int
	OpenConnections    int
	InUse              int
	Idle               int
	WaitCount          int64
	WaitDuration       time.Duration
}

func (s DBPoolStat) Utilization() float64 {
	if s.MaxOpenConnections <= 0 {
		return 0
	}
	return float64(s.InUse) / float64(s.MaxOpenConnections)
}

type DBPoolAlert struct {
	PoolName             string
	Utilization          float64
	UtilizationRisk      bool
	WaitCount            int64
	WaitCountDelta       int64
	WaitDurationDelta    time.Duration
	MaxOpenConnections   int
	OpenConnections      int
	InUse                int
	Idle                 int
	UtilizationThreshold float64
}

type DBPoolMonitorOptions struct {
	Interval                  time.Duration
	UtilizationAlertThreshold float64
	WaitCountAlertDelta       int64
	Logf                      func(format string, args ...any)
	OnAlert                   func(DBPoolAlert)
}

type DBPoolMonitor struct {
	database                  *Database
	interval                  time.Duration
	utilizationAlertThreshold float64
	waitCountAlertDelta       int64
	logf                      func(format string, args ...any)
	onAlert                   func(DBPoolAlert)
	previous                  map[string]DBPoolStat
}

func NewDBPoolMonitor(database *Database, opts DBPoolMonitorOptions) *DBPoolMonitor {
	normalized := normalizeDBPoolMonitorOptions(opts)
	return &DBPoolMonitor{
		database:                  database,
		interval:                  normalized.Interval,
		utilizationAlertThreshold: normalized.UtilizationAlertThreshold,
		waitCountAlertDelta:       normalized.WaitCountAlertDelta,
		logf:                      normalized.Logf,
		onAlert:                   normalized.OnAlert,
		previous:                  make(map[string]DBPoolStat),
	}
}

func normalizeDBPoolMonitorOptions(opts DBPoolMonitorOptions) DBPoolMonitorOptions {
	normalized := opts
	if normalized.Interval <= 0 {
		normalized.Interval = DefaultDBPoolMonitorInterval
	}
	if normalized.UtilizationAlertThreshold <= 0 {
		normalized.UtilizationAlertThreshold = DefaultDBPoolMonitorUtilizationAlertThreshold
	}
	if normalized.UtilizationAlertThreshold > 1 {
		normalized.UtilizationAlertThreshold = 1
	}
	if normalized.WaitCountAlertDelta <= 0 {
		normalized.WaitCountAlertDelta = DefaultDBPoolMonitorWaitCountAlertDelta
	}
	if normalized.Logf == nil {
		normalized.Logf = func(string, ...any) {}
	}
	return normalized
}

func (m *DBPoolMonitor) Run(ctx context.Context) {
	if m == nil || m.database == nil {
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

func (m *DBPoolMonitor) scanAndAlert() {
	stats := m.database.DBPoolStats()
	for _, stat := range stats {
		previous := m.previous[stat.Name]
		alert, ok := detectDBPoolAlert(stat, previous, m.utilizationAlertThreshold, m.waitCountAlertDelta)
		m.previous[stat.Name] = stat
		if !ok {
			continue
		}

		m.logf(
			"数据库连接池告警: pool=%s utilization=%.0f%% in_use=%d open=%d max_open=%d wait_count_total=%d wait_count_delta=%d wait_duration_delta=%s",
			alert.PoolName,
			alert.Utilization*100,
			alert.InUse,
			alert.OpenConnections,
			alert.MaxOpenConnections,
			alert.WaitCount,
			alert.WaitCountDelta,
			alert.WaitDurationDelta,
		)
		if m.onAlert != nil {
			m.onAlert(alert)
		}
	}
}

func detectDBPoolAlert(current, previous DBPoolStat, utilizationThreshold float64, waitCountAlertDelta int64) (DBPoolAlert, bool) {
	waitCountDelta := current.WaitCount - previous.WaitCount
	if waitCountDelta < 0 {
		waitCountDelta = current.WaitCount
	}

	waitDurationDelta := current.WaitDuration - previous.WaitDuration
	if waitDurationDelta < 0 {
		waitDurationDelta = current.WaitDuration
	}

	utilization := current.Utilization()
	previousUtilization := previous.Utilization()
	utilizationRisk := current.MaxOpenConnections > 0 &&
		utilization >= utilizationThreshold &&
		(previous.MaxOpenConnections <= 0 || previousUtilization < utilizationThreshold)
	waitRisk := waitCountDelta >= waitCountAlertDelta
	if !utilizationRisk && !waitRisk {
		return DBPoolAlert{}, false
	}

	return DBPoolAlert{
		PoolName:             current.Name,
		Utilization:          utilization,
		UtilizationRisk:      utilizationRisk,
		WaitCount:            current.WaitCount,
		WaitCountDelta:       waitCountDelta,
		WaitDurationDelta:    waitDurationDelta,
		MaxOpenConnections:   current.MaxOpenConnections,
		OpenConnections:      current.OpenConnections,
		InUse:                current.InUse,
		Idle:                 current.Idle,
		UtilizationThreshold: utilizationThreshold,
	}, true
}

func (d *Database) DBPoolStats() []DBPoolStat {
	if d == nil {
		return nil
	}
	if d.tenantRouter == nil {
		return []DBPoolStat{dbPoolStatFromRaw("primary", d.raw)}
	}

	stats := make([]DBPoolStat, 0, len(d.tenantRouter.shards))
	for index, shard := range d.tenantRouter.shards {
		if shard == nil {
			continue
		}
		stats = append(stats, dbPoolStatFromRaw(fmt.Sprintf("shard_%02d", index), shard.raw))
	}
	return stats
}

func dbPoolStatFromRaw(name string, raw *sql.DB) DBPoolStat {
	if raw == nil {
		return DBPoolStat{Name: name}
	}
	stat := raw.Stats()
	return DBPoolStat{
		Name:               name,
		MaxOpenConnections: stat.MaxOpenConnections,
		OpenConnections:    stat.OpenConnections,
		InUse:              stat.InUse,
		Idle:               stat.Idle,
		WaitCount:          stat.WaitCount,
		WaitDuration:       stat.WaitDuration,
	}
}
