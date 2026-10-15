package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	cardProviderPrimary  = "primary"
	cardProviderAlipay   = "alipay"
	cardProviderBinlist  = "binlist"
	cardProviderAPICN    = "apicn"
	cardProviderHaoshudi = "haoshudi"
)

var supportedCardProviders = []string{
	cardProviderPrimary,
	cardProviderBinlist,
	cardProviderAlipay,
}

type CardRoutingConfig struct {
	PrimaryEndpoint        string
	PrimaryAPIKey          string
	PrimaryAPIHeader       string
	DefaultProviderTimeout time.Duration
	ProviderTimeouts       map[string]time.Duration
	CacheTTL               time.Duration
	CacheMaxEntries        int
	DisableCache           bool
}

type cardCacheEntry struct {
	info      CardInfo
	expiresAt time.Time
}

func DefaultCardRoutingConfig() CardRoutingConfig {
	return CardRoutingConfig{
		PrimaryEndpoint:        "https://api.iinlist.com/cards",
		PrimaryAPIKey:          "",
		PrimaryAPIHeader:       "X-API-Key",
		DefaultProviderTimeout: 2500 * time.Millisecond,
		ProviderTimeouts: map[string]time.Duration{
			cardProviderPrimary: 2500 * time.Millisecond,
			cardProviderAlipay:  2500 * time.Millisecond,
			cardProviderBinlist: 2500 * time.Millisecond,
		},
		CacheTTL:        5 * time.Minute,
		CacheMaxEntries: 2048,
		DisableCache:    false,
	}
}

func normalizeCardRoutingConfig(cfg CardRoutingConfig) CardRoutingConfig {
	defaults := DefaultCardRoutingConfig()

	if cfg.DefaultProviderTimeout <= 0 {
		cfg.DefaultProviderTimeout = defaults.DefaultProviderTimeout
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaults.CacheTTL
	}
	if cfg.CacheMaxEntries <= 0 {
		cfg.CacheMaxEntries = defaults.CacheMaxEntries
	}
	cfg.PrimaryEndpoint = strings.TrimSpace(cfg.PrimaryEndpoint)
	cfg.PrimaryAPIKey = strings.TrimSpace(cfg.PrimaryAPIKey)
	cfg.PrimaryAPIHeader = strings.TrimSpace(cfg.PrimaryAPIHeader)
	if cfg.PrimaryAPIHeader == "" {
		cfg.PrimaryAPIHeader = defaults.PrimaryAPIHeader
	}

	normalized := make(map[string]time.Duration, len(supportedCardProviders))
	for _, provider := range supportedCardProviders {
		normalized[provider] = cfg.DefaultProviderTimeout
	}
	for provider, timeout := range cfg.ProviderTimeouts {
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" || timeout <= 0 {
			continue
		}
		normalized[name] = timeout
	}
	cfg.ProviderTimeouts = normalized
	return cfg
}

func (s *CardService) effectiveRoutingConfig() CardRoutingConfig {
	if s == nil {
		return DefaultCardRoutingConfig()
	}
	return normalizeCardRoutingConfig(s.routing)
}

func (s *CardService) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *CardService) providerTimeout(provider string) time.Duration {
	cfg := s.effectiveRoutingConfig()
	provider = strings.ToLower(strings.TrimSpace(provider))
	if t, ok := cfg.ProviderTimeouts[provider]; ok && t > 0 {
		return t
	}
	return cfg.DefaultProviderTimeout
}

func cardCacheKey(cardNo string) string {
	if len(cardNo) >= 8 {
		return cardNo[:8]
	}
	return cardNo
}

func (s *CardService) cardCacheKey(cardNo string) string {
	if s.primaryCardDetailEnabled() {
		return strings.TrimSpace(cardNo)
	}
	return cardCacheKey(cardNo)
}

