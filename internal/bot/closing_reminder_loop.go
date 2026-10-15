package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

const closingReminderDateLayout = "2006-01-02"

type closingReminderDMRecipient struct {
	UserID   int64
	Username string
}

func (a *App) runClosingReminderLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := a.runClosingReminderScanAt(ctx, a.currentTimeInLocation()); err != nil && ctx.Err() == nil {
			log.Printf("下课提醒自动扫描失败: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) runClosingReminderScanAt(ctx context.Context, now time.Time) error {
	if a == nil || a.db == nil {
		return nil
	}
	configs, err := a.db.ListEnabledClosingReminderConfigs()
	if err != nil {
		return err
	}
	loc := a.loc
	if loc == nil {
		loc = time.UTC
	}
	var scanErrs []error
	for _, config := range configs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		group, err := a.db.GetGroup(config.ChatID)
		if err != nil {
			scanErrs = append(scanErrs, fmt.Errorf("读取下课提醒群配置失败(chat=%d): %w", config.ChatID, err))
			continue
		}
		if active, _ := a.checkGroupEntitlement(group); !active {
			continue
		}
		if !closingReminderShouldRunAt(config.ReminderTime, now.In(loc), group.DayCutTime) {
			continue
		}
		if err := a.sendClosingReminderAt(ctx, config.ChatID, false, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			scanErrs = append(scanErrs, fmt.Errorf("发送下课提醒失败(chat=%d): %w", config.ChatID, err))
			continue
		}
	}
	return errors.Join(scanErrs...)
}

func (a *App) triggerClosingReminderNow(msg *tgbotapi.Message, g db.GroupConfig) error {
	if msg == nil || msg.Chat == nil {
		return nil
	}
	return a.sendClosingReminderAt(context.Background(), msg.Chat.ID, true, a.currentTimeInLocation())
}

func (a *App) sendClosingReminderAt(ctx context.Context, chatID int64, manual bool, now time.Time) error {
	if a == nil || a.db == nil {
		return nil
	}
	loc := a.loc
	if loc == nil {
		loc = time.UTC
	}
	group, err := a.db.GetGroup(chatID)
	if err != nil {
		return err
	}
	config, err := a.db.GetClosingReminderConfig(chatID)
	if err != nil {
		return err
	}
	businessDate, periodStart, periodEnd := resolveClosingReminderWindow(now, loc, group.DayCutTime)
	summary, err := a.db.BuildClosingReminderSummary(chatID, businessDate, periodStart, periodEnd, config.ThresholdU)
	if err != nil {
		summary = db.ClosingReminderSummary{
			ChatID:       chatID,
			BusinessDate: businessDate,
			PeriodStart:  periodStart.UTC().Format("2006-01-02T15:04:05"),
			PeriodEnd:    periodEnd.UTC().Format("2006-01-02T15:04:05"),
			Status:       db.ClosingReminderStatusFailed,
			GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		}
	}
	if !manual && err == nil && summary.DepositCount == 0 && summary.PayoutCount == 0 {
		return nil
	}
	if !manual {
		inserted, markErr := a.db.MarkClosingReminderRun(chatID, businessDate, summary.Status, false)
		if markErr != nil {
			return markErr
		}
		if !inserted {
			return nil
		}
	}
	if err := a.notifyClosingReminder(ctx, group, config, summary, manual); err != nil {
		var groupSendErr *closingReminderGroupSendError
		if !manual && errors.As(err, &groupSendErr) {
			if rollbackErr := a.db.DeleteClosingReminderRun(chatID, businessDate); rollbackErr != nil {
				return errors.Join(err, rollbackErr)
			}
		}
		return err
	}
	return nil
}

type closingReminderGroupSendError struct {
	err error
}

func (e *closingReminderGroupSendError) Error() string {
	if e == nil || e.err == nil {
		return "发送下课提醒群消息失败"
	}
	return "发送下课提醒群消息失败: " + e.err.Error()
}

