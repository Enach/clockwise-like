package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-67: the lifecycle of a user's stored Google, Outlook and Zoom
// credentials. Every consumer renews through a token source built here, so:
//   - an expired access token is renewed with the OWNER's refresh token and the
//     renewal is written back for the owner only, whoever caused the use
//     (a teammate, a booking visitor, a scheduled job: spec D-12, D-13);
//   - concurrent renewals for one owner and provider in this process are
//     serialised, so a rotating provider never sees a refresh token twice
//     (AC-5);
//   - a permanent refusal (OAuth invalid_grant) records a loss for the owner
//     and writes one connection_lost entry per loss (AC-8, AC-9, AC-25);
//     a temporary failure records nothing (AC-10).

// ErrConnectionLost matches every LostError with errors.Is.
var ErrConnectionLost = errors.New("connection lost")

// LostError is returned when a stored credential cannot be renewed: the
// provider permanently refused it (Recorded: the loss was recorded for the
// owner), or it has expired and holds no refresh token (not Recorded: such a
// credential was never reported connected, so no loss starts).
type LostError struct {
	Provider string
	Recorded bool
	Err      error
}

func (e *LostError) Error() string {
	return fmt.Sprintf("%s: connection lost: %v", e.Provider, e.Err)
}

func (e *LostError) Unwrap() error        { return e.Err }
func (e *LostError) Is(target error) bool { return target == ErrConnectionLost }

var errNoRefreshToken = errors.New("access token expired and no refresh token is stored")

// IsPermanentRefusal reports whether err is the provider refusing a renewal
// for good (RFC 6749 invalid_grant: revoked, expired refresh token, consent
// withdrawn). Anything else — a network error, a timeout, a 5xx — is
// temporary.
func IsPermanentRefusal(err error) bool {
	var re *oauth2.RetrieveError
	return errors.As(err, &re) && re.ErrorCode == "invalid_grant"
}

// RecordLoss marks owner's credential for provider as lost and, when this
// starts a loss, writes one connection_lost entry to the owner's own log
// (best effort, like every activity entry: spec D-16). The entry names only
// the provider: never the requester, never token material.
// It reports whether the loss is recorded (started now or already open).
func RecordLoss(ctx context.Context, db storage.DBTX, owner uuid.UUID, provider string) bool {
	started, err := storage.RecordCredentialLossCtx(ctx, db, owner, provider)
	if err != nil {
		log.Printf("credential: %s loss for user %s not recorded: %v", provider, owner, err)
		return false
	}
	if started {
		storage.WriteAuditLogCtx(ctx, db, owner, "connection_lost", providerDetails(provider))
	}
	return true
}

func providerDetails(provider string) string {
	data, _ := json.Marshal(struct {
		Provider string `json:"provider"`
	}{provider})
	return string(data)
}

// credentialStore reads and writes one owner's stored credential. Every
// call runs on the context it is given: the renewal's request context, or
// the finishing context below.
type credentialStore interface {
	load(ctx context.Context) (*oauth2.Token, error)
	save(ctx context.Context, t *oauth2.Token) error
}

// finishBound bounds the finishing step of a renewal (PAC-80 item 4, spec
// D-6): once the provider has answered, the loss decision (reload, loss
// record, activity entry) or the save of the renewed credential runs on the
// request's values, so the session re-check still applies, but no longer
// ends when the requester hangs up; this bound keeps a stuck database from
// holding it longer. A variable only so that tests can shorten it.
var finishBound = 5 * time.Second

// finishing returns the context of a renewal's finishing step: ctx's values
// without its cancellation, bounded by finishBound.
func finishing(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), finishBound)
}

// renewalRefused reports whether a reload error means the renewal must not
// reach the provider (PAC-80 item 3, spec D-5): the request's session is
// stale, or its generation cannot be read. Nothing could be stored for such
// a request, so a provider call would be wasted, and with a rotating
// provider it would burn the stored refresh token.
func renewalRefused(err error) bool {
	return errors.Is(err, storage.ErrStaleSession) || errors.Is(err, storage.ErrGenerationUnavailable)
}

