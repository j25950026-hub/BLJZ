package bot

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestControlPoint152DistributedRequestLock(t *testing.T) {
	t.Run("控制点 1.5.2 功能验证", func(t *testing.T) {
		mini, err := miniredis.Run()
		if err != nil {
			t.Fatalf("启动 miniredis 失败: %v", err)
		}
		defer mini.Close()

		locker, err := NewRedisRequestLocker(RedisRequestLockerConfig{
			Addr:           mini.Addr(),
			KeyPrefix:      "lock:request:",
			AcquireTimeout: time.Second,
		})
		if err != nil {
			t.Fatalf("创建 Redis 分布式锁失败: %v", err)
		}
		defer locker.Close()

		lockKey := buildRequestLockKey("ledger", -10022334455, 9902)
		firstValue := buildRequestLockValue(time.Date(2026, 2, 18, 8, 0, 0, 0, time.UTC))
		acquired, err := locker.Acquire(context.Background(), lockKey, firstValue, 10*time.Second)
		if err != nil {
			t.Fatalf("首次获取分布式锁失败: %v", err)
		}
		if !acquired {
			t.Fatalf("首次请求应成功获取分布式锁")
		}

		redisKey := "lock:request:" + lockKey
		if !mini.Exists(redisKey) {
			t.Fatalf("Redis 锁键未写入: %s", redisKey)
		}
		ttl := mini.TTL(redisKey)
		if ttl <= 0 || ttl > 10*time.Second {
			t.Fatalf("锁 TTL 异常: %v", ttl)
		}

		secondValue := buildRequestLockValue(time.Date(2026, 2, 18, 8, 0, 1, 0, time.UTC))
		acquired, err = locker.Acquire(context.Background(), lockKey, secondValue, 10*time.Second)
		if err != nil {
			t.Fatalf("重复获取分布式锁失败: %v", err)
		}
		if acquired {
			t.Fatalf("10 秒窗口内重复请求应被 SETNX 阻断")
		}

		mini.FastForward(11 * time.Second)
		thirdValue := buildRequestLockValue(time.Date(2026, 2, 18, 8, 0, 12, 0, time.UTC))
		acquired, err = locker.Acquire(context.Background(), lockKey, thirdValue, 10*time.Second)
		if err != nil {
			t.Fatalf("锁过期后重新获取失败: %v", err)
		}
		if !acquired {
			t.Fatalf("锁过期后请求应可重新获取")
		}
	})
}
