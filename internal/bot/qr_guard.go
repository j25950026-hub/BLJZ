package bot

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const qrImageMaxBytes int64 = 5 * 1024 * 1024

type telegramFileURLResolver interface {
	GetFileDirectURL(fileID string) (string, error)
}

func (a *App) inspectQRCodeAddressByImage(msg *tgbotapi.Message) (bool, error) {
	if a != nil && !a.businessRiskControlsEnabled() {
		return false, nil
	}
	if a == nil || a.api == nil || a.address == nil || msg == nil || msg.Chat == nil {
		return false, nil
	}
	fileID := extractImageFileIDFromMessage(msg)
	if fileID == "" {
		return false, nil
	}
	payload, err := downloadTelegramFileByID(a.api, fileID, qrImageMaxBytes)
	if err != nil {
		return false, err
	}
	hits, err := a.address.ScanQRCodeImageForAddresses(payload)
	if err != nil {
		return false, err
	}
	for _, hit := range hits {
		if !hit.Validation.Blacklisted {
			continue
		}
		label := strings.TrimSpace(hit.Validation.BlacklistLabel)
		if label == "" {
			label = "未知来源"
		}
		detail := fmt.Sprintf("source=qr network=%s address=%s scope=%s label=%s", hit.Validation.Network, hit.Address, hit.Validation.BlacklistScope, label)
		userID := int64(0)
		if msg.From != nil {
			userID = msg.From.ID
		}
		if a.db != nil {
			_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, userID, "qr_blacklisted_address", "warn", detail)
		}
		_ = a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("检测到二维码地址命中%s：%s，请勿通过图片传播可疑地址。", hit.Validation.BlacklistScope, label))
		return true, nil
	}
	return false, nil
}

func extractImageFileIDFromMessage(msg *tgbotapi.Message) string {
	if msg == nil {
		return ""
	}
	largestIdx := -1
	for idx := range msg.Photo {
		if strings.TrimSpace(msg.Photo[idx].FileID) == "" {
			continue
		}
		if largestIdx == -1 || msg.Photo[idx].FileSize > msg.Photo[largestIdx].FileSize {
			largestIdx = idx
		}
	}
	if largestIdx >= 0 {
		return strings.TrimSpace(msg.Photo[largestIdx].FileID)
	}
	if msg.Document != nil {
		mime := strings.ToLower(strings.TrimSpace(msg.Document.MimeType))
		if strings.HasPrefix(mime, "image/") {
			return strings.TrimSpace(msg.Document.FileID)
		}
	}
	return ""
}

func downloadTelegramFileByID(api telegramFileURLResolver, fileID string, maxBytes int64) ([]byte, error) {
	if api == nil {
		return nil, errors.New("telegram api 不能为空")
	}
	id := strings.TrimSpace(fileID)
	if id == "" {
		return nil, errors.New("file id 不能为空")
	}
	if maxBytes <= 0 {
		maxBytes = qrImageMaxBytes
	}
	directURL, err := api.GetFileDirectURL(id)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, directURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("下载图片失败: %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, errors.New("图片体积超过限制")
	}
	return payload, nil
}
