package admin

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"go-bot/internal/db"
)

func (s *Server) handlePermissions(w http.ResponseWriter, r *http.Request) {
	selectedChatID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("chat_id")), 10, 64)
	if r.Method == http.MethodPost {
		s.handlePermissionsPost(w, r, selectedChatID)
		return
	}
	s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", ""))
}

func (s *Server) handlePermissionsPost(w http.ResponseWriter, r *http.Request, selectedChatID int64) {
	if err := parseLimitedAdminForm(w, r); err != nil {
		s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", "表单解析失败："+err.Error()))
		return
	}
	action := strings.TrimSpace(r.PostFormValue("action"))
	userID, err := parseAdminPositiveUserID(r)
	if err != nil {
		s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", err.Error()))
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))

	switch action {
	case "add_global_owner":
		if err := s.db.AddGlobalOwner(userID, username); err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", "添加全局权限人失败："+err.Error()))
			return
		}
		s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "全局权限人已添加", ""))
	case "remove_global_owner":
		if err := s.db.RemoveGlobalOwner(userID); err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", "删除全局权限人失败："+err.Error()))
			return
		}
		s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "全局权限人已删除", ""))
	case "add_global_operator":
		if err := s.db.AddOperator(0, userID, username, true); err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", "添加全局操作人失败："+err.Error()))
			return
		}
		s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "全局操作人已添加", ""))
	case "remove_global_operator":
		if err := s.db.RemoveOperator(0, userID, true); err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", "删除全局操作人失败："+err.Error()))
			return
		}
		s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "全局操作人已删除", ""))
	case "add_group_operator":
		chatID, err := s.parseAdminPermissionGroupChatID(r)
		if err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", err.Error()))
			return
		}
		if _, err := s.db.GetGroup(chatID); err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(chatID, "", "群不存在"))
			return
		}
		if err := s.db.AddOperator(chatID, userID, username, false); err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(chatID, "", "添加群操作人失败："+err.Error()))
			return
		}
		s.writePage(w, "权限管理", s.renderPermissionsPage(chatID, "群操作人已添加", ""))
	case "remove_group_operator":
		chatID, err := s.parseAdminPermissionGroupChatID(r)
		if err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", err.Error()))
			return
		}
		if _, err := s.db.GetGroup(chatID); err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(chatID, "", "群不存在"))
			return
		}
		if err := s.db.RemoveOperator(chatID, userID, false); err != nil {
			s.writePage(w, "权限管理", s.renderPermissionsPage(chatID, "", "删除群操作人失败："+err.Error()))
			return
		}
		s.writePage(w, "权限管理", s.renderPermissionsPage(chatID, "群操作人已删除", ""))
	default:
		s.writePage(w, "权限管理", s.renderPermissionsPage(selectedChatID, "", "暂不支持该权限操作。"))
	}
}

func (s *Server) renderPermissionsPage(selectedChatID int64, msg, errMsg string) string {
	groups, groupErr := s.db.ListAllGroups()
	globalOwners, ownerErr := s.db.ListGlobalOwners()
	globalOperators, globalOperatorErr := s.db.ListOperators(0, true)
	groupOperators := []db.Operator{}
	var groupOperatorErr error
	if selectedChatID != 0 {
		groupOperators, groupOperatorErr = s.db.ListOperators(selectedChatID, false)
	}

	var b strings.Builder
	if msg != "" {
		b.WriteString(`<div class="msg">` + html.EscapeString(msg) + `</div>`)
	}
	if errMsg != "" {
		b.WriteString(`<div class="err">` + html.EscapeString(errMsg) + `</div>`)
	}
	for _, err := range []error{groupErr, ownerErr, globalOperatorErr, groupOperatorErr} {
		if err != nil {
			b.WriteString(`<div class="err">读取权限数据失败：` + html.EscapeString(err.Error()) + `</div>`)
			break
		}
	}

	b.WriteString(`<h2>权限管理</h2>`)
	b.WriteString(`<form method="get" action="/admin/permissions">`)
	if token := strings.TrimSpace(s.cfg.AdminToken); token != "" {
		b.WriteString(`<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`)
	}
	b.WriteString(`<label>选择群：<select name="chat_id">`)
	b.WriteString(`<option value="">不选择群</option>`)
	for _, group := range groups {
		chatIDText := strconv.FormatInt(group.ChatID, 10)
		selected := ""
		if group.ChatID == selectedChatID {
			selected = ` selected`
		}
		label := strings.TrimSpace(group.Title)
		if label == "" {
			label = chatIDText
		}
		b.WriteString(`<option value="` + html.EscapeString(chatIDText) + `"` + selected + `>` + html.EscapeString(label+" "+chatIDText) + `</option>`)
	}
	b.WriteString(`</select></label> <button type="submit">查看群操作人</button></form>`)

	b.WriteString(`<h2>新增全局权限人</h2>`)
	b.WriteString(adminPermissionForm(s.withToken("/admin/permissions"), "add_global_owner", 0, "添加全局权限人"))
	b.WriteString(`<h2>新增全局操作人</h2>`)
	b.WriteString(adminPermissionForm(s.withToken("/admin/permissions"), "add_global_operator", 0, "添加全局操作人"))
	b.WriteString(`<h2>新增群操作人</h2>`)
	b.WriteString(adminPermissionForm(s.withToken("/admin/permissions"), "add_group_operator", selectedChatID, "添加群操作人"))

	b.WriteString(`<h2>全局权限人</h2>`)
	b.WriteString(s.renderGlobalOwnersTable(globalOwners))
	b.WriteString(`<h2>全局操作人</h2>`)
	b.WriteString(s.renderOperatorsTable(globalOperators, true, 0))
	b.WriteString(`<h2>群操作人</h2>`)
	if selectedChatID == 0 {
		b.WriteString(`<div class="muted">请选择群后查看群操作人。</div>`)
	} else {
		b.WriteString(`<div class="muted">当前群：` + html.EscapeString(strconv.FormatInt(selectedChatID, 10)) + `</div>`)
		b.WriteString(s.renderOperatorsTable(groupOperators, false, selectedChatID))
	}
	return b.String()
}

