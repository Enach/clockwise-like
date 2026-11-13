package auth

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// PAC-97 D-9: the in-memory resolver every detection test uses. No test in
// this package reaches live DNS: the package default detector is replaced at
// init by one whose resolver panics on use (AC-14), and every test that wants
// an answer builds its own Detector over a fakeMX.

// forbiddenResolver fails the run on any use. A panic in the lookup goroutine
// stops the test binary, which is the point: a test that reaches the default
// detector's resolver would otherwise query the real internet.
type forbiddenResolver struct{}

func (forbiddenResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	panic(fmt.Sprintf("PAC-97 AC-14: a test reached the default detector's resolver (query %q): inject a fake", name))
}

func init() {
	SetDefaultDetector(NewDetector(DetectorOptions{Resolver: forbiddenResolver{}}))
}

// fakeAnswer is one domain's DNS behaviour.
type fakeAnswer struct {
	records []*net.MX
	err     error
	// hang makes the lookup wait for its context to end (a resolver that
	// never answers).
	hang bool
	// gate, when non-nil, holds the lookup until it is closed.
	gate chan struct{}
}

// fakeQuery is one recorded lookup.
type fakeQuery struct {
	name        string
	at          time.Time
	deadline    time.Time
	hasDeadline bool
}

type fakeMX struct {
	mu       sync.Mutex
	answers  map[string]fakeAnswer // lower-case name, no trailing dot
	fallback *fakeAnswer
	queries  []fakeQuery
	started  chan string // optional: receives each query name as it starts
}

func newFakeMX() *fakeMX { return &fakeMX{answers: map[string]fakeAnswer{}} }

func (f *fakeMX) set(name string, a fakeAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[strings.ToLower(strings.TrimSuffix(name, "."))] = a
}

func (f *fakeMX) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	dl, ok := ctx.Deadline()
	f.mu.Lock()
	f.queries = append(f.queries, fakeQuery{name: name, at: time.Now(), deadline: dl, hasDeadline: ok})
	a, found := f.answers[strings.ToLower(strings.TrimSuffix(name, "."))]
	if !found && f.fallback != nil {
		a, found = *f.fallback, true
	}
	started := f.started
	f.mu.Unlock()
	if started != nil {
		started <- name
	}
	if !found {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	if a.gate != nil {
		select {
		case <-a.gate:
		case <-ctx.Done():
			return nil, &net.DNSError{Err: ctx.Err().Error(), Name: name, IsTimeout: true}
		}
	}
	if a.hang {
		<-ctx.Done()
		return nil, &net.DNSError{Err: "i/o timeout", Name: name, IsTimeout: true}
	}
	return a.records, a.err
}

func (f *fakeMX) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries)
}

func (f *fakeMX) recorded() []fakeQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeQuery(nil), f.queries...)
}

// mx builds records from "pref host" pairs.
func mx(pairs ...string) []*net.MX {
	out := make([]*net.MX, 0, len(pairs))
	for _, p := range pairs {
		var pref uint16
		var host string
		if _, err := fmt.Sscanf(p, "%d %s", &pref, &host); err != nil {
			panic(err)
		}
		out = append(out, &net.MX{Host: host, Pref: pref})
	}
	return out
}

