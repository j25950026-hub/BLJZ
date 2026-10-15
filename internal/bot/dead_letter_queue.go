package bot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

const (
	outboundDeadLetterPayloadMaxRunes = 2048
	outboundDeadLetterErrorMaxRunes   = 512
	outboundDeadLetterTypeMaxRunes    = 128
	outboundDeadLetterOpMaxRunes      = 32
)

var addressVerificationMediaURLPattern = regexp.MustCompile(`https://autobookbl\.site/tg/media/[0-9a-f]{32}\.jpg`)

func (a *App) enqueueOutboundDeadLetter(operation string, priority outboundMessagePriority, message tgbotapi.Chattable, cause error) {
	if a == nil || a.db == nil || cause == nil {
		return
	}
	chatID, messageType, payload := buildOutboundDeadLetterSnapshot(message)
	item := db.OutboundDeadLetterInput{
		ChatID:       chatID,
		Priority:     int(normalizeOutboundPriority(priority)),
		Operation:    truncateOutboundDeadLetterText(operation, outboundDeadLetterOpMaxRunes),
		MessageType:  truncateOutboundDeadLetterText(messageType, outboundDeadLetterTypeMaxRunes),
		Payload:      truncateOutboundDeadLetterText(payload, outboundDeadLetterPayloadMaxRunes),
		ErrorMessage: truncateOutboundDeadLetterText(cause.Error(), outboundDeadLetterErrorMaxRunes),
	}
	deadLetterID, err := a.db.AddOutboundDeadLetter(item)
	if err != nil {
		log.Printf("写入死信队列失败: op=%s chat=%d err=%v", item.Operation, chatID, err)
		return
	}
	a.currentOutboundMetrics().ObserveOutboundDeadLetter(outboundOperationMetricLabel(operation), outboundPriorityMetricLabel(priority))
	log.Printf("发送失败消息已入死信队列: id=%d op=%s priority=%d chat=%d type=%s", deadLetterID, item.Operation, item.Priority, chatID, item.MessageType)
}

func buildOutboundDeadLetterSnapshot(message tgbotapi.Chattable) (int64, string, string) {
	if message == nil {
		return 0, "", ""
	}
	chatID := extractDeadLetterChatID(message)
	messageType := fmt.Sprintf("%T", message)
	payload, err := json.Marshal(message)
	if err != nil {
		return chatID, messageType, redactOutboundDeadLetterText(fmt.Sprintf("%#v", message))
	}
	return chatID, messageType, redactOutboundDeadLetterPayload(string(payload))
}

func redactOutboundDeadLetterPayload(payload string) string {
	var value any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return redactOutboundDeadLetterText(payload)
	}
	redacted := redactOutboundDeadLetterValue(value)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return redactOutboundDeadLetterText(payload)
	}
	return string(encoded)
}

func redactOutboundDeadLetterValue(value any) any {
	switch item := value.(type) {
	case string:
		return redactOutboundDeadLetterString(item)
	case []any:
		for index := range item {
			item[index] = redactOutboundDeadLetterValue(item[index])
		}
		return item
	case map[string]any:
		for key, nested := range item {
			item[key] = redactOutboundDeadLetterValue(nested)
		}
		return item
	default:
		return value
	}
}

func redactOutboundDeadLetterString(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && isAddressVerificationMediaURL(parsed) {
		return redactedAddressVerificationMediaURL(value)
	}
	return redactOutboundDeadLetterText(value)
}

func redactOutboundDeadLetterText(value string) string {
	return addressVerificationMediaURLPattern.ReplaceAllStringFunc(value, redactedAddressVerificationMediaURL)
}

func isAddressVerificationMediaURL(value *url.URL) bool {
	if value == nil {
		return false
	}
	return value.Scheme == "https" && value.Host == "autobookbl.site" && value.User == nil && value.RawQuery == "" && value.Fragment == "" && addressVerificationMediaURLPattern.MatchString(value.String())
}

func redactedAddressVerificationMediaURL(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "[redacted-address-verification-media:" + hex.EncodeToString(digest[:])[:12] + "]"
}

func extractDeadLetterChatID(message tgbotapi.Chattable) int64 {
	if message == nil {
		return 0
	}
	value := reflect.ValueOf(message)
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return 0
	}

	queue := []reflect.Value{value}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		currentType := current.Type()
		for idx := 0; idx < current.NumField(); idx++ {
			fieldType := currentType.Field(idx)
			if fieldType.PkgPath != "" {
				continue
			}
			fieldValue := current.Field(idx)
			if strings.EqualFold(fieldType.Name, "ChatID") {
				if chatID, ok := reflectValueToInt64(fieldValue); ok {
					return chatID
				}
			}
			switch fieldValue.Kind() {
			case reflect.Struct:
				queue = append(queue, fieldValue)
			case reflect.Pointer:
				if !fieldValue.IsNil() && fieldValue.Elem().Kind() == reflect.Struct {
					queue = append(queue, fieldValue.Elem())
				}
			}
		}
	}
	return 0
}

func reflectValueToInt64(value reflect.Value) (int64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int64(value.Uint()), true
	default:
		return 0, false
	}
}

func truncateOutboundDeadLetterText(raw string, limit int) string {
	trimmed := strings.TrimSpace(raw)
	if limit <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit]) + "...(truncated)"
}
