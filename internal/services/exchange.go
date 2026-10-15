package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-bot/internal/system"
)

type RateResult struct {
	Provider     string
	PayType      string
	Price        float64
	Top10        []float64
	Top10Quotes  []RateQuote
	Source       string
	AvgPrice     float64
	SampleCount  int
	FetchedAt    time.Time
	FallbackUsed bool
}

type RateQuote struct {
	Merchant string
	Price    float64
}

type ExchangeConfig struct {
	HTTPClient   *http.Client
	Timeout      time.Duration
	RetryMax     int
	Backoff      time.Duration
	MinSample    int
	CacheTTL     time.Duration
	DisableCache bool
}

type ExchangeService struct {
	client *http.Client
	now    func() time.Time
	sleep  func(time.Duration)

	retryMax  int
	backoff   time.Duration
	minSample int

	cacheTTL     time.Duration
	disableCache bool
	cacheMu      sync.RWMutex
	cache        map[string]rateCacheEntry
}

const (
	minSamplesForOutlierFiltering = 4
	outlierIQRMultiplier          = 1.5
	defaultRateCacheTTL           = 10 * time.Second
	defaultRateFiat               = "CNY"
	koreanSpotProvider            = "korea_spot"
)

var exchangePublicDNSServers = []string{
	"1.1.1.1:53",
	"8.8.8.8:53",
}

type rateCacheEntry struct {
	result    RateResult
	expiresAt time.Time
}

func NewExchangeService(timeout time.Duration) *ExchangeService {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return NewExchangeServiceWithConfig(ExchangeConfig{
		Timeout:      timeout,
		RetryMax:     1,
		Backoff:      200 * time.Millisecond,
		MinSample:    3,
		CacheTTL:     defaultRateCacheTTL,
		DisableCache: false,
	})
}

func NewExchangeServiceWithConfig(cfg ExchangeConfig) *ExchangeService {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.RetryMax < 0 {
		cfg.RetryMax = 0
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 200 * time.Millisecond
	}
	if cfg.MinSample <= 0 {
		cfg.MinSample = 3
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultRateCacheTTL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = system.WrapHTTPClientWithTracing(newExchangeHTTPClient(cfg.Timeout), "exchange-service")
	}

	return &ExchangeService{
		client:       client,
		now:          time.Now,
		sleep:        time.Sleep,
		retryMax:     cfg.RetryMax,
		backoff:      cfg.Backoff,
		minSample:    cfg.MinSample,
		cacheTTL:     cfg.CacheTTL,
		disableCache: cfg.DisableCache,
		cache:        make(map[string]rateCacheEntry),
	}
}

func newExchangeHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	base, _ := http.DefaultTransport.(*http.Transport)
	transport := &http.Transport{}
	if base != nil {
		transport = base.Clone()
	}
	transport.DialContext = newExchangeDialContext(timeout)
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func newExchangeDialContext(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialTimeout := 2 * time.Second
	if timeout > 0 && timeout < 4*time.Second {
		dialTimeout = timeout / 2
		if dialTimeout < time.Second {
			dialTimeout = time.Second
		}
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := &net.Dialer{Timeout: dialTimeout}
			var lastErr error
			for _, server := range exchangePublicDNSServers {
				conn, err := d.DialContext(ctx, network, server)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = errors.New("no dns server available")
			}
			return nil, lastErr
		},
	}
	dialer := &net.Dialer{
		Timeout:  timeout,
		Resolver: resolver,
	}
	return dialer.DialContext
}

