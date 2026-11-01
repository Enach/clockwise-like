package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-73 Part 2 (spec docs/specs/NEW-llm-bring-your-own-part2.md §3.1,
// §3.1a, §3.4, §3.5): which configuration each request uses, on parse,
// brief refresh, the probe and the settings read, with recording transports
// so that every request to any provider is seen (the byok harness).

const (
	p2KeyA  = "sk-p2-KA-org/with/slash"
	p2KeyA2 = "sk-p2-KA2-rotated"
	p2KeyW  = "sk-p2-KW-next-version"
	p2KeyU  = "sk-p2-KU-own-key"
	p2Mark  = "GATEWAY-MARKER-X-7731"
)

// p2Org puts the byok user into the organisation of domain and returns its id.
func p2Org(t *testing.T, b *byok, domain string) uuid.UUID {
	t.Helper()
	db := openTestDB(t)
	org, err := storage.UpsertOrg(db, domain, domain)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.SetUserOrg(db, b.user, org.ID); err != nil {
		t.Fatal(err)
	}
	return org.ID
}

func p2Member(t *testing.T, label string, org uuid.UUID) *byok {
	t.Helper()
	b := newBYOK(t, label)
	if err := storage.SetUserOrg(openTestDB(t), b.user, org); err != nil {
		t.Fatal(err)
	}
	return b
}

// p2Admin changes org's AI configuration as a proven administrator (a grant
// of this purpose) through the real one-transaction write.
func p2Admin(t *testing.T, admin, org uuid.UUID, patch storage.OrgAIPatch) storage.OrgAIConfig {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	cur, err := storage.GetOrgAIConfig(ctx, db, org)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.IssueSSOStepupGrant(ctx, db, admin, org, "google", storage.SSOGrantPurposeLLMOrgKeyWrite, time.Now()); err != nil {
		t.Fatal(err)
	}
	out, err := storage.WriteOrgAIConfig(ctx, db, storage.OrgAIWrite{UserID: admin, OrgID: org, ExpectedVersion: cur.Version, Patch: patch, Now: time.Now()})
	if err != nil || out.Status != storage.OrgAIStored {
		t.Fatalf("admin change %+v = %+v, %v", patch, out, err)
	}
	return out.Config
}

func sp(v string) *string { return &v }
func bp(v bool) *bool     { return &v }

func p2OpenAI(key string) storage.OrgAIPatch {
	return storage.OrgAIPatch{Provider: sp("openai"), APIKey: sp(key)}
}

// orgAIFor is the organisation-AI handler set over the shared database.
func orgAIFor(t *testing.T) *orgAIHandlers {
	t.Helper()
	return newOrgAIHandlers(openTestDB(t), &ssoHandlers{clock: engine.SystemClock, breakGlass: map[string]bool{}})
}

// confirm calls confirmOrganizationAI for the byok user with version.
func (b *byok) confirm(version int64) *httptest.ResponseRecorder {
	b.t.Helper()
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/organization/ai/consent", strings.NewReader(fmt.Sprintf(`{"configurationVersion":%d}`, version))), b.user)
	w := httptest.NewRecorder()
	orgAIFor(b.t).confirm(w, req)
	return w
}

func (b *byok) mustConfirm(version int64) {
	b.t.Helper()
	if w := b.confirm(version); w.Code != http.StatusOK {
		b.t.Fatalf("confirm %d = %d %s", version, w.Code, w.Body.String())
	}
}

// sentKeys are the keys the recorded requests carried, in any provider's
// header.
func (b *byok) sentKeys() []string {
	var out []string
	for _, r := range b.requests() {
		for _, h := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Goog-Api-Key"} {
			if v := r.Header.Get(h); v != "" {
				out = append(out, strings.TrimPrefix(v, "Bearer "))
			}
		}
	}
	return out
}

// sentOnly asserts that requests were made and every one carried key to
// host.
func (b *byok) sentOnly(what, key, host string) {
	b.t.Helper()
	reqs := b.requests()
	if len(reqs) == 0 {
		b.t.Fatalf("%s: nothing was sent, want requests with the key to %s", what, host)
	}
	for _, r := range reqs {
		if r.Host != host {
			b.t.Errorf("%s: a request went to %s, want only %s", what, r.Host, host)
		}
	}
	for _, k := range b.sentKeys() {
		if k != key {
			b.t.Errorf("%s: a request carried %q, want only %q", what, k, key)
		}
	}
}

// reset forgets what was recorded so far.
func (b *byok) reset() {
	b.rec = nlptest.Ambient(b.t, byokApproved)
	b.usr = nlptest.UserDestinations(b.t, "", "")
}

