package engine

import (
	"container/list"
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

// defaultFreeBusyCacheMax bounds the participant-lookup cache (PAC-133 §4):
// an entry is an address plus a few windows, so 10,000 caps it near 10 MiB.
const defaultFreeBusyCacheMax = 10000

// freeBusyTTL is how long a stored answer is served, from the store; a hit
// does not extend it.
const freeBusyTTL = 15 * time.Minute

// FreeBusyService fetches participant free/busy data using the requesting user's
// Google or Microsoft credentials.
type FreeBusyService struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	// Microsoft renews the requester's Outlook credential (PAC-67); nil means
	// this server's configuration (auth.MicrosoftConfigFromEnv).
	Microsoft *auth.MicrosoftConfig
	Clock     Clock

	// The cache (PAC-133 §4): exact-range keys in a bounded least-recently-
	// used list. maxEntries 0 means defaultFreeBusyCacheMax; a test sets a
	// small one.
	mu         sync.Mutex
	cache      map[string]*list.Element
	lru        *list.List // front = most recently used; values are *cacheItem
	maxEntries int

	// newOutlook builds the Outlook free/busy reader over a token source; a
	// test seam, nil means calendar.NewOutlookClient.
	newOutlook func(oauth2.TokenSource) outlookFreeBusy
}

type cacheItem struct {
	key string
	val cachedResult
}

type outlookFreeBusy interface {
	GetFreeBusy(ctx context.Context, emails []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error)
}

func (s *FreeBusyService) outlookReader(ts oauth2.TokenSource) outlookFreeBusy {
	if s.newOutlook != nil {
		return s.newOutlook(ts)
	}
	return calendar.NewOutlookClient(ts)
}

func (s *FreeBusyService) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return SystemClock.Now()
}

// NewFreeBusyService constructs a FreeBusyService.
func NewFreeBusyService(db *sql.DB, cfg *oauth2.Config) *FreeBusyService {
	return &FreeBusyService{
		DB:          db,
		OAuthConfig: cfg,
	}
}

// freeBusyCacheKey is the exact question: requester, lowercased address and
// the range's start and end instants (PAC-133 §4, SF-033). Two offsets of the
// same instant share a key; nothing else is reused.
func freeBusyCacheKey(requester uuid.UUID, email string, start, end time.Time) string {
	return requester.String() + ":" + email + ":" + start.UTC().Format(time.RFC3339Nano) + "/" + end.UTC().Format(time.RFC3339Nano)
}

