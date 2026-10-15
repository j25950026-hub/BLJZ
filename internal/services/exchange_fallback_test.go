package services

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFallbackChainTable(t *testing.T) {
	cases := map[string][]string{
		"okx":     {"okx", "huobi", "binance"},
		"huobi":   {"huobi", "binance", "okx"},
		"binance": {"binance", "okx", "huobi"},
		"":        {"okx", "huobi", "binance"},
	}
	for provider, want := range cases {
		got := fallbackChain(provider)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("fallbackChain mismatch: provider=%q got=%v want=%v", provider, got, want)
		}
	}
}

func TestGetRateFallbackOrder(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			host := req.URL.Host
			mu.Lock()
			calls[host]++
			count := calls[host]
			mu.Unlock()

			switch {
			case strings.Contains(host, "okx.com"):
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"error":"okx down"}`)),
					Header:     make(http.Header),
				}, nil
			case strings.Contains(host, "trygofast.com"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":[{"price":"7.20"},{"price":"7.21"},{"price":"7.22"}]}`)),
					Header:     make(http.Header),
				}, nil
			case strings.Contains(host, "binance.com"):
				_ = count
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":[{"adv":{"price":"7.19"}}]}`)),
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

	svc := NewExchangeServiceWithConfig(ExchangeConfig{
		HTTPClient:   client,
		Timeout:      2 * time.Second,
		RetryMax:     1,
		Backoff:      5 * time.Millisecond,
		MinSample:    3,
		CacheTTL:     0,
		DisableCache: true,
	})

	row, err := svc.GetRate("okx", "all", "CNY")
	if err != nil {
		t.Fatalf("GetRate unexpected error: %v", err)
	}
	if row.Provider != "huobi" {
		t.Fatalf("GetRate fallback provider mismatch: want=huobi got=%s", row.Provider)
	}
	if row.SampleCount < 3 {
		t.Fatalf("GetRate sample count should be >=3 after fallback: got=%d", row.SampleCount)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["www.okx.com"] != 2 {
		t.Fatalf("okx should be retried once on 5xx, got=%d", calls["www.okx.com"])
	}
	if calls["otc-api.trygofast.com"] != 1 {
		t.Fatalf("huobi should be called once after okx failure, got=%d", calls["otc-api.trygofast.com"])
	}
}

func TestGetRateMinSampleThreshold(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := `{"data":[{"price":"7.20"},{"price":"7.21"}]}`
			if strings.Contains(req.URL.Host, "binance.com") {
				body = `{"data":[{"adv":{"price":"7.20"}},{"adv":{"price":"7.21"}}]}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	svc := NewExchangeServiceWithConfig(ExchangeConfig{
		HTTPClient:   client,
		Timeout:      2 * time.Second,
		RetryMax:     1,
		Backoff:      5 * time.Millisecond,
		MinSample:    3,
		CacheTTL:     0,
		DisableCache: true,
	})

	_, err := svc.GetRate("okx", "all", "CNY")
	if code := ServiceErrorCodeOf(err); code != ErrNoData {
		t.Fatalf("GetRate min sample should return ErrNoData, got=%s err=%v", code, err)
	}
}

func TestGetRateSchemaDrift(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := `{"data":[{"price":{"value":"7.2"}}]}`
			if strings.Contains(req.URL.Host, "binance.com") {
				body = `{"data":[{"adv":{"price":{"value":"7.2"}}}]}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	svc := NewExchangeServiceWithConfig(ExchangeConfig{
		HTTPClient:   client,
		Timeout:      2 * time.Second,
		RetryMax:     1,
		Backoff:      5 * time.Millisecond,
		MinSample:    1,
		CacheTTL:     0,
		DisableCache: true,
	})
	_, err := svc.GetRate("okx", "all", "CNY")
	if code := ServiceErrorCodeOf(err); code != ErrUpstream {
		t.Fatalf("GetRate schema drift should return ErrUpstream, got=%s err=%v", code, err)
	}
}

func TestGetRateTimeoutAndNon2xxFallback(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			host := req.URL.Host
			mu.Lock()
			calls[host]++
			count := calls[host]
			mu.Unlock()

			if strings.Contains(host, "trygofast.com") {
				if count == 1 {
					return nil, context.DeadlineExceeded
				}
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader(`{"error":"bad gateway"}`)),
					Header:     make(http.Header),
				}, nil
			}
			if strings.Contains(host, "binance.com") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":[{"adv":{"price":"7.18"}},{"adv":{"price":"7.19"}},{"adv":{"price":"7.20"}}]}`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"price":"7.21"},{"price":"7.22"},{"price":"7.23"}]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	svc := NewExchangeServiceWithConfig(ExchangeConfig{
		HTTPClient:   client,
		Timeout:      100 * time.Millisecond,
		RetryMax:     1,
		Backoff:      5 * time.Millisecond,
		MinSample:    3,
		CacheTTL:     0,
		DisableCache: true,
	})

	row, err := svc.GetRate("huobi", "all", "CNY")
	if err != nil {
		t.Fatalf("GetRate timeout+non2xx fallback should succeed, got err=%v", err)
	}
	if row.Provider != "binance" {
		t.Fatalf("GetRate timeout+non2xx should fallback to binance, got=%s", row.Provider)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls["otc-api.trygofast.com"] != 2 {
		t.Fatalf("huobi should be retried once on timeout/5xx, got=%d", calls["otc-api.trygofast.com"])
	}
}

