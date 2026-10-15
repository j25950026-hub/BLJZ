package bot

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"go-bot/internal/config"
)

func TestControlPoint425DistributedSession(t *testing.T) {
	t.Run("控制点 4.2.5 功能验证", func(t *testing.T) {
		mini, err := miniredis.Run()
		if err != nil {
			t.Fatalf("启动 miniredis 失败: %v", err)
		}
		defer mini.Close()

		const keyPrefix = "session:dist:"
		storeA, err := NewRedisDistributedSessionStore(RedisDistributedSessionStoreConfig{
			Addr:      mini.Addr(),
			KeyPrefix: keyPrefix,
			TTL:       30 * time.Minute,
			Timeout:   time.Second,
		})
		if err != nil {
			t.Fatalf("创建分布式 Session 存储失败: %v", err)
		}
		defer storeA.Close()

		storeB, err := NewRedisDistributedSessionStore(RedisDistributedSessionStoreConfig{
			Addr:      mini.Addr(),
			KeyPrefix: keyPrefix,
			TTL:       30 * time.Minute,
			Timeout:   time.Second,
		})
		if err != nil {
			t.Fatalf("创建第二个分布式 Session 存储失败: %v", err)
		}
		defer storeB.Close()

		now := time.Now()
		session := DistributedSession{
			SessionID: "tg-webapp-9527",
			UserID:    9527,
			LoggedIn:  true,
			Context: map[string]string{
				"scope":           "tg_webapp",
				"selected_chat":   "-100100200300",
				"selected_action": "一键账单",
			},
			IssuedAt:   now.Unix(),
			ExpiresAt:  now.Add(15 * time.Minute).Unix(),
			LastSeenAt: now.Unix(),
		}
		if err := storeA.Save(context.Background(), session); err != nil {
			t.Fatalf("保存分布式 Session 失败: %v", err)
		}

		loaded, ok, err := storeB.Get(context.Background(), session.SessionID)
		if err != nil {
			t.Fatalf("读取分布式 Session 失败: %v", err)
		}
		if !ok {
			t.Fatalf("分布式 Session 应可被其他节点读取")
		}
		if !loaded.LoggedIn {
			t.Fatalf("应保留登录态")
		}
		if loaded.UserID != session.UserID {
			t.Fatalf("UserID 不匹配，want=%d got=%d", session.UserID, loaded.UserID)
		}
		if loaded.Context["selected_chat"] != "-100100200300" {
			t.Fatalf("会话上下文未命中 selected_chat，got=%q", loaded.Context["selected_chat"])
		}

		redisKey := keyPrefix + session.SessionID
		if !mini.Exists(redisKey) {
			t.Fatalf("Redis 中不存在会话键: %s", redisKey)
		}
		if ttl := mini.TTL(redisKey); ttl <= 0 {
			t.Fatalf("会话 TTL 异常: %v", ttl)
		}

		loaded.Context["selected_action"] = "查单列表"
		loaded.LastSeenAt = now.Add(2 * time.Minute).Unix()
		if err := storeB.Save(context.Background(), loaded); err != nil {
			t.Fatalf("更新分布式 Session 失败: %v", err)
		}

		updated, ok, err := storeA.Get(context.Background(), session.SessionID)
		if err != nil {
			t.Fatalf("回读更新后的会话失败: %v", err)
		}
		if !ok {
			t.Fatalf("更新后的会话不应丢失")
		}
		if updated.Context["selected_action"] != "查单列表" {
			t.Fatalf("会话上下文更新未生效，got=%q", updated.Context["selected_action"])
		}

		if err := storeA.Delete(context.Background(), session.SessionID); err != nil {
			t.Fatalf("删除分布式 Session 失败: %v", err)
		}
		_, ok, err = storeB.Get(context.Background(), session.SessionID)
		if err != nil {
			t.Fatalf("删除后读取分布式 Session 失败: %v", err)
		}
		if ok {
			t.Fatalf("会话删除后应不可读取")
		}
	})
}

func TestWebAppAuthWithDistributedSession(t *testing.T) {
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("启动 miniredis 失败: %v", err)
	}
	defer mini.Close()

	store, err := NewRedisDistributedSessionStore(RedisDistributedSessionStoreConfig{
		Addr:      mini.Addr(),
		KeyPrefix: "session:dist:",
		TTL:       30 * time.Minute,
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("初始化分布式 Session 存储失败: %v", err)
	}
	defer store.Close()

	now := time.Now()
	app := &App{
		cfg: config.Config{
			BotToken:       "bot-token",
			TGWebAppURL:    "https://example.com/tg/webapp",
			TGWebAppSecret: "distributed-session-webapp-secret",
			Timezone:       "Asia/Shanghai",
		},
		now:          func() time.Time { return now },
		sessionStore: store,
	}

	webURL := app.BuildWebAppURL(10001)
	if strings.TrimSpace(webURL) == "" {
		t.Fatalf("启用分布式 Session 后应生成 WebApp URL")
	}

	parsed, err := url.Parse(webURL)
	if err != nil {
		t.Fatalf("解析 WebApp URL 失败: %v", err)
	}
	uid, _ := strconv.ParseInt(parsed.Query().Get("uid"), 10, 64)
	exp, _ := strconv.ParseInt(parsed.Query().Get("exp"), 10, 64)
	sig := parsed.Query().Get("sig")
	if uid == 0 || exp == 0 || strings.TrimSpace(sig) == "" {
		t.Fatalf("WebApp URL 缺少鉴权参数: %s", webURL)
	}

	if err := app.VerifyWebAppAuth(uid, exp, sig); err != nil {
		t.Fatalf("分布式 Session 鉴权应通过: %v", err)
	}

	sessionID := buildWebAppSessionID(uid, exp, sig)
	if sessionID == "" {
		t.Fatalf("会话 ID 生成失败")
	}
	if err := store.Delete(context.Background(), sessionID); err != nil {
		t.Fatalf("删除会话失败: %v", err)
	}
	if err := app.VerifyWebAppAuth(uid, exp, sig); err == nil {
		t.Fatalf("会话丢失后鉴权应失败")
	}
}
