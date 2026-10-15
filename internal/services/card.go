package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go-bot/internal/system"
	"go-bot/internal/utils"
)

type CardInfo struct {
	CardNo       string
	Valid        bool
	BankCode     string
	BankName     string
	Brand        string
	CardType     string
	Country      string
	Province     string
	City         string
	RegionCode   string
	Address      string
	CardName     string
	BankPhone    string
	BankURL      string
	BIN          string
	CardLength   string
	Source       string
	DetailSource string
	Fallback     bool
}

func CardValidationStatus(info CardInfo) string {
	if strings.EqualFold(strings.TrimSpace(info.Source), CardSourceDomesticBankcard) {
		if info.Valid {
			return "通过（国内BIN/长度）"
		}
		return "未通过（国内BIN/长度）"
	}
	if info.Valid {
		return "通过（Luhn）"
	}
	return "未通过（Luhn）"
}

func CardSourceLabel(info CardInfo) string {
	base := cardSourceLabel(strings.TrimSpace(info.Source), info.Fallback)
	detail := cardSourceLabel(strings.TrimSpace(info.DetailSource), false)
	if detail != "" && detail != "未知" && !strings.EqualFold(base, detail) {
		if base == "" || base == "未知" {
			return detail
		}
		return base + " + " + detail
	}
	if base == "" {
		return "未知"
	}
	return base
}

func cardSourceLabel(source string, fallback bool) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case CardSourceDomesticBankcard:
		return "本地国内BIN库"
	case cardProviderAPICN:
		return "API.CN银行卡归属地"
	case cardProviderHaoshudi:
		return "号属地网页查询"
	case cardProviderPrimary:
		return "商业主源"
	case cardProviderBinlist:
		if fallback {
			return "Binlist备用通道"
		}
		return "Binlist"
	case cardProviderAlipay:
		if fallback {
			return "支付宝备用通道"
		}
		return "支付宝"
	case "local_heuristic":
		return "本地卡组织规则"
	case "local_map":
		return "本地银行代码映射"
	case "":
		return "未知"
	default:
		return source
	}
}

func CardLocationLabel(info CardInfo) string {
	province := strings.TrimSpace(info.Province)
	city := strings.TrimSpace(info.City)
	switch {
	case province != "" && city != "":
		return province + " " + city
	case province != "":
		return province
	case city != "":
		return city
	default:
		return strings.TrimSpace(info.Address)
	}
}

type BranchAPIConfig struct {
	URL     string
	Token   string
	Header  string
	Timeout time.Duration
}

type BranchLookupResult struct {
	CNAPSCode  string
	BankName   string
	BranchName string
	Province   string
	City       string
	Message    string
	Source     string
}

type CardService struct {
	client       *http.Client
	branchClient *http.Client
	branchAPI    BranchAPIConfig
	retryMax     int
	backoff      time.Duration
	sleep        func(time.Duration)
	now          func() time.Time

	routing    CardRoutingConfig
	cacheMu    sync.RWMutex
	cache      map[string]cardCacheEntry
	cacheOrder []string
}

func NewCardService(timeout time.Duration, branchAPI BranchAPIConfig) *CardService {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if branchAPI.Timeout <= 0 {
		branchAPI.Timeout = timeout
	}
	branchAPI.URL = strings.TrimSpace(branchAPI.URL)
	branchAPI.Token = strings.TrimSpace(branchAPI.Token)
	branchAPI.Header = strings.TrimSpace(branchAPI.Header)
	if branchAPI.Header == "" {
		branchAPI.Header = "Authorization"
	}
	return NewCardServiceWithClients(
		system.WrapHTTPClientWithTracing(&http.Client{Timeout: timeout}, "card-service"),
		system.WrapHTTPClientWithTracing(&http.Client{Timeout: branchAPI.Timeout}, "branch-code-service"),
		branchAPI,
	)
}

func NewCardServiceWithClients(client *http.Client, branchClient *http.Client, branchAPI BranchAPIConfig) *CardService {
	return NewCardServiceWithPolicy(client, branchClient, branchAPI, 1, 200*time.Millisecond)
}

func NewCardServiceWithPolicy(client *http.Client, branchClient *http.Client, branchAPI BranchAPIConfig, retryMax int, backoff time.Duration) *CardService {
	return NewCardServiceWithPolicyAndRouting(client, branchClient, branchAPI, retryMax, backoff, DefaultCardRoutingConfig())
}

