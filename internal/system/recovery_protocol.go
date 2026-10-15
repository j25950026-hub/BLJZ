package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const recoveryProtocolVersion = 1

type RecoveryAction string

const (
	RecoveryActionRecover  RecoveryAction = "recover"
	RecoveryActionRollback RecoveryAction = "rollback"
)

type RecoveryRequest struct {
	Version     int            `json:"version"`
	ID          string         `json:"id"`
	Action      RecoveryAction `json:"action"`
	RequestedAt time.Time      `json:"requested_at"`
	ExpiresAt   time.Time      `json:"expires_at"`
	Actor       string         `json:"actor"`
}

type RecoveryRequestStore struct {
	path string
	mu   sync.Mutex
}

type GuardianStateStore struct {
	path string
	mu   sync.Mutex
}

func NewGuardianStateStore(path string) *GuardianStateStore {
	return &GuardianStateStore{path: strings.TrimSpace(path)}
}

func (s *GuardianStateStore) Write(ctx context.Context, state GuardianState) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil || s.path == "" {
		return errors.New("guardian state path is empty")
	}
	if err := validateGuardianState(state); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAtomicJSON(s.path, state, 0o640)
}

func (s *GuardianStateStore) Load(ctx context.Context) (GuardianState, error) {
	if err := contextErr(ctx); err != nil {
		return GuardianState{}, err
	}
	if s == nil || s.path == "" {
		return GuardianState{}, errors.New("guardian state path is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return GuardianState{Version: recoveryProtocolVersion, Phase: GuardianPhaseHealthy}, nil
	}
	if err != nil {
		return GuardianState{}, fmt.Errorf("read guardian state: %w", err)
	}
	var state GuardianState
	if err := json.Unmarshal(raw, &state); err != nil {
		return GuardianState{}, fmt.Errorf("decode guardian state: %w", err)
	}
	if err := validateGuardianState(state); err != nil {
		return GuardianState{}, err
	}
	return state, nil
}

func NewRecoveryRequestStore(path string) *RecoveryRequestStore {
	return &RecoveryRequestStore{path: strings.TrimSpace(path)}
}

func (s *RecoveryRequestStore) Write(ctx context.Context, request RecoveryRequest) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil || s.path == "" {
		return errors.New("recovery request path is empty")
	}
	request.Version = recoveryProtocolVersion
	if err := validateRecoveryRequest(request); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAtomicJSON(s.path, request, 0o640)
}

func (s *RecoveryRequestStore) Take(ctx context.Context, now time.Time) (RecoveryRequest, bool, error) {
	if err := contextErr(ctx); err != nil {
		return RecoveryRequest{}, false, err
	}
	if s == nil || s.path == "" {
		return RecoveryRequest{}, false, errors.New("recovery request path is empty")
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return RecoveryRequest{}, false, nil
	}
	if err != nil {
		return RecoveryRequest{}, false, fmt.Errorf("read recovery request: %w", err)
	}
	var request RecoveryRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		_ = os.Remove(s.path)
		return RecoveryRequest{}, false, fmt.Errorf("decode recovery request: %w", err)
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return RecoveryRequest{}, false, fmt.Errorf("consume recovery request: %w", err)
	}
	if err := validateRecoveryRequest(request); err != nil {
		return RecoveryRequest{}, false, err
	}
	if !request.ExpiresAt.After(now) {
		return RecoveryRequest{}, false, nil
	}
	return request, true, nil
}

func validateRecoveryRequest(request RecoveryRequest) error {
	if request.Version != recoveryProtocolVersion {
		return fmt.Errorf("unsupported recovery request version: %d", request.Version)
	}
	if strings.TrimSpace(request.ID) == "" || len(request.ID) > 128 {
		return errors.New("recovery request id is invalid")
	}
	if request.Action != RecoveryActionRecover && request.Action != RecoveryActionRollback {
		return fmt.Errorf("unsupported recovery action: %q", request.Action)
	}
	if strings.TrimSpace(request.Actor) == "" || len(request.Actor) > 256 {
		return errors.New("recovery request actor is invalid")
	}
	if request.RequestedAt.IsZero() || request.ExpiresAt.IsZero() || !request.ExpiresAt.After(request.RequestedAt) {
		return errors.New("recovery request expiry is invalid")
	}
	return nil
}

