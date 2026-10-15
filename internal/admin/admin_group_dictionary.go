package admin

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"go-bot/internal/db"
	"go-bot/internal/utils"
)

type adminGroupDictionaryRequest struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sortOrder,omitempty"`
}

type adminGroupDictionaryItem struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int    `json:"sortOrder"`
	GroupCount  int    `json:"groupCount"`
	InCatalog   bool   `json:"inCatalog"`
}

type adminGroupDictionaryData struct {
	Types []adminGroupDictionaryItem `json:"types"`
	Tags  []adminGroupDictionaryItem `json:"tags"`
}

func (s *Server) handleAdminGroupDictionariesAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAdminGroupDictionariesList(w, r)
	case http.MethodPost:
		s.handleAdminGroupDictionarySave(w, r)
	case http.MethodDelete:
		s.handleAdminGroupDictionaryDelete(w, r)
	default:
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"ok":    false,
			"error": "method not allowed",
		})
	}
}

func (s *Server) handleAdminGroupDictionariesList(w http.ResponseWriter, _ *http.Request) {
	data := adminGroupDictionaryData{
		Types: []adminGroupDictionaryItem{},
		Tags:  []adminGroupDictionaryItem{},
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
		return
	}
	typeCatalog, err := s.db.ListAdminGroupDictionaryItems("group_type")
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	tagCatalog, err := s.db.ListAdminGroupDictionaryItems("group_tag")
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	typeUsage, tagUsage, err := s.adminGroupDictionaryUsage()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	data.Types = mergeAdminGroupDictionaryItems("group_type", typeCatalog, typeUsage)
	data.Tags = mergeAdminGroupDictionaryItems("group_tag", tagCatalog, tagUsage)
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func (s *Server) handleAdminGroupDictionarySave(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	req, err := decodeAdminGroupDictionaryRequest(w, r)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := s.db.SaveAdminGroupDictionaryItem(req.Kind, req.Name, req.Description, req.SortOrder); err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": adminGroupDictionaryItem{
			Kind:        req.Kind,
			Name:        req.Name,
			Description: req.Description,
			SortOrder:   req.SortOrder,
			InCatalog:   true,
		},
	})
}

func (s *Server) handleAdminGroupDictionaryDelete(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database is not configured"})
		return
	}
	req, err := decodeAdminGroupDictionaryRequest(w, r)
	if err != nil {
		s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := s.db.DeleteAdminGroupDictionaryItem(req.Kind, req.Name); err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func decodeAdminGroupDictionaryRequest(w http.ResponseWriter, r *http.Request) (adminGroupDictionaryRequest, error) {
	var req adminGroupDictionaryRequest
	if err := decodeLimitedAdminJSON(w, r, &req); err != nil {
		return req, fmt.Errorf("invalid json: %w", err)
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if req.Kind != "group_type" && req.Kind != "group_tag" {
		return req, fmt.Errorf("不支持的字典类型")
	}
	if req.Name == "" || len([]rune(req.Name)) > 64 {
		return req, fmt.Errorf("字典名称不能为空且不能超过 64 个字符")
	}
	if len([]rune(req.Description)) > 200 {
		return req, fmt.Errorf("字典说明不能超过 200 个字符")
	}
	return req, nil
}

func (s *Server) adminGroupDictionaryUsage() (map[string]int, map[string]int, error) {
	typeUsage := map[string]int{}
	tagUsage := map[string]int{}
	rows, err := s.db.ListAllGroups()
	if err != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		groupType := strings.TrimSpace(row.GroupType)
		if groupType != "" {
			typeUsage[groupType]++
		}
		for _, tag := range utils.SplitTags(row.GroupTags) {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tagUsage[tag]++
			}
		}
	}
	return typeUsage, tagUsage, nil
}

func mergeAdminGroupDictionaryItems(kind string, catalog []db.AdminGroupDictionaryItem, usage map[string]int) []adminGroupDictionaryItem {
	out := make([]adminGroupDictionaryItem, 0, len(catalog)+len(usage))
	seen := map[string]struct{}{}
	for _, item := range catalog {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, adminGroupDictionaryItem{
			Kind:        kind,
			Name:        name,
			Description: strings.TrimSpace(item.Description),
			SortOrder:   item.SortOrder,
			GroupCount:  usage[name],
			InCatalog:   true,
		})
	}
	for name, count := range usage {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		out = append(out, adminGroupDictionaryItem{
			Kind:       kind,
			Name:       name,
			GroupCount: count,
			InCatalog:  false,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].InCatalog != out[j].InCatalog {
			return out[i].InCatalog
		}
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].Name < out[j].Name
	})
	return out
}
