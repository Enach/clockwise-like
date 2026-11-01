package main

import (
	"cmp"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Enach/paceday/backend/api"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/scheduler"
	"github.com/Enach/paceday/backend/storage"
	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"
)

func main() {
	// Health mode (PAC-81 AC-A16): `paceday health` asks the running server's
	// readiness on the local port and exits 0 or 1. It runs before anything
	// else: no Sentry, no database, no migration, no secret.
	if len(os.Args) > 1 && os.Args[1] == "health" {
		os.Exit(healthMode(os.Getenv))
	}
	os.Exit(run())
}

// healthMode is the container health check: GET /api/ready on the local
// port, success only on a 200 within 2 s.
func healthMode(getenv func(string) string) int {
	port := cmp.Or(getenv("PORT"), "8080")
	if lifecycle.Probe("http://127.0.0.1:"+port+"/api/ready", 2*time.Second) {
		return 0
	}
	return 1
}

// cronJob wraps a cron function as a tracked unit (PAC-81 AC-A6): it is
// awaited by the drain, its context is cancelled at the end of the drain,
// and no new run starts once shutdown has begun.
func cronJob(name string, fn func(ctx context.Context)) func() {
	return func() {
		g := lifecycle.Default()
		if g.ShuttingDown() {
			return
		}
		done := g.Track(lifecycle.Unit{Kind: "cron", ID: name})
		defer done()
		fn(g.Context())
	}
}

func run() int {
	// Sentry MUST be initialized before any DB open or cron start so a panic
	// during startup is still captured; buffered events are flushed at the
	// end of the shutdown sequence (contract §2). Init is non-fatal: an empty
	// DSN means "monitoring disabled" and the server still boots (AC-A14).
	initSentry()

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
	// PAC-81 AC-A15: an http:// public URL with Secure cookies breaks sign-in.
	for _, n := range config.InsecurePublicURLs(os.Getenv, secureCookies) {
		log.Printf("WARNING: %s", n)
	}

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
	// PAC-81 AC-A9: pool caps, operator settings with the spec's defaults.
	pool, poolNotices := config.DecideDBPool(os.Getenv)
	for _, n := range poolNotices {
		log.Printf("WARNING: %s", n)
	}
	storage.ApplyPool(db, pool.MaxOpen, pool.MaxIdle, pool.MaxLifetime, pool.MaxIdleTime)

	oauthConfig := auth.NewGoogleOAuthConfig(
		os.Getenv("GOOGLE_CLIENT_ID"),
		os.Getenv("GOOGLE_CLIENT_SECRET"),
		os.Getenv("GOOGLE_REDIRECT_URL"),
	)

	// Focus time auto-scheduler cron (schedule configured in settings).
	focusCron := scheduler.NewFocusCron(db, oauthConfig)
	focusCron.Start()

	// Personal calendar blocker — sync every 30 minutes, per user.
	// We re-query users on every tick so users connecting calendars mid-run
	// don't have to wait for a restart.
	blocker := &engine.PersonalBlocker{DB: db, OAuthConfig: oauthConfig}
	personalCron := cron.New()
	if _, err := personalCron.AddFunc("@every 30m", cronJob("personal-blocker", func(work context.Context) {
		userIDs, err := storage.ListUsersWithPersonalCalendars(db)
		if err != nil {
			log.Printf("personal blocker: list users: %v", err)
			sentry.CaptureException(err)
			return
		}
		for _, userID := range userIDs {
			if work.Err() != nil {
				return
			}
			ctx, cancel := context.WithTimeout(work, 5*time.Minute)
			if err := blocker.SyncAllForUser(ctx, userID); err != nil {
				log.Printf("personal blocker sync for user %s: %v", userID, err)
				sentry.CaptureException(err)
			}
			cancel()
		}
	})); err != nil {
		log.Printf("personal blocker cron registration error: %v", err)
	}
	personalCron.Start()

	// Automatic RSVP decline — checks the next ten working days every 15 minutes.
	// The per-user setting is off by default and the service only handles
	// pending/tentative incoming invitations outside work or protected lunch.
	autoDecliner := &engine.AutoDeclineService{DB: db, OAuthConfig: oauthConfig}
	autoDeclineCron := cron.New()
	if _, err := autoDeclineCron.AddFunc("@every 15m", cronJob("auto-decline", func(work context.Context) {
		ctx, cancel := context.WithTimeout(work, 5*time.Minute)
		defer cancel()
		if err := autoDecliner.RunAll(ctx); err != nil {
			log.Printf("auto-decline batch: %v", err)
			sentry.CaptureException(err)
		}
	})); err != nil {
		log.Printf("auto-decline cron registration error: %v", err)
	}
	autoDeclineCron.Start()

	// Daily morning recap — fires every minute, checks per-user send time.
	recapSvc := &engine.DailyRecapService{DB: db}
	recapCron := cron.New()
	if _, err := recapCron.AddFunc("* * * * *", cronJob("daily-recap", func(work context.Context) {
		ctx, cancel := context.WithTimeout(work, 55*time.Second)
		defer cancel()
		recapSvc.RunAll(ctx)
	})); err != nil {
		log.Printf("recap cron registration error: %v", err)
	}
	recapCron.Start()

	// Manager team detection — runs every Sunday at 03:00 UTC.
	mgrEng := &engine.ManagerEngine{DB: db, OAuthConfig: oauthConfig}
	mgrCron := cron.New()
	if _, err := mgrCron.AddFunc("0 3 * * 0", cronJob("manager-detection", func(work context.Context) {
		ctx, cancel := context.WithTimeout(work, 10*time.Minute)
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
	})); err != nil {
		log.Printf("manager cron registration error: %v", err)
	}
	mgrCron.Start()

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET environment variable is required")
	}
	allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
	frontendURL := os.Getenv("FRONTEND_URL")

	r := chi.NewRouter()
	api.RegisterRoutes(r, db, oauthConfig, jwtSecret, allowedOrigin, frontendURL)

	// PAC-81 Part A: the server's own timeouts (AC-A1, AC-A5), request
	// contexts that the shutdown sequence can cancel, and the shutdown
	// sequence on SIGTERM or SIGINT (AC-A6, AC-A7).
	base, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	srv := lifecycle.NewServer(":"+port, r, lifecycle.DefaultTimeouts, base)
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stopSignals()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	log.Printf("server listening on :%s", port)

	stopScheduling := func() {
		focusCron.Stop()
		for _, c := range []*cron.Cron{personalCron, autoDeclineCron, recapCron, mgrCron} {
			c.Stop() // no new run; a running one is a tracked unit
		}
	}
	code := 0
	select {
	case err := <-serveErr:
		log.Printf("server error: %v", err)
		code = 1
		stopScheduling()
	case <-sigCtx.Done():
		log.Printf("shutdown: signal received")
		if !lifecycle.Default().Shutdown(srv, lifecycle.DefaultPhases, stopScheduling, cancelRequests) {
			code = 1
		}
	}
	// The pool closes only after every unit returned or was abandoned.
	if err := db.Close(); err != nil {
		log.Printf("shutdown: close database pool: %v", err)
	}
	sentry.Flush(2 * time.Second)
	return code
}
