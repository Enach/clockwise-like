package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-71 address standing at the address-as-person sites (spec §3 "What
// standing allows"; AC-28, AC-29 (granting resolves case), AC-38, AC-40,
// AC-43) and step-up check 4b (AC-21).

func linkHosts(t *testing.T, link string) map[uuid.UUID]string {
	t.Helper()
	rows, err := openTestDB(t).Query(`SELECT user_id, status FROM scheduling_link_hosts WHERE link_id = $1`, link)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var s string
		if err := rows.Scan(&id, &s); err != nil {
			t.Fatal(err)
		}
		out[id] = s
	}
	return out
}

func newLink(t *testing.T, r http.Handler, owner uuid.UUID, coHosts ...string) string {
	t.Helper()
	body := `{"title":"L","slug":"pac71-` + uuid.NewString()[:8] + `","duration_options":[30]`
	if len(coHosts) > 0 {
		body += `,"co_host_emails":["` + strings.Join(coHosts, `","`) + `"]`
	}
	body += `}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/scheduling-links", strings.NewReader(body)), owner))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create link = %d %s", rec.Code, rec.Body.String())
	}
	var id string
	_ = openTestDB(t).QueryRow(`SELECT id FROM scheduling_links WHERE owner_user_id = $1 ORDER BY created_at DESC LIMIT 1`, owner).Scan(&id)
	return id
}

// AC-28 and AC-40: every granting site resolves an unconfirmed address to
// nobody, and a domain-confirmed one (in any letter case, AC-29) as today.
func TestPAC71_AC28_AC40_GrantingSitesResolveOnlyDomainConfirmedHolders(t *testing.T) {
	links := setupSchedulingLinkRoutes(t)
	owner := createConfirmedUser(t, "owner-"+uuid.NewString()[:8]+"@example.com")
	for _, standing := range []string{storage.AddressUnconfirmed, storage.AddressDomainConfirmed} {
		t.Run(standing, func(t *testing.T) {
			d := pac71Domain()
			email := "henry@" + d
			var holder uuid.UUID
			if standing == storage.AddressUnconfirmed {
				// AC-40: the squatter's account, created with mailbox evidence.
				holder, _ = sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), email, true, "")))
			} else {
				holder = pac71Bound(t, email, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
			}
			asked := "Henry@" + strings.ToUpper(d) // AC-29: any letter case
			resolves := standing == storage.AddressDomainConfirmed

			t.Run("co-host on create", func(t *testing.T) {
				hosts := linkHosts(t, newLink(t, links, owner, asked))
				if _, ok := hosts[holder]; ok != resolves {
					t.Fatalf("hosts = %v", hosts)
				}
			})
			t.Run("co-host add", func(t *testing.T) {
				link := newLink(t, links, owner)
				rec := httptest.NewRecorder()
				links.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+link+"/hosts", strings.NewReader(`{"email":"`+asked+`"}`)), owner))
				if (rec.Code == http.StatusCreated) != resolves || (!resolves && (rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "user not found"))) {
					t.Fatalf("= %d %s", rec.Code, rec.Body.String())
				}
			})
			t.Run("co-host list patch, new entry", func(t *testing.T) {
				link := newLink(t, links, owner)
				rec := httptest.NewRecorder()
				links.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPatch, "/api/scheduling-links/"+link, strings.NewReader(`{"co_host_emails":["`+asked+`"]}`)), owner))
				if rec.Code != http.StatusOK {
					t.Fatalf("= %d %s", rec.Code, rec.Body.String())
				}
				if _, ok := linkHosts(t, link)[holder]; ok != resolves {
					t.Fatal("patch resolution wrong")
				}
			})
			t.Run("team invite acceptance", func(t *testing.T) {
				team, err := storage.CreateTeam(openTestDB(t), "T", owner)
				if err != nil {
					t.Fatal(err)
				}
				inv, err := storage.CreateTeamInvite(openTestDB(t), team.ID, asked, owner)
				if err != nil {
					t.Fatal(err)
				}
				rec := httptest.NewRecorder()
				setupTeamRoutes(t).ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/teams/invites/"+inv.Token+"/accept", nil), holder))
				if resolves && rec.Code != http.StatusNoContent {
					t.Fatalf("= %d %s", rec.Code, rec.Body.String())
				}
				if !resolves && (rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "this invite is for a different email address")) {
					t.Fatalf("= %d %s", rec.Code, rec.Body.String())
				}
			})
			t.Run("manager member add", func(t *testing.T) {
				m := setupManagerRoutes(t, &fakeManagerWorkflow{})
				rec := managerRequest(t, m, http.MethodPost, "/api/manager/team/members", owner, map[string]string{"email": asked, "cadence": "weekly"})
				if rec.Code != http.StatusCreated {
					t.Fatalf("= %d %s", rec.Code, rec.Body.String())
				}
				var mu *uuid.UUID
				_ = openTestDB(t).QueryRow(`SELECT member_user_id FROM manager_team_members WHERE manager_user_id = $1 AND lower(member_email) = lower($2)`, owner, asked).Scan(&mu)
				if (mu != nil && *mu == holder) != resolves {
					t.Fatalf("member_user_id = %v", mu)
				}
			})
			t.Run("roster entry for a formal member", func(t *testing.T) {
				o := createConfirmedUser(t, "mgr-"+uuid.NewString()[:8]+"@example.com")
				team := createManagerTestTeam(t, o, "F")
				if err := storage.AddTeamMember(openTestDB(t), team.ID, holder, "member"); err != nil {
					t.Fatal(err)
				}
				m := setupManagerRoutes(t, &fakeManagerWorkflow{})
				rec := managerRequest(t, m, http.MethodPatch, "/api/manager/team/members/"+url.PathEscape(email)+"?team_id="+team.ID.String(), o, map[string]string{"cadence": "weekly"})
				if (rec.Code == http.StatusOK) != resolves {
					t.Fatalf("= %d %s", rec.Code, rec.Body.String())
				}
			})
			t.Run("roster import resolution", func(t *testing.T) {
				id, ok, err := storage.ConfirmedHolder(openTestDB(t), asked)
				if err != nil || ok != resolves || (ok && id != holder) {
					t.Fatalf("ConfirmedHolder = %s %v %v", id, ok, err)
				}
			})
		})
	}
}

// AC-43 and AC-28's revoking and keeping sites: they act on every carrier.
func TestPAC71_AC43_RemovalAndKeepActOnEveryCarrier(t *testing.T) {
	m := setupManagerRoutes(t, &fakeManagerWorkflow{})
	owner := createConfirmedUser(t, "mgr-"+uuid.NewString()[:8]+"@example.com")
	t.Run("an unconfirmed member is removed", func(t *testing.T) {
		team := createManagerTestTeam(t, owner, "R")
		e := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
		var email string
		_ = openTestDB(t).QueryRow(`SELECT email FROM users WHERE id = $1`, e).Scan(&email)
		if err := storage.AddTeamMember(openTestDB(t), team.ID, e, "member"); err != nil {
			t.Fatal(err)
		}
		rec := managerRequest(t, m, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape(email)+"?team_id="+team.ID.String(), owner, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("= %d", rec.Code)
		}
		if _, err := storage.GetTeamMember(openTestDB(t), team.ID, e); err == nil {
			t.Fatal("E is still a member")
		}
	})
	t.Run("both case-collided accounts are removed", func(t *testing.T) {
		team := createManagerTestTeam(t, owner, "C")
		d := pac71Domain()
		x1 := pac71Legacy(t, "x@"+d, "google", "")
		x2 := pac71Legacy(t, "X@"+d, "google", "")
		for _, id := range []uuid.UUID{x1, x2} {
			if err := storage.AddTeamMember(openTestDB(t), team.ID, id, "member"); err != nil {
				t.Fatal(err)
			}
		}
		managerRequest(t, m, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape("x@"+d)+"?team_id="+team.ID.String(), owner, nil)
		for _, id := range []uuid.UUID{x1, x2} {
			if _, err := storage.GetTeamMember(openTestDB(t), team.ID, id); err == nil {
				t.Fatal("a carrier is still a member")
			}
		}
	})
	t.Run("the owner among the carriers: 422, nothing removed", func(t *testing.T) {
		d := pac71Domain()
		own := pac71Legacy(t, "own@"+d, "google", "")
		team := createManagerTestTeam(t, own, "O")
		rec := managerRequest(t, m, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape("OWN@"+d)+"?team_id="+team.ID.String(), own, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("= %d", rec.Code)
		}
	})
	t.Run("no carrier: still 204", func(t *testing.T) {
		team := createManagerTestTeam(t, owner, "N")
		rec := managerRequest(t, m, http.MethodDelete, "/api/manager/team/members/nobody%40nowhere.test?team_id="+team.ID.String(), owner, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("= %d", rec.Code)
		}
	})
	t.Run("an accepted mixed-case or unconfirmed co-host is kept", func(t *testing.T) {
		links := setupSchedulingLinkRoutes(t)
		lo := createConfirmedUser(t, "lo-"+uuid.NewString()[:8]+"@example.com")
		d := pac71Domain()
		pat := pac71Legacy(t, "Pat@"+strings.ToUpper(d[:1])+d[1:], "google", "")
		link := newLink(t, links, lo)
		execT(t, `INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'accepted')`, link, pat)
		rec := httptest.NewRecorder()
		links.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPatch, "/api/scheduling-links/"+link, strings.NewReader(`{"co_host_emails":["pat@`+d+`"]}`)), lo))
		if rec.Code != http.StatusOK {
			t.Fatalf("= %d %s", rec.Code, rec.Body.String())
		}
		if s := linkHosts(t, link)[pat]; s != "accepted" {
			t.Fatalf("pat = %q, want kept accepted", s)
		}
	})
	t.Run("the manager's roster merges formal members at any standing", func(t *testing.T) {
		team := createManagerTestTeam(t, owner, "L")
		e := pac71Legacy(t, "Unconfirmed@"+pac71Domain(), "google", "")
		if err := storage.AddTeamMember(openTestDB(t), team.ID, e, "member"); err != nil {
			t.Fatal(err)
		}
		rec := managerRequest(t, m, http.MethodGet, "/api/manager/team?team_id="+team.ID.String(), owner, nil)
		if rec.Code != http.StatusOK || !strings.Contains(strings.ToLower(rec.Body.String()), "unconfirmed@") {
			t.Fatalf("= %d %s", rec.Code, rec.Body.String())
		}
	})
}

// AC-38: break-glass authorises the account that holds the listed address
// domain-confirmed, never a session's email claim.
func TestPAC71_AC38_BreakGlassIsTheAccountNotTheClaim(t *testing.T) {
	t.Run("domain-confirmed holder writes", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "bg-ok")
		e = newSSOEnv(t, u.Email)
		if rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), session(t, u, time.Now())); rec.Code != http.StatusCreated {
			t.Fatalf("= %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("unconfirmed (unbound legacy) holder does not", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "bg-unconfirmed")
		execT(t, `DELETE FROM user_identities WHERE user_id = $1`, u.ID)
		execT(t, `UPDATE users SET address_standing = 'unconfirmed' WHERE id = $1`, u.ID)
		e = newSSOEnv(t, u.Email)
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), session(t, u, time.Now()))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "sso_write_requires_admin_stepup") {
			t.Fatalf("= %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("a listed claim on an account whose address was freed does not", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "bg-freed")
		listed := u.Email
		sess := session(t, u, time.Now()) // carries the listed email claim
		if _, err := storage.FreeAddress(context.Background(), e.db, u.ID, false, storage.OperatorRun{Operator: "op", Reason: "AC-38"}); err != nil {
			t.Fatal(err)
		}
		execT(t, `UPDATE users SET session_generation = 0 WHERE id = $1`, u.ID) // keep the session valid: only the claim is in question
		e = newSSOEnv(t, listed)
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "sso_write_requires_admin_stepup") {
			t.Fatalf("= %d %s", rec.Code, rec.Body.String())
		}
	})
}

// AC-21: step-up check 4b.
func TestPAC71_AC21_StepupMustBeTheBoundIdentity(t *testing.T) {
	t.Run("Google", func(t *testing.T) {
		for _, c := range []struct {
			sub  string
			want string
		}{{"bound", "stepup=granted"}, {"G2-other", "stepup=denied&reason=identity_mismatch"}} {
			bound := "G1-" + uuid.NewString()
			e := newSSOEnv(t)
			u, org := e.member(t, "g-4b")
			execT(t, `UPDATE user_identities SET provider = 'google', tenant = '', subject = $2 WHERE user_id = $1`, u.ID, bound)
			e.google.set(googleAdmin(u.Email, org.Domain))
			sub := c.sub
			if sub == "bound" {
				sub = bound
			}
			e.google.set(func(i *stepupIdP) { i.claims["sub"] = sub })
			rec := e.stepup(t, "google", session(t, u, time.Now()))
			wantOutcome(t, rec, rec.Code, c.want)
			if grants := e.liveGrants(t, u.ID); (grants == 1) != (c.want == "stepup=granted") {
				t.Fatalf("grants = %d", grants)
			}
		}
	})
	t.Run("Microsoft", func(t *testing.T) {
		for _, c := range []struct {
			tenant, oid, want string
		}{
			{"tenant-abc", "BOUND", "stepup=granted"},
			{"tenant-abc", "O2", "stepup=denied&reason=identity_mismatch"},
			{"tenant-other", "O2", "stepup=granted"}, // bound in another tenant: 4b does not apply
		} {
			e := newSSOEnv(t)
			u, org := e.member(t, "m-4b")
			o1 := strings.ToLower("O1-" + uuid.NewString())
			execT(t, `UPDATE user_identities SET provider = 'microsoft', tenant = $2, subject = $3 WHERE user_id = $1`, u.ID, c.tenant, o1)
			oid := c.oid
			if oid == "BOUND" {
				oid = strings.ToUpper(o1)
			}
			e.ms.set(microsoftAdmin(u.Email, "tenant-abc", org.Domain))
			e.ms.set(func(i *stepupIdP) { i.claims["oid"] = oid })
			rec := e.stepup(t, "microsoft", session(t, u, time.Now()))
			wantOutcome(t, rec, rec.Code, c.want)
		}
	})
	t.Run("an SSO-bound account keeps today's checks", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "s-4b")
		e.google.set(googleAdmin(u.Email, org.Domain))
		rec := e.stepup(t, "google", session(t, u, time.Now()))
		wantOutcome(t, rec, rec.Code, "stepup=granted")
	})
}
