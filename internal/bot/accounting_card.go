package bot

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
	"go-bot/internal/utils"
)

const ledgerCardMaxLinesPerSide = 12

var ledgerCardReversalRemarkPattern = regexp.MustCompile(`(?i)^REV:\s*[0-9]+(?:\s|$)`)

type ledgerCardFallback struct {
	EntryType   string
	Amount      float64
	Currency    string
	Rate        float64
	FinalAmount float64
	Note        string
	CreatedAt   time.Time
}

type ledgerCardDisplayConfig struct {
	PayoutFeeRate float64
	PayoutRate    float64
	HideReversed  bool
}

func ledgerCardReceiptMarkup() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 查看账单", ledgerCardCallbackPrefix+ledgerCardActionDetail),
		),
	)
}

func (a *App) buildAccountingDataCardText(chatID int64, g db.GroupConfig, fallback ledgerCardFallback) string {
	startAt := utils.GetDayStartISO(g.DayCutTime, a.cfg.Timezone)
	rows, err := a.db.ListLedgerSince(chatID, startAt)
	if err != nil || len(rows) == 0 {
		rows = []db.LedgerEntry{toLedgerCardFallbackEntry(fallback)}
	}
	return buildAccountingDataCardMarkdownV2FromRows(rows, g.FeeRate, g.CustomRate, a.loc, ledgerCardDisplayConfig{
		PayoutFeeRate: g.PayoutFeeRate,
		PayoutRate:    g.PayoutRate,
		HideReversed:  g.LedgerCardHideReversed == 1,
	})
}

func (a *App) replyAccountingDataCard(msg *tgbotapi.Message, g db.GroupConfig) error {
	return a.replyAccountingDataCardWithStatus(msg, g, "", "ledger_card", false)
}

func (a *App) replyAccountingDataCardWithStatus(msg *tgbotapi.Message, g db.GroupConfig, statusLine, commandKey string, cacheable bool) error {
	if a == nil || a.db == nil || msg == nil || msg.Chat == nil {
		return nil
	}
	startAt := utils.GetDayStartISO(g.DayCutTime, a.cfg.Timezone)
	rows, err := a.db.ListLedgerSince(msg.Chat.ID, startAt)
	if err != nil {
		return err
	}
	cardText := buildAccountingDataCardMarkdownV2FromRows(rows, g.FeeRate, g.CustomRate, a.loc, ledgerCardDisplayConfig{
		PayoutFeeRate: g.PayoutFeeRate,
		PayoutRate:    g.PayoutRate,
		HideReversed:  g.LedgerCardHideReversed == 1,
	})
	statusLine = strings.TrimSpace(statusLine)
	if statusLine != "" {
		cardText = escapeTelegramMarkdownV2(statusLine) + "\n\n" + cardText
	}
	markup := ledgerCardReceiptMarkup()
	commandKey = strings.TrimSpace(commandKey)
	if commandKey == "" {
		commandKey = "ledger_card"
	}
	return a.replyBookkeepingResultWithMarkupAndParseMode(msg, commandKey, cardText, cacheable, &markup, telegramParseModeMarkdownV)
}

func toLedgerCardFallbackEntry(fallback ledgerCardFallback) db.LedgerEntry {
	createdAt := fallback.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return db.LedgerEntry{
		EntryType:   strings.TrimSpace(fallback.EntryType),
		Amount:      fallback.Amount,
		Currency:    strings.TrimSpace(fallback.Currency),
		Rate:        fallback.Rate,
		FinalAmount: fallback.FinalAmount,
		Note:        strings.TrimSpace(fallback.Note),
		CreatedAt:   createdAt.Format(time.RFC3339),
	}
}