func TestGetRateOKXNewSchemaDataSell(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Host, "okx.com") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"code":0,
						"data":{
							"sell":[
								{"price":"6.94","nickName":"商家A"},
								{"price":"6.95","nickName":"商家B"},
								{"price":"6.96","nickName":"商家C"}
							]
						}
					}`)),
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
	svc := NewExchangeServiceWithConfig(ExchangeConfig{
		HTTPClient:   client,
		Timeout:      2 * time.Second,
		RetryMax:     0,
		MinSample:    3,
		CacheTTL:     0,
		DisableCache: true,
	})
	row, err := svc.GetRate("okx", "all", "CNY")
	if err != nil {
		t.Fatalf("GetRate okx new schema should succeed: %v", err)
	}
	if row == nil || row.Provider != "okx" {
		t.Fatalf("GetRate provider mismatch: want=okx got=%v", row)
	}
	if len(row.Top10Quotes) < 3 {
		t.Fatalf("GetRate should parse top quotes from data.sell, got=%d", len(row.Top10Quotes))
	}
}

func TestGetRateHuobiFallsBackToGlobalProfileOnNoData(t *testing.T) {
	var mu sync.Mutex
	callsByCurrency := map[string]int{}
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if !strings.Contains(req.URL.Host, "trygofast.com") {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			}
			currency := req.URL.Query().Get("currency")
			mu.Lock()
			callsByCurrency[currency]++
			mu.Unlock()
			switch currency {
			case "1":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"code":200,"message":"Success","totalCount":0,"data":[]}`)),
					Header:     make(http.Header),
				}, nil
			case "13":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"code":200,
						"message":"Success",
						"totalCount":3,
						"data":[
							{"price":"7.80","userName":"A"},
							{"price":"7.81","userName":"B"},
							{"price":"7.82","userName":"C"}
						]
					}`)),
					Header: make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"code":200,"message":"Success","totalCount":0,"data":[]}`)),
					Header:     make(http.Header),
				}, nil
			}
		}),
	}

	svc := NewExchangeServiceWithConfig(ExchangeConfig{
		HTTPClient:   client,
		Timeout:      2 * time.Second,
		RetryMax:     0,
		Backoff:      5 * time.Millisecond,
		MinSample:    3,
		CacheTTL:     0,
		DisableCache: true,
	})

	row, err := svc.GetRate("huobi", "all", "CNY")
	if err != nil {
		t.Fatalf("GetRate huobi profile fallback should succeed: %v", err)
	}
	if row == nil || row.Provider != "huobi" {
		t.Fatalf("GetRate huobi provider mismatch: got=%v", row)
	}
	if row.SampleCount < 3 {
		t.Fatalf("GetRate huobi sample should be >=3: got=%d", row.SampleCount)
	}
	mu.Lock()
	defer mu.Unlock()
	if callsByCurrency["1"] == 0 || callsByCurrency["13"] == 0 {
		t.Fatalf("huobi should query both legacy and global profile currencies, got=%v", callsByCurrency)
	}
}

