package system

import (
	"path/filepath"
	"testing"
)

func TestControlPoint352DiskSpaceAlert(t *testing.T) {
	t.Run("控制点 3.5.2 功能验证", func(t *testing.T) {
		snapshots := []DiskUsageStat{
			{Path: "/var/log/bot", TotalBytes: 1000, AvailableBytes: 350},
			{Path: "/var/log/bot", TotalBytes: 1000, AvailableBytes: 80},
			{Path: "/var/log/bot", TotalBytes: 1000, AvailableBytes: 70},
			{Path: "/var/log/bot", TotalBytes: 1000, AvailableBytes: 280},
		}
		next := 0

		alerts := make([]DiskSpaceAlert, 0, 2)
		recovered := make([]DiskUsageStat, 0, 1)
		monitor := NewDiskSpaceMonitor(DiskSpaceMonitorOptions{
			Path:           "/var/log/bot",
			MinFreeBytes:   100,
			MinFreePercent: 10,
			Probe: func(string) (DiskUsageStat, error) {
				if next >= len(snapshots) {
					return snapshots[len(snapshots)-1], nil
				}
				stat := snapshots[next]
				next++
				return stat, nil
			},
			OnAlert: func(alert DiskSpaceAlert) {
				alerts = append(alerts, alert)
			},
			OnRecover: func(stat DiskUsageStat) {
				recovered = append(recovered, stat)
			},
		})

		monitor.scanAndAlert()
		monitor.scanAndAlert()
		monitor.scanAndAlert()
		monitor.scanAndAlert()

		if len(alerts) != 1 {
			t.Fatalf("低磁盘空间期间应只触发一次告警: got=%d", len(alerts))
		}
		alert := alerts[0]
		if alert.Path != filepath.Clean("/var/log/bot") {
			t.Fatalf("告警路径错误: %s", alert.Path)
		}
		if !alert.LowByBytes || !alert.LowByPercent {
			t.Fatalf("告警触发维度错误: %+v", alert)
		}
		if alert.AvailableBytes != 80 {
			t.Fatalf("告警可用空间错误: %d", alert.AvailableBytes)
		}
		if alert.FreePercent != 8 {
			t.Fatalf("告警剩余比例错误: %.2f", alert.FreePercent)
		}

		if len(recovered) != 1 {
			t.Fatalf("空间恢复后应触发恢复事件: got=%d", len(recovered))
		}
		if recovered[0].AvailableBytes != 280 {
			t.Fatalf("恢复事件可用空间错误: %d", recovered[0].AvailableBytes)
		}
	})
}

func TestDetectDiskSpaceAlert(t *testing.T) {
	tests := []struct {
		name           string
		stat           DiskUsageStat
		minFreeBytes   uint64
		minFreePercent float64
		wantAlert      bool
		wantByBytes    bool
		wantByPercent  bool
	}{
		{
			name:           "按剩余字节触发",
			stat:           DiskUsageStat{TotalBytes: 1000, AvailableBytes: 90},
			minFreeBytes:   100,
			minFreePercent: 5,
			wantAlert:      true,
			wantByBytes:    true,
			wantByPercent:  false,
		},
		{
			name:           "按剩余比例触发",
			stat:           DiskUsageStat{TotalBytes: 1000, AvailableBytes: 80},
			minFreeBytes:   10,
			minFreePercent: 10,
			wantAlert:      true,
			wantByBytes:    false,
			wantByPercent:  true,
		},
		{
			name:           "空间正常不触发",
			stat:           DiskUsageStat{TotalBytes: 1000, AvailableBytes: 500},
			minFreeBytes:   100,
			minFreePercent: 10,
			wantAlert:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			alert, gotAlert := detectDiskSpaceAlert(tc.stat, tc.minFreeBytes, tc.minFreePercent)
			if gotAlert != tc.wantAlert {
				t.Fatalf("告警判定错误: want=%v got=%v", tc.wantAlert, gotAlert)
			}
			if !tc.wantAlert {
				return
			}
			if alert.LowByBytes != tc.wantByBytes {
				t.Fatalf("LowByBytes 错误: want=%v got=%v", tc.wantByBytes, alert.LowByBytes)
			}
			if alert.LowByPercent != tc.wantByPercent {
				t.Fatalf("LowByPercent 错误: want=%v got=%v", tc.wantByPercent, alert.LowByPercent)
			}
		})
	}
}

func TestReadDiskUsage(t *testing.T) {
	dir := t.TempDir()
	stat, err := ReadDiskUsage(dir)
	if err != nil {
		t.Fatalf("读取磁盘用量失败: %v", err)
	}
	if stat.Path != filepath.Clean(dir) {
		t.Fatalf("磁盘路径错误: %q", stat.Path)
	}
	if stat.TotalBytes == 0 {
		t.Fatalf("总磁盘容量应大于 0")
	}
	if stat.FreePercent < 0 || stat.FreePercent > 100 {
		t.Fatalf("剩余比例范围错误: %.2f", stat.FreePercent)
	}
}
