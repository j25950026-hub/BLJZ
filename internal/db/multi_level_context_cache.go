package db

import (
	"errors"
	"sync"
	"time"
)

const (
	defaultMemoryContextCacheTTL        = 30 * time.Second
	defaultMemoryContextCacheMaxEntries = 4096
)

type MemoryContextCacheConfig struct {
	TTL        time.Duration
	MaxEntries int
}

type memoryGroupCacheEntry struct {
	group     GroupConfig
	expiresAt time.Time
}

type memoryUserAccessCacheKey struct {
	chatID int64
	userID int64
}

type memoryUserAccessCacheEntry struct {
	access    UserAccessContext
	expiresAt time.Time
}

type MemoryContextCache struct {
	ttl        time.Duration
	maxEntries int

	mu         sync.RWMutex
	groups     map[int64]memoryGroupCacheEntry
	userAccess map[memoryUserAccessCacheKey]memoryUserAccessCacheEntry
}

func NewMemoryContextCache(cfg MemoryContextCacheConfig) *MemoryContextCache {
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultMemoryContextCacheTTL
	}
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultMemoryContextCacheMaxEntries
	}
	return &MemoryContextCache{
		ttl:        ttl,
		maxEntries: maxEntries,
		groups:     make(map[int64]memoryGroupCacheEntry),
		userAccess: make(map[memoryUserAccessCacheKey]memoryUserAccessCacheEntry),
	}
}

func (m *MemoryContextCache) GetGroupConfig(chatID int64) (GroupConfig, bool, error) {
	if m == nil || chatID == 0 {
		return GroupConfig{}, false, nil
	}
	m.mu.RLock()
	entry, ok := m.groups[chatID]
	m.mu.RUnlock()
	if !ok {
		return GroupConfig{}, false, nil
	}
	if m.expired(entry.expiresAt) {
		m.mu.Lock()
		if current, exists := m.groups[chatID]; exists && current.expiresAt.Equal(entry.expiresAt) {
			delete(m.groups, chatID)
		}
		m.mu.Unlock()
		return GroupConfig{}, false, nil
	}
	group := entry.group
	if group.ChatID == 0 {
		group.ChatID = chatID
	}
	return group, true, nil
}

func (m *MemoryContextCache) SetGroupConfig(group GroupConfig) error {
	if m == nil || group.ChatID == 0 {
		return nil
	}
	m.mu.Lock()
	m.evictExpiredGroupsLocked()
	if len(m.groups) >= m.maxEntries {
		m.evictOneGroupLocked()
	}
	m.groups[group.ChatID] = memoryGroupCacheEntry{group: group, expiresAt: time.Now().UTC().Add(m.ttl)}
	m.mu.Unlock()
	return nil
}

func (m *MemoryContextCache) DeleteGroupConfig(chatID int64) error {
	if m == nil || chatID == 0 {
		return nil
	}
	m.mu.Lock()
	delete(m.groups, chatID)
	m.mu.Unlock()
	return nil
}

func (m *MemoryContextCache) GetUserAccess(chatID, userID int64) (UserAccessContext, bool, error) {
	if m == nil || chatID == 0 || userID == 0 {
		return newMemberUserAccessContext(), false, nil
	}
	key := memoryUserAccessCacheKey{chatID: chatID, userID: userID}
	m.mu.RLock()
	entry, ok := m.userAccess[key]
	m.mu.RUnlock()
	if !ok {
		return newMemberUserAccessContext(), false, nil
	}
	if m.expired(entry.expiresAt) {
		m.mu.Lock()
		if current, exists := m.userAccess[key]; exists && current.expiresAt.Equal(entry.expiresAt) {
			delete(m.userAccess, key)
		}
		m.mu.Unlock()
		return newMemberUserAccessContext(), false, nil
	}
	return normalizeUserAccessContext(entry.access), true, nil
}

func (m *MemoryContextCache) SetUserAccess(chatID, userID int64, access UserAccessContext) error {
	if m == nil || chatID == 0 || userID == 0 {
		return nil
	}
	key := memoryUserAccessCacheKey{chatID: chatID, userID: userID}
	m.mu.Lock()
	m.evictExpiredUsersLocked()
	if len(m.userAccess) >= m.maxEntries {
		m.evictOneUserLocked()
	}
	m.userAccess[key] = memoryUserAccessCacheEntry{access: normalizeUserAccessContext(access), expiresAt: time.Now().UTC().Add(m.ttl)}
	m.mu.Unlock()
	return nil
}

func (m *MemoryContextCache) DeleteUserAccess(chatID, userID int64) error {
	if m == nil || chatID == 0 || userID == 0 {
		return nil
	}
	m.mu.Lock()
	delete(m.userAccess, memoryUserAccessCacheKey{chatID: chatID, userID: userID})
	m.mu.Unlock()
	return nil
}

func (m *MemoryContextCache) DeleteUserAccessByUserID(userID int64) error {
	if m == nil || userID == 0 {
		return nil
	}
	m.mu.Lock()
	for key := range m.userAccess {
		if key.userID != userID {
			continue
		}
		delete(m.userAccess, key)
	}
	m.mu.Unlock()
	return nil
}

