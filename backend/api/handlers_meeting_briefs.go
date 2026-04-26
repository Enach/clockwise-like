package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

type meetingBriefHandlers struct {
	db          *sql.DB
	oauthConfig *oauth2.Config
}

type briefResponse struct {
	Status      string                 `json:"status"`
	GeneratedAt string                 `json:"generated_at,omitempty"`
	BriefText   string                 `json:"brief_text,omitempty"`
	Sources     briefSources           `json:"sources"`
}

type briefSources struct {
	Slack  []storage.SlackMessage `json:"slack"`
	Notion []storage.NotionPage   `json:"notion"`
}

func (h *meetingBriefHandlers) getBrief(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	eventID := chi.URLParam(r, "event_id")
	if eventID == "" {
		http.Error(w, "event_id required", http.StatusBadRequest)
		return
	}

	brief, err := storage.GetMeetingBrief(h.db, userID, eventID)
	w.Header().Set("Content-Type", "application/json")
	if err == sql.ErrNoRows {
		_ = json.NewEncoder(w).Encode(&briefResponse{Status: "pending", Sources: briefSources{Slack: []storage.SlackMessage{}, Notion: []storage.NotionPage{}}})
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(briefToResponse(brief))
}

func (h *meetingBriefHandlers) refreshBrief(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	eventID := chi.URLParam(r, "event_id")
	if eventID == "" {
		http.Error(w, "event_id required", http.StatusBadRequest)
		return
	}

	// Build a minimal event stub from the event ID.
	// In a full implementation this would fetch from the calendar API.
	// We use a stub so the service can look up the correct brief and force-regenerate.
	event := &googlecalendar.Event{Id: eventID}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	svc := &engine.MeetingBriefService{DB: h.db}
	if settings, err := storage.GetSettings(h.db); err == nil {
		if llm, err := nlp.NewLLMClientFromSettings(settings); err == nil {
			svc.LLM = llm
		}
	}
	brief, err := svc.Generate(ctx, userID, event, true)
	if err != nil {
		http.Error(w, "generation error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(briefToResponse(brief))
}

func briefToResponse(b *storage.MeetingBrief) *briefResponse {
	slack := b.SlackResults
	if slack == nil {
		slack = []storage.SlackMessage{}
	}
	notion := b.NotionResults
	if notion == nil {
		notion = []storage.NotionPage{}
	}
	return &briefResponse{
		Status:      b.Status,
		GeneratedAt: b.GeneratedAt.Format(time.RFC3339),
		BriefText:   b.BriefText,
		Sources: briefSources{
			Slack:  slack,
			Notion: notion,
		},
	}
}
