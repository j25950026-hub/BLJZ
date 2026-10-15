package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestControlPoint423HotDataKeepAlive(t *testing.T) {
	t.Run("控制点 4.2.3 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "hot-data-keep-alive.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		cache := NewMemoryContextCache(MemoryContextCacheConfig{
			TTL:        3 * time.Second,
			MaxEntries: 128,
		})
		defer cache.Close()
		database.SetContextCache(cache)

		database.groupKeepAliveMu.Lock()
		database.groupKeepAliveInterval = 20 * time.Millisecond
		database.groupKeepAliveMu.Unlock()

		const (
			chatID         int64 = -1004203001
			originTitle          = "热点群组原始标题"
			refreshedTitle       = "热点群组异步刷新标题"
			refreshedOwner int64 = 420399
		)

		if _, err := database.EnsureGroup(chatID, originTitle); err != nil {
			t.Fatalf("初始化群配置失败: %v", err)
		}
		if _, err := database.GetGroup(chatID); err != nil {
			t.Fatalf("预热群配置缓存失败: %v", err)
		}

		if _, err := database.raw.Exec(`UPDATE group_config SET title = ?, owner_user_id = ? WHERE chat_id = ?`, refreshedTitle, refreshedOwner, chatID); err != nil {
			t.Fatalf("旁路改写群配置失败: %v", err)
		}

		firstRead, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取缓存群配置失败: %v", err)
		}
		if firstRead.Title != originTitle || firstRead.OwnerUserID != 0 {
			t.Fatalf("首次读取应命中旧缓存，actual=%+v", firstRead)
		}

		deadline := time.Now().Add(800 * time.Millisecond)
		for time.Now().Before(deadline) {
			hotGroup, readErr := database.GetGroup(chatID)
			if readErr != nil {
				t.Fatalf("轮询热点群配置失败: %v", readErr)
			}
			if hotGroup.Title == refreshedTitle && hotGroup.OwnerUserID == refreshedOwner {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		lastGroup, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取最终群配置失败: %v", err)
		}
		t.Fatalf("热点群配置应后台异步刷新并回填缓存，actual=%+v", lastGroup)
	})
}
