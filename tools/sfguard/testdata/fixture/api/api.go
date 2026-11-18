package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
)

type handler struct {
	db *sql.DB
	q  storage.Querier
	s  storage.Store
}

// Flagged: the shapes the register names.
func (h *handler) show(w http.ResponseWriter, id int) {
	name, _ := storage.GetUser(h.q, id)
	_ = storage.Save(h.q, name)
	var title string
	_ = h.db.QueryRow(`SELECT 1`).Scan(&title)
	tok, _ := auth.LoadToken(id)
	_ = tok
	// Excluded: deliberate discards outside the targeted set.
	_ = json.NewEncoder(w).Encode(map[string]string{"name": name})
	_, _ = w.Write([]byte("ok"))
	n, _ := strconv.Atoi("1")
	_ = n
	p, _ := auth.ParseToken("x") // auth, but not a Load*
	_ = p
	_ = storage.Pure("x") // no error result
	_ = storage.Begin().Rollback()
}

// Flagged: formatting does not hide one.
func (h handler) odd() {
	var (
		a, b, _ = storage.Count(
			h.q,
		)
	)
	_, _ = a, b
	v, _ := (h.s.Get)(1)
	_ = v
	var x string
	x, _ = h.s.Get(2)
	_ = x
	_ = (storage.Save(h.q, "y"))
}

// Not flagged: the error is checked, or used.
func checked(q storage.Querier) error {
	if _, err := storage.GetUser(q, 1); err != nil {
		return err
	}
	return storage.Save(q, "z")
}

// Flagged twice with the same key: the baseline counts occurrences.
func twice(q storage.Querier) {
	_ = storage.Save(q, "1")
	func() {
		_ = storage.Save(q, "2")
	}()
}