func (s *ExchangeService) GetRate(provider, payType, fiat string) (*RateResult, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	payType = strings.ToLower(strings.TrimSpace(payType))
	fiatCode, serr := normalizeExchangeFiatCode(fiat)
	if serr != nil {
		return nil, serr
	}
	if cached, ok := s.getCachedRate(provider, payType, fiatCode); ok {
		return cached, nil
	}
	if isKoreanSpotFiat(fiatCode) {
		row, err := s.fetchKoreanSpotRates()
		if err != nil {
			return nil, err
		}
		if row == nil || row.SampleCount < s.minSample {
			return nil, NewServiceError(ErrNoData, "报价样本不足", false)
		}
		row.Provider = koreanSpotProvider
		row.PayType = "spot"
		s.setCachedRate(provider, payType, fiatCode, row)
		return cloneRateResult(row), nil
	}

	chain := fallbackChain(provider)
	var (
		lastErr     error
		sawUpstream bool
		sawNoData   bool
	)

	for i, item := range chain {
		row, err := s.fetchWithRetry(item, payType, fiatCode)
		if err == nil && row != nil && row.SampleCount >= s.minSample {
			row.Provider = item
			row.FallbackUsed = i > 0
			row.PayType = payType
			s.setCachedRate(provider, payType, fiatCode, row)
			return cloneRateResult(row), nil
		}
		if code := ServiceErrorCodeOf(err); code == ErrNoData || (err == nil && row != nil && row.SampleCount < s.minSample) {
			sawNoData = true
		} else {
			sawUpstream = true
		}
		if err != nil {
			lastErr = err
		}
	}

	if sawNoData && !sawUpstream {
		return nil, NewServiceError(ErrNoData, "报价样本不足", false)
	}
	if serr, ok := AsServiceError(lastErr); ok {
		return nil, serr
	}
	if lastErr != nil {
		return nil, WrapServiceError(ErrUpstream, "汇率查询失败", false, lastErr)
	}
	return nil, NewServiceError(ErrUpstream, "汇率查询失败", false)
}

func (s *ExchangeService) CompareRates(payType, fiat string) ([]RateResult, error) {
	fiatCode, serr := normalizeExchangeFiatCode(fiat)
	if serr != nil {
		return nil, serr
	}
	if isKoreanSpotFiat(fiatCode) {
		row, err := s.fetchKoreanSpotRates()
		if err != nil {
			return nil, err
		}
		if row == nil || row.SampleCount < s.minSample {
			return nil, NewServiceError(ErrNoData, "报价样本不足", false)
		}
		out := make([]RateResult, 0, len(row.Top10Quotes))
		for _, quote := range row.Top10Quotes {
			provider := strings.ToLower(strings.TrimSpace(quote.Merchant))
			if provider == "" {
				provider = koreanSpotProvider
			}
			out = append(out, RateResult{
				Provider:     provider,
				PayType:      "spot",
				Price:        quote.Price,
				Top10:        []float64{quote.Price},
				Top10Quotes:  []RateQuote{quote},
				Source:       row.Source,
				AvgPrice:     quote.Price,
				SampleCount:  1,
				FetchedAt:    row.FetchedAt,
				FallbackUsed: false,
			})
		}
		return out, nil
	}
	providers := []string{"okx", "huobi", "binance"}
	out := make([]RateResult, 0, 3)
	for _, p := range providers {
		row, err := s.fetchWithRetry(p, payType, fiatCode)
		if err == nil && row != nil {
			row.Provider = p
			out = append(out, *row)
		}
	}
	if len(out) == 0 {
		return nil, NewServiceError(ErrUpstream, "汇率查询失败", false)
	}
	return out, nil
}

func isKoreanSpotFiat(fiat string) bool {
	return strings.EqualFold(strings.TrimSpace(fiat), "KRW")
}

func fallbackChain(provider string) []string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "okx":
		return []string{"okx", "huobi", "binance"}
	case "huobi":
		return []string{"huobi", "binance", "okx"}
	case "binance":
		return []string{"binance", "okx", "huobi"}
	default:
		return []string{"okx", "huobi", "binance"}
	}
}

func normalizeExchangeFiatCode(raw string) (string, *ServiceError) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if code == "" {
		return defaultRateFiat, nil
	}
	switch code {
	case "CNY", "JPY", "KRW", "VND", "INR", "EUR", "USD":
		return code, nil
	default:
		return "", NewServiceError(ErrInvalidInput, fmt.Sprintf("不支持的法币市场: %s", code), false)
	}
}

