package api

import (
	"database/sql"

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
		r.Get("/status", ah.status)
		r.Delete("/disconnect", ah.disconnect)
	})

	ch := &calendarHandlers{oauthConfig: oauthConfig, db: db}
	r.Route("/api/calendar", func(r chi.Router) {
		r.Get("/events", ch.listEvents)
		r.Get("/freebusy", ch.freeBusy)
	})

	r.Route("/api/settings", func(r chi.Router) {
		// T-03: Settings handlers
	})

	r.Route("/api/focus", func(r chi.Router) {
		// T-04: Focus time engine handlers
	})

	r.Route("/api/schedule", func(r chi.Router) {
		// T-06: Smart scheduling handlers
	})

	r.Route("/api/nlp", func(r chi.Router) {
		// T-07: NLP backend handlers
	})

	_ = db
}
