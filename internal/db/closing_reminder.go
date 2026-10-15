package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ClosingReminderStatusNormal  = "normal"
	ClosingReminderStatusPending = "pending"
	ClosingReminderStatusNoData  = "no_data"
	ClosingReminderStatusReview  = "review"
	ClosingReminderStatusFailed  = "failed"

	ClosingReminderDMModeOff                  = "off"
	ClosingReminderDMModeOwner                = "owner"
	ClosingReminderDMModeOperators            = "operators"
	ClosingReminderDMModeOwnerOperators       = "owner_operators"
	ClosingReminderDMModeCustom               = "custom"
	ClosingReminderDMModeOwnerOperatorsCustom = "owner_operators_custom"

	defaultClosingReminderTime       = "04:00"
	defaultClosingReminderThresholdU = 1.5
	defaultClosingReminderDMMode     = ClosingReminderDMModeOwnerOperatorsCustom
)

func (d *Database) GetClosingReminderConfig(chatID int64) (GroupClosingReminderConfig, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetClosingReminderConfig(chatID)
	}
	if chatID == 0 {
		return GroupClosingReminderConfig{}, errors.New("chat_id is required")
	}
	row := d.raw.QueryRow(`
SELECT chat_id, enabled, reminder_time, threshold_u, dm_mode,
	text_normal, text_pending, text_no_data, text_review, text_failed,
	updated_by, updated_at
FROM group_closing_reminder_config
WHERE chat_id = ?`, chatID)

	var config GroupClosingReminderConfig
	err := row.Scan(
		&config.ChatID,
		&config.Enabled,
		&config.ReminderTime,
		&config.ThresholdU,
		&config.DMMode,
		&config.TextNormal,
		&config.TextPending,
		&config.TextNoData,
		&config.TextReview,
		&config.TextFailed,
		&config.UpdatedBy,
		&config.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return defaultClosingReminderConfig(chatID), nil
		}
		return GroupClosingReminderConfig{}, err
	}
	return normalizeClosingReminderConfig(config), nil
}

func (d *Database) UpsertClosingReminderConfig(config GroupClosingReminderConfig) error {
	if routed := d.databaseForChat(config.ChatID); routed != d {
		return routed.UpsertClosingReminderConfig(config)
	}
	config = normalizeClosingReminderConfig(config)
	if config.ChatID == 0 {
		return errors.New("chat_id is required")
	}
	_, err := d.raw.Exec(`
INSERT INTO group_closing_reminder_config (
	chat_id, enabled, reminder_time, threshold_u, dm_mode,
	text_normal, text_pending, text_no_data, text_review, text_failed,
	updated_by, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(chat_id) DO UPDATE SET
	enabled = excluded.enabled,
	reminder_time = excluded.reminder_time,
	threshold_u = excluded.threshold_u,
	dm_mode = excluded.dm_mode,
	text_normal = excluded.text_normal,
	text_pending = excluded.text_pending,
	text_no_data = excluded.text_no_data,
	text_review = excluded.text_review,
	text_failed = excluded.text_failed,
	updated_by = excluded.updated_by,
	updated_at = CURRENT_TIMESTAMP`,
		config.ChatID,
		config.Enabled,
		config.ReminderTime,
		config.ThresholdU,
		config.DMMode,
		config.TextNormal,
		config.TextPending,
		config.TextNoData,
		config.TextReview,
		config.TextFailed,
		config.UpdatedBy,
	)
	return err
}

func (d *Database) ListEnabledClosingReminderConfigs() ([]GroupClosingReminderConfig, error) {
	sqlText := `
SELECT chat_id, enabled, reminder_time, threshold_u, dm_mode,
	text_normal, text_pending, text_no_data, text_review, text_failed,
	updated_by, updated_at
FROM group_closing_reminder_config
WHERE enabled = 1
ORDER BY reminder_time ASC, chat_id ASC`
	if d.tenantRouter != nil {
		out := make([]GroupClosingReminderConfig, 0, 16)
		for _, shard := range d.tenantRouter.shards {
			rows, err := shard.listClosingReminderConfigsBySQL(sqlText)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].ReminderTime == out[j].ReminderTime {
				return out[i].ChatID < out[j].ChatID
			}
			return out[i].ReminderTime < out[j].ReminderTime
		})
		return out, nil
	}
	return d.listClosingReminderConfigsBySQL(sqlText)
}

