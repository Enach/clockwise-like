package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/storage"
)

// PAC-89: the Microsoft step-up holds its ID token to PAC-71 sign-in's rules
// (contract PAC-89 section 2), through the real router, the callback and the
// Microsoft stand-in.

const (
	pac89TenantA = "a89a0000-0000-4000-8000-00000000000a"
	pac89TenantB = "b89b0000-0000-4000-8000-00000000000b"
	// pac89TenantG is spelled in upper-case hex (AC-17).
	pac89TenantG = "C89C0000-0000-4000-8000-0000000000CF"
)

type pac89Case struct {
	e   *ssoEnv
	u   *storage.User
	org *storage.Org
}

// pac89Env is a fresh environment (fresh verifier, so key requests are
// counted from zero) with a Microsoft admin of tenant tid, an optional seeded
// pin, and mutate applied to the stand-in.
func pac89Env(t *testing.T, tid, pin string, mutate func(i *stepupIdP)) *pac89Case {
	t.Helper()
	e := newSSOEnv(t)
	u, org := e.member(t, "m89")
	if pin != "" {
		if _, err := e.db.Exec(`UPDATE organizations SET ms_tenant_id = $2 WHERE id = $1`, org.ID, pin); err != nil {
			t.Fatal(err)
		}
	}
	e.ms.set(microsoftAdmin(u.Email, tid, org.Domain))
	if mutate != nil {
		e.ms.set(mutate)
	}
	return &pac89Case{e: e, u: u, org: org}
}

func (c *pac89Case) run(t *testing.T) *pac89Outcome {
	t.Helper()
	rec := c.e.stepup(t, "microsoft", session(t, c.u, time.Now()))
	o := &pac89Outcome{c: c, code: rec.Code, header: rec.Header()}
	return o
}

type pac89Outcome struct {
	c      *pac89Case
	code   int
	header http.Header
}

func (o *pac89Outcome) Header() http.Header { return o.header }

