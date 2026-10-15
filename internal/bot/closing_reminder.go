package bot

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
	"go-bot/internal/utils"
)

const (
	closingReminderStatusLabelNormal  = "正常"
	closingReminderStatusLabelPending = "待确认"
	closingReminderStatusLabelNoData  = "无账单"
	closingReminderStatusLabelReview  = "复核"
	closingReminderStatusLabelFailed  = "失败"

	closingReminderBatchActionEnable  = "enable"
	closingReminderBatchActionDisable = "disable"
	closingReminderBatchActionSetTime = "set_time"
	closingReminderBatchActionQuery   = "query"

	closingReminderBatchScopeType = "群类型"
	closingReminderBatchScopeTag  = "群标签"
)

type closingReminderBatchCommand struct {
	Action       string
	ScopeKind    string
	ScopeValue   string
	ReminderTime string
}

func (a *App) handleClosingReminderCommand(msg *tgbotapi.Message, g db.GroupConfig, text string) (bool, error) {
	if !isClosingReminderCommand(text) {
		return false, nil
	}
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return true, nil
	}

	if isClosingReminderBatchCommand(text) {
		if !a.db.IsGlobalOwner(msg.From.ID) {
			return true, nil
		}
		return true, a.handleBatchClosingReminderCommand(msg, text)
	}
	if isClosingReminderOwnerOnlyCommand(text) && !a.db.IsOwner(msg.Chat.ID, msg.From.ID) {
		return true, nil
	}
	if !a.db.IsPrivileged(msg.Chat.ID, msg.From.ID) {
		return true, nil
	}

	switch {
	case text == "立即下课":
		return true, a.triggerClosingReminderNow(msg, g)
	case text == "开启下课提醒":
		return true, a.updateClosingReminderConfig(msg, func(config *db.GroupClosingReminderConfig) {
			config.Enabled = 1
		}, "下课提醒已开启。")
	case text == "关闭下课提醒":
		return true, a.updateClosingReminderConfig(msg, func(config *db.GroupClosingReminderConfig) {
			config.Enabled = 0
		}, "下课提醒已关闭。")
	case strings.HasPrefix(text, "设置下课时间"):
		return true, a.handleSetClosingReminderTime(msg, text)
	case text == "查询下课提醒":
		return true, a.replyClosingReminderConfig(msg)
	case text == "查询下课文案":
		return true, a.replyClosingReminderTextOverview(msg)
	case strings.HasPrefix(text, "预览下课文案"):
		return true, a.handlePreviewClosingReminderText(msg, text)
	case strings.HasPrefix(text, "设置下课文案"):
		return true, a.handleSetClosingReminderText(msg, text)
	case text == "恢复全部下课文案":
		return true, a.handleRestoreAllClosingReminderTexts(msg)
	case strings.HasPrefix(text, "恢复下课文案"):
		return true, a.handleRestoreClosingReminderText(msg, text)
	case text == "查询下课私聊":
		return true, a.replyClosingReminderDMConfig(msg)
	case strings.HasPrefix(text, "设置下课私聊"):
		return true, a.handleSetClosingReminderDMMode(msg, text)
	case text == "预览下课私聊":
		return true, a.reply(msg.Chat.ID, msg.MessageID, defaultClosingReminderDMPreviewText(g))
	case strings.HasPrefix(text, "添加下课私聊"):
		return true, a.handleAddClosingDMRecipients(msg, text)
	case strings.HasPrefix(text, "删除下课私聊"):
		return true, a.handleDeleteClosingDMRecipients(msg, text)
	case text == "清空下课私聊用户":
		return true, a.handleClearClosingDMRecipients(msg)
	default:
		return true, nil
	}
}

