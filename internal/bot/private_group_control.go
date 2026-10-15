package bot

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
	"go-bot/internal/utils"
)

type privateGroupControlAction string

const (
	privateGroupControlActionNone      privateGroupControlAction = ""
	privateGroupControlActionTag       privateGroupControlAction = "tag"
	privateGroupControlActionType      privateGroupControlAction = "type"
	privateGroupControlActionCategory  privateGroupControlAction = "cat"
	privateGroupControlActionCloseTime privateGroupControlAction = "closing_time"
	privateGroupControlActionBroadcast privateGroupControlAction = "broadcast"
)

type privateGroupControlCategoryKind string

const (
	privateGroupControlCategoryKindType privateGroupControlCategoryKind = "type"
	privateGroupControlCategoryKindTag  privateGroupControlCategoryKind = "tag"
)

type privateGroupControlCategoryOp string

const (
	privateGroupControlCategoryOpNone   privateGroupControlCategoryOp = ""
	privateGroupControlCategoryOpAdd    privateGroupControlCategoryOp = "add"
	privateGroupControlCategoryOpRemove privateGroupControlCategoryOp = "rm"
)

type privateGroupControlClosingReminderAction string

const (
	privateGroupControlClosingReminderActionNone        privateGroupControlClosingReminderAction = ""
	privateGroupControlClosingReminderActionEnable      privateGroupControlClosingReminderAction = "enable"
	privateGroupControlClosingReminderActionDisable     privateGroupControlClosingReminderAction = "disable"
	privateGroupControlClosingReminderActionSetTime     privateGroupControlClosingReminderAction = "set_time"
	privateGroupControlClosingReminderActionThreshold   privateGroupControlClosingReminderAction = "threshold"
	privateGroupControlClosingReminderActionSetText     privateGroupControlClosingReminderAction = "set_text"
	privateGroupControlClosingReminderActionRestoreText privateGroupControlClosingReminderAction = "restore_text"
	privateGroupControlClosingReminderActionRestoreAll  privateGroupControlClosingReminderAction = "restore_all"
)

type privateGroupControlStage string

const (
	privateGroupControlPageSize                                               = 20
	privateGroupControlSelectionFilterPageSize                                = 20
	privateGroupControlStagePanel                    privateGroupControlStage = "panel"
	privateGroupControlStageSelecting                privateGroupControlStage = "selecting"
	privateGroupControlStageCategoryMenu             privateGroupControlStage = "cat_menu"
	privateGroupControlStageCategoryList             privateGroupControlStage = "cat_list"
	privateGroupControlStageCategoryDetail           privateGroupControlStage = "cat_detail"
	privateGroupControlStageClosingReminderMenu      privateGroupControlStage = "closing_reminder_menu"
	privateGroupControlStageClosingReminderTextMenu  privateGroupControlStage = "closing_reminder_text_menu"
	privateGroupControlStageAwaitingTagInput         privateGroupControlStage = "awaiting_tag"
	privateGroupControlStageAwaitingTypeInput        privateGroupControlStage = "awaiting_type"
	privateGroupControlStageAwaitingCloseTimeInput   privateGroupControlStage = "awaiting_closing_time"
	privateGroupControlStageAwaitingThresholdInput   privateGroupControlStage = "awaiting_threshold"
	privateGroupControlStageAwaitingClosingTextInput privateGroupControlStage = "awaiting_closing_text"
	privateGroupControlStageAwaitingBroadcastContent privateGroupControlStage = "awaiting_broadcast"
	privateGroupControlStageAwaitingConfirm          privateGroupControlStage = "awaiting_confirm"
	privateGroupControlStageRecovery                 privateGroupControlStage = "recovery"
)

type privateGroupControlSession struct {
	SessionID            string
	Version              uint64
	UserID               int64
	PrivateChatID        int64
	Action               privateGroupControlAction
	Stage                privateGroupControlStage
	Groups               []db.GroupConfig
	SelectedChatIDs      map[int64]bool
	CurrentPage          int
	PendingValue         string
	CategoryKind         privateGroupControlCategoryKind
	CategoryValue        string
	CategoryOp           privateGroupControlCategoryOp
	PendingThresholdU    float64
	HasPendingThresholdU bool
	PendingClosingAction string
	PendingClosingStatus string
	ContentMode          string
	Content              string
	ContentPreview       string
	SourceChatID         int64
	SourceMessageID      int
	Executing            bool
	RecoveryIssues       []privateGroupControlTargetIssue
	UpdatedAt            time.Time
}

type privateGroupControlCallbackData struct {
	SessionID string
	Version   uint64
	Verb      string
	Arg       string
}

type privateGroupControlExecutionResult struct {
	Expected      int
	Succeeded     int
	Failed        int
	Invalid       int
	FailedGroups  []string
	InvalidGroups []string
	FailedIssues  []privateGroupControlTargetIssue
	InvalidIssues []privateGroupControlTargetIssue
}

type privateGroupControlTargetIssue struct {
	ChatID int64
	Title  string
	Reason string
}

type privateGroupControlGroupsCacheEntry struct {
	Groups    []db.GroupConfig
	CheckedAt time.Time
}

type privateGroupControlMemberCacheKey struct {
	ChatID int64
	UserID int64
}

type privateGroupControlMemberCacheEntry struct {
	IsMember  bool
	CheckedAt time.Time
}

type broadcastBatchResult struct {
	Expected     int
	Succeeded    int
	Failed       int
	FailedGroups []string
	FailedIssues []privateGroupControlTargetIssue
}

type privateGroupControlSelectionOption struct {
	Value string
	Count int
}

const (
	privateGroupControlBroadcastConcurrency       = 8
	privateGroupControlMemberCheckConcurrency     = 8
	privateGroupControlCategoryCASMaxAttempts     = 3
	privateGroupControlCategoryConcurrentModified = "当前配置被并发修改，请重试"
	privateGroupControlCacheTTL                   = 30 * time.Second
)

func (a *App) replyPrivateGroupControlPanel(chatID int64, replyTo int, userID int64) error {
	session := a.newPrivateGroupControlPanelSession(userID, chatID)
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlPanel(session)
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyToMessageID = replyTo
	m.ReplyMarkup = markup
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) editPrivateGroupControlPanel(cq *tgbotapi.CallbackQuery, userID int64) error {
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil {
		return nil
	}
	session := a.newPrivateGroupControlPanelSession(userID, cq.Message.Chat.ID)
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlPanel(session)
	a.answerCallback(cq.ID, "已打开群控面板", false)
	edit := tgbotapi.NewEditMessageTextAndMarkup(cq.Message.Chat.ID, cq.Message.MessageID, text, markup)
	if err := a.sendWithPriority(outboundPriorityNormalChat, edit); err != nil && !isMessageNotModified(err) {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "打开群控面板失败，请稍后重试。")
		return err
	}
	return nil
}

func (a *App) newPrivateGroupControlPanelSession(userID, privateChatID int64) privateGroupControlSession {
	return privateGroupControlSession{
		SessionID:       newPrivateGroupControlSessionID(),
		Version:         1,
		UserID:          userID,
		PrivateChatID:   privateChatID,
		Action:          privateGroupControlActionNone,
		Stage:           privateGroupControlStagePanel,
		SelectedChatIDs: map[int64]bool{},
		UpdatedAt:       a.currentTime(),
	}
}

func newPrivateGroupControlSessionID() string {
	var b [4]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

func (a *App) setPrivateGroupControlSession(session privateGroupControlSession) {
	if a == nil || session.UserID == 0 {
		return
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	if a.privateGroupControlSessions == nil {
		a.privateGroupControlSessions = map[int64]privateGroupControlSession{}
	}
	a.privateGroupControlSessions[session.UserID] = clonePrivateGroupControlSession(session)
}

func (a *App) getPrivateGroupControlSession(userID int64) (privateGroupControlSession, bool) {
	if a == nil || userID == 0 {
		return privateGroupControlSession{}, false
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	if a.privateGroupControlSessions == nil {
		return privateGroupControlSession{}, false
	}
	session, ok := a.privateGroupControlSessions[userID]
	if !ok {
		return privateGroupControlSession{}, false
	}
	if !session.UpdatedAt.IsZero() && a.currentTime().Sub(session.UpdatedAt) > batchTagSessionTTL {
		delete(a.privateGroupControlSessions, userID)
		return privateGroupControlSession{}, false
	}
	return clonePrivateGroupControlSession(session), true
}

func (a *App) clearPrivateGroupControlSession(userID int64) {
	if a == nil || userID == 0 {
		return
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	if a.privateGroupControlSessions == nil {
		return
	}
	delete(a.privateGroupControlSessions, userID)
}

func clonePrivateGroupControlCachedGroups(groups []db.GroupConfig) []db.GroupConfig {
	if len(groups) == 0 {
		return nil
	}
	out := make([]db.GroupConfig, len(groups))
	copy(out, groups)
	return out
}

func (a *App) getPrivateGroupControlGroupsCache(userID int64) ([]db.GroupConfig, bool) {
	if a == nil || userID == 0 {
		return nil, false
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	if a.privateGroupControlGroups == nil {
		return nil, false
	}
	entry, ok := a.privateGroupControlGroups[userID]
	if !ok {
		return nil, false
	}
	if !entry.CheckedAt.IsZero() && a.currentTime().Sub(entry.CheckedAt) > privateGroupControlCacheTTL {
		delete(a.privateGroupControlGroups, userID)
		return nil, false
	}
	return clonePrivateGroupControlCachedGroups(entry.Groups), true
}

func (a *App) setPrivateGroupControlGroupsCache(userID int64, groups []db.GroupConfig) {
	if a == nil || userID == 0 {
		return
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	if a.privateGroupControlGroups == nil {
		a.privateGroupControlGroups = map[int64]privateGroupControlGroupsCacheEntry{}
	}
	a.privateGroupControlGroups[userID] = privateGroupControlGroupsCacheEntry{
		Groups:    clonePrivateGroupControlCachedGroups(groups),
		CheckedAt: a.currentTime(),
	}
}

func (a *App) getPrivateGroupControlMemberCache(chatID, userID int64) (bool, bool) {
	if a == nil || chatID == 0 || userID == 0 {
		return false, false
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	if a.privateGroupControlMembers == nil {
		return false, false
	}
	key := privateGroupControlMemberCacheKey{ChatID: chatID, UserID: userID}
	entry, ok := a.privateGroupControlMembers[key]
	if !ok {
		return false, false
	}
	if !entry.CheckedAt.IsZero() && a.currentTime().Sub(entry.CheckedAt) > privateGroupControlCacheTTL {
		delete(a.privateGroupControlMembers, key)
		return false, false
	}
	return entry.IsMember, true
}

func (a *App) setPrivateGroupControlMemberCache(chatID, userID int64, isMember bool) {
	if a == nil || chatID == 0 || userID == 0 {
		return
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	if a.privateGroupControlMembers == nil {
		a.privateGroupControlMembers = map[privateGroupControlMemberCacheKey]privateGroupControlMemberCacheEntry{}
	}
	key := privateGroupControlMemberCacheKey{ChatID: chatID, UserID: userID}
	a.privateGroupControlMembers[key] = privateGroupControlMemberCacheEntry{
		IsMember:  isMember,
		CheckedAt: a.currentTime(),
	}
}

func (a *App) clearPrivateGroupControlCaches(userID int64) {
	if a == nil {
		return
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	if userID != 0 && a.privateGroupControlGroups != nil {
		delete(a.privateGroupControlGroups, userID)
	}
	a.privateGroupControlMembers = map[privateGroupControlMemberCacheKey]privateGroupControlMemberCacheEntry{}
}

func (a *App) listPrivateGroupControlPrivilegedGroups(userID int64) ([]db.GroupConfig, error) {
	if userID == 0 {
		return nil, nil
	}
	if cached, ok := a.getPrivateGroupControlGroupsCache(userID); ok {
		return cached, nil
	}
	rows, err := a.ListPrivilegedGroups(userID)
	if err != nil {
		return nil, err
	}
	a.setPrivateGroupControlGroupsCache(userID, rows)
	return rows, nil
}

func (a *App) isPrivateGroupControlMemberCached(chatID, userID int64) bool {
	if cached, ok := a.getPrivateGroupControlMemberCache(chatID, userID); ok {
		return cached
	}
	isMember := a.IsGroupMember(chatID, userID)
	a.setPrivateGroupControlMemberCache(chatID, userID, isMember)
	return isMember
}

func (a *App) markPrivateGroupControlExecuting(userID int64, parsed privateGroupControlCallbackData, privateChatID int64) (privateGroupControlSession, string, bool) {
	if a == nil || userID == 0 {
		return privateGroupControlSession{}, "会话已过期，请重新打开群控面板。", false
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	session, ok := a.privateGroupControlSessions[userID]
	if !ok {
		return privateGroupControlSession{}, "任务正在执行或已完成。", false
	}
	if !session.UpdatedAt.IsZero() && a.currentTime().Sub(session.UpdatedAt) > batchTagSessionTTL {
		delete(a.privateGroupControlSessions, userID)
		return privateGroupControlSession{}, "会话已过期，请重新打开群控面板。", false
	}
	if session.PrivateChatID != privateChatID || session.UserID != userID {
		return privateGroupControlSession{}, "无权操作此面板。", false
	}
	if session.SessionID != parsed.SessionID || session.Version != parsed.Version {
		return privateGroupControlSession{}, "面板已刷新，请使用最新面板。", false
	}
	if session.Executing {
		return privateGroupControlSession{}, "任务正在执行或已完成。", false
	}
	canConfirm := session.Stage == privateGroupControlStageAwaitingConfirm
	if session.Action == privateGroupControlActionCategory &&
		session.Stage == privateGroupControlStageSelecting &&
		privateGroupControlSessionHasCategoryPayload(session) {
		if countPrivateGroupControlSelected(session.SelectedChatIDs) == 0 {
			return privateGroupControlSession{}, "请至少选择一个群。", false
		}
		session.Stage = privateGroupControlStageAwaitingConfirm
		canConfirm = true
	}
	if !canConfirm {
		return privateGroupControlSession{}, "当前步骤不能确认，请按面板提示继续。", false
	}
	session.Executing = true
	session.UpdatedAt = a.currentTime()
	a.privateGroupControlSessions[userID] = session
	return clonePrivateGroupControlSession(session), "", true
}

func (a *App) markPrivateGroupControlRecoveryExecuting(userID int64, parsed privateGroupControlCallbackData, privateChatID int64) (privateGroupControlSession, string, bool) {
	if a == nil || userID == 0 {
		return privateGroupControlSession{}, "会话已过期，请重新打开群控面板。", false
	}
	a.privateGroupControlMu.Lock()
	defer a.privateGroupControlMu.Unlock()
	session, ok := a.privateGroupControlSessions[userID]
	if !ok {
		return privateGroupControlSession{}, "任务正在执行或已完成。", false
	}
	if !session.UpdatedAt.IsZero() && a.currentTime().Sub(session.UpdatedAt) > batchTagSessionTTL {
		delete(a.privateGroupControlSessions, userID)
		return privateGroupControlSession{}, "会话已过期，请重新打开群控面板。", false
	}
	if session.PrivateChatID != privateChatID || session.UserID != userID {
		return privateGroupControlSession{}, "无权操作此面板。", false
	}
	if session.SessionID != parsed.SessionID || session.Version != parsed.Version {
		return privateGroupControlSession{}, "面板已刷新，请使用最新面板。", false
	}
	if session.Executing {
		return privateGroupControlSession{}, "任务正在执行或已完成。", false
	}
	if session.Stage != privateGroupControlStageRecovery || countPrivateGroupControlSelected(session.SelectedChatIDs) == 0 {
		return privateGroupControlSession{}, "当前没有可重试的失败群。", false
	}
	session.Executing = true
	session.UpdatedAt = a.currentTime()
	a.privateGroupControlSessions[userID] = session
	return clonePrivateGroupControlSession(session), "", true
}

func clonePrivateGroupControlSession(session privateGroupControlSession) privateGroupControlSession {
	session.Groups = append([]db.GroupConfig(nil), session.Groups...)
	session.RecoveryIssues = append([]privateGroupControlTargetIssue(nil), session.RecoveryIssues...)
	if session.SelectedChatIDs == nil {
		session.SelectedChatIDs = map[int64]bool{}
		return session
	}
	selected := make(map[int64]bool, len(session.SelectedChatIDs))
	for chatID, ok := range session.SelectedChatIDs {
		selected[chatID] = ok
	}
	session.SelectedChatIDs = selected
	return session
}

func buildPrivateGroupControlCallbackData(session privateGroupControlSession, verb string, arg ...string) string {
	parts := []string{
		strings.TrimSuffix(privateGroupControlCallbackPrefix, ":"),
		session.SessionID,
		strconv.FormatUint(session.Version, 10),
		strings.TrimSpace(verb),
	}
	if len(arg) > 0 && strings.TrimSpace(arg[0]) != "" {
		parts = append(parts, strings.TrimSpace(arg[0]))
	}
	data := strings.Join(parts, ":")
	if len(data) > 64 {
		return privateGroupControlCallbackPrefix + "invalid"
	}
	return data
}

func parsePrivateGroupControlCallbackData(data string) (privateGroupControlCallbackData, error) {
	raw := strings.TrimSpace(data)
	if len(raw) == 0 || len(raw) > 64 {
		return privateGroupControlCallbackData{}, errors.New("invalid callback length")
	}
	if !strings.HasPrefix(raw, privateGroupControlCallbackPrefix) {
		return privateGroupControlCallbackData{}, errors.New("invalid callback prefix")
	}
	parts := strings.Split(raw, ":")
	if len(parts) < 4 || len(parts) > 5 || parts[0] != "pc" {
		return privateGroupControlCallbackData{}, errors.New("invalid callback format")
	}
	version, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return privateGroupControlCallbackData{}, errors.New("invalid callback version")
	}
	if strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[3]) == "" {
		return privateGroupControlCallbackData{}, errors.New("invalid callback token")
	}
	out := privateGroupControlCallbackData{
		SessionID: parts[1],
		Version:   version,
		Verb:      parts[3],
	}
	if len(parts) == 5 {
		out.Arg = parts[4]
	}
	return out, nil
}

func (a *App) renderPrivateGroupControlPanel(session privateGroupControlSession) (string, tgbotapi.InlineKeyboardMarkup) {
	text := strings.Join([]string{
		"私聊群控面板",
		"请选择操作：",
		"权限：仅机器人系统权限人/操作人可操作，不要求 Telegram 管理员。",
		"条件：机器人仍在群内才会显示和执行。",
	}, "\n")
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("批量设置群标签", buildPrivateGroupControlCallbackData(session, "action", string(privateGroupControlActionTag))),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("批量设置群类型", buildPrivateGroupControlCallbackData(session, "action", string(privateGroupControlActionType))),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("类型/标签列表管理", buildPrivateGroupControlCallbackData(session, "cat")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("下课提醒管理", buildPrivateGroupControlCallbackData(session, "action", string(privateGroupControlActionCloseTime))),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("选择群群发", buildPrivateGroupControlCallbackData(session, "action", string(privateGroupControlActionBroadcast))),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("刷新群列表", buildPrivateGroupControlCallbackData(session, "panel")),
			tgbotapi.NewInlineKeyboardButtonData("返回主菜单", privateCallbackPrefix+"menu"),
		),
	}
	return text, tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (a *App) renderPrivateGroupControlSelection(session privateGroupControlSession) (string, tgbotapi.InlineKeyboardMarkup) {
	if len(session.Groups) == 0 {
		return "未找到可管理群。", tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("返回群控面板", buildPrivateGroupControlCallbackData(session, "panel"))),
		)
	}
	totalPages := privateGroupControlTotalPages(len(session.Groups))
	page := clampPrivateGroupControlPage(session.CurrentPage, totalPages)
	start := page * privateGroupControlPageSize
	end := start + privateGroupControlPageSize
	if end > len(session.Groups) {
		end = len(session.Groups)
	}

	lines := []string{
		privateGroupControlSelectionTitleForSession(session),
		fmt.Sprintf("操作：%s", privateGroupControlActionLabelForSession(session)),
		fmt.Sprintf("页码：%d/%d", page+1, totalPages),
		fmt.Sprintf("已选：%d 个群", countPrivateGroupControlSelected(session.SelectedChatIDs)),
		"--------------------------------",
	}
	if session.Action == privateGroupControlActionCloseTime {
		lines = []string{
			"下课提醒管理",
			"第1步：先选择目标群，下一步再设置时间、U阈值或文案。",
			fmt.Sprintf("页码：%d/%d", page+1, totalPages),
			fmt.Sprintf("已选：%d 个群", countPrivateGroupControlSelected(session.SelectedChatIDs)),
			"--------------------------------",
		}
	}
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, privateGroupControlPageSize+4)
	for i := start; i < end; i++ {
		group := session.Groups[i]
		title := privateGroupControlGroupTitle(group)
		prefix := "☐"
		if session.SelectedChatIDs[group.ChatID] {
			prefix = "☑"
		}
		lines = append(lines, privateGroupControlGroupDetailLines(group, prefix)...)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s %s", prefix, title), buildPrivateGroupControlCallbackData(session, "group", strconv.FormatInt(group.ChatID, 10))),
		))
	}
	lines = append(lines, "--------------------------------")
	lines = append(lines, "请点击群按钮选择目标。")

	prevPage := page - 1
	if prevPage < 0 {
		prevPage = 0
	}
	nextPage := page + 1
	if nextPage >= totalPages {
		nextPage = totalPages - 1
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("上一页", buildPrivateGroupControlCallbackData(session, "page", strconv.Itoa(prevPage))),
		tgbotapi.NewInlineKeyboardButtonData("下一页", buildPrivateGroupControlCallbackData(session, "page", strconv.Itoa(nextPage))),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("全选本页", buildPrivateGroupControlCallbackData(session, "select_all")),
		tgbotapi.NewInlineKeyboardButtonData("全选全部群", buildPrivateGroupControlCallbackData(session, "select_all_groups")),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("按类型选择", buildPrivateGroupControlCallbackData(session, "type_menu")),
		tgbotapi.NewInlineKeyboardButtonData("按标签选择", buildPrivateGroupControlCallbackData(session, "tag_menu")),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("清空选择", buildPrivateGroupControlCallbackData(session, "clear")),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(privateGroupControlSelectionConfirmLabel(session), buildPrivateGroupControlCallbackData(session, privateGroupControlSelectionConfirmVerb(session))),
		tgbotapi.NewInlineKeyboardButtonData("取消", buildPrivateGroupControlCallbackData(session, "cancel")),
	))
	return strings.Join(lines, "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (a *App) openPrivateGroupControlCategoryMenu(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	switch session.Stage {
	case privateGroupControlStagePanel, privateGroupControlStageCategoryMenu, privateGroupControlStageCategoryList, privateGroupControlStageCategoryDetail:
	default:
		a.answerCallback(cq.ID, "当前步骤不能打开列表管理，请返回群控面板。", true)
		return nil
	}
	a.answerCallback(cq.ID, "正在读取群列表...", false)
	if err := a.refreshPrivateGroupControlCategoryGroups(&session); err != nil {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "读取群列表失败，请稍后重试。")
		return err
	}
	if len(session.Groups) == 0 {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "未找到可管理群。")
		return nil
	}
	session.Action = privateGroupControlActionCategory
	session.Stage = privateGroupControlStageCategoryMenu
	session.SelectedChatIDs = map[int64]bool{}
	session.CurrentPage = 0
	session.PendingValue = ""
	privateGroupControlClearCategoryPending(&session)
	privateGroupControlClearClosingReminderPending(&session)
	session.Content = ""
	session.ContentMode = ""
	session.ContentPreview = ""
	session.SourceChatID = 0
	session.SourceMessageID = 0
	session.RecoveryIssues = nil
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlCategoryMenu(session)
	return a.editPrivateGroupControlMessageAfterCallback(cq, text, markup)
}

