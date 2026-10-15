package services

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-bot/internal/system"
)

var (
	trc20Pattern = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
	erc20Pattern = regexp.MustCompile(`^0x[a-fA-F0-9]{40}$`)
)

const (
	defaultTRC20TransferEndpoint = "https://apilist.tronscanapi.com/api/token_trc20/transfers"
	defaultTronScanAccountAPI    = "https://apilist.tronscanapi.com/api/account"
	defaultTronGridEndpoint      = "https://api.trongrid.io"
	defaultEtherscanEndpoint     = "https://api.etherscan.io/api"
	providerNameTronScan         = "tronscan"
	providerNameTronGrid         = "trongrid"
	providerNameEtherscan        = "etherscan"
	tronUSDTContract             = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	tronNativeTokenDecimals      = 6
	tronAddressVersionByte       = byte(0x41)
	tronAddressDecodedLength     = 25
	base58Alphabet               = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
)

type AddressValidation struct {
	Valid          bool
	Network        string
	Reason         string
	Blacklisted    bool
	BlacklistScope string
	BlacklistLabel string
}

type APIQuotaSample struct {
	Provider    string
	Endpoint    string
	StatusCode  int
	Limit       int64
	Remaining   int64
	Used        int64
	ResetAt     time.Time
	CollectedAt time.Time
}

type APIQuotaRecorder func(APIQuotaSample)

type OnChainInquiry struct {
	Address           string
	Network           string
	Active            bool
	HasBalance        bool
	Balance           string
	BalanceUnit       string
	NativeBalance     string
	NativeBalanceUnit string
	TransactionCount  int64
	CreatedAtUnix     int64
}

type AddressService struct {
	client             *http.Client
	blacklist          *addressBlacklist
	trc20Endpoint      string
	tronScanAccountAPI string
	tronGridEndpoint   string
	tronGridAPIKey     string
	etherscanEndpoint  string
	etherscanAPIKey    string
	quotaRecorder      APIQuotaRecorder
	quotaRecorderM     sync.RWMutex
}

func NewAddressService(timeout time.Duration) *AddressService {
	return &AddressService{
		client:             system.WrapHTTPClientWithTracing(&http.Client{Timeout: timeout}, "address-service"),
		blacklist:          defaultAddressBlacklist(),
		trc20Endpoint:      defaultTRC20TransferEndpoint,
		tronScanAccountAPI: defaultTronScanAccountAPI,
		tronGridEndpoint:   defaultTronGridEndpoint,
		tronGridAPIKey:     strings.TrimSpace(os.Getenv("TRONGRID_API_KEY")),
		etherscanEndpoint:  defaultEtherscanEndpoint,
		etherscanAPIKey:    strings.TrimSpace(os.Getenv("ETHERSCAN_API_KEY")),
	}
}

func (s *AddressService) SetAPIQuotaRecorder(recorder APIQuotaRecorder) {
	if s == nil {
		return
	}
	s.quotaRecorderM.Lock()
	s.quotaRecorder = recorder
	s.quotaRecorderM.Unlock()
}

func (s *AddressService) recordAPIQuota(sample APIQuotaSample) {
	if s == nil {
		return
	}
	s.quotaRecorderM.RLock()
	recorder := s.quotaRecorder
	s.quotaRecorderM.RUnlock()
	if recorder == nil {
		return
	}
	recorder(sample)
}

func (s *AddressService) Validate(address string) AddressValidation {
	addr := strings.TrimSpace(address)
	if addr == "" {
		return AddressValidation{Valid: false, Reason: "地址不能为空"}
	}
	network := ""
	switch {
	case erc20Pattern.MatchString(addr):
		network = "ERC20"
	case strings.HasPrefix(addr, "T"):
		if reason := validateTRC20AddressReason(addr); reason != "" {
			return AddressValidation{Valid: false, Network: "TRC20", Reason: reason}
		}
		network = "TRC20"
	default:
		return AddressValidation{Valid: false, Reason: "仅支持 TRC20/ERC20 地址"}
	}
	if hit, ok := s.matchBlacklist(network, addr); ok {
		label := strings.TrimSpace(hit.label)
		if label == "" {
			label = "未知来源"
		}
		reason := fmt.Sprintf("命中%s：%s", hit.scope, label)
		return AddressValidation{
			Valid:          false,
			Network:        network,
			Reason:         reason,
			Blacklisted:    true,
			BlacklistScope: hit.scope,
			BlacklistLabel: label,
		}
	}
	return AddressValidation{Valid: true, Network: network, Reason: "格式通过"}
}