func (b *byok) parseSends(what, key, host string) {
	b.t.Helper()
	b.reset()
	if w := b.parse(); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "not_configured") {
		b.t.Fatalf("%s: parse = %d %s, want it sent", what, w.Code, w.Body.String())
	}
	b.sentOnly(what, key, host)
	b.reset()
}

// P2-AC-29, P2-AC-14, P2-AC-15, P2-AC-16: one account per reachable row;
// parse, refresh, probe {} and the settings read give that row; only rows 2
// and 4 send.
func TestPAC73P2_EveryRowOneAnswer(t *testing.T) {
	admin := newBYOK(t, "p2-rows-admin")
	complete := p2Org(t, admin, "p2-rows-complete.example")
	p2Admin(t, admin.user, complete, p2OpenAI(p2KeyA))
	incomplete := p2Org(t, newBYOK(t, "p2-rows-admin2"), "p2-rows-incomplete.example")
	fcAdmin := newBYOK(t, "p2-rows-admin4")
	fc := p2Org(t, fcAdmin, "p2-rows-fc.example")
	p2Admin(t, fcAdmin.user, fc, p2OpenAI(p2KeyA))
	p2Admin(t, fcAdmin.user, fc, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	fiAdmin := newBYOK(t, "p2-rows-admin5")
	fi := p2Org(t, fiAdmin, "p2-rows-fi.example")
	p2Admin(t, fiAdmin.user, fi, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})

	t.Run("row 1 off, even forbidden and confirmed", func(t *testing.T) { // P2-AC-16
		b := p2Member(t, "p2-row1", fc)
		b.mustConfirm(1)
		b.mustPatch(`{"llmProvider":""}`)
		b.allThree("off", "", "user")
	})
	t.Run("row 10", func(t *testing.T) {
		b := p2Member(t, "p2-row10", fc)
		b.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
		b.allThree("personal_keys_forbidden", "", "user")
	})
	t.Run("row 11", func(t *testing.T) {
		b := p2Member(t, "p2-row11", fi)
		b.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
		b.allThree("personal_keys_forbidden", "", "organization_admin")
	})
	t.Run("row 2 own key in a complete organisation", func(t *testing.T) {
		b := p2Member(t, "p2-row2", complete)
		b.mustPatch(`{"llmProvider":"anthropic","llmApiKey":"` + p2KeyU + `"}`)
		st := b.aiStatus()
		if st["configured"] != true || st["configuration"] != "own" || st["provider"] != "anthropic" {
			t.Fatalf("aiStatus = %v", st)
		}
		b.parseSends("row 2", p2KeyU, "api.anthropic.com")
	})
	t.Run("row 3 bedrock without key, before and after complete", func(t *testing.T) { // P2-AC-15
		b := p2Member(t, "p2-row3", incomplete)
		b.mustPatch(`{"llmProvider":"bedrock"}`)
		b.allThree("needs_key", "bedrock", "user")
		b2 := p2Member(t, "p2-row3b", complete)
		b2.mustPatch(`{"llmProvider":"bedrock"}`)
		b2.allThree("needs_key", "bedrock", "user")
	})
	t.Run("rows 6, 5, 4 for a post-Part-1 account", func(t *testing.T) { // P2-AC-14
		b := p2Member(t, "p2-row654", incomplete)
		b.allThree("nothing_configured", "", "user", "organization_admin")
		c := p2Member(t, "p2-row54", complete)
		c.allThree("organization_unconfirmed", "", "user")
		c.mustConfirm(1)
		st := c.aiStatus()
		if st["configured"] != true || st["configuration"] != "organization" || st["provider"] != "openai" || st["notConfigured"] != nil {
			t.Fatalf("aiStatus after confirming = %v", st)
		}
		c.parseSends("row 4", p2KeyA, "api.openai.com")
	})
	t.Run("row 6 forbids, not complete: only the admin", func(t *testing.T) {
		b := p2Member(t, "p2-row6f", fi)
		b.allThree("nothing_configured", "", "organization_admin")
	})
	t.Run("rows 7 and 8 for an Ollama account", func(t *testing.T) { // P2-AC-15
		b := p2Member(t, "p2-row7", complete)
		b.sql(`llm_provider = 'ollama'`)
		b.allThree("provider_removed_organization_available", "", "user")
		c := p2Member(t, "p2-row8", incomplete)
		c.sql(`llm_provider = 'ollama'`)
		c.allThree("provider_removed", "", "user", "organization_admin")
	})
}

