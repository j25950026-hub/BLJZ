package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryRequestStoreTakesOnlyUnexpiredFixedAction(t *testing.T) {
	store := NewRecoveryRequestStore(filepath.Join(t.TempDir(), "recovery-request.json"))
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	req := RecoveryRequest{
		Version:     recoveryProtocolVersion,
		ID:          "request-1",
		Action:      RecoveryActionRecover,
		RequestedAt: now,
		ExpiresAt:   now.Add(5 * time.Minute),
		Actor:       "primary-admin",
	}
	if err := store.Write(context.Background(), req); err != nil {
		t.Fatalf("写入恢复请求: %v", err)
	}
	got, ok, err := store.Take(context.Background(), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("提取恢复请求: %v", err)
	}
	if !ok || got != req {
		t.Fatalf("有效固定恢复请求错误: ok=%t got=%#v", ok, got)
	}
	_, ok, err = store.Take(context.Background(), now.Add(time.Minute))
	if err != nil || ok {
		t.Fatalf("请求必须只能消费一次: ok=%t err=%v", ok, err)
	}
}

func TestRecoveryRequestStoreDiscardsExpiredRequest(t *testing.T) {
	store := NewRecoveryRequestStore(filepath.Join(t.TempDir(), "recovery-request.json"))
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	if err := store.Write(context.Background(), RecoveryRequest{
		Version:     recoveryProtocolVersion,
		ID:          "request-expired",
		Action:      RecoveryActionRollback,
		RequestedAt: now.Add(-10 * time.Minute),
		ExpiresAt:   now.Add(-time.Minute),
		Actor:       "primary-admin",
	}); err != nil {
		t.Fatalf("写入过期请求: %v", err)
	}
	_, ok, err := store.Take(context.Background(), now)
	if err != nil {
		t.Fatalf("读取过期请求: %v", err)
	}
	if ok {
		t.Fatal("过期请求不得执行")
	}
}

func TestRecoveryRequestStoreRejectsArbitraryAction(t *testing.T) {
	store := NewRecoveryRequestStore(filepath.Join(t.TempDir(), "recovery-request.json"))
	err := store.Write(context.Background(), RecoveryRequest{
		Version:     recoveryProtocolVersion,
		ID:          "request-command",
		Action:      RecoveryAction("shell"),
		RequestedAt: time.Now(),
		ExpiresAt:   time.Now().Add(time.Minute),
		Actor:       "primary-admin",
	})
	if err == nil {
		t.Fatal("任意动作必须被拒绝")
	}
}

func TestGuardianStateStorePersistsLatestState(t *testing.T) {
	store := NewGuardianStateStore(filepath.Join(t.TempDir(), "guardian-state.json"))
	want := GuardianState{
		Version:       recoveryProtocolVersion,
		Phase:         GuardianPhaseFrozen,
		LastAction:    "trial_rollback",
		LastError:     "rollback trial did not restore functional readiness",
		UpdatedAt:     time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC),
		NextAllowedAt: time.Date(2026, time.August, 14, 12, 15, 0, 0, time.UTC),
	}
	if err := store.Write(context.Background(), want); err != nil {
		t.Fatalf("写入 Guardian 状态: %v", err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("读取 Guardian 状态: %v", err)
	}
	if got != want {
		t.Fatalf("Guardian 状态持久化不一致: want=%#v got=%#v", want, got)
	}
}