func (a *App) refreshPrivateGroupControlCategoryGroups(session *privateGroupControlSession) error {
	if a == nil || session == nil {
		return nil
	}
	groups, err := a.listPrivateGroupControlEligibleGroups(session.UserID)
	if err != nil {
		return err
	}
	session.Groups = groups
	return nil
}

func (a *App) renderPrivateGroupControlCategoryMenu(session privateGroupControlSession) (string, tgbotapi.InlineKeyboardMarkup) {
	typeCount := len(privateGroupControlExistingSelectionOptions(session.Groups, string(privateGroupControlCategoryKindType)))
	tagCount := len(privateGroupControlExistingSelectionOptions(session.Groups, string(privateGroupControlCategoryKindTag)))
	lines := []string{
		"类型/标签列表管理",
		"基于当前群配置实时聚合，不单独维护词库。",
		fmt.Sprintf("可管理群：%d 个", len(session.Groups)),
		fmt.Sprintf("类型列表：%d 个", typeCount),
		fmt.Sprintf("标签列表：%d 个", tagCount),
		"--------------------------------",
		"可查看某个类型/标签下的群，并添加或移出群。",
	}
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("类型列表", buildPrivateGroupControlCallbackData(session, "ctl", "0")),
			tgbotapi.NewInlineKeyboardButtonData("标签列表", buildPrivateGroupControlCallbackData(session, "tgl", "0")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("返回群控面板", buildPrivateGroupControlCallbackData(session, "panel")),
			tgbotapi.NewInlineKeyboardButtonData("取消", buildPrivateGroupControlCallbackData(session, "cancel")),
		),
	}
	return strings.Join(lines, "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (a *App) showPrivateGroupControlCategoryList(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, kind privateGroupControlCategoryKind, rawPage string) error {
	if session.Action != privateGroupControlActionCategory {
		a.answerCallback(cq.ID, "当前步骤不能查看类型/标签列表。", true)
		return nil
	}
	switch session.Stage {
	case privateGroupControlStageCategoryMenu, privateGroupControlStageCategoryList, privateGroupControlStageCategoryDetail:
	default:
		a.answerCallback(cq.ID, "当前步骤不能查看类型/标签列表。", true)
		return nil
	}
	if !privateGroupControlCategoryKindValid(kind) {
		a.answerCallback(cq.ID, "列表类型无效。", true)
		return nil
	}
	page, err := strconv.Atoi(strings.TrimSpace(rawPage))
	if strings.TrimSpace(rawPage) == "" {
		page = 0
		err = nil
	}
	if err != nil {
		a.answerCallback(cq.ID, "页码无效。", true)
		return nil
	}
	a.answerCallback(cq.ID, "正在读取群列表...", false)
	if err := a.refreshPrivateGroupControlCategoryGroups(&session); err != nil {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "读取群列表失败，请稍后重试。")
		return err
	}
	session.Stage = privateGroupControlStageCategoryList
	session.CategoryKind = kind
	session.CategoryValue = ""
	session.CategoryOp = privateGroupControlCategoryOpNone
	session.SelectedChatIDs = map[int64]bool{}
	session.CurrentPage = clampPrivateGroupControlSelectionFilterPage(page, privateGroupControlSelectionFilterTotalPages(len(privateGroupControlExistingSelectionOptions(session.Groups, string(kind)))))
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := renderPrivateGroupControlCategoryList(session, kind, session.CurrentPage)
	return a.editPrivateGroupControlMessageAfterCallback(cq, text, markup)
}

func renderPrivateGroupControlCategoryList(session privateGroupControlSession, kind privateGroupControlCategoryKind, rawPage int) (string, tgbotapi.InlineKeyboardMarkup) {
	options := privateGroupControlExistingSelectionOptions(session.Groups, string(kind))
	totalPages := privateGroupControlSelectionFilterTotalPages(len(options))
	page := clampPrivateGroupControlSelectionFilterPage(rawPage, totalPages)
	start := page * privateGroupControlSelectionFilterPageSize
	end := start + privateGroupControlSelectionFilterPageSize
	if end > len(options) {
		end = len(options)
	}
	title := privateGroupControlCategoryKindLabel(kind) + "列表"
	lines := []string{
		title,
		"类型/标签列表管理",
		fmt.Sprintf("页码：%d/%d", page+1, totalPages),
		fmt.Sprintf("可管理群：%d 个", len(session.Groups)),
		"--------------------------------",
	}
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, privateGroupControlSelectionFilterPageSize+3)
	if len(options) == 0 {
		lines = append(lines, "暂无已有"+privateGroupControlCategoryKindLabel(kind)+"。")
	} else {
		for i := start; i < end; i++ {
			option := options[i]
			lines = append(lines, fmt.Sprintf("%s：%d 个群", option.Value, option.Count))
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s (%d)", trimForPreview(option.Value, 20), option.Count), buildPrivateGroupControlCallbackData(session, privateGroupControlCategoryDetailVerb(kind), strconv.Itoa(i))),
			))
		}
	}
	prevPage := page - 1
	if prevPage < 0 {
		prevPage = 0
	}
	nextPage := page + 1
	if nextPage >= totalPages {
		nextPage = totalPages - 1
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("上一页", buildPrivateGroupControlCallbackData(session, privateGroupControlCategoryListVerb(kind), strconv.Itoa(prevPage))),
		tgbotapi.NewInlineKeyboardButtonData("下一页", buildPrivateGroupControlCallbackData(session, privateGroupControlCategoryListVerb(kind), strconv.Itoa(nextPage))),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("返回管理", buildPrivateGroupControlCallbackData(session, "cat")),
		tgbotapi.NewInlineKeyboardButtonData("返回群控面板", buildPrivateGroupControlCallbackData(session, "panel")),
	))
	return strings.Join(lines, "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (a *App) showPrivateGroupControlCategoryDetail(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, kind privateGroupControlCategoryKind, rawIndex string) error {
	if session.Action != privateGroupControlActionCategory || session.Stage != privateGroupControlStageCategoryList {
		a.answerCallback(cq.ID, "当前步骤不能查看详情。", true)
		return nil
	}
	if !privateGroupControlCategoryKindValid(kind) {
		a.answerCallback(cq.ID, "列表类型无效。", true)
		return nil
	}
	options := privateGroupControlExistingSelectionOptions(session.Groups, string(kind))
	index, err := strconv.Atoi(strings.TrimSpace(rawIndex))
	if err != nil || index < 0 || index >= len(options) {
		a.answerCallback(cq.ID, "列表项已失效，请重新打开列表。", true)
		return nil
	}
	value := options[index].Value
	a.answerCallback(cq.ID, "正在读取群列表...", false)
	if err := a.refreshPrivateGroupControlCategoryGroups(&session); err != nil {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "读取群列表失败，请稍后重试。")
		return err
	}
	session.Stage = privateGroupControlStageCategoryDetail
	session.CategoryKind = kind
	session.CategoryValue = value
	session.CategoryOp = privateGroupControlCategoryOpNone
	session.SelectedChatIDs = map[int64]bool{}
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := renderPrivateGroupControlCategoryDetail(session)
	return a.editPrivateGroupControlMessageAfterCallback(cq, text, markup)
}

func renderPrivateGroupControlCategoryDetail(session privateGroupControlSession) (string, tgbotapi.InlineKeyboardMarkup) {
	value := strings.TrimSpace(session.CategoryValue)
	kindLabel := privateGroupControlCategoryKindLabel(session.CategoryKind)
	matched := privateGroupControlFilterCategoryGroups(session.Groups, session.CategoryKind, privateGroupControlCategoryOpRemove, value)
	lines := []string{
		kindLabel + "详情",
		fmt.Sprintf("%s：%s", kindLabel, value),
		fmt.Sprintf("群数：%d", len(matched)),
		"--------------------------------",
	}
	if len(matched) == 0 {
		lines = append(lines, "暂无匹配群。")
	} else {
		for _, group := range matched {
			lines = append(lines, privateGroupControlGroupDetailLines(group, "•")...)
		}
	}
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("添加群到"+value, buildPrivateGroupControlCallbackData(session, privateGroupControlCategoryAddVerb(session.CategoryKind))),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("从"+value+"移出群", buildPrivateGroupControlCallbackData(session, privateGroupControlCategoryRemoveVerb(session.CategoryKind))),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("返回"+kindLabel+"列表", buildPrivateGroupControlCallbackData(session, privateGroupControlCategoryListVerb(session.CategoryKind), strconv.Itoa(session.CurrentPage))),
			tgbotapi.NewInlineKeyboardButtonData("返回管理", buildPrivateGroupControlCallbackData(session, "cat")),
		),
	}
	return strings.Join(lines, "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (a *App) startPrivateGroupControlCategorySelection(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, kind privateGroupControlCategoryKind, op privateGroupControlCategoryOp) error {
	if session.Action != privateGroupControlActionCategory || session.Stage != privateGroupControlStageCategoryDetail {
		a.answerCallback(cq.ID, "当前步骤不能选择群。", true)
		return nil
	}
	if !privateGroupControlCategoryKindValid(kind) || (op != privateGroupControlCategoryOpAdd && op != privateGroupControlCategoryOpRemove) {
		a.answerCallback(cq.ID, "类型/标签操作无效。", true)
		return nil
	}
	if session.CategoryKind != kind || strings.TrimSpace(session.CategoryValue) == "" {
		a.answerCallback(cq.ID, "当前类型/标签已失效，请重新打开详情。", true)
		return nil
	}
	a.answerCallback(cq.ID, "正在读取群列表...", false)
	if err := a.refreshPrivateGroupControlCategoryGroups(&session); err != nil {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "读取群列表失败，请稍后重试。")
		return err
	}
	session.Groups = privateGroupControlFilterCategoryGroups(session.Groups, kind, op, session.CategoryValue)
	if len(session.Groups) == 0 {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "暂无可操作的群。")
		return nil
	}
	session.Stage = privateGroupControlStageSelecting
	session.CategoryOp = op
	session.SelectedChatIDs = map[int64]bool{}
	session.CurrentPage = 0
	session.PendingValue = ""
	privateGroupControlClearClosingReminderPending(&session)
	session.Content = ""
	session.ContentMode = ""
	session.ContentPreview = ""
	session.SourceChatID = 0
	session.SourceMessageID = 0
	session.RecoveryIssues = nil
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlSelection(session)
	return a.editPrivateGroupControlMessageAfterCallback(cq, text, markup)
}

