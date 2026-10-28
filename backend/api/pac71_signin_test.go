package api

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-71 sign-in by identity (spec docs/specs/PAC-71.md §3, §4 AC-1 to AC-41;
// contract googleOAuthCallback / microsoftOAuthCallback / ssoOidcCallback).
// Every test drives the real callback handlers with the fake providers of
// pac35_auth_flows_test.go (spec §4 conventions).

const pac71Secret = "secret"
const pac71RefusedURL = pac35Frontend + "/?error=signin_refused"

var pac71Seq atomic.Int64

// pac71GoogleID is a fresh identifier in Google's format.
func pac71GoogleID() string { return fmt.Sprintf("1071%014d", pac71Seq.Add(1)) }

// pac71GUID is a fresh GUID (Microsoft object and tenant ids).
func pac71GUID() string { return uuid.NewString() }

// pac71Domain is a fresh organisation domain.
func pac71Domain() string { return "acme-" + uuid.NewString()[:8] + ".test" }

func pac71Handlers(t *testing.T) *authHandlers {
	t.Helper()
	return &authHandlers{db: openTestDB(t), jwtSecret: pac71Secret, frontendURL: pac35Frontend}
}

func googleProfile(id, email string, verified any, hd string) map[string]any {
	p := map[string]any{"id": id, "email": email, "name": "G " + email}
	if verified != nil {
		p["verified_email"] = verified
	}
	if hd != "" {
		p["hd"] = hd
	}
	return p
}

// pac71Google runs one Google sign-in answered with profile.
func pac71Google(t *testing.T, profile map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(profile)
	return runGoogleCallback(t, pac71Handlers(t), p)
}

// orgAnswer is a Graph /organization answer for one work tenant.
func orgAnswer(tenant string, domains ...string) func(http.ResponseWriter) {
	vd := make([]any, 0, len(domains))
	for _, d := range domains {
		vd = append(vd, map[string]any{"name": d})
	}
	return jsonProfile(map[string]any{"value": []any{map[string]any{"id": tenant, "verifiedDomains": vd}}})
}

func orgStatus(code int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { http.Error(w, `{"error":{"code":"Forbidden"}}`, code) }
}

// pac71Microsoft runs one Microsoft sign-in: Graph /me answers (oid, mail),
// /organization answers org.
func pac71Microsoft(t *testing.T, oid, mail string, org func(http.ResponseWriter)) *httptest.ResponseRecorder {
	t.Helper()
	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(map[string]string{"id": oid, "mail": mail, "displayName": "M " + mail})
	p.organization = org
	return runMicrosoftCallback(t, pac71Handlers(t), p)
}

// pac71Legacy is an account created before PAC-71: unbound, unconfirmed,
// generation 0, with providerID as its recorded last-sign-in identifier.
func pac71Legacy(t *testing.T, email, provider, providerID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := openTestDB(t).QueryRow(`INSERT INTO users (email, name, provider, provider_id) VALUES ($1, 'Legacy', $2, $3) RETURNING id`,
		email, provider, providerID).Scan(&id); err != nil {
		t.Fatalf("legacy account: %v", err)
	}
	return id
}

// pac71Bound is an account bound to ident with standing.
func pac71Bound(t *testing.T, email string, ident storage.Identity, standing string) uuid.UUID {
	t.Helper()
	db := openTestDB(t)
	var id uuid.UUID
	if err := db.QueryRow(`INSERT INTO users (email, email_key, name, provider, provider_id, address_standing) VALUES ($1, lower($1), 'Henry', $2, $3, $4) RETURNING id`,
		email, ident.Provider, ident.Subject, standing).Scan(&id); err != nil {
		t.Fatalf("bound account: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by) VALUES ($1, $2, $3, $4, 'authoritative', 'created')`,
		id, ident.Provider, ident.Tenant, ident.Subject); err != nil {
		t.Fatalf("bind: %v", err)
	}
	return id
}

func googleIdentity(sub string) storage.Identity {
	return storage.Identity{Provider: storage.IdentityGoogle, Subject: sub}
}

func msIdentity(tenant, oid string) storage.Identity {
	return storage.Identity{Provider: storage.IdentityMicrosoft, Tenant: strings.ToLower(tenant), Subject: strings.ToLower(oid)}
}

// assertRefused is spec §4 "Refused" at the HTTP boundary: the one fixed
// refusal Location, distinct from auth_failed, and auth_token untouched.
func assertRefused(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != pac71RefusedURL {
		t.Fatalf("= %d Location %q, want 302 %s; body %s", rec.Code, rec.Header().Get("Location"), pac71RefusedURL, rec.Body.String())
	}
	if got := setCookieNamed(rec, "auth_token"); len(got) != 0 {
		t.Fatalf("a refusal touched auth_token: %v", got)
	}
}

// sessionOf decodes the auth_token the response set: account and gen.
func sessionOf(t *testing.T, rec *httptest.ResponseRecorder) (uuid.UUID, int64) {
	t.Helper()
	c := authCookie(rec)
	if c == nil {
		t.Fatalf("no session issued; Location %q", rec.Header().Get("Location"))
	}
	claims, err := auth.ValidateToken(c.Value, pac71Secret)
	if err != nil {
		t.Fatal(err)
	}
	gen, _ := claims.Generation()
	return uuid.MustParse(claims.UserID), gen
}

type accountState struct {
	email, standing, providerID string
	gen                         int64
	identity                    string
	googleAccess                string
	msTokens                    string
	calendarProvider            string
	orgID                       uuid.NullUUID
}

func readAccount(t *testing.T, id uuid.UUID) accountState {
	t.Helper()
	db := openTestDB(t)
	var a accountState
	if err := db.QueryRow(`SELECT email, address_standing, provider_id, session_generation, org_id FROM users WHERE id = $1`, id).
		Scan(&a.email, &a.standing, &a.providerID, &a.gen, &a.orgID); err != nil {
		t.Fatalf("read account: %v", err)
	}
	if b, err := storage.GetBoundIdentity(db, id); err != nil {
		t.Fatal(err)
	} else if b != nil {
		a.identity = b.String()
	}
	_ = db.QueryRow(`SELECT access_token FROM oauth_tokens WHERE user_id = $1`, id).Scan(&a.googleAccess)
	var ms, cp sql.NullString
	_ = db.QueryRow(`SELECT microsoft_tokens, calendar_provider FROM settings WHERE user_id = $1`, id).Scan(&ms, &cp)
	a.msTokens, a.calendarProvider = ms.String, cp.String
	return a
}

func carriersCount(t *testing.T, email string) int {
	t.Helper()
	var n int
	if err := openTestDB(t).QueryRow(`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func seedGoogleToken(t *testing.T, id uuid.UUID, access string) {
	t.Helper()
	if err := auth.UpsertUserToken(openTestDB(t), id, &oauth2.Token{AccessToken: access, RefreshToken: "r-" + access}); err != nil {
		t.Fatal(err)
	}
}

// pac71Logs captures the server log for the test.
func pac71Logs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return buf
}

// meStatus is the status of GET /api/auth/me for a session of id at gen.
func meStatus(t *testing.T, id uuid.UUID, gen int64) int {
	t.Helper()
	tok, err := auth.GenerateSessionToken(id, "x@example.com", gen, pac71Secret)
	if err != nil {
		t.Fatal(err)
	}
	return pac35Do(pac35Router(openTestDB(t), pac71Secret), http.MethodGet, "/api/auth/me", &http.Cookie{Name: "auth_token", Value: tok}).Code
}

// ---- Takeover scenarios (spec §1) ------------------------------------------

func TestPAC71_AC1_SecondGoogleIdentityWithHenrysAddressIsRefused(t *testing.T) {
	d := pac71Domain()
	henry := pac71Bound(t, "henry@"+d, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
	seedGoogleToken(t, henry, "henry-g1-token")
	before := readAccount(t, henry)
	assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, "")))
	if after := readAccount(t, henry); after != before {
		t.Fatalf("Henry changed: %+v -> %+v", before, after)
	}
}

func TestPAC71_AC2_MicrosoftAccountWithHenrysAddressIsRefused(t *testing.T) {
	for name, verifies := range map[string]bool{"tenant verifies the domain": true, "tenant does not": false} {
		t.Run(name, func(t *testing.T) {
			d := pac71Domain()
			henry := pac71Bound(t, "henry@"+d, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
			before := readAccount(t, henry)
			domains := []string{"other.test"}
			if verifies {
				domains = append(domains, d)
			}
			assertRefused(t, pac71Microsoft(t, pac71GUID(), "henry@"+d, orgAnswer(pac71GUID(), domains...)))
			if after := readAccount(t, henry); after != before {
				t.Fatalf("Henry changed: %+v -> %+v", before, after)
			}
		})
	}
}

func TestPAC71_AC3_GoogleWorkspaceIdentityCannotReachMicrosoftBoundHenry(t *testing.T) {
	d := pac71Domain()
	henry := pac71Bound(t, "henry@"+d, msIdentity(pac71GUID(), pac71GUID()), storage.AddressDomainConfirmed)
	before := readAccount(t, henry)
	assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
	if after := readAccount(t, henry); after != before {
		t.Fatalf("Henry changed: %+v -> %+v", before, after)
	}
}

func TestPAC71_AC4_SameObjectIdOtherTenantAndOtherObjectSameTenantAreRefused(t *testing.T) {
	d := pac71Domain()
	t1, o1 := pac71GUID(), pac71GUID()
	henry := pac71Bound(t, "henry@"+d, msIdentity(t1, o1), storage.AddressDomainConfirmed)
	before := readAccount(t, henry)
	t.Run("(T1, O2)", func(t *testing.T) {
		assertRefused(t, pac71Microsoft(t, pac71GUID(), "henry@"+d, orgAnswer(t1, d)))
	})
	t.Run("(T2, O1)", func(t *testing.T) {
		assertRefused(t, pac71Microsoft(t, o1, "henry@"+d, orgAnswer(pac71GUID(), d)))
	})
	if after := readAccount(t, henry); after != before {
		t.Fatalf("Henry changed: %+v -> %+v", before, after)
	}
}

// AC-5, AC-11c: a personal Microsoft account cannot be classified as work
// (Graph refuses /organization for it), so it is refused, whatever it would
// have reached; nothing is written.
func TestPAC71_AC5_AC11c_PersonalMicrosoftAccountIsRefused(t *testing.T) {
	d := pac71Domain()
	t1, o1 := pac71GUID(), pac71GUID()
	henry := pac71Bound(t, "henry@"+d, msIdentity(t1, o1), storage.AddressDomainConfirmed)
	legacy := pac71Legacy(t, "legacy@"+d, "microsoft", "")
	personal := orgStatus(http.StatusForbidden)
	t.Run("bound Henry", func(t *testing.T) {
		before := readAccount(t, henry)
		// The same object id: only the tenant answer differs.
		assertRefused(t, pac71Microsoft(t, o1, "henry@"+d, personal))
		if after := readAccount(t, henry); after != before {
			t.Fatalf("Henry changed")
		}
	})
	t.Run("unbound legacy account", func(t *testing.T) {
		before := readAccount(t, legacy)
		assertRefused(t, pac71Microsoft(t, pac71GUID(), "legacy@"+d, personal))
		if after := readAccount(t, legacy); after != before {
			t.Fatalf("legacy changed")
		}
	})
	t.Run("AC-11c no account", func(t *testing.T) {
		assertRefused(t, pac71Microsoft(t, pac71GUID(), "erin@"+d, personal))
		if n := carriersCount(t, "erin@"+d); n != 0 {
			t.Fatalf("an account was created (%d)", n)
		}
	})
}

func TestPAC71_AC6_SSOIdentityNeverReachesAnotherAccountAndIssuerChangeIsRefused(t *testing.T) {
	db := openTestDB(t)
	d := pac71Domain()
	idp1 := newFakeIdP(t, "client-"+d)
	seedOIDCProvider(t, d, idp1)
	ah := pac71Handlers(t)
	okCookie := &http.Cookie{Name: "sso_state", Value: "s1|" + d}
	serve := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		ssoRouter(ah).ServeHTTP(rec, oidcCallbackRequest(d, okCookie))
		return rec
	}

	henry := pac71Bound(t, "henry@"+d, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
	idp1.email, idp1.sub = "henry@"+d, "S1"
	assertRefused(t, serve())
	if b, _ := storage.GetBoundIdentity(db, henry); b == nil || b.Provider != storage.IdentityGoogle {
		t.Fatalf("Henry's binding changed: %v", b)
	}

	// Henry2 bound to (I1, S1); the configuration moves to issuer I2, which
	// asserts the same subject.
	d2 := pac71Domain()
	idpA := newFakeIdP(t, "client-"+d2)
	henry2 := pac71Bound(t, "henry@"+d2, storage.Identity{Provider: storage.IdentityOIDC, Tenant: idpA.srv.URL, Subject: "S1"}, storage.AddressDomainConfirmed)
	idpB := newFakeIdP(t, "client-"+d2)
	seedOIDCProvider(t, d2, idpB)
	idpB.email, idpB.sub = "henry@"+d2, "S1"
	rec := httptest.NewRecorder()
	ssoRouter(ah).ServeHTTP(rec, oidcCallbackRequest(d2, &http.Cookie{Name: "sso_state", Value: "s1|" + d2}))
	assertRefused(t, rec)
	if a := readAccount(t, henry2); a.gen != 0 || !strings.Contains(a.identity, idpA.srv.URL) {
		t.Fatalf("Henry2 changed: %+v", a)
	}
}

// AC-7: two authoritative identities race to create new@acme; the second
// one's creation is stored between the first's check and its store.
func TestPAC71_AC7_ConcurrentCreationForOneAddressCreatesOneAccount(t *testing.T) {
	for name, emails := range map[string][2]string{
		"same address":         {"new@", "new@"},
		"letter-case variants": {"Zoe@", "zoe@"},
	} {
		t.Run(name, func(t *testing.T) {
			d := pac71Domain()
			first, second := pac71GoogleID(), pac71GoogleID()
			var fired atomic.Bool
			var secondRec *httptest.ResponseRecorder
			storage.SignInHooks.BeforeStore = func(req storage.SignInRequest) {
				if req.Identity.Subject == first && fired.CompareAndSwap(false, true) {
					secondRec = pac71Google(t, googleProfile(second, emails[1]+d, true, d))
				}
			}
			t.Cleanup(func() { storage.SignInHooks.BeforeStore = nil })
			assertRefused(t, pac71Google(t, googleProfile(first, emails[0]+d, true, d)))
			if secondRec == nil {
				t.Fatal("the seam did not run")
			}
			uid, _ := sessionOf(t, secondRec)
			if n := carriersCount(t, emails[0]+d); n != 1 {
				t.Fatalf("%d accounts carry the address, want 1", n)
			}
			if a := readAccount(t, uid); a.identity != googleIdentity(second).String() {
				t.Fatalf("account bound to %q, want the second identity", a.identity)
			}
		})
	}
	t.Run("sequential: first creates, second refused", func(t *testing.T) {
		d := pac71Domain()
		rec := pac71Google(t, googleProfile(pac71GoogleID(), "seq@"+d, true, d))
		sessionOf(t, rec)
		assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "SEQ@"+d, true, d)))
	})
}

// AC-7b: an empty legacy account, Google and Microsoft both qualifying under
// rule (b); the other's binding is stored inside the first's check-to-store
// window.
func TestPAC71_AC7b_ConcurrentBindingOfOneLegacyAccountBindsOnce(t *testing.T) {
	run := func(t *testing.T, googleFirst bool) {
		d := pac71Domain()
		a := pac71Legacy(t, "henry@"+d, "google", "")
		g2 := pac71GoogleID()
		t1, o3 := pac71GUID(), pac71GUID()
		google := func() *httptest.ResponseRecorder {
			return pac71Google(t, googleProfile(g2, "henry@"+d, true, d))
		}
		ms := func() *httptest.ResponseRecorder { return pac71Microsoft(t, o3, "henry@"+d, orgAnswer(t1, d)) }
		var fired atomic.Bool
		var inner *httptest.ResponseRecorder
		storage.SignInHooks.BeforeStore = func(req storage.SignInRequest) {
			if fired.CompareAndSwap(false, true) {
				if googleFirst {
					inner = ms()
				} else {
					inner = google()
				}
			}
		}
		t.Cleanup(func() { storage.SignInHooks.BeforeStore = nil })
		var outer *httptest.ResponseRecorder
		if googleFirst {
			outer = google()
		} else {
			outer = ms()
		}
		assertRefused(t, outer)
		uid, gen := sessionOf(t, inner)
		if uid != a || gen != 1 {
			t.Fatalf("inner reached %s gen %d, want %s gen 1", uid, gen, a)
		}
		st := readAccount(t, a)
		want := msIdentity(t1, o3).String()
		if !googleFirst {
			want = googleIdentity(g2).String()
		}
		if st.identity != want || st.gen != 1 || st.standing != storage.AddressDomainConfirmed {
			t.Fatalf("account = %+v, want bound to %s once", st, want)
		}
		if googleFirst && st.googleAccess != "" {
			t.Fatal("the refused Google sign-in stored its credential")
		}
		if n := carriersCount(t, "henry@"+d); n != 1 {
			t.Fatalf("%d accounts", n)
		}
	}
	t.Run("Microsoft binds inside Google's window", func(t *testing.T) { run(t, true) })
	t.Run("mirror: Google binds inside Microsoft's window", func(t *testing.T) { run(t, false) })
	t.Run("sequential", func(t *testing.T) {
		d := pac71Domain()
		a := pac71Legacy(t, "henry@"+d, "google", "")
		uid, _ := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
		if uid != a {
			t.Fatal("first did not bind the legacy account")
		}
		assertRefused(t, pac71Microsoft(t, pac71GUID(), "henry@"+d, orgAnswer(pac71GUID(), d)))
	})
}

func TestPAC71_AC25_MailboxEvidenceOnACarriedAddressIsRefused(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, email string){
		"bound, domain-confirmed": func(t *testing.T, email string) {
			pac71Bound(t, email, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
		},
		"bound, unconfirmed": func(t *testing.T, email string) {
			pac71Bound(t, email, googleIdentity(pac71GoogleID()), storage.AddressUnconfirmed)
		},
		"unbound legacy, other letter case": func(t *testing.T, email string) {
			pac71Legacy(t, strings.ToUpper(email[:1])+email[1:], "google", pac71GoogleID())
		},
	} {
		t.Run(name, func(t *testing.T) {
			email := "alice@example-" + uuid.NewString()[:8] + ".org"
			setup(t, email)
			assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), email, true, "")))
			if n := carriersCount(t, email); n != 1 {
				t.Fatalf("%d carriers", n)
			}
		})
	}
}

// ---- Ordinary sign-in keeps working ----------------------------------------

func TestPAC71_AC8_BoundIdentityReachesHenryWithTodaysRedirect(t *testing.T) {
	t.Run("Google", func(t *testing.T) {
		d := pac71Domain()
		g1 := pac71GoogleID()
		henry := pac71Bound(t, "henry@"+d, googleIdentity(g1), storage.AddressDomainConfirmed)
		rec := pac71Google(t, googleProfile(g1, "henry@"+d, true, d))
		assertSignedIn(t, rec, pac35Frontend+"/auth/callback")
		if uid, gen := sessionOf(t, rec); uid != henry || gen != 0 {
			t.Fatalf("session %s gen %d", uid, gen)
		}
		if readAccount(t, henry).googleAccess == "" {
			t.Fatal("credential not stored on Henry")
		}
	})
	t.Run("Microsoft", func(t *testing.T) {
		d := pac71Domain()
		t1, o1 := pac71GUID(), pac71GUID()
		henry := pac71Bound(t, "henry@"+d, msIdentity(t1, o1), storage.AddressDomainConfirmed)
		// Graph answers the GUIDs in upper case: identifiers compare as GUIDs.
		rec := pac71Microsoft(t, strings.ToUpper(o1), "henry@"+d, orgAnswer(strings.ToUpper(t1), d))
		assertSignedIn(t, rec, pac35Frontend+"/auth/callback?provider=outlook")
		if uid, _ := sessionOf(t, rec); uid != henry {
			t.Fatal("did not reach Henry")
		}
		if readAccount(t, henry).msTokens == "" {
			t.Fatal("credential not stored on Henry")
		}
	})
	t.Run("SSO", func(t *testing.T) {
		d := pac71Domain()
		idp := newFakeIdP(t, "client-"+d)
		seedOIDCProvider(t, d, idp)
		henry := pac71Bound(t, "henry@"+d, storage.Identity{Provider: storage.IdentityOIDC, Tenant: idp.srv.URL, Subject: "S-henry"}, storage.AddressDomainConfirmed)
		idp.email, idp.sub = "henry@"+d, "S-henry"
		rec := httptest.NewRecorder()
		ssoRouter(pac71Handlers(t)).ServeHTTP(rec, oidcCallbackRequest(d, &http.Cookie{Name: "sso_state", Value: "s1|" + d}))
		assertSignedIn(t, rec, pac35Frontend+"/auth/callback?provider=sso")
		if uid, _ := sessionOf(t, rec); uid != henry {
			t.Fatal("did not reach Henry")
		}
	})
}

func TestPAC71_AC9_NewAddressAtTheProviderStillReachesTheAccount(t *testing.T) {
	d := pac71Domain()
	g1 := pac71GoogleID()
	henry := pac71Bound(t, "henry@"+d, googleIdentity(g1), storage.AddressDomainConfirmed)
	other := pac71Bound(t, "henry.new@"+d, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
	otherBefore := readAccount(t, other)
	rec := pac71Google(t, googleProfile(g1, "henry.new@"+d, true, d))
	if uid, _ := sessionOf(t, rec); uid != henry {
		t.Fatal("did not reach Henry")
	}
	if a := readAccount(t, henry); a.email != "henry@"+d || a.standing != storage.AddressDomainConfirmed {
		t.Fatalf("Henry's address or standing changed: %+v", a)
	}
	if after := readAccount(t, other); after != otherBefore {
		t.Fatalf("the other holder changed")
	}
}

// ---- New accounts and evidence ---------------------------------------------

func TestPAC71_AC10_MailboxEvidenceCreatesAnUnconfirmedAccount(t *testing.T) {
	email := "alice@example-" + uuid.NewString()[:8] + ".org"
	g5 := pac71GoogleID()
	rec := pac71Google(t, googleProfile(g5, email, true, ""))
	uid, _ := sessionOf(t, rec)
	if a := readAccount(t, uid); a.standing != storage.AddressUnconfirmed || a.identity != googleIdentity(g5).String() {
		t.Fatalf("account = %+v", a)
	}
	again, _ := sessionOf(t, pac71Google(t, googleProfile(g5, email, true, "")))
	if again != uid {
		t.Fatal("the second sign-in did not reach the account")
	}
	for name, flag := range map[string]any{"verified false": false, "flag absent": nil} {
		t.Run(name, func(t *testing.T) {
			e := "nobody@example-" + uuid.NewString()[:8] + ".org"
			assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), e, flag, "")))
			if n := carriersCount(t, e); n != 0 {
				t.Fatal("an account was created")
			}
		})
	}
}

func TestPAC71_AC10b_AuthoritativeGoogleEvidence(t *testing.T) {
	d := pac71Domain()
	cases := []struct {
		name, email, hd, want string
	}{
		{"(a) gmail.com verified", "bob" + uuid.NewString()[:8] + "@gmail.com", "", storage.AddressDomainConfirmed},
		{"(b) hd equals the domain", "carol@" + d, d, storage.AddressDomainConfirmed},
		{"(c) a subdomain of hd is not", "carol@sub." + d, d, storage.AddressUnconfirmed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uid, _ := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), c.email, true, c.hd)))
			if a := readAccount(t, uid); a.standing != c.want {
				t.Fatalf("standing %q, want %q", a.standing, c.want)
			}
		})
	}
}

func TestPAC71_AC11_MicrosoftNewAccountNeedsATenantThatVerifiesTheDomain(t *testing.T) {
	d := pac71Domain()
	o7 := pac71GUID()
	rec := pac71Microsoft(t, o7, "dave@"+d, orgAnswer(pac71GUID(), "x.test", d))
	uid, _ := sessionOf(t, rec)
	a := readAccount(t, uid)
	if a.standing != storage.AddressDomainConfirmed || !strings.HasSuffix(a.identity, ":"+o7) || a.calendarProvider != "outlook" {
		t.Fatalf("account = %+v", a)
	}
	t.Run("tenant does not verify the domain", func(t *testing.T) {
		e := "dave@" + pac71Domain()
		assertRefused(t, pac71Microsoft(t, pac71GUID(), e, orgAnswer(pac71GUID(), "x.test")))
		if carriersCount(t, e) != 0 {
			t.Fatal("created")
		}
	})
	t.Run("lookup error", func(t *testing.T) {
		e := "dave@" + pac71Domain()
		assertRefused(t, pac71Microsoft(t, pac71GUID(), e, orgStatus(http.StatusServiceUnavailable)))
		if carriersCount(t, e) != 0 {
			t.Fatal("created")
		}
	})
}

// AC-11b: the tenant fails closed for (i) no carrier, (ii) an empty legacy
// account and (iii) a bound account; the refusal, not auth_failed.
func TestPAC71_AC11b_UnobtainableTenantIsRefusedInEveryCase(t *testing.T) {
	answers := map[string]func(t1 string) func(http.ResponseWriter){
		"error 500":      func(string) func(http.ResponseWriter) { return orgStatus(http.StatusInternalServerError) },
		"403 permission": func(string) func(http.ResponseWriter) { return orgStatus(http.StatusForbidden) },
		"empty value":    func(string) func(http.ResponseWriter) { return jsonProfile(map[string]any{"value": []any{}}) },
		"multi-entry": func(t1 string) func(http.ResponseWriter) {
			return jsonProfile(map[string]any{"value": []any{map[string]any{"id": t1}, map[string]any{"id": pac71GUID()}}})
		},
		"non-GUID id": func(string) func(http.ResponseWriter) { return orgAnswer("contoso-tenant") },
		"undecodable body": func(string) func(http.ResponseWriter) {
			return func(w http.ResponseWriter) { _, _ = w.Write([]byte("<html>")) }
		},
	}
	for name, answer := range answers {
		t.Run(name, func(t *testing.T) {
			d := pac71Domain()
			t1, o1 := pac71GUID(), pac71GUID()
			assertRefused(t, pac71Microsoft(t, pac71GUID(), "new@"+d, answer(t1)))
			if carriersCount(t, "new@"+d) != 0 {
				t.Fatal("(i) created")
			}
			legacy := pac71Legacy(t, "legacy@"+d, "microsoft", "")
			assertRefused(t, pac71Microsoft(t, pac71GUID(), "legacy@"+d, answer(t1)))
			if a := readAccount(t, legacy); a.identity != "" || a.gen != 0 {
				t.Fatalf("(ii) legacy changed: %+v", a)
			}
			henry := pac71Bound(t, "henry@"+d, msIdentity(t1, o1), storage.AddressDomainConfirmed)
			before := readAccount(t, henry)
			assertRefused(t, pac71Microsoft(t, o1, "henry@"+d, answer(t1)))
			if after := readAccount(t, henry); after != before {
				t.Fatalf("(iii) Henry changed")
			}
		})
	}
}