func NewCardServiceWithPolicyAndRouting(
	client *http.Client,
	branchClient *http.Client,
	branchAPI BranchAPIConfig,
	retryMax int,
	backoff time.Duration,
	routing CardRoutingConfig,
) *CardService {
	if client == nil {
		client = system.WrapHTTPClientWithTracing(&http.Client{Timeout: 10 * time.Second}, "card-service")
	}
	if branchClient == nil {
		timeout := branchAPI.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		branchClient = system.WrapHTTPClientWithTracing(&http.Client{Timeout: timeout}, "branch-code-service")
	}
	branchAPI.URL = strings.TrimSpace(branchAPI.URL)
	branchAPI.Token = strings.TrimSpace(branchAPI.Token)
	branchAPI.Header = strings.TrimSpace(branchAPI.Header)
	if branchAPI.Header == "" {
		branchAPI.Header = "Authorization"
	}
	if retryMax < 0 {
		retryMax = 0
	}
	if backoff <= 0 {
		backoff = 200 * time.Millisecond
	}
	routing = normalizeCardRoutingConfig(routing)
	return &CardService{
		client:       client,
		branchClient: branchClient,
		branchAPI:    branchAPI,
		retryMax:     retryMax,
		backoff:      backoff,
		sleep:        time.Sleep,
		now:          time.Now,
		routing:      routing,
		cache:        make(map[string]cardCacheEntry),
	}
}

func (s *CardService) QueryCard(cardNo string) CardInfo {
	info, _ := s.QueryCardWithStatus(cardNo)
	return info
}

func (s *CardService) QueryCardWithStatus(cardNo string) (CardInfo, *ServiceError) {
	no := digitsOnly(cardNo)
	if len(no) < 12 || len(no) > 19 {
		return CardInfo{CardNo: no, Valid: false}, NewServiceError(ErrInvalidInput, "银行卡号长度不正确", false)
	}
	valid := utils.LuhnCheck(no)
	info := CardInfo{CardNo: no, Valid: valid}
	if applyDomesticBankcard(no, &info) {
		if s.primaryCardDetailEnabled() {
			if cached, ok := s.getCachedCardInfo(no, valid); ok && strings.TrimSpace(cached.DetailSource) != "" {
				normalizeCardLocation(&cached)
				cached.Valid = info.Valid
				return cached, nil
			}
			s.enrichCardWithPrimaryDetail(no, &info)
		}
		normalizeCardLocation(&info)
		if strings.TrimSpace(info.DetailSource) != "" {
			s.setCachedCardInfo(no, info)
		}
		return info, nil
	}
	if cached, ok := s.getCachedCardInfo(no, valid); ok {
		normalizeCardLocation(&cached)
		return cached, nil
	}

	routedInfo, serr := s.queryCardWithRouting(no, valid)
	normalizeCardLocation(&routedInfo)
	if serr == nil && isCardDataPresent(routedInfo) {
		s.setCachedCardInfo(no, routedInfo)
	}
	return routedInfo, serr
}

func (s *CardService) doWithRetry(req *http.Request) (*http.Response, error) {
	attempts := s.retryMax + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		cloned := req.Clone(req.Context())
		resp, err := s.client.Do(cloned)
		if err != nil {
			lastErr = err
			if i < attempts-1 {
				s.sleepWithBackoff(i)
				continue
			}
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			if i < attempts-1 {
				_ = resp.Body.Close()
				s.sleepWithBackoff(i)
				continue
			}
		}
		return resp, nil
	}
	return nil, lastErr
}

