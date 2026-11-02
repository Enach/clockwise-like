package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/internal/secretbox/secretboxtest"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// PAC-84 storage tests: conversion, rotation, decrypt-all, lost-key reset,
// downgrade, status, key load, refresher, lease, locks, per-field failure.

func canary(t *testing.T, label string) string {
	t.Helper()
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	_ = label
	return "canary-" + hex.EncodeToString(b)
}

// encKeys builds a fresh key config with one random key named id.
func encKeys(t *testing.T, id string) secretbox.KeyConfig {
	t.Helper()
	raw := secretboxtest.RandomKey()
	k, err := secretbox.NewKEK(id, raw)
	if err != nil {
		t.Fatal(err)
	}
	testKeyBytes.Store(id, raw)
	return secretbox.KeyConfig{Active: k}
}

// installLoaded loads the keyring for db (fresh data key) and installs it
// for the test.
func installLoaded(t *testing.T, db *sql.DB, cfg secretbox.KeyConfig) *KeyLoad {
	t.Helper()
	load, err := PrepareKeyring(context.Background(), db, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	prev := secretbox.Current()
	secretbox.Install(load.Keyring)
	t.Cleanup(func() { secretbox.Install(prev) })
	return load
}

// legacy is a database in the pre-upgrade state: a plain canary in every
// one of the 18 inventory columns, written by SQL.
type legacy struct {
	user, user2, org uuid.UUID
	ssoID            uuid.UUID
	calID            int64
	values           map[string]string // table.column -> canary
	canaries         []string          // the canaries alone (leak checks)
}

func seedLegacy(t *testing.T, db *sql.DB) *legacy {
	t.Helper()
	l := &legacy{values: map[string]string{}}
	for _, c := range Inventory {
		l.values[c.Name()] = canary(t, strings.ReplaceAll(c.Name(), ".", "_"))
		l.canaries = append(l.canaries, l.values[c.Name()])
	}
	v := l.values
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.QueryRow(`INSERT INTO organizations (name, domain) VALUES ('Enc', $1) RETURNING id`, "enc-"+uuid.NewString()[:8]+".example").Scan(&l.org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO users (email, name, org_id) VALUES ($1, 'U', $2) RETURNING id`, uuid.NewString()+"@enc.example", l.org).Scan(&l.user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO users (email, name) VALUES ($1, 'U2') RETURNING id`, uuid.NewString()+"@enc.example").Scan(&l.user2); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO oauth_tokens (user_id, calendar_id, access_token, refresh_token, expiry, updated_at) VALUES ($1, 'primary', $2, $3, now() + interval '1 hour', now() - interval '3 days')`,
		l.user, v["oauth_tokens.access_token"], v["oauth_tokens.refresh_token"])
	must(`INSERT INTO settings (user_id, microsoft_tokens, zoom_tokens, webcal_url, llm_api_key, llm_api_key_openai, llm_api_key_anthropic,
		llm_api_key_azure_openai, llm_api_key_bedrock, llm_api_key_vertex, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now() - interval '2 days')`,
		l.user, `{"access_token":"`+v["settings.microsoft_tokens"]+`"}`, `{"access_token":"`+v["settings.zoom_tokens"]+`","refresh_token":"r"}`,
		v["settings.webcal_url"], v["settings.llm_api_key"], v["settings.llm_api_key_openai"], v["settings.llm_api_key_anthropic"],
		v["settings.llm_api_key_azure_openai"], v["settings.llm_api_key_bedrock"], v["settings.llm_api_key_vertex"])
	l.values["settings.microsoft_tokens"] = `{"access_token":"` + v["settings.microsoft_tokens"] + `"}`
	l.values["settings.zoom_tokens"] = `{"access_token":"` + v["settings.zoom_tokens"] + `","refresh_token":"r"}`
	if err := db.QueryRow(`INSERT INTO personal_calendars (user_id, provider, name, url, credentials_json, enabled) VALUES ($1, 'webcal', 'P', $2, $3, true) RETURNING id`,
		l.user, v["personal_calendars.url"], v["personal_calendars.credentials_json"]).Scan(&l.calID); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO workspace_connections (user_id, provider, access_token, bot_token, workspace_id, workspace_name, connected_at) VALUES ($1, 'slack', $2, $3, 'W', 'WS', now() - interval '4 days')`,
		l.user, v["workspace_connections.access_token"], v["workspace_connections.bot_token"])
	must(`INSERT INTO org_ai_configurations (org_id, provider, api_key, configuration_version, updated_at) VALUES ($1, 'openai', $2, 7, now() - interval '5 days')`,
		l.org, v["org_ai_configurations.api_key"])
	if err := db.QueryRow(`INSERT INTO sso_providers (domain, provider_name, provider_type, enabled, oidc_issuer, oidc_client_id, oidc_client_secret, saml_cert, updated_at)
		VALUES ($1, 'Okta', 'oidc', true, 'https://idp.example', 'cid', $2, $3, now() - interval '6 days') RETURNING id`,
		"sso-"+uuid.NewString()[:8]+".example", v["sso_providers.oidc_client_secret"], v["sso_providers.saml_cert"]).Scan(&l.ssoID); err != nil {
		t.Fatal(err)
	}
	return l
}

