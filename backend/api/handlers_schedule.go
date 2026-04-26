package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Enach/clockwise-like/backend/auth"
	"github.com/Enach/clockwise-like/backend/calendar"
	"github.com/Enach/clockwise-like/backend/engine"
	"github.com/Enach/clockwise-like/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
	"golang.org/x/oauth2"
)

type scheduleHandlers struct {
	eng *engine.CompressionEngine
	db  *sql.DB
	oauthConfig *oauth2.Config
}

func (h *scheduleHandlers) compress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Date string `json:"date"`
		Week string `json:"week"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	var results []*engine.CompressionResult

	if body.Week != "" {
		weekStart, err := time.Parse("2006-01-02", body.Week)
		if err != nil {
			writeError(w, "invalid week format", http.StatusBadRequest)
			return
		}
		monday := startOfWeek(weekStart)
		for i := 0; i < 5; i++ {
			day := monday.AddDate(0, 0, i)
			res, err := h.eng.SuggestForDay(r.Context(), day)
			if err != nil {
				continue
			}
			results = append(results, res)
		}
	} else {
		target := time.Now()
		if body.Date != "" {
			parsed, err := time.Parse("2006-01-02", body.Date)
			if err != nil {
				writeError(w, "invalid date format", http.StatusBadRequest)
				return
			}
			target = parsed
		}
		res, err := h.eng.SuggestForDay(r.Context(), target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		results = append(results, res)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

type applyRequest struct {
	Proposals []struct {
		EventID       string `json:"event_id"`
		ProposedStart string `json:"proposed_start"`
		ProposedEnd   string `json:"proposed_end"`
	} `json:"proposals"`
}

func (h *scheduleHandlers) applyCompress(w http.ResponseWriter, r *http.Request) {
	var req applyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	token, err := auth.TokenFromDB(h.db)
	if err != nil || token == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	ts := auth.TokenSource(r.Context(), h.oauthConfig, token)
	client, err := calendar.NewClient(r.Context(), ts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var applied, failed []string

	for _, p := range req.Proposals {
		start, err1 := time.Parse(time.RFC3339, p.ProposedStart)
		end, err2 := time.Parse(time.RFC3339, p.ProposedEnd)
		if err1 != nil || err2 != nil {
			failed = append(failed, p.EventID+": invalid time format")
			continue
		}

		busy, err := client.GetFreeBusy(r.Context(), nil, start, end)
		if err == nil && hasConflict(busy, start, end) {
			failed = append(failed, p.EventID+": attendee conflict at proposed time")
			continue
		}

		existing, err := client.GetEvent(r.Context(), client.CalendarID, p.EventID)
		if err != nil {
			failed = append(failed, p.EventID+": "+err.Error())
			continue
		}

		existing.Start = &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)}
		existing.End = &googlecalendar.EventDateTime{DateTime: end.Format(time.RFC3339)}

		if _, err := client.UpdateEvent(r.Context(), client.CalendarID, p.EventID, existing); err != nil {
			failed = append(failed, p.EventID+": "+err.Error())
			continue
		}

		storage.WriteAuditLog(h.db, "meeting_moved", `{"event_id":"`+p.EventID+`","new_start":"`+p.ProposedStart+`"}`)
		applied = append(applied, p.EventID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"applied": applied,
		"failed":  failed,
	})
}

func startOfWeek(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-wd+1, 0, 0, 0, 0, t.Location())
}

func hasConflict(busy map[string][]calendar.TimeSlot, start, end time.Time) bool {
	for _, slots := range busy {
		for _, s := range slots {
			if start.Before(s.End) && end.After(s.Start) {
				return true
			}
		}
	}
	return false
}
