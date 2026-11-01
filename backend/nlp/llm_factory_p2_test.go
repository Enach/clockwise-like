package nlp

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-73 Part 2 (spec docs/specs/NEW-llm-bring-your-own-part2.md §3.1,
// §3.5): the decision table with an organisation configuration, at unit
// level. The HTTP-level counterparts (every operation, nothing sent) are in
// backend/api/pac73p2_*_test.go.

const (
	p2OrgKey = "sk-org-unit-key/with/slashes"
	p2OwnKey = "sk-own-unit-key"
)

type p2Org struct {
	in, complete, forbids bool
	confirmed             bool
}

func p2State(s *storage.Settings, o p2Org) AIState {
	st := AIState{Settings: s, Org: storage.OrgAIState{Config: storage.OrgAIConfig{PersonalKeysAllowed: !o.forbids, Version: 7}}}
	if o.in {
		id := uuid.New()
		st.Org.OrgID = &id
	}
	if o.complete {
		st.Org.Config.Provider, st.Org.Config.APIKey, st.Org.Config.Model = ProviderAnthropic, p2OrgKey, "claude-org"
	}
	if o.confirmed {
		st.Org.ConfirmedVersion = 7
	}
	return st
}

func p2Settings(choice, stored string, key bool) *storage.Settings {
	s := &storage.Settings{LLMProvider: choice, StoredLLMProvider: stored, LLMModel: "own-model"}
	if s.StoredLLMProvider == "" {
		s.StoredLLMProvider = choice
	}
	if key && IsKeyProvider(choice) {
		s.LLMAPIKeys.Set(choice, p2OwnKey)
	}
	return s
}

// P2-AC-29 at unit level: one state, one row, one reason, who can fix it;
// only rows 2 and 4 configured. Row 9 is reserved and never answered.
func TestDecideState_EveryRow(t *testing.T) {
	inOrg := p2Org{in: true}
	complete := p2Org{in: true, complete: true}
	for _, tc := range []struct {
		row      string
		s        *storage.Settings
		org      p2Org
		reason   string
		enablers string
		config   string
	}{
		{"1 off (forbidden, complete, confirmed)", p2Settings("", "", false), p2Org{in: true, complete: true, forbids: true, confirmed: true}, ReasonOff, "user", ""},
		{"10 own provider, forbids, complete", p2Settings("openai", "", true), p2Org{in: true, complete: true, forbids: true, confirmed: true}, ReasonPersonalKeysForbidden, "user", ""},
		{"10 removed provider, forbids, complete", p2Settings("unsupported", "ollama", false), p2Org{in: true, complete: true, forbids: true}, ReasonPersonalKeysForbidden, "user", ""},
		{"11 own provider, forbids, not complete", p2Settings("openai", "", true), p2Org{in: true, forbids: true}, ReasonPersonalKeysForbidden, "organization_admin", ""},
		{"2 own key in a complete org", p2Settings("openai", "", true), complete, "", "", ConfigurationOwn},
		{"3 own provider without key", p2Settings("bedrock", "", false), complete, ReasonNeedsKey, "user", ""},
		{"4 follower, complete, confirmed", p2Settings("organization", "", false), p2Org{in: true, complete: true, confirmed: true}, "", "", ConfigurationOrganization},
		{"5 follower, complete, not confirmed", p2Settings("organization", "", false), complete, ReasonOrganizationUnconfirmed, "user", ""},
		{"6 follower, no org", p2Settings("organization", "", false), p2Org{}, ReasonNothingConfigured, "user", ""},
		{"6 follower, org not complete", p2Settings("organization", "", false), inOrg, ReasonNothingConfigured, "user,organization_admin", ""},
		{"6 follower, org forbids, not complete", p2Settings("organization", "", false), p2Org{in: true, forbids: true}, ReasonNothingConfigured, "organization_admin", ""},
		{"7 ollama, org complete", p2Settings("unsupported", "ollama", false), complete, ReasonProviderRemovedOrganizationAvailable, "user", ""},
		{"8 ollama, org not complete", p2Settings("unsupported", "ollama", false), inOrg, ReasonProviderRemoved, "user,organization_admin", ""},
		{"8 ollama, no org", p2Settings("unsupported", "ollama", false), p2Org{}, ReasonProviderRemoved, "user", ""},
	} {
		t.Run(tc.row, func(t *testing.T) {
			st := p2State(tc.s, tc.org)
			d := DecideState(st)
			status := StatusOf(st)
			probe, bad := DecideProbeState(st, ProbeRequest{})
			if bad != "" {
				t.Fatalf("probe {} refused: %s", bad)
			}
			if tc.config != "" {
				if d.Client == nil || d.Configuration != tc.config || probe.Configuration != tc.config || !status.Configured || status.Configuration != tc.config {
					t.Fatalf("decision %+v / probe %+v / status %+v, want configured %s", d, probe, status, tc.config)
				}
				return
			}
			for what, nc := range map[string]*storage.AINotConfigured{"saved": d.NotConfigured, "probe {}": probe.NotConfigured, "status": status.NotConfigured} {
				if nc == nil || nc.Reason != tc.reason || strings.Join(nc.Enablers, ",") != tc.enablers {
					t.Fatalf("%s = %+v, want %s [%s]", what, nc, tc.reason, tc.enablers)
				}
				if got, want := strings.Contains(nc.Message, "administrator of your organisation"), strings.Contains(tc.enablers, "organization_admin"); got != want {
					t.Errorf("%s message %q names the admin = %v, want %v", what, nc.Message, got, want)
				}
				if strings.Contains(nc.Message, p2OrgKey) || strings.Contains(nc.Message, p2OwnKey) {
					t.Errorf("%s message carries a key", what)
				}
			}
			if d.Client != nil || status.Configured || status.Configuration != "" {
				t.Errorf("not configured but %+v / %+v", d, status)
			}
		})
	}
}

// Row 4 sends only the organisation's values (parent D-18, P2-AC-12): the
// member's own destination values and model never mix in.
func TestDecideState_Row4UsesOnlyTheOrganisation(t *testing.T) {
	s := p2Settings("organization", "", false)
	s.AzureEndpoint, s.AzureDeployment, s.AWSRegion, s.LLMBaseURL = "https://member.openai.azure.com", "member-dep", "ap-south-1", "https://member-gw.example"
	s.LLMAPIKeys.Set(ProviderOpenAI, p2OwnKey)
	st := p2State(s, p2Org{in: true, confirmed: true})
	st.Org.Config = storage.OrgAIConfig{Provider: ProviderAzureOpenAI, APIKey: p2OrgKey, AzureEndpoint: "https://org.openai.azure.com",
		AzureDeployment: "org-dep", PersonalKeysAllowed: true, Version: 7}
	c, ok := DecideState(st).Client.(*AzureOpenAIClient)
	if !ok || c.APIKey != p2OrgKey || c.Endpoint != "https://org.openai.azure.com" || c.Deployment != "org-dep" || c.APIVersion != defaultAzureAPIVersion {
		t.Fatalf("row 4 azure client = %+v", c)
	}
	st.Org.Config = storage.OrgAIConfig{Provider: ProviderOpenAI, APIKey: p2OrgKey, PersonalKeysAllowed: true, Version: 7}
	o, ok := DecideState(st).Client.(*OpenAIClient)
	if !ok || o.BaseURL != "" || o.APIKey != p2OrgKey || o.Model != defaultOpenAIModel {
		t.Fatalf("row 4 openai client = %+v (the member's gateway and model must not be used)", o)
	}
	for _, p := range []string{ProviderAnthropic, ProviderBedrock, ProviderVertex} {
		st.Org.Config = storage.OrgAIConfig{Provider: p, APIKey: p2OrgKey, Model: "org-model", AWSRegion: "eu-west-3", PersonalKeysAllowed: true, Version: 7}
		switch c := DecideState(st).Client.(type) {
		case *AnthropicClient:
			if c.Model != "org-model" || c.APIKey != p2OrgKey || c.BaseURL != "" {
				t.Errorf("anthropic %+v", c)
			}
		case *BedrockClient:
			if c.ModelID != "org-model" || c.Region != "eu-west-3" || c.APIKey != p2OrgKey {
				t.Errorf("bedrock %+v", c)
			}
		case *VertexClient:
			if c.ModelID != "org-model" || c.APIKey != p2OrgKey {
				t.Errorf("vertex %+v", c)
			}
		default:
			t.Errorf("%s: client %T", p, c)
		}
	}
	// A confirmation of another version is not a confirmation.
	st.Org.ConfirmedVersion = 6
	if d := DecideState(st); d.NotConfigured == nil || d.NotConfigured.Reason != ReasonOrganizationUnconfirmed {
		t.Fatalf("stale confirmation = %+v, want row 5", d)
	}
}

// §3.5, P2-AC-20, P2-AC-28: the probe's Part 2 rules.
func TestDecideProbeState_Part2(t *testing.T) {
	str := func(v string) *string { return &v }
	follower := p2Settings("organization", "", false)
	confirmed := p2Org{in: true, complete: true, confirmed: true}
	forbidden := p2Org{in: true, complete: true, confirmed: true, forbids: true}
	for _, tc := range []struct {
		name     string
		org      p2Org
		req      ProbeRequest
		bad      string
		reason   string
		enablers string
		config   string
		key      string
	}{
		{"{} tests the organisation", confirmed, ProbeRequest{}, "", "", "", ConfigurationOrganization, p2OrgKey},
		{"organization tests the organisation", confirmed, ProbeRequest{Provider: str("organization")}, "", "", "", ConfigurationOrganization, p2OrgKey},
		{"naming the org's provider never selects its key", confirmed, ProbeRequest{Provider: str("anthropic")}, "", ReasonNeedsKey, "user", "", ""},
		{"naming a provider with a key sends only that key", confirmed, ProbeRequest{Provider: str("openai"), Key: str("sk-typed")}, "", "", "", ConfigurationOwn, "sk-typed"},
		{"{} with a model is refused", confirmed, ProbeRequest{Model: str("m")}, MsgProbeOrganizationNoModel, "", "", "", ""},
		{"organization with a key is refused", confirmed, ProbeRequest{Provider: str("organization"), Key: str("k")}, MsgProbeOrganizationNoModel, "", "", "", ""},
		{"unconfirmed {} is row 5", p2Org{in: true, complete: true}, ProbeRequest{}, "", ReasonOrganizationUnconfirmed, "user", "", ""},
		{"forbidden: naming a provider", forbidden, ProbeRequest{Provider: str("openai")}, "", ReasonPersonalKeysForbidden, "organization_admin", "", ""},
		{"forbidden: a key with nothing named beats the row-4 400", forbidden, ProbeRequest{Key: str("k")}, "", ReasonPersonalKeysForbidden, "organization_admin", "", ""},
		{"forbidden: organization with a key", forbidden, ProbeRequest{Provider: str("organization"), Key: str("k")}, "", ReasonPersonalKeysForbidden, "organization_admin", "", ""},
		{"forbidden: a shape error still wins", forbidden, ProbeRequest{Provider: str("ollama"), Key: str("k")}, MsgProbeProviderInvalid, "", "", "", ""},
		{"forbidden: {} by a follower is row 4", forbidden, ProbeRequest{}, "", "", "", ConfigurationOrganization, p2OrgKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, bad := DecideProbeState(p2State(follower, tc.org), tc.req)
			if bad != tc.bad {
				t.Fatalf("bad = %q, want %q", bad, tc.bad)
			}
			if bad != "" {
				return
			}
			if tc.reason != "" {
				if d.NotConfigured == nil || d.NotConfigured.Reason != tc.reason || strings.Join(d.NotConfigured.Enablers, ",") != tc.enablers {
					t.Fatalf("decision %+v, want %s [%s]", d.NotConfigured, tc.reason, tc.enablers)
				}
				return
			}
			if d.Configuration != tc.config || !containsKey(d.Secrets, tc.key) {
				t.Fatalf("decision %+v, want %s with key %s", d, tc.config, tc.key)
			}
		})
	}
	// A member on their own provider while forbidden: the probe naming
	// nothing answers row 10 (P2-AC-18).
	own := p2Settings("openai", "", true)
	if d, _ := DecideProbeState(p2State(own, forbidden), ProbeRequest{}); d.NotConfigured == nil || d.NotConfigured.Reason != ReasonPersonalKeysForbidden || d.NotConfigured.Enablers[0] != EnablerUser {
		t.Fatalf("own provider, forbidden, {} = %+v", d)
	}
}