// rawValue reads one inventory value of l raw.
func (l *legacy) raw(t *testing.T, db *sql.DB, col SecretColumn) string {
	t.Helper()
	var where string
	var args []any
	switch col.Table {
	case "oauth_tokens":
		where, args = "user_id = $1 AND calendar_id = 'primary'", []any{l.user}
	case "settings":
		where, args = "user_id = $1", []any{l.user}
	case "workspace_connections":
		where, args = "user_id = $1 AND provider = 'slack'", []any{l.user}
	case "org_ai_configurations":
		where, args = "org_id = $1", []any{l.org}
	case "sso_providers":
		where, args = "id = $1", []any{l.ssoID}
	case "personal_calendars":
		where, args = "id = $1", []any{l.calID}
	}
	var v sql.NullString
	if err := db.QueryRow(fmt.Sprintf(`SELECT %s FROM %s WHERE %s`, col.Column, col.Table, where), args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v.String
}

func (l *legacy) binding(col SecretColumn) secretbox.Binding {
	switch col.Table {
	case "oauth_tokens":
		return col.Binding(l.user.String(), "primary")
	case "settings":
		return col.Binding(l.user.String())
	case "workspace_connections":
		return col.Binding(l.user.String(), "slack")
	case "org_ai_configurations":
		return col.Binding(l.org.String())
	case "sso_providers":
		return col.Binding(l.ssoID.String())
	}
	return col.Binding(fmt.Sprint(l.calID))
}

// otherColumns snapshots every non-secret column of l's rows.
func (l *legacy) otherColumns(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []struct {
		sql string
		arg any
	}{
		{`SELECT row_to_json(t)::jsonb - 'access_token' - 'refresh_token' FROM oauth_tokens t WHERE user_id = $1`, l.user},
		{`SELECT row_to_json(t)::jsonb - 'microsoft_tokens' - 'zoom_tokens' - 'webcal_url' - 'llm_api_key' - 'llm_api_key_openai' - 'llm_api_key_anthropic' - 'llm_api_key_azure_openai' - 'llm_api_key_bedrock' - 'llm_api_key_vertex' FROM settings t WHERE user_id = $1`, l.user},
		{`SELECT row_to_json(t)::jsonb - 'url' - 'credentials_json' FROM personal_calendars t WHERE id = $1`, l.calID},
		{`SELECT row_to_json(t)::jsonb - 'access_token' - 'bot_token' FROM workspace_connections t WHERE user_id = $1`, l.user},
		{`SELECT row_to_json(t)::jsonb - 'api_key' FROM org_ai_configurations t WHERE org_id = $1`, l.org},
		{`SELECT row_to_json(t)::jsonb - 'oidc_client_secret' - 'saml_cert' FROM sso_providers t WHERE id = $1`, l.ssoID},
	} {
		var s string
		if err := db.QueryRow(q.sql, q.arg).Scan(&s); err != nil {
			t.Fatal(err)
		}
		b.WriteString(s + "\n")
	}
	return b.String()
}

// captureDB opens dsn with a pgx tracer recording every statement and
// argument the client sends (AC-19c, at the driver).
type captured struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captured) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(&c.buf, "%s %v\n", d.SQL, d.Args)
	return ctx
}
func (c *captured) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (c *captured) String() string                                                  { c.mu.Lock(); defer c.mu.Unlock(); return c.buf.String() }

func openEncDB(t *testing.T) (*sql.DB, string, *captured) {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Skipf("PostgreSQL unavailable (PAC-84 storage tests need it; scripts/test-backend.sh provides it): %v", err)
	}
	db, err := Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_ = cleanup()
	})
	return db, dsn, nil
}

// captureLog collects the standard logger's output for the test.
func encCaptureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	var mu sync.Mutex
	w := writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return buf.Write(p) })
	prev := log.Writer()
	log.SetOutput(w)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// encodings of s that must never appear (AC-1): as-is, base64, hex, every
// 12-character substring.
func leaks(hay, s string) bool {
	if strings.Contains(hay, s) || strings.Contains(hay, base64.StdEncoding.EncodeToString([]byte(s))) ||
		strings.Contains(hay, hex.EncodeToString([]byte(s))) {
		return true
	}
	for i := 0; i+12 <= len(s); i++ {
		if strings.Contains(hay, s[i:i+12]) {
			return true
		}
	}
	return false
}

// dumpData is the data section of a plain pg_dump: COPY ... TO STDOUT of
// every table of the public schema.
func dumpData(t *testing.T, dsn string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	var out bytes.Buffer
	for _, tb := range tables {
		fmt.Fprintf(&out, "COPY public.%s FROM stdin;\n", tb)
		if _, err := conn.PgConn().CopyTo(ctx, &out, fmt.Sprintf(`COPY public.%q TO STDOUT`, tb)); err != nil {
			t.Fatalf("copy %s: %v", tb, err)
		}
	}
	return out.String()
}