func (o *pac89Outcome) pin(t *testing.T) string {
	t.Helper()
	p, err := storage.GetOrgMSTenantID(context.Background(), o.c.e.db, o.c.org.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// denied asserts a denial with reason: the redirect, zero live grants, one
// denied event, the pin unchanged and zero Graph requests.
func (o *pac89Outcome) denied(t *testing.T, reason, pinBefore string) {
	t.Helper()
	wantOutcome(t, o, o.code, "stepup=denied&reason="+reason)
	if n := o.c.e.liveGrants(t, o.c.u.ID); n != 0 {
		t.Fatalf("%d live grants after a denial", n)
	}
	wantOneStepupEvent(t, o.c.e, o.c.u, o.c.org, "microsoft", "denied", reason)
	if p := o.pin(t); p != pinBefore {
		t.Fatalf("pin = %q, want %q", p, pinBefore)
	}
	o.c.e.ms.mu.Lock()
	defer o.c.e.ms.mu.Unlock()
	if o.c.e.ms.graphHits != 0 {
		t.Fatalf("%d Graph requests, want 0", o.c.e.ms.graphHits)
	}
}

func (o *pac89Outcome) tokenInvalid(t *testing.T) { t.Helper(); o.denied(t, "token_invalid", "") }

func (o *pac89Outcome) granted(t *testing.T, pin string) {
	t.Helper()
	wantOutcome(t, o, o.code, "stepup=granted")
	if n := o.c.e.liveGrants(t, o.c.u.ID); n != 1 {
		t.Fatalf("%d live grants, want 1", n)
	}
	wantOneStepupEvent(t, o.c.e, o.c.u, o.c.org, "microsoft", "granted", "")
	if p := o.pin(t); p != pin {
		t.Fatalf("pin = %q, want %q", p, pin)
	}
}

func (o *pac89Outcome) keyPaths() []string {
	o.c.e.ms.mu.Lock()
	defer o.c.e.ms.mu.Unlock()
	return append([]string(nil), o.c.e.ms.keyPaths...)
}

// AC-1 (guard): an issuer naming another tenant than tid.
func TestPAC89_AC1_IssuerOfAnotherTenant(t *testing.T) {
	c := pac89Env(t, pac89TenantA, "", func(i *stepupIdP) { i.claims["iss"] = i.srv.URL + "/" + pac89TenantB + "/v2.0" })
	c.run(t).tokenInvalid(t)
}

// AC-2: keys come from the token's own tenant, not the multi-tenant set.
func TestPAC89_AC2_KeysAreTheTenantsOwn(t *testing.T) {
	c := pac89Env(t, pac89TenantA, "", func(i *stepupIdP) { i.tenantKeysOff = true })
	o := c.run(t)
	o.tokenInvalid(t)
	if got := o.keyPaths(); len(got) != 1 || got[0] != "/"+pac89TenantA+"/discovery/v2.0/keys" {
		t.Fatalf("key requests %v", got)
	}
	pac89Env(t, pac89TenantA, "", nil).run(t).granted(t, pac89TenantA)
}

// AC-3: a non-GUID tid is refused with zero key requests.
func TestPAC89_AC3_NonGUIDTenant(t *testing.T) {
	for name, tid := range map[string]any{
		"t1": "t1", "common": "common", "organizations": "organizations",
		"slash": "a/b", "dots": "..", "empty": "", "absent": nil,
	} {
		t.Run(name, func(t *testing.T) {
			c := pac89Env(t, pac89TenantA, "", func(i *stepupIdP) { i.claims["tid"] = tid })
			o := c.run(t)
			o.tokenInvalid(t)
			if got := o.keyPaths(); len(got) != 0 {
				t.Fatalf("key requests %v, want none", got)
			}
		})
	}
}

// AC-4: ver must be 2.0.
func TestPAC89_AC4_Version(t *testing.T) {
	for name, ver := range map[string]any{"absent": nil, "1.0": "1.0"} {
		t.Run(name, func(t *testing.T) {
			pac89Env(t, pac89TenantA, "", func(i *stepupIdP) { i.claims["ver"] = ver }).run(t).tokenInvalid(t)
		})
	}
}

// AC-5: aud is the client id, as a string or a one-element array.
func TestPAC89_AC5_Audience(t *testing.T) {
	t.Run("two elements", func(t *testing.T) {
		pac89Env(t, pac89TenantA, "", func(i *stepupIdP) { i.claims["aud"] = []string{i.clientID, "other"} }).run(t).tokenInvalid(t)
	})
	t.Run("string", func(t *testing.T) {
		pac89Env(t, pac89TenantA, "", func(i *stepupIdP) { i.claims["aud"] = i.clientID }).run(t).granted(t, pac89TenantA)
	})
	t.Run("one element", func(t *testing.T) {
		pac89Env(t, pac89TenantA, "", func(i *stepupIdP) { i.claims["aud"] = []string{i.clientID} }).run(t).granted(t, pac89TenantA)
	})
}

// AC-6: oid must be a GUID.
func TestPAC89_AC6_ObjectID(t *testing.T) {
	for name, oid := range map[string]any{"absent": nil, "not a GUID": "O2"} {
		t.Run(name, func(t *testing.T) {
			pac89Env(t, pac89TenantA, "", func(i *stepupIdP) { i.claims["oid"] = oid }).run(t).tokenInvalid(t)
		})
	}
}

// AC-7: exp and nbf with 60 s of skew, against the injected clock.
func TestPAC89_AC7_ClockSkew(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, c := range []struct {
		name    string
		claims  map[string]any
		granted bool
	}{
		{"exp 30 s ago", map[string]any{"exp": now.Add(-30 * time.Second).Unix()}, true},
		{"exp 90 s ago", map[string]any{"exp": now.Add(-90 * time.Second).Unix()}, false},
		{"nbf in 2 min", map[string]any{"exp": now.Add(time.Hour).Unix(), "nbf": now.Add(2 * time.Minute).Unix()}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := pac89Env(t, pac89TenantA, "", func(i *stepupIdP) {
				for k, v := range c.claims {
					i.claims[k] = v
				}
			})
			env.e.h.stepup.microsoft.MicrosoftIDTokens().Now = func() time.Time { return now }
			o := env.run(t)
			if c.granted {
				o.granted(t, pac89TenantA)
			} else {
				o.tokenInvalid(t)
			}
		})
	}
}

// AC-8: alg, kid and nonce.
func TestPAC89_AC8_HeaderAndNonce(t *testing.T) {
	for name, mutate := range map[string]func(i *stepupIdP){
		"alg names another algorithm": func(i *stepupIdP) { i.header = map[string]any{"alg": "PS256"} },
		"no kid":                      func(i *stepupIdP) { i.header = map[string]any{"kid": nil} },
		"another nonce":               func(i *stepupIdP) { i.claims["nonce"] = "not-this-stepup" },
		"no nonce":                    func(i *stepupIdP) { i.claims["nonce"] = nil },
	} {
		t.Run(name, func(t *testing.T) {
			pac89Env(t, pac89TenantA, "", mutate).run(t).tokenInvalid(t)
		})
	}
}

// AC-9: the consumer tenant is refused as tid_mismatch before Graph.
func TestPAC89_AC9_ConsumerTenant(t *testing.T) {
	c := pac89Env(t, auth.MicrosoftConsumerTenant, "", nil)
	c.run(t).denied(t, "tid_mismatch", "")
}

// AC-11: the key fetch is bounded by the identity-provider limit, and a
// failing key server is a token-invalid denial, never a 5xx.
func TestPAC89_AC11_KeyServerFailures(t *testing.T) {
	t.Run("never answers", func(t *testing.T) {
		lim := outbound.Current()
		lim.Identity = 300 * time.Millisecond
		t.Cleanup(outbound.SetForTest(lim))
		release := make(chan struct{})
		c := pac89Env(t, pac89TenantA, "", func(i *stepupIdP) {
			i.keysHook = func(http.ResponseWriter, *http.Request) { <-release }
		})
		t.Cleanup(func() { close(release) }) // runs before the stand-in closes
		start := time.Now()
		o := c.run(t)
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("answered after %v, want within 2 s", d)
		}
		o.tokenInvalid(t)
	})
	t.Run("500", func(t *testing.T) {
		pac89Env(t, pac89TenantA, "", func(i *stepupIdP) {
			i.keysHook = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", http.StatusInternalServerError) }
		}).run(t).tokenInvalid(t)
	})
	t.Run("over 1 MiB", func(t *testing.T) {
		pac89Env(t, pac89TenantA, "", func(i *stepupIdP) {
			i.keysHook = func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"keys":[],"pad":"` + strings.Repeat("x", 1<<20) + `"}`))
			}
		}).run(t).tokenInvalid(t)
	})
}

// AC-13: a conforming administrator is granted, with or without wids, and
// the pin is the lower-case tenant.
func TestPAC89_AC13_AdminGranted(t *testing.T) {
	for _, viaGraph := range []bool{false, true} {
		c := pac89Env(t, pac89TenantG, "", func(i *stepupIdP) {
			if viaGraph {
				i.claims["wids"] = nil
				i.roles = []string{globalAdmin}
			}
		})
		c.run(t).granted(t, strings.ToLower(pac89TenantG))
	}
}

// AC-17: tenant ids compare ignoring case; a grant stores the lower case.
func TestPAC89_AC17_TenantCase(t *testing.T) {
	lowerG := strings.ToLower(pac89TenantG)
	t.Run("upper-case pin, lower-case token and Graph", func(t *testing.T) {
		pac89Env(t, lowerG, pac89TenantG, nil).run(t).granted(t, lowerG)
	})
	t.Run("no pin, Graph in the other case", func(t *testing.T) {
		pac89Env(t, lowerG, "", func(i *stepupIdP) { i.orgID = pac89TenantG }).run(t).granted(t, lowerG)
	})
	t.Run("pinned to another GUID", func(t *testing.T) {
		c := pac89Env(t, lowerG, pac89TenantB, nil)
		o := c.run(t)
		wantOutcome(t, o, o.code, "stepup=denied&reason=tid_changed")
		if n := c.e.liveGrants(t, c.u.ID); n != 0 {
			t.Fatalf("%d grants", n)
		}
		wantOneStepupEvent(t, c.e, c.u, c.org, "microsoft", "denied", "tid_changed")
		if p := o.pin(t); p != pac89TenantB {
			t.Fatalf("pin = %q", p)
		}
	})
}
