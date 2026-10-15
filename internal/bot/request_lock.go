package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"go-bot/internal/db"
)

const (
	defaultDistributedRequestLockTTL       = 10 * time.Second
	defaultDistributedRequestLockKeyPrefix = "lock:request:"
)

type RequestLocker interface {
	Acquire(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	Close() error
}

type noopRequestLocker struct{}

func (noopRequestLocker) Acquire(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (noopRequestLocker) Close() error {
	return nil
}

type RedisRequestLockerConfig struct {
	Addr           string
	Password       string
	DB             int
	KeyPrefix      string
	AcquireTimeout time.Duration
}

type RedisRequestLocker struct {
	client         *redis.Client
	keyPrefix      string
	acquireTimeout time.Duration
}

func NewRedisRequestLocker(cfg RedisRequestLockerConfig) (*RedisRequestLocker, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, fmt.Errorf("redis 地址不能为空")
	}
	keyPrefix := strings.TrimSpace(cfg.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultDistributedRequestLockKeyPrefix
	}
	acquireTimeout := cfg.AcquireTimeout
	if acquireTimeout <= 0 {
		acquireTimeout = 2 * time.Second
	}
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: strings.TrimSpace(cfg.Password),
		DB:       cfg.DB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), acquireTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &RedisRequestLocker{
		client:         client,
		keyPrefix:      keyPrefix,
		acquireTimeout: acquireTimeout,
	}, nil
}

func (r *RedisRequestLocker) Acquire(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	if r == nil || r.client == nil {
		return false, fmt.Errorf("redis request locker 未初始化")
	}
	if ttl <= 0 {
		ttl = defaultDistributedRequestLockTTL
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lockCtx, cancel := context.WithTimeout(ctx, r.acquireTimeout)
	defer cancel()
	return r.client.SetNX(lockCtx, r.keyPrefix+strings.TrimSpace(key), value, ttl).Result()
}

func (r *RedisRequestLocker) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func buildRequestLockKey(commandKey string, chatID int64, messageID int) string {
	return normalizeRequestCommandKey(commandKey) + ":" + db.BuildRequestFingerprint(chatID, messageID)
}

func normalizeRequestCommandKey(commandKey string) string {
	key := strings.ToLower(strings.TrimSpace(commandKey))
	if key == "" {
		return "request"
	}
	return key
}

func buildRequestLockValue(now time.Time) string {
	return strconv.FormatInt(now.UTC().UnixNano(), 10)
}