// AC-1 (data half), AC-12, AC-14 (second run), AC-15, AC-19, AC-19c, AC-20:
// a legacy database is converted by one pass: nothing plain remains, every
// value reads back through its read path, no other column changes, no
// statement carries a canary, and a dump holds no canary and no key.
func TestPAC84_ConversionEndToEnd(t *testing.T) {
	db, dsn, _ := openEncDB(t)
	logs := encCaptureLog(t)
	ctx := context.Background()
	cfg := encKeys(t, "conv-key")
	l := seedLegacy(t, db)
	before := l.otherColumns(t, db)

	// Old plain values keep working before the conversion (D-7).
	load := installLoaded(t, db, cfg)
	s, err := GetOrCreateSettingsByUser(db, l.user)
	if err != nil || s.LLMAPIKeys.OpenAI != l.values["settings.llm_api_key_openai"] {
		t.Fatalf("plain read before conversion: %v", err)
	}
	st, err := ReadEncryptionStatus(ctx, db)
	if err != nil || st.PlainTotal() < int64(len(Inventory)) {
		t.Fatalf("status before: %v plain=%d", err, st.PlainTotal())
	}

	// The pass runs over a traced pool: every statement and argument sent.
	tcfg, _ := pgx.ParseConfig(dsn)
	trace := &captured{}
	tcfg.Tracer = trace
	traced := stdlib.OpenDB(*tcfg)
	defer traced.Close()
	res, err := RunPass(ctx, traced, PassOptions{Kind: PassConvert, Batch: 2, Keyring: load.Keyring})
	if err != nil || res.Partial || len(res.Failures) > 0 {
		t.Fatalf("convert: %v %+v", err, res)
	}
	if got := res.Total("encrypted"); got != int64(len(Inventory)) {
		t.Fatalf("encrypted %d, want %d", got, len(Inventory))
	}
	for _, v := range l.canaries {
		if leaks(trace.String(), v) {
			t.Fatalf("a statement sent during the conversion carries a canary (AC-19c)")
		}
	}

	// Every column holds a sealed value that opens to the canary, bound to
	// its row; nothing else of these rows changed (AC-12).
	for _, col := range Inventory {
		raw := l.raw(t, db, col)
		if secretbox.Classify(raw) != secretbox.KindSealed {
			t.Fatalf("%s still %q", col.Name(), raw)
		}
		got, err := load.Keyring.Open(l.binding(col), raw)
		if err != nil || got != l.values[col.Name()] {
			t.Fatalf("%s: open %v", col.Name(), err)
		}
	}
	if after := l.otherColumns(t, db); after != before {
		t.Fatalf("conversion changed other columns:\n%s\n%s", before, after)
	}
	st, _ = ReadEncryptionStatus(ctx, db)
	if st.PlainTotal() != 0 || len(st.DataKeys) != 1 || st.DataKeys[0].Values != int64(len(Inventory)) {
		t.Fatalf("status after: plain %d keys %+v", st.PlainTotal(), st.DataKeys)
	}

	// Read paths return the canaries.
	s, _ = GetOrCreateSettingsByUser(db, l.user)
	if s.WebcalURL != l.values["settings.webcal_url"] || s.LLMUnboundAPIKey != l.values["settings.llm_api_key"] ||
		s.LLMAPIKeys.Vertex != l.values["settings.llm_api_key_vertex"] || s.MicrosoftTokens != l.values["settings.microsoft_tokens"] {
		t.Fatal("settings read path")
	}
	wc, _ := GetWorkspaceConnection(db, l.user, "slack")
	if wc.TokenErr != nil || wc.BotToken != l.values["workspace_connections.bot_token"] {
		t.Fatal("workspace read path")
	}
	pc, _ := GetPersonalCalendar(db, l.calID)
	if pc.SecretErr != nil || pc.URL != l.values["personal_calendars.url"] || pc.CredentialsJSON != l.values["personal_calendars.credentials_json"] {
		t.Fatal("personal calendar read path")
	}
	oc, _ := GetOrgAIConfig(ctx, db, l.org)
	if oc.APIKey != l.values["org_ai_configurations.api_key"] {
		t.Fatal("org AI read path")
	}
	sp, _ := GetSSOProviderByDomainAnyState(db, mustDomain(t, db, l.ssoID))
	if sp.OIDCClientSecret != l.values["sso_providers.oidc_client_secret"] || sp.SAMLCert != l.values["sso_providers.saml_cert"] {
		t.Fatal("sso read path")
	}
	zc, err := LoadZoomCredential(db, l.user)
	if err != nil || !strings.HasPrefix(zc.AccessToken, "canary-") {
		t.Fatalf("zoom read path: %v", err)
	}

	// AC-14: a second full run changes nothing.
	res, err = RunPass(ctx, db, PassOptions{Kind: PassConvert, Keyring: load.Keyring})
	if err != nil || res.Total("encrypted") != 0 {
		t.Fatalf("second run: %v %+v", err, res.Counts)
	}

	// AC-15: a plain value written by SQL reads, counts plain, converts.
	if _, err := db.Exec(`UPDATE settings SET webcal_url = 'https://plain.example/x.ics' WHERE user_id = $1`, l.user); err != nil {
		t.Fatal(err)
	}
	s, _ = GetOrCreateSettingsByUser(db, l.user)
	st, _ = ReadEncryptionStatus(ctx, db)
	if s.WebcalURL != "https://plain.example/x.ics" || st.PlainTotal() != 1 {
		t.Fatalf("plain after encryption: %q plain=%d", s.WebcalURL, st.PlainTotal())
	}
	if res, _ := RunPass(ctx, db, PassOptions{Kind: PassConvert, Keyring: load.Keyring}); res.Total("encrypted") != 1 {
		t.Fatal("next run did not encrypt the plain value")
	}

	// AC-1 (data half): the dump holds no canary and no key.
	dump := dumpData(t, dsn)
	for _, v := range l.canaries {
		if leaks(dump, v) {
			t.Fatalf("dump contains a canary")
		}
	}
	if !strings.Contains(dump, "pdenc:1:") {
		t.Fatal("dump holds no sealed value: the dump test is not looking at the data")
	}
	assertNoKeyIn(t, dump, cfg)
	// AC-19: the log carries no canary, no key, no ciphertext.
	for _, v := range l.canaries {
		if leaks(logs.String(), v) {
			t.Fatal("log carries a canary")
		}
	}
	assertNoKeyIn(t, logs.String(), cfg)
	if strings.Contains(logs.String(), "pdenc:1:") {
		t.Fatal("log carries ciphertext")
	}
}

func mustDomain(t *testing.T, db *sql.DB, id uuid.UUID) string {
	t.Helper()
	var d string
	if err := db.QueryRow(`SELECT domain FROM sso_providers WHERE id = $1`, id).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func assertNoKeyIn(t *testing.T, hay string, cfg secretbox.KeyConfig) {
	t.Helper()
	for _, k := range cfg.All() {
		raw := keyBytes(t, k)
		for _, enc := range []string{string(raw), base64.StdEncoding.EncodeToString(raw), hex.EncodeToString(raw), base64.RawURLEncoding.EncodeToString(raw)} {
			if strings.Contains(hay, enc) {
				t.Fatalf("key %q found", k.ID)
			}
		}
	}
}

// testKeyBytes keeps the raw bytes of the keys encKeys made, by id.
var testKeyBytes sync.Map // id -> []byte

func keyBytes(t *testing.T, k secretbox.KEK) []byte {
	t.Helper()
	if v, ok := testKeyBytes.Load(k.ID); ok {
		return v.([]byte)
	}
	return nil
}

// AC-13: a value written by its owner between the conversion's read and
// its update is what stays.
func TestPAC84_ConversionNeverOverwritesANewerWrite(t *testing.T) {
	db, _, _ := openEncDB(t)
	ctx := context.Background()
	l := seedLegacy(t, db)
	load := installLoaded(t, db, encKeys(t, "cas"))
	const newer = "https://newer.example/cal.ics"
	defer func() { passHook = nil }()
	passHook = func(col SecretColumn, _ int) {
		if col.Name() != "settings.webcal_url" {
			return
		}
		passHook = nil
		s, err := GetOrCreateSettingsByUser(db, l.user)
		if err != nil {
			t.Error(err)
			return
		}
		s.WebcalURL = newer
		s.MarkSecretReplaced(SettingsColWebcalURL)
		if err := SaveSettingsForUser(db, l.user, s); err != nil {
			t.Error(err)
		}
	}
	if _, err := RunPass(ctx, db, PassOptions{Kind: PassConvert, Keyring: load.Keyring}); err != nil {
		t.Fatal(err)
	}
	s, _ := GetOrCreateSettingsByUser(db, l.user)
	if s.WebcalURL != newer {
		t.Fatalf("the conversion overwrote the user's write: %q", s.WebcalURL)
	}
}

// AC-14: a conversion stopped part-way completes on the next run; nothing
// is encrypted twice.
func TestPAC84_ConversionResumes(t *testing.T) {
	db, _, _ := openEncDB(t)
	l := seedLegacy(t, db)
	load := installLoaded(t, db, encKeys(t, "resume"))
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	defer func() { passHook = nil }()
	passHook = func(SecretColumn, int) {
		if n++; n == 4 {
			cancel()
		}
	}
	_, err := RunPass(ctx, db, PassOptions{Kind: PassConvert, Batch: 1, Keyring: load.Keyring})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run: %v", err)
	}
	passHook = nil
	res, err := RunPass(context.Background(), db, PassOptions{Kind: PassConvert, Keyring: load.Keyring})
	if err != nil || res.Total("encrypted") == 0 || res.Total("encrypted") >= int64(len(Inventory)) {
		t.Fatalf("resumed run: %v %d", err, res.Total("encrypted"))
	}
	for _, col := range Inventory {
		got, err := load.Keyring.Open(l.binding(col), l.raw(t, db, col))
		if err != nil || got != l.values[col.Name()] {
			t.Fatalf("%s after resume: %v", col.Name(), err)
		}
	}
	if res, _ := RunPass(context.Background(), db, PassOptions{Kind: PassConvert, Keyring: load.Keyring}); res.Total("encrypted") != 0 {
		t.Fatal("third run changed rows")
	}
}