func buildAccountingDataCardFromRows(rows []db.LedgerEntry, feeRate, defaultRate float64, loc *time.Location, extras ...ledgerCardDisplayConfig) string {
	rows = ledgerCardVisibleRows(rows, ledgerCardHideReversedFromExtras(extras))
	location := loc
	if location == nil {
		location = time.FixedZone("CST", 8*3600)
	}
	fallbackRate := normalizeCardRate(defaultRate)
	payoutAmountWidth, payoutConvertedWidth := measureLedgerCardPayoutColumnWidths(rows, fallbackRate)
	depositFormulaWidth := measureLedgerCardDepositFormulaWidth(rows, fallbackRate, feeRate)

	depositLines := make([]string, 0, len(rows))
	payoutLines := make([]string, 0, len(rows))
	depositCount := 0
	payoutCount := 0
	depositTotalAmount := 0.0
	depositTotalU := 0.0
	payoutTotalU := 0.0
	currentDepositRate := fallbackRate

	for _, row := range rows {
		entryType := strings.ToLower(strings.TrimSpace(row.EntryType))
		switch entryType {
		case "deposit":
			depositCount++
			rate := normalizeCardRateWithFallback(row.Rate, fallbackRate)
			currentDepositRate = rate
			amount := normalizeLedgerCardAmount(row)
			netConverted, feeFactor := computeLedgerCardDepositConvertedAndFactor(row, amount, rate, feeRate)
			displayConverted := roundLedgerCardDisplayU(netConverted)
			trackLink := resolveLedgerCardTrackLink(row)
			depositLine := formatLedgerCardDepositLine(
				formatLedgerCardClock(row.CreatedAt, location),
				amount,
				rate,
				row.Currency,
				feeFactor,
				displayConverted,
				depositFormulaWidth,
				trackLink,
				formatLedgerCardActorText(row.Nickname),
			)
			depositLines = append(depositLines, appendLedgerCardRemark(depositLine, row))
			if row.Reversed != 1 {
				depositTotalAmount += amount
				depositTotalU += displayConverted
			}
		case "payout":
			payoutCount++
			rate := normalizeCardRateWithFallback(row.Rate, fallbackRate)
			amount := normalizeLedgerCardAmount(row)
			settlementAmount := normalizeLedgerCardPayoutSettlementAmount(row, amount)
			payoutConvertedU := roundLedgerCardDisplayU(convertLedgerCardAmountToU(settlementAmount, row.Currency, rate))
			amountText, convertedText := formatLedgerCardPayoutColumnTexts(amount, row.Currency, rate, payoutConvertedU)
			payoutLine := fmt.Sprintf(
				"%s %*s [%*s]",
				formatLedgerCardClock(row.CreatedAt, location),
				payoutAmountWidth,
				amountText,
				payoutConvertedWidth,
				convertedText,
			)
			payoutLine += formatLedgerCardActorText(row.Nickname)
			payoutLines = append(payoutLines, appendLedgerCardRemark(payoutLine, row))
			if row.Reversed != 1 {
				payoutTotalU += payoutConvertedU
			}
		}
	}

	depositLines = tailLedgerCardLines(depositLines, ledgerCardMaxLinesPerSide)
	payoutLines = tailLedgerCardLines(payoutLines, ledgerCardMaxLinesPerSide)

	expectedPayout := depositTotalU
	remaining := expectedPayout - payoutTotalU
	var b strings.Builder
	b.WriteString(fmt.Sprintf("入款 (%d) 笔:\n", depositCount))
	if len(depositLines) > 0 {
		b.WriteString(strings.Join(depositLines, "\n"))
	}
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("下发 (%d) 笔:\n", payoutCount))
	if len(payoutLines) > 0 {
		b.WriteString(strings.Join(payoutLines, "\n"))
	}
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("当前入款: %s | %s U\n", utils.FormatAmount(depositTotalAmount), formatLedgerCardDisplayU(depositTotalU)))
	b.WriteString(fmt.Sprintf("入款费率: %s%%\n", utils.FormatAmount(feeRate)))
	b.WriteString(fmt.Sprintf("入款汇率: %s\n", utils.FormatAmount(currentDepositRate)))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("应下发: %s U\n", formatLedgerCardDisplayU(expectedPayout)))
	b.WriteString(fmt.Sprintf("总下发: %s U\n", formatLedgerCardDisplayU(payoutTotalU)))
	b.WriteString(fmt.Sprintf("未下发: %s U", formatLedgerCardDisplayU(remaining)))
	return b.String()
}