// P2-AC-10, P2-AC-11, P2-AC-12, P2-AC-13: the follower's requests carry only
// the organisation's values; a member's own key goes only with them.
func TestPAC73P2_WhoseConfigurationIsSent(t *testing.T) {
	m1 := newBYOK(t, "p2-ac10-m1")
	org := p2Org(t, m1, "p2-ac10.example")
	p2Admin(t, m1.user, org, storage.OrgAIPatch{Provider: sp("azure_openai"), APIKey: sp(p2KeyA),
		AzureEndpoint: sp("https://" + byokAzure), AzureDeployment: sp("org-dep")})
	m1.mustConfirm(1)
	// M1's own account carries other destinations and an OpenAI gateway.
	m1.sql(`azure_endpoint = 'https://other.openai.azure.com', azure_deployment = 'own-dep', aws_region = 'ap-south-1', llm_base_url = 'https://api.openai.com'`)
	m2 := p2Member(t, "p2-ac10-m2", org)
	m2.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)

	m1.parseSends("M1 parse", p2KeyA, byokAzure)
	m1.reset()
	if w := m1.probe(`{}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configuration":"organization"`) {
		t.Fatalf("M1 probe {} = %d %s", w.Code, w.Body.String())
	}
	for _, r := range m1.requests() {
		if r.Path != "/openai/deployments/org-dep/chat/completions" {
			t.Errorf("M1 sent to %s%s, want the organisation's deployment", r.Host, r.Path)
		}
	}
	m1.sentOnly("M1 probe", p2KeyA, byokAzure)
	m2.parseSends("M2 parse", p2KeyU, "api.openai.com")
	m2.reset()
	if w := m2.probe(`{}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configuration":"own"`) {
		t.Fatalf("M2 probe {} = %d %s", w.Code, w.Body.String())
	}

	// P2-AC-11: M2 removes their own key: needs a key, nothing sent.
	m2.reset()
	m2.mustPatch(`{"llmApiKey":""}`)
	m2.allThree("needs_key", "openai", "user")

	// P2-AC-12, OpenAI with no gateway: the public host, whatever M1 stored.
	o := newBYOK(t, "p2-ac12")
	org2 := p2Org(t, o, "p2-ac12.example")
	p2Admin(t, o.user, org2, p2OpenAI(p2KeyA))
	o.mustConfirm(1)
	o.sql(`llm_base_url = 'https://gw.member.example/v1'`)
	o.parseSends("public host despite the member's gateway", p2KeyA, "api.openai.com")

	// P2-AC-13: the admin removes the key; followers answer "nothing
	// configured" naming the user and the admin; an own-key member is
	// unaffected.
	own := p2Member(t, "p2-ac13-own", org2)
	own.mustPatch(`{"llmProvider":"anthropic","llmApiKey":"` + p2KeyU + `"}`)
	p2Admin(t, o.user, org2, storage.OrgAIPatch{APIKey: sp("")})
	o.allThree("nothing_configured", "", "user", "organization_admin")
	own.parseSends("own key after the org key is removed", p2KeyU, "api.anthropic.com")
}

// P2-AC-18 to P2-AC-21: forbidding personal keys moves nobody and sends no
// personal key.
func TestPAC73P2_PersonalKeysForbidden(t *testing.T) {
	admin := newBYOK(t, "p2-ac18-admin")
	org := p2Org(t, admin, "p2-ac18.example")
	p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
	m2 := p2Member(t, "p2-ac18-m2", org)
	m2.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
	m3 := p2Member(t, "p2-ac18-m3", org) // never changes their choice
	m3.mustPatch(`{"llmProvider":"anthropic","llmApiKey":"` + p2KeyU + `"}`)
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})

	m2.allThree("personal_keys_forbidden", "", "user")
	w := m2.parse()
	if !strings.Contains(w.Body.String(), "kept but not used") || !strings.Contains(w.Body.String(), "Use my organisation") {
		t.Errorf("row 10 sentence = %s", w.Body.String())
	}
	// Confirming alone does not move M2.
	m2.mustConfirm(1)
	m2.allThree("personal_keys_forbidden", "", "user")
	// P2-AC-20: forbidden probes, nothing reaches any host.
	for _, body := range []string{`{"llmProvider":"openai"}`, `{"llmProvider":"anthropic","llmApiKey":"k"}`, `{"llmApiKey":"k"}`, `{"llmProvider":"organization","llmApiKey":"k"}`} {
		conflict409(t, "probe "+body, m2.probe(body), "personal_keys_forbidden", "", "organization_admin")
	}
	m2.noRequests("forbidden probes")
	// P2-AC-21: a personal key is still accepted and stored, never sent.
	doc := m2.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
	if ks, _ := doc["llmApiKeysSet"].(map[string]any); ks["openai"] != true {
		t.Fatalf("llmApiKeysSet = %v", doc["llmApiKeysSet"])
	}
	m2.allThree("personal_keys_forbidden", "", "user")
	// Choosing "Use my organisation's AI" and confirming the displayed
	// version moves M2 to row 4; the OpenAI key is still stored.
	m2.mustPatch(`{"llmProvider":"organization"}`)
	m2.parseNotConfigured("organization_unconfirmed", "", "user") // the openai write ended the confirmation
	m2.mustConfirm(1)
	m2.parseSends("M2 on the organisation", p2KeyA, "api.openai.com")
	if s := m2.settings(); s.LLMAPIKeys.OpenAI != p2KeyU {
		t.Fatal("M2's own key was rewritten")
	}
	// Allowed again: M3 is back on their own key with no action.
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(true)})
	m3.parseSends("M3 after personal keys are allowed again", p2KeyU, "api.anthropic.com")

	// P2-AC-19: forbidden with no key: own member row 11, follower only the
	// admin; revision 2.1: the removal keeps "forbidden".
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	p2Admin(t, admin.user, org, storage.OrgAIPatch{Provider: sp("")})
	cfg, _ := storage.GetOrgAIConfig(context.Background(), openTestDB(t), org)
	if cfg.PersonalKeysAllowed {
		t.Fatal("the removal re-allowed personal keys (spec revision 2.1)")
	}
	m3.allThree("personal_keys_forbidden", "", "organization_admin")
	f := p2Member(t, "p2-ac19-f", org)
	f.allThree("nothing_configured", "", "organization_admin")
	// A new provider (a provider change from nothing) keeps it too.
	p2Admin(t, admin.user, org, storage.OrgAIPatch{Provider: sp("anthropic"), APIKey: sp(p2KeyA)})
	m3.allThree("personal_keys_forbidden", "", "user")
}