// ownerLocks serialises renewal per provider and owner in this process.
var ownerLocks sync.Map

func ownerLock(provider string, owner uuid.UUID) *sync.Mutex {
	v, _ := ownerLocks.LoadOrStore(provider+":"+owner.String(), &sync.Mutex{})
	return v.(*sync.Mutex)
}

type renewingSource struct {
	ctx      context.Context
	db       storage.DBTX
	owner    uuid.UUID
	provider string
	cfg      *oauth2.Config
	store    credentialStore
	// zeroExpiryIsExpired: a credential with no stored expiry (Zoom before
	// PAC-67) is renewed rather than trusted forever.
	zeroExpiryIsExpired bool

	mu  sync.Mutex
	cur *oauth2.Token
}

func (s *renewingSource) usable(t *oauth2.Token) bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	if s.zeroExpiryIsExpired && t.Expiry.IsZero() {
		return false
	}
	return t.Valid()
}

func (s *renewingSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usable(s.cur) {
		return copyToken(s.cur), nil
	}

	l := ownerLock(s.provider, s.owner)
	l.Lock()
	defer l.Unlock()

	// Another action may have renewed (and rotated) it meanwhile, or the owner
	// may have reconnected: the stored credential is the latest one.
	latest, lerr := s.store.load(s.ctx)
	if errors.Is(lerr, secretbox.ErrDecrypt) {
		// PAC-84 (AC-16): never renew with, or classify, a credential that
		// cannot be decrypted: no provider call, no loss recorded.
		return nil, lerr
	}
	if renewalRefused(lerr) {
		// PAC-80 item 3: no provider call, no loss, nothing stored; the
		// request is refused as stale (401) or unavailable (500).
		return nil, lerr
	}
	if lerr == nil && latest != nil {
		s.cur = latest
		if s.usable(latest) {
			return copyToken(latest), nil
		}
	}
	if s.cur == nil || s.cur.RefreshToken == "" {
		return nil, &LostError{Provider: s.provider, Err: errNoRefreshToken}
	}
	if s.cfg == nil {
		return nil, fmt.Errorf("%s: renew credential: provider is not configured", s.provider)
	}

	refresh := s.cur.RefreshToken
	renewed, err := s.cfg.TokenSource(outbound.WithIdentityClient(s.ctx), &oauth2.Token{RefreshToken: refresh}).Token()
	if err != nil {
		if !IsPermanentRefusal(err) {
			return nil, fmt.Errorf("%s: renew credential: %w", s.provider, err)
		}
		// Only a refusal of the credential still stored is a loss: one that a
		// reconnect replaced (or a disconnect removed) meanwhile is not.
		// PAC-80 item 4: the provider has answered, so the decision and the
		// record finish even if the requester has gone (bounded, re-checked).
		if s.recordLossIfStillStored(refresh) {
			return nil, &LostError{Provider: s.provider, Recorded: true, Err: err}
		}
		return nil, &LostError{Provider: s.provider, Err: err}
	}
	if renewed.RefreshToken == "" {
		renewed.RefreshToken = refresh
	}
	// PAC-80 item 4: a rotating provider has already retired the old refresh
	// token, so the renewal is stored even if the requester has gone.
	fctx, cancel := finishing(s.ctx)
	err = s.store.save(fctx, renewed)
	cancel()
	if err != nil {
		log.Printf("credential: renewed %s token for user %s not stored: %v", s.provider, s.owner, err)
	}
	s.cur = renewed
	return copyToken(renewed), nil
}

// recordLossIfStillStored is the finishing step of a permanent refusal of
// refresh: it reloads the stored credential and, when it still holds refresh,
// records the loss with its activity entry. It runs on the request's values
// (the re-check applies: a stale session records nothing) under finishBound,
// not on the request's cancellation. It reports whether the loss is recorded.
func (s *renewingSource) recordLossIfStillStored(refresh string) bool {
	fctx, cancel := finishing(s.ctx)
	defer cancel()
	latest, err := s.store.load(fctx)
	if err != nil {
		log.Printf("credential: %s loss for user %s not decided: %v", s.provider, s.owner, err)
		return false
	}
	if latest == nil || latest.RefreshToken != refresh {
		return false
	}
	return RecordLoss(fctx, s.db, s.owner, s.provider)
}

