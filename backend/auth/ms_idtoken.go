package auth

import (
	"container/list"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/outbound"
)

// PAC-71 Microsoft sign-in ID-token verifier (contract revision 3.1,
// microsoftOAuthCallback SIGN-IN step 2; owner OQ-8 and OQ-9, 2026-11-01).
//
// It is tenant-pinned: the tenant id is checked to be a canonical GUID before
// any key is fetched, and the keys come from that tenant's own discovery
// document. PAC-89: it is also the PAC-50 step-up's Microsoft verifier (one
// implementation of the rules, spec D-1). A verifier's key address and
// expected issuer are its own (KeysURL, Issuer): sign-in's are Microsoft's,
// the step-up's follow the identity-provider decision (config.IdPEndpoint),
// and every cache entry is identified by the key address it was fetched
// from, never by the tenant alone (contract PAC-89 section 2.2, AC-18).

// MicrosoftConsumerTenant is Microsoft's documented tenant id of every
// personal Microsoft account.
const MicrosoftConsumerTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"

// Limits of the key cache (contract revision 3.1, 3.1-8). Each fetch is bounded
// by the identity-provider class of PAC-81 Part A (outbound, 10 s; 3.1-7).
const (
	msKeysTTL           = 24 * time.Hour
	msKeysRefetchPeriod = time.Minute
	msKeysMaxTenants    = 1000
	msClockSkew         = 60 * time.Second
)

// MicrosoftKeysURL is the published keys document of a tenant. A test seam:
// tests point it at a stub serving a test key.
var MicrosoftKeysURL = func(tid string) string {
	return "https://login.microsoftonline.com/" + tid + "/discovery/v2.0/keys"
}

// microsoftIssuer is the production v2.0 issuer template of sign-in.
const microsoftIssuer = "https://login.microsoftonline.com/" + config.TenantPlaceholder + "/v2.0"

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

// MicrosoftIDClaims is what an accepted ID token says to the PAC-50 step-up:
// the identity plus the claims its admin checks read (PAC-89 contract 2.1).
type MicrosoftIDClaims struct {
	MicrosoftIdentity
	Email             string
	PreferredUsername string
	WIDs              []string
	WIDsPresent       bool
}

// MicrosoftIDTokenVerifier verifies sign-in ID tokens. Its key cache is per
// tenant: kept up to 24 hours, refetched at most once a minute on an unknown
// kid, at most 1000 tenants with the least recently used evicted first. The
// first fetch for a tenant that is not cached (new, expired or evicted) is
// not subject to the once-a-minute limit. Fetches are single-flight per
// tenant: a request that needs the tenant's keys while a fetch for them is in
// flight waits for that fetch instead of starting one or being refused, so a
// key rotation costs one fetch however many sign-ins arrive at once. Safe for
// concurrent use.
type MicrosoftIDTokenVerifier struct {
	HTTPClient *http.Client
	Now        func() time.Time
	// KeysURL is the per-tenant key address. Nil means MicrosoftKeysURL,
	// read at each verification (sign-in, and its test seam).
	KeysURL func(tid string) string
	// Issuer is the expected `iss`, with config.TenantPlaceholder standing
	// for the tenant. Empty means Microsoft's v2.0 issuer.
	Issuer string

	mu      sync.Mutex
	tenants map[string]*list.Element // key address -> element holding *msTenantKeys
	flights map[string]*msFlight     // key address -> the fetch in flight
	lru     *list.List               // front = most recently used
	fetches int                      // number of key fetches made (for tests)
}

// msFlight is one key fetch in flight; done closes when keys/err are set.
type msFlight struct {
	done chan struct{}
	keys map[string]*rsa.PublicKey
	err  error
}

type msTenantKeys struct {
	addr        string // the key address the keys were fetched from
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastRefetch time.Time
}

