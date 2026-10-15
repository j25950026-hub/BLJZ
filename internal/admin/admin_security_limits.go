package admin

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	adminSecurityFailureLimit = 5
	adminSecurityBlockWindow  = time.Minute
)

type adminSecurityAttempt struct {
	Failures   int
	BlockedTil time.Time
}

type adminSecurityLimiter struct {
	mu      sync.Mutex
	attempt map[string]adminSecurityAttempt
}

func newAdminSecurityLimiter() *adminSecurityLimiter {
	return &adminSecurityLimiter{attempt: make(map[string]adminSecurityAttempt)}
}

func (l *adminSecurityLimiter) allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.attempt[key]
	if state.BlockedTil.After(now) {
		return false
	}
	if !state.BlockedTil.IsZero() && !state.BlockedTil.After(now) {
		state = adminSecurityAttempt{}
		l.attempt[key] = state
	}
	return true
}

func (l *adminSecurityLimiter) failure(key string, now time.Time) {
	if l == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.attempt[key]
	if state.BlockedTil.After(now) {
		return
	}
	state.Failures++
	if state.Failures >= adminSecurityFailureLimit {
		state.BlockedTil = now.Add(adminSecurityBlockWindow)
	}
	l.attempt[key] = state
}

func (l *adminSecurityLimiter) success(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.attempt, strings.TrimSpace(key))
	l.mu.Unlock()
}

func adminSecurityLimitKey(r *http.Request, username string) string {
	remote := ""
	if r != nil {
		remote = strings.TrimSpace(r.RemoteAddr)
	}
	return remote + "|" + strings.TrimSpace(username)
}