func (s *CardService) getCachedCardInfo(cardNo string, valid bool) (CardInfo, bool) {
	cfg := s.effectiveRoutingConfig()
	if cfg.DisableCache || cfg.CacheTTL <= 0 {
		return CardInfo{}, false
	}
	key := s.cardCacheKey(cardNo)
	if key == "" {
		return CardInfo{}, false
	}

	s.cacheMu.RLock()
	entry, ok := s.cache[key]
	s.cacheMu.RUnlock()
	if !ok {
		return CardInfo{}, false
	}
	if s.nowTime().After(entry.expiresAt) {
		s.cacheMu.Lock()
		if current, exists := s.cache[key]; exists && current.expiresAt.Equal(entry.expiresAt) {
			delete(s.cache, key)
		}
		s.cacheMu.Unlock()
		return CardInfo{}, false
	}

	cached := entry.info
	cached.CardNo = cardNo
	cached.Valid = valid
	return cached, true
}

func (s *CardService) setCachedCardInfo(cardNo string, info CardInfo) {
	cfg := s.effectiveRoutingConfig()
	if cfg.DisableCache || cfg.CacheTTL <= 0 || !isCardDataPresent(info) {
		return
	}
	key := s.cardCacheKey(cardNo)
	if key == "" {
		return
	}

	entryInfo := info
	entryInfo.CardNo = ""
	entryInfo.Valid = false
	entry := cardCacheEntry{
		info:      entryInfo,
		expiresAt: s.nowTime().Add(cfg.CacheTTL),
	}

	s.cacheMu.Lock()
	if s.cache == nil {
		s.cache = make(map[string]cardCacheEntry)
	}
	if _, exists := s.cache[key]; !exists {
		s.cacheOrder = append(s.cacheOrder, key)
	}
	s.cache[key] = entry
	for len(s.cache) > cfg.CacheMaxEntries && len(s.cacheOrder) > 0 {
		oldest := s.cacheOrder[0]
		s.cacheOrder = s.cacheOrder[1:]
		delete(s.cache, oldest)
	}
	s.cacheMu.Unlock()
}

func (s *CardService) queryCardWithRouting(cardNo string, valid bool) (CardInfo, *ServiceError) {
	info := CardInfo{CardNo: cardNo, Valid: valid}
	order := s.cardProviderOrder()
	if len(order) == 0 {
		order = []string{cardProviderBinlist}
	}

	var (
		hitData       bool
		sawUpstream   bool
		sawNoData     bool
		primarySource = ""
	)

	for i, provider := range order {
		present, err := s.queryProvider(provider, cardNo, &info)
		if err != nil {
			sawUpstream = true
			continue
		}
		if !present {
			sawNoData = true
			continue
		}
		if primarySource == "" {
			primarySource = strings.TrimSpace(info.Source)
			if primarySource == "" {
				primarySource = strings.TrimSpace(info.DetailSource)
			}
			if primarySource == "" {
				primarySource = provider
				info.Source = provider
			}
			info.Fallback = i > 0
		}
		hitData = true
		if hasSufficientCardData(info) {
			break
		}
	}

	heuristicHit := applyLocalCardHeuristic(&info)
	if heuristicHit {
		hitData = true
	}
	if strings.TrimSpace(info.BankName) == "" && strings.TrimSpace(info.BankCode) != "" {
		if mapped := bankCodeName(info.BankCode); mapped != "" {
			info.BankName = mapped
			if strings.TrimSpace(info.Source) == "" {
				info.Source = "local_map"
				info.Fallback = true
			}
			hitData = true
		}
	}

	if hitData || isCardDataPresent(info) {
		if strings.TrimSpace(info.Source) == "" {
			info.Source = "unknown"
		}
		return info, nil
	}
	if sawUpstream {
		return info, NewServiceError(ErrUpstream, "上游查卡接口不可用", false)
	}
	if sawNoData {
		return info, NewServiceError(ErrNoData, "未查询到卡信息", false)
	}
	return info, NewServiceError(ErrNoData, "未查询到卡信息", false)
}

