package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type guardianControllerStub struct {
	restarts   int
	switches   []string
	current    string
	readiness  []bool
	restartErr error
	switchErr  error
}

func (s *guardianControllerStub) Restart(context.Context) error {
	s.restarts++
	return s.restartErr
}

func (s *guardianControllerStub) WaitForReadiness(context.Context, time.Duration) bool {
	if len(s.readiness) == 0 {
		return false
	}
	ready := s.readiness[0]
	s.readiness = s.readiness[1:]
	return ready
}

func (s *guardianControllerStub) CurrentRelease(context.Context) (string, error) {
	return s.current, nil
}

func (s *guardianControllerStub) SwitchCurrent(_ context.Context, directory string) error {
	if s.switchErr != nil {
		return s.switchErr
	}
	s.switches = append(s.switches, directory)
	s.current = directory
	return nil
}

func TestGuardianRunOnceRestartsStaleTransientPollOnce(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	pollStore := NewPollStatusStore(filepath.Join(t.TempDir(), "poll.json"))
	if _, err := pollStore.RecordFailure(context.Background(), PollFailure{
		At: now.Add(-91 * time.Second), Class: PollErrorClassTransient, Error: "timeout",
	}); err != nil {
		t.Fatalf("记录失败轮询: %v", err)
	}
	controller := &guardianControllerStub{readiness: []bool{true}, current: "current"}
	guardian := NewGuardian(GuardianOptions{
		PollStore:  pollStore,
		Controller: controller,
		Policy: GuardianPolicy{
			StartedAt:     now.Add(-time.Hour),
			MaxSuccessAge: 90 * time.Second,
		},
		Now: func() time.Time { return now },
	})

	state, err := guardian.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("执行 Guardian: %v", err)
	}
	if controller.restarts != 1 || state.Phase != GuardianPhaseRecovered {
		t.Fatalf("陈旧瞬时错误必须只重启并恢复: restarts=%d state=%#v", controller.restarts, state)
	}
	if len(controller.switches) != 0 {
		t.Fatalf("瞬时错误不得回滚: %#v", controller.switches)
	}
}

func TestGuardianRunOnceFreezesNonRecoverableTelegramError(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	pollStore := NewPollStatusStore(filepath.Join(t.TempDir(), "poll.json"))
	if _, err := pollStore.RecordFailure(context.Background(), PollFailure{
		At: now.Add(-91 * time.Second), Class: PollErrorClassConfiguration, Error: "401 unauthorized",
	}); err != nil {
		t.Fatalf("记录失败轮询: %v", err)
	}
	controller := &guardianControllerStub{}
	guardian := NewGuardian(GuardianOptions{
		PollStore:  pollStore,
		Controller: controller,
		Policy:     GuardianPolicy{StartedAt: now.Add(-time.Hour), MaxSuccessAge: 90 * time.Second},
		Now:        func() time.Time { return now },
	})

	state, err := guardian.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("执行 Guardian: %v", err)
	}
	if state.Phase != GuardianPhaseFrozen || controller.restarts != 0 {
		t.Fatalf("401 必须熔断且不重启: restarts=%d state=%#v", controller.restarts, state)
	}
}

func TestGuardianRunOnceRollsBackReleaseRegressionOnlyWhenRestartFails(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	root, candidate := writeGuardianCandidate(t, "release-good")
	pollStore := NewPollStatusStore(filepath.Join(t.TempDir(), "poll.json"))
	if _, err := pollStore.RecordFailure(context.Background(), PollFailure{
		At: now.Add(-91 * time.Second), Class: PollErrorClassReleaseRegression, Error: "context canceled",
	}); err != nil {
		t.Fatalf("记录失败轮询: %v", err)
	}
	controller := &guardianControllerStub{readiness: []bool{false, true}, current: "release-current"}
	guardian := NewGuardian(GuardianOptions{
		PollStore:   pollStore,
		Controller:  controller,
		ReleaseRoot: root,
		Policy: GuardianPolicy{
			StartedAt:          now.Add(-time.Hour),
			MaxSuccessAge:      90 * time.Second,
			EnableAutoRollback: true,
		},
		Now: func() time.Time { return now },
	})

	state, err := guardian.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("执行 Guardian: %v", err)
	}
	if state.Phase != GuardianPhaseRolledBack || controller.restarts != 2 || len(controller.switches) != 1 || controller.switches[0] != candidate {
		t.Fatalf("发布回归必须一次重启后试回滚: restarts=%d switches=%#v state=%#v", controller.restarts, controller.switches, state)
	}
}

func TestGuardianRunOnceRestartOnlyFreezesReleaseRegressionWithoutRollback(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	root, _ := writeGuardianCandidate(t, "release-good")
	pollStore := NewPollStatusStore(filepath.Join(t.TempDir(), "poll.json"))
	if _, err := pollStore.RecordFailure(context.Background(), PollFailure{
		At: now.Add(-91 * time.Second), Class: PollErrorClassReleaseRegression, Error: "context canceled",
	}); err != nil {
		t.Fatalf("记录失败轮询: %v", err)
	}
	controller := &guardianControllerStub{readiness: []bool{false}, current: "release-current"}
	guardian := NewGuardian(GuardianOptions{
		PollStore:   pollStore,
		Controller:  controller,
		ReleaseRoot: root,
		Policy:      GuardianPolicy{StartedAt: now.Add(-time.Hour), MaxSuccessAge: 90 * time.Second},
		Now:         func() time.Time { return now },
	})

	state, err := guardian.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("执行 restart-only Guardian: %v", err)
	}
	if state.Phase != GuardianPhaseFrozen || controller.restarts != 1 || len(controller.switches) != 0 {
		t.Fatalf("restart-only 模式必须只重启并冻结，不得回滚: restarts=%d switches=%#v state=%#v", controller.restarts, controller.switches, state)
	}
}

