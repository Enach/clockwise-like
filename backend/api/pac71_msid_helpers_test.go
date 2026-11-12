package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
)

// PAC-71 Microsoft sign-in ID tokens in tests (contract revision 3.1): a
// test RSA key, served as every tenant's published keys by the fake
// provider's /keys/{tid}, and a signer. Fixtures set claims and the header;
// the default is a token that passes every check.

const testMicrosoftKid = "pac71-test-kid"

var (
	testMSKeyOnce sync.Once
	testMSKey     *rsa.PrivateKey
)

func testMicrosoftKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	testMSKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testMSKey = k
	})
	return testMSKey
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// testMicrosoftJWKS is the keys document publishing the test key under kids.
func testMicrosoftJWKS(t testing.TB, kids ...string) []byte {
	k := testMicrosoftKey(t)
	keys := []any{}
	for _, kid := range kids {
		keys = append(keys, map[string]any{"kty": "RSA", "use": "sig", "kid": kid,
			"n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())})
	}
	b, _ := json.Marshal(map[string]any{"keys": keys})
	return b
}

// idTokenSpec is one ID token: header and claims (nil values are dropped).
type idTokenSpec struct {
	header map[string]any
	claims map[string]any
	// key signs the token; nil means the test key.
	key *rsa.PrivateKey
}

func signMicrosoftIDToken(t testing.TB, s idTokenSpec) string {
	t.Helper()
	h := map[string]any{"alg": "RS256", "typ": "JWT", "kid": testMicrosoftKid}
	for k, v := range s.header {
		if v == nil {
			delete(h, k)
		} else {
			h[k] = v
		}
	}
	c := map[string]any{}
	for k, v := range s.claims {
		if v != nil {
			c[k] = v
		}
	}
	hb, _ := json.Marshal(h)
	cb, _ := json.Marshal(c)
	in := b64(hb) + "." + b64(cb)
	key := s.key
	if key == nil {
		key = testMicrosoftKey(t)
	}
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return in + "." + b64(sig)
}

func (p *fakeOAuthProvider) tenantID() string {
	if p.tenant != "" {
		return p.tenant
	}
	return pac35Tenant
}

// profileID reads the object id the fake Graph /me answers.
func (p *fakeOAuthProvider) profileID() string {
	rec := httptest.NewRecorder()
	p.profile(rec)
	var me struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	return me.ID
}

// defaultIDClaims is a token for the sign-in whose state is state, for the
// account /me describes, in p's tenant: every check passes.
func (p *fakeOAuthProvider) defaultIDClaims(state string) idTokenSpec {
	tid := p.tenantID()
	now := time.Now()
	return idTokenSpec{claims: map[string]any{
		"iss": "https://login.microsoftonline.com/" + tid + "/v2.0", "ver": "2.0",
		"aud": "ms-client", "tid": tid, "oid": p.oidForToken(),
		"nonce": auth.MicrosoftNonce(state), "iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(),
		"exp": now.Add(time.Hour).Unix(), "sub": "pairwise-sub",
	}}
}

// msKeysStub serves the keys documents and counts the fetches per tenant.
type msKeysStub struct {
	mu     sync.Mutex
	hits   map[string]int
	status int           // non-zero: answer this status
	delay  time.Duration // sleep before answering
	kids   []string      // published kids (default testMicrosoftKid)
}

func (s *msKeysStub) count(tid string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[tid]
}

// useTestMicrosoftKeys points the sign-in verifier at a fresh cache and at a
// keys stub that publishes the test key for every tenant.
func useTestMicrosoftKeys(t *testing.T, p *fakeOAuthProvider) *msKeysStub {
	t.Helper()
	stub := &msKeysStub{hits: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := strings.TrimPrefix(r.URL.Path, "/keys/")
		stub.mu.Lock()
		stub.hits[tid]++
		status, delay, kids := stub.status, stub.delay, stub.kids
		stub.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		if status != 0 {
			http.Error(w, "unavailable", status)
			return
		}
		if len(kids) == 0 {
			kids = []string{testMicrosoftKid}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(testMicrosoftJWKS(t, kids...))
	}))
	t.Cleanup(srv.Close)
	prevURL, prevVerifier := auth.MicrosoftKeysURL, microsoftIDTokens
	auth.MicrosoftKeysURL = func(tid string) string { return srv.URL + "/keys/" + tid }
	microsoftIDTokens = auth.NewMicrosoftIDTokenVerifier()
	t.Cleanup(func() { auth.MicrosoftKeysURL, microsoftIDTokens = prevURL, prevVerifier })
	_ = p
	return stub
}

var (
	otherMSKeyOnce sync.Once
	otherMSKey     *rsa.PrivateKey
)

// otherKey is an RSA key Microsoft never published.
func otherKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	otherMSKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		otherMSKey = k
	})
	return otherMSKey
}

// oidForToken is /me's id when it is a GUID, else a fixed GUID: a fixture
// whose /me fails or is malformed still gets a valid token, so the sign-in
// reaches /me (step 3) as before PAC-71.
func (p *fakeOAuthProvider) oidForToken() string {
	if id := p.profileID(); auth.IsGUID(id) {
		return id
	}
	return "0a5e0000-0000-4000-8000-00000000ffff"
}
