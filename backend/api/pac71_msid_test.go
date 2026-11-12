package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/storage"
)

// PAC-71 Microsoft sign-in by the signed ID token (spec revision 5.1,
// contract revision 3.1): AC-5, AC-8, AC-11, AC-11b, AC-11c, AC-51.

// msRun is one Microsoft sign-in in tenant tid for (oid, mail); mutate
// changes the default (valid) ID token. orgCalls counts /organization reads.
type msRun struct {
	tid, oid, mail string
	org            func(http.ResponseWriter)
	mutate         func(*idTokenSpec)
	noIDToken      bool
	keys           func(*msKeysStub)
	orgCalls       *atomic.Int64
}

func (m msRun) do(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	p := newFakeOAuthProvider(t)
	p.tenant = m.tid
	p.profile = jsonProfile(map[string]string{"id": m.oid, "mail": m.mail, "displayName": "M"})
	org := m.org
	if org == nil {
		org = orgAnswer(m.tid, strings.SplitN(m.mail, "@", 2)[1])
	}
	p.organization = func(w http.ResponseWriter) {
		if m.orgCalls != nil {
			m.orgCalls.Add(1)
		}
		org(w)
	}
	p.keys = useTestMicrosoftKeys(t, p)
	if m.keys != nil {
		m.keys(p.keys)
	}
	p.idToken = func() string {
		if m.noIDToken {
			return ""
		}
		spec := p.defaultIDClaims("s1")
		if m.mutate != nil {
			m.mutate(&spec)
		}
		return signMicrosoftIDToken(t, spec)
	}
	return runMicrosoftCallback(t, pac71Handlers(t), p)
}

// AC-5, AC-11c: a personal account (the consumer tenant) is refused whatever
// it would have reached, before /organization is read; nothing is written.
func TestPAC71_AC5_AC11c_PersonalMicrosoftAccountIsRefused(t *testing.T) {
	d := pac71Domain()
	o1 := pac71GUID()
	henry := pac71Bound(t, "henry@"+d, msIdentity(pac71GUID(), o1), storage.AddressDomainConfirmed)
	legacy := pac71Legacy(t, "legacy@"+d, "microsoft", "")
	cases := map[string]msRun{
		"bound Henry":                 {oid: o1, mail: "henry@" + d},
		"unbound legacy account":      {oid: pac71GUID(), mail: "legacy@" + d},
		"AC-11c no account, evidence": {oid: pac71GUID(), mail: "erin@" + d},
	}
	hb, lb := readAccount(t, henry), readAccount(t, legacy)
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var org atomic.Int64
			c.tid, c.orgCalls = auth.MicrosoftConsumerTenant, &org
			c.org = orgAnswer(auth.MicrosoftConsumerTenant, d) // evidence it would carry
			assertRefused(t, c.do(t))
			if org.Load() != 0 {
				t.Fatal("/organization was read for a personal account")
			}
		})
	}
	if readAccount(t, henry) != hb || readAccount(t, legacy) != lb || carriersCount(t, "erin@"+d) != 0 {
		t.Fatal("a personal-account sign-in wrote something")
	}
}

