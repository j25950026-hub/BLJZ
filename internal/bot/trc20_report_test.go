package bot

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"go-bot/internal/services"
)

func TestBuildTRC20AnalysisReportCoversAMLSectionsAndTopFlows(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	status := services.OnChainInquiry{
		Address:           address,
		Network:           "TRC20",
		Active:            true,
		HasBalance:        true,
		Balance:           "2.000000",
		BalanceUnit:       "USDT",
		NativeBalance:     "12.500000",
		NativeBalanceUnit: "TRX",
		TransactionCount:  36,
		CreatedAtUnix:     1700000000,
	}
	validation := services.AddressValidation{Valid: true, Network: "TRC20", Reason: "格式通过"}
	rows := []map[string]any{
		{
			"txid":      "tx-in-1",
			"amount":    "100",
			"timestamp": int64(1700000100),
			"from":      "TTSource1111111111111111111111111111",
			"to":        address,
		},
		{
			"txid":      "tx-out-1",
			"amount":    "98",
			"timestamp": int64(1700000500),
			"from":      address,
			"to":        "TTDest111111111111111111111111111111",
		},
		{
			"txid":      "tx-in-2",
			"amount":    "5000",
			"timestamp": int64(1700000600),
			"from":      "TTWhale22222222222222222222222222222",
			"to":        address,
		},
		{
			"txid":      "tx-out-2",
			"amount":    "4900",
			"timestamp": int64(1700000900),
			"from":      address,
			"to":        "TTDest111111111111111111111111111111",
		},
	}

	report := buildTRC20AnalysisReport(status, validation, rows, nil, time.UTC, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"TRC20 地址链上分析报告",
		"1. 地址基本信息",
		"2. 资产情况",
		"3. 交易行为",
		"4. 资金来源分析",
		"Top资金来源",
		"TTWhale22222222222222222222222222222：5000 U / 1 笔",
		"TTSource1111111111111111111111111111：100 U / 1 笔",
		"5. 资金流向分析",
		"Top资金去向",
		"TTDest111111111111111111111111111111：4998 U / 2 笔",
		"6. 地址画像",
		"7. 行为模式分析",
		"快进快出：疑似",
		"低余额中转：疑似",
		"8. 风险评估（AML）",
		"洗钱风险：高",
		"是否属于交易所：数据不足",
		"是否属于博彩资金：数据不足",
		"9. 网络关系分析",
		"10. 总结",
		"风险评分：",
		"是否建议继续交易：暂缓并人工复核",
		"是否建议列入监控名单：是",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("报告缺少 %q\n完整报告:\n%s", want, report)
		}
	}
	if strings.Contains(report, "属于博彩平台") || strings.Contains(report, "属于交易所热钱包") {
		t.Fatalf("报告不应把未验证标签写成事实:\n%s", report)
	}
}

func TestBuildTRC20AnalysisReportHandlesInsufficientData(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	status := services.OnChainInquiry{Address: address, Network: "TRC20", Balance: "0"}
	validation := services.AddressValidation{Valid: true, Network: "TRC20", Reason: "格式通过"}

	report := buildTRC20AnalysisReport(status, validation, nil, nil, time.UTC, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"数据状态：数据不足",
		"最近交易：暂无 USDT 流水",
		"资金来源：最近样本不足，无法判断",
		"资金去向：最近样本不足，无法判断",
		"个人钱包：数据不足",
		"洗钱风险：无法判断",
		"风险评分：数据不足",
		"地址画像：画像数据不足",
		"主要用途：数据不足",
		"是否建议继续交易：人工复核",
		"是否建议列入监控名单：是",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("数据不足报告缺少 %q\n完整报告:\n%s", want, report)
		}
	}
	for _, forbidden := range []string{
		"资金来源：最近样本未发现入账",
		"资金去向：最近样本未发现出账",
		"洗钱风险：低",
		"风险等级：★★☆☆☆ 低",
		"风险评分：20/100",
		"地址画像：普通收付款地址",
		"主要用途：收付款",
		"个人钱包：中",
		"是否存在洗钱特征：未发现",
		"是否建议继续交易：谨慎",
		"是否建议列入监控名单：否",
	} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("数据不足报告不应包含确定性结论 %q\n完整报告:\n%s", forbidden, report)
		}
	}
}

func TestBuildTRC20AnalysisReportAvoidsDefinitiveRiskConclusionWhenRecentFetchFails(t *testing.T) {
	address := "TNe6Evnktm8z1DogburnULydKK9Yrw7Wvp"
	status := services.OnChainInquiry{
		Address:           address,
		Network:           "TRC20",
		Active:            true,
		HasBalance:        true,
		Balance:           "11392.994029",
		BalanceUnit:       "USDT",
		NativeBalance:     "151.18242",
		NativeBalanceUnit: "TRX",
		TransactionCount:  36817,
		CreatedAtUnix:     1756923213,
	}
	validation := services.AddressValidation{Valid: true, Network: "TRC20", Reason: "格式通过"}

	report := buildTRC20AnalysisReport(status, validation, nil, errors.New("upstream unavailable"), time.UTC, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"数据状态：数据不足",
		"最近交易：暂无法获取最近流水",
		"资金来源：流水获取失败，无法判断",
		"资金去向：流水获取失败，无法判断",
		"个人钱包：数据不足",
		"洗钱风险：无法判断",
		"是否存在洗钱特征：无法判断",
		"风险等级：数据不足 无法判断",
		"风险评分：数据不足",
		"地址画像：画像数据不足",
		"主要用途：数据不足",
		"是否建议继续交易：人工复核",
		"是否建议列入监控名单：是",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("流水失败报告缺少 %q\n完整报告:\n%s", want, report)
		}
	}
	for _, forbidden := range []string{
		"洗钱风险：低",
		"风险等级：★★☆☆☆ 低",
		"地址画像：普通收付款地址",
		"主要用途：收付款",
		"个人钱包：中",
		"是否建议继续交易：谨慎",
		"是否存在洗钱特征：未发现",
		"是否建议列入监控名单：否",
	} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("流水失败报告不应包含确定性结论 %q\n完整报告:\n%s", forbidden, report)
		}
	}
	if regexp.MustCompile(`风险评分：[0-9]+/100`).MatchString(report) {
		t.Fatalf("流水失败报告不应输出数字风险分\n完整报告:\n%s", report)
	}
}

