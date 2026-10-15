package admin

import (
	"fmt"
	"html"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"go-bot/internal/db"
	"go-bot/internal/utils"
)

func (s *Server) handleGroupConfig(w http.ResponseWriter, r *http.Request) {
	chatID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("chat_id")), 10, 64)
	if chatID == 0 {
		s.writePage(w, "群配置", `<div class="err">缺少 chat_id 参数。</div>`)
		return
	}
	if r.Method == http.MethodPost {
		s.handleGroupConfigPost(w, r, chatID)
		return
	}
	group, closing, ok := s.loadAdminGroupConfigPageState(w, chatID, false)
	if !ok {
		return
	}
	s.writePage(w, "群配置", s.renderGroupConfigPage(group, closing, "", ""))
}

func (s *Server) handleGroupConfigPost(w http.ResponseWriter, r *http.Request, chatID int64) {
	group, closing, ok := s.loadAdminGroupConfigPageState(w, chatID, true)
	if !ok {
		return
	}
	if err := parseLimitedAdminForm(w, r); err != nil {
		s.writePage(w, "群配置", s.renderGroupConfigPage(group, closing, "", "表单解析失败："+err.Error()))
		return
	}

	action := strings.TrimSpace(r.PostFormValue("action"))
	switch action {
	case "save_group_config":
		fields, err := s.parseAdminGroupConfigPost(r, chatID)
		if err != nil {
			s.writePage(w, "群配置", s.renderGroupConfigPage(group, closing, "", err.Error()))
			return
		}
		if err := s.db.UpdateGroupFields(chatID, fields); err != nil {
			s.writePage(w, "群配置", s.renderGroupConfigPage(group, closing, "", "保存群配置失败："+err.Error()))
			return
		}
		group, _ = s.db.GetGroup(chatID)
		s.writePage(w, "群配置", s.renderGroupConfigPage(group, closing, "群配置已保存", ""))
	case "save_closing_reminder":
		nextClosing, err := s.parseAdminClosingReminderPost(r, chatID)
		if err != nil {
			s.writePage(w, "群配置", s.renderGroupConfigPage(group, closing, "", err.Error()))
			return
		}
		if err := s.db.UpsertClosingReminderConfig(nextClosing); err != nil {
			s.writePage(w, "群配置", s.renderGroupConfigPage(group, closing, "", "保存下课时间失败："+err.Error()))
			return
		}
		closing, _ = s.db.GetClosingReminderConfig(chatID)
		s.writePage(w, "群配置", s.renderGroupConfigPage(group, closing, "下课时间已保存", ""))
	default:
		s.writePage(w, "群配置", s.renderGroupConfigPage(group, closing, "", "暂不支持该配置操作。"))
	}
}

func (s *Server) loadAdminGroupConfigPageState(w http.ResponseWriter, chatID int64, post bool) (db.GroupConfig, db.GroupClosingReminderConfig, bool) {
	group, err := s.db.GetGroup(chatID)
	if err != nil {
		msg := "读取群详情失败：" + err.Error()
		if post {
			msg = "群不存在"
		}
		s.writePage(w, "群配置", `<div class="err">`+html.EscapeString(msg)+`</div>`)
		return db.GroupConfig{}, db.GroupClosingReminderConfig{}, false
	}
	closing, err := s.db.GetClosingReminderConfig(chatID)
	if err != nil {
		s.writePage(w, "群配置", `<div class="err">读取下课提醒失败：`+html.EscapeString(err.Error())+`</div>`)
		return db.GroupConfig{}, db.GroupClosingReminderConfig{}, false
	}
	return group, closing, true
}

