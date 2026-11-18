package auth

import "github.com/Enach/paceday/backend/storage"

// credentialStore is a listed storage seam (seams in analyzer.go), and so is
// its implementation googleStore.
type credentialStore interface {
	load() (string, error)
}

type googleStore struct{ q storage.Querier }

func (s googleStore) load() (string, error) { return storage.GetUser(s.q, 1) }

func renew(cs credentialStore, g googleStore) string {
	tok, _ := cs.load() // flagged: through the seam
	t2, _ := g.load()   // flagged: on the concrete store
	return tok + t2
}
