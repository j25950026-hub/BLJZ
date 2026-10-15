package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlPoint351DBPoolMonitor(t *testing.T) {
	t.Run("控制点 3.5.1 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "db-pool-monitor.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer func() {
			if closeErr := database.Close(); closeErr != nil {
				t.Fatalf("关闭数据库失败: %v", closeErr)
			}
		}()

		alertCh := make(chan DBPoolAlert, 64)
		monitorCtx, cancelMonitor := context.WithCancel(context.Background())
		defer cancelMonitor()
		monitor := NewDBPoolMonitor(database, DBPoolMonitorOptions{
			Interval:                  20 * time.Millisecond,
			UtilizationAlertThreshold: 0.8,
			WaitCountAlertDelta:       1,
			OnAlert: func(alert DBPoolAlert) {
				select {
				case alertCh <- alert:
				default:
				}
			},
		})
		go monitor.Run(monitorCtx)

		tx, err := database.raw.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("开启事务失败: %v", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		blockErrCh := make(chan error, 1)
		go func() {
			waitCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			var value int
			blockErrCh <- database.raw.QueryRowContext(waitCtx, "SELECT 1").Scan(&value)
		}()

		blockErr := <-blockErrCh
		if blockErr == nil {
			t.Fatalf("连接池耗尽场景应触发等待超时")
		}
		if !errors.Is(blockErr, context.DeadlineExceeded) && !strings.Contains(blockErr.Error(), "context deadline exceeded") {
			t.Fatalf("等待超时错误不符合预期: %v", blockErr)
		}

		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		var matched DBPoolAlert
		for {
			select {
			case alert := <-alertCh:
				if alert.WaitCountDelta >= 1 {
					matched = alert
					goto ALERT_FOUND
				}
			case <-deadline.C:
				stats := database.DBPoolStats()
				t.Fatalf("未捕获 WaitCount 增长告警: stats=%+v", stats)
			}
		}

	ALERT_FOUND:
		if matched.PoolName != "primary" {
			t.Fatalf("告警池名称错误: %s", matched.PoolName)
		}
		if matched.MaxOpenConnections != 1 {
			t.Fatalf("最大连接数异常: %d", matched.MaxOpenConnections)
		}
		if matched.WaitCountDelta < 1 {
			t.Fatalf("应捕获连接等待增长: %+v", matched)
		}
	})
}

func TestDetectDBPoolAlert(t *testing.T) {
	prev := DBPoolStat{
		Name:               "primary",
		MaxOpenConnections: 10,
		InUse:              2,
		WaitCount:          5,
		WaitDuration:       5 * time.Millisecond,
	}
	current := DBPoolStat{
		Name:               "primary",
		MaxOpenConnections: 10,
		OpenConnections:    10,
		InUse:              9,
		Idle:               1,
		WaitCount:          8,
		WaitDuration:       15 * time.Millisecond,
	}

	alert, ok := detectDBPoolAlert(current, prev, 0.8, 1)
	if !ok {
		t.Fatalf("连接池高占用+等待增长应触发告警")
	}
	if !alert.UtilizationRisk {
		t.Fatalf("高占用应触发 utilization risk")
	}
	if alert.WaitCountDelta != 3 {
		t.Fatalf("WaitCountDelta 计算错误: %d", alert.WaitCountDelta)
	}
	if alert.WaitDurationDelta != 10*time.Millisecond {
		t.Fatalf("WaitDurationDelta 计算错误: %s", alert.WaitDurationDelta)
	}
}