func avgTop10(prices []float64) (float64, []float64) {
	filtered := filterOutliers(prices)
	if len(filtered) == 0 {
		return 0, nil
	}

	top10 := append([]float64(nil), filtered...)
	if len(top10) > 10 {
		top10 = top10[:10]
	}

	return weightedTrimmedAverage(top10), top10
}

func avgTop10Quotes(quotes []RateQuote) (float64, []float64, []RateQuote) {
	filtered := filterQuoteOutliers(quotes)
	if len(filtered) == 0 {
		return 0, nil, nil
	}
	top := filtered
	if len(top) > 10 {
		top = top[:10]
	}
	topQuotes := append([]RateQuote(nil), top...)
	topPrices := make([]float64, 0, len(topQuotes))
	for _, q := range topQuotes {
		topPrices = append(topPrices, q.Price)
	}
	return weightedTrimmedAverage(topPrices), topPrices, topQuotes
}

// ComputeFairPrice 计算市场公允价：去极值后做中心加权平均。
func ComputeFairPrice(prices []float64) float64 {
	filtered := filterOutliers(prices)
	if len(filtered) == 0 {
		return 0
	}
	return weightedTrimmedAverage(filtered)
}

func weightedTrimmedAverage(sortedPrices []float64) float64 {
	fairPrices := sortedPrices
	if len(fairPrices) > 2 {
		// 控制点 3.4.2：去掉最高/最低报价，降低尖刺价格对市场公允价的影响。
		fairPrices = fairPrices[1 : len(fairPrices)-1]
	}
	center := float64(len(fairPrices)-1) / 2
	weightedSum := 0.0
	totalWeight := 0.0
	for i, p := range fairPrices {
		weight := 1 / (1 + math.Abs(float64(i)-center))
		weightedSum += p * weight
		totalWeight += weight
	}
	if totalWeight == 0 {
		return 0
	}
	return weightedSum / totalWeight
}

// 控制点 3.4.3：使用 IQR 显式过滤离群值，降低插针行情对汇率的污染。
func filterOutliers(prices []float64) []float64 {
	normalized := normalizePrices(prices)
	if len(normalized) == 0 {
		return nil
	}
	if len(normalized) < minSamplesForOutlierFiltering {
		sort.Float64s(normalized)
		return normalized
	}

	sorted := append([]float64(nil), normalized...)
	sort.Float64s(sorted)

	q1 := percentile(sorted, 0.25)
	q3 := percentile(sorted, 0.75)
	iqr := q3 - q1
	lowerBound := q1 - outlierIQRMultiplier*iqr
	upperBound := q3 + outlierIQRMultiplier*iqr

	filtered := make([]float64, 0, len(sorted))
	for _, price := range sorted {
		if price < lowerBound || price > upperBound {
			continue
		}
		filtered = append(filtered, price)
	}
	if len(filtered) == 0 {
		return sorted
	}
	return filtered
}

func filterQuoteOutliers(quotes []RateQuote) []RateQuote {
	normalized := make([]RateQuote, 0, len(quotes))
	for i, quote := range quotes {
		if quote.Price <= 0 || math.IsNaN(quote.Price) || math.IsInf(quote.Price, 0) {
			continue
		}
		merchant := strings.TrimSpace(quote.Merchant)
		if merchant == "" {
			merchant = fmt.Sprintf("商家%d", i+1)
		}
		normalized = append(normalized, RateQuote{
			Merchant: merchant,
			Price:    quote.Price,
		})
	}
	if len(normalized) == 0 {
		return nil
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Price < normalized[j].Price
	})
	if len(normalized) < minSamplesForOutlierFiltering {
		return normalized
	}
	prices := make([]float64, 0, len(normalized))
	for _, quote := range normalized {
		prices = append(prices, quote.Price)
	}
	q1 := percentile(prices, 0.25)
	q3 := percentile(prices, 0.75)
	iqr := q3 - q1
	lowerBound := q1 - outlierIQRMultiplier*iqr
	upperBound := q3 + outlierIQRMultiplier*iqr

	filtered := make([]RateQuote, 0, len(normalized))
	for _, quote := range normalized {
		if quote.Price < lowerBound || quote.Price > upperBound {
			continue
		}
		filtered = append(filtered, quote)
	}
	if len(filtered) == 0 {
		return normalized
	}
	return filtered
}

