package bot

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"

	"go-bot/internal/utils"
)

const feeRateQuoteCompareDefaultAmount = 100000.0

type feeRateQuoteCompareOption struct {
	Raw          string
	FeeRate      float64
	ExchangeRate float64
	Result       float64
}

func renderFeeRateQuoteCompareReply(raw string) (string, bool) {
	spec, handled, errText := parseFeeRateQuoteCompareCommand(raw)
	if !handled {
		return "", false
	}
	if errText != "" {
		return errText, true
	}
	return formatFeeRateQuoteCompareReply(spec), true
}

func parseFeeRateQuoteCompareCommand(raw string) ([]feeRateQuoteCompareOption, bool, string) {
	normalized := normalizeFeeRateQuoteCompareInput(raw)
	if normalized == "" {
		return nil, false, ""
	}
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return nil, false, ""
	}
	if !isFeeRateQuoteCompareCommand(fields[0]) {
		return nil, false, ""
	}
	if len(fields) < 3 {
		return nil, true, feeRateQuoteCompareUsageText("至少发送 2 组方案才可以比较。")
	}
	if len(fields)-1 > 10 {
		return nil, true, "最多支持 10 组方案，请减少后重试。"
	}

	options := make([]feeRateQuoteCompareOption, 0, len(fields)-1)
	for _, token := range fields[1:] {
		option, errText := parseFeeRateQuoteCompareOption(token)
		if errText != "" {
			return nil, true, errText
		}
		options = append(options, option)
	}
	if len(options) < 2 {
		return nil, true, feeRateQuoteCompareUsageText("至少发送 2 组方案才可以比较。")
	}
	return options, true, ""
}

func feeRateQuoteCompareUsageText(prefix string) string {
	lines := make([]string, 0, 5)
	if strings.TrimSpace(prefix) != "" {
		lines = append(lines, strings.TrimSpace(prefix))
	}
	lines = append(lines,
		"格式：比价 费率-汇率 费率-汇率",
		"示例：<code>比价 11-205 10-210 10-220</code>",
		"默认金额：<code>100000</code>",
		"公式：<code>金额*(1-费率/100)/汇率</code>",
	)
	return strings.Join(lines, "\n")
}

func formatFeeRateQuoteCompareReply(options []feeRateQuoteCompareOption) string {
	sorted := make([]feeRateQuoteCompareOption, len(options))
	copy(sorted, options)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Result == sorted[j].Result {
			return sorted[i].Raw < sorted[j].Raw
		}
		return sorted[i].Result > sorted[j].Result
	})

	lines := []string{
		"比价结果（默认金额：<code>100000</code>，越大越好）",
		"公式：<code>金额*(1-费率/100)/汇率</code>",
	}
	best := sorted[0]
	for i, item := range sorted {
		line := fmt.Sprintf("%d. <code>%s</code> = <code>%s</code>", i+1, html.EscapeString(item.Raw), html.EscapeString(utils.FormatAmount(item.Result)))
		if i == 0 {
			line += " 最高"
		} else {
			diff := (best.Result - item.Result) / best.Result * 100
			line += fmt.Sprintf("，较最高低 <code>%s%%</code>", html.EscapeString(utils.FormatAmount(diff)))
		}
		lines = append(lines, line)
	}

	if len(sorted) > 1 {
		lines = append(lines, "", "相互增率：")
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				higher := sorted[i]
				lower := sorted[j]
				diff := (higher.Result/lower.Result - 1) * 100
				lines = append(lines, fmt.Sprintf("<code>%s</code> 比 <code>%s</code> 高 <code>%s%%</code>", html.EscapeString(higher.Raw), html.EscapeString(lower.Raw), html.EscapeString(utils.FormatAmount(diff))))
			}
		}
	}

	return strings.Join(lines, "\n")
}

func normalizeFeeRateQuoteCompareInput(raw string) string {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		",", " ",
		"，", " ",
		"、", " ",
		";", " ",
		"；", " ",
		"\t", " ",
		"\r", " ",
	)
	return replacer.Replace(normalized)
}

func isFeeRateQuoteCompareCommand(token string) bool {
	switch strings.TrimSpace(token) {
	case "比价", "金额最大", "方案比较":
		return true
	default:
		return false
	}
}

func parseFeeRateQuoteCompareOption(token string) (feeRateQuoteCompareOption, string) {
	trimmed := strings.TrimSpace(token)
	trimmed = strings.Trim(trimmed, "[](){}<>")
	if trimmed == "" {
		return feeRateQuoteCompareOption{}, "格式：费率-汇率，例如：11-205"
	}
	trimmed = strings.TrimSuffix(trimmed, "%")
	trimmed = strings.TrimSuffix(trimmed, "％")
	if strings.Count(trimmed, "-") != 1 {
		return feeRateQuoteCompareOption{}, "格式：费率-汇率，例如：11-205"
	}
	parts := strings.SplitN(trimmed, "-", 2)
	feeRate, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || feeRate < 0 || feeRate >= 100 {
		return feeRateQuoteCompareOption{}, "费率必须是 0 到 100 之间的数字。"
	}
	exchangeRate, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil || exchangeRate <= 0 {
		return feeRateQuoteCompareOption{}, "汇率必须是大于 0 的数字。"
	}
	result := feeRateQuoteCompareDefaultAmount * (1 - feeRate/100) / exchangeRate
	return feeRateQuoteCompareOption{
		Raw:          strings.TrimSpace(trimmed),
		FeeRate:      feeRate,
		ExchangeRate: exchangeRate,
		Result:       result,
	}, ""
}

func (a *App) tryHandleFeeRateQuoteCompare(chatID int64, replyTo int, text string) (bool, error) {
	reply, handled := renderFeeRateQuoteCompareReply(text)
	if !handled {
		return false, nil
	}
	return true, a.replyHTML(chatID, replyTo, reply)
}
