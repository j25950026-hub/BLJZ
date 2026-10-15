package bot

import "math"

type feeTierRule struct {
	LevelName       string
	MinMonthlyTrade float64
	RateDelta       float64
}

type tieredFeeDecision struct {
	EffectiveRate float64
	MonthlyTrade  float64
	Rule          feeTierRule
}

var defaultFeeTierRules = []feeTierRule{
	{LevelName: "L1", MinMonthlyTrade: 0, RateDelta: 0},
	{LevelName: "L2", MinMonthlyTrade: 100000, RateDelta: -0.1},
	{LevelName: "L3", MinMonthlyTrade: 300000, RateDelta: -0.2},
	{LevelName: "L4", MinMonthlyTrade: 1000000, RateDelta: -0.3},
}

func resolveFeeTierRule(monthlyTrade float64) feeTierRule {
	volume := math.Abs(monthlyTrade)
	selected := defaultFeeTierRules[0]
	for _, rule := range defaultFeeTierRules {
		if volume < rule.MinMonthlyTrade {
			break
		}
		selected = rule
	}
	return selected
}

func calculateTieredFee(baseRate, monthlyTrade float64) tieredFeeDecision {
	rule := resolveFeeTierRule(monthlyTrade)
	return tieredFeeDecision{
		EffectiveRate: baseRate + rule.RateDelta,
		MonthlyTrade:  math.Abs(monthlyTrade),
		Rule:          rule,
	}
}
