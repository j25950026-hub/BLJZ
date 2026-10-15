package bot

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-bot/internal/services"
	"go-bot/internal/utils"
)

type trc20FlowStat struct {
	Address string
	Count   int
	Amount  float64
}

type trc20BehaviorSignals struct {
	DepositCount      int
	PayoutCount       int
	RelatedCount      int
	CounterpartyCount int
	RepeatedCount     int
	TotalIn           float64
	TotalOut          float64
	MaxAmount         float64
	AvgAmount         float64
	NetFlow           float64
	FirstSeenUnix     int64
	LastSeenUnix      int64
	FastInOut         bool
	LowBalanceRelay   bool
	HighFrequency     bool
	LargeSingle       bool
	RepeatedAmount    bool
	ConcentratedPeer  bool
	ManyInOneOut      bool
	OneInManyOut      bool
	Score             int
	RiskLevel         string
	Stars             string
	Advice            string
	Watchlist         string
}

const trc20PlainTextReportChunkRunes = 3600

func buildTRC20AnalysisReport(status services.OnChainInquiry, validation services.AddressValidation, rows []map[string]any, recentErr error, loc *time.Location, generatedAt time.Time) string {
	if loc == nil {
		loc = time.Local
	}
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	address := strings.TrimSpace(status.Address)
	if address == "" {
		address = "未知"
	}
	network := strings.ToUpper(strings.TrimSpace(status.Network))
	if network == "" {
		network = strings.ToUpper(strings.TrimSpace(validation.Network))
	}
	if network == "" {
		network = "TRC20"
	}
	sourceStats, destStats, signals := analyzeTRC20Flows(address, status, rows)
	applyTRC20RiskScore(&signals, status, validation, len(rows), recentErr)
	sampleAvailable := recentErr == nil && len(rows) > 0
	fetchFailed := recentErr != nil

	dataStatus := "完整"
	if recentErr != nil || len(rows) == 0 {
		dataStatus = "数据不足"
	} else if len(rows) < 5 {
		dataStatus = "部分可用"
	}
	validText := "有效"
	if !validation.Valid {
		validText = "无效"
	}
	if validation.Blacklisted {
		validText = "命中黑名单"
	}
	activeText := "不活跃"
	if status.Active {
		activeText = "活跃"
	}
	createdAt := "未知"
	if t := formatTRC20Time(status.CreatedAtUnix, loc); t != "" {
		createdAt = t
	}
	txCount := "数据不足"
	if status.TransactionCount > 0 {
		txCount = fmt.Sprintf("%d 笔", status.TransactionCount)
	}
	firstTrade := "数据不足"
	if signals.FirstSeenUnix > 0 {
		firstTrade = formatTRC20Time(signals.FirstSeenUnix, loc)
	}
	lastTrade := "数据不足"
	if signals.LastSeenUnix > 0 {
		lastTrade = formatTRC20Time(signals.LastSeenUnix, loc)
	}

	usdtBalance := normalizeTRC20Amount(status.Balance)
	trxBalance := normalizeTRC20Amount(status.NativeBalance)
	depositCountText := "数据不足"
	payoutCountText := "数据不足"
	avgAmountText := "数据不足"
	maxAmountText := "数据不足"
	netFlowText := "数据不足"
	counterpartyCountText := "数据不足"
	repeatedCountText := "数据不足"
	if sampleAvailable {
		depositCountText = fmt.Sprintf("%d 笔", signals.DepositCount)
		payoutCountText = fmt.Sprintf("%d 笔", signals.PayoutCount)
		avgAmountText = fmt.Sprintf("%s U", utils.FormatAmount(signals.AvgAmount))
		maxAmountText = fmt.Sprintf("%s U", utils.FormatAmount(signals.MaxAmount))
		netFlowText = fmt.Sprintf("%s U", utils.FormatAmount(signals.NetFlow))
		counterpartyCountText = fmt.Sprintf("%d", signals.CounterpartyCount)
		repeatedCountText = fmt.Sprintf("%d 个", signals.RepeatedCount)
	}
	riskScoreText := trc20RiskScoreText(signals)
	topFlowEmptyText := trc20TopFlowEmptyText(fetchFailed, sampleAvailable)
	lines := []string{
		"TRC20 地址链上分析报告",
		fmt.Sprintf("地址：%s", address),
		fmt.Sprintf("报告时间：%s", generatedAt.In(loc).Format("2006-01-02 15:04:05")),
		fmt.Sprintf("数据状态：%s", dataStatus),
		"--------------------------------",
		"1. 地址基本信息",
		fmt.Sprintf("- 地址有效性：%s（%s）", validText, orDefault(validation.Reason, "格式未校验")),
		fmt.Sprintf("- 网络：%s", network),
		fmt.Sprintf("- 地址状态：%s", activeText),
		fmt.Sprintf("- 地址类型：普通钱包/未知；交易所/合约标签数据不足"),
		fmt.Sprintf("- 创建时间：%s", createdAt),
		fmt.Sprintf("- 总交易次数：%s", txCount),
		fmt.Sprintf("- 公开标签：%s", trc20BlacklistLabel(validation)),
		"",
		"2. 资产情况",
		fmt.Sprintf("- TRX余额：%s TRX", orDefault(trxBalance, "0")),
		fmt.Sprintf("- USDT(TRC20)余额：%s U", orDefault(usdtBalance, "0")),
		"- 持有的TRC20 Token：当前仅统计 USDT；其它 Token 数据不足",
		"- 总资产估值：仅按已获取的 USDT/TRX 余额展示，完整估值数据不足",
		"",
		"3. 交易行为",
		fmt.Sprintf("- 最近交易：%s", trc20RecentRowsStatus(rows, recentErr)),
		fmt.Sprintf("- 收款次数：%s", depositCountText),
		fmt.Sprintf("- 转账次数：%s", payoutCountText),
		fmt.Sprintf("- 第一笔交易时间：%s", firstTrade),
		fmt.Sprintf("- 最近交易时间：%s", lastTrade),
		fmt.Sprintf("- 日均交易量：数据不足"),
		fmt.Sprintf("- 平均单笔金额：%s", avgAmountText),
		fmt.Sprintf("- 最大单笔金额：%s", maxAmountText),
		fmt.Sprintf("- 最近样本净流入：%s", netFlowText),
		"",
		"4. 资金来源分析",
		fmt.Sprintf("- 资金来源：%s", trc20SourceSummary(signals, fetchFailed, sampleAvailable)),
		"- 是否来自交易所：数据不足",
		"- 是否来自OTC：数据不足",
		"- 是否来自矿池：数据不足",
		"- 是否来自博彩平台：数据不足",
		"- 是否来自诈骗地址：" + trc20ScamStatus(validation),
		"- 是否来自混币服务：数据不足",
		"Top资金来源",
	}
	lines = append(lines, formatTRC20TopFlowLines(sourceStats, 10, topFlowEmptyText)...)
	lines = append(lines,
		"",
		"5. 资金流向分析",
		fmt.Sprintf("- 资金去向：%s", trc20DestinationSummary(signals, fetchFailed, sampleAvailable)),
		"- 是否最终流入交易所：数据不足",
		fmt.Sprintf("- 是否流入多个新地址：%s", trc20YesNoSuspect(signals.OneInManyOut, sampleAvailable)),
		fmt.Sprintf("- 是否存在洗钱特征：%s", trc20MoneyLaunderingStatus(signals, validation, sampleAvailable)),
		fmt.Sprintf("- 是否存在资金归集行为：%s", trc20YesNoSuspect(signals.ManyInOneOut, sampleAvailable)),
		"Top资金去向",
	)
	lines = append(lines, formatTRC20TopFlowLines(destStats, 10, topFlowEmptyText)...)
	lines = append(lines,
		"",
		"6. 地址画像",
		fmt.Sprintf("- 个人钱包：%s", trc20ProfileConfidence(!signals.HighFrequency && signals.CounterpartyCount <= 3 && signals.MaxAmount < 1000, sampleAvailable)),
		"- 企业钱包：数据不足",
		"- 交易所热钱包：数据不足",
		"- 冷钱包：数据不足",
		"- 做市商：数据不足",
		fmt.Sprintf("- 自动化/套利行为特征：%s", trc20BehaviorFeatureStatus(signals.HighFrequency || signals.ConcentratedPeer, sampleAvailable)),
		"- DeFi用户：数据不足",
		"- 博彩平台：数据不足",
		"- 支付平台：数据不足",
		fmt.Sprintf("- OTC商户行为特征：%s", trc20BehaviorFeatureStatus(signals.CounterpartyCount >= 4 && signals.DepositCount > 0 && signals.PayoutCount > 0, sampleAvailable)),
		fmt.Sprintf("- 高风险行为特征：%s", trc20BehaviorFeatureStatus(signals.Score >= 60 || validation.Blacklisted, sampleAvailable)),
		"判断依据：基于最近 USDT 流水、余额留存、对手方数量、交易节奏和本地黑名单；未接入外部实体标签时不作事实归属。",
		"",
		"7. 行为模式分析",
		fmt.Sprintf("- 批量转账：%s", trc20YesNoSuspect(signals.RepeatedAmount, sampleAvailable)),
		fmt.Sprintf("- 固定时间转账：%s", trc20YesNoSuspect(signals.HighFrequency, sampleAvailable)),
		fmt.Sprintf("- 是否机器人行为：%s", trc20YesNoSuspect(signals.HighFrequency || signals.RepeatedAmount, sampleAvailable)),
		fmt.Sprintf("- 是否自动归集：%s", trc20YesNoSuspect(signals.ManyInOneOut, sampleAvailable)),
		fmt.Sprintf("- 是否频繁拆分资金：%s", trc20YesNoSuspect(signals.OneInManyOut, sampleAvailable)),
		fmt.Sprintf("- 是否短时间大量收发：%s", trc20YesNoSuspect(signals.HighFrequency, sampleAvailable)),
		fmt.Sprintf("- 快进快出：%s", trc20YesNoSuspect(signals.FastInOut, sampleAvailable)),
		fmt.Sprintf("- 低余额中转：%s", trc20YesNoSuspect(signals.LowBalanceRelay, sampleAvailable)),
		"",
		"8. 风险评估（AML）",
		fmt.Sprintf("- 洗钱风险：%s", signals.RiskLevel),
		fmt.Sprintf("- 是否与制裁地址有关联：%s", trc20SanctionStatus(validation)),
		"- 是否与黑客资金有关：数据不足",
		fmt.Sprintf("- 是否与诈骗资金有关：%s", trc20ScamStatus(validation)),
		"- 是否与赌博有关：数据不足",
		fmt.Sprintf("- 是否属于交易所：数据不足"),
		fmt.Sprintf("- 是否属于博彩资金：数据不足"),
		fmt.Sprintf("- 是否属于OTC：数据不足"),
		fmt.Sprintf("- 是否存在洗钱特征：%s", trc20MoneyLaunderingStatus(signals, validation, sampleAvailable)),
		fmt.Sprintf("- 风险等级：%s %s", signals.Stars, signals.RiskLevel),
		fmt.Sprintf("- 风险评分：%s", riskScoreText),
		"",
		"9. 网络关系分析",
		fmt.Sprintf("- 关联地址数量：%s", counterpartyCountText),
		fmt.Sprintf("- 高频交互地址：%s", repeatedCountText),
		fmt.Sprintf("- 核心资金节点：%s", trc20CoreNodeSummary(sourceStats, destStats)),
		fmt.Sprintf("- 是否形成资金网络：%s", trc20NetworkStatus(signals, sampleAvailable)),
		"说明：当前仅统计直接对手方，未做多跳实体聚类。",
		"",
		"10. 总结",
		fmt.Sprintf("- 地址画像：%s", trc20ProfileSummary(signals, sampleAvailable)),
		fmt.Sprintf("- 主要用途：%s", trc20MainUse(signals, sampleAvailable)),
		fmt.Sprintf("- 风险评分：%s", riskScoreText),
		fmt.Sprintf("- 是否建议继续交易：%s", signals.Advice),
		fmt.Sprintf("- 是否建议列入监控名单：%s", signals.Watchlist),
	)
	return strings.Join(lines, "\n")
}

