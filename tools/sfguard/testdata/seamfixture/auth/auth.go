package auth

import "github.com/Enach/paceday/backend/storage"

// credentialStore is a listed seam; otherStore implements it with a storage
// call but is not listed itself. The audit must refuse that.
type credentialStore interface {
	load() (string, error)
}

type otherStore struct{}

func (otherStore) load() (string, error) { return storage.GetWidget(1) }

var _ credentialStore = otherStore{}
