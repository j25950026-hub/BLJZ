package utils

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Knetic/govaluate"
)

var (
	ledgerPattern = regexp.MustCompile(`^(入款|下发)?\s*([+-])?\s*(\d+(?:\.\d+)?)(?:\s*\*\s*(\d+(?:\.\d+)?))?(?:\s*/\s*([+-]?\d+(?:\.\d+)?))?\s*([a-zA-Z]+)?(?:#(.+))?$`)
	ledgerHint    = regexp.MustCompile(`^(?:入款|下发)\s*[+-]*\s*\d+(?:\.\d+)?(?:\s*\*\s*[^#\s]+)?(?:\s*/\s*[^#\s]+)?\s*[a-zA-Z]*(?:#.*)?$|^[+-]+\s*\d+(?:\.\d+)?(?:\s*\*\s*[^#\s]+)?(?:\s*/\s*[^#\s]+)?\s*[a-zA-Z]*(?:#.*)?$`)
	cardPattern   = regexp.MustCompile(`\b\d{12,19}\b`)
	mathPattern   = regexp.MustCompile(`^[0-9\s\+\-\*\/\(\)\.]+$`)
	mathBinaryOp  = regexp.MustCompile(`[0-9\)]\s*[\+\-\*\/]\s*\(?\s*[\+\-]?\s*[0-9]`)
	scheduleRegex = regexp.MustCompile(`^定时群发\s+(\d{4}-\d{2}-\d{2})\s+(\d{2}:\d{2})(?::(\d{2}))?(?:\s+(.+))?$`)
)

type LedgerParsed struct {
	EntryType string
	Amount    float64
	Currency  string
	Note      string
	// RateOverride is a per-entry rate override (e.g. +500/4).
	RateOverride *float64
	// FeeRateOverride is a per-entry fee percentage override (e.g. +500*0.93).
	FeeRateOverride *float64
}

func BeijingNow(timezoneName string) time.Time {
	loc, err := time.LoadLocation(timezoneName)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return time.Now().In(loc)
}

func UTCNowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05")
}

func ParseHMS(value string) (int, int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("时间格式必须为 HH:MM:SS")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, err
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, err
	}
	s, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 || s < 0 || s > 59 {
		return 0, 0, 0, fmt.Errorf("时间范围不合法")
	}
	return h, m, s, nil
}

func GetDayStartISO(dayCutTime, timezoneName string) string {
	now := BeijingNow(timezoneName)
	h, m, s, err := ParseHMS(dayCutTime)
	if err != nil {
		h, m, s = 4, 0, 0
	}
	cut := time.Date(now.Year(), now.Month(), now.Day(), h, m, s, 0, now.Location())
	if now.Before(cut) {
		cut = cut.Add(-24 * time.Hour)
	}
	return cut.UTC().Format("2006-01-02T15:04:05")
}

