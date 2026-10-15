package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAdminSecuritySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin-security.db")
	database, err := NewDatabase(path)
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	for _, table := range []string{"admin_setup_state", "admin_sessions"} {
		var count int
		if err := database.raw.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("检查表 %s 失败: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("表 %s 未创建", table)
		}
	}

	var primaryIndexSQL sql.NullString
	if err := database.raw.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_admin_sessions_one_primary'`).Scan(&primaryIndexSQL); err != nil {
		t.Fatalf("检查主设备唯一索引失败: %v", err)
	}
	if !primaryIndexSQL.Valid || primaryIndexSQL.String == "" {
		t.Fatalf("主设备唯一索引 SQL 为空")
	}

	state, err := database.GetAdminSetupState(context.Background())
	if err != nil {
		t.Fatalf("读取 setup 单例失败: %v", err)
	}
	if state.SingletonID != 1 || state.AuthEpoch != 1 || state.ConsumedAt != 0 {
		t.Fatalf("setup 单例默认值异常: %+v", state)
	}

	if err := database.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}
	reopened, err := NewDatabase(path)
	if err != nil {
		t.Fatalf("重新打开数据库失败: %v", err)
	}
	defer reopened.Close()
	var rows int
	if err := reopened.raw.QueryRow(`SELECT COUNT(1) FROM admin_setup_state`).Scan(&rows); err != nil {
		t.Fatalf("重新打开后检查 setup 单例失败: %v", err)
	}
	if rows != 1 {
		t.Fatalf("重新打开后 setup 单例应保持一行，实际=%d", rows)
	}
}

func TestAdminSecurityRootDatabase(t *testing.T) {
	database, err := NewDatabaseWithOptions(filepath.Join(t.TempDir(), "root.db"), Options{TenantShardCount: 2})
	if err != nil {
		t.Fatalf("初始化分片数据库失败: %v", err)
	}
	defer database.Close()

	primary, err := database.CreateAdminSession(context.Background(), "admin", "root-handle", "root-token-hash", "ua", "127.0.0.1", 100, time.Hour)
	if err != nil {
		t.Fatalf("根库创建 admin session 失败: %v", err)
	}
	if !primary.IsPrimary {
		t.Fatalf("根库首个 session 应成为主设备: %+v", primary)
	}

	var rootCount, shardCount int
	if err := database.raw.QueryRow(`SELECT COUNT(1) FROM admin_sessions WHERE session_handle = 'root-handle'`).Scan(&rootCount); err != nil {
		t.Fatalf("读取根库 session 失败: %v", err)
	}
	if err := database.tenantRouter.shards[1].raw.QueryRow(`SELECT COUNT(1) FROM admin_sessions WHERE session_handle = 'root-handle'`).Scan(&shardCount); err != nil {
		t.Fatalf("读取分片库 session 失败: %v", err)
	}
	if rootCount != 1 || shardCount != 0 {
		t.Fatalf("admin session 必须固定写根库，root=%d shard=%d", rootCount, shardCount)
	}
}

func TestAdminSetupConsumeRace(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "setup-race.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const workers = 8
	results := make(chan bool, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			consumed, consumeErr := database.ConsumeAdminSetup(context.Background(), "device", int64(100+index))
			if consumeErr != nil {
				t.Errorf("并发消费 setup 失败: %v", consumeErr)
			}
			results <- consumed
		}(i)
	}
	wait.Wait()
	close(results)

	consumedCount := 0
	for consumed := range results {
		if consumed {
			consumedCount++
		}
	}
	if consumedCount != 1 {
		t.Fatalf("并发 setup 消费必须恰好成功一次，实际=%d", consumedCount)
	}
}

func TestAdminPrimarySessionRace(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "primary-race.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	const workers = 8
	results := make(chan AdminSessionRecord, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			record, createErr := database.CreateAdminSession(
				context.Background(), "admin", "handle-"+string(rune('a'+index)), "hash-"+string(rune('a'+index)), "ua", "127.0.0.1", int64(200+index), 12*time.Hour,
			)
			if createErr != nil {
				errs <- createErr
				return
			}
			results <- record
		}(i)
	}
	wait.Wait()
	close(results)
	close(errs)

	primaryCount := 0
	createdCount := 0
	for record := range results {
		createdCount++
		if record.IsPrimary {
			primaryCount++
		}
	}
	for createErr := range errs {
		if createErr != nil {
			t.Fatalf("并发创建 session 不应失败: %v", createErr)
		}
	}
	if createdCount != workers || primaryCount != 1 {
		t.Fatalf("并发登录必须创建 %d 个 session 且只有一个主设备，created=%d primary=%d", workers, createdCount, primaryCount)
	}

	var validPrimary int
	if err := database.raw.QueryRow(`SELECT COUNT(1) FROM admin_sessions WHERE is_primary = 1 AND revoked_at IS NULL`).Scan(&validPrimary); err != nil {
		t.Fatalf("检查有效主设备失败: %v", err)
	}
	if validPrimary != 1 {
		t.Fatalf("数据库必须保证只有一个有效主设备，实际=%d", validPrimary)
	}
}