// P2-AC-22 to P2-AC-25, P2-AC-36, P2-AC-38, P2-AC-42: telling and asking
// once, per version.
func TestPAC73P2_ConfirmationPerVersion(t *testing.T) {
	admin := newBYOK(t, "p2-ac22-admin")
	org := p2Org(t, admin, "p2-ac22.example")
	m := p2Member(t, "p2-ac22-m", org)
	m3 := p2Member(t, "p2-ac22-m3", org)
	// P2-AC-24: nothing to confirm yet.
	if w := m.confirm(1); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"reason":"organization_not_configured"`) {
		t.Fatalf("confirm with no configuration = %d %s", w.Code, w.Body.String())
	}
	p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
	// P2-AC-22: row 5 first use, nothing sent; the sentence names the provider.
	m.parseNotConfigured("organization_unconfirmed", "", "user")
	if w := m.parse(); !strings.Contains(w.Body.String(), "OpenAI") || !strings.Contains(w.Body.String(), "access") {
		t.Fatalf("row 5 = %s", w.Body.String())
	}
	// P2-AC-24: a stale or not-yet version is refused; nothing recorded.
	for _, v := range []int64{2, 7} {
		w := m.confirm(v)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"reason":"configuration_changed"`) || strings.Contains(w.Body.String(), p2KeyA) {
			t.Fatalf("confirm %d = %d %s", v, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"configurationVersion":0}`, `{"configurationVersion":"1"}`, `{"configurationVersion":1,"x":1}`, `[]`, `null`} {
		req := withUser(httptest.NewRequest(http.MethodPost, "/api/organization/ai/consent", strings.NewReader(body)), m.user)
		w := httptest.NewRecorder()
		orgAIFor(t).confirm(w, req)
		wantError(t, "consent "+body, w, http.StatusBadRequest, `invalid request: send {"configurationVersion": <the version you were shown>}`)
	}
	m.parseNotConfigured("organization_unconfirmed", "", "user")
	// Confirming twice succeeds twice; the 200 is the caller's AI status.
	for i := 0; i < 2; i++ {
		w := m.confirm(1)
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"configuration":"organization","configured":true,"inOrganization":true,"provider":"openai"}` {
			t.Fatalf("confirm #%d = %d %s", i, w.Code, w.Body.String())
		}
	}
	m.parseSends("after confirming", p2KeyA, "api.openai.com")
	// P2-AC-25: M's confirmation does not confirm M3.
	m3.parseNotConfigured("organization_unconfirmed", "", "user")

	// P2-AC-23: a model or personal-keys change does not re-ask; a key, a
	// destination or a provider change does.
	p2Admin(t, admin.user, org, storage.OrgAIPatch{Model: sp("gpt-4o")})
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	m.parseSends("after model and personal-keys changes", p2KeyA, "api.openai.com")
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(true)})
	for i, patch := range []storage.OrgAIPatch{{APIKey: sp(p2KeyA2)}, {BaseURL: sp("https://api.openai.com")}, {Provider: sp("anthropic"), APIKey: sp(p2KeyA)}} {
		cfg := p2Admin(t, admin.user, org, patch)
		m.allThree("organization_unconfirmed", "", "user")
		m.mustConfirm(cfg.Version)
		if i == 2 {
			m.parseSends("after re-confirming", p2KeyA, "api.anthropic.com")
		}
	}

	// P2-AC-36: choosing one's own provider or off ends the confirmation;
	// a settings write of "organization" records none (P2-AC-38).
	cur, _ := storage.GetOrgAIConfig(context.Background(), openTestDB(t), org)
	for _, choice := range []string{"", "openai"} {
		m.mustConfirm(cur.Version)
		m.mustPatch(`{"llmProvider":"` + choice + `"}`)
		m.mustPatch(`{"llmProvider":"organization"}`)
		m.allThree("organization_unconfirmed", "", "user")
	}
	// A whole settings document carrying "organization" records nothing.
	_, doc := m.get()
	delete(doc, "aiStatus")
	raw, _ := json.Marshal(doc)
	if w := m.patch(string(raw)); w.Code != http.StatusOK {
		t.Fatalf("whole-document write = %d %s", w.Code, w.Body.String())
	}
	m.parseNotConfigured("organization_unconfirmed", "", "user")
	// P2-AC-36 with a forbid: confirmed, then own key, then forbidden: row 10.
	m.mustConfirm(cur.Version)
	m.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	m.allThree("personal_keys_forbidden", "", "user")
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(true)})

	// P2-AC-38: V displayed, admin moves to V+1, the confirm of V is refused.
	m.mustPatch(`{"llmProvider":"organization"}`)
	shown := cur.Version
	next := p2Admin(t, admin.user, org, storage.OrgAIPatch{APIKey: sp(p2KeyW)})
	if w := m.confirm(shown); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), fmt.Sprintf(`"configurationVersion":%d`, next.Version)) {
		t.Fatalf("confirm of the displayed, now stale version = %d %s", w.Code, w.Body.String())
	}
	m.parseNotConfigured("organization_unconfirmed", "", "user")

	// P2-AC-42: confirmed V, removal, a new setup repeating the provider and
	// key: a version never had before, row 5, nothing sent.
	m.mustConfirm(next.Version)
	removed := p2Admin(t, admin.user, org, storage.OrgAIPatch{Provider: sp("")})
	again := p2Admin(t, admin.user, org, storage.OrgAIPatch{Provider: sp("openai"), APIKey: sp(p2KeyW)})
	if removed.Version != next.Version+1 || again.Version != next.Version+2 {
		t.Fatalf("versions %d -> %d -> %d, want strictly increasing", next.Version, removed.Version, again.Version)
	}
	m.allThree("organization_unconfirmed", "", "user")
	m.mustConfirm(again.Version)
	m.parseSends("after confirming the new setup", p2KeyW, "api.openai.com")
}

