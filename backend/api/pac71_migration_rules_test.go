package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-71 accounts created before the change: migration rules (a) and (b)
// (spec §3 "Accounts created before this change", AC-15 to AC-17, AC-29,
// AC-37, AC-39 to AC-45).

func execT(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := openTestDB(t).Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func assertUnboundUnchanged(t *testing.T, id uuid.UUID, before accountState) {
	t.Helper()
	after := readAccount(t, id)
	if after != before || after.identity != "" {
		t.Fatalf("account changed: %+v -> %+v", before, after)
	}
}

func TestPAC71_AC15_RuleA_RecordedGoogleIdentifierBinds(t *testing.T) {
	email := "henry@example-" + uuid.NewString()[:8] + ".org"
	g1 := pac71GoogleID()
	a := pac71Legacy(t, email, "google", g1)
	execT(t, `INSERT INTO habits (user_id, title, duration_minutes) VALUES ($1, 'Read', 30)`, a) // not empty: (a) does not care
	uid, gen := sessionOf(t, pac71Google(t, googleProfile(g1, email, true, "")))
	if uid != a || gen != 1 {
		t.Fatalf("reached %s gen %d", uid, gen)
	}
	if st := readAccount(t, a); st.identity != googleIdentity(g1).String() || st.standing != storage.AddressUnconfirmed {
		t.Fatalf("account = %+v (mailbox evidence: unconfirmed)", st)
	}
	assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), email, true, "")))
}

func TestPAC71_AC16_NonEmptyLegacyAccountRefusesEveryOtherIdentity(t *testing.T) {
	d := pac71Domain()
	email := "henry@" + d
	a := pac71Legacy(t, email, "google", pac71GoogleID())
	execT(t, `INSERT INTO habits (user_id, title, duration_minutes) VALUES ($1, 'Gym', 60)`, a)
	before := readAccount(t, a)
	logs := pac71Logs(t)
	t.Run("(a) G2 no hd", func(t *testing.T) { assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), email, true, ""))) })
	t.Run("(b) G2 hd", func(t *testing.T) {
		logs.Reset()
		assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), email, true, d)))
		if !strings.Contains(logs.String(), a.String()) || !strings.Contains(logs.String(), "habit") {
			t.Fatalf("log does not name the account and the kind: %s", logs.String())
		}
	})
	t.Run("(c) Microsoft verifying", func(t *testing.T) {
		logs.Reset()
		assertRefused(t, pac71Microsoft(t, pac71GUID(), email, orgAnswer(pac71GUID(), d)))
		if !strings.Contains(logs.String(), a.String()) || !strings.Contains(logs.String(), "habit") {
			t.Fatalf("log does not name the account and the kind: %s", logs.String())
		}
	})
	t.Run("(d) Microsoft not verifying", func(t *testing.T) {
		assertRefused(t, pac71Microsoft(t, pac71GUID(), email, orgAnswer(pac71GUID(), "x.test")))
	})
	assertUnboundUnchanged(t, a, before)
}

func TestPAC71_AC17_DifferentGoogleIdentityOnACustomDomainIsRefused(t *testing.T) {
	email := "bob@example-" + uuid.NewString()[:8] + ".org"
	a := pac71Legacy(t, email, "google", pac71GoogleID())
	before := readAccount(t, a)
	assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), email, true, "")))
	assertUnboundUnchanged(t, a, before)
}

func TestPAC71_AC37_SSOAndIdentifierFormats(t *testing.T) {
	d := pac71Domain()
	idp := newFakeIdP(t, "client-"+d)
	seedOIDCProvider(t, d, idp)
	x := pac71GoogleID()
	a := pac71Legacy(t, "henry@"+d, "sso", x)
	before := readAccount(t, a)
	sso := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		ssoRouter(pac71Handlers(t)).ServeHTTP(rec, oidcCallbackRequest(d, &http.Cookie{Name: "sso_state", Value: "s1|" + d}))
		return rec
	}
	idp.email, idp.sub = "henry@"+d, "S-other"
	assertRefused(t, sso())
	idp.sub = x
	assertRefused(t, sso())
	assertUnboundUnchanged(t, a, before)

	t.Run("a Google-format record never matches Microsoft", func(t *testing.T) {
		// Microsoft ids are GUIDs, so the decimal record can never equal one;
		// the tenant does not verify the domain, so rule (b) cannot apply.
		assertRefused(t, pac71Microsoft(t, pac71GUID(), "henry@"+d, orgAnswer(pac71GUID(), "x.test")))
		assertUnboundUnchanged(t, a, before)
	})
	t.Run("a GUID record matches Microsoft's /me id under rule (a)", func(t *testing.T) {
		d2 := pac71Domain()
		o := pac71GUID()
		b := pac71Legacy(t, "erin@"+d2, "microsoft", strings.ToUpper(o))
		execT(t, `INSERT INTO habits (user_id, title, duration_minutes) VALUES ($1, 'x', 30)`, b)
		uid, _ := sessionOf(t, pac71Microsoft(t, o, "erin@"+d2, orgAnswer(pac71GUID(), d2)))
		if uid != b {
			t.Fatal("rule (a) did not bind")
		}
		// Rule (a) never changes the work calendar (AC-31).
		if st := readAccount(t, b); st.calendarProvider == "outlook" {
			t.Fatal("a migration binding set the work calendar")
		}
	})
}

