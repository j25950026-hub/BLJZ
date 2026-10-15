package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultUserFSMKeyPrefix = "session:fsm:"
	defaultUserFSMTTL       = 5 * time.Minute
	defaultUserFSMTimeout   = 2 * time.Second
)

type UserInteractionState string

const (
	UserInteractionStateIdle            UserInteractionState = "IDLE"
	UserInteractionStateAwaitCustomRate UserInteractionState = "AWAIT_RATE"
	UserInteractionStateAwaitPayoutRate UserInteractionState = "AWAIT_PAYOUT_RATE"
)

type UserInteractionContext struct {
	State     UserInteractionState `json:"state"`
	Scene     string               `json:"scene,omitempty"`
	UpdatedAt int64                `json:"updated_at"`
}

type UserFSM interface {
	Get(ctx context.Context, chatID, userID int64) (UserInteractionContext, bool, error)
	Transition(ctx context.Context, chatID, userID int64, next UserInteractionContext) (UserInteractionContext, bool, error)
	Clear(ctx context.Context, chatID, userID int64) error
	Enabled() bool
	Close() error
}

type noopUserFSM struct{}

func (noopUserFSM) Get(context.Context, int64, int64) (UserInteractionContext, bool, error) {
	return UserInteractionContext{}, false, nil
}

func (noopUserFSM) Transition(context.Context, int64, int64, UserInteractionContext) (UserInteractionContext, bool, error) {
	return UserInteractionContext{}, false, nil
}

func (noopUserFSM) Clear(context.Context, int64, int64) error {
	return nil
}

func (noopUserFSM) Enabled() bool {
	return false
}

func (noopUserFSM) Close() error {
	return nil
}

func newDefaultUserFSM() UserFSM {
	return NewMemoryUserFSM(MemoryUserFSMConfig{})
}

type MemoryUserFSMConfig struct {
	TTL time.Duration
	Now func() time.Time
}

type MemoryUserFSM struct {
	mu     sync.Mutex
	states map[string]memoryUserFSMEntry
	ttl    time.Duration
	now    func() time.Time
}

type memoryUserFSMEntry struct {
	state   UserInteractionContext
	expires time.Time
}

func NewMemoryUserFSM(cfg MemoryUserFSMConfig) *MemoryUserFSM {
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultUserFSMTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &MemoryUserFSM{
		states: map[string]memoryUserFSMEntry{},
		ttl:    ttl,
		now:    now,
	}
}

func (m *MemoryUserFSM) Get(_ context.Context, chatID, userID int64) (UserInteractionContext, bool, error) {
	if m == nil {
		return UserInteractionContext{}, false, nil
	}
	if chatID == 0 || userID == 0 {
		return UserInteractionContext{}, false, nil
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gcLocked(now)
	entry, exists := m.states[m.stateKey(chatID, userID)]
	if !exists {
		return UserInteractionContext{}, false, nil
	}
	if !entry.expires.After(now) {
		delete(m.states, m.stateKey(chatID, userID))
		return UserInteractionContext{}, false, nil
	}
	return entry.state, true, nil
}

func (m *MemoryUserFSM) Transition(_ context.Context, chatID, userID int64, next UserInteractionContext) (UserInteractionContext, bool, error) {
	if m == nil {
		return UserInteractionContext{}, false, nil
	}
	if chatID == 0 || userID == 0 {
		return UserInteractionContext{}, false, fmt.Errorf("chat_id 和 user_id 不能为空")
	}
	now := m.now()
	normalized := normalizeUserInteractionContext(next, now)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.gcLocked(now)
	key := m.stateKey(chatID, userID)
	prevEntry, hadPrev := m.states[key]
	if normalized.State == UserInteractionStateIdle {
		delete(m.states, key)
		if !hadPrev {
			return UserInteractionContext{}, false, nil
		}
		return prevEntry.state, true, nil
	}
	m.states[key] = memoryUserFSMEntry{
		state:   normalized,
		expires: now.Add(m.ttl),
	}
	if !hadPrev {
		return UserInteractionContext{}, false, nil
	}
	return prevEntry.state, true, nil
}

func (m *MemoryUserFSM) Clear(_ context.Context, chatID, userID int64) error {
	if m == nil || chatID == 0 || userID == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, m.stateKey(chatID, userID))
	return nil
}

func (m *MemoryUserFSM) Enabled() bool {
	return m != nil
}

func (m *MemoryUserFSM) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = map[string]memoryUserFSMEntry{}
	return nil
}

func (m *MemoryUserFSM) gcLocked(now time.Time) {
	for key, entry := range m.states {
		if !entry.expires.After(now) {
			delete(m.states, key)
		}
	}
}

func (m *MemoryUserFSM) stateKey(chatID, userID int64) string {
	return fmt.Sprintf("%d:%d", chatID, userID)
}

type RedisUserFSMConfig struct {
	Addr      string
	Password  string
	DB        int
	KeyPrefix string
	TTL       time.Duration
	Timeout   time.Duration
}

type RedisUserFSM struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
	timeout   time.Duration
}

