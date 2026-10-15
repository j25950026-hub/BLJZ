package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultDistributedSessionKeyPrefix = "session:dist:"
	defaultDistributedSessionTTL       = 20 * time.Minute
	defaultDistributedSessionTimeout   = 2 * time.Second
	distributedSessionExpiryGrace      = 30 * time.Second
)

type DistributedSession struct {
	SessionID  string            `json:"session_id"`
	UserID     int64             `json:"user_id"`
	LoggedIn   bool              `json:"logged_in"`
	Context    map[string]string `json:"context,omitempty"`
	IssuedAt   int64             `json:"issued_at"`
	ExpiresAt  int64             `json:"expires_at"`
	LastSeenAt int64             `json:"last_seen_at"`
}

type DistributedSessionStore interface {
	Save(ctx context.Context, session DistributedSession) error
	Get(ctx context.Context, sessionID string) (DistributedSession, bool, error)
	Delete(ctx context.Context, sessionID string) error
	Enabled() bool
	Close() error
}

type noopDistributedSessionStore struct{}

func (noopDistributedSessionStore) Save(context.Context, DistributedSession) error {
	return nil
}

func (noopDistributedSessionStore) Get(context.Context, string) (DistributedSession, bool, error) {
	return DistributedSession{}, false, nil
}

func (noopDistributedSessionStore) Delete(context.Context, string) error {
	return nil
}

func (noopDistributedSessionStore) Enabled() bool {
	return false
}

func (noopDistributedSessionStore) Close() error {
	return nil
}

type RedisDistributedSessionStoreConfig struct {
	Addr      string
	Password  string
	DB        int
	KeyPrefix string
	TTL       time.Duration
	Timeout   time.Duration
}

type RedisDistributedSessionStore struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
	timeout   time.Duration
}

func NewRedisDistributedSessionStore(cfg RedisDistributedSessionStoreConfig) (*RedisDistributedSessionStore, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, fmt.Errorf("redis 地址不能为空")
	}
	keyPrefix := strings.TrimSpace(cfg.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultDistributedSessionKeyPrefix
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultDistributedSessionTTL
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultDistributedSessionTimeout
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: strings.TrimSpace(cfg.Password),
		DB:       cfg.DB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &RedisDistributedSessionStore{
		client:    client,
		keyPrefix: keyPrefix,
		ttl:       ttl,
		timeout:   timeout,
	}, nil
}

func (r *RedisDistributedSessionStore) Save(ctx context.Context, session DistributedSession) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("distributed session store 未初始化")
	}
	sessionID := strings.TrimSpace(session.SessionID)
	if sessionID == "" {
		return fmt.Errorf("session_id 不能为空")
	}
	if session.UserID == 0 {
		return fmt.Errorf("user_id 不能为空")
	}
	nowUnix := time.Now().Unix()
	if session.IssuedAt <= 0 {
		session.IssuedAt = nowUnix
	}
	if session.LastSeenAt <= 0 {
		session.LastSeenAt = session.IssuedAt
	}
	if session.Context == nil {
		session.Context = map[string]string{}
	}
	session.SessionID = sessionID

	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}

	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()
	return r.client.Set(runCtx, r.sessionKey(sessionID), payload, r.resolveTTL(session.ExpiresAt)).Err()
}

func (r *RedisDistributedSessionStore) Get(ctx context.Context, sessionID string) (DistributedSession, bool, error) {
	if r == nil || r.client == nil {
		return DistributedSession{}, false, nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return DistributedSession{}, false, nil
	}

	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()
	raw, err := r.client.Get(runCtx, r.sessionKey(sessionID)).Bytes()
	if err == redis.Nil {
		return DistributedSession{}, false, nil
	}
	if err != nil {
		return DistributedSession{}, false, err
	}

	var session DistributedSession
	if err := json.Unmarshal(raw, &session); err != nil {
		_, _ = r.client.Del(runCtx, r.sessionKey(sessionID)).Result()
		return DistributedSession{}, false, err
	}
	if session.SessionID == "" {
		session.SessionID = sessionID
	}
	if session.Context == nil {
		session.Context = map[string]string{}
	}
	if session.ExpiresAt > 0 && session.ExpiresAt < time.Now().Unix() {
		_, _ = r.client.Del(runCtx, r.sessionKey(sessionID)).Result()
		return DistributedSession{}, false, nil
	}
	return session, true, nil
}

func (r *RedisDistributedSessionStore) Delete(ctx context.Context, sessionID string) error {
	if r == nil || r.client == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()
	return r.client.Del(runCtx, r.sessionKey(sessionID)).Err()
}

func (r *RedisDistributedSessionStore) Enabled() bool {
	return r != nil && r.client != nil
}

func (r *RedisDistributedSessionStore) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func (r *RedisDistributedSessionStore) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, r.timeout)
}

func (r *RedisDistributedSessionStore) sessionKey(sessionID string) string {
	return r.keyPrefix + sessionID
}

func (r *RedisDistributedSessionStore) resolveTTL(expiresAt int64) time.Duration {
	if expiresAt <= 0 {
		return r.ttl
	}
	ttl := time.Until(time.Unix(expiresAt, 0)) + distributedSessionExpiryGrace
	if ttl > 0 {
		return ttl
	}
	return time.Second
}
