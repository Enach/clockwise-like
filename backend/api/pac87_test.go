package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-87 (spec docs/specs/PAC-87.md; contract contracts/features/PAC-87.md):
// the organisation's AI re-asks only on a provider change, and members on
// their own (or a removed) provider in an organisation that forbids personal
// keys are moved onto its AI after one confirmation (row 9), without their
// stored choice or keys being rewritten. Same byok harness as Part 2: every
// request to any provider is recorded.

const (
	p87KeyU2  = "sk-p87-KU2-own-new"
	p87Member = "https://gw.member-p87.example/v1"
	p87OrgGW  = "https://gw.org-p87.example/v1"
)

// row9Status is the AIStatus of rows 4 and 9 on the organisation's OpenAI.
const row9Status = `{"configuration":"organization","configured":true,"inOrganization":true,"provider":"openai"}`

func (b *byok) storedProvider() string {
	b.t.Helper()
	var p string
	if err := openTestDB(b.t).QueryRow(`SELECT llm_provider FROM settings WHERE user_id = $1`, b.user).Scan(&p); err != nil {
		b.t.Fatal(err)
	}
	return p
}

// PAC87-AC-8, -9, -13, -14, -12 (row 9 part): row 10 -> one confirmation,
// no settings write -> row 9, which sends only the organisation's values;
// allowing personal keys restores the own choice; forbidding again without
// a provider change is row 9 without asking; a provider change asks again.
func TestPAC87_Row9MovesAfterOneConfirmation(t *testing.T) {
	admin := newBYOK(t, "p87-ac8-admin")
	org := p2Org(t, admin, "p87-ac8.example")
	p2Admin(t, admin.user, org, storage.OrgAIPatch{Provider: sp("openai"), APIKey: sp(p2KeyA), Model: sp("gpt-org")})
	m2 := p2Member(t, "p87-ac8-m2", org)
	m2.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
	m2.sql(`llm_model = 'm2-model', llm_base_url = $2`, p87Member)
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})

	// Row 10: the user only; the sentence names A's provider, says access,
	// says the own choice and key are kept, and to confirm; nothing sent.
	m2.allThree("organization_unconfirmed", "", "user")
	var res struct {
		NC notConfigured `json:"not_configured"`
	}
	_ = json.Unmarshal(m2.parse().Body.Bytes(), &res)
	for _, want := range []string{"through OpenAI", "access", "Your own choice (OpenAI) and any key you saved are kept", "used again if your organisation allows personal keys", "Confirm in Paceday"} {
		if !strings.Contains(res.NC.Message, want) {
			t.Errorf("row 10 sentence lacks %q: %s", want, res.NC.Message)
		}
	}
	m2.noRequests("row 10")

	// Confirming (no settings write): the 200 is row 9's status.
	w := m2.confirm(1)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != row9Status {
		t.Fatalf("confirm = %d %s", w.Code, w.Body.String())
	}
	if m2.storedProvider() != "openai" {
		t.Fatal("confirming rewrote the stored choice")
	}
	// The next parse carries A's key to A's host: never K2, never g2.
	m2.parseSends("row 9 parse", p2KeyA, "api.openai.com")
	_, doc := m2.get()
	if doc["llmProvider"] != "openai" || doc["llmModel"] != "m2-model" || doc["llmBaseUrl"] != p87Member {
		t.Fatalf("settings = %v, want the own choice kept", doc)
	}
	if ks, _ := doc["llmApiKeysSet"].(map[string]any); ks["openai"] != true {
		t.Fatalf("llmApiKeysSet = %v", doc["llmApiKeysSet"])
	}
	if st := m2.aiStatus(); st["configured"] != true || st["configuration"] != "organization" || st["provider"] != "openai" {
		t.Fatalf("aiStatus = %v", st)
	}
	// Brief refresh in row 9 is sent on A too.
	m2.reset()
	if bw := m2.brief(http.MethodPost, "evt-p87-ac8-"+m2.user.String()[:6]); bw.Code != http.StatusOK {
		t.Fatalf("row 9 brief refresh = %d %s", bw.Code, bw.Body.String())
	}
	m2.sentOnly("row 9 brief refresh", p2KeyA, "api.openai.com")

	// PAC87-AC-14: the probe in row 9.
	m2.reset()
	for _, body := range []string{`{"llmProvider":"openai"}`, `{"llmApiKey":"` + p2KeyU + `"}`} {
		conflict409(t, "row 9 probe "+body, m2.probe(body), "personal_keys_forbidden", "", "organization_admin")
	}
	wantError(t, "row 9 probe with a model", m2.probe(`{"llmModel":"m2-model"}`), http.StatusBadRequest, nlp.MsgProbeOrganizationNoModel)
	m2.noRequests("refused row 9 probes")
	if pw := m2.probe(`{}`); pw.Code != http.StatusOK || !strings.Contains(pw.Body.String(), `"configuration":"organization"`) {
		t.Fatalf("row 9 probe {} = %d %s", pw.Code, pw.Body.String())
	}
	m2.sentOnly("row 9 probe {}", p2KeyA, "api.openai.com")
	m2.reset()

	// PAC87-AC-13 (P87-D3): storing OpenAI unchanged keeps row 9.
	_, doc = m2.get()
	delete(doc, "aiStatus")
	raw, _ := json.Marshal(doc)
	m2.mustPatch(string(raw))
	m2.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p87KeyU2 + `"}`)
	m2.mustPatch(`{"llmModel":"m2-other"}`)
	m2.parseSends("row 9 after unchanged OpenAI writes", p2KeyA, "api.openai.com")

	// PAC87-AC-9: allowed again -> the own key with no action (gateway
	// cleared: the operator approves none for this member).
	m2.sql(`llm_base_url = ''`)
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(true)})
	m2.parseSends("own key after personal keys are allowed again", p87KeyU2, "api.openai.com")
	// Forbidden again, provider unchanged: row 9 without asking (P87-D2).
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	m2.parseSends("row 9 after forbidding again", p2KeyA, "api.openai.com")
	// A provider change re-asks moved members too.
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(true)})
	q := p2Admin(t, admin.user, org, storage.OrgAIPatch{Provider: sp("anthropic"), APIKey: sp(p2KeyW)})
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	m2.allThree("organization_unconfirmed", "", "user")
	m2.mustConfirm(q.Version)
	m2.parseSends("row 9 on the new provider", p2KeyW, "api.anthropic.com")

	// PAC87-AC-13: a change of the stored choice ends it -> row 10.
	m2.mustPatch(`{"llmProvider":"vertex"}`)
	m2.allThree("organization_unconfirmed", "", "user")
	// PAC87-AC-12: row 9 chooses off -> row 1; OpenAI again -> row 10.
	m2.mustConfirm(q.Version)
	m2.mustPatch(`{"llmProvider":""}`)
	m2.allThree("off", "", "user")
	m2.mustPatch(`{"llmProvider":"openai"}`)
	m2.allThree("organization_unconfirmed", "", "user")
	// M4 stored "off" holding a confirmation: row 1, nothing sent.
	m4 := p2Member(t, "p87-ac12-m4", org)
	m4.mustConfirm(q.Version)
	m4.sql(`llm_provider = ''`)
	m4.allThree("off", "", "user")
}

