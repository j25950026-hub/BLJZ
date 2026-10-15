package services

import (
	"strings"
	"testing"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

func TestControlPoint315QRCodeParsing(t *testing.T) {
	t.Run("控制点 3.1.5 功能验证", func(t *testing.T) {
		svc := NewAddressService(time.Second)
		payload := "https://wallet.example/pay?to=0xD90E2F925DA726B50C4ED8D0FB90AD053324F31B&backup=0x742d35Cc6634C0532925a3b844Bc454e4438f44e"
		img := mustEncodeQRCodePNG(t, payload)

		hits, err := svc.ScanQRCodeImageForAddresses(img)
		if err != nil {
			t.Fatalf("二维码解析失败: %v", err)
		}
		if len(hits) != 2 {
			t.Fatalf("应识别到 2 个地址，got=%d", len(hits))
		}

		hitByAddress := make(map[string]QRAddressDetection, len(hits))
		for _, hit := range hits {
			hitByAddress[strings.ToLower(hit.Address)] = hit
		}

		blacklistedKey := strings.ToLower("0xD90E2F925DA726B50C4ED8D0FB90AD053324F31B")
		blacklistedHit, ok := hitByAddress[blacklistedKey]
		if !ok {
			t.Fatalf("缺少黑名单地址识别结果")
		}
		if blacklistedHit.Validation.Valid {
			t.Fatalf("黑名单地址不应通过验证")
		}
		if !blacklistedHit.Validation.Blacklisted {
			t.Fatalf("黑名单地址应被标记")
		}
		if blacklistedHit.Validation.BlacklistScope != screeningScopeOFAC {
			t.Fatalf("黑名单类型错误，got=%s", blacklistedHit.Validation.BlacklistScope)
		}

		normalKey := strings.ToLower("0x742d35Cc6634C0532925a3b844Bc454e4438f44e")
		normalHit, ok := hitByAddress[normalKey]
		if !ok {
			t.Fatalf("缺少正常地址识别结果")
		}
		if !normalHit.Validation.Valid {
			t.Fatalf("正常地址不应被误拦截，reason=%s", normalHit.Validation.Reason)
		}
		if normalHit.Validation.Blacklisted {
			t.Fatalf("正常地址不应命中黑名单")
		}
	})

	t.Run("二维码无地址时返回空结果", func(t *testing.T) {
		svc := NewAddressService(time.Second)
		img := mustEncodeQRCodePNG(t, "hello, ledgerbot")
		hits, err := svc.ScanQRCodeImageForAddresses(img)
		if err != nil {
			t.Fatalf("二维码解析失败: %v", err)
		}
		if len(hits) != 0 {
			t.Fatalf("不应识别到地址，got=%d", len(hits))
		}
	})

	t.Run("非法图片输入返回错误", func(t *testing.T) {
		svc := NewAddressService(time.Second)
		if _, err := svc.ScanQRCodeImageForAddresses([]byte("not-an-image")); err == nil {
			t.Fatalf("非法图片输入应返回错误")
		}
	})
}

func mustEncodeQRCodePNG(t *testing.T, payload string) []byte {
	t.Helper()
	imageBytes, err := qrcode.Encode(payload, qrcode.Medium, 256)
	if err != nil {
		t.Fatalf("生成二维码失败: %v", err)
	}
	return imageBytes
}