func (s *CardService) cardProviderOrder() []string {
	cfg := s.effectiveRoutingConfig()
	order := make([]string, 0, 2)
	if primaryProviderConfigured(cfg) {
		order = append(order, cardProviderPrimary)
	}
	order = append(order, cardProviderBinlist)
	return order
}

func hasSufficientCardData(info CardInfo) bool {
	if strings.TrimSpace(info.BankName) != "" && strings.TrimSpace(info.CardType) != "" {
		return true
	}
	if strings.TrimSpace(info.BankName) != "" && strings.TrimSpace(info.CardName) != "" {
		return true
	}
	if strings.TrimSpace(info.BankName) != "" && strings.TrimSpace(info.Brand) != "" {
		return true
	}
	if strings.TrimSpace(info.BankCode) != "" && strings.TrimSpace(info.CardType) != "" {
		return true
	}
	return false
}

func primaryProviderConfigured(cfg CardRoutingConfig) bool {
	endpoint := strings.TrimSpace(cfg.PrimaryEndpoint)
	if endpoint == "" {
		return false
	}
	if strings.TrimSpace(cfg.PrimaryAPIKey) != "" {
		return true
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	return isHaoshudiWebEndpoint(parsed) ||
		(isAPICNPrimaryEndpoint(parsed) && strings.TrimSpace(parsed.Query().Get("token")) != "")
}

func (s *CardService) primaryCardDetailEnabled() bool {
	cfg := s.effectiveRoutingConfig()
	if !primaryProviderConfigured(cfg) {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.PrimaryEndpoint))
	if err != nil {
		return false
	}
	return isHaoshudiWebEndpoint(parsed) || isAPICNPrimaryEndpoint(parsed)
}

func isAPICNPrimaryEndpoint(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Host))
	path := strings.ToLower(strings.TrimSpace(parsed.Path))
	return !isHaoshudiWebEndpoint(parsed) && (strings.Contains(host, "api.cn") || strings.Contains(path, "yinhangka"))
}

func isHaoshudiWebEndpoint(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Host))
	path := strings.ToLower(strings.TrimSpace(parsed.Path))
	return strings.Contains(host, "haoshudi.com") && strings.Contains(path, "yinhangka")
}

func (s *CardService) enrichCardWithPrimaryDetail(cardNo string, info *CardInfo) {
	if info == nil || !s.primaryCardDetailEnabled() {
		return
	}
	timeout := s.providerTimeout(cardProviderPrimary)
	ctx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	_, _ = s.fetchFromPrimary(ctx, cardNo, info)
}

func (s *CardService) queryProvider(provider, cardNo string, info *CardInfo) (bool, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	timeout := s.providerTimeout(provider)
	ctx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	switch provider {
	case cardProviderPrimary:
		return s.fetchFromPrimary(ctx, cardNo, info)
	case cardProviderAlipay:
		return s.fetchFromAlipay(ctx, cardNo, info)
	case cardProviderBinlist:
		return s.fetchFromBinlist(ctx, cardNo, info)
	default:
		return false, nil
	}
}