// PAC87-AC-10: a member stored on a removed provider (Ollama) is row 10
// naming Ollama as the kept choice, row 9 after confirming, and is still
// stored on Ollama.
func TestPAC87_RemovedProviderMoves(t *testing.T) {
	admin := newBYOK(t, "p87-ac10-admin")
	org := p2Org(t, admin, "p87-ac10.example")
	p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	m3 := p2Member(t, "p87-ac10-m3", org)
	m3.sql(`llm_provider = 'ollama'`)
	m3.allThree("organization_unconfirmed", "", "user")
	if b := m3.parse().Body.String(); !strings.Contains(b, "Your own choice (Ollama)") {
		t.Fatalf("row 10 for Ollama = %s", b)
	}
	m3.mustConfirm(1)
	m3.parseSends("Ollama member in row 9", p2KeyA, "api.openai.com")
	if p := m3.storedProvider(); p != "ollama" {
		t.Fatalf("stored %q, want ollama", p)
	}
	// A whole settings document sent back ("unsupported") keeps it.
	_, doc := m3.get()
	delete(doc, "aiStatus")
	raw, _ := json.Marshal(doc)
	m3.mustPatch(string(raw))
	if p := m3.storedProvider(); p != "ollama" {
		t.Fatalf("stored %q after a whole-document write", p)
	}
	m3.parseSends("Ollama member after a whole-document write", p2KeyA, "api.openai.com")
}

