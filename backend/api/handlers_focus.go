package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

type focusHandlers struct {
	eng FocusEngine
	db  *sql.DB
}

// decodeOptionalBody decodes an optional JSON request body into v. An absent,
// empty or whitespace-only body is no options and leaves v untouched, and so
// does the literal null. Anything else that does not decode is an error, which
// the caller answers with 400 "invalid JSON" (PAC-36, API-035): it used to be
// silently treated as {}, and the caller got a 200 for a request it never made.
func decodeOptionalBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (h *focusHandlers) runFocus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Week string `json:"week"`
	}
	if err := decodeOptionalBody(r, &body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	targetWeek := time.Now()
	if body.Week != "" {
		parsed, err := time.Parse("2006-01-02", body.Week)
		if err != nil {
			writeError(w, "invalid week format, use YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		targetWeek = parsed
	}

	result, err := h.eng.Run(r.Context(), targetWeek)
	if err != nil {
		writeCalendarFailure(w, r, "runFocus", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (h *focusHandlers) listBlocks(w http.ResponseWriter, r *http.Request) {
	weekStr := r.URL.Query().Get("week")
	weekStart := time.Now()
	if weekStr != "" {
		parsed, err := time.Parse("2006-01-02", weekStr)
		if err != nil {
			writeError(w, "invalid week format", http.StatusBadRequest)
			return
		}
		weekStart = parsed
	}

	blocks, err := storage.ListFocusBlocksForUserWeek(h.db, userIDFromCtx(r.Context()), weekStart)
	if err != nil {
		writeServerError(w, r, "listBlocks", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(blocks)
}

func (h *focusHandlers) clearBlocks(w http.ResponseWriter, r *http.Request) {
	weekStr := r.URL.Query().Get("week")
	weekStart := time.Now()
	if weekStr != "" {
		parsed, err := time.Parse("2006-01-02", weekStr)
		if err != nil {
			writeError(w, "invalid week format", http.StatusBadRequest)
			return
		}
		weekStart = parsed
	}

	n, err := h.eng.ClearWeek(r.Context(), weekStart)
	if err != nil {
		writeCalendarFailure(w, r, "clearBlocks", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"deleted": n})
}

func newFocusHandlers(db *sql.DB, oauthConfig *oauth2.Config) *focusHandlers {
	return &focusHandlers{
		eng: &engine.FocusTimeEngine{DB: db, OAuthConfig: oauthConfig},
		db:  db,
	}
}
