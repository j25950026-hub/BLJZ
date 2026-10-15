package db

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	permissionLevelNone     = 0
	permissionLevelOperator = 1
	permissionLevelOwner    = 2

	userRoleMember   = "member"
	userRoleOperator = "operator"
	userRoleOwner    = "owner"

	defaultContextCacheKeyPrefix = "ctxcache:"
	defaultContextCacheTTL       = 5 * time.Minute
	defaultContextCacheTimeout   = 2 * time.Second
)

type UserAccessContext struct {
	Role            string `json:"role"`
	PermissionLevel int    `json:"permission_level"`
	IsOwner         bool   `json:"is_owner"`
	IsOperator      bool   `json:"is_operator"`
}

func newMemberUserAccessContext() UserAccessContext {
	return UserAccessContext{
		Role:            userRoleMember,
		PermissionLevel: permissionLevelNone,
		IsOwner:         false,
		IsOperator:      false,
	}
}

func buildUserAccessContext(isOwner bool, isOperator bool) UserAccessContext {
	access := newMemberUserAccessContext()
	access.IsOwner = isOwner
	access.IsOperator = isOperator
	switch {
	case isOwner:
		access.Role = userRoleOwner
		access.PermissionLevel = permissionLevelOwner
	case isOperator:
		access.Role = userRoleOperator
		access.PermissionLevel = permissionLevelOperator
	default:
		access.Role = userRoleMember
		access.PermissionLevel = permissionLevelNone
	}
	return access
}

func normalizeUserAccessContext(access UserAccessContext) UserAccessContext {
	switch access.Role {
	case userRoleOwner:
		access.IsOwner = true
		access.PermissionLevel = permissionLevelOwner
	case userRoleOperator:
		access.IsOwner = false
		access.IsOperator = true
		access.PermissionLevel = permissionLevelOperator
	default:
		if access.IsOwner {
			access.Role = userRoleOwner
			access.PermissionLevel = permissionLevelOwner
			break
		}
		if access.IsOperator {
			access.Role = userRoleOperator
			access.PermissionLevel = permissionLevelOperator
			break
		}
		access.Role = userRoleMember
		access.PermissionLevel = permissionLevelNone
		access.IsOwner = false
		access.IsOperator = false
	}
	if access.PermissionLevel < permissionLevelNone {
		access.PermissionLevel = permissionLevelNone
	}
	if access.PermissionLevel > permissionLevelOwner {
		access.PermissionLevel = permissionLevelOwner
	}
	return access
}

type ContextCache interface {
	GetGroupConfig(chatID int64) (GroupConfig, bool, error)
	SetGroupConfig(group GroupConfig) error
	DeleteGroupConfig(chatID int64) error
	GetUserAccess(chatID int64, userID int64) (UserAccessContext, bool, error)
	SetUserAccess(chatID int64, userID int64, access UserAccessContext) error
	DeleteUserAccess(chatID int64, userID int64) error
	DeleteUserAccessByUserID(userID int64) error
	Close() error
}

type RedisContextCacheConfig struct {
	Addr      string
	Password  string
	DB        int
	KeyPrefix string
	TTL       time.Duration
	Timeout   time.Duration
}

type RedisContextCache struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
	timeout   time.Duration
}

func NewRedisContextCache(cfg RedisContextCacheConfig) (*RedisContextCache, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, fmt.Errorf("redis 地址不能为空")
	}
	keyPrefix := strings.TrimSpace(cfg.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultContextCacheKeyPrefix
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultContextCacheTTL
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultContextCacheTimeout
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
	return &RedisContextCache{
		client:    client,
		keyPrefix: keyPrefix,
		ttl:       ttl,
		timeout:   timeout,
	}, nil
}

func (r *RedisContextCache) GetGroupConfig(chatID int64) (GroupConfig, bool, error) {
	if r == nil || r.client == nil || chatID == 0 {
		return GroupConfig{}, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	raw, err := r.client.Get(ctx, r.groupKey(chatID)).Bytes()
	if err == redis.Nil {
		return GroupConfig{}, false, nil
	}
	if err != nil {
		return GroupConfig{}, false, err
	}
	var group GroupConfig
	if err := json.Unmarshal(raw, &group); err != nil {
		_, _ = r.client.Del(ctx, r.groupKey(chatID)).Result()
		return GroupConfig{}, false, err
	}
	if group.ChatID == 0 {
		group.ChatID = chatID
	}
	return group, true, nil
}

func (r *RedisContextCache) SetGroupConfig(group GroupConfig) error {
	if r == nil || r.client == nil || group.ChatID == 0 {
		return nil
	}
	payload, err := json.Marshal(group)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.client.Set(ctx, r.groupKey(group.ChatID), payload, r.ttl).Err()
}

func (r *RedisContextCache) DeleteGroupConfig(chatID int64) error {
	if r == nil || r.client == nil || chatID == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.client.Del(ctx, r.groupKey(chatID)).Err()
}

func (r *RedisContextCache) GetUserAccess(chatID int64, userID int64) (UserAccessContext, bool, error) {
	if r == nil || r.client == nil || chatID == 0 || userID == 0 {
		return newMemberUserAccessContext(), false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	raw, err := r.client.Get(ctx, r.userKey(chatID, userID)).Bytes()
	if err == redis.Nil {
		return newMemberUserAccessContext(), false, nil
	}
	if err != nil {
		return newMemberUserAccessContext(), false, err
	}
	var access UserAccessContext
	if err := json.Unmarshal(raw, &access); err != nil {
		_, _ = r.client.Del(ctx, r.userKey(chatID, userID)).Result()
		return newMemberUserAccessContext(), false, err
	}
	return normalizeUserAccessContext(access), true, nil
}

func (r *RedisContextCache) SetUserAccess(chatID int64, userID int64, access UserAccessContext) error {
	if r == nil || r.client == nil || chatID == 0 || userID == 0 {
		return nil
	}
	payload, err := json.Marshal(normalizeUserAccessContext(access))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.client.Set(ctx, r.userKey(chatID, userID), payload, r.ttl).Err()
}

func (r *RedisContextCache) DeleteUserAccess(chatID int64, userID int64) error {
	if r == nil || r.client == nil || chatID == 0 || userID == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.client.Del(ctx, r.userKey(chatID, userID)).Err()
}

func (r *RedisContextCache) DeleteUserAccessByUserID(userID int64) error {
	if r == nil || r.client == nil || userID == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	pattern := fmt.Sprintf("%suser:*:%d", r.keyPrefix, userID)
	var cursor uint64
	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := r.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		if nextCursor == 0 {
			return nil
		}
		cursor = nextCursor
	}
}

func (r *RedisContextCache) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func (r *RedisContextCache) groupKey(chatID int64) string {
	return fmt.Sprintf("%sgroup:%d", r.keyPrefix, chatID)
}

func (r *RedisContextCache) userKey(chatID int64, userID int64) string {
	return fmt.Sprintf("%suser:%d:%d", r.keyPrefix, chatID, userID)
}

func parseAnyInt64(raw any) (int64, bool) {
	switch value := raw.(type) {
	case int:
		return int64(value), true
	case int8:
		return int64(value), true
	case int16:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case uint:
		return int64(value), true
	case uint8:
		return int64(value), true
	case uint16:
		return int64(value), true
	case uint32:
		return int64(value), true
	case uint64:
		if value > math.MaxInt64 {
			return 0, false
		}
		return int64(value), true
	case float32:
		return int64(value), true
	case float64:
		return int64(value), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
