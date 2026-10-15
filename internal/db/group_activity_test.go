package db

import (
	"path/filepath"
	"testing"
)

func TestListGroupLastSeenAtNormalizesMixedFormats(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "group-activity.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	const (
		chatMixed   int64 = -100710001
		chatInvalid int64 = -100710002
		chatEmpty   int64 = -100710003
	)
	for _, item := range []struct {
		chatID int64
		title  string
	}{
		{chatMixed, "混合时间格式群"},
		{chatInvalid, "非法时间兜底群"},
		{chatEmpty, "无活跃记录群"},
	} {
		if _, err := database.EnsureGroup(item.chatID, item.title); err != nil {
			t.Fatalf("初始化群失败(chat=%d): %v", item.chatID, err)
		}
	}

	inserts := []struct {
		chatID int64
		userID int64
		seenAt string
	}{
		{chatMixed, 71000101, "2026-07-17T00:01:00"},
		{chatMixed, 71000102, "2026-07-17 23:59:00"},
		{chatInvalid, 71000201, "9999-invalid"},
		{chatInvalid, 71000202, "2026-07-18 01:02:03"},
		{chatEmpty, 71000301, "invalid-only"},
	}
	for _, item := range inserts {
		if _, err := database.raw.Exec(
			`INSERT INTO group_users (chat_id, user_id, username, nickname, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
			item.chatID,
			item.userID,
			"user",
			"User",
			item.seenAt,
		); err != nil {
			t.Fatalf("写入活跃记录失败(chat=%d user=%d): %v", item.chatID, item.userID, err)
		}
	}

	got, err := database.ListGroupLastSeenAt([]int64{chatMixed, chatInvalid, chatEmpty, chatMixed, 0})
	if err != nil {
		t.Fatalf("查询群活跃时间失败: %v", err)
	}
	if got[chatMixed] != "2026-07-17 23:59:00" {
		t.Fatalf("混合 T/空格时间格式应取真实最新时间: got=%q", got[chatMixed])
	}
	if got[chatInvalid] != "2026-07-18 01:02:03" {
		t.Fatalf("非法时间不应遮蔽同群有效时间: got=%q", got[chatInvalid])
	}
	if _, ok := got[chatEmpty]; ok {
		t.Fatalf("只有非法活跃记录的群不应返回有效时间: got=%q", got[chatEmpty])
	}
}