func (d *Database) listClosingReminderConfigsBySQL(sqlText string, args ...any) ([]GroupClosingReminderConfig, error) {
	rows, err := d.raw.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []GroupClosingReminderConfig{}
	for rows.Next() {
		var config GroupClosingReminderConfig
		if err := rows.Scan(
			&config.ChatID,
			&config.Enabled,
			&config.ReminderTime,
			&config.ThresholdU,
			&config.DMMode,
			&config.TextNormal,
			&config.TextPending,
			&config.TextNoData,
			&config.TextReview,
			&config.TextFailed,
			&config.UpdatedBy,
			&config.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, normalizeClosingReminderConfig(config))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Database) UpsertClosingDMRecipient(chatID int64, userID int64, username, nickname string, addedBy int64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.UpsertClosingDMRecipient(chatID, userID, username, nickname, addedBy)
	}
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	if userID == 0 {
		return errors.New("user_id is required")
	}
	_, err := d.raw.Exec(`
INSERT INTO group_closing_dm_recipients (chat_id, user_id, username, nickname, added_by, added_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(chat_id, user_id) DO UPDATE SET
	username = excluded.username,
	nickname = excluded.nickname,
	added_by = excluded.added_by,
	added_at = CURRENT_TIMESTAMP`,
		chatID,
		userID,
		strings.TrimSpace(username),
		strings.TrimSpace(nickname),
		addedBy,
	)
	return err
}

func (d *Database) ListClosingDMRecipients(chatID int64) ([]GroupClosingDMRecipient, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListClosingDMRecipients(chatID)
	}
	rows, err := d.raw.Query(`
SELECT chat_id, user_id, username, nickname, added_by, added_at
FROM group_closing_dm_recipients
WHERE chat_id = ?
ORDER BY added_at ASC, user_id ASC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []GroupClosingDMRecipient{}
	for rows.Next() {
		var item GroupClosingDMRecipient
		if err := rows.Scan(&item.ChatID, &item.UserID, &item.Username, &item.Nickname, &item.AddedBy, &item.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Database) DeleteClosingDMRecipient(chatID int64, userID int64) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.DeleteClosingDMRecipient(chatID, userID)
	}
	res, err := d.raw.Exec(`DELETE FROM group_closing_dm_recipients WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (d *Database) ClearClosingDMRecipients(chatID int64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ClearClosingDMRecipients(chatID)
	}
	_, err := d.raw.Exec(`DELETE FROM group_closing_dm_recipients WHERE chat_id = ?`, chatID)
	return err
}

func (d *Database) BuildClosingReminderSummary(chatID int64, businessDate string, periodStart, periodEnd time.Time, thresholdU float64) (ClosingReminderSummary, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.BuildClosingReminderSummary(chatID, businessDate, periodStart, periodEnd, thresholdU)
	}
	if chatID == 0 {
		return ClosingReminderSummary{}, errors.New("chat_id is required")
	}
	normalizedDate, startUTC, endUTC, err := normalizeDailySettlementWindow(businessDate, periodStart, periodEnd)
	if err != nil {
		return ClosingReminderSummary{}, err
	}
	if thresholdU <= 0 {
		thresholdU = defaultClosingReminderThresholdU
	}

	startText := startUTC.Format(dailySettlementQueryTimeLayout)
	endText := endUTC.Format(dailySettlementQueryTimeLayout)
	rows, err := d.raw.Query(`
SELECT entry_type, amount, currency, fee_rate, rate, final_amount
FROM ledger_entries
WHERE chat_id = ? AND reversed = 0
  AND datetime(created_at) >= datetime(?)
  AND datetime(created_at) < datetime(?)
ORDER BY id ASC`, chatID, startText, endText)
	if err != nil {
		return ClosingReminderSummary{}, err
	}
	defer rows.Close()

	summary := ClosingReminderSummary{
		ChatID:       chatID,
		BusinessDate: normalizedDate,
		PeriodStart:  startUTC.Format(dailySettlementStoreTimeLayout),
		PeriodEnd:    endUTC.Format(dailySettlementStoreTimeLayout),
		GeneratedAt:  nowUTCText(),
	}
	for rows.Next() {
		var (
			entryType   string
			amount      float64
			currency    string
			feeRate     float64
			rate        float64
			finalAmount float64
		)
		if err := rows.Scan(&entryType, &amount, &currency, &feeRate, &rate, &finalAmount); err != nil {
			return ClosingReminderSummary{}, err
		}
		switch strings.ToLower(strings.TrimSpace(entryType)) {
		case "deposit":
			summary.DepositCount++
			summary.DepositTotal += closingReminderLedgerEntryU(amount, currency, feeRate, rate, finalAmount, true)
		case "payout":
			summary.PayoutCount++
			summary.PayoutTotal += closingReminderLedgerEntryU(amount, currency, feeRate, rate, finalAmount, false)
		}
	}
	if err := rows.Err(); err != nil {
		return ClosingReminderSummary{}, err
	}

	summary.PendingTotal = summary.DepositTotal - summary.PayoutTotal
	summary.Status = determineClosingReminderStatus(summary, thresholdU)
	return summary, nil
}

func closingReminderLedgerEntryU(amount float64, currency string, feeRate float64, rate float64, finalAmount float64, deposit bool) float64 {
	settlementAmount := finalAmount
	if almostZeroDB(settlementAmount) {
		settlementAmount = amount
		if deposit && !almostZeroDB(feeRate) {
			settlementAmount = amount * (1 - feeRate/100.0)
		}
	}
	return roundClosingReminderDisplayU(convertClosingReminderAmountToU(settlementAmount, currency, rate))
}