func copyToken(t *oauth2.Token) *oauth2.Token {
	c := *t
	return &c
}

// UserTokenSource renews owner's stored Google credential (PAC-67). It is the
// only way a Google calendar client is built from a stored credential.
func UserTokenSource(ctx context.Context, db storage.DBTX, cfg *oauth2.Config, owner uuid.UUID, tok *oauth2.Token) oauth2.TokenSource {
	return &renewingSource{ctx: ctx, db: db, owner: owner, provider: storage.CredentialGoogle, cfg: cfg, store: googleStore{db, owner}, cur: tok}
}

// MicrosoftTokenSource renews owner's stored Outlook (Microsoft) credential.
// A nil cfg (Microsoft not configured) cannot renew: a temporary failure.
func MicrosoftTokenSource(ctx context.Context, db storage.DBTX, cfg *MicrosoftConfig, owner uuid.UUID, tok *oauth2.Token) oauth2.TokenSource {
	var oc *oauth2.Config
	if cfg != nil {
		oc = cfg.oauthConfig()
	}
	return &renewingSource{ctx: ctx, db: db, owner: owner, provider: storage.CredentialOutlook, cfg: oc, store: microsoftStore{db, owner}, cur: tok}
}

// ZoomTokenSource renews owner's stored Zoom credential. cfg is Zoom's token
// endpoint with client credentials in Basic auth; nil cannot renew.
func ZoomTokenSource(ctx context.Context, db storage.DBTX, cfg *oauth2.Config, owner uuid.UUID, cred *storage.ZoomCredential) oauth2.TokenSource {
	return &renewingSource{ctx: ctx, db: db, owner: owner, provider: storage.CredentialZoom, cfg: cfg, store: zoomStore{db, owner}, cur: zoomToken(cred), zeroExpiryIsExpired: true}
}

func zoomToken(c *storage.ZoomCredential) *oauth2.Token {
	if c == nil {
		return nil
	}
	t := &oauth2.Token{AccessToken: c.AccessToken, RefreshToken: c.RefreshToken}
	if c.Expiry != nil {
		t.Expiry = *c.Expiry
	}
	return t
}

type googleStore struct {
	db    storage.DBTX
	owner uuid.UUID
}

func (g googleStore) load(ctx context.Context) (*oauth2.Token, error) {
	return LoadUserTokenCtx(ctx, g.db, g.owner)
}
func (g googleStore) save(ctx context.Context, t *oauth2.Token) error {
	return storage.WithSessionGeneration(ctx, g.db, func(q storage.Querier) error { return UpdateUserToken(q, g.owner, t) })
}

type microsoftStore struct {
	db    storage.DBTX
	owner uuid.UUID
}

func (m microsoftStore) load(ctx context.Context) (*oauth2.Token, error) {
	return LoadMicrosoftTokenCtx(ctx, m.db, m.owner)
}
func (m microsoftStore) save(ctx context.Context, t *oauth2.Token) error {
	data, err := json.Marshal(microsoftTokenRecord{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Expiry: t.Expiry.UTC()})
	if err != nil {
		return err
	}
	return storage.UpdateMicrosoftCredentialCtx(ctx, m.db, m.owner, string(data))
}

type zoomStore struct {
	db    storage.DBTX
	owner uuid.UUID
}

func (z zoomStore) load(ctx context.Context) (*oauth2.Token, error) {
	c, err := storage.LoadZoomCredentialCtx(ctx, z.db, z.owner)
	return zoomToken(c), err
}

func (z zoomStore) save(ctx context.Context, t *oauth2.Token) error {
	c := storage.ZoomCredential{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken}
	if !t.Expiry.IsZero() {
		e := t.Expiry
		c.Expiry = &e
	}
	return storage.UpdateZoomCredentialCtx(ctx, z.db, z.owner, c)
}
