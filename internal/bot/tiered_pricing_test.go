package bot

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"go-bot/internal/db"
)

func TestControlPoint142TieredPricing(t *testing.T) {
	t.Run("控制点 1.4.2 功能验证", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "tiered-pricing.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const (
			chatID int64 = 4201
			userID int64 = 8801
		)
		messageID := 7000

		mustApplyDeposit := func(amount float64) {
			messageID++
			result, applyErr := database.ApplyLedgerEntry(db.LedgerEntryInput{
				ChatID:           chatID,
				MessageID:        messageID,
				UserID:           userID,
				Username:         "tier-user",
				Nickname:         "TierUser",
				EntryType:        "deposit",
				Amount:           amount,
				Currency:         "CNY",
				BaseAmount:       amount,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      amount,
				Note:             "tier-test",
				TrackMessageLink: "tg://msg/tier-test",
			})
			if applyErr != nil {
				t.Fatalf("写入记账失败: %v", applyErr)
			}
			if result.Duplicate || result.EntryID == 0 {
				t.Fatalf("写入记账结果异常: %+v", result)
			}
		}

		mustApplyDeposit(80000)
		mustApplyDeposit(200000)
		messageID++
		reversal, err := database.ReverseLastLedger(chatID, messageID, userID, "tier-user", "TierUser", "deposit", "tg://msg/reversal")
		if err != nil {
			t.Fatalf("构造撤回样例失败: %v", err)
		}
		if reversal.Duplicate || reversal.NotFound || reversal.Settled || reversal.ReversalID == 0 {
			t.Fatalf("撤回结果异常: %+v", reversal)
		}

		monthlyTrade, err := database.GetUserMonthlyTradeVolume(chatID, userID, time.Now().UTC())
		if err != nil {
			t.Fatalf("查询月交易量失败: %v", err)
		}
		if !nearlyEqual(monthlyTrade, 80000) {
			t.Fatalf("撤回样例不应计入月交易量，want=80000 got=%.8f", monthlyTrade)
		}

		app := &App{db: database}
		l1 := app.resolveTieredFeeForUser(chatID, userID, 2, 5000)
		if l1.Rule.LevelName != "L1" || !nearlyEqual(l1.EffectiveRate, 2) {
			t.Fatalf("L1 费率计算异常: %+v", l1)
		}

		l2 := app.resolveTieredFeeForUser(chatID, userID, 2, 25000)
		if l2.Rule.LevelName != "L2" || !nearlyEqual(l2.EffectiveRate, 1.9) {
			t.Fatalf("L2 费率计算异常: %+v", l2)
		}

		mustApplyDeposit(250000)
		l3 := app.resolveTieredFeeForUser(chatID, userID, 2, 1000)
		if l3.Rule.LevelName != "L3" || !nearlyEqual(l3.EffectiveRate, 1.8) {
			t.Fatalf("L3 费率计算异常: %+v", l3)
		}

		mustApplyDeposit(700000)
		l4 := app.resolveTieredFeeForUser(chatID, userID, 2, 1000)
		if l4.Rule.LevelName != "L4" || !nearlyEqual(l4.EffectiveRate, 1.7) {
			t.Fatalf("L4 费率计算异常: %+v", l4)
		}
	})
}

func nearlyEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