func TestPAC71_AC39_GmailLegacyAccount(t *testing.T) {
	email := "bob" + uuid.NewString()[:8] + "@gmail.com"
	a := pac71Legacy(t, email, "google", "")
	g1 := pac71GoogleID()
	uid, _ := sessionOf(t, pac71Google(t, googleProfile(g1, email, true, "")))
	if st := readAccount(t, a); uid != a || st.standing != storage.AddressDomainConfirmed || st.identity != googleIdentity(g1).String() {
		t.Fatalf("empty gmail account: reached %s, %+v", uid, st)
	}
	email2 := "bob" + uuid.NewString()[:8] + "@gmail.com"
	b := pac71Legacy(t, email2, "google", pac71GoogleID())
	execT(t, `INSERT INTO habits (user_id, title, duration_minutes) VALUES ($1, 'x', 30)`, b)
	before := readAccount(t, b)
	assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), email2, true, "")))
	assertUnboundUnchanged(t, b, before)
}

// AC-41 first bullet and AC-42 second bullet: an empty legacy account binds
// whatever its recorded identifier; the binding ends both old sessions.
func TestPAC71_AC41_EmptyLegacyAccountBindsAndEndsOldSessions(t *testing.T) {
	records := map[string][2]string{
		"no recorded identifier":           {"google", ""},
		"an SSO subject, created by SSO":   {"sso", "idp-subject-xyz"},
		"Google-format value, SSO-created": {"sso", pac71GoogleID()},
		"a consumer Google account GA":     {"google", pac71GoogleID()},
	}
	for name, rec := range records {
		t.Run(name, func(t *testing.T) {
			d := pac71Domain()
			a := pac71Legacy(t, "henry@"+d, rec[0], rec[1])
			execT(t, `INSERT INTO settings (user_id) VALUES ($1)`, a)
			if meStatus(t, a, 0) != http.StatusOK {
				t.Fatal("old session not valid before the binding")
			}
			g1 := pac71GoogleID()
			uid, gen := sessionOf(t, pac71Google(t, googleProfile(g1, "henry@"+d, true, d)))
			if uid != a || gen != 1 {
				t.Fatalf("reached %s gen %d", uid, gen)
			}
			st := readAccount(t, a)
			if st.standing != storage.AddressDomainConfirmed || st.providerID != g1 || st.calendarProvider != "google" {
				t.Fatalf("account = %+v", st)
			}
			if c := meStatus(t, a, 0); c != http.StatusUnauthorized {
				t.Fatalf("old session = %d, want 401", c)
			}
			if c := meStatus(t, a, 1); c != http.StatusOK {
				t.Fatalf("binding session = %d, want 200", c)
			}
		})
	}
}

