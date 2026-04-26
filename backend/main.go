package main

import (
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/Enach/clockwise-like/backend/api"
	"github.com/Enach/clockwise-like/backend/auth"
	"github.com/Enach/clockwise-like/backend/scheduler"
	"github.com/Enach/clockwise-like/backend/storage"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	db, err := storage.Open(dsn)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	oauthConfig := auth.NewGoogleOAuthConfig(
		os.Getenv("GOOGLE_CLIENT_ID"),
		os.Getenv("GOOGLE_CLIENT_SECRET"),
		os.Getenv("GOOGLE_REDIRECT_URL"),
	)

	focusCron := scheduler.NewFocusCron(db, oauthConfig)
	focusCron.Start()
	defer focusCron.Stop()

	r := chi.NewRouter()
	api.RegisterRoutes(r, db, oauthConfig)

	log.Printf("server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
