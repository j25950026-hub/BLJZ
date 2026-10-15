package bot

import (
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/utils"
)

const degradationHighLoadThresholdPercent = 80

func (a *App) shouldDropByDegradation(msg *tgbotapi.Message, text string) bool {
	if !a.isHighLoad() {
		return false
	}
	if !isQueryOrEntertainmentInstruction(text) {
		return false
	}

	if msg != nil && msg.Chat != nil {
		userID := int64(0)
		if msg.From != nil {
			userID = msg.From.ID
		}
		log.Printf(
			"高负载降级已触发：drop query/entertainment chat_id=%d user_id=%d message_id=%d text=%q",
			msg.Chat.ID,
			userID,
			msg.MessageID,
			trimForAudit(text, 128),
		)
	}
	return true
}

func (a *App) isHighLoad() bool {
	if a == nil {
		return false
	}
	if reachesLoadThreshold(len(a.webhookUpdates), cap(a.webhookUpdates), degradationHighLoadThresholdPercent) {
		return true
	}
	if a.bookkeepingQueue != nil {
		queued, capacity := a.bookkeepingQueue.usage()
		if reachesLoadThreshold(queued, capacity, degradationHighLoadThresholdPercent) {
			return true
		}
	}
	if a.outboundQueue != nil {
		queued, capacity := a.outboundQueue.usage()
		if reachesLoadThreshold(queued, capacity, degradationHighLoadThresholdPercent) {
			return true
		}
	}
	return false
}

func reachesLoadThreshold(queued, capacity, thresholdPercent int) bool {
	if queued <= 0 || capacity <= 0 {
		return false
	}
	if thresholdPercent <= 0 {
		thresholdPercent = 1
	}
	if thresholdPercent > 100 {
		thresholdPercent = 100
	}
	return queued*100 >= capacity*thresholdPercent
}

func isBookkeepingInstruction(text string) bool {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return false
	}
	switch normalized {
	case "记账撤回", "撤回入款", "撤回下发":
		return true
	}
	_, ok := utils.ParseLedgerText(normalized)
	return ok
}

func isQueryOrEntertainmentInstruction(text string) bool {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return false
	}
	if isBookkeepingInstruction(normalized) {
		return false
	}

	lower := strings.ToLower(normalized)
	switch normalized {
	case "菜单", "帮助", "功能总览", "查单管理",
		"一键账单", "一键显示账单", "完整账单", "显示账单",
		"查单列表", "延时查单列表", "显示入款信息", "显示下发信息",
		"汇率对比", "z0", "lk", "lz", "lw",
		"欧易交易所", "欧意交易所", "火币交易所", "币安交易所",
		"世界时间", "记账信息跟踪", "查看群评价", "查看业务员评价",
		"自动回复列表", "关键词转发列表", "自定义内容列表":
		return true
	}
	switch {
	case strings.HasPrefix(normalized, "自助查单"),
		strings.HasPrefix(normalized, "延时查单"),
		strings.HasPrefix(normalized, "查卡"),
		strings.HasPrefix(normalized, "查联行号"),
		strings.HasPrefix(normalized, "查询USDT-TRC20最近交易记录"),
		strings.HasPrefix(normalized, "查询TRC20"),
		strings.HasPrefix(normalized, "查询链上"),
		strings.HasPrefix(normalized, "链上查询"),
		strings.HasPrefix(normalized, "距离"),
		strings.HasPrefix(lower, "trc20"),
		strings.HasPrefix(lower, "jl"):
		return true
	}
	if _, ok := utils.ParseWorldTimeQuery(normalized); ok {
		return true
	}
	if _, ok := utils.ParseMathExpression(normalized); ok {
		return true
	}
	return false
}