func isClosingReminderCommand(text string) bool {
	if strings.TrimSpace(text) != text || text == "" {
		text = strings.TrimSpace(text)
	}
	switch {
	case text == "开启下课提醒",
		text == "关闭下课提醒",
		text == "查询下课提醒",
		text == "立即下课",
		text == "查询下课文案",
		text == "恢复全部下课文案",
		text == "查询下课私聊",
		text == "预览下课私聊",
		text == "清空下课私聊用户":
		return true
	case strings.HasPrefix(text, "设置下课时间"),
		strings.HasPrefix(text, "批量开启下课提醒"),
		strings.HasPrefix(text, "批量关闭下课提醒"),
		strings.HasPrefix(text, "批量设置下课时间"),
		strings.HasPrefix(text, "查询下课提醒群类型"),
		strings.HasPrefix(text, "查询下课提醒群标签"),
		strings.HasPrefix(text, "预览下课文案"),
		strings.HasPrefix(text, "设置下课文案"),
		strings.HasPrefix(text, "恢复下课文案"),
		strings.HasPrefix(text, "设置下课私聊"),
		strings.HasPrefix(text, "添加下课私聊"),
		strings.HasPrefix(text, "删除下课私聊"):
		return true
	default:
		return false
	}
}

func isClosingReminderOwnerOnlyCommand(text string) bool {
	return strings.HasPrefix(text, "添加下课私聊") ||
		strings.HasPrefix(text, "删除下课私聊") ||
		text == "清空下课私聊用户"
}

func isClosingReminderBatchCommand(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "批量开启下课提醒") ||
		strings.HasPrefix(text, "批量关闭下课提醒") ||
		strings.HasPrefix(text, "批量设置下课时间") ||
		strings.HasPrefix(text, "查询下课提醒群类型") ||
		strings.HasPrefix(text, "查询下课提醒群标签")
}

func (a *App) handleSetClosingReminderTime(msg *tgbotapi.Message, text string) error {
	raw := strings.TrimSpace(strings.TrimPrefix(text, "设置下课时间"))
	reminderTime, err := parseClosingReminderTime(raw)
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置下课时间04:00")
	}
	return a.updateClosingReminderConfig(msg, func(config *db.GroupClosingReminderConfig) {
		config.ReminderTime = reminderTime
	}, fmt.Sprintf("下课提醒时间已设置为 %s。", reminderTime))
}

func (a *App) handleBatchClosingReminderCommand(msg *tgbotapi.Message, text string) error {
	command, ok, formatText := parseClosingReminderBatchCommand(text)
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, formatText)
	}
	groups, err := a.listClosingReminderBatchGroups(command)
	if err != nil {
		return err
	}
	if command.Action == closingReminderBatchActionQuery {
		return a.replyClosingReminderBatchQuery(msg, command, groups)
	}
	return a.updateClosingReminderBatchConfigs(msg, command, groups)
}

func parseClosingReminderBatchCommand(text string) (closingReminderBatchCommand, bool, string) {
	text = strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(text, "批量开启下课提醒"):
		scope, ok := parseClosingReminderBatchScope(strings.TrimSpace(strings.TrimPrefix(text, "批量开启下课提醒")))
		if !ok {
			return closingReminderBatchCommand{}, false, "格式：批量开启下课提醒 群类型 代收\n或：批量开启下课提醒 群标签 夜班"
		}
		scope.Action = closingReminderBatchActionEnable
		return scope, true, ""
	case strings.HasPrefix(text, "批量关闭下课提醒"):
		scope, ok := parseClosingReminderBatchScope(strings.TrimSpace(strings.TrimPrefix(text, "批量关闭下课提醒")))
		if !ok {
			return closingReminderBatchCommand{}, false, "格式：批量关闭下课提醒 群类型 代收\n或：批量关闭下课提醒 群标签 夜班"
		}
		scope.Action = closingReminderBatchActionDisable
		return scope, true, ""
	case strings.HasPrefix(text, "批量设置下课时间"):
		payload := strings.TrimSpace(strings.TrimPrefix(text, "批量设置下课时间"))
		fields := strings.Fields(payload)
		if len(fields) < 3 {
			return closingReminderBatchCommand{}, false, closingReminderBatchSetTimeFormatText()
		}
		reminderTime, err := parseClosingReminderTime(fields[len(fields)-1])
		if err != nil {
			return closingReminderBatchCommand{}, false, closingReminderBatchSetTimeFormatText()
		}
		scope, ok := parseClosingReminderBatchScope(strings.Join(fields[:len(fields)-1], " "))
		if !ok {
			return closingReminderBatchCommand{}, false, closingReminderBatchSetTimeFormatText()
		}
		scope.Action = closingReminderBatchActionSetTime
		scope.ReminderTime = reminderTime
		return scope, true, ""
	case strings.HasPrefix(text, "查询下课提醒群类型"):
		value := strings.TrimSpace(strings.TrimPrefix(text, "查询下课提醒群类型"))
		if value == "" {
			return closingReminderBatchCommand{}, false, "格式：查询下课提醒群类型 代收"
		}
		return closingReminderBatchCommand{Action: closingReminderBatchActionQuery, ScopeKind: closingReminderBatchScopeType, ScopeValue: value}, true, ""
	case strings.HasPrefix(text, "查询下课提醒群标签"):
		value := strings.TrimSpace(strings.TrimPrefix(text, "查询下课提醒群标签"))
		if !isSingleClosingReminderBatchTag(value) {
			return closingReminderBatchCommand{}, false, "格式：查询下课提醒群标签 夜班"
		}
		return closingReminderBatchCommand{Action: closingReminderBatchActionQuery, ScopeKind: closingReminderBatchScopeTag, ScopeValue: value}, true, ""
	default:
		return closingReminderBatchCommand{}, false, "未知下课提醒批量命令。"
	}
}

