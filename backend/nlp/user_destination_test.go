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

// PAC-66-B: a user-set OpenAI, Anthropic or Ollama base URL goes only to a
// destination approved for the provider in use. Every test runs with a
// sentinel user key and a recording transport (nlptest.UserDestinations), so
// the pre-PAC-66-B code would have sent each refused request.

const (
	gatewayHost   = "gateway.example"
	ollamaOrigin  = "http://ollama.internal:11434"
	internalAddr  = "http://169.254.169.254"
	sentinelUser  = nlptest.UserKey
	openAIPath    = "/v1/chat/completions"
	anthropicPath = "/v1/messages"
	ollamaPath    = "/api/chat"
)

// keyed is a stored row for a user-key provider with the sentinel key.
func keyed(provider, base string) storage.Settings {
	return storage.Settings{LLMProvider: provider, LLMAPIKey: sentinelUser, LLMBaseURL: base}
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
				rec := nlptest.UserDestinations(t, "", "", "")
				refusal(t, rec, client(t, keyed(provider, base)), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
				err := completeErr(client(t, keyed(provider, base)))
				if strings.Contains(err.Error(), sentinelUser) {
					t.Errorf("refusal carries the key: %v", err)
				}
			})
		}
	}
}

func TestUserKey_RefusalDoesNotDependOnTheKey(t *testing.T) { // §B4 "Refusal does not depend on the key"
	_ = nlptest.UserDestinations(t, "", "", "")
	for _, provider := range []string{"openai", "anthropic"} {
		_, err := nlp.NewLLMClientFromSettings(&storage.Settings{LLMProvider: provider, LLMBaseURL: internalAddr})
		var r *nlp.DestinationRefusal
		if !errors.As(err, &r) || r.Reason != nlp.ReasonDestinationNotApproved || r.Property != "llmBaseUrl" {
			t.Errorf("%s with no key and an unapproved base URL: err = %v, want the destination refusal", provider, err)
		}
		// With no key and an approved (empty) base URL it is still "not configured".
		_, err = nlp.NewLLMClientFromSettings(&storage.Settings{LLMProvider: provider})
		if err == nil || errors.As(err, &r) {
			t.Errorf("%s with no key and no base URL: err = %v, want the not-configured error", provider, err)
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
				rec := nlptest.UserDestinations(t, gatewayHost, gatewayHost, "")
				req := sentOnceTo(t, rec, client(t, keyed(tc.provider, base)), "https", tc.public, tc.path)
				if req.Header.Get(tc.header) != tc.value {
					t.Errorf("%q: %s = %q, want the sentinel key", base, tc.header, req.Header.Get(tc.header))
				}
			}
			rec := nlptest.UserDestinations(t, gatewayHost, gatewayHost, "")
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
				rec := nlptest.UserDestinations(t, gatewayHost, gatewayHost, "")
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
				rec := nlptest.UserDestinations(t, gatewayHost, gatewayHost, "")
				refusal(t, rec, client(t, keyed(provider, base)), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
			})
		}
	}
}

func TestUserKey_DestinationApprovedOnlyForOllamaNeverGetsTheKey(t *testing.T) { // AC-B11
	for _, provider := range []string{"openai", "anthropic"} {
		rec := nlptest.UserDestinations(t, "", "", ollamaOrigin+",https://"+gatewayHost)
		for _, base := range []string{ollamaOrigin, "https://" + gatewayHost} {
			refusal(t, rec, client(t, keyed(provider, base)), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
		}
		noKeyIn(t, rec.Requests())
	}
	// Converse: a host approved only for openai/anthropic is refused for ollama.
	rec := nlptest.UserDestinations(t, gatewayHost, gatewayHost, ollamaOrigin)
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "ollama", OllamaBaseURL: "https://" + gatewayHost}),
		nlp.ReasonDestinationNotApproved, "ollamaBaseUrl")
}

func TestOllama_NothingApprovedMeansNotEnabled(t *testing.T) { // AC-B8, D-B1
	for name, s := range map[string]storage.Settings{
		"starting state": {LLMProvider: "ollama", OllamaBaseURL: "http://localhost:11434"},
		"both empty":     {LLMProvider: "ollama"},
		"shared only":    {LLMProvider: "ollama", LLMBaseURL: ollamaOrigin},
		"internal":       {LLMProvider: "ollama", OllamaBaseURL: internalAddr},
	} {
		t.Run(name, func(t *testing.T) {
			rec := nlptest.UserDestinations(t, gatewayHost, "", "")
			refusal(t, rec, client(t, s), nlp.ReasonProviderNotEnabled, "")
			if err := completeErr(client(t, s)); !strings.Contains(err.Error(), "not enabled on this deployment") {
				t.Errorf("message %q does not say ollama is not enabled", err)
			}
		})
	}
}