func (s *Server) parseAdminGroupConfigPost(r *http.Request, chatID int64) (map[string]any, error) {
	if err := adminValidateFormChatID(r, chatID); err != nil {
		return nil, err
	}

	title, err := adminRequiredString(r, "title", "群名", 128)
	if err != nil {
		return nil, err
	}
	ownerUserID, err := adminRequiredInt64(r, "owner_user_id", "权限人ID")
	if err != nil {
		return nil, err
	}
	feeRate, err := adminRequiredFloat(r, "fee_rate", "入款费率")
	if err != nil {
		return nil, err
	}
	if feeRate < -100 || feeRate > 100 {
		return nil, fmt.Errorf("入款费率必须在 -100 到 100 之间")
	}
	payoutFeeRate, err := adminRequiredFloat(r, "payout_fee_rate", "下发费率")
	if err != nil {
		return nil, err
	}
	if payoutFeeRate < -100 || payoutFeeRate > 100 {
		return nil, fmt.Errorf("下发费率必须在 -100 到 100 之间")
	}
	customRate, err := adminRequiredFloat(r, "custom_rate", "入款汇率")
	if err != nil {
		return nil, err
	}
	if customRate <= 0 {
		return nil, fmt.Errorf("汇率必须大于 0")
	}
	payoutRate, err := adminRequiredFloat(r, "payout_rate", "下发汇率")
	if err != nil {
		return nil, err
	}
	if payoutRate <= 0 {
		return nil, fmt.Errorf("下发汇率必须大于 0")
	}
	rateSource, err := adminRequiredString(r, "rate_source", "汇率来源", 32)
	if err != nil {
		return nil, err
	}
	switch rateSource {
	case "", "okx", "binance":
	default:
		return nil, fmt.Errorf("汇率来源不支持")
	}
	dayCutTime, err := normalizeAdminClock(adminPostFormValue(r, "day_cut_time"), true)
	if err != nil {
		return nil, fmt.Errorf("日切时间格式错误")
	}
	payoutAddress, err := adminRequiredString(r, "payout_address", "地址", 256)
	if err != nil {
		return nil, err
	}
	payoutAddressRemark, err := adminRequiredString(r, "payout_address_remark", "下发地址备注", 256)
	if err != nil {
		return nil, err
	}
	exclusiveAddress, err := adminRequiredString(r, "exclusive_address", "地址", 256)
	if err != nil {
		return nil, err
	}
	exclusiveAddressOwner, err := adminRequiredString(r, "exclusive_address_owner", "专属地址归属", 256)
	if err != nil {
		return nil, err
	}
	exclusiveAddressRemark, err := adminRequiredString(r, "exclusive_address_remark", "专属地址备注", 256)
	if err != nil {
		return nil, err
	}
	groupType, err := adminRequiredString(r, "group_type", "群类型", 128)
	if err != nil {
		return nil, err
	}
	groupTags, err := adminRequiredString(r, "group_tags", "群标签", 256)
	if err != nil {
		return nil, err
	}
	classStartAd, err := adminRequiredString(r, "class_start_ad", "上课广告", 2000)
	if err != nil {
		return nil, err
	}
	classEndAd, err := adminRequiredString(r, "class_end_ad", "下课广告", 2000)
	if err != nil {
		return nil, err
	}
	welcomeText, err := adminRequiredString(r, "welcome_text", "欢迎语", 2000)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"title":                    title,
		"owner_user_id":            ownerUserID,
		"bookkeeping_enabled":      adminFormBool(r, "bookkeeping_enabled"),
		"display_nickname":         adminFormBool(r, "display_nickname"),
		"show_usdt_conversion":     adminFormBool(r, "show_usdt_conversion"),
		"fee_rate":                 feeRate,
		"payout_fee_rate":          payoutFeeRate,
		"custom_rate":              customRate,
		"payout_rate":              payoutRate,
		"use_realtime_rate":        adminFormBool(r, "use_realtime_rate"),
		"rate_source":              rateSource,
		"day_cut_time":             dayCutTime,
		"payout_address":           payoutAddress,
		"payout_address_remark":    payoutAddressRemark,
		"exclusive_address":        exclusiveAddress,
		"exclusive_address_owner":  exclusiveAddressOwner,
		"exclusive_address_remark": exclusiveAddressRemark,
		"group_type":               groupType,
		"group_tags":               groupTags,
		"duplicate_alert_enabled":  adminFormBool(r, "duplicate_alert_enabled"),
		"at_broadcast_enabled":     adminFormBool(r, "at_broadcast_enabled"),
		"auto_reply_enabled":       adminFormBool(r, "auto_reply_enabled"),
		"custom_content_with_time": adminFormBool(r, "custom_content_with_time"),
		"class_start_ad":           classStartAd,
		"class_end_ad":             classEndAd,
		"welcome_enabled":          adminFormBool(r, "welcome_enabled"),
		"welcome_text":             welcomeText,
	}, nil
}

