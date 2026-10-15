package services

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCardRoutingUsesPrimaryFirstWhenConfigured(t *testing.T) {
	var (
		mu           sync.Mutex
		primaryCalls int
		binCalls     int
	)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Host, "api.iinlist.com"):
				mu.Lock()
				primaryCalls++
				mu.Unlock()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"_embedded":{"cards":[{"scheme":{"name":"Mastercard"},"account":{"funding":"debit","country":{"name":"United Kingdom","code":"GB"}},"issuer":{"name":"Primary Bank"}}]}}`,
					)),
					Header: make(http.Header),
				}, nil
			case strings.Contains(req.URL.Host, "lookup.binlist.net"):
				mu.Lock()
				binCalls++
				mu.Unlock()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"scheme":"visa","type":"debit","bank":{"name":"Binlist Bank"},"country":{"name":"US"}}`)),
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
	}

	svc := NewCardServiceWithPolicyAndRouting(
		client,
		client,
		BranchAPIConfig{},
		0,
		10*time.Millisecond,
		CardRoutingConfig{
			PrimaryEndpoint:  "https://api.iinlist.com/cards",
			PrimaryAPIKey:    "demo-key",
			PrimaryAPIHeader: "X-API-Key",
			DisableCache:     true,
		},
	)

	info, serr := svc.QueryCardWithStatus("5555555555554444")
	if serr != nil {
		t.Fatalf("expected query success, got err=%+v", serr)
	}
	if info.Source != "primary" {
		t.Fatalf("expected source=primary, got=%s", info.Source)
	}
	if info.Fallback {
		t.Fatalf("expected fallback=false when primary source hits")
	}
	if info.BankName != "Primary Bank" {
		t.Fatalf("expected bank from primary source, got=%s", info.BankName)
	}
	if info.Brand != "mastercard" || info.CardType != "debit" || info.Country == "" {
		t.Fatalf("unexpected primary parsed fields: %+v", info)
	}

	mu.Lock()
	defer mu.Unlock()
	if primaryCalls == 0 {
		t.Fatalf("expected primary provider to be called")
	}
	if binCalls != 0 {
		t.Fatalf("binlist should not be called when primary already has enough data, got=%d", binCalls)
	}
}

func TestQueryCardEnrichesDomesticBankcardWithAPICNDetails(t *testing.T) {
	const cardNo = "622305453434432224"

	var (
		mu           sync.Mutex
		requestedNum string
		tokenHeader  string
		apiCalls     int
		binCalls     int
	)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Host, "tenant.api.cn"):
				mu.Lock()
				apiCalls++
				requestedNum = req.URL.Query().Get("num")
				tokenHeader = req.Header.Get("token")
				mu.Unlock()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"status":true,"code":10200,"msg":"查询成功","data":{"num":"622305453434432224","bankname":"江苏省农村信用社联合社","province":"江苏省","city":"淮安","code":"320800","cardname":"圆鼎借记IC卡 公","cardtype":"借记卡","bin":"622305","banken":"ICBC","banktel":"96008","bankurl":"http://www.jsnx.net/cn/","luhn":true}}`,
					)),
					Header: make(http.Header),
				}, nil
			case strings.Contains(req.URL.Host, "lookup.binlist.net"):
				mu.Lock()
				binCalls++
				mu.Unlock()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"scheme":"unionpay","type":"debit","bank":{"name":"Binlist Bank"},"country":{"name":"CN"}}`)),
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
	}

	svc := NewCardServiceWithPolicyAndRouting(
		client,
		client,
		BranchAPIConfig{},
		0,
		10*time.Millisecond,
		CardRoutingConfig{
			PrimaryEndpoint:  "https://tenant.api.cn/yinhangka/",
			PrimaryAPIKey:    "secret-token",
			PrimaryAPIHeader: "",
			DisableCache:     true,
		},
	)

	info, serr := svc.QueryCardWithStatus(cardNo)
	if serr != nil {
		t.Fatalf("expected domestic card enriched by API.CN, got err=%+v", serr)
	}
	if info.BankName != "江苏省农村信用社联合社" {
		t.Fatalf("API.CN bank name should override local bank name, got=%s", info.BankName)
	}
	if info.Province != "江苏省" || info.City != "淮安" || info.CardName != "圆鼎借记IC卡 公" {
		t.Fatalf("API.CN detail fields not populated: %+v", info)
	}
	if info.CardType != "借记卡" || info.BankPhone != "96008" || info.BankURL != "http://www.jsnx.net/cn/" {
		t.Fatalf("API.CN card type/contact fields not populated: %+v", info)
	}
	if info.BankCode != "ICBC" || info.BIN != "622305" || info.RegionCode != "320800" {
		t.Fatalf("API.CN code fields not populated: %+v", info)
	}
	if !strings.Contains(CardSourceLabel(info), "本地国内BIN库") || !strings.Contains(CardSourceLabel(info), "API.CN") {
		t.Fatalf("source should mention local base and API.CN detail, got=%s", CardSourceLabel(info))
	}

	mu.Lock()
	defer mu.Unlock()
	if apiCalls != 1 {
		t.Fatalf("expected one API.CN call for domestic enrichment, got=%d", apiCalls)
	}
	if requestedNum != cardNo {
		t.Fatalf("API.CN request should use full num, got=%q", requestedNum)
	}
	if tokenHeader != "secret-token" {
		t.Fatalf("API.CN request should send token header, got=%q", tokenHeader)
	}
	if binCalls != 0 {
		t.Fatalf("binlist should not be called when API.CN completes detail, got=%d", binCalls)
	}
}

