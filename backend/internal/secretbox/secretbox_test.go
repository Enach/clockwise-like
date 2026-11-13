package secretbox_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/internal/secretbox/secretboxtest"
)

func bind(table, column string, parts ...string) secretbox.Binding {
	b := secretbox.Binding{Table: table, Column: column}
	for i := 0; i+1 < len(parts); i += 2 {
		b.Row = append(b.Row, secretbox.Part{Name: parts[i], Value: parts[i+1]})
	}
	return b
}

var userA = bind("settings", "llm_api_key_openai", "user_id", "8d5e7a52-0f3c-4f4e-9a54-6f2d8f4c0a11")

// AC-3: the same value sealed twice gives two different stored values,
// neither equal to nor containing the value.
func TestPAC84_SealTwiceDiffersAndHidesValue(t *testing.T) {
	k, _ := secretboxtest.NewKeyring()
	const v = "sk-canary-0123456789abcdefghijkl"
	a, err := k.Seal(userA, v)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := k.Seal(userA, v)
	if a == b {
		t.Fatal("two seals of one value are equal")
	}
	for _, s := range []string{a, b} {
		if strings.Contains(s, v) || !strings.HasPrefix(s, "pdenc:1:1:") {
			t.Fatalf("stored value %q leaks the value or has the wrong envelope", s)
		}
		got, err := k.Open(userA, s)
		if err != nil || got != v {
			t.Fatalf("open = %q, %v", got, err)
		}
	}
	if empty, _ := k.Seal(userA, ""); empty != "" {
		t.Fatalf("empty sealed to %q", empty)
	}
}

// AC-5: a value copied to another user, column, calendar or org fails.
func TestPAC84_ValueBoundToItsPlace(t *testing.T) {
	k, _ := secretboxtest.NewKeyring()
	cases := []struct{ from, to secretbox.Binding }{
		{userA, bind("settings", "llm_api_key_openai", "user_id", "1d5e7a52-0f3c-4f4e-9a54-6f2d8f4c0a11")},
		{userA, bind("settings", "llm_api_key_anthropic", "user_id", "8d5e7a52-0f3c-4f4e-9a54-6f2d8f4c0a11")},
		{bind("oauth_tokens", "access_token", "user_id", "u", "calendar_id", "primary"),
			bind("oauth_tokens", "refresh_token", "user_id", "u", "calendar_id", "primary")},
		{bind("oauth_tokens", "access_token", "user_id", "u", "calendar_id", "primary"),
			bind("oauth_tokens", "access_token", "user_id", "u", "calendar_id", "work")},
		{bind("org_ai_configurations", "api_key", "org_id", "o1"), bind("org_ai_configurations", "api_key", "org_id", "o2")},
		{bind("sso_providers", "oidc_client_secret", "id", "a"), bind("sso_providers", "oidc_client_secret", "id", "b")},
	}
	for i, c := range cases {
		s, err := k.Seal(c.from, "canary-value-abcdefghijklmnop")
		if err != nil {
			t.Fatal(err)
		}
		got, err := k.Open(c.to, s)
		if !errors.Is(err, secretbox.ErrDecrypt) || got != "" {
			t.Fatalf("case %d: open elsewhere = %q, %v; want ErrDecrypt", i, got, err)
		}
		de, _ := secretbox.AsDecryptError(err)
		if de.Reason != secretbox.ReasonAuth || de.DEK != 1 || de.KEK != "test" {
			t.Fatalf("case %d: decrypt error %+v", i, de)
		}
	}
}

// AC-6: any single byte of the stored text changed fails, never returns
// altered plaintext (prefix, version, data key id and body).
func TestPAC84_AnyByteChangedFails(t *testing.T) {
	k, _ := secretboxtest.NewKeyring()
	const v = "canary-value-abcdefghijklmnop"
	s, _ := k.Seal(userA, v)
	for i := 0; i < len(s); i++ {
		for _, flip := range []byte{0x01, 0x20} {
			b := []byte(s)
			b[i] ^= flip
			got, err := k.Open(userA, string(b))
			if err == nil {
				t.Fatalf("byte %d ^%#x: opened to %q", i, flip, got)
			}
			if !errors.Is(err, secretbox.ErrDecrypt) || got != "" {
				t.Fatalf("byte %d ^%#x: got %q, %v", i, flip, got, err)
			}
		}
	}
}

