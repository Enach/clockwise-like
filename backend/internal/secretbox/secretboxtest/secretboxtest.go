// Package secretboxtest installs a throw-away keyring for tests (contract
// 6.1, 11): a random key per test process, generated here, so no test needs
// a developer-supplied PACEDAY_ENCRYPTION_KEY.
package secretboxtest

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/internal/secretbox"
)

// RandomKey returns 32 random bytes.
func RandomKey() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// Key returns a random valid PACEDAY_ENCRYPTION_KEY value ("test-<n>:<b64>").
func Key(t testing.TB) string {
	t.Helper()
	return KeyValue("test")
}

// KeyValue returns "<id>:<base64 of 32 random bytes>".
func KeyValue(id string) string {
	return id + ":" + base64.StdEncoding.EncodeToString(RandomKey())
}

// NewKeyring builds a keyring with a random KEK and one active data key
// (id 1), not installed.
func NewKeyring() (*secretbox.Keyring, secretbox.KeyConfig) {
	kek, err := secretbox.NewKEK("test", RandomKey())
	if err != nil {
		panic(err)
	}
	cfg := secretbox.KeyConfig{Active: kek}
	w, err := secretbox.NewDEK(1, kek)
	if err != nil {
		panic(err)
	}
	k, err := secretbox.NewKeyring(cfg, []secretbox.WrappedDEK{w})
	if err != nil {
		panic(fmt.Sprintf("secretboxtest: %v", err))
	}
	return k, cfg
}

// Install installs a fresh keyring for the test and restores the previous
// one on cleanup.
func Install(t testing.TB) *secretbox.Keyring {
	t.Helper()
	prev := secretbox.Current()
	k, _ := NewKeyring()
	secretbox.Install(k)
	t.Cleanup(func() { secretbox.Install(prev) })
	return k
}

var processOnce sync.Once

// EnsureProcess installs a process-wide keyring once if none is installed
// (internal/testdb calls it, so every database test can write secrets).
func EnsureProcess() {
	processOnce.Do(func() {
		if secretbox.Current() == nil {
			k, _ := NewKeyring()
			secretbox.Install(k)
		}
	})
}
