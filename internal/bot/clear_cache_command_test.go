package bot

import (
	"context"
	"testing"
	"time"
)

func TestClearCacheCommandClearsDuplicateAlertHistory(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "clear-cache-command.db")
	chatID := int64(-1004410902)
	ownerID := int64(4410902)
	cachedUserID := int64(4410910)
	if _, err := database.EnsureGroup(chatID, "清除缓存测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(chatID, cachedUserID, "cached_member", "缓存成员"); err != nil {
		t.Fatalf("写入用户缓存失败: %v", err)
	}
	app.requestResultCache = newRequestResultCache(time.Minute)
	resultCacheKey := buildRequestResultCacheKey("ledger", chatID, 4419204)
	app.requestResultCache.Set(resultCacheKey, chatID, "请求结果缓存")

	accountNo := "110288285406"
	if count, _, err := database.RecordTextDuplicateAccount(chatID, 4419201, accountNo); err != nil {
		t.Fatalf("写入纯文本重复记录失败: %v", err)
	} else if count != 1 {
		t.Fatalf("首条纯文本记录计数异常: %d", count)
	}

	baseDate := int(time.Now().Unix())
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(4419202, 4419202, chatID, ownerID, "owner_cache", "清除缓存", baseDate))

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 4419202, "重复预警记录") {
		t.Fatalf("清除缓存回执应包含重复预警记录清理结果: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, chatID, 4419202, "用户缓存") || fourLayerHasReply(snapshot, chatID, 4419202, "请求结果缓存") {
		t.Fatalf("清除缓存回执不应包含非重复缓存清理结果: %+v", snapshot)
	}

	if count, _, err := database.RecordTextDuplicateAccount(chatID, 4419203, accountNo); err != nil {
		t.Fatalf("清理后写入纯文本重复记录失败: %v", err)
	} else if count != 1 {
		t.Fatalf("清理后纯文本重复计数应重置为 1，实际=%d", count)
	}
	if row, err := database.FindUserByUsername(chatID, "cached_member"); err != nil {
		t.Fatalf("读取用户缓存失败: %v", err)
	} else if row == nil || row.UserID != cachedUserID {
		t.Fatalf("清除缓存不应删除用户缓存: %+v", row)
	}
	if got, ok := app.requestResultCache.Get(resultCacheKey); !ok || got != "请求结果缓存" {
		t.Fatalf("清除缓存不应删除请求结果缓存: hit=%t got=%q", ok, got)
	}
}
