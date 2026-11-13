package nlp_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
)

// PAC-66-B: a user-set OpenAI or Anthropic base URL goes only to a
// destination approved for the provider in use (with an approved gateway
// path prefix since NEW-llm-bring-your-own, owner decision O-10). Every test
// runs with a sentinel user key and a recording transport
// (nlptest.UserDestinations), so the pre-PAC-66-B code would have sent each
// refused request. The Ollama tests are gone with Ollama (owner decision
// O-4); they are replaced by api/byok_test.go (AC-6, AC-10) and the
// config tests for the retired variable (AC-19).

const (
	gatewayHost   = "gateway.example"
	ollamaOrigin  = "http://ollama.internal:11434"
	internalAddr  = "http://169.254.169.254"
	sentinelUser  = nlptest.UserKey
	openAIPath    = "/v1/chat/completions"
	anthropicPath = "/v1/messages"
)

// keyed is a stored row for a user-key provider with the sentinel key bound
// to it.
func keyed(provider, base string) storage.Settings {
	s := storage.Settings{LLMProvider: provider, LLMBaseURL: base}
	s.LLMAPIKeys.Set(provider, sentinelUser)
	return s
}

// noKeyIn asserts the sentinel key appears in no recorded request.
func noKeyIn(t *testing.T, reqs []nlptest.Request) {
	t.Helper()
	for _, r := range reqs {
		for k, v := range r.Header {
			if strings.Contains(strings.Join(v, " "), sentinelUser) {
				t.Errorf("request to %s carries the user key in %s", r.Host, k)
			}
		}
	}
}

// sentOnceTo asserts Complete succeeds with exactly one POST to scheme://host
// at path with no query, and returns it.
func sentOnceTo(t *testing.T, rec *nlptest.Recorder, c nlp.LLMClient, scheme, host, path string) nlptest.Request {
	t.Helper()
	if err := nlp.CheckDestination(context.Background(), c); err != nil {
		t.Fatalf("CheckDestination: %v", err)
	}
	if out, err := c.Complete(context.Background(), "sys", "user"); err != nil || out != "hello" {
		t.Fatalf("Complete = %q, %v", out, err)
	}
	got := rec.Requests()
	if len(got) != 1 || got[0].Method != http.MethodPost || got[0].Scheme != scheme ||
		!strings.EqualFold(got[0].Host, host) || got[0].Path != path || got[0].Query != "" {
		t.Fatalf("requests = %+v, want exactly one POST %s://%s%s with no query", got, scheme, host, path)
	}
	return got[0]
}

func TestUserKey_UnapprovedBaseURLSendsNothing(t *testing.T) { // AC-B1, AC-B2, AC-B16
	for _, provider := range []string{"openai", "anthropic"} {
		for _, base := range []string{internalAddr, "https://attacker.example", "http://10.0.0.1:5432", "http:/"} {
			t.Run(provider+" "+base, func(t *testing.T) {
				rec := nlptest.UserDestinations(t, "", "")
				refusal(t, rec, client(t, keyed(provider, base)), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
				err := completeErr(client(t, keyed(provider, base)))
				if strings.Contains(err.Error(), sentinelUser) {
					t.Errorf("refusal carries the key: %v", err)
				}
			})
		}
	}
}

// Was TestUserKey_RefusalDoesNotDependOnTheKey. NEW-llm-bring-your-own
// contract §6: with no key the caller is "AI not configured", decided before
// the destination, so no client is built and nothing can be sent; with a key
// the refusal is the destination's (TestUserKey_UnapprovedBaseURLSendsNothing).
func TestUserKey_NoKeyIsNotConfiguredBeforeTheDestination(t *testing.T) {
	_ = nlptest.UserDestinations(t, "", "")
	for _, provider := range []string{"openai", "anthropic"} {
		for _, base := range []string{internalAddr, ""} {
			d := nlp.DecideSaved(&storage.Settings{LLMProvider: provider, LLMBaseURL: base}, false)
			if d.Client != nil || d.NotConfigured == nil || d.NotConfigured.Reason != nlp.ReasonNeedsKey || d.NotConfigured.Provider != provider {
				t.Errorf("%s with no key and base %q: %+v, want needs_key", provider, base, d)
			}
		}
	}
}

func TestUserKey_PublicHostAndApprovedGatewaySendOnce(t *testing.T) { // AC-B5, AC-B19 (positive half)
	for _, tc := range []struct{ provider, public, path, header, value string }{
		{"openai", "api.openai.com", openAIPath, "Authorization", "Bearer " + sentinelUser},
		{"anthropic", "api.anthropic.com", anthropicPath, "x-api-key", sentinelUser},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			for _, base := range []string{"", "https://" + tc.public, "https://" + tc.public + "/", "https://" + strings.ToUpper(tc.public) + ":443"} {
				rec := nlptest.UserDestinations(t, gatewayHost, gatewayHost)
				req := sentOnceTo(t, rec, client(t, keyed(tc.provider, base)), "https", tc.public, tc.path)
				if req.Header.Get(tc.header) != tc.value {
					t.Errorf("%q: %s = %q, want the sentinel key", base, tc.header, req.Header.Get(tc.header))
				}
			}
			rec := nlptest.UserDestinations(t, gatewayHost, gatewayHost)
			req := sentOnceTo(t, rec, client(t, keyed(tc.provider, "https://"+gatewayHost)), "https", gatewayHost, tc.path)
			if req.Header.Get(tc.header) != tc.value {
				t.Errorf("gateway: %s = %q, want the sentinel key", tc.header, req.Header.Get(tc.header))
			}
		})
	}
}

