package db

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestControlPoint452CacheConsistency(t *testing.T) {
	t.Run("控制点 4.5.2 功能验证", func(t *testing.T) {
		mini, err := miniredis.Run()
		if err != nil {
			t.Fatalf("启动 miniredis 失败: %v", err)
		}
		defer mini.Close()

		database, err := NewDatabase(filepath.Join(t.TempDir(), "cache-consistency.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		cache, err := NewRedisContextCache(RedisContextCacheConfig{
			Addr:      mini.Addr(),
			KeyPrefix: "ctxcache:",
			TTL:       3 * time.Minute,
			Timeout:   2 * time.Second,
		})
		if err != nil {
			t.Fatalf("初始化 Redis 上下文缓存失败: %v", err)
		}
		defer cache.Close()
		database.SetContextCache(cache)

		const (
			chatID       int64 = -100452001
			ownerID      int64 = 45201
			newOwnerID   int64 = 45202
			operatorID   int64 = 45203
			groupOrigin        = "一致性验证群"
			groupUpdated       = "一致性验证群-已更新"
		)

		if _, err := database.EnsureGroup(chatID, groupOrigin); err != nil {
			t.Fatalf("初始化群配置失败: %v", err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置权限人失败: %v", err)
		}

		group, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取群配置失败: %v", err)
		}
		if group.Title != groupOrigin || group.OwnerUserID != ownerID {
			t.Fatalf("预热群配置异常: %+v", group)
		}
		if !database.IsOwner(chatID, ownerID) {
			t.Fatalf("权限人缓存预热失败")
		}
		if database.IsOperator(chatID, operatorID) {
			t.Fatalf("预热阶段操作人不应存在")
		}

		groupKey := cache.groupKey(chatID)
		ownerKey := cache.userKey(chatID, ownerID)
		newOwnerKey := cache.userKey(chatID, newOwnerID)
		operatorKey := cache.userKey(chatID, operatorID)

		if !redisStringKeyExists(mini, groupKey) {
			t.Fatalf("预热后 Redis 中应存在群配置缓存")
		}
		if !redisStringKeyExists(mini, ownerKey) {
			t.Fatalf("预热后 Redis 中应存在权限人缓存")
		}
		if !redisStringKeyExists(mini, operatorKey) {
			t.Fatalf("预热后 Redis 中应存在普通成员缓存")
		}

		if err := database.UpdateGroupFields(chatID, map[string]any{
			"title":         groupUpdated,
			"owner_user_id": newOwnerID,
		}); err != nil {
			t.Fatalf("更新群配置失败: %v", err)
		}

		if redisStringKeyExists(mini, groupKey) {
			t.Fatalf("DB 更新后应立即失效群配置缓存")
		}
		if redisStringKeyExists(mini, ownerKey) {
			t.Fatalf("DB 更新后应立即失效旧权限人缓存")
		}
		if redisStringKeyExists(mini, newOwnerKey) {
			t.Fatalf("DB 更新后不应残留新权限人缓存")
		}

		group, err = database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("更新后读取群配置失败: %v", err)
		}
		if group.Title != groupUpdated || group.OwnerUserID != newOwnerID {
			t.Fatalf("缓存回源后群配置异常: %+v", group)
		}
		if !redisStringKeyExists(mini, groupKey) {
			t.Fatalf("回源后 Redis 中应重建群配置缓存")
		}
		groupPayload, err := mini.Get(groupKey)
		if err != nil {
			t.Fatalf("读取 Redis 群配置缓存失败: %v", err)
		}
		if !strings.Contains(groupPayload, "\"Title\":\"一致性验证群-已更新\"") || !strings.Contains(groupPayload, "\"OwnerUserID\":45202") {
			t.Fatalf("回源后群配置缓存内容不正确: %s", groupPayload)
		}

		if !database.IsOwner(chatID, newOwnerID) {
			t.Fatalf("新权限人应具备 owner 权限")
		}
		if database.IsOwner(chatID, ownerID) {
			t.Fatalf("旧权限人缓存失效后不应再具备 owner 权限")
		}

		if err := database.AddOperator(chatID, operatorID, "cache-consistency-op", false); err != nil {
			t.Fatalf("添加操作人失败: %v", err)
		}
		if redisStringKeyExists(mini, operatorKey) {
			t.Fatalf("DB 更新后应立即失效操作人缓存")
		}
		if !database.IsOperator(chatID, operatorID) {
			t.Fatalf("缓存失效后应回源并命中新操作人权限")
		}
		operatorPayload, err := mini.Get(operatorKey)
		if err != nil {
			t.Fatalf("读取 Redis 操作人缓存失败: %v", err)
		}
		if !strings.Contains(operatorPayload, "\"role\":\"operator\"") || !strings.Contains(operatorPayload, "\"permission_level\":1") {
			t.Fatalf("操作人缓存回源后内容异常: %s", operatorPayload)
		}
	})
}