func TestGetRateOKXUsesRequestedFiatQuery(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.URL.Query().Get("quoteCurrency"); got != "usd" {
				t.Fatalf("okx quoteCurrency mismatch: got=%q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"price":"7.10","nickName":"A"},{"price":"7.11","nickName":"B"},{"price":"7.12","nickName":"C"}]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	svc := NewExchangeServiceWithConfig(ExchangeConfig{
		HTTPClient:   client,
		Timeout:      2 * time.Second,
		RetryMax:     0,
		MinSample:    3,
		CacheTTL:     0,
		DisableCache: true,
	})

	row, err := svc.GetRate("okx", "all", "USD")
	if err != nil {
		t.Fatalf("GetRate okx usd should succeed: %v", err)
	}
	if row == nil || row.Provider != "okx" {
		t.Fatalf("GetRate okx usd provider mismatch: got=%v", row)
	}
}

func TestGetRateBinanceUsesRequestedFiatPayload(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body failed: %v", err)
			}
			if !strings.Contains(string(body), `"fiat":"EUR"`) {
				t.Fatalf("binance fiat payload mismatch: body=%s", string(body))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"data":[
						{"adv":{"price":"0.92","asset":"USDT"},"advertiser":{"nickName":"A"}},
						{"adv":{"price":"0.93","asset":"USDT"},"advertiser":{"nickName":"B"}},
						{"adv":{"price":"0.94","asset":"USDT"},"advertiser":{"nickName":"C"}}
					]
				}`)),
				Header: make(http.Header),
			}, nil
		}),
	}
	svc := NewExchangeServiceWithConfig(ExchangeConfig{
		HTTPClient:   client,
		Timeout:      2 * time.Second,
		RetryMax:     0,
		MinSample:    3,
		CacheTTL:     0,
		DisableCache: true,
	})

	row, err := svc.GetRate("binance", "all", "EUR")
	if err != nil {
		t.Fatalf("GetRate binance eur should succeed: %v", err)
	}
	if row == nil || row.Provider != "binance" {
		t.Fatalf("GetRate binance eur provider mismatch: got=%v", row)
	}
}

func TestGetRateKRWUsesKoreanSpotSources(t *testing.T) {
	var hosts []string
	var mu sync.Mutex
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			mu.Lock()
			hosts = append(hosts, req.URL.Host)
			mu.Unlock()

			var body string
			switch req.URL.Host {
			case "api.upbit.com":
				body = `[{"market":"KRW-USDT","trade_price":1491}]`
			case "api.bithumb.com":
				body = `{"status":"0000","data":{"closing_price":"1490"}}`
			case "api.coinone.co.kr":
				body = `{"result":"success","tickers":[{"quote_currency":"krw","target_currency":"usdt","last":"1492"}]}`
			case "api.korbit.co.kr":
				body = `{"last":"1493"}`
			default:
				t.Fatalf("KRW should not request C2C host: %s", req.URL.Host)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	svc := NewExchangeServiceWithConfig(ExchangeConfig{
		HTTPClient:   client,
		Timeout:      2 * time.Second,
		RetryMax:     0,
		MinSample:    3,
		CacheTTL:     0,
		DisableCache: true,
	})

	row, err := svc.GetRate("binance", "all", "KRW")
	if err != nil {
		t.Fatalf("GetRate krw should use korean spot sources: %v", err)
	}
	if row == nil || row.Provider != "korea_spot" {
		t.Fatalf("GetRate krw provider mismatch: got=%v", row)
	}
	if row.PayType != "spot" {
		t.Fatalf("GetRate krw pay type mismatch: got=%s want=spot", row.PayType)
	}
	if row.SampleCount != 4 {
		t.Fatalf("GetRate krw sample count mismatch: got=%d want=4", row.SampleCount)
	}
	if len(row.Top10Quotes) != 4 {
		t.Fatalf("GetRate krw should expose venue quotes, got=%d", len(row.Top10Quotes))
	}
	if row.Top10Quotes[0].Merchant != "BITHUMB" || row.Top10Quotes[0].Price != 1490 {
		t.Fatalf("GetRate krw should sort venue quotes by price: %+v", row.Top10Quotes)
	}
	if row.Top10Quotes[3].Merchant != "KORBIT" || row.Top10Quotes[3].Price != 1493 {
		t.Fatalf("GetRate krw should include korbit quote: %+v", row.Top10Quotes)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(hosts) != 4 {
		t.Fatalf("GetRate krw should query four korean spot hosts, got=%v", hosts)
	}
}