func TestQueryCardEnrichesDomesticBankcardWithHaoshudiWebFlow(t *testing.T) {
	const cardNo = "622305453434432224"

	var (
		mu              sync.Mutex
		tokenCalls      int
		queryCalls      int
		areaCalls       int
		tokenReferer    string
		queryReferer    string
		areaReferer     string
		requestedNum    string
		requestedCard   string
		requestedTicket string
	)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/token/create/":
				mu.Lock()
				tokenCalls++
				tokenReferer = req.Header.Get("Referer")
				mu.Unlock()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"status":true,"code":0,"msg":"ok","data":{"ticket":"web-ticket","expire_time":"1783791951"}}`)),
					Header:     make(http.Header),
				}, nil
			case "/api/bank/query/":
				mu.Lock()
				queryCalls++
				queryReferer = req.Header.Get("Referer")
				requestedNum = req.URL.Query().Get("num")
				requestedTicket = req.URL.Query().Get("ticket")
				mu.Unlock()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"status":true,"code":10200,"msg":"查询成功","data":{"num":"622305453434432224","bankname":"工商银行","bin":"622305","cardtype":"贷记卡","type":"借记卡","cardname":"E时代卡","length":"18","luhn":false}}`,
					)),
					Header: make(http.Header),
				}, nil
			case "/api/bank/area/":
				mu.Lock()
				areaCalls++
				areaReferer = req.Header.Get("Referer")
				requestedCard = req.URL.Query().Get("card")
				mu.Unlock()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"status":true,"code":10200,"msg":"查询成功","data":{"address":"江苏省 淮安"}}`)),
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
	}

	svc := NewCardServiceWithPolicyAndRouting(
		client,
		client,
		BranchAPIConfig{},
		0,
		10*time.Millisecond,
		CardRoutingConfig{
			PrimaryEndpoint: "https://www.haoshudi.com/yinhangka/",
			DisableCache:    true,
		},
	)

	info, serr := svc.QueryCardWithStatus(cardNo)
	if serr != nil {
		t.Fatalf("expected domestic card enriched by haoshudi web flow, got err=%+v", serr)
	}
	if info.BankName != "工商银行" || info.CardName != "E时代卡" || info.CardType != "借记卡" {
		t.Fatalf("haoshudi bank query fields not populated: %+v", info)
	}
	if info.BIN != "622305" || info.CardLength != "18" {
		t.Fatalf("haoshudi bin/length fields not populated: %+v", info)
	}
	if info.Address != "江苏省 淮安" || CardLocationLabel(info) != "江苏省 淮安" {
		t.Fatalf("haoshudi area field not populated: %+v", info)
	}
	if strings.TrimSpace(info.BankPhone) != "" || strings.TrimSpace(info.BankURL) != "" {
		t.Fatalf("haoshudi free web flow should not fabricate phone or website: %+v", info)
	}
	if CardValidationStatus(info) != "通过（国内BIN/长度）" {
		t.Fatalf("haoshudi luhn should not override domestic validation, got=%s", CardValidationStatus(info))
	}
	if !strings.Contains(CardSourceLabel(info), "号属地网页查询") {
		t.Fatalf("source should mention haoshudi web query, got=%s", CardSourceLabel(info))
	}

	mu.Lock()
	defer mu.Unlock()
	if tokenCalls != 1 || queryCalls != 1 || areaCalls != 1 {
		t.Fatalf("expected token/query/area once, got token=%d query=%d area=%d", tokenCalls, queryCalls, areaCalls)
	}
	for name, referer := range map[string]string{"token": tokenReferer, "query": queryReferer, "area": areaReferer} {
		if referer != "https://www.haoshudi.com/yinhangka/" {
			t.Fatalf("%s request referer mismatch: %q", name, referer)
		}
	}
	if requestedNum != cardNo || requestedCard != cardNo || requestedTicket != "web-ticket" {
		t.Fatalf("haoshudi request parameters mismatch: num=%q card=%q ticket=%q", requestedNum, requestedCard, requestedTicket)
	}
}

func TestDomesticAPICNDetailCacheKeepsDomesticValidation(t *testing.T) {
	const cardNo = "622305453434432224"

	var (
		mu       sync.Mutex
		apiCalls int
	)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Host, "tenant.api.cn") {
				mu.Lock()
				apiCalls++
				mu.Unlock()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"status":true,"code":10200,"msg":"查询成功","data":{"num":"622305453434432224","bankname":"中国工商银行","province":"江苏省","city":"淮安","cardname":"圆鼎借记IC卡 公","cardtype":"借记卡","banktel":"96008","bankurl":"http://www.jsnx.net/cn/","luhn":false}}`,
					)),
					Header: make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	svc := NewCardServiceWithPolicyAndRouting(
		client,
		client,
		BranchAPIConfig{},
		0,
		10*time.Millisecond,
		CardRoutingConfig{
			PrimaryEndpoint: "https://tenant.api.cn/yinhangka/",
			PrimaryAPIKey:   "secret-token",
			CacheTTL:        time.Minute,
			CacheMaxEntries: 16,
		},
	)

	first, serr := svc.QueryCardWithStatus(cardNo)
	if serr != nil {
		t.Fatalf("first query failed: %+v", serr)
	}
	second, serr := svc.QueryCardWithStatus(cardNo)
	if serr != nil {
		t.Fatalf("second query failed: %+v", serr)
	}
	if CardValidationStatus(first) != "通过（国内BIN/长度）" || CardValidationStatus(second) != "通过（国内BIN/长度）" {
		t.Fatalf("cached domestic detail should keep domestic validation: first=%s second=%s", CardValidationStatus(first), CardValidationStatus(second))
	}
	if second.CardName != "圆鼎借记IC卡 公" || second.BankPhone != "96008" {
		t.Fatalf("cached detail fields missing: %+v", second)
	}

	mu.Lock()
	defer mu.Unlock()
	if apiCalls != 1 {
		t.Fatalf("expected API.CN result to be cached, got calls=%d", apiCalls)
	}
}

