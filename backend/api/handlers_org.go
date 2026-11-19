package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

type orgHandlers struct {
	db *sql.DB
}

func (h *orgHandlers) members(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userID := userIDFromCtx(r.Context())
	if userID == uuid.Nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	user, err := storage.GetUserByIDNoOrg(h.db, userID)
	if err != nil || user.OrgID == nil {
		_ = json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	// PAC-130 (contract md §2.5): the set is keyed by the viewer, through the
	// one definition the Manager visibility rule (b) reads too.
	members, err := storage.GetOrgMembers(h.db, userID)
	if err != nil {
		logServerError(r, "org members", err, http.StatusInternalServerError)
		writeError(w, "internal", http.StatusInternalServerError)
		return
	}
	if members == nil {
		members = []*storage.User{}
	}
	_ = json.NewEncoder(w).Encode(members)
}
