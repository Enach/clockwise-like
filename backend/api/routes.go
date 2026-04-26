package api

import (
	"database/sql"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

func RegisterRoutes(r *chi.Mux, db *sql.DB, oauthConfig *oauth2.Config, jwtSecret string) {
	r.Use(corsMiddleware)
	r.Use(loggingMiddleware)

	r.Get("/api/health", healthHandler)

	// Public auth routes — no JWT required
	ah := &authHandlers{oauthConfig: oauthConfig, db: db, jwtSecret: jwtSecret}
	r.Route("/api/auth", func(r chi.Router) {
		r.Get("/google", ah.startOAuth)
		r.Get("/callback", ah.callback)
		r.Get("/microsoft", ah.startMicrosoftOAuth)
		r.Get("/microsoft/callback", ah.microsoftCallback)
		r.Get("/zoom", (&conferencingHandlers{db: db, oauthConfig: oauthConfig}).startZoomOAuth)
		r.Get("/zoom/callback", (&conferencingHandlers{db: db, oauthConfig: oauthConfig}).zoomCallback)

		// Protected sub-routes (status, disconnect)
		r.Group(func(r chi.Router) {
			if jwtSecret != "" {
				r.Use(requireAuth(jwtSecret))
			}
			r.Get("/status", ah.statusWithProvider)
			r.Delete("/disconnect", ah.disconnect)
		})
	})

	// /api/auth/me and /api/auth/logout — protected
	mh := &meHandlers{db: db}
	r.Group(func(r chi.Router) {
		if jwtSecret != "" {
			r.Use(requireAuth(jwtSecret))
		}
		r.Get("/api/auth/me", mh.me)
		r.Post("/api/auth/logout", mh.logout)
	})

	// All remaining API routes — protected
	r.Group(func(r chi.Router) {
		if jwtSecret != "" {
			r.Use(requireAuth(jwtSecret))
		}

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

		ph := newPersonalHandlers(db, oauthConfig)
		r.Route("/api/personal-calendars", func(r chi.Router) {
			r.Get("/", ph.list)
			r.Post("/", ph.create)
			r.Delete("/{id}", ph.delete)
			r.Get("/{id}/preview", ph.preview)
			r.Post("/{id}/sync", ph.sync)
		})

		eh := &eventHandlers{db: db, oauthConfig: oauthConfig}
		r.Route("/api/events", func(r chi.Router) {
			r.Patch("/{id}", eh.patchEvent)
			r.Delete("/{id}", eh.deleteEvent)
		})
		r.Get("/api/rooms", eh.listRooms)
		r.Get("/api/attendees/suggest", eh.suggestAttendees)

		cnh := &conferencingHandlers{db: db, oauthConfig: oauthConfig}
		r.Post("/api/conference/create", cnh.createConference)
	})
}
