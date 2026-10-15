package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	dailySettlementReportDateLayout = "2006-01-02"
	dailySettlementQueryTimeLayout  = "2006-01-02 15:04:05"
	dailySettlementStoreTimeLayout  = "2006-01-02T15:04:05"
)

func (d *Database) RunDailySettlementBatch(reportDate string, periodStart, periodEnd time.Time) (DailySettlementBatchResult, error) {
	if d == nil {
		return DailySettlementBatchResult{}, errors.New("database is nil")
	}
	normalizedDate, startUTC, endUTC, err := normalizeDailySettlementWindow(reportDate, periodStart, periodEnd)
	if err != nil {
		return DailySettlementBatchResult{}, err
	}

	result := DailySettlementBatchResult{
		ReportDate:  normalizedDate,
		PeriodStart: startUTC.Format(dailySettlementStoreTimeLayout),
		PeriodEnd:   endUTC.Format(dailySettlementStoreTimeLayout),
	}

	groups, err := d.ListEnabledGroups()
	if err != nil {
		return result, err
	}
	result.TotalGroups = len(groups)

	for _, group := range groups {
		if group.ChatID == 0 {
			result.SkippedGroups++
			continue
		}
		report, buildErr := d.BuildDailySettlementReport(group.ChatID, normalizedDate, startUTC, endUTC)
		if buildErr != nil {
			return result, buildErr
		}
		if report.TotalEntries == 0 {
			result.SkippedGroups++
			continue
		}
		if upsertErr := d.UpsertDailySettlementReport(report); upsertErr != nil {
			return result, upsertErr
		}
		result.GeneratedReports++
	}

	return result, nil
}

func (d *Database) BuildDailySettlementReport(chatID int64, reportDate string, periodStart, periodEnd time.Time) (DailySettlementReport, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.BuildDailySettlementReport(chatID, reportDate, periodStart, periodEnd)
	}
	normalizedDate, startUTC, endUTC, err := normalizeDailySettlementWindow(reportDate, periodStart, periodEnd)
	if err != nil {
		return DailySettlementReport{}, err
	}

	startText := startUTC.Format(dailySettlementQueryTimeLayout)
	endText := endUTC.Format(dailySettlementQueryTimeLayout)
	rows, err := d.raw.Query(`
SELECT entry_type, UPPER(TRIM(currency)), final_amount
FROM ledger_entries
WHERE chat_id = ? AND reversed = 0
  AND datetime(created_at) >= datetime(?)
  AND datetime(created_at) < datetime(?)
ORDER BY id ASC`, chatID, startText, endText)
	if err != nil {
		return DailySettlementReport{}, err
	}
	defer rows.Close()

	report := DailySettlementReport{
		ChatID:            chatID,
		ReportDate:        normalizedDate,
		PeriodStart:       startUTC.Format(dailySettlementStoreTimeLayout),
		PeriodEnd:         endUTC.Format(dailySettlementStoreTimeLayout),
		CurrencyBreakdown: []DailySettlementCurrencySummary{},
		GeneratedAt:       nowUTCText(),
	}
	currencyStats := make(map[string]DailySettlementCurrencySummary)
	for rows.Next() {
		var (
			entryType   string
			currency    sql.NullString
			finalAmount float64
		)
		if err := rows.Scan(&entryType, &currency, &finalAmount); err != nil {
			return DailySettlementReport{}, err
		}

		normalizedType := strings.ToLower(strings.TrimSpace(entryType))
		if normalizedType != "deposit" && normalizedType != "payout" {
			continue
		}

		normalizedCurrency := "UNKNOWN"
		if currency.Valid {
			trimmedCurrency := strings.ToUpper(strings.TrimSpace(currency.String))
			if trimmedCurrency != "" {
				normalizedCurrency = trimmedCurrency
			}
		}

		summary := currencyStats[normalizedCurrency]
		if summary.Currency == "" {
			summary.Currency = normalizedCurrency
		}
		summary.EntryCount++
		report.TotalEntries++

		switch normalizedType {
		case "deposit":
			report.DepositCount++
			report.DepositTotal += finalAmount
			summary.DepositCount++
			summary.DepositTotal += finalAmount
		case "payout":
			report.PayoutCount++
			report.PayoutTotal += finalAmount
			summary.PayoutCount++
			summary.PayoutTotal += finalAmount
		}

		currencyStats[normalizedCurrency] = summary
	}
	if err := rows.Err(); err != nil {
		return DailySettlementReport{}, err
	}

	report.NetProfit = report.DepositTotal - report.PayoutTotal
	report.CurrencyBreakdown = flattenDailySettlementCurrencySummary(currencyStats)
	return report, nil
}