func privateGroupControlFilterCategoryGroups(groups []db.GroupConfig, kind privateGroupControlCategoryKind, op privateGroupControlCategoryOp, value string) []db.GroupConfig {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	out := make([]db.GroupConfig, 0, len(groups))
	for _, group := range groups {
		matched := privateGroupControlGroupMatchesExistingSelection(group, string(kind), value)
		switch op {
		case privateGroupControlCategoryOpAdd:
			if !matched {
				out = append(out, group)
			}
		case privateGroupControlCategoryOpRemove:
			if matched {
				out = append(out, group)
			}
		}
	}
	return out
}

func privateGroupControlCategoryKindValid(kind privateGroupControlCategoryKind) bool {
	return kind == privateGroupControlCategoryKindType || kind == privateGroupControlCategoryKindTag
}

func privateGroupControlCategoryKindLabel(kind privateGroupControlCategoryKind) string {
	switch kind {
	case privateGroupControlCategoryKindType:
		return "类型"
	case privateGroupControlCategoryKindTag:
		return "标签"
	default:
		return "列表"
	}
}

func privateGroupControlCategoryListVerb(kind privateGroupControlCategoryKind) string {
	switch kind {
	case privateGroupControlCategoryKindType:
		return "ctl"
	case privateGroupControlCategoryKindTag:
		return "tgl"
	default:
		return ""
	}
}

func privateGroupControlCategoryDetailVerb(kind privateGroupControlCategoryKind) string {
	switch kind {
	case privateGroupControlCategoryKindType:
		return "ctv"
	case privateGroupControlCategoryKindTag:
		return "tgv"
	default:
		return ""
	}
}

func privateGroupControlCategoryAddVerb(kind privateGroupControlCategoryKind) string {
	switch kind {
	case privateGroupControlCategoryKindType:
		return "cta"
	case privateGroupControlCategoryKindTag:
		return "tga"
	default:
		return ""
	}
}

func privateGroupControlCategoryRemoveVerb(kind privateGroupControlCategoryKind) string {
	switch kind {
	case privateGroupControlCategoryKindType:
		return "ctr"
	case privateGroupControlCategoryKindTag:
		return "tgr"
	default:
		return ""
	}
}

func privateGroupControlClearCategoryPending(session *privateGroupControlSession) {
	if session == nil {
		return
	}
	session.CategoryKind = ""
	session.CategoryValue = ""
	session.CategoryOp = privateGroupControlCategoryOpNone
}

func privateGroupControlSelectionTitle(action privateGroupControlAction) string {
	switch action {
	case privateGroupControlActionTag:
		return "选择群标签目标"
	case privateGroupControlActionType:
		return "选择群类型目标"
	case privateGroupControlActionCategory:
		return "选择类型/标签目标"
	case privateGroupControlActionCloseTime:
		return "下课提醒管理"
	case privateGroupControlActionBroadcast:
		return "选择群发目标"
	default:
		return "选择目标群"
	}
}

func privateGroupControlSelectionTitleForSession(session privateGroupControlSession) string {
	if session.Action == privateGroupControlActionCategory {
		if label := privateGroupControlCategoryOperationLabel(session); label != "" {
			return label
		}
	}
	return privateGroupControlSelectionTitle(session.Action)
}

func privateGroupControlActionLabel(action privateGroupControlAction) string {
	switch action {
	case privateGroupControlActionTag:
		return "批量设置群标签"
	case privateGroupControlActionType:
		return "批量设置群类型"
	case privateGroupControlActionCategory:
		return "类型/标签列表管理"
	case privateGroupControlActionCloseTime:
		return "下课提醒管理"
	case privateGroupControlActionBroadcast:
		return "选择群群发"
	default:
		return "群控"
	}
}

func privateGroupControlActionLabelForSession(session privateGroupControlSession) string {
	if session.Action == privateGroupControlActionCategory {
		if label := privateGroupControlCategoryOperationLabel(session); label != "" {
			return label
		}
	}
	return privateGroupControlActionLabel(session.Action)
}

func privateGroupControlCategoryOperationLabel(session privateGroupControlSession) string {
	value := strings.TrimSpace(session.CategoryValue)
	if value == "" {
		return ""
	}
	switch session.CategoryOp {
	case privateGroupControlCategoryOpAdd:
		return "添加群到" + value
	case privateGroupControlCategoryOpRemove:
		return "从" + value + "移出群"
	default:
		return ""
	}
}

func privateGroupControlGroupTitle(group db.GroupConfig) string {
	title := strings.TrimSpace(group.Title)
	if title == "" {
		title = "未命名群"
	}
	return title
}

func privateGroupControlGroupDetailLines(group db.GroupConfig, prefix string) []string {
	return []string{
		fmt.Sprintf("%s %s", strings.TrimSpace(prefix), privateGroupControlGroupTitle(group)),
		"  现有类型：" + privateGroupControlGroupTypeDisplay(group),
		"  现有标签：" + privateGroupControlGroupTagsDisplay(group),
	}
}

func privateGroupControlGroupTypeDisplay(group db.GroupConfig) string {
	value := strings.TrimSpace(group.GroupType)
	if value == "" {
		return "未设置"
	}
	return value
}

func privateGroupControlGroupTagsDisplay(group db.GroupConfig) string {
	tags := utils.SplitTags(group.GroupTags)
	if len(tags) == 0 {
		return "未设置"
	}
	return strings.Join(tags, "、")
}

func privateGroupControlSelectionConfirmLabel(session privateGroupControlSession) string {
	if session.Action == privateGroupControlActionCategory {
		switch session.CategoryOp {
		case privateGroupControlCategoryOpAdd:
			return "确认添加"
		case privateGroupControlCategoryOpRemove:
			return "确认移出"
		}
	}
	return "确认选择"
}

func privateGroupControlSelectionConfirmVerb(session privateGroupControlSession) string {
	if session.Action == privateGroupControlActionCategory && privateGroupControlSessionHasCategoryPayload(session) {
		return "confirm"
	}
	return "choose"
}

func countPrivateGroupControlSelected(selected map[int64]bool) int {
	count := 0
	for _, ok := range selected {
		if ok {
			count++
		}
	}
	return count
}

func privateGroupControlTotalPages(total int) int {
	if total <= 0 {
		return 1
	}
	pages := (total + privateGroupControlPageSize - 1) / privateGroupControlPageSize
	if pages <= 0 {
		return 1
	}
	return pages
}

func clampPrivateGroupControlPage(page, totalPages int) int {
	if totalPages <= 0 {
		totalPages = 1
	}
	if page < 0 {
		return 0
	}
	if page >= totalPages {
		return totalPages - 1
	}
	return page
}

func (a *App) handlePrivateGroupControlCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	_ = ctx
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil || cq.From == nil || !cq.Message.Chat.IsPrivate() {
		if cq != nil {
			a.answerCallback(cq.ID, "请在私聊中使用群控面板。", true)
		}
		return nil
	}
	if cq.From.ID != cq.Message.Chat.ID {
		a.answerCallback(cq.ID, "无权操作此面板。", true)
		return nil
	}
	parsed, err := parsePrivateGroupControlCallbackData(cq.Data)
	if err != nil {
		a.answerCallback(cq.ID, "无效操作，请重新打开群控面板。", true)
		return nil
	}
	userID := cq.From.ID
	if parsed.Verb == "confirm" {
		session, reason, ok := a.markPrivateGroupControlExecuting(userID, parsed, cq.Message.Chat.ID)
		if !ok {
			a.answerCallback(cq.ID, reason, true)
			return nil
		}
		a.answerCallback(cq.ID, "任务已提交，正在执行。", false)
		return a.executePrivateGroupControlConfirm(cq, session)
	}
	if parsed.Verb == "retry" {
		session, reason, ok := a.markPrivateGroupControlRecoveryExecuting(userID, parsed, cq.Message.Chat.ID)
		if !ok {
			a.answerCallback(cq.ID, reason, true)
			return nil
		}
		a.answerCallback(cq.ID, "正在重试失败群。", false)
		return a.executePrivateGroupControlConfirm(cq, session)
	}

	session, ok := a.getPrivateGroupControlSession(userID)
	if !ok {
		a.answerCallback(cq.ID, "会话已过期，请重新打开群控面板。", true)
		return nil
	}
	if session.UserID != userID || session.PrivateChatID != cq.Message.Chat.ID {
		a.answerCallback(cq.ID, "无权操作此面板。", true)
		return nil
	}
	if session.SessionID != parsed.SessionID || session.Version != parsed.Version {
		a.answerCallback(cq.ID, "面板已刷新，请使用最新面板。", true)
		return nil
	}
	if session.Executing {
		a.answerCallback(cq.ID, "任务正在执行或已完成。", true)
		return nil
	}

	switch parsed.Verb {
	case "panel":
		return a.refreshPrivateGroupControlPanel(cq, userID)
	case "action":
		return a.startPrivateGroupControlAction(cq, session, privateGroupControlAction(parsed.Arg))
	case "cat":
		return a.openPrivateGroupControlCategoryMenu(cq, session)
	case "ctl":
		return a.showPrivateGroupControlCategoryList(cq, session, privateGroupControlCategoryKindType, parsed.Arg)
	case "tgl":
		return a.showPrivateGroupControlCategoryList(cq, session, privateGroupControlCategoryKindTag, parsed.Arg)
	case "ctv":
		return a.showPrivateGroupControlCategoryDetail(cq, session, privateGroupControlCategoryKindType, parsed.Arg)
	case "tgv":
		return a.showPrivateGroupControlCategoryDetail(cq, session, privateGroupControlCategoryKindTag, parsed.Arg)
	case "cta":
		return a.startPrivateGroupControlCategorySelection(cq, session, privateGroupControlCategoryKindType, privateGroupControlCategoryOpAdd)
	case "ctr":
		return a.startPrivateGroupControlCategorySelection(cq, session, privateGroupControlCategoryKindType, privateGroupControlCategoryOpRemove)
	case "tga":
		return a.startPrivateGroupControlCategorySelection(cq, session, privateGroupControlCategoryKindTag, privateGroupControlCategoryOpAdd)
	case "tgr":
		return a.startPrivateGroupControlCategorySelection(cq, session, privateGroupControlCategoryKindTag, privateGroupControlCategoryOpRemove)
	case "group":
		return a.togglePrivateGroupControlGroup(cq, session, parsed.Arg)
	case "page":
		return a.pagePrivateGroupControlSelection(cq, session, parsed.Arg)
	case "select_all":
		return a.selectPrivateGroupControlPage(cq, session)
	case "select_all_groups":
		return a.selectPrivateGroupControlAllGroups(cq, session)
	case "type_menu":
		return a.showPrivateGroupControlSelectionFilter(cq, session, "type", parsed.Arg)
	case "tag_menu":
		return a.showPrivateGroupControlSelectionFilter(cq, session, "tag", parsed.Arg)
	case "select_type":
		return a.selectPrivateGroupControlByExisting(cq, session, "type", parsed.Arg)
	case "select_tag":
		return a.selectPrivateGroupControlByExisting(cq, session, "tag", parsed.Arg)
	case "clear":
		return a.clearPrivateGroupControlSelection(cq, session)
	case "choose":
		return a.choosePrivateGroupControlTargets(cq, session)
	case "cact":
		return a.handlePrivateGroupControlClosingReminderAction(cq, session, parsed.Arg)
	case "ctm":
		return a.showPrivateGroupControlClosingReminderTextManagement(cq, session)
	case "cback":
		return a.returnPrivateGroupControlClosingReminderMenu(cq, session)
	case "ctext":
		return a.handlePrivateGroupControlClosingReminderTextAction(cq, session, parsed.Arg)
	case "crest":
		return a.handlePrivateGroupControlClosingReminderRestoreText(cq, session, parsed.Arg)
	case "back":
		return a.backPrivateGroupControl(cq, session)
	case "reselect":
		return a.reselectPrivateGroupControlTargets(cq, session)
	case "export":
		return a.exportPrivateGroupControlFailureList(cq, session)
	case "cancel":
		a.clearPrivateGroupControlSession(userID)
		a.answerCallback(cq.ID, "已取消。", false)
		return a.replyPrivateMenu(cq.Message.Chat.ID, cq.Message.MessageID, userID)
	default:
		a.answerCallback(cq.ID, "无效操作，请重新打开群控面板。", true)
		return nil
	}
}

func (a *App) refreshPrivateGroupControlPanel(cq *tgbotapi.CallbackQuery, userID int64) error {
	a.clearPrivateGroupControlCaches(userID)
	session := a.newPrivateGroupControlPanelSession(userID, cq.Message.Chat.ID)
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlPanel(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "已刷新群控面板")
}

func (a *App) startPrivateGroupControlAction(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, action privateGroupControlAction) error {
	if session.Stage != privateGroupControlStagePanel {
		a.answerCallback(cq.ID, "当前步骤不能切换操作，请返回群控面板。", true)
		return nil
	}
	switch action {
	case privateGroupControlActionTag, privateGroupControlActionType, privateGroupControlActionCloseTime, privateGroupControlActionBroadcast:
	default:
		a.answerCallback(cq.ID, "未知群控操作。", true)
		return nil
	}
	a.answerCallback(cq.ID, "正在读取群列表...", false)
	groups, err := a.listPrivateGroupControlEligibleGroups(session.UserID)
	if err != nil {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "读取群列表失败，请稍后重试。")
		return err
	}
	if len(groups) == 0 {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "未找到可管理群。")
		return nil
	}
	session.Action = action
	session.Stage = privateGroupControlStageSelecting
	session.Groups = groups
	session.SelectedChatIDs = map[int64]bool{}
	session.CurrentPage = 0
	session.PendingValue = ""
	privateGroupControlClearCategoryPending(&session)
	session.PendingThresholdU = 0
	session.HasPendingThresholdU = false
	session.PendingClosingAction = ""
	session.PendingClosingStatus = ""
	session.Content = ""
	session.ContentMode = ""
	session.ContentPreview = ""
	session.SourceChatID = 0
	session.SourceMessageID = 0
	session.RecoveryIssues = nil
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlSelection(session)
	return a.editPrivateGroupControlMessageAfterCallback(cq, text, markup)
}

func (a *App) togglePrivateGroupControlGroup(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, rawChatID string) error {
	if session.Stage != privateGroupControlStageSelecting {
		a.answerCallback(cq.ID, "当前步骤不能选择群。", true)
		return nil
	}
	chatID, err := strconv.ParseInt(strings.TrimSpace(rawChatID), 10, 64)
	if err != nil || !privateGroupControlSessionHasGroup(session, chatID) {
		a.answerCallback(cq.ID, "目标群无效，请刷新面板。", true)
		return nil
	}
	if session.SelectedChatIDs == nil {
		session.SelectedChatIDs = map[int64]bool{}
	}
	session.SelectedChatIDs[chatID] = !session.SelectedChatIDs[chatID]
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlSelection(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "已更新选择")
}

func (a *App) pagePrivateGroupControlSelection(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, rawPage string) error {
	if session.Stage != privateGroupControlStageSelecting {
		a.answerCallback(cq.ID, "当前步骤不能翻页。", true)
		return nil
	}
	page, err := strconv.Atoi(strings.TrimSpace(rawPage))
	if err != nil {
		a.answerCallback(cq.ID, "页码无效。", true)
		return nil
	}
	session.CurrentPage = clampPrivateGroupControlPage(page, privateGroupControlTotalPages(len(session.Groups)))
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlSelection(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, fmt.Sprintf("已切换到第%d页", session.CurrentPage+1))
}

func (a *App) selectPrivateGroupControlPage(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	if session.Stage != privateGroupControlStageSelecting {
		a.answerCallback(cq.ID, "当前步骤不能选择群。", true)
		return nil
	}
	if session.SelectedChatIDs == nil {
		session.SelectedChatIDs = map[int64]bool{}
	}
	page := clampPrivateGroupControlPage(session.CurrentPage, privateGroupControlTotalPages(len(session.Groups)))
	start := page * privateGroupControlPageSize
	end := start + privateGroupControlPageSize
	if end > len(session.Groups) {
		end = len(session.Groups)
	}
	for _, group := range session.Groups[start:end] {
		session.SelectedChatIDs[group.ChatID] = true
	}
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlSelection(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "已全选本页")
}

func (a *App) selectPrivateGroupControlAllGroups(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	if session.Stage != privateGroupControlStageSelecting {
		a.answerCallback(cq.ID, "当前步骤不能选择群。", true)
		return nil
	}
	if session.SelectedChatIDs == nil {
		session.SelectedChatIDs = map[int64]bool{}
	}
	for _, group := range session.Groups {
		session.SelectedChatIDs[group.ChatID] = true
	}
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlSelection(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "已全选全部群")
}

