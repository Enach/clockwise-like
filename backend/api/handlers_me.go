package api

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	if err != nil { // PAC-122 SF-072: a failed read is not "user not found"
		writeServerError(w, r, "current user", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (h *meHandlers) logout(w http.ResponseWriter, r *http.Request) {
	setCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	// 204: no body, which both web API clients accept (PAC-35, API-063).
	w.WriteHeader(http.StatusNoContent)
}