// pac71Kinds are the non-empty variants of rule (b) (AC-41 second bullet and
// AC-45), each a statement on account $1 (and $2, another account B).
var pac71Kinds = map[string]string{
	"slack":                         `INSERT INTO workspace_connections (user_id, provider, access_token) VALUES ($1, 'slack', 'x')`,
	"notion":                        `INSERT INTO workspace_connections (user_id, provider, access_token) VALUES ($1, 'notion', 'x')`,
	"ai key":                        `INSERT INTO settings (user_id, llm_api_key_openai) VALUES ($1, 'sk-x')`,
	"zoom":                          `INSERT INTO settings (user_id, zoom_tokens) VALUES ($1, '{"access_token":"z"}')`,
	"google credential":             `INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expiry, calendar_id) VALUES ($1, 'a', 'r', NOW(), 'primary')`,
	"microsoft credential":          `INSERT INTO settings (user_id, microsoft_tokens) VALUES ($1, '{"access_token":"m"}')`,
	"personal calendar":             `INSERT INTO personal_calendars (user_id, provider, name, url) VALUES ($1, 'ics', 'p', 'https://p.test/x.ics')`,
	"credential loss":               `INSERT INTO credential_losses (user_id, provider) VALUES ($1, 'google')`,
	"team member of B's team":       `WITH t AS (INSERT INTO teams (name, created_by) VALUES ('B team', $2) RETURNING id) INSERT INTO team_members (team_id, user_id, role) SELECT id, $1, 'member' FROM t`,
	"team created":                  `INSERT INTO teams (name, created_by) VALUES ('A team', $1)`,
	"team invite sent":              `WITH t AS (INSERT INTO teams (name, created_by) VALUES ('B team', $2) RETURNING id) INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at) SELECT id, 'z@x.test', $1, md5(random()::text), NOW() + interval '1 day' FROM t`,
	"co-host accepted":              `WITH l AS (INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($2, md5(random()::text), 'L') RETURNING id) INSERT INTO scheduling_link_hosts (link_id, user_id, status) SELECT id, $1, 'accepted' FROM l`,
	"co-host pending":               `WITH l AS (INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($2, md5(random()::text), 'L') RETURNING id) INSERT INTO scheduling_link_hosts (link_id, user_id, status) SELECT id, $1, 'pending' FROM l`,
	"co-host declined":              `WITH l AS (INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($2, md5(random()::text), 'L') RETURNING id) INSERT INTO scheduling_link_hosts (link_id, user_id, status) SELECT id, $1, 'declined' FROM l`,
	"owned link":                    `INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, md5(random()::text), 'L')`,
	"manager profile":               `INSERT INTO user_profiles (user_id, is_manager) VALUES ($1, true)`,
	"roster kept":                   `INSERT INTO manager_team_members (manager_user_id, member_email, display_name) VALUES ($1, 'r@x.test', 'R')`,
	"entry in B's roster":           `INSERT INTO manager_team_members (manager_user_id, member_email, member_user_id, display_name) VALUES ($2, 'a@x.test', $1, 'A')`,
	"focus block":                   `INSERT INTO focus_blocks (user_id, google_event_id, start_time, end_time, date) VALUES ($1, md5(random()::text), NOW(), NOW() + interval '1 hour', '2030-01-07')`,
	"habit":                         `INSERT INTO habits (user_id, title, duration_minutes) VALUES ($1, 'h', 30)`,
	"analytics week":                `INSERT INTO analytics_weeks (user_id, week_start) VALUES ($1, CURRENT_DATE)`,
	"meeting brief":                 `INSERT INTO meeting_briefs (user_id, calendar_event_id) VALUES ($1, 'e')`,
	"recap record":                  `INSERT INTO recap_sends (user_id, sent_date) VALUES ($1, CURRENT_DATE)`,
	"activity log entry":            `INSERT INTO audit_log (user_id, action, details) VALUES ($1, 'x', '{}')`,
	"step-up grant":                 `WITH o AS (INSERT INTO organizations (name, domain) VALUES ('O', md5(random()::text) || '.test') RETURNING id) INSERT INTO sso_stepup_grants (user_id, org_id, provider, purpose, issued_at, expires_at) SELECT $1, id, 'google', 'sso_write', NOW(), NOW() + interval '10 minutes' FROM o`,
	"SSO write record":              `INSERT INTO sso_events (user_id, kind, details) VALUES ($1, 'sso_provider_written', '{}')`,
	"changed preference (timezone)": `INSERT INTO settings (user_id, timezone) VALUES ($1, 'Europe/Paris')`,
	"outlook left as work calendar": `INSERT INTO settings (user_id, calendar_provider) VALUES ($1, 'outlook')`,
}

func TestPAC71_AC41_AC45_EveryKindOfDataOrRelationshipRefusesRuleB(t *testing.T) {
	for kind, stmt := range pac71Kinds {
		t.Run(kind, func(t *testing.T) {
			d := pac71Domain()
			a := pac71Legacy(t, "henry@"+d, "google", "")
			b := createTestUser(t, "b-"+uuid.NewString()[:8]+"@x.test")
			db := openTestDB(t)
			if _, err := db.Exec(stmt, a, b); err != nil {
				// Statements that use only $1 reject the extra argument.
				if _, err2 := db.Exec(stmt, a); err2 != nil {
					t.Fatalf("seed %s: %v / %v", kind, err, err2)
				}
			}
			before := readAccount(t, a)
			logs := pac71Logs(t)
			assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
			assertUnboundUnchanged(t, a, before)
			if !strings.Contains(logs.String(), a.String()) || !strings.Contains(logs.String(), "first non-empty kind") {
				t.Fatalf("log does not name the account and kind: %s", logs.String())
			}
		})
	}
	t.Run("GA recorded and a Slack connection", func(t *testing.T) {
		d := pac71Domain()
		a := pac71Legacy(t, "henry@"+d, "google", pac71GoogleID())
		execT(t, pac71Kinds["slack"], a)
		before := readAccount(t, a)
		assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
		assertUnboundUnchanged(t, a, before)
	})
}

