package bot

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
	"go-bot/internal/services"
	"go-bot/internal/utils"
)

type AdminBroadcastRequest struct {
	SourceChatID  int64
	TargetChatIDs []int64
	TargetTags    []string
	IdentityName  string
	MediaType     string
	MediaFileID   string
	Content       string
}

type GroupStats struct {
	DepositCount int
	PayoutCount  int
	DepositTotal float64
	PayoutTotal  float64
}

type AdminScheduleCreateRequest struct {
	ChatID          int64
	SendAt          string
	Content         string
	SourceChatID    int64
	SourceMessageID int
	IdentityName    string
	TargetTags      string
	CreatorUserID   int64
}

type AdminScheduleUpdateRequest struct {
	ChatID          int64
	ScheduleID      int64
	SendAt          string
	Content         string
	SourceChatID    int64
	SourceMessageID int
	IdentityName    string
	TargetTags      string
}

func (a *App) AdminBroadcast(req AdminBroadcastRequest) (int, error) {
	if req.SourceChatID == 0 && len(req.TargetChatIDs) == 0 && len(req.TargetTags) == 0 {
		return 0, fmt.Errorf("来源群、目标群或目标标签至少填写一项")
	}
	mode := strings.TrimSpace(strings.ToLower(req.MediaType))
	if mode == "" {
		mode = "text"
	}
	if mode != "text" && strings.TrimSpace(req.MediaFileID) == "" {
		return 0, fmt.Errorf("媒体模式必须提供 FileID")
	}
	if mode == "text" && strings.TrimSpace(req.Content) == "" {
		return 0, fmt.Errorf("纯文字群发时内容不能为空")
	}

	targetGroups, err := a.adminTargetGroups(req.SourceChatID, req.TargetChatIDs, req.TargetTags)
	if err != nil {
		return 0, err
	}
	if len(targetGroups) == 0 {
		return 0, fmt.Errorf("未找到可发送的目标群")
	}

	sent := 0
	for _, item := range targetGroups {
		sendText := strings.TrimSpace(req.Content)
		if strings.TrimSpace(req.IdentityName) != "" && item.AtBroadcastEnabled == 1 {
			mentions, _ := a.buildIdentityMentions(item.ChatID, req.IdentityName)
			if strings.TrimSpace(mentions) != "" {
				sendText = strings.TrimSpace(mentions + "\n" + sendText)
			}
		}
		if err := a.adminSendComposite(item.ChatID, mode, req.MediaFileID, sendText); err != nil {
			continue
		}
		sent++
	}
	return sent, nil
}

func (a *App) adminTargetGroups(sourceChatID int64, chatIDs []int64, tags []string) ([]db.GroupConfig, error) {
	if len(chatIDs) > 0 {
		if len(chatIDs) > 500 {
			return nil, fmt.Errorf("一次最多选择 500 个目标群")
		}
		seen := make(map[int64]struct{}, len(chatIDs))
		rows := make([]db.GroupConfig, 0, len(chatIDs))
		for _, chatID := range chatIDs {
			if chatID == 0 {
				continue
			}
			if _, ok := seen[chatID]; ok {
				continue
			}
			seen[chatID] = struct{}{}
			row, err := a.db.GetGroup(chatID)
			if err != nil {
				return nil, fmt.Errorf("目标群 %d 不存在: %w", chatID, err)
			}
			rows = append(rows, row)
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("未找到可发送的目标群")
		}
		return rows, nil
	}
	tags = utils.SplitTags(strings.Join(tags, ","))
	if len(tags) > 0 {
		rows, err := a.db.ListGroupsByTags(tags)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return rows, nil
		}
		return nil, fmt.Errorf("按标签未找到目标群：%s", strings.Join(tags, ","))
	}
	return a.listTargetGroups(sourceChatID)
}