func (m *MemoryContextCache) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	clear(m.groups)
	clear(m.userAccess)
	m.mu.Unlock()
	return nil
}

func (m *MemoryContextCache) expired(expiresAt time.Time) bool {
	if expiresAt.IsZero() {
		return false
	}
	return time.Now().UTC().After(expiresAt)
}

func (m *MemoryContextCache) evictExpiredGroupsLocked() {
	now := time.Now().UTC()
	for key, entry := range m.groups {
		if entry.expiresAt.IsZero() || now.Before(entry.expiresAt) {
			continue
		}
		delete(m.groups, key)
	}
}

func (m *MemoryContextCache) evictExpiredUsersLocked() {
	now := time.Now().UTC()
	for key, entry := range m.userAccess {
		if entry.expiresAt.IsZero() || now.Before(entry.expiresAt) {
			continue
		}
		delete(m.userAccess, key)
	}
}

func (m *MemoryContextCache) evictOneGroupLocked() {
	for key := range m.groups {
		delete(m.groups, key)
		return
	}
}

func (m *MemoryContextCache) evictOneUserLocked() {
	for key := range m.userAccess {
		delete(m.userAccess, key)
		return
	}
}

type MultiLevelContextCache struct {
	mu sync.RWMutex
	l1 ContextCache
	l2 ContextCache
}

func NewMultiLevelContextCache(l1 ContextCache, l2 ContextCache) *MultiLevelContextCache {
	if l1 == nil && l2 == nil {
		return nil
	}
	return &MultiLevelContextCache{l1: l1, l2: l2}
}

func (m *MultiLevelContextCache) GetGroupConfig(chatID int64) (GroupConfig, bool, error) {
	if m == nil || chatID == 0 {
		return GroupConfig{}, false, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.l1 != nil {
		if group, ok, err := m.l1.GetGroupConfig(chatID); err == nil && ok {
			return group, true, nil
		}
	}
	if m.l2 == nil {
		return GroupConfig{}, false, nil
	}
	group, ok, err := m.l2.GetGroupConfig(chatID)
	if err != nil || !ok {
		return GroupConfig{}, false, err
	}
	if m.l1 != nil {
		_ = m.l1.SetGroupConfig(group)
	}
	return group, true, nil
}

func (m *MultiLevelContextCache) SetGroupConfig(group GroupConfig) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var l1Err, l2Err error
	if m.l1 != nil {
		l1Err = m.l1.SetGroupConfig(group)
	}
	if m.l2 != nil {
		l2Err = m.l2.SetGroupConfig(group)
	}
	return errors.Join(l1Err, l2Err)
}

func (m *MultiLevelContextCache) DeleteGroupConfig(chatID int64) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var l1Err, l2Err error
	if m.l1 != nil {
		l1Err = m.l1.DeleteGroupConfig(chatID)
	}
	if m.l2 != nil {
		l2Err = m.l2.DeleteGroupConfig(chatID)
	}
	return errors.Join(l1Err, l2Err)
}

func (m *MultiLevelContextCache) GetUserAccess(chatID int64, userID int64) (UserAccessContext, bool, error) {
	if m == nil || chatID == 0 || userID == 0 {
		return newMemberUserAccessContext(), false, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.l1 != nil {
		if access, ok, err := m.l1.GetUserAccess(chatID, userID); err == nil && ok {
			return normalizeUserAccessContext(access), true, nil
		}
	}
	if m.l2 == nil {
		return newMemberUserAccessContext(), false, nil
	}
	access, ok, err := m.l2.GetUserAccess(chatID, userID)
	if err != nil || !ok {
		return newMemberUserAccessContext(), false, err
	}
	if m.l1 != nil {
		_ = m.l1.SetUserAccess(chatID, userID, access)
	}
	return normalizeUserAccessContext(access), true, nil
}

func (m *MultiLevelContextCache) SetUserAccess(chatID int64, userID int64, access UserAccessContext) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var l1Err, l2Err error
	if m.l1 != nil {
		l1Err = m.l1.SetUserAccess(chatID, userID, access)
	}
	if m.l2 != nil {
		l2Err = m.l2.SetUserAccess(chatID, userID, access)
	}
	return errors.Join(l1Err, l2Err)
}

func (m *MultiLevelContextCache) DeleteUserAccess(chatID int64, userID int64) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var l1Err, l2Err error
	if m.l1 != nil {
		l1Err = m.l1.DeleteUserAccess(chatID, userID)
	}
	if m.l2 != nil {
		l2Err = m.l2.DeleteUserAccess(chatID, userID)
	}
	return errors.Join(l1Err, l2Err)
}

func (m *MultiLevelContextCache) DeleteUserAccessByUserID(userID int64) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var l1Err, l2Err error
	if m.l1 != nil {
		l1Err = m.l1.DeleteUserAccessByUserID(userID)
	}
	if m.l2 != nil {
		l2Err = m.l2.DeleteUserAccessByUserID(userID)
	}
	return errors.Join(l1Err, l2Err)
}

func (m *MultiLevelContextCache) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var l1Err, l2Err error
	if m.l1 != nil {
		l1Err = m.l1.Close()
	}
	if m.l2 != nil {
		l2Err = m.l2.Close()
	}
	return errors.Join(l1Err, l2Err)
}