func convertClosingReminderAmountToU(amount float64, currency string, rate float64) float64 {
	if strings.EqualFold(strings.TrimSpace(currency), "USDT") {
		return amount
	}
	if almostZeroDB(rate) {
		rate = 1
	}
	return amount / rate
}

func roundClosingReminderDisplayU(value float64) float64 {
	rounded, err := strconv.ParseFloat(fmt.Sprintf("%.2f", value), 64)
	if err != nil {
		return value
	}
	return rounded
}

func almostZeroDB(value float64) bool {
	return value < 1e-9 && value > -1e-9
}

func (d *Database) MarkClosingReminderRun(chatID int64, businessDate string, status string, manual bool) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.MarkClosingReminderRun(chatID, businessDate, status, manual)
	}
	if chatID == 0 {
		return false, errors.New("chat_id is required")
	}
	trimmedDate := strings.TrimSpace(businessDate)
	if _, err := time.Parse(dailySettlementReportDateLayout, trimmedDate); err != nil {
		return false, fmt.Errorf("invalid business_date %q: %w", businessDate, err)
	}
	if manual {
		return true, nil
	}
	res, err := d.raw.Exec(`
INSERT OR IGNORE INTO group_closing_reminder_runs (chat_id, business_date, status, manual, sent_at)
VALUES (?, ?, ?, 0, CURRENT_TIMESTAMP)`, chatID, trimmedDate, normalizeClosingReminderStatus(status))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (d *Database) DeleteClosingReminderRun(chatID int64, businessDate string) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.DeleteClosingReminderRun(chatID, businessDate)
	}
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	trimmedDate := strings.TrimSpace(businessDate)
	if _, err := time.Parse(dailySettlementReportDateLayout, trimmedDate); err != nil {
		return fmt.Errorf("invalid business_date %q: %w", businessDate, err)
	}
	_, err := d.raw.Exec(`DELETE FROM group_closing_reminder_runs WHERE chat_id = ? AND business_date = ?`, chatID, trimmedDate)
	return err
}

func defaultClosingReminderConfig(chatID int64) GroupClosingReminderConfig {
	return GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      0,
		ReminderTime: defaultClosingReminderTime,
		ThresholdU:   defaultClosingReminderThresholdU,
		DMMode:       defaultClosingReminderDMMode,
	}
}

func normalizeClosingReminderConfig(config GroupClosingReminderConfig) GroupClosingReminderConfig {
	if config.Enabled != 0 {
		config.Enabled = 1
	}
	config.ReminderTime = normalizeClosingReminderTime(config.ReminderTime)
	if config.ThresholdU <= 0 {
		config.ThresholdU = defaultClosingReminderThresholdU
	}
	config.DMMode = normalizeClosingReminderDMMode(config.DMMode)
	return config
}

func normalizeClosingReminderTime(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultClosingReminderTime
	}
	if parsed, err := time.Parse("15:04", trimmed); err == nil {
		return parsed.Format("15:04")
	}
	if parsed, err := time.Parse("15:04:05", trimmed); err == nil {
		return parsed.Format("15:04")
	}
	return defaultClosingReminderTime
}

func normalizeClosingReminderDMMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ClosingReminderDMModeOff:
		return ClosingReminderDMModeOff
	case ClosingReminderDMModeOwner:
		return ClosingReminderDMModeOwner
	case ClosingReminderDMModeOperators:
		return ClosingReminderDMModeOperators
	case ClosingReminderDMModeOwnerOperators:
		return ClosingReminderDMModeOwnerOperators
	case ClosingReminderDMModeCustom:
		return ClosingReminderDMModeCustom
	case ClosingReminderDMModeOwnerOperatorsCustom:
		return ClosingReminderDMModeOwnerOperatorsCustom
	default:
		return defaultClosingReminderDMMode
	}
}

func normalizeClosingReminderStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ClosingReminderStatusNormal:
		return ClosingReminderStatusNormal
	case ClosingReminderStatusPending:
		return ClosingReminderStatusPending
	case ClosingReminderStatusNoData:
		return ClosingReminderStatusNoData
	case ClosingReminderStatusReview:
		return ClosingReminderStatusReview
	case ClosingReminderStatusFailed:
		return ClosingReminderStatusFailed
	default:
		return ClosingReminderStatusFailed
	}
}

func determineClosingReminderStatus(summary ClosingReminderSummary, thresholdU float64) string {
	if summary.DepositCount == 0 && summary.PayoutCount == 0 {
		return ClosingReminderStatusNoData
	}
	if summary.PayoutCount > 0 && summary.DepositCount == 0 {
		return ClosingReminderStatusReview
	}
	if summary.PayoutTotal > summary.DepositTotal+thresholdU {
		return ClosingReminderStatusReview
	}
	if summary.DepositTotal-summary.PayoutTotal > thresholdU {
		return ClosingReminderStatusPending
	}
	return ClosingReminderStatusNormal
}
