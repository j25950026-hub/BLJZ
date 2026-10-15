package services

import (
	"strings"
	"testing"
	"time"
)

func TestControlPoint312SanctionsScreening(t *testing.T) {
	t.Run("控制点 3.1.2 功能验证", func(t *testing.T) {
		svc := NewAddressService(time.Second)

		ofacResult := svc.Validate("0xD90E2F925DA726B50C4ED8D0FB90AD053324F31B")
		if ofacResult.Valid {
			t.Fatalf("OFAC 地址应被拦截")
		}
		if !ofacResult.Blacklisted {
			t.Fatalf("OFAC 地址应标记为黑名单")
		}
		if ofacResult.Network != "ERC20" {
			t.Fatalf("网络识别异常，got=%s", ofacResult.Network)
		}
		if ofacResult.BlacklistScope != screeningScopeOFAC {
			t.Fatalf("黑名单类型异常，got=%s", ofacResult.BlacklistScope)
		}
		if !strings.Contains(ofacResult.Reason, "OFAC") {
			t.Fatalf("命中原因应包含 OFAC，got=%s", ofacResult.Reason)
		}

		scamResult := svc.Validate("T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb")
		if scamResult.Valid {
			t.Fatalf("诈骗地址应被拦截")
		}
		if !scamResult.Blacklisted {
			t.Fatalf("诈骗地址应标记为黑名单")
		}
		if scamResult.Network != "TRC20" {
			t.Fatalf("网络识别异常，got=%s", scamResult.Network)
		}
		if scamResult.BlacklistScope != screeningScopeScam {
			t.Fatalf("黑名单类型异常，got=%s", scamResult.BlacklistScope)
		}

		normalResult := svc.Validate("0x742d35Cc6634C0532925a3b844Bc454e4438f44e")
		if !normalResult.Valid {
			t.Fatalf("普通地址不应被误拦截，reason=%s", normalResult.Reason)
		}
		if normalResult.Blacklisted {
			t.Fatalf("普通地址不应命中黑名单")
		}
	})
}

func TestValidateTRC20UsesBase58Checksum(t *testing.T) {
	svc := NewAddressService(time.Second)

	validResult := svc.Validate(tronUSDTContract)
	if !validResult.Valid {
		t.Fatalf("合法 TRC20 地址不应被拒绝，reason=%s", validResult.Reason)
	}
	if validResult.Network != "TRC20" {
		t.Fatalf("网络识别异常，got=%s", validResult.Network)
	}

	invalidChecksumAddress := tronUSDTContract[:len(tronUSDTContract)-1] + "u"
	invalidResult := svc.Validate(invalidChecksumAddress)
	if invalidResult.Valid {
		t.Fatalf("checksum 错误的 TRC20 地址不应通过")
	}
	if invalidResult.Network != "TRC20" {
		t.Fatalf("checksum 错误地址仍应识别为 TRC20，got=%s", invalidResult.Network)
	}
	if !strings.Contains(invalidResult.Reason, "校验和") {
		t.Fatalf("错误原因应指出校验和问题，got=%s", invalidResult.Reason)
	}
}

func TestTRC20USDTTokenRequiresOfficialContractWhenIDPresent(t *testing.T) {
	if !isTRC20USDTToken("", tronUSDTContract) {
		t.Fatalf("官方 USDT 合约应被识别")
	}
	if !isTRC20USDTToken("USDT", "") {
		t.Fatalf("缺少合约字段时允许按 USDT 符号兜底")
	}
	if isTRC20USDTToken("USDT", "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq") {
		t.Fatalf("带非官方合约地址的 USDT 符号不应被识别为官方 USDT")
	}
}