func (s *AddressService) FetchTRC20Recent(address string, limit int) ([]map[string]any, error) {
	addr := strings.TrimSpace(address)
	if reason := validateTRC20AddressReason(addr); reason != "" {
		return nil, fmt.Errorf("地址格式错误")
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.fetchTRC20RecentFromTronScan(addr, limit)
	if err == nil && len(rows) > 0 {
		return rows, nil
	}
	fallbackRows, fallbackErr := s.fetchTRC20RecentFromTronGrid(addr, limit)
	if fallbackErr == nil {
		if len(fallbackRows) > 0 || err != nil {
			return fallbackRows, nil
		}
		return rows, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TronScan 最近流水失败: %v; TronGrid fallback 失败: %w", err, fallbackErr)
	}
	return rows, nil
}

func (s *AddressService) fetchTRC20RecentFromTronScan(addr string, limit int) ([]map[string]any, error) {
	endpoint := strings.TrimSpace(s.trc20Endpoint)
	if endpoint == "" {
		endpoint = defaultTRC20TransferEndpoint
	}
	pageSize := limit * 4
	switch {
	case pageSize < 20:
		pageSize = 20
	case pageSize > 100:
		pageSize = 100
	}
	out := make([]map[string]any, 0, limit)
	for start := 0; len(out) < limit && start < 300; start += pageSize {
		req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
		q := req.URL.Query()
		q.Set("limit", fmt.Sprintf("%d", pageSize))
		q.Set("start", fmt.Sprintf("%d", start))
		q.Set("relatedAddress", addr)
		req.URL.RawQuery = q.Encode()
		req.Header.Set("User-Agent", "ledger-go-bot/1.0")

		resp, err := s.client.Do(req)
		if err != nil {
			s.recordAPIQuota(APIQuotaSample{
				Provider:    providerNameTronScan,
				Endpoint:    req.URL.Host,
				StatusCode:  0,
				Limit:       -1,
				Remaining:   -1,
				Used:        -1,
				CollectedAt: time.Now(),
			})
			if len(out) > 0 {
				break
			}
			return nil, err
		}
		s.recordAPIQuota(buildAPIQuotaSample(providerNameTronScan, req.URL.Host, resp))
		if resp.StatusCode >= 300 {
			resp.Body.Close()
			if len(out) > 0 {
				break
			}
			return nil, fmt.Errorf("接口返回异常: %d", resp.StatusCode)
		}
		var raw map[string]any
		decoder := json.NewDecoder(resp.Body)
		decoder.UseNumber()
		decodeErr := decoder.Decode(&raw)
		resp.Body.Close()
		if decodeErr != nil {
			if len(out) > 0 {
				break
			}
			return nil, decodeErr
		}

		items, _ := raw["token_transfers"].([]any)
		if len(items) == 0 {
			items, _ = raw["data"].([]any)
		}
		if len(items) == 0 {
			break
		}

		for _, row := range items {
			if len(out) >= limit {
				break
			}
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			tokenInfo, _ := m["tokenInfo"].(map[string]any)
			tokenSymbol := strings.ToUpper(strings.TrimSpace(coalesceString(tokenInfo["tokenAbbr"], m["tokenAbbr"])))
			tokenID := strings.TrimSpace(coalesceString(tokenInfo["tokenId"], tokenInfo["tokenID"], m["tokenId"], m["contract_address"], m["contractAddress"]))
			if !isTRC20USDTToken(tokenSymbol, tokenID) {
				continue
			}

			triggerInfo, _ := m["trigger_info"].(map[string]any)
			triggerParams, _ := triggerInfo["parameter"].(map[string]any)
			rawAmount := valueToString(coalesceAny(m["quant"], m["amount"], triggerParams["_value"]))
			decimals := int(parseInt64FromAny(tokenInfo["tokenDecimal"], parseInt64FromAny(m["tokenDecimal"], 6)))
			if decimals <= 0 {
				decimals = 6
			}
			out = append(out, map[string]any{
				"txid":      coalesceString(m["transaction_id"], m["hash"], m["transactionHash"]),
				"amount":    formatTokenAmount(rawAmount, decimals),
				"token":     "USDT",
				"timestamp": coalesceAny(m["block_ts"], m["timestamp"]),
				"from":      coalesceString(m["from_address"], m["ownerAddress"], m["fromAddress"]),
				"to":        coalesceString(m["to_address"], m["toAddress"]),
			})
		}

		if len(items) < pageSize {
			break
		}
	}
	return out, nil
}

func (s *AddressService) fetchTRC20RecentFromTronGrid(addr string, limit int) ([]map[string]any, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(s.tronGridEndpoint), "/")
	if baseURL == "" {
		baseURL = defaultTronGridEndpoint
	}
	pageSize := limit
	switch {
	case pageSize < 20:
		pageSize = 20
	case pageSize > 100:
		pageSize = 100
	}
	out := make([]map[string]any, 0, limit)
	fingerprint := ""
	for len(out) < limit {
		txURL, _ := url.Parse(baseURL + "/v1/accounts/" + addr + "/transactions/trc20")
		query := txURL.Query()
		query.Set("limit", fmt.Sprintf("%d", pageSize))
		query.Set("only_confirmed", "true")
		query.Set("order_by", "block_timestamp,desc")
		if fingerprint != "" {
			query.Set("fingerprint", fingerprint)
		}
		txURL.RawQuery = query.Encode()

		req, _ := http.NewRequest(http.MethodGet, txURL.String(), nil)
		req.Header.Set("User-Agent", "ledger-go-bot/1.0")
		if apiKey := strings.TrimSpace(s.tronGridAPIKey); apiKey != "" {
			req.Header.Set("TRON-PRO-API-KEY", apiKey)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			s.recordAPIQuota(APIQuotaSample{
				Provider:    providerNameTronGrid,
				Endpoint:    req.URL.Host,
				StatusCode:  0,
				Limit:       -1,
				Remaining:   -1,
				Used:        -1,
				CollectedAt: time.Now(),
			})
			return nil, err
		}
		s.recordAPIQuota(buildAPIQuotaSample(providerNameTronGrid, req.URL.Host, resp))
		if resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("接口返回异常: %d", resp.StatusCode)
		}

		var raw map[string]any
		decoder := json.NewDecoder(resp.Body)
		decoder.UseNumber()
		decodeErr := decoder.Decode(&raw)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, decodeErr
		}

		items, _ := raw["data"].([]any)
		if len(items) == 0 {
			break
		}
		for _, row := range items {
			if len(out) >= limit {
				break
			}
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			tokenInfo, _ := m["token_info"].(map[string]any)
			tokenSymbol := strings.ToUpper(strings.TrimSpace(coalesceString(tokenInfo["symbol"], tokenInfo["tokenAbbr"], m["tokenAbbr"])))
			tokenID := strings.TrimSpace(coalesceString(tokenInfo["address"], tokenInfo["tokenId"], tokenInfo["tokenID"], m["contract_address"], m["contractAddress"]))
			if !isTRC20USDTToken(tokenSymbol, tokenID) {
				continue
			}
			rawAmount := valueToString(coalesceAny(m["value"], m["quant"], m["amount"]))
			decimals := int(parseInt64FromAny(tokenInfo["decimals"], parseInt64FromAny(tokenInfo["tokenDecimal"], parseInt64FromAny(m["tokenDecimal"], 6))))
			if decimals <= 0 {
				decimals = 6
			}
			out = append(out, map[string]any{
				"txid":      coalesceString(m["transaction_id"], m["hash"], m["transactionHash"]),
				"amount":    formatTokenAmount(rawAmount, decimals),
				"token":     "USDT",
				"timestamp": coalesceAny(m["block_timestamp"], m["block_ts"], m["timestamp"]),
				"from":      coalesceString(m["from"], m["from_address"], m["ownerAddress"], m["fromAddress"]),
				"to":        coalesceString(m["to"], m["to_address"], m["toAddress"]),
			})
		}

		nextFingerprint := parseTronGridFingerprint(raw)
		if len(items) < pageSize || nextFingerprint == "" || nextFingerprint == fingerprint {
			break
		}
		fingerprint = nextFingerprint
	}
	return out, nil
}

