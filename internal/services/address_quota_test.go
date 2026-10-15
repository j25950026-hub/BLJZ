package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestAddressServiceRecordsAPIQuota(t *testing.T) {
	resetAt := time.Unix(1_760_000_000, 0)
	const address = tronUSDTContract
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/token_trc20/transfers" {
			t.Fatalf("请求路径错误: %s", r.URL.Path)
		}
		if r.URL.Query().Get("relatedAddress") != address {
			t.Fatalf("查询地址参数错误: %s", r.URL.Query().Get("relatedAddress"))
		}
		w.Header().Set("X-RateLimit-Limit", "1000")
		w.Header().Set("X-RateLimit-Remaining", "80")
		w.Header().Set("X-RateLimit-Reset", "1760000000")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token_transfers": []map[string]any{
				{
					"transaction_id": "tx-1",
					"quant":          "10000000",
					"block_ts":       1700000000,
					"from_address":   address,
					"to_address":     "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb",
					"tokenInfo": map[string]any{
						"tokenAbbr":    "USDT",
						"tokenDecimal": 6,
					},
				},
			},
		})
	}))
	defer server.Close()

	svc := NewAddressService(time.Second)
	svc.client = server.Client()
	svc.trc20Endpoint = server.URL + "/api/token_trc20/transfers"
	var gotSamples []APIQuotaSample
	svc.SetAPIQuotaRecorder(func(sample APIQuotaSample) {
		gotSamples = append(gotSamples, sample)
	})

	rows, err := svc.FetchTRC20Recent(address, 1)
	if err != nil {
		t.Fatalf("查询 TRC20 交易失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("返回交易条数错误: %d", len(rows))
	}
	if rows[0]["amount"] != "10" {
		t.Fatalf("交易金额解析错误: %+v", rows[0])
	}
	if rows[0]["token"] != "USDT" {
		t.Fatalf("交易代币解析错误: %+v", rows[0])
	}

	if len(gotSamples) != 1 {
		t.Fatalf("应记录一次 API 额度采样: %d", len(gotSamples))
	}
	sample := gotSamples[0]
	if sample.Provider != providerNameTronScan {
		t.Fatalf("provider 错误: %s", sample.Provider)
	}
	if sample.Endpoint == "" {
		t.Fatalf("endpoint 错误: %s", sample.Endpoint)
	}
	if sample.StatusCode != 200 {
		t.Fatalf("状态码错误: %d", sample.StatusCode)
	}
	if sample.Limit != 1000 || sample.Remaining != 80 || sample.Used != 920 {
		t.Fatalf("额度字段错误: %+v", sample)
	}
	if sample.ResetAt.Unix() != resetAt.Unix() {
		t.Fatalf("重置时间解析错误: %s", sample.ResetAt)
	}
}

func TestBuildAPIQuotaSampleWithoutHeaders(t *testing.T) {
	resp := &http.Response{
		StatusCode: 204,
		Header:     make(http.Header),
	}
	sample := buildAPIQuotaSample(providerNameTronScan, "apilist.tronscanapi.com", resp)
	if sample.Provider != providerNameTronScan {
		t.Fatalf("provider 错误: %s", sample.Provider)
	}
	if sample.Limit != -1 || sample.Remaining != -1 || sample.Used != -1 {
		t.Fatalf("无配额头时应保持 unknown: %+v", sample)
	}
}

func TestFetchTRC20RecentFiltersUSDTWithPagination(t *testing.T) {
	const address = tronUSDTContract
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/token_trc20/transfers" {
			t.Fatalf("请求路径错误: %s", r.URL.Path)
		}
		if r.URL.Query().Get("relatedAddress") != address {
			t.Fatalf("查询地址参数错误: %s", r.URL.Query().Get("relatedAddress"))
		}
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit <= 0 {
			t.Fatalf("分页 limit 参数错误: %s", r.URL.Query().Get("limit"))
		}
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		callCount++
		switch start {
		case 0:
			rows := make([]map[string]any, 0, limit)
			for i := 0; i < limit; i++ {
				rows = append(rows, map[string]any{
					"transaction_id": "noise-tx",
					"quant":          "1000000",
					"from_address":   "TNOISEFROM",
					"to_address":     "TNOISETO",
					"tokenInfo": map[string]any{
						"tokenAbbr":    "USDC",
						"tokenDecimal": 6,
					},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token_transfers": rows,
			})
		case limit:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token_transfers": []map[string]any{
					{
						"transaction_id": "usdt-tx-1",
						"quant":          "1234567",
						"block_ts":       1700000000000,
						"from_address":   "TFROM1111111111111111111111111111111",
						"to_address":     address,
						"tokenInfo": map[string]any{
							"tokenAbbr":    "USDT",
							"tokenDecimal": 6,
						},
					},
					{
						"transaction_id": "usdt-tx-2",
						"quant":          "3000000",
						"block_ts":       1700000100000,
						"from_address":   address,
						"to_address":     "TTO22222222222222222222222222222222",
						"tokenInfo": map[string]any{
							"tokenId":      tronUSDTContract,
							"tokenDecimal": 6,
						},
					},
				},
			})
		default:
			t.Fatalf("分页 start 参数异常: %d", start)
		}
	}))
	defer server.Close()

	svc := NewAddressService(time.Second)
	svc.client = server.Client()
	svc.trc20Endpoint = server.URL + "/api/token_trc20/transfers"

	rows, err := svc.FetchTRC20Recent(address, 2)
	if err != nil {
		t.Fatalf("查询 TRC20 最近交易失败: %v", err)
	}
	if callCount < 2 {
		t.Fatalf("应至少分页请求两次，实际=%d", callCount)
	}
	if len(rows) != 2 {
		t.Fatalf("USDT 交易筛选后条数错误: %d", len(rows))
	}
	if rows[0]["txid"] != "usdt-tx-1" || rows[0]["amount"] != "1.234567" || rows[0]["token"] != "USDT" {
		t.Fatalf("第1笔 USDT 交易解析错误: %+v", rows[0])
	}
	if rows[1]["txid"] != "usdt-tx-2" || rows[1]["amount"] != "3" || rows[1]["token"] != "USDT" {
		t.Fatalf("第2笔 USDT 交易解析错误: %+v", rows[1])
	}
}

