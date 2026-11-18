package engine

// PAC-129 (spec §3.4, §3.5, D-2, D-11; contract contracts/features/PAC-129.md
// §6): the team path's free/busy entry point. Added people are read only
// through the adding owner's credential, in batches within each provider's
// documented limit, at most 3 batches at a time, each on its own time limit
// under the request budget. It reuses PAC-133's readers (unreadable addresses
// are unknown), PAC-133's cache (key, LRU, 15-minute TTL) and its rule that a
// lookup cut by the request's own deadline is never cached. Query, used by
// POST /api/freebusy and Manager analytics, is not changed.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/peritem"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// The provider limits (spec §2.5): Google freebusy.query answers at most 50
// calendars per request; Microsoft getSchedule at most 20 schedules.
const (
	googleBatchMax     = 50
	outlookBatchMax    = 20
	teamBatchesAtOnce  = 3
	teamBatchTimeLimit = 5 * time.Second
)

type googleFreeBusy interface {
	GetFreeBusy(ctx context.Context, ids []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error)
}

// teamBatching holds the team path's knobs; the zero value is production's.
// Tests set them (unexported, PAC-80: no package-level variables).
type teamBatching struct {
	googleMax  int
	outlookMax int
	atOnce     int
	limit      time.Duration
	// newGoogle builds the Google reader over a token source; nil means
	// calendar.NewClient (contract §11: readers injectable per provider).
	newGoogle func(ctx context.Context, ts oauth2.TokenSource) (googleFreeBusy, error)
}

func orDefault[T int | time.Duration](v, d T) T {
	if v > 0 {
		return v
	}
	return d
}

func (s *FreeBusyService) googleReader(ctx context.Context, ts oauth2.TokenSource) (googleFreeBusy, error) {
	if s.team.newGoogle != nil {
		return s.team.newGoogle(ctx, ts)
	}
	return calendar.NewClient(ctx, ts)
}

// QueryForTeam reads emails' free/busy over [start, end) through ownerID's
// calendar credential (the adding owner: never the requester's, spec D-2).
// Each address is "known" or "unknown" exactly as Query decides it. It
// returns budget.ErrExpired, and caches nothing of the cut batch, when a
// batch was not started because the request budget reached the stop margin,
// or was cut by a deadline the budget shortened (contract §6).
func (s *FreeBusyService) QueryForTeam(ctx context.Context, ownerID uuid.UUID, emails []string, start, end time.Time) ([]ParticipantBusy, error) {
	provider, err := storage.CalendarProviderOf(ctx, s.DB, ownerID)
	if err != nil {
		return nil, err
	}
	outlook := strings.EqualFold(provider, "outlook")
	size := orDefault(s.team.googleMax, googleBatchMax)
	if outlook {
		size = orDefault(s.team.outlookMax, outlookBatchMax)
	}

	results := make(map[string]ParticipantBusy, len(emails))
	var toQuery []string
	for _, email := range emails {
		email = strings.ToLower(storage.TrimAddressSpace(email))
		if _, dup := results[email]; dup {
			continue
		}
		if personalDomains[emailDomain(email)] {
			results[email] = ParticipantBusy{Email: email, Coverage: "unknown", Busy: []calendar.TimeSlot{}}
			continue
		}
		if cached, ok := s.getCached(freeBusyCacheKey(ownerID, email, start, end)); ok {
			results[email] = ParticipantBusy{Email: email, Coverage: cached.coverage, Busy: cached.slots}
			continue
		}
		results[email] = ParticipantBusy{} // placeholder, filled by its batch
		toQuery = append(toQuery, email)
	}

	var batches [][]string
	for i := 0; i < len(toQuery); i += size {
		batches = append(batches, toQuery[i:min(i+size, len(toQuery))])
	}

	var (
		mu  sync.Mutex
		cut bool
		wg  sync.WaitGroup
	)
	sem := make(chan struct{}, orDefault(s.team.atOnce, teamBatchesAtOnce))
	limit := orDefault(s.team.limit, teamBatchTimeLimit)
	margin := peritem.Current().Margin
	for _, batch := range batches {
		sem <- struct{}{}
		wg.Add(1)
		go func(batch []string) {
			defer wg.Done()
			defer func() { <-sem }()
			got, ok := s.teamBatch(ctx, ownerID, outlook, batch, start, end, limit, margin)
			mu.Lock()
			defer mu.Unlock()
			if !ok {
				cut = true
				return
			}
			for _, r := range got {
				results[r.Email] = r
			}
		}(batch)
	}
	wg.Wait()
	if cut {
		return nil, budget.ErrExpired
	}

	out := make([]ParticipantBusy, 0, len(results))
	seen := map[string]bool{}
	for _, email := range emails {
		email = strings.ToLower(storage.TrimAddressSpace(email))
		if !seen[email] {
			seen[email] = true
			out = append(out, results[email])
		}
	}
	return out, nil
}

