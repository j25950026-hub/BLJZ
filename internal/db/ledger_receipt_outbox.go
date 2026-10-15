package db

import (
	"database/sql"
	"strings"
	"time"
)

const (
	ledgerReceiptOutboxStatusPending    = "pending"
	ledgerReceiptOutboxStatusProcessing = "processing"
	ledgerReceiptOutboxStatusSent       = "sent"
	ledgerReceiptOutboxStatusFailed     = "failed"
)

func insertLedgerReceiptOutboxTx(tx *sql.Tx, item LedgerReceiptOutboxInput) (int64, error) {
	commandKey := strings.TrimSpace(item.CommandKey)
	if commandKey == "" {
		commandKey = "ledger"
	}
	now := nowUTCText()
	res, err := tx.Exec(`
INSERT INTO ledger_receipt_outbox (
	chat_id, source_message_id, entry_id, user_id, command_key, payload,
	status, next_attempt_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(chat_id, source_message_id, command_key)
DO UPDATE SET
	entry_id = excluded.entry_id,
	user_id = excluded.user_id,
	payload = excluded.payload,
	status = excluded.status,
	next_attempt_at = excluded.next_attempt_at,
	last_error = '',
	processing_started_at = '',
	sent_at = '',
	updated_at = excluded.updated_at`,
		item.ChatID,
		item.SourceMessageID,
		item.EntryID,
		item.UserID,
		commandKey,
		strings.TrimSpace(item.Payload),
		ledgerReceiptOutboxStatusPending,
		now,
		now,
		now,
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id != 0 {
		return id, nil
	}
	row := tx.QueryRow(`
SELECT id FROM ledger_receipt_outbox
WHERE chat_id = ? AND source_message_id = ? AND command_key = ?`,
		item.ChatID,
		item.SourceMessageID,
		commandKey,
	)
	if err := row.Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (d *Database) ClaimLedgerReceiptOutbox(now time.Time, visibilityTimeout time.Duration) (*LedgerReceiptOutbox, error) {
	if d == nil {
		return nil, nil
	}
	if d.tenantRouter != nil {
		shards := d.tenantRouter.shards
		if len(shards) == 0 {
			return nil, nil
		}
		start := int((d.tenantRouter.nextOutboxClaimShard.Add(1) - 1) % uint64(len(shards)))
		for offset := 0; offset < len(shards); offset++ {
			shard := shards[(start+offset)%len(shards)]
			if shard == nil {
				continue
			}
			item, err := shard.claimLedgerReceiptOutboxLocal(now, visibilityTimeout)
			if err != nil {
				return nil, err
			}
			if item != nil {
				return item, nil
			}
		}
		return nil, nil
	}
	return d.claimLedgerReceiptOutboxLocal(now, visibilityTimeout)
}

func (d *Database) claimLedgerReceiptOutboxLocal(now time.Time, visibilityTimeout time.Duration) (*LedgerReceiptOutbox, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if visibilityTimeout <= 0 {
		visibilityTimeout = time.Minute
	}
	nowText := formatOutboxTime(now)
	staleBefore := formatOutboxTime(now.Add(-visibilityTimeout))
	var item *LedgerReceiptOutbox
	err := d.withWriteTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
UPDATE ledger_receipt_outbox
SET status = ?, processing_started_at = '', updated_at = ?
WHERE status = ?
  AND processing_started_at <> ''
  AND datetime(processing_started_at) <= datetime(?)`,
			ledgerReceiptOutboxStatusPending,
			nowText,
			ledgerReceiptOutboxStatusProcessing,
			staleBefore,
		); err != nil {
			return err
		}

		var id int64
		err := tx.QueryRow(`
SELECT candidate.id
FROM ledger_receipt_outbox AS candidate
WHERE candidate.status = ?
  AND datetime(candidate.next_attempt_at) <= datetime(?)
  AND NOT EXISTS (
	SELECT 1
	FROM ledger_receipt_outbox AS busy
	WHERE busy.chat_id = candidate.chat_id
	  AND busy.status = ?
  )
ORDER BY candidate.id ASC
LIMIT 1`,
			ledgerReceiptOutboxStatusPending,
			nowText,
			ledgerReceiptOutboxStatusProcessing,
		).Scan(&id)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}

		res, err := tx.Exec(`
UPDATE ledger_receipt_outbox
SET status = ?,
	attempt_count = attempt_count + 1,
	processing_started_at = ?,
	updated_at = ?
WHERE id = ? AND status = ?`,
			ledgerReceiptOutboxStatusProcessing,
			nowText,
			nowText,
			id,
			ledgerReceiptOutboxStatusPending,
		)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
		row, err := getLedgerReceiptOutboxByIDTx(tx, id)
		if err != nil {
			return err
		}
		item = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (d *Database) MarkLedgerReceiptOutboxSent(chatID, id int64, sentAt time.Time) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.MarkLedgerReceiptOutboxSent(chatID, id, sentAt)
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	sentAtText := formatOutboxTime(sentAt)
	return d.withWriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
UPDATE ledger_receipt_outbox
SET status = ?,
	sent_at = ?,
	processing_started_at = '',
	updated_at = ?
WHERE id = ? AND chat_id = ?`,
			ledgerReceiptOutboxStatusSent,
			sentAtText,
			sentAtText,
			id,
			chatID,
		)
		return err
	})
}

