package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const sqliteDateTimeLayout = "2006-01-02 15:04:05"

type coldArchiveLedgerRow struct {
	id               int64
	chatID           int64
	messageID        int
	userID           int64
	username         string
	nickname         string
	entryType        string
	amount           float64
	currency         string
	baseAmount       float64
	feeRate          float64
	rate             float64
	finalAmount      float64
	note             string
	cardNo           string
	trackMessageLink string
	createdAt        string
	reversed         int
	reversalOf       sql.NullInt64
}

func (d *Database) initColdArchive(masterPath string, opts Options) error {
	coldPath := strings.TrimSpace(opts.ColdArchivePath)
	if coldPath == "" {
		return nil
	}
	if sameDBPath(masterPath, coldPath) {
		return fmt.Errorf("冷热分离配置无效：ColdArchivePath 不能与主库路径相同")
	}

	coldRaw, err := openSQLite(coldPath)
	if err != nil {
		return err
	}
	if err := configureWaitTimeoutForDB(coldRaw, d.waitTimeout); err != nil {
		_ = coldRaw.Close()
		return err
	}
	if err := ensureColdArchiveSchema(coldRaw); err != nil {
		_ = coldRaw.Close()
		return err
	}

	d.coldArchiveRaw = coldRaw
	d.hotDataRetention = opts.HotDataRetention
	d.coldArchiveBatchSize = opts.ColdArchiveBatchSize

	if _, err := d.archiveHistoricalLedgerEntries(time.Now().UTC()); err != nil {
		_ = d.closeColdArchive()
		return err
	}
	if opts.ColdArchiveSweep > 0 {
		d.coldArchiveSweepStop = make(chan struct{})
		d.coldArchiveSweepDone = make(chan struct{})
		go d.runColdArchiveWorker(opts.ColdArchiveSweep)
	}
	return nil
}

func ensureColdArchiveSchema(raw *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS ledger_entries_archive (
	id INTEGER PRIMARY KEY,
	chat_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	username TEXT DEFAULT '',
	nickname TEXT DEFAULT '',
	entry_type TEXT NOT NULL,
	amount REAL NOT NULL,
	currency TEXT NOT NULL,
	base_amount REAL NOT NULL,
	fee_rate REAL NOT NULL,
	rate REAL NOT NULL,
	final_amount REAL NOT NULL,
	note TEXT DEFAULT '',
	card_no TEXT DEFAULT '',
	track_message_link TEXT DEFAULT '',
	created_at TEXT NOT NULL,
	reversed INTEGER DEFAULT 0,
	reversal_of INTEGER,
	archived_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_archive_msg_id ON ledger_entries_archive(chat_id, message_id);
CREATE INDEX IF NOT EXISTS idx_archive_chat_created ON ledger_entries_archive(chat_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_archive_archived_at ON ledger_entries_archive(archived_at DESC);
`
	_, err := raw.Exec(schema)
	return err
}

func (d *Database) closeColdArchive() error {
	if d == nil || d.coldArchiveRaw == nil {
		return nil
	}
	closeErr := d.coldArchiveRaw.Close()
	d.coldArchiveRaw = nil
	d.hotDataRetention = 0
	d.coldArchiveBatchSize = 0
	return closeErr
}

func (d *Database) runColdArchiveWorker(interval time.Duration) {
	defer close(d.coldArchiveSweepDone)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_, _ = d.archiveHistoricalLedgerEntries(time.Now().UTC())
		case <-d.coldArchiveSweepStop:
			_, _ = d.archiveHistoricalLedgerEntries(time.Now().UTC())
			return
		}
	}
}

func (d *Database) archiveHistoricalLedgerEntries(now time.Time) (int, error) {
	if d == nil || d.coldArchiveRaw == nil || d.hotDataRetention <= 0 || d.coldArchiveBatchSize <= 0 {
		return 0, nil
	}
	cutoff := now.UTC().Add(-d.hotDataRetention).Format(sqliteDateTimeLayout)
	total := 0
	for {
		moved, err := d.archiveHistoricalLedgerBatch(cutoff, d.coldArchiveBatchSize)
		if err != nil {
			return total, err
		}
		total += moved
		if moved < d.coldArchiveBatchSize {
			break
		}
	}
	return total, nil
}

func (d *Database) archiveHistoricalLedgerBatch(cutoff string, batchSize int) (int, error) {
	tx, err := d.raw.Begin()
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(`
SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
FROM ledger_entries
WHERE datetime(created_at) < datetime(?)
ORDER BY id ASC
LIMIT ?`, cutoff, batchSize)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}

	batch := make([]coldArchiveLedgerRow, 0, batchSize)
	for rows.Next() {
		var row coldArchiveLedgerRow
		if err := rows.Scan(
			&row.id,
			&row.chatID,
			&row.messageID,
			&row.userID,
			&row.username,
			&row.nickname,
			&row.entryType,
			&row.amount,
			&row.currency,
			&row.baseAmount,
			&row.feeRate,
			&row.rate,
			&row.finalAmount,
			&row.note,
			&row.cardNo,
			&row.trackMessageLink,
			&row.createdAt,
			&row.reversed,
			&row.reversalOf,
		); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return 0, err
		}
		batch = append(batch, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = tx.Rollback()
		return 0, err
	}
	_ = rows.Close()
	if len(batch) == 0 {
		_ = tx.Rollback()
		return 0, nil
	}

	archiveTx, err := d.coldArchiveRaw.Begin()
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	for _, row := range batch {
		if _, err := archiveTx.Exec(`
INSERT OR IGNORE INTO ledger_entries_archive (
	id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.id,
			row.chatID,
			row.messageID,
			row.userID,
			row.username,
			row.nickname,
			row.entryType,
			row.amount,
			row.currency,
			row.baseAmount,
			row.feeRate,
			row.rate,
			row.finalAmount,
			row.note,
			row.cardNo,
			row.trackMessageLink,
			row.createdAt,
			row.reversed,
			row.reversalOf,
		); err != nil {
			_ = archiveTx.Rollback()
			_ = tx.Rollback()
			return 0, err
		}
	}
	if err := archiveTx.Commit(); err != nil {
		_ = tx.Rollback()
		return 0, err
	}

	for _, row := range batch {
		if _, err := tx.Exec(`DELETE FROM ledger_entries WHERE id = ?`, row.id); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(batch), nil
}