func buildAccountingDataCardMarkdownV2FromRows(rows []db.LedgerEntry, feeRate, defaultRate float64, loc *time.Location, extras ...ledgerCardDisplayConfig) string {
	rows = ledgerCardVisibleRows(rows, ledgerCardHideReversedFromExtras(extras))
	location := loc
	if location == nil {
		location = time.FixedZone("CST", 8*3600)
	}
	fallbackRate := normalizeCardRate(defaultRate)
	payoutAmountWidth, payoutConvertedWidth := measureLedgerCardPayoutColumnWidths(rows, fallbackRate)

	depositLines := make([]string, 0, len(rows))
	payoutLines := make([]string, 0, len(rows))
	depositCount := 0
	payoutCount := 0
	depositTotalAmount := 0.0
	depositTotalU := 0.0
	payoutTotalU := 0.0
	currentDepositRate := fallbackRate

	for _, row := range rows {
		entryType := strings.ToLower(strings.TrimSpace(row.EntryType))
		switch entryType {
		case "deposit":
			depositCount++
			rate := normalizeCardRateWithFallback(row.Rate, fallbackRate)
			currentDepositRate = rate
			amount := normalizeLedgerCardAmount(row)
			netConverted, feeFactor := computeLedgerCardDepositConvertedAndFactor(row, amount, rate, feeRate)
			displayConverted := roundLedgerCardDisplayU(netConverted)
			trackLink := resolveLedgerCardTrackLink(row)
			line := formatLedgerCardDepositLineMarkdownV2(
				formatLedgerCardClock(row.CreatedAt, location),
				amount,
				rate,
				row.Currency,
				feeFactor,
				displayConverted,
				trackLink,
				row.Nickname,
			)
			depositLines = append(depositLines, appendLedgerCardRemarkMarkdown(line, row))
			if row.Reversed != 1 {
				depositTotalAmount += amount
				depositTotalU += displayConverted
			}
		case "payout":
			payoutCount++
			rate := normalizeCardRateWithFallback(row.Rate, fallbackRate)
			amount := normalizeLedgerCardAmount(row)
			settlementAmount := normalizeLedgerCardPayoutSettlementAmount(row, amount)
			payoutConvertedU := roundLedgerCardDisplayU(convertLedgerCardAmountToU(settlementAmount, row.Currency, rate))
			amountText, convertedText := formatLedgerCardPayoutColumnTexts(amount, row.Currency, rate, payoutConvertedU)
			paddedAmountText := fmt.Sprintf("%*s", payoutAmountWidth, amountText)
			trackLink := resolveLedgerCardTrackLink(row)
			line := mdCode(formatLedgerCardClock(row.CreatedAt, location)) +
				" " + formatLedgerCardPayoutAmountMarkdownWithLink(paddedAmountText, trackLink) +
				" \\[" + mdCodePreserveSpaces(fmt.Sprintf("%*s", payoutConvertedWidth, convertedText)) + "\\]"
			line += formatLedgerCardActorMarkdownWithLink(row.Nickname, trackLink)
			payoutLines = append(payoutLines, appendLedgerCardRemarkMarkdown(line, row))
			if row.Reversed != 1 {
				payoutTotalU += payoutConvertedU
			}
		}
	}

	depositLines = tailLedgerCardLines(depositLines, ledgerCardMaxLinesPerSide)
	payoutLines = tailLedgerCardLines(payoutLines, ledgerCardMaxLinesPerSide)

	expectedPayout := depositTotalU
	remaining := expectedPayout - payoutTotalU
	var b strings.Builder
	b.WriteString("今日入款 \\(" + mdCode(strconv.Itoa(depositCount)) + "\\) 笔:\n")
	if len(depositLines) > 0 {
		b.WriteString(strings.Join(depositLines, "\n"))
	}
	b.WriteString("\n\n")
	b.WriteString("今日下发 \\(" + mdCode(strconv.Itoa(payoutCount)) + "\\) 笔:\n")
	if len(payoutLines) > 0 {
		b.WriteString(strings.Join(payoutLines, "\n"))
	}
	b.WriteString("\n\n")
	b.WriteString("当前入款: " + mdCode(utils.FormatAmount(depositTotalAmount)) + " \\| " + mdCode(formatLedgerCardDisplayU(depositTotalU)) + " U\n")
	b.WriteString("入款费率: " + mdCode(utils.FormatAmount(feeRate)) + "%\n")
	b.WriteString("入款汇率: " + mdCode(utils.FormatAmount(currentDepositRate)) + "\n")
	b.WriteString("\n")
	b.WriteString("应下发: " + mdCode(formatLedgerCardDisplayU(expectedPayout)) + " U\n")
	b.WriteString("总下发: " + mdCode(formatLedgerCardDisplayU(payoutTotalU)) + " U\n")
	b.WriteString("未下发: " + mdCode(formatLedgerCardDisplayU(remaining)) + " U")
	return b.String()
}