func (d *Database) MarkLedgerReceiptOutboxSentIfProcessing(chatID, id int64, processingStarted string, sentAt time.Time) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.MarkLedgerReceiptOutboxSentIfProcessing(chatID, id, processingStarted, sentAt)
	}
	ownerToken := strings.TrimSpace(processingStarted)
	if ownerToken == "" {
		return false, nil
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	sentAtText := formatOutboxTime(sentAt)
	var affected int64
	err := d.withWriteTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
UPDATE ledger_receipt_outbox
SET status = ?,
	sent_at = ?,
	processing_started_at = '',
	updated_at = ?
WHERE id = ?
  AND chat_id = ?
  AND status = ?
  AND processing_started_at = ?`,
			ledgerReceiptOutboxStatusSent,
			sentAtText,
			sentAtText,
			id,
			chatID,
			ledgerReceiptOutboxStatusProcessing,
			ownerToken,
		)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (d *Database) MarkLedgerReceiptOutboxRetry(chatID, id int64, errorMessage string, nextAttemptAt time.Time, final bool) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.MarkLedgerReceiptOutboxRetry(chatID, id, errorMessage, nextAttemptAt, final)
	}
	if nextAttemptAt.IsZero() {
		nextAttemptAt = time.Now().UTC().Add(time.Minute)
	}
	status := ledgerReceiptOutboxStatusPending
	if final {
		status = ledgerReceiptOutboxStatusFailed
	}
	nowText := nowUTCText()
	nextAttemptText := formatOutboxTime(nextAttemptAt)
	return d.withWriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
UPDATE ledger_receipt_outbox
SET status = ?,
	last_error = ?,
	next_attempt_at = ?,
	processing_started_at = '',
	updated_at = ?
WHERE id = ? AND chat_id = ?`,
			status,
			limitDeadLetterText(errorMessage, 512),
			nextAttemptText,
			nowText,
			id,
			chatID,
		)
		return err
	})
}

func (d *Database) MarkLedgerReceiptOutboxRetryIfProcessing(chatID, id int64, processingStarted, errorMessage string, nextAttemptAt time.Time, final bool) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.MarkLedgerReceiptOutboxRetryIfProcessing(chatID, id, processingStarted, errorMessage, nextAttemptAt, final)
	}
	ownerToken := strings.TrimSpace(processingStarted)
	if ownerToken == "" {
		return false, nil
	}
	if nextAttemptAt.IsZero() {
		nextAttemptAt = time.Now().UTC().Add(time.Minute)
	}
	status := ledgerReceiptOutboxStatusPending
	if final {
		status = ledgerReceiptOutboxStatusFailed
	}
	nowText := nowUTCText()
	nextAttemptText := formatOutboxTime(nextAttemptAt)
	var affected int64
	err := d.withWriteTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
UPDATE ledger_receipt_outbox
SET status = ?,
	last_error = ?,
	next_attempt_at = ?,
	processing_started_at = '',
	updated_at = ?
WHERE id = ?
  AND chat_id = ?
  AND status = ?
  AND processing_started_at = ?`,
			status,
			limitDeadLetterText(errorMessage, 512),
			nextAttemptText,
			nowText,
			id,
			chatID,
			ledgerReceiptOutboxStatusProcessing,
			ownerToken,
		)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (d *Database) ReleaseLedgerReceiptOutboxClaim(chatID, id int64, processingStarted string) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ReleaseLedgerReceiptOutboxClaim(chatID, id, processingStarted)
	}
	ownerToken := strings.TrimSpace(processingStarted)
	if ownerToken == "" {
		return false, nil
	}
	nowText := nowUTCText()
	var affected int64
	err := d.withWriteTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
UPDATE ledger_receipt_outbox
SET status = ?,
	attempt_count = CASE WHEN attempt_count > 0 THEN attempt_count - 1 ELSE 0 END,
	processing_started_at = '',
	updated_at = ?
WHERE id = ?
  AND chat_id = ?
  AND status = ?
  AND processing_started_at = ?`,
			ledgerReceiptOutboxStatusPending,
			nowText,
			id,
			chatID,
			ledgerReceiptOutboxStatusProcessing,
			ownerToken,
		)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func getLedgerReceiptOutboxByIDTx(tx *sql.Tx, id int64) (*LedgerReceiptOutbox, error) {
	row := tx.QueryRow(`
SELECT id, chat_id, source_message_id, entry_id, user_id, command_key, payload,
	status, attempt_count, last_error, next_attempt_at, processing_started_at,
	sent_at, created_at, updated_at
FROM ledger_receipt_outbox
WHERE id = ?`,
		id,
	)
	var item LedgerReceiptOutbox
	if err := row.Scan(
		&item.ID,
		&item.ChatID,
		&item.SourceMessageID,
		&item.EntryID,
		&item.UserID,
		&item.CommandKey,
		&item.Payload,
		&item.Status,
		&item.AttemptCount,
		&item.LastError,
		&item.NextAttemptAt,
		&item.ProcessingStarted,
		&item.SentAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &item, nil
}

func formatOutboxTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return t.UTC().Format("2006-01-02T15:04:05")
}
