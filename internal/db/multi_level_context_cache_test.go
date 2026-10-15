package db

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

type blockingGroupConfigCache struct {
	*MemoryContextCache
	setStarted chan struct{}
	allowSet   chan struct{}
	setOnce    sync.Once
}

func (c *blockingGroupConfigCache) SetGroupConfig(group GroupConfig) error {
	c.setOnce.Do(func() { close(c.setStarted) })
	<-c.allowSet
	return c.MemoryContextCache.SetGroupConfig(group)
}

func TestMultiLevelContextCacheDoesNotExposeL1BeforeL2Commit(t *testing.T) {
	l1 := NewMemoryContextCache(MemoryContextCacheConfig{TTL: time.Minute, MaxEntries: 8})
	l2 := &blockingGroupConfigCache{
		MemoryContextCache: NewMemoryContextCache(MemoryContextCacheConfig{TTL: time.Minute, MaxEntries: 8}),
		setStarted:         make(chan struct{}),
		allowSet:           make(chan struct{}),
	}
	cache := NewMultiLevelContextCache(l1, l2)
	group := GroupConfig{ChatID: -100421001, Title: "多级缓存原子写入测试群"}

	setDone := make(chan error, 1)
	go func() {
		setDone <- cache.SetGroupConfig(group)
	}()
	select {
	case <-l2.setStarted:
	case <-time.After(time.Second):
		t.Fatal("L2 写入未进入可控阻塞点")
	}

	type groupReadResult struct {
		group GroupConfig
		ok    bool
		err   error
	}
	readDone := make(chan groupReadResult, 1)
	go func() {
		got, ok, err := cache.GetGroupConfig(group.ChatID)
		readDone <- groupReadResult{group: got, ok: ok, err: err}
	}()

	select {
	case result := <-readDone:
		close(l2.allowSet)
		if err := <-setDone; err != nil {
			t.Fatalf("完成 L2 写入失败: %v", err)
		}
		if result.err != nil {
			t.Fatalf("读取多级缓存失败: %v", result.err)
		}
		if result.ok {
			t.Fatalf("L2 尚未提交时不应提前暴露 L1 缓存: %+v", result.group)
		}
	case <-time.After(100 * time.Millisecond):
		close(l2.allowSet)
		if err := <-setDone; err != nil {
			t.Fatalf("完成 L2 写入失败: %v", err)
		}
		result := <-readDone
		if result.err != nil {
			t.Fatalf("L2 提交后读取多级缓存失败: %v", result.err)
		}
		if !result.ok || result.group.ChatID != group.ChatID || result.group.Title != group.Title {
			t.Fatalf("L2 提交后应读取完整群配置: got=%+v ok=%v", result.group, result.ok)
		}
	}
}

func TestControlPoint421MultiLevelContextCache(t *testing.T) {
	t.Run("控制点 4.2.1 功能验证", func(t *testing.T) {
		mini, err := miniredis.Run()
		if err != nil {
			t.Fatalf("启动 miniredis 失败: %v", err)
		}
		defer mini.Close()

		database, err := NewDatabase(filepath.Join(t.TempDir(), "multi-level-cache.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		l1 := NewMemoryContextCache(MemoryContextCacheConfig{
			TTL:        2 * time.Minute,
			MaxEntries: 128,
		})
		redisCache, err := NewRedisContextCache(RedisContextCacheConfig{
			Addr:      mini.Addr(),
			KeyPrefix: "ctxcache:",
			TTL:       5 * time.Minute,
			Timeout:   2 * time.Second,
		})
		if err != nil {
			t.Fatalf("初始化 Redis 上下文缓存失败: %v", err)
		}
		cache := NewMultiLevelContextCache(l1, redisCache)
		if cache == nil {
			t.Fatalf("多级缓存初始化失败")
		}
		defer cache.Close()
		database.SetContextCache(cache)

		const (
			chatID         int64 = -1004201001
			ownerID        int64 = 42001
			operatorID     int64 = 42002
			bypassOwnerID  int64 = 42099
			originGroupTag       = "多级缓存测试群"
			bypassGroupTag       = "旁路更新群标题"
		)

		if _, err := database.EnsureGroup(chatID, originGroupTag); err != nil {
			t.Fatalf("初始化群配置失败: %v", err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置群主失败: %v", err)
		}
		if err := database.AddOperator(chatID, operatorID, "multi-cache-op", false); err != nil {
			t.Fatalf("添加操作人失败: %v", err)
		}

		group, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("预热群配置缓存失败: %v", err)
		}
		if group.Title != originGroupTag || group.OwnerUserID != ownerID {
			t.Fatalf("群配置预热结果异常: %+v", group)
		}
		if !database.IsOperator(chatID, operatorID) {
			t.Fatalf("预热操作人缓存失败")
		}

		if !redisStringKeyExists(mini, redisCache.groupKey(chatID)) {
			t.Fatalf("L2 Redis 未写入群配置缓存")
		}
		if !redisStringKeyExists(mini, redisCache.userKey(chatID, operatorID)) {
			t.Fatalf("L2 Redis 未写入用户权限缓存")
		}

		if _, err := database.raw.Exec(`UPDATE group_config SET title = ?, owner_user_id = ? WHERE chat_id = ?`, bypassGroupTag, bypassOwnerID, chatID); err != nil {
			t.Fatalf("旁路改写群配置失败: %v", err)
		}
		if _, err := database.raw.Exec(`DELETE FROM operators WHERE chat_id = ? AND user_id = ? AND is_global = 0`, chatID, operatorID); err != nil {
			t.Fatalf("旁路移除操作人失败: %v", err)
		}

		if err := l1.DeleteGroupConfig(chatID); err != nil {
			t.Fatalf("清理 L1 群缓存失败: %v", err)
		}
		if err := l1.DeleteUserAccess(chatID, operatorID); err != nil {
			t.Fatalf("清理 L1 用户缓存失败: %v", err)
		}

		group, err = database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("L2 回源读取群配置失败: %v", err)
		}
		if group.Title != originGroupTag || group.OwnerUserID != ownerID {
			t.Fatalf("应优先命中 L2 旧值，实际=%+v", group)
		}
		if !database.IsOperator(chatID, operatorID) {
			t.Fatalf("应优先命中 L2 操作人缓存")
		}
		if _, ok, err := l1.GetGroupConfig(chatID); err != nil || !ok {
			t.Fatalf("L2 命中后应回填 L1 群缓存")
		}
		if _, ok, err := l1.GetUserAccess(chatID, operatorID); err != nil || !ok {
			t.Fatalf("L2 命中后应回填 L1 用户缓存")
		}

		mini.FlushAll()
		if err := l1.DeleteGroupConfig(chatID); err != nil {
			t.Fatalf("清理 L1 群缓存失败: %v", err)
		}
		if err := l1.DeleteUserAccess(chatID, operatorID); err != nil {
			t.Fatalf("清理 L1 用户缓存失败: %v", err)
		}

		group, err = database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("L1/L2 均失效后读取群配置失败: %v", err)
		}
		if group.Title != bypassGroupTag || group.OwnerUserID != bypassOwnerID {
			t.Fatalf("缓存失效后应回源数据库，实际=%+v", group)
		}
		if database.IsOperator(chatID, operatorID) {
			t.Fatalf("缓存失效后应感知旁路删除的操作人")
		}
	})
}