func TestOllama_ApprovedOriginSendsOnceWithNoKey(t *testing.T) { // AC-B9, AC-B18 (positive half)
	for _, base := range []string{ollamaOrigin, ollamaOrigin + "/", "HTTP://Ollama.Internal:11434"} {
		rec := nlptest.UserDestinations(t, "", "", ollamaOrigin)
		req := sentOnceTo(t, rec, client(t, storage.Settings{LLMProvider: "ollama", OllamaBaseURL: base, LLMAPIKey: sentinelUser}),
			"http", "ollama.internal:11434", ollamaPath)
		if req.Header.Get("Authorization") != "" || req.Header.Get("x-api-key") != "" {
			t.Errorf("%q: the ollama request carries a credential: %v", base, req.Header)
		}
	}
	// A different scheme, host or port is refused.
	for _, base := range []string{"https://ollama.internal:11434", "http://ollama.internal:11435", "http://other:11434", "http://ollama.internal"} {
		rec := nlptest.UserDestinations(t, "", "", ollamaOrigin)
		refusal(t, rec, client(t, storage.Settings{LLMProvider: "ollama", OllamaBaseURL: base}),
			nlp.ReasonDestinationNotApproved, "ollamaBaseUrl")
	}
}

func TestOllama_SharedBaseURLIsNotTheDestinationWhileTheDefaultIsStored(t *testing.T) { // AC-B9 (Revision B2), D-B9
	s := storage.Settings{LLMProvider: "ollama", OllamaBaseURL: "http://localhost:11434", LLMBaseURL: ollamaOrigin}
	rec := nlptest.UserDestinations(t, "", "", ollamaOrigin)
	refusal(t, rec, client(t, s), nlp.ReasonDestinationNotApproved, "ollamaBaseUrl")

	rec = nlptest.UserDestinations(t, "", "", ollamaOrigin+",http://localhost:11434")
	sentOnceTo(t, rec, client(t, s), "http", "localhost:11434", ollamaPath)
}

func TestOllama_EffectiveDestination(t *testing.T) { // AC-B10
	rec := nlptest.UserDestinations(t, "", "", "http://localhost:11434")
	sentOnceTo(t, rec, client(t, storage.Settings{LLMProvider: "ollama", OllamaBaseURL: "http://localhost:11434"}),
		"http", "localhost:11434", ollamaPath)
	rec = nlptest.UserDestinations(t, "", "", "http://localhost:11434")
	sentOnceTo(t, rec, client(t, storage.Settings{LLMProvider: "ollama"}), "http", "localhost:11434", ollamaPath)

	// Default not approved, something else is: refused, naming the property the
	// user would save an approved origin in.
	for _, s := range []storage.Settings{
		{LLMProvider: "ollama", OllamaBaseURL: "http://localhost:11434"},
		{LLMProvider: "ollama"},
	} {
		rec = nlptest.UserDestinations(t, "", "", ollamaOrigin)
		refusal(t, rec, client(t, s), nlp.ReasonDestinationNotApproved, "ollamaBaseUrl")
	}
	// The shared base URL is the destination only when ollamaBaseUrl is empty,
	// and then it is the property named.
	rec = nlptest.UserDestinations(t, "", "", ollamaOrigin)
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "ollama", LLMBaseURL: "http://other:11434"}),
		nlp.ReasonDestinationNotApproved, "llmBaseUrl")
	rec = nlptest.UserDestinations(t, "", "", ollamaOrigin)
	sentOnceTo(t, rec, client(t, storage.Settings{LLMProvider: "ollama", LLMBaseURL: ollamaOrigin}),
		"http", "ollama.internal:11434", ollamaPath)
}

