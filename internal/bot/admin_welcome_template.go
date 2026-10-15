package bot

import (
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

// WelcomeTemplateSample is synthetic input used only to render an admin preview.
type WelcomeTemplateSample struct {
	UserID    int64
	Username  string
	FirstName string
	LastName  string
}

type AdminMessageTemplateSample struct {
	ChatID   int64
	UserID   int64
	Username string
	Nickname string
	Text     string
	Now      string
}

type AdminMessageTemplatePreview struct {
	Text      string
	ParseMode string
}

// RenderAdminMessageTemplatePreview renders an automatic-reply template without sending a message.
func RenderAdminMessageTemplatePreview(rawTemplate string, sample AdminMessageTemplateSample) (AdminMessageTemplatePreview, error) {
	now := sample.Now
	if now == "" {
		now = time.Now().Format("2006-01-02 15:04:05")
	}
	rendered, err := newTemplateRenderer().RenderMessageTemplate(rawTemplate, messageTemplateData{
		ChatID:   sample.ChatID,
		UserID:   sample.UserID,
		Username: sample.Username,
		Nickname: sample.Nickname,
		Text:     sample.Text,
		Now:      now,
	})
	if err != nil {
		return AdminMessageTemplatePreview{}, err
	}
	return AdminMessageTemplatePreview{Text: rendered.Text, ParseMode: rendered.ParseMode}, nil
}

// RenderWelcomeTemplatePreview shares the production renderer without sending a Telegram message.
func RenderWelcomeTemplatePreview(groupName, template string, sample WelcomeTemplateSample) string {
	return buildMemberJoinWelcomeText(
		db.GroupConfig{Title: groupName, WelcomeText: template},
		groupName,
		tgbotapi.User{
			ID:        sample.UserID,
			UserName:  sample.Username,
			FirstName: sample.FirstName,
			LastName:  sample.LastName,
		},
	)
}
