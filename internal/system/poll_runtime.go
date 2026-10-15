package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const pollStatusVersion = 1

type PollErrorClass string

const (
	PollErrorClassNone              PollErrorClass = "none"
	PollErrorClassTransient         PollErrorClass = "transient"
	PollErrorClassReleaseRegression PollErrorClass = "release_regression"
	PollErrorClassConfiguration     PollErrorClass = "configuration"
	PollErrorClassOwnershipConflict PollErrorClass = "ownership_conflict"
	PollErrorClassRateLimited       PollErrorClass = "rate_limited"
	PollErrorClassUnknown           PollErrorClass = "unknown"
)

type PollStatus struct {
	Version              int            `json:"version"`
	StartedAt            time.Time      `json:"started_at,omitempty"`
	LastSuccessfulPollAt time.Time      `json:"last_successful_poll_at,omitempty"`
	LastAttemptAt        time.Time      `json:"last_attempt_at,omitempty"`
	LastErrorAt          time.Time      `json:"last_error_at,omitempty"`
	ConsecutiveFailures  int            `json:"consecutive_failures"`
	ConsecutiveSuccesses int            `json:"consecutive_successes"`
	LastErrorClass       PollErrorClass `json:"last_error_class"`
	LastError            string         `json:"last_error,omitempty"`
	NextOffset           int            `json:"next_offset"`
	ReleaseID            string         `json:"release_id,omitempty"`
	ReleaseSHA           string         `json:"release_sha,omitempty"`
}

type PollSuccess struct {
	At         time.Time
	NextOffset int
	ReleaseID  string
	ReleaseSHA string
}

type PollFailure struct {
	At    time.Time
	Class PollErrorClass
	Error string
}

type PollStatusStore struct {
	path string
	mu   sync.Mutex
}

func NewPollStatusStore(path string) *PollStatusStore {
	return &PollStatusStore{path: strings.TrimSpace(path)}
}