func normalizePrices(prices []float64) []float64 {
	normalized := make([]float64, 0, len(prices))
	for _, price := range prices {
		if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			continue
		}
		normalized = append(normalized, price)
	}
	return normalized
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}

	position := p * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}

	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func (s *ExchangeService) fetchWithRetry(provider, payType, fiat string) (*RateResult, error) {
	attempts := s.retryMax + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr *ServiceError
	for i := 0; i < attempts; i++ {
		row, serr := s.fetchProvider(provider, payType, fiat)
		if serr == nil {
			return row, nil
		}
		lastErr = serr
		if serr.Retryable && i < attempts-1 {
			s.sleepWithBackoff(i)
			continue
		}
		return nil, serr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, NewServiceError(ErrUpstream, "上游查询失败", false)
}

func (s *ExchangeService) sleepWithBackoff(attempt int) {
	if s.sleep == nil {
		return
	}
	wait := s.backoff
	if wait <= 0 {
		wait = 200 * time.Millisecond
	}
	if attempt > 0 {
		wait = wait * time.Duration(1<<attempt)
	}
	s.sleep(wait)
}

func (s *ExchangeService) fetchProvider(provider, payType, fiat string) (*RateResult, *ServiceError) {
	switch provider {
	case "okx":
		return s.fetchOKX(payType, fiat)
	case "huobi":
		return s.fetchHuobi(payType, fiat)
	case "binance":
		return s.fetchBinance(payType, fiat)
	default:
		return nil, NewServiceError(ErrInvalidInput, fmt.Sprintf("不支持的交易所: %s", provider), false)
	}
}

func classifyExchangeRequestError(err error) *ServiceError {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") {
		return WrapServiceError(ErrTimeout, "上游请求超时", true, err)
	}
	return WrapServiceError(ErrUpstream, "上游请求失败", true, err)
}

func (s *ExchangeService) fetchOKX(payType, fiat string) (*RateResult, *ServiceError) {
	mapping := map[string]string{"all": "all", "bank": "bank", "alipay": "aliPay", "wechat": "wxPay"}
	method := mapping[payType]
	if method == "" {
		method = "all"
	}
	url := "https://www.okx.com/v3/c2c/tradingOrders/books"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	q := req.URL.Query()
	q.Set("quoteCurrency", strings.ToLower(fiat))
	q.Set("baseCurrency", "usdt")
	q.Set("side", "sell")
	q.Set("paymentMethod", method)
	q.Set("userType", "all")
	q.Set("showTrade", "false")
	q.Set("showFollow", "false")
	q.Set("sortType", "price_asc")
	q.Set("currentPage", "1")
	q.Set("pageSize", "10")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, classifyExchangeRequestError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nil, NewServiceError(ErrUpstream, fmt.Sprintf("okx 返回状态异常: %d", resp.StatusCode), true)
	}
	if resp.StatusCode >= 300 {
		return nil, NewServiceError(ErrUpstream, fmt.Sprintf("okx 返回状态异常: %d", resp.StatusCode), false)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, WrapServiceError(ErrUpstream, "okx 响应解析失败", false, err)
	}
	items := toSlice(raw["data"])
	if len(items) == 0 {
		items = toSlice(raw["sell"])
	}
	if len(items) == 0 {
		if dataObj, ok := raw["data"].(map[string]any); ok {
			items = toSlice(dataObj["sell"])
		}
	}
	quotes := make([]RateQuote, 0, 10)
	for _, row := range items {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		var price float64
		var okPrice bool
		if p, ok := toFloat(m["price"]); ok {
			price = p
			okPrice = true
		} else if p, ok := toFloat(m["priceNum"]); ok {
			price = p
			okPrice = true
		}
		if !okPrice {
			continue
		}
		quotes = append(quotes, RateQuote{
			Merchant: extractOKXMerchantName(m),
			Price:    price,
		})
	}
	if len(quotes) == 0 {
		if len(items) > 0 {
			return nil, NewServiceError(ErrUpstream, "okx 价格字段结构异常", false)
		}
		return nil, NewServiceError(ErrNoData, "okx 无有效价格", false)
	}
	avg, top, topQuotes := avgTop10Quotes(quotes)
	return &RateResult{
		Provider:    "okx",
		PayType:     payType,
		Price:       avg,
		Top10:       top,
		Top10Quotes: topQuotes,
		Source:      req.URL.String(),
		AvgPrice:    avg,
		SampleCount: len(topQuotes),
		FetchedAt:   s.currentTime(),
	}, nil
}