func (s *AddressService) QueryOnChainStatus(address string) (OnChainInquiry, error) {
	addr := strings.TrimSpace(address)
	if addr == "" {
		return OnChainInquiry{}, fmt.Errorf("地址不能为空")
	}
	network := detectAddressNetwork(addr)
	if network == "" {
		return OnChainInquiry{}, fmt.Errorf("仅支持 TRC20/ERC20 地址")
	}
	switch network {
	case "TRC20":
		return s.queryTRC20OnChainStatus(addr)
	case "ERC20":
		return s.queryERC20OnChainStatus(addr)
	default:
		return OnChainInquiry{}, fmt.Errorf("暂不支持网络: %s", network)
	}
}

func (s *AddressService) queryTRC20OnChainStatus(address string) (OnChainInquiry, error) {
	result := OnChainInquiry{
		Address:           address,
		Network:           "TRC20",
		Balance:           "0",
		BalanceUnit:       "USDT",
		NativeBalance:     "0",
		NativeBalanceUnit: "TRX",
	}

	accountEndpoint := strings.TrimSpace(s.tronScanAccountAPI)
	if accountEndpoint == "" {
		accountEndpoint = defaultTronScanAccountAPI
	}
	accountURL, err := url.Parse(accountEndpoint)
	if err == nil {
		query := accountURL.Query()
		query.Set("address", address)
		accountURL.RawQuery = query.Encode()

		accountPayload, accountErr := s.getJSON(providerNameTronScan, accountURL.String(), nil)
		if accountErr == nil {
			if usdtBalance, ok := parseTronScanUSDTBalance(accountPayload); ok {
				result.Balance = usdtBalance
				result.HasBalance = amountPositive(result.Balance)
			}
			result.NativeBalance = formatTokenAmount(normalizeIntegerStringFromAny(accountPayload["balance"]), tronNativeTokenDecimals)
			result.TransactionCount = parseTransactionCountFromAny(accountPayload,
				"transactions",
				"totalTransactionCount",
				"transaction_count",
				"tx_count",
			)
			if result.TransactionCount <= 0 {
				inCount := parseInt64FromAny(accountPayload["transactions_in"], 0)
				outCount := parseInt64FromAny(accountPayload["transactions_out"], 0)
				result.TransactionCount = inCount + outCount
			}
			result.CreatedAtUnix = parseUnixTimeFromAny(
				accountPayload["date_created"],
				accountPayload["create_time"],
				accountPayload["createTime"],
				accountPayload["firstTransactionTime"],
			)
			result.Active = result.TransactionCount > 0
			if !result.Active && (result.HasBalance || amountPositive(result.NativeBalance)) {
				result.Active = true
			}
			return result, nil
		}
	}

	// Fallback to TronGrid if Tronscan account API is unavailable.
	baseURL := strings.TrimRight(strings.TrimSpace(s.tronGridEndpoint), "/")
	if baseURL == "" {
		baseURL = defaultTronGridEndpoint
	}
	setTronGridAuth := func(req *http.Request) {
		apiKey := strings.TrimSpace(s.tronGridAPIKey)
		if apiKey != "" {
			req.Header.Set("TRON-PRO-API-KEY", apiKey)
		}
	}
	accountPayload, err := s.getJSON(providerNameTronGrid, baseURL+"/v1/accounts/"+address, setTronGridAuth)
	if err != nil {
		return result, err
	}
	accountRows, _ := accountPayload["data"].([]any)
	if len(accountRows) > 0 {
		accountRow, _ := accountRows[0].(map[string]any)
		result.NativeBalance = formatTokenAmount(normalizeIntegerStringFromAny(accountRow["balance"]), tronNativeTokenDecimals)
		if usdtBalance, ok := parseTronGridUSDTBalance(accountRow); ok {
			result.Balance = usdtBalance
			result.HasBalance = amountPositive(result.Balance)
		}
		result.TransactionCount = parseTransactionCountFromAny(accountRow,
			"totalTransactionCount",
			"transaction_count",
			"tx_count",
		)
		result.CreatedAtUnix = parseUnixTimeFromAny(
			accountRow["create_time"],
			accountRow["createTime"],
			accountRow["date_created"],
		)
	}

	txURL, _ := url.Parse(baseURL + "/v1/accounts/" + address + "/transactions")
	txQuery := txURL.Query()
	txQuery.Set("limit", "1")
	txQuery.Set("only_confirmed", "true")
	txQuery.Set("order_by", "block_timestamp,desc")
	txURL.RawQuery = txQuery.Encode()
	txPayload, err := s.getJSON(providerNameTronGrid, txURL.String(), setTronGridAuth)
	if err == nil {
		count := int64(countJSONArray(txPayload["data"]))
		if count > 0 {
			result.Active = true
			if result.TransactionCount <= 0 {
				result.TransactionCount = count
			}
		}
	}
	if !result.Active && result.TransactionCount > 0 {
		result.Active = true
	}
	if !result.Active && (result.HasBalance || amountPositive(result.NativeBalance)) {
		result.Active = true
	}
	return result, nil
}