// NewMicrosoftIDTokenVerifier returns a verifier with an empty cache.
func NewMicrosoftIDTokenVerifier() *MicrosoftIDTokenVerifier {
	return &MicrosoftIDTokenVerifier{
		Now:     time.Now,
		tenants: map[string]*list.Element{},
		flights: map[string]*msFlight{},
		lru:     list.New(),
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
	id, _, err := v.verify(ctx, raw, clientID, nonce)
	return id, err
}

// msStepupClaims is the part of the payload only the step-up reads.
type msStepupClaims struct {
	Email             string    `json:"email"`
	PreferredUsername string    `json:"preferred_username"`
	WIDs              *[]string `json:"wids"`
}

// VerifyClaims applies exactly Verify's rules and, for an accepted token,
// also returns the claims the PAC-50 step-up reads (PAC-89 spec D-1).
func (v *MicrosoftIDTokenVerifier) VerifyClaims(ctx context.Context, raw, clientID, nonce string) (*MicrosoftIDClaims, error) {
	id, payload, err := v.verify(ctx, raw, clientID, nonce)
	if err != nil {
		return nil, err
	}
	var c msStepupClaims
	if err := decodeSegment(payload, &c); err != nil {
		return nil, idTokenErr("payload: %v", err)
	}
	out := &MicrosoftIDClaims{MicrosoftIdentity: *id, Email: c.Email, PreferredUsername: c.PreferredUsername}
	if c.WIDs != nil {
		out.WIDs, out.WIDsPresent = *c.WIDs, true
	}
	return out, nil
}

// keysURL is this verifier's key address for tid (a lower-case GUID).
func (v *MicrosoftIDTokenVerifier) keysURL(tid string) string {
	if v.KeysURL != nil {
		return v.KeysURL(tid)
	}
	return MicrosoftKeysURL(tid)
}

func (v *MicrosoftIDTokenVerifier) issuer() string {
	if v.Issuer != "" {
		return v.Issuer
	}
	return microsoftIssuer
}

// verify applies every rule and returns the identity and the payload
// segment it was read from.
func (v *MicrosoftIDTokenVerifier) verify(ctx context.Context, raw, clientID, nonce string) (*MicrosoftIdentity, string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, "", idTokenErr("not a compact JWS")
	}
	var h msIDHeader
	if err := decodeSegment(parts[0], &h); err != nil {
		return nil, "", idTokenErr("header: %v", err)
	}
	if h.Alg != "RS256" {
		return nil, "", idTokenErr("alg %q is not RS256", h.Alg)
	}
	if h.Kid == "" {
		return nil, "", idTokenErr("no kid")
	}
	var c msIDClaims
	if err := decodeSegment(parts[1], &c); err != nil {
		return nil, "", idTokenErr("payload: %v", err)
	}
	// The tenant is checked before anything is fetched, so no other value
	// ever reaches a URL.
	if !IsGUID(c.Tid) {
		return nil, "", idTokenErr("tid is not a GUID")
	}
	tid := strings.ToLower(c.Tid)
	key, err := v.key(ctx, v.keysURL(tid), h.Kid)
	if err != nil {
		return nil, "", err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, "", idTokenErr("signature encoding")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return nil, "", idTokenErr("signature does not verify")
	}
	if !issuerNames(v.issuer(), c.Iss, tid) {
		return nil, "", idTokenErr("iss %q does not name the token's tenant", c.Iss)
	}
	if c.Ver != "2.0" {
		return nil, "", idTokenErr("ver %q is not 2.0", c.Ver)
	}
	if !audienceIs(c.Aud, clientID) {
		return nil, "", idTokenErr("aud is not the client id")
	}
	if !IsGUID(c.Oid) {
		return nil, "", idTokenErr("oid absent or not a GUID")
	}
	now := v.Now()
	if c.Exp == nil {
		return nil, "", idTokenErr("no exp")
	}
	exp, err := c.Exp.Int64()
	if err != nil || !now.Before(time.Unix(exp, 0).Add(msClockSkew)) {
		return nil, "", idTokenErr("expired")
	}
	if c.Nbf != nil {
		nbf, err := c.Nbf.Int64()
		if err != nil || now.Add(msClockSkew).Before(time.Unix(nbf, 0)) {
			return nil, "", idTokenErr("not yet valid")
		}
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1 {
		return nil, "", idTokenErr("nonce does not match the request")
	}
	return &MicrosoftIdentity{TenantID: tid, ObjectID: strings.ToLower(c.Oid)}, parts[1], nil
}

// issuerNames reports whether iss is template (for sign-in
// "https://login.microsoftonline.com/{tenantid}/v2.0") naming tenant tid
// (lower-case): the scheme, host and path compared exactly and the tenant
// part compared case-insensitively (a GUID's hex digits may come in either
// case).
func issuerNames(template, iss, tid string) bool {
	prefix, suffix, ok := strings.Cut(template, config.TenantPlaceholder)
	if !ok {
		return false
	}
	if !strings.HasPrefix(iss, prefix) || !strings.HasSuffix(iss, suffix) || len(iss) < len(prefix)+len(suffix) {
		return false
	}
	return strings.EqualFold(iss[len(prefix):len(iss)-len(suffix)], tid)
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

// key returns the key kid published at addr (one tenant's key address),
// fetching the keys when they are not cached or older than 24 h, and
// refetching at most once a minute when kid is unknown. A request that finds
// a fetch for addr in flight waits for it (single flight) and then looks kid
// up in what it returned.
func (v *MicrosoftIDTokenVerifier) key(ctx context.Context, addr, kid string) (*rsa.PublicKey, error) {
	now := v.Now()
	v.mu.Lock()
	entry := v.lookup(addr)
	fresh := entry != nil && now.Sub(entry.fetchedAt) < msKeysTTL
	if fresh {
		if k, ok := entry.keys[kid]; ok {
			v.mu.Unlock()
			return k, nil
		}
	}
	if f, ok := v.flights[addr]; ok {
		v.mu.Unlock()
		select {
		case <-f.done:
		case <-ctx.Done():
			return nil, idTokenErr("waiting for the tenant's keys: %v", ctx.Err())
		}
		return pick(f.keys, f.err, kid)
	}
	if fresh {
		if !entry.lastRefetch.IsZero() && now.Sub(entry.lastRefetch) < msKeysRefetchPeriod {
			v.mu.Unlock()
			return nil, idTokenErr("kid %q unknown and the tenant's refetch is rate-limited", kid)
		}
		entry.lastRefetch = now
	}
	f := &msFlight{done: make(chan struct{})}
	v.flights[addr] = f
	v.mu.Unlock()

	// PAC-80 item 5: the flight is always ended, in a defer, so a fetch that
	// does not return normally can never leave it registered (waiters would
	// otherwise block until their own deadlines, and every later sign-in for
	// this key address with them, until a restart). Unless the fetch
	// returned keys, it ends with an error and nothing is cached.
	var keys map[string]*rsa.PublicKey
	err := idTokenErr("keys fetch did not complete")
	defer func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		if err == nil {
			e := v.store(addr, keys, now)
			if fresh {
				e.lastRefetch = entry.lastRefetch
			}
		}
		f.keys, f.err = keys, err
		delete(v.flights, addr)
		close(f.done)
	}()

	// The fetch serves every request waiting on it, so it does not end with
	// this request's context; it is bounded by the identity-provider limit.
	keys, err = v.fetchRecovering(context.WithoutCancel(ctx), addr)
	return pick(keys, err, kid)
}

// fetchRecovering is fetch with a panic (for instance from a custom HTTP
// client's transport) turned into the fetch's error for the leader and every
// waiter (PAC-80 item 5, spec D-7). The panic is logged once, with the key
// address and the panic value; the fetch handles no token material.
func (v *MicrosoftIDTokenVerifier) fetchRecovering(ctx context.Context, addr string) (keys map[string]*rsa.PublicKey, err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("microsoft id token: keys fetch from %s panicked: %v", addr, p)
			keys, err = nil, idTokenErr("keys fetch panicked")
		}
	}()
	return v.fetch(ctx, addr)
}

