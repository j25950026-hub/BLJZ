package bot

import (
	"context"
	"fmt"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestInteractiveBroadcastTagFlowSupportsPhotoAndVideo(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "interactive-broadcast-media-flow.db")
	sourceChatID := int64(-100662001)
	targetChatID := int64(-100662002)
	ownerID := int64(662001)
	memberID := int64(662099)

	if _, err := database.EnsureGroup(sourceChatID, "群发媒体源群"); err != nil {
		t.Fatalf("初始化来源群失败: %v", err)
	}
	if _, err := database.EnsureGroup(targetChatID, "群发媒体目标群"); err != nil {
		t.Fatalf("初始化目标群失败: %v", err)
	}
	if err := database.SetOwner(sourceChatID, ownerID); err != nil {
		t.Fatalf("设置来源群权限人失败: %v", err)
	}
	if err := database.UpdateGroupFields(sourceChatID, map[string]any{"group_tags": "业务A"}); err != nil {
		t.Fatalf("设置来源群标签失败: %v", err)
	}
	if err := database.UpdateGroupFields(targetChatID, map[string]any{"group_tags": "业务A"}); err != nil {
		t.Fatalf("设置目标群标签失败: %v", err)
	}

	baseDate := int(time.Now().Unix())

	// 第一轮：图片（无文字）
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		662101, 662101, sourceChatID, ownerID, "owner_media", "开启群发功能", baseDate,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupPhotoUpdate(
		662102, 662102, sourceChatID, ownerID, "owner_media", "", baseDate+1,
	))
	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, sourceChatID, 662102, "本次内容：图片") {
		t.Fatalf("图片内容草稿识别异常: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, sourceChatID, 662102, "请点击下方标签按钮执行群发") {
		t.Fatalf("标签按钮提示缺失: %+v", snapshot)
	}

	photoCallback := &tgbotapi.CallbackQuery{
		ID:   "interactive-media-bt-photo",
		Data: buildInteractiveBroadcastTagCallbackData(0),
		From: &tgbotapi.User{
			ID:       memberID,
			UserName: "member_clicker",
		},
		Message: &tgbotapi.Message{
			MessageID: 900201,
			Chat: &tgbotapi.Chat{
				ID:    sourceChatID,
				Type:  "supergroup",
				Title: "群发媒体源群",
			},
		},
	}
	if err := app.handleCallbackQuery(context.Background(), photoCallback); err != nil {
		t.Fatalf("图片标签点击失败: %v", err)
	}
	photoSnapshot := waitForReplyContaining(t, capture, sourceChatID, 900201, "本次发送内容：图片")
	if !fourLayerHasReply(photoSnapshot, sourceChatID, 900201, "本次发送内容：图片") {
		t.Fatalf("图片群发结果回执异常: %+v", capture.snapshot())
	}
	if !fourLayerHasReply(photoSnapshot, sourceChatID, 900201, "发送成功：2") {
		t.Fatalf("图片群发发送数量异常: %+v", capture.snapshot())
	}

	// 第二轮：视频+文字
	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		662103, 662103, sourceChatID, ownerID, "owner_media", "开启群发功能", baseDate+2,
	))
	app.dispatchUpdate(context.Background(), fourLayerGroupVideoUpdate(
		662104, 662104, sourceChatID, ownerID, "owner_media", "今晚九点开会", baseDate+3,
	))
	snapshot = capture.snapshot()
	if !fourLayerHasReply(snapshot, sourceChatID, 662104, "本次内容：视频+文字：今晚九点开会") {
		t.Fatalf("视频+文字草稿识别异常: %+v", snapshot)
	}

	videoCallback := &tgbotapi.CallbackQuery{
		ID:   "interactive-media-bt-video",
		Data: buildInteractiveBroadcastTagCallbackData(0),
		From: &tgbotapi.User{
			ID:       memberID,
			UserName: "member_clicker",
		},
		Message: &tgbotapi.Message{
			MessageID: 900202,
			Chat: &tgbotapi.Chat{
				ID:    sourceChatID,
				Type:  "supergroup",
				Title: "群发媒体源群",
			},
		},
	}
	if err := app.handleCallbackQuery(context.Background(), videoCallback); err != nil {
		t.Fatalf("视频标签点击失败: %v", err)
	}
	videoSnapshot := waitForReplyContaining(t, capture, sourceChatID, 900202, "本次发送内容：视频+文字：今晚九点开会")
	if !fourLayerHasReply(videoSnapshot, sourceChatID, 900202, "本次发送内容：视频+文字：今晚九点开会") {
		t.Fatalf("视频群发结果回执异常: %+v", capture.snapshot())
	}
}

func fourLayerGroupPhotoUpdate(updateID, messageID int, chatID, userID int64, username, caption string, date int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "四层测试群",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: username,
			},
			Caption: caption,
			Photo: []tgbotapi.PhotoSize{
				{
					FileID:       fmt.Sprintf("photo_%d", messageID),
					FileUniqueID: fmt.Sprintf("photo_unique_%d", messageID),
					Width:        100,
					Height:       100,
				},
			},
		},
	}
}

func fourLayerGroupVideoUpdate(updateID, messageID int, chatID, userID int64, username, caption string, date int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "四层测试群",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: username,
			},
			Caption: caption,
			Video: &tgbotapi.Video{
				FileID:       fmt.Sprintf("video_%d", messageID),
				FileUniqueID: fmt.Sprintf("video_unique_%d", messageID),
				Width:        640,
				Height:       360,
				Duration:     1,
			},
		},
	}
}