func (a *App) showPrivateGroupControlSelectionFilter(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, kind, rawPage string) error {
	if session.Stage != privateGroupControlStageSelecting {
		a.answerCallback(cq.ID, "当前步骤不能按条件选择群。", true)
		return nil
	}
	if kind != "type" && kind != "tag" {
		a.answerCallback(cq.ID, "筛选条件无效。", true)
		return nil
	}
	page, err := strconv.Atoi(strings.TrimSpace(rawPage))
	if strings.TrimSpace(rawPage) == "" {
		page = 0
		err = nil
	}
	if err != nil {
		a.answerCallback(cq.ID, "筛选页码无效。", true)
		return nil
	}
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := renderPrivateGroupControlSelectionFilter(session, kind, page)
	return a.editPrivateGroupControlMessage(cq, text, markup, "请选择已有"+privateGroupControlSelectionFilterName(kind))
}

func renderPrivateGroupControlSelectionFilter(session privateGroupControlSession, kind string, rawPage int) (string, tgbotapi.InlineKeyboardMarkup) {
	options := privateGroupControlExistingSelectionOptions(session.Groups, kind)
	totalPages := privateGroupControlSelectionFilterTotalPages(len(options))
	page := clampPrivateGroupControlSelectionFilterPage(rawPage, totalPages)
	start := page * privateGroupControlSelectionFilterPageSize
	end := start + privateGroupControlSelectionFilterPageSize
	if end > len(options) {
		end = len(options)
	}
	lines := []string{
		privateGroupControlSelectionFilterTitle(kind),
		fmt.Sprintf("操作：%s", privateGroupControlActionLabelForSession(session)),
		fmt.Sprintf("已选：%d 个群", countPrivateGroupControlSelected(session.SelectedChatIDs)),
		fmt.Sprintf("条件页码：%d/%d", page+1, totalPages),
		fmt.Sprintf("可选%s：%d 个", privateGroupControlSelectionFilterName(kind), len(options)),
		"点击后会追加选择匹配群。",
		"--------------------------------",
	}
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, privateGroupControlSelectionFilterPageSize+2)
	verb := privateGroupControlSelectionFilterVerb(kind)
	if len(options) == 0 {
		lines = append(lines, "暂无已有"+privateGroupControlSelectionFilterName(kind)+"。")
	} else {
		for i := start; i < end; i++ {
			option := options[i]
			lines = append(lines, fmt.Sprintf("- %s：%d 个群", option.Value, option.Count))
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%02d. %s (%d)", i+1, trimForPreview(option.Value, 20), option.Count), buildPrivateGroupControlCallbackData(session, verb, strconv.Itoa(i))),
			))
		}
	}
	prevPage := page - 1
	if prevPage < 0 {
		prevPage = 0
	}
	nextPage := page + 1
	if nextPage >= totalPages {
		nextPage = totalPages - 1
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("上一页", buildPrivateGroupControlCallbackData(session, privateGroupControlSelectionMenuVerb(kind), strconv.Itoa(prevPage))),
		tgbotapi.NewInlineKeyboardButtonData("下一页", buildPrivateGroupControlCallbackData(session, privateGroupControlSelectionMenuVerb(kind), strconv.Itoa(nextPage))),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("返回选群", buildPrivateGroupControlCallbackData(session, "page", strconv.Itoa(clampPrivateGroupControlPage(session.CurrentPage, privateGroupControlTotalPages(len(session.Groups)))))),
		tgbotapi.NewInlineKeyboardButtonData("取消", buildPrivateGroupControlCallbackData(session, "cancel")),
	))
	return strings.Join(lines, "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func privateGroupControlSelectionFilterTotalPages(total int) int {
	if total <= 0 {
		return 1
	}
	pages := (total + privateGroupControlSelectionFilterPageSize - 1) / privateGroupControlSelectionFilterPageSize
	if pages <= 0 {
		return 1
	}
	return pages
}

func clampPrivateGroupControlSelectionFilterPage(page, totalPages int) int {
	if totalPages <= 0 {
		totalPages = 1
	}
	if page < 0 {
		return 0
	}
	if page >= totalPages {
		return totalPages - 1
	}
	return page
}

func privateGroupControlExistingSelectionOptions(groups []db.GroupConfig, kind string) []privateGroupControlSelectionOption {
	counts := map[string]int{}
	for _, group := range groups {
		switch kind {
		case "type":
			value := strings.TrimSpace(group.GroupType)
			if value != "" {
				counts[value]++
			}
		case "tag":
			seen := map[string]bool{}
			for _, tag := range utils.SplitTags(group.GroupTags) {
				if tag == "" || seen[tag] {
					continue
				}
				counts[tag]++
				seen[tag] = true
			}
		}
	}
	options := make([]privateGroupControlSelectionOption, 0, len(counts))
	for value, count := range counts {
		options = append(options, privateGroupControlSelectionOption{Value: value, Count: count})
	}
	sort.Slice(options, func(i, j int) bool {
		left := strings.ToLower(options[i].Value)
		right := strings.ToLower(options[j].Value)
		if left != right {
			return left < right
		}
		return options[i].Value < options[j].Value
	})
	return options
}

func privateGroupControlSelectionFilterTitle(kind string) string {
	switch kind {
	case "type":
		return "按已有群类型选择"
	case "tag":
		return "按已有群标签选择"
	default:
		return "按已有条件选择"
	}
}

func privateGroupControlSelectionFilterName(kind string) string {
	switch kind {
	case "type":
		return "群类型"
	case "tag":
		return "群标签"
	default:
		return "条件"
	}
}

func privateGroupControlSelectionFilterVerb(kind string) string {
	switch kind {
	case "type":
		return "select_type"
	case "tag":
		return "select_tag"
	default:
		return ""
	}
}

func privateGroupControlSelectionMenuVerb(kind string) string {
	switch kind {
	case "type":
		return "type_menu"
	case "tag":
		return "tag_menu"
	default:
		return ""
	}
}

func (a *App) selectPrivateGroupControlByExisting(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, kind, rawIndex string) error {
	if session.Stage != privateGroupControlStageSelecting {
		a.answerCallback(cq.ID, "当前步骤不能按条件选择群。", true)
		return nil
	}
	options := privateGroupControlExistingSelectionOptions(session.Groups, kind)
	index, err := strconv.Atoi(strings.TrimSpace(rawIndex))
	if err != nil || index < 0 || index >= len(options) {
		a.answerCallback(cq.ID, "筛选条件已失效，请重新打开。", true)
		return nil
	}
	if session.SelectedChatIDs == nil {
		session.SelectedChatIDs = map[int64]bool{}
	}
	value := options[index].Value
	for _, group := range session.Groups {
		if privateGroupControlGroupMatchesExistingSelection(group, kind, value) {
			session.SelectedChatIDs[group.ChatID] = true
		}
	}
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlSelection(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "已按"+privateGroupControlSelectionFilterName(kind)+"选择")
}

func privateGroupControlGroupMatchesExistingSelection(group db.GroupConfig, kind, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	switch kind {
	case "type":
		return strings.TrimSpace(group.GroupType) == value
	case "tag":
		for _, tag := range utils.SplitTags(group.GroupTags) {
			if tag == value {
				return true
			}
		}
	}
	return false
}

func (a *App) clearPrivateGroupControlSelection(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	if session.Stage != privateGroupControlStageSelecting {
		a.answerCallback(cq.ID, "当前步骤不能清空选择。", true)
		return nil
	}
	session.SelectedChatIDs = map[int64]bool{}
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlSelection(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "已清空选择")
}

func (a *App) choosePrivateGroupControlTargets(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	if session.Stage != privateGroupControlStageSelecting {
		a.answerCallback(cq.ID, "当前步骤不能确认选择。", true)
		return nil
	}
	if countPrivateGroupControlSelected(session.SelectedChatIDs) == 0 {
		a.answerCallback(cq.ID, "请至少选择一个群。", true)
		return nil
	}
	if privateGroupControlSessionHasPayload(session) {
		session.Stage = privateGroupControlStageAwaitingConfirm
		session.RecoveryIssues = nil
		session.Version++
		session.UpdatedAt = a.currentTime()
		a.setPrivateGroupControlSession(session)
		text, markup := a.renderPrivateGroupControlConfirm(session)
		return a.editPrivateGroupControlMessage(cq, text, markup, "请确认执行")
	}
	switch session.Action {
	case privateGroupControlActionTag:
		session.Stage = privateGroupControlStageAwaitingTagInput
	case privateGroupControlActionType:
		session.Stage = privateGroupControlStageAwaitingTypeInput
	case privateGroupControlActionCloseTime:
		session.Stage = privateGroupControlStageClosingReminderMenu
	case privateGroupControlActionBroadcast:
		session.Stage = privateGroupControlStageAwaitingBroadcastContent
	default:
		a.answerCallback(cq.ID, "未知群控操作。", true)
		return nil
	}
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	if session.Stage == privateGroupControlStageClosingReminderMenu {
		text, markup := a.renderPrivateGroupControlClosingReminderMenu(session)
		return a.editPrivateGroupControlMessage(cq, text, markup, "请选择下课提醒设置项")
	}
	text, markup := a.renderPrivateGroupControlInputPrompt(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "请发送输入内容")
}

func privateGroupControlSessionHasPayload(session privateGroupControlSession) bool {
	switch session.Action {
	case privateGroupControlActionTag, privateGroupControlActionType:
		return strings.TrimSpace(session.PendingValue) != ""
	case privateGroupControlActionCategory:
		return privateGroupControlSessionHasCategoryPayload(session)
	case privateGroupControlActionCloseTime:
		return privateGroupControlClosingReminderSessionHasPayload(session)
	case privateGroupControlActionBroadcast:
		return strings.TrimSpace(session.Content) != "" || session.SourceMessageID > 0
	default:
		return false
	}
}

func privateGroupControlSessionHasCategoryPayload(session privateGroupControlSession) bool {
	if session.Action != privateGroupControlActionCategory {
		return false
	}
	if !privateGroupControlCategoryKindValid(session.CategoryKind) {
		return false
	}
	if strings.TrimSpace(session.CategoryValue) == "" {
		return false
	}
	return session.CategoryOp == privateGroupControlCategoryOpAdd || session.CategoryOp == privateGroupControlCategoryOpRemove
}

func privateGroupControlClosingReminderSessionHasPayload(session privateGroupControlSession) bool {
	switch privateGroupControlClosingReminderAction(session.PendingClosingAction) {
	case privateGroupControlClosingReminderActionEnable,
		privateGroupControlClosingReminderActionDisable,
		privateGroupControlClosingReminderActionRestoreAll:
		return true
	case privateGroupControlClosingReminderActionSetTime:
		return strings.TrimSpace(session.PendingValue) != ""
	case privateGroupControlClosingReminderActionThreshold:
		return session.HasPendingThresholdU && session.PendingThresholdU > 0
	case privateGroupControlClosingReminderActionSetText:
		return strings.TrimSpace(session.PendingClosingStatus) != "" && strings.TrimSpace(session.PendingValue) != ""
	case privateGroupControlClosingReminderActionRestoreText:
		return strings.TrimSpace(session.PendingClosingStatus) != ""
	default:
		return false
	}
}

func privateGroupControlClearClosingReminderPending(session *privateGroupControlSession) {
	if session == nil {
		return
	}
	session.PendingValue = ""
	session.PendingThresholdU = 0
	session.HasPendingThresholdU = false
	session.PendingClosingAction = ""
	session.PendingClosingStatus = ""
}

func (a *App) backPrivateGroupControl(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	switch session.Stage {
	case privateGroupControlStageCategoryDetail:
		kind := session.CategoryKind
		if !privateGroupControlCategoryKindValid(kind) {
			kind = privateGroupControlCategoryKindType
		}
		session.Stage = privateGroupControlStageCategoryList
		session.CategoryValue = ""
		session.CategoryOp = privateGroupControlCategoryOpNone
		session.SelectedChatIDs = map[int64]bool{}
		session.Version++
		session.UpdatedAt = a.currentTime()
		a.setPrivateGroupControlSession(session)
		text, markup := renderPrivateGroupControlCategoryList(session, kind, session.CurrentPage)
		return a.editPrivateGroupControlMessage(cq, text, markup, "已返回列表")
	case privateGroupControlStageCategoryList:
		session.Stage = privateGroupControlStageCategoryMenu
		privateGroupControlClearCategoryPending(&session)
		session.SelectedChatIDs = map[int64]bool{}
		session.CurrentPage = 0
		session.Version++
		session.UpdatedAt = a.currentTime()
		a.setPrivateGroupControlSession(session)
		text, markup := a.renderPrivateGroupControlCategoryMenu(session)
		return a.editPrivateGroupControlMessage(cq, text, markup, "已返回列表管理")
	case privateGroupControlStageCategoryMenu:
		return a.refreshPrivateGroupControlPanel(cq, session.UserID)
	case privateGroupControlStageClosingReminderTextMenu:
		session.Stage = privateGroupControlStageClosingReminderMenu
		session.Version++
		session.UpdatedAt = a.currentTime()
		a.setPrivateGroupControlSession(session)
		text, markup := a.renderPrivateGroupControlClosingReminderMenu(session)
		return a.editPrivateGroupControlMessage(cq, text, markup, "已返回下课提醒设置")
	case privateGroupControlStageClosingReminderMenu:
		session.Stage = privateGroupControlStageSelecting
		session.PendingValue = ""
		session.PendingThresholdU = 0
		session.HasPendingThresholdU = false
		session.PendingClosingAction = ""
		session.PendingClosingStatus = ""
		session.RecoveryIssues = nil
		session.Version++
		session.UpdatedAt = a.currentTime()
		a.setPrivateGroupControlSession(session)
		text, markup := a.renderPrivateGroupControlSelection(session)
		return a.editPrivateGroupControlMessage(cq, text, markup, "已返回选择群")
	case privateGroupControlStageAwaitingCloseTimeInput, privateGroupControlStageAwaitingThresholdInput, privateGroupControlStageAwaitingClosingTextInput:
		if session.Action == privateGroupControlActionCloseTime {
			fromTextInput := session.Stage == privateGroupControlStageAwaitingClosingTextInput
			privateGroupControlClearClosingReminderPending(&session)
			session.Stage = privateGroupControlStageClosingReminderMenu
			if fromTextInput {
				session.Stage = privateGroupControlStageClosingReminderTextMenu
			}
			session.Version++
			session.UpdatedAt = a.currentTime()
			a.setPrivateGroupControlSession(session)
			if session.Stage == privateGroupControlStageClosingReminderTextMenu {
				text, markup := a.renderPrivateGroupControlClosingReminderTextMenu(session)
				return a.editPrivateGroupControlMessage(cq, text, markup, "已返回文案管理")
			}
			text, markup := a.renderPrivateGroupControlClosingReminderMenu(session)
			return a.editPrivateGroupControlMessage(cq, text, markup, "已返回下课提醒设置")
		}
		fallthrough
	case privateGroupControlStageAwaitingTagInput, privateGroupControlStageAwaitingTypeInput, privateGroupControlStageAwaitingBroadcastContent, privateGroupControlStageRecovery:
		session.Stage = privateGroupControlStageSelecting
		session.PendingValue = ""
		session.PendingThresholdU = 0
		session.HasPendingThresholdU = false
		session.PendingClosingAction = ""
		session.PendingClosingStatus = ""
		session.Content = ""
		session.ContentMode = ""
		session.ContentPreview = ""
		session.SourceChatID = 0
		session.SourceMessageID = 0
		session.RecoveryIssues = nil
		session.Version++
		session.UpdatedAt = a.currentTime()
		a.setPrivateGroupControlSession(session)
		text, markup := a.renderPrivateGroupControlSelection(session)
		return a.editPrivateGroupControlMessage(cq, text, markup, "已返回选择群")
	case privateGroupControlStageAwaitingConfirm:
		if session.Action == privateGroupControlActionCloseTime {
			pendingAction := privateGroupControlClosingReminderAction(session.PendingClosingAction)
			privateGroupControlClearClosingReminderPending(&session)
			session.Stage = privateGroupControlStageClosingReminderMenu
			if pendingAction == privateGroupControlClosingReminderActionSetText ||
				pendingAction == privateGroupControlClosingReminderActionRestoreText ||
				pendingAction == privateGroupControlClosingReminderActionRestoreAll {
				session.Stage = privateGroupControlStageClosingReminderTextMenu
			}
			session.Version++
			session.UpdatedAt = a.currentTime()
			a.setPrivateGroupControlSession(session)
			if session.Stage == privateGroupControlStageClosingReminderTextMenu {
				text, markup := a.renderPrivateGroupControlClosingReminderTextMenu(session)
				return a.editPrivateGroupControlMessage(cq, text, markup, "已返回文案管理")
			}
			text, markup := a.renderPrivateGroupControlClosingReminderMenu(session)
			return a.editPrivateGroupControlMessage(cq, text, markup, "已返回下课提醒设置")
		}
		session.Stage = privateGroupControlStageSelecting
		session.PendingValue = ""
		session.PendingThresholdU = 0
		session.HasPendingThresholdU = false
		session.Content = ""
		session.ContentMode = ""
		session.ContentPreview = ""
		session.SourceChatID = 0
		session.SourceMessageID = 0
		session.RecoveryIssues = nil
		session.Version++
		session.UpdatedAt = a.currentTime()
		a.setPrivateGroupControlSession(session)
		text, markup := a.renderPrivateGroupControlSelection(session)
		return a.editPrivateGroupControlMessage(cq, text, markup, "已返回选择群")
	default:
		a.answerCallback(cq.ID, "当前步骤不能返回。", true)
		return nil
	}
}