func (e *closingReminderGroupSendError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (a *App) notifyClosingReminder(ctx context.Context, g db.GroupConfig, config db.GroupClosingReminderConfig, summary db.ClosingReminderSummary, manual bool) error {
	if a == nil {
		return nil
	}
	groupText := closingReminderGroupTextForStatus(config, summary.Status)
	groupMsg := tgbotapi.NewMessage(summary.ChatID, groupText)
	if err := a.sendWithPriority(outboundPriorityNormalChat, groupMsg); err != nil {
		return &closingReminderGroupSendError{err: err}
	}
	if !closingReminderStatusShouldDM(summary.Status, config.DMMode) {
		return nil
	}
	recipients, err := a.resolveClosingReminderDMRecipients(g, config)
	if err != nil {
		return err
	}
	loc := a.loc
	if loc == nil {
		loc = time.UTC
	}
	dmText := renderClosingReminderDMText(g, summary, loc)
	for _, recipient := range recipients {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msg := tgbotapi.NewMessage(recipient.UserID, dmText)
		if err := a.sendWithPriority(outboundPriorityErrorAlert, msg); err != nil {
			log.Printf("发送下课提醒私聊失败(chat=%d user=%d): %v", summary.ChatID, recipient.UserID, err)
		}
	}
	return nil
}

func (a *App) resolveClosingReminderDMRecipients(g db.GroupConfig, config db.GroupClosingReminderConfig) ([]closingReminderDMRecipient, error) {
	if a == nil || a.db == nil {
		return nil, nil
	}
	mode := strings.TrimSpace(config.DMMode)
	if mode == "" {
		mode = db.ClosingReminderDMModeOwnerOperatorsCustom
	}
	seen := map[int64]struct{}{}
	out := []closingReminderDMRecipient{}
	appendRecipient := func(userID int64, username string) {
		if userID == 0 {
			return
		}
		if _, ok := seen[userID]; ok {
			return
		}
		seen[userID] = struct{}{}
		out = append(out, closingReminderDMRecipient{UserID: userID, Username: normalizeTelegramUsername(username)})
	}

	if mode == db.ClosingReminderDMModeOwner ||
		mode == db.ClosingReminderDMModeOwnerOperators ||
		mode == db.ClosingReminderDMModeOwnerOperatorsCustom {
		appendRecipient(g.OwnerUserID, "")
	}
	if mode == db.ClosingReminderDMModeOperators ||
		mode == db.ClosingReminderDMModeOwnerOperators ||
		mode == db.ClosingReminderDMModeOwnerOperatorsCustom {
		operators, err := a.db.ListOperators(g.ChatID, false)
		if err != nil {
			return nil, err
		}
		for _, operator := range operators {
			appendRecipient(operator.UserID, operator.Username)
		}
	}
	if mode == db.ClosingReminderDMModeCustom ||
		mode == db.ClosingReminderDMModeOwnerOperatorsCustom {
		recipients, err := a.db.ListClosingDMRecipients(g.ChatID)
		if err != nil {
			return nil, err
		}
		for _, recipient := range recipients {
			appendRecipient(recipient.UserID, recipient.Username)
		}
	}
	return out, nil
}

func renderClosingReminderDMText(g db.GroupConfig, summary db.ClosingReminderSummary, loc *time.Location) string {
	groupTitle := strings.TrimSpace(g.Title)
	if groupTitle == "" {
		groupTitle = fmt.Sprintf("%d", g.ChatID)
	}
	return strings.Join([]string{
		"收班明细",
		"群：" + groupTitle,
		fmt.Sprintf("账期：%s - %s", formatClosingReminderPeriod(summary.PeriodStart, loc), formatClosingReminderPeriod(summary.PeriodEnd, loc)),
		"状态：" + closingReminderStatusText(summary.Status),
		"",
		fmt.Sprintf("入款：%d 笔 / %s U", summary.DepositCount, formatClosingReminderAmount(summary.DepositTotal)),
		fmt.Sprintf("下发：%d 笔 / %s U", summary.PayoutCount, formatClosingReminderAmount(summary.PayoutTotal)),
		fmt.Sprintf("待确认：%s U", formatClosingReminderAmount(summary.PendingTotal)),
		"",
		"处理建议：" + closingReminderSuggestion(summary.Status),
	}, "\n")
}

func closingReminderShouldRunAt(reminderTime string, now time.Time, dayCutTime string) bool {
	h, m, ok := parseClosingReminderHourMinute(reminderTime)
	if !ok {
		h, m = 4, 0
	}
	cutH, cutM, cutS := parseClosingReminderCutTime(dayCutTime)
	targetH, targetM, targetS := h, m, 0
	if closingReminderClockBefore(h, m, 0, cutH, cutM, cutS) {
		targetH, targetM, targetS = cutH, cutM, cutS
	}
	target := time.Date(now.Year(), now.Month(), now.Day(), targetH, targetM, targetS, 0, now.Location())
	return !now.Before(target)
}

func closingReminderClockBefore(h, m, s, otherH, otherM, otherS int) bool {
	if h != otherH {
		return h < otherH
	}
	if m != otherM {
		return m < otherM
	}
	return s < otherS
}

func resolveClosingReminderWindow(now time.Time, loc *time.Location, dayCutTime string) (string, time.Time, time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	h, m, s := parseClosingReminderCutTime(dayCutTime)
	localNow := now.In(loc)
	cutToday := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), h, m, s, 0, loc)
	periodEndLocal := cutToday
	if localNow.Before(cutToday) {
		periodEndLocal = cutToday.AddDate(0, 0, -1)
	}
	periodStartLocal := periodEndLocal.AddDate(0, 0, -1)
	return periodStartLocal.Format(closingReminderDateLayout), periodStartLocal.UTC(), periodEndLocal.UTC()
}