func TestResolveLastKnownGoodValidatesManifestAndReleaseBoundary(t *testing.T) {
	root := t.TempDir()
	releaseDir := filepath.Join(root, "releases", "release-a")
	if err := os.MkdirAll(releaseDir, 0o750); err != nil {
		t.Fatalf("创建发布目录: %v", err)
	}
	binary := []byte("ledgerbot release a")
	if err := os.WriteFile(filepath.Join(releaseDir, "ledgerbot"), binary, 0o750); err != nil {
		t.Fatalf("写入二进制: %v", err)
	}
	sum := sha256.Sum256(binary)
	manifest := ReleaseManifest{
		Version:          recoveryProtocolVersion,
		ReleaseID:        "release-a",
		LedgerbotSHA256:  hex.EncodeToString(sum[:]),
		RollbackEligible: true,
	}
	if err := WriteReleaseManifest(filepath.Join(releaseDir, "manifest.json"), manifest); err != nil {
		t.Fatalf("写入 manifest: %v", err)
	}
	if err := os.Symlink(releaseDir, filepath.Join(root, "last-known-good")); err != nil {
		t.Fatalf("创建 last-known-good 链接: %v", err)
	}

	got, err := ResolveLastKnownGood(root)
	if err != nil {
		t.Fatalf("解析 last-known-good: %v", err)
	}
	if got.Directory != releaseDir || got.Manifest.ReleaseID != "release-a" {
		t.Fatalf("候选版本错误: %#v", got)
	}
}

func TestResolveLastKnownGoodRejectsLinkOutsideReleases(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "last-known-good")); err != nil {
		t.Fatalf("创建越界链接: %v", err)
	}
	if _, err := ResolveLastKnownGood(root); err == nil {
		t.Fatal("last-known-good 越出 releases 根目录必须拒绝")
	}
}

func TestResolveRollbackCandidateValidatesIndependentCandidate(t *testing.T) {
	root := t.TempDir()
	releaseDir := writeProtocolRelease(t, root, "release-old", true)
	if err := os.Symlink(releaseDir, filepath.Join(root, "rollback-candidate")); err != nil {
		t.Fatalf("创建 rollback-candidate 链接: %v", err)
	}
	if err := os.Symlink(releaseDir, filepath.Join(root, "current")); err != nil {
		t.Fatalf("创建 current 链接: %v", err)
	}

	candidate, err := ResolveRollbackCandidate(root)
	if err == nil {
		t.Fatalf("rollback-candidate 与 current 相同必须拒绝: candidate=%#v", candidate)
	}
}

func TestPromoteCurrentReleaseRotatesPreviousKnownGoodIntoRollbackCandidate(t *testing.T) {
	root := t.TempDir()
	previousDir := writeProtocolRelease(t, root, "release-previous", true)
	currentDir := writeProtocolRelease(t, root, "release-current", false)
	if err := os.Symlink(previousDir, filepath.Join(root, "last-known-good")); err != nil {
		t.Fatalf("创建旧 last-known-good 链接: %v", err)
	}
	if err := os.Symlink(currentDir, filepath.Join(root, "current")); err != nil {
		t.Fatalf("创建 current 链接: %v", err)
	}

	manifest := readProtocolManifest(t, currentDir)
	promoted, err := PromoteCurrentRelease(root, PollStatus{
		Version:              pollStatusVersion,
		ConsecutiveSuccesses: 2,
		ReleaseID:            manifest.ReleaseID,
		ReleaseSHA:           manifest.LedgerbotSHA256,
	})
	if err != nil || !promoted {
		t.Fatalf("当前 release 晋升失败: promoted=%t err=%v", promoted, err)
	}

	knownGood, err := ResolveLastKnownGood(root)
	if err != nil {
		t.Fatalf("解析新的 last-known-good: %v", err)
	}
	if knownGood.Directory != currentDir {
		t.Fatalf("last-known-good 未指向当前 release: got=%s want=%s", knownGood.Directory, currentDir)
	}
	rollbackCandidate, err := ResolveRollbackCandidate(root)
	if err != nil {
		t.Fatalf("解析独立 rollback-candidate: %v", err)
	}
	if rollbackCandidate.Directory != previousDir {
		t.Fatalf("rollback-candidate 未保留旧健康 release: got=%s want=%s", rollbackCandidate.Directory, previousDir)
	}
}