func (s *AddressService) queryERC20OnChainStatus(address string) (OnChainInquiry, error) {
	result := OnChainInquiry{
		Address:     address,
		Network:     "ERC20",
		Balance:     "0",
		BalanceUnit: "WEI",
	}
	endpoint := strings.TrimSpace(s.etherscanEndpoint)
	if endpoint == "" {
		endpoint = defaultEtherscanEndpoint
	}

	balanceQuery := url.Values{}
	balanceQuery.Set("module", "account")
	balanceQuery.Set("action", "balance")
	balanceQuery.Set("tag", "latest")
	balanceQuery.Set("address", address)
	if apiKey := strings.TrimSpace(s.etherscanAPIKey); apiKey != "" {
		balanceQuery.Set("apikey", apiKey)
	}
	balancePayload, err := s.getJSON(providerNameEtherscan, endpoint+"?"+balanceQuery.Encode(), nil)
	if err != nil {
		return result, err
	}
	balanceStatus := strings.TrimSpace(valueToString(balancePayload["status"]))
	balanceMessage := strings.TrimSpace(valueToString(balancePayload["message"]))
	if balanceStatus != "1" {
		return result, fmt.Errorf("Etherscan 余额查询失败: status=%s message=%s", orDefaultString(balanceStatus, "unknown"), orDefaultString(balanceMessage, "unknown"))
	}
	result.Balance = normalizeIntegerStringFromAny(balancePayload["result"])
	result.HasBalance = integerStringPositive(result.Balance)

	txQuery := url.Values{}
	txQuery.Set("module", "account")
	txQuery.Set("action", "txlist")
	txQuery.Set("address", address)
	txQuery.Set("startblock", "0")
	txQuery.Set("endblock", "99999999")
	txQuery.Set("page", "1")
	txQuery.Set("offset", "1")
	txQuery.Set("sort", "desc")
	if apiKey := strings.TrimSpace(s.etherscanAPIKey); apiKey != "" {
		txQuery.Set("apikey", apiKey)
	}
	txPayload, err := s.getJSON(providerNameEtherscan, endpoint+"?"+txQuery.Encode(), nil)
	if err != nil {
		return result, err
	}
	txStatus := strings.TrimSpace(valueToString(txPayload["status"]))
	txMessage := strings.ToLower(strings.TrimSpace(valueToString(txPayload["message"])))
	txCount := countJSONArray(txPayload["result"])
	if txStatus == "1" {
		result.Active = txCount > 0
		return result, nil
	}
	if txStatus == "0" && strings.Contains(txMessage, "no transactions") {
		result.Active = false
		return result, nil
	}
	return result, fmt.Errorf("Etherscan 活跃度查询失败: status=%s message=%s", orDefaultString(txStatus, "unknown"), orDefaultString(strings.TrimSpace(valueToString(txPayload["message"])), "unknown"))
}