func parseClosingReminderBatchScope(raw string) (closingReminderBatchCommand, bool) {
	raw = strings.TrimSpace(raw)
	for _, kind := range []string{closingReminderBatchScopeType, closingReminderBatchScopeTag} {
		if !strings.HasPrefix(raw, kind) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(raw, kind))
		if value == "" {
			return closingReminderBatchCommand{}, false
		}
		if kind == closingReminderBatchScopeTag && !isSingleClosingReminderBatchTag(value) {
			return closingReminderBatchCommand{}, false
		}
		return closingReminderBatchCommand{ScopeKind: kind, ScopeValue: value}, true
	}
	return closingReminderBatchCommand{}, false
}

func isSingleClosingReminderBatchTag(value string) bool {
	value = strings.TrimSpace(value)
	tags := utils.SplitTags(value)
	return len(tags) == 1 && tags[0] == value
}

func closingReminderBatchSetTimeFormatText() string {
	return "格式：批量设置下课时间 群类型 代收 21:49\n或：批量设置下课时间 群标签 夜班 21:49"
}

func (a *App) listClosingReminderBatchGroups(command closingReminderBatchCommand) ([]db.GroupConfig, error) {
	var (
		groups []db.GroupConfig
		err    error
	)
	switch command.ScopeKind {
	case closingReminderBatchScopeType:
		groups, err = a.db.ListGroupsByType(command.ScopeValue)
	case closingReminderBatchScopeTag:
		groups, err = a.db.ListGroupsByTags([]string{command.ScopeValue})
		if err == nil {
			groups = filterClosingReminderGroupsByExactTag(groups, command.ScopeValue)
		}
	default:
		groups = nil
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].ChatID < groups[j].ChatID })
	return dedupeClosingReminderBatchGroups(groups), nil
}

func filterClosingReminderGroupsByExactTag(groups []db.GroupConfig, tag string) []db.GroupConfig {
	tag = strings.TrimSpace(tag)
	out := make([]db.GroupConfig, 0, len(groups))
	for _, group := range groups {
		for _, current := range utils.SplitTags(group.GroupTags) {
			if current == tag {
				out = append(out, group)
				break
			}
		}
	}
	return out
}

func dedupeClosingReminderBatchGroups(groups []db.GroupConfig) []db.GroupConfig {
	if len(groups) < 2 {
		return groups
	}
	seen := make(map[int64]struct{}, len(groups))
	out := make([]db.GroupConfig, 0, len(groups))
	for _, group := range groups {
		if _, ok := seen[group.ChatID]; ok {
			continue
		}
		seen[group.ChatID] = struct{}{}
		out = append(out, group)
	}
	return out
}