func TestCardRoutingPrimaryTimeoutFallbackToBinlist(t *testing.T) {
	var (
		mu           sync.Mutex
		primaryCalls int
		binCalls     int
	)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Host, "api.iinlist.com"):
				mu.Lock()
				primaryCalls++
				mu.Unlock()
				<-req.Context().Done()
				return nil, req.Context().Err()
			case strings.Contains(req.URL.Host, "lookup.binlist.net"):
				mu.Lock()
				binCalls++
				mu.Unlock()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"scheme":"maestro","type":"debit","bank":{"name":"Fallback Bank"},"country":{"name":"CN"}}`)),
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
	}

	svc := NewCardServiceWithPolicyAndRouting(
		client,
		client,
		BranchAPIConfig{},
		0,
		10*time.Millisecond,
		CardRoutingConfig{
			PrimaryEndpoint:        "https://api.iinlist.com/cards",
			PrimaryAPIKey:          "demo-key",
			PrimaryAPIHeader:       "X-API-Key",
			DefaultProviderTimeout: 20 * time.Millisecond,
			ProviderTimeouts: map[string]time.Duration{
				"primary": 15 * time.Millisecond,
				"binlist": 500 * time.Millisecond,
			},
			DisableCache: true,
		},
	)

	info, serr := svc.QueryCardWithStatus("56540200000233")
	if serr != nil {
		t.Fatalf("expected timeout fallback success, got err=%+v", serr)
	}
	if info.Source != "binlist" || !info.Fallback {
		t.Fatalf("expected source=binlist fallback=true, got source=%s fallback=%v", info.Source, info.Fallback)
	}

	mu.Lock()
	defer mu.Unlock()
	if primaryCalls == 0 || binCalls == 0 {
		t.Fatalf("expected both providers called, got primary=%d binlist=%d", primaryCalls, binCalls)
	}
}

func TestCardRoutingCacheAvoidsRepeatedUpstreamCalls(t *testing.T) {
	var (
		mu      sync.Mutex
		callNum int
	)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			mu.Lock()
			callNum++
			mu.Unlock()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"scheme":"visa","type":"debit","bank":{"name":"Cache Bank"},"country":{"name":"US"}}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	svc := NewCardServiceWithPolicyAndRouting(
		client,
		client,
		BranchAPIConfig{},
		0,
		10*time.Millisecond,
		CardRoutingConfig{
			CacheTTL:        2 * time.Minute,
			CacheMaxEntries: 100,
			DisableCache:    false,
		},
	)

	_, serr := svc.QueryCardWithStatus("4539578763621486")
	if serr != nil {
		t.Fatalf("first query failed: %+v", serr)
	}
	second, serr := svc.QueryCardWithStatus("4539578763999999")
	if serr != nil {
		t.Fatalf("second query failed: %+v", serr)
	}
	if second.CardNo != "4539578763999999" {
		t.Fatalf("cached response should keep current card no, got=%s", second.CardNo)
	}

	mu.Lock()
	defer mu.Unlock()
	if callNum != 1 {
		t.Fatalf("expected only 1 upstream call due BIN cache, got=%d", callNum)
	}
}

func TestCardRoutingHeuristicBeforeLocalMapFallback(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Host, "api.iinlist.com"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"_embedded":{"cards":[{"bank":{"code":"ICBC"}}]}}`,
					)),
					Header: make(http.Header),
				}, nil
			case strings.Contains(req.URL.Host, "lookup.binlist.net"):
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
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
	}

	svc := NewCardServiceWithPolicyAndRouting(
		client,
		client,
		BranchAPIConfig{},
		0,
		10*time.Millisecond,
		CardRoutingConfig{
			PrimaryEndpoint:  "https://api.iinlist.com/cards",
			PrimaryAPIKey:    "demo-key",
			PrimaryAPIHeader: "X-API-Key",
			DisableCache:     true,
		},
	)

	info, serr := svc.QueryCardWithStatus("56540200000233")
	if serr != nil {
		t.Fatalf("expected query success, got err=%+v", serr)
	}
	if info.BankCode != "ICBC" {
		t.Fatalf("expected bank code from upstream, got=%s", info.BankCode)
	}
	if info.BankName != "中国工商银行" {
		t.Fatalf("expected local map bank name fallback, got=%s", info.BankName)
	}
	if info.Brand != "maestro" {
		t.Fatalf("expected heuristic brand fallback, got=%s", info.Brand)
	}
	if info.Source != "primary" {
		t.Fatalf("expected source kept as upstream primary, got=%s", info.Source)
	}
	if !info.Fallback {
		t.Fatalf("expected fallback=true after heuristic")
	}
}