func (s *AddressService) getJSON(provider, rawURL string, decorate func(*http.Request)) (map[string]any, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("地址服务未初始化")
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")
	if decorate != nil {
		decorate(req)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		s.recordAPIQuota(APIQuotaSample{
			Provider:    provider,
			Endpoint:    req.URL.Host,
			StatusCode:  0,
			Limit:       -1,
			Remaining:   -1,
			Used:        -1,
			CollectedAt: time.Now(),
		})
		return nil, err
	}
	defer resp.Body.Close()
	s.recordAPIQuota(buildAPIQuotaSample(provider, req.URL.Host, resp))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("接口返回异常: %d", resp.StatusCode)
	}
	var payload map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func normalizeIntegerStringFromAny(value any) string {
	switch v := value.(type) {
	case nil:
		return "0"
	case string:
		return normalizeIntegerString(v)
	case json.Number:
		return normalizeIntegerString(v.String())
	case float64:
		return normalizeIntegerString(strconv.FormatFloat(v, 'f', -1, 64))
	case float32:
		return normalizeIntegerString(strconv.FormatFloat(float64(v), 'f', -1, 64))
	case int:
		return strconv.Itoa(v)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	default:
		return normalizeIntegerString(valueToString(v))
	}
}

func normalizeIntegerString(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "0"
	}
	if i, ok := new(big.Int).SetString(text, 10); ok {
		return i.String()
	}
	if f, ok := new(big.Float).SetString(text); ok {
		intPart, _ := f.Int(nil)
		if intPart == nil {
			return "0"
		}
		return intPart.String()
	}
	return "0"
}

