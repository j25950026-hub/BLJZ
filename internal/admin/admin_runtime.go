package admin

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-bot/internal/system"
)

const adminRecoveryRequestTTL = 5 * time.Minute

type adminRecoveryRequestBody struct {
	Action string `json:"action"`
}

type adminRuntimeReadiness struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason"`
}

type adminRuntimePollSnapshot struct {
	Available            bool                  `json:"available"`
	StartedAt            time.Time             `json:"started_at,omitempty"`
	LastSuccessfulPollAt time.Time             `json:"last_successful_poll_at,omitempty"`
	LastAttemptAt        time.Time             `json:"last_attempt_at,omitempty"`
	LastErrorAt          time.Time             `json:"last_error_at,omitempty"`
	ConsecutiveFailures  int                   `json:"consecutive_failures"`
	LastErrorClass       system.PollErrorClass `json:"last_error_class"`
	NextOffset           int                   `json:"next_offset"`
	ReleaseID            string                `json:"release_id,omitempty"`
	ReleaseSHA           string                `json:"release_sha,omitempty"`
	Readiness            adminRuntimeReadiness `json:"readiness"`
}

type adminRuntimeGuardianSnapshot struct {
	Available     bool                 `json:"available"`
	Phase         system.GuardianPhase `json:"phase"`
	LastAction    string               `json:"last_action,omitempty"`
	UpdatedAt     time.Time            `json:"updated_at,omitempty"`
	NextAllowedAt time.Time            `json:"next_allowed_at,omitempty"`
	TargetRelease string               `json:"target_release,omitempty"`
}

type adminRuntimeStatusData struct {
	Poll     adminRuntimePollSnapshot     `json:"poll"`
	Guardian adminRuntimeGuardianSnapshot `json:"guardian"`
}

func (s *Server) handleAdminRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	now := time.Now().UTC()
	pollStore := system.NewPollStatusStore(s.cfg.PollStatusPath)
	readiness := pollStore.Readiness(r.Context(), now, system.PollReadinessPolicy{
		StartupGrace:    s.cfg.PollStartupGrace,
		MaxSuccessAge:   s.cfg.PollMaxSuccessAge,
		MinFailureCount: s.cfg.PollMinFailureCount,
	})
	poll := adminRuntimePollSnapshot{
		Readiness: adminRuntimeReadiness{Ready: readiness.Ready, Reason: string(readiness.Reason)},
	}
	if status, err := pollStore.Load(r.Context()); err == nil {
		poll = adminPollSnapshot(status, readiness)
	} else if !errors.Is(err, os.ErrNotExist) {
		poll.Readiness.Reason = string(system.PollReadinessReasonInvalid)
	}

	guardian := adminRuntimeGuardianSnapshot{Phase: system.GuardianPhaseHealthy}
	if state, err := system.NewGuardianStateStore(s.guardianStatePath()).Load(r.Context()); err == nil {
		guardian = adminGuardianSnapshot(state)
	}

	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": adminRuntimeStatusData{Poll: poll, Guardian: guardian},
	})
}

func (s *Server) handleAdminRecoveryRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	session, ok := s.requirePrimarySessionRecovery(w, r)
	if !ok {
		return
	}
	var body adminRecoveryRequestBody
	if err := decodeLimitedAdminJSON(w, r, &body); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid recovery request"})
		return
	}
	action := system.RecoveryAction(strings.TrimSpace(body.Action))
	if action != system.RecoveryActionRecover && action != system.RecoveryActionRollback {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unsupported recovery action"})
		return
	}
	id, err := randomAdminToken(24)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "recovery request unavailable"})
		return
	}
	now := time.Now().UTC()
	request := system.RecoveryRequest{
		ID:          id,
		Action:      action,
		RequestedAt: now,
		ExpiresAt:   now.Add(adminRecoveryRequestTTL),
		Actor:       session.Username,
	}
	if err := system.NewRecoveryRequestStore(s.recoveryRequestPath()).Write(context.Background(), request); err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "recovery request unavailable"})
		return
	}
	s.writeAdminJSON(w, http.StatusAccepted, map[string]any{
		"ok": true,
		"data": map[string]any{
			"request_id": request.ID,
			"action":     request.Action,
			"expires_at": request.ExpiresAt,
		},
	})
}

func (s *Server) recoveryRuntimeDirectory() string {
	path := strings.TrimSpace(s.cfg.PollStatusPath)
	if path == "" {
		path = filepath.FromSlash("data/recovery/poll-status.json")
	}
	return filepath.Dir(path)
}

func (s *Server) guardianStatePath() string {
	return filepath.Join(s.recoveryRuntimeDirectory(), "guardian-state.json")
}

func (s *Server) recoveryRequestPath() string {
	return filepath.Join(s.recoveryRuntimeDirectory(), "recovery-request.json")
}

func adminPollSnapshot(status system.PollStatus, readiness system.PollReadiness) adminRuntimePollSnapshot {
	return adminRuntimePollSnapshot{
		Available:            true,
		StartedAt:            status.StartedAt,
		LastSuccessfulPollAt: status.LastSuccessfulPollAt,
		LastAttemptAt:        status.LastAttemptAt,
		LastErrorAt:          status.LastErrorAt,
		ConsecutiveFailures:  status.ConsecutiveFailures,
		LastErrorClass:       status.LastErrorClass,
		NextOffset:           status.NextOffset,
		ReleaseID:            status.ReleaseID,
		ReleaseSHA:           status.ReleaseSHA,
		Readiness: adminRuntimeReadiness{
			Ready:  readiness.Ready,
			Reason: string(readiness.Reason),
		},
	}
}

func adminGuardianSnapshot(state system.GuardianState) adminRuntimeGuardianSnapshot {
	return adminRuntimeGuardianSnapshot{
		Available:     true,
		Phase:         state.Phase,
		LastAction:    state.LastAction,
		UpdatedAt:     state.UpdatedAt,
		NextAllowedAt: state.NextAllowedAt,
		TargetRelease: state.TargetRelease,
	}
}