func analyzeTRC20Flows(address string, status services.OnChainInquiry, rows []map[string]any) ([]trc20FlowStat, []trc20FlowStat, trc20BehaviorSignals) {
	source := map[string]trc20FlowStat{}
	dest := map[string]trc20FlowStat{}
	counterparties := map[string]int{}
	amounts := make([]float64, 0, len(rows))
	inEvents := make([]struct {
		ts     int64
		amount float64
	}, 0)
	outEvents := make([]struct {
		ts     int64
		amount float64
	}, 0)
	signals := trc20BehaviorSignals{}
	for _, row := range rows {
		from := strings.TrimSpace(fmt.Sprintf("%v", row["from"]))
		to := strings.TrimSpace(fmt.Sprintf("%v", row["to"]))
		amount := parseTRC20AmountAny(row["amount"])
		ts := parseTRC20AnyTimestamp(row["timestamp"])
		if amount > 0 {
			amounts = append(amounts, amount)
			signals.AvgAmount += amount
			if amount > signals.MaxAmount {
				signals.MaxAmount = amount
			}
		}
		if ts > 0 {
			if signals.FirstSeenUnix == 0 || ts < signals.FirstSeenUnix {
				signals.FirstSeenUnix = ts
			}
			if ts > signals.LastSeenUnix {
				signals.LastSeenUnix = ts
			}
		}
		direction, _ := inferTRC20Direction(address, from, to)
		switch direction {
		case "转入":
			signals.DepositCount++
			signals.TotalIn += amount
			inEvents = append(inEvents, struct {
				ts     int64
				amount float64
			}{ts: ts, amount: amount})
			accumulateTRC20Flow(source, from, amount)
			if from != "" {
				counterparties[from]++
			}
		case "转出":
			signals.PayoutCount++
			signals.TotalOut += amount
			outEvents = append(outEvents, struct {
				ts     int64
				amount float64
			}{ts: ts, amount: amount})
			accumulateTRC20Flow(dest, to, amount)
			if to != "" {
				counterparties[to]++
			}
		default:
			signals.RelatedCount++
		}
	}
	if len(amounts) > 0 {
		signals.AvgAmount /= float64(len(amounts))
	}
	signals.NetFlow = signals.TotalIn - signals.TotalOut
	signals.CounterpartyCount = len(counterparties)
	for _, count := range counterparties {
		if count >= 2 {
			signals.RepeatedCount++
		}
		if count >= 3 {
			signals.ConcentratedPeer = true
		}
	}
	signals.FastInOut = hasTRC20FastInOut(inEvents, outEvents)
	signals.HighFrequency = hasTRC20HighFrequency(rows)
	signals.LargeSingle = signals.MaxAmount >= 1000
	signals.RepeatedAmount = hasTRC20RepeatedAmounts(amounts)
	signals.ManyInOneOut = len(source) >= 2 && len(dest) == 1 && signals.PayoutCount > 0
	signals.OneInManyOut = len(source) == 1 && len(dest) >= 2 && signals.DepositCount > 0
	balance := parseTRC20AmountAny(status.Balance)
	signals.LowBalanceRelay = balance <= 5 && signals.TotalIn >= 100 && signals.TotalOut >= 80 && math.Abs(signals.NetFlow) <= math.Max(20, signals.TotalIn*0.1)
	return flattenTRC20FlowStats(source), flattenTRC20FlowStats(dest), signals
}