func (s *CardService) sleepWithBackoff(attempt int) {
	if s == nil || s.sleep == nil {
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

func (s *CardService) QueryBranchCode(cardNo string) string {
	info := s.QueryCard(cardNo)
	if info.CardNo == "" {
		return "银行卡号格式不正确。"
	}

	headCode := headOfficeCNAPS(info.BankCode, info.BankName)
	lines := []string{
		"联行号查询结果：",
		fmt.Sprintf("银行卡号：%s", info.CardNo),
		fmt.Sprintf("银行名称：%s", nz(info.BankName)),
		fmt.Sprintf("银行代码：%s", nz(info.BankCode)),
		fmt.Sprintf("卡组织：%s", nz(info.Brand)),
		fmt.Sprintf("卡类型：%s", nz(info.CardType)),
		fmt.Sprintf("国家：%s", nz(info.Country)),
	}
	if headCode != "" {
		lines = append(lines, fmt.Sprintf("总行联行号参考：%s", headCode))
	} else {
		lines = append(lines, "总行联行号参考：未命中内置库")
	}

	third, thirdErr := s.queryBranchFromProvider(info)
	if third != nil && strings.TrimSpace(third.CNAPSCode) != "" {
		lines = append(lines,
			"第三方支行联行号：",
			fmt.Sprintf("联行号：%s", third.CNAPSCode),
			fmt.Sprintf("支行：%s", nz(third.BranchName)),
			fmt.Sprintf("所属银行：%s", nz(orDefault(third.BankName, info.BankName))),
			fmt.Sprintf("地区：%s %s", nz(third.Province), nz(third.City)),
			fmt.Sprintf("来源：%s", nz(third.Source)),
		)
		if strings.TrimSpace(third.Message) != "" {
			lines = append(lines, "接口备注："+third.Message)
		}
		lines = append(lines, "说明：已命中第三方支行级联行号结果。")
		return strings.Join(lines, "\n")
	}

	lines = append(lines,
		"说明：支行级联行号通常需要“银行卡号+城市+支行名”并接入银行专用数据源。",
		"当前结果为内置总行联行号参考；如已配置第三方接口，将自动优先返回支行级结果。",
	)
	if strings.TrimSpace(s.branchAPI.URL) != "" {
		if thirdErr != nil {
			lines = append(lines, "第三方接口状态：不可用，已自动回退内置结果。")
		} else {
			lines = append(lines, "第三方接口状态：已启用，但本次未返回可用支行联行号。")
		}
	}
	return strings.Join(lines, "\n")
}

func (s *CardService) queryBranchFromProvider(info CardInfo) (*BranchLookupResult, error) {
	endpoint := strings.TrimSpace(s.branchAPI.URL)
	if endpoint == "" {
		return nil, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := parsed.Query()
	q.Set("card_no", info.CardNo)
	q.Set("bank_code", info.BankCode)
	q.Set("bank_name", info.BankName)
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")
	if token := strings.TrimSpace(s.branchAPI.Token); token != "" {
		headerName := strings.TrimSpace(s.branchAPI.Header)
		if strings.EqualFold(headerName, "Authorization") && !strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = "Bearer " + token
		}
		req.Header.Set(headerName, token)
	}

	resp, err := s.branchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("状态码异常: %d", resp.StatusCode)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	payload := raw
	if nested := pickMap(raw, "data", "result", "payload"); len(nested) > 0 {
		payload = nested
	}

	out := &BranchLookupResult{
		CNAPSCode:  firstNonEmpty(payload, "cnaps_code", "cnaps", "branch_code", "pay_code", "line_number", "bank_line_code"),
		BankName:   firstNonEmpty(payload, "bank_name", "bank", "name"),
		BranchName: firstNonEmpty(payload, "branch_name", "sub_branch", "branch", "outlet_name"),
		Province:   firstNonEmpty(payload, "province", "prov"),
		City:       firstNonEmpty(payload, "city", "city_name"),
		Message:    firstNonEmpty(raw, "message", "msg", "detail"),
		Source:     parsed.Host,
	}
	if out.CNAPSCode == "" {
		return nil, fmt.Errorf("未返回支行联行号字段")
	}
	return out, nil
}

func isCardDataPresent(info CardInfo) bool {
	if strings.TrimSpace(info.BankCode) != "" {
		return true
	}
	if strings.TrimSpace(info.BankName) != "" {
		return true
	}
	if strings.TrimSpace(info.Brand) != "" {
		return true
	}
	if strings.TrimSpace(info.CardType) != "" {
		return true
	}
	if strings.TrimSpace(info.Country) != "" {
		return true
	}
	if strings.TrimSpace(info.Province) != "" {
		return true
	}
	if strings.TrimSpace(info.City) != "" {
		return true
	}
	if strings.TrimSpace(info.Address) != "" {
		return true
	}
	if strings.TrimSpace(info.CardName) != "" {
		return true
	}
	if strings.TrimSpace(info.BankPhone) != "" {
		return true
	}
	if strings.TrimSpace(info.BankURL) != "" {
		return true
	}
	if strings.TrimSpace(info.BIN) != "" {
		return true
	}
	return false
}

func firstNonEmpty(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := raw[key]; ok {
			switch v := val.(type) {
			case string:
				if text := strings.TrimSpace(v); text != "" {
					return text
				}
			case float64:
				text := strings.TrimSpace(fmt.Sprintf("%.0f", v))
				if text != "" && text != "0" {
					return text
				}
			case int:
				text := strings.TrimSpace(fmt.Sprintf("%d", v))
				if text != "" && text != "0" {
					return text
				}
			}
		}
	}
	return ""
}

func pickMap(raw map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		v, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := v.(type) {
		case map[string]any:
			return typed
		case []any:
			for _, item := range typed {
				if m, ok := item.(map[string]any); ok {
					return m
				}
			}
		}
	}
	return nil
}

