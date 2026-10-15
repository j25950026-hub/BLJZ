package bot

import (
	"context"
	"errors"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type addressVerificationPhotoSendResult struct {
	mediaMode             string
	mediaURLReady         bool
	mediaPublishDuration  time.Duration
	sendAttemptCount      int
	telegramResultClass   string
	mediaPublishErrorText string
}

func (a *App) sendAddressVerificationPhoto(chatID int64, replyTo int, caption string, imageBytes []byte) (telegramRequestTimingSnapshot, addressVerificationPhotoSendResult, error) {
	result := addressVerificationPhotoSendResult{
		mediaMode:           "multipart",
		sendAttemptCount:    1,
		telegramResultClass: "unknown",
	}
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
		Name:  "address_verify.jpg",
		Bytes: imageBytes,
	})
	photo.Caption = caption
	photo.ParseMode = telegramParseModeHTML
	photo.ReplyToMessageID = replyTo

	useURL := false
	if publisher := a.addressVerificationMediaPublisher; publisher != nil {
		startedAt := time.Now()
		publicURL, publishErr := publisher.Publish(context.Background(), imageBytes)
		result.mediaPublishDuration = time.Since(startedAt)
		if publishErr == nil {
			photo.File = tgbotapi.FileURL(publicURL)
			result.mediaMode = "local_url"
			result.mediaURLReady = true
			useURL = true
		} else {
			result.mediaMode = "multipart_fallback"
			result.mediaPublishErrorText = publishErr.Error()
		}
	}

	var timing telegramRequestTimingSnapshot
	var err error
	if useURL {
		timing, err = a.sendWithPrioritySingleAttemptTiming(outboundPriorityNormalChat, photo)
	} else {
		timing, err = a.sendWithPriorityTiming(outboundPriorityNormalChat, photo)
	}
	result.telegramResultClass = classifyAddressVerificationTelegramResult(err)
	return timing, result, err
}

func classifyAddressVerificationTelegramResult(err error) string {
	if err == nil {
		return "success"
	}
	var telegramErr *tgbotapi.Error
	if errors.As(err, &telegramErr) && telegramErr.Code >= 400 && telegramErr.Code < 500 {
		return "confirmed_rejection"
	}
	return "unknown"
}