func (s *ExchangeService) fetchHuobi(payType, fiat string) (*RateResult, *ServiceError) {
	if !strings.EqualFold(strings.TrimSpace(fiat), "CNY") {
		return nil, NewServiceError(ErrNoData, fmt.Sprintf("huobi 暂不支持 %s 法币市场", strings.ToUpper(strings.TrimSpace(fiat))), false)
	}
	url := "https://otc-api.trygofast.com/v1/data/trade-market"
	profiles := []struct {
		name     string
		coinID   string
		currency string
		payMap   map[string]string
	}{
		{
			name:     "legacy-cny",
			coinID:   "2",
			currency: "1",
			payMap:   map[string]string{"all": "0", "bank": "1", "alipay": "28", "wechat": "29"},
		},
		{
			name:     "global-alt",
			coinID:   "2",
			currency: "13",
			payMap:   map[string]string{"all": "0", "bank": "1", "alipay": "2", "wechat": "3"},
		},
	}
	var lastErr *ServiceError
	for _, profile := range profiles {
		row, serr := s.fetchHuobiWithProfile(url, payType, profile.name, profile.coinID, profile.currency, profile.payMap)
		if serr == nil {
			return row, nil
		}
		lastErr = serr
		// 仅在“无报价”场景尝试下一个参数集；网络/上游异常沿用原逻辑直接返回。
		if serr.Code != ErrNoData {
			return nil, serr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, NewServiceError(ErrNoData, "huobi 无有效价格", false)
}

func (s *ExchangeService) fetchHuobiWithProfile(url, payType, profileName, coinID, currency string, payMap map[string]string) (*RateResult, *ServiceError) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	q := req.URL.Query()
	q.Set("coinId", coinID)
	q.Set("currency", currency)
	q.Set("tradeType", "sell")
	q.Set("currPage", "1")
	q.Set("payMethod", payMap[payType])
	if q.Get("payMethod") == "" {
		q.Set("payMethod", "0")
	}
	q.Set("acceptOrder", "-1")
	q.Set("country", "")
	q.Set("blockType", "general")
	q.Set("online", "1")
	q.Set("range", "0")
	q.Set("amount", "")
	q.Set("isThumbsUp", "false")
	q.Set("isMerchant", "false")
	q.Set("isTraded", "false")
	q.Set("payMethods", "")
	q.Set("pageSize", "10")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, classifyExchangeRequestError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nil, NewServiceError(ErrUpstream, fmt.Sprintf("huobi 返回状态异常: %d", resp.StatusCode), true)
	}
	if resp.StatusCode >= 300 {
		return nil, NewServiceError(ErrUpstream, fmt.Sprintf("huobi 返回状态异常: %d", resp.StatusCode), false)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, WrapServiceError(ErrUpstream, "huobi 响应解析失败", false, err)
	}
	items := toSlice(raw["data"])
	quotes := make([]RateQuote, 0, 10)
	for _, row := range items {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		if p, ok := toFloat(m["price"]); ok {
			quotes = append(quotes, RateQuote{
				Merchant: extractHuobiMerchantName(m),
				Price:    p,
			})
		}
	}
	if len(quotes) == 0 {
		if len(items) > 0 {
			return nil, NewServiceError(ErrUpstream, "huobi 价格字段结构异常", false)
		}
		return nil, NewServiceError(ErrNoData, "huobi 无有效价格", false)
	}
	avg, top, topQuotes := avgTop10Quotes(quotes)
	return &RateResult{
		Provider:    "huobi",
		PayType:     payType,
		Price:       avg,
		Top10:       top,
		Top10Quotes: topQuotes,
		Source:      req.URL.String() + "&profile=" + profileName,
		AvgPrice:    avg,
		SampleCount: len(topQuotes),
		FetchedAt:   s.currentTime(),
	}, nil
}