func integerStringPositive(raw string) bool {
	i, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	return ok && i.Sign() > 0
}

func countJSONArray(value any) int {
	switch v := value.(type) {
	case []any:
		return len(v)
	default:
		return 0
	}
}

func valueToString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func orDefaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func coalesceString(values ...any) string {
	for _, v := range values {
		s, ok := v.(string)
		if ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func coalesceAny(values ...any) any {
	for _, v := range values {
		switch value := v.(type) {
		case nil:
			continue
		case string:
			if strings.TrimSpace(value) == "" {
				continue
			}
			return value
		default:
			return value
		}
	}
	return nil
}

func parseInt64FromAny(value any, fallback int64) int64 {
	switch v := value.(type) {
	case nil:
		return fallback
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case uint:
		return int64(v)
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		if v > ^uint64(0)>>1 {
			return fallback
		}
		return int64(v)
	case float32:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return int64(f)
		}
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return fallback
		}
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(text, 64); err == nil {
			return int64(f)
		}
	}
	return fallback
}

func parseTransactionCountFromAny(source map[string]any, keys ...string) int64 {
	if len(source) == 0 {
		return 0
	}
	for _, key := range keys {
		if count := parseInt64FromAny(source[key], 0); count > 0 {
			return count
		}
	}
	return 0
}

func parseUnixTimeFromAny(values ...any) int64 {
	for _, value := range values {
		unix := parseInt64FromAny(value, 0)
		if unix <= 0 {
			text := strings.TrimSpace(valueToString(value))
			if text == "" {
				continue
			}
			if parsed, err := time.Parse(time.RFC3339, text); err == nil {
				return parsed.Unix()
			}
			if parsed, err := time.Parse("2006-01-02 15:04:05", text); err == nil {
				return parsed.Unix()
			}
			continue
		}
		if unix >= 1_000_000_000_000 {
			return unix / 1000
		}
		return unix
	}
	return 0
}

func isTRC20USDTToken(symbol, tokenID string) bool {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	id := strings.TrimSpace(tokenID)
	if id != "" {
		return id == tronUSDTContract
	}
	return s == "USDT"
}

