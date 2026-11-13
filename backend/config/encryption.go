package config

import (
	"encoding/base64"
	"fmt"

	"github.com/Enach/paceday/backend/internal/secretbox"
)

// EnvAllowTestKey lets a process start with a committed e2e key (PAC-84
// contract 11, K-12). Only the e2e stack sets it.
const EnvAllowTestKey = "PACEDAY_ENCRYPTION_ALLOW_TEST_KEY"

// committedTestKeys are the key bytes of the two committed e2e keys
// (contract 11): `paceday-e2e-test-key-NOT-SECRET!` and
// `paceday-e2e-test-key-2-NOTSECRET`. They are refused by bytes, whatever
// id names them, unless EnvAllowTestKey is exactly "true".
var committedTestKeys = []string{
	"cGFjZWRheS1lMmUtdGVzdC1rZXktTk9ULVNFQ1JFVCE=",
	"cGFjZWRheS1lMmUtdGVzdC1rZXktMi1OT1RTRUNSRVQ=",
}

// DecideEncryptionKeys parses PACEDAY_ENCRYPTION_KEY and
// PACEDAY_ENCRYPTION_KEY_PREVIOUS (contract 5.1) and refuses a committed e2e
// key unless PACEDAY_ENCRYPTION_ALLOW_TEST_KEY=true (contract 9.1, 11). The
// error text has neither the "refusing to start: " prefix nor the runbook
// reference; RefusalText adds them. It never contains key material.
func DecideEncryptionKeys(getenv func(string) string) (secretbox.KeyConfig, error) {
	cfg, err := secretbox.ParseKeyConfig(getenv("PACEDAY_ENCRYPTION_KEY"), getenv("PACEDAY_ENCRYPTION_KEY_PREVIOUS"))
	if err != nil {
		return secretbox.KeyConfig{}, err
	}
	if getenv("PACEDAY_ENCRYPTION_ALLOW_TEST_KEY") == "true" {
		return cfg, nil
	}
	for _, k := range cfg.All() {
		for _, b64 := range committedTestKeys {
			raw, _ := base64.StdEncoding.DecodeString(b64)
			if k.SameBytes(raw) {
				return secretbox.KeyConfig{}, fmt.Errorf("%w: %s is the committed e2e test key %q; generate a real key",
					secretbox.ErrKeyMalformed, k.Var, k.ID)
			}
		}
	}
	return cfg, nil
}

// EncryptionRefusal formats a key error as contract 9.1's line: prefix
// "refusing to start: " (backend) or "encryption: " (CLI), the error text,
// then the runbook reference.
func EncryptionRefusal(prefix string, err error) string {
	return prefix + stripSentinel(err) + " " + secretbox.RunbookRef
}

// stripSentinel drops the leading "<sentinel>: " that wrapping adds, so the
// line starts with the variable or data key it is about.
func stripSentinel(err error) string {
	msg := err.Error()
	for _, s := range []error{secretbox.ErrKeyMissing, secretbox.ErrKeyMalformed, secretbox.ErrWrongKey,
		secretbox.ErrUnknownKEK, secretbox.ErrNoActiveDEK} {
		p := s.Error() + ": "
		if len(msg) > len(p) && msg[:len(p)] == p {
			return msg[len(p):]
		}
	}
	return msg
}
