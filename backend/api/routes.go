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

	// Public booking routes — no JWT required
	bh := newBookingHandlers(db, oauthConfig)
	r.Get("/api/book/{slug}", bh.getLinkInfo)
	r.Get("/api/book/{slug}/slots", bh.getSlots)
	r.Post("/api/book/{slug}", bh.createBooking)

	// Public auth routes — no JWT required
	ah := &authHandlers{oauthConfig: oauthConfig, db: db, jwtSecret: jwtSecret}
	ssoh := &ssoHandlers{ah: ah}
	r.Route("/api/auth", func(r chi.Router) {
		r.Get("/google", ah.startOAuth)
		r.Get("/callback", ah.callback)
		r.Get("/microsoft", ah.startMicrosoftOAuth)
		r.Get("/microsoft/callback", ah.microsoftCallback)
		r.Get("/zoom", (&conferencingHandlers{db: db, oauthConfig: oauthConfig}).startZoomOAuth)
		r.Get("/zoom/callback", (&conferencingHandlers{db: db, oauthConfig: oauthConfig}).zoomCallback)
		r.Post("/detect", ssoh.detect)
		r.Get("/sso/{domain}", ssoh.startSSO)
		r.Get("/callback/oidc/{domain}", ssoh.oidcCallback)

		// Protected sub-routes (status, disconnect)
		r.Group(func(r chi.Router) {
			if jwtSecret != "" {
				r.Use(requireAuth(jwtSecret))
			}
			r.Get("/status", ah.status)
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

		oh := &orgHandlers{db: db}
		r.Get("/api/org/members", oh.members)

		ah2 := newAnalyticsHandlers(db, oauthConfig)
		r.Route("/api/analytics", func(r chi.Router) {
			r.Get("/week", ah2.week)
			r.Get("/trends", ah2.trends)
			r.Get("/meetings", ah2.meetings)
			r.Post("/recompute", ah2.recompute)
		})

		hh := newHabitsHandlers(db, oauthConfig)
		r.Route("/api/habits", func(r chi.Router) {
			r.Get("/templates", hh.templates)
			r.Post("/reoptimize", hh.reoptimize)
			r.Post("/", hh.create)
			r.Get("/", hh.list)
			r.Patch("/{id}", hh.update)
			r.Delete("/{id}", hh.deactivate)
			r.Get("/{id}/occurrences", hh.occurrences)
		})

		slh := newSchedulingLinkHandlers(db, oauthConfig)
		r.Route("/api/scheduling-links", func(r chi.Router) {
			r.Post("/", slh.createLink)
			r.Get("/", slh.listLinks)
			r.Get("/host-invites", slh.listHostInvites)
			r.Post("/host-invites/{id}/accept", slh.acceptInvite)
			r.Post("/host-invites/{id}/decline", slh.declineInvite)
			r.Get("/{id}", slh.getLink)
			r.Patch("/{id}", slh.updateLink)
			r.Delete("/{id}", slh.deleteLink)
			r.Get("/{id}/bookings", slh.listBookings)
			r.Post("/{id}/hosts", slh.inviteHost)
		})

		r.Route("/api/admin/sso", func(r chi.Router) {
			r.Post("/", ssoh.createSSOProvider)
			r.Get("/", ssoh.listSSOProviders)
			r.Delete("/{domain}", ssoh.deleteSSOProvider)
		})

		fbh := newFreeBusyHandlers(db, oauthConfig)
		r.Post("/api/freebusy", fbh.query)

		ih := &integrationsHandlers{db: db}
		r.Route("/api/integrations", func(r chi.Router) {
			r.Get("/slack/connect", ih.slackConnect)
			r.Get("/slack/callback", ih.slackCallback)
			r.Delete("/slack", ih.slackDisconnect)
			r.Get("/slack/status", ih.slackStatus)
			r.Get("/notion/connect", ih.notionConnect)
			r.Get("/notion/callback", ih.notionCallback)
			r.Delete("/notion", ih.notionDisconnect)
			r.Get("/notion/status", ih.notionStatus)
		})

		mbh := &meetingBriefHandlers{db: db, oauthConfig: oauthConfig}
		r.Get("/api/meetings/{event_id}/brief", mbh.getBrief)
		r.Post("/api/meetings/{event_id}/brief/refresh", mbh.refreshBrief)

		th := newTeamHandlers(db, oauthConfig)
		r.Route("/api/teams", func(r chi.Router) {
			r.Post("/", th.createTeam)
			r.Get("/", th.listTeams)
			r.Get("/invites/{token}", th.getInvite)
			r.Post("/invites/{token}/accept", th.acceptInvite)
			r.Get("/{id}", th.getTeam)
			r.Patch("/{id}", th.patchTeam)
			r.Delete("/{id}", th.deleteTeam)
			r.Post("/{id}/members/invite", th.inviteMember)
			r.Delete("/{id}/members/{userId}", th.removeMember)
			r.Post("/{id}/no-meeting-zones", th.createZone)
			r.Get("/{id}/no-meeting-zones", th.listZones)
			r.Delete("/{id}/no-meeting-zones/{zoneId}", th.deleteZone)
			r.Get("/{id}/availability", th.availability)
			r.Get("/{id}/analytics", th.analyticsHandler)
		})
	})
}