// fakeClock is an injected clock (AC-8, AC-9).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// The spec's fixture table (docs/specs/PAC-97.md §4). Each value is the DNS
// answer the fake serves; "Expected" is in pac97Fixtures below.
func pac97FixtureAnswers() map[string]fakeAnswer {
	notFound := func(n string) error { return &net.DNSError{Err: "no such host", Name: n, IsNotFound: true} }
	return map[string]fakeAnswer{
		"gorgias.com": {records: mx("1 aspmx.l.google.com.", "5 alt1.aspmx.l.google.com.", "5 alt2.aspmx.l.google.com.",
			"10 alt3.aspmx.l.google.com.", "10 alt4.aspmx.l.google.com.")},
		"google-new.test":        {records: mx("1 smtp.google.com.")},
		"googlemail-legacy.test": {records: mx("10 aspmx2.googlemail.com.", "10 aspmx3.googlemail.com.")},
		"contoso.test":           {records: mx("0 contoso-test.mail.protection.outlook.com.")},
		"dane.test":              {records: mx("0 dane-test.o-v1.mx.microsoft.")},
		"eo-legacy.test":         {records: mx("10 eo-legacy-test.mail.eo.outlook.com.")},
		"olc.test":               {records: mx("10 olc-test.olc.protection.outlook.com.")},
		"sovereign.test":         {records: mx("0 sovereign-test.mail.protection.office365.us.")},
		"mimecast.test":          {records: mx("10 eu-smtp-inbound-1.mimecast.com.", "10 eu-smtp-inbound-2.mimecast.com.")},
		"proofpoint.test":        {records: mx("10 mx0a-001.pphosted.com.", "10 mx0b-001.pphosted.com.")},
		"migrating.test":         {records: mx("1 aspmx.l.google.com.", "5 migrating-test.mail.protection.outlook.com.")},
		"backup.test":            {records: mx("1 aspmx.l.google.com.", "20 mx.backup-provider.example.")},
		"gateway-first.test":     {records: mx("1 mx.gateway.example.", "20 aspmx.l.google.com.")},
		"lookalike.test":         {records: mx("10 mail.evilgoogle.com.", "10 aspmx.l.google.com.evil.example.")},
		"nullmx.test":            {records: mx("0 .")},
		"nxdomain.test":          {err: notFound("nxdomain.test.")},
		"nomx.test":              {err: notFound("nomx.test.")},
		"slow.test":              {hang: true},
		"servfail.test":          {err: &net.DNSError{Err: "server misbehaving", Name: "servfail.test.", IsTemporary: true}},
		"google-case.test":       {records: mx("1 ASPMX.L.GOOGLE.COM.")},
		"stripe-like.test":       {records: mx("10 aspmx.l.google.com.", "20 alt1.aspmx.l.google.com.", "30 aspmx2.googlemail.com.")},
		"partial.test":           {records: mx("10 aspmx.l.google.com."), err: &net.DNSError{Err: "DNS response contained records which contain invalid names", Name: "partial.test."}},
		"nullmix.test":           {records: mx("0 .", "10 aspmx.l.google.com.")},
	}
}

// pac97Fixtures is the table: domain as typed, expected answer.
var pac97Fixtures = []struct{ domain, want string }{
	{"gorgias.com", DetectGoogle},
	{"google-new.test", DetectGoogle},
	{"googlemail-legacy.test", DetectGoogle},
	{"contoso.test", DetectMicrosoft},
	{"dane.test", DetectMicrosoft},
	{"eo-legacy.test", DetectMicrosoft},
	{"olc.test", DetectChoose},
	{"sovereign.test", DetectChoose},
	{"mimecast.test", DetectChoose},
	{"proofpoint.test", DetectChoose},
	{"migrating.test", DetectChoose},
	{"backup.test", DetectGoogle},
	{"gateway-first.test", DetectChoose},
	{"lookalike.test", DetectChoose},
	{"nullmx.test", DetectChoose},
	{"nxdomain.test", DetectChoose},
	{"nomx.test", DetectChoose},
	{"slow.test", DetectChoose},
	{"servfail.test", DetectChoose},
	{"GOOGLE-CASE.TEST", DetectGoogle},
	{"stripe-like.test", DetectGoogle},
	{"partial.test", DetectChoose},
	{"nullmix.test", DetectChoose},
	{"intranet", DetectChoose},
}

func newFixtureFake() *fakeMX {
	f := newFakeMX()
	for name, a := range pac97FixtureAnswers() {
		f.set(name, a)
	}
	return f
}