func TestPromoteCurrentReleaseRequiresTwoMatchingSuccessfulPolls(t *testing.T) {
	root := t.TempDir()
	releaseDir := filepath.Join(root, "releases", "release-a")
	if err := os.MkdirAll(releaseDir, 0o750); err != nil {
		t.Fatalf("创建发布目录: %v", err)
	}
	binary := []byte("ledgerbot release a")
	if err := os.WriteFile(filepath.Join(releaseDir, "ledgerbot"), binary, 0o750); err != nil {
		t.Fatalf("写入二进制: %v", err)
	}
	sum := sha256.Sum256(binary)
	manifest := ReleaseManifest{
		Version:          recoveryProtocolVersion,
		ReleaseID:        "release-a",
		LedgerbotSHA256:  hex.EncodeToString(sum[:]),
		RollbackEligible: false,
	}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("编码发布清单: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, "manifest.json"), rawManifest, 0o640); err != nil {
		t.Fatalf("写入发布清单: %v", err)
	}
	if err := os.Symlink(releaseDir, filepath.Join(root, "current")); err != nil {
		t.Fatalf("创建当前版本链接: %v", err)
	}

	tooEarly, err := PromoteCurrentRelease(root, PollStatus{
		Version:              pollStatusVersion,
		ConsecutiveSuccesses: 1,
		ReleaseID:            manifest.ReleaseID,
		ReleaseSHA:           manifest.LedgerbotSHA256,
	})
	if err != nil {
		t.Fatalf("不足两次成功不应报错: %v", err)
	}
	if tooEarly {
		t.Fatal("不足两次成功不得提升 last-known-good")
	}
	if _, err := os.Lstat(filepath.Join(root, "last-known-good")); !os.IsNotExist(err) {
		t.Fatalf("不足两次成功不得创建 last-known-good: %v", err)
	}

	promoted, err := PromoteCurrentRelease(root, PollStatus{
		Version:              pollStatusVersion,
		ConsecutiveSuccesses: 2,
		ReleaseID:            manifest.ReleaseID,
		ReleaseSHA:           manifest.LedgerbotSHA256,
	})
	if err != nil || !promoted {
		t.Fatalf("两次匹配成功必须提升: promoted=%t err=%v", promoted, err)
	}
	candidate, err := ResolveLastKnownGood(root)
	if err != nil {
		t.Fatalf("提升后必须成为合格回滚候选: %v", err)
	}
	if candidate.Directory != releaseDir || !candidate.Manifest.RollbackEligible {
		t.Fatalf("提升结果错误: %#v", candidate)
	}
}

func writeProtocolRelease(t *testing.T, root, releaseID string, rollbackEligible bool) string {
	t.Helper()
	releaseDir := filepath.Join(root, "releases", releaseID)
	if err := os.MkdirAll(releaseDir, 0o750); err != nil {
		t.Fatalf("创建发布目录: %v", err)
	}
	binary := []byte("ledgerbot " + releaseID)
	if err := os.WriteFile(filepath.Join(releaseDir, "ledgerbot"), binary, 0o750); err != nil {
		t.Fatalf("写入发布二进制: %v", err)
	}
	sum := sha256.Sum256(binary)
	if err := WriteReleaseManifest(filepath.Join(releaseDir, "manifest.json"), ReleaseManifest{
		Version:          recoveryProtocolVersion,
		ReleaseID:        releaseID,
		LedgerbotSHA256:  hex.EncodeToString(sum[:]),
		RollbackEligible: rollbackEligible,
	}); err != nil {
		t.Fatalf("写入发布清单: %v", err)
	}
	return releaseDir
}

func readProtocolManifest(t *testing.T, releaseDir string) ReleaseManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(releaseDir, "manifest.json"))
	if err != nil {
		t.Fatalf("读取发布清单: %v", err)
	}
	var manifest ReleaseManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("解析发布清单: %v", err)
	}
	return manifest
}