func parseClosingReminderCutTime(raw string) (int, int, int) {
	trimmed := strings.TrimSpace(raw)
	for _, layout := range []string{"15:04:05", "15:04"} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed.Hour(), parsed.Minute(), parsed.Second()
		}
	}
	return 4, 0, 0
}

func parseClosingReminderHourMinute(raw string) (int, int, bool) {
	trimmed := strings.TrimSpace(raw)
	for _, layout := range []string{"15:04", "15:04:05"} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed.Hour(), parsed.Minute(), true
		}
	}
	return 0, 0, false
}

func closingReminderStatusShouldDM(status string, dmMode string) bool {
	if strings.TrimSpace(dmMode) == db.ClosingReminderDMModeOff {
		return false
	}
	switch status {
	case db.ClosingReminderStatusPending, db.ClosingReminderStatusReview, db.ClosingReminderStatusFailed:
		return true
	default:
		return false
	}
}

func closingReminderStatusText(status string) string {
	switch status {
	case db.ClosingReminderStatusNormal:
		return "正常"
	case db.ClosingReminderStatusPending:
		return "待确认"
	case db.ClosingReminderStatusNoData:
		return "无账单"
	case db.ClosingReminderStatusReview:
		return "待复核"
	case db.ClosingReminderStatusFailed:
		return "稍后确认"
	default:
		return "稍后确认"
	}
}

func closingReminderSuggestion(status string) string {
	switch status {
	case db.ClosingReminderStatusPending:
		return "请确认剩余收尾事项后再结束当日账务。"
	case db.ClosingReminderStatusReview:
		return "请复核收支数据，确认账单口径后再归档。"
	case db.ClosingReminderStatusFailed:
		return "请稍后重新触发或联系技术人员检查。"
	default:
		return "无需处理。"
	}
}

func formatClosingReminderPeriod(raw string, loc *time.Location) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range []string{"2006-01-02T15:04:05", time.RFC3339, "2006-01-02 15:04:05"} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed.In(loc).Format("2006-01-02 15:04")
		}
	}
	return trimmed
}