// AC-11b, AC-19c: rotate-data-key retires N, re-encrypts its values without
// sending plaintext, and a second run changes nothing.
func TestPAC84_RotateDataKey(t *testing.T) {
	db, dsn, _ := openEncDB(t)
	ctx := context.Background()
	l := seedLegacy(t, db)
	cfg := encKeys(t, "rot")
	load := installLoaded(t, db, cfg)
	if _, err := RunPass(ctx, db, PassOptions{Kind: PassConvert, Keyring: load.Keyring}); err != nil {
		t.Fatal(err)
	}
	old := load.Active
	newID, err := RotateStep1(ctx, db, cfg, old, false)
	if err != nil || newID == 0 || newID == old {
		t.Fatalf("step 1: %d %v", newID, err)
	}
	if again, err := RotateStep1(ctx, db, cfg, old, false); err != nil || again != 0 {
		t.Fatalf("step 1 twice: %d %v", again, err)
	}
	deks, _ := LoadDataKeys(ctx, db)
	k, err := secretbox.NewKeyring(cfg, deks)
	if err != nil || k.ActiveDEK() != newID {
		t.Fatal(err)
	}
	tcfg, _ := pgx.ParseConfig(dsn)
	trace := &captured{}
	tcfg.Tracer = trace
	traced := stdlib.OpenDB(*tcfg)
	defer traced.Close()
	res, err := RunPass(ctx, traced, PassOptions{Kind: PassRotate, Keyring: k, Retire: old, Batch: 3})
	if err != nil || res.Total("reencrypted") != int64(len(Inventory)) {
		t.Fatalf("rotate pass: %v %+v", err, res.Counts)
	}
	for _, v := range l.canaries {
		if leaks(trace.String(), v) {
			t.Fatal("rotation sent a plaintext (AC-19c)")
		}
	}
	if n, _ := CountValuesUnder(ctx, db, old); n != 0 {
		t.Fatalf("%d values still under %d", n, old)
	}
	for _, col := range Inventory {
		raw := l.raw(t, db, col)
		if id, _ := secretbox.SealedDEK(raw); id != newID {
			t.Fatalf("%s under %d", col.Name(), id)
		}
	}
	if res, _ := RunPass(ctx, db, PassOptions{Kind: PassRotate, Keyring: k, Retire: old}); res.Total("reencrypted") != 0 {
		t.Fatal("second rotation changed rows")
	}
}

// AC-22, AC-21 (decrypt-all half): the down step refuses while anything is
// encrypted (in Go, leaving the version clean; and by the DO block);
// decrypt-all writes every plaintext back and removes the data keys; then
// downgrade-schema succeeds and the inventory holds the plaintexts.
func TestPAC84_DecryptAllAndDowngrade(t *testing.T) {
	db, _, _ := openEncDB(t)
	ctx := context.Background()
	l := seedLegacy(t, db)
	cfg := encKeys(t, "down")
	load := installLoaded(t, db, cfg)
	if _, err := RunPass(ctx, db, PassOptions{Kind: PassConvert, Keyring: load.Keyring}); err != nil {
		t.Fatal(err)
	}
	want, _ := EncryptionMigrationVersion()
	if _, _, err := DowngradeSchema(ctx, db); !errors.Is(err, ErrDowngradeRefused) {
		t.Fatalf("downgrade with encrypted values: %v", err)
	}
	var v uint
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&v, &dirty); err != nil || v != want || dirty {
		t.Fatalf("schema_migrations after refusal: %d %v %v", v, dirty, err)
	}
	// The raw down body refuses too (the DO block backstop).
	raw, err := migrationFiles.ReadFile(fmt.Sprintf("migrations/%03d_encryption_at_rest.down.sql", want))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(raw)); err == nil || !strings.Contains(err.Error(), "paceday-operator encryption decrypt-all") {
		t.Fatalf("DO block: %v", err)
	}
	var tables int
	_ = db.QueryRow(`SELECT count(*) FROM pg_tables WHERE tablename IN ('encryption_data_keys', 'encryption_backend_heartbeats')`).Scan(&tables)
	if tables != 2 {
		t.Fatal("the refused down body changed the schema")
	}

	res, err := RunPass(ctx, db, PassOptions{Kind: PassDecrypt, Keyring: load.Keyring})
	if err != nil || res.Total("decrypted") != int64(len(Inventory)) || len(res.Failures) != 0 {
		t.Fatalf("decrypt-all: %v %+v", err, res)
	}
	removed, ok, err := DeleteAllDataKeysIfUnused(ctx, db)
	if err != nil || !ok || len(removed) != 1 {
		t.Fatalf("delete keys: %v %v %v", removed, ok, err)
	}
	from, to, err := DowngradeSchema(ctx, db)
	if err != nil || from != want || to >= want {
		t.Fatalf("downgrade: %d->%d %v", from, to, err)
	}
	for _, col := range Inventory {
		if got := l.raw(t, db, col); got != l.values[col.Name()] {
			t.Fatalf("%s after downgrade is not its plaintext", col.Name())
		}
	}
	var audit, encTables int
	_ = db.QueryRow(`SELECT count(*) FROM pg_tables WHERE tablename = 'operator_audit'`).Scan(&audit)
	_ = db.QueryRow(`SELECT count(*) FROM pg_tables WHERE tablename LIKE 'encryption_%'`).Scan(&encTables)
	if audit != 1 || encTables != 0 {
		t.Fatalf("after down: operator_audit %d, encryption tables %d", audit, encTables)
	}
	if err := WriteEncryptionAudit(ctx, db, AuditEncryptionDowngrade, "op", "why", AuditDetails{Outcome: "completed"}); err != nil {
		t.Fatalf("widened CHECK not kept: %v", err)
	}
}

