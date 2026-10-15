package services

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatBankCardTXT(t *testing.T) {
	result := BankCardTXTResult{
		CardNo:     "6230522370002681578",
		BankName:   "中国农业银行",
		CardType:   "借记卡",
		CardName:   "银联借记卡",
		CreditType: "借记卡",
		BankCode:   "ABC",
		CNAPSCode:  "103501005025",
		BranchName: "中国农业银行股份有限公司焦作人民路支行",
		BankPhone:  "95599",
		Province:   "河南省",
		City:       "焦作",
	}

	actual, err := FormatBankCardTXT(result)
	if err != nil {
		t.Fatalf("FormatBankCardTXT returned error: %v", err)
	}

	expected := strings.Join([]string{
		"银行卡号: 6230522370002681578",
		"银行名称: 中国农业银行",
		"卡类型: 借记卡",
		"卡名称: 银联借记卡",
		"借识卡/贷记卡识别码: 借记卡",
		"银行名称编码: ABC",
		"联行号: 103501005025",
		"开户行名称: 中国农业银行股份有限公司焦作人民路支行",
		"银行客服电话: 95599",
		"省份: 河南省",
		"城市: 焦作",
	}, "\n")
	if actual != expected {
		t.Fatalf("unexpected bank.txt output:\n%s", actual)
	}
}

func TestFormatBankCardTXTRejectsMissingExactBranchFields(t *testing.T) {
	result := BankCardTXTResult{
		CardNo:     "6230522370002681578",
		BankName:   "中国农业银行",
		CardType:   "借记卡",
		CardName:   "银联借记卡",
		CreditType: "借记卡",
		BankCode:   "ABC",
		BankPhone:  "95599",
		Province:   "河南省",
		City:       "焦作",
	}

	_, err := FormatBankCardTXT(result)
	if err == nil {
		t.Fatal("expected missing exact branch fields error")
	}
	var missingErr *MissingBankCardFieldsError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected MissingBankCardFieldsError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "联行号") || !strings.Contains(err.Error(), "开户行名称") {
		t.Fatalf("missing fields should be listed: %v", err)
	}
}

func TestFormatBankCardNineFields(t *testing.T) {
	result := BankCardTXTResult{
		CardNo:     "6230522370002681578",
		BankName:   "中国农业银行",
		CardType:   "借记卡",
		CardName:   "银联借记卡",
		CreditType: "借记卡",
		BankCode:   "ABC",
		BankPhone:  "95599",
		Province:   "河南省",
		City:       "焦作",
	}

	actual, err := FormatBankCardNineFields(result)
	if err != nil {
		t.Fatalf("FormatBankCardNineFields returned error: %v", err)
	}

	expected := strings.Join([]string{
		"银行卡号: 6230522370002681578",
		"银行名称: 中国农业银行",
		"卡类型: 借记卡",
		"卡名称: 银联借记卡",
		"借识卡/贷记卡识别码: 借记卡",
		"银行名称编码: ABC",
		"银行客服电话: 95599",
		"省份: 河南省",
		"城市: 焦作",
	}, "\n")
	if actual != expected {
		t.Fatalf("unexpected nine-field bank.txt output:\n%s", actual)
	}
}

func TestFormatBankCardNineFieldsReportsOnlyBaseFieldGaps(t *testing.T) {
	result := BankCardTXTResult{
		CardNo:     "6230522370002681578",
		BankName:   "中国农业银行",
		CardType:   "借记卡",
		CardName:   "银联借记卡",
		CreditType: "借记卡",
		BankCode:   "ABC",
	}

	_, err := FormatBankCardNineFields(result)
	if err == nil {
		t.Fatal("expected missing nine-field error")
	}
	if strings.Contains(err.Error(), "联行号") || strings.Contains(err.Error(), "开户行名称") {
		t.Fatalf("nine-field error must not require exact branch fields: %v", err)
	}
	for _, field := range []string{"银行客服电话", "省份", "城市"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("nine-field error should list %s: %v", field, err)
		}
	}
}

func TestNormalizeCardCreditType(t *testing.T) {
	tests := map[string]string{
		"借记卡":    "借记卡",
		"储蓄卡":    "借记卡",
		"debit":  "借记卡",
		"贷记卡":    "贷记卡",
		"信用卡":    "贷记卡",
		"credit": "贷记卡",
	}
	for input, expected := range tests {
		if actual := NormalizeCardCreditType(input); actual != expected {
			t.Fatalf("NormalizeCardCreditType(%q) = %q, want %q", input, actual, expected)
		}
	}
}
