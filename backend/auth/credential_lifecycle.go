package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

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
func RecordLoss(db *sql.DB, owner uuid.UUID, provider string) {
	started, err := storage.RecordCredentialLoss(db, owner, provider)
	if err != nil {
		log.Printf("credential: %s loss for user %s not recorded: %v", provider, owner, err)
		return
	}
	if started {
		storage.WriteAuditLog(db, owner, "connection_lost", providerDetails(provider))
	}
}

func providerDetails(provider string) string {
	data, _ := json.Marshal(struct {
		Provider string `json:"provider"`
	}{provider})
	return string(data)
}

// credentialStore reads and writes one owner's stored credential.
type credentialStore interface {
	load() (*oauth2.Token, error)
	save(*oauth2.Token) error
}

// ownerLocks serialises renewal per provider and owner in this process.
var ownerLocks sync.Map

func ownerLock(provider string, owner uuid.UUID) *sync.Mutex {
	v, _ := ownerLocks.LoadOrStore(provider+":"+owner.String(), &sync.Mutex{})
	return v.(*sync.Mutex)
}

type renewingSource struct {
	ctx      context.Context
	db       *sql.DB
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
	if latest, err := s.store.load(); err == nil && latest != nil {
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
	renewed, err := s.cfg.TokenSource(s.ctx, &oauth2.Token{RefreshToken: refresh}).Token()
	if err != nil {
		if !IsPermanentRefusal(err) {
			return nil, fmt.Errorf("%s: renew credential: %w", s.provider, err)
		}
		// Only a refusal of the credential still stored is a loss: one that a
		// reconnect replaced (or a disconnect removed) meanwhile is not.
		if latest, lerr := s.store.load(); lerr == nil && latest != nil && latest.RefreshToken == refresh {
			RecordLoss(s.db, s.owner, s.provider)
			return nil, &LostError{Provider: s.provider, Recorded: true, Err: err}
		}
		return nil, &LostError{Provider: s.provider, Err: err}
	}
	if renewed.RefreshToken == "" {
		renewed.RefreshToken = refresh
	}
	if err := s.store.save(renewed); err != nil {
		log.Printf("credential: renewed %s token for user %s not stored: %v", s.provider, s.owner, err)
	}
	s.cur = renewed
	return copyToken(renewed), nil
}

func copyToken(t *oauth2.Token) *oauth2.Token {
	c := *t
	return &c
}

// UserTokenSource renews owner's stored Google credential (PAC-67). It is the
// only way a Google calendar client is built from a stored credential.
func UserTokenSource(ctx context.Context, db *sql.DB, cfg *oauth2.Config, owner uuid.UUID, tok *oauth2.Token) oauth2.TokenSource {
	return &renewingSource{ctx: ctx, db: db, owner: owner, provider: storage.CredentialGoogle, cfg: cfg, store: googleStore{ctx, db, owner}, cur: tok}
}

// MicrosoftTokenSource renews owner's stored Outlook (Microsoft) credential.
// A nil cfg (Microsoft not configured) cannot renew: a temporary failure.
func MicrosoftTokenSource(ctx context.Context, db *sql.DB, cfg *MicrosoftConfig, owner uuid.UUID, tok *oauth2.Token) oauth2.TokenSource {
	var oc *oauth2.Config
	if cfg != nil {
		oc = cfg.oauthConfig()
	}
	return &renewingSource{ctx: ctx, db: db, owner: owner, provider: storage.CredentialOutlook, cfg: oc, store: microsoftStore{ctx, db, owner}, cur: tok}
}

// ZoomTokenSource renews owner's stored Zoom credential. cfg is Zoom's token
// endpoint with client credentials in Basic auth; nil cannot renew.
func ZoomTokenSource(ctx context.Context, db *sql.DB, cfg *oauth2.Config, owner uuid.UUID, cred *storage.ZoomCredential) oauth2.TokenSource {
	return &renewingSource{ctx: ctx, db: db, owner: owner, provider: storage.CredentialZoom, cfg: cfg, store: zoomStore{ctx, db, owner}, cur: zoomToken(cred), zeroExpiryIsExpired: true}
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
	ctx   context.Context
	db    *sql.DB
	owner uuid.UUID
}

func (g googleStore) load() (*oauth2.Token, error) { return LoadUserTokenCtx(g.ctx, g.db, g.owner) }
func (g googleStore) save(t *oauth2.Token) error   { return UpdateUserToken(g.db, g.owner, t) }

type microsoftStore struct {
	ctx   context.Context
	db    *sql.DB
	owner uuid.UUID
}

func (m microsoftStore) load() (*oauth2.Token, error) { return LoadMicrosoftTokenCtx(m.ctx, m.db, m.owner) }
func (m microsoftStore) save(t *oauth2.Token) error {
	data, err := json.Marshal(microsoftTokenRecord{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Expiry: t.Expiry.UTC()})
	if err != nil {
		return err
	}
	return storage.UpdateMicrosoftCredential(m.db, m.owner, string(data))
}

type zoomStore struct {
	ctx   context.Context
	db    *sql.DB
	owner uuid.UUID
}

func (z zoomStore) load() (*oauth2.Token, error) {
	c, err := storage.LoadZoomCredentialCtx(z.ctx, z.db, z.owner)
	return zoomToken(c), err
}

func (z zoomStore) save(t *oauth2.Token) error {
	c := storage.ZoomCredential{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken}
	if !t.Expiry.IsZero() {
		e := t.Expiry
		c.Expiry = &e
	}
	return storage.UpdateZoomCredential(z.db, z.owner, c)
}
