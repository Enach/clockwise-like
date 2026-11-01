package auth

import (
	"container/list"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PAC-71 Microsoft sign-in ID-token verifier (contract revision 3.1,
// microsoftOAuthCallback SIGN-IN step 2; owner OQ-8 and OQ-9, 2026-11-01).
//
// It is tenant-pinned and used by sign-in only: the tenant id is checked to
// be a canonical GUID before any key is fetched, and the keys come from that
// tenant's own discovery document. It is deliberately NOT the PAC-50 step-up
// verifier (stepup.go), which reads the /common keys.

// MicrosoftConsumerTenant is Microsoft's documented tenant id of every
// personal Microsoft account.
const MicrosoftConsumerTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"

// Limits of the key cache and fetch (contract revision 3.1, 3.1-7 and 3.1-8).
const (
	msKeysTTL           = 24 * time.Hour
	msKeysRefetchPeriod = time.Minute
	msKeysMaxTenants    = 1000
	msClockSkew         = 60 * time.Second
)

// MicrosoftKeysFetchTimeout bounds each keys fetch (the identity-provider
// class of PAC-81 Part A). A variable only so a test can shorten it.
var MicrosoftKeysFetchTimeout = 10 * time.Second

// MicrosoftKeysURL is the published keys document of a tenant. A test seam:
// tests point it at a stub serving a test key.
var MicrosoftKeysURL = func(tid string) string {
	return "https://login.microsoftonline.com/" + tid + "/discovery/v2.0/keys"
}

var msGUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsGUID reports whether s is a canonical 8-4-4-4-12 hex GUID.
func IsGUID(s string) bool { return msGUID.MatchString(s) }

// ErrMicrosoftIDToken is wrapped by every reason an ID token is not accepted.
var ErrMicrosoftIDToken = errors.New("microsoft id token rejected")

func idTokenErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMicrosoftIDToken, fmt.Sprintf(format, args...))
}

// MicrosoftIdentity is what an accepted sign-in ID token says.
type MicrosoftIdentity struct {
	TenantID string // lower-case GUID
	ObjectID string // lower-case GUID
}

// MicrosoftIDTokenVerifier verifies sign-in ID tokens. Its key cache is per
// tenant: kept up to 24 hours, refetched at most once a minute on an unknown
// kid, at most 1000 tenants with the least recently used evicted first. The
// first fetch for a tenant that is not cached (new, expired or evicted) is
// not subject to the once-a-minute limit. Safe for concurrent use.
type MicrosoftIDTokenVerifier struct {
	HTTPClient *http.Client
	Now        func() time.Time

	mu      sync.Mutex
	tenants map[string]*list.Element // tid -> element holding *msTenantKeys
	lru     *list.List               // front = most recently used
	fetches int                      // number of key fetches made (for tests)
}

type msTenantKeys struct {
	tid         string
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastRefetch time.Time
}

// NewMicrosoftIDTokenVerifier returns a verifier with an empty cache.
func NewMicrosoftIDTokenVerifier() *MicrosoftIDTokenVerifier {
	return &MicrosoftIDTokenVerifier{
		HTTPClient: &http.Client{},
		Now:        time.Now,
		tenants:    map[string]*list.Element{},
		lru:        list.New(),
	}
}

// Fetches returns how many key documents the verifier has fetched.
func (v *MicrosoftIDTokenVerifier) Fetches() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fetches
}

type msIDHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type msIDClaims struct {
	Iss   string          `json:"iss"`
	Ver   string          `json:"ver"`
	Aud   json.RawMessage `json:"aud"`
	Tid   string          `json:"tid"`
	Oid   string          `json:"oid"`
	Nonce string          `json:"nonce"`
	Exp   *json.Number    `json:"exp"`
	Nbf   *json.Number    `json:"nbf"`
}