func TestUserKey_InsecureOrOddPortOrNonCanonicalSendsNothing(t *testing.T) { // AC-B6, OQ-B6
	for _, provider := range []string{"openai", "anthropic"} {
		for _, base := range []string{
			"http://" + gatewayHost,
			"https://" + gatewayHost + ":8443",
			"https://" + gatewayHost + ".",
			"https://sub." + gatewayHost,
			"http://api.openai.com",
			"https://api.openai.com:8443",
			"https://api.anthropic.com.",
		} {
			t.Run(provider+" "+base, func(t *testing.T) {
				rec := nlptest.UserDestinations(t, gatewayHost, gatewayHost)
				refusal(t, rec, client(t, keyed(provider, base)), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
			})
		}
	}
}

func TestUserKey_PathQueryFragmentSendNothing(t *testing.T) { // AC-B19, D-B8
	for _, provider := range []string{"openai", "anthropic"} {
		for _, base := range []string{
			"https://api.openai.com/v1",
			"https://api.anthropic.com/v1/x",
			"https://" + gatewayHost + "/?x=1",
			"https://" + gatewayHost + "#",
			"https://" + gatewayHost + "?",
			"https://user@" + gatewayHost,
			" https://" + gatewayHost,
		} {
			t.Run(provider+" "+base, func(t *testing.T) {
				rec := nlptest.UserDestinations(t, gatewayHost, gatewayHost)
				refusal(t, rec, client(t, keyed(provider, base)), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
			})
		}
	}
}

// Was TestUserKey_DestinationApprovedOnlyForOllamaNeverGetsTheKey (AC-B11).
// Ollama approvals are gone: an origin once approved for Ollama is approved
// for no remaining provider (spec D-14), and an approval for one vendor still
// never transfers to the other.
func TestUserKey_DestinationApprovedOnlyForAnotherProviderNeverGetsTheKey(t *testing.T) { // AC-B11, D-14
	for _, provider := range []string{"openai", "anthropic"} {
		rec := nlptest.UserDestinations(t, "", "")
		refusal(t, rec, client(t, keyed(provider, ollamaOrigin)), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
		noKeyIn(t, rec.Requests())
	}
	rec := nlptest.UserDestinations(t, "", gatewayHost)
	refusal(t, rec, client(t, keyed("openai", "https://"+gatewayHost)), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
	rec = nlptest.UserDestinations(t, "https://"+gatewayHost+"/openai", "")
	refusal(t, rec, client(t, keyed("anthropic", "https://"+gatewayHost+"/openai")), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
}

// NEW-llm-bring-your-own AC-18, owner decision O-10: an operator-approved
// destination may carry a path prefix; requests go to the prefix followed by
// the chat operation and nowhere else; the same host with another, longer or
// missing prefix is refused.
func TestUserKey_GatewayPathPrefix(t *testing.T) { // AC-18
	for _, tc := range []struct{ provider, path string }{{"openai", openAIPath}, {"anthropic", anthropicPath}} {
		t.Run(tc.provider, func(t *testing.T) {
			for _, base := range []string{"https://" + gatewayHost + "/team/llm", "https://" + gatewayHost + "/team/llm/", "https://GATEWAY.example/team/llm"} {
				rec := nlptest.UserDestinations(t, "https://"+gatewayHost+"/team/llm", "https://"+gatewayHost+"/team/llm")
				sentOnceTo(t, rec, client(t, keyed(tc.provider, base)), "https", gatewayHost, "/team/llm"+tc.path)
			}
			for _, base := range []string{
				"https://" + gatewayHost,
				"https://" + gatewayHost + "/team",
				"https://" + gatewayHost + "/team/llm/x",
				"https://" + gatewayHost + "/Team/llm",
				"https://" + gatewayHost + "/team/llm?x=1",
				"https://" + gatewayHost + "/team/./llm",
				"https://" + gatewayHost + "/team/%6Clm",
			} {
				rec := nlptest.UserDestinations(t, "https://"+gatewayHost+"/team/llm", "https://"+gatewayHost+"/team/llm")
				refusal(t, rec, client(t, keyed(tc.provider, base)), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
			}
		})
	}
}

func TestUserProviders_RedirectIsNeverFollowed(t *testing.T) { // AC-B7, D-B6
	type dest struct {
		name   string
		s      storage.Settings
		scheme string
		host   string
		path   string
	}
	dests := []dest{
		{"openai public", keyed("openai", ""), "https", "api.openai.com", openAIPath},
		{"anthropic public", keyed("anthropic", ""), "https", "api.anthropic.com", anthropicPath},
		{"openai gateway", keyed("openai", "https://"+gatewayHost), "https", gatewayHost, openAIPath},
		{"anthropic gateway", keyed("anthropic", "https://"+gatewayHost), "https", gatewayHost, anthropicPath},
	}
	for _, d := range dests {
		origin := d.scheme + "://" + d.host
		targets := []string{
			"https://attacker.example" + d.path,
			"https://sub." + strings.Split(d.host, ":")[0] + d.path,
			"http://" + d.host + d.path,
			internalAddr + "/latest/meta-data",
			origin + "/api/pull",
			origin + d.path + "?x=1",
			"https://second.example" + d.path, // also approved for the same provider
			ollamaOrigin + d.path,
		}
		for _, status := range []int{301, 302, 303, 307, 308} {
			for _, target := range targets {
				t.Run(d.name+" "+http.StatusText(status)+" "+target, func(t *testing.T) {
					rec := nlptest.UserDestinations(t, gatewayHost+",second.example", gatewayHost+",second.example")
					first := true
					rec.Answer = func(r *http.Request) *http.Response {
						if first {
							first = false
							return nlptest.Response(r, status, "", http.Header{"Location": {target}})
						}
						return nil
					}
					err := completeErr(client(t, d.s))
					if err == nil {
						t.Fatal("Complete succeeded after a redirect; want failure")
					}
					var r *nlp.DestinationRefusal
					if errors.As(err, &r) {
						t.Errorf("a redirect is reported as a destination refusal (422); want a provider failure (502): %v", err)
					}
					got := rec.Requests()
					if len(got) != 1 || got[0].Method != http.MethodPost || !strings.EqualFold(got[0].Host, d.host) || got[0].Path != d.path {
						t.Fatalf("requests = %+v, want only the first POST to %s%s", got, d.host, d.path)
					}
					if strings.Contains(err.Error(), sentinelUser) {
						t.Errorf("error carries the key: %v", err)
					}
				})
			}
		}
	}
}

func TestUserProviders_ParseRefusesAndDoesNotFollowRedirects(t *testing.T) { // AC-B12, round-3 NB-2
	db := openNLPDB(t)
	rec := nlptest.UserDestinations(t, "", "")
	s := keyed("openai", internalAddr)
	ctx := nlpUser(t, db, &s)
	res, err := (&nlp.NLPService{DB: db}).Parse(ctx, "meet bob tomorrow")
	if err != nil || res.Intent != "unknown" || !strings.Contains(res.Error, "not an approved") || res.NotConfigured != nil {
		t.Fatalf("parse = %+v, %v; want intent unknown with a not-approved sentence", res, err)
	}
	if strings.Contains(res.Error, sentinelUser) {
		t.Errorf("parse error carries the key: %q", res.Error)
	}
	if got := rec.Requests(); len(got) != 0 {
		t.Fatalf("parse recorded %d requests, want zero: %+v", len(got), got)
	}

	// An approved destination that redirects: exactly one request per Complete
	// on the parse path.
	rec = nlptest.UserDestinations(t, "", "")
	rec.Answer = func(r *http.Request) *http.Response {
		return nlptest.Response(r, http.StatusTemporaryRedirect, "", http.Header{"Location": {internalAddr + "/x"}})
	}
	s = keyed("openai", "")
	s.Timezone = "UTC"
	ctx = nlpUser(t, db, &s)
	_, _ = (&nlp.NLPService{DB: db}).Parse(ctx, "block focus tomorrow")
	for _, r := range rec.Requests() {
		if r.Host != "api.openai.com" || r.Path != openAIPath || r.Query != "" {
			t.Errorf("parse sent a request to %s%s?%s; want only the approved chat operation", r.Host, r.Path, r.Query)
		}
	}
	// Parse calls Complete for the attendees first; its failure is ignored,
	// then the prompt's Complete fails too. Each is one request, no follow-up.
	if got := rec.Requests(); len(got) != 2 {
		t.Errorf("parse recorded %d requests, want 2 (one per Complete, no redirect followed): %+v", len(got), got)
	}
}

func TestUserWriteChecks(t *testing.T) { // AC-B3, AC-B4, AC-B6, AC-B19, AC-18 (write side)
	_ = nlptest.UserDestinations(t, gatewayHost+",https://gw.example/openai", "proxy.example")
	for v, want := range map[string]string{
		"":                                 "",
		"https://api.openai.com":           "",
		"https://api.anthropic.com/":       "",
		"https://" + gatewayHost:           "",
		"https://proxy.example":            "",
		"https://gw.example/openai":        "",
		"https://gw.example/openai/":       "",
		"https://gw.example":               nlp.MsgLLMBaseURLNotApproved,
		"https://gw.example/other":         nlp.MsgLLMBaseURLNotApproved,
		"https://gw.example/openai/x":      nlp.MsgLLMBaseURLNotApproved,
		"https://gw.example/openai?x=1":    nlp.MsgLLMBaseURLNotApproved,
		ollamaOrigin:                       nlp.MsgLLMBaseURLNotApproved, // approved for no remaining provider (D-14)
		internalAddr:                       nlp.MsgLLMBaseURLNotApproved,
		"http://" + gatewayHost:            nlp.MsgLLMBaseURLNotApproved,
		"https://" + gatewayHost + ":8443": nlp.MsgLLMBaseURLNotApproved,
		"https://api.openai.com/v1":        nlp.MsgLLMBaseURLNotApproved,
		ollamaOrigin + "/api/pull?x=":      nlp.MsgLLMBaseURLNotApproved,
		ollamaOrigin + "#":                 nlp.MsgLLMBaseURLNotApproved,
		ollamaOrigin + "/x/../api/pull":    nlp.MsgLLMBaseURLNotApproved,
		"not a url":                        nlp.MsgLLMBaseURLNotApproved,
	} {
		if got := nlp.LLMBaseURLWriteProblem(v); got != want {
			t.Errorf("llmBaseUrl %q: %q, want %q", v, got, want)
		}
	}
	if nlp.LLMBaseURLWriteProblem("") != "" {
		t.Error("\"\" must always be accepted (AC-B14)")
	}
}

func openNLPDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		if err := cleanup(); err != nil {
			t.Errorf("cleanup test database: %v", err)
		}
	})
	return db
}

// nlpUser saves s as a new user's settings and returns a context carrying that user.
func nlpUser(t *testing.T, db *sql.DB, s *storage.Settings) context.Context {
	t.Helper()
	email := fmt.Sprintf("pac66b-%d@example.com", time.Now().UnixNano())
	u, err := storage.UpsertUser(db, email, email, "", "google", email)
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if err := storage.SaveSettingsForUser(db, u.ID, s); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	return context.WithValue(context.Background(), auth.UserIDKey, u.ID)
}