func (a *App) updateClosingReminderBatchConfigs(msg *tgbotapi.Message, command closingReminderBatchCommand, groups []db.GroupConfig) error {
	successCount := 0
	failedCount := 0
	failedGroups := make([]string, 0, 5)
	for _, group := range groups {
		config, err := a.db.GetClosingReminderConfig(group.ChatID)
		if err == nil {
			switch command.Action {
			case closingReminderBatchActionEnable:
				config.Enabled = 1
			case closingReminderBatchActionDisable:
				config.Enabled = 0
			case closingReminderBatchActionSetTime:
				config.ReminderTime = command.ReminderTime
			}
			config.ChatID = group.ChatID
			config.UpdatedBy = msg.From.ID
			err = a.db.UpsertClosingReminderConfig(config)
		}
		if err != nil {
			failedCount++
			if len(failedGroups) < 5 {
				failedGroups = append(failedGroups, closingReminderBatchGroupLabel(group))
			}
			continue
		}
		successCount++
	}

	lines := []string{
		closingReminderBatchActionLabel(command.Action) + "完成：",
		closingReminderBatchScopeLine(command),
		fmt.Sprintf("目标群：%d 个", len(groups)),
		fmt.Sprintf("成功：%d 个", successCount),
		fmt.Sprintf("失败：%d 个", failedCount),
	}
	if command.Action == closingReminderBatchActionSetTime {
		lines = append(lines, "时间："+command.ReminderTime)
	}
	if len(failedGroups) > 0 {
		lines = append(lines, "失败群："+strings.Join(failedGroups, "；"))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) replyClosingReminderBatchQuery(msg *tgbotapi.Message, command closingReminderBatchCommand, groups []db.GroupConfig) error {
	enabledCount := 0
	disabledCount := 0
	timeCounts := make(map[string]int, len(groups))
	failedGroups := make([]string, 0, 5)
	for _, group := range groups {
		config, err := a.db.GetClosingReminderConfig(group.ChatID)
		if err != nil {
			if len(failedGroups) < 5 {
				failedGroups = append(failedGroups, closingReminderBatchGroupLabel(group))
			}
			continue
		}
		if config.Enabled != 0 {
			enabledCount++
		} else {
			disabledCount++
		}
		timeCounts[config.ReminderTime]++
	}
	times := make([]string, 0, len(timeCounts))
	for reminderTime := range timeCounts {
		times = append(times, reminderTime)
	}
	sort.Strings(times)

	lines := []string{
		"下课提醒批量查询：",
		closingReminderBatchScopeLine(command),
		fmt.Sprintf("目标群：%d 个", len(groups)),
		fmt.Sprintf("已开启：%d 个", enabledCount),
		fmt.Sprintf("已关闭：%d 个", disabledCount),
		"时间分布：",
	}
	if len(times) == 0 {
		lines = append(lines, "- 无")
	}
	for _, reminderTime := range times {
		lines = append(lines, fmt.Sprintf("- %s：%d 个", reminderTime, timeCounts[reminderTime]))
	}
	if len(failedGroups) > 0 {
		lines = append(lines, "读取失败："+strings.Join(failedGroups, "；"))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func closingReminderBatchActionLabel(action string) string {
	switch action {
	case closingReminderBatchActionEnable:
		return "批量开启下课提醒"
	case closingReminderBatchActionDisable:
		return "批量关闭下课提醒"
	case closingReminderBatchActionSetTime:
		return "批量设置下课时间"
	default:
		return "批量设置下课提醒"
	}
}

func closingReminderBatchScopeLine(command closingReminderBatchCommand) string {
	return fmt.Sprintf("范围：%s【%s】", command.ScopeKind, command.ScopeValue)
}

func closingReminderBatchGroupLabel(group db.GroupConfig) string {
	title := strings.TrimSpace(group.Title)
	if title == "" {
		title = "未命名群"
	}
	return fmt.Sprintf("%s(%d)", title, group.ChatID)
}

func (a *App) replyClosingReminderConfig(msg *tgbotapi.Message) error {
	config, err := a.db.GetClosingReminderConfig(msg.Chat.ID)
	if err != nil {
		return err
	}
	status := "已关闭"
	if config.Enabled != 0 {
		status = "已开启"
	}
	lines := []string{
		"下课提醒：" + status,
		"时间：" + config.ReminderTime,
		fmt.Sprintf("阈值：%s U", formatClosingReminderAmount(config.ThresholdU)),
		"私聊：" + closingReminderDMModeLabel(config.DMMode),
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) replyClosingReminderTextOverview(msg *tgbotapi.Message) error {
	config, err := a.db.GetClosingReminderConfig(msg.Chat.ID)
	if err != nil {
		return err
	}
	lines := []string{"下课文案："}
	for _, item := range closingReminderStatusLabelList() {
		lines = append(lines, fmt.Sprintf("- %s：%s", item.label, closingReminderGroupTextForStatus(config, item.status)))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) handlePreviewClosingReminderText(msg *tgbotapi.Message, text string) error {
	label := strings.TrimSpace(strings.TrimPrefix(text, "预览下课文案"))
	status, ok := closingReminderStatusFromLabel(label)
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：预览下课文案正常/待确认/无账单/复核/失败")
	}
	config, err := a.db.GetClosingReminderConfig(msg.Chat.ID)
	if err != nil {
		return err
	}
	return a.reply(msg.Chat.ID, msg.MessageID, closingReminderGroupTextForStatus(config, status))
}

func (a *App) handleSetClosingReminderText(msg *tgbotapi.Message, text string) error {
	payload := strings.TrimSpace(strings.TrimPrefix(text, "设置下课文案"))
	status, content, ok := splitClosingReminderStatusPayload(payload)
	if !ok || strings.TrimSpace(content) == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置下课文案正常 文案内容")
	}
	if !closingReminderGroupTextIsSafe(content) {
		return a.reply(msg.Chat.ID, msg.MessageID, "下课文案包含敏感词或金额，请调整为群内体面文案。")
	}
	return a.updateClosingReminderConfig(msg, func(config *db.GroupClosingReminderConfig) {
		setClosingReminderTextField(config, status, content)
	}, "下课文案已更新。")
}

func (a *App) handleRestoreClosingReminderText(msg *tgbotapi.Message, text string) error {
	label := strings.TrimSpace(strings.TrimPrefix(text, "恢复下课文案"))
	status, ok := closingReminderStatusFromLabel(label)
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：恢复下课文案正常/待确认/无账单/复核/失败")
	}
	return a.updateClosingReminderConfig(msg, func(config *db.GroupClosingReminderConfig) {
		setClosingReminderTextField(config, status, "")
	}, "下课文案已恢复默认。")
}

func (a *App) handleRestoreAllClosingReminderTexts(msg *tgbotapi.Message) error {
	return a.updateClosingReminderConfig(msg, func(config *db.GroupClosingReminderConfig) {
		config.TextNormal = ""
		config.TextPending = ""
		config.TextNoData = ""
		config.TextReview = ""
		config.TextFailed = ""
	}, "全部下课文案已恢复默认。")
}

func (a *App) replyClosingReminderDMConfig(msg *tgbotapi.Message) error {
	config, err := a.db.GetClosingReminderConfig(msg.Chat.ID)
	if err != nil {
		return err
	}
	recipients, err := a.db.ListClosingDMRecipients(msg.Chat.ID)
	if err != nil {
		return err
	}
	lines := []string{
		"下课私聊：" + closingReminderDMModeLabel(config.DMMode),
		fmt.Sprintf("指定用户：%d 人", len(recipients)),
	}
	for _, item := range recipients {
		label := orDefault(item.Username, strconv.FormatInt(item.UserID, 10))
		lines = append(lines, "- @"+label)
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) handleSetClosingReminderDMMode(msg *tgbotapi.Message, text string) error {
	mode, ok := closingReminderDMModeFromCommand(text)
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置下课私聊关闭/权限人/操作人/权限人和操作人/指定用户/全部")
	}
	return a.updateClosingReminderConfig(msg, func(config *db.GroupClosingReminderConfig) {
		config.DMMode = mode
	}, "下课私聊已设置为 "+closingReminderDMModeLabel(mode)+"。")
}

func (a *App) handleAddClosingDMRecipients(msg *tgbotapi.Message, text string) error {
	targets, err := a.extractTargetUsers(msg, text)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "请通过回复消息或 @用户名 指定下课私聊用户。")
	}
	for _, target := range targets {
		if err := a.db.UpsertClosingDMRecipient(msg.Chat.ID, target.UserID, target.Username, "", msg.From.ID); err != nil {
			return err
		}
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("下课私聊用户已添加，共 %d 人。", len(targets)))
}

