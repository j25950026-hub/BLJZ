package utils

import (
	"math"
	"testing"
)

func TestParseLedgerText(t *testing.T) {
	row, ok := ParseLedgerText("+100u#备注")
	if !ok || row.EntryType != "deposit" || row.Currency != "USDT" {
		t.Fatalf("解析入款失败: %+v", row)
	}
	row2, ok := ParseLedgerText("-100")
	if !ok || row2.EntryType != "payout" || row2.Currency != "" {
		t.Fatalf("解析下发失败: %+v", row2)
	}
	row3, ok := ParseLedgerText("下发-100#修正")
	if !ok || row3.Amount >= 0 {
		t.Fatalf("解析修正失败: %+v", row3)
	}
	row4, ok := ParseLedgerText("-500U")
	if !ok || row4.EntryType != "payout" || row4.Amount != 500 || row4.Currency != "USDT" {
		t.Fatalf("解析 -500U 失败: %+v", row4)
	}
	row5, ok := ParseLedgerText("－500Ｕ")
	if !ok || row5.EntryType != "payout" || row5.Amount != 500 || row5.Currency != "USDT" {
		t.Fatalf("解析全角下发失败: %+v", row5)
	}
	row6, ok := ParseLedgerText("−500usdt#补下发")
	if !ok || row6.EntryType != "payout" || row6.Amount != 500 || row6.Currency != "USDT" || row6.Note != "补下发" {
		t.Fatalf("解析 Unicode 减号失败: %+v", row6)
	}
	row7, ok := ParseLedgerText("+500/4#单笔汇率")
	if !ok || row7.EntryType != "deposit" || row7.Amount != 500 || row7.RateOverride == nil || *row7.RateOverride != 4 {
		t.Fatalf("解析单笔入款汇率失败: %+v", row7)
	}
	row8, ok := ParseLedgerText("下发100/1.5u#单笔下发汇率")
	if !ok || row8.EntryType != "payout" || row8.Amount != 100 || row8.Currency != "USDT" || row8.RateOverride == nil || *row8.RateOverride != 1.5 {
		t.Fatalf("解析单笔下发汇率失败: %+v", row8)
	}
	row9, ok := ParseLedgerText("+231480*0.93/124#复合入款")
	if !ok || row9.EntryType != "deposit" || row9.Amount != 231480 || row9.RateOverride == nil || *row9.RateOverride != 124 || row9.Note != "复合入款" {
		t.Fatalf("解析复合入款格式失败: %+v", row9)
	}
	if _, ok := ParseLedgerText("--500U"); ok {
		t.Fatalf("双减号不应自动纠正通过")
	}
	if _, ok := ParseLedgerText("++100"); ok {
		t.Fatalf("双加号不应自动纠正通过")
	}
	if _, ok := ParseLedgerText("下发--500U"); ok {
		t.Fatalf("前缀双减号不应自动纠正通过")
	}
	if _, ok := ParseLedgerText("-500USD"); ok {
		t.Fatalf("非法单位不应通过解析")
	}
	if _, ok := ParseLedgerText("+500/abc"); ok {
		t.Fatalf("非法单笔汇率不应通过解析")
	}
	if _, ok := ParseLedgerText("-1/26000"); ok {
		t.Fatalf("无前缀负数除法不应解析为下发")
	}
}

func TestLooksLikeLedgerText(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{text: "-500U", want: true},
		{text: "--500U", want: true},
		{text: "－500Ｕ", want: true},
		{text: "+500/4", want: true},
		{text: "+500/abc", want: true},
		{text: "下发 500usdt#补下发", want: true},
		{text: "-1+2", want: false},
		{text: "设置下发地址 0x123", want: false},
	}
	for _, tc := range cases {
		if got := LooksLikeLedgerText(tc.text); got != tc.want {
			t.Fatalf("LooksLikeLedgerText(%q)=%v want=%v", tc.text, got, tc.want)
		}
	}
}

func TestMath(t *testing.T) {
	v, ok := ParseMathExpression("1+2*3")
	if !ok || v != 7 {
		t.Fatalf("计算失败: %v", v)
	}
	if _, ok := ParseMathExpression("120"); ok {
		t.Fatalf("纯数字不应被当作计算器表达式")
	}
	if _, ok := ParseMathExpression("__import__('os')"); ok {
		t.Fatalf("非法表达式未被拦截")
	}
	v, ok = ParseMathExpression("-1/26000")
	if !ok || math.Abs(v-(-1.0/26000.0)) > 1e-12 {
		t.Fatalf("负数除法应作为计算器表达式: got=%v ok=%v", v, ok)
	}
}

func TestLuhn(t *testing.T) {
	if !LuhnCheck("4539578763621486") {
		t.Fatalf("Luhn 校验失败")
	}
	if LuhnCheck("4539578763621487") {
		t.Fatalf("Luhn 误判")
	}
}

func TestBuildMessageLink(t *testing.T) {
	cases := []struct {
		name      string
		chatID    int64
		messageID int
		want      string
	}{
		{
			name:      "supergroup chat id",
			chatID:    -1004756417513,
			messageID: 838354,
			want:      "https://t.me/c/4756417513/838354",
		},
		{
			name:      "non supergroup chat id",
			chatID:    -5266232385,
			messageID: 14,
			want:      "",
		},
		{
			name:      "invalid message id",
			chatID:    -1004756417513,
			messageID: 0,
			want:      "",
		},
		{
			name:      "private chat id",
			chatID:    6253397539,
			messageID: 10,
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildMessageLink(tc.chatID, tc.messageID)
			if got != tc.want {
				t.Fatalf("BuildMessageLink() = %q, want %q", got, tc.want)
			}
		})
	}
}