func (a *App) renderPrivateGroupControlClosingReminderMenu(session privateGroupControlSession) (string, tgbotapi.InlineKeyboardMarkup) {
	summary := a.privateGroupControlClosingReminderSummary(session)
	lines := []string{
		"下课提醒设置",
		fmt.Sprintf("已选：%d 个群", countPrivateGroupControlSelected(session.SelectedChatIDs)),
		"第2步：选择要批量修改的内容",
		"",
		"权限说明",
		"全局/本群权限人、操作人可设置",
		"不要求 Telegram 群管理员",
		"机器人必须在目标群内",
		"--------------------------------",
		"当前配置",
		"",
		"提醒状态",
		fmt.Sprintf("开启：%d 个", summary.Enabled),
		fmt.Sprintf("关闭：%d 个", summary.Disabled),
		"",
		"下课时间",
	}
	lines = append(lines, privateGroupControlPlainCountLines(summary.TimeCounts)...)
	lines = append(lines, "", "U差额阈值")
	lines = append(lines, privateGroupControlPlainCountLines(summary.ThresholdCounts)...)
	lines = append(lines, "", "文案状态")
	lines = append(lines, privateGroupControlClosingReminderMenuTextSummaryLines(summary, summary.Total)...)
	lines = append(lines, "--------------------------------")
	lines = append(lines, privateGroupControlClosingReminderGuideLines()...)
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("开启提醒", buildPrivateGroupControlCallbackData(session, "cact", string(privateGroupControlClosingReminderActionEnable))),
			tgbotapi.NewInlineKeyboardButtonData("关闭提醒", buildPrivateGroupControlCallbackData(session, "cact", string(privateGroupControlClosingReminderActionDisable))),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("设置下课时间", buildPrivateGroupControlCallbackData(session, "cact", string(privateGroupControlClosingReminderActionSetTime))),
			tgbotapi.NewInlineKeyboardButtonData("设置U差额阈值", buildPrivateGroupControlCallbackData(session, "cact", string(privateGroupControlClosingReminderActionThreshold))),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("查询下课提醒", buildPrivateGroupControlCallbackData(session, "cact", "query")),
			tgbotapi.NewInlineKeyboardButtonData("文案管理", buildPrivateGroupControlCallbackData(session, "ctm")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("返回重选", buildPrivateGroupControlCallbackData(session, "back")),
			tgbotapi.NewInlineKeyboardButtonData("取消", buildPrivateGroupControlCallbackData(session, "cancel")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("返回群控面板", buildPrivateGroupControlCallbackData(session, "panel")),
		),
	}
	return strings.Join(lines, "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

type privateGroupControlClosingReminderSummary struct {
	Total           int
	Enabled         int
	Disabled        int
	TimeCounts      map[string]int
	TimeGroups      map[string][]string
	ThresholdCounts map[string]int
	ThresholdGroups map[string][]string
	TextCustom      map[string]int
	FailedGroups    []string
}

func (a *App) privateGroupControlClosingReminderSummary(session privateGroupControlSession) privateGroupControlClosingReminderSummary {
	groups := privateGroupControlSelectedGroups(session)
	summary := privateGroupControlClosingReminderSummary{
		Total:           len(groups),
		TimeCounts:      map[string]int{},
		TimeGroups:      map[string][]string{},
		ThresholdCounts: map[string]int{},
		ThresholdGroups: map[string][]string{},
		TextCustom:      map[string]int{},
	}
	if a == nil || a.db == nil {
		return summary
	}
	for _, group := range groups {
		config, err := a.db.GetClosingReminderConfig(group.ChatID)
		if err != nil {
			if len(summary.FailedGroups) < 5 {
				summary.FailedGroups = append(summary.FailedGroups, privateGroupControlFailedGroupLabel(group))
			}
			continue
		}
		if config.Enabled != 0 {
			summary.Enabled++
		} else {
			summary.Disabled++
		}
		groupTitle := privateGroupControlGroupTitle(group)
		timeKey := config.ReminderTime
		thresholdKey := formatClosingReminderAmount(config.ThresholdU) + " U"
		summary.TimeCounts[timeKey]++
		summary.TimeGroups[timeKey] = append(summary.TimeGroups[timeKey], groupTitle)
		summary.ThresholdCounts[thresholdKey]++
		summary.ThresholdGroups[thresholdKey] = append(summary.ThresholdGroups[thresholdKey], groupTitle)
		for _, item := range closingReminderStatusLabelList() {
			if closingReminderTextField(config, item.status) != "" {
				summary.TextCustom[item.status]++
			}
		}
	}
	return summary
}

func privateGroupControlClosingReminderTextSummaryLines(summary privateGroupControlClosingReminderSummary, total int) []string {
	lines := make([]string, 0, len(closingReminderStatusLabelList()))
	for _, item := range closingReminderStatusLabelList() {
		custom := summary.TextCustom[item.status]
		lines = append(lines, fmt.Sprintf("- %s：%d 个自定义 / %d 个默认", item.label, custom, total-custom))
	}
	if len(summary.FailedGroups) > 0 {
		lines = append(lines, "读取失败："+strings.Join(summary.FailedGroups, "；"))
	}
	return lines
}

func privateGroupControlClosingReminderMenuTextSummaryLines(summary privateGroupControlClosingReminderSummary, total int) []string {
	lines := make([]string, 0, len(closingReminderStatusLabelList())+1)
	for _, item := range closingReminderStatusLabelList() {
		custom := summary.TextCustom[item.status]
		if custom <= 0 {
			continue
		}
		if total > 0 && custom >= total {
			lines = append(lines, item.label+"：全部自定义")
			continue
		}
		lines = append(lines, fmt.Sprintf("%s：%d 个群自定义，其余默认", item.label, custom))
	}
	if len(lines) == 0 {
		lines = append(lines, "全部使用默认文案")
	} else {
		lines = append(lines, "未列状态：全部默认")
	}
	if len(summary.FailedGroups) > 0 {
		lines = append(lines, "读取失败："+strings.Join(summary.FailedGroups, "；"))
	}
	return lines
}

func (a *App) renderPrivateGroupControlClosingReminderTextMenu(session privateGroupControlSession) (string, tgbotapi.InlineKeyboardMarkup) {
	summary := a.privateGroupControlClosingReminderSummary(session)
	lines := []string{
		"文案管理",
		fmt.Sprintf("已选：%d 个群", countPrivateGroupControlSelected(session.SelectedChatIDs)),
		"下课文案按状态发送到群内；群内文案不展示金额、差额明细。",
		"文案摘要：",
	}
	lines = append(lines, privateGroupControlClosingReminderTextSummaryLines(summary, summary.Total)...)
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("查询全部文案", buildPrivateGroupControlCallbackData(session, "cact", "text_query")),
			tgbotapi.NewInlineKeyboardButtonData("预览全部文案", buildPrivateGroupControlCallbackData(session, "cact", "text_preview")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("设置正常文案", buildPrivateGroupControlCallbackData(session, "ctext", db.ClosingReminderStatusNormal)),
			tgbotapi.NewInlineKeyboardButtonData("恢复正常文案", buildPrivateGroupControlCallbackData(session, "crest", db.ClosingReminderStatusNormal)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("设置待确认文案", buildPrivateGroupControlCallbackData(session, "ctext", db.ClosingReminderStatusPending)),
			tgbotapi.NewInlineKeyboardButtonData("恢复待确认文案", buildPrivateGroupControlCallbackData(session, "crest", db.ClosingReminderStatusPending)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("设置无账单文案", buildPrivateGroupControlCallbackData(session, "ctext", db.ClosingReminderStatusNoData)),
			tgbotapi.NewInlineKeyboardButtonData("恢复无账单文案", buildPrivateGroupControlCallbackData(session, "crest", db.ClosingReminderStatusNoData)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("设置复核文案", buildPrivateGroupControlCallbackData(session, "ctext", db.ClosingReminderStatusReview)),
			tgbotapi.NewInlineKeyboardButtonData("恢复复核文案", buildPrivateGroupControlCallbackData(session, "crest", db.ClosingReminderStatusReview)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("设置失败文案", buildPrivateGroupControlCallbackData(session, "ctext", db.ClosingReminderStatusFailed)),
			tgbotapi.NewInlineKeyboardButtonData("恢复失败文案", buildPrivateGroupControlCallbackData(session, "crest", db.ClosingReminderStatusFailed)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("恢复全部文案", buildPrivateGroupControlCallbackData(session, "cact", string(privateGroupControlClosingReminderActionRestoreAll))),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("返回提醒设置", buildPrivateGroupControlCallbackData(session, "cback")),
			tgbotapi.NewInlineKeyboardButtonData("返回群控面板", buildPrivateGroupControlCallbackData(session, "panel")),
		),
	}
	return strings.Join(lines, "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (a *App) showPrivateGroupControlClosingReminderTextManagement(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	if session.Action != privateGroupControlActionCloseTime ||
		(session.Stage != privateGroupControlStageClosingReminderMenu && session.Stage != privateGroupControlStageClosingReminderTextMenu) {
		a.answerCallback(cq.ID, "当前步骤不能管理下课文案。", true)
		return nil
	}
	session.Stage = privateGroupControlStageClosingReminderTextMenu
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlClosingReminderTextMenu(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "已打开文案管理")
}

func (a *App) returnPrivateGroupControlClosingReminderMenu(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	if session.Action != privateGroupControlActionCloseTime {
		a.answerCallback(cq.ID, "当前步骤不能返回提醒设置。", true)
		return nil
	}
	session.Stage = privateGroupControlStageClosingReminderMenu
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlClosingReminderMenu(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "已返回下课提醒设置")
}

func (a *App) handlePrivateGroupControlClosingReminderAction(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, rawAction string) error {
	if session.Action != privateGroupControlActionCloseTime ||
		(session.Stage != privateGroupControlStageClosingReminderMenu && session.Stage != privateGroupControlStageClosingReminderTextMenu) {
		a.answerCallback(cq.ID, "当前步骤不能设置下课提醒。", true)
		return nil
	}
	switch strings.TrimSpace(rawAction) {
	case "query":
		a.answerCallback(cq.ID, "正在查询下课提醒", false)
		return a.replyPrivateGroupControlClosingReminderConfigQuery(cq, session)
	case "text_query":
		a.answerCallback(cq.ID, "正在查询下课文案", false)
		return a.replyPrivateGroupControlClosingReminderTextQuery(cq, session)
	case "text_preview":
		a.answerCallback(cq.ID, "正在预览下课文案", false)
		return a.replyPrivateGroupControlClosingReminderTextPreview(cq, session)
	case string(privateGroupControlClosingReminderActionEnable),
		string(privateGroupControlClosingReminderActionDisable),
		string(privateGroupControlClosingReminderActionRestoreAll):
		session.PendingClosingAction = strings.TrimSpace(rawAction)
		session.PendingClosingStatus = ""
		session.PendingValue = ""
		session.PendingThresholdU = 0
		session.HasPendingThresholdU = false
		session.Stage = privateGroupControlStageAwaitingConfirm
	case string(privateGroupControlClosingReminderActionSetTime):
		privateGroupControlClearClosingReminderPending(&session)
		session.PendingClosingAction = string(privateGroupControlClosingReminderActionSetTime)
		session.Stage = privateGroupControlStageAwaitingCloseTimeInput
	case string(privateGroupControlClosingReminderActionThreshold):
		privateGroupControlClearClosingReminderPending(&session)
		session.PendingClosingAction = string(privateGroupControlClosingReminderActionThreshold)
		session.Stage = privateGroupControlStageAwaitingThresholdInput
	default:
		a.answerCallback(cq.ID, "未知下课提醒操作。", true)
		return nil
	}
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	if session.Stage == privateGroupControlStageAwaitingConfirm {
		text, markup := a.renderPrivateGroupControlConfirm(session)
		return a.editPrivateGroupControlMessage(cq, text, markup, "请确认执行")
	}
	text, markup := a.renderPrivateGroupControlInputPrompt(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "请发送输入内容")
}

func (a *App) handlePrivateGroupControlClosingReminderTextAction(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, rawStatus string) error {
	if session.Action != privateGroupControlActionCloseTime ||
		(session.Stage != privateGroupControlStageClosingReminderMenu && session.Stage != privateGroupControlStageClosingReminderTextMenu) {
		a.answerCallback(cq.ID, "当前步骤不能设置下课文案。", true)
		return nil
	}
	status := strings.TrimSpace(rawStatus)
	if _, ok := closingReminderStatusLabelFromStatus(status); !ok {
		a.answerCallback(cq.ID, "未知下课文案状态。", true)
		return nil
	}
	privateGroupControlClearClosingReminderPending(&session)
	session.PendingClosingAction = string(privateGroupControlClosingReminderActionSetText)
	session.PendingClosingStatus = status
	session.Stage = privateGroupControlStageAwaitingClosingTextInput
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlInputPrompt(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "请发送下课文案")
}

func (a *App) handlePrivateGroupControlClosingReminderRestoreText(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, rawStatus string) error {
	if session.Action != privateGroupControlActionCloseTime ||
		(session.Stage != privateGroupControlStageClosingReminderMenu && session.Stage != privateGroupControlStageClosingReminderTextMenu) {
		a.answerCallback(cq.ID, "当前步骤不能恢复下课文案。", true)
		return nil
	}
	status := strings.TrimSpace(rawStatus)
	if _, ok := closingReminderStatusLabelFromStatus(status); !ok {
		a.answerCallback(cq.ID, "未知下课文案状态。", true)
		return nil
	}
	privateGroupControlClearClosingReminderPending(&session)
	session.PendingClosingAction = string(privateGroupControlClosingReminderActionRestoreText)
	session.PendingClosingStatus = status
	session.Stage = privateGroupControlStageAwaitingConfirm
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlConfirm(session)
	return a.editPrivateGroupControlMessage(cq, text, markup, "请确认执行")
}

func (a *App) replyPrivateGroupControlClosingReminderConfigQuery(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	summary := a.privateGroupControlClosingReminderSummary(session)
	lines := []string{
		"下课提醒批量查询：",
		fmt.Sprintf("目标群：%d 个", summary.Total),
		fmt.Sprintf("已开启：%d 个", summary.Enabled),
		fmt.Sprintf("已关闭：%d 个", summary.Disabled),
		"时间分布：",
	}
	lines = append(lines, privateGroupControlCountWithGroupsLines(summary.TimeCounts, summary.TimeGroups)...)
	lines = append(lines, "U阈值分布：")
	lines = append(lines, privateGroupControlCountWithGroupsLines(summary.ThresholdCounts, summary.ThresholdGroups)...)
	if len(summary.FailedGroups) > 0 {
		lines = append(lines, "读取失败："+strings.Join(summary.FailedGroups, "；"))
	}
	return a.reply(cq.Message.Chat.ID, cq.Message.MessageID, strings.Join(lines, "\n"))
}

func (a *App) replyPrivateGroupControlClosingReminderTextQuery(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	summary := a.privateGroupControlClosingReminderSummary(session)
	lines := []string{
		"下课文案批量查询：",
		fmt.Sprintf("目标群：%d 个", summary.Total),
	}
	lines = append(lines, privateGroupControlClosingReminderTextSummaryLines(summary, summary.Total)...)
	lines = append(lines, privateGroupControlClosingReminderGuideLines()...)
	return a.reply(cq.Message.Chat.ID, cq.Message.MessageID, strings.Join(lines, "\n"))
}

func (a *App) replyPrivateGroupControlClosingReminderTextPreview(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	groups := privateGroupControlSelectedGroups(session)
	if len(groups) == 0 || a == nil || a.db == nil {
		return a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "暂无可预览的目标群。")
	}
	config, err := a.db.GetClosingReminderConfig(groups[0].ChatID)
	if err != nil {
		return err
	}
	lines := []string{
		"下课文案预览：",
		"样本群：" + privateGroupControlGroupTitle(groups[0]),
	}
	for _, item := range closingReminderStatusLabelList() {
		lines = append(lines, item.label+"：")
		lines = append(lines, trimForPreview(closingReminderGroupTextForStatus(config, item.status), 80))
	}
	return a.reply(cq.Message.Chat.ID, cq.Message.MessageID, strings.Join(lines, "\n"))
}

func privateGroupControlCountLines(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return []string{"- 无"}
	}
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("- %s：%d 个", key, counts[key]))
	}
	return lines
}

func privateGroupControlCountWithGroupsLines(counts map[string]int, groups map[string][]string) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return []string{"- 无"}
	}
	lines := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("- %s：%d 个", key, counts[key]))
		if groupLine := privateGroupControlGroupNamesLine(groups[key], 12); groupLine != "" {
			lines = append(lines, "  群："+groupLine)
		}
	}
	return lines
}

func privateGroupControlGroupNamesLine(names []string, limit int) string {
	clean := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			clean = append(clean, name)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	sort.Strings(clean)
	if limit <= 0 || len(clean) <= limit {
		return strings.Join(clean, "、")
	}
	visible := append([]string(nil), clean[:limit]...)
	return strings.Join(visible, "、") + fmt.Sprintf(" 等 %d 个群", len(clean))
}

