package bot

import (
	"errors"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type OutboundMetricsRecorder interface {
	ObserveOutboundQueue(queued, capacity, workers int)
	ObserveOutboundQueueSubmission(priority, result string)
	ObserveOutboundDispatchFallback(priority, result string)
	ObserveOutboundQueueWait(priority string, duration time.Duration)
	ObserveOutboundTask(priority, result string, duration time.Duration)
	ObserveOutboundSend(operation, priority, result string)
	ObserveTelegramRetryAfter(operation, priority string, duration time.Duration)
	ObserveOutboundDeadLetter(operation, priority string)
	ObserveLedgerReceiptOutbox(result string, duration time.Duration)
}

type noopOutboundMetricsRecorder struct{}

func (noopOutboundMetricsRecorder) ObserveOutboundQueue(int, int, int)             {}
func (noopOutboundMetricsRecorder) ObserveOutboundQueueSubmission(string, string)  {}
func (noopOutboundMetricsRecorder) ObserveOutboundDispatchFallback(string, string) {}
func (noopOutboundMetricsRecorder) ObserveOutboundQueueWait(string, time.Duration) {}
func (noopOutboundMetricsRecorder) ObserveOutboundTask(string, string, time.Duration) {
}
func (noopOutboundMetricsRecorder) ObserveOutboundSend(string, string, string) {}
func (noopOutboundMetricsRecorder) ObserveTelegramRetryAfter(string, string, time.Duration) {
}
func (noopOutboundMetricsRecorder) ObserveOutboundDeadLetter(string, string) {}
func (noopOutboundMetricsRecorder) ObserveLedgerReceiptOutbox(string, time.Duration) {
}

const (
	outboundMetricResultAccepted             = "accepted"
	outboundMetricResultDroppedBackpressure  = "dropped_backpressure"
	outboundMetricResultFallbackBackpressure = "fallback_backpressure"
	outboundMetricResultFallbackClosed       = "fallback_closed"
	outboundMetricResultRejected             = "rejected"
	outboundMetricResultDropped              = "dropped"
	outboundMetricResultSuccess              = "success"
	outboundMetricResultError                = "error"
	outboundMetricResultCanceled             = "canceled"
	outboundMetricResultRateLimited          = "rate_limited"
)

func outboundPriorityMetricLabel(priority outboundMessagePriority) string {
	switch normalizeOutboundPriority(priority) {
	case outboundPriorityBookkeepingReceipt:
		return "bookkeeping_receipt"
	case outboundPriorityErrorAlert:
		return "error_alert"
	case outboundPriorityNormalChat:
		return "normal_chat"
	case outboundPriorityBroadcastAd:
		return "broadcast_ad"
	default:
		return "normal_chat"
	}
}

func outboundOperationMetricLabel(operation string) string {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "send":
		return "send"
	case "request":
		return "request"
	default:
		return "other"
	}
}

func outboundSendResultMetricLabel(err error) string {
	if err == nil {
		return outboundMetricResultSuccess
	}
	if errorsIsContextDone(err) {
		return outboundMetricResultCanceled
	}
	var tgErr *tgbotapi.Error
	if errors.As(err, &tgErr) && tgErr.Code == 429 {
		return outboundMetricResultRateLimited
	}
	return outboundMetricResultError
}

func telegramRetryAfterDuration(err error) time.Duration {
	var tgErr *tgbotapi.Error
	if !errors.As(err, &tgErr) || tgErr.Code != 429 || tgErr.RetryAfter <= 0 {
		return 0
	}
	return time.Duration(tgErr.RetryAfter) * time.Second
}