func adminPermissionForm(actionURL, action string, chatID int64, button string) string {
	var b strings.Builder
	b.WriteString(`<form method="post" action="` + html.EscapeString(actionURL) + `">`)
	b.WriteString(`<input type="hidden" name="action" value="` + html.EscapeString(action) + `">`)
	if chatID != 0 {
		b.WriteString(`<input type="hidden" name="chat_id" value="` + html.EscapeString(strconv.FormatInt(chatID, 10)) + `">`)
	}
	b.WriteString(`用户ID：<input name="user_id" required> 用户名：<input name="username"> `)
	b.WriteString(`<button type="submit">` + html.EscapeString(button) + `</button></form>`)
	return b.String()
}

func (s *Server) renderGlobalOwnersTable(rows []db.GlobalOwner) string {
	var b strings.Builder
	b.WriteString(`<table><thead><tr><th>用户ID</th><th>用户名</th><th>操作</th></tr></thead><tbody>`)
	for _, row := range rows {
		b.WriteString(`<tr>`)
		b.WriteString(`<td>` + strconv.FormatInt(row.UserID, 10) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.Username) + `</td>`)
		b.WriteString(`<td>` + adminPermissionDeleteForm(s.withToken("/admin/permissions"), "remove_global_owner", 0, row.UserID, "删除全局权限人") + `</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func (s *Server) renderOperatorsTable(rows []db.Operator, isGlobal bool, chatID int64) string {
	action := "remove_group_operator"
	button := "删除群操作人"
	actionURL := s.withToken("/admin/permissions")
	if isGlobal {
		action = "remove_global_operator"
		button = "删除全局操作人"
	} else if chatID != 0 {
		actionURL = s.withToken("/admin/permissions?chat_id=" + strconv.FormatInt(chatID, 10))
	}

	var b strings.Builder
	b.WriteString(`<table><thead><tr><th>用户ID</th><th>用户名</th><th>范围</th><th>操作</th></tr></thead><tbody>`)
	for _, row := range rows {
		scope := "群操作人"
		if isGlobal || row.IsGlobal == 1 {
			scope = "全局操作人"
		}
		b.WriteString(`<tr>`)
		b.WriteString(`<td>` + strconv.FormatInt(row.UserID, 10) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(row.Username) + `</td>`)
		b.WriteString(`<td>` + html.EscapeString(scope) + `</td>`)
		b.WriteString(`<td>` + adminPermissionDeleteForm(actionURL, action, chatID, row.UserID, button) + `</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func adminPermissionDeleteForm(actionURL, action string, chatID, userID int64, button string) string {
	var b strings.Builder
	b.WriteString(`<form method="post" action="` + html.EscapeString(actionURL) + `" style="display:inline">`)
	b.WriteString(`<input type="hidden" name="action" value="` + html.EscapeString(action) + `">`)
	if chatID != 0 {
		b.WriteString(`<input type="hidden" name="chat_id" value="` + html.EscapeString(strconv.FormatInt(chatID, 10)) + `">`)
	}
	b.WriteString(`<input type="hidden" name="user_id" value="` + html.EscapeString(strconv.FormatInt(userID, 10)) + `">`)
	b.WriteString(`<button type="submit">` + html.EscapeString(button) + `</button></form>`)
	return b.String()
}

func parseAdminPositiveUserID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(adminPostFormValue(r, "user_id"))
	userID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || userID <= 0 {
		return 0, fmt.Errorf("用户ID格式错误")
	}
	return userID, nil
}

func (s *Server) parseAdminPermissionGroupChatID(r *http.Request) (int64, error) {
	formRaw := strings.TrimSpace(adminPostFormValue(r, "chat_id"))
	queryRaw := strings.TrimSpace(r.URL.Query().Get("chat_id"))

	var chatID int64
	if formRaw != "" {
		formChatID, err := strconv.ParseInt(formRaw, 10, 64)
		if err != nil || formChatID == 0 {
			return 0, fmt.Errorf("群ID格式错误")
		}
		chatID = formChatID
	}
	if queryRaw != "" {
		queryChatID, err := strconv.ParseInt(queryRaw, 10, 64)
		if err != nil || queryChatID == 0 {
			return 0, fmt.Errorf("群ID格式错误")
		}
		if chatID != 0 && chatID != queryChatID {
			return 0, fmt.Errorf("chat_id 不一致")
		}
		chatID = queryChatID
	}
	if chatID == 0 {
		return 0, fmt.Errorf("群ID格式错误")
	}
	return chatID, nil
}