func pick(keys map[string]*rsa.PublicKey, err error, kid string) (*rsa.PublicKey, error) {
	if err != nil {
		return nil, err
	}
	k, ok := keys[kid]
	if !ok {
		return nil, idTokenErr("kid %q is not published by the tenant", kid)
	}
	return k, nil
}

// lookup returns the cache entry of a key address and marks it recently used.
// Callers hold v.mu.
func (v *MicrosoftIDTokenVerifier) lookup(addr string) *msTenantKeys {
	el, ok := v.tenants[addr]
	if !ok {
		return nil
	}
	v.lru.MoveToFront(el)
	return el.Value.(*msTenantKeys)
}

// store caches keys fetched from addr, evicting the least recently used entry
// beyond the cap. Callers hold v.mu.
func (v *MicrosoftIDTokenVerifier) store(addr string, keys map[string]*rsa.PublicKey, now time.Time) *msTenantKeys {
	if el, ok := v.tenants[addr]; ok {
		e := el.Value.(*msTenantKeys)
		e.keys, e.fetchedAt = keys, now
		v.lru.MoveToFront(el)
		return e
	}
	e := &msTenantKeys{addr: addr, keys: keys, fetchedAt: now}
	v.tenants[addr] = v.lru.PushFront(e)
	for v.lru.Len() > msKeysMaxTenants {
		last := v.lru.Back()
		v.lru.Remove(last)
		delete(v.tenants, last.Value.(*msTenantKeys).addr)
	}
	return e
}

// Cached reports whether tid's keys, at this verifier's key address for tid,
// are cached (for tests).
func (v *MicrosoftIDTokenVerifier) Cached(tid string) bool {
	addr := v.keysURL(strings.ToLower(tid))
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.tenants[addr]
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

func (v *MicrosoftIDTokenVerifier) fetch(ctx context.Context, addr string) (map[string]*rsa.PublicKey, error) {
	v.mu.Lock()
	v.fetches++
	v.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, outbound.Current().Identity)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, idTokenErr("keys request: %v", err)
	}
	client := v.HTTPClient
	if client == nil {
		// PAC-81 Part A: the identity-provider class, 10 s.
		client = outbound.IdentityClient()
	}
	resp, err := client.Do(req)
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
