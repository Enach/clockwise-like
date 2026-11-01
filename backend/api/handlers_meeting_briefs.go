package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
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
	// source is where the AI decision reads from; nil means db (PAC-73
	// Part 2, P2-AC-37).
	source nlp.AISource
}

// orgProviderFailureText is the failed brief's whole text after a failed
// call on the organisation's configuration (spec §3.1a, P2-D16): written by
// Paceday, carrying nothing the provider or gateway returned.
const orgProviderFailureText = "The organisation's AI provider returned an error."

type briefResponse struct {
	Status      string       `json:"status"`
	GeneratedAt string       `json:"generated_at,omitempty"`
	BriefText   string       `json:"brief_text,omitempty"`
	Sources     briefSources `json:"sources"`
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
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
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

	// NEW-llm-bring-your-own: decide the AI configuration before anything
	// else, so that "not configured" (409) and a settings-load failure (500)
	// write no brief, fetch no Slack or Notion context and send nothing
	// (spec AC-14, D-12; contract C-2).
	src := h.source
	if src == nil {
		src = nlp.DBSource{DB: h.db}
	}
	st, err := nlp.LoadAIState(ctx, src, userID)
	if err != nil {
		writeServerError(w, r, "load AI configuration", err, http.StatusInternalServerError)
		return
	}
	decision := nlp.DecideState(st)
	if decision.NotConfigured != nil {
		writeNotConfigured(w, decision.NotConfigured)
		return
	}

	// Part 2 (spec §3.1a): provider text is redacted of every key sent and
	// the caller's keys before it is stored, returned, logged or reported;
	// on the organisation's configuration a failure stores a fixed sentence.
	svc := &engine.MeetingBriefService{DB: h.db, Redact: decision.Redact,
		OnLLMError: func(err error) {
			log.Printf("meeting brief %s: provider call failed: %v", eventID, err)
			reportServerError(r, err)
		}}
	if decision.Organization() {
		svc.FailureText = orgProviderFailureText
	}
	// PAC-66 / PAC-66-B: a refused destination of a configured caller gives
	// the existing failed brief, with the reason as its text, and sends
	// nothing.
	var refusal *nlp.DestinationRefusal
	if errors.As(nlp.CheckDestination(ctx, decision.Client), &refusal) {
		if decision.Organization() {
			refusal = nlp.OrganizationDestinationRefusal(refusal)
		}
		svc.LLMUnavailable = refusal.Message
	} else {
		svc.LLM = decision.Client
	}
	brief, err := svc.Generate(ctx, userID, event, true)
	if err != nil {
		writeServerError(w, r, "generation error", err, http.StatusInternalServerError)
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
