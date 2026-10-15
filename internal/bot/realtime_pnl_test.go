package bot

import (
	"math"
	"testing"

	"go-bot/internal/db"
)

func TestControlPoint143RealTimePNL(t *testing.T) {
	t.Run("控制点 1.4.3 功能验证", func(t *testing.T) {
		group := db.GroupConfig{
			CustomRate: 7.30,
			PayoutRate: 7.10,
		}

		depositPNL := buildRealtimePNLBreakdown(group, "deposit", 10000, 9800, 7.30)
		if !floatAlmostEqual(depositPNL.DepositAmount, 10000) || !floatAlmostEqual(depositPNL.PayoutAmount, 0) {
			t.Fatalf("入款拆分错误: %+v", depositPNL)
		}
		if !floatAlmostEqual(depositPNL.RateDiff, 0.2) {
			t.Fatalf("入款汇率差错误: got=%.8f", depositPNL.RateDiff)
		}
		if !floatAlmostEqual(depositPNL.DepositRate, 7.30) || !floatAlmostEqual(depositPNL.PayoutRate, 7.10) {
			t.Fatalf("入款基准汇率错误: %+v", depositPNL)
		}
		if !floatAlmostEqual(depositPNL.FXGainLoss, 2000) {
			t.Fatalf("入款汇率损益错误: got=%.8f", depositPNL.FXGainLoss)
		}
		if !floatAlmostEqual(depositPNL.FeeAmount, 200) {
			t.Fatalf("入款手续费错误: got=%.8f", depositPNL.FeeAmount)
		}
		if !floatAlmostEqual(depositPNL.EstimatedPNL, 2200) {
			t.Fatalf("入款实时利润计算错误: want=2200 got=%.8f", depositPNL.EstimatedPNL)
		}

		payoutPNL := buildRealtimePNLBreakdown(group, "payout", 5000, 5100, 7.10)
		if !floatAlmostEqual(payoutPNL.DepositAmount, 0) || !floatAlmostEqual(payoutPNL.PayoutAmount, 5000) {
			t.Fatalf("下发拆分错误: %+v", payoutPNL)
		}
		if !floatAlmostEqual(payoutPNL.RateDiff, 0.2) {
			t.Fatalf("下发汇率差错误: got=%.8f", payoutPNL.RateDiff)
		}
		if !floatAlmostEqual(payoutPNL.DepositRate, 7.30) || !floatAlmostEqual(payoutPNL.PayoutRate, 7.10) {
			t.Fatalf("下发基准汇率错误: %+v", payoutPNL)
		}
		if !floatAlmostEqual(payoutPNL.FXGainLoss, -1000) {
			t.Fatalf("下发汇率损益错误: got=%.8f", payoutPNL.FXGainLoss)
		}
		if !floatAlmostEqual(payoutPNL.FeeAmount, -100) {
			t.Fatalf("下发手续费错误: got=%.8f", payoutPNL.FeeAmount)
		}
		if !floatAlmostEqual(payoutPNL.EstimatedPNL, -1100) {
			t.Fatalf("下发实时利润计算错误: want=-1100 got=%.8f", payoutPNL.EstimatedPNL)
		}
	})
}

func floatAlmostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
