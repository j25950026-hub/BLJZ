package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"go-bot/internal/bot"
	"go-bot/internal/db"
)

type adminTemplateLibraryRequest struct {
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Content     json.RawMessage `json:"content"`
}

type adminTemplateLibraryPreviewRequest struct {
	TemplateID int64              `json:"templateId"`
	ChatID     int64              `json:"chatId"`
	Sample     adminWelcomeSample `json:"sample"`
}

type adminTemplateLibraryResponse struct {
	ID            int64           `json:"id"`
	Kind          string          `json:"kind"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Version       int             `json:"version"`
	Content       json.RawMessage `json:"content"`
	OwnerUsername string          `json:"ownerUsername"`
	CreatedAt     string          `json:"createdAt"`
}

func (s *Server) handleAdminTemplateLibraryAPI(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		kind := strings.TrimSpace(r.URL.Query().Get("kind"))
		rows, err := s.db.ListAdminTemplateLibrary(kind)
		if err != nil {
			s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		items := make([]adminTemplateLibraryResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, adminTemplateLibraryResponseFromDB(row))
		}
		s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": items}})
	case http.MethodPost:
		var request adminTemplateLibraryRequest
		if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
			s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
			return
		}
		if len(request.Content) == 0 || !json.Valid(request.Content) {
			s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "模板内容必须是合法 JSON"})
			return
		}
		row, err := s.db.SaveAdminTemplateLibrary(db.AdminTemplateLibrary{
			Kind:          request.Kind,
			Name:          request.Name,
			Description:   request.Description,
			ContentJSON:   string(request.Content),
			OwnerUsername: s.adminSessionUsername(r),
		})
		if err != nil {
			s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": adminTemplateLibraryResponseFromDB(row)})
	default:
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
	}
}

func (s *Server) handleAdminTemplateLibraryPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	var request adminTemplateLibraryPreviewRequest
	if err := decodeLimitedAdminJSON(w, r, &request); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	if request.TemplateID == 0 || request.ChatID == 0 {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "templateId 和 chatId 不能为空"})
		return
	}
	template, err := s.db.GetAdminTemplateLibrary(request.TemplateID)
	if err != nil {
		s.writeAdminJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "模板版本不存在"})
		return
	}
	group, err := s.db.GetGroup(request.ChatID)
	if err != nil {
		s.writeAdminJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "群不存在"})
		return
	}
	var content struct {
		Enabled  *bool  `json:"enabled"`
		Template string `json:"template"`
		Keyword  string `json:"keyword"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal([]byte(template.ContentJSON), &content); err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "模板内容格式错误"})
		return
	}
	data := map[string]any{"template": adminTemplateLibraryResponseFromDB(template)}
	switch template.Kind {
	case "welcome":
		if strings.TrimSpace(content.Template) == "" {
			s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "欢迎语模板不能为空"})
			return
		}
		data["preview"] = renderAdminWelcomePreview(group.Title, content.Template, request.Sample)
	case "auto_reply":
		preview, err := bot.RenderAdminMessageTemplatePreview(content.Text, bot.AdminMessageTemplateSample{ChatID: request.ChatID, UserID: request.Sample.UserID, Nickname: request.Sample.FirstName})
		if err != nil {
			s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("自动回复模板格式错误: %v", err)})
			return
		}
		data["preview"] = adminTemplateRenderedPreview{Text: preview.Text, ParseMode: preview.ParseMode}
	default:
		data["preview"] = content
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func adminTemplateLibraryResponseFromDB(row db.AdminTemplateLibrary) adminTemplateLibraryResponse {
	content := json.RawMessage(row.ContentJSON)
	if len(content) == 0 {
		content = json.RawMessage(`{}`)
	}
	return adminTemplateLibraryResponse{
		ID:            row.ID,
		Kind:          row.Kind,
		Name:          row.Name,
		Description:   row.Description,
		Version:       row.Version,
		Content:       content,
		OwnerUsername: row.OwnerUsername,
		CreatedAt:     row.CreatedAt,
	}
}

func parseAdminTemplateLibraryLimit(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 50
	}
	if value > 200 {
		return 200
	}
	return value
}