// D-3 / contract 3.1: every value of the e2e seed and one sample of each
// plain form is classified plain (never mistaken for ciphertext).
func TestPAC84_PlainFormsAndSeedClassifyPlain(t *testing.T) {
	samples := []string{
		"ya29.a0AfH6SMB", "1//0gExample", "EwBwA8l6BAAU", "eyJ0eXAiOiJKV1Qi", "xoxp-1-2-3", "xoxb-1-2",
		"secret_abc", "ntn_abc", `{"access_token":"a"}`, "https://example.com/cal.ics", "webcal://example.com/a.ics",
		"-----BEGIN CERTIFICATE-----", "sk-abc", "sk-ant-abc", "AKIAABCDEF", "0123456789abcdef0123456789abcdef",
	}
	raw, err := os.ReadFile("../../../e2e/seed/seed.sql")
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	for _, m := range regexp.MustCompile(`'((?:[^']|'')*)'`).FindAllStringSubmatch(string(raw), -1) {
		samples = append(samples, strings.ReplaceAll(m[1], "''", "'"))
	}
	for _, s := range samples {
		if s == "" {
			continue
		}
		if got := secretbox.Classify(s); got != secretbox.KindPlain {
			t.Fatalf("%q classified %v, want plain", s, got)
		}
	}
	if secretbox.Classify("") != secretbox.KindEmpty || secretbox.Classify("pdenc:2:1:abc") != secretbox.KindSealed {
		t.Fatal("empty or sealed misclassified")
	}
	if len(samples) < 30 {
		t.Fatalf("only %d samples: the seed was not read", len(samples))
	}
}

func TestPAC84_EnvelopeErrors(t *testing.T) {
	k, _ := secretboxtest.NewKeyring()
	reason := func(s string) secretbox.Reason {
		_, err := k.Open(userA, s)
		de, ok := secretbox.AsDecryptError(err)
		if !ok {
			t.Fatalf("%q: %v is not a DecryptError", s, err)
		}
		return de.Reason
	}
	body := base64.RawURLEncoding.EncodeToString(make([]byte, 40))
	for s, want := range map[string]secretbox.Reason{
		"pdenc:":                     secretbox.ReasonUnsupportedVersion,
		"pdenc:2:1:" + body:          secretbox.ReasonUnsupportedVersion,
		"pdenc:1:1":                  secretbox.ReasonMalformed,
		"pdenc:1:01:" + body:         secretbox.ReasonMalformed,
		"pdenc:1:1:" + body + ":x":   secretbox.ReasonMalformed,
		"pdenc:1:1:AAAA":             secretbox.ReasonMalformed,
		"pdenc:1:9:" + body:          secretbox.ReasonUnknownDataKey,
		"pdenc:1:1:" + body:          secretbox.ReasonAuth,
		"pdenc:1:2147483648:" + body: secretbox.ReasonMalformed,
	} {
		if got := reason(s); got != want {
			t.Fatalf("%q: reason %s, want %s", s, got, want)
		}
	}
	if id, ok := secretbox.SealedDEK("pdenc:1:42:" + body); !ok || id != 42 {
		t.Fatalf("SealedDEK = %d, %v", id, ok)
	}
	// Without a keyring: empty and plain still work, sealed fails no_keyring.
	prev := secretbox.Current()
	secretbox.Install(nil)
	defer secretbox.Install(prev)
	if v, err := secretbox.Open(userA, "plain"); err != nil || v != "plain" {
		t.Fatal("plain open without keyring failed")
	}
	if _, err := secretbox.Open(userA, "pdenc:1:1:"+body); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("sealed without keyring: %v", err)
	}
	if _, err := secretbox.Seal(userA, "x"); !errors.Is(err, secretbox.ErrNoKeyring) {
		t.Fatalf("seal without keyring: %v", err)
	}
	if v, err := secretbox.Seal(userA, ""); err != nil || v != "" {
		t.Fatal("empty seal without keyring must stay empty")
	}
}