func privateGroupControlPlainCountLines(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return []string{"无"}
	}
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s：%d 个", key, counts[key]))
	}
	return lines
}

func (a *App) renderPrivateGroupControlInputPrompt(session privateGroupControlSession) (string, tgbotapi.InlineKeyboardMarkup) {
	lines := []string{
		privateGroupControlActionLabel(session.Action),
		fmt.Sprintf("已选：%d 个群", countPrivateGroupControlSelected(session.SelectedChatIDs)),
	}
	switch session.Stage {
	case privateGroupControlStageAwaitingTagInput:
		lines = append(lines, "请发送目标标签，多个标签可用逗号分隔。")
	case privateGroupControlStageAwaitingTypeInput:
		lines = append(lines, "请发送目标群类型。")
	case privateGroupControlStageAwaitingCloseTimeInput:
		lines = append(lines, "请发送下课提醒时间，如 21:49。")
		lines = append(lines, "设置时间后，可返回下课提醒设置层继续设置U差额阈值和各状态文案。")
	case privateGroupControlStageAwaitingThresholdInput:
		lines = append(lines, "请发送U差额提醒阈值，例如 2.5。")
		lines = append(lines, "该阈值用于判断待确认/复核状态，不影响下课提醒时间。")
	case privateGroupControlStageAwaitingClosingTextInput:
		label, _ := closingReminderStatusLabelFromStatus(session.PendingClosingStatus)
		lines = append(lines, fmt.Sprintf("请发送%s状态下课文案。", label))
		lines = append(lines, "文案会按状态自动发送到群内，不能包含金额、差额、风险等敏感内容。")
	case privateGroupControlStageAwaitingBroadcastContent:
		lines = append(lines, "请发送群发内容，支持文字、图片、视频、语音。")
	default:
		lines = append(lines, "请按当前面板提示继续。")
	}
	backLabel := "返回重选"
	if session.Action == privateGroupControlActionCloseTime {
		backLabel = "返回设置"
	}
	if session.Stage == privateGroupControlStageAwaitingClosingTextInput {
		backLabel = "返回文案管理"
	}
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(backLabel, buildPrivateGroupControlCallbackData(session, "back")),
			tgbotapi.NewInlineKeyboardButtonData("取消", buildPrivateGroupControlCallbackData(session, "cancel")),
		),
	)
	return strings.Join(lines, "\n"), markup
}

func privateGroupControlSessionHasGroup(session privateGroupControlSession, chatID int64) bool {
	for _, group := range session.Groups {
		if group.ChatID == chatID {
			return true
		}
	}
	return false
}

func (a *App) editPrivateGroupControlMessage(cq *tgbotapi.CallbackQuery, text string, markup tgbotapi.InlineKeyboardMarkup, answer string) error {
	a.answerCallback(cq.ID, answer, false)
	return a.editPrivateGroupControlMessageAfterCallback(cq, text, markup)
}

func (a *App) editPrivateGroupControlMessageAfterCallback(cq *tgbotapi.CallbackQuery, text string, markup tgbotapi.InlineKeyboardMarkup) error {
	edit := tgbotapi.NewEditMessageTextAndMarkup(cq.Message.Chat.ID, cq.Message.MessageID, text, markup)
	if err := a.sendWithPriority(outboundPriorityNormalChat, edit); err != nil && !isMessageNotModified(err) {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "更新面板失败，请稍后重试。")
		return err
	}
	return nil
}

func (a *App) reselectPrivateGroupControlTargets(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	if session.Stage != privateGroupControlStageRecovery {
		a.answerCallback(cq.ID, "当前没有可重新选择的失败任务。", true)
		return nil
	}
	a.answerCallback(cq.ID, "正在读取群列表...", false)
	groups, err := a.listPrivateGroupControlEligibleGroups(session.UserID)
	if err != nil {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "读取群列表失败，请稍后重试。")
		return err
	}
	if len(groups) == 0 {
		_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "未找到可管理群。")
		return nil
	}
	if session.Action == privateGroupControlActionCategory && privateGroupControlSessionHasCategoryPayload(session) {
		groups = privateGroupControlFilterCategoryGroups(groups, session.CategoryKind, session.CategoryOp, session.CategoryValue)
		if len(groups) == 0 {
			_ = a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "暂无可操作的群。")
			return nil
		}
	}
	session.Stage = privateGroupControlStageSelecting
	session.Groups = groups
	session.SelectedChatIDs = map[int64]bool{}
	session.CurrentPage = 0
	session.Executing = false
	session.RecoveryIssues = nil
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	text, markup := a.renderPrivateGroupControlSelection(session)
	return a.editPrivateGroupControlMessageAfterCallback(cq, text, markup)
}

func (a *App) exportPrivateGroupControlFailureList(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	if session.Stage != privateGroupControlStageRecovery || len(session.RecoveryIssues) == 0 {
		a.answerCallback(cq.ID, "当前没有失败列表。", true)
		return nil
	}
	m := tgbotapi.NewMessage(cq.Message.Chat.ID, privateGroupControlFailureListText(session))
	m.ReplyToMessageID = cq.Message.MessageID
	if err := a.sendWithPriority(outboundPriorityNormalChat, m); err != nil {
		a.answerCallback(cq.ID, "导出失败列表失败", true)
		return err
	}
	a.answerCallback(cq.ID, "已导出失败列表", false)
	return nil
}

func (a *App) listPrivateGroupControlEligibleGroups(userID int64) ([]db.GroupConfig, error) {
	if a == nil || a.db == nil || userID == 0 {
		return nil, nil
	}
	botID := int64(0)
	if a.api != nil {
		botID = a.api.Self.ID
	}
	if botID == 0 {
		return nil, errors.New("bot identity is empty")
	}
	rows, err := a.listPrivateGroupControlPrivilegedGroups(userID)
	if err != nil {
		return nil, err
	}
	out := make([]db.GroupConfig, 0, len(rows))
	memberResults := make([]bool, len(rows))
	workerCount := len(rows)
	if workerCount > privateGroupControlMemberCheckConcurrency {
		workerCount = privateGroupControlMemberCheckConcurrency
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				group := rows[index]
				memberResults[index] = a.isPrivateGroupControlMemberCached(group.ChatID, botID)
			}
		}()
	}
	for index := range rows {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	for index, group := range rows {
		if memberResults[index] {
			out = append(out, group)
		}
	}
	if err := a.sortPrivateGroupControlGroupsByActivity(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *App) sortPrivateGroupControlGroupsByActivity(groups []db.GroupConfig) error {
	if len(groups) <= 1 || a == nil || a.db == nil {
		return nil
	}
	activity := make(map[int64]time.Time, len(groups))
	chatIDs := make([]int64, 0, len(groups))
	for _, group := range groups {
		chatIDs = append(chatIDs, group.ChatID)
	}
	lastSeenByChatID, err := a.db.ListGroupLastSeenAt(chatIDs)
	if err != nil {
		return err
	}
	for chatID, lastSeenAt := range lastSeenByChatID {
		if seenAt, ok := parsePrivateGroupControlActivityTime(lastSeenAt); ok {
			activity[chatID] = seenAt
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		leftSeen, leftActive := activity[groups[i].ChatID]
		rightSeen, rightActive := activity[groups[j].ChatID]
		if leftActive != rightActive {
			return leftActive
		}
		if leftActive && !leftSeen.Equal(rightSeen) {
			return leftSeen.After(rightSeen)
		}
		leftTitle := strings.ToLower(privateGroupControlGroupTitle(groups[i]))
		rightTitle := strings.ToLower(privateGroupControlGroupTitle(groups[j]))
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return groups[i].ChatID < groups[j].ChatID
	})
	return nil
}

func parsePrivateGroupControlActivityTime(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func (a *App) handlePrivateGroupControlInput(ctx context.Context, msg *tgbotapi.Message, userID int64, text string) (bool, error) {
	_ = ctx
	if msg == nil || msg.Chat == nil || !msg.Chat.IsPrivate() {
		return false, nil
	}
	session, ok := a.getPrivateGroupControlSession(userID)
	if !ok {
		return false, nil
	}
	if session.UserID != userID || session.PrivateChatID != msg.Chat.ID {
		return false, nil
	}
	switch session.Stage {
	case privateGroupControlStageAwaitingTagInput:
		return true, a.handlePrivateGroupControlTagInput(msg, session, text)
	case privateGroupControlStageAwaitingTypeInput:
		return true, a.handlePrivateGroupControlTypeInput(msg, session, text)
	case privateGroupControlStageAwaitingCloseTimeInput:
		return true, a.handlePrivateGroupControlCloseTimeInput(msg, session, text)
	case privateGroupControlStageAwaitingThresholdInput:
		return true, a.handlePrivateGroupControlThresholdInput(msg, session, text)
	case privateGroupControlStageAwaitingClosingTextInput:
		return true, a.handlePrivateGroupControlClosingTextInput(msg, session, text)
	case privateGroupControlStageAwaitingBroadcastContent:
		return true, a.handlePrivateGroupControlBroadcastInput(msg, session, text)
	case privateGroupControlStageSelecting, privateGroupControlStageCategoryMenu, privateGroupControlStageCategoryList, privateGroupControlStageCategoryDetail, privateGroupControlStageClosingReminderMenu, privateGroupControlStageClosingReminderTextMenu, privateGroupControlStageAwaitingConfirm:
		return true, a.reply(msg.Chat.ID, msg.MessageID, "请点击当前面板按钮继续。")
	default:
		return false, nil
	}
}

func (a *App) handlePrivateGroupControlTagInput(msg *tgbotapi.Message, session privateGroupControlSession, text string) error {
	if strings.TrimSpace(msg.Text) == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "标签不能为空，请重新输入。")
	}
	tags := utils.SplitTags(text)
	if len(tags) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "标签不能为空，请重新输入。")
	}
	session.PendingValue = strings.Join(tags, ",")
	session.Stage = privateGroupControlStageAwaitingConfirm
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	textOut, markup := a.renderPrivateGroupControlConfirm(session)
	m := tgbotapi.NewMessage(msg.Chat.ID, textOut)
	m.ReplyToMessageID = msg.MessageID
	m.ReplyMarkup = markup
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) handlePrivateGroupControlTypeInput(msg *tgbotapi.Message, session privateGroupControlSession, text string) error {
	value := strings.TrimSpace(msg.Text)
	if value == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "类型不能为空，请重新输入。")
	}
	session.PendingValue = value
	session.Stage = privateGroupControlStageAwaitingConfirm
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	textOut, markup := a.renderPrivateGroupControlConfirm(session)
	m := tgbotapi.NewMessage(msg.Chat.ID, textOut)
	m.ReplyToMessageID = msg.MessageID
	m.ReplyMarkup = markup
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) handlePrivateGroupControlCloseTimeInput(msg *tgbotapi.Message, session privateGroupControlSession, text string) error {
	value, err := parseClosingReminderTime(text)
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式错误，请发送 21:49。")
	}
	privateGroupControlClearClosingReminderPending(&session)
	session.PendingClosingAction = string(privateGroupControlClosingReminderActionSetTime)
	session.PendingValue = value
	session.Stage = privateGroupControlStageAwaitingConfirm
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	textOut, markup := a.renderPrivateGroupControlConfirm(session)
	m := tgbotapi.NewMessage(msg.Chat.ID, textOut)
	m.ReplyToMessageID = msg.MessageID
	m.ReplyMarkup = markup
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) handlePrivateGroupControlThresholdInput(msg *tgbotapi.Message, session privateGroupControlSession, text string) error {
	thresholdU, err := parsePrivateGroupControlThresholdU(text)
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式错误，请发送U差额提醒阈值，例如 2.5。")
	}
	privateGroupControlClearClosingReminderPending(&session)
	session.PendingClosingAction = string(privateGroupControlClosingReminderActionThreshold)
	session.PendingThresholdU = thresholdU
	session.HasPendingThresholdU = true
	session.Stage = privateGroupControlStageAwaitingConfirm
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	textOut, markup := a.renderPrivateGroupControlConfirm(session)
	m := tgbotapi.NewMessage(msg.Chat.ID, textOut)
	m.ReplyToMessageID = msg.MessageID
	m.ReplyMarkup = markup
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) handlePrivateGroupControlClosingTextInput(msg *tgbotapi.Message, session privateGroupControlSession, text string) error {
	content := strings.TrimSpace(text)
	status := strings.TrimSpace(session.PendingClosingStatus)
	if _, ok := closingReminderStatusLabelFromStatus(status); !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, "下课文案状态无效，请返回设置重新选择。")
	}
	if content == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "下课文案不能为空，请重新输入。")
	}
	if !closingReminderGroupTextIsSafe(content) {
		return a.reply(msg.Chat.ID, msg.MessageID, "下课文案包含敏感词或金额，请调整为群内体面文案。")
	}
	session.PendingClosingAction = string(privateGroupControlClosingReminderActionSetText)
	session.PendingClosingStatus = status
	session.PendingValue = content
	session.PendingThresholdU = 0
	session.HasPendingThresholdU = false
	session.Stage = privateGroupControlStageAwaitingConfirm
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	textOut, markup := a.renderPrivateGroupControlConfirm(session)
	m := tgbotapi.NewMessage(msg.Chat.ID, textOut)
	m.ReplyToMessageID = msg.MessageID
	m.ReplyMarkup = markup
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func parsePrivateGroupControlThresholdU(text string) (float64, error) {
	raw := strings.TrimSpace(text)
	if strings.Count(raw, " ") > 0 || strings.Count(raw, "\t") > 0 || raw == "" {
		return 0, errors.New("invalid closing reminder threshold")
	}
	raw = strings.TrimSuffix(strings.TrimSuffix(raw, "U"), "u")
	thresholdU, err := strconv.ParseFloat(raw, 64)
	if err != nil || thresholdU <= 0 {
		return 0, errors.New("invalid closing reminder threshold")
	}
	return thresholdU, nil
}

func privateGroupControlClosingReminderGuideLines() []string {
	return []string{
		"状态规则",
		"",
		"正常：收支差额在阈值内",
		"待确认：入款大于下发，且超过阈值",
		"无账单：本账期无入款/下发",
		"复核：只有下发，或下发大于入款且超过阈值",
		"失败：统计或发送异常",
		"",
		"U阈值只影响“待确认/复核”的判断，不影响下课时间。",
	}
}

func (a *App) handlePrivateGroupControlBroadcastInput(msg *tgbotapi.Message, session privateGroupControlSession, text string) error {
	if strings.TrimSpace(msg.MediaGroupID) != "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "不支持相册消息，请发送单条文字、图片、视频或语音。")
	}
	if privateGroupControlHasUnsupportedMedia(msg) {
		return a.reply(msg.Chat.ID, msg.MessageID, "不支持的群发内容，请发送文字、图片、视频或语音。")
	}
	content := strings.TrimSpace(text)
	hasMedia := isInteractiveBroadcastMediaMessage(msg)
	if !hasMedia && content == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "群发内容不能为空，请重新输入。")
	}
	session.Stage = privateGroupControlStageAwaitingConfirm
	session.ContentPreview = interactiveBroadcastContentPreview(msg, content)
	if hasMedia {
		session.ContentMode = "copy"
		session.Content = ""
		session.SourceChatID = msg.Chat.ID
		session.SourceMessageID = msg.MessageID
	} else {
		session.ContentMode = "text"
		session.Content = content
		session.SourceChatID = 0
		session.SourceMessageID = 0
	}
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)
	textOut, markup := a.renderPrivateGroupControlConfirm(session)
	m := tgbotapi.NewMessage(msg.Chat.ID, textOut)
	m.ReplyToMessageID = msg.MessageID
	m.ReplyMarkup = markup
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func privateGroupControlHasUnsupportedMedia(msg *tgbotapi.Message) bool {
	if msg == nil {
		return false
	}
	return msg.Document != nil || msg.Audio != nil || msg.Animation != nil || msg.Sticker != nil
}

func (a *App) renderPrivateGroupControlConfirm(session privateGroupControlSession) (string, tgbotapi.InlineKeyboardMarkup) {
	lines := []string{
		"私聊群控确认",
		fmt.Sprintf("操作：%s", privateGroupControlActionLabelForSession(session)),
		fmt.Sprintf("目标群：%d 个", countPrivateGroupControlSelected(session.SelectedChatIDs)),
	}
	switch session.Action {
	case privateGroupControlActionTag:
		lines = append(lines, "目标标签："+strings.Join(utils.SplitTags(session.PendingValue), "、"))
		lines = append(lines, "点击【确认设置】执行。")
	case privateGroupControlActionType:
		lines = append(lines, "目标类型："+strings.TrimSpace(session.PendingValue))
		lines = append(lines, "点击【确认设置】执行。")
	case privateGroupControlActionCategory:
		lines = append(lines, privateGroupControlCategoryPendingLines(session)...)
		lines = append(lines, "点击【"+privateGroupControlCategoryConfirmLabel(session)+"】执行。")
	case privateGroupControlActionCloseTime:
		lines = append(lines, a.privateGroupControlClosingReminderConfirmLines(session)...)
		lines = append(lines, "点击【确认设置】执行。")
	case privateGroupControlActionBroadcast:
		preview := strings.TrimSpace(session.ContentPreview)
		if preview == "" {
			preview = "媒体消息"
		}
		lines = append(lines, "群发内容："+preview)
		lines = append(lines, "点击【确认群发】执行。")
	}
	lines = append(lines, "--------------------------------")
	for _, group := range privateGroupControlSelectedGroups(session) {
		lines = append(lines, "• "+privateGroupControlGroupTitle(group))
	}
	confirmLabel := "确认设置"
	if session.Action == privateGroupControlActionBroadcast {
		confirmLabel = "确认群发"
	}
	if session.Action == privateGroupControlActionCategory {
		confirmLabel = privateGroupControlCategoryConfirmLabel(session)
	}
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(confirmLabel, buildPrivateGroupControlCallbackData(session, "confirm")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(privateGroupControlConfirmBackLabel(session), buildPrivateGroupControlCallbackData(session, "back")),
			tgbotapi.NewInlineKeyboardButtonData("取消", buildPrivateGroupControlCallbackData(session, "cancel")),
		),
	)
	return strings.Join(lines, "\n"), markup
}