func (a *App) adminSendComposite(chatID int64, mode, mediaFileID, content string) error {
	rendered := a.renderTemplateMessage(content, messageTemplateData{
		ChatID: chatID,
		Now:    utils.BeijingNow(a.cfg.Timezone).Format("2006-01-02 15:04:05"),
	})
	finalContent := rendered.Text
	parseMode := rendered.ParseMode
	if parseMode == "" && strings.Contains(finalContent, "<a href=") {
		parseMode = telegramParseModeHTML
	}

	switch mode {
	case "", "text":
		m := tgbotapi.NewMessage(chatID, finalContent)
		if strings.TrimSpace(finalContent) == "" {
			m.Text = "[空内容]"
		}
		if parseMode != "" {
			m.ParseMode = parseMode
		}
		return a.adminSendBroadcastChattable(m)
	case "photo":
		msg := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(mediaFileID))
		msg.Caption = trimRunes(finalContent, 1000)
		if parseMode != "" {
			msg.ParseMode = parseMode
		}
		return a.adminSendBroadcastChattable(msg)
	case "video":
		msg := tgbotapi.NewVideo(chatID, tgbotapi.FileID(mediaFileID))
		msg.Caption = trimRunes(finalContent, 1000)
		if parseMode != "" {
			msg.ParseMode = parseMode
		}
		return a.adminSendBroadcastChattable(msg)
	case "voice":
		msg := tgbotapi.NewVoice(chatID, tgbotapi.FileID(mediaFileID))
		msg.Caption = trimRunes(finalContent, 1000)
		if parseMode != "" {
			msg.ParseMode = parseMode
		}
		return a.adminSendBroadcastChattable(msg)
	default:
		return fmt.Errorf("不支持的媒体类型: %s", mode)
	}
}

func (a *App) adminSendBroadcastChattable(message tgbotapi.Chattable) error {
	dropped, err := a.sendWithPriorityOutcome(outboundPriorityBroadcastAd, message)
	if err != nil {
		return err
	}
	if dropped {
		return errOutboundDropped
	}
	return nil
}

func trimRunes(v string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(v) <= n {
		return v
	}
	out := make([]rune, 0, n)
	for _, r := range v {
		if len(out) >= n {
			break
		}
		out = append(out, r)
	}
	return string(out)
}

func (a *App) AdminGroupStats(chatID int64, dayCutTime string) (GroupStats, error) {
	startAt := utils.GetDayStartISO(dayCutTime, a.cfg.Timezone)
	rows, err := a.db.ListLedgerSince(chatID, startAt)
	if err != nil {
		return GroupStats{}, err
	}
	out := GroupStats{}
	for _, row := range rows {
		if row.EntryType == "deposit" {
			out.DepositCount++
			out.DepositTotal += row.FinalAmount
		} else {
			out.PayoutCount++
			out.PayoutTotal += row.FinalAmount
		}
	}
	return out, nil
}

func (a *App) AdminQueryCard(cardNo string) services.CardInfo {
	return a.card.QueryCard(cardNo)
}

func (a *App) AdminQueryBranch(cardNo string) string {
	return a.card.QueryBranchCode(cardNo)
}

func (a *App) AdminListSchedules(chatID int64, limit int) ([]db.Schedule, error) {
	if chatID == 0 {
		return nil, fmt.Errorf("群ID不能为空")
	}
	return a.db.ListSchedulesByChat(chatID, limit)
}

func (a *App) AdminCreateSchedule(req AdminScheduleCreateRequest) (int64, error) {
	if req.ChatID == 0 {
		return 0, fmt.Errorf("群ID不能为空")
	}
	runAt, err := a.parseAdminScheduleTime(req.SendAt)
	if err != nil {
		return 0, err
	}
	if runAt.Before(time.Now().In(a.loc)) {
		return 0, fmt.Errorf("定时发送时间必须晚于当前时间")
	}
	content := strings.TrimSpace(req.Content)
	sourceChatID := req.SourceChatID
	sourceMessageID := req.SourceMessageID
	if sourceMessageID > 0 {
		if sourceChatID == 0 {
			sourceChatID = req.ChatID
		}
		if content == "" {
			content = "[媒体群发]"
		}
	} else if content == "" {
		return 0, fmt.Errorf("文本内容不能为空，若发媒体请填写来源消息ID")
	}
	scheduleID, err := a.db.AddSchedule(
		req.ChatID,
		runAt.Format(time.RFC3339),
		content,
		sourceChatID,
		sourceMessageID,
		strings.TrimSpace(req.IdentityName),
		strings.TrimSpace(req.TargetTags),
		req.CreatorUserID,
	)
	if err != nil {
		return 0, err
	}
	a.scheduleJob(scheduleID, runAt)
	return scheduleID, nil
}