func (s *Server) parseAdminClosingReminderPost(r *http.Request, chatID int64) (db.GroupClosingReminderConfig, error) {
	if err := adminValidateFormChatID(r, chatID); err != nil {
		return db.GroupClosingReminderConfig{}, err
	}
	reminderTime, err := normalizeAdminClock(adminPostFormValue(r, "reminder_time"), false)
	if err != nil {
		return db.GroupClosingReminderConfig{}, fmt.Errorf("下课时间格式错误")
	}
	thresholdU, err := adminRequiredFloat(r, "threshold_u", "下课阈值")
	if err != nil {
		return db.GroupClosingReminderConfig{}, err
	}
	if thresholdU <= 0 {
		return db.GroupClosingReminderConfig{}, fmt.Errorf("下课阈值必须大于 0")
	}
	dmMode, err := adminRequiredString(r, "dm_mode", "私聊通知模式", 64)
	if err != nil {
		return db.GroupClosingReminderConfig{}, err
	}
	switch dmMode {
	case db.ClosingReminderDMModeOff,
		db.ClosingReminderDMModeOwner,
		db.ClosingReminderDMModeOperators,
		db.ClosingReminderDMModeOwnerOperators,
		db.ClosingReminderDMModeCustom,
		db.ClosingReminderDMModeOwnerOperatorsCustom:
	default:
		return db.GroupClosingReminderConfig{}, fmt.Errorf("私聊通知模式不支持")
	}
	updatedBy, err := adminOptionalInt64(r, "updated_by", "更新人ID")
	if err != nil {
		return db.GroupClosingReminderConfig{}, err
	}
	textNormal, err := adminOptionalString(r, "text_normal", "正常文案", 2000)
	if err != nil {
		return db.GroupClosingReminderConfig{}, err
	}
	textPending, err := adminOptionalString(r, "text_pending", "待确认文案", 2000)
	if err != nil {
		return db.GroupClosingReminderConfig{}, err
	}
	textNoData, err := adminOptionalString(r, "text_no_data", "无数据文案", 2000)
	if err != nil {
		return db.GroupClosingReminderConfig{}, err
	}
	textReview, err := adminOptionalString(r, "text_review", "复核文案", 2000)
	if err != nil {
		return db.GroupClosingReminderConfig{}, err
	}
	textFailed, err := adminOptionalString(r, "text_failed", "失败文案", 2000)
	if err != nil {
		return db.GroupClosingReminderConfig{}, err
	}

	return db.GroupClosingReminderConfig{
		ChatID:       chatID,
		Enabled:      adminFormBool(r, "closing_enabled"),
		ReminderTime: reminderTime,
		ThresholdU:   thresholdU,
		DMMode:       dmMode,
		TextNormal:   textNormal,
		TextPending:  textPending,
		TextNoData:   textNoData,
		TextReview:   textReview,
		TextFailed:   textFailed,
		UpdatedBy:    updatedBy,
	}, nil
}