// P2-AC-28: the probe and the organisation's configuration.
func TestPAC73P2_ProbeAndTheOrganisation(t *testing.T) {
	m := newBYOK(t, "p2-ac28")
	org := p2Org(t, m, "p2-ac28.example")
	p2Admin(t, m.user, org, p2OpenAI(p2KeyA))
	u := p2Member(t, "p2-ac28-unconfirmed", org)
	conflict409(t, "unconfirmed {}", u.probe(`{}`), "organization_unconfirmed", "", "user")
	u.noRequests("unconfirmed probe")
	m.mustConfirm(1)
	for _, body := range []string{`{}`, `{"llmProvider":"organization"}`} {
		m.reset()
		w := m.probe(body)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configuration":"organization"`) || strings.Contains(w.Body.String(), p2KeyA) {
			t.Fatalf("probe %s = %d %s", body, w.Code, w.Body.String())
		}
		m.sentOnly("probe "+body, p2KeyA, "api.openai.com")
	}
	m.reset()
	conflict409(t, "naming openai, no key", m.probe(`{"llmProvider":"openai"}`), "needs_key", "openai", "user")
	m.noRequests("naming the organisation's provider")
	for _, body := range []string{`{"llmModel":"m"}`, `{"llmApiKey":"k"}`, `{"llmProvider":"organization","llmModel":"m"}`} {
		wantError(t, "probe "+body, m.probe(body), http.StatusBadRequest, "llmModel and llmApiKey cannot be used with your organisation's AI configuration")
	}
	m.noRequests("refused probes")
	w := m.probe(`{"llmProvider":"anthropic","llmApiKey":"` + p2KeyU + `"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configuration":"own"`) {
		t.Fatalf("anthropic with a key = %d %s", w.Code, w.Body.String())
	}
	m.sentOnly("anthropic with a key", p2KeyU, "api.anthropic.com")
}