// confirmHookStore applies a change before or after the confirmation's
// storage access (PAC87-AC-5's per-access runs).
type confirmHookStore struct {
	orgAIStore
	before, after func()
}

func (s confirmHookStore) confirm(ctx context.Context, userID, orgID uuid.UUID, version int64, now time.Time) (storage.OrgAIConfirmStatus, storage.OrgAIConfig, error) {
	if s.before != nil {
		s.before()
	}
	st, c, err := s.orgAIStore.confirm(ctx, userID, orgID, version, now)
	if s.after != nil {
		s.after()
	}
	return st, c, err
}

// PAC87-AC-5: shown P, then a key or destination change, then confirmed:
// 200 and the next parse uses the current key. A provider change applied at
// each storage access of the confirmation never leaves a confirmation
// recorded that covers the new provider.
func TestPAC87_ConfirmationAgainstAdministratorChanges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		patch    storage.OrgAIPatch
		before   bool
		wantCode int
		sendKey  string
		sendHost string
	}{
		{"rotation before the record", storage.OrgAIPatch{APIKey: sp(p2KeyA2)}, true, http.StatusOK, p2KeyA2, "api.openai.com"},
		{"rotation after the record", storage.OrgAIPatch{APIKey: sp(p2KeyA2)}, false, http.StatusOK, p2KeyA2, "api.openai.com"},
		{"provider change before the record", storage.OrgAIPatch{Provider: sp("anthropic"), APIKey: sp(p2KeyW)}, true, http.StatusConflict, "", ""},
		{"provider change after the record", storage.OrgAIPatch{Provider: sp("anthropic"), APIKey: sp(p2KeyW)}, false, http.StatusOK, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			label := "p87-ac5-" + strings.ReplaceAll(tc.name, " ", "-")
			admin := newBYOK(t, label+"-admin")
			org := p2Org(t, admin, label+".example")
			p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
			m := p2Member(t, label+"-m", org)
			apply := func() { p2Admin(t, admin.user, org, tc.patch) }
			oa := orgAIFor(t)
			store := confirmHookStore{orgAIStore: oa.store}
			if tc.before {
				store.before = apply
			} else {
				store.after = apply
			}
			oa.store = store
			req := withUser(httptest.NewRequest(http.MethodPost, "/api/organization/ai/consent", strings.NewReader(`{"configurationVersion":1}`)), m.user)
			w := httptest.NewRecorder()
			oa.confirm(w, req)
			if w.Code != tc.wantCode {
				t.Fatalf("confirm = %d %s, want %d", w.Code, w.Body.String(), tc.wantCode)
			}
			if w.Code == http.StatusConflict && (!strings.Contains(w.Body.String(), `"reason":"configuration_changed"`) || !strings.Contains(w.Body.String(), `"provider":"anthropic"`)) {
				t.Fatalf("409 = %s, want configuration_changed carrying the new provider", w.Body.String())
			}
			if tc.sendKey != "" {
				m.parseSends(tc.name, tc.sendKey, tc.sendHost)
				return
			}
			// The new provider is never covered: row 5, nothing sent.
			m.allThree("organization_unconfirmed", "", "user")
		})
	}
	// A version above the current one, and an incomplete configuration.
	admin := newBYOK(t, "p87-ac5-edges-admin")
	org := p2Org(t, admin, "p87-ac5-edges.example")
	m := p2Member(t, "p87-ac5-edges-m", org)
	if w := m.confirm(1); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"reason":"organization_not_configured"`) {
		t.Fatalf("incomplete = %d %s", w.Code, w.Body.String())
	}
	p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
	if w := m.confirm(2); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"reason":"configuration_changed"`) {
		t.Fatalf("future version = %d %s", w.Code, w.Body.String())
	}
}

