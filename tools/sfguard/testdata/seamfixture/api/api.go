package api

import "github.com/Enach/paceday/backend/storage"

// widgetStore is a storage seam that is not in the seams list.
type widgetStore interface {
	get(id int) (string, error)
}

type dbWidgetStore struct{}

func (dbWidgetStore) get(id int) (string, error) { return storage.GetWidget(id) }

// pure has an implementation that calls nothing targeted: not a seam.
type pure interface {
	name() (string, error)
}

type constName struct{}

func (constName) name() (string, error) { return "c", nil }

func use(w widgetStore, p pure) {
	_, _ = w, p
}
