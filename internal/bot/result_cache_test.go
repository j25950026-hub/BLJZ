package bot

import (
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestControlPoint155ResultCaching(t *testing.T) {
	t.Run("控制点 1.5.5 功能验证", func(t *testing.T) {
		cache := newRequestResultCache(time.Minute)
		app := &App{requestResultCache: cache}
		msg := &tgbotapi.Message{
			MessageID: 9905,
			Chat:      &tgbotapi.Chat{ID: -10022334455},
		}

		cacheKey := buildRequestResultCacheKey("ledger", msg.Chat.ID, msg.MessageID)
		if cacheKey == "" {
			t.Fatalf("结果缓存键不能为空")
		}

		expectedReply := "记账成功（ID:42）"
		cache.Set(cacheKey, msg.Chat.ID, expectedReply)

		cachedReply, ok := app.cachedBookkeepingResult(msg, "ledger")
		if !ok {
			t.Fatalf("已处理请求应命中结果缓存")
		}
		if cachedReply != expectedReply {
			t.Fatalf("结果缓存内容不一致: want=%q got=%q", expectedReply, cachedReply)
		}

		businessExecuted := false
		if _, exists := app.cachedBookkeepingResult(msg, "ledger"); !exists {
			businessExecuted = true
		}
		if businessExecuted {
			t.Fatalf("命中缓存后不应继续执行业务逻辑")
		}
	})
}

func TestRequestResultCacheExpiryAndClear(t *testing.T) {
	cache := newRequestResultCache(5 * time.Millisecond)
	cacheKey := buildRequestResultCacheKey("reversal", -10022334456, 9906)
	cache.Set(cacheKey, -10022334456, "撤回成功。")

	time.Sleep(15 * time.Millisecond)
	if _, ok := cache.Get(cacheKey); ok {
		t.Fatalf("过期缓存不应继续命中")
	}

	cache = newRequestResultCache(time.Minute)
	cache.Set(buildRequestResultCacheKey("ledger", -10001, 1), -10001, "A")
	cache.Set(buildRequestResultCacheKey("ledger", -10001, 2), -10001, "B")
	cache.Set(buildRequestResultCacheKey("ledger", -10002, 3), -10002, "C")

	if removed := cache.ClearChat(-10001); removed != 2 {
		t.Fatalf("按群清理结果缓存数量异常: want=2 got=%d", removed)
	}
	if _, ok := cache.Get(buildRequestResultCacheKey("ledger", -10001, 1)); ok {
		t.Fatalf("目标群缓存应被清理")
	}
	if _, ok := cache.Get(buildRequestResultCacheKey("ledger", -10002, 3)); !ok {
		t.Fatalf("非目标群缓存不应被清理")
	}
}

func TestAppClearBookkeepingResultCacheByChat(t *testing.T) {
	cache := newRequestResultCache(time.Minute)
	app := &App{requestResultCache: cache}
	cache.Set(buildRequestResultCacheKey("ledger", -10001, 1), -10001, "A")
	cache.Set(buildRequestResultCacheKey("reversal", -10001, 2), -10001, "B")
	cache.Set(buildRequestResultCacheKey("ledger", -10002, 3), -10002, "C")

	if removed := app.ClearBookkeepingResultCache(-10001); removed != 2 {
		t.Fatalf("按群清理账单回执缓存数量异常: want=2 got=%d", removed)
	}
	if _, ok := cache.Get(buildRequestResultCacheKey("ledger", -10001, 1)); ok {
		t.Fatalf("受影响群的账单缓存应被清理")
	}
	if _, ok := cache.Get(buildRequestResultCacheKey("ledger", -10002, 3)); !ok {
		t.Fatalf("其他群的账单缓存不应被清理")
	}
}