// PAC87-AC-6: a key rotation with a destination change applied at each
// storage access of a request: all of S1 or all of S2, never a mix.
func TestPAC87_OneConsistentReadAcrossARotation(t *testing.T) {
	for _, op := range []string{"parse", "probe", "brief"} {
		for at := 0; at <= 2; at++ {
			t.Run(fmt.Sprintf("%s/at%d", op, at), func(t *testing.T) {
				m := newBYOK(t, fmt.Sprintf("p87-ac6-%s-%d", op, at))
				org := p2Org(t, m, fmt.Sprintf("p87-ac6-%s-%d.example", op, at))
				p2Admin(t, m.user, org, storage.OrgAIPatch{Provider: sp("openai"), APIKey: sp(p2KeyA), Model: sp("model-v")})
				m.mustConfirm(1)
				m.reset()
				m.usr = nlptest.UserDestinations(t, p87OrgGW, "")
				db := openTestDB(t)
				src := &hookSource{inner: nlp.DBSource{DB: db}, at: at, apply: func() {
					p2Admin(t, m.user, org, storage.OrgAIPatch{APIKey: sp(p2KeyW), BaseURL: sp(p87OrgGW)})
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
					w = briefWith(t, m, &meetingBriefHandlers{db: db, source: src}, "evt-p87-6")
				}
				reqs := m.requests()
				if len(reqs) == 0 {
					t.Fatalf("nothing sent (the confirmation survives a rotation): %d %s", w.Code, w.Body.String())
				}
				wantKey, wantHost := p2KeyA, "api.openai.com" // S1: read before the change
				if at < 2 {
					wantKey, wantHost = p2KeyW, "gw.org-p87.example" // S2: read after it
				}
				for _, r := range reqs {
					if r.Host != wantHost {
						t.Errorf("sent to %s, want %s", r.Host, wantHost)
					}
				}
				for _, k := range m.sentKeys() {
					if k != wantKey {
						t.Errorf("sent %q with %s's host: a mix of two states", k, wantHost)
					}
				}
			})
		}
	}
}

// PAC87-AC-11: row 9's provider error text never reaches a member, and no
// operator surface carries the organisation's key or the member's own.
func TestPAC87_Row9ProviderErrorIsRedacted(t *testing.T) {
	cap := captureOperatorSurfaces(t)
	admin := newBYOK(t, "p87-ac11-admin")
	org := p2Org(t, admin, "p87-ac11.example")
	p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	m2 := p2Member(t, "p87-ac11-m2", org)
	m2.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
	m2.mustConfirm(1)
	m2.usr.Answer = echoer{func(k string) string { return k }}.answer
	ev := "evt-p87-11-" + m2.user.String()[:6]
	answers := map[string]*httptest.ResponseRecorder{
		"refresh": m2.brief(http.MethodPost, ev),
		"parse":   m2.parse(),
		"probe":   m2.probe(`{}`),
	}
	answers["brief read"] = m2.brief(http.MethodGet, ev)
	if len(m2.requests()) == 0 {
		t.Fatal("row 9 sent nothing")
	}
	for what, w := range answers {
		for _, bad := range []string{p2Mark, p2KeyA, p2KeyU} {
			if strings.Contains(w.Body.String(), bad) {
				t.Errorf("%s answer carries %q: %s", what, bad, w.Body.String())
			}
		}
	}
	ops := cap.all()
	if !strings.Contains(ops, p2Mark) {
		t.Fatal("the provider text reached no operator surface")
	}
	for _, k := range []string{p2KeyA, p2KeyU} {
		if strings.Contains(ops, k) {
			t.Errorf("an operator surface carries %q", k)
		}
	}
	var n int
	_ = openTestDB(t).QueryRow(`SELECT count(*) FROM sso_events WHERE details::text LIKE '%' || $1 || '%' OR details::text LIKE '%' || $2 || '%'`, p2KeyA, p2KeyU).Scan(&n)
	if n != 0 {
		t.Errorf("%d operator records carry a key", n)
	}
}

// countingSource counts the storage reads of a decision; every secret a
// decision can open is opened by one of them (the settings row's and the
// organisation's), so equal reads over equal stored values are equal
// decryptions.
type countingSource struct {
	inner          nlp.AISource
	settings, orgs atomic.Int32
}

func (s *countingSource) Settings(ctx context.Context, u uuid.UUID) (*storage.Settings, error) {
	s.settings.Add(1)
	return s.inner.Settings(ctx, u)
}

func (s *countingSource) OrgAI(ctx context.Context, u uuid.UUID) (storage.OrgAIState, error) {
	s.orgs.Add(1)
	return s.inner.OrgAI(ctx, u)
}

func parseWith(t *testing.T, b *byok, src nlp.AISource) *httptest.ResponseRecorder {
	t.Helper()
	db := openTestDB(t)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/nlp/parse", strings.NewReader(`{"text":"meet bob"}`)), b.user)
	w := httptest.NewRecorder()
	(&nlpHandlers{svc: &nlp.NLPService{DB: db, Source: src}, smart: &mockScheduler{}, db: db}).parse(w, req)
	return w
}

// PAC87-AC-22 (PAC-84): a member key that cannot be decrypted never stops
// row 9 and is not mentioned; the organisation's key failing gives row 4's
// answer; deciding row 9 reads (so decrypts) exactly what row 4 does.
func TestPAC87_Row9AndUndecryptableKeys(t *testing.T) {
	admin := newBYOK(t, "p87-ac22-admin")
	org := p2Org(t, admin, "p87-ac22.example")
	p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
	p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	m2 := p2Member(t, "p87-ac22-m2", org)
	m2.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
	m2.mustConfirm(1)
	m2.sql(`llm_api_key_openai = $2`, undecryptable)
	f := p2Member(t, "p87-ac22-f", org) // row 4 with the same stored keys
	f.mustConfirm(1)
	f.sql(`llm_api_key_openai = $2`, undecryptable)

	db := openTestDB(t)
	counts := map[string][2]int32{}
	for name, b := range map[string]*byok{"row 9": m2, "row 4": f} {
		b.reset()
		src := &countingSource{inner: nlp.DBSource{DB: db}}
		w := parseWith(t, b, src)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "not_configured") || strings.Contains(w.Body.String(), "key") {
			t.Fatalf("%s parse = %d %s", name, w.Code, w.Body.String())
		}
		b.sentOnly(name, p2KeyA, "api.openai.com")
		counts[name] = [2]int32{src.settings.Load(), src.orgs.Load()}
	}
	if counts["row 9"] != counts["row 4"] {
		t.Fatalf("reads while deciding: row 9 %v, row 4 %v", counts["row 9"], counts["row 4"])
	}
	// The organisation's key cannot be decrypted: row 9 answers as row 4.
	if _, err := db.Exec(`UPDATE org_ai_configurations SET api_key = $2 WHERE org_id = $1`, org, undecryptable); err != nil {
		t.Fatal(err)
	}
	m2.reset()
	f.reset()
	w9, w4 := m2.parse(), f.parse()
	if w9.Code != w4.Code || w9.Body.String() != w4.Body.String() {
		t.Fatalf("org key undecryptable: row 9 %d %s, row 4 %d %s", w9.Code, w9.Body.String(), w4.Code, w4.Body.String())
	}
	m2.noRequests("row 9 with the organisation's key undecryptable")
	f.noRequests("row 4 with the organisation's key undecryptable")
}