// AC-23: reset-lost-key gives every sealed value its owner's-removal end
// state; plain values and everything else are untouched.
func TestPAC84_ResetLostKey(t *testing.T) {
	db, _, _ := openEncDB(t)
	ctx := context.Background()
	l := seedLegacy(t, db)
	lost := encKeys(t, "lost")
	load := installLoaded(t, db, lost)
	if _, err := RunPass(ctx, db, PassOptions{Kind: PassConvert, Keyring: load.Keyring}); err != nil {
		t.Fatal(err)
	}
	// A second user keeps a plain value; Outlook conferencing and recorded
	// losses for the first.
	if _, err := db.Exec(`INSERT INTO settings (user_id, webcal_url) VALUES ($1, 'https://keep.example/p.ics')`, l.user2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE settings SET conferencing_provider = 'zoom' WHERE user_id = $1`, l.user); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"google", "outlook", "zoom"} {
		if _, err := RecordCredentialLoss(db, l.user, p); err != nil {
			t.Fatal(err)
		}
	}
	fresh := encKeys(t, "fresh")
	res, removed, created, err := ResetLostKey(ctx, db, fresh)
	if err != nil || len(removed) != 1 || created == 0 {
		t.Fatalf("reset: %v %v %d", err, removed, created)
	}
	count := func(q string, args ...any) int {
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(`SELECT count(*) FROM oauth_tokens WHERE user_id = $1`, l.user) != 0 ||
		count(`SELECT count(*) FROM workspace_connections WHERE user_id = $1`, l.user) != 0 ||
		count(`SELECT count(*) FROM credential_losses WHERE user_id = $1`, l.user) != 0 {
		t.Fatal("S1/S7 rows or losses remain")
	}
	var ms, zs, webcal, conf, k1 string
	_ = db.QueryRow(`SELECT microsoft_tokens, zoom_tokens, webcal_url, conferencing_provider, llm_api_key_openai FROM settings WHERE user_id = $1`, l.user).Scan(&ms, &zs, &webcal, &conf, &k1)
	if ms != "" || zs != "" || webcal != "" || k1 != "" || conf != "meet" {
		t.Fatalf("settings after reset: conf %q", conf)
	}
	var enabled bool
	var url, creds string
	_ = db.QueryRow(`SELECT enabled, url, credentials_json FROM personal_calendars WHERE id = $1`, l.calID).Scan(&enabled, &url, &creds)
	if enabled || url != "" || creds != "" {
		t.Fatal("personal calendar not disabled and emptied")
	}
	if l.raw(t, db, Inventory[15]) != "" || l.raw(t, db, Inventory[16]) != "" || l.raw(t, db, Inventory[17]) != "" {
		t.Fatal("S9/S10/S11 not emptied")
	}
	var plain string
	_ = db.QueryRow(`SELECT webcal_url FROM settings WHERE user_id = $1`, l.user2).Scan(&plain)
	if plain != "https://keep.example/p.ics" {
		t.Fatal("a plain value was touched")
	}
	if res.Counts["oauth_tokens.access_token"]["deleted"] != 1 || res.Counts["personal_calendars.url"]["disabled"] != 1 {
		t.Fatalf("counts %+v", res.Counts)
	}
	deks, _ := LoadDataKeys(ctx, db)
	if len(deks) != 1 || deks[0].ID != created || deks[0].KEKID != "fresh" {
		t.Fatalf("data keys after reset %+v", deks)
	}
	if _, err := PrepareKeyring(ctx, db, fresh, true); err != nil {
		t.Fatalf("start with the new key: %v", err)
	}
	if _, _, ok := secretbox.OpensAny(lost, deks); ok {
		t.Fatal("the lost key opens the new data key")
	}
}

// AC-8..AC-11, AC-26, AC-27: key load at start.
func TestPAC84_KeyLoad(t *testing.T) {
	db, _, _ := openEncDB(t)
	ctx := context.Background()
	k1 := encKeys(t, "k1")
	inst := Instance{ID: uuid.New(), Hostname: "h"}

	// AC-27: two processes start at once on a fresh database.
	var wg sync.WaitGroup
	loads := make([]*KeyLoad, 2)
	errs := make([]error, 2)
	for i := range loads {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			loads[i], errs[i] = LoadKeyring(ctx, db, k1, Instance{ID: uuid.New()})
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || loads[0].Active != loads[1].Active {
		t.Fatalf("racing loads: %v %v", errs[0], errs[1])
	}
	deks, _ := LoadDataKeys(ctx, db)
	if len(deks) != 1 || (loads[0].Created == loads[1].Created) {
		t.Fatalf("exactly one data key must be created: %d", len(deks))
	}
	b := bindSettings("webcal_url", uuid.New())
	s, _ := loads[0].Keyring.Seal(b, "shared-value-abcdefghijklmn")
	if v, err := loads[1].Keyring.Open(b, s); err != nil || v != "shared-value-abcdefghijklmn" {
		t.Fatal("values not readable by both")
	}
	hb, _ := LiveHeartbeats(ctx, db)
	if len(hb) != 2 || hb[0].ActiveDEK == nil || *hb[0].ActiveDEK != loads[0].Active {
		t.Fatalf("heartbeats %+v", hb)
	}
	if loads[0].Keyring.Unwraps() != 1 {
		t.Fatalf("AC-26: unwraps %d, want 1 (one data key)", loads[0].Keyring.Unwraps())
	}

	// AC-9: same id, other bytes -> wrong key. AC-10: unknown id.
	wrong := encKeys(t, "k1")
	if _, err := LoadKeyring(ctx, db, wrong, inst); !errors.Is(err, secretbox.ErrWrongKey) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := LoadKeyring(ctx, db, encKeys(t, "zz"), inst); !errors.Is(err, secretbox.ErrUnknownKEK) || !strings.Contains(err.Error(), `"k1"`) {
		t.Fatalf("unknown: %v", err)
	}
	if n, _ := LoadDataKeys(ctx, db); len(n) != 1 {
		t.Fatal("a refused load created a data key")
	}

	// AC-11: K2 active, K1 previous -> re-wrapped in that start, no
	// inventory row rewritten; then K2 alone starts.
	u := uuid.New()
	if _, err := db.Exec(`INSERT INTO users (id, email, name) VALUES ($1, $2, 'R')`, u, u.String()+"@rw.example"); err != nil {
		t.Fatal(err)
	}
	prev := secretbox.Current()
	secretbox.Install(loads[0].Keyring)
	defer secretbox.Install(prev)
	if err := UpdateMicrosoftCredential(db, u, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings (user_id) VALUES ($1)`, u); err != nil {
		t.Fatal(err)
	}
	s0 := &Settings{WebcalURL: "https://rewrap.example/a.ics"}
	if err := SaveSettingsForUser(db, u, s0); err != nil {
		t.Fatal(err)
	}
	var xminBefore string
	_ = db.QueryRow(`SELECT xmin::text FROM settings WHERE user_id = $1`, u).Scan(&xminBefore)
	k2 := encKeys(t, "k2")
	k1p := k1.Active
	k1p.Var = secretbox.EnvKeyPrevious
	rot := secretbox.KeyConfig{Active: k2.Active, Previous: []secretbox.KEK{k1p}}
	load, err := LoadKeyring(ctx, db, rot, inst)
	if err != nil || load.Rewrapped != 1 || len(load.UnusedPrevious) != 1 {
		t.Fatalf("rotation start: %v %+v", err, load)
	}
	var xminAfter string
	_ = db.QueryRow(`SELECT xmin::text FROM settings WHERE user_id = $1`, u).Scan(&xminAfter)
	if xminAfter != xminBefore {
		t.Fatal("the KEK rotation rewrote an inventory row")
	}
	st, _ := ReadEncryptionStatus(ctx, db)
	for _, d := range st.DataKeys {
		if d.KEKID != "k2" {
			t.Fatal("a data key is still under k1")
		}
	}
	load2, err := LoadKeyring(ctx, db, k2, inst)
	if err != nil {
		t.Fatalf("K2 alone: %v", err)
	}
	secretbox.Install(load2.Keyring)
	got, _ := GetOrCreateSettingsByUser(db, u)
	if got.WebcalURL != "https://rewrap.example/a.ics" || got.SecretErr(SettingsColWebcalURL) != nil {
		t.Fatal("secret unreadable after KEK rotation")
	}
}

