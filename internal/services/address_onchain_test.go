package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestControlPoint313OnChainInquiry(t *testing.T) {
	t.Run("控制点 3.1.3 功能验证", func(t *testing.T) {
		const (
			tronAddress     = tronUSDTContract
			ercActiveAddr   = "0x742d35Cc6634C0532925a3b844Bc454e4438f44e"
			ercInactiveAddr = "0x1111111111111111111111111111111111111111"
		)

		tronServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/account":
				if r.URL.Query().Get("address") != tronAddress {
					t.Fatalf("Tronscan 查询参数异常: %s", r.URL.RawQuery)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"trc20token_balances": []map[string]any{
						{
							"tokenAbbr":    "USDT",
							"tokenDecimal": 6,
							"balance":      "12345678",
						},
					},
					"transactions": 2,
				})
			case "/v1/accounts/" + tronAddress:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{
						{
							"address": tronAddress,
							"balance": "1500000",
						},
					},
				})
			case "/v1/accounts/" + tronAddress + "/transactions":
				if r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("only_confirmed") != "true" {
					t.Fatalf("TronGrid 查询参数异常: %s", r.URL.RawQuery)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{
						{"txID": "tron-tx-1"},
					},
				})
			default:
				t.Fatalf("TronGrid 请求路径异常: %s", r.URL.Path)
			}
		}))
		defer tronServer.Close()

		ethServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api" {
				t.Fatalf("Etherscan 请求路径异常: %s", r.URL.Path)
			}
			query := r.URL.Query()
			if query.Get("module") != "account" {
				t.Fatalf("Etherscan module 参数异常: %s", query.Get("module"))
			}
			if query.Get("apikey") != "test-etherscan-key" {
				t.Fatalf("Etherscan apikey 参数异常: %s", query.Get("apikey"))
			}

			action := query.Get("action")
			address := strings.ToLower(query.Get("address"))
			switch action {
			case "balance":
				switch address {
				case strings.ToLower(ercActiveAddr):
					_ = json.NewEncoder(w).Encode(map[string]any{
						"status":  "1",
						"message": "OK",
						"result":  "3200000000000000000",
					})
				case strings.ToLower(ercInactiveAddr):
					_ = json.NewEncoder(w).Encode(map[string]any{
						"status":  "1",
						"message": "OK",
						"result":  "0",
					})
				default:
					t.Fatalf("Etherscan balance 查询地址异常: %s", query.Get("address"))
				}
			case "txlist":
				switch address {
				case strings.ToLower(ercActiveAddr):
					_ = json.NewEncoder(w).Encode(map[string]any{
						"status":  "1",
						"message": "OK",
						"result": []map[string]any{
							{"hash": "0xabc"},
						},
					})
				case strings.ToLower(ercInactiveAddr):
					_ = json.NewEncoder(w).Encode(map[string]any{
						"status":  "0",
						"message": "No transactions found",
						"result":  []any{},
					})
				default:
					t.Fatalf("Etherscan txlist 查询地址异常: %s", query.Get("address"))
				}
			default:
				t.Fatalf("Etherscan action 参数异常: %s", action)
			}
		}))
		defer ethServer.Close()

		svc := NewAddressService(time.Second)
		svc.client = tronServer.Client()
		svc.tronScanAccountAPI = tronServer.URL + "/api/account"
		svc.tronGridEndpoint = tronServer.URL
		svc.etherscanEndpoint = ethServer.URL + "/api"
		svc.etherscanAPIKey = "test-etherscan-key"

		tronResult, err := svc.QueryOnChainStatus(tronAddress)
		if err != nil {
			t.Fatalf("TRC20 链上核验失败: %v", err)
		}
		if tronResult.Network != "TRC20" {
			t.Fatalf("TRC20 网络识别错误: %s", tronResult.Network)
		}
		if !tronResult.Active {
			t.Fatalf("TRC20 地址应识别为活跃")
		}
		if !tronResult.HasBalance {
			t.Fatalf("TRC20 地址应识别为有余额")
		}
		if tronResult.Balance != "12.345678" || tronResult.BalanceUnit != "USDT" {
			t.Fatalf("TRC20 余额解析错误: %+v", tronResult)
		}

		ercActiveResult, err := svc.QueryOnChainStatus(ercActiveAddr)
		if err != nil {
			t.Fatalf("ERC20 活跃地址核验失败: %v", err)
		}
		if ercActiveResult.Network != "ERC20" {
			t.Fatalf("ERC20 网络识别错误: %s", ercActiveResult.Network)
		}
		if !ercActiveResult.Active || !ercActiveResult.HasBalance {
			t.Fatalf("ERC20 活跃地址应返回 active=true 且 has_balance=true: %+v", ercActiveResult)
		}
		if ercActiveResult.Balance != "3200000000000000000" || ercActiveResult.BalanceUnit != "WEI" {
			t.Fatalf("ERC20 活跃地址余额解析错误: %+v", ercActiveResult)
		}

		ercInactiveResult, err := svc.QueryOnChainStatus(ercInactiveAddr)
		if err != nil {
			t.Fatalf("ERC20 非活跃地址核验失败: %v", err)
		}
		if ercInactiveResult.Active {
			t.Fatalf("ERC20 非活跃地址不应识别为活跃: %+v", ercInactiveResult)
		}
		if ercInactiveResult.HasBalance {
			t.Fatalf("ERC20 非活跃地址不应识别为有余额: %+v", ercInactiveResult)
		}
		if ercInactiveResult.Balance != "0" || ercInactiveResult.BalanceUnit != "WEI" {
			t.Fatalf("ERC20 非活跃地址余额解析错误: %+v", ercInactiveResult)
		}
	})
}

func TestQueryTRC20OnChainStatusFallbackUsesUSDTBalance(t *testing.T) {
	const tronAddress = tronUSDTContract
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/account":
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "tronscan unavailable",
			})
		case "/v1/accounts/" + tronAddress:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"address":               tronAddress,
						"balance":               "102839738",
						"totalTransactionCount": 2468,
						"create_time":           1666074819000,
						"trc20": []map[string]any{
							{tronUSDTContract: "240888812"},
						},
					},
				},
			})
		case "/v1/accounts/" + tronAddress + "/transactions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []any{},
			})
		default:
			t.Fatalf("请求路径异常: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc := NewAddressService(time.Second)
	svc.client = server.Client()
	svc.tronScanAccountAPI = server.URL + "/api/account"
	svc.tronGridEndpoint = server.URL

	result, err := svc.QueryOnChainStatus(tronAddress)
	if err != nil {
		t.Fatalf("fallback 查询失败: %v", err)
	}
	if result.Network != "TRC20" {
		t.Fatalf("网络类型错误: %s", result.Network)
	}
	if result.Balance != "240.888812" || result.BalanceUnit != "USDT" {
		t.Fatalf("USDT 余额解析错误: %+v", result)
	}
	if result.NativeBalance != "102.839738" || result.NativeBalanceUnit != "TRX" {
		t.Fatalf("TRX 余额解析错误: %+v", result)
	}
	if !result.HasBalance || !result.Active {
		t.Fatalf("fallback 场景应识别为活跃且有余额: %+v", result)
	}
	if result.TransactionCount != 2468 {
		t.Fatalf("交易次数解析错误: %+v", result)
	}
	if result.CreatedAtUnix != 1666074819 {
		t.Fatalf("注册时间解析错误: %+v", result)
	}
}