func FormatAmount(value float64) string {
	if math.Abs(value) >= 1 {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", value), "0"), ".")
}

func ParseLedgerText(text string) (*LedgerParsed, bool) {
	m := ledgerPattern.FindStringSubmatch(normalizeLedgerInput(text))
	if m == nil {
		return nil, false
	}
	prefix := strings.TrimSpace(m[1])
	sign := strings.TrimSpace(m[2])
	amount, err := strconv.ParseFloat(m[3], 64)
	if err != nil || amount <= 0 {
		return nil, false
	}
	feeFactorRaw := strings.TrimSpace(m[4])
	var feeRateOverride *float64
	if feeFactorRaw != "" {
		feeFactor, parseErr := strconv.ParseFloat(feeFactorRaw, 64)
		if parseErr != nil || feeFactor <= 0 {
			return nil, false
		}
		feeRate := (1 - feeFactor) * 100
		feeRateOverride = &feeRate
	}
	overrideRaw := strings.TrimSpace(m[5])
	var rateOverride *float64
	if overrideRaw != "" {
		override, parseErr := strconv.ParseFloat(overrideRaw, 64)
		if parseErr != nil {
			return nil, false
		}
		rateOverride = &override
	}
	unit := strings.ToLower(strings.TrimSpace(m[6]))
	note := strings.TrimSpace(m[7])
	switch unit {
	case "", "u", "usdt":
	default:
		return nil, false
	}
	if prefix == "" && sign == "-" && overrideRaw != "" {
		return nil, false
	}

	entryType := ""
	signedAmount := amount

	switch prefix {
	case "入款":
		entryType = "deposit"
		if sign == "-" {
			signedAmount = -amount
		}
	case "下发":
		entryType = "payout"
		if sign == "-" {
			signedAmount = -amount
		}
	default:
		if sign == "+" {
			entryType = "deposit"
			signedAmount = amount
		} else if sign == "-" {
			entryType = "payout"
			signedAmount = amount
		} else {
			return nil, false
		}
	}
	if feeRateOverride != nil && entryType != "deposit" {
		return nil, false
	}

	currency := ""
	if unit != "" {
		currency = "USDT"
	}
	return &LedgerParsed{
		EntryType:       entryType,
		Amount:          signedAmount,
		Currency:        currency,
		Note:            note,
		RateOverride:    rateOverride,
		FeeRateOverride: feeRateOverride,
	}, true
}

func LooksLikeLedgerText(text string) bool {
	return ledgerHint.MatchString(normalizeLedgerInput(text))
}

func normalizeLedgerInput(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	mapped := strings.Map(func(r rune) rune {
		switch r {
		case '＋', '﹢':
			return '+'
		case '－', '﹣', '−', '–', '—':
			return '-'
		case '＃', '﹟':
			return '#'
		case '　', '\u00A0':
			return ' '
		}
		if r >= '！' && r <= '～' {
			return r - 0xFEE0
		}
		return r
	}, trimmed)
	return strings.TrimSpace(mapped)
}

func ExtractCardNo(text string) string {
	for _, m := range cardPattern.FindAllString(text, -1) {
		if LuhnCheck(m) {
			return m
		}
	}
	return ""
}

func LuhnCheck(cardNo string) bool {
	digits := make([]int, 0, len(cardNo))
	for _, ch := range cardNo {
		if ch >= '0' && ch <= '9' {
			digits = append(digits, int(ch-'0'))
		}
	}
	if len(digits) < 12 {
		return false
	}
	checksum := 0
	parity := len(digits) % 2
	for i, d := range digits {
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		checksum += d
	}
	return checksum%10 == 0
}

func ParseMathExpression(text string) (float64, bool) {
	exprText := strings.TrimSpace(text)
	exprText = strings.NewReplacer("x", "*", "X", "*", "×", "*", "÷", "/").Replace(exprText)
	if exprText == "" || !mathPattern.MatchString(exprText) || !mathBinaryOp.MatchString(exprText) {
		return 0, false
	}
	expr, err := govaluate.NewEvaluableExpression(exprText)
	if err != nil {
		return 0, false
	}
	out, err := expr.Evaluate(nil)
	if err != nil {
		return 0, false
	}
	n, ok := out.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}

func ParseScheduleCommand(text string, loc *time.Location) (time.Time, string, bool) {
	m := scheduleRegex.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return time.Time{}, "", false
	}
	sec := m[3]
	if sec == "" {
		sec = "00"
	}
	raw := fmt.Sprintf("%s %s:%s", m[1], m[2], sec)
	t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, loc)
	if err != nil {
		return time.Time{}, "", false
	}
	return t, strings.TrimSpace(m[4]), true
}

func ParseWorldTimeQuery(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasSuffix(text, "时间") {
		return "", false
	}
	region := strings.TrimSpace(strings.TrimSuffix(text, "时间"))
	if region == "" {
		return "", false
	}
	return region, true
}

func BuildMessageLink(chatID int64, messageID int) string {
	if messageID <= 0 {
		return ""
	}
	cid := strconv.FormatInt(chatID, 10)
	if !strings.HasPrefix(cid, "-100") {
		return ""
	}
	token := strings.TrimPrefix(cid, "-100")
	if token == "" {
		return ""
	}
	return fmt.Sprintf("https://t.me/c/%s/%d", token, messageID)
}

func SplitTags(text string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(text), func(r rune) bool {
		return r == ',' || r == '，' || unicode.IsSpace(r)
	})
	seen := make(map[string]struct{})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func FindFirstInvalidSymbol(text string) (int, rune, bool) {
	for i, ch := range []rune(text) {
		if ch == '\u2028' || ch == '\u2029' || ch == '\u200b' || ch == '\u200e' || ch == '\u200f' || ch == '\ufeff' {
			return i, ch, true
		}
		if ch < 32 && ch != '\n' && ch != '\t' && ch != '\r' {
			return i, ch, true
		}
	}
	return 0, 0, false
}
