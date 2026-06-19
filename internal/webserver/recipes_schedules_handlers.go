package webserver

import (
	"encoding/json"
	"net/http"
)

// ScheduleListItem is the JSON shape returned by GET /api/v1/schedules.
type ScheduleListItem struct {
	AppName      string            `json:"app_name"`
	ScheduleName string            `json:"schedule_name"`
	Cron         string            `json:"cron"`
	TimeZone     string            `json:"timezone,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	RecipePath   string            `json:"recipe_path"`
}

func (s *Server) handleSchedulesList(w http.ResponseWriter, _ *http.Request) {
	items := make([]ScheduleListItem, 0)

	if s.scheduleManager != nil {
		for _, e := range s.scheduleManager.Entries() {
			items = append(items, ScheduleListItem{
				AppName:      e.AppName,
				ScheduleName: e.ScheduleName,
				Cron:         e.Schedule.Cron,
				TimeZone:     e.Schedule.TimeZone,
				Env:          e.Schedule.Env,
				RecipePath:   e.RecipePath,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}
