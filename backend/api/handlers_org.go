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

	user, err := storage.GetUserByID(h.db, userID)
	if err != nil || user.OrgID == nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	members, err := storage.GetOrgMembers(h.db, *user.OrgID)
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	if members == nil {
		members = []*storage.User{}
	}
	json.NewEncoder(w).Encode(members)
}
