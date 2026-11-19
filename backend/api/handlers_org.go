package api

import (
	"database/sql"
	"encoding/json"
	"errors"
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

	// PAC-148 (contract md §3, D-8): a caller row that does not exist (an
	// account deleted under a live session) belongs to no organisation, so
	// the empty list is the true answer; any other failed read is the 500,
	// never "no organisation" (API-047, SF-075). OrgID is the effective
	// organisation (the membership rule), so a caller who belongs nowhere
	// gets the empty list with no further statement (equal work, md §2.6).
	user, err := storage.GetUserByIDNoOrg(h.db, userID)
	if errors.Is(err, sql.ErrNoRows) {
		_ = json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	if err != nil {
		logServerError(r, "org members: read caller", err, http.StatusInternalServerError)
		writeError(w, "internal", http.StatusInternalServerError)
		return
	}
	if user.OrgID == nil {
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