func (a *App) handleDeleteClosingDMRecipients(msg *tgbotapi.Message, text string) error {
	targets, err := a.extractTargetUsers(msg, text)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "请通过回复消息或 @用户名 指定下课私聊用户。")
	}
	deleted := 0
	for _, target := range targets {
		ok, err := a.db.DeleteClosingDMRecipient(msg.Chat.ID, target.UserID)
		if err != nil {
			return err
		}
		if ok {
			deleted++
		}
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("下课私聊用户已删除，共 %d 人。", deleted))
}

func (a *App) handleClearClosingDMRecipients(msg *tgbotapi.Message) error {
	if err := a.db.ClearClosingDMRecipients(msg.Chat.ID); err != nil {
		return err
	}
	return a.reply(msg.Chat.ID, msg.MessageID, "下课私聊指定用户已清空。")
}

func (a *App) updateClosingReminderConfig(msg *tgbotapi.Message, mutate func(*db.GroupClosingReminderConfig), replyText string) error {
	config, err := a.db.GetClosingReminderConfig(msg.Chat.ID)
	if err != nil {
		return err
	}
	mutate(&config)
	config.ChatID = msg.Chat.ID
	config.UpdatedBy = msg.From.ID
	if err := a.db.UpsertClosingReminderConfig(config); err != nil {
		return err
	}
	return a.reply(msg.Chat.ID, msg.MessageID, replyText)
}

