package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
	"go-bot/internal/utils"
)

const (
	ledgerReceiptOutboxMaxAttempts      = 3
	ledgerReceiptOutboxPollInterval     = time.Second
	ledgerReceiptOutboxVisibilityWindow = 2 * time.Minute
	ledgerReceiptOutboxRiskEventType    = "ledger_receipt_outbox_failed"
)

type ledgerReceiptOutboxPayload struct {
	Group        db.GroupConfig     `json:"group"`
	Fallback     ledgerCardFallback `json:"fallback"`
	WarningLines []string           `json:"warning_lines,omitempty"`
	ParseMode    string             `json:"parse_mode"`
	Cacheable    bool               `json:"cacheable"`
}

func (a *App) buildLedgerReceiptOutboxInput(
	msg *tgbotapi.Message,
	g db.GroupConfig,
	parsed *utils.LedgerParsed,
	ledgerInput db.LedgerEntryInput,
	result db.LedgerApplyResult,
	transactionAt time.Time,
	timeAnomalyWarning bool,
) (*db.LedgerReceiptOutboxInput, error) {
	if msg == nil || msg.Chat == nil || parsed == nil || result.EntryID == 0 {
		return nil, nil
	}
	warningLines := buildLedgerReceiptWarningLines(a, g, ledgerInput, result, transactionAt, timeAnomalyWarning)
	payload := ledgerReceiptOutboxPayload{
		Group: g,
		Fallback: ledgerCardFallback{
			EntryType:   parsed.EntryType,
			Amount:      parsed.Amount,
			Currency:    parsed.Currency,
			Rate:        ledgerInput.Rate,
			FinalAmount: ledgerInput.FinalAmount,
			Note:        parsed.Note,
			CreatedAt:   transactionAt,
		},
		WarningLines: warningLines,
		ParseMode:    telegramParseModeMarkdownV,
		Cacheable:    true,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &db.LedgerReceiptOutboxInput{
		ChatID:          msg.Chat.ID,
		SourceMessageID: msg.MessageID,
		EntryID:         result.EntryID,
		UserID:          ledgerInput.UserID,
		CommandKey:      "ledger",
		Payload:         string(raw),
	}, nil
}

func buildLedgerReceiptWarningLines(
	a *App,
	g db.GroupConfig,
	ledgerInput db.LedgerEntryInput,
	result db.LedgerApplyResult,
	transactionAt time.Time,
	timeAnomalyWarning bool,
) []string {
	if a == nil || !a.businessRiskControlsEnabled() {
		return nil
	}
	warningLines := make([]string, 0, 5)
	if result.RecentAmountSeenCount > 1 {
		warningLines = append(warningLines, escapeTelegramMarkdownV2(
			fmt.Sprintf("⚠️ 重复预警：%s相同金额已出现 %d 次。", duplicateAlertWindowText, result.RecentAmountSeenCount),
		))
	}
	if strings.TrimSpace(ledgerInput.CardNo) != "" && result.RecentCardSeenCount > 1 {
		duplicateTimes := result.RecentCardSeenCount - 1
		scopeText := "本群"
		if g.DuplicateAlertEnabled == 1 && strings.TrimSpace(g.GroupType) != "" {
			scopeText = fmt.Sprintf("同类型群（%s）", strings.TrimSpace(g.GroupType))
		}
		warningLines = append(warningLines, escapeTelegramMarkdownV2(
			fmt.Sprintf("⚠️ 重复预警：当前信息重复发送%d次（%s，%s）。", duplicateTimes, scopeText, duplicateAlertWindowText),
		))
	}
	if result.RecentAddressSeenCount > 1 {
		warningLines = append(warningLines, escapeTelegramMarkdownV2(
			fmt.Sprintf("⚠️ 重复预警：%s相同地址已出现 %d 次。", duplicateAlertWindowText, result.RecentAddressSeenCount),
		))
	}
	if timeAnomalyWarning {
		warningLines = append(warningLines, escapeTelegramMarkdownV2(
			fmt.Sprintf("⚠️ 异常时间预警：交易时间 %s，已通知权限人复核。", transactionAt.Format("2006-01-02 15:04:05")),
		))
	}
	return warningLines
}

func (a *App) runLedgerReceiptOutboxWorkers(ctx context.Context) {
	if a == nil {
		return
	}
	workers := ledgerReceiptOutboxWorkersFromConfig(a.cfg)
	for i := 0; i < workers; i++ {
		go a.runLedgerReceiptOutboxLoop(ctx)
	}
}

func (a *App) runLedgerReceiptOutboxLoop(ctx context.Context) {
	if a == nil || a.db == nil || a.api == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(ledgerReceiptOutboxPollInterval)
	defer ticker.Stop()
	for {
		delivered, err := a.deliverNextLedgerReceipt(ctx)
		if err != nil && !errorsIsContextDone(err) {
			log.Printf("异步记账回执发送失败: %v", err)
		}
		if delivered {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) deliverLedgerReceiptInlineForDirectCall() error {
	if a == nil || a.ledgerReceiptAsyncOnly || a.bookkeepingQueue != nil {
		return nil
	}
	_, err := a.deliverNextLedgerReceiptAt(context.Background(), time.Now().UTC())
	return err
}

func (a *App) deliverNextLedgerReceipt(ctx context.Context) (bool, error) {
	return a.deliverNextLedgerReceiptAt(ctx, a.currentTimeInLocation().UTC())
}

func (a *App) deliverNextLedgerReceiptAt(ctx context.Context, now time.Time) (bool, error) {
	if a == nil || a.db == nil || a.api == nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	startedAt := time.Now()
	item, err := a.db.ClaimLedgerReceiptOutbox(now.UTC(), ledgerReceiptOutboxVisibilityWindow)
	if err != nil || item == nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		_, releaseErr := a.db.ReleaseLedgerReceiptOutboxClaim(item.ChatID, item.ID, item.ProcessingStarted)
		if releaseErr != nil {
			a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("error", time.Since(startedAt))
			return false, releaseErr
		}
		a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("canceled", time.Since(startedAt))
		return false, err
	}

	message, receiptText, parseMode, err := a.buildLedgerReceiptMessage(item)
	if err != nil {
		_, _ = a.db.MarkLedgerReceiptOutboxRetryIfProcessing(item.ChatID, item.ID, item.ProcessingStarted, err.Error(), a.currentTimeInLocation().UTC(), true)
		a.recordLedgerReceiptOutboxFailure(item, err, true)
		a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("failed", time.Since(startedAt))
		return true, nil
	}

	sendErr := a.dispatchOutboundTask(ctx, outboundPriorityBookkeepingReceipt, func(context.Context) error {
		_, err := a.api.Send(message)
		metrics := a.currentOutboundMetrics()
		priorityLabel := outboundPriorityMetricLabel(outboundPriorityBookkeepingReceipt)
		metrics.ObserveOutboundSend("send", priorityLabel, outboundSendResultMetricLabel(err))
		if retryAfter := telegramRetryAfterDuration(err); retryAfter > 0 {
			metrics.ObserveTelegramRetryAfter("send", priorityLabel, retryAfter)
		}
		return err
	})
	if sendErr != nil {
		if errorsIsContextDone(sendErr) {
			if _, releaseErr := a.db.ReleaseLedgerReceiptOutboxClaim(item.ChatID, item.ID, item.ProcessingStarted); releaseErr != nil {
				a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("error", time.Since(startedAt))
				return false, releaseErr
			}
			a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("canceled", time.Since(startedAt))
			return false, sendErr
		}
		final := item.AttemptCount >= ledgerReceiptOutboxMaxAttempts
		nextAttemptAt := now.UTC().Add(ledgerReceiptOutboxRetryDelay(item.AttemptCount))
		marked, markErr := a.db.MarkLedgerReceiptOutboxRetryIfProcessing(item.ChatID, item.ID, item.ProcessingStarted, sendErr.Error(), nextAttemptAt, final)
		if markErr != nil {
			a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("error", time.Since(startedAt))
			return true, markErr
		}
		if !marked {
			a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("stale", time.Since(startedAt))
			return true, nil
		}
		if final {
			a.enqueueOutboundDeadLetter("send", outboundPriorityBookkeepingReceipt, message, sendErr)
			a.recordLedgerReceiptOutboxFailure(item, sendErr, true)
			a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("failed", time.Since(startedAt))
		} else {
			a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("retry", time.Since(startedAt))
		}
		return true, nil
	}

	marked, err := a.db.MarkLedgerReceiptOutboxSentIfProcessing(item.ChatID, item.ID, item.ProcessingStarted, now.UTC())
	if err != nil {
		a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("error", time.Since(startedAt))
		return true, err
	}
	if !marked {
		a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("stale", time.Since(startedAt))
		return true, nil
	}
	if item.CommandKey != "" && a.requestResultCache != nil {
		cacheKey := buildRequestResultCacheKey(item.CommandKey, item.ChatID, item.SourceMessageID)
		if cacheKey != "" {
			a.requestResultCache.Set(cacheKey, item.ChatID, encodeBookkeepingCachedResponse(receiptText, parseMode))
		}
	}
	a.currentOutboundMetrics().ObserveLedgerReceiptOutbox("sent", time.Since(startedAt))
	return true, nil
}

func (a *App) buildLedgerReceiptMessage(item *db.LedgerReceiptOutbox) (tgbotapi.MessageConfig, string, string, error) {
	if item == nil {
		return tgbotapi.MessageConfig{}, "", "", fmt.Errorf("ledger receipt outbox item is nil")
	}
	var payload ledgerReceiptOutboxPayload
	if err := json.Unmarshal([]byte(item.Payload), &payload); err != nil {
		return tgbotapi.MessageConfig{}, "", "", err
	}
	parseMode := strings.TrimSpace(payload.ParseMode)
	if parseMode == "" {
		parseMode = telegramParseModeMarkdownV
	}
	group := payload.Group
	if a.db != nil {
		if current, getErr := a.db.GetGroup(item.ChatID); getErr == nil {
			group.LedgerCardHideReversed = current.LedgerCardHideReversed
		}
	}
	receiptText := a.buildAccountingDataCardText(item.ChatID, group, payload.Fallback)
	if len(payload.WarningLines) > 0 {
		receiptText = strings.Join(payload.WarningLines, "\n") + "\n\n" + receiptText
	}
	message := tgbotapi.NewMessage(item.ChatID, receiptText)
	message.ReplyToMessageID = item.SourceMessageID
	message.ParseMode = parseMode
	markup := ledgerCardReceiptMarkup()
	message.ReplyMarkup = markup
	return message, receiptText, parseMode, nil
}

func (a *App) recordLedgerReceiptOutboxFailure(item *db.LedgerReceiptOutbox, cause error, final bool) {
	if a == nil || a.db == nil || item == nil || cause == nil {
		return
	}
	severity := "warn"
	if final {
		severity = "critical"
	}
	detail := fmt.Sprintf(
		"outbox_id=%d entry_id=%d attempts=%d final=%t error=%q",
		item.ID,
		item.EntryID,
		item.AttemptCount,
		final,
		truncateLedgerSagaText(cause.Error(), ledgerSagaErrorTextLimit),
	)
	_ = a.db.AddRiskEvent(item.ChatID, item.SourceMessageID, item.UserID, ledgerReceiptOutboxRiskEventType, severity, detail)
}

func ledgerReceiptOutboxRetryDelay(attempt int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	return time.Duration(attempt) * time.Minute
}

func errorsIsContextDone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