// P2-AC-27: rows 5, 10 and 11 refresh nothing, whatever the stored brief.
func TestPAC73P2_BriefRefreshInRows5_10_11ChangesNothing(t *testing.T) {
	admin := newBYOK(t, "p2-ac27-admin")
	org := p2Org(t, admin, "p2-ac27.example")
	p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
	forbidsIncomplete := p2Org(t, newBYOK(t, "p2-ac27-admin2"), "p2-ac27-fi.example")
	p2Admin(t, admin.user, forbidsIncomplete, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	row5 := p2Member(t, "p2-ac27-r5", org)
	row10 := p2Member(t, "p2-ac27-r10", org)
	row10.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
	row11 := p2Member(t, "p2-ac27-r11", forbidsIncomplete)
	row11.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	db := openTestDB(t)
	for _, tc := range []struct {
		b      *byok
		reason string
		en     []string
	}{{row5, "organization_unconfirmed", []string{"user"}}, {row10, "personal_keys_forbidden", []string{"user"}}, {row11, "personal_keys_forbidden", []string{"organization_admin"}}} {
		for _, state := range []string{"none", "pending", "ready", "failed"} {
			ev := "evt-p2-" + state + "-" + tc.b.user.String()[:6]
			if state != "none" {
				if err := storage.UpsertMeetingBrief(db, &storage.MeetingBrief{CalendarEventID: ev, UserID: tc.b.user, GeneratedAt: time.Now().UTC(), Status: state, BriefText: "kept " + state}); err != nil {
					t.Fatal(err)
				}
			}
			before := tc.b.brief(http.MethodGet, ev).Body.String()
			conflict409(t, state, tc.b.brief(http.MethodPost, ev), tc.reason, "", tc.en...)
			if after := tc.b.brief(http.MethodGet, ev).Body.String(); after != before {
				t.Errorf("%s/%s: brief read changed %s -> %s", tc.reason, state, before, after)
			}
		}
		tc.b.noRequests(tc.reason)
	}
}

// P2-AC-41: a destination the operator no longer approves: nothing sent,
// the sentence is about the organisation's destination and its admin.
func TestPAC73P2_OrganisationDestinationRefusal(t *testing.T) {
	m := newBYOK(t, "p2-ac41")
	org := p2Org(t, m, "p2-ac41.example")
	nlptest.UserDestinations(t, "https://gw.p2-41.example/v1", "")
	p2Admin(t, m.user, org, storage.OrgAIPatch{Provider: sp("openai"), APIKey: sp(p2KeyA), BaseURL: sp("https://gw.p2-41.example/v1")})
	m.mustConfirm(1)
	m.usr = nlptest.UserDestinations(t, "", "") // the operator stops approving it
	m.rec = nlptest.Ambient(t, byokApproved)
	w := m.parse()
	var res map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	msg, _ := res["error"].(string)
	if w.Code != http.StatusOK || !strings.Contains(msg, "organisation's AI destination is not approved") || strings.Contains(msg, "Settings") {
		t.Fatalf("parse = %d %s", w.Code, w.Body.String())
	}
	ev := "evt-p2-41-" + m.user.String()[:6]
	bw := m.brief(http.MethodPost, ev)
	if bw.Code != http.StatusOK || !strings.Contains(bw.Body.String(), "administrator of your organisation") || strings.Contains(bw.Body.String(), "Settings") {
		t.Fatalf("brief = %d %s", bw.Code, bw.Body.String())
	}
	pw := m.probe(`{}`)
	if pw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("probe = %d %s", pw.Code, pw.Body.String())
	}
	var refusal map[string]any
	_ = json.Unmarshal(pw.Body.Bytes(), &refusal)
	if refusal["configuration"] != "organization" || refusal["property"] != "llmBaseUrl" || strings.Contains(refusal["error"].(string), "Settings") {
		t.Fatalf("422 = %s", pw.Body.String())
	}
	m.noRequests("refused organisation destination")
	if st := m.aiStatus(); st["configured"] != true || st["configuration"] != "organization" {
		t.Fatalf("settings read = %v, want still configured", st)
	}
}

// hookSource applies an administrator's change at one storage access of a
// request (P2-AC-37): at 0 before the first, 1 between the two, 2 after
// the last.
type hookSource struct {
	inner nlp.AISource
	at    int
	apply func()
}

func (s *hookSource) Settings(ctx context.Context, u uuid.UUID) (*storage.Settings, error) {
	if s.at == 0 {
		s.apply()
	}
	return s.inner.Settings(ctx, u)
}

func (s *hookSource) OrgAI(ctx context.Context, u uuid.UUID) (storage.OrgAIState, error) {
	if s.at == 1 {
		s.apply()
	}
	st, err := s.inner.OrgAI(ctx, u)
	if s.at == 2 {
		s.apply()
	}
	return st, err
}

