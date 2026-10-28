package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Enach/paceday/backend/api"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/scheduler"
	"github.com/Enach/paceday/backend/storage"
	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"
)

func main() {
	// Sentry MUST be initialized before any DB open or cron start so a panic
	// during startup is still captured, and Flush MUST be deferred so buffered
	// events are delivered on shutdown (contract §2). Init is non-fatal: a
	// bad/empty DSN degrades to "monitoring disabled" and the server still boots.
	initSentry()
	defer sentry.Flush(2 * time.Second)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Calendar-provider decision (PAC-43). This runs before the database is
	// opened and before any cron or route exists, so an unsafe configuration
	// refuses to start before it touches anything. Configure is called in
	// every deployment, production included.
	calDecision, err := config.DecideCalendarEndpoint(
		os.Getenv(config.EnvCalendarBaseURL),
		os.Getenv(config.EnvAllowStandins),
	)
	if err != nil {
		log.Fatalf("refusing to start: %v", err)
	}
	if calDecision.Notice != "" {
		log.Printf("WARNING: %s", calDecision.Notice)
	}
	calendar.Configure(calDecision)

	// SSO admin step-up identity providers (PAC-50 §4.1): the same test-mode
	// declaration governs any redirection, and an unsafe one refuses to start.
	googleIdP, err := config.DecideGoogleIdP(os.Getenv(config.EnvGoogleIdPBaseURL), os.Getenv(config.EnvAllowStandins))
	if err != nil {
		log.Fatalf("refusing to start: %v", err)
	}
	microsoftIdP, err := config.DecideMicrosoftIdP(os.Getenv(config.EnvMicrosoftIdPBaseURL), os.Getenv(config.EnvAllowStandins))
	if err != nil {
		log.Fatalf("refusing to start: %v", err)
	}
	for _, n := range []string{googleIdP.Notice, microsoftIdP.Notice} {
		if n != "" {
			log.Printf("WARNING: %s", n)
		}
	}
	api.ConfigureSSOStepup(googleIdP, microsoftIdP)

	// LLM approved hosts (PAC-66). Never fatal: a malformed entry is logged and
	// approves nothing; unset approves nothing (fail closed).
	llmHosts, llmNotices := config.DecideLLMApprovedHosts(os.Getenv)
	for _, n := range llmNotices {
		log.Printf("WARNING: %s", n)
	}
	nlp.SetApprovedHosts(llmHosts)
	// PAC-66-B: approved destinations of openai and anthropic (beside their
	// built-in public hosts), gateway prefixes allowed. Never fatal. The
	// retired Ollama variable is reported here once and approves nothing
	// (NEW-llm-bring-your-own AC-19).
	llmUserDest, llmUserNotices := config.DecideLLMUserDestinations(os.Getenv)
	for _, n := range llmUserNotices {
		log.Printf("WARNING: %s", n)
	}
	nlp.SetUserDestinations(llmUserDest)

	// Cookie Secure attribute (PAC-20). Never fatal; anything but an explicit
	// PACEDAY_INSECURE_COOKIES=true keeps every cookie Secure (fail closed).
	secureCookies, cookieNotice := config.DecideSecureCookies(os.Getenv)
	if cookieNotice != "" {
		log.Printf("WARNING: %s", cookieNotice)
	}
	api.SetSecureCookies(secureCookies)

	// Public booking rate limits (NEW-booking-rate-limit). Never fatal; the
	// counts can be raised only in test mode (the e2e stack), never lowered
	// or changed in production.
	bookLimit, slotsLimit, limitNotices := config.DecideBookingRateLimits(os.Getenv)
	for _, n := range limitNotices {
		log.Printf("WARNING: %s", n)
	}
	api.SetPublicBookingLimits(bookLimit, slotsLimit)

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

	// Personal calendar blocker — sync every 30 minutes, per user.
	// We re-query users on every tick so users connecting calendars mid-run
	// don't have to wait for a restart.
	blocker := &engine.PersonalBlocker{DB: db, OAuthConfig: oauthConfig}
	personalCron := cron.New()
	if _, err := personalCron.AddFunc("@every 30m", func() {
		userIDs, err := storage.ListUsersWithPersonalCalendars(db)
		if err != nil {
			log.Printf("personal blocker: list users: %v", err)
			sentry.CaptureException(err)
			return
		}
		for _, userID := range userIDs {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := blocker.SyncAllForUser(ctx, userID); err != nil {
				log.Printf("personal blocker sync for user %s: %v", userID, err)
				sentry.CaptureException(err)
			}
			cancel()
		}
	}); err != nil {
		log.Printf("personal blocker cron registration error: %v", err)
	}
	personalCron.Start()
	defer personalCron.Stop()

	// Automatic RSVP decline — checks the next ten working days every 15 minutes.
	// The per-user setting is off by default and the service only handles
	// pending/tentative incoming invitations outside work or protected lunch.
	autoDecliner := &engine.AutoDeclineService{DB: db, OAuthConfig: oauthConfig}
	autoDeclineCron := cron.New()
	if _, err := autoDeclineCron.AddFunc("@every 15m", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := autoDecliner.RunAll(ctx); err != nil {
			log.Printf("auto-decline batch: %v", err)
			sentry.CaptureException(err)
		}
	}); err != nil {
		log.Printf("auto-decline cron registration error: %v", err)
	}
	autoDeclineCron.Start()
	defer autoDeclineCron.Stop()

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

	// Manager team detection — runs every Sunday at 03:00 UTC.
	mgrEng := &engine.ManagerEngine{DB: db, OAuthConfig: oauthConfig}
	mgrCron := cron.New()
	if _, err := mgrCron.AddFunc("0 3 * * 0", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		ids, err := storage.ListManagerUserIDs(db)
		if err != nil {
			log.Printf("manager batch: list users error: %v", err)
			return
		}
		for _, uid := range ids {
			if _, err := mgrEng.DetectTeam(ctx, uid); err != nil {
				log.Printf("manager batch: detect %s: %v", uid, err)
			}
		}
	}); err != nil {
		log.Printf("manager cron registration error: %v", err)
	}
	mgrCron.Start()
	defer mgrCron.Stop()

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET environment variable is required")
	}
	allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
	frontendURL := os.Getenv("FRONTEND_URL")

	r := chi.NewRouter()
	api.RegisterRoutes(r, db, oauthConfig, jwtSecret, allowedOrigin, frontendURL)

	log.Printf("server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