// Contract 3.2: length-prefixed fields make the encoding injective.
func TestPAC84_AADInjective(t *testing.T) {
	a := bind("ab", "c", "id", "1").AAD()
	b := bind("a", "bc", "id", "1").AAD()
	if bytes.Equal(a, b) || a == nil {
		t.Fatal("AAD not injective")
	}
	if !bytes.HasPrefix(a, []byte("paceday/secret\x00\x01\x00\x02ab")) {
		t.Fatalf("AAD layout %x", a)
	}
	long := bind("t", strings.Repeat("x", 65536), "id", "1")
	if long.AAD() != nil {
		t.Fatal("over-long field encoded")
	}
	k, _ := secretboxtest.NewKeyring()
	if _, err := k.Seal(long, "v"); err == nil {
		t.Fatal("seal accepted an over-long binding")
	}
	if got := bind("oauth_tokens", "access_token", "user_id", "u", "calendar_id", "c").RowString(); got != "user_id=u,calendar_id=c" {
		t.Fatal(got)
	}
}

// AC-9: malformed keys are refused naming the variable and the problem,
// never echoing input bytes other than a valid key id.
func TestPAC84_ParseKeyConfig(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(secretboxtest.RandomKey())
	good2 := base64.StdEncoding.EncodeToString(secretboxtest.RandomKey())
	short := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	cfg, err := secretbox.ParseKeyConfig(" k1:"+good+" ", "k0:"+good2+" ,  ")
	if err == nil {
		t.Fatal("a trailing empty previous entry must be malformed")
	}
	cfg, err = secretbox.ParseKeyConfig(" k1:"+good+" ", " k0:"+good2+" ")
	if err != nil || cfg.Active.ID != "k1" || len(cfg.Previous) != 1 || cfg.Previous[0].ID != "k0" {
		t.Fatalf("parse: %+v %v", cfg, err)
	}
	if _, ok := cfg.Lookup("k0"); !ok {
		t.Fatal("lookup previous")
	}
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = fmt.Sprintf("p%d:%s", i, base64.StdEncoding.EncodeToString(secretboxtest.RandomKey()))
	}
	for _, c := range []struct {
		active, previous, want string
		sentinel               error
	}{
		{"", "", "PACEDAY_ENCRYPTION_KEY is not set", secretbox.ErrKeyMissing},
		{"   ", "", "PACEDAY_ENCRYPTION_KEY is not set", secretbox.ErrKeyMissing},
		{good, "", "expected <key id>:<base64 of 32 bytes>", secretbox.ErrKeyMalformed},
		{"-bad:" + good, "", "invalid key id", secretbox.ErrKeyMalformed},
		{"k1:" + strings.TrimRight(good, "="), "", "invalid base64", secretbox.ErrKeyMalformed},
		{"k1:" + base64.RawURLEncoding.EncodeToString(secretboxtest.RandomKey()) + "_", "", "invalid base64", secretbox.ErrKeyMalformed},
		{"k1:" + short, "", "decodes to 16 bytes, need 32", secretbox.ErrKeyMalformed},
		{"k1:" + good, "k1:" + good2, `key id "k1" appears twice`, secretbox.ErrKeyMalformed},
		{"k1:" + good, "k2:" + good, `keys "k1" and "k2" are the same key`, secretbox.ErrKeyMalformed},
		{"k1:" + good, strings.Join(nine, ","), "more than 8 previous keys", secretbox.ErrKeyMalformed},
		{"k1:" + good, "k2:" + short, "entry 1 of PACEDAY_ENCRYPTION_KEY_PREVIOUS is malformed", secretbox.ErrKeyMalformed},
	} {
		_, err := secretbox.ParseKeyConfig(c.active, c.previous)
		if !errors.Is(err, c.sentinel) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%q/%q: %v, want %q", c.active, c.previous, err, c.want)
		}
		for _, secret := range []string{good, good2, short, strings.TrimRight(good, "=")} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error echoes key material: %v", err)
			}
		}
	}
}

