package services

import (
	"fmt"
	"strings"
)

// BankCardTXTResult contains the real values required by the bank.txt output format.
type BankCardTXTResult struct {
	CardNo     string
	BankName   string
	CardType   string
	CardName   string
	CreditType string
	BankCode   string
	CNAPSCode  string
	BranchName string
	BankPhone  string
	Province   string
	City       string
}

// MissingBankCardFieldsError reports fields that cannot be filled with real data.
type MissingBankCardFieldsError struct {
	Fields []string
}

func (e *MissingBankCardFieldsError) Error() string {
	if e == nil || len(e.Fields) == 0 {
		return "银行卡信息不完整"
	}
	return "银行卡信息不完整，缺少：" + strings.Join(e.Fields, "、")
}

// FormatBankCardTXT renders the fixed field order used by bank.txt.
func FormatBankCardTXT(result BankCardTXTResult) (string, error) {
	return formatBankCardTXT(result, true)
}

// FormatBankCardNineFields renders the fields that can be resolved from a card number without exact branch data.
func FormatBankCardNineFields(result BankCardTXTResult) (string, error) {
	return formatBankCardTXT(result, false)
}

func formatBankCardTXT(result BankCardTXTResult, includeBranch bool) (string, error) {
	fields := []struct {
		label string
		value string
	}{
		{"银行卡号", result.CardNo},
		{"银行名称", result.BankName},
		{"卡类型", result.CardType},
		{"卡名称", result.CardName},
		{"借识卡/贷记卡识别码", result.CreditType},
		{"银行名称编码", result.BankCode},
		{"银行客服电话", result.BankPhone},
		{"省份", result.Province},
		{"城市", result.City},
	}
	if includeBranch {
		fields = append(fields[:6], append([]struct {
			label string
			value string
		}{
			{"联行号", result.CNAPSCode},
			{"开户行名称", result.BranchName},
		}, fields[6:]...)...)
	}

	missing := make([]string, 0)
	lines := make([]string, 0, len(fields))
	for _, field := range fields {
		value := strings.TrimSpace(field.value)
		if value == "" {
			missing = append(missing, field.label)
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			return "", fmt.Errorf("%s包含非法换行符", field.label)
		}
		lines = append(lines, field.label+": "+value)
	}
	if len(missing) > 0 {
		return "", &MissingBankCardFieldsError{Fields: missing}
	}
	return strings.Join(lines, "\n"), nil
}

// NormalizeCardCreditType maps provider-specific card type values to debit/credit labels.
func NormalizeCardCreditType(cardType string) string {
	switch strings.ToLower(strings.TrimSpace(cardType)) {
	case "借记卡", "储蓄卡", "debit", "debit card":
		return "借记卡"
	case "贷记卡", "信用卡", "准贷记卡", "credit", "credit card":
		return "贷记卡"
	default:
		return strings.TrimSpace(cardType)
	}
}