func (s *Server) renderGroupConfigPage(group db.GroupConfig, closing db.GroupClosingReminderConfig, msg string, errMsg string) string {
	chatIDText := strconv.FormatInt(group.ChatID, 10)
	orderURL := s.withToken("/admin/orders?chat_id=" + chatIDText)
	broadcastURL := s.withToken("/admin/broadcast?source_chat_id=" + chatIDText)
	scheduleURL := s.withToken("/admin/schedules?chat_id=" + chatIDText)
	automationURL := s.withToken("/admin/automation?chat_id=" + chatIDText)

	var b strings.Builder
	if msg != "" {
		b.WriteString(`<div class="msg">` + html.EscapeString(msg) + `</div>`)
	}
	if errMsg != "" {
		b.WriteString(`<div class="err">` + html.EscapeString(errMsg) + `</div>`)
	}

	b.WriteString(`<h2>基础信息</h2><table><tbody>`)
	for _, row := range [][2]string{
		{"群ID", chatIDText},
		{"群名", group.Title},
		{"群类型", group.GroupType},
		{"群标签", group.GroupTags},
		{"权限人", strconv.FormatInt(group.OwnerUserID, 10)},
		{"记账状态", map[bool]string{true: "开启", false: "停止"}[group.BookkeepingEnabled == 1]},
		{"日切时间", group.DayCutTime},
		{"汇率来源", group.RateSource},
		{"入款费率", utils.FormatAmount(group.FeeRate) + "%"},
		{"下发费率", utils.FormatAmount(group.PayoutFeeRate) + "%"},
		{"下发地址", group.PayoutAddress},
		{"专属地址", group.ExclusiveAddress},
		{"新成员欢迎", map[bool]string{true: "开启", false: "关闭"}[group.WelcomeEnabled == 1]},
	} {
		b.WriteString(`<tr><th style="width:180px">` + html.EscapeString(row[0]) + `</th><td>` + html.EscapeString(row[1]) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)

	if s.app != nil {
		if stats, err := s.app.AdminGroupStats(group.ChatID, group.DayCutTime); err == nil {
			b.WriteString(`<h2>当日统计</h2><div class="msg">`)
			b.WriteString(`入款：` + strconv.Itoa(stats.DepositCount) + ` 笔 / ` + html.EscapeString(utils.FormatAmount(stats.DepositTotal)))
			b.WriteString(`，下发：` + strconv.Itoa(stats.PayoutCount) + ` 笔 / ` + html.EscapeString(utils.FormatAmount(stats.PayoutTotal)))
			b.WriteString(`，利润：` + html.EscapeString(utils.FormatAmount(stats.DepositTotal-stats.PayoutTotal)))
			b.WriteString(`</div>`)
		}
	}

	b.WriteString(`<p><a href="` + html.EscapeString(orderURL) + `">查看查单列表</a> | <a href="` + html.EscapeString(broadcastURL) + `">进入群发面板</a> | <a href="` + html.EscapeString(scheduleURL) + `">进入定时群发</a> | <a href="` + html.EscapeString(automationURL) + `">进入自动化管理</a></p>`)

	b.WriteString(`<h2>群配置</h2>`)
	b.WriteString(`<form method="post" action="` + html.EscapeString(s.withToken("/admin/group?chat_id="+chatIDText)) + `">`)
	b.WriteString(`<input type="hidden" name="action" value="save_group_config">`)
	b.WriteString(`<input type="hidden" name="chat_id" value="` + html.EscapeString(chatIDText) + `">`)
	b.WriteString(`<table><tbody>`)
	b.WriteString(adminTextInputRow("群名", "title", group.Title, 360))
	b.WriteString(adminTextInputRow("权限人ID", "owner_user_id", strconv.FormatInt(group.OwnerUserID, 10), 220))
	b.WriteString(adminCheckboxRow("开启记账", "bookkeeping_enabled", group.BookkeepingEnabled == 1))
	b.WriteString(adminCheckboxRow("显示昵称", "display_nickname", group.DisplayNickname == 1))
	b.WriteString(adminCheckboxRow("开启换算", "show_usdt_conversion", group.ShowUSDTConversion == 1))
	b.WriteString(adminTextInputRow("入款费率", "fee_rate", utils.FormatAmount(group.FeeRate), 160))
	b.WriteString(adminTextInputRow("入款汇率", "custom_rate", utils.FormatAmount(group.CustomRate), 160))
	b.WriteString(adminTextInputRow("下发费率", "payout_fee_rate", utils.FormatAmount(group.PayoutFeeRate), 160))
	b.WriteString(adminTextInputRow("下发汇率", "payout_rate", utils.FormatAmount(group.PayoutRate), 160))
	b.WriteString(adminCheckboxRow("使用实时汇率", "use_realtime_rate", group.UseRealtimeRate == 1))
	b.WriteString(adminSelectRow("汇率来源", "rate_source", group.RateSource, []string{"", "okx", "binance"}))
	b.WriteString(adminTextInputRow("下发地址", "payout_address", group.PayoutAddress, 520))
	b.WriteString(adminTextInputRow("下发地址备注", "payout_address_remark", group.PayoutAddressRemark, 360))
	b.WriteString(adminTextInputRow("专属地址", "exclusive_address", group.ExclusiveAddress, 520))
	b.WriteString(adminTextInputRow("专属地址归属", "exclusive_address_owner", group.ExclusiveAddressOwner, 260))
	b.WriteString(adminTextInputRow("专属地址备注", "exclusive_address_remark", group.ExclusiveAddressRemark, 360))
	b.WriteString(adminTextInputRow("群类型", "group_type", group.GroupType, 260))
	b.WriteString(adminTextInputRow("群标签", "group_tags", group.GroupTags, 360))
	b.WriteString(adminCheckboxRow("重复预警", "duplicate_alert_enabled", group.DuplicateAlertEnabled == 1))
	b.WriteString(adminCheckboxRow("@群发", "at_broadcast_enabled", group.AtBroadcastEnabled == 1))
	b.WriteString(adminCheckboxRow("自动回复", "auto_reply_enabled", group.AutoReplyEnabled == 1))
	b.WriteString(adminCheckboxRow("自定义内容带时间", "custom_content_with_time", group.CustomContentWithTime == 1))
	b.WriteString(adminCheckboxRow("新成员欢迎", "welcome_enabled", group.WelcomeEnabled == 1))
	b.WriteString(adminTextareaRow("上课广告", "class_start_ad", group.ClassStartAd))
	b.WriteString(adminTextareaRow("下课广告", "class_end_ad", group.ClassEndAd))
	b.WriteString(adminTextareaRow("欢迎语", "welcome_text", group.WelcomeText))
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<h2>账期设置</h2>`)
	b.WriteString(`<p>日切时间：<input name="day_cut_time" value="` + html.EscapeString(group.DayCutTime) + `" placeholder="03:00:00"></p>`)
	b.WriteString(`<p><button type="submit">保存群配置</button></p>`)
	b.WriteString(`</form>`)

	b.WriteString(`<h2>下课提醒</h2>`)
	b.WriteString(`<form method="post" action="` + html.EscapeString(s.withToken("/admin/group?chat_id="+chatIDText)) + `">`)
	b.WriteString(`<input type="hidden" name="action" value="save_closing_reminder">`)
	b.WriteString(`<input type="hidden" name="chat_id" value="` + html.EscapeString(chatIDText) + `">`)
	b.WriteString(`<table><tbody>`)
	b.WriteString(adminCheckboxRow("启用下课提醒", "closing_enabled", closing.Enabled == 1))
	b.WriteString(adminTextInputRow("下课时间", "reminder_time", closing.ReminderTime, 160))
	b.WriteString(adminTextInputRow("异常阈值 U", "threshold_u", utils.FormatAmount(closing.ThresholdU), 160))
	b.WriteString(adminSelectRow("私聊通知模式", "dm_mode", closing.DMMode, []string{
		db.ClosingReminderDMModeOff,
		db.ClosingReminderDMModeOwner,
		db.ClosingReminderDMModeOperators,
		db.ClosingReminderDMModeOwnerOperators,
		db.ClosingReminderDMModeCustom,
		db.ClosingReminderDMModeOwnerOperatorsCustom,
	}))
	b.WriteString(adminTextareaRow("正常文案", "text_normal", closing.TextNormal))
	b.WriteString(adminTextareaRow("待确认文案", "text_pending", closing.TextPending))
	b.WriteString(adminTextareaRow("无数据文案", "text_no_data", closing.TextNoData))
	b.WriteString(adminTextareaRow("复核文案", "text_review", closing.TextReview))
	b.WriteString(adminTextareaRow("失败文案", "text_failed", closing.TextFailed))
	b.WriteString(adminTextInputRow("更新人ID", "updated_by", strconv.FormatInt(closing.UpdatedBy, 10), 220))
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<p><button type="submit">保存下课时间</button></p>`)
	b.WriteString(`</form>`)

	return b.String()
}