// D-11: a KEK never formats its bytes.
func TestPAC84_KEKNeverFormatsKey(t *testing.T) {
	raw := secretboxtest.RandomKey()
	k, err := secretbox.NewKEK("idx", raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := secretbox.KeyConfig{Active: k, Previous: []secretbox.KEK{k}}
	out := fmt.Sprintf("%v %+v %#v %s %q %x %v", k, k, k, k, k, k, cfg)
	for _, enc := range []string{string(raw), hex.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw), fmt.Sprint(raw)} {
		if strings.Contains(out, enc) {
			t.Fatalf("formatted KEK leaks key bytes: %s", out)
		}
	}
	if !strings.Contains(out, "idx") {
		t.Fatal("id not printed")
	}
}

// AC-9, AC-10, AC-26: wrong key, unknown wrapping key, re-wrap, unwrap count.
func TestPAC84_KeyringFromWrappedKeys(t *testing.T) {
	k1, _ := secretbox.NewKEK("k1", secretboxtest.RandomKey())
	k2, _ := secretbox.NewKEK("k2", secretboxtest.RandomKey())
	k1bad, _ := secretbox.NewKEK("k1", secretboxtest.RandomKey())
	w1, _ := secretbox.NewDEK(1, k1)
	w2, _ := secretbox.NewDEK(2, k1)
	w1.State = "retired"
	only1 := secretbox.KeyConfig{Active: k1}
	ring, err := secretbox.NewKeyring(only1, []secretbox.WrappedDEK{w1, w2})
	if err != nil || ring.ActiveDEK() != 2 || ring.Unwraps() != 2 {
		t.Fatalf("keyring: %v, active %d, unwraps %d", err, ring.ActiveDEK(), ring.Unwraps())
	}
	if _, err := secretbox.NewKeyring(secretbox.KeyConfig{Active: k1bad}, []secretbox.WrappedDEK{w2}); !errors.Is(err, secretbox.ErrWrongKey) ||
		!strings.Contains(err.Error(), `key "k1" (PACEDAY_ENCRYPTION_KEY) does not open data key 2 (wrapped by key "k1")`) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := secretbox.NewKeyring(secretbox.KeyConfig{Active: k2}, []secretbox.WrappedDEK{w2}); !errors.Is(err, secretbox.ErrUnknownKEK) ||
		!strings.Contains(err.Error(), `data key 2 is wrapped by key "k1", which is neither PACEDAY_ENCRYPTION_KEY ("k2") nor in PACEDAY_ENCRYPTION_KEY_PREVIOUS ("none")`) {
		t.Fatalf("unknown kek: %v", err)
	}
	if _, err := secretbox.NewKeyring(only1, []secretbox.WrappedDEK{w1}); !errors.Is(err, secretbox.ErrNoActiveDEK) {
		t.Fatalf("no active: %v", err)
	}
	// Re-wrap under K2 with K1 as previous; the re-wrapped key opens what
	// the old one sealed (D-6, AC-11).
	s, _ := ring.Seal(userA, "canary-rotation-value-000000")
	k1prev := k1
	k1prev.Var = secretbox.EnvKeyPrevious
	rot := secretbox.KeyConfig{Active: k2, Previous: []secretbox.KEK{k1prev}}
	nw, err := secretbox.Rewrap(w2, rot)
	if err != nil || nw.KEKID != "k2" || bytes.Equal(nw.Wrapped, w2.Wrapped) {
		t.Fatalf("rewrap: %v %+v", err, nw.KEKID)
	}
	only2, err := secretbox.NewKeyring(secretbox.KeyConfig{Active: k2}, []secretbox.WrappedDEK{nw})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := only2.Open(userA, s); err != nil || v != "canary-rotation-value-000000" {
		t.Fatalf("open after rewrap: %v", err)
	}
	// A wrapped key relabelled with another kek_id or id fails to unwrap.
	relabel := nw
	relabel.ID = 7
	if _, err := secretbox.NewKeyring(secretbox.KeyConfig{Active: k2}, []secretbox.WrappedDEK{relabel}); !errors.Is(err, secretbox.ErrWrongKey) {
		t.Fatalf("relabelled id: %v", err)
	}
	// With unwraps only new ids; counts are shared.
	w3, _ := secretbox.NewDEK(3, k2)
	nw.State = "retired"
	ring2, err := only2.With(secretbox.KeyConfig{Active: k2}, []secretbox.WrappedDEK{nw, w3})
	if err != nil || ring2.ActiveDEK() != 3 || ring2.Unwraps() != 2 || !ring2.Has(2) {
		t.Fatalf("With: %v active %d unwraps %d", err, ring2.ActiveDEK(), ring2.Unwraps())
	}
	if _, _, ok := secretbox.OpensAny(secretbox.KeyConfig{Active: k1}, []secretbox.WrappedDEK{w3}); ok {
		t.Fatal("OpensAny with an unrelated key")
	}
	if _, _, ok := secretbox.OpensAny(secretbox.KeyConfig{Active: k2}, []secretbox.WrappedDEK{w3}); !ok {
		t.Fatal("OpensAny missed the key")
	}
	// Seal counts per data key, taken once.
	_, _ = ring2.Seal(userA, "a")
	_, _ = ring2.SealWith(2, userA, "b")
	c := ring2.TakeSealCounts()
	if c[3] != 1 || c[2] != 1 || len(ring2.TakeSealCounts()) != 0 {
		t.Fatalf("seal counts %v", c)
	}
	ring2.RestoreSealCounts(c)
	if ring2.TakeSealCounts()[3] != 1 {
		t.Fatal("restore")
	}
}