func privateGroupControlCategoryConfirmLabel(session privateGroupControlSession) string {
	switch session.CategoryOp {
	case privateGroupControlCategoryOpAdd:
		return "确认添加"
	case privateGroupControlCategoryOpRemove:
		return "确认移出"
	default:
		return "确认执行"
	}
}

func (a *App) privateGroupControlClosingReminderConfirmLines(session privateGroupControlSession) []string {
	lines := []string{"子操作：" + privateGroupControlClosingReminderSubactionLabel(session)}
	summary := a.privateGroupControlClosingReminderSummary(session)
	switch privateGroupControlClosingReminderAction(session.PendingClosingAction) {
	case privateGroupControlClosingReminderActionSetTime:
		lines = append(lines, "当前时间分布：")
		lines = append(lines, privateGroupControlCountLines(summary.TimeCounts)...)
		lines = append(lines, "新时间："+strings.TrimSpace(session.PendingValue))
		lines = append(lines, "影响说明：统一修改下课提醒时间，不改变U阈值和文案。")
	case privateGroupControlClosingReminderActionThreshold:
		lines = append(lines, "当前U阈值分布：")
		lines = append(lines, privateGroupControlCountLines(summary.ThresholdCounts)...)
		lines = append(lines, "目标阈值："+formatClosingReminderAmount(session.PendingThresholdU)+" U")
		lines = append(lines, "新阈值："+formatClosingReminderAmount(session.PendingThresholdU)+" U")
		lines = append(lines, "影响说明：只影响待确认/复核状态判断，不改变下课时间和文案。")
	case privateGroupControlClosingReminderActionEnable:
		lines = append(lines, fmt.Sprintf("当前开关：已开启 %d 个 / 已关闭 %d 个", summary.Enabled, summary.Disabled))
		lines = append(lines, "新状态：开启下课提醒")
		lines = append(lines, "影响说明：不改变下课时间、U阈值和文案。")
	case privateGroupControlClosingReminderActionDisable:
		lines = append(lines, fmt.Sprintf("当前开关：已开启 %d 个 / 已关闭 %d 个", summary.Enabled, summary.Disabled))
		lines = append(lines, "新状态：关闭下课提醒")
		lines = append(lines, "影响说明：不改变下课时间、U阈值和文案。")
	case privateGroupControlClosingReminderActionSetText:
		label, _ := closingReminderStatusLabelFromStatus(session.PendingClosingStatus)
		lines = append(lines, "当前文案摘要：")
		lines = append(lines, privateGroupControlClosingReminderSingleTextSummaryLine(summary, summary.Total, session.PendingClosingStatus))
		lines = append(lines, "状态："+label)
		lines = append(lines, "目标文案："+trimForPreview(strings.TrimSpace(session.PendingValue), 60))
		lines = append(lines, "影响说明：只修改该状态文案，不改变开关、时间和U阈值。")
	case privateGroupControlClosingReminderActionRestoreText:
		label, _ := closingReminderStatusLabelFromStatus(session.PendingClosingStatus)
		lines = append(lines, "当前文案摘要：")
		lines = append(lines, privateGroupControlClosingReminderSingleTextSummaryLine(summary, summary.Total, session.PendingClosingStatus))
		lines = append(lines, "状态："+label)
		lines = append(lines, "新值：恢复默认文案")
		lines = append(lines, "影响说明：只恢复该状态文案，不改变开关、时间和U阈值。")
	case privateGroupControlClosingReminderActionRestoreAll:
		lines = append(lines, "当前文案摘要：")
		lines = append(lines, privateGroupControlClosingReminderTextSummaryLines(summary, summary.Total)...)
		lines = append(lines, "新值：全部恢复默认文案")
		lines = append(lines, "影响说明：只恢复文案，不改变开关、时间和U阈值。")
	default:
		lines = append(lines, "请返回设置重新选择。")
	}
	return lines
}

func privateGroupControlClosingReminderSingleTextSummaryLine(summary privateGroupControlClosingReminderSummary, total int, status string) string {
	label, _ := closingReminderStatusLabelFromStatus(status)
	custom := summary.TextCustom[status]
	return fmt.Sprintf("- %s：%d 个自定义 / %d 个默认", label, custom, total-custom)
}

func privateGroupControlConfirmBackLabel(session privateGroupControlSession) string {
	if session.Action == privateGroupControlActionCloseTime {
		switch privateGroupControlClosingReminderAction(session.PendingClosingAction) {
		case privateGroupControlClosingReminderActionSetText,
			privateGroupControlClosingReminderActionRestoreText,
			privateGroupControlClosingReminderActionRestoreAll:
			return "返回文案管理"
		}
		return "返回设置"
	}
	return "返回重选"
}

func privateGroupControlCategoryPendingLines(session privateGroupControlSession) []string {
	kindLabel := privateGroupControlCategoryKindLabel(session.CategoryKind)
	value := strings.TrimSpace(session.CategoryValue)
	lines := []string{kindLabel + "：" + value}
	switch session.CategoryOp {
	case privateGroupControlCategoryOpAdd:
		lines = append(lines, "动作：添加群到"+kindLabel)
	case privateGroupControlCategoryOpRemove:
		lines = append(lines, "动作：从"+kindLabel+"移出群")
	default:
		lines = append(lines, "动作：未知")
	}
	return lines
}

func privateGroupControlClosingReminderPendingLines(session privateGroupControlSession) []string {
	subAction := privateGroupControlClosingReminderSubactionLabel(session)
	lines := []string{"子操作：" + subAction}
	switch privateGroupControlClosingReminderAction(session.PendingClosingAction) {
	case privateGroupControlClosingReminderActionSetTime:
		lines = append(lines, "目标时间："+strings.TrimSpace(session.PendingValue))
	case privateGroupControlClosingReminderActionThreshold:
		lines = append(lines, "目标阈值："+formatClosingReminderAmount(session.PendingThresholdU)+" U")
	case privateGroupControlClosingReminderActionSetText:
		label, _ := closingReminderStatusLabelFromStatus(session.PendingClosingStatus)
		lines = append(lines, "状态："+label)
		lines = append(lines, "目标文案："+trimForPreview(strings.TrimSpace(session.PendingValue), 60))
	case privateGroupControlClosingReminderActionRestoreText:
		label, _ := closingReminderStatusLabelFromStatus(session.PendingClosingStatus)
		lines = append(lines, "状态："+label)
	case privateGroupControlClosingReminderActionEnable, privateGroupControlClosingReminderActionDisable, privateGroupControlClosingReminderActionRestoreAll:
	default:
		lines = append(lines, "请返回设置重新选择。")
	}
	return lines
}

func privateGroupControlClosingReminderSubactionLabel(session privateGroupControlSession) string {
	switch privateGroupControlClosingReminderAction(session.PendingClosingAction) {
	case privateGroupControlClosingReminderActionEnable:
		return "开启下课提醒"
	case privateGroupControlClosingReminderActionDisable:
		return "关闭下课提醒"
	case privateGroupControlClosingReminderActionSetTime:
		return "设置下课时间"
	case privateGroupControlClosingReminderActionThreshold:
		return "设置U差额阈值"
	case privateGroupControlClosingReminderActionSetText:
		label, _ := closingReminderStatusLabelFromStatus(session.PendingClosingStatus)
		return "设置" + label + "文案"
	case privateGroupControlClosingReminderActionRestoreText:
		label, _ := closingReminderStatusLabelFromStatus(session.PendingClosingStatus)
		return "恢复" + label + "文案"
	case privateGroupControlClosingReminderActionRestoreAll:
		return "恢复全部文案"
	default:
		return "下课提醒设置"
	}
}

func privateGroupControlClosingReminderResultTitle(session privateGroupControlSession) string {
	switch privateGroupControlClosingReminderAction(session.PendingClosingAction) {
	case privateGroupControlClosingReminderActionEnable:
		return "批量开启下课提醒结果："
	case privateGroupControlClosingReminderActionDisable:
		return "批量关闭下课提醒结果："
	case privateGroupControlClosingReminderActionSetTime:
		return "批量设置下课时间结果："
	case privateGroupControlClosingReminderActionThreshold:
		return "批量设置U差额阈值结果："
	case privateGroupControlClosingReminderActionSetText:
		return "批量设置下课文案结果："
	case privateGroupControlClosingReminderActionRestoreText, privateGroupControlClosingReminderActionRestoreAll:
		return "批量恢复下课文案结果："
	default:
		return "批量设置下课提醒结果："
	}
}

func closingReminderStatusLabelFromStatus(status string) (string, bool) {
	for _, item := range closingReminderStatusLabelList() {
		if strings.TrimSpace(status) == item.status {
			return item.label, true
		}
	}
	return "", false
}

func privateGroupControlSelectedGroups(session privateGroupControlSession) []db.GroupConfig {
	out := make([]db.GroupConfig, 0, countPrivateGroupControlSelected(session.SelectedChatIDs))
	for _, group := range session.Groups {
		if session.SelectedChatIDs[group.ChatID] {
			out = append(out, group)
		}
	}
	return out
}

func (a *App) executePrivateGroupControlConfirm(cq *tgbotapi.CallbackQuery, session privateGroupControlSession) error {
	validGroups, invalidIssues, err := a.validatePrivateGroupControlTargets(session)
	if err != nil {
		a.clearPrivateGroupControlSession(session.UserID)
		return err
	}
	result := privateGroupControlExecutionResult{
		Expected:      countPrivateGroupControlSelected(session.SelectedChatIDs),
		Invalid:       len(invalidIssues),
		InvalidGroups: privateGroupControlIssueLabels(invalidIssues, 5),
		InvalidIssues: invalidIssues,
	}
	if len(validGroups) == 0 {
		result.Failed = result.Expected
		return a.replyPrivateGroupControlResult(cq, session, result, "没有有效目标，请重新选择。")
	}

	switch session.Action {
	case privateGroupControlActionTag:
		result = a.executePrivateGroupControlUpdate(validGroups, session, "group_tags", strings.TrimSpace(session.PendingValue), result)
	case privateGroupControlActionType:
		result = a.executePrivateGroupControlUpdate(validGroups, session, "group_type", strings.TrimSpace(session.PendingValue), result)
	case privateGroupControlActionCategory:
		result = a.executePrivateGroupControlCategoryUpdate(validGroups, session, result)
	case privateGroupControlActionCloseTime:
		result = a.executePrivateGroupControlClosingReminderTime(validGroups, session, result)
	case privateGroupControlActionBroadcast:
		br := a.executePrivateGroupControlBroadcast(validGroups, session)
		result.Expected = countPrivateGroupControlSelected(session.SelectedChatIDs)
		result.Succeeded = br.Succeeded
		result.Failed = br.Failed
		result.FailedGroups = br.FailedGroups
		result.FailedIssues = br.FailedIssues
		result.Invalid = len(invalidIssues)
		result.InvalidGroups = privateGroupControlIssueLabels(invalidIssues, 5)
		result.InvalidIssues = invalidIssues
	default:
		a.clearPrivateGroupControlSession(session.UserID)
		return a.reply(cq.Message.Chat.ID, cq.Message.MessageID, "未知群控操作，任务已结束。")
	}

	return a.replyPrivateGroupControlResult(cq, session, result, "")
}

func (a *App) executePrivateGroupControlUpdate(groups []db.GroupConfig, session privateGroupControlSession, field, value string, result privateGroupControlExecutionResult) privateGroupControlExecutionResult {
	for _, group := range groups {
		if err := a.db.UpdateGroupFields(group.ChatID, map[string]any{field: value}); err != nil {
			result.Failed++
			if len(result.FailedGroups) < 5 {
				result.FailedGroups = append(result.FailedGroups, privateGroupControlFailedGroupLabel(group))
			}
			result.FailedIssues = append(result.FailedIssues, privateGroupControlIssueFromGroup(group, "写入失败："+err.Error()))
			continue
		}
		result.Succeeded++
	}
	return result
}

func (a *App) executePrivateGroupControlCategoryUpdate(groups []db.GroupConfig, session privateGroupControlSession, result privateGroupControlExecutionResult) privateGroupControlExecutionResult {
	value := strings.TrimSpace(session.CategoryValue)
	if value == "" || !privateGroupControlCategoryKindValid(session.CategoryKind) {
		for _, group := range groups {
			result = privateGroupControlAppendFailedIssue(result, group, "类型/标签已失效")
		}
		return result
	}
	for _, group := range groups {
		result = a.executePrivateGroupControlCategoryUpdateOne(group, session, value, result)
	}
	return result
}

func (a *App) executePrivateGroupControlCategoryUpdateOne(group db.GroupConfig, session privateGroupControlSession, value string, result privateGroupControlExecutionResult) privateGroupControlExecutionResult {
	for attempt := 0; attempt < privateGroupControlCategoryCASMaxAttempts; attempt++ {
		latest, err := a.db.GetGroup(group.ChatID)
		if err != nil {
			return privateGroupControlAppendFailedIssue(result, group, "读取最新配置失败："+err.Error())
		}
		field, currentValue, nextValue, skipReason := privateGroupControlCategoryFieldAndNextValue(latest, session.CategoryKind, session.CategoryOp, value)
		if skipReason != "" {
			return privateGroupControlAppendInvalidIssue(result, latest, skipReason)
		}
		if field == "" {
			return privateGroupControlAppendFailedIssue(result, latest, "类型/标签操作无效")
		}
		updated, err := a.db.UpdateGroupTextFieldIfCurrent(latest.ChatID, field, currentValue, nextValue)
		if err != nil {
			return privateGroupControlAppendFailedIssue(result, latest, "写入失败："+err.Error())
		}
		if updated {
			result.Succeeded++
			return result
		}
	}
	return privateGroupControlAppendInvalidIssue(result, group, privateGroupControlCategoryConcurrentModified)
}

func privateGroupControlCategoryFieldAndNextValue(group db.GroupConfig, kind privateGroupControlCategoryKind, op privateGroupControlCategoryOp, value string) (string, string, string, string) {
	switch kind {
	case privateGroupControlCategoryKindType:
		nextValue, skipReason := privateGroupControlNextTypeValue(group.GroupType, op, value)
		return "group_type", group.GroupType, nextValue, skipReason
	case privateGroupControlCategoryKindTag:
		nextValue, skipReason := privateGroupControlNextTagValue(group.GroupTags, op, value)
		return "group_tags", group.GroupTags, nextValue, skipReason
	default:
		return "", "", "", "类型/标签操作无效"
	}
}

func privateGroupControlNextTypeValue(currentType string, op privateGroupControlCategoryOp, value string) (string, string) {
	current := strings.TrimSpace(currentType)
	switch op {
	case privateGroupControlCategoryOpAdd:
		return value, ""
	case privateGroupControlCategoryOpRemove:
		if current != value {
			return "", "当前已不属于该类型"
		}
		return "", ""
	default:
		return "", "类型操作无效"
	}
}

func privateGroupControlNextTagValue(currentTags string, op privateGroupControlCategoryOp, value string) (string, string) {
	tags := utils.SplitTags(currentTags)
	switch op {
	case privateGroupControlCategoryOpAdd:
		if !privateGroupControlTagListContains(tags, value) {
			tags = append(tags, value)
		}
		return strings.Join(tags, ","), ""
	case privateGroupControlCategoryOpRemove:
		if !privateGroupControlTagListContains(tags, value) {
			return "", "当前已不包含该标签"
		}
		filtered := make([]string, 0, len(tags))
		for _, tag := range tags {
			if tag != value {
				filtered = append(filtered, tag)
			}
		}
		return strings.Join(filtered, ","), ""
	default:
		return "", "标签操作无效"
	}
}

func privateGroupControlTagListContains(tags []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, tag := range tags {
		if tag == value {
			return true
		}
	}
	return false
}

func privateGroupControlAppendInvalidIssue(result privateGroupControlExecutionResult, group db.GroupConfig, reason string) privateGroupControlExecutionResult {
	result.Invalid++
	if len(result.InvalidGroups) < 5 {
		result.InvalidGroups = append(result.InvalidGroups, privateGroupControlFailedGroupLabel(group))
	}
	result.InvalidIssues = append(result.InvalidIssues, privateGroupControlIssueFromGroup(group, reason))
	return result
}

