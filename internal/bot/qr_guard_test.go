package bot

import (
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/services"
)

func TestInspectQRCodeAddressByImageBypassesBusinessRiskWhenDisabled(t *testing.T) {
	app := &App{
		api:     &tgbotapi.BotAPI{},
		address: services.NewAddressService(time.Second),
	}
	msg := &tgbotapi.Message{
		MessageID: 9001,
		Chat: &tgbotapi.Chat{
			ID: -1009001,
		},
		Photo: []tgbotapi.PhotoSize{
			{FileID: "qr-photo-file-id", FileSize: 128},
		},
	}

	blocked, err := app.inspectQRCodeAddressByImage(msg)
	if err != nil {
		t.Fatalf("业务风控关闭后二维码检查不应报错: %v", err)
	}
	if blocked {
		t.Fatalf("业务风控关闭后二码黑名单不应阻断业务")
	}
}