// AC-29: letter case, and case collisions between two legacy accounts.
func TestPAC71_AC29_LetterCaseAndCollisions(t *testing.T) {
	d := pac71Domain()
	pac71Bound(t, "henry@"+d, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
	assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "HENRY@"+strings.ToUpper(d), true, "")))

	gx := pac71GoogleID()
	lower := pac71Legacy(t, "x@"+d, "google", gx)
	upper := pac71Legacy(t, "X@"+d, "google", pac71GoogleID())
	bl, bu := readAccount(t, lower), readAccount(t, upper)
	logs := pac71Logs(t)
	// Even rule (a)'s identifier matching one of them is refused.
	assertRefused(t, pac71Google(t, googleProfile(gx, "x@"+d, true, d)))
	assertUnboundUnchanged(t, lower, bl)
	assertUnboundUnchanged(t, upper, bu)
	if !strings.Contains(logs.String(), "case collision") {
		t.Fatalf("log does not name the collision: %s", logs.String())
	}
	// A new account created from Zoe@ is found by zoe@ (one carrier).
	uid, _ := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), "Zoe@"+d, true, d)))
	if id, ok, err := storage.ConfirmedHolder(openTestDB(t), "zoe@"+d); err != nil || !ok || id != uid {
		t.Fatalf("zoe@ resolves to %s %v %v, want %s", id, ok, err, uid)
	}
}

// AC-20: a Microsoft sign-in matching Henry only by address changes neither
// his organisation nor his work calendar.
func TestPAC71_AC20_RefusalChangesNoOrganisationOrWorkCalendar(t *testing.T) {
	d := pac71Domain()
	henry := pac71Bound(t, "henry@"+d, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
	db := openTestDB(t)
	if err := storage.AssociateUserWithOrg(db, henry, "henry@"+d); err != nil {
		t.Fatal(err)
	}
	execT(t, `INSERT INTO settings (user_id, calendar_provider) VALUES ($1, 'google')`, henry)
	before := readAccount(t, henry)
	assertRefused(t, pac71Microsoft(t, pac71GUID(), "henry@"+d, orgAnswer(pac71GUID(), d)))
	if after := readAccount(t, henry); after != before {
		t.Fatalf("Henry changed: %+v -> %+v", before, after)
	}
}

// AC-40: the pre-emptive squat. Granting sites are covered in
// pac71_address_test.go; here: Henry is refused, A is unchanged, the log
// names A.
func TestPAC71_AC40_SquatterGainsNothingAndHenryIsRefusedNotMerged(t *testing.T) {
	d := pac71Domain()
	ga := pac71GoogleID()
	a, _ := sessionOf(t, pac71Google(t, googleProfile(ga, "henry@"+d, true, "")))
	if st := readAccount(t, a); st.standing != storage.AddressUnconfirmed {
		t.Fatalf("squatter's address is %s, want unconfirmed", st.standing)
	}
	before := readAccount(t, a)
	logs := pac71Logs(t)
	assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
	if after := readAccount(t, a); after != before {
		t.Fatal("A changed")
	}
	if !strings.Contains(logs.String(), a.String()) {
		t.Fatalf("log does not name A: %s", logs.String())
	}
	if meStatus(t, a, 0) != http.StatusOK {
		t.Fatal("A's session ended on a refusal")
	}
}

// AC-19 (backend half): every refusal cause answers the same Location, and it
// is not the generic failure state.
func TestPAC71_AC19_OneRefusalStateForEveryCause(t *testing.T) {
	if pac71RefusedURL == pac35FailureURL {
		t.Fatal("refusal and failure share a state")
	}
	d := pac71Domain()
	pac71Bound(t, "henry@"+d, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
	locs := map[string]string{}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"bound elsewhere":   pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)),
		"no evidence":       pac71Google(t, googleProfile(pac71GoogleID(), "x@"+pac71Domain(), false, "")),
		"tenant unobtained": pac71Microsoft(t, pac71GUID(), "y@"+d, orgStatus(http.StatusForbidden)),
	} {
		locs[name] = rec.Header().Get("Location")
		assertRefused(t, rec)
	}
	if len(locs) != 3 {
		t.Fatal(fmt.Sprint(locs))
	}
}
