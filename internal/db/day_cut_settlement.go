package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultDayCutSettlementTime = "04:00:00"

func (d *Database) RunDayCutSettlementBatch(runAt time.Time, timezoneName string) (DayCutSettlementBatchResult, error) {
	if d == nil {
		return DayCutSettlementBatchResult{}, errors.New("database is nil")
	}
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	loc := loadDayCutSettlementLocation(timezoneName)

	result := DayCutSettlementBatchResult{
		RunAt:    runAt.UTC().Format(dailySettlementStoreTimeLayout),
		Timezone: loc.String(),
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
		targetReportDate, normalizedCutTime, _, _, windowErr := resolveDayCutSettlementWindow(runAt, loc, group.DayCutTime)
		if windowErr != nil {
			return result, windowErr
		}
		pendingReportDates, pendingErr := d.listPendingDayCutSettlementReportDates(group.ChatID, targetReportDate)
		if pendingErr != nil {
			return result, pendingErr
		}
		if len(pendingReportDates) == 0 {
			result.SkippedGroups++
			continue
		}
		generatedForGroup := 0
		for _, reportDate := range pendingReportDates {
			normalizedDate, normalizedDateCutTime, periodStart, periodEnd, periodErr := resolveDayCutSettlementWindowByReportDate(reportDate, loc, normalizedCutTime)
			if periodErr != nil {
				return result, periodErr
			}
			report, buildErr := d.BuildDayCutSettlementReport(group.ChatID, normalizedDate, normalizedDateCutTime, periodStart, periodEnd)
			if buildErr != nil {
				return result, buildErr
			}
			if report.TotalEntries == 0 {
				continue
			}
			if upsertErr := d.UpsertDayCutSettlementReport(report); upsertErr != nil {
				return result, upsertErr
			}
			generatedForGroup++
			result.GeneratedReports++
			if normalizedDate != targetReportDate {
				result.ForcedReports++
			}
			if report.NetFeeTotal < 0 {
				result.DeficitReports++
			}
		}
		if generatedForGroup == 0 {
			result.SkippedGroups++
		}
	}
	return result, nil
}

func (d *Database) BuildDayCutSettlementReport(chatID int64, reportDate, dayCutTime string, periodStart, periodEnd time.Time) (DayCutSettlementReport, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.BuildDayCutSettlementReport(chatID, reportDate, dayCutTime, periodStart, periodEnd)
	}
	if chatID == 0 {
		return DayCutSettlementReport{}, errors.New("chat_id is required")
	}
	normalizedDate, startUTC, endUTC, err := normalizeDailySettlementWindow(reportDate, periodStart, periodEnd)
	if err != nil {
		return DayCutSettlementReport{}, err
	}
	normalizedCutTime, _, _, _ := normalizeDayCutSettlementTime(dayCutTime)

	startText := startUTC.Format(dailySettlementQueryTimeLayout)
	endText := endUTC.Format(dailySettlementQueryTimeLayout)
	rows, err := d.raw.Query(`
SELECT entry_type, UPPER(TRIM(currency)), base_amount, final_amount
FROM ledger_entries
WHERE chat_id = ? AND reversed = 0
  AND datetime(created_at) >= datetime(?)
  AND datetime(created_at) < datetime(?)
ORDER BY id ASC`, chatID, startText, endText)
	if err != nil {
		return DayCutSettlementReport{}, err
	}
	defer rows.Close()

	report := DayCutSettlementReport{
		ChatID:            chatID,
		ReportDate:        normalizedDate,
		DayCutTime:        normalizedCutTime,
		PeriodStart:       startUTC.Format(dailySettlementStoreTimeLayout),
		PeriodEnd:         endUTC.Format(dailySettlementStoreTimeLayout),
		CurrencyBreakdown: []DayCutSettlementCurrencySummary{},
		GeneratedAt:       nowUTCText(),
	}
	currencyStats := make(map[string]DayCutSettlementCurrencySummary)
	for rows.Next() {
		var (
			entryType   string
			currency    sql.NullString
			baseAmount  float64
			finalAmount float64
		)
		if err := rows.Scan(&entryType, &currency, &baseAmount, &finalAmount); err != nil {
			return DayCutSettlementReport{}, err
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
			fee := baseAmount - finalAmount
			report.DepositCount++
			report.DepositFeeTotal += fee
			summary.DepositCount++
			summary.DepositFeeTotal += fee
		case "payout":
			fee := finalAmount - baseAmount
			report.PayoutCount++
			report.PayoutFeeTotal += fee
			summary.PayoutCount++
			summary.PayoutFeeTotal += fee
		}

		currencyStats[normalizedCurrency] = summary
	}
	if err := rows.Err(); err != nil {
		return DayCutSettlementReport{}, err
	}

	report.NetFeeTotal = report.DepositFeeTotal - report.PayoutFeeTotal
	report.CurrencyBreakdown = flattenDayCutSettlementCurrencySummary(currencyStats)
	return report, nil
}