func tailLedgerCardLines(lines []string, max int) []string {
	if max <= 0 || len(lines) <= max {
		return lines
	}
	return lines[len(lines)-max:]
}

func normalizeCardRate(rate float64) float64 {
	if almostZero(rate) {
		return 1
	}
	return rate
}

func normalizeCardRateWithFallback(rate, fallback float64) float64 {
	if !almostZero(rate) {
		return rate
	}
	return normalizeCardRate(fallback)
}

func normalizeLedgerCardAmount(row db.LedgerEntry) float64 {
	if !almostZero(row.Amount) {
		return row.Amount
	}
	if !almostZero(row.FinalAmount) {
		return row.FinalAmount
	}
	return row.BaseAmount
}

func normalizeLedgerCardPayoutSettlementAmount(row db.LedgerEntry, fallbackAmount float64) float64 {
	if !almostZero(row.FinalAmount) || !almostZero(row.FeeRate) {
		return row.FinalAmount
	}
	return fallbackAmount
}

func convertLedgerCardAmountToU(amount float64, currency string, rate float64) float64 {
	if isLedgerCardUSDT(currency) {
		return amount
	}
	return amount / normalizeCardRate(rate)
}

func isLedgerCardUSDT(currency string) bool {
	return strings.EqualFold(strings.TrimSpace(currency), "USDT")
}

func normalizeLedgerCardDisplayCurrency(raw string) string {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if currency == "" || currency == "CNY" {
		return ""
	}
	return currency
}

func normalizeLedgerCardRemark(note string) string {
	return strings.TrimSpace(note)
}

func isLedgerCardReversalEntry(row db.LedgerEntry) bool {
	return ledgerCardReversalRemarkPattern.MatchString(strings.TrimSpace(row.Note))
}

func ledgerCardHideReversedFromExtras(extras []ledgerCardDisplayConfig) bool {
	return len(extras) > 0 && extras[0].HideReversed
}

func ledgerCardVisibleRows(rows []db.LedgerEntry, hideReversed bool) []db.LedgerEntry {
	visible := make([]db.LedgerEntry, 0, len(rows))
	for _, row := range rows {
		if hideReversed {
			if row.Reversed == 1 || row.ReversalOf != nil || isLedgerCardLegacyReversalEntry(row) {
				continue
			}
		} else if isLedgerCardReversalEntry(row) {
			continue
		}
		visible = append(visible, row)
	}
	return visible
}

func isLedgerCardLegacyReversalEntry(row db.LedgerEntry) bool {
	if !isLedgerCardReversalEntry(row) {
		return false
	}
	return row.Amount < 0 || row.BaseAmount < 0 || row.FinalAmount < 0
}

func ledgerCardRemark(row db.LedgerEntry) string {
	if isLedgerCardReversalEntry(row) {
		return ""
	}
	remark := normalizeLedgerCardRemark(row.Note)
	if row.Reversed != 1 {
		return remark
	}
	if remark == "" {
		return "已撤回"
	}
	return remark + " | 已撤回"
}

func appendLedgerCardRemark(prefix string, row db.LedgerEntry) string {
	remark := ledgerCardRemark(row)
	if remark == "" {
		return prefix
	}
	return prefix + " " + remark
}

func appendLedgerCardRemarkMarkdown(prefix string, row db.LedgerEntry) string {
	remark := ledgerCardRemark(row)
	if remark == "" {
		return prefix
	}
	return prefix + " \\| " + escapeTelegramMarkdownV2(remark)
}

func mdCode(raw string) string {
	return "`" + strings.TrimSpace(raw) + "`"
}

func mdCodePreserveSpaces(raw string) string {
	return "`" + raw + "`"
}

func computeLedgerCardDepositConvertedAndFactor(row db.LedgerEntry, amount, rate, fallbackFeeRate float64) (float64, float64) {
	baseConverted := convertLedgerCardAmountToU(amount, row.Currency, rate)
	if !almostZero(row.FinalAmount) {
		finalConverted := convertLedgerCardAmountToU(row.FinalAmount, row.Currency, rate)
		if almostZero(baseConverted) {
			return finalConverted, 1
		}
		return finalConverted, finalConverted / baseConverted
	}
	effectiveFeeRate := row.FeeRate
	if almostZero(effectiveFeeRate) {
		effectiveFeeRate = fallbackFeeRate
	}
	feeFactor := 1 + effectiveFeeRate/100.0
	return baseConverted * feeFactor, feeFactor
}

