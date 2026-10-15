package bot

import (
	"strings"

	"go-bot/internal/db"
)

type realtimePNLBreakdown struct {
	DepositAmount float64
	PayoutAmount  float64
	DepositRate   float64
	PayoutRate    float64
	RateDiff      float64
	FXGainLoss    float64
	FeeAmount     float64
	EstimatedPNL  float64
}

func calculateFXGainLoss(depositAmount, payoutAmount, depositRate, payoutRate float64) float64 {
	return (depositAmount - payoutAmount) * (depositRate - payoutRate)
}

func calculateRealtimePNL(fxGainLoss, feeAmount float64) float64 {
	return fxGainLoss + feeAmount
}

func buildRealtimePNLBreakdown(g db.GroupConfig, entryType string, baseAmount, finalAmount, currentRate float64) realtimePNLBreakdown {
	depositRate, payoutRate := resolveRealtimePNLBenchmarkRates(g, entryType, currentRate)
	depositAmount, payoutAmount := splitRealtimePNLAmounts(entryType, baseAmount)
	feeAmount := baseAmount - finalAmount
	rateDiff := depositRate - payoutRate
	fxGainLoss := calculateFXGainLoss(depositAmount, payoutAmount, depositRate, payoutRate)
	return realtimePNLBreakdown{
		DepositAmount: depositAmount,
		PayoutAmount:  payoutAmount,
		DepositRate:   depositRate,
		PayoutRate:    payoutRate,
		RateDiff:      rateDiff,
		FXGainLoss:    fxGainLoss,
		FeeAmount:     feeAmount,
		EstimatedPNL:  calculateRealtimePNL(fxGainLoss, feeAmount),
	}
}

func resolveRealtimePNLBenchmarkRates(g db.GroupConfig, entryType string, currentRate float64) (float64, float64) {
	depositRate := normalizeRealtimePNLRate(g.CustomRate, currentRate)
	payoutRate := normalizeRealtimePNLRate(g.PayoutRate, currentRate)
	switch strings.ToLower(strings.TrimSpace(entryType)) {
	case "deposit":
		depositRate = currentRate
	case "payout":
		payoutRate = currentRate
	}
	return depositRate, payoutRate
}

func splitRealtimePNLAmounts(entryType string, amount float64) (float64, float64) {
	switch strings.ToLower(strings.TrimSpace(entryType)) {
	case "deposit":
		return amount, 0
	case "payout":
		return 0, amount
	default:
		return 0, 0
	}
}

func normalizeRealtimePNLRate(rate, fallback float64) float64 {
	if almostZero(rate) {
		return fallback
	}
	return rate
}
