package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go-bot/internal/system"
)

type guardianConfig struct {
	PollStatusPath     string
	StatePath          string
	RequestPath        string
	ReleaseRoot        string
	ServiceName        string
	StartupGrace       time.Duration
	MaxSuccessAge      time.Duration
	MinFailureCount    int
	Cooldown           time.Duration
	VerifyTimeout      time.Duration
	EnableAutoRollback bool
}

func main() {
	if err := runGuardian(context.Background(), os.Args[1:], os.Getenv, log.Printf); err != nil {
		log.Printf("LedgerBot Guardian 失败: %v", err)
		os.Exit(1)
	}
}

func runGuardian(ctx context.Context, args []string, getenv func(string) string, logf func(string, ...any)) error {
	flags := flag.NewFlagSet("ledgerbot-guardian", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dryRun := flags.Bool("dry-run", false, "inspect state without changing the service or release")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected Guardian arguments: %s", strings.Join(flags.Args(), " "))
	}
	config, err := loadGuardianConfig(getenv)
	if err != nil {
		return err
	}
	pollStore := system.NewPollStatusStore(config.PollStatusPath)
	stateStore := system.NewGuardianStateStore(config.StatePath)
	requestStore := system.NewRecoveryRequestStore(config.RequestPath)
	if *dryRun {
		return logGuardianDryRun(ctx, pollStore, stateStore, config, logf)
	}
	state, err := stateStore.Load(ctx)
	if err != nil {
		return err
	}
	controller := &systemdGuardianController{
		serviceName: config.ServiceName,
		releaseRoot: config.ReleaseRoot,
		pollStore:   pollStore,
		policy: system.PollReadinessPolicy{
			StartupGrace:    config.StartupGrace,
			MaxSuccessAge:   config.MaxSuccessAge,
			MinFailureCount: config.MinFailureCount,
		},
	}
	guardian := system.NewGuardian(system.GuardianOptions{
		PollStore:    pollStore,
		RequestStore: requestStore,
		Controller:   controller,
		ReleaseRoot:  config.ReleaseRoot,
		State:        state,
		Policy: system.GuardianPolicy{
			StartupGrace:       config.StartupGrace,
			MaxSuccessAge:      config.MaxSuccessAge,
			MinFailureCount:    config.MinFailureCount,
			Cooldown:           config.Cooldown,
			VerifyTimeout:      config.VerifyTimeout,
			EnableAutoRollback: config.EnableAutoRollback,
		},
	})
	nextState, err := guardian.RunOnce(ctx)
	if err != nil {
		return err
	}
	if err := stateStore.Write(ctx, nextState); err != nil {
		return err
	}
	logf("LedgerBot Guardian 状态: phase=%s action=%s target_release=%s", nextState.Phase, nextState.LastAction, nextState.TargetRelease)
	return nil
}

func loadGuardianConfig(getenv func(string) string) (guardianConfig, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	pollStatusPath := strings.TrimSpace(getenv("POLL_STATUS_PATH"))
	if pollStatusPath == "" {
		pollStatusPath = "data/recovery/poll-status.json"
	}
	recoveryDir := filepath.Dir(pollStatusPath)
	config := guardianConfig{
		PollStatusPath:     pollStatusPath,
		StatePath:          recoveryPath(getenv("GUARDIAN_STATE_PATH"), recoveryDir, "guardian-state.json"),
		RequestPath:        recoveryPath(getenv("RECOVERY_REQUEST_PATH"), recoveryDir, "recovery-request.json"),
		ReleaseRoot:        strings.TrimSpace(getenv("LEDGERBOT_RELEASE_ROOT")),
		ServiceName:        strings.TrimSpace(getenv("LEDGERBOT_SERVICE_NAME")),
		MinFailureCount:    3,
		EnableAutoRollback: false,
	}
	if config.ReleaseRoot == "" {
		config.ReleaseRoot = "."
	}
	if config.ServiceName == "" {
		config.ServiceName = "bilibili-ledgerbot.service"
	}
	var err error
	if config.StartupGrace, err = guardianDuration(getenv("POLL_STARTUP_GRACE"), 90*time.Second); err != nil {
		return guardianConfig{}, fmt.Errorf("POLL_STARTUP_GRACE: %w", err)
	}
	if config.MaxSuccessAge, err = guardianDuration(getenv("POLL_MAX_SUCCESS_AGE"), 90*time.Second); err != nil {
		return guardianConfig{}, fmt.Errorf("POLL_MAX_SUCCESS_AGE: %w", err)
	}
	if config.Cooldown, err = guardianDuration(getenv("GUARDIAN_COOLDOWN"), 15*time.Minute); err != nil {
		return guardianConfig{}, fmt.Errorf("GUARDIAN_COOLDOWN: %w", err)
	}
	if config.VerifyTimeout, err = guardianDuration(getenv("GUARDIAN_VERIFY_TIMEOUT"), 90*time.Second); err != nil {
		return guardianConfig{}, fmt.Errorf("GUARDIAN_VERIFY_TIMEOUT: %w", err)
	}
	if config.EnableAutoRollback, err = guardianBool(getenv("GUARDIAN_AUTO_ROLLBACK_ENABLED"), false); err != nil {
		return guardianConfig{}, fmt.Errorf("GUARDIAN_AUTO_ROLLBACK_ENABLED: %w", err)
	}
	if raw := strings.TrimSpace(getenv("POLL_MIN_FAILURE_COUNT")); raw != "" {
		var value int
		if _, err := fmt.Sscanf(raw, "%d", &value); err != nil || value <= 0 {
			return guardianConfig{}, errors.New("POLL_MIN_FAILURE_COUNT must be a positive integer")
		}
		config.MinFailureCount = value
	}
	return config, nil
}