func containsKey(list []string, k string) bool {
	for _, v := range list {
		if v == k {
			return true
		}
	}
	return false
}

// P2-AC-26: every not-configured sentence; the row 5 sentence contains
// "access"; none contains "read", "reads" or "see" as a word.
func TestNotConfiguredSentences_O13Wording(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)\b(read|reads|see)\b`)
	str := func(v string) *string { return &v }
	stored := []*storage.Settings{
		p2Settings("", "", false), p2Settings("organization", "", false), p2Settings("openai", "", true),
		p2Settings("openai", "", false), p2Settings("unsupported", "ollama", false), p2Settings("unsupported", "skynet", false),
	}
	orgs := []p2Org{{}, {in: true}, {in: true, complete: true}, {in: true, complete: true, confirmed: true},
		{in: true, forbids: true}, {in: true, complete: true, forbids: true}}
	reqs := []ProbeRequest{{}, {Key: str("")}, {Provider: str("organization")}, {Provider: str("")}, {Provider: str("anthropic")},
		{Provider: str("openai"), Key: str("x")}, {Provider: str("unsupported")}}
	seen := map[string]bool{}
	check := func(what string, nc *storage.AINotConfigured) {
		if nc == nil {
			return
		}
		seen[nc.Reason] = true
		if forbidden.MatchString(nc.Message) {
			t.Errorf("%s: %q contains read/reads/see", what, nc.Message)
		}
		if nc.Reason == ReasonOrganizationUnconfirmed && !strings.Contains(nc.Message, "access") {
			t.Errorf("%s: the row 5 sentence %q does not say access", what, nc.Message)
		}
	}
	for _, s := range stored {
		for _, o := range orgs {
			st := p2State(s, o)
			check("saved", DecideState(st).NotConfigured)
			for _, req := range reqs {
				d, _ := DecideProbeState(st, req)
				check("probe", d.NotConfigured)
			}
		}
	}
	for _, r := range []string{ReasonOff, ReasonNeedsKey, ReasonOrganizationUnconfirmed, ReasonNothingConfigured,
		ReasonProviderRemovedOrganizationAvailable, ReasonProviderRemoved, ReasonPersonalKeysForbidden} {
		if !seen[r] {
			t.Errorf("no sentence for %s was checked", r)
		}
	}
	// Row 10's sentence holds for a member on a removed provider, who has
	// no key (spec revision 2.1 NB4).
	d := DecideState(p2State(p2Settings("unsupported", "ollama", false), p2Org{in: true, complete: true, forbids: true}))
	if !strings.Contains(d.NotConfigured.Message, "Ollama") || !strings.Contains(d.NotConfigured.Message, "kept but not used") ||
		!strings.Contains(d.NotConfigured.Message, `"Use my organisation's AI"`) {
		t.Errorf("row 10 for Ollama = %q", d.NotConfigured.Message)
	}
	if !strings.Contains(DecideState(p2State(p2Settings("organization", "", false), p2Org{in: true, complete: true})).NotConfigured.Message, "Anthropic") {
		t.Error("row 5 does not name the organisation's provider")
	}
}

// §3.1a: exact-string redaction, longest first.
func TestRedactSecrets(t *testing.T) {
	got := RedactSecrets("a sk-1 b sk-12 c sk-12", []string{"", "sk-1", "sk-12"})
	if got != "a [redacted] b [redacted] c [redacted]" {
		t.Errorf("RedactSecrets = %q", got)
	}
	d := Decision{Secrets: []string{p2OrgKey}}
	if d.Redact("echo "+p2OrgKey) != "echo [redacted]" {
		t.Error("Decision.Redact")
	}
}

// Spec P2-D18: the organisation's destination addresses.
func TestOrgDestinationWriteChecks(t *testing.T) {
	for v, ok := range map[string]bool{
		"": true, "https://org.openai.azure.com": true, "https://org.openai.azure.com/": true,
		"http://org.openai.azure.com": false, "https://org.openai.azure.com/path": false,
		"https://org.openai.azure.com?x=1": false, "https://org.openai.azure.com#f": false,
		"https://user:pw@org.openai.azure.com": false, "https://org.openai.azure.com:8443": false,
		"https://org.openai.azure.com//": false, "org.openai.azure.com": false,
	} {
		if got := OrgAzureEndpointShapeProblem(v) == ""; got != ok {
			t.Errorf("OrgAzureEndpointShapeProblem(%q) acceptable = %v, want %v", v, got, ok)
		}
	}
	d, notices := config.DecideLLMUserDestinations(func(k string) string {
		if k == config.EnvLLMApprovedHostsOpenAI {
			return "https://gw.example/openai"
		}
		return ""
	})
	if len(notices) > 0 {
		t.Fatal(notices)
	}
	t.Cleanup(SetUserDestinations(d))
	for _, tc := range []struct {
		provider, v string
		ok          bool
	}{
		{ProviderOpenAI, "", true},
		{ProviderOpenAI, "https://gw.example/openai", true},
		{ProviderOpenAI, "https://api.openai.com", true},
		{ProviderOpenAI, "https://gw.example/openai?token=x", false},
		{ProviderOpenAI, "https://gw.example/openai#x", false},
		{ProviderOpenAI, "https://gw.example/other", false},
		{ProviderOpenAI, "https://u:p@gw.example/openai", false},
		{ProviderAnthropic, "https://gw.example/openai", false},
	} {
		if got := OrgBaseURLWriteProblem(tc.provider, tc.v) == ""; got != tc.ok {
			t.Errorf("OrgBaseURLWriteProblem(%s, %q) acceptable = %v, want %v", tc.provider, tc.v, got, tc.ok)
		}
	}
	r := OrganizationDestinationRefusal(&DestinationRefusal{Reason: ReasonDestinationNotApproved, Property: "llmBaseUrl", Message: "clear it in Settings"})
	if strings.Contains(r.Message, "Settings") || !strings.Contains(r.Message, "administrator of your organisation") || r.Property != "llmBaseUrl" {
		t.Errorf("organisation refusal = %+v", r)
	}
}