func (s *PollStatusStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *PollStatusStore) RecordSuccess(ctx context.Context, success PollSuccess) (PollStatus, error) {
	if err := contextErr(ctx); err != nil {
		return PollStatus{}, err
	}
	if s == nil || s.path == "" {
		return PollStatus{}, errors.New("poll status path is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	status, err := s.loadLocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return PollStatus{}, err
	}
	if status.Version == 0 {
		status.Version = pollStatusVersion
	}
	at := normalizePollTime(success.At)
	if status.StartedAt.IsZero() {
		status.StartedAt = at
	}
	releaseID := strings.TrimSpace(success.ReleaseID)
	releaseSHA := strings.TrimSpace(success.ReleaseSHA)
	if status.ReleaseID != "" && (status.ReleaseID != releaseID || status.ReleaseSHA != releaseSHA) {
		status.ConsecutiveSuccesses = 0
	}
	status.LastAttemptAt = at
	status.LastSuccessfulPollAt = at
	status.ConsecutiveFailures = 0
	status.ConsecutiveSuccesses++
	status.LastErrorClass = PollErrorClassNone
	status.LastError = ""
	status.LastErrorAt = time.Time{}
	status.NextOffset = success.NextOffset
	status.ReleaseID = releaseID
	status.ReleaseSHA = releaseSHA
	if err := s.writeLocked(status); err != nil {
		return PollStatus{}, err
	}
	return status, nil
}

func (s *PollStatusStore) MarkStarted(ctx context.Context, at time.Time, releaseID, releaseSHA string) (PollStatus, error) {
	if err := contextErr(ctx); err != nil {
		return PollStatus{}, err
	}
	if s == nil || s.path == "" {
		return PollStatus{}, errors.New("poll status path is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status, err := s.loadLocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return PollStatus{}, err
	}
	if status.Version == 0 {
		status.Version = pollStatusVersion
	}
	status.StartedAt = normalizePollTime(at)
	status.ConsecutiveSuccesses = 0
	status.ReleaseID = strings.TrimSpace(releaseID)
	status.ReleaseSHA = strings.TrimSpace(releaseSHA)
	if err := s.writeLocked(status); err != nil {
		return PollStatus{}, err
	}
	return status, nil
}

func (s *PollStatusStore) RecordFailure(ctx context.Context, failure PollFailure) (PollStatus, error) {
	if err := contextErr(ctx); err != nil {
		return PollStatus{}, err
	}
	if s == nil || s.path == "" {
		return PollStatus{}, errors.New("poll status path is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	status, err := s.loadLocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return PollStatus{}, err
	}
	if status.Version == 0 {
		status.Version = pollStatusVersion
	}
	at := normalizePollTime(failure.At)
	if status.StartedAt.IsZero() {
		status.StartedAt = at
	}
	status.LastAttemptAt = at
	status.LastErrorAt = at
	status.ConsecutiveFailures++
	status.ConsecutiveSuccesses = 0
	status.LastErrorClass = normalizePollErrorClass(failure.Class)
	status.LastError = sanitizePollError(failure.Error)
	if err := s.writeLocked(status); err != nil {
		return PollStatus{}, err
	}
	return status, nil
}

func (s *PollStatusStore) Load(ctx context.Context) (PollStatus, error) {
	if err := contextErr(ctx); err != nil {
		return PollStatus{}, err
	}
	if s == nil || s.path == "" {
		return PollStatus{}, errors.New("poll status path is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *PollStatusStore) loadLocked() (PollStatus, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return PollStatus{}, err
	}
	var status PollStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return PollStatus{}, fmt.Errorf("decode poll status: %w", err)
	}
	if status.Version != pollStatusVersion {
		return PollStatus{}, fmt.Errorf("unsupported poll status version: %d", status.Version)
	}
	status.LastErrorClass = normalizePollErrorClass(status.LastErrorClass)
	if status.ConsecutiveFailures < 0 {
		return PollStatus{}, errors.New("poll status consecutive failures must not be negative")
	}
	if status.ConsecutiveSuccesses < 0 {
		return PollStatus{}, errors.New("poll status consecutive successes must not be negative")
	}
	return status, nil
}

func (s *PollStatusStore) writeLocked(status PollStatus) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("create poll status directory: %w", err)
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("encode poll status: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".poll-status-*.tmp")
	if err != nil {
		return fmt.Errorf("create poll status temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return fmt.Errorf("set poll status permissions: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write poll status: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync poll status: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close poll status: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace poll status: %w", err)
	}
	return nil
}

type PollReadinessReason string

const (
	PollReadinessReasonHealthy  PollReadinessReason = "healthy"
	PollReadinessReasonStarting PollReadinessReason = "starting"
	PollReadinessReasonStale    PollReadinessReason = "stale"
	PollReadinessReasonMissing  PollReadinessReason = "missing"
	PollReadinessReasonInvalid  PollReadinessReason = "invalid"
)

type PollReadinessPolicy struct {
	StartedAt       time.Time
	StartupGrace    time.Duration
	MaxSuccessAge   time.Duration
	MinFailureCount int
}

type PollReadiness struct {
	Ready  bool
	Reason PollReadinessReason
}

func (s *PollStatusStore) Readiness(ctx context.Context, now time.Time, policy PollReadinessPolicy) PollReadiness {
	status, err := s.Load(ctx)
	if err != nil {
		starting := EvaluatePollReadiness(PollStatus{}, now, policy)
		if starting.Ready {
			return starting
		}
		if errors.Is(err, os.ErrNotExist) {
			return PollReadiness{Reason: PollReadinessReasonMissing}
		}
		return PollReadiness{Reason: PollReadinessReasonInvalid}
	}
	if policy.StartedAt.IsZero() {
		policy.StartedAt = status.StartedAt
	}
	return EvaluatePollReadiness(status, now, policy)
}

func EvaluatePollReadiness(status PollStatus, now time.Time, policy PollReadinessPolicy) PollReadiness {
	if now.IsZero() {
		now = time.Now()
	}
	policy = normalizePollReadinessPolicy(policy)
	if !policy.StartedAt.IsZero() && now.Before(policy.StartedAt.Add(policy.StartupGrace)) {
		return PollReadiness{Ready: true, Reason: PollReadinessReasonStarting}
	}
	if status.LastSuccessfulPollAt.IsZero() {
		return PollReadiness{Reason: PollReadinessReasonMissing}
	}
	if now.Sub(status.LastAttemptAt) > policy.MaxSuccessAge {
		return PollReadiness{Reason: PollReadinessReasonStale}
	}
	if now.Sub(status.LastSuccessfulPollAt) > policy.MaxSuccessAge && status.ConsecutiveFailures >= policy.MinFailureCount {
		return PollReadiness{Reason: PollReadinessReasonStale}
	}
	return PollReadiness{Ready: true, Reason: PollReadinessReasonHealthy}
}

func ClassifyPollError(err error) PollErrorClass {
	if err == nil {
		return PollErrorClassNone
	}
	if errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "context canceled") {
		return PollErrorClassReleaseRegression
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return PollErrorClassTransient
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "401"), strings.Contains(message, "unauthorized"):
		return PollErrorClassConfiguration
	case strings.Contains(message, "409"), strings.Contains(message, "conflict"):
		return PollErrorClassOwnershipConflict
	case strings.Contains(message, "429"), strings.Contains(message, "too many requests"):
		return PollErrorClassRateLimited
	case strings.Contains(message, "timeout"), strings.Contains(message, "connection"), strings.Contains(message, "tls"), strings.Contains(message, "dns"), strings.Contains(message, "temporary"):
		return PollErrorClassTransient
	default:
		return PollErrorClassUnknown
	}
}

func normalizePollReadinessPolicy(policy PollReadinessPolicy) PollReadinessPolicy {
	if policy.StartupGrace <= 0 {
		policy.StartupGrace = 90 * time.Second
	}
	if policy.MaxSuccessAge <= 0 {
		policy.MaxSuccessAge = 90 * time.Second
	}
	if policy.MinFailureCount <= 0 {
		policy.MinFailureCount = 3
	}
	return policy
}

func normalizePollErrorClass(class PollErrorClass) PollErrorClass {
	switch class {
	case PollErrorClassNone, PollErrorClassTransient, PollErrorClassReleaseRegression, PollErrorClassConfiguration, PollErrorClassOwnershipConflict, PollErrorClassRateLimited, PollErrorClassUnknown:
		return class
	default:
		return PollErrorClassUnknown
	}
}

func normalizePollTime(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now().UTC()
	}
	return at.UTC()
}

var (
	telegramBotPathTokenPattern = regexp.MustCompile(`(?i)(/bot)[^/\s"']+`)
	telegramBotTokenPattern     = regexp.MustCompile(`\b[0-9]{6,}:[A-Za-z0-9_-]{20,}\b`)
)

func SanitizeTelegramError(message string) string {
	message = strings.TrimSpace(message)
	message = telegramBotPathTokenPattern.ReplaceAllString(message, `${1}[REDACTED]`)
	message = telegramBotTokenPattern.ReplaceAllString(message, `[REDACTED]`)
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func sanitizePollError(message string) string {
	return SanitizeTelegramError(message)
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
