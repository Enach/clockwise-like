package api

import (
	"database/sql"

	"github.com/Enach/clockwise-like/backend/engine"
	"github.com/Enach/clockwise-like/backend/nlp"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

func RegisterRoutes(r *chi.Mux, db *sql.DB, oauthConfig *oauth2.Config) {
	r.Use(corsMiddleware)
	r.Use(loggingMiddleware)

	r.Get("/api/health", healthHandler)

	ah := &authHandlers{oauthConfig: oauthConfig, db: db}
	r.Route("/api/auth", func(r chi.Router) {
		r.Get("/google", ah.startOAuth)
		r.Get("/callback", ah.callback)
		r.Get("/status", ah.statusWithProvider)
		r.Delete("/disconnect", ah.disconnect)
		r.Get("/microsoft", ah.startMicrosoftOAuth)
		r.Get("/microsoft/callback", ah.microsoftCallback)
		r.Get("/zoom", (&conferencingHandlers{db: db, oauthConfig: oauthConfig}).startZoomOAuth)
		r.Get("/zoom/callback", (&conferencingHandlers{db: db, oauthConfig: oauthConfig}).zoomCallback)
	})

	ch := &calendarHandlers{oauthConfig: oauthConfig, db: db}
	r.Route("/api/calendar", func(r chi.Router) {
		r.Get("/events", ch.listEvents)
		r.Get("/freebusy", ch.freeBusy)
	})

	sh := &settingsHandlers{db: db}
	r.Route("/api/settings", func(r chi.Router) {
		r.Get("/", sh.getSettings)
		r.Put("/", sh.putSettings)
	})

	fh := newFocusHandlers(db, oauthConfig)
	r.Route("/api/focus", func(r chi.Router) {
		r.Post("/run", fh.runFocus)
		r.Get("/blocks", fh.listBlocks)
		r.Delete("/blocks", fh.clearBlocks)
	})

	comprEng := &engine.CompressionEngine{DB: db, OAuthConfig: oauthConfig}
	smartEng := &engine.SmartScheduler{DB: db, OAuthConfig: oauthConfig}
	sched := &scheduleHandlers{eng: comprEng, smart: smartEng, db: db}
	r.Route("/api/schedule", func(r chi.Router) {
		r.Post("/compress", sched.compress)
		r.Post("/compress/apply", sched.applyCompress)
		r.Post("/suggest", sched.suggestMeeting)
		r.Post("/create", sched.createMeeting)
	})

	nlpSvc := &nlp.NLPService{DB: db, OAuthConfig: oauthConfig}
	nh := &nlpHandlers{svc: nlpSvc, smart: smartEng, db: db}
	r.Route("/api/nlp", func(r chi.Router) {
		r.Post("/parse", nh.parse)
		r.Post("/confirm", nh.confirm)
	})

	lh := &llmHandlers{db: db}
	r.Post("/api/llm/test", lh.testLLM)

	// Personal calendars (T-14)
	ph := newPersonalHandlers(db, oauthConfig)
	r.Route("/api/personal-calendars", func(r chi.Router) {
		r.Get("/", ph.list)
		r.Post("/", ph.create)
		r.Delete("/{id}", ph.delete)
		r.Get("/{id}/preview", ph.preview)
		r.Post("/{id}/sync", ph.sync)
	})

	// Event editor (T-15)
	eh := &eventHandlers{db: db, oauthConfig: oauthConfig}
	r.Route("/api/events", func(r chi.Router) {
		r.Patch("/{id}", eh.patchEvent)
		r.Delete("/{id}", eh.deleteEvent)
	})
	r.Get("/api/rooms", eh.listRooms)
	r.Get("/api/attendees/suggest", eh.suggestAttendees)

	// Conferencing (T-16)
	cnh := &conferencingHandlers{db: db, oauthConfig: oauthConfig}
	r.Post("/api/conference/create", cnh.createConference)
}