func (s *ExchangeService) fetchBinance(payType, fiat string) (*RateResult, *ServiceError) {
	payMap := map[string][]string{"all": {}, "bank": {"BANK"}, "alipay": {"ALIPAY"}, "wechat": {"WECHAT"}}
	payload := map[string]any{
		"page":          1,
		"rows":          10,
		"payTypes":      payMap[payType],
		"asset":         "USDT",
		"tradeType":     "SELL",
		"fiat":          strings.ToUpper(strings.TrimSpace(fiat)),
		"publisherType": nil,
	}
	if _, ok := payMap[payType]; !ok {
		payload["payTypes"] = []string{}
	}
	body, _ := json.Marshal(payload)
	url := "https://p2p.binance.com/bapi/c2c/v2/friendly/c2c/adv/search"
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, classifyExchangeRequestError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nil, NewServiceError(ErrUpstream, fmt.Sprintf("binance 返回状态异常: %d", resp.StatusCode), true)
	}
	if resp.StatusCode >= 300 {
		return nil, NewServiceError(ErrUpstream, fmt.Sprintf("binance 返回状态异常: %d", resp.StatusCode), false)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, WrapServiceError(ErrUpstream, "binance 响应解析失败", false, err)
	}
	items := toSlice(raw["data"])
	quotes := make([]RateQuote, 0, 10)
	for _, row := range items {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		adv, _ := m["adv"].(map[string]any)
		if p, ok := toFloat(adv["price"]); ok {
			quotes = append(quotes, RateQuote{
				Merchant: extractBinanceMerchantName(m, adv),
				Price:    p,
			})
		}
	}
	if len(quotes) == 0 {
		if len(items) > 0 {
			return nil, NewServiceError(ErrUpstream, "binance 价格字段结构异常", false)
		}
		return nil, NewServiceError(ErrNoData, "binance 无有效价格", false)
	}
	avg, top, topQuotes := avgTop10Quotes(quotes)
	return &RateResult{
		Provider:    "binance",
		PayType:     payType,
		Price:       avg,
		Top10:       top,
		Top10Quotes: topQuotes,
		Source:      url,
		AvgPrice:    avg,
		SampleCount: len(topQuotes),
		FetchedAt:   s.currentTime(),
	}, nil
}