func TestFetchTRC20RecentFallsBackToTronGridWhenTronScanFails(t *testing.T) {
	const address = tronUSDTContract
	var tronGridCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token_trc20/transfers":
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "rate limited"})
		case "/v1/accounts/" + address + "/transactions/trc20":
			tronGridCalled = true
			if r.Header.Get("TRON-PRO-API-KEY") != "test-trongrid-key" {
				t.Fatalf("TronGrid API Key 未写入请求头")
			}
			query := r.URL.Query()
			if query.Get("only_confirmed") != "true" || query.Get("order_by") != "block_timestamp,desc" {
				t.Fatalf("TronGrid 查询参数异常: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"transaction_id":  "trongrid-usdt-1",
						"value":           "14000000",
						"block_timestamp": 1700000000000,
						"from":            address,
						"to":              "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb",
						"token_info": map[string]any{
							"symbol":   "USDT",
							"address":  tronUSDTContract,
							"decimals": 6,
						},
					},
				},
				"success": true,
			})
		default:
			t.Fatalf("请求路径异常: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc := NewAddressService(time.Second)
	svc.client = server.Client()
	svc.trc20Endpoint = server.URL + "/api/token_trc20/transfers"
	svc.tronGridEndpoint = server.URL
	svc.tronGridAPIKey = "test-trongrid-key"

	rows, err := svc.FetchTRC20Recent(address, 1)
	if err != nil {
		t.Fatalf("TronScan 失败后应 fallback 到 TronGrid: %v", err)
	}
	if !tronGridCalled {
		t.Fatalf("应调用 TronGrid fallback")
	}
	if len(rows) != 1 {
		t.Fatalf("fallback 后 USDT 交易条数错误: %d", len(rows))
	}
	if rows[0]["txid"] != "trongrid-usdt-1" || rows[0]["amount"] != "14" || rows[0]["token"] != "USDT" {
		t.Fatalf("TronGrid USDT 交易解析错误: %+v", rows[0])
	}
}

func TestFetchTRC20RecentFallsBackToTronGridWhenTronScanHasNoUSDTRows(t *testing.T) {
	const address = tronUSDTContract
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token_trc20/transfers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token_transfers": []map[string]any{
					{
						"transaction_id": "noise-usdc",
						"quant":          "1000000",
						"tokenInfo": map[string]any{
							"tokenId":      "TXYZNotUSDT111111111111111111111111",
							"tokenAbbr":    "USDC",
							"tokenDecimal": 6,
						},
					},
				},
			})
		case "/v1/accounts/" + address + "/transactions/trc20":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"transaction_id":  "trongrid-usdt-2",
						"value":           "2500000",
						"block_timestamp": 1700000300000,
						"from":            "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb",
						"to":              address,
						"token_info": map[string]any{
							"symbol":   "USDT",
							"address":  tronUSDTContract,
							"decimals": 6,
						},
					},
				},
				"success": true,
			})
		default:
			t.Fatalf("请求路径异常: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc := NewAddressService(time.Second)
	svc.client = server.Client()
	svc.trc20Endpoint = server.URL + "/api/token_trc20/transfers"
	svc.tronGridEndpoint = server.URL

	rows, err := svc.FetchTRC20Recent(address, 1)
	if err != nil {
		t.Fatalf("TronScan 无 USDT 时应 fallback 到 TronGrid: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("fallback 后 USDT 交易条数错误: %d", len(rows))
	}
	if rows[0]["txid"] != "trongrid-usdt-2" || rows[0]["amount"] != "2.5" || rows[0]["from"] == "" || rows[0]["to"] == "" {
		t.Fatalf("TronGrid USDT 交易解析错误: %+v", rows[0])
	}
}