func adminTextInputRow(label, name, value string, width int) string {
	return `<tr><th>` + html.EscapeString(label) + `</th><td><input name="` + html.EscapeString(name) + `" value="` + html.EscapeString(value) + `" style="width:` + strconv.Itoa(width) + `px"></td></tr>`
}

func adminCheckboxRow(label, name string, checked bool) string {
	checkedAttr := ""
	if checked {
		checkedAttr = ` checked`
	}
	return `<tr><th>` + html.EscapeString(label) + `</th><td><label><input type="checkbox" name="` + html.EscapeString(name) + `" value="1"` + checkedAttr + `> 开启</label></td></tr>`
}

func adminTextareaRow(label, name, value string) string {
	return `<tr><th>` + html.EscapeString(label) + `</th><td><textarea name="` + html.EscapeString(name) + `">` + html.EscapeString(value) + `</textarea></td></tr>`
}

func adminSelectRow(label, name, selected string, values []string) string {
	var b strings.Builder
	b.WriteString(`<tr><th>` + html.EscapeString(label) + `</th><td><select name="` + html.EscapeString(name) + `">`)
	for _, value := range values {
		attrs := ""
		if value == selected {
			attrs = ` selected`
		}
		labelText := value
		if labelText == "" {
			labelText = "默认"
		}
		b.WriteString(`<option value="` + html.EscapeString(value) + `"` + attrs + `>` + html.EscapeString(labelText) + `</option>`)
	}
	b.WriteString(`</select></td></tr>`)
	return b.String()
}

