package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func RegisterRoutes(r *chi.Mux, db *sql.DB) {
	r.Use(corsMiddleware)
	r.Use(loggingMiddleware)

	r.Get("/api/health", healthHandler)

	r.Route("/api/auth", func(r chi.Router) {
		// T-02: Google OAuth handlers
	})

	r.Route("/api/settings", func(r chi.Router) {
		// T-03: Settings handlers
	})

	r.Route("/api/calendar", func(r chi.Router) {
		// T-04: Calendar / focus handlers
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
	_ = http.MethodGet
}
