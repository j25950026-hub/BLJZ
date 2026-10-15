package db

import (
	"strings"
	"sync"
)

type hotAccountKey struct {
	userID   int64
	currency string
}

type hotAccountBatcher struct {
	mu sync.Mutex

	hotUsers  map[int64]struct{}
	batchSize int

	pendingDelta map[hotAccountKey]float64
	pendingCount map[hotAccountKey]int
}

func newHotAccountBatcher(userIDs []int64, batchSize int) *hotAccountBatcher {
	if len(userIDs) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = defaultHotAccountBatchSize
	}
	users := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		users[userID] = struct{}{}
	}
	if len(users) == 0 {
		return nil
	}
	return &hotAccountBatcher{
		hotUsers:     users,
		batchSize:    batchSize,
		pendingDelta: make(map[hotAccountKey]float64),
		pendingCount: make(map[hotAccountKey]int),
	}
}

func (b *hotAccountBatcher) shouldBatch(userID int64, currency string) bool {
	if b == nil || userID <= 0 {
		return false
	}
	normalized := strings.ToUpper(strings.TrimSpace(currency))
	if normalized == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.hotUsers[userID]
	return ok
}

func (b *hotAccountBatcher) addDelta(userID int64, currency string, delta float64) bool {
	if b == nil || delta == 0 {
		return false
	}
	normalized := strings.ToUpper(strings.TrimSpace(currency))
	if normalized == "" {
		return false
	}
	key := hotAccountKey{userID: userID, currency: normalized}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pendingDelta[key] += delta
	b.pendingCount[key]++
	return b.pendingCount[key] >= b.batchSize
}

func (b *hotAccountBatcher) pending(userID int64, currency string) float64 {
	if b == nil {
		return 0
	}
	normalized := strings.ToUpper(strings.TrimSpace(currency))
	if normalized == "" {
		return 0
	}
	key := hotAccountKey{userID: userID, currency: normalized}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pendingDelta[key]
}

func (b *hotAccountBatcher) drainAll() map[hotAccountKey]float64 {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pendingDelta) == 0 {
		return nil
	}
	out := make(map[hotAccountKey]float64, len(b.pendingDelta))
	for key, delta := range b.pendingDelta {
		out[key] = delta
	}
	b.pendingDelta = make(map[hotAccountKey]float64)
	b.pendingCount = make(map[hotAccountKey]int)
	return out
}

func (b *hotAccountBatcher) mergeBack(deltaByAccount map[hotAccountKey]float64) {
	if b == nil || len(deltaByAccount) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, delta := range deltaByAccount {
		if delta == 0 {
			continue
		}
		b.pendingDelta[key] += delta
		b.pendingCount[key]++
	}
}
