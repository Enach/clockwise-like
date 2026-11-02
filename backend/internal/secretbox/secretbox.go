// Package secretbox encrypts the stored secrets of the inventory (PAC-84,
// contracts/features/PAC-84.md sections 3 and 6): the ciphertext envelope,
// the associated data that binds a value to its place, the key-encryption
// keys from the environment, the wrapped data keys and the in-memory keyring.
//
// It runs no SQL and does not import storage; storage imports it.
//
// Nothing in this package ever formats a key, a plaintext or a ciphertext:
// every error and log line carries identifiers only (spec D-11).
package secretbox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Marker starts every sealed value (contract 3.1).
const Marker = "pdenc:"

// FormatV1 is the only envelope format version.
const FormatV1 = 1

// Kind of a stored value, decided from its bytes only (contract 3.1).
type Kind int

const (
	KindEmpty Kind = iota
	KindPlain
	KindSealed
)

// Classify decides what a stored value is: "" is empty, a value starting
// with Marker is sealed, anything else is plain (spec D-7). A value that is
// one byte away from Marker in its first six bytes and otherwise has the
// envelope's shape is also sealed (a damaged envelope, AC-6): it never reads
// as plaintext.
func Classify(stored string) Kind {
	switch {
	case stored == "":
		return KindEmpty
	case strings.HasPrefix(stored, Marker):
		return KindSealed
	case damagedMarker(stored):
		return KindSealed
	}
	return KindPlain
}

// damagedMarker reports a value whose first six bytes differ from Marker in
// exactly one byte and whose rest is "<digits>:<digits>:<base64url>".
func damagedMarker(stored string) bool {
	if len(stored) < len(Marker)+5 {
		return false
	}
	diff := 0
	for i := 0; i < len(Marker); i++ {
		if stored[i] != Marker[i] {
			diff++
		}
	}
	if diff != 1 {
		return false
	}
	parts := strings.Split(stored[len(Marker):], ":")
	if len(parts) != 3 || !allDigits(parts[0]) || !allDigits(parts[1]) || parts[2] == "" {
		return false
	}
	for i := 0; i < len(parts[2]); i++ {
		c := parts[2][i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// SealedDEK returns the data-key id of a sealed value; ok is false unless the
// value is KindSealed with a parseable version-1 envelope header.
func SealedDEK(stored string) (id int32, ok bool) {
	if !strings.HasPrefix(stored, Marker) {
		return 0, false
	}
	parts := strings.Split(stored, ":")
	if len(parts) != 4 || parts[1] != "1" {
		return 0, false
	}
	return parseDEKID(parts[2])
}

// parseDEKID parses a data-key id: ASCII decimal, 1..2147483647, no sign, no
// leading zero.
func parseDEKID(s string) (int32, bool) {
	if !allDigits(s) || s[0] == '0' || len(s) > 10 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n < 1 {
		return 0, false
	}
	return int32(n), true
}

// Part is one row-identity part (contract 3.3).
type Part struct{ Name, Value string }

// Binding is where a value lives: its table, column and row identity
// (contract 3.2, 3.3).
type Binding struct {
	Table, Column string
	Row           []Part
}

const maxField = 65535

// aad builds the associated data, or an error when a field is too long.
func (b Binding) aad() ([]byte, error) {
	if len(b.Row) < 1 || len(b.Row) > 2 {
		return nil, fmt.Errorf("secretbox: binding of %s.%s has %d row parts, want 1 or 2", b.Table, b.Column, len(b.Row))
	}
	out := make([]byte, 0, 64)
	out = append(out, "paceday/secret"...)
	out = append(out, 0x00, FormatV1)
	var err error
	if out, err = lp16(out, b.Table); err != nil {
		return nil, err
	}
	if out, err = lp16(out, b.Column); err != nil {
		return nil, err
	}
	out = append(out, byte(len(b.Row)))
	for _, p := range b.Row {
		if out, err = lp16(out, p.Name); err != nil {
			return nil, err
		}
		if out, err = lp16(out, p.Value); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AAD returns the associated data of b (contract 3.2). A binding whose
// fields cannot be encoded yields nil; Seal and Open refuse such a binding.
func (b Binding) AAD() []byte {
	a, err := b.aad()
	if err != nil {
		return nil
	}
	return a
}

// RowString renders the row identity for logs: "user_id=…,calendar_id=…".
func (b Binding) RowString() string {
	parts := make([]string, 0, len(b.Row))
	for _, p := range b.Row {
		parts = append(parts, p.Name+"="+p.Value)
	}
	return strings.Join(parts, ",")
}

func lp16(out []byte, s string) ([]byte, error) {
	if len(s) > maxField {
		return nil, errors.New("secretbox: binding field longer than 65535 bytes")
	}
	out = binary.BigEndian.AppendUint16(out, uint16(len(s)))
	return append(out, s...), nil
}

// Errors. None carries input bytes, ciphertext or plaintext.
var (
	ErrDecrypt      = errors.New("stored secret cannot be decrypted")
	ErrNoKeyring    = errors.New("encryption keyring not installed")
	ErrKeyMissing   = errors.New("encryption key missing")
	ErrKeyMalformed = errors.New("encryption key malformed")
	ErrWrongKey     = errors.New("encryption key does not open a data key")
	ErrUnknownKEK   = errors.New("data key wrapped by an unknown key")
	ErrNoActiveDEK  = errors.New("no active data key")
	// ErrSealPaused: the process's seal lease has lapsed (contract 4.3).
	ErrSealPaused = errors.New("encryption: sealing paused (heartbeat not written)")
)

// Reason says why a sealed value could not be opened.
type Reason string

const (
	ReasonAuth               Reason = "authentication_failed"
	ReasonUnknownDataKey     Reason = "unknown_data_key"
	ReasonMalformed          Reason = "malformed"
	ReasonUnsupportedVersion Reason = "unsupported_version"
	ReasonNoKeyring          Reason = "no_keyring"
)

// DecryptError is a stored secret that could not be opened. It carries
// identifiers only (contract 9.2).
type DecryptError struct {
	Table, Column, Row string
	DEK                int32
	KEK                string
	Reason             Reason
}

func (e *DecryptError) fields() (dek, kek string) {
	dek, kek = "-", "-"
	if e.DEK > 0 {
		dek = strconv.Itoa(int(e.DEK))
	}
	if e.KEK != "" {
		kek = e.KEK
	}
	return dek, kek
}

// Error is contract 9.2's text without the user.
func (e *DecryptError) Error() string {
	dek, kek := e.fields()
	return fmt.Sprintf("cannot decrypt %s.%s row=%s dek=%s kek=%s reason=%s", e.Table, e.Column, e.Row, dek, kek, e.Reason)
}

// LogLine is contract 9.2's line: user is the acting or owning user, "" when
// not known.
func (e *DecryptError) LogLine(user string) string {
	dek, kek := e.fields()
	if user == "" {
		user = "-"
	}
	return fmt.Sprintf("encryption: cannot decrypt %s.%s row=%s dek=%s kek=%s user=%s reason=%s",
		e.Table, e.Column, e.Row, dek, kek, user, e.Reason)
}

// Is makes errors.Is(err, ErrDecrypt) true for every DecryptError.
func (e *DecryptError) Is(target error) bool { return target == ErrDecrypt }

// AsDecryptError returns the DecryptError in err's chain, if any.
func AsDecryptError(err error) (*DecryptError, bool) {
	var de *DecryptError
	if errors.As(err, &de) {
		return de, true
	}
	return nil, false
}
