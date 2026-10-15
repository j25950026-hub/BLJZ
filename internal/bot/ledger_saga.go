package bot

import (
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/utils"
)

const (
	ledgerSagaEventCompensated       = "ledger_saga_compensated"
	ledgerSagaEventCompensationError = "ledger_saga_compensation_failed"
	ledgerSagaErrorTextLimit         = 240
)

func (a *App) deliverLedgerReceiptWithSaga(msg *tgbotapi.Message, parsed *utils.LedgerParsed, entryID int64, receiptText string, markup *tgbotapi.InlineKeyboardMarkup, parseMode string) error {
	if err := a.replyBookkeepingResultWithMarkupAndParseMode(msg, "ledger", receiptText, true, markup, parseMode); err != nil {
		return a.compensateLedgerOnReceiptFailure(msg, parsed, entryID, err)
	}
	return nil
}

func (a *App) compensateLedgerOnReceiptFailure(msg *tgbotapi.Message, parsed *utils.LedgerParsed, entryID int64, notifyErr error) error {
	if notifyErr == nil {
		return nil
	}
	if a == nil || a.db == nil || msg == nil || msg.Chat == nil || parsed == nil {
		return notifyErr
	}

	userID := int64(0)
	username := ""
	nickname := ""
	if msg.From != nil {
		userID = msg.From.ID
		username = msg.From.UserName
		nickname = composeNickname(msg.From.FirstName, msg.From.LastName)
	}
	track := utils.BuildMessageLink(msg.Chat.ID, msg.MessageID)
	compensateMessageID := ledgerSagaCompensationMessageID(msg.MessageID)
	reverseResult, reverseErr := a.db.ReverseLastLedger(
		msg.Chat.ID,
		compensateMessageID,
		userID,
		username,
		nickname,
		parsed.EntryType,
		track,
	)
	if reverseErr != nil {
		detail := fmt.Sprintf(
			"entry_id=%d step=notify_receipt notify_error=%q compensate_error=%q",
			entryID,
			truncateLedgerSagaText(notifyErr.Error(), ledgerSagaErrorTextLimit),
			truncateLedgerSagaText(reverseErr.Error(), ledgerSagaErrorTextLimit),
		)
		detail += fmt.Sprintf(" compensate_message_id=%d", compensateMessageID)
		_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, userID, ledgerSagaEventCompensationError, "critical", detail)
		return fmt.Errorf("记账通知失败且 Saga 补偿失败: notify_err=%w, compensate_err=%v", notifyErr, reverseErr)
	}

	if reverseResult.Duplicate || reverseResult.NotFound || reverseResult.Settled || reverseResult.ReversalID == 0 || reverseResult.OriginalID != entryID {
		detail := fmt.Sprintf(
			"entry_id=%d reversed_origin=%d reversal_id=%d duplicate=%t not_found=%t settled=%t step=notify_receipt notify_error=%q",
			entryID,
			reverseResult.OriginalID,
			reverseResult.ReversalID,
			reverseResult.Duplicate,
			reverseResult.NotFound,
			reverseResult.Settled,
			truncateLedgerSagaText(notifyErr.Error(), ledgerSagaErrorTextLimit),
		)
		detail += fmt.Sprintf(" compensate_message_id=%d", compensateMessageID)
		_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, userID, ledgerSagaEventCompensationError, "critical", detail)
		return fmt.Errorf(
			"记账通知失败且 Saga 补偿状态异常: notify_err=%w original_id=%d reversal_id=%d duplicate=%t not_found=%t settled=%t",
			notifyErr,
			reverseResult.OriginalID,
			reverseResult.ReversalID,
			reverseResult.Duplicate,
			reverseResult.NotFound,
			reverseResult.Settled,
		)
	}

	detail := fmt.Sprintf(
		"entry_id=%d reversal_id=%d step=notify_receipt notify_error=%q",
		entryID,
		reverseResult.ReversalID,
		truncateLedgerSagaText(notifyErr.Error(), ledgerSagaErrorTextLimit),
	)
	detail += fmt.Sprintf(" compensate_message_id=%d", compensateMessageID)
	_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, userID, ledgerSagaEventCompensated, "warn", detail)
	log.Printf("记账 Saga 补偿完成: chat_id=%d message_id=%d entry_id=%d reversal_id=%d", msg.Chat.ID, msg.MessageID, entryID, reverseResult.ReversalID)
	return fmt.Errorf("记账通知失败，已执行 Saga 补偿回滚: %w", notifyErr)
}

func truncateLedgerSagaText(raw string, limit int) string {
	trimmed := strings.TrimSpace(raw)
	if limit <= 0 || len([]rune(trimmed)) <= limit {
		return trimmed
	}
	runes := []rune(trimmed)
	return string(runes[:limit]) + "..."
}

func ledgerSagaCompensationMessageID(messageID int) int {
	if messageID == 0 {
		return -1
	}
	if messageID < 0 {
		return messageID
	}
	return -messageID
}