func formatLedgerCardDepositLine(clock string, amount, rate float64, currency string, feeFactor, converted float64, formulaWidth int, trackLink, actorText string) string {
	var line strings.Builder
	line.WriteString(clock)
	line.WriteString(" ")
	line.WriteString(formatLedgerCardDepositFormulaAligned(amount, rate, currency, feeFactor, converted, formulaWidth))
	line.WriteString(formatLedgerCardTrackText(trackLink))
	line.WriteString(actorText)
	return line.String()
}

func formatLedgerCardDepositLineMarkdownV2(clock string, amount, rate float64, currency string, feeFactor, converted float64, trackLink, actorName string) string {
	var line strings.Builder
	line.WriteString(mdCode(clock))
	line.WriteString(" ")
	line.WriteString(formatLedgerCardDepositFormulaMarkdownV2(amount, rate, currency, feeFactor, converted, trackLink))
	line.WriteString(formatLedgerCardDepositActorMarkdownWithLink(actorName, trackLink))
	return line.String()
}

func formatLedgerCardTrackText(trackLink string) string {
	track := strings.TrimSpace(trackLink)
	if track == "" {
		return ""
	}
	return " (" + track + ")"
}

func resolveLedgerCardTrackLink(row db.LedgerEntry) string {
	track := strings.TrimSpace(row.TrackLink)
	if isLedgerCardUserProfileLink(track) || isLedgerCardInaccessibleMessageLink(track, row.ChatID) {
		track = ""
	}
	if track != "" {
		return track
	}
	if row.ChatID != 0 && row.MessageID != 0 {
		if messageLink := strings.TrimSpace(utils.BuildMessageLink(row.ChatID, row.MessageID)); messageLink != "" {
			return messageLink
		}
	}
	return ""
}

func formatLedgerCardDepositFormulaAligned(amount, rate float64, currency string, feeFactor, converted float64, formulaWidth int) string {
	prefix, result := formatLedgerCardDepositFormulaParts(amount, rate, currency, feeFactor, converted)
	formula := prefix + " " + result
	if padding := formulaWidth - ledgerCardDepositFormulaWidth(prefix, result); padding > 0 {
		formula += strings.Repeat(" ", padding)
	}
	return formula
}

func formatLedgerCardDepositFormulaMarkdownV2(amount, rate float64, currency string, feeFactor, converted float64, trackLink string) string {
	prefix, result := formatLedgerCardDepositFormulaParts(amount, rate, currency, feeFactor, converted)
	amountText := utils.FormatAmount(amount)
	suffix := strings.TrimPrefix(prefix, amountText)
	formula := formatLedgerCardMarkdownLink(amountText, trackLink) + escapeTelegramMarkdownV2(suffix) + " " + mdCode(result) + " "
	return formula
}

func formatLedgerCardDepositFormulaParts(amount, rate float64, currency string, feeFactor, converted float64) (string, string) {
	result := formatLedgerCardDisplayU(converted) + "U"
	if isLedgerCardUSDT(currency) {
		return fmt.Sprintf("%s * %s =", utils.FormatAmount(amount), utils.FormatAmount(feeFactor)), result
	}
	return fmt.Sprintf("%s * %s / %s =", utils.FormatAmount(amount), utils.FormatAmount(feeFactor), utils.FormatAmount(rate)), result
}

func measureLedgerCardDepositFormulaWidth(rows []db.LedgerEntry, fallbackRate, fallbackFeeRate float64) int {
	maxWidth := 0
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(row.EntryType), "deposit") {
			continue
		}
		rate := normalizeCardRateWithFallback(row.Rate, fallbackRate)
		amount := normalizeLedgerCardAmount(row)
		converted, feeFactor := computeLedgerCardDepositConvertedAndFactor(row, amount, rate, fallbackFeeRate)
		prefix, result := formatLedgerCardDepositFormulaParts(amount, rate, row.Currency, feeFactor, roundLedgerCardDisplayU(converted))
		if width := ledgerCardDepositFormulaWidth(prefix, result); width > maxWidth {
			maxWidth = width
		}
	}
	return maxWidth
}

func ledgerCardDepositFormulaWidth(prefix, result string) int {
	return len(prefix) + 1 + len(result)
}