// P2-AC-37: a request never sends a value of another version than the one
// it decided on, and never mixes two versions.
func TestPAC73P2_OneConsistentVersionPerRequest(t *testing.T) {
	for _, op := range []string{"parse", "probe", "brief"} {
		for at := 0; at <= 2; at++ {
			for _, modelOnly := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/at%d/modelOnly=%v", op, at, modelOnly), func(t *testing.T) {
					m := newBYOK(t, fmt.Sprintf("p2-ac37-%s-%d-%v", op, at, modelOnly))
					org := p2Org(t, m, fmt.Sprintf("p2-ac37-%s-%d-%v.example", op, at, modelOnly))
					p2Admin(t, m.user, org, storage.OrgAIPatch{Provider: sp("openai"), APIKey: sp(p2KeyA), Model: sp("model-v")})
					m.mustConfirm(1)
					m.reset()
					db := openTestDB(t)
					src := &hookSource{inner: nlp.DBSource{DB: db}, at: at, apply: func() {
						if modelOnly {
							p2Admin(t, m.user, org, storage.OrgAIPatch{Model: sp("model-new")})
						} else {
							p2Admin(t, m.user, org, storage.OrgAIPatch{Provider: sp("anthropic"), APIKey: sp(p2KeyW)})
						}
					}}
					var w *httptest.ResponseRecorder
					switch op {
					case "parse":
						req := withUser(httptest.NewRequest(http.MethodPost, "/api/nlp/parse", strings.NewReader(`{"text":"meet bob"}`)), m.user)
						w = httptest.NewRecorder()
						(&nlpHandlers{svc: &nlp.NLPService{DB: db, Source: src}, smart: &mockScheduler{}, db: db}).parse(w, req)
					case "probe":
						req := withUser(httptest.NewRequest(http.MethodPost, "/api/llm/test", strings.NewReader(`{}`)), m.user)
						w = httptest.NewRecorder()
						(&llmHandlers{db: db, source: src}).testLLM(w, req)
					default:
						w = briefWith(t, m, &meetingBriefHandlers{db: db, source: src}, "evt-p2-37")
					}
					sent := m.sentKeys()
					if !modelOnly && at < 2 {
						// The change was stored before the configuration was
						// read: row 5, nothing sent.
						if len(sent) != 0 || !strings.Contains(w.Body.String(), "organization_unconfirmed") {
							t.Fatalf("change before the read: %d %s, sent %v", w.Code, w.Body.String(), sent)
						}
						return
					}
					if len(sent) == 0 {
						t.Fatalf("nothing sent: %d %s", w.Code, w.Body.String())
					}
					for _, k := range sent {
						if k != p2KeyA {
							t.Errorf("sent %q, want only version 1's key", k)
						}
					}
					for _, r := range m.requests() {
						if r.Host != "api.openai.com" {
							t.Errorf("sent to %s, want version 1's provider only", r.Host)
						}
					}
				})
			}
		}
	}
}

func briefWith(t *testing.T, b *byok, h *meetingBriefHandlers, eventID string) *httptest.ResponseRecorder {
	t.Helper()
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/meetings/"+eventID+"/brief/refresh", nil), b.user)
	req = withEventID(req, eventID)
	w := httptest.NewRecorder()
	h.refreshBrief(w, req)
	return w
}

func withEventID(r *http.Request, eventID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("event_id", eventID)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// captured is what the server log and the error reporter received.
type captured struct {
	mu     sync.Mutex
	logs   bytes.Buffer
	events []string
}

func captureOperatorSurfaces(t *testing.T) *captured {
	t.Helper()
	c := &captured{}
	log.SetOutput(&lockedWriter{c: c})
	prev := reportServerError
	reportServerError = func(_ *http.Request, err error) {
		c.mu.Lock()
		c.events = append(c.events, err.Error())
		c.mu.Unlock()
	}
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		reportServerError = prev
	})
	return c
}

type lockedWriter struct{ c *captured }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	return w.c.logs.Write(p)
}

func (c *captured) all() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.logs.String() + "\n" + strings.Join(c.events, "\n")
}

// echo is a gateway double: an error body with the marker and the key it
// was sent, in the given encoding.
type echoer struct{ encode func(string) string }

