package admin

import (
	"strings"
	"testing"
)

func TestTGWebAppRateBoardUsesCurrencyPairTemplate(t *testing.T) {
	body := renderTGWebAppRateBoardText("全部", "7.35", []string{
		"1) 7.35 CNY/USDT  广深商行",
		"2) 7.35 CNY/USDT  金大福U商",
		"3) 7.35 CNY/USDT  正旺商行",
	})
	for _, want := range []string{
		"OKX 实时汇率",
		"法币市场：人民币 (CNY)",
		"Top10均价：7.35 CNY/USDT",
		"今日 Okx 商家实时交易汇率 Top10:",
		"1) 7.35 CNY/USDT  广深商行",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("WebApp 汇率卡片缺少片段 %q", want)
		}
	}
	if strings.Contains(body, "今日Okx商家实时交易汇率top10") {
		t.Fatalf("WebApp 汇率卡片不应保留旧 top10 文案: %s", body)
	}
}