// AC-11b: every way the ID token can fail is the refusal (not auth_failed),
// for (i) no carrier, (ii) an empty unbound legacy account, (iii) Henry
// bound to (T1, O1).
func TestPAC71_AC11b_IDTokenFailuresAreRefusedInEveryCase(t *testing.T) {
	failures := map[string]func(r *msRun){
		"no id_token":         func(r *msRun) { r.noIDToken = true },
		"alg not RS256":       func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.header = map[string]any{"alg": "RS384"} } },
		"alg none":            func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.header = map[string]any{"alg": "none"} } },
		"bad signature":       func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.key = otherKey(t) } },
		"kid not published":   func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.header = map[string]any{"kid": "rotated-away"} } },
		"no kid":              func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.header = map[string]any{"kid": nil} } },
		"keys endpoint error": func(r *msRun) { r.keys = func(k *msKeysStub) { k.status = http.StatusServiceUnavailable } },
		"tid absent":          func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.claims["tid"] = nil } },
		"tid not a GUID":      func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.claims["tid"] = "../../evil" } },
		"iss of another tenant": func(r *msRun) {
			r.mutate = func(s *idTokenSpec) { s.claims["iss"] = "https://login.microsoftonline.com/" + pac71GUID() + "/v2.0" }
		},
		"iss v1 shape": func(r *msRun) {
			r.mutate = func(s *idTokenSpec) { s.claims["iss"] = "https://sts.windows.net/" + s.claims["tid"].(string) + "/" }
		},
		"aud other client":      func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.claims["aud"] = "other-client" } },
		"aud two-element array": func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.claims["aud"] = []string{"ms-client", "x"} } },
		"aud empty array":       func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.claims["aud"] = []string{} } },
		"ver not 2.0":           func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.claims["ver"] = "1.0" } },
		"expired": func(r *msRun) {
			r.mutate = func(s *idTokenSpec) { s.claims["exp"] = time.Now().Add(-2 * time.Minute).Unix() }
		},
		"nbf in the future": func(r *msRun) {
			r.mutate = func(s *idTokenSpec) { s.claims["nbf"] = time.Now().Add(5 * time.Minute).Unix() }
		},
		"nonce absent": func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.claims["nonce"] = nil } },
		"nonce of another state": func(r *msRun) {
			r.mutate = func(s *idTokenSpec) { s.claims["nonce"] = auth.MicrosoftNonce("attacker") }
		},
		"oid absent":     func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.claims["oid"] = nil } },
		"oid not a GUID": func(r *msRun) { r.mutate = func(s *idTokenSpec) { s.claims["oid"] = "not-a-guid" } },
		"/me id differs from oid": func(r *msRun) {
			r.mutate = func(s *idTokenSpec) { s.claims["oid"] = pac71GUID() }
		},
	}
	for name, fail := range failures {
		t.Run(name, func(t *testing.T) {
			d := pac71Domain()
			t1, o1 := pac71GUID(), pac71GUID()
			check := func(label string, r msRun, before func() bool) {
				t.Helper()
				fail(&r)
				assertRefused(t, r.do(t))
				if !before() {
					t.Fatalf("%s: something was written", label)
				}
			}
			check("(i) no carrier", msRun{tid: t1, oid: pac71GUID(), mail: "new@" + d}, func() bool { return carriersCount(t, "new@"+d) == 0 })
			legacy := pac71Legacy(t, "legacy@"+d, "microsoft", "")
			lb := readAccount(t, legacy)
			check("(ii) empty legacy", msRun{tid: t1, oid: pac71GUID(), mail: "legacy@" + d}, func() bool { return readAccount(t, legacy) == lb })
			henry := pac71Bound(t, "henry@"+d, msIdentity(t1, o1), storage.AddressDomainConfirmed)
			hb := readAccount(t, henry)
			check("(iii) bound Henry", msRun{tid: t1, oid: o1, mail: "henry@" + d}, func() bool { return readAccount(t, henry) == hb })
		})
	}
}

func TestPAC71_AC11b_KeysFetchBeyondTheLimitIsRefused(t *testing.T) {
	lim := outbound.Current()
	lim.Identity = 50 * time.Millisecond // stands in for 10 s
	t.Cleanup(outbound.SetForTest(lim))
	d := pac71Domain()
	r := msRun{tid: pac71GUID(), oid: pac71GUID(), mail: "slow@" + d, keys: func(k *msKeysStub) { k.delay = 300 * time.Millisecond }}
	assertRefused(t, r.do(t))
	if carriersCount(t, "slow@"+d) != 0 {
		t.Fatal("created")
	}
}

// The key cache: a first fetch for an uncached tenant is never rate-limited;
// an unknown kid triggers one refetch per tenant per minute; a kid still
// unknown while the refetch is rate-limited is refused.
func TestPAC71_AC11b_KeyCacheRefetchIsRateLimitedPerTenant(t *testing.T) {
	d := pac71Domain()
	t1, o1 := pac71GUID(), pac71GUID()
	henry := pac71Bound(t, "henry@"+d, msIdentity(t1, o1), storage.AddressDomainConfirmed)
	p := newFakeOAuthProvider(t)
	p.tenant = t1
	p.profile = jsonProfile(map[string]string{"id": o1, "mail": "henry@" + d})
	p.keys = useTestMicrosoftKeys(t, p)
	kid := testMicrosoftKid
	p.idToken = func() string {
		s := p.defaultIDClaims("s1")
		s.header = map[string]any{"kid": kid}
		return signMicrosoftIDToken(t, s)
	}
	ah := pac71Handlers(t)
	if uid, _ := sessionOf(t, runMicrosoftCallback(t, ah, p)); uid != henry {
		t.Fatal("first sign-in did not reach Henry")
	}
	if p.keys.count(t1) != 1 {
		t.Fatalf("fetches = %d", p.keys.count(t1))
	}
	// A rotated kid: one refetch, still unknown -> refused; a second unknown
	// kid within the minute makes no fetch and is refused.
	kid = "rotated"
	assertRefused(t, runMicrosoftCallback(t, ah, p))
	assertRefused(t, runMicrosoftCallback(t, ah, p))
	if p.keys.count(t1) != 2 {
		t.Fatalf("fetches = %d, want 2 (one refetch per minute)", p.keys.count(t1))
	}
	// The known kid is still served from the cache.
	kid = testMicrosoftKid
	if uid, _ := sessionOf(t, runMicrosoftCallback(t, ah, p)); uid != henry || p.keys.count(t1) != 2 {
		t.Fatal("cached key not used")
	}
}