func (s *CardService) fetchFromPrimary(ctx context.Context, cardNo string, info *CardInfo) (bool, error) {
	cfg := s.effectiveRoutingConfig()
	endpoint := strings.TrimSpace(cfg.PrimaryEndpoint)
	if endpoint == "" {
		return false, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false, WrapServiceError(ErrUpstream, "商业主源地址配置错误", false, err)
	}
	if isHaoshudiWebEndpoint(parsed) {
		return s.fetchFromHaoshudiWeb(ctx, parsed, cardNo, info)
	}
	isAPICN := isAPICNPrimaryEndpoint(parsed)

	query := parsed.Query()
	if isAPICN {
		query.Set("num", strings.TrimSpace(cardNo))
	} else {
		iin := strings.TrimSpace(cardNo)
		if len(iin) > 8 {
			iin = iin[:8]
		}
		if len(iin) < 6 {
			return false, nil
		}
		query.Set("iin", iin)
	}
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return false, WrapServiceError(ErrUpstream, "构建商业主源查卡请求失败", false, err)
	}
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")
	req.Header.Set("Accept", "application/hal+json,application/json")
	if key := strings.TrimSpace(cfg.PrimaryAPIKey); key != "" {
		header := strings.TrimSpace(cfg.PrimaryAPIHeader)
		if isAPICN && (header == "" || strings.EqualFold(header, "X-API-Key")) {
			header = "token"
		}
		req.Header.Set(header, key)
	}

	resp, err := s.doWithRetry(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return false, NewServiceError(ErrTimeout, "商业主源查卡超时", true)
		}
		return false, WrapServiceError(ErrUpstream, "商业主源查卡请求失败", true, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return false, NewServiceError(ErrUpstream, "商业主源接口不可用", true)
	}
	if resp.StatusCode >= 300 {
		return false, nil
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return false, WrapServiceError(ErrUpstream, "商业主源响应解析失败", false, err)
	}
	present := applyPrimaryRecord(raw, info)
	if present && isAPICN {
		assignCardSource(info, cardProviderAPICN)
	}
	return present, nil
}

func (s *CardService) fetchFromHaoshudiWeb(ctx context.Context, pageURL *url.URL, cardNo string, info *CardInfo) (bool, error) {
	if pageURL == nil {
		return false, nil
	}
	referer := haoshudiReferer(pageURL)
	ticket, err := s.fetchHaoshudiTicket(ctx, pageURL, referer)
	if err != nil || strings.TrimSpace(ticket) == "" {
		return false, err
	}

	present, err := s.fetchHaoshudiBankQuery(ctx, pageURL, referer, ticket, cardNo, info)
	if err != nil || !present {
		return present, err
	}
	_, _ = s.fetchHaoshudiArea(ctx, pageURL, referer, ticket, cardNo, info)
	assignCardSource(info, cardProviderHaoshudi)
	return true, nil
}

func haoshudiReferer(pageURL *url.URL) string {
	if pageURL == nil {
		return "https://www.haoshudi.com/yinhangka/"
	}
	scheme := strings.TrimSpace(pageURL.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	host := strings.TrimSpace(pageURL.Host)
	if host == "" {
		host = "www.haoshudi.com"
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: "/yinhangka/"}).String()
}

func haoshudiAPIURL(pageURL *url.URL, path string) string {
	scheme := "https"
	host := "www.haoshudi.com"
	if pageURL != nil {
		if strings.TrimSpace(pageURL.Scheme) != "" {
			scheme = strings.TrimSpace(pageURL.Scheme)
		}
		if strings.TrimSpace(pageURL.Host) != "" {
			host = strings.TrimSpace(pageURL.Host)
		}
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: path}).String()
}

