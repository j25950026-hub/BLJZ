package bot

import (
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestPrivateTutorialTextCoversBusinessFlow(t *testing.T) {
	overview := privateTutorialOverviewText(true, "demo_bot")
	checks := []string{
		"方式A：一键邀请入群",
		"方式B：手动拉群后初始化",
		"进群 -> 设置操作人 -> 开始记账 -> 一键设置汇率 -> 群控面板/查单管理",
		"@demo_bot",
		"WebApp",
	}
	for _, want := range checks {
		if !strings.Contains(overview, want) {
			t.Fatalf("教程总览缺少关键链路：want=%q got=%q", want, overview)
		}
	}

	manual := privateInviteMethodBText("demo_bot")
	if !strings.Contains(manual, "@demo_bot") {
		t.Fatalf("手动邀请教程应包含机器人用户名: %q", manual)
	}
	if !strings.Contains(manual, "显示群ID") {
		t.Fatalf("手动邀请教程应包含群接入校验步骤: %q", manual)
	}
}

func TestPrivateTutorialMarkupIncludesActionButtons(t *testing.T) {
	app := &App{
		api: &tgbotapi.BotAPI{
			Self: tgbotapi.User{UserName: "demo_bot"},
		},
	}

	markup := app.privateTutorialMarkup(1001)
	if len(markup.InlineKeyboard) == 0 {
		t.Fatalf("教程按钮不应为空")
	}

	expectedActions := []string{
		privateCallbackPrefix + "tutorial_manual",
		privateCallbackPrefix + "tutorial_init",
		privateCallbackPrefix + "tutorial_faq",
		privateCallbackPrefix + "panel",
		privateCallbackPrefix + "menu",
	}
	for _, want := range expectedActions {
		if !containsCallbackData(markup, want) {
			t.Fatalf("教程按钮缺少动作: %s", want)
		}
	}

	if !containsURL(markup, "https://t.me/demo_bot?startgroup=true") {
		t.Fatalf("教程按钮缺少一键邀请链接")
	}
	if containsCallbackData(markup, privateCallbackPrefix+"tutorial_auto") {
		t.Fatalf("方式A按钮不应再使用回调，应直接走选群邀请链接")
	}
}

func containsCallbackData(markup tgbotapi.InlineKeyboardMarkup, target string) bool {
	for _, row := range markup.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData != nil && strings.TrimSpace(*btn.CallbackData) == target {
				return true
			}
		}
	}
	return false
}

func containsURL(markup tgbotapi.InlineKeyboardMarkup, target string) bool {
	for _, row := range markup.InlineKeyboard {
		for _, btn := range row {
			if btn.URL != nil && strings.TrimSpace(*btn.URL) == target {
				return true
			}
		}
	}
	return false
}