func TestAdminCredentialEpochRevokesSessions(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "credential-epoch.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	created, err := database.CreateAdminSession(context.Background(), "admin", "epoch-handle", "epoch-hash", "ua", "127.0.0.1", 300, time.Hour)
	if err != nil {
		t.Fatalf("创建 session 失败: %v", err)
	}

	epoch, changed, err := database.UpdateAdminCredentialFingerprint(context.Background(), "fingerprint-v1", 301)
	if err != nil || !changed || epoch != created.AuthEpoch+1 {
		t.Fatalf("首次更新凭据 fingerprint 异常: epoch=%d changed=%v err=%v", epoch, changed, err)
	}
	revoked, err := database.GetAdminSessionByTokenHash(context.Background(), "epoch-hash")
	if err != nil {
		t.Fatalf("读取被撤销 session 失败: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatalf("凭据 fingerprint 变化后旧 session 必须撤销")
	}

	epoch, changed, err = database.UpdateAdminCredentialFingerprint(context.Background(), "fingerprint-v1", 302)
	if err != nil || changed || epoch != created.AuthEpoch+1 {
		t.Fatalf("重复 fingerprint 不应递增 epoch: epoch=%d changed=%v err=%v", epoch, changed, err)
	}
}

func TestAdminSessionLifecycle(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "session-lifecycle.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	primary, err := database.CreateAdminSession(context.Background(), "admin", "primary-handle", "primary-hash", "primary ua", "127.0.0.1", 400, time.Hour)
	if err != nil {
		t.Fatalf("创建主设备失败: %v", err)
	}
	secondary, err := database.CreateAdminSession(context.Background(), "admin", "secondary-handle", "secondary-hash", "secondary ua", "127.0.0.2", 400, 12*time.Hour)
	if err != nil {
		t.Fatalf("创建次设备失败: %v", err)
	}
	if !primary.IsPrimary || secondary.IsPrimary || secondary.ExpiresAt == nil {
		t.Fatalf("主次设备属性异常: primary=%+v secondary=%+v", primary, secondary)
	}

	items, err := database.ListAdminSessionSummaries(context.Background(), "admin", "secondary-handle", 401)
	if err != nil || len(items) != 2 {
		t.Fatalf("设备列表异常: len=%d err=%v", len(items), err)
	}
	for _, item := range items {
		if item.SessionHandle == "secondary-handle" && !item.IsCurrent {
			t.Fatalf("当前设备摘要未标记: %+v", item)
		}
	}

	if changed, err := database.UpdateAdminSessionExpiry(context.Background(), "admin", "secondary-handle", int64(24*time.Hour/time.Second)+400, 402); err != nil || !changed {
		t.Fatalf("更新次设备过期时间失败: changed=%v err=%v", changed, err)
	}
	if changed, err := database.UpdateAdminSessionExpiry(context.Background(), "admin", "primary-handle", 999, 403); err != nil || changed {
		t.Fatalf("主设备不允许改为有限过期: changed=%v err=%v", changed, err)
	}
	if changed, err := database.RevokeAdminSession(context.Background(), "admin", "secondary-handle", 404); err != nil || !changed {
		t.Fatalf("撤销次设备失败: changed=%v err=%v", changed, err)
	}
	if changed, err := database.RevokeAdminSession(context.Background(), "admin", "secondary-handle", 405); err != nil || changed {
		t.Fatalf("重复撤销次设备应为条件失败: changed=%v err=%v", changed, err)
	}

	newSecondary, err := database.CreateAdminSession(context.Background(), "admin", "transfer-handle", "transfer-hash", "transfer ua", "127.0.0.3", 406, 12*time.Hour)
	if err != nil {
		t.Fatalf("创建转移目标失败: %v", err)
	}
	if err := database.TransferAdminPrimary(context.Background(), "admin", newSecondary.SessionHandle, 407); err != nil {
		t.Fatalf("转移主设备失败: %v", err)
	}
	oldPrimary, err := database.GetAdminSessionByTokenHash(context.Background(), "primary-hash")
	if err != nil {
		t.Fatalf("读取旧主设备失败: %v", err)
	}
	if oldPrimary.RevokedAt == nil {
		t.Fatalf("转移主设备后旧主设备必须撤销")
	}
	transferred, err := database.GetAdminSessionByTokenHash(context.Background(), "transfer-hash")
	if err != nil {
		t.Fatalf("读取新主设备失败: %v", err)
	}
	if !transferred.IsPrimary || transferred.ExpiresAt != nil {
		t.Fatalf("新主设备属性异常: %+v", transferred)
	}

	if count, err := database.RevokeOtherAdminSessions(context.Background(), "admin", transferred.SessionHandle, 408); err != nil || count != 0 {
		t.Fatalf("撤销其他设备后当前主设备之外应无有效设备: count=%d err=%v", count, err)
	}

	if _, err := database.GetAdminSessionByTokenHash(context.Background(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("不存在的 token 应返回 sql.ErrNoRows，实际=%v", err)
	}
}

func TestAdminExpiredSessionCannotBecomePrimary(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "expired-transfer.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	primary, err := database.CreateAdminSession(context.Background(), "admin", "active-primary", "active-primary-hash", "ua", "127.0.0.1", 500, time.Hour)
	if err != nil {
		t.Fatalf("创建主设备失败: %v", err)
	}
	if !primary.IsPrimary {
		t.Fatalf("首个 session 应成为主设备")
	}
	expired, err := database.CreateAdminSession(context.Background(), "admin", "expired-secondary", "expired-secondary-hash", "ua", "127.0.0.2", 500, time.Second)
	if err != nil {
		t.Fatalf("创建次设备失败: %v", err)
	}
	if err := database.TransferAdminPrimary(context.Background(), "admin", expired.SessionHandle, *expired.ExpiresAt+1); err == nil {
		t.Fatalf("已过期次设备不应转移为主设备")
	}

	stillPrimary, err := database.GetAdminSessionByTokenHash(context.Background(), "active-primary-hash")
	if err != nil {
		t.Fatalf("读取原主设备失败: %v", err)
	}
	if stillPrimary.RevokedAt != nil || !stillPrimary.IsPrimary {
		t.Fatalf("转移失败时原主设备必须保持有效: %+v", stillPrimary)
	}
}