func (e echoer) answer(r *http.Request) *http.Response {
	key := r.Header.Get("X-Api-Key")
	if key == "" {
		key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return nlptest.Response(r, http.StatusUnauthorized, `{"error":"`+p2Mark+` bad key `+e.encode(key)+`"}`, nil)
}

// P2-AC-39: no provider text reaches a member from the organisation's
// configuration; operator surfaces carry no exact key; Part 1's own-key
// diagnosis is kept, redacted.
func TestPAC73P2_NoKeyLeavesThroughProviderErrorText(t *testing.T) {
	verbatim := func(k string) string { return k }
	jsonEscaped := func(k string) string { return strings.ReplaceAll(k, "/", `\/`) }
	percent := func(k string) string { return url.QueryEscape(k) }
	for name, enc := range map[string]func(string) string{"verbatim": verbatim, "json-escaped": jsonEscaped, "percent-encoded": percent} {
		t.Run(name, func(t *testing.T) {
			cap := captureOperatorSurfaces(t)
			m := newBYOK(t, "p2-ac39-"+name)
			org := p2Org(t, m, "p2-ac39-"+name+".example")
			p2Admin(t, m.user, org, p2OpenAI(p2KeyA))
			m.mustConfirm(1)
			m.sql(`llm_api_key_anthropic = $2`, p2KeyU) // the caller's own key, stored
			m.usr.Answer = echoer{enc}.answer
			ev := "evt-p2-39-" + m.user.String()[:6]
			answers := map[string]*httptest.ResponseRecorder{
				"refresh": m.brief(http.MethodPost, ev),
				"parse":   m.parse(),
				"probe":   m.probe(`{}`),
			}
			answers["brief read"] = m.brief(http.MethodGet, ev)
			var b struct {
				Status    string `json:"status"`
				BriefText string `json:"brief_text"`
			}
			_ = json.Unmarshal(answers["brief read"].Body.Bytes(), &b)
			if b.Status != "failed" || b.BriefText != orgProviderFailureText {
				t.Fatalf("brief read = %s, want the fixed sentence", answers["brief read"].Body.String())
			}
			if answers["parse"].Code != http.StatusInternalServerError || answers["probe"].Code != http.StatusBadGateway {
				t.Fatalf("parse %d, probe %d", answers["parse"].Code, answers["probe"].Code)
			}
			for what, w := range answers {
				body := w.Body.String()
				for _, bad := range []string{p2Mark, p2KeyA, jsonEscaped(p2KeyA), percent(p2KeyA), p2KeyU} {
					if strings.Contains(body, bad) {
						t.Errorf("%s answer carries %q: %s", what, bad, body)
					}
				}
			}
			if name == "verbatim" {
				ops := cap.all()
				if !strings.Contains(ops, p2Mark) {
					t.Fatal("the provider text did not reach the log or the reporter at all")
				}
				for _, k := range []string{p2KeyA, p2KeyU} {
					if strings.Contains(ops, k) {
						t.Errorf("an operator surface carries %q", k)
					}
				}
				var n int
				_ = openTestDB(t).QueryRow(`SELECT count(*) FROM sso_events WHERE details::text LIKE '%' || $1 || '%' OR details::text LIKE '%' || $2 || '%'`, p2Mark, p2KeyA).Scan(&n)
				if n != 0 {
					t.Errorf("%d operator records carry provider text or the key", n)
				}
			}
		})
	}

	// (iv) The administrator rotates the key while the request is in flight:
	// the old key (sent) is redacted, the new one appears nowhere.
	t.Run("rotation in flight", func(t *testing.T) {
		cap := captureOperatorSurfaces(t)
		m := newBYOK(t, "p2-ac39-rot")
		org := p2Org(t, m, "p2-ac39-rot.example")
		p2Admin(t, m.user, org, p2OpenAI(p2KeyA))
		m.mustConfirm(1)
		inner := echoer{func(k string) string { return k }}.answer
		m.usr.Answer = func(r *http.Request) *http.Response {
			p2Admin(t, m.user, org, storage.OrgAIPatch{APIKey: sp(p2KeyA2)})
			return inner(r)
		}
		_ = m.parse()
		ops := cap.all()
		if !strings.Contains(ops, p2Mark) || strings.Contains(ops, p2KeyA) || strings.Contains(ops, p2KeyA2) {
			t.Fatalf("operator surfaces after a rotation in flight: %s", ops)
		}
	})

	// (v), (vi) A row-2 member's own call fails: logs and reports carry no
	// K_U; the member's failed brief keeps the provider text, redacted.
	t.Run("row 2", func(t *testing.T) {
		cap := captureOperatorSurfaces(t)
		m := newBYOK(t, "p2-ac39-own")
		m.mustPatch(`{"llmProvider":"anthropic","llmApiKey":"` + p2KeyU + `"}`)
		m.usr.Answer = echoer{func(k string) string { return k }}.answer
		ev := "evt-p2-39-own"
		_ = m.brief(http.MethodPost, ev)
		_ = m.parse()
		read := m.brief(http.MethodGet, ev).Body.String()
		if !strings.Contains(read, p2Mark) || strings.Contains(read, p2KeyU) || !strings.Contains(read, "[redacted]") {
			t.Fatalf("own failed brief = %s, want the provider text redacted of the own key", read)
		}
		if ops := cap.all(); strings.Contains(ops, p2KeyU) || !strings.Contains(ops, p2Mark) {
			t.Fatalf("operator surfaces = %s", ops)
		}
	})
}