func recoveryPath(raw, directory, fallback string) string {
	if path := strings.TrimSpace(raw); path != "" {
		return path
	}
	return filepath.Join(directory, fallback)
}

func guardianDuration(raw string, fallback time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, errors.New("must be a positive Go duration")
	}
	return value, nil
}

func guardianBool(raw string, fallback bool) (bool, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return fallback, nil
	}
	switch raw {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true, nil
	case "0", "false", "no", "off", "disable", "disabled":
		return false, nil
	default:
		return false, errors.New("must be a boolean")
	}
}

func logGuardianDryRun(ctx context.Context, pollStore *system.PollStatusStore, stateStore *system.GuardianStateStore, config guardianConfig, logf func(string, ...any)) error {
	status, statusErr := pollStore.Load(ctx)
	state, stateErr := stateStore.Load(ctx)
	if statusErr != nil && !errors.Is(statusErr, os.ErrNotExist) {
		return statusErr
	}
	if stateErr != nil {
		return stateErr
	}
	readiness := pollStore.Readiness(ctx, time.Now(), system.PollReadinessPolicy{
		StartupGrace:    config.StartupGrace,
		MaxSuccessAge:   config.MaxSuccessAge,
		MinFailureCount: config.MinFailureCount,
	})
	logf("LedgerBot Guardian dry-run: ready=%t reason=%s failures=%d error_class=%s guardian_phase=%s", readiness.Ready, readiness.Reason, status.ConsecutiveFailures, status.LastErrorClass, state.Phase)
	return nil
}

type systemdGuardianController struct {
	serviceName string
	releaseRoot string
	pollStore   *system.PollStatusStore
	policy      system.PollReadinessPolicy
}

func (c *systemdGuardianController) Restart(ctx context.Context) error {
	if c == nil || strings.TrimSpace(c.serviceName) == "" {
		return errors.New("guardian service name is empty")
	}
	return exec.CommandContext(ctx, "systemctl", "restart", c.serviceName).Run()
}

func (c *systemdGuardianController) WaitForReadiness(ctx context.Context, timeout time.Duration) bool {
	if c == nil || c.pollStore == nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	for {
		if c.pollStore.Readiness(ctx, time.Now(), c.policy).Ready {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func (c *systemdGuardianController) CurrentRelease(context.Context) (string, error) {
	return c.resolveRelease(filepath.Join(c.releaseRoot, "current"))
}

func (c *systemdGuardianController) SwitchCurrent(_ context.Context, directory string) error {
	target, err := c.resolveRelease(directory)
	if err != nil {
		return err
	}
	currentLink := filepath.Join(c.releaseRoot, "current")
	temporaryLink := currentLink + ".guardian-next"
	if err := os.Remove(temporaryLink); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(target, temporaryLink); err != nil {
		return err
	}
	if err := os.Rename(temporaryLink, currentLink); err != nil {
		_ = os.Remove(temporaryLink)
		return err
	}
	return nil
}

func (c *systemdGuardianController) resolveRelease(path string) (string, error) {
	root, err := filepath.EvalSymlinks(c.releaseRoot)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	releasesRoot := filepath.Join(root, "releases")
	relative, err := filepath.Rel(releasesRoot, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("release is outside releases root")
	}
	return candidate, nil
}
