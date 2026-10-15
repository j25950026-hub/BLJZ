package services

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestQueryCardUsesDomesticBankcardDataBeforeUpstream(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("unexpected upstream request to %s", req.URL.String())
		}),
		Timeout: 2 * time.Second,
	}
	svc := NewCardServiceWithPolicyAndRouting(
		client,
		client,
		BranchAPIConfig{},
		0,
		10*time.Millisecond,
		CardRoutingConfig{DisableCache: true},
	)

	info, serr := svc.QueryCardWithStatus("622305453434432224")
	if serr != nil {
		t.Fatalf("domestic card should resolve from local data, got err=%+v", serr)
	}
	if info.Source != "domestic_bankcard" || info.Fallback {
		t.Fatalf("source mismatch: source=%s fallback=%v", info.Source, info.Fallback)
	}
	if !info.Valid {
		t.Fatalf("domestic BIN + length validation should pass")
	}
	if info.BankCode != "ICBC" || info.BankName != "中国工商银行" {
		t.Fatalf("bank mismatch: code=%s name=%s", info.BankCode, info.BankName)
	}
	if info.Brand != "unionpay" {
		t.Fatalf("brand mismatch: %s", info.Brand)
	}
	if info.CardType != "储蓄卡" {
		t.Fatalf("card type mismatch: %s", info.CardType)
	}
	if info.Country != "中国" {
		t.Fatalf("country mismatch: %s", info.Country)
	}
}

func TestQueryCardDomesticBankcardUsesLengthToDisambiguateDuplicateBIN(t *testing.T) {
	svc := NewCardServiceWithPolicyAndRouting(
		&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("unexpected upstream request to %s", req.URL.String())
		})},
		nil,
		BranchAPIConfig{},
		0,
		10*time.Millisecond,
		CardRoutingConfig{DisableCache: true},
	)

	info, serr := svc.QueryCardWithStatus("6223051234567890")
	if serr != nil {
		t.Fatalf("domestic duplicate BIN card should resolve from local data, got err=%+v", serr)
	}
	if info.BankCode != "NJCB" || info.BankName != "南京银行" {
		t.Fatalf("expected 16-digit 622305 to resolve to NJCB, got code=%s name=%s", info.BankCode, info.BankName)
	}
	if info.CardType != "储蓄卡" {
		t.Fatalf("card type mismatch: %s", info.CardType)
	}
}