// Query returns free/busy data for the given emails, using the requesting
// user's calendar credentials. Each address is "known" (readable, its busy
// time complete) or "unknown" (not readable, carrying any windows the provider
// reported for it), as the reader decided (PAC-133 §2.1). An answer is cached
// for 15 minutes for exactly the same question (§4).
func (s *FreeBusyService) Query(ctx context.Context, requestingUserID uuid.UUID, emails []string, start, end time.Time) ([]ParticipantBusy, error) {
	settings, err := storage.GetOrCreateSettingsByUserCtx(ctx, s.DB, requestingUserID)
	if err != nil {
		return nil, err
	}

	// Separate personal-domain emails (skip) from queryable emails.
	type pending struct {
		email    string
		cacheKey string
	}
	var toQuery []pending
	results := make([]ParticipantBusy, 0, len(emails))

	for _, email := range emails {
		email = strings.ToLower(strings.TrimSpace(email))
		domain := emailDomain(email)
		if personalDomains[domain] {
			results = append(results, ParticipantBusy{Email: email, Coverage: "unknown", Busy: []calendar.TimeSlot{}})
			continue
		}
		key := freeBusyCacheKey(requestingUserID, email, start, end)
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

	var fetched map[string]calendar.FreeBusyEntry
	provider := strings.ToLower(settings.CalendarProvider)
	credential := storage.CredentialGoogle

	// The requester's own credential, renewed when expired and stored for them
	// (PAC-67). A failure still degrades to unknown coverage (D-6); a permanent
	// refusal is recorded as the requester's loss by the token source.
	switch provider {
	case "outlook":
		credential = storage.CredentialOutlook
		token, err := auth.LoadMicrosoftTokenCtx(ctx, s.DB, requestingUserID)
		if err == nil && token != nil {
			cfg := s.Microsoft
			if cfg == nil {
				cfg = auth.MicrosoftConfigFromEnv()
			}
			ts := auth.MicrosoftTokenSource(qCtx, s.DB, cfg, requestingUserID, token)
			fetched, _ = s.outlookReader(ts).GetFreeBusy(qCtx, queryEmails, start, end)
		}
	default:
		// Google (default).
		token, err := auth.LoadUserTokenCtx(ctx, s.DB, requestingUserID)
		if err == nil && token != nil {
			ts := auth.UserTokenSource(qCtx, s.DB, s.OAuthConfig, requestingUserID, token)
			gc, err := calendar.NewClient(qCtx, ts)
			if err == nil {
				fetched, _ = gc.GetFreeBusy(qCtx, queryEmails, start, end)
			}
		}
	}

	// A degraded result computed while the credential is recorded as lost is
	// not cached: after a reconnect the lookup is asked again with the new
	// credential instead of serving it (PAC-67 AC-20). Caching after a
	// temporary failure is unchanged. A lookup cut short by its own request's
	// deadline (budget expiry, or the client gone) is never cached: it learnt
	// nothing about the calendars (PAC-133 §4, as PAC-129 §6 rules it).
	cacheable := ctx.Err() == nil
	if cacheable && fetched == nil {
		if lost, err := storage.IsCredentialLost(s.DB, requestingUserID, credential); err != nil || lost {
			cacheable = false
		}
	}

	expires := s.now().Add(freeBusyTTL)
	for _, p := range toQuery {
		// The reader answers every address it was asked about; nothing at all
		// (the whole call failed, or no credential) is unknown with no windows.
		// An unknown address keeps the windows reported for it (§2.1 step 5).
		entry := fetched[p.email]
		coverage := "unknown"
		if entry.Known {
			coverage = "known"
		}
		slots := entry.Busy
		if slots == nil {
			slots = []calendar.TimeSlot{}
		}
		if cacheable {
			s.setCached(p.cacheKey, cachedResult{slots: slots, coverage: coverage, expiresAt: expires})
		}
		results = append(results, ParticipantBusy{Email: p.email, Coverage: coverage, Busy: slots})
	}

	return results, nil
}

func (s *FreeBusyService) cacheMax() int {
	if s.maxEntries > 0 {
		return s.maxEntries
	}
	return defaultFreeBusyCacheMax
}

// initCache keeps the zero-value service usable.
func (s *FreeBusyService) initCache() {
	if s.cache == nil {
		s.cache = make(map[string]*list.Element)
		s.lru = list.New()
	}
}

// getCached serves an unexpired entry and makes it the most recently used.
// An expired entry is never served, whatever its recency: it is removed when
// found.
func (s *FreeBusyService) getCached(key string) (cachedResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initCache()
	el, ok := s.cache[key]
	if !ok {
		return cachedResult{}, false
	}
	item := el.Value.(*cacheItem)
	if s.now().After(item.val.expiresAt) {
		s.lru.Remove(el)
		delete(s.cache, key)
		return cachedResult{}, false
	}
	s.lru.MoveToFront(el)
	return item.val, true
}

// setCached stores an entry as the most recently used, evicting the least
// recently used ones past the maximum.
func (s *FreeBusyService) setCached(key string, r cachedResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initCache()
	if el, ok := s.cache[key]; ok {
		el.Value.(*cacheItem).val = r
		s.lru.MoveToFront(el)
		return
	}
	s.cache[key] = s.lru.PushFront(&cacheItem{key: key, val: r})
	for s.lru.Len() > s.cacheMax() {
		oldest := s.lru.Back()
		s.lru.Remove(oldest)
		delete(s.cache, oldest.Value.(*cacheItem).key)
	}
}

func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return email[at+1:]
}