func NewRedisUserFSM(cfg RedisUserFSMConfig) (*RedisUserFSM, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, fmt.Errorf("redis 地址不能为空")
	}
	keyPrefix := strings.TrimSpace(cfg.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultUserFSMKeyPrefix
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultUserFSMTTL
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultUserFSMTimeout
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
	return &RedisUserFSM{
		client:    client,
		keyPrefix: keyPrefix,
		ttl:       ttl,
		timeout:   timeout,
	}, nil
}

func (r *RedisUserFSM) Get(ctx context.Context, chatID, userID int64) (UserInteractionContext, bool, error) {
	if r == nil || r.client == nil || chatID == 0 || userID == 0 {
		return UserInteractionContext{}, false, nil
	}
	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()
	key := r.stateKey(chatID, userID)
	raw, err := r.client.Get(runCtx, key).Result()
	if err == redis.Nil {
		return UserInteractionContext{}, false, nil
	}
	if err != nil {
		return UserInteractionContext{}, false, err
	}
	decoded, ok, decodeErr := decodeUserInteractionContext(raw)
	if decodeErr != nil {
		_, _ = r.client.Del(runCtx, key).Result()
		return UserInteractionContext{}, false, decodeErr
	}
	if !ok {
		_, _ = r.client.Del(runCtx, key).Result()
		return UserInteractionContext{}, false, nil
	}
	return decoded, true, nil
}

func (r *RedisUserFSM) Transition(ctx context.Context, chatID, userID int64, next UserInteractionContext) (UserInteractionContext, bool, error) {
	if r == nil || r.client == nil {
		return UserInteractionContext{}, false, fmt.Errorf("redis user fsm 未初始化")
	}
	if chatID == 0 || userID == 0 {
		return UserInteractionContext{}, false, fmt.Errorf("chat_id 和 user_id 不能为空")
	}
	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()

	key := r.stateKey(chatID, userID)
	normalized := normalizeUserInteractionContext(next, time.Now())
	if normalized.State == UserInteractionStateIdle {
		if err := r.client.Del(runCtx, key).Err(); err != nil {
			return UserInteractionContext{}, false, err
		}
		return UserInteractionContext{}, false, nil
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return UserInteractionContext{}, false, err
	}
	oldRaw, err := r.client.GetSet(runCtx, key, payload).Result()
	if err != nil && err != redis.Nil {
		return UserInteractionContext{}, false, err
	}
	if err := r.client.Expire(runCtx, key, r.ttl).Err(); err != nil {
		return UserInteractionContext{}, false, err
	}
	if err == redis.Nil {
		return UserInteractionContext{}, false, nil
	}
	previous, ok, decodeErr := decodeUserInteractionContext(oldRaw)
	if decodeErr != nil {
		return UserInteractionContext{}, false, decodeErr
	}
	if !ok {
		return UserInteractionContext{}, false, nil
	}
	return previous, true, nil
}

func (r *RedisUserFSM) Clear(ctx context.Context, chatID, userID int64) error {
	if r == nil || r.client == nil || chatID == 0 || userID == 0 {
		return nil
	}
	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()
	return r.client.Del(runCtx, r.stateKey(chatID, userID)).Err()
}

func (r *RedisUserFSM) Enabled() bool {
	return r != nil && r.client != nil
}

func (r *RedisUserFSM) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func (r *RedisUserFSM) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, r.timeout)
}

func (r *RedisUserFSM) stateKey(chatID, userID int64) string {
	return fmt.Sprintf("%s%d:%d", r.keyPrefix, chatID, userID)
}

func normalizeUserInteractionContext(state UserInteractionContext, now time.Time) UserInteractionContext {
	normalizedState := normalizeUserInteractionState(state.State)
	if normalizedState == UserInteractionStateIdle {
		return UserInteractionContext{
			State:     UserInteractionStateIdle,
			UpdatedAt: now.Unix(),
		}
	}
	updatedAt := state.UpdatedAt
	if updatedAt <= 0 {
		updatedAt = now.Unix()
	}
	return UserInteractionContext{
		State:     normalizedState,
		Scene:     strings.TrimSpace(state.Scene),
		UpdatedAt: updatedAt,
	}
}

func normalizeUserInteractionState(state UserInteractionState) UserInteractionState {
	switch strings.ToUpper(strings.TrimSpace(string(state))) {
	case string(UserInteractionStateAwaitCustomRate):
		return UserInteractionStateAwaitCustomRate
	case string(UserInteractionStateAwaitPayoutRate):
		return UserInteractionStateAwaitPayoutRate
	default:
		return UserInteractionStateIdle
	}
}

func decodeUserInteractionContext(raw string) (UserInteractionContext, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return UserInteractionContext{}, false, nil
	}
	var snapshot UserInteractionContext
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return UserInteractionContext{}, false, err
	}
	snapshot = normalizeUserInteractionContext(snapshot, time.Now())
	if snapshot.State == UserInteractionStateIdle {
		return UserInteractionContext{}, false, nil
	}
	return snapshot, true, nil
}
