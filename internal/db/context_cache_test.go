package db

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestControlPoint223ContextCaching(t *testing.T) {
	t.Run("控制点 2.2.3 功能验证", func(t *testing.T) {
		mini, err := miniredis.Run()
		if err != nil {
			t.Fatalf("启动 miniredis 失败: %v", err)
		}
		defer mini.Close()

		database, err := NewDatabase(filepath.Join(t.TempDir(), "context-cache.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		cache, err := NewRedisContextCache(RedisContextCacheConfig{
			Addr:      mini.Addr(),
			KeyPrefix: "ctxcache:",
			TTL:       2 * time.Minute,
			Timeout:   2 * time.Second,
		})
		if err != nil {
			t.Fatalf("初始化 Redis 上下文缓存失败: %v", err)
		}
		defer cache.Close()
		database.SetContextCache(cache)

		const (
			chatID        int64 = -10022334455
			ownerID       int64 = 9001
			newOwnerID    int64 = 9002
			operatorID    int64 = 9010
			observerID    int64 = 9020
			bypassOwnerID int64 = 9030
		)

		group, err := database.EnsureGroup(chatID, "缓存测试群")
		if err != nil {
			t.Fatalf("初始化群配置失败: %v", err)
		}
		if group.ChatID != chatID {
			t.Fatalf("群配置 chat_id 异常: got=%d want=%d", group.ChatID, chatID)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置权限人失败: %v", err)
		}
		if err := database.AddOperator(chatID, operatorID, "cache-op", false); err != nil {
			t.Fatalf("添加操作人失败: %v", err)
		}

		group, err = database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("查询群配置失败: %v", err)
		}
		if group.Title != "缓存测试群" || group.OwnerUserID != ownerID {
			t.Fatalf("群配置内容异常: %+v", group)
		}
		if !database.IsOwner(chatID, ownerID) {
			t.Fatalf("权限人应命中 owner 上下文")
		}
		if !database.IsOperator(chatID, operatorID) {
			t.Fatalf("操作人应命中 operator 上下文")
		}
		if !database.IsPrivileged(chatID, operatorID) {
			t.Fatalf("操作人权限等级应为 privileged")
		}
		if database.IsPrivileged(chatID, observerID) {
			t.Fatalf("普通成员不应具备 privileged 权限")
		}

		groupPayload, err := mini.Get(cache.groupKey(chatID))
		if err != nil {
			t.Fatalf("Redis 中应存在群配置缓存: %v", err)
		}
		if !strings.Contains(groupPayload, `"ChatID":-10022334455`) {
			t.Fatalf("群配置缓存内容异常: %s", groupPayload)
		}
		operatorPayload, err := mini.Get(cache.userKey(chatID, operatorID))
		if err != nil {
			t.Fatalf("Redis 中应存在操作人上下文缓存: %v", err)
		}
		if !strings.Contains(operatorPayload, `"role":"operator"`) || !strings.Contains(operatorPayload, `"permission_level":1`) {
			t.Fatalf("操作人缓存缺少角色或权限等级: %s", operatorPayload)
		}

		if _, err := database.raw.Exec(`UPDATE group_config SET title = ?, owner_user_id = ? WHERE chat_id = ?`, "被旁路改写的标题", bypassOwnerID, chatID); err != nil {
			t.Fatalf("旁路改写群配置失败: %v", err)
		}
		if _, err := database.raw.Exec(`DELETE FROM operators WHERE chat_id = ? AND user_id = ? AND is_global = 0`, chatID, operatorID); err != nil {
			t.Fatalf("旁路删除操作人失败: %v", err)
		}

		cachedGroup, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取缓存群配置失败: %v", err)
		}
		if cachedGroup.Title != "缓存测试群" || cachedGroup.OwnerUserID != ownerID {
			t.Fatalf("群配置应优先命中缓存，实际=%+v", cachedGroup)
		}
		if !database.IsOwner(chatID, ownerID) {
			t.Fatalf("旁路改写后，owner 判断应仍命中缓存")
		}
		if !database.IsOperator(chatID, operatorID) {
			t.Fatalf("旁路删除后，operator 判断应仍命中缓存")
		}

		if err := database.UpdateGroupFields(chatID, map[string]any{
			"title":         "接口更新标题",
			"owner_user_id": newOwnerID,
		}); err != nil {
			t.Fatalf("更新群配置失败: %v", err)
		}
		refreshedGroup, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取更新后的群配置失败: %v", err)
		}
		if refreshedGroup.Title != "接口更新标题" || refreshedGroup.OwnerUserID != newOwnerID {
			t.Fatalf("群配置缓存失效后应回源并刷新: %+v", refreshedGroup)
		}
		if !database.IsOwner(chatID, newOwnerID) {
			t.Fatalf("新权限人应具备 owner 权限")
		}
		if database.IsOwner(chatID, ownerID) {
			t.Fatalf("旧权限人缓存应失效")
		}

		if err := database.RemoveOperator(chatID, operatorID, false); err != nil {
			t.Fatalf("移除操作人失败: %v", err)
		}
		if database.IsOperator(chatID, operatorID) {
			t.Fatalf("移除后不应再具备 operator 角色")
		}
		if database.IsPrivileged(chatID, operatorID) {
			t.Fatalf("移除后不应再具备 privileged 权限")
		}
	})

	t.Run("控制点 2.2.3 全局操作人缓存失效验证", func(t *testing.T) {
		mini, err := miniredis.Run()
		if err != nil {
			t.Fatalf("启动 miniredis 失败: %v", err)
		}
		defer mini.Close()

		database, err := NewDatabase(filepath.Join(t.TempDir(), "context-cache-global.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		cache, err := NewRedisContextCache(RedisContextCacheConfig{
			Addr:      mini.Addr(),
			KeyPrefix: "ctxcache:",
			TTL:       2 * time.Minute,
			Timeout:   2 * time.Second,
		})
		if err != nil {
			t.Fatalf("初始化 Redis 上下文缓存失败: %v", err)
		}
		defer cache.Close()
		database.SetContextCache(cache)

		const (
			chatA          int64 = -10011
			chatB          int64 = -10012
			globalOperator int64 = 9988
		)
		if _, err := database.EnsureGroup(chatA, "A群"); err != nil {
			t.Fatalf("初始化 A 群失败: %v", err)
		}
		if _, err := database.EnsureGroup(chatB, "B群"); err != nil {
			t.Fatalf("初始化 B 群失败: %v", err)
		}

		if err := database.AddOperator(chatA, globalOperator, "global-op", true); err != nil {
			t.Fatalf("添加全局操作人失败: %v", err)
		}
		if !database.IsPrivileged(chatA, globalOperator) || !database.IsPrivileged(chatB, globalOperator) {
			t.Fatalf("全局操作人应在所有群具备 privileged 权限")
		}
		if err := database.RemoveOperator(chatA, globalOperator, true); err != nil {
			t.Fatalf("移除全局操作人失败: %v", err)
		}
		if database.IsPrivileged(chatA, globalOperator) || database.IsPrivileged(chatB, globalOperator) {
			t.Fatalf("移除全局操作人后，两侧缓存都应失效")
		}
	})
}
