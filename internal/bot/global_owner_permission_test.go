package bot

import (
	"context"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestGlobalOwnerCannotMutateGlobalOwnersSilently(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "global-owner-permission.db")

	const (
		chatID       int64 = -100777001
		localOwnerID int64 = 77700101
		globalOwner  int64 = 77700102
		targetID     int64 = 77700103
	)
	if _, err := database.EnsureGroup(chatID, "全局权限人静默测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, localOwnerID); err != nil {
		t.Fatalf("设置本群权限人失败: %v", err)
	}
	if err := database.AddGlobalOwner(globalOwner, "global777"); err != nil {
		t.Fatalf("设置全局权限人失败: %v", err)
	}

	baseDate := int(time.Now().Unix())
	setCommand := tgbotapi.Update{
		UpdateID: 777001,
		Message: &tgbotapi.Message{
			MessageID: 777001,
			Date:      baseDate,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "全局权限人静默测试群",
			},
			From: &tgbotapi.User{
				ID:       globalOwner,
				UserName: "global777",
			},
			Text: "设置全局权限人",
			ReplyToMessage: &tgbotapi.Message{
				From: &tgbotapi.User{
					ID:       targetID,
					UserName: "target777",
				},
			},
		},
	}
	deleteCommand := tgbotapi.Update{
		UpdateID: 777002,
		Message: &tgbotapi.Message{
			MessageID: 777002,
			Date:      baseDate + 1,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "全局权限人静默测试群",
			},
			From: &tgbotapi.User{
				ID:       globalOwner,
				UserName: "global777",
			},
			Text: "删除全局权限人",
			ReplyToMessage: &tgbotapi.Message{
				From: &tgbotapi.User{
					ID:       globalOwner,
					UserName: "global777",
				},
			},
		},
	}

	app.dispatchUpdate(context.Background(), setCommand)
	app.dispatchUpdate(context.Background(), deleteCommand)

	if !database.IsGlobalOwner(globalOwner) {
		t.Fatalf("全局权限人不应被删除")
	}
	if database.IsGlobalOwner(targetID) {
		t.Fatalf("全局权限人不应新增目标用户")
	}

	rows, err := database.ListGlobalOwners()
	if err != nil {
		t.Fatalf("读取全局权限人列表失败: %v", err)
	}
	if len(rows) != 1 || rows[0].UserID != globalOwner {
		t.Fatalf("全局权限人列表异常: %+v", rows)
	}

	if snapshot := capture.snapshot(); len(snapshot) != 0 {
		t.Fatalf("应保持静默，不应发送任何回执: %+v", snapshot)
	}
}