func (s *CardService) newHaoshudiRequest(ctx context.Context, pageURL *url.URL, referer, path string, query url.Values) (*http.Request, error) {
	rawURL := haoshudiAPIURL(pageURL, path)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	parsed.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept", "application/json,text/javascript,*/*;q=0.01")
	req.Header.Set("Referer", referer)
	return req, nil
}

func (s *CardService) fetchHaoshudiTicket(ctx context.Context, pageURL *url.URL, referer string) (string, error) {
	req, err := s.newHaoshudiRequest(ctx, pageURL, referer, "/api/token/create/", url.Values{})
	if err != nil {
		return "", WrapServiceError(ErrUpstream, "构建号属地ticket请求失败", false, err)
	}
	raw, err := s.doHaoshudiJSON(req, "号属地ticket获取失败")
	if err != nil {
		return "", err
	}
	if status, ok := raw["status"].(bool); ok && !status {
		return "", nil
	}
	return firstNonEmpty(pickMap(raw, "data"), "ticket"), nil
}

func (s *CardService) fetchHaoshudiBankQuery(ctx context.Context, pageURL *url.URL, referer, ticket, cardNo string, info *CardInfo) (bool, error) {
	query := url.Values{}
	query.Set("num", strings.TrimSpace(cardNo))
	query.Set("ticket", strings.TrimSpace(ticket))
	req, err := s.newHaoshudiRequest(ctx, pageURL, referer, "/api/bank/query/", query)
	if err != nil {
		return false, WrapServiceError(ErrUpstream, "构建号属地查卡请求失败", false, err)
	}
	raw, err := s.doHaoshudiJSON(req, "号属地查卡失败")
	if err != nil {
		return false, err
	}
	if status, ok := raw["status"].(bool); ok && !status {
		return false, nil
	}
	data := pickMap(raw, "data")
	if len(data) == 0 {
		return false, nil
	}
	return applySinglePrimaryRecord(data, info), nil
}

func (s *CardService) fetchHaoshudiArea(ctx context.Context, pageURL *url.URL, referer, ticket, cardNo string, info *CardInfo) (bool, error) {
	query := url.Values{}
	query.Set("card", strings.TrimSpace(cardNo))
	query.Set("ticket", strings.TrimSpace(ticket))
	req, err := s.newHaoshudiRequest(ctx, pageURL, referer, "/api/bank/area/", query)
	if err != nil {
		return false, WrapServiceError(ErrUpstream, "构建号属地归属地请求失败", false, err)
	}
	raw, err := s.doHaoshudiJSON(req, "号属地归属地查询失败")
	if err != nil {
		return false, err
	}
	if status, ok := raw["status"].(bool); ok && !status {
		return false, nil
	}
	data := pickMap(raw, "data")
	if len(data) == 0 {
		return false, nil
	}
	return applySinglePrimaryRecord(data, info), nil
}

func (s *CardService) doHaoshudiJSON(req *http.Request, message string) (map[string]any, error) {
	resp, err := s.doWithRetry(req)
	if err != nil {
		return nil, WrapServiceError(ErrUpstream, message, true, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, NewServiceError(ErrUpstream, message, true)
	}
	if resp.StatusCode >= 300 {
		return map[string]any{}, nil
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, WrapServiceError(ErrUpstream, message, false, err)
	}
	return raw, nil
}

func applyPrimaryRecord(raw map[string]any, info *CardInfo) bool {
	if info == nil || len(raw) == 0 {
		return false
	}
	if status, ok := raw["status"].(bool); ok && !status {
		return false
	}
	records := pickPrimaryRecords(raw)
	present := false
	for _, record := range records {
		if applySinglePrimaryRecord(record, info) {
			present = true
		}
	}
	return present
}

func pickPrimaryRecords(raw map[string]any) []map[string]any {
	records := make([]map[string]any, 0, 2)
	if data, ok := raw["data"].(map[string]any); ok {
		records = append(records, data)
	}
	if embedded, ok := raw["_embedded"].(map[string]any); ok {
		if cards, ok := embedded["cards"].([]any); ok {
			for _, item := range cards {
				if card, ok := item.(map[string]any); ok {
					records = append(records, card)
				}
			}
		}
	}
	for _, key := range []string{"cards", "data", "items", "results"} {
		list, ok := raw[key].([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			if record, ok := item.(map[string]any); ok {
				records = append(records, record)
			}
		}
	}
	if len(records) == 0 {
		records = append(records, raw)
	}
	return records
}

func applySinglePrimaryRecord(record map[string]any, info *CardInfo) bool {
	if info == nil || len(record) == 0 {
		return false
	}
	present := false

	if issuer, ok := record["issuer"].(map[string]any); ok {
		if name, ok := issuer["name"].(string); ok {
			name = strings.TrimSpace(name)
			if name != "" && strings.TrimSpace(info.BankName) == "" {
				info.BankName = name
				present = true
			}
		}
	}
	if scheme, ok := record["scheme"].(map[string]any); ok {
		if name, ok := scheme["name"].(string); ok {
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "" && strings.TrimSpace(info.Brand) == "" {
				info.Brand = name
				present = true
			}
		}
	} else if scheme, ok := record["scheme"].(string); ok {
		scheme = strings.ToLower(strings.TrimSpace(scheme))
		if scheme != "" && strings.TrimSpace(info.Brand) == "" {
			info.Brand = scheme
			present = true
		}
	}
	if account, ok := record["account"].(map[string]any); ok {
		if funding, ok := account["funding"].(string); ok {
			funding = strings.ToLower(strings.TrimSpace(funding))
			if funding != "" && strings.TrimSpace(info.CardType) == "" {
				info.CardType = funding
				present = true
			}
		}
		if country, ok := account["country"].(map[string]any); ok {
			if name, ok := country["name"].(string); ok {
				name = strings.TrimSpace(name)
				if name != "" && strings.TrimSpace(info.Country) == "" {
					info.Country = name
					present = true
				}
			}
			if strings.TrimSpace(info.Country) == "" {
				if code, ok := country["code"].(string); ok {
					code = strings.TrimSpace(code)
					if code != "" {
						info.Country = code
						present = true
					}
				}
			}
		}
	}
	if bank, ok := record["bank"].(map[string]any); ok {
		if name, ok := bank["name"].(string); ok {
			name = strings.TrimSpace(name)
			if name != "" && strings.TrimSpace(info.BankName) == "" {
				info.BankName = name
				present = true
			}
		}
		if code, ok := bank["code"].(string); ok {
			code = strings.TrimSpace(code)
			if code != "" && strings.TrimSpace(info.BankCode) == "" {
				info.BankCode = code
				present = true
			}
		}
	}
	if name := firstNonEmpty(record, "bankname", "bank_name", "bankName"); name != "" {
		info.BankName = name
		present = true
	}
	if code := firstNonEmpty(record, "banken", "bank_code", "bankcode", "bankCode"); code != "" && strings.TrimSpace(info.BankCode) == "" {
		info.BankCode = code
		present = true
	}
	if province := firstNonEmpty(record, "province", "prov"); province != "" {
		info.Province = province
		present = true
	}
	if city := firstNonEmpty(record, "city", "city_name", "cityName"); city != "" {
		info.City = city
		present = true
	}
	if regionCode := firstNonEmpty(record, "code", "region_code", "regionCode", "area_code", "areaCode"); regionCode != "" {
		info.RegionCode = regionCode
		present = true
	}
	if address := firstNonEmpty(record, "address", "area"); address != "" {
		info.Address = address
		present = true
	}
	if cardName := firstNonEmpty(record, "cardname", "card_name", "cardName"); cardName != "" {
		info.CardName = cardName
		present = true
	}
	if cardType := firstNonEmpty(record, "type", "cardtype", "card_type", "cardType"); cardType != "" {
		info.CardType = cardType
		present = true
	}
	if bin := firstNonEmpty(record, "bin", "iin"); bin != "" {
		info.BIN = bin
		present = true
	}
	if length := firstNonEmpty(record, "length", "card_length", "cardLength"); length != "" {
		info.CardLength = length
		present = true
	}
	if phone := firstNonEmpty(record, "banktel", "bank_phone", "bankPhone", "tel", "phone"); phone != "" {
		info.BankPhone = phone
		present = true
	}
	if bankURL := firstNonEmpty(record, "bankurl", "bank_url", "bankUrl", "url", "website"); bankURL != "" {
		info.BankURL = bankURL
		present = true
	}
	if luhn, ok := record["luhn"].(bool); ok {
		if !strings.EqualFold(strings.TrimSpace(info.Source), CardSourceDomesticBankcard) {
			info.Valid = luhn
		}
		present = true
	}

	return present
}

func assignCardSource(info *CardInfo, source string) {
	if info == nil {
		return
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return
	}
	current := strings.TrimSpace(info.Source)
	if current == "" || strings.EqualFold(current, "unknown") {
		info.Source = source
		return
	}
	if !strings.EqualFold(current, source) && strings.TrimSpace(info.DetailSource) == "" {
		info.DetailSource = source
	}
}

func (s *CardService) fetchFromAlipay(ctx context.Context, cardNo string, info *CardInfo) (bool, error) {
	aliURL := fmt.Sprintf(
		"https://ccdcapi.alipay.com/validateAndCacheCardInfo.json?_input_charset=utf-8&cardNo=%s&cardBinCheck=true",
		cardNo,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, aliURL, nil)
	if err != nil {
		return false, WrapServiceError(ErrUpstream, "构建支付宝查卡请求失败", false, err)
	}
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")

	resp, err := s.doWithRetry(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return false, NewServiceError(ErrTimeout, "支付宝查卡超时", true)
		}
		return false, WrapServiceError(ErrUpstream, "支付宝查卡请求失败", true, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return false, NewServiceError(ErrUpstream, "支付宝查卡接口不可用", true)
	}
	if resp.StatusCode >= 300 {
		return false, nil
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return false, WrapServiceError(ErrUpstream, "支付宝查卡响应解析失败", false, err)
	}

	present := false
	if bank, ok := raw["bank"].(string); ok {
		bank = strings.TrimSpace(bank)
		if bank != "" && strings.TrimSpace(info.BankCode) == "" {
			info.BankCode = bank
			present = true
		}
		if strings.TrimSpace(info.BankName) == "" {
			if mapped := bankCodeName(bank); mapped != "" {
				info.BankName = mapped
				present = true
			}
		}
	}
	if cardType, ok := raw["cardType"].(string); ok {
		cardType = strings.TrimSpace(cardType)
		if cardType != "" && strings.TrimSpace(info.CardType) == "" {
			info.CardType = cardType
			present = true
		}
	}
	return present, nil
}

func (s *CardService) fetchFromBinlist(ctx context.Context, cardNo string, info *CardInfo) (bool, error) {
	if len(cardNo) < 8 {
		return false, nil
	}
	binURL := fmt.Sprintf("https://lookup.binlist.net/%s", cardNo[:8])
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binURL, nil)
	if err != nil {
		return false, WrapServiceError(ErrUpstream, "构建Binlist查卡请求失败", false, err)
	}
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")

	resp, err := s.doWithRetry(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return false, NewServiceError(ErrTimeout, "Binlist查卡超时", true)
		}
		return false, WrapServiceError(ErrUpstream, "Binlist查卡请求失败", true, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return false, NewServiceError(ErrUpstream, "Binlist查卡接口不可用", true)
	}
	if resp.StatusCode >= 300 {
		return false, nil
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return false, WrapServiceError(ErrUpstream, "Binlist查卡响应解析失败", false, err)
	}

	present := false
	if scheme, ok := raw["scheme"].(string); ok {
		scheme = strings.TrimSpace(scheme)
		if scheme != "" && strings.TrimSpace(info.Brand) == "" {
			info.Brand = scheme
			present = true
		}
	}
	if cardType, ok := raw["type"].(string); ok {
		cardType = strings.TrimSpace(cardType)
		if cardType != "" && strings.TrimSpace(info.CardType) == "" {
			info.CardType = cardType
			present = true
		}
	}
	if bank, ok := raw["bank"].(map[string]any); ok {
		if name, ok := bank["name"].(string); ok {
			name = strings.TrimSpace(name)
			if name != "" && strings.TrimSpace(info.BankName) == "" {
				info.BankName = name
				present = true
			}
		}
	}
	if country, ok := raw["country"].(map[string]any); ok {
		if name, ok := country["name"].(string); ok {
			name = strings.TrimSpace(name)
			if name != "" && strings.TrimSpace(info.Country) == "" {
				info.Country = name
				present = true
			}
		}
	}

	return present, nil
}
