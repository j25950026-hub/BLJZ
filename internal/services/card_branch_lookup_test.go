package services

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLookupBranchCodeForCardReturnsStructuredResult(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Get("card_no") != "622305453434432224" {
				t.Fatalf("unexpected card_no: %s", req.URL.Query().Get("card_no"))
			}
			return branchJSONResponse(`{"data":{"cnaps_code":"102100099996","bank_name":"中国工商银行","branch_name":"中国工商银行深圳南山支行","province":"广东省","city":"深圳"}}`), nil
		}),
		Timeout: 2 * time.Second,
	}
	svc := NewCardServiceWithClients(client, client, BranchAPIConfig{URL: "https://branch.example.test/query"})

	info, serr := svc.QueryCardWithStatus("622305453434432224")
	if serr != nil {
		t.Fatalf("QueryCardWithStatus failed: %v", serr)
	}
	branch, err := svc.LookupBranchCodeForCard(info)
	if err != nil {
		t.Fatalf("LookupBranchCodeForCard failed: %v", err)
	}
	if branch == nil {
		t.Fatal("expected branch result")
	}
	if branch.CNAPSCode != "102100099996" || branch.BranchName != "中国工商银行深圳南山支行" {
		t.Fatalf("unexpected branch result: %+v", branch)
	}
	if branch.Province != "广东省" || branch.City != "深圳" {
		t.Fatalf("unexpected branch location: %+v", branch)
	}
}

func TestLookupBranchCodeForCardRejectsMissingExactCNAPS(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return branchJSONResponse(`{"data":{"bank_name":"中国工商银行","branch_name":"中国工商银行深圳南山支行"}}`), nil
		}),
		Timeout: 2 * time.Second,
	}
	svc := NewCardServiceWithClients(client, client, BranchAPIConfig{URL: "https://branch.example.test/query"})
	info, serr := svc.QueryCardWithStatus("622305453434432224")
	if serr != nil {
		t.Fatalf("QueryCardWithStatus failed: %v", serr)
	}

	branch, err := svc.LookupBranchCodeForCard(info)
	if err == nil || branch != nil {
		t.Fatalf("expected missing CNAPS error, branch=%+v err=%v", branch, err)
	}
	if !strings.Contains(err.Error(), "支行联行号") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func branchJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestLookupBranchCodeForCardReportsUnconfiguredProvider(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unexpected request: %s", req.URL.String())
	})}
	svc := NewCardServiceWithClients(client, client, BranchAPIConfig{})
	info := CardInfo{CardNo: "622305453434432224", BankName: "中国工商银行"}

	branch, err := svc.LookupBranchCodeForCard(info)
	if branch != nil || err != ErrBranchLookupNotConfigured {
		t.Fatalf("expected ErrBranchLookupNotConfigured, branch=%+v err=%v", branch, err)
	}
}
