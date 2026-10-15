package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	idempotentConsumerStatusPending    = "pending"
	idempotentConsumerStatusProcessing = "processing"
	idempotentConsumerStatusConsumed   = "consumed"

	defaultIdempotentConsumerLease     = 30 * time.Second
	idempotentConsumerClaimRetry       = 4
	idempotentConsumerMaxLastErrorRune = 512
)

type IdempotentMessageInput struct {
	ChatID     int64
	Topic      string
	MessageKey string
	Payload    string
}

type IdempotentEnqueueResult struct {
	MessageID int64
	Duplicate bool
}

type IdempotentQueuedMessage struct {
	ID            int64
	ChatID        int64
	Topic         string
	MessageKey    string
	Payload       string
	DeliveryCount int
	Status        string
}

type IdempotentConsumeInput struct {
	ChatID   int64
	Topic    string
	Consumer string
	Lease    time.Duration
}

type IdempotentConsumeResult struct {
	Polled    bool
	Consumed  bool
	Duplicate bool
	Message   IdempotentQueuedMessage
}

func (d *Database) EnqueueIdempotentMessage(input IdempotentMessageInput) (IdempotentEnqueueResult, error) {
	if routed := d.databaseForChat(input.ChatID); routed != d {
		return routed.EnqueueIdempotentMessage(input)
	}
	if d == nil || d.raw == nil {
		return IdempotentEnqueueResult{}, errors.New("database is nil")
	}
	if err := d.ensureIdempotentConsumerSchema(); err != nil {
		return IdempotentEnqueueResult{}, err
	}

	topic := strings.TrimSpace(input.Topic)
	if topic == "" {
		return IdempotentEnqueueResult{}, errors.New("message topic is required")
	}
	messageKey := strings.TrimSpace(input.MessageKey)
	if messageKey == "" {
		return IdempotentEnqueueResult{}, errors.New("message key is required")
	}

	now := nowUTCText()
	res, err := d.raw.Exec(`
INSERT OR IGNORE INTO idempotent_consumer_messages (
	chat_id, topic, message_key, payload, status, delivery_count, lease_until_unix, last_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 0, 0, '', ?, ?)`,
		input.ChatID,
		topic,
		messageKey,
		strings.TrimSpace(input.Payload),
		idempotentConsumerStatusPending,
		now,
		now,
	)
	if err != nil {
		return IdempotentEnqueueResult{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return IdempotentEnqueueResult{}, err
	}
	if affected > 0 {
		messageID, _ := res.LastInsertId()
		return IdempotentEnqueueResult{
			MessageID: messageID,
			Duplicate: false,
		}, nil
	}

	var existingID int64
	if err := d.raw.QueryRow(
		`SELECT id FROM idempotent_consumer_messages WHERE chat_id = ? AND topic = ? AND message_key = ?`,
		input.ChatID,
		topic,
		messageKey,
	).Scan(&existingID); err != nil {
		return IdempotentEnqueueResult{}, err
	}
	return IdempotentEnqueueResult{
		MessageID: existingID,
		Duplicate: true,
	}, nil
}

func (d *Database) ConsumeIdempotentMessage(input IdempotentConsumeInput, handler func(IdempotentQueuedMessage) error) (IdempotentConsumeResult, error) {
	if routed := d.databaseForChat(input.ChatID); routed != d {
		return routed.ConsumeIdempotentMessage(input, handler)
	}
	if d == nil || d.raw == nil {
		return IdempotentConsumeResult{}, errors.New("database is nil")
	}
	if handler == nil {
		return IdempotentConsumeResult{}, errors.New("consumer handler is nil")
	}
	if err := d.ensureIdempotentConsumerSchema(); err != nil {
		return IdempotentConsumeResult{}, err
	}

	topic := strings.TrimSpace(input.Topic)
	if topic == "" {
		return IdempotentConsumeResult{}, errors.New("consumer topic is required")
	}
	consumer := strings.TrimSpace(input.Consumer)
	if consumer == "" {
		consumer = "default"
	}
	lease := input.Lease
	if lease <= 0 {
		lease = defaultIdempotentConsumerLease
	}

	message, found, err := d.claimIdempotentMessage(input.ChatID, topic, lease)
	if err != nil {
		return IdempotentConsumeResult{}, err
	}
	if !found {
		return IdempotentConsumeResult{}, nil
	}

	result := IdempotentConsumeResult{
		Polled:  true,
		Message: message,
	}
	alreadyConsumed, err := d.isIdempotentMessageConsumed(input.ChatID, topic, consumer, message.MessageKey)
	if err != nil {
		releaseErr := d.releaseIdempotentMessageForRetry(message.ID, err)
		if releaseErr != nil {
			return result, errors.Join(err, releaseErr)
		}
		return result, err
	}
	if alreadyConsumed {
		if err := d.markIdempotentMessageConsumed(message.ID); err != nil {
			return result, err
		}
		result.Consumed = true
		result.Duplicate = true
		result.Message.Status = idempotentConsumerStatusConsumed
		return result, nil
	}

	handleErr := handler(message)
	if handleErr != nil {
		releaseErr := d.releaseIdempotentMessageForRetry(message.ID, handleErr)
		if releaseErr != nil {
			return result, errors.Join(handleErr, releaseErr)
		}
		return result, handleErr
	}

	duplicate, ackErr := d.ackIdempotentMessage(input.ChatID, topic, consumer, message)
	if ackErr != nil {
		releaseErr := d.releaseIdempotentMessageForRetry(message.ID, ackErr)
		if releaseErr != nil {
			return result, errors.Join(ackErr, releaseErr)
		}
		return result, ackErr
	}

	result.Consumed = true
	result.Duplicate = duplicate
	result.Message.Status = idempotentConsumerStatusConsumed
	return result, nil
}

func (d *Database) ensureIdempotentConsumerSchema() error {
	if d == nil || d.raw == nil {
		return errors.New("database is nil")
	}
	_, err := d.raw.Exec(`
CREATE TABLE IF NOT EXISTS idempotent_consumer_messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	topic TEXT NOT NULL,
	message_key TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	delivery_count INTEGER NOT NULL DEFAULT 0,
	lease_until_unix INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chat_id, topic, message_key)
);

CREATE TABLE IF NOT EXISTS idempotent_consumer_checkpoints (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	topic TEXT NOT NULL,
	consumer_name TEXT NOT NULL,
	message_key TEXT NOT NULL,
	consumed_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chat_id, topic, consumer_name, message_key)
);

CREATE INDEX IF NOT EXISTS idx_idempotent_consumer_claim
ON idempotent_consumer_messages(chat_id, topic, status, lease_until_unix, id);
CREATE INDEX IF NOT EXISTS idx_idempotent_consumer_checkpoint
ON idempotent_consumer_checkpoints(chat_id, topic, consumer_name, consumed_at DESC);
`)
	return err
}

func (d *Database) claimIdempotentMessage(chatID int64, topic string, lease time.Duration) (IdempotentQueuedMessage, bool, error) {
	for attempt := 0; attempt < idempotentConsumerClaimRetry; attempt++ {
		nowUnix := time.Now().UTC().UnixNano()
		leaseUntil := time.Now().UTC().Add(lease).UnixNano()

		tx, err := d.raw.Begin()
		if err != nil {
			return IdempotentQueuedMessage{}, false, err
		}

		message, found, err := claimOneIdempotentMessageTx(tx, chatID, topic, nowUnix)
		if err != nil {
			_ = tx.Rollback()
			return IdempotentQueuedMessage{}, false, err
		}
		if !found {
			_ = tx.Rollback()
			return IdempotentQueuedMessage{}, false, nil
		}

		updated, err := updateIdempotentMessageClaimTx(tx, message.ID, nowUnix, leaseUntil)
		if err != nil {
			_ = tx.Rollback()
			return IdempotentQueuedMessage{}, false, err
		}
		if !updated {
			_ = tx.Rollback()
			continue
		}
		if err := tx.Commit(); err != nil {
			return IdempotentQueuedMessage{}, false, err
		}

		message.DeliveryCount++
		message.Status = idempotentConsumerStatusProcessing
		return message, true, nil
	}
	return IdempotentQueuedMessage{}, false, nil
}

func claimOneIdempotentMessageTx(tx *sql.Tx, chatID int64, topic string, nowUnix int64) (IdempotentQueuedMessage, bool, error) {
	row := tx.QueryRow(`
SELECT id, chat_id, topic, message_key, payload, delivery_count
FROM idempotent_consumer_messages
WHERE chat_id = ?
	AND topic = ?
	AND (
		status = ?
		OR (status = ? AND lease_until_unix <= ?)
	)
ORDER BY id ASC
LIMIT 1`,
		chatID,
		topic,
		idempotentConsumerStatusPending,
		idempotentConsumerStatusProcessing,
		nowUnix,
	)
	message := IdempotentQueuedMessage{}
	err := row.Scan(
		&message.ID,
		&message.ChatID,
		&message.Topic,
		&message.MessageKey,
		&message.Payload,
		&message.DeliveryCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return IdempotentQueuedMessage{}, false, nil
	}
	if err != nil {
		return IdempotentQueuedMessage{}, false, err
	}
	return message, true, nil
}

func updateIdempotentMessageClaimTx(tx *sql.Tx, messageID int64, nowUnix, leaseUntil int64) (bool, error) {
	res, err := tx.Exec(`
UPDATE idempotent_consumer_messages
SET status = ?,
	delivery_count = delivery_count + 1,
	lease_until_unix = ?,
	last_error = '',
	updated_at = ?
WHERE id = ?
	AND (
		status = ?
		OR (status = ? AND lease_until_unix <= ?)
	)`,
		idempotentConsumerStatusProcessing,
		leaseUntil,
		nowUTCText(),
		messageID,
		idempotentConsumerStatusPending,
		idempotentConsumerStatusProcessing,
		nowUnix,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (d *Database) isIdempotentMessageConsumed(chatID int64, topic, consumer, messageKey string) (bool, error) {
	var count int
	err := d.raw.QueryRow(
		`SELECT COUNT(1) FROM idempotent_consumer_checkpoints WHERE chat_id = ? AND topic = ? AND consumer_name = ? AND message_key = ?`,
		chatID,
		topic,
		consumer,
		messageKey,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (d *Database) releaseIdempotentMessageForRetry(messageID int64, cause error) error {
	lastError := ""
	if cause != nil {
		lastError = truncateIdempotentMessageError(cause.Error(), idempotentConsumerMaxLastErrorRune)
	}
	_, err := d.raw.Exec(`
UPDATE idempotent_consumer_messages
SET status = ?,
	lease_until_unix = 0,
	last_error = ?,
	updated_at = ?
WHERE id = ?`,
		idempotentConsumerStatusPending,
		lastError,
		nowUTCText(),
		messageID,
	)
	return err
}

func (d *Database) markIdempotentMessageConsumed(messageID int64) error {
	_, err := d.raw.Exec(`
UPDATE idempotent_consumer_messages
SET status = ?,
	lease_until_unix = 0,
	last_error = '',
	updated_at = ?
WHERE id = ?`,
		idempotentConsumerStatusConsumed,
		nowUTCText(),
		messageID,
	)
	return err
}

func (d *Database) ackIdempotentMessage(chatID int64, topic, consumer string, message IdempotentQueuedMessage) (bool, error) {
	tx, err := d.raw.Begin()
	if err != nil {
		return false, err
	}
	checkpoint, err := tx.Exec(`
INSERT OR IGNORE INTO idempotent_consumer_checkpoints (chat_id, topic, consumer_name, message_key, consumed_at)
VALUES (?, ?, ?, ?, ?)`,
		chatID,
		topic,
		consumer,
		message.MessageKey,
		nowUTCText(),
	)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	checkpointRows, err := checkpoint.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}

	if _, err := tx.Exec(`
UPDATE idempotent_consumer_messages
SET status = ?,
	lease_until_unix = 0,
	last_error = '',
	updated_at = ?
WHERE id = ?`,
		idempotentConsumerStatusConsumed,
		nowUTCText(),
		message.ID,
	); err != nil {
		_ = tx.Rollback()
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return checkpointRows == 0, nil
}

func truncateIdempotentMessageError(raw string, limit int) string {
	trimmed := strings.TrimSpace(raw)
	if limit <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return fmt.Sprintf("%s...", string(runes[:limit-3]))
}
