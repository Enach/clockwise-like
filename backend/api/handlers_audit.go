package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

func auditHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := userIDFromCtx(r.Context())
		if userID == uuid.Nil {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		// Atoi's error is not needed: unparseable text is 0 (the default) and
		// an overflowing value saturates (the maximum), as the contract states.
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		entries, err := storage.ListAuditLog(db, userID, limit)
		if err != nil {
			writeServerError(w, r, "audit", err, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}
}
