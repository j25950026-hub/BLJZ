package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type adminPermissionGroup struct {
	ChatID int64  `json:"chatId"`
	Title  string `json:"title"`
}

type adminPermissionActor struct {
	UserID   int64  `json:"userId"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Scope    string `json:"scope"`
	ChatID   int64  `json:"chatId,omitempty"`
	Group    string `json:"group,omitempty"`
}

type adminPermissionsData struct {
	Groups          []adminPermissionGroup `json:"groups"`
	GlobalOwners    []adminPermissionActor `json:"globalOwners"`
	GlobalOperators []adminPermissionActor `json:"globalOperators"`
	GroupOperators  []adminPermissionActor `json:"groupOperators"`
	SelectedChatID  int64                  `json:"selectedChatId,omitempty"`
}

type adminPermissionWriteRequest struct {
	Action   string `json:"action"`
	UserID   int64  `json:"userId"`
	Username string `json:"username"`
}

func (s *Server) handleAdminPermissionsAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAdminPermissionsList(w, r)
	case http.MethodPost:
		s.handleAdminPermissionsWrite(w, r)
	default:
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"ok":    false,
			"error": "method not allowed",
		})
	}
}

func (s *Server) handleAdminPermissionsList(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"data": adminPermissionsData{
				Groups:          []adminPermissionGroup{},
				GlobalOwners:    []adminPermissionActor{},
				GlobalOperators: []adminPermissionActor{},
				GroupOperators:  []adminPermissionActor{},
			},
		})
		return
	}
	data, err := s.buildAdminPermissionsData(r)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func (s *Server) handleAdminPermissionsWrite(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	request, err := decodeAdminPermissionWriteRequest(w, r)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	switch request.Action {
	case "upsert_global_owner":
		if err := s.db.AddGlobalOwner(request.UserID, request.Username); err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	case "remove_global_owner":
		if err := s.db.RemoveGlobalOwner(request.UserID); err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	case "upsert_global_operator":
		if err := s.db.AddOperator(0, request.UserID, request.Username, true); err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	case "remove_global_operator":
		if err := s.db.RemoveOperator(0, request.UserID, true); err != nil {
			s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	default:
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "不支持的权限操作"})
		return
	}

	data, err := s.buildAdminPermissionsData(r)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func decodeAdminPermissionWriteRequest(w http.ResponseWriter, r *http.Request) (adminPermissionWriteRequest, error) {
	var request adminPermissionWriteRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		return request, fmt.Errorf("invalid json: %w", err)
	}
	request.Action = strings.TrimSpace(request.Action)
	request.Username = strings.TrimSpace(request.Username)
	if request.UserID <= 0 {
		return request, fmt.Errorf("userId 必须大于 0")
	}
	return request, nil
}

func (s *Server) buildAdminPermissionsData(r *http.Request) (adminPermissionsData, error) {
	data := adminPermissionsData{
		Groups:          []adminPermissionGroup{},
		GlobalOwners:    []adminPermissionActor{},
		GlobalOperators: []adminPermissionActor{},
		GroupOperators:  []adminPermissionActor{},
	}
	groups, err := s.db.ListAllGroups()
	if err != nil {
		return data, err
	}
	groupNames := map[int64]string{}
	for _, group := range groups {
		title := strings.TrimSpace(group.Title)
		if title == "" {
			title = strconv.FormatInt(group.ChatID, 10)
		}
		groupNames[group.ChatID] = title
		data.Groups = append(data.Groups, adminPermissionGroup{
			ChatID: group.ChatID,
			Title:  title,
		})
	}

	owners, err := s.db.ListGlobalOwners()
	if err != nil {
		return data, err
	}
	for _, owner := range owners {
		data.GlobalOwners = append(data.GlobalOwners, adminPermissionActor{
			UserID:   owner.UserID,
			Username: strings.TrimSpace(owner.Username),
			Role:     "global_owner",
			Scope:    "all_groups",
		})
	}

	globalOperators, err := s.db.ListOperators(0, true)
	if err != nil {
		return data, err
	}
	for _, operator := range globalOperators {
		data.GlobalOperators = append(data.GlobalOperators, adminPermissionActor{
			UserID:   operator.UserID,
			Username: strings.TrimSpace(operator.Username),
			Role:     "global_operator",
			Scope:    "all_groups",
		})
	}

	chatID, err := adminPermissionsSelectedChatID(r)
	if err != nil {
		return data, err
	}
	data.SelectedChatID = chatID
	if chatID == 0 {
		return data, nil
	}
	if _, ok := groupNames[chatID]; !ok {
		return data, fmt.Errorf("群不存在")
	}
	groupOperators, err := s.db.ListOperators(chatID, false)
	if err != nil {
		return data, err
	}
	for _, operator := range groupOperators {
		data.GroupOperators = append(data.GroupOperators, adminPermissionActor{
			UserID:   operator.UserID,
			Username: strings.TrimSpace(operator.Username),
			Role:     "group_operator",
			Scope:    "single_group",
			ChatID:   chatID,
			Group:    groupNames[chatID],
		})
	}
	return data, nil
}

func adminPermissionsSelectedChatID(r *http.Request) (int64, error) {
	if r == nil || r.URL == nil {
		return 0, nil
	}
	raw := strings.TrimSpace(r.URL.Query().Get("chat_id"))
	if raw == "" {
		return 0, nil
	}
	chatID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || chatID == 0 {
		return 0, fmt.Errorf("群ID格式错误")
	}
	return chatID, nil
}