// waitLockOrDone waits until done is closed or a backend waits on a lock.
func waitLockOrDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	db := openTestDB(t)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return
		default:
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the concurrent confirmation neither finished nor waited on a lock")
}

// PAC87-AC-23 through PATCH /api/settings: M's confirmation runs
// concurrently at a storage access of the write OpenAI -> Anthropic. Before
// the write holds the caller's users row, it lands first and the change ends
// it (row 10 after a forbid); from then on it waits and lands after the
// change (an explicit confirmation after it: row 9).
func TestPAC87_SettingsWriteAndConfirmationAreOrdered(t *testing.T) {
	for _, tc := range []struct {
		step storage.SettingsWriteStep
		want string
	}{{storage.SettingsWriteLock, "row 10"}, {storage.SettingsWriteReread, "row 9"}, {storage.SettingsWriteSave, "row 9"}, {storage.SettingsWriteEndConfirmation, "row 9"}} {
		t.Run(fmt.Sprintf("step%d", tc.step), func(t *testing.T) {
			label := fmt.Sprintf("p87-ac23-%d", tc.step)
			admin := newBYOK(t, label+"-admin")
			org := p2Org(t, admin, label+".example")
			p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
			m := p2Member(t, label+"-m", org)
			m.mustConfirm(1)
			m.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
			db := openTestDB(t)
			var once sync.Once
			done := make(chan struct{})
			var confirmCode int
			h := &settingsHandlers{db: db, beforeWriteStep: func(at storage.SettingsWriteStep) {
				if at != tc.step {
					return
				}
				once.Do(func() {
					go func() {
						defer close(done)
						confirmCode = m.confirm(1).Code
					}()
					waitLockOrDone(t, done)
				})
			}}
			req := withUser(httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(`{"llmProvider":"anthropic"}`)), m.user)
			w := httptest.NewRecorder()
			h.patchSettings(w, req)
			<-done
			if w.Code != http.StatusOK || confirmCode != http.StatusOK {
				t.Fatalf("write %d %s, confirm %d", w.Code, w.Body.String(), confirmCode)
			}
			if p := m.storedProvider(); p != "anthropic" {
				t.Fatalf("stored %q", p)
			}
			p2Admin(t, admin.user, org, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
			if tc.want == "row 10" {
				m.allThree("organization_unconfirmed", "", "user")
				return
			}
			m.parseSends("confirmed after the change", p2KeyA, "api.openai.com")
		})
	}
}

