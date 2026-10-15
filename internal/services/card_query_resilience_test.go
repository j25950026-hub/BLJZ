package services

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestQueryCardResilience(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Host, "ccdcapi.alipay.com"):
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader(`{"msg":"rate limit"}`)),
					Header:     make(http.Header),
				}, nil
			case strings.Contains(req.URL.Host, "lookup.binlist.net"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"scheme":"unionpay","type":"debit","bank":{"name":"Test Bank"},"country":{"name":"CN"}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			}
		}),
		Timeout: 2 * time.Second,
	}

	svc := &CardService{
		client:       client,
		branchClient: client,
		branchAPI:    BranchAPIConfig{Header: "Authorization"},
	}
	info, serr := svc.QueryCardWithStatus("6222021234567890")
	if serr != nil {
		t.Fatalf("QueryCardWithStatus should succeed via fallback source, got err=%+v", serr)
	}
	if info.Source != "binlist" || !info.Fallback {
		t.Fatalf("fallback source mismatch: got source=%s fallback=%v", info.Source, info.Fallback)
	}
}

func TestQueryCardInvalidInput(t *testing.T) {
	svc := NewCardService(2*time.Second, BranchAPIConfig{})
	_, serr := svc.QueryCardWithStatus("abc")
	if serr == nil || serr.Code != ErrInvalidInput {
		t.Fatalf("QueryCardWithStatus invalid input should return ErrInvalidInput, got=%+v", serr)
	}
}

func TestQueryCardNoData(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Host, "ccdcapi.alipay.com"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"bank":"","cardType":""}`)),
					Header:     make(http.Header),
				}, nil
			case strings.Contains(req.URL.Host, "lookup.binlist.net"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"scheme":"","type":"","bank":{"name":""},"country":{"name":""}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			}
		}),
		Timeout: 2 * time.Second,
	}
	svc := &CardService{
		client:       client,
		branchClient: client,
		branchAPI:    BranchAPIConfig{Header: "Authorization"},
	}
	_, serr := svc.QueryCardWithStatus("4539578763621486")
	if serr == nil || serr.Code != ErrNoData {
		t.Fatalf("QueryCardWithStatus no data should return ErrNoData, got=%+v", serr)
	}
}

func TestQueryCardNonLuhnStillQueriesBin(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Host, "ccdcapi.alipay.com"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"bank":"","cardType":""}`)),
					Header:     make(http.Header),
				}, nil
			case strings.Contains(req.URL.Host, "lookup.binlist.net"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"scheme":"mastercard","type":"debit","bank":{"name":"Test Bank"},"country":{"name":"CN"}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			}
		}),
		Timeout: 2 * time.Second,
	}
	svc := &CardService{
		client:       client,
		branchClient: client,
		branchAPI:    BranchAPIConfig{Header: "Authorization"},
	}

	info, serr := svc.QueryCardWithStatus("56540200000233")
	if serr != nil {
		t.Fatalf("non-luhn card should still query bin and return info, got err=%+v", serr)
	}
	if info.Valid {
		t.Fatalf("expected non-luhn card to be marked invalid")
	}
	if info.Source != "binlist" {
		t.Fatalf("unexpected source for non-luhn bin query: %s", info.Source)
	}
	if info.BankName == "" {
		t.Fatalf("bank name should be populated from binlist fallback")
	}
}

func TestQueryCardNonLuhnUsesLocalHeuristicWhenUpstreamNoData(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Host, "ccdcapi.alipay.com"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"bank":"","cardType":""}`)),
					Header:     make(http.Header),
				}, nil
			case strings.Contains(req.URL.Host, "lookup.binlist.net"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"number":null,"country":{},"bank":{}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			}
		}),
		Timeout: 2 * time.Second,
	}
	svc := &CardService{
		client:       client,
		branchClient: client,
		branchAPI:    BranchAPIConfig{Header: "Authorization"},
	}

	info, serr := svc.QueryCardWithStatus("56540200000233")
	if serr != nil {
		t.Fatalf("non-luhn with no upstream data should fallback to local heuristic, got err=%+v", serr)
	}
	if info.Valid {
		t.Fatalf("expected luhn invalid for heuristic case")
	}
	if info.Brand != "maestro" {
		t.Fatalf("expected heuristic brand=maestro, got=%s", info.Brand)
	}
	if info.Source != "local_heuristic" || !info.Fallback {
		t.Fatalf("heuristic source mismatch: source=%s fallback=%v", info.Source, info.Fallback)
	}
}
