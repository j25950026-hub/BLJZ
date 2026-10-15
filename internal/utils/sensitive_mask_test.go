package utils

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestMaskSensitiveText(t *testing.T) {
	raw := "card=4539578763621486 erc20=0xd90e2f925da726b50c4ed8d0fb90ad053324f31b trc20=TAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	masked := MaskSensitiveText(raw)

	checks := []string{
		"card=453957******1486",
		"erc20=0xd90e********************************f31b",
		"trc20=TAAAAA************************AAAA",
	}
	for _, want := range checks {
		if !strings.Contains(masked, want) {
			t.Fatalf("脱敏结果缺少 %q, got=%s", want, masked)
		}
	}
	for _, leak := range []string{"4539578763621486", "0xd90e2f925da726b50c4ed8d0fb90ad053324f31b", "TAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if strings.Contains(masked, leak) {
			t.Fatalf("敏感信息未脱敏: %s", leak)
		}
	}
}

func TestMaskSensitiveTextSkipsNonCardNumber(t *testing.T) {
	raw := "chat_id=-1002003004005"
	if got := MaskSensitiveText(raw); got != raw {
		t.Fatalf("非银行卡数字不应被脱敏: %s", got)
	}
}

func TestNewSensitiveMaskWriter(t *testing.T) {
	var sink bytes.Buffer
	writer := NewSensitiveMaskWriter(&sink)
	if _, err := io.WriteString(writer, "card=4539578763621486\n"); err != nil {
		t.Fatalf("写入脱敏日志失败: %v", err)
	}
	if got := sink.String(); strings.Contains(got, "4539578763621486") {
		t.Fatalf("writer 未脱敏敏感信息: %s", got)
	}
	if !strings.Contains(sink.String(), "453957******1486") {
		t.Fatalf("writer 脱敏格式错误: %s", sink.String())
	}
}