func validateGuardianState(state GuardianState) error {
	if state.Version != recoveryProtocolVersion {
		return fmt.Errorf("unsupported guardian state version: %d", state.Version)
	}
	switch state.Phase {
	case GuardianPhaseHealthy, GuardianPhaseRestarting, GuardianPhaseRecovered, GuardianPhaseRolledBack, GuardianPhaseFrozen:
	default:
		return fmt.Errorf("unsupported guardian phase: %q", state.Phase)
	}
	if len(state.LastError) > 512 || len(state.TargetRelease) > 256 {
		return errors.New("guardian state is too large")
	}
	return nil
}

type ReleaseManifest struct {
	Version          int       `json:"version"`
	ReleaseID        string    `json:"release_id"`
	LedgerbotSHA256  string    `json:"ledgerbot_sha256"`
	GuardianSHA256   string    `json:"guardian_sha256,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	RollbackEligible bool      `json:"rollback_eligible"`
}

type ReleaseCandidate struct {
	Directory string
	Manifest  ReleaseManifest
}

func WriteReleaseManifest(path string, manifest ReleaseManifest) error {
	manifest.Version = recoveryProtocolVersion
	if err := validateReleaseManifest(manifest); err != nil {
		return err
	}
	return writeAtomicJSON(path, manifest, 0o640)
}

func ResolveLastKnownGood(root string) (ReleaseCandidate, error) {
	return resolveReleasePointer(root, "last-known-good", false)
}

func ResolveRollbackCandidate(root string) (ReleaseCandidate, error) {
	return resolveReleasePointer(root, "rollback-candidate", true)
}

func resolveReleasePointer(root, pointerName string, rejectCurrent bool) (ReleaseCandidate, error) {
	resolvedRoot, releasesRoot, err := resolveReleaseRoot(root)
	if err != nil {
		return ReleaseCandidate{}, err
	}
	candidateDir, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, pointerName))
	if err != nil {
		return ReleaseCandidate{}, fmt.Errorf("resolve %s: %w", pointerName, err)
	}
	if rejectCurrent {
		currentDir, currentErr := filepath.EvalSymlinks(filepath.Join(resolvedRoot, "current"))
		if currentErr == nil && filepath.Clean(currentDir) == filepath.Clean(candidateDir) {
			return ReleaseCandidate{}, errors.New("rollback candidate is the current release")
		}
	}
	return readReleaseCandidate(releasesRoot, candidateDir, true)
}

func validateReleaseManifest(manifest ReleaseManifest) error {
	if manifest.Version != recoveryProtocolVersion {
		return fmt.Errorf("unsupported release manifest version: %d", manifest.Version)
	}
	if strings.TrimSpace(manifest.ReleaseID) == "" || len(manifest.ReleaseID) > 128 {
		return errors.New("release manifest id is invalid")
	}
	sha := strings.TrimSpace(manifest.LedgerbotSHA256)
	if len(sha) != sha256.Size*2 {
		return errors.New("release manifest ledgerbot checksum is invalid")
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return errors.New("release manifest ledgerbot checksum is invalid")
	}
	return nil
}

func PromoteCurrentRelease(root string, status PollStatus) (bool, error) {
	if status.Version != pollStatusVersion {
		return false, fmt.Errorf("unsupported poll status version: %d", status.Version)
	}
	if status.ConsecutiveSuccesses < 2 {
		return false, nil
	}
	if strings.TrimSpace(status.ReleaseID) == "" || strings.TrimSpace(status.ReleaseSHA) == "" {
		return false, errors.New("poll status release identity is missing")
	}
	resolvedRoot, releasesRoot, err := resolveReleaseRoot(root)
	if err != nil {
		return false, err
	}
	currentDir, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, "current"))
	if err != nil {
		return false, fmt.Errorf("resolve current release: %w", err)
	}
	candidate, err := readReleaseCandidate(releasesRoot, currentDir, false)
	if err != nil {
		return false, err
	}
	if candidate.Manifest.ReleaseID != strings.TrimSpace(status.ReleaseID) || !strings.EqualFold(candidate.Manifest.LedgerbotSHA256, strings.TrimSpace(status.ReleaseSHA)) {
		return false, errors.New("poll status does not match current release manifest")
	}
	if candidate.Manifest.RollbackEligible && lastKnownGoodPointsTo(resolvedRoot, candidate.Directory) {
		return false, nil
	}
	previousKnownGood, previousErr := filepath.EvalSymlinks(filepath.Join(resolvedRoot, "last-known-good"))
	if previousErr == nil && filepath.Clean(previousKnownGood) != filepath.Clean(candidate.Directory) {
		if _, err := readReleaseCandidate(releasesRoot, previousKnownGood, true); err != nil {
			return false, fmt.Errorf("validate previous last-known-good: %w", err)
		}
	}
	candidate.Manifest.RollbackEligible = true
	if err := WriteReleaseManifest(filepath.Join(candidate.Directory, "manifest.json"), candidate.Manifest); err != nil {
		return false, fmt.Errorf("mark current release rollback eligible: %w", err)
	}
	if previousErr == nil && filepath.Clean(previousKnownGood) != filepath.Clean(candidate.Directory) {
		if err := replaceReleaseLink(filepath.Join(resolvedRoot, "rollback-candidate"), previousKnownGood); err != nil {
			return false, fmt.Errorf("preserve previous last-known-good: %w", err)
		}
	}
	return true, replaceReleaseLink(filepath.Join(resolvedRoot, "last-known-good"), candidate.Directory)
}

func resolveReleaseRoot(root string) (string, string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", "", errors.New("release root is empty")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve release root: %w", err)
	}
	return resolvedRoot, filepath.Join(resolvedRoot, "releases"), nil
}

func readReleaseCandidate(releasesRoot, candidateDir string, requireRollbackEligible bool) (ReleaseCandidate, error) {
	if !pathWithin(releasesRoot, candidateDir) {
		return ReleaseCandidate{}, errors.New("release is outside releases root")
	}
	manifestPath := filepath.Join(candidateDir, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return ReleaseCandidate{}, fmt.Errorf("read release manifest: %w", err)
	}
	var manifest ReleaseManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ReleaseCandidate{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := validateReleaseManifest(manifest); err != nil {
		return ReleaseCandidate{}, err
	}
	if requireRollbackEligible && !manifest.RollbackEligible {
		return ReleaseCandidate{}, errors.New("release is not rollback eligible")
	}
	binaryPath := filepath.Join(candidateDir, "ledgerbot")
	resolvedBinary, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return ReleaseCandidate{}, fmt.Errorf("resolve release binary: %w", err)
	}
	if !pathWithin(candidateDir, resolvedBinary) {
		return ReleaseCandidate{}, errors.New("release binary is outside candidate directory")
	}
	rawBinary, err := os.ReadFile(resolvedBinary)
	if err != nil {
		return ReleaseCandidate{}, fmt.Errorf("read release binary: %w", err)
	}
	sum := sha256.Sum256(rawBinary)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), manifest.LedgerbotSHA256) {
		return ReleaseCandidate{}, errors.New("release ledgerbot checksum mismatch")
	}
	return ReleaseCandidate{Directory: candidateDir, Manifest: manifest}, nil
}

func lastKnownGoodPointsTo(root, candidateDir string) bool {
	current, err := filepath.EvalSymlinks(filepath.Join(root, "last-known-good"))
	return err == nil && filepath.Clean(current) == filepath.Clean(candidateDir)
}

func replaceReleaseLink(path, target string) error {
	temporary := path + ".next"
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove temporary release link: %w", err)
	}
	if err := os.Symlink(target, temporary); err != nil {
		return fmt.Errorf("create temporary release link: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace release link: %w", err)
	}
	return nil
}

func writeAtomicJSON(path string, value any, mode os.FileMode) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("atomic json path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create state temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("set state permissions: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
