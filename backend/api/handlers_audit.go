package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Enach/paceday/backend/storage"
)

func auditHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		entries, err := storage.ListAuditLog(db, limit)
		if err != nil {
			writeError(w, "audit: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}
}