// Factory decision (2026-11-12): leaving the organisation ends the
// member's confirmation; rejoining asks again before anything is sent.
func TestPAC87_RejoiningAsksAgain(t *testing.T) {
	admin := newBYOK(t, "p87-leave-admin")
	org := p2Org(t, admin, "p87-leave.example")
	other := p2Org(t, newBYOK(t, "p87-leave-admin2"), "p87-leave-other.example")
	p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
	m := p2Member(t, "p87-leave-m", org)
	m.mustConfirm(1)
	m.parseSends("before leaving", p2KeyA, "api.openai.com")
	db := openTestDB(t)
	if err := storage.SetUserOrg(db, m.user, other); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetUserOrg(db, m.user, org); err != nil {
		t.Fatal(err)
	}
	m.allThree("organization_unconfirmed", "", "user")
}

// PAC-71 x PAC-87 (review finding 1): a session revoked before the request
// gets requireAuth's 401 from PATCH /api/settings and POST
// /api/organization/ai/consent through the real router; one revoked while
// the request runs (just before the settings write's or the confirmation's
// transaction) saves nothing, ends nothing and records nothing, and the
// guard answers the 401.
func TestPAC87_RevokedSessionNeitherSavesNorConfirms(t *testing.T) {
	admin := newBYOK(t, "p87-stale-admin")
	org := p2Org(t, admin, "p87-stale.example")
	p2Admin(t, admin.user, org, p2OpenAI(p2KeyA))
	m := p2Member(t, "p87-stale-m", org)
	m.mustConfirm(1)
	db := openTestDB(t)
	bump := func() {
		t.Helper()
		if _, err := db.Exec(`UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, m.user); err != nil {
			t.Fatal(err)
		}
	}
	gen := func() int64 {
		var g int64
		_ = db.QueryRow(`SELECT session_generation FROM users WHERE id = $1`, m.user).Scan(&g)
		return g
	}
	stored := func() string {
		var p string
		_ = db.QueryRow(`SELECT COALESCE((SELECT llm_provider FROM settings WHERE user_id = $1), 'organization')`, m.user).Scan(&p)
		return p
	}
	confirmations := func() int {
		var n int
		_ = db.QueryRow(`SELECT count(*) FROM org_ai_confirmations WHERE user_id = $1`, m.user).Scan(&n)
		return n
	}

	// Revoked before the request: the router answers 401.
	old := gen()
	bump()
	r := pac71Router(t, &oauth2.Config{})
	assert401(t, doAs(t, r, http.MethodPatch, "/api/settings", `{"llmProvider":"anthropic"}`, sessionCookie(t, m.user, old)))
	assert401(t, doAs(t, r, http.MethodPost, "/api/organization/ai/consent", `{"configurationVersion":1}`, sessionCookie(t, m.user, old)))
	if stored() != "organization" || confirmations() != 1 {
		t.Fatalf("a revoked session changed something: %q, %d confirmations", stored(), confirmations())
	}

	// Revoked while the settings write runs, just before its transaction's
	// re-check.
	g := &storage.SessionGuard{UserID: m.user, Gen: gen()}
	h := &settingsHandlers{db: db, beforeWriteStep: func(at storage.SettingsWriteStep) {
		if at == storage.SettingsWriteRecheck {
			bump()
		}
	}}
	req := withUser(httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(`{"llmProvider":"anthropic"}`)), m.user)
	req = req.WithContext(storage.WithSessionGuard(req.Context(), g))
	gw := &guardedResponseWriter{ResponseWriter: httptest.NewRecorder(), r: req, guard: g}
	h.patchSettings(gw, req)
	gw.finish()
	if !g.Stale() {
		t.Fatal("the settings write did not re-check the session")
	}
	assert401(t, gw.ResponseWriter.(*httptest.ResponseRecorder))
	if stored() != "organization" || confirmations() != 1 {
		t.Fatalf("a session revoked mid-request saved %q or ended the confirmation (%d)", stored(), confirmations())
	}

	// Revoked just before the confirmation's transaction.
	if _, err := db.Exec(`DELETE FROM org_ai_confirmations WHERE user_id = $1`, m.user); err != nil {
		t.Fatal(err)
	}
	g = &storage.SessionGuard{UserID: m.user, Gen: gen()}
	oa := orgAIFor(t)
	oa.store = confirmHookStore{orgAIStore: oa.store, before: bump}
	req = withUser(httptest.NewRequest(http.MethodPost, "/api/organization/ai/consent", strings.NewReader(`{"configurationVersion":1}`)), m.user)
	req = req.WithContext(storage.WithSessionGuard(req.Context(), g))
	gw = &guardedResponseWriter{ResponseWriter: httptest.NewRecorder(), r: req, guard: g}
	oa.confirm(gw, req)
	gw.finish()
	if !g.Stale() {
		t.Fatal("the confirmation did not re-check the session")
	}
	assert401(t, gw.ResponseWriter.(*httptest.ResponseRecorder))
	if confirmations() != 0 {
		t.Fatal("a session revoked mid-request recorded a confirmation")
	}
}
