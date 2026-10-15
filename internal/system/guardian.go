package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type GuardianPhase string

const (
	GuardianPhaseHealthy    GuardianPhase = "healthy"
	GuardianPhaseRestarting GuardianPhase = "restarting"
	GuardianPhaseRecovered  GuardianPhase = "recovered"
	GuardianPhaseRolledBack GuardianPhase = "rolled_back"
	GuardianPhaseFrozen     GuardianPhase = "frozen"
)

type GuardianState struct {
	Version       int           `json:"version"`
	Phase         GuardianPhase `json:"phase"`
	LastAction    string        `json:"last_action,omitempty"`
	LastError     string        `json:"last_error,omitempty"`
	UpdatedAt     time.Time     `json:"updated_at,omitempty"`
	NextAllowedAt time.Time     `json:"next_allowed_at,omitempty"`
	TargetRelease string        `json:"target_release,omitempty"`
}

type GuardianController interface {
	Restart(context.Context) error
	WaitForReadiness(context.Context, time.Duration) bool
	CurrentRelease(context.Context) (string, error)
	SwitchCurrent(context.Context, string) error
}

type GuardianPolicy struct {
	StartedAt          time.Time
	StartupGrace       time.Duration
	MaxSuccessAge      time.Duration
	MinFailureCount    int
	Cooldown           time.Duration
	VerifyTimeout      time.Duration
	EnableAutoRollback bool
}

type GuardianOptions struct {
	PollStore    *PollStatusStore
	RequestStore *RecoveryRequestStore
	Controller   GuardianController
	ReleaseRoot  string
	Policy       GuardianPolicy
	State        GuardianState
	Now          func() time.Time
}

type Guardian struct {
	pollStore    *PollStatusStore
	requestStore *RecoveryRequestStore
	controller   GuardianController
	releaseRoot  string
	policy       GuardianPolicy
	state        GuardianState
	now          func() time.Time
}

func NewGuardian(opts GuardianOptions) *Guardian {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Guardian{
		pollStore:    opts.PollStore,
		requestStore: opts.RequestStore,
		controller:   opts.Controller,
		releaseRoot:  strings.TrimSpace(opts.ReleaseRoot),
		policy:       normalizeGuardianPolicy(opts.Policy),
		state:        normalizeGuardianState(opts.State),
		now:          now,
	}
}

func (g *Guardian) RunOnce(ctx context.Context) (GuardianState, error) {
	if g == nil || g.pollStore == nil {
		return GuardianState{}, errors.New("guardian poll store is nil")
	}
	now := g.now().UTC()
	manualRequest, manualRequested, err := g.takeManualRequest(ctx, now)
	if err != nil {
		return GuardianState{}, err
	}
	if manualRequested && manualRequest.Action == RecoveryActionRollback {
		return g.runTrialRollback(ctx, now)
	}
	readiness := g.pollStore.Readiness(ctx, now, PollReadinessPolicy{
		StartedAt:       g.policy.StartedAt,
		StartupGrace:    g.policy.StartupGrace,
		MaxSuccessAge:   g.policy.MaxSuccessAge,
		MinFailureCount: g.policy.MinFailureCount,
	})
	if readiness.Ready && !manualRequested {
		g.state = GuardianState{Version: recoveryProtocolVersion, Phase: GuardianPhaseHealthy, UpdatedAt: now}
		if status, err := g.pollStore.Load(ctx); err == nil {
			if promoted, promoteErr := PromoteCurrentRelease(g.releaseRoot, status); promoteErr != nil {
				g.state.LastAction = "last_known_good_promotion_failed"
				g.state.LastError = truncateGuardianError(promoteErr.Error())
			} else if promoted {
				g.state.LastAction = "promote_last_known_good"
			}
		}
		return g.state, nil
	}
	if !manualRequested && !g.state.NextAllowedAt.IsZero() && now.Before(g.state.NextAllowedAt) {
		return g.state, nil
	}

	status, err := g.pollStore.Load(ctx)
	if err != nil {
		status.LastErrorClass = PollErrorClassUnknown
		status.LastError = "poll status unavailable"
	}
	if !manualRequested && guardianMustFreeze(status.LastErrorClass) {
		g.state = g.freeze(now, "non-recoverable telegram error: "+string(status.LastErrorClass))
		return g.state, nil
	}
	if g.controller == nil {
		return GuardianState{}, errors.New("guardian controller is nil")
	}

	g.state = GuardianState{
		Version:       recoveryProtocolVersion,
		Phase:         GuardianPhaseRestarting,
		LastAction:    "restart",
		UpdatedAt:     now,
		NextAllowedAt: now.Add(g.policy.Cooldown),
	}
	if err := g.controller.Restart(ctx); err != nil {
		g.state = g.freeze(now, "restart failed: "+err.Error())
		return g.state, nil
	}
	if g.controller.WaitForReadiness(ctx, g.policy.VerifyTimeout) {
		g.state.Phase = GuardianPhaseRecovered
		g.state.LastError = ""
		return g.state, nil
	}
	if manualRequested || status.LastErrorClass != PollErrorClassReleaseRegression || !g.policy.EnableAutoRollback {
		g.state = g.freeze(now, "restart did not restore functional readiness")
		return g.state, nil
	}
	return g.runTrialRollback(ctx, now)
}