func accumulateTRC20Flow(target map[string]trc20FlowStat, address string, amount float64) {
	addr := strings.TrimSpace(address)
	if addr == "" || addr == "<nil>" {
		addr = "未知地址"
	}
	row := target[addr]
	row.Address = addr
	row.Count++
	row.Amount += amount
	target[addr] = row
}

func flattenTRC20FlowStats(stats map[string]trc20FlowStat) []trc20FlowStat {
	out := make([]trc20FlowStat, 0, len(stats))
	for _, row := range stats {
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if math.Abs(out[i].Amount-out[j].Amount) > 1e-9 {
			return out[i].Amount > out[j].Amount
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Address < out[j].Address
	})
	return out
}

func applyTRC20RiskScore(signals *trc20BehaviorSignals, status services.OnChainInquiry, validation services.AddressValidation, rowCount int, recentErr error) {
	if signals == nil {
		return
	}
	score := 20
	if validation.Blacklisted {
		score = 85
	}
	if status.TransactionCount > 1000 {
		score += 5
	}
	if signals.FastInOut {
		score += 18
	}
	if signals.LowBalanceRelay {
		score += 12
	}
	if signals.HighFrequency {
		score += 10
	}
	if signals.LargeSingle {
		score += 10
	}
	if signals.RepeatedAmount {
		score += 8
	}
	if signals.ConcentratedPeer {
		score += 6
	}
	if signals.ManyInOneOut || signals.OneInManyOut {
		score += 10
	}
	if (rowCount == 0 || recentErr != nil) && !validation.Blacklisted {
		signals.Score = 0
		signals.RiskLevel = "无法判断"
		signals.Stars = "数据不足"
		signals.Advice = "人工复核"
		signals.Watchlist = "是"
		return
	}
	score = maxTRC20ReportInt(0, minTRC20ReportInt(100, score))
	signals.Score = score
	switch {
	case score >= 80:
		signals.RiskLevel = "极高"
		signals.Stars = "★★★★★"
		signals.Advice = "不建议"
		signals.Watchlist = "是"
	case score >= 60:
		signals.RiskLevel = "高"
		signals.Stars = "★★★★☆"
		signals.Advice = "暂缓并人工复核"
		signals.Watchlist = "是"
	case score >= 40:
		signals.RiskLevel = "中"
		signals.Stars = "★★★☆☆"
		signals.Advice = "人工复核"
		signals.Watchlist = "是"
	case score >= 20:
		signals.RiskLevel = "低"
		signals.Stars = "★★☆☆☆"
		signals.Advice = "谨慎"
		signals.Watchlist = "否"
	default:
		signals.RiskLevel = "极低"
		signals.Stars = "★☆☆☆☆"
		signals.Advice = "建议"
		signals.Watchlist = "否"
	}
}