func parseTronScanUSDTBalance(payload map[string]any) (string, bool) {
	rows, _ := payload["trc20token_balances"].([]any)
	for _, row := range rows {
		item, ok := row.(map[string]any)
		if !ok {
			continue
		}
		tokenAbbr := strings.ToUpper(strings.TrimSpace(coalesceString(item["tokenAbbr"], item["tokenName"])))
		tokenID := strings.TrimSpace(coalesceString(item["tokenId"], item["tokenID"], item["contractAddress"], item["contract_address"]))
		if !isTRC20USDTToken(tokenAbbr, tokenID) {
			continue
		}
		rawBalance := normalizeIntegerStringFromAny(item["balance"])
		decimals := int(parseInt64FromAny(item["tokenDecimal"], 6))
		if decimals <= 0 {
			decimals = 6
		}
		return formatTokenAmount(rawBalance, decimals), true
	}
	return "0", false
}

func parseTronGridUSDTBalance(accountRow map[string]any) (string, bool) {
	rows, _ := accountRow["trc20"].([]any)
	for _, row := range rows {
		item, ok := row.(map[string]any)
		if !ok {
			continue
		}
		for key, value := range item {
			if strings.TrimSpace(key) != tronUSDTContract {
				continue
			}
			return formatTokenAmount(normalizeIntegerStringFromAny(value), 6), true
		}
		tokenAbbr := strings.ToUpper(strings.TrimSpace(coalesceString(item["tokenAbbr"], item["tokenName"])))
		tokenID := strings.TrimSpace(coalesceString(item["tokenId"], item["tokenID"], item["contractAddress"], item["contract_address"]))
		if !isTRC20USDTToken(tokenAbbr, tokenID) {
			continue
		}
		rawBalance := normalizeIntegerStringFromAny(coalesceAny(item["balance"], item["amount"], item["value"]))
		decimals := int(parseInt64FromAny(item["tokenDecimal"], 6))
		if decimals <= 0 {
			decimals = 6
		}
		return formatTokenAmount(rawBalance, decimals), true
	}
	return "0", false
}

func parseTronGridFingerprint(payload map[string]any) string {
	meta, _ := payload["meta"].(map[string]any)
	return strings.TrimSpace(coalesceString(meta["fingerprint"]))
}

func formatTokenAmount(raw string, decimals int) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "0"
	}
	negative := false
	if strings.HasPrefix(text, "-") {
		negative = true
		text = strings.TrimSpace(strings.TrimPrefix(text, "-"))
	}
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return "0"
	}
	if decimals <= 0 {
		if negative && value.Sign() > 0 {
			return "-" + value.String()
		}
		return value.String()
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(value, scale, remainder)
	if remainder.Sign() == 0 {
		if negative && quotient.Sign() > 0 {
			return "-" + quotient.String()
		}
		return quotient.String()
	}
	frac := remainder.String()
	if len(frac) < decimals {
		frac = strings.Repeat("0", decimals-len(frac)) + frac
	}
	frac = strings.TrimRight(frac, "0")
	amount := quotient.String() + "." + frac
	if negative && amount != "0" {
		return "-" + amount
	}
	return amount
}

func amountPositive(raw string) bool {
	text := strings.TrimSpace(raw)
	if text == "" {
		return false
	}
	value, _, err := big.ParseFloat(text, 10, 256, big.ToNearestEven)
	if err != nil {
		return false
	}
	return value.Sign() > 0
}

func detectAddressNetwork(address string) string {
	switch {
	case erc20Pattern.MatchString(address):
		return "ERC20"
	case validateTRC20AddressReason(address) == "":
		return "TRC20"
	default:
		return ""
	}
}

func validateTRC20AddressReason(address string) string {
	addr := strings.TrimSpace(address)
	if addr == "" {
		return "地址不能为空"
	}
	if !strings.HasPrefix(addr, "T") || len(addr) != 34 {
		return "TRC20 地址长度错误"
	}
	if !trc20Pattern.MatchString(addr) {
		return "TRC20 地址包含非法 Base58 字符"
	}
	decoded, ok := decodeBase58(addr)
	if !ok {
		return "TRC20 地址包含非法 Base58 字符"
	}
	if len(decoded) != tronAddressDecodedLength {
		return "TRC20 地址长度错误"
	}
	if decoded[0] != tronAddressVersionByte {
		return "TRC20 地址前缀错误"
	}
	payload := decoded[:tronAddressDecodedLength-4]
	checksum := decoded[tronAddressDecodedLength-4:]
	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])
	for i := 0; i < 4; i++ {
		if checksum[i] != second[i] {
			return "TRC20 地址校验和错误"
		}
	}
	return ""
}