func (d *Database) UpsertDailySettlementReport(report DailySettlementReport) error {
	if report.ChatID == 0 {
		return errors.New("chat_id is required")
	}
	if routed := d.databaseForChat(report.ChatID); routed != d {
		return routed.UpsertDailySettlementReport(report)
	}

	if strings.TrimSpace(report.ReportDate) == "" {
		return errors.New("report_date is required")
	}
	if _, err := time.Parse(dailySettlementReportDateLayout, strings.TrimSpace(report.ReportDate)); err != nil {
		return fmt.Errorf("invalid report_date %q: %w", report.ReportDate, err)
	}
	if strings.TrimSpace(report.PeriodStart) == "" || strings.TrimSpace(report.PeriodEnd) == "" {
		return errors.New("period_start and period_end are required")
	}
	if strings.TrimSpace(report.GeneratedAt) == "" {
		report.GeneratedAt = nowUTCText()
	}

	report.NetProfit = report.DepositTotal - report.PayoutTotal
	breakdownJSON, err := json.Marshal(flattenDailySettlementCurrencySummary(sliceToDailySettlementCurrencySummaryMap(report.CurrencyBreakdown)))
	if err != nil {
		return err
	}

	_, err = d.raw.Exec(`
INSERT INTO daily_settlement_reports (
	chat_id, report_date, period_start, period_end,
	total_entries, deposit_count, payout_count,
	deposit_total, payout_total, net_profit,
	currency_breakdown, generated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(chat_id, report_date) DO UPDATE SET
	period_start = excluded.period_start,
	period_end = excluded.period_end,
	total_entries = excluded.total_entries,
	deposit_count = excluded.deposit_count,
	payout_count = excluded.payout_count,
	deposit_total = excluded.deposit_total,
	payout_total = excluded.payout_total,
	net_profit = excluded.net_profit,
	currency_breakdown = excluded.currency_breakdown,
	generated_at = excluded.generated_at`,
		report.ChatID,
		strings.TrimSpace(report.ReportDate),
		strings.TrimSpace(report.PeriodStart),
		strings.TrimSpace(report.PeriodEnd),
		report.TotalEntries,
		report.DepositCount,
		report.PayoutCount,
		report.DepositTotal,
		report.PayoutTotal,
		report.NetProfit,
		string(breakdownJSON),
		strings.TrimSpace(report.GeneratedAt),
	)
	return err
}

func (d *Database) GetDailySettlementReport(chatID int64, reportDate string) (DailySettlementReport, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetDailySettlementReport(chatID, reportDate)
	}
	if chatID == 0 {
		return DailySettlementReport{}, false, errors.New("chat_id is required")
	}
	trimmedDate := strings.TrimSpace(reportDate)
	if _, err := time.Parse(dailySettlementReportDateLayout, trimmedDate); err != nil {
		return DailySettlementReport{}, false, fmt.Errorf("invalid report_date %q: %w", reportDate, err)
	}

	row := d.raw.QueryRow(`
SELECT chat_id, report_date, period_start, period_end,
	total_entries, deposit_count, payout_count,
	deposit_total, payout_total, net_profit,
	currency_breakdown, generated_at
FROM daily_settlement_reports
WHERE chat_id = ? AND report_date = ?`, chatID, trimmedDate)

	var (
		report        DailySettlementReport
		breakdownJSON string
	)
	if err := row.Scan(
		&report.ChatID,
		&report.ReportDate,
		&report.PeriodStart,
		&report.PeriodEnd,
		&report.TotalEntries,
		&report.DepositCount,
		&report.PayoutCount,
		&report.DepositTotal,
		&report.PayoutTotal,
		&report.NetProfit,
		&breakdownJSON,
		&report.GeneratedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return DailySettlementReport{}, false, nil
		}
		return DailySettlementReport{}, false, err
	}

	report.CurrencyBreakdown = []DailySettlementCurrencySummary{}
	if strings.TrimSpace(breakdownJSON) != "" {
		if err := json.Unmarshal([]byte(breakdownJSON), &report.CurrencyBreakdown); err != nil {
			return DailySettlementReport{}, false, err
		}
	}
	report.CurrencyBreakdown = flattenDailySettlementCurrencySummary(sliceToDailySettlementCurrencySummaryMap(report.CurrencyBreakdown))
	report.NetProfit = report.DepositTotal - report.PayoutTotal
	return report, true, nil
}

func normalizeDailySettlementWindow(reportDate string, periodStart, periodEnd time.Time) (string, time.Time, time.Time, error) {
	if periodStart.IsZero() || periodEnd.IsZero() {
		return "", time.Time{}, time.Time{}, errors.New("period_start and period_end are required")
	}
	startUTC := periodStart.UTC()
	endUTC := periodEnd.UTC()
	if !endUTC.After(startUTC) {
		return "", time.Time{}, time.Time{}, errors.New("period_end must be greater than period_start")
	}

	trimmedDate := strings.TrimSpace(reportDate)
	if trimmedDate == "" {
		trimmedDate = startUTC.Format(dailySettlementReportDateLayout)
	}
	if _, err := time.Parse(dailySettlementReportDateLayout, trimmedDate); err != nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("invalid report_date %q: %w", reportDate, err)
	}
	return trimmedDate, startUTC, endUTC, nil
}

func flattenDailySettlementCurrencySummary(stats map[string]DailySettlementCurrencySummary) []DailySettlementCurrencySummary {
	if len(stats) == 0 {
		return []DailySettlementCurrencySummary{}
	}
	keys := make([]string, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]DailySettlementCurrencySummary, 0, len(keys))
	for _, key := range keys {
		item := stats[key]
		if item.Currency == "" {
			item.Currency = key
		}
		item.NetProfit = item.DepositTotal - item.PayoutTotal
		out = append(out, item)
	}
	return out
}

func sliceToDailySettlementCurrencySummaryMap(items []DailySettlementCurrencySummary) map[string]DailySettlementCurrencySummary {
	if len(items) == 0 {
		return map[string]DailySettlementCurrencySummary{}
	}
	out := make(map[string]DailySettlementCurrencySummary, len(items))
	for _, item := range items {
		currency := strings.ToUpper(strings.TrimSpace(item.Currency))
		if currency == "" {
			currency = "UNKNOWN"
		}
		item.Currency = currency
		item.NetProfit = item.DepositTotal - item.PayoutTotal
		out[currency] = item
	}
	return out
}
