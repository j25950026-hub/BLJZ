package system

import (
	"testing"
	"time"
)

func TestControlPoint354APIQuotaMonitor(t *testing.T) {
	t.Run("控制点 3.5.4 功能验证", func(t *testing.T) {
		alerts := make([]APIQuotaAlert, 0, 2)
		recovered := make([]APIQuotaStat, 0, 1)
		monitor := NewAPIQuotaMonitor(APIQuotaMonitorOptions{
			LowRemainingRatio: 0.2,
			LowRemainingCount: 100,
			OnAlert: func(alert APIQuotaAlert) {
				alerts = append(alerts, alert)
			},
			OnRecover: func(stat APIQuotaStat) {
				recovered = append(recovered, stat)
			},
		})

		monitor.Observe(APIQuotaSample{
			Provider:    "tronscan",
			Endpoint:    "apilist.tronscanapi.com",
			StatusCode:  200,
			Limit:       1000,
			Remaining:   600,
			CollectedAt: time.Unix(100, 0),
		})
		monitor.scanAndAlert()

		monitor.Observe(APIQuotaSample{
			Provider:    "tronscan",
			Endpoint:    "apilist.tronscanapi.com",
			StatusCode:  200,
			Limit:       1000,
			Remaining:   80,
			CollectedAt: time.Unix(130, 0),
		})
		monitor.scanAndAlert()

		monitor.Observe(APIQuotaSample{
			Provider:    "tronscan",
			Endpoint:    "apilist.tronscanapi.com",
			StatusCode:  200,
			Limit:       1000,
			Remaining:   70,
			CollectedAt: time.Unix(160, 0),
		})
		monitor.scanAndAlert()

		monitor.Observe(APIQuotaSample{
			Provider:    "tronscan",
			Endpoint:    "apilist.tronscanapi.com",
			StatusCode:  200,
			Limit:       1000,
			Remaining:   350,
			CollectedAt: time.Unix(190, 0),
		})
		monitor.scanAndAlert()

		if len(alerts) != 1 {
			t.Fatalf("低额度阶段应只触发一次告警: got=%d", len(alerts))
		}
		alert := alerts[0]
		if alert.Provider != "tronscan" {
			t.Fatalf("告警 provider 错误: %s", alert.Provider)
		}
		if alert.Endpoint != "apilist.tronscanapi.com" {
			t.Fatalf("告警 endpoint 错误: %s", alert.Endpoint)
		}
		if !alert.LowByCount || !alert.LowByRatio {
			t.Fatalf("告警维度应同时命中数量与比例阈值: %+v", alert)
		}
		if alert.Remaining != 80 || alert.Limit != 1000 || alert.Used != 920 {
			t.Fatalf("额度字段错误: %+v", alert)
		}
		if alert.RequestsTotal != 2 {
			t.Fatalf("请求计数错误: %d", alert.RequestsTotal)
		}

		if len(recovered) != 1 {
			t.Fatalf("额度恢复后应触发一次恢复事件: got=%d", len(recovered))
		}
		recoverStat := recovered[0]
		if recoverStat.Remaining != 350 {
			t.Fatalf("恢复剩余额度错误: %d", recoverStat.Remaining)
		}
		if recoverStat.RequestsTotal != 4 {
			t.Fatalf("恢复阶段请求计数错误: %d", recoverStat.RequestsTotal)
		}
	})
}

func TestDetectAPIQuotaAlert(t *testing.T) {
	stat := APIQuotaStat{
		Provider:      "tronscan",
		Endpoint:      "apilist.tronscanapi.com",
		RequestsTotal: 10,
		Limit:         1000,
		Remaining:     90,
		Used:          910,
	}

	alert, ok := detectAPIQuotaAlert(stat, 0.2, 100)
	if !ok {
		t.Fatalf("低额度场景应触发告警")
	}
	if !alert.LowByCount || !alert.LowByRatio {
		t.Fatalf("告警维度错误: %+v", alert)
	}
	if alert.RemainingRatio != 0.09 {
		t.Fatalf("额度比例计算错误: %.4f", alert.RemainingRatio)
	}

	stat.Remaining = 500
	if _, low := detectAPIQuotaAlert(stat, 0.2, 100); low {
		t.Fatalf("额度充足时不应触发告警")
	}
}
