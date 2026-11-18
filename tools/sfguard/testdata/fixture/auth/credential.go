package auth

import "github.com/Enach/paceday/backend/storage"

// credentialStore is a listed storage seam (seams in analyzer.go).
type credentialStore interface {
	load() (string, error)
}

type dbCredentialStore struct{ q storage.Querier }

func (s dbCredentialStore) load() (string, error) { return storage.GetUser(s.q, 1) }

func renew(cs credentialStore) string {
	tok, _ := cs.load() // flagged: through the seam
	return tok
}
