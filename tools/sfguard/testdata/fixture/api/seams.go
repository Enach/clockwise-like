package api

import (
	"context"
	"database/sql"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
)

// orgAIStore and ConferenceEventClient are listed seams: a call through the
// interface hides the storage or calendar call behind it.
type orgAIStore interface {
	read(id int) (string, error)
}

type dbOrgAIStore struct{ q storage.Querier }

func (s dbOrgAIStore) read(id int) (string, error) { return storage.GetUser(s.q, id) }

type ConferenceEventClient interface {
	GetEvent(id string) (string, error)
}

var _ ConferenceEventClient = (*calendar.Client)(nil)

func seamCalls(st orgAIStore, cc ConferenceEventClient, db dbOrgAIStore) {
	a, _ := st.read(1)       // flagged: interface seam
	b, _ := cc.GetEvent("x") // flagged: calendar-ops seam
	c, _ := db.read(2)       // flagged: the seam's production type
	_, _, _ = a, b, c
}

// Generic storage calls and the other database/sql handles.
func handles(ctx context.Context, q storage.Querier, tx *sql.Tx, conn *sql.Conn) {
	v, _ := storage.Find[string](q)
	_ = v
	_, _ = tx.Exec("UPDATE x")
	_, _ = conn.ExecContext(ctx, "SELECT 1")
	res, err := tx.Exec("UPDATE y")
	if err == nil {
		n, _ := res.RowsAffected() // sql.Result is excluded by design
		_ = n
	}
}