func TestGuardianRunOnceRestoresOriginalReleaseAfterFailedTrial(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	root, candidate := writeGuardianCandidate(t, "release-good")
	pollStore := NewPollStatusStore(filepath.Join(t.TempDir(), "poll.json"))
	if _, err := pollStore.RecordFailure(context.Background(), PollFailure{
		At: now.Add(-91 * time.Second), Class: PollErrorClassReleaseRegression, Error: "context canceled",
	}); err != nil {
		t.Fatalf("记录失败轮询: %v", err)
	}
	controller := &guardianControllerStub{readiness: []bool{false, false}, current: "release-current"}
	guardian := NewGuardian(GuardianOptions{
		PollStore:   pollStore,
		Controller:  controller,
		ReleaseRoot: root,
		Policy:      GuardianPolicy{StartedAt: now.Add(-time.Hour), MaxSuccessAge: 90 * time.Second, EnableAutoRollback: true},
		Now:         func() time.Time { return now },
	})

	state, err := guardian.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("执行 Guardian: %v", err)
	}
	if state.Phase != GuardianPhaseFrozen || controller.restarts != 3 || len(controller.switches) != 2 || controller.switches[0] != candidate || controller.switches[1] != "release-current" {
		t.Fatalf("失败试回滚必须恢复原版本并熔断: restarts=%d switches=%#v state=%#v", controller.restarts, controller.switches, state)
	}
}

func TestGuardianRunOnceHonorsCooldown(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	pollStore := NewPollStatusStore(filepath.Join(t.TempDir(), "poll.json"))
	if _, err := pollStore.RecordFailure(context.Background(), PollFailure{
		At: now.Add(-91 * time.Second), Class: PollErrorClassTransient, Error: "timeout",
	}); err != nil {
		t.Fatalf("记录失败轮询: %v", err)
	}
	controller := &guardianControllerStub{}
	guardian := NewGuardian(GuardianOptions{
		PollStore:  pollStore,
		Controller: controller,
		State:      GuardianState{Phase: GuardianPhaseRecovered, NextAllowedAt: now.Add(time.Minute)},
		Policy:     GuardianPolicy{StartedAt: now.Add(-time.Hour), MaxSuccessAge: 90 * time.Second},
		Now:        func() time.Time { return now },
	})

	state, err := guardian.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("执行 Guardian: %v", err)
	}
	if controller.restarts != 0 || state.Phase != GuardianPhaseRecovered {
		t.Fatalf("冷却期间不得操作: restarts=%d state=%#v", controller.restarts, state)
	}
}

func TestGuardianRunOnceHonorsManualRollbackRequest(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	root, candidate := writeGuardianCandidate(t, "release-good")
	pollStore := NewPollStatusStore(filepath.Join(t.TempDir(), "poll.json"))
	if _, err := pollStore.RecordSuccess(context.Background(), PollSuccess{At: now.Add(-time.Second)}); err != nil {
		t.Fatalf("记录健康轮询: %v", err)
	}
	requestStore := NewRecoveryRequestStore(filepath.Join(t.TempDir(), "recovery-request.json"))
	if err := requestStore.Write(context.Background(), RecoveryRequest{
		Version: recoveryProtocolVersion, ID: "manual-rollback", Action: RecoveryActionRollback,
		RequestedAt: now, ExpiresAt: now.Add(time.Minute), Actor: "primary-admin",
	}); err != nil {
		t.Fatalf("写入手工回滚请求: %v", err)
	}
	controller := &guardianControllerStub{readiness: []bool{true}, current: "release-current"}
	guardian := NewGuardian(GuardianOptions{
		PollStore:    pollStore,
		RequestStore: requestStore,
		Controller:   controller,
		ReleaseRoot:  root,
		Policy:       GuardianPolicy{StartedAt: now.Add(-time.Hour), MaxSuccessAge: 90 * time.Second},
		Now:          func() time.Time { return now },
	})

	state, err := guardian.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("执行 Guardian: %v", err)
	}
	if state.Phase != GuardianPhaseRolledBack || controller.restarts != 1 || len(controller.switches) != 1 || controller.switches[0] != candidate {
		t.Fatalf("手工回滚请求必须执行一次受控回滚: restarts=%d switches=%#v state=%#v", controller.restarts, controller.switches, state)
	}
}

func writeGuardianCandidate(t *testing.T, releaseID string) (string, string) {
	t.Helper()
	root := t.TempDir()
	releaseDir := filepath.Join(root, "releases", releaseID)
	if err := os.MkdirAll(releaseDir, 0o750); err != nil {
		t.Fatalf("创建发布目录: %v", err)
	}
	binary := []byte(releaseID)
	if err := os.WriteFile(filepath.Join(releaseDir, "ledgerbot"), binary, 0o750); err != nil {
		t.Fatalf("写入发布二进制: %v", err)
	}
	sum := sha256.Sum256(binary)
	if err := WriteReleaseManifest(filepath.Join(releaseDir, "manifest.json"), ReleaseManifest{
		Version: recoveryProtocolVersion, ReleaseID: releaseID, LedgerbotSHA256: hex.EncodeToString(sum[:]), RollbackEligible: true,
	}); err != nil {
		t.Fatalf("写入 release manifest: %v", err)
	}
	if err := os.Symlink(releaseDir, filepath.Join(root, "last-known-good")); err != nil {
		t.Fatalf("创建 last-known-good: %v", err)
	}
	if err := os.Symlink(releaseDir, filepath.Join(root, "rollback-candidate")); err != nil {
		t.Fatalf("创建 rollback-candidate: %v", err)
	}
	return root, releaseDir
}