func decodeBase58(raw string) ([]byte, bool) {
	value := big.NewInt(0)
	base := big.NewInt(58)
	for _, r := range raw {
		idx := strings.IndexRune(base58Alphabet, r)
		if idx < 0 {
			return nil, false
		}
		value.Mul(value, base)
		value.Add(value, big.NewInt(int64(idx)))
	}
	decoded := value.Bytes()
	leadingZeros := 0
	for leadingZeros < len(raw) && raw[leadingZeros] == '1' {
		leadingZeros++
	}
	if leadingZeros > 0 {
		decoded = append(make([]byte, leadingZeros), decoded...)
	}
	return decoded, true
}

func (s *AddressService) matchBlacklist(network, address string) (blacklistHit, bool) {
	if s == nil || s.blacklist == nil {
		return blacklistHit{}, false
	}
	return s.blacklist.Match(network, address)
}

func buildAPIQuotaSample(provider, endpoint string, resp *http.Response) APIQuotaSample {
	collectedAt := time.Now()
	sample := APIQuotaSample{
		Provider:    strings.ToLower(strings.TrimSpace(provider)),
		Endpoint:    strings.TrimSpace(endpoint),
		Limit:       -1,
		Remaining:   -1,
		Used:        -1,
		CollectedAt: collectedAt,
	}
	if resp == nil {
		return sample
	}
	sample.StatusCode = resp.StatusCode
	sample.Limit = readRateLimitHeaderInt(resp.Header,
		"X-RateLimit-Limit",
		"X-Ratelimit-Limit",
		"RateLimit-Limit",
		"X-RateLimit-Limit-Day",
		"X-RateLimit-Limit-Minute",
	)
	sample.Remaining = readRateLimitHeaderInt(resp.Header,
		"X-RateLimit-Remaining",
		"X-Ratelimit-Remaining",
		"RateLimit-Remaining",
		"X-RateLimit-Remaining-Day",
		"X-RateLimit-Remaining-Minute",
	)
	sample.Used = readRateLimitHeaderInt(resp.Header,
		"X-RateLimit-Used",
		"X-Ratelimit-Used",
		"RateLimit-Used",
	)
	if sample.Used < 0 && sample.Limit >= 0 && sample.Remaining >= 0 && sample.Remaining <= sample.Limit {
		sample.Used = sample.Limit - sample.Remaining
	}
	sample.ResetAt = parseRateLimitReset(collectedAt, resp.Header,
		"X-RateLimit-Reset",
		"X-Ratelimit-Reset",
		"RateLimit-Reset",
	)
	return sample
}

func readRateLimitHeaderInt(header http.Header, keys ...string) int64 {
	if header == nil {
		return -1
	}
	for _, key := range keys {
		raw := strings.TrimSpace(header.Get(key))
		if raw == "" {
			continue
		}
		raw = strings.TrimSpace(strings.Split(raw, ",")[0])
		if raw == "" {
			continue
		}
		if number, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return number
		}
		if decimal, err := strconv.ParseFloat(raw, 64); err == nil {
			return int64(decimal)
		}
	}
	return -1
}

func parseRateLimitReset(now time.Time, header http.Header, keys ...string) time.Time {
	if header == nil {
		return time.Time{}
	}
	for _, key := range keys {
		raw := strings.TrimSpace(header.Get(key))
		if raw == "" {
			continue
		}
		raw = strings.TrimSpace(strings.Split(raw, ",")[0])
		if raw == "" {
			continue
		}
		if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
			switch {
			case unix >= 1_000_000_000_000:
				return time.UnixMilli(unix)
			case unix >= 1_000_000_000:
				return time.Unix(unix, 0)
			case unix > 0:
				return now.Add(time.Duration(unix) * time.Second)
			default:
				return time.Time{}
			}
		}
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed
		}
		if parsed, err := http.ParseTime(raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
