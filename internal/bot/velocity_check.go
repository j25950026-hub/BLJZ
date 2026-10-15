package bot

import (
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/utils"
)

const (
	defaultVelocityWindow       = time.Second
	defaultVelocityMaxPerWindow = 5
	velocityCleanupInterval     = 30 * time.Second
	velocityEntryTTL            = 2 * time.Minute
)

type velocityCounterEntry struct {
	slot      int64
	count     int
	updatedAt time.Time
}

type userCommandVelocityChecker struct {
	window       time.Duration
	maxPerWindow int
	now          func() time.Time

	mu          sync.Mutex
	counters    map[int64]velocityCounterEntry
	lastCleanup time.Time
}

func shouldApplyCommandVelocityLimit(msg *tgbotapi.Message, text string) bool {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return false
	}
	if msg != nil && msg.IsCommand() {
		return true
	}
	if strings.HasPrefix(normalized, "/") {
		return true
	}
	if isCommandVelocityExactText(normalized) || isCommandVelocityPrefixText(normalized) {
		return true
	}
	if isShowGroupIDCommand(normalized) ||
		looksLikeRateQueryCommand(normalized) ||
		isQuickAccountingDataCardCommand(normalized) ||
		isAddressManagementCommand(normalized) {
		return true
	}
	if _, ok := utils.ParseLedgerText(normalized); ok {
		return true
	}
	if utils.LooksLikeLedgerText(normalized) {
		return true
	}
	if _, ok := utils.ParseWorldTimeQuery(normalized); ok {
		return true
	}
	return false
}

func isCommandVelocityExactText(text string) bool {
	switch text {
	case "菜单", "帮助", "界面", "功能", "功能总览",
		"群组管理", "群控面板", "控制面板",
		"一键账单", "一键显示账单", "完整账单", "显示账单",
		"导出账单", "导出报表", "导出Excel", "导出PDF",
		"世界时间", "worldtime", "工作计时", "开始计时", "结束计时",
		"查看群评价", "查看业务员评价",
		"上课", "下课",
		"开始记账", "一键开始", "一键开始机器人", "开始", "停止记账", "停止",
		"查单列表", "查单管理", "延时查单列表", "清理延时查单", "延时查单清理",
		"显示入款信息", "显示下发信息",
		"清除缓存", "清理缓存",
		"显示昵称", "关闭昵称",
		"开启换算", "关闭换算", "开关U显示", "开关u显示",
		"设置实时汇率", "一键设置汇率",
		"担保", "担保规则",
		"清除群标签", "恢复默认群标签", "显示群标签", "查看群标签", "本群标签",
		"显示群类型", "同类型群数据统计", "单独群数据统计",
		"显示本群操作人", "显示操作人", "本群操作人",
		"显示全局操作人", "显示全局权限人", "转移本群权限人",
		"本群身份",
		"开启自定义内容时间", "关闭自定义内容时间",
		"开启自动回复", "关闭自动回复",
		"自动回复列表", "关键词转发列表", "自定义内容列表",
		"转发", "@所有人", "机器人退出群组",
		"记账信息跟踪", "记账撤回", "撤回入款", "撤回下发",
		"设置欧易交易所", "设置欧意交易所", "设置火币交易所", "设置币安交易所":
		return true
	default:
		return false
	}
}

func isCommandVelocityPrefixText(text string) bool {
	prefixes := []string{
		"评价服务",
		"设置上课广告", "设置下课广告", "设置欢迎语", "设置进群欢迎",
		"自助查单", "延时查单",
		"设置日切时间", "开关",
		"设置汇率", "设置美金汇率", "设置美元汇率",
		"设置下发汇率", "设置费率", "设置下发费率", "单笔下发汇率",
		"设置担保规则",
		"设置群标签", "批量设置群标签", "批量设置群类型", "设置群类型",
		"设置操作人", "删除操作人",
		"设置全局操作人", "删除全局操作人",
		"设置全局权限人", "删除全局权限人",
		"设置身份", "删除身份", "显示",
		"设置自定义内容", "删除自定义内容",
		"设置自动回复", "删除自动回复",
		"设置关键词转发", "删除关键词转发",
		"一键群发", "定时群发", "查看定时群发", "编辑定时群发", "重新发送定时群发", "指定@群发",
		"换算-",
		"查卡", "设置联行号", "删除联行号", "查联行号",
		"查询USDT-TRC20最近交易记录", "查询TRC20", "查询链上", "链上查询",
		"距离",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	lower := strings.ToLower(text)
	return strings.HasPrefix(lower, "trc20") || strings.HasPrefix(lower, "jl")
}

func newUserCommandVelocityChecker(window time.Duration, maxPerWindow int) *userCommandVelocityChecker {
	if window <= 0 {
		window = defaultVelocityWindow
	}
	if maxPerWindow <= 0 {
		maxPerWindow = defaultVelocityMaxPerWindow
	}
	return &userCommandVelocityChecker{
		window:       window,
		maxPerWindow: maxPerWindow,
		now:          time.Now,
		counters:     make(map[int64]velocityCounterEntry),
	}
}

func (c *userCommandVelocityChecker) Allow(userID int64) (bool, int) {
	if c == nil || userID <= 0 {
		return true, 0
	}
	nowFn := c.now
	if nowFn == nil {
		nowFn = time.Now
	}
	return c.allowAt(userID, nowFn())
}

func (c *userCommandVelocityChecker) MaxPerWindow() int {
	if c == nil || c.maxPerWindow <= 0 {
		return defaultVelocityMaxPerWindow
	}
	return c.maxPerWindow
}

func (c *userCommandVelocityChecker) Window() time.Duration {
	if c == nil || c.window <= 0 {
		return defaultVelocityWindow
	}
	return c.window
}

func (c *userCommandVelocityChecker) allowAt(userID int64, now time.Time) (bool, int) {
	if c == nil || userID <= 0 {
		return true, 0
	}
	window := c.Window()
	nowUTC := now.UTC()
	slot := nowUTC.UnixNano() / int64(window)

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.counters[userID]
	if entry.slot != slot {
		entry.slot = slot
		entry.count = 0
	}
	entry.count++
	entry.updatedAt = nowUTC
	c.counters[userID] = entry

	if c.lastCleanup.IsZero() || nowUTC.Sub(c.lastCleanup) >= velocityCleanupInterval {
		cutoff := nowUTC.Add(-velocityEntryTTL)
		for uid, item := range c.counters {
			if item.updatedAt.Before(cutoff) {
				delete(c.counters, uid)
			}
		}
		c.lastCleanup = nowUTC
	}
	if entry.count > c.MaxPerWindow() {
		return false, entry.count
	}
	return true, entry.count
}