// AC-21b, AC-21c (start half): the maintenance lock is exclusive and names
// its holder; a backend refuses to start while decrypt-all holds it.
func TestPAC84_LocksAndOfflineOperation(t *testing.T) {
	_, dsn, _ := openEncDB(t)
	ctx := context.Background()
	op, err := OpenAs(dsn, OperatorApp("decrypt-all"))
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	backend, err := OpenAs(dsn, AppBackend)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	lock, err := TryMaintenanceLock(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = TryMaintenanceLock(ctx, backend)
	var held *LockHeldError
	if !errors.As(err, &held) || held.Holder == nil || held.Holder.AppName != "paceday-operator encryption decrypt-all" || held.Holder.PID == 0 {
		t.Fatalf("second lock: %v", err)
	}
	_, err = LoadKeyring(ctx, backend, encKeys(t, "x"), Instance{ID: uuid.New()})
	if !errors.Is(err, ErrOfflineOperation) || !strings.Contains(err.Error(), "paceday-operator encryption decrypt-all is running (pid") {
		t.Fatalf("backend start under decrypt-all: %v", err)
	}
	lock.Release()
	if _, err := LoadKeyring(ctx, backend, encKeys(t, "x"), Instance{ID: uuid.New()}); err != nil {
		t.Fatalf("start after release: %v", err)
	}
	l2, err := TryMaintenanceLock(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	l2.Release()
}

// AC-21c (lease half): Seal is refused once the last successful heartbeat
// was sent more than 60 s ago; a renewal resumes it.
func TestPAC84_SealLease(t *testing.T) {
	now := time.Now()
	l := NewLease(now)
	var logged string
	l.Logf = func(f string, a ...any) { logged = fmt.Sprintf(f, a...) }
	l.now = func() time.Time { return now.Add(59 * time.Second) }
	if l.Check() != nil {
		t.Fatal("lease ended early")
	}
	l.now = func() time.Time { return now.Add(61 * time.Second) }
	if !errors.Is(l.Check(), secretbox.ErrSealPaused) || !strings.Contains(logged, "sealing paused") {
		t.Fatal("lease did not end after 60 s")
	}
	l.Renew(now.Add(60 * time.Second))
	if l.Check() != nil {
		t.Fatal("renewal did not resume sealing")
	}
}

// AC-11b, AC-29, AC-31, AC-21c: the refresher installs a rotated key before
// the heartbeat reports it, flushes sealed_count, rotates automatically at
// the bound, keeps its keyring when it cannot unwrap, and shuts down when
// its data key is deleted.
func TestPAC84_Refresher(t *testing.T) {
	db, _, _ := openEncDB(t)
	ctx := context.Background()
	cfg := encKeys(t, "ref")
	inst := Instance{ID: uuid.New()}
	load, err := LoadKeyring(ctx, db, cfg, inst)
	if err != nil {
		t.Fatal(err)
	}
	prev := secretbox.Current()
	secretbox.Install(load.Keyring)
	defer secretbox.Install(prev)
	var reasons []string
	r := &Refresher{DB: db, Cfg: cfg, Inst: inst, Lease: NewLease(time.Now()), Logf: t.Logf,
		Shutdown: func(s string) { reasons = append(reasons, s) }}

	// sealed_count grows by the seals of the process within one tick.
	for i := 0; i < 5; i++ {
		if _, err := secretbox.Seal(bindSettings("webcal_url", uuid.New()), "v"); err != nil {
			t.Fatal(err)
		}
	}
	if !r.Tick(ctx) {
		t.Fatal("tick stopped")
	}
	_, _, sealed, _ := ActiveDataKey(ctx, db)
	if sealed != 5 {
		t.Fatalf("sealed_count %d, want 5", sealed)
	}

	// Operator rotation: the next tick switches, then reports it.
	newID, _ := RotateStep1(ctx, db, cfg, load.Active, false)
	r.Tick(ctx)
	if secretbox.Current().ActiveDEK() != newID {
		t.Fatal("refresher did not switch")
	}
	hb, _ := LiveHeartbeats(ctx, db)
	if len(hb) != 1 || *hb[0].ActiveDEK != newID {
		t.Fatal("heartbeat does not report the new key")
	}

	// AC-29: at the bound the tick rotates once.
	if _, err := db.Exec(`UPDATE encryption_data_keys SET sealed_count = $1 WHERE id = $2`, SealBound, newID); err != nil {
		t.Fatal(err)
	}
	r2 := &Refresher{DB: db, Cfg: cfg, Inst: Instance{ID: uuid.New()}, Logf: t.Logf}
	var wg sync.WaitGroup
	for _, rr := range []*Refresher{r, r2} {
		wg.Add(1)
		go func(rr *Refresher) { defer wg.Done(); rr.Tick(ctx) }(rr)
	}
	wg.Wait()
	deks, _ := LoadDataKeys(ctx, db)
	if len(deks) != 3 {
		t.Fatalf("automatic rotation made %d data keys, want 3", len(deks))
	}
	auto := secretbox.Current().ActiveDEK()
	if auto == newID {
		t.Fatal("not switched to the automatic key")
	}
	st, _ := ReadEncryptionStatus(ctx, db)
	for _, d := range st.DataKeys {
		if d.ID == newID && d.State != "retired" {
			t.Fatal("bound key not retired")
		}
	}
	// "rotate due" advisory.
	if !(DataKeyStatus{State: "active", SealedCount: SealAdvisory, CreatedAt: time.Now()}).RotateDue(time.Now()) ||
		!(DataKeyStatus{State: "active", CreatedAt: time.Now().Add(-366 * 24 * time.Hour)}).RotateDue(time.Now()) {
		t.Fatal("rotate due")
	}

	// AC-31: a data key wrapped by a key this process lacks: keyring kept,
	// active_dek not advanced.
	other := encKeys(t, "other")
	tx, _ := db.BeginTx(ctx, nil)
	_, _ = tx.Exec(`UPDATE encryption_data_keys SET state = 'retired', retired_at = now() WHERE state = 'active'`)
	if _, err := insertDataKey(ctx, tx, other.Active); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	r.Tick(ctx)
	if secretbox.Current().ActiveDEK() != auto {
		t.Fatal("switched to a key it cannot unwrap")
	}
	hb, _ = LiveHeartbeats(ctx, db)
	for _, h := range hb {
		if h.InstanceID == inst.ID && *h.ActiveDEK != auto {
			t.Fatal("active_dek advanced")
		}
	}

	// AC-21c: the active data key deleted -> shutdown, sealing stops.
	if _, err := db.Exec(`DELETE FROM encryption_data_keys`); err != nil {
		t.Fatal(err)
	}
	defer secretbox.SetSealGate(nil)
	if r.Tick(ctx) || len(reasons) != 1 || !strings.Contains(reasons[0], "no longer exists") {
		t.Fatalf("shutdown: %v", reasons)
	}
	if _, err := secretbox.Seal(bindSettings("webcal_url", uuid.New()), "v"); !errors.Is(err, secretbox.ErrSealPaused) {
		t.Fatal("still sealing after shutdown began")
	}
}

// AC-16b, AC-16c, AC-7: one undecryptable secret never fails a read, a save
// or a list; presence flags stay set; a save writes it back byte-identical;
// an explicit value (empty included) replaces it.
func TestPAC84_PerFieldFailure(t *testing.T) {
	db, _, _ := openEncDB(t)
	ctx := context.Background()
	logs := encCaptureLog(t)
	l := seedLegacy(t, db)
	load := installLoaded(t, db, encKeys(t, "pf"))
	if _, err := RunPass(ctx, db, PassOptions{Kind: PassConvert, Keyring: load.Keyring}); err != nil {
		t.Fatal(err)
	}
	// AC-5 state: user's openai key copied into the anthropic slot.
	if _, err := db.Exec(`UPDATE settings SET llm_api_key_anthropic = llm_api_key_openai WHERE user_id = $1`, l.user); err != nil {
		t.Fatal(err)
	}
	stored := l.raw(t, db, Inventory[7])
	s, err := GetOrCreateSettingsByUser(db, l.user)
	if err != nil {
		t.Fatalf("settings read failed: %v", err)
	}
	if !errors.Is(s.SecretErr("llm_api_key_anthropic"), secretbox.ErrDecrypt) || s.LLMAPIKeys.Anthropic != "" ||
		!s.LLMAPIKeysSet.Anthropic || s.LLMAPIKeys.OpenAI != l.values["settings.llm_api_key_openai"] {
		t.Fatal("per-field failure not isolated")
	}
	if !strings.Contains(logs.String(), "encryption: cannot decrypt settings.llm_api_key_anthropic row=user_id="+l.user.String()) ||
		!strings.Contains(logs.String(), "user="+l.user.String()+" reason=authentication_failed") {
		t.Fatalf("log line missing: %s", logs.String())
	}
	s.FocusLabel = "changed"
	if err := SaveSettingsForUser(db, l.user, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := l.raw(t, db, Inventory[7]); got != stored {
		t.Fatal("save rewrote the undecryptable key")
	}
	if got := l.raw(t, db, Inventory[6]); secretbox.Classify(got) != secretbox.KindSealed {
		t.Fatal("unchanged key not kept")
	}
	s.LLMAPIKeys.Anthropic = ""
	s.MarkSecretReplaced("llm_api_key_anthropic")
	_ = SaveSettingsForUser(db, l.user, s)
	if got := l.raw(t, db, Inventory[7]); got != "" {
		t.Fatal("an explicit empty value did not replace it")
	}

	// AC-7: NULL microsoft_tokens stays NULL, '' stays ''.
	if _, err := db.Exec(`UPDATE settings SET microsoft_tokens = NULL WHERE user_id = $1`, l.user); err != nil {
		t.Fatal(err)
	}
	s, _ = GetOrCreateSettingsByUser(db, l.user)
	_ = SaveSettingsForUser(db, l.user, s)
	_ = UpdateMicrosoftCredential(db, l.user, "renewed")
	var ms sql.NullString
	_ = db.QueryRow(`SELECT microsoft_tokens FROM settings WHERE user_id = $1`, l.user).Scan(&ms)
	if ms.Valid {
		t.Fatal("NULL microsoft_tokens changed")
	}

	// Workspace: tokens swapped between columns -> TokenErr, read succeeds.
	_, _ = db.Exec(`UPDATE workspace_connections SET bot_token = access_token WHERE user_id = $1`, l.user)
	wc, err := GetWorkspaceConnection(db, l.user, "slack")
	if err != nil || !errors.Is(wc.TokenErr, secretbox.ErrDecrypt) || wc.WorkspaceName != "WS" {
		t.Fatalf("workspace: %v", err)
	}

	// Personal calendars: one bad url never hides the list; delete works.
	id2, err := InsertPersonalCalendar(db, &PersonalCalendar{UserID: l.user, Provider: "webcal", Name: "Good", URL: "https://good.example/g.ics", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`UPDATE personal_calendars SET url = (SELECT url FROM personal_calendars WHERE id = $2) WHERE id = $1`, l.calID, id2)
	cals, err := ListPersonalCalendarsByUser(db, l.user)
	if err != nil || len(cals) != 2 || cals[0].URL != "" || cals[0].SecretErr == nil || cals[1].URL != "https://good.example/g.ics" {
		t.Fatalf("list: %v %+v", err, cals)
	}
	if c, err := UpdatePersonalCalendar(db, l.calID, l.user, strp("renamed"), nil, nil); err != nil || c.Name != "renamed" {
		t.Fatalf("update without url: %v", err)
	}
	if ok, err := DeletePersonalCalendarForUser(db, l.calID, l.user); err != nil || !ok {
		t.Fatal("delete the bad calendar")
	}

	// Org AI and SSO: read succeeds, presence kept, a save keeping the
	// secret writes it back byte-identical.
	_, _ = db.Exec(`UPDATE org_ai_configurations SET api_key = (SELECT llm_api_key_openai FROM settings WHERE user_id = $1) WHERE org_id = $2`, l.user, l.org)
	oc, err := GetOrgAIConfig(ctx, db, l.org)
	if err != nil || oc.KeyErr() == nil || !oc.KeyPresent() || !oc.Complete() {
		t.Fatalf("org read: %v", err)
	}
	orgRaw := l.raw(t, db, Inventory[15])
	if _, err := db.Exec(`INSERT INTO sso_stepup_grants (user_id, org_id, provider, purpose, issued_at, expires_at) VALUES ($1, $2, 'google', $3, now(), now() + interval '5 minutes')`,
		l.user, l.org, SSOGrantPurposeLLMOrgKeyWrite); err != nil {
		t.Fatal(err)
	}
	out, err := WriteOrgAIConfig(ctx, db, OrgAIWrite{UserID: l.user, OrgID: l.org, ExpectedVersion: 7,
		Patch: OrgAIPatch{Model: strp("gpt-x")}, Now: time.Now()})
	if err != nil || out.Status != OrgAIStored || l.raw(t, db, Inventory[15]) != orgRaw {
		t.Fatalf("org save keeping the key: %v %v", err, out.Status)
	}

	_, _ = db.Exec(`UPDATE sso_providers SET oidc_client_secret = saml_cert WHERE id = $1`, l.ssoID)
	ssoRaw := l.raw(t, db, Inventory[16])
	d := mustDomain(t, db, l.ssoID)
	sp, err := GetSSOProviderByDomain(db, d)
	if err != nil || sp == nil || sp.ClientSecretErr() == nil || !sp.HasClientSecret() {
		t.Fatalf("sso read: %v", err)
	}
	if _, err := SaveOIDCProvider(db, OIDCProviderWrite{Domain: d, ProviderName: "Renamed", Issuer: sp.OIDCIssuer, ClientID: sp.OIDCClientID}); err != nil {
		t.Fatalf("sso save keeping the secret: %v", err)
	}
	if l.raw(t, db, Inventory[16]) != ssoRaw {
		t.Fatal("the kept SSO secret was rewritten")
	}

	// Removal: an undecryptable credential is removed and reported so.
	_, _ = db.Exec(`UPDATE settings SET zoom_tokens = webcal_url WHERE user_id = $1`, l.user)
	removed, err := RemoveZoomCredential(db, l.user)
	if err != nil || removed != UndecryptableRemoved {
		t.Fatalf("remove: %q %v", removed, err)
	}
	if strings.Contains(logs.String(), "pdenc:1:") {
		t.Fatal("ciphertext in the log")
	}
}

// AC-32: operator_audit equals PAC-71's 033 definition; its CHECK admits
// exactly the eight actions.
func TestPAC84_OperatorAuditPinned(t *testing.T) {
	db, _, _ := openEncDB(t)
	rows, err := db.Query(`SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns WHERE table_name = 'operator_audit' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var n, ty, null, def string
		_ = rows.Scan(&n, &ty, &null, &def)
		got = append(got, fmt.Sprintf("%s %s %s %s", n, ty, null, def))
	}
	rows.Close()
	want := []string{
		"id uuid NO gen_random_uuid()",
		"action text NO ",
		"operator text NO ",
		"reason text NO ",
		"account_id uuid YES ",
		"account_email text YES ",
		"previous_identity text YES ",
		"new_identity text YES ",
		"evidence text YES ",
		"removed jsonb NO '[]'::jsonb",
		"details jsonb NO '{}'::jsonb",
		"created_at timestamp with time zone NO now()",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("operator_audit differs from PAC-71's 033:\n%s", strings.Join(got, "\n"))
	}
	ctx := context.Background()
	for _, a := range []string{"free_address", "reassign", "advance_all", AuditEncryptionConvert, AuditEncryptionRotate,
		AuditEncryptionDecrypt, AuditEncryptionResetLost, AuditEncryptionDowngrade} {
		if err := WriteEncryptionAudit(ctx, db, a, "op", "r", AuditDetails{Outcome: "completed"}); err != nil {
			t.Fatalf("%s refused: %v", a, err)
		}
	}
	if err := WriteEncryptionAudit(ctx, db, "encryption_other", "op", "r", AuditDetails{}); err == nil {
		t.Fatal("a ninth action was admitted")
	}
	if err := WriteEncryptionAudit(ctx, db, AuditEncryptionConvert, "", "r", AuditDetails{}); err == nil {
		t.Fatal("empty operator admitted")
	}
}