// AC-8 (Microsoft half) and AC-11: a valid ID token with evidence creates the
// account; the authorize URLs carry exactly the contract's scopes.
func TestPAC71_AC11_ScopeNonceAndEvidence(t *testing.T) {
	d := pac71Domain()
	o7, t1 := pac71GUID(), pac71GUID()
	uid, _ := sessionOf(t, msRun{tid: t1, oid: o7, mail: "dave@" + d}.do(t))
	if a := readAccount(t, uid); a.standing != storage.AddressDomainConfirmed || a.identity != msIdentity(t1, o7).String() {
		t.Fatalf("account = %+v", a)
	}
	for name, org := range map[string]func(http.ResponseWriter){
		"domain not verified": orgAnswer(t1, "x.test"),
		"lookup error":        orgStatus(http.StatusServiceUnavailable),
	} {
		t.Run(name, func(t *testing.T) {
			e := "dave@" + pac71Domain()
			logs := pac71Logs(t)
			assertRefused(t, msRun{tid: t1, oid: pac71GUID(), mail: e, org: org}.do(t))
			if carriersCount(t, e) != 0 || strings.Contains(logs.String(), "tenant unobtainable") {
				t.Fatal("created, or refused as tenant-unobtainable")
			}
		})
	}
	t.Run("authorize URLs", func(t *testing.T) {
		t.Setenv("MICROSOFT_CLIENT_ID", "ms-client")
		rec := httptest.NewRecorder()
		ah := pac71Handlers(t)
		ah.startMicrosoftOAuth(rec, httptest.NewRequest(http.MethodGet, "/api/auth/microsoft", nil))
		u, _ := url.Parse(rec.Header().Get("Location"))
		var state string
		for _, c := range rec.Result().Cookies() {
			if c.Name == "ms_oauth_state" {
				state = c.Value
			}
		}
		if u.Query().Get("scope") != "openid profile Calendars.ReadWrite User.Read offline_access" || u.Query().Get("nonce") != auth.MicrosoftNonce(state) || state == "" {
			t.Fatalf("sign-in authorize URL %s", u)
		}
		cu, _ := url.Parse(auth.GetMicrosoftConnectURL(&auth.MicrosoftConfig{ClientID: "ms-client"}, "st"))
		if cu.Query().Get("scope") != "Calendars.ReadWrite User.Read offline_access" || cu.Query().Get("nonce") != "" {
			t.Fatalf("connect authorize URL %s", cu)
		}
	})
}

// AC-51: the verified-domain lookup is evidence only; its failure is never a
// refusal by itself.
func TestPAC71_AC51_OrganizationLookupIsEvidenceOnly(t *testing.T) {
	failures := map[string]func(t1 string) func(http.ResponseWriter){
		"error": func(string) func(http.ResponseWriter) { return orgStatus(http.StatusInternalServerError) },
		"403":   func(string) func(http.ResponseWriter) { return orgStatus(http.StatusForbidden) },
		"empty": func(string) func(http.ResponseWriter) { return jsonProfile(map[string]any{"value": []any{}}) },
		"multi-entry": func(t1 string) func(http.ResponseWriter) {
			return jsonProfile(map[string]any{"value": []any{map[string]any{"id": t1}, map[string]any{"id": pac71GUID()}}})
		},
		"another tenant": func(string) func(http.ResponseWriter) { return orgAnswer(pac71GUID(), "acme.test") },
	}
	for name, fail := range failures {
		t.Run(name, func(t *testing.T) {
			d := pac71Domain()
			t1, o1 := pac71GUID(), pac71GUID()
			logs := pac71Logs(t)
			// (i) bound Henry reaches his account. ("Not joined" is PAC-68's
			// membership rule, not built yet; today's association by domain is kept.)
			henry := pac71Bound(t, "henry@"+d, msIdentity(t1, o1), storage.AddressDomainConfirmed)
			if uid, _ := sessionOf(t, msRun{tid: t1, oid: o1, mail: "henry@" + d, org: fail(t1)}.do(t)); uid != henry {
				t.Fatal("(i) bound Henry did not reach his account")
			}
			// (ii) no carrier: refused under rule 5.
			assertRefused(t, msRun{tid: t1, oid: pac71GUID(), mail: "new@" + d, org: fail(t1)}.do(t))
			// (iii) empty legacy: refused under (b), stays unbound.
			legacy := pac71Legacy(t, "legacy@"+d, "microsoft", "")
			assertRefused(t, msRun{tid: t1, oid: pac71GUID(), mail: "legacy@" + d, org: fail(t1)}.do(t))
			if readAccount(t, legacy).identity != "" {
				t.Fatal("(iii) bound")
			}
			// (iv) non-empty legacy with recorded identifier O2: refused under
			// rule (a), stays unbound, generation unchanged.
			o2 := pac71GUID()
			nonEmpty := pac71Legacy(t, "carol@"+d, "microsoft", o2)
			execT(t, `INSERT INTO habits (user_id, title, duration_minutes) VALUES ($1, 'h', 30)`, nonEmpty)
			assertRefused(t, msRun{tid: t1, oid: o2, mail: "carol@" + d, org: fail(t1)}.do(t))
			if st := readAccount(t, nonEmpty); st.identity != "" || st.gen != 0 {
				t.Fatalf("(iv) %+v", st)
			}
			if strings.Contains(logs.String(), "tenant unobtainable") {
				t.Fatal("logged as tenant-unobtainable")
			}
		})
	}
}