func (d *Database) UpsertDayCutSettlementReport(report DayCutSettlementReport) error {
	if report.ChatID == 0 {
		return errors.New("chat_id is required")
	}
	if routed := d.databaseForChat(report.ChatID); routed != d {
		return routed.UpsertDayCutSettlementReport(report)
	}

	trimmedDate := strings.TrimSpace(report.ReportDate)
	if trimmedDate == "" {
		return errors.New("report_date is required")
	}
	if _, err := time.Parse(dailySettlementReportDateLayout, trimmedDate); err != nil {
		return fmt.Errorf("invalid report_date %q: %w", report.ReportDate, err)
	}

	normalizedCutTime, _, _, _ := normalizeDayCutSettlementTime(report.DayCutTime)
	if strings.TrimSpace(report.PeriodStart) == "" || strings.TrimSpace(report.PeriodEnd) == "" {
		return errors.New("period_start and period_end are required")
	}
	if strings.TrimSpace(report.GeneratedAt) == "" {
		report.GeneratedAt = nowUTCText()
	}
	report.DayCutTime = normalizedCutTime
	report.NetFeeTotal = report.DepositFeeTotal - report.PayoutFeeTotal

	breakdownJSON, err := json.Marshal(flattenDayCutSettlementCurrencySummary(sliceToDayCutSettlementCurrencySummaryMap(report.CurrencyBreakdown)))
	if err != nil {
		return err
	}

	_, err = d.raw.Exec(`
INSERT INTO day_cut_settlement_reports (
	chat_id, report_date, day_cut_time, period_start, period_end,
	total_entries, deposit_count, payout_count,
	deposit_fee_total, payout_fee_total, net_fee_total,
	currency_breakdown, generated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(chat_id, report_date) DO UPDATE SET
	day_cut_time = excluded.day_cut_time,
	period_start = excluded.period_start,
	period_end = excluded.period_end,
	total_entries = excluded.total_entries,
	deposit_count = excluded.deposit_count,
	payout_count = excluded.payout_count,
	deposit_fee_total = excluded.deposit_fee_total,
	payout_fee_total = excluded.payout_fee_total,
	net_fee_total = excluded.net_fee_total,
	currency_breakdown = excluded.currency_breakdown,
	generated_at = excluded.generated_at`,
		report.ChatID,
		trimmedDate,
		report.DayCutTime,
		strings.TrimSpace(report.PeriodStart),
		strings.TrimSpace(report.PeriodEnd),
		report.TotalEntries,
		report.DepositCount,
		report.PayoutCount,
		report.DepositFeeTotal,
		report.PayoutFeeTotal,
		report.NetFeeTotal,
		string(breakdownJSON),
		strings.TrimSpace(report.GeneratedAt),
	)
	return err
}