func (a *App) AdminUpdateSchedule(req AdminScheduleUpdateRequest) error {
	if req.ChatID == 0 || req.ScheduleID == 0 {
		return fmt.Errorf("群ID与任务ID不能为空")
	}
	row, err := a.db.GetSchedule(req.ScheduleID)
	if err != nil {
		return err
	}
	if row == nil || row.ChatID != req.ChatID {
		return fmt.Errorf("任务不存在")
	}
	runAt, err := a.parseAdminScheduleTime(req.SendAt)
	if err != nil {
		return err
	}
	if runAt.Before(time.Now().In(a.loc)) {
		return fmt.Errorf("新时间必须晚于当前时间")
	}
	content := strings.TrimSpace(req.Content)
	sourceChatID := req.SourceChatID
	sourceMessageID := req.SourceMessageID
	identityName := strings.TrimSpace(req.IdentityName)
	targetTags := strings.TrimSpace(req.TargetTags)
	if sourceMessageID > 0 {
		if sourceChatID == 0 {
			sourceChatID = req.ChatID
		}
		if content == "" {
			content = row.Content
			if strings.TrimSpace(content) == "" {
				content = "[媒体群发]"
			}
		}
	} else if content == "" {
		return fmt.Errorf("内容不能为空")
	}
	if err := a.db.UpdateSchedule(req.ScheduleID, map[string]any{
		"send_at":           runAt.Format(time.RFC3339),
		"content":           content,
		"source_chat_id":    sourceChatID,
		"source_message_id": sourceMessageID,
		"identity_name":     identityName,
		"target_tags":       targetTags,
		"status":            "pending",
	}); err != nil {
		return err
	}
	a.scheduleJob(req.ScheduleID, runAt)
	return nil
}

func (a *App) AdminResendSchedule(chatID, scheduleID int64) error {
	if chatID == 0 || scheduleID == 0 {
		return fmt.Errorf("群ID与任务ID不能为空")
	}
	row, err := a.db.GetSchedule(scheduleID)
	if err != nil {
		return err
	}
	if row == nil || row.ChatID != chatID {
		return fmt.Errorf("任务不存在")
	}
	runAt := time.Now().In(a.loc).Add(2 * time.Second)
	if err := a.db.UpdateSchedule(scheduleID, map[string]any{
		"send_at": runAt.Format(time.RFC3339),
		"status":  "pending",
	}); err != nil {
		return err
	}
	a.scheduleJob(scheduleID, runAt)
	return nil
}

func (a *App) AdminDisableSchedule(chatID, scheduleID int64) error {
	if chatID == 0 || scheduleID == 0 {
		return fmt.Errorf("群ID与任务ID不能为空")
	}
	row, err := a.db.GetSchedule(scheduleID)
	if err != nil {
		return err
	}
	if row == nil || row.ChatID != chatID {
		return fmt.Errorf("任务不存在")
	}
	if err := a.db.SetScheduleStatus(scheduleID, "canceled"); err != nil {
		return err
	}
	a.stopScheduleTimer(scheduleID)
	return nil
}

func (a *App) AdminDeleteSchedule(chatID, scheduleID int64) error {
	if chatID == 0 || scheduleID == 0 {
		return fmt.Errorf("群ID与任务ID不能为空")
	}
	row, err := a.db.GetSchedule(scheduleID)
	if err != nil {
		return err
	}
	if row == nil || row.ChatID != chatID {
		return fmt.Errorf("任务不存在")
	}
	if err := a.db.DeleteSchedule(scheduleID); err != nil {
		return err
	}
	a.stopScheduleTimer(scheduleID)
	return nil
}

func (a *App) parseAdminScheduleTime(raw string) (time.Time, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, fmt.Errorf("请填写定时发送时间")
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	}
	for _, layout := range layouts {
		var dt time.Time
		var err error
		if layout == time.RFC3339 {
			dt, err = time.Parse(layout, text)
			if err == nil {
				return dt.In(a.loc), nil
			}
		} else {
			dt, err = time.ParseInLocation(layout, text, a.loc)
			if err == nil {
				return dt, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("时间格式错误，请使用 2006-01-02 15:04")
}

func (a *App) stopScheduleTimer(scheduleID int64) {
	a.schedMu.Lock()
	defer a.schedMu.Unlock()
	timer, ok := a.timers[scheduleID]
	if !ok {
		return
	}
	timer.Stop()
	delete(a.timers, scheduleID)
}