func privateGroupControlAppendFailedIssue(result privateGroupControlExecutionResult, group db.GroupConfig, reason string) privateGroupControlExecutionResult {
	result.Failed++
	if len(result.FailedGroups) < 5 {
		result.FailedGroups = append(result.FailedGroups, privateGroupControlFailedGroupLabel(group))
	}
	result.FailedIssues = append(result.FailedIssues, privateGroupControlIssueFromGroup(group, reason))
	return result
}

func (a *App) executePrivateGroupControlClosingReminderTime(groups []db.GroupConfig, session privateGroupControlSession, result privateGroupControlExecutionResult) privateGroupControlExecutionResult {
	for _, group := range groups {
		config, err := a.db.GetClosingReminderConfig(group.ChatID)
		if err == nil {
			config.ChatID = group.ChatID
			privateGroupControlApplyClosingReminderPending(&config, session)
			config.UpdatedBy = session.UserID
			err = a.db.UpsertClosingReminderConfig(config)
		}
		if err != nil {
			result.Failed++
			if len(result.FailedGroups) < 5 {
				result.FailedGroups = append(result.FailedGroups, privateGroupControlFailedGroupLabel(group))
			}
			result.FailedIssues = append(result.FailedIssues, privateGroupControlIssueFromGroup(group, "写入失败："+err.Error()))
			continue
		}
		result.Succeeded++
	}
	return result
}

func privateGroupControlApplyClosingReminderPending(config *db.GroupClosingReminderConfig, session privateGroupControlSession) {
	if config == nil {
		return
	}
	switch privateGroupControlClosingReminderAction(session.PendingClosingAction) {
	case privateGroupControlClosingReminderActionEnable:
		config.Enabled = 1
	case privateGroupControlClosingReminderActionDisable:
		config.Enabled = 0
	case privateGroupControlClosingReminderActionSetTime:
		config.ReminderTime = strings.TrimSpace(session.PendingValue)
	case privateGroupControlClosingReminderActionThreshold:
		if session.HasPendingThresholdU && session.PendingThresholdU > 0 {
			config.ThresholdU = session.PendingThresholdU
		}
	case privateGroupControlClosingReminderActionSetText:
		setClosingReminderTextField(config, session.PendingClosingStatus, strings.TrimSpace(session.PendingValue))
	case privateGroupControlClosingReminderActionRestoreText:
		setClosingReminderTextField(config, session.PendingClosingStatus, "")
	case privateGroupControlClosingReminderActionRestoreAll:
		config.TextNormal = ""
		config.TextPending = ""
		config.TextNoData = ""
		config.TextReview = ""
		config.TextFailed = ""
	}
}

func (a *App) executePrivateGroupControlBroadcast(groups []db.GroupConfig, session privateGroupControlSession) broadcastBatchResult {
	if session.ContentMode == "copy" && session.SourceMessageID > 0 {
		result, err := a.broadcastForwardToGroupsDetailed(groups, session.SourceChatID, session.SourceMessageID)
		if err != nil {
			log.Printf("私聊媒体群发失败: %v", err)
		}
		return result
	}
	result, err := a.broadcastTextToGroupsDetailed(groups, session.Content, "")
	if err != nil {
		log.Printf("私聊文字群发失败: %v", err)
	}
	return result
}

func (a *App) validatePrivateGroupControlTargets(session privateGroupControlSession) ([]db.GroupConfig, []privateGroupControlTargetIssue, error) {
	if a == nil || a.db == nil || session.UserID == 0 {
		return nil, nil, nil
	}
	botID := int64(0)
	if a.api != nil {
		botID = a.api.Self.ID
	}
	if botID == 0 {
		return nil, nil, errors.New("bot identity is empty")
	}
	current, err := a.ListPrivilegedGroups(session.UserID)
	if err != nil {
		return nil, nil, err
	}
	validByID := make(map[int64]db.GroupConfig, len(current))
	for _, group := range current {
		validByID[group.ChatID] = group
	}
	valid := make([]db.GroupConfig, 0, len(session.SelectedChatIDs))
	invalid := make([]privateGroupControlTargetIssue, 0)
	for _, original := range privateGroupControlSelectedGroups(session) {
		group, ok := validByID[original.ChatID]
		if !ok {
			invalid = append(invalid, privateGroupControlIssueFromGroup(original, "权限已失效"))
			continue
		}
		if !a.IsGroupMember(group.ChatID, botID) {
			invalid = append(invalid, privateGroupControlIssueFromGroup(group, "机器人不在群内"))
			continue
		}
		if active, reason := a.checkGroupEntitlement(group); !active {
			invalid = append(invalid, privateGroupControlIssueFromGroup(group, orDefault(reason, "群功能已到期")))
			continue
		}
		valid = append(valid, group)
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].ChatID < valid[j].ChatID })
	return valid, invalid, nil
}

func privateGroupControlIssueFromGroup(group db.GroupConfig, reason string) privateGroupControlTargetIssue {
	return privateGroupControlTargetIssue{
		ChatID: group.ChatID,
		Title:  privateGroupControlGroupTitle(group),
		Reason: strings.TrimSpace(reason),
	}
}

func privateGroupControlIssueLabels(issues []privateGroupControlTargetIssue, limit int) []string {
	if limit <= 0 || len(issues) == 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for i, issue := range issues {
		if i >= limit {
			break
		}
		out = append(out, privateGroupControlIssueLabel(issue))
	}
	return out
}

func privateGroupControlIssueLabel(issue privateGroupControlTargetIssue) string {
	title := strings.TrimSpace(issue.Title)
	if title == "" {
		title = "未命名群"
	}
	return fmt.Sprintf("%s(%d)", title, issue.ChatID)
}

func privateGroupControlResultIssues(result privateGroupControlExecutionResult) []privateGroupControlTargetIssue {
	issues := make([]privateGroupControlTargetIssue, 0, len(result.InvalidIssues)+len(result.FailedIssues))
	issues = append(issues, result.InvalidIssues...)
	issues = append(issues, result.FailedIssues...)
	return issues
}

func privateGroupControlSelectedIDsFromIssues(issues []privateGroupControlTargetIssue) map[int64]bool {
	selected := make(map[int64]bool, len(issues))
	for _, issue := range issues {
		selected[issue.ChatID] = true
	}
	return selected
}

func privateGroupControlGroupsFromIssues(issues []privateGroupControlTargetIssue) []db.GroupConfig {
	groups := make([]db.GroupConfig, 0, len(issues))
	seen := make(map[int64]bool, len(issues))
	for _, issue := range issues {
		if seen[issue.ChatID] {
			continue
		}
		seen[issue.ChatID] = true
		groups = append(groups, db.GroupConfig{ChatID: issue.ChatID, Title: issue.Title})
	}
	return groups
}

func privateGroupControlRecoveryMarkup(session privateGroupControlSession) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("重试失败群", buildPrivateGroupControlCallbackData(session, "retry")),
			tgbotapi.NewInlineKeyboardButtonData("重新选择", buildPrivateGroupControlCallbackData(session, "reselect")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("导出失败列表", buildPrivateGroupControlCallbackData(session, "export")),
		),
	)
}

func (a *App) replyPrivateGroupControlResult(cq *tgbotapi.CallbackQuery, session privateGroupControlSession, result privateGroupControlExecutionResult, extra string) error {
	issues := privateGroupControlResultIssues(result)
	text := a.privateGroupControlResultText(session, result, extra)
	if len(issues) == 0 {
		if session.Action == privateGroupControlActionCloseTime {
			next := session
			privateGroupControlClearClosingReminderPending(&next)
			next.Stage = privateGroupControlStageClosingReminderMenu
			next.Executing = false
			next.RecoveryIssues = nil
			next.Version++
			next.UpdatedAt = a.currentTime()
			a.setPrivateGroupControlSession(next)
			_, markup := a.renderPrivateGroupControlClosingReminderMenu(next)
			m := tgbotapi.NewMessage(cq.Message.Chat.ID, text)
			m.ReplyToMessageID = cq.Message.MessageID
			m.ReplyMarkup = markup
			return a.sendWithPriority(outboundPriorityNormalChat, m)
		}
		a.clearPrivateGroupControlSession(session.UserID)
		return a.reply(cq.Message.Chat.ID, cq.Message.MessageID, text)
	}

	session.Stage = privateGroupControlStageRecovery
	session.Groups = privateGroupControlGroupsFromIssues(issues)
	session.SelectedChatIDs = privateGroupControlSelectedIDsFromIssues(issues)
	session.CurrentPage = 0
	session.Executing = false
	session.RecoveryIssues = issues
	session.Version++
	session.UpdatedAt = a.currentTime()
	a.setPrivateGroupControlSession(session)

	m := tgbotapi.NewMessage(cq.Message.Chat.ID, text)
	m.ReplyToMessageID = cq.Message.MessageID
	m.ReplyMarkup = privateGroupControlRecoveryMarkup(session)
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) privateGroupControlResultText(session privateGroupControlSession, result privateGroupControlExecutionResult, extra string) string {
	title := "私聊群控执行结果："
	switch session.Action {
	case privateGroupControlActionTag:
		title = "批量设置群标签结果："
	case privateGroupControlActionType:
		title = "批量设置群类型结果："
	case privateGroupControlActionCategory:
		title = privateGroupControlCategoryResultTitle(session)
	case privateGroupControlActionCloseTime:
		title = privateGroupControlClosingReminderResultTitle(session)
	case privateGroupControlActionBroadcast:
		title = "选择群群发结果："
	}
	lines := []string{title}
	switch session.Action {
	case privateGroupControlActionTag:
		lines = append(lines, "目标标签："+strings.Join(utils.SplitTags(session.PendingValue), "、"))
	case privateGroupControlActionType:
		lines = append(lines, "目标类型："+strings.TrimSpace(session.PendingValue))
	case privateGroupControlActionCategory:
		lines = append(lines, privateGroupControlCategoryPendingLines(session)...)
	case privateGroupControlActionCloseTime:
		lines = append(lines, privateGroupControlClosingReminderPendingLines(session)...)
	case privateGroupControlActionBroadcast:
		preview := strings.TrimSpace(session.ContentPreview)
		if preview == "" {
			preview = "媒体消息"
		}
		lines = append(lines, "群发内容："+preview)
	}
	lines = append(lines, fmt.Sprintf("应执行目标：%d", result.Expected))
	if session.Action == privateGroupControlActionBroadcast {
		lines = append(lines,
			fmt.Sprintf("发送成功：%d", result.Succeeded),
			fmt.Sprintf("发送失败：%d", result.Failed),
		)
	} else {
		lines = append(lines,
			fmt.Sprintf("设置成功：%d", result.Succeeded),
			fmt.Sprintf("设置失败：%d", result.Failed),
		)
	}
	lines = append(lines, fmt.Sprintf("失效目标：%d", result.Invalid))
	if strings.TrimSpace(extra) != "" {
		lines = append(lines, extra)
	}
	if len(result.InvalidGroups) > 0 {
		lines = append(lines, "失效群："+strings.Join(result.InvalidGroups, "；"))
	}
	if len(result.FailedGroups) > 0 {
		lines = append(lines, "失败群："+strings.Join(result.FailedGroups, "；"))
	}
	if issues := privateGroupControlResultIssues(result); len(issues) > 0 {
		lines = append(lines, "失败/失效明细：")
		for i, issue := range issues {
			if i >= 10 {
				lines = append(lines, fmt.Sprintf("...还有 %d 个目标，点击【导出失败列表】查看完整明细。", len(issues)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("- %s：%s", privateGroupControlIssueLabel(issue), orDefault(issue.Reason, "执行失败")))
		}
		lines = append(lines, "下一步：可重试失败群、重新选择或导出失败列表。")
		lines = append(lines, "会话状态：等待处理")
	} else {
		if session.Action == privateGroupControlActionCloseTime {
			lines = append(lines, "会话状态：可继续设置")
		} else {
			lines = append(lines, "会话状态：已结束")
		}
	}
	return strings.Join(lines, "\n")
}

func privateGroupControlCategoryResultTitle(session privateGroupControlSession) string {
	if label := privateGroupControlCategoryOperationLabel(session); label != "" {
		return label + "结果："
	}
	return "类型/标签列表管理结果："
}

func privateGroupControlFailureListText(session privateGroupControlSession) string {
	lines := []string{
		"失败/失效列表",
		"操作：" + privateGroupControlActionLabelForSession(session),
	}
	switch session.Action {
	case privateGroupControlActionTag:
		lines = append(lines, "目标标签："+strings.Join(utils.SplitTags(session.PendingValue), "、"))
	case privateGroupControlActionType:
		lines = append(lines, "目标类型："+strings.TrimSpace(session.PendingValue))
	case privateGroupControlActionCategory:
		lines = append(lines, privateGroupControlCategoryPendingLines(session)...)
	case privateGroupControlActionCloseTime:
		lines = append(lines, privateGroupControlClosingReminderPendingLines(session)...)
	case privateGroupControlActionBroadcast:
		preview := strings.TrimSpace(session.ContentPreview)
		if preview == "" {
			preview = "媒体消息"
		}
		lines = append(lines, "群发内容："+preview)
	}
	lines = append(lines, "--------------------------------")
	for _, issue := range session.RecoveryIssues {
		lines = append(lines, fmt.Sprintf("- %s：%s", privateGroupControlIssueLabel(issue), orDefault(issue.Reason, "执行失败")))
	}
	return strings.Join(lines, "\n")
}

func privateGroupControlFailedGroupLabel(group db.GroupConfig) string {
	return fmt.Sprintf("%s(%d)", privateGroupControlGroupTitle(group), group.ChatID)
}

func runPrivateGroupControlBroadcastWorkers(groups []db.GroupConfig, send func(db.GroupConfig) error) []error {
	if len(groups) == 0 {
		return nil
	}
	if send == nil {
		return make([]error, len(groups))
	}

	workerCount := privateGroupControlBroadcastConcurrency
	if workerCount > len(groups) {
		workerCount = len(groups)
	}

	jobs := make(chan int)
	errs := make([]error, len(groups))
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()
			for index := range jobs {
				errs[index] = send(groups[index])
			}
		}()
	}

	for i := range groups {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return errs
}

func (a *App) broadcastTextToGroupsDetailed(groups []db.GroupConfig, content, identity string) (broadcastBatchResult, error) {
	result := broadcastBatchResult{Expected: len(groups)}
	if len(groups) == 0 {
		return result, nil
	}
	errs := runPrivateGroupControlBroadcastWorkers(groups, func(group db.GroupConfig) error {
		rendered := a.renderTemplateMessage(content, messageTemplateData{
			ChatID: group.ChatID,
			Now:    utils.BeijingNow(a.cfg.Timezone).Format("2006-01-02 15:04:05"),
		})
		sendText := rendered.Text
		parseMode := rendered.ParseMode
		if strings.TrimSpace(identity) != "" {
			mentions, _ := a.buildIdentityMentions(group.ChatID, identity)
			if mentions != "" {
				sendText = strings.TrimSpace(mentions + "\n" + sendText)
				parseMode = telegramParseModeHTML
			}
		}
		m := tgbotapi.NewMessage(group.ChatID, sendText)
		if parseMode == "" && strings.Contains(sendText, "<a href=") {
			parseMode = telegramParseModeHTML
		}
		if parseMode != "" {
			m.ParseMode = parseMode
		}
		return a.sendWithPriority(outboundPriorityBroadcastAd, m)
	})
	for i, err := range errs {
		group := groups[i]
		if err != nil {
			log.Printf("群发失败: %d", group.ChatID)
			result.Failed++
			if len(result.FailedGroups) < 5 {
				result.FailedGroups = append(result.FailedGroups, privateGroupControlFailedGroupLabel(group))
			}
			result.FailedIssues = append(result.FailedIssues, privateGroupControlIssueFromGroup(group, "发送失败："+err.Error()))
			continue
		}
		result.Succeeded++
	}
	return result, nil
}

func (a *App) broadcastForwardToGroupsDetailed(groups []db.GroupConfig, sourceChatID int64, sourceMessageID int) (broadcastBatchResult, error) {
	result := broadcastBatchResult{Expected: len(groups)}
	if len(groups) == 0 {
		return result, nil
	}
	errs := runPrivateGroupControlBroadcastWorkers(groups, func(group db.GroupConfig) error {
		fwd := tgbotapi.NewCopyMessage(group.ChatID, sourceChatID, sourceMessageID)
		return a.requestWithPriority(outboundPriorityBroadcastAd, fwd)
	})
	for i, err := range errs {
		group := groups[i]
		if err != nil {
			log.Printf("转发群发失败: %d", group.ChatID)
			result.Failed++
			if len(result.FailedGroups) < 5 {
				result.FailedGroups = append(result.FailedGroups, privateGroupControlFailedGroupLabel(group))
			}
			result.FailedIssues = append(result.FailedIssues, privateGroupControlIssueFromGroup(group, "发送失败："+err.Error()))
			continue
		}
		result.Succeeded++
	}
	return result, nil
}