func (d *Database) GetDayCutSettlementReport(chatID int64, reportDate string) (DayCutSettlementReport, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetDayCutSettlementReport(chatID, reportDate)
	}
	if chatID == 0 {
		return DayCutSettlementReport{}, false, errors.New("chat_id is required")
	}
	trimmedDate := strings.TrimSpace(reportDate)
	if _, err := time.Parse(dailySettlementReportDateLayout, trimmedDate); err != nil {
		return DayCutSettlementReport{}, false, fmt.Errorf("invalid report_date %q: %w", reportDate, err)
	}

	row := d.raw.QueryRow(`
SELECT chat_id, report_date, day_cut_time, period_start, period_end,
	total_entries, deposit_count, payout_count,
	deposit_fee_total, payout_fee_total, net_fee_total,
	currency_breakdown, generated_at
FROM day_cut_settlement_reports
WHERE chat_id = ? AND report_date = ?`, chatID, trimmedDate)

	var (
		report        DayCutSettlementReport
		breakdownJSON string
	)
	if err := row.Scan(
		&report.ChatID,
		&report.ReportDate,
		&report.DayCutTime,
		&report.PeriodStart,
		&report.PeriodEnd,
		&report.TotalEntries,
		&report.DepositCount,
		&report.PayoutCount,
		&report.DepositFeeTotal,
		&report.PayoutFeeTotal,
		&report.NetFeeTotal,
		&breakdownJSON,
		&report.GeneratedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return DayCutSettlementReport{}, false, nil
		}
		return DayCutSettlementReport{}, false, err
	}

	report.DayCutTime, _, _, _ = normalizeDayCutSettlementTime(report.DayCutTime)
	report.CurrencyBreakdown = []DayCutSettlementCurrencySummary{}
	if strings.TrimSpace(breakdownJSON) != "" {
		if err := json.Unmarshal([]byte(breakdownJSON), &report.CurrencyBreakdown); err != nil {
			return DayCutSettlementReport{}, false, err
		}
	}
	report.CurrencyBreakdown = flattenDayCutSettlementCurrencySummary(sliceToDayCutSettlementCurrencySummaryMap(report.CurrencyBreakdown))
	report.NetFeeTotal = report.DepositFeeTotal - report.PayoutFeeTotal
	return report, true, nil
}

func (d *Database) listPendingDayCutSettlementReportDates(chatID int64, targetReportDate string) ([]string, error) {
	latestReportDate, found, err := d.getLatestDayCutSettlementReportDate(chatID)
	if err != nil {
		return nil, err
	}
	return buildPendingDayCutSettlementReportDates(latestReportDate, found, targetReportDate)
}

func (d *Database) getLatestDayCutSettlementReportDate(chatID int64) (string, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.getLatestDayCutSettlementReportDate(chatID)
	}
	if chatID == 0 {
		return "", false, errors.New("chat_id is required")
	}

	row := d.raw.QueryRow(`
SELECT report_date
FROM day_cut_settlement_reports
WHERE chat_id = ?
ORDER BY report_date DESC
LIMIT 1`, chatID)
	var reportDate string
	if err := row.Scan(&reportDate); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(reportDate), true, nil
}

func buildPendingDayCutSettlementReportDates(latestReportDate string, hasLatest bool, targetReportDate string) ([]string, error) {
	trimmedTargetDate := strings.TrimSpace(targetReportDate)
	targetDay, err := time.Parse(dailySettlementReportDateLayout, trimmedTargetDate)
	if err != nil {
		return nil, fmt.Errorf("invalid target report_date %q: %w", targetReportDate, err)
	}

	startDay := targetDay
	if hasLatest {
		latestDay, parseErr := time.Parse(dailySettlementReportDateLayout, strings.TrimSpace(latestReportDate))
		if parseErr != nil {
			return nil, fmt.Errorf("invalid latest report_date %q: %w", latestReportDate, parseErr)
		}
		startDay = latestDay.AddDate(0, 0, 1)
		if startDay.After(targetDay) {
			return []string{}, nil
		}
	}

	pending := make([]string, 0, int(targetDay.Sub(startDay).Hours()/24)+1)
	for currentDay := startDay; !currentDay.After(targetDay); currentDay = currentDay.AddDate(0, 0, 1) {
		pending = append(pending, currentDay.Format(dailySettlementReportDateLayout))
	}
	return pending, nil
}

