package admin

import "net/http"

type adminInsightsStats struct {
	TotalGroups        int `json:"totalGroups"`
	HealthyGroups      int `json:"healthyGroups"`
	IncompleteGroups   int `json:"incompleteGroups"`
	AverageHealthScore int `json:"averageHealthScore"`
	PendingSchedules   int `json:"pendingSchedules"`
	FailedTasks        int `json:"failedTasks"`
}

type adminInsightRecommendation struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Title      string `json:"title"`
	Reason     string `json:"reason"`
	ChatID     int64  `json:"chatId,omitempty"`
	ActionURL  string `json:"actionUrl,omitempty"`
	Visibility string `json:"visibility"`
}

func (s *Server) handleAdminInsightsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s.db == nil {
		s.writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"stats": adminInsightsStats{}, "recommendations": []adminInsightRecommendation{}}})
		return
	}
	console, err := s.buildAdminConsoleData()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	riskItems, err := s.buildAdminRiskRadarItems()
	if err != nil {
		s.writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	recommendations := make([]adminInsightRecommendation, 0, len(riskItems)+len(console.NextSteps))
	for _, item := range riskItems {
		recommendations = append(recommendations, adminInsightRecommendation{
			Code:       item.Code,
			Severity:   item.Severity,
			Title:      item.Title,
			Reason:     item.Reason,
			ChatID:     item.ChatID,
			ActionURL:  item.ActionURL,
			Visibility: "admin_only",
		})
	}
	for _, item := range console.NextSteps {
		recommendations = append(recommendations, adminInsightRecommendation{
			Code:       "console_next_step:" + item.Title,
			Severity:   item.Severity,
			Title:      item.Title,
			Reason:     item.Description,
			ChatID:     item.ChatID,
			ActionURL:  item.ActionURL,
			Visibility: "admin_only",
		})
	}
	s.writeAdminJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"stats": adminInsightsStats{
				TotalGroups:        console.Summary.TotalGroups,
				HealthyGroups:      console.Summary.HealthyGroups,
				IncompleteGroups:   console.Summary.IncompleteGroups,
				AverageHealthScore: console.Summary.AverageHealthScore,
				PendingSchedules:   console.Summary.PendingSchedules,
				FailedTasks:        console.Summary.FailedTasks,
			},
			"recommendations": recommendations,
		},
	})
}