func TestOllama_PathQueryFragmentSendNothing(t *testing.T) { // AC-B18
	for _, base := range []string{
		ollamaOrigin + "/api/pull?x=",
		ollamaOrigin + "#",
		ollamaOrigin + "/x/../api/pull",
		ollamaOrigin + "/api/chat",
		ollamaOrigin + "?",
	} {
		rec := nlptest.UserDestinations(t, "", "", ollamaOrigin)
		refusal(t, rec, client(t, storage.Settings{LLMProvider: "ollama", OllamaBaseURL: base}),
			nlp.ReasonDestinationNotApproved, "ollamaBaseUrl")
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
		{"ollama", storage.Settings{LLMProvider: "ollama", OllamaBaseURL: ollamaOrigin}, "http", "ollama.internal:11434", ollamaPath},
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
			"http://second-ollama:11434" + d.path,
		}
		for _, status := range []int{301, 302, 303, 307, 308} {
			for _, target := range targets {
				t.Run(d.name+" "+http.StatusText(status)+" "+target, func(t *testing.T) {
					rec := nlptest.UserDestinations(t, gatewayHost+",second.example", gatewayHost+",second.example",
						ollamaOrigin+",http://second-ollama:11434")
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
	rec := nlptest.UserDestinations(t, "", "", ollamaOrigin)
	ctx := nlpUser(t, db, &storage.Settings{LLMProvider: "openai", LLMAPIKey: sentinelUser, LLMBaseURL: internalAddr})
	res, err := (&nlp.NLPService{DB: db}).Parse(ctx, "meet bob tomorrow")
	if err != nil || res.Intent != "unknown" || !strings.Contains(res.Error, "not an approved") {
		t.Fatalf("parse = %+v, %v; want intent unknown with a not-approved sentence", res, err)
	}
	if strings.Contains(res.Error, sentinelUser) {
		t.Errorf("parse error carries the key: %q", res.Error)
	}
	if got := rec.Requests(); len(got) != 0 {
		t.Fatalf("parse recorded %d requests, want zero: %+v", len(got), got)
	}

	// An approved destination that redirects: exactly one request on the parse path.
	rec = nlptest.UserDestinations(t, "", "", ollamaOrigin)
	rec.Answer = func(r *http.Request) *http.Response {
		return nlptest.Response(r, http.StatusTemporaryRedirect, "", http.Header{"Location": {internalAddr + "/x"}})
	}
	ctx = nlpUser(t, db, &storage.Settings{LLMProvider: "ollama", OllamaBaseURL: ollamaOrigin, Timezone: "UTC"})
	_, _ = (&nlp.NLPService{DB: db}).Parse(ctx, "block focus tomorrow")
	for _, r := range rec.Requests() {
		if r.Host != "ollama.internal:11434" || r.Path != ollamaPath || r.Query != "" {
			t.Errorf("parse sent a request to %s%s?%s; want only the approved chat operation", r.Host, r.Path, r.Query)
		}
	}
	// Parse calls Complete twice (attendees, then the prompt); each is one
	// request with no follow-up.
	if got := rec.Requests(); len(got) != 2 {
		t.Errorf("parse recorded %d requests, want 2 (one per Complete, no redirect followed): %+v", len(got), got)
	}
}

func TestUserWriteChecks(t *testing.T) { // AC-B3, AC-B4, AC-B6, AC-B18, AC-B19 (write side)
	_ = nlptest.UserDestinations(t, gatewayHost, "proxy.example", ollamaOrigin)
	for v, want := range map[string]string{
		"":                                 "",
		"https://api.openai.com":           "",
		"https://api.anthropic.com/":       "",
		"https://" + gatewayHost:           "",
		"https://proxy.example":            "",
		ollamaOrigin:                       "", // approved for ollama (D-B5)
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
	for v, want := range map[string]string{
		"":                            "",
		ollamaOrigin:                  "",
		ollamaOrigin + "/":            "",
		"http://ollama.internal:80":   nlp.MsgOllamaBaseURLNotApproved,
		"http://localhost:11434":      nlp.MsgOllamaBaseURLNotApproved,
		"https://" + gatewayHost:      nlp.MsgOllamaBaseURLNotApproved,
		ollamaOrigin + "/api/pull?x=": nlp.MsgOllamaBaseURLNotApproved,
	} {
		if got := nlp.OllamaBaseURLWriteProblem(v); got != want {
			t.Errorf("ollamaBaseUrl %q: %q, want %q", v, got, want)
		}
	}

	_ = nlptest.UserDestinations(t, "", "", "")
	if got := nlp.OllamaBaseURLWriteProblem(ollamaOrigin); got != nlp.MsgOllamaBaseURLNotEnabled {
		t.Errorf("nothing approved for ollama: %q, want %q", got, nlp.MsgOllamaBaseURLNotEnabled)
	}
	if got := nlp.LLMBaseURLWriteProblem(ollamaOrigin); got != nlp.MsgLLMBaseURLNotApproved {
		t.Errorf("nothing approved for ollama: llmBaseUrl %q, want %q", got, nlp.MsgLLMBaseURLNotApproved)
	}
	if nlp.OllamaBaseURLWriteProblem("") != "" || nlp.LLMBaseURLWriteProblem("") != "" {
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
