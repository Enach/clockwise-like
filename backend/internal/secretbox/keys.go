package secretbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"regexp"
	"strings"
)

// The environment variables of contract 5.1.
const (
	EnvKey         = "PACEDAY_ENCRYPTION_KEY"
	EnvKeyPrevious = "PACEDAY_ENCRYPTION_KEY_PREVIOUS"
)

// RunbookRef ends every key refusal (contract 9.1).
const RunbookRef = `(docs/runbook.md, "Encryption key")`

const maxPrevious = 8

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// ValidKeyID reports whether id is a valid key id (contract 5.1).
func ValidKeyID(id string) bool { return keyIDPattern.MatchString(id) }

// KEK is a key-encryption key from the environment. Its bytes are
// unexported and never formatted: String and Format print the id only.
type KEK struct {
	ID  string
	key [32]byte
	// Var is the variable the key came from (EnvKey or EnvKeyPrevious).
	Var string
}

// String is the key id only.
func (k KEK) String() string { return k.ID }

// Format prints the key id only, whatever the verb.
func (k KEK) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(k.ID)) }

// GoString prints the key id only.
func (k KEK) GoString() string { return "secretbox.KEK{ID:" + fmt.Sprintf("%q", k.ID) + "}" }

// Equal reports whether k and o hold the same key bytes.
func (k KEK) Equal(o KEK) bool { return k.key == o.key }

// SameBytes reports whether k's key bytes equal b.
func (k KEK) SameBytes(b []byte) bool { return len(b) == 32 && bytes.Equal(k.key[:], b) }

// NewKEK builds a KEK from raw bytes (tests, generated keys).
func NewKEK(id string, key []byte) (KEK, error) {
	if !ValidKeyID(id) {
		return KEK{}, fmt.Errorf("%w: invalid key id", ErrKeyMalformed)
	}
	if len(key) != 32 {
		return KEK{}, fmt.Errorf("%w: decodes to %d bytes, need 32", ErrKeyMalformed, len(key))
	}
	k := KEK{ID: id, Var: EnvKey}
	copy(k.key[:], key)
	return k, nil
}

// KeyConfig is the active key and the previous keys of the environment.
type KeyConfig struct {
	Active   KEK
	Previous []KEK
}

// IDs lists the previous key ids, `"a", "b"` or `none`.
func (c KeyConfig) previousIDs() string {
	if len(c.Previous) == 0 {
		return `"none"`
	}
	ids := make([]string, 0, len(c.Previous))
	for _, p := range c.Previous {
		ids = append(ids, fmt.Sprintf("%q", p.ID))
	}
	return strings.Join(ids, ", ")
}

// PreviousIDs is the previous key ids, in order.
func (c KeyConfig) PreviousIDs() []string {
	out := make([]string, 0, len(c.Previous))
	for _, p := range c.Previous {
		out = append(out, p.ID)
	}
	return out
}

// Lookup returns the configured key with id.
func (c KeyConfig) Lookup(id string) (KEK, bool) {
	if c.Active.ID == id && id != "" {
		return c.Active, true
	}
	for _, p := range c.Previous {
		if p.ID == id {
			return p, true
		}
	}
	return KEK{}, false
}

// All returns the active key then the previous ones.
func (c KeyConfig) All() []KEK { return append([]KEK{c.Active}, c.Previous...) }

// malformed builds a malformed-key error; it echoes the id only when valid.
func malformed(where, reason string) error {
	return fmt.Errorf("%w: %s is malformed: %s", ErrKeyMalformed, where, reason)
}

// ParseKeyConfig parses the two variables (contract 5.1). The error text
// names the variable, the entry and the reason, never an input byte other
// than a valid key id; it starts with neither prefix nor runbook reference.
func ParseKeyConfig(active, previous string) (KeyConfig, error) {
	active = strings.TrimSpace(active)
	if active == "" {
		return KeyConfig{}, fmt.Errorf("%w: %s is not set; generate one with: echo \"%s=$(date -u +%%Y%%m%%d):$(openssl rand -base64 32)\" > encryption.env",
			ErrKeyMissing, EnvKey, EnvKey)
	}
	var cfg KeyConfig
	var err error
	if cfg.Active, err = parseEntry(active, EnvKey, EnvKey); err != nil {
		return KeyConfig{}, err
	}
	previous = strings.TrimSpace(previous)
	if previous != "" {
		entries := strings.Split(previous, ",")
		if len(entries) > maxPrevious {
			return KeyConfig{}, malformed(EnvKeyPrevious, "more than 8 previous keys")
		}
		for i, e := range entries {
			where := fmt.Sprintf("entry %d of %s", i+1, EnvKeyPrevious)
			k, err := parseEntry(strings.Trim(e, " "), where, EnvKeyPrevious)
			if err != nil {
				return KeyConfig{}, err
			}
			cfg.Previous = append(cfg.Previous, k)
		}
	}
	all := cfg.All()
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i].ID == all[j].ID {
				return KeyConfig{}, malformed(all[j].Var, fmt.Sprintf("key id %q appears twice", all[j].ID))
			}
			if all[i].key == all[j].key {
				return KeyConfig{}, malformed(all[j].Var, fmt.Sprintf("keys %q and %q are the same key", all[i].ID, all[j].ID))
			}
		}
	}
	return cfg, nil
}

