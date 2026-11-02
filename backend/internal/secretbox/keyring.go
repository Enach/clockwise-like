package secretbox

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type dekEntry struct {
	aead  cipher.AEAD
	kekID string
	state string
}

// Keyring is an immutable snapshot of unwrapped data keys; safe for
// concurrent use. Keyrings derived with With share the unwrap and seal
// counters, which are process-wide.
type Keyring struct {
	deks    map[int32]dekEntry
	active  int32
	unwraps *atomic.Int64
	seals   *sealCounts
}

type sealCounts struct{ m sync.Map } // int32 -> *atomic.Int64

func (s *sealCounts) add(id int32) {
	v, _ := s.m.LoadOrStore(id, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

// NewKeyring unwraps every data key of deks with the configured keys. It
// fails with ErrUnknownKEK (a data key wrapped by an id that is not
// configured), ErrWrongKey (a configured key does not open it) or
// ErrNoActiveDEK (none of deks is active).
func NewKeyring(cfg KeyConfig, deks []WrappedDEK) (*Keyring, error) {
	k := &Keyring{deks: map[int32]dekEntry{}, unwraps: new(atomic.Int64), seals: &sealCounts{}}
	return k.add(cfg, deks)
}

// With returns a keyring holding k's data keys plus those of more that k
// does not hold yet (only those are unwrapped), with states and the active
// id taken from more when more names them. The refresher's step.
func (k *Keyring) With(cfg KeyConfig, more []WrappedDEK) (*Keyring, error) {
	n := &Keyring{deks: make(map[int32]dekEntry, len(k.deks)+len(more)), active: k.active, unwraps: k.unwraps, seals: k.seals}
	for id, e := range k.deks {
		n.deks[id] = e
	}
	return n.add(cfg, more)
}

func (k *Keyring) add(cfg KeyConfig, deks []WrappedDEK) (*Keyring, error) {
	active := int32(0)
	for _, w := range deks {
		if e, ok := k.deks[w.ID]; ok {
			e.state = w.State
			k.deks[w.ID] = e
		} else {
			kek, ok := cfg.Lookup(w.KEKID)
			if !ok {
				return nil, unknownKEKError(w, cfg)
			}
			raw, err := unwrap(w, kek)
			k.unwraps.Add(1)
			if err != nil {
				return nil, err
			}
			a, err := gcm(raw)
			if err != nil {
				return nil, err
			}
			k.deks[w.ID] = dekEntry{aead: a, kekID: w.KEKID, state: w.State}
		}
		if w.State == "active" {
			active = w.ID
		}
	}
	if active != 0 {
		k.active = active
	}
	if k.active == 0 {
		return nil, ErrNoActiveDEK
	}
	return k, nil
}

// ActiveDEK is the data key new values are sealed with.
func (k *Keyring) ActiveDEK() int32 { return k.active }

// Has reports whether the keyring holds data key id.
func (k *Keyring) Has(id int32) bool { _, ok := k.deks[id]; return ok }

// IDs lists the data keys held, ascending.
func (k *Keyring) IDs() []int32 {
	out := make([]int32, 0, len(k.deks))
	for id := range k.deks {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// KEKOf returns the key id wrapping data key id ("" if not held).
func (k *Keyring) KEKOf(id int32) string { return k.deks[id].kekID }

// Unwraps counts the data keys unwrapped by this keyring and every keyring
// derived from it with With (AC-26).
func (k *Keyring) Unwraps() int64 { return k.unwraps.Load() }

// TakeSealCounts returns the seals per data key since the last call and
// resets them (the refresher adds them to sealed_count, contract 4.1).
func (k *Keyring) TakeSealCounts() map[int32]int64 {
	out := map[int32]int64{}
	k.seals.m.Range(func(key, v any) bool {
		if n := v.(*atomic.Int64).Swap(0); n != 0 {
			out[key.(int32)] = n
		}
		return true
	})
	return out
}

// RestoreSealCounts adds back counts that could not be flushed.
func (k *Keyring) RestoreSealCounts(counts map[int32]int64) {
	for id, n := range counts {
		v, _ := k.seals.m.LoadOrStore(id, new(atomic.Int64))
		v.(*atomic.Int64).Add(n)
	}
}

// Seal encrypts plaintext for b with the active data key. "" stays "";
// the result is never the plaintext.
func (k *Keyring) Seal(b Binding, plaintext string) (string, error) {
	return k.SealWith(k.active, b, plaintext)
}

// SealWith encrypts with data key dek (rotation).
func (k *Keyring) SealWith(dek int32, b Binding, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	e, ok := k.deks[dek]
	if !ok {
		return "", fmt.Errorf("secretbox: data key %d is not held", dek)
	}
	aad, err := b.aad()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	body := e.aead.Seal(nonce, nonce, []byte(plaintext), aad)
	k.seals.add(dek)
	return Marker + "1:" + strconv.Itoa(int(dek)) + ":" + base64.RawURLEncoding.EncodeToString(body), nil
}

// Open returns the plaintext of stored: empty is "", plain is returned as
// is, sealed is decrypted. A failure is a *DecryptError.
func (k *Keyring) Open(b Binding, stored string) (string, error) {
	switch Classify(stored) {
	case KindEmpty:
		return "", nil
	case KindPlain:
		return stored, nil
	}
	fail := func(dek int32, r Reason) (string, error) {
		e := &DecryptError{Table: b.Table, Column: b.Column, Row: b.RowString(), DEK: dek, Reason: r}
		if k != nil && dek > 0 {
			e.KEK = k.deks[dek].kekID
		}
		return "", e
	}
	if !strings.HasPrefix(stored, Marker) {
		return fail(0, ReasonMalformed)
	}
	parts := strings.Split(stored, ":")
	if len(parts) < 2 || parts[1] != "1" {
		return fail(0, ReasonUnsupportedVersion)
	}
	if len(parts) != 4 {
		return fail(0, ReasonMalformed)
	}
	dek, ok := parseDEKID(parts[2])
	if !ok {
		return fail(0, ReasonMalformed)
	}
	body, err := base64.RawURLEncoding.Strict().DecodeString(parts[3])
	if err != nil || len(body) < 28 {
		return fail(dek, ReasonMalformed)
	}
	if k == nil {
		return fail(dek, ReasonNoKeyring)
	}
	e, ok := k.deks[dek]
	if !ok {
		return fail(dek, ReasonUnknownDataKey)
	}
	aad, err := b.aad()
	if err != nil {
		return fail(dek, ReasonMalformed)
	}
	pt, err := e.aead.Open(nil, body[:12], body[12:], aad)
	if err != nil {
		return fail(dek, ReasonAuth)
	}
	return string(pt), nil
}

// The process-wide keyring (contract 6.1).
var (
	current  atomic.Pointer[Keyring]
	sealGate atomic.Pointer[func() error]
	reporter atomic.Pointer[func(string)]
)

// Install makes k the process-wide keyring (nil uninstalls).
func Install(k *Keyring) { current.Store(k) }

// Current is the installed keyring, nil before Install.
func Current() *Keyring { return current.Load() }

// SetSealGate installs a check every package-level Seal runs first (the
// seal lease, contract 4.3); nil removes it.
func SetSealGate(g func() error) {
	if g == nil {
		sealGate.Store(nil)
		return
	}
	sealGate.Store(&g)
}

// Seal seals with the keyring installed at call time.
func Seal(b Binding, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	k := Current()
	if k == nil {
		return "", ErrNoKeyring
	}
	if g := sealGate.Load(); g != nil {
		if err := (*g)(); err != nil {
			return "", err
		}
	}
	return k.Seal(b, plaintext)
}

// Open opens with the keyring installed at call time: empty and plain work
// without a keyring; sealed without one fails with reason no_keyring.
func Open(b Binding, stored string) (string, error) {
	return Current().Open(b, stored)
}

// SetReporter installs the error reporter that receives contract 9.2's
// line (Sentry in the backend); nil removes it.
func SetReporter(f func(string)) {
	if f == nil {
		reporter.Store(nil)
		return
	}
	reporter.Store(&f)
}

// ReportFailure writes contract 9.2's line for err (a *DecryptError) to the
// log and the error reporter; user is the acting or owning user ("" when
// not known). Any other error is ignored.
func ReportFailure(err error, user string) {
	de, ok := AsDecryptError(err)
	if !ok {
		return
	}
	line := de.LogLine(user)
	if !firstReport(line) {
		return
	}
	log.Print(line)
	if r := reporter.Load(); r != nil {
		(*r)(line)
	}
}

// Review #4 (contract 9.2): a storage read opens every secret of a row, so
// one undecryptable secret would be reported on every read of that row by
// every request and cron tick. Each distinct line is reported once per
// ReportWindow per process instead.
const ReportWindow = time.Hour

const maxReported = 10000

var (
	reportedMu sync.Mutex
	reported   = map[string]time.Time{}
	reportNow  = time.Now
)

// firstReport reports whether line was not reported within ReportWindow,
// and records it.
func firstReport(line string) bool {
	reportedMu.Lock()
	defer reportedMu.Unlock()
	now := reportNow()
	if at, ok := reported[line]; ok && now.Sub(at) < ReportWindow {
		return false
	}
	if len(reported) >= maxReported {
		reported = map[string]time.Time{}
	}
	reported[line] = now
	return true
}

// ResetReported forgets every reported failure (tests).
func ResetReported() {
	reportedMu.Lock()
	defer reportedMu.Unlock()
	reported = map[string]time.Time{}
}
