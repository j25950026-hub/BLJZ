package admin

import (
	"net/http"
	"strconv"
)

type adminRiskRadarItem struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Title      string `json:"title"`
	Reason     string `json:"reason"`
	ChatID     int64  `json:"chatId,omitempty"`
	ActionURL  string `json:"actionUrl,omitempty"`
	Visibility string `json:"visibility"`
}

func (s *Server) handleAdminRiskRadarAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": []adminRiskRadarItem{}}})
		return
	}
	items, err := s.buildAdminRiskRadarItems()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": items}})
}

func (s *Server) buildAdminRiskRadarItems() ([]adminRiskRadarItem, error) {
	owners, err := s.db.ListGlobalOwners()
	if err != nil {
		return nil, err
	}
	groups, err := s.db.ListAllGroups()
	if err != nil {
		return nil, err
	}
	items := make([]adminRiskRadarItem, 0)
	if len(owners) == 0 {
		items = append(items, adminRiskRadarItem{
			Code:       "no_global_owner",
			Severity:   "high",
			Title:      "没有全局权限人",
			Reason:     "后台无法确认全局权限覆盖范围，建议配置至少一名全局权限人。",
			Visibility: "admin_only",
		})
	}
	for _, group := range groups {
		operators, err := s.db.ListOperators(group.ChatID, false)
		if err != nil {
			return nil, err
		}
		if len(operators) > 0 || group.OwnerUserID != 0 {
			continue
		}
		items = append(items, adminRiskRadarItem{
			Code:       "group_missing_operator",
			Severity:   "high",
			Title:      "群缺少操作人",
			Reason:     "该群没有群权限人或群级操作人，自动化异常时无法明确处理责任人。",
			ChatID:     group.ChatID,
			ActionURL:  "/admin/group?chat_id=" + strconv.FormatInt(group.ChatID, 10),
			Visibility: "admin_only",
		})
	}
	if len(owners) == 1 && len(groups) > 5 {
		items = append(items, adminRiskRadarItem{
			Code:       "single_global_owner_broad_scope",
			Severity:   "medium",
			Title:      "单个全局权限人覆盖范围较大",
			Reason:     "当前只有一名全局权限人且覆盖多个群，建议检查是否需要备用人员。",
			Visibility: "admin_only",
		})
	}
	return items, nil
}
