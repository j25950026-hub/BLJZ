package bot

import (
	"sync"
	"time"
)

const sensitiveOperationConfirmTTL = 60 * time.Second

var sensitiveOperationConfirmCommands = map[string]string{
	"清理今日数据": "确认清理今日数据",
	"清理入款数据": "确认清理入款数据",
	"清理下发数据": "确认清理下发数据",
}

var sensitiveOperationByConfirmCommand = reverseSensitiveOperationConfirmCommands(sensitiveOperationConfirmCommands)

type sensitiveOperationConfirmationStatus int

const (
	sensitiveOperationConfirmationAccepted sensitiveOperationConfirmationStatus = iota
	sensitiveOperationConfirmationMissing
	sensitiveOperationConfirmationExpired
	sensitiveOperationConfirmationMismatch
)

type sensitiveOperationConfirmationKey struct {
	chatID int64
	userID int64
}

type sensitiveOperationPending struct {
	command  string
	expireAt time.Time
}

type sensitiveOperationConfirmer struct {
	mu      sync.Mutex
	ttl     time.Duration
	pending map[sensitiveOperationConfirmationKey]sensitiveOperationPending
}

func newSensitiveOperationConfirmer(ttl time.Duration) *sensitiveOperationConfirmer {
	if ttl <= 0 {
		ttl = sensitiveOperationConfirmTTL
	}
	return &sensitiveOperationConfirmer{
		ttl:     ttl,
		pending: make(map[sensitiveOperationConfirmationKey]sensitiveOperationPending),
	}
}

func sensitiveOperationConfirmCommand(command string) (string, bool) {
	confirmCommand, ok := sensitiveOperationConfirmCommands[command]
	return confirmCommand, ok
}

func sensitiveOperationByConfirm(confirmCommand string) (string, bool) {
	command, ok := sensitiveOperationByConfirmCommand[confirmCommand]
	return command, ok
}

func (c *sensitiveOperationConfirmer) Request(chatID, userID int64, command string) time.Time {
	return c.RequestAt(chatID, userID, command, time.Now())
}

func (c *sensitiveOperationConfirmer) RequestAt(chatID, userID int64, command string, now time.Time) time.Time {
	if c == nil {
		return time.Time{}
	}
	if now.IsZero() {
		now = time.Now()
	}
	expireAt := now.Add(c.ttl)
	key := sensitiveOperationConfirmationKey{chatID: chatID, userID: userID}
	c.mu.Lock()
	c.pending[key] = sensitiveOperationPending{
		command:  command,
		expireAt: expireAt,
	}
	c.mu.Unlock()
	return expireAt
}

func (c *sensitiveOperationConfirmer) Confirm(chatID, userID int64, command string) (sensitiveOperationConfirmationStatus, string) {
	return c.ConfirmAt(chatID, userID, command, time.Now())
}

func (c *sensitiveOperationConfirmer) ConfirmAt(chatID, userID int64, command string, now time.Time) (sensitiveOperationConfirmationStatus, string) {
	if c == nil {
		return sensitiveOperationConfirmationMissing, ""
	}
	if now.IsZero() {
		now = time.Now()
	}
	key := sensitiveOperationConfirmationKey{chatID: chatID, userID: userID}

	c.mu.Lock()
	defer c.mu.Unlock()

	pending, ok := c.pending[key]
	if !ok {
		return sensitiveOperationConfirmationMissing, ""
	}
	if !pending.expireAt.After(now) {
		delete(c.pending, key)
		return sensitiveOperationConfirmationExpired, pending.command
	}
	if pending.command != command {
		return sensitiveOperationConfirmationMismatch, pending.command
	}
	delete(c.pending, key)
	return sensitiveOperationConfirmationAccepted, pending.command
}

func reverseSensitiveOperationConfirmCommands(source map[string]string) map[string]string {
	out := make(map[string]string, len(source))
	for command, confirmCommand := range source {
		out[confirmCommand] = command
	}
	return out
}