// Contract 4.3: the seal gate refuses package-level Seal; Open still works.
func TestPAC84_SealGateAndReporter(t *testing.T) {
	k := secretboxtest.Install(t)
	s, err := secretbox.Seal(userA, "value-abcdefghijklmnopqrstu")
	if err != nil {
		t.Fatal(err)
	}
	secretbox.SetSealGate(func() error { return secretbox.ErrSealPaused })
	defer secretbox.SetSealGate(nil)
	if _, err := secretbox.Seal(userA, "x"); !errors.Is(err, secretbox.ErrSealPaused) {
		t.Fatalf("gate: %v", err)
	}
	if v, err := secretbox.Open(userA, s); err != nil || v == "" {
		t.Fatal("open refused under a paused lease")
	}
	_ = k
	var got string
	secretbox.SetReporter(func(l string) { got = l })
	defer secretbox.SetReporter(nil)
	_, err = secretbox.Open(bind("settings", "webcal_url", "user_id", "u2"), s)
	secretbox.ReportFailure(err, "u2")
	want := "encryption: cannot decrypt settings.webcal_url row=user_id=u2 dek=1 kek=test user=u2 reason=authentication_failed"
	if got != want || strings.Contains(got, s) {
		t.Fatalf("reported %q, want %q", got, want)
	}
	secretbox.ReportFailure(errors.New("other"), "")
}

// AC-26: opening all nine secrets of a settings row takes under 1 ms.
func BenchmarkOpenSettingsRow(b *testing.B) {
	k, _ := secretboxtest.NewKeyring()
	cols := []string{"microsoft_tokens", "zoom_tokens", "webcal_url", "llm_api_key", "llm_api_key_openai",
		"llm_api_key_anthropic", "llm_api_key_azure_openai", "llm_api_key_bedrock", "llm_api_key_vertex"}
	stored := make([]string, len(cols))
	binds := make([]secretbox.Binding, len(cols))
	for i, c := range cols {
		binds[i] = bind("settings", c, "user_id", "8d5e7a52-0f3c-4f4e-9a54-6f2d8f4c0a11")
		stored[i], _ = k.Seal(binds[i], strings.Repeat("v", 400))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range cols {
			if _, err := k.Open(binds[j], stored[j]); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func TestPAC84_OpenSettingsRowUnderOneMillisecond(t *testing.T) {
	if testing.Short() {
		t.Skip("benchmark-backed check; runs in the full suite (PAC-84 AC-26)")
	}
	r := testing.Benchmark(BenchmarkOpenSettingsRow)
	if r.N == 0 || r.NsPerOp() >= 1_000_000 {
		t.Fatalf("BenchmarkOpenSettingsRow: %d ns/op (N=%d), want < 1 ms", r.NsPerOp(), r.N)
	}
	t.Logf("BenchmarkOpenSettingsRow: %d ns/op", r.NsPerOp())
}