func digitsOnly(raw string) string {
	b := strings.Builder{}
	for _, ch := range raw {
		if ch >= '0' && ch <= '9' {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

func nz(v string) string {
	if strings.TrimSpace(v) == "" {
		return "未知"
	}
	return v
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func bankCodeName(code string) string {
	m := map[string]string{
		"ICBC":   "中国工商银行",
		"ABC":    "中国农业银行",
		"BOC":    "中国银行",
		"CCB":    "中国建设银行",
		"COMM":   "交通银行",
		"CMB":    "招商银行",
		"CMBC":   "中国民生银行",
		"CIB":    "兴业银行",
		"SPDB":   "浦发银行",
		"CITIC":  "中信银行",
		"CEB":    "中国光大银行",
		"HXBANK": "华夏银行",
		"PAB":    "平安银行",
		"SDB":    "平安银行",
		"GDB":    "广发银行",
		"PSBC":   "中国邮政储蓄银行",
		"BJBANK": "北京银行",
		"BOB":    "北京银行",
		"SHBANK": "上海银行",
		"NBBANK": "宁波银行",
		"NJCB":   "南京银行",
		"BEA":    "东亚银行",
	}
	name := m[strings.ToUpper(strings.TrimSpace(code))]
	return strings.TrimSpace(name)
}

func headOfficeCNAPS(bankCode, bankName string) string {
	codeMap := map[string]string{
		"ICBC":   "102100099996",
		"ABC":    "103100000026",
		"BOC":    "104100000004",
		"CCB":    "105100000017",
		"COMM":   "301290000007",
		"CMB":    "308584000013",
		"CMBC":   "305100000013",
		"CIB":    "309391000011",
		"SPDB":   "310290000013",
		"CITIC":  "302100011000",
		"CEB":    "303100000006",
		"HXBANK": "304100040000",
		"PAB":    "307584007998",
		"SDB":    "307584007998",
		"GDB":    "306581000003",
		"PSBC":   "403100000004",
		"BJBANK": "313100000013",
		"BOB":    "313100000013",
		"SHBANK": "313290000017",
	}
	if v := codeMap[strings.ToUpper(strings.TrimSpace(bankCode))]; v != "" {
		return v
	}
	nameMap := map[string]string{
		"工商":   "102100099996",
		"农业":   "103100000026",
		"中国银行": "104100000004",
		"建设":   "105100000017",
		"交通":   "301290000007",
		"招商":   "308584000013",
		"民生":   "305100000013",
		"兴业":   "309391000011",
		"浦发":   "310290000013",
		"中信":   "302100011000",
		"光大":   "303100000006",
		"华夏":   "304100040000",
		"平安":   "307584007998",
		"广发":   "306581000003",
		"邮政":   "403100000004",
		"北京银行": "313100000013",
		"上海银行": "313290000017",
	}
	for key, val := range nameMap {
		if strings.Contains(bankName, key) {
			return val
		}
	}
	return ""
}

func applyLocalCardHeuristic(info *CardInfo) bool {
	if info == nil || info.Valid {
		return false
	}
	if strings.TrimSpace(info.Brand) == "" {
		info.Brand = inferCardBrandByIIN(info.CardNo)
	}
	if !isCardDataPresent(*info) {
		return false
	}
	if strings.TrimSpace(info.Source) == "" || strings.EqualFold(strings.TrimSpace(info.Source), "unknown") {
		info.Source = "local_heuristic"
	}
	info.Fallback = true
	return true
}

func inferCardBrandByIIN(cardNo string) string {
	no := strings.TrimSpace(cardNo)
	if len(no) < 2 {
		return ""
	}

	if strings.HasPrefix(no, "4") {
		return "visa"
	}
	if len(no) >= 2 {
		p2 := no[:2]
		if p2 >= "51" && p2 <= "55" {
			return "mastercard"
		}
		if p2 == "34" || p2 == "37" {
			return "amex"
		}
		if p2 == "62" {
			return "unionpay"
		}
		if p2 == "36" || p2 == "38" || p2 == "39" {
			return "diners"
		}
		if p2 == "50" || (p2 >= "56" && p2 <= "69") {
			return "maestro"
		}
		if p2 == "65" {
			return "discover"
		}
	}
	if len(no) >= 4 {
		p4 := no[:4]
		if p4 >= "2221" && p4 <= "2720" {
			return "mastercard"
		}
		if p4 >= "3528" && p4 <= "3589" {
			return "jcb"
		}
		if p4 == "6011" {
			return "discover"
		}
	}
	if len(no) >= 3 {
		p3 := no[:3]
		if p3 >= "300" && p3 <= "305" {
			return "diners"
		}
		if p3 >= "644" && p3 <= "649" {
			return "discover"
		}
	}
	return ""
}
