package services

import "strings"

var bankCustomerServicePhones = map[string]string{
	"ABC":  "95599",
	"ICBC": "95588",
	"BOC":  "95566",
	"CCB":  "95533",
	"CMB":  "95555",
	"PSBC": "95580",
	"SPDB": "95528",
}

// BankCustomerServicePhone returns a known bank customer-service number without inventing unknown values.
func BankCustomerServicePhone(bankCode, bankName string) string {
	code := strings.ToUpper(strings.TrimSpace(bankCode))
	if phone := bankCustomerServicePhones[code]; phone != "" {
		return phone
	}

	name := strings.TrimSpace(bankName)
	switch {
	case strings.Contains(name, "农业银行"):
		return bankCustomerServicePhones["ABC"]
	case strings.Contains(name, "工商银行"):
		return bankCustomerServicePhones["ICBC"]
	case name == "中国银行" || name == "中行":
		return bankCustomerServicePhones["BOC"]
	case strings.Contains(name, "建设银行"):
		return bankCustomerServicePhones["CCB"]
	case strings.Contains(name, "招商银行"):
		return bankCustomerServicePhones["CMB"]
	case strings.Contains(name, "邮政储蓄") || strings.Contains(name, "邮储银行"):
		return bankCustomerServicePhones["PSBC"]
	case strings.Contains(name, "浦发银行") || strings.Contains(name, "浦东发展银行"):
		return bankCustomerServicePhones["SPDB"]
	default:
		return ""
	}
}