func adminValidateFormChatID(r *http.Request, queryChatID int64) error {
	formValue := strings.TrimSpace(adminPostFormValue(r, "chat_id"))
	if formValue == "" {
		return nil
	}
	formChatID, err := strconv.ParseInt(formValue, 10, 64)
	if err != nil || formChatID != queryChatID {
		return fmt.Errorf("chat_id 不一致")
	}
	return nil
}

func adminFormBool(r *http.Request, name string) int {
	if strings.TrimSpace(adminPostFormValue(r, name)) == "1" {
		return 1
	}
	return 0
}

func adminRequiredString(r *http.Request, name, label string, maxLen int) (string, error) {
	if r == nil || r.PostForm == nil {
		return "", fmt.Errorf("%s不能为空", label)
	}
	if _, ok := r.PostForm[name]; !ok {
		return "", fmt.Errorf("%s不能为空", label)
	}
	value := strings.TrimSpace(r.PostFormValue(name))
	if maxLen > 0 && utf8.RuneCountInString(value) > maxLen {
		return "", fmt.Errorf("%s长度不能超过 %d", label, maxLen)
	}
	return value, nil
}

func adminRequiredFloat(r *http.Request, name, label string) (float64, error) {
	if r == nil || r.PostForm == nil {
		return 0, fmt.Errorf("%s不能为空", label)
	}
	if _, ok := r.PostForm[name]; !ok {
		return 0, fmt.Errorf("%s不能为空", label)
	}
	raw := strings.TrimSpace(r.PostFormValue(name))
	if raw == "" {
		return 0, fmt.Errorf("%s不能为空", label)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%s格式错误", label)
	}
	return value, nil
}

func adminRequiredInt64(r *http.Request, name, label string) (int64, error) {
	if r == nil || r.PostForm == nil {
		return 0, fmt.Errorf("%s格式错误", label)
	}
	if _, ok := r.PostForm[name]; !ok {
		return 0, fmt.Errorf("%s格式错误", label)
	}
	raw := strings.TrimSpace(r.PostFormValue(name))
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s格式错误", label)
	}
	return value, nil
}

func adminOptionalString(r *http.Request, name, label string, maxLen int) (string, error) {
	value := strings.TrimSpace(adminPostFormValue(r, name))
	if maxLen > 0 && utf8.RuneCountInString(value) > maxLen {
		return "", fmt.Errorf("%s长度不能超过 %d", label, maxLen)
	}
	return value, nil
}

func adminOptionalInt64(r *http.Request, name, label string) (int64, error) {
	raw := strings.TrimSpace(adminPostFormValue(r, name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s格式错误", label)
	}
	return value, nil
}

func adminPostFormValue(r *http.Request, name string) string {
	if r == nil || r.PostForm == nil {
		return ""
	}
	return r.PostFormValue(name)
}

func normalizeAdminClock(raw string, allowSeconds bool) (string, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 && !(allowSeconds && len(parts) == 3) {
		return "", fmt.Errorf("invalid clock")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", err
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", err
	}
	second := 0
	if len(parts) == 3 {
		second, err = strconv.Atoi(parts[2])
		if err != nil {
			return "", err
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return "", fmt.Errorf("invalid clock range")
	}
	if allowSeconds {
		return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second), nil
	}
	return fmt.Sprintf("%02d:%02d", hour, minute), nil
}
