package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"
	"github.com/Enach/paceday/backend/api"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/scheduler"
	"github.com/Enach/paceday/backend/storage"
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

	// Focus time auto-scheduler cron (schedule configured in settings).
	focusCron := scheduler.NewFocusCron(db, oauthConfig)
	focusCron.Start()
	defer focusCron.Stop()

	// Personal calendar blocker — sync every 30 minutes.
	blocker := &engine.PersonalBlocker{DB: db, OAuthConfig: oauthConfig}
	personalCron := cron.New()
	if _, err := personalCron.AddFunc("@every 30m", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := blocker.SyncAll(ctx); err != nil {
			log.Printf("personal blocker sync error: %v", err)
		}
	}); err != nil {
		log.Printf("personal blocker cron registration error: %v", err)
	}
	personalCron.Start()
	defer personalCron.Stop()

	// Daily morning recap — fires every minute, checks per-user send time.
	recapSvc := &engine.DailyRecapService{DB: db}
	recapCron := cron.New()
	if _, err := recapCron.AddFunc("* * * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
		defer cancel()
		recapSvc.RunAll(ctx)
	}); err != nil {
		log.Printf("recap cron registration error: %v", err)
	}
	recapCron.Start()
	defer recapCron.Stop()

	jwtSecret := os.Getenv("JWT_SECRET")

	r := chi.NewRouter()
	api.RegisterRoutes(r, db, oauthConfig, jwtSecret)

	log.Printf("server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