func loadDayCutSettlementLocation(timezoneName string) *time.Location {
	locationName := strings.TrimSpace(timezoneName)
	if locationName == "" {
		locationName = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(locationName)
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

func resolveDayCutSettlementWindow(runAt time.Time, loc *time.Location, dayCutTime string) (string, string, time.Time, time.Time, error) {
	if runAt.IsZero() {
		return "", "", time.Time{}, time.Time{}, errors.New("run_at is required")
	}
	if loc == nil {
		loc = time.UTC
	}
	normalizedCutTime, h, m, s := normalizeDayCutSettlementTime(dayCutTime)
	localNow := runAt.In(loc)
	cutToday := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), h, m, s, 0, loc)
	periodEndLocal := cutToday
	if localNow.Before(cutToday) {
		periodEndLocal = cutToday.AddDate(0, 0, -1)
	}
	periodStartLocal := periodEndLocal.AddDate(0, 0, -1)
	reportDate := periodStartLocal.Format(dailySettlementReportDateLayout)
	return reportDate, normalizedCutTime, periodStartLocal.UTC(), periodEndLocal.UTC(), nil
}

func resolveDayCutSettlementWindowByReportDate(reportDate string, loc *time.Location, dayCutTime string) (string, string, time.Time, time.Time, error) {
	trimmedReportDate := strings.TrimSpace(reportDate)
	if trimmedReportDate == "" {
		return "", "", time.Time{}, time.Time{}, errors.New("report_date is required")
	}
	if loc == nil {
		loc = time.UTC
	}
	normalizedCutTime, h, m, s := normalizeDayCutSettlementTime(dayCutTime)
	localDate, err := time.ParseInLocation(dailySettlementReportDateLayout, trimmedReportDate, loc)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, fmt.Errorf("invalid report_date %q: %w", reportDate, err)
	}
	periodStartLocal := time.Date(localDate.Year(), localDate.Month(), localDate.Day(), h, m, s, 0, loc)
	periodEndLocal := periodStartLocal.AddDate(0, 0, 1)
	return localDate.Format(dailySettlementReportDateLayout), normalizedCutTime, periodStartLocal.UTC(), periodEndLocal.UTC(), nil
}

func normalizeDayCutSettlementTime(dayCutTime string) (string, int, int, int) {
	trimmed := strings.TrimSpace(dayCutTime)
	if trimmed == "" {
		return defaultDayCutSettlementTime, 4, 0, 0
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) != 3 {
		return defaultDayCutSettlementTime, 4, 0, 0
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return defaultDayCutSettlementTime, 4, 0, 0
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return defaultDayCutSettlementTime, 4, 0, 0
	}
	s, err := strconv.Atoi(parts[2])
	if err != nil || s < 0 || s > 59 {
		return defaultDayCutSettlementTime, 4, 0, 0
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s), h, m, s
}

func flattenDayCutSettlementCurrencySummary(stats map[string]DayCutSettlementCurrencySummary) []DayCutSettlementCurrencySummary {
	if len(stats) == 0 {
		return []DayCutSettlementCurrencySummary{}
	}
	keys := make([]string, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]DayCutSettlementCurrencySummary, 0, len(keys))
	for _, key := range keys {
		item := stats[key]
		if item.Currency == "" {
			item.Currency = key
		}
		item.NetFeeTotal = item.DepositFeeTotal - item.PayoutFeeTotal
		out = append(out, item)
	}
	return out
}

func sliceToDayCutSettlementCurrencySummaryMap(items []DayCutSettlementCurrencySummary) map[string]DayCutSettlementCurrencySummary {
	if len(items) == 0 {
		return map[string]DayCutSettlementCurrencySummary{}
	}
	out := make(map[string]DayCutSettlementCurrencySummary, len(items))
	for _, item := range items {
		currency := strings.ToUpper(strings.TrimSpace(item.Currency))
		if currency == "" {
			currency = "UNKNOWN"
		}
		item.Currency = currency
		item.NetFeeTotal = item.DepositFeeTotal - item.PayoutFeeTotal
		out[currency] = item
	}
	return out
}
