package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
)

type scheduleHandlers struct {
	eng   Compressor
	smart Scheduler
	db    *sql.DB
}

func (h *scheduleHandlers) compress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Date string `json:"date"`
		Week string `json:"week"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	results := make([]*engine.CompressionResult, 0)

	var weekStart, target time.Time
	if body.Week != "" {
		parsed, err := time.Parse("2006-01-02", body.Week)
		if err != nil {
			writeError(w, "invalid week format", http.StatusBadRequest)
			return
		}
		weekStart = parsed
	} else {
		target = time.Now()
		if body.Date != "" {
			parsed, err := time.Parse("2006-01-02", body.Date)
			if err != nil {
				writeError(w, "invalid date format", http.StatusBadRequest)
				return
			}
			target = parsed
		}
	}

	// The credential is checked once, before any day is computed, in both
	// modes: week mode skips days that fail, and must not turn "no calendar"
	// or an unreadable credential store into 200 [] (PAC-21, API-054).
	if err := h.eng.CheckCalendar(r.Context()); err != nil {
		writeCalendarFailure(w, r, "compress", err)
		return
	}

	if body.Week != "" {
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
		res, err := h.eng.SuggestForDay(r.Context(), target)
		if err != nil {
			writeCalendarFailure(w, r, "compress", err)
			return
		}
		results = append(results, res)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
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

	var proposals []engine.MoveProposal
	failed := make([]string, 0)
	for _, p := range req.Proposals {
		start, err1 := time.Parse(time.RFC3339, p.ProposedStart)
		end, err2 := time.Parse(time.RFC3339, p.ProposedEnd)
		if err1 != nil || err2 != nil {
			failed = append(failed, p.EventID+": invalid time format")
			continue
		}
		proposals = append(proposals, engine.MoveProposal{
			EventID:       p.EventID,
			ProposedStart: start,
			ProposedEnd:   end,
		})
	}

	applied, engineFailed, err := h.eng.Apply(r.Context(), proposals)
	if err != nil {
		writeCalendarFailure(w, r, "applyCompress", err)
		return
	}
	failed = append(failed, engineFailed...)
	if applied == nil {
		applied = make([]string, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
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

func (h *scheduleHandlers) suggestMeeting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DurationMinutes int      `json:"duration_minutes"`
		Attendees       []string `json:"attendees"`
		RangeStart      string   `json:"range_start"`
		RangeEnd        string   `json:"range_end"`
		Title           string   `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	start, err1 := time.Parse(time.RFC3339, body.RangeStart)
	end, err2 := time.Parse(time.RFC3339, body.RangeEnd)
	if err1 != nil || err2 != nil {
		writeError(w, "invalid range_start or range_end (use RFC3339)", http.StatusBadRequest)
		return
	}

	req := engine.ScheduleRequest{
		DurationMinutes: body.DurationMinutes,
		Attendees:       body.Attendees,
		RangeStart:      start,
		RangeEnd:        end,
		Title:           body.Title,
	}
	suggestions, err := h.smart.Suggest(r.Context(), req)
	if err != nil {
		writeCalendarFailure(w, r, "suggestMeeting", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(suggestions)
}

func (h *scheduleHandlers) createMeeting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string   `json:"title"`
		Start       string   `json:"start"`
		End         string   `json:"end"`
		Attendees   []string `json:"attendees"`
		Description string   `json:"description"`
		Location    string   `json:"location"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	start, err1 := time.Parse(time.RFC3339, body.Start)
	end, err2 := time.Parse(time.RFC3339, body.End)
	if err1 != nil || err2 != nil {
		writeError(w, "invalid start or end (use RFC3339)", http.StatusBadRequest)
		return
	}

	req := engine.ScheduleRequest{
		Title:       body.Title,
		Description: body.Description,
		Location:    body.Location,
		Attendees:   body.Attendees,
	}
	slot := engine.SuggestedSlot{Start: start, End: end}

	created, err := h.smart.CreateMeeting(r.Context(), req, slot)
	if err != nil {
		writeCreateMeetingFailure(w, r, "createMeeting", err)
		return
	}

	storage.WriteAuditLog(h.db, userIDFromCtx(r.Context()), "meeting_created", `{"event_id":"`+created.Id+`","title":"`+body.Title+`"}`)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(created)
}

// writeCreateMeetingFailure answers a failed SmartScheduler.CreateMeeting. The
// out-of-hours refusal keeps its documented fixed text (its 500 status is
// API-036), now as the JSON envelope; no calendar connected is the 401
// CalendarNotConnected; every other failure is logged and answered
// generically (PAC-21).
func writeCreateMeetingFailure(w http.ResponseWriter, r *http.Request, op string, err error) {
	if errors.Is(err, engine.ErrOutOfHoursAllowance) {
		writeError(w, "out-of-hours meeting allowance reached", http.StatusInternalServerError)
		return
	}
	writeCalendarFailure(w, r, op, err)
}
