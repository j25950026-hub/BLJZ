package bot

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go-bot/internal/services"
)

func TestBuildTRC20QueryReplyUI(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	status := services.OnChainInquiry{
		Address:           address,
		Network:           "TRC20",
		Active:            true,
		HasBalance:        true,
		Balance:           "240.888812",
		BalanceUnit:       "USDT",
		NativeBalance:     "102.839738",
		NativeBalanceUnit: "TRX",
		TransactionCount:  2468,
		CreatedAtUnix:     1666103619,
	}
	rows := []map[string]any{
		{
			"txid":      "e2d43763c8df5c4be8d5dc40990a8d5f6ca2d6772b45f5f6bcc84ab4d60b3ea1",
			"amount":    "0.03",
			"timestamp": int64(1697443284),
			"from":      "TTREVwUpftpNUa7LxCqF3R2ZCPVBxC5ZwP",
			"to":        address,
		},
		{
			"txid":      "83668ac563a57bb30f43f81ec41ed8ef6f63a25e9c6e88f59fd991a732648950",
			"amount":    "2717",
			"timestamp": int64(1697443269),
			"from":      address,
			"to":        "TTRIZwPqLf1cLQ9Hn5fPJ6g2QKrZqV9W4x",
		},
	}

	reply := buildTRC20QueryReply(status, rows, nil, time.UTC)
	mustContain := []string{
		"🏦 *地址档案 ｜ TRC20*",
		"🟢 状态：活跃  ·  📊 交易数：2468笔",
		"⏳ 注册时间：`2022-10-18 14:33:39`",
		"🔗 *钱包地址*（点击复制）",
		"`TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B`",
		"💵 *240\\.888812* `USDT`",
		"💎 *102\\.839738* `TRX`",
		"🧾 *最近 5 笔 USDT 流水*",
		"1\\. 🟢 *\\+0\\.03 U* ｜ 🕒 `10-16 08:01`",
		"2\\. 🔴 *\\-2717 U* ｜ 🕒 `10-16 08:01`",
		"📤 从：`TTREVwUpftpNUa7LxCqF3R2ZCPVBxC5ZwP`",
		"📥 至：`TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B`",
		"🔗 哈希：`e2d43763c8...0b3ea1`",
		"🔗 哈希：`83668ac563...648950`",
	}
	for _, keyword := range mustContain {
		if !strings.Contains(reply, keyword) {
			t.Fatalf("回执格式缺少字段 %q\nreply:\n%s", keyword, reply)
		}
	}
}

func TestBuildTRC20QueryReplyNoRecentRows(t *testing.T) {
	status := services.OnChainInquiry{
		Address:     "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B",
		Network:     "TRC20",
		Balance:     "0",
		BalanceUnit: "USDT",
	}

	reply := buildTRC20QueryReply(status, nil, errors.New("network"), time.UTC)
	if !strings.Contains(reply, "暂无法获取最近流水，请稍后重试。") {
		t.Fatalf("异常场景提示缺失: %s", reply)
	}

	reply = buildTRC20QueryReply(status, nil, nil, time.UTC)
	if !strings.Contains(reply, "暂无 USDT 流水。") {
		t.Fatalf("空结果提示缺失: %s", reply)
	}
}

func TestTRC20QueryActionMarkup(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	markup := trc20QueryActionMarkup(address)
	dataSet := flattenInlineCallbackData(markup)
	expected := []string{
		buildTRC20QueryCallbackData(trc20ActionRefresh, address),
		buildTRC20QueryCallbackData(trc20ActionChart, address),
		buildTRC20QueryCallbackData(trc20ActionMenu, address),
	}
	for _, item := range expected {
		if !dataSet[item] {
			t.Fatalf("缺少回调按钮: %s", item)
		}
		if len(item) > 64 {
			t.Fatalf("回调数据超过 Telegram 限制 64 字节: len=%d data=%s", len(item), item)
		}
	}
	removed := []string{
		buildTRC20QueryCallbackData(trc20ActionWatch, address),
		buildTRC20QueryCallbackData(trc20ActionRisk, address),
	}
	for _, item := range removed {
		if dataSet[item] {
			t.Fatalf("地址分析卡片不应再包含回调按钮: %s", item)
		}
	}
}

func TestParseTRC20QueryCallbackData(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	raw := buildTRC20QueryCallbackData(trc20ActionRefresh, address)
	action, gotAddr, ok := parseTRC20QueryCallbackData(raw)
	if !ok {
		t.Fatalf("回调解析失败: %s", raw)
	}
	if action != trc20ActionRefresh || gotAddr != address {
		t.Fatalf("回调解析异常: action=%s addr=%s", action, gotAddr)
	}
	if _, _, ok := parseTRC20QueryCallbackData("tq:broken"); ok {
		t.Fatalf("非法回调不应解析成功")
	}
}
