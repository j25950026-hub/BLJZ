package bot

import (
	"strings"
	"sync"
	"time"
)

const defaultRequestResultCacheTTL = 2 * time.Hour

type requestResultCacheEntry struct {
	chatID    int64
	response  string
	expiresAt time.Time
}

type requestResultCache struct {
	ttl   time.Duration
	mu    sync.RWMutex
	items map[string]requestResultCacheEntry
}

func newRequestResultCache(ttl time.Duration) *requestResultCache {
	if ttl <= 0 {
		ttl = defaultRequestResultCacheTTL
	}
	return &requestResultCache{
		ttl:   ttl,
		items: make(map[string]requestResultCacheEntry),
	}
}

func (c *requestResultCache) Get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		return "", false
	}
	now := time.Now().UTC()

	c.mu.RLock()
	entry, exists := c.items[normalizedKey]
	c.mu.RUnlock()
	if !exists {
		return "", false
	}
	if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
		c.mu.Lock()
		if current, ok := c.items[normalizedKey]; ok && current.expiresAt.Equal(entry.expiresAt) {
			delete(c.items, normalizedKey)
		}
		c.mu.Unlock()
		return "", false
	}
	return entry.response, true
}

func (c *requestResultCache) Set(key string, chatID int64, response string) {
	if c == nil {
		return
	}
	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" || strings.TrimSpace(response) == "" {
		return
	}
	c.mu.Lock()
	c.items[normalizedKey] = requestResultCacheEntry{
		chatID:    chatID,
		response:  response,
		expiresAt: time.Now().UTC().Add(c.ttl),
	}
	c.mu.Unlock()
}

func (c *requestResultCache) ClearChat(chatID int64) int {
	if c == nil || chatID == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	for key, entry := range c.items {
		if entry.chatID != chatID {
			continue
		}
		delete(c.items, key)
		removed++
	}
	return removed
}

func buildRequestResultCacheKey(commandKey string, chatID int64, messageID int) string {
	if chatID == 0 || messageID <= 0 {
		return ""
	}
	return buildRequestLockKey(commandKey, chatID, messageID)
}
