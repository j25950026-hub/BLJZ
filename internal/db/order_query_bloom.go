package db

import (
	"database/sql"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"sync"
)

const (
	defaultOrderQueryBloomBits      = 1 << 22
	defaultOrderQueryBloomHashCount = 6
	orderQueryBloomNGramSize        = 3
	orderAmountScale                = 1_000_000
)

type orderQueryBloomFilter struct {
	mu        sync.RWMutex
	bits      []uint64
	bitSize   uint64
	hashCount uint64
}

func newOrderQueryBloomFilter(bitSize uint64, hashCount uint64) *orderQueryBloomFilter {
	if bitSize == 0 {
		bitSize = defaultOrderQueryBloomBits
	}
	if hashCount == 0 {
		hashCount = defaultOrderQueryBloomHashCount
	}
	wordCount := int((bitSize + 63) / 64)
	if wordCount <= 0 {
		wordCount = 1
	}
	return &orderQueryBloomFilter{
		bits:      make([]uint64, wordCount),
		bitSize:   uint64(wordCount) * 64,
		hashCount: hashCount,
	}
}

func (f *orderQueryBloomFilter) warmupFromDB(raw *sql.DB, decryptNote func(string) (string, error)) error {
	if f == nil || raw == nil {
		return nil
	}
	rows, err := raw.Query(`
SELECT chat_id, amount, note, username, nickname
FROM ledger_entries`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			chatID   int64
			amount   float64
			note     string
			username string
			nickname string
		)
		if err := rows.Scan(&chatID, &amount, &note, &username, &nickname); err != nil {
			return err
		}
		if decryptNote != nil {
			plainNote, err := decryptNote(note)
			if err != nil {
				return err
			}
			note = plainNote
		}
		f.addLedgerEntry(chatID, amount, note, username, nickname)
	}
	return rows.Err()
}

func (f *orderQueryBloomFilter) addLedgerEntry(chatID int64, amount float64, fields ...string) {
	if f == nil {
		return
	}
	tokens := make([]string, 0, 1+len(fields)*4)
	tokens = append(tokens, buildOrderAmountBloomToken(chatID, amount))
	for _, field := range fields {
		tokens = append(tokens, buildOrderTextBloomTokens(chatID, field)...)
	}
	f.addTokens(tokens)
}

func (f *orderQueryBloomFilter) mayContainQuery(chatID int64, keyword string, amount *float64) (bool, bool) {
	if f == nil {
		return true, false
	}
	tokens := buildOrderQueryBloomTokens(chatID, keyword, amount)
	if len(tokens) == 0 {
		return true, false
	}
	for _, token := range tokens {
		if !f.containsToken(token) {
			return false, true
		}
	}
	return true, true
}

func (f *orderQueryBloomFilter) addTokens(tokens []string) {
	if f == nil || len(tokens) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, token := range tokens {
		f.addTokenLocked(token)
	}
}

func (f *orderQueryBloomFilter) containsToken(token string) bool {
	if f == nil || token == "" {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.containsTokenLocked(token)
}

func (f *orderQueryBloomFilter) addTokenLocked(token string) {
	if token == "" || f.bitSize == 0 {
		return
	}
	h1, h2 := bloomHashes(token)
	for idx := uint64(0); idx < f.hashCount; idx++ {
		position := (h1 + idx*h2 + idx*idx) % f.bitSize
		wordIdx := position / 64
		bitIdx := position % 64
		f.bits[wordIdx] |= uint64(1) << bitIdx
	}
}

func (f *orderQueryBloomFilter) containsTokenLocked(token string) bool {
	if token == "" || f.bitSize == 0 {
		return false
	}
	h1, h2 := bloomHashes(token)
	for idx := uint64(0); idx < f.hashCount; idx++ {
		position := (h1 + idx*h2 + idx*idx) % f.bitSize
		wordIdx := position / 64
		bitIdx := position % 64
		if f.bits[wordIdx]&(uint64(1)<<bitIdx) == 0 {
			return false
		}
	}
	return true
}

func bloomHashes(token string) (uint64, uint64) {
	h1Hasher := fnv.New64a()
	_, _ = h1Hasher.Write([]byte(token))
	h1 := h1Hasher.Sum64()

	h2Hasher := fnv.New64()
	_, _ = h2Hasher.Write([]byte(token))
	h2 := h2Hasher.Sum64()
	if h2 == 0 {
		h2 = 0x9e3779b97f4a7c15
	}
	return h1, h2
}

func buildOrderQueryBloomTokens(chatID int64, keyword string, amount *float64) []string {
	out := make([]string, 0, 8)
	if amount != nil {
		out = append(out, buildOrderAmountBloomToken(chatID, *amount))
	}
	normalizedKeyword := normalizeOrderQueryText(keyword)
	if normalizedKeyword == "" {
		return out
	}
	grams := extractOrderTextNGrams(normalizedKeyword)
	if len(grams) == 0 {
		return out
	}
	for _, gram := range grams {
		out = append(out, buildOrderKeywordBloomToken(chatID, gram))
	}
	return out
}

func buildOrderTextBloomTokens(chatID int64, raw string) []string {
	normalized := normalizeOrderQueryText(raw)
	if normalized == "" {
		return nil
	}
	grams := extractOrderTextNGrams(normalized)
	if len(grams) == 0 {
		return nil
	}
	out := make([]string, 0, len(grams))
	for _, gram := range grams {
		out = append(out, buildOrderKeywordBloomToken(chatID, gram))
	}
	return out
}

func normalizeOrderQueryText(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(trimmed)
}

func extractOrderTextNGrams(text string) []string {
	runes := []rune(text)
	if len(runes) < orderQueryBloomNGramSize {
		return nil
	}
	seen := make(map[string]struct{}, len(runes)-orderQueryBloomNGramSize+1)
	out := make([]string, 0, len(runes)-orderQueryBloomNGramSize+1)
	for idx := 0; idx+orderQueryBloomNGramSize <= len(runes); idx++ {
		gram := string(runes[idx : idx+orderQueryBloomNGramSize])
		if _, exists := seen[gram]; exists {
			continue
		}
		seen[gram] = struct{}{}
		out = append(out, gram)
	}
	return out
}

func buildOrderKeywordBloomToken(chatID int64, gram string) string {
	return fmt.Sprintf("k:%d:%s", chatID, gram)
}

func buildOrderAmountBloomToken(chatID int64, amount float64) string {
	scaled := int64(math.Round(amount * orderAmountScale))
	return fmt.Sprintf("a:%d:%d", chatID, scaled)
}