func (g *Guardian) takeManualRequest(ctx context.Context, now time.Time) (RecoveryRequest, bool, error) {
	if g.requestStore == nil {
		return RecoveryRequest{}, false, nil
	}
	return g.requestStore.Take(ctx, now)
}

func (g *Guardian) runTrialRollback(ctx context.Context, now time.Time) (GuardianState, error) {
	if g.controller == nil {
		return GuardianState{}, errors.New("guardian controller is nil")
	}
	if g.releaseRoot == "" {
		g.state = g.freeze(now, "no release root")
		return g.state, nil
	}
	candidate, err := ResolveRollbackCandidate(g.releaseRoot)
	if err != nil {
		g.state = g.freeze(now, "no eligible rollback candidate: "+err.Error())
		return g.state, nil
	}
	original, err := g.controller.CurrentRelease(ctx)
	if err != nil || strings.TrimSpace(original) == "" {
		g.state = g.freeze(now, "current release unavailable")
		return g.state, nil
	}
	if err := g.controller.SwitchCurrent(ctx, candidate.Directory); err != nil {
		g.state = g.freeze(now, "switch to last-known-good failed: "+err.Error())
		return g.state, nil
	}
	g.state.LastAction = "trial_rollback"
	g.state.TargetRelease = candidate.Manifest.ReleaseID
	if err := g.controller.Restart(ctx); err == nil && g.controller.WaitForReadiness(ctx, g.policy.VerifyTimeout) {
		g.state.Phase = GuardianPhaseRolledBack
		g.state.LastError = ""
		return g.state, nil
	}

	if restoreErr := g.controller.SwitchCurrent(ctx, original); restoreErr != nil {
		g.state = g.freeze(now, "rollback trial failed and original release restore failed: "+restoreErr.Error())
		return g.state, nil
	}
	_ = g.controller.Restart(ctx)
	g.state = g.freeze(now, "rollback trial did not restore functional readiness")
	return g.state, nil
}

func (g *Guardian) freeze(now time.Time, reason string) GuardianState {
	return GuardianState{
		Version:       recoveryProtocolVersion,
		Phase:         GuardianPhaseFrozen,
		LastAction:    g.state.LastAction,
		LastError:     truncateGuardianError(reason),
		UpdatedAt:     now,
		NextAllowedAt: now.Add(g.policy.Cooldown),
		TargetRelease: g.state.TargetRelease,
	}
}

func normalizeGuardianPolicy(policy GuardianPolicy) GuardianPolicy {
	readiness := normalizePollReadinessPolicy(PollReadinessPolicy{
		StartedAt:       policy.StartedAt,
		StartupGrace:    policy.StartupGrace,
		MaxSuccessAge:   policy.MaxSuccessAge,
		MinFailureCount: policy.MinFailureCount,
	})
	policy.StartedAt = readiness.StartedAt
	policy.StartupGrace = readiness.StartupGrace
	policy.MaxSuccessAge = readiness.MaxSuccessAge
	policy.MinFailureCount = readiness.MinFailureCount
	if policy.Cooldown <= 0 {
		policy.Cooldown = 15 * time.Minute
	}
	if policy.VerifyTimeout <= 0 {
		policy.VerifyTimeout = 90 * time.Second
	}
	return policy
}

func normalizeGuardianState(state GuardianState) GuardianState {
	if state.Version == 0 {
		state.Version = recoveryProtocolVersion
	}
	if state.Phase == "" {
		state.Phase = GuardianPhaseHealthy
	}
	return state
}

func guardianMustFreeze(class PollErrorClass) bool {
	return class == PollErrorClassConfiguration || class == PollErrorClassOwnershipConflict || class == PollErrorClassRateLimited
}

func truncateGuardianError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 512 {
		return message[:512]
	}
	return message
}

func (s GuardianState) String() string {
	return fmt.Sprintf("phase=%s action=%s", s.Phase, s.LastAction)
}