// teamBatch reads one batch on its own limit, shortened to the budget left
// minus the stop margin. ok is false when the batch was not started or was
// cut by the budget; then nothing is cached (contract §6, M-3).
func (s *FreeBusyService) teamBatch(ctx context.Context, ownerID uuid.UUID, outlook bool, emails []string, start, end time.Time, limit, margin time.Duration) ([]ParticipantBusy, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	d, shortened := limit, false
	if remaining, bounded := peritem.Remaining(ctx); bounded {
		if remaining <= margin {
			return nil, false
		}
		if remaining-margin < limit {
			d, shortened = remaining-margin, true
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	fetched, credential := s.fetchThrough(callCtx, ctx, ownerID, outlook, emails, start, end)
	if callCtx.Err() != nil && (shortened || ctx.Err() != nil) {
		return nil, false // cut by the budget: learnt nothing, cache nothing
	}

	// Not cut: PAC-133's one cache-write rule. ctx (the request) is not done
	// here, so an own-limit timeout or a provider refusal is cached unknown
	// for 15 minutes (PAC-67, unchanged); a result computed while the owner's
	// credential is recorded as lost is not (PAC-67 AC-20).
	return s.settleLookup(ctx, ownerID, credential, fetched, emails, start, end), true
}

// fetchThrough asks the owner's provider about emails with the owner's
// credential, renewed and stored for the owner; a refused renewal is recorded
// as the owner's loss by the token source (PAC-67), never the requester's. A
// missing credential or a failed call is nil (every address unknown). The
// credential is loaded on dbCtx (the request), the provider called on callCtx.
func (s *FreeBusyService) fetchThrough(callCtx, dbCtx context.Context, ownerID uuid.UUID, outlook bool, emails []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, string) {
	if outlook {
		token, err := auth.LoadMicrosoftTokenCtx(dbCtx, s.DB, ownerID)
		if err != nil || token == nil {
			return nil, storage.CredentialOutlook
		}
		cfg := s.Microsoft
		if cfg == nil {
			cfg = auth.MicrosoftConfigFromEnv()
		}
		ts := auth.MicrosoftTokenSource(callCtx, s.DB, cfg, ownerID, token)
		fetched, _ := s.outlookReader(ts).GetFreeBusy(callCtx, emails, start, end)
		return fetched, storage.CredentialOutlook
	}
	token, err := auth.LoadUserTokenCtx(dbCtx, s.DB, ownerID)
	if err != nil || token == nil {
		return nil, storage.CredentialGoogle
	}
	ts := auth.UserTokenSource(callCtx, s.DB, s.OAuthConfig, ownerID, token)
	gc, err := s.googleReader(callCtx, ts)
	if err != nil {
		return nil, storage.CredentialGoogle
	}
	fetched, _ := gc.GetFreeBusy(callCtx, emails, start, end)
	return fetched, storage.CredentialGoogle
}