func formatLedgerCardPayoutColumnTexts(amount float64, currency string, rate, payoutConvertedU float64) (string, string) {
	amountText := utils.FormatAmount(amount)
	displayCurrency := normalizeLedgerCardDisplayCurrency(currency)
	if displayCurrency != "" && !isLedgerCardUSDT(currency) {
		amountText += " " + displayCurrency
	}

	convertedText := fmt.Sprintf("%s U", formatLedgerCardDisplayU(payoutConvertedU))
	return amountText, convertedText
}

func formatLedgerCardPayoutAmountMarkdownWithLink(paddedAmountText, trackLink string) string {
	amountText := strings.TrimLeft(paddedAmountText, " ")
	leadingSpaces := strings.TrimSuffix(paddedAmountText, amountText)
	return escapeTelegramMarkdownV2(leadingSpaces) + formatLedgerCardMarkdownLink(amountText, trackLink)
}

func measureLedgerCardPayoutColumnWidths(rows []db.LedgerEntry, fallbackRate float64) (int, int) {
	amountWidth := 0
	convertedWidth := 0
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(row.EntryType), "payout") {
			continue
		}
		rate := normalizeCardRateWithFallback(row.Rate, fallbackRate)
		amount := normalizeLedgerCardAmount(row)
		payoutConvertedU := roundLedgerCardDisplayU(convertLedgerCardAmountToU(amount, row.Currency, rate))
		amountText, convertedText := formatLedgerCardPayoutColumnTexts(amount, row.Currency, rate, payoutConvertedU)
		if len(amountText) > amountWidth {
			amountWidth = len(amountText)
		}
		if len(convertedText) > convertedWidth {
			convertedWidth = len(convertedText)
		}
	}
	return amountWidth, convertedWidth
}

func roundLedgerCardDisplayU(value float64) float64 {
	rounded, err := strconv.ParseFloat(fmt.Sprintf("%.2f", value), 64)
	if err != nil {
		return value
	}
	return rounded
}

func formatLedgerCardDisplayU(value float64) string {
	return fmt.Sprintf("%.2f", roundLedgerCardDisplayU(value))
}

func formatLedgerCardMarkdownLink(label, trackLink string) string {
	if label == "" {
		return ""
	}
	track := strings.TrimSpace(trackLink)
	if track == "" || isLedgerCardUserProfileLink(track) {
		return escapeTelegramMarkdownV2(label)
	}
	return "[" + escapeTelegramMarkdownV2(label) + "](" + escapeLedgerCardMarkdownLinkURL(track) + ")"
}

func isLedgerCardUserProfileLink(track string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(track)), "tg://user?id=")
}

func isLedgerCardInaccessibleMessageLink(track string, chatID int64) bool {
	normalized := strings.ToLower(strings.TrimSpace(track))
	if !strings.HasPrefix(normalized, "https://t.me/c/") {
		return false
	}
	if chatID == 0 {
		return false
	}
	return !isLedgerCardSupergroupChat(chatID)
}

func isLedgerCardSupergroupChat(chatID int64) bool {
	return strings.HasPrefix(strconv.FormatInt(chatID, 10), "-100")
}

func escapeLedgerCardMarkdownLinkURL(raw string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		`)`, `\)`,
	)
	return replacer.Replace(raw)
}

func formatLedgerCardActorText(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	return "  |  " + trimmed
}

func formatLedgerCardActorMarkdown(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	return "  \\|  " + escapeTelegramMarkdownV2(trimmed)
}

func formatLedgerCardActorMarkdownWithLink(name, trackLink string) string {
	return formatLedgerCardActorMarkdownWithLinkPrefix(name, trackLink, "  \\|  ")
}

func formatLedgerCardDepositActorMarkdownWithLink(name, trackLink string) string {
	return formatLedgerCardActorMarkdownWithLinkPrefix(name, trackLink, " \\|  ")
}

func formatLedgerCardActorMarkdownWithLinkPrefix(name, trackLink, separator string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	return separator + formatLedgerCardMarkdownLink(trimmed, trackLink)
}

func formatLedgerCardClock(raw string, loc *time.Location) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "--:--:--"
	}
	layoutsWithUTC := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layoutsWithUTC {
		if t, err := time.ParseInLocation(layout, trimmed, time.UTC); err == nil {
			return t.In(loc).Format("15:04:05")
		}
	}
	if len(trimmed) >= 19 {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", trimmed[:19], time.UTC); err == nil {
			return t.In(loc).Format("15:04:05")
		}
	}
	return "--:--:--"
}