func TestBuildTRC20AnalysisReportKeepsBlacklistHighRiskWithNoRecentRows(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	status := services.OnChainInquiry{Address: address, Network: "TRC20", Balance: "0"}
	validation := services.AddressValidation{
		Valid:          false,
		Network:        "TRC20",
		Reason:         "命中已知诈骗名单：测试标签",
		Blacklisted:    true,
		BlacklistScope: "已知诈骗名单",
		BlacklistLabel: "测试标签",
	}

	report := buildTRC20AnalysisReport(status, validation, nil, nil, time.UTC, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"地址有效性：命中黑名单",
		"风险等级：★★★★★ 极高",
		"风险评分：85/100",
		"是否存在洗钱特征：疑似",
		"是否建议继续交易：不建议",
		"是否建议列入监控名单：是",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("黑名单报告缺少 %q\n完整报告:\n%s", want, report)
		}
	}
}

func TestBuildTRC20AnalysisReportDoesNotTreatGenericBlacklistAsAMLFact(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	status := services.OnChainInquiry{Address: address, Network: "TRC20", Balance: "0"}
	validation := services.AddressValidation{
		Valid:          false,
		Network:        "TRC20",
		Reason:         "命中已知诈骗名单：测试诈骗标签",
		Blacklisted:    true,
		BlacklistScope: "已知诈骗名单",
		BlacklistLabel: "测试诈骗标签",
	}

	report := buildTRC20AnalysisReport(status, validation, nil, nil, time.UTC, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	if strings.Contains(report, "是否存在洗钱特征：命中") {
		t.Fatalf("非 AML 黑名单不应写成洗钱特征命中:\n%s", report)
	}
	if !strings.Contains(report, "是否存在洗钱特征：疑似") {
		t.Fatalf("非 AML 黑名单应保持需复核语义:\n%s", report)
	}
}

func TestBuildTRC20AnalysisReportUsesBehaviorWordingForOTCProfile(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	status := services.OnChainInquiry{Address: address, Network: "TRC20", Balance: "12"}
	validation := services.AddressValidation{Valid: true, Network: "TRC20", Reason: "格式通过"}
	rows := []map[string]any{
		{"amount": "100", "timestamp": int64(1700000100), "from": "TSource11111111111111111111111111111", "to": address},
		{"amount": "200", "timestamp": int64(1700000200), "from": "TSource22222222222222222222222222222", "to": address},
		{"amount": "50", "timestamp": int64(1700000300), "from": address, "to": "TDest111111111111111111111111111111"},
		{"amount": "75", "timestamp": int64(1700000400), "from": address, "to": "TDest222222222222222222222222222222"},
	}

	report := buildTRC20AnalysisReport(status, validation, rows, nil, time.UTC, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	if strings.Contains(report, "OTC商户：中") || strings.Contains(report, "OTC商户：低") {
		t.Fatalf("OTC 画像不应像实体归属结论:\n%s", report)
	}
	if !strings.Contains(report, "OTC商户行为特征：疑似") {
		t.Fatalf("OTC 画像应使用行为推断文案:\n%s", report)
	}
}

func TestSplitTRC20PlainTextReportKeepsChunksUnderTelegramLimit(t *testing.T) {
	address := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"
	status := services.OnChainInquiry{Address: address, Network: "TRC20", Active: true, Balance: "1.23", TransactionCount: 8888}
	validation := services.AddressValidation{Valid: true, Network: "TRC20", Reason: "格式通过"}
	rows := make([]map[string]any, 0, 50)
	for i := 0; i < 25; i++ {
		rows = append(rows, map[string]any{
			"amount":    "1234567.890123",
			"timestamp": int64(1700000000 + i*60),
			"from":      "TSourceAddressForReportLimitTest000000" + string(rune('A'+i%26)),
			"to":        address,
		})
		rows = append(rows, map[string]any{
			"amount":    "1234560.000000",
			"timestamp": int64(1700000030 + i*60),
			"from":      address,
			"to":        "TDestAddressForReportLimitTest00000000" + string(rune('A'+i%26)),
		})
	}

	report := buildTRC20AnalysisReport(status, validation, rows, nil, time.UTC, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	chunks := splitTRC20PlainTextReport(report, 1200)
	if len(chunks) < 2 {
		t.Fatalf("长报告应被拆分，chunks=%d report_runes=%d", len(chunks), len([]rune(report)))
	}
	for i, chunk := range chunks {
		if got := len([]rune(chunk)); got > 1200 {
			t.Fatalf("第 %d 段报告过长，got=%d want<=1200\n内容:\n%s", i+1, got, chunk)
		}
	}
	if got := strings.Join(chunks, "\n"); got != report {
		t.Fatalf("分片后内容不应丢失或改写")
	}
}