func parseEntry(entry, where, variable string) (KEK, error) {
	i := strings.IndexByte(entry, ':')
	if i < 0 {
		return KEK{}, malformed(where, "expected <key id>:<base64 of 32 bytes>")
	}
	id, b64 := entry[:i], entry[i+1:]
	if !ValidKeyID(id) {
		return KEK{}, malformed(where, "invalid key id")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(b64)
	if err != nil || strings.ContainsAny(b64, " \t\r\n") {
		return KEK{}, malformed(where, fmt.Sprintf("invalid base64 (key %q)", id))
	}
	if len(raw) != 32 {
		return KEK{}, malformed(where, fmt.Sprintf("decodes to %d bytes, need 32 (key %q)", len(raw), id))
	}
	k := KEK{ID: id, Var: variable}
	copy(k.key[:], raw)
	return k, nil
}

// WrappedDEK is a stored data key as read from encryption_data_keys.
type WrappedDEK struct {
	ID      int32
	State   string
	KEKID   string
	Wrapped []byte
}

const wrappedLen = 12 + 32 + 16

func wrapAAD(id int32, kekID string) []byte {
	out := append([]byte("paceday/dek"), 0x00, FormatV1)
	out = binary.BigEndian.AppendUint32(out, uint32(id))
	out, _ = lp16(out, kekID)
	return out
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func wrap(id int32, kek KEK, dek []byte) ([]byte, error) {
	a, err := gcm(kek.key[:])
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, dek, wrapAAD(id, kek.ID)), nil
}

func unwrap(w WrappedDEK, kek KEK) ([]byte, error) {
	if len(w.Wrapped) != wrappedLen {
		return nil, fmt.Errorf("data key %d: wrapped key has %d bytes, want %d", w.ID, len(w.Wrapped), wrappedLen)
	}
	a, err := gcm(kek.key[:])
	if err != nil {
		return nil, err
	}
	dek, err := a.Open(nil, w.Wrapped[:12], w.Wrapped[12:], wrapAAD(w.ID, w.KEKID))
	if err != nil {
		return nil, wrongKeyError(w, kek)
	}
	return dek, nil
}

func wrongKeyError(w WrappedDEK, kek KEK) error {
	return fmt.Errorf("%w: key %q (%s) does not open data key %d (wrapped by key %q)", ErrWrongKey, kek.ID, kek.Var, w.ID, w.KEKID)
}

func unknownKEKError(w WrappedDEK, cfg KeyConfig) error {
	return fmt.Errorf("%w: data key %d is wrapped by key %q, which is neither %s (%q) nor in %s (%s)",
		ErrUnknownKEK, w.ID, w.KEKID, EnvKey, cfg.Active.ID, EnvKeyPrevious, cfg.previousIDs())
}

// NewDEK creates a random 32-byte data key with id, wrapped by kek
// (contract 4.2).
func NewDEK(id int32, kek KEK) (WrappedDEK, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return WrappedDEK{}, err
	}
	wrapped, err := wrap(id, kek, dek)
	if err != nil {
		return WrappedDEK{}, err
	}
	return WrappedDEK{ID: id, State: "active", KEKID: kek.ID, Wrapped: wrapped}, nil
}

// Rewrap unwraps w with the configured key it names and wraps it again
// under cfg.Active (D-6). A data key already wrapped by cfg.Active is
// returned unchanged.
func Rewrap(w WrappedDEK, cfg KeyConfig) (WrappedDEK, error) {
	kek, ok := cfg.Lookup(w.KEKID)
	if !ok {
		return WrappedDEK{}, unknownKEKError(w, cfg)
	}
	dek, err := unwrap(w, kek)
	if err != nil {
		return WrappedDEK{}, err
	}
	if w.KEKID == cfg.Active.ID {
		return w, nil
	}
	wrapped, err := wrap(w.ID, cfg.Active, dek)
	if err != nil {
		return WrappedDEK{}, err
	}
	return WrappedDEK{ID: w.ID, State: w.State, KEKID: cfg.Active.ID, Wrapped: wrapped}, nil
}

// OpensAny reports whether any configured key unwraps any of deks (the
// "key is not lost" check of reset-lost-key, contract 8.2).
func OpensAny(cfg KeyConfig, deks []WrappedDEK) (WrappedDEK, KEK, bool) {
	for _, w := range deks {
		for _, k := range cfg.All() {
			if _, err := unwrap(w, k); err == nil {
				return w, k, true
			}
		}
	}
	return WrappedDEK{}, KEK{}, false
}