func parseClosingReminderTime(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("empty closing reminder time")
	}
	for _, layout := range []string{"15:04", "15:04:05"} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed.Format("15:04"), nil
		}
	}
	return "", fmt.Errorf("invalid closing reminder time %q", raw)
}

type closingReminderStatusLabel struct {
	label  string
	status string
}

func closingReminderStatusLabelList() []closingReminderStatusLabel {
	return []closingReminderStatusLabel{
		{label: closingReminderStatusLabelNormal, status: db.ClosingReminderStatusNormal},
		{label: closingReminderStatusLabelPending, status: db.ClosingReminderStatusPending},
		{label: closingReminderStatusLabelNoData, status: db.ClosingReminderStatusNoData},
		{label: closingReminderStatusLabelReview, status: db.ClosingReminderStatusReview},
		{label: closingReminderStatusLabelFailed, status: db.ClosingReminderStatusFailed},
	}
}

func closingReminderStatusFromLabel(label string) (string, bool) {
	trimmed := strings.TrimSpace(label)
	for _, item := range closingReminderStatusLabelList() {
		if trimmed == item.label {
			return item.status, true
		}
	}
	return "", false
}

func splitClosingReminderStatusPayload(payload string) (string, string, bool) {
	trimmed := strings.TrimSpace(payload)
	for _, item := range closingReminderStatusLabelList() {
		if strings.HasPrefix(trimmed, item.label) {
			content := strings.TrimSpace(strings.TrimPrefix(trimmed, item.label))
			return item.status, content, true
		}
	}
	return "", "", false
}

func setClosingReminderTextField(config *db.GroupClosingReminderConfig, status string, value string) {
	switch status {
	case db.ClosingReminderStatusNormal:
		config.TextNormal = value
	case db.ClosingReminderStatusPending:
		config.TextPending = value
	case db.ClosingReminderStatusNoData:
		config.TextNoData = value
	case db.ClosingReminderStatusReview:
		config.TextReview = value
	case db.ClosingReminderStatusFailed:
		config.TextFailed = value
	}
}

func closingReminderTextField(config db.GroupClosingReminderConfig, status string) string {
	switch status {
	case db.ClosingReminderStatusNormal:
		return strings.TrimSpace(config.TextNormal)
	case db.ClosingReminderStatusPending:
		return strings.TrimSpace(config.TextPending)
	case db.ClosingReminderStatusNoData:
		return strings.TrimSpace(config.TextNoData)
	case db.ClosingReminderStatusReview:
		return strings.TrimSpace(config.TextReview)
	case db.ClosingReminderStatusFailed:
		return strings.TrimSpace(config.TextFailed)
	default:
		return ""
	}
}

