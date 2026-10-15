package services

import "testing"

func TestBankCustomerServicePhoneUsesBankCode(t *testing.T) {
	tests := map[string]string{
		"ABC":  "95599",
		"ICBC": "95588",
		"BOC":  "95566",
		"CCB":  "95533",
	}
	for bankCode, expected := range tests {
		if actual := BankCustomerServicePhone(bankCode, ""); actual != expected {
			t.Fatalf("BankCustomerServicePhone(%q, empty) = %q, want %q", bankCode, actual, expected)
		}
	}
}

func TestBankCustomerServicePhoneFallsBackToBankName(t *testing.T) {
	if actual := BankCustomerServicePhone("", "中国农业银行"); actual != "95599" {
		t.Fatalf("bank name fallback = %q, want 95599", actual)
	}
}

func TestBankCustomerServicePhoneDoesNotInventUnknownNumber(t *testing.T) {
	if actual := BankCustomerServicePhone("UNKNOWN", "未知银行"); actual != "" {
		t.Fatalf("unknown bank should have no phone, got %q", actual)
	}
}