func (s *ExchangeService) fetchKoreanSpotRates() (*RateResult, *ServiceError) {
	sources := []struct {
		name  string
		url   string
		parse func(any) (float64, bool)
	}{
		{
			name:  "UPBIT",
			url:   "https://api.upbit.com/v1/ticker?markets=KRW-USDT",
			parse: parseUpbitUSDTKRWPrice,
		},
		{
			name:  "BITHUMB",
			url:   "https://api.bithumb.com/public/ticker/USDT_KRW",
			parse: parseBithumbUSDTKRWPrice,
		},
		{
			name:  "COINONE",
			url:   "https://api.coinone.co.kr/public/v2/ticker_new/KRW/USDT",
			parse: parseCoinoneUSDTKRWPrice,
		},
		{
			name:  "KORBIT",
			url:   "https://api.korbit.co.kr/v1/ticker/detailed?currency_pair=usdt_krw",
			parse: parseKorbitUSDTKRWPrice,
		},
	}
	quotes := make([]RateQuote, 0, len(sources))
	sourceURLs := make([]string, 0, len(sources))
	var lastErr *ServiceError
	for _, source := range sources {
		price, serr := s.fetchKoreanSpotQuote(source.name, source.url, source.parse)
		if serr != nil {
			lastErr = serr
			continue
		}
		quotes = append(quotes, RateQuote{
			Merchant: source.name,
			Price:    price,
		})
		sourceURLs = append(sourceURLs, source.url)
	}
	if len(quotes) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, NewServiceError(ErrNoData, "韩国现货源无有效价格", false)
	}
	avg, top, topQuotes := avgTop10Quotes(quotes)
	return &RateResult{
		Provider:    koreanSpotProvider,
		PayType:     "spot",
		Price:       avg,
		Top10:       top,
		Top10Quotes: topQuotes,
		Source:      strings.Join(sourceURLs, ","),
		AvgPrice:    avg,
		SampleCount: len(topQuotes),
		FetchedAt:   s.currentTime(),
	}, nil
}

func (s *ExchangeService) fetchKoreanSpotQuote(name, url string, parse func(any) (float64, bool)) (float64, *ServiceError) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, classifyExchangeRequestError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return 0, NewServiceError(ErrUpstream, fmt.Sprintf("%s 返回状态异常: %d", strings.ToLower(name), resp.StatusCode), true)
	}
	if resp.StatusCode >= 300 {
		return 0, NewServiceError(ErrUpstream, fmt.Sprintf("%s 返回状态异常: %d", strings.ToLower(name), resp.StatusCode), false)
	}
	var raw any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return 0, WrapServiceError(ErrUpstream, strings.ToLower(name)+" 响应解析失败", false, err)
	}
	price, ok := parse(raw)
	if !ok || price <= 0 {
		return 0, NewServiceError(ErrNoData, strings.ToLower(name)+" 无有效价格", false)
	}
	return price, nil
}

func parseUpbitUSDTKRWPrice(raw any) (float64, bool) {
	items := toSlice(raw)
	if len(items) == 0 {
		return 0, false
	}
	row, ok := items[0].(map[string]any)
	if !ok {
		return 0, false
	}
	return toFloat(row["trade_price"])
}

func parseBithumbUSDTKRWPrice(raw any) (float64, bool) {
	row, ok := raw.(map[string]any)
	if !ok {
		return 0, false
	}
	data, ok := row["data"].(map[string]any)
	if !ok {
		return 0, false
	}
	return toFloat(data["closing_price"])
}

func parseCoinoneUSDTKRWPrice(raw any) (float64, bool) {
	row, ok := raw.(map[string]any)
	if !ok {
		return 0, false
	}
	items := toSlice(row["tickers"])
	if len(items) == 0 {
		return 0, false
	}
	ticker, ok := items[0].(map[string]any)
	if !ok {
		return 0, false
	}
	return toFloat(ticker["last"])
}

func parseKorbitUSDTKRWPrice(raw any) (float64, bool) {
	row, ok := raw.(map[string]any)
	if !ok {
		return 0, false
	}
	return toFloat(row["last"])
}

func (s *ExchangeService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func toSlice(v any) []any {
	if v == nil {
		return nil
	}
	out, ok := v.([]any)
	if !ok {
		return nil
	}
	return out
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		n, err := x.Float64()
		return n, err == nil
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func toString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		value := strings.TrimSpace(x)
		return value, value != ""
	case json.Number:
		value := strings.TrimSpace(x.String())
		return value, value != ""
	case fmt.Stringer:
		value := strings.TrimSpace(x.String())
		return value, value != ""
	default:
		return "", false
	}
}

func pickMerchantName(candidates ...string) string {
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate)
		if value != "" {
			return value
		}
	}
	return ""
}

