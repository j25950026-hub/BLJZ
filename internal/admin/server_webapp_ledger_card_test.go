package admin

import (
	"math"
	"testing"
	"time"

	"go-bot/internal/db"
)

func TestBuildTGWebAppLedgerCardVM_CNYAndUSDT(t *testing.T) {
	now := time.Date(2026, 2, 21, 14, 33, 0, 0, time.UTC)
	rows := []db.LedgerEntry{
		{
			EntryType:   "deposit",
			FinalAmount: 10000,
			Rate:        10,
			CreatedAt:   "2026-02-21T01:07:12Z",
			Note:        "首笔入款",
		},
		{
			EntryType:   "payout",
			FinalAmount: 2000,
			Currency:    "CNY",
			Rate:        10,
			CreatedAt:   "2026-02-21T02:00:00Z",
			Note:        "下发备注",
		},
		{
			EntryType: "payout",
			Amount:    50,
			Currency:  "USDT",
			Rate:      7.2,
			CreatedAt: "2026-02-21T03:00:00Z",
			Note:      "USDT下发",
		},
	}
	group := db.GroupConfig{
		FeeRate:    0,
		CustomRate: 10,
	}

	got := buildTGWebAppLedgerCardVM(rows, group, "Asia/Shanghai", now)
	if got == nil {
		t.Fatalf("构建结果不能为空")
	}
	if got.MessageTime != "22:33" {
		t.Fatalf("消息时间异常: got=%s", got.MessageTime)
	}
	if len(got.Deposits) != 1 {
		t.Fatalf("入款条目数异常: %d", len(got.Deposits))
	}
	if len(got.Payouts) != 2 {
		t.Fatalf("下发条目数异常: %d", len(got.Payouts))
	}
	if got.Deposits[0].Converted != 1000 {
		t.Fatalf("入款换算异常: %+v", got.Deposits[0])
	}
	if got.Payouts[0].PayoutU != 200 {
		t.Fatalf("CNY 下发折算异常: %+v", got.Payouts[0])
	}
	if got.Payouts[0].Currency != "" {
		t.Fatalf("CNY 下发币种显示应为空: %+v", got.Payouts[0])
	}
	if got.Payouts[0].Converted != "≈200 U" {
		t.Fatalf("CNY 下发展示异常: %+v", got.Payouts[0])
	}
	if got.Payouts[1].Currency != "USDT" {
		t.Fatalf("USDT 下发币种异常: %+v", got.Payouts[1])
	}
	if got.Payouts[1].Amount != 50 {
		t.Fatalf("USDT 下发金额异常: %+v", got.Payouts[1])
	}
	if got.Payouts[1].PayoutU != 50 {
		t.Fatalf("USDT 下发折算异常: %+v", got.Payouts[1])
	}
	if got.Payouts[1].Converted != "≈360" {
		t.Fatalf("USDT 下发展示异常: %+v", got.Payouts[1])
	}
}

func TestBuildTGWebAppLedgerCardVM_DepositUsesFeeFactorAndTrack(t *testing.T) {
	now := time.Date(2026, 2, 21, 14, 33, 0, 0, time.UTC)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    1000,
			Currency:  "CNY",
			Rate:      10,
			FeeRate:   5,
			CreatedAt: "2026-02-21T01:07:12Z",
			TrackLink: "https://t.me/c/123/456",
		},
		{
			EntryType: "deposit",
			Amount:    2000,
			Currency:  "CNY",
			Rate:      10,
			CreatedAt: "2026-02-21T01:10:12Z",
		},
	}
	group := db.GroupConfig{
		FeeRate:    5,
		CustomRate: 10,
	}

	got := buildTGWebAppLedgerCardVM(rows, group, "Asia/Shanghai", now)
	if got == nil {
		t.Fatalf("构建结果不能为空")
	}
	if len(got.Deposits) != 2 {
		t.Fatalf("入款条目数异常: %d", len(got.Deposits))
	}
	if diff := math.Abs(got.Deposits[0].Converted - 95); diff > 1e-9 {
		t.Fatalf("首笔入款费后换算异常: converted=%f", got.Deposits[0].Converted)
	}
	if diff := math.Abs(got.Deposits[0].FeeFactor - 0.95); diff > 1e-9 {
		t.Fatalf("首笔入款费率因子异常: feeFactor=%f", got.Deposits[0].FeeFactor)
	}
	if got.Deposits[0].SourceCurrency != "CNY" {
		t.Fatalf("首笔入款源币种异常: %+v", got.Deposits[0])
	}
	if got.Deposits[0].Track != "https://t.me/c/123/456" {
		t.Fatalf("首笔入款跟踪链接异常: %+v", got.Deposits[0])
	}
	if diff := math.Abs(got.Deposits[1].Converted - 190); diff > 1e-9 {
		t.Fatalf("第二笔入款应按群费率折算: converted=%f", got.Deposits[1].Converted)
	}
	if diff := math.Abs(got.Deposits[1].FeeFactor - 0.95); diff > 1e-9 {
		t.Fatalf("第二笔入款费率因子异常: feeFactor=%f", got.Deposits[1].FeeFactor)
	}
}

func TestBuildTGWebAppLedgerCardVM_DepositFinalAmountPrecedence(t *testing.T) {
	now := time.Date(2026, 2, 21, 14, 33, 0, 0, time.UTC)
	rows := []db.LedgerEntry{
		{
			EntryType:   "deposit",
			Amount:      1000,
			FinalAmount: 920,
			Currency:    "CNY",
			Rate:        10,
			FeeRate:     5,
			CreatedAt:   "2026-02-21T01:07:12Z",
		},
	}
	group := db.GroupConfig{
		FeeRate:    5,
		CustomRate: 10,
	}

	got := buildTGWebAppLedgerCardVM(rows, group, "Asia/Shanghai", now)
	if got == nil || len(got.Deposits) != 1 {
		t.Fatalf("构建结果异常: %+v", got)
	}
	if diff := math.Abs(got.Deposits[0].Converted - 92); diff > 1e-9 {
		t.Fatalf("应优先使用最终金额换算，got=%+v", got.Deposits[0])
	}
	if diff := math.Abs(got.Deposits[0].FeeFactor - 0.92); diff > 1e-9 {
		t.Fatalf("应基于最终金额回推费率因子，got=%+v", got.Deposits[0])
	}
}
