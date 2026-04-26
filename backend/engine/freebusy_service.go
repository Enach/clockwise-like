package engine

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// personalDomains are domains where free/busy lookup is never attempted.
var personalDomains = map[string]bool{
	"gmail.com":      true,
	"googlemail.com": true,
	"outlook.com":    true,
	"hotmail.com":    true,
	"yahoo.com":      true,
	"icloud.com":     true,
	"live.com":       true,
	"msn.com":        true,
	"proton.me":      true,
	"protonmail.com": true,
}

// ParticipantBusy holds the free/busy result for one email.
type ParticipantBusy struct {
	Email    string              `json:"email"`
	Coverage string              `json:"coverage"` // "known" | "unknown"
	Busy     []calendar.TimeSlot `json:"busy"`
}

// cachedResult is a TTL-aware cache entry.
type cachedResult struct {
	slots     []calendar.TimeSlot
	coverage  string
	expiresAt time.Time
}

// FreeBusyService fetches participant free/busy data using the requesting user's
// Google or Microsoft credentials.
type FreeBusyService struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config

	mu    sync.Mutex
	cache map[string]cachedResult
}

// NewFreeBusyService constructs a FreeBusyService.
func NewFreeBusyService(db *sql.DB, cfg *oauth2.Config) *FreeBusyService {
	return &FreeBusyService{
		DB:          db,
		OAuthConfig: cfg,
		cache:       make(map[string]cachedResult),
	}
}

// Query returns free/busy data for the given emails, using the requesting user's
// calendar credentials. Results are cached for 15 minutes.
func (s *FreeBusyService) Query(ctx context.Context, requestingUserID uuid.UUID, emails []string, start, end time.Time) ([]ParticipantBusy, error) {
	settings, err := storage.GetSettings(s.DB)
	if err != nil {
		return nil, err
	}

	// Separate personal-domain emails (skip) from queryable emails.
	type pending struct {
		email   string
		cacheKey string
	}
	var toQuery []pending
	results := make([]ParticipantBusy, 0, len(emails))

	dateStr := start.UTC().Format("2006-01-02")
	for _, email := range emails {
		email = strings.ToLower(strings.TrimSpace(email))
		domain := emailDomain(email)
		if personalDomains[domain] {
			results = append(results, ParticipantBusy{Email: email, Coverage: "unknown", Busy: []calendar.TimeSlot{}})
			continue
		}
		key := requestingUserID.String() + ":" + email + ":" + dateStr
		if cached, ok := s.getCached(key); ok {
			results = append(results, ParticipantBusy{Email: email, Coverage: cached.coverage, Busy: cached.slots})
			continue
		}
		toQuery = append(toQuery, pending{email: email, cacheKey: key})
	}

	if len(toQuery) == 0 {
		return results, nil
	}

	// Build email list for the API call.
	queryEmails := make([]string, len(toQuery))
	for i, p := range toQuery {
		queryEmails[i] = p.email
	}

	// Use a 5s timeout for external calls.
	qCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var fetched map[string][]calendar.TimeSlot
	provider := strings.ToLower(settings.CalendarProvider)

	switch provider {
	case "outlook":
		token, err := auth.LoadMicrosoftToken(s.DB)
		if err == nil && token != nil {
			oc := calendar.NewOutlookClient(oauth2.StaticTokenSource(token))
			fetched, _ = oc.GetFreeBusy(qCtx, queryEmails, start, end)
		}
	default:
		// Google (default).
		token, err := auth.LoadUserToken(s.DB, requestingUserID)
		if err == nil && token != nil {
			ts := auth.TokenSource(qCtx, s.OAuthConfig, token)
			gc, err := calendar.NewClient(qCtx, ts)
			if err == nil {
				fetched, _ = gc.GetFreeBusy(qCtx, queryEmails, start, end)
			}
		}
	}

	ttl := time.Now().Add(15 * time.Minute)
	for _, p := range toQuery {
		slots, ok := fetched[p.email]
		coverage := "unknown"
		if ok {
			coverage = "known"
		}
		if slots == nil {
			slots = []calendar.TimeSlot{}
		}
		s.setCached(p.cacheKey, cachedResult{slots: slots, coverage: coverage, expiresAt: ttl})
		results = append(results, ParticipantBusy{Email: p.email, Coverage: coverage, Busy: slots})
	}

	return results, nil
}

func (s *FreeBusyService) getCached(key string) (cachedResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cache[key]
	if !ok || time.Now().After(c.expiresAt) {
		delete(s.cache, key)
		return cachedResult{}, false
	}
	return c, true
}

func (s *FreeBusyService) setCached(key string, r cachedResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[key] = r
}

func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return email[at+1:]
}