func formatTRC20TopFlowLines(stats []trc20FlowStat, limit int, emptyText string) []string {
	if len(stats) == 0 {
		return []string{orDefault(emptyText, "- 无；最近样本未发现")}
	}
	if limit <= 0 || limit > len(stats) {
		limit = len(stats)
	}
	lines := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		row := stats[i]
		lines = append(lines, fmt.Sprintf("%d. %s：%s U / %d 笔", i+1, row.Address, utils.FormatAmount(row.Amount), row.Count))
	}
	return lines
}

func parseTRC20AmountAny(raw any) float64 {
	text := normalizeTRC20Amount(fmt.Sprintf("%v", raw))
	amount, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || amount < 0 {
		return 0
	}
	return amount
}

func hasTRC20FastInOut(inEvents, outEvents []struct {
	ts     int64
	amount float64
}) bool {
	for _, in := range inEvents {
		if in.ts <= 0 || in.amount <= 0 {
			continue
		}
		for _, out := range outEvents {
			if out.ts <= in.ts || out.ts-in.ts > 3600 || out.amount <= 0 {
				continue
			}
			ratio := out.amount / in.amount
			if ratio >= 0.8 && ratio <= 1.05 {
				return true
			}
		}
	}
	return false
}

func hasTRC20HighFrequency(rows []map[string]any) bool {
	times := make([]int64, 0, len(rows))
	for _, row := range rows {
		if ts := parseTRC20AnyTimestamp(row["timestamp"]); ts > 0 {
			times = append(times, ts)
		}
	}
	if len(times) < 3 {
		return false
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	return times[len(times)-1]-times[0] <= 600
}

func hasTRC20RepeatedAmounts(amounts []float64) bool {
	if len(amounts) < 3 {
		return false
	}
	counts := map[string]int{}
	for _, amount := range amounts {
		key := utils.FormatAmount(math.Round(amount*100) / 100)
		counts[key]++
		if counts[key] >= 3 {
			return true
		}
	}
	return false
}

func trc20RecentRowsStatus(rows []map[string]any, recentErr error) string {
	if recentErr != nil {
		return "暂无法获取最近流水"
	}
	if len(rows) == 0 {
		return "暂无 USDT 流水"
	}
	return fmt.Sprintf("最近 %d 笔 USDT 流水", len(rows))
}

func trc20SourceSummary(signals trc20BehaviorSignals, fetchFailed bool, sampleAvailable bool) string {
	if fetchFailed {
		return "流水获取失败，无法判断"
	}
	if !sampleAvailable {
		return "最近样本不足，无法判断"
	}
	switch {
	case signals.DepositCount == 0:
		return "最近样本未发现入账"
	case signals.DepositCount >= 2 && signals.CounterpartyCount >= 3:
		return "多地址分散流入"
	default:
		return "外部钱包流入"
	}
}

func trc20DestinationSummary(signals trc20BehaviorSignals, fetchFailed bool, sampleAvailable bool) string {
	if fetchFailed {
		return "流水获取失败，无法判断"
	}
	if !sampleAvailable {
		return "最近样本不足，无法判断"
	}
	switch {
	case signals.PayoutCount == 0:
		return "最近样本未发现出账"
	case signals.ManyInOneOut:
		return "多地址流入后单地址集中流出"
	case signals.OneInManyOut:
		return "单地址流入后多地址分散流出"
	default:
		return "外部钱包流出"
	}
}

func trc20ScamStatus(validation services.AddressValidation) string {
	if validation.Blacklisted && strings.Contains(strings.ToLower(validation.BlacklistScope+" "+validation.BlacklistLabel), "scam") {
		return "命中"
	}
	if validation.Blacklisted {
		return "疑似"
	}
	return "未发现"
}

func trc20SanctionStatus(validation services.AddressValidation) string {
	if validation.Blacklisted && strings.Contains(strings.ToLower(validation.BlacklistScope+" "+validation.BlacklistLabel), "ofac") {
		return "命中"
	}
	return "未发现"
}

func trc20BlacklistLabel(validation services.AddressValidation) string {
	if validation.Blacklisted {
		return validation.BlacklistScope + "：" + orDefault(validation.BlacklistLabel, "未知来源")
	}
	return "未发现公开标签/本地黑名单命中"
}

func trc20MoneyLaunderingStatus(signals trc20BehaviorSignals, validation services.AddressValidation, sampleAvailable bool) string {
	if validation.Blacklisted && hasTRC20AMLBlacklistKeyword(validation) {
		return "命中"
	}
	if validation.Blacklisted || signals.FastInOut || signals.LowBalanceRelay || signals.ManyInOneOut || signals.OneInManyOut {
		return "疑似"
	}
	if !sampleAvailable {
		return "无法判断"
	}
	return "未发现"
}

func hasTRC20AMLBlacklistKeyword(validation services.AddressValidation) bool {
	text := strings.ToLower(validation.BlacklistScope + " " + validation.BlacklistLabel)
	for _, keyword := range []string{
		"aml",
		"launder",
		"money laundering",
		"mixer",
		"mixing",
		"tornado",
		"洗钱",
		"混币",
	} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func trc20YesNoSuspect(ok bool, sampleAvailable bool) string {
	if ok {
		return "疑似"
	}
	if !sampleAvailable {
		return "数据不足"
	}
	return "未发现"
}

func trc20ProfileConfidence(ok bool, sampleAvailable bool) string {
	if !sampleAvailable {
		return "数据不足"
	}
	if ok {
		return "中"
	}
	return "低"
}

func trc20BehaviorFeatureStatus(ok bool, sampleAvailable bool) string {
	if ok {
		return "疑似"
	}
	if !sampleAvailable {
		return "数据不足"
	}
	return "未发现"
}

func splitTRC20PlainTextReport(text string, limit int) []string {
	if limit <= 0 {
		limit = trc20PlainTextReportChunkRunes
	}
	if len([]rune(text)) <= limit {
		return []string{text}
	}
	lines := strings.Split(text, "\n")
	chunks := make([]string, 0, len(lines)/20+1)
	current := make([]string, 0, 32)
	currentLen := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		chunks = append(chunks, strings.Join(current, "\n"))
		current = current[:0]
		currentLen = 0
	}
	for _, line := range lines {
		lineLen := len([]rune(line))
		if lineLen > limit {
			flush()
			runes := []rune(line)
			for len(runes) > limit {
				chunks = append(chunks, string(runes[:limit]))
				runes = runes[limit:]
			}
			if len(runes) > 0 {
				current = append(current, string(runes))
				currentLen = len(runes)
			}
			continue
		}
		addLen := lineLen
		if len(current) > 0 {
			addLen++
		}
		if len(current) > 0 && currentLen+addLen > limit {
			flush()
			addLen = lineLen
		}
		current = append(current, line)
		currentLen += addLen
	}
	flush()
	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

func trc20CoreNodeSummary(sourceStats, destStats []trc20FlowStat) string {
	nodes := make([]string, 0, 2)
	if len(sourceStats) > 0 {
		nodes = append(nodes, sourceStats[0].Address)
	}
	if len(destStats) > 0 {
		nodes = append(nodes, destStats[0].Address)
	}
	if len(nodes) == 0 {
		return "数据不足"
	}
	return strings.Join(nodes, " / ")
}

func trc20NetworkStatus(signals trc20BehaviorSignals, sampleAvailable bool) string {
	if !sampleAvailable {
		return "数据不足"
	}
	if signals.CounterpartyCount >= 4 || signals.ManyInOneOut || signals.OneInManyOut {
		return "疑似"
	}
	return "未发现"
}

func trc20ProfileSummary(signals trc20BehaviorSignals, sampleAvailable bool) string {
	if !sampleAvailable {
		return "画像数据不足"
	}
	switch {
	case signals.LowBalanceRelay || signals.FastInOut:
		return "疑似中转地址"
	case signals.HighFrequency || signals.RepeatedAmount:
		return "疑似自动化/机器人地址"
	case signals.DepositCount > 0 && signals.PayoutCount == 0:
		return "收款型地址"
	case signals.PayoutCount > 0 && signals.DepositCount == 0:
		return "付款型地址"
	default:
		return "普通收付款地址"
	}
}

func trc20MainUse(signals trc20BehaviorSignals, sampleAvailable bool) string {
	if !sampleAvailable {
		return "数据不足"
	}
	switch {
	case signals.ManyInOneOut:
		return "资金归集/中转"
	case signals.OneInManyOut:
		return "资金分发"
	case signals.DepositCount > signals.PayoutCount:
		return "收款"
	case signals.PayoutCount > signals.DepositCount:
		return "转账"
	default:
		return "收付款"
	}
}

func trc20RiskScoreText(signals trc20BehaviorSignals) string {
	if signals.RiskLevel == "无法判断" {
		return "数据不足"
	}
	return fmt.Sprintf("%d/100", signals.Score)
}

func trc20TopFlowEmptyText(fetchFailed bool, sampleAvailable bool) string {
	if fetchFailed {
		return "- 流水获取失败，无法展示"
	}
	if !sampleAvailable {
		return "- 数据不足；无法判断"
	}
	return "- 无；最近样本未发现"
}

func minTRC20ReportInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxTRC20ReportInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
