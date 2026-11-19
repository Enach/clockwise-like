package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

type dailyRecapHandlers struct {
	db *sql.DB
}

type recapSettingsBody struct {
	Enabled            *bool   `json:"enabled"`
	SendTime           *string `json:"send_time"`
	SendTo             *string `json:"send_to"`
	ChannelID          *string `json:"channel_id"`
	IncludeBriefs      *bool   `json:"include_briefs"`
	IncludeFocusBlocks *bool   `json:"include_focus_blocks"`
	IncludeHabits      *bool   `json:"include_habits"`
}

func (h *dailyRecapHandlers) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := storage.GetOrCreateSettingsByUserCtx(r.Context(), h.db, userIDFromCtx(r.Context()))
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":              s.RecapEnabled,
		"send_time":            s.RecapSendTime,
		"send_to":              s.RecapSendTo,
		"channel_id":           s.RecapChannelID,
		"include_briefs":       s.RecapIncludeBriefs,
		"include_focus_blocks": s.RecapIncludeFocus,
		"include_habits":       s.RecapIncludeHabits,
	})
}

// recapPatchProperties is every property PATCH /api/settings/daily-recap accepts.
var recapPatchProperties = map[string]bool{
	"enabled": true, "send_time": true, "send_to": true, "channel_id": true,
	"include_briefs": true, "include_focus_blocks": true, "include_habits": true,
}

func (h *dailyRecapHandlers) patchSettings(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	// A null is not "leave unchanged" and an unknown property is not ignored
	// (PAC-24, matching PATCH /api/settings): both are a 400.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	for name, value := range fields {
		if !recapPatchProperties[name] {
			http.Error(w, "unknown property: "+name, http.StatusBadRequest)
			return
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			http.Error(w, name+" must not be null", http.StatusBadRequest)
			return
		}
	}
	var body recapSettingsBody
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	s, err := storage.GetOrCreateSettingsByUserCtx(r.Context(), h.db, userID)
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	if body.Enabled != nil {
		s.RecapEnabled = *body.Enabled
	}
	if body.SendTime != nil {
		if !validClockTime(*body.SendTime) {
			http.Error(w, "send_time must be in HH:MM format", http.StatusBadRequest)
			return
		}
		s.RecapSendTime = *body.SendTime
	}
	if body.SendTo != nil {
		if *body.SendTo != "dm" && *body.SendTo != "channel" {
			http.Error(w, "send_to must be 'dm' or 'channel'", http.StatusBadRequest)
			return
		}
		s.RecapSendTo = *body.SendTo
	}
	if body.ChannelID != nil {
		s.RecapChannelID = *body.ChannelID
	}
	if body.IncludeBriefs != nil {
		s.RecapIncludeBriefs = *body.IncludeBriefs
	}
	if body.IncludeFocusBlocks != nil {
		s.RecapIncludeFocus = *body.IncludeFocusBlocks
	}
	if body.IncludeHabits != nil {
		s.RecapIncludeHabits = *body.IncludeHabits
	}

	if err := storage.SaveRecapSettingsForUserCtx(r.Context(), h.db, userID, s); err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *dailyRecapHandlers) preview(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	s, err := storage.GetOrCreateSettingsByUserCtx(r.Context(), h.db, userID)
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	userName, err := recapUserName(r.Context(), h.db, userID)
	if err != nil { // PAC-141 SF-109: a failed read of the caller's name is the documented 500
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	svc := &engine.DailyRecapService{DB: h.db}
	now := time.Now().In(recapLocation(s))
	blocks, err := svc.BuildMessage(r.Context(), userID, userName, now, s)
	if err != nil { // PAC-122 SF-046: a failed read of the day is the documented 500, not "a clear calendar"
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"blocks": blocks})
}

func (h *dailyRecapHandlers) test(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	s, err := storage.GetOrCreateSettingsByUserCtx(r.Context(), h.db, userID)
	if err != nil {
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	conn, err := storage.GetWorkspaceConnectionCtx(r.Context(), h.db, userID, "slack")
	if err == nil && conn.TokenErr != nil {
		// PAC-84 (contract 1, K-2): an undecryptable Slack token is the
		// operation's documented 500, never 400 "Slack not connected".
		logServerError(r, "db error", conn.TokenErr, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}
	if err != nil {
		http.Error(w, "Slack not connected", http.StatusBadRequest)
		return
	}

	userName, err := recapUserName(r.Context(), h.db, userID)
	if err != nil { // PAC-141 SF-109: a failed read of the caller's name is the documented 500
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	svc := &engine.DailyRecapService{DB: h.db}
	now := time.Now().In(recapLocation(s))
	blocks, err := svc.BuildMessage(r.Context(), userID, userName, now, s)
	if err != nil { // PAC-122 SF-046: a failed read of the day is the documented 500, not "a clear calendar"
		logServerError(r, "db error", err, http.StatusInternalServerError)
		writeError(w, "db error", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ts, err := engine.SendSlackRecap(ctx, conn.BotToken, s.RecapSendTo, s.RecapChannelID, conn.WorkspaceID, userID, blocks)
	if err != nil {
		writeServerError(w, r, "send error", err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "slack_message_ts": ts})
}

// recapUserName is the caller's display name for the recap greeting. A user
// row that does not exist is no name (the greeting has none, as before); any
// other failed read is the error (PAC-141 SF-109), never an empty name.
func recapUserName(ctx context.Context, db *sql.DB, userID uuid.UUID) (string, error) {
	u, err := storage.GetUserByID(storage.Bind(ctx, db), userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read user name: %w", err)
	}
	return u.Name, nil
}

// recapLocation is the caller's zone, as the scheduled recap uses
// (engine.DailyRecapService.RunForUser), or UTC when it cannot be resolved.
func recapLocation(s *storage.Settings) *time.Location {
	if loc, err := time.LoadLocation(s.Timezone); err == nil {
		return loc
	}
	return time.UTC
}
