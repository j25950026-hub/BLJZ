package db

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlPoint333ReversalAudit(t *testing.T) {
	t.Run("控制点 3.3.3 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "reversal-audit.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer func() {
			if closeErr := database.Close(); closeErr != nil {
				t.Fatalf("关闭数据库失败: %v", closeErr)
			}
		}()

		const (
			chatID     int64 = 333001
			operatorID int64 = 33300101
		)
		baseInput := LedgerEntryInput{
			ChatID:      chatID,
			UserID:      operatorID,
			Username:    "cp333",
			Nickname:    "ControlPoint333",
			EntryType:   "deposit",
			Amount:      100,
			Currency:    "CNY",
			BaseAmount:  100,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 100,
			Note:        "控制点3.3.3样本",
			CardNo:      "6222000033300001",
		}

		for i := 0; i < 5; i++ {
			item := baseInput
			item.MessageID = 333100 + i
			item.TrackMessageLink = fmt.Sprintf("tg://msg/%d", item.MessageID)
			applyResult, applyErr := database.ApplyLedgerEntry(item)
			if applyErr != nil {
				t.Fatalf("构造样本记账失败: %v", applyErr)
			}
			if applyResult.Duplicate || applyResult.EntryID == 0 {
				t.Fatalf("样本记账状态异常: %+v", applyResult)
			}
		}

		for i := 0; i < 2; i++ {
			messageID := 333200 + i
			reversal, reversalErr := database.ReverseLastLedger(
				chatID,
				messageID,
				operatorID,
				"cp333",
				"ControlPoint333",
				"deposit",
				fmt.Sprintf("tg://msg/%d", messageID),
			)
			if reversalErr != nil {
				t.Fatalf("阈值前撤回失败: %v", reversalErr)
			}
			if reversal.Duplicate || reversal.NotFound || reversal.Settled || reversal.ReversalID == 0 {
				t.Fatalf("阈值前撤回状态异常: %+v", reversal)
			}
		}

		riskCount := mustCountRows(
			t,
			database,
			`SELECT COUNT(1) FROM risk_events WHERE chat_id = ? AND user_id = ? AND event_type = ?`,
			chatID,
			operatorID,
			reversalAuditRiskEventType,
		)
		if riskCount != 0 {
			t.Fatalf("未达到阈值前不应触发风险标记: got=%d", riskCount)
		}

		finalMessageID := 333202
		finalReversal, err := database.ReverseLastLedger(
			chatID,
			finalMessageID,
			operatorID,
			"cp333",
			"ControlPoint333",
			"deposit",
			fmt.Sprintf("tg://msg/%d", finalMessageID),
		)
		if err != nil {
			t.Fatalf("达到阈值后的撤回失败: %v", err)
		}
		if finalReversal.Duplicate || finalReversal.NotFound || finalReversal.Settled || finalReversal.ReversalID == 0 {
			t.Fatalf("达到阈值后的撤回状态异常: %+v", finalReversal)
		}

		riskCount = mustCountRows(
			t,
			database,
			`SELECT COUNT(1) FROM risk_events WHERE chat_id = ? AND user_id = ? AND event_type = ?`,
			chatID,
			operatorID,
			reversalAuditRiskEventType,
		)
		if riskCount != 1 {
			t.Fatalf("达到阈值后应触发一次风险标记: got=%d", riskCount)
		}

		var (
			severity string
			detail   string
		)
		if scanErr := database.raw.QueryRow(
			`SELECT severity, detail FROM risk_events WHERE chat_id = ? AND user_id = ? AND event_type = ? ORDER BY id DESC LIMIT 1`,
			chatID,
			operatorID,
			reversalAuditRiskEventType,
		).Scan(&severity, &detail); scanErr != nil {
			t.Fatalf("读取风险事件详情失败: %v", scanErr)
		}
		if severity != reversalAuditRiskSeverity {
			t.Fatalf("风险等级错误: want=%s got=%s", reversalAuditRiskSeverity, severity)
		}

		expectedRate := float64(3) / float64(8)
		wants := []string{
			fmt.Sprintf("operator_user_id=%d", operatorID),
			"reversal_count=3",
			"total_count=8",
			fmt.Sprintf("reversal_rate=%.4f", expectedRate),
			fmt.Sprintf("threshold=%.4f", reversalAuditRateThreshold),
			fmt.Sprintf("min_actions=%d", reversalAuditMinActionCount),
		}
		for _, want := range wants {
			if !strings.Contains(detail, want) {
				t.Fatalf("风险详情缺少字段 %q，got=%s", want, detail)
			}
		}
	})
}
