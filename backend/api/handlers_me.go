package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

type meHandlers struct {
	db *sql.DB
}

func (h *meHandlers) me(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	if userID == uuid.Nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	user, err := storage.GetUserByID(h.db, userID)
	if err != nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (h *meHandlers) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusOK)
}