func extractNestedName(raw map[string]any, key string, fields ...string) string {
	nested, _ := raw[key].(map[string]any)
	if len(nested) == 0 {
		return ""
	}
	for _, field := range fields {
		if value, ok := toString(nested[field]); ok {
			return value
		}
	}
	return ""
}

func extractOKXMerchantName(raw map[string]any) string {
	name := pickMerchantName(
		stringOf(raw["nickName"]),
		stringOf(raw["nickname"]),
		stringOf(raw["merchantName"]),
		stringOf(raw["userName"]),
		extractNestedName(raw, "merchant", "nickName", "nickname", "merchantName", "userName"),
		extractNestedName(raw, "user", "nickName", "nickname", "userName"),
		extractNestedName(raw, "userInfo", "nickName", "nickname", "userName"),
	)
	if name != "" {
		return name
	}
	return "OKX商家"
}

func extractHuobiMerchantName(raw map[string]any) string {
	name := pickMerchantName(
		stringOf(raw["userName"]),
		stringOf(raw["nickName"]),
		stringOf(raw["nickname"]),
		stringOf(raw["merchantName"]),
		extractNestedName(raw, "merchant", "nickName", "nickname", "merchantName", "userName"),
		extractNestedName(raw, "user", "nickName", "nickname", "userName"),
	)
	if name != "" {
		return name
	}
	return "HUOBI商家"
}

func extractBinanceMerchantName(raw map[string]any, adv map[string]any) string {
	name := pickMerchantName(
		stringOf(adv["nickName"]),
		stringOf(adv["nickname"]),
		extractNestedName(raw, "advertiser", "nickName", "nickname", "userName", "userNo"),
		extractNestedName(raw, "user", "nickName", "nickname", "userName"),
	)
	if name != "" {
		return name
	}
	return "BINANCE商家"
}

func stringOf(value any) string {
	if text, ok := toString(value); ok {
		return text
	}
	return ""
}

func (s *ExchangeService) getCachedRate(provider, payType, fiat string) (*RateResult, bool) {
	if s == nil || s.disableCache || s.cacheTTL <= 0 {
		return nil, false
	}
	key := rateCacheKey(provider, payType, fiat)
	nowFn := s.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()

	s.cacheMu.RLock()
	entry, ok := s.cache[key]
	s.cacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	if now.After(entry.expiresAt) {
		s.cacheMu.Lock()
		current, exists := s.cache[key]
		if exists && current.expiresAt.Equal(entry.expiresAt) {
			delete(s.cache, key)
		}
		s.cacheMu.Unlock()
		return nil, false
	}
	return cloneRateResult(&entry.result), true
}

func (s *ExchangeService) setCachedRate(provider, payType, fiat string, result *RateResult) {
	if s == nil || s.disableCache || s.cacheTTL <= 0 || result == nil {
		return
	}
	nowFn := s.now
	if nowFn == nil {
		nowFn = time.Now
	}
	entry := rateCacheEntry{
		result:    *cloneRateResult(result),
		expiresAt: nowFn().Add(s.cacheTTL),
	}
	key := rateCacheKey(provider, payType, fiat)
	s.cacheMu.Lock()
	if s.cache == nil {
		s.cache = make(map[string]rateCacheEntry)
	}
	s.cache[key] = entry
	s.cacheMu.Unlock()
}

func rateCacheKey(provider, payType, fiat string) string {
	return strings.TrimSpace(strings.ToLower(provider)) + "|" +
		strings.TrimSpace(strings.ToLower(payType)) + "|" +
		strings.TrimSpace(strings.ToUpper(fiat))
}

func cloneRateResult(result *RateResult) *RateResult {
	if result == nil {
		return nil
	}
	cloned := *result
	if result.Top10 != nil {
		cloned.Top10 = append([]float64(nil), result.Top10...)
	}
	if result.Top10Quotes != nil {
		cloned.Top10Quotes = append([]RateQuote(nil), result.Top10Quotes...)
	}
	return &cloned
}