func closingReminderGroupTextForStatus(config db.GroupClosingReminderConfig, status string) string {
	custom := closingReminderTextField(config, status)
	if custom != "" && closingReminderGroupTextIsSafe(custom) {
		return custom
	}
	switch status {
	case db.ClosingReminderStatusPending:
		return "下课啦，今天辛苦大家。\n今日账单已同步，部分收尾事项待确认。"
	case db.ClosingReminderStatusNoData:
		return "下课啦，今天辛苦大家。\n今日暂无账单同步。"
	case db.ClosingReminderStatusReview:
		return "下课啦，今天辛苦大家。\n今日账单已同步，收支数据待复核。"
	case db.ClosingReminderStatusFailed:
		return "下课啦，今天辛苦大家。\n今日账单同步稍后确认。"
	default:
		return "下课啦，今天辛苦大家。\n今日账单已同步，收尾完成。"
	}
}

func closingReminderGroupTextIsSafe(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	for _, forbidden := range []string{"金额", "未下发", "忘记下发", "异常", "风险", "错误堆栈", "数据库错误"} {
		if strings.Contains(trimmed, forbidden) {
			return false
		}
	}
	return !closingReminderTextContainsUnsafeNumber(trimmed)
}

func closingReminderTextContainsUnsafeNumber(text string) bool {
	hasChineseNumber := false
	for _, r := range text {
		if unicode.IsNumber(r) {
			return true
		}
		if strings.ContainsRune("零〇一二两三四五六七八九十百千万亿", r) {
			hasChineseNumber = true
		}
	}
	return hasChineseNumber && closingReminderTextContainsAmountContext(text)
}

func closingReminderTextContainsAmountContext(text string) bool {
	lower := strings.ToLower(text)
	for _, token := range []string{
		"usdt", "u", "￥", "¥", "$", "元", "块",
		"金额", "差额", "余额", "待确认", "入款", "下发", "收支", "账单", "账务",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func closingReminderDMModeFromCommand(text string) (string, bool) {
	switch text {
	case "设置下课私聊权限人和操作人":
		return db.ClosingReminderDMModeOwnerOperators, true
	case "设置下课私聊关闭":
		return db.ClosingReminderDMModeOff, true
	case "设置下课私聊权限人":
		return db.ClosingReminderDMModeOwner, true
	case "设置下课私聊操作人":
		return db.ClosingReminderDMModeOperators, true
	case "设置下课私聊指定用户":
		return db.ClosingReminderDMModeCustom, true
	case "设置下课私聊全部":
		return db.ClosingReminderDMModeOwnerOperatorsCustom, true
	default:
		return "", false
	}
}

func closingReminderDMModeLabel(mode string) string {
	switch mode {
	case db.ClosingReminderDMModeOff:
		return "关闭"
	case db.ClosingReminderDMModeOwner:
		return "权限人"
	case db.ClosingReminderDMModeOperators:
		return "操作人"
	case db.ClosingReminderDMModeOwnerOperators:
		return "权限人和操作人"
	case db.ClosingReminderDMModeCustom:
		return "指定用户"
	case db.ClosingReminderDMModeOwnerOperatorsCustom:
		return "权限人、操作人和指定用户"
	default:
		return "权限人、操作人和指定用户"
	}
}

func defaultClosingReminderDMPreviewText(g db.GroupConfig) string {
	title := strings.TrimSpace(g.Title)
	if title == "" {
		title = "当前群"
	}
	return strings.Join([]string{
		"收班明细",
		"群：" + title,
		"账期：账期开始 - 账期结束",
		"状态：待确认",
		"",
		"入款：0 笔 / 0 U",
		"下发：0 笔 / 0 U",
		"待确认：0 U",
		"",
		"处理建议：请按实际账单状态复核。",
	}, "\n")
}

func formatClosingReminderAmount(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}