// Verify accepts raw only when every check of the contract holds: compact
// JWS, RS256 with a kid, a GUID tid (checked before any fetch), a signature
// by that tenant's published key, iss and ver, aud = clientID (a string or a
// one-element array), a GUID oid, exp/nbf with 60 s of skew, and nonce.
func (v *MicrosoftIDTokenVerifier) Verify(ctx context.Context, raw, clientID, nonce string) (*MicrosoftIdentity, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, idTokenErr("not a compact JWS")
	}
	var h msIDHeader
	if err := decodeSegment(parts[0], &h); err != nil {
		return nil, idTokenErr("header: %v", err)
	}
	if h.Alg != "RS256" {
		return nil, idTokenErr("alg %q is not RS256", h.Alg)
	}
	if h.Kid == "" {
		return nil, idTokenErr("no kid")
	}
	var c msIDClaims
	if err := decodeSegment(parts[1], &c); err != nil {
		return nil, idTokenErr("payload: %v", err)
	}
	// The tenant is checked before anything is fetched, so no other value
	// ever reaches a URL.
	if !IsGUID(c.Tid) {
		return nil, idTokenErr("tid is not a GUID")
	}
	tid := strings.ToLower(c.Tid)
	key, err := v.key(ctx, tid, h.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, idTokenErr("signature encoding")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return nil, idTokenErr("signature does not verify")
	}
	if c.Iss != "https://login.microsoftonline.com/"+c.Tid+"/v2.0" {
		return nil, idTokenErr("iss %q does not name the token's tenant", c.Iss)
	}
	if c.Ver != "2.0" {
		return nil, idTokenErr("ver %q is not 2.0", c.Ver)
	}
	if !audienceIs(c.Aud, clientID) {
		return nil, idTokenErr("aud is not the client id")
	}
	if !IsGUID(c.Oid) {
		return nil, idTokenErr("oid absent or not a GUID")
	}
	now := v.Now()
	if c.Exp == nil {
		return nil, idTokenErr("no exp")
	}
	exp, err := c.Exp.Int64()
	if err != nil || !now.Before(time.Unix(exp, 0).Add(msClockSkew)) {
		return nil, idTokenErr("expired")
	}
	if c.Nbf != nil {
		nbf, err := c.Nbf.Int64()
		if err != nil || now.Add(msClockSkew).Before(time.Unix(nbf, 0)) {
			return nil, idTokenErr("not yet valid")
		}
	}
	if nonce == "" || c.Nonce != nonce {
		return nil, idTokenErr("nonce does not match the request")
	}
	return &MicrosoftIdentity{TenantID: tid, ObjectID: strings.ToLower(c.Oid)}, nil
}

func decodeSegment(seg string, dst any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	return dec.Decode(dst)
}

func audienceIs(raw json.RawMessage, clientID string) bool {
	if clientID == "" || len(raw) == 0 {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s == clientID
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return len(arr) == 1 && arr[0] == clientID
	}
	return false
}

// key returns the tenant's key kid, fetching the tenant's keys when they are
// not cached or older than 24 h, and refetching at most once a minute when
// kid is unknown.
func (v *MicrosoftIDTokenVerifier) key(ctx context.Context, tid, kid string) (*rsa.PublicKey, error) {
	now := v.Now()
	v.mu.Lock()
	entry := v.lookup(tid)
	if entry != nil && now.Sub(entry.fetchedAt) < msKeysTTL {
		if k, ok := entry.keys[kid]; ok {
			v.mu.Unlock()
			return k, nil
		}
		if !entry.lastRefetch.IsZero() && now.Sub(entry.lastRefetch) < msKeysRefetchPeriod {
			v.mu.Unlock()
			return nil, idTokenErr("kid %q unknown and the tenant's refetch is rate-limited", kid)
		}
		entry.lastRefetch = now
	}
	v.mu.Unlock()

	keys, err := v.fetch(ctx, tid)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	e := v.store(tid, keys, now)
	if entry != nil {
		e.lastRefetch = entry.lastRefetch
	}
	k, ok := keys[kid]
	if !ok {
		return nil, idTokenErr("kid %q is not published by the tenant", kid)
	}
	return k, nil
}

// lookup returns the tenant's cache entry and marks it recently used. Callers
// hold v.mu.
func (v *MicrosoftIDTokenVerifier) lookup(tid string) *msTenantKeys {
	el, ok := v.tenants[tid]
	if !ok {
		return nil
	}
	v.lru.MoveToFront(el)
	return el.Value.(*msTenantKeys)
}

// store caches keys for tid, evicting the least recently used tenant beyond
// the cap. Callers hold v.mu.
func (v *MicrosoftIDTokenVerifier) store(tid string, keys map[string]*rsa.PublicKey, now time.Time) *msTenantKeys {
	if el, ok := v.tenants[tid]; ok {
		e := el.Value.(*msTenantKeys)
		e.keys, e.fetchedAt = keys, now
		v.lru.MoveToFront(el)
		return e
	}
	e := &msTenantKeys{tid: tid, keys: keys, fetchedAt: now}
	v.tenants[tid] = v.lru.PushFront(e)
	for v.lru.Len() > msKeysMaxTenants {
		last := v.lru.Back()
		v.lru.Remove(last)
		delete(v.tenants, last.Value.(*msTenantKeys).tid)
	}
	return e
}

// Cached reports whether tid's keys are cached (for tests).
func (v *MicrosoftIDTokenVerifier) Cached(tid string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.tenants[strings.ToLower(tid)]
	return ok
}

type jwkSet struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (v *MicrosoftIDTokenVerifier) fetch(ctx context.Context, tid string) (map[string]*rsa.PublicKey, error) {
	v.mu.Lock()
	v.fetches++
	v.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, MicrosoftKeysFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, MicrosoftKeysURL(tid), nil)
	if err != nil {
		return nil, idTokenErr("keys request: %v", err)
	}
	resp, err := v.HTTPClient.Do(req)
	if err != nil {
		return nil, idTokenErr("keys fetch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, idTokenErr("keys fetch: status %d", resp.StatusCode)
	}
	var set jwkSet
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, idTokenErr("keys document: %v", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	return keys, nil
}
