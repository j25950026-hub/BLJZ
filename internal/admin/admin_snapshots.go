package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"go-bot/internal/db"
)

type adminSnapshotItemResponse struct {
	ID            int64  `json:"id"`
	SnapshotID    int64  `json:"snapshotId"`
	ChatID        int64  `json:"chatId"`
	BeforeJSON    string `json:"beforeJson"`
	AfterJSON     string `json:"afterJson"`
	Status        string `json:"status"`
	FailureReason string `json:"failureReason,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

type adminSnapshotResponse struct {
	ID            int64                       `json:"id"`
	OwnerUsername string                      `json:"ownerUsername"`
	Action        string                      `json:"action"`
	SummaryJSON   string                      `json:"summaryJson"`
	Status        string                      `json:"status"`
	CreatedAt     string                      `json:"createdAt"`
	Items         []adminSnapshotItemResponse `json:"items"`
}

func (s *Server) handleAdminSnapshotsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": []adminSnapshotResponse{}}})
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			s.writeAdminJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "limit 格式错误"})
			return
		}
		limit = parsed
	}
	rows, err := s.db.ListAdminChangeSnapshots(s.adminSessionUsername(r), limit)
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	items := make([]adminSnapshotResponse, 0, len(rows))
	for _, row := range rows {
		item := adminSnapshotResponse{
			ID:            row.ID,
			OwnerUsername: row.OwnerUsername,
			Action:        row.Action,
			SummaryJSON:   row.SummaryJSON,
			Status:        row.Status,
			CreatedAt:     row.CreatedAt,
			Items:         make([]adminSnapshotItemResponse, 0, len(row.Items)),
		}
		for _, detail := range row.Items {
			item.Items = append(item.Items, adminSnapshotItemResponse{
				ID:            detail.ID,
				SnapshotID:    detail.SnapshotID,
				ChatID:        detail.ChatID,
				BeforeJSON:    detail.BeforeJSON,
				AfterJSON:     detail.AfterJSON,
				Status:        detail.Status,
				FailureReason: detail.FailureReason,
				CreatedAt:     detail.CreatedAt,
			})
		}
		items = append(items, item)
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"items": items}})
}

func (s *Server) recordAdminSwitchSnapshot(plan adminSwitchPlan, receipt adminSwitchReceipt, action string) {
	if s == nil || s.db == nil || len(plan.Items) == 0 {
		return
	}
	items := make([]db.AdminChangeSnapshotItem, 0, len(plan.Items))
	for _, planned := range plan.Items {
		status := "failed"
		failureReason := ""
		for _, updated := range receipt.Updated {
			if updated.ChatID == planned.ChatID {
				status = "updated"
				break
			}
		}
		if status != "updated" {
			for _, skipped := range receipt.Skipped {
				if skipped.ChatID == planned.ChatID {
					status = "skipped"
					failureReason = skipped.Reason
					break
				}
			}
		}
		if status == "failed" {
			for _, failed := range receipt.Failed {
				if failed.ChatID == planned.ChatID {
					failureReason = failed.Reason
					break
				}
			}
		}
		beforeJSON, _ := json.Marshal(map[string]string{"value": planned.Before})
		afterJSON, _ := json.Marshal(map[string]string{"value": planned.After})
		items = append(items, db.AdminChangeSnapshotItem{
			ChatID:        planned.ChatID,
			BeforeJSON:    string(beforeJSON),
			AfterJSON:     string(afterJSON),
			Status:        status,
			FailureReason: failureReason,
		})
	}
	summary, _ := json.Marshal(receipt)
	status := "completed"
	if receipt.FailedCount > 0 && receipt.UpdatedCount > 0 {
		status = "partial"
	} else if receipt.FailedCount > 0 {
		status = "failed"
	}
	_, _ = s.db.SaveAdminChangeSnapshot(db.AdminChangeSnapshot{
		OwnerUsername: plan.Username,
		Action:        strings.TrimSpace(action),
		SummaryJSON:   string(summary),
		Status:        status,
		Items:         items,
	})
}
