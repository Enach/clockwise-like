package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
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
	s, err := storage.GetSettings(h.db)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
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

func (h *dailyRecapHandlers) patchSettings(w http.ResponseWriter, r *http.Request) {
	s, err := storage.GetSettings(h.db)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	var body recapSettingsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if body.Enabled != nil {
		s.RecapEnabled = *body.Enabled
	}
	if body.SendTime != nil {
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

	if err := storage.SaveSettings(h.db, s); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *dailyRecapHandlers) preview(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	s, err := storage.GetSettings(h.db)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	var userName string
	_ = h.db.QueryRow(`SELECT name FROM users WHERE id = $1`, userID).Scan(&userName)

	svc := &engine.DailyRecapService{DB: h.db}
	now := time.Now()
	blocks := svc.BuildMessage(r.Context(), userID, userName, now, s)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"blocks": blocks})
}

func (h *dailyRecapHandlers) test(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	s, err := storage.GetSettings(h.db)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	conn, err := storage.GetWorkspaceConnection(h.db, userID, "slack")
	if err != nil {
		http.Error(w, "Slack not connected", http.StatusBadRequest)
		return
	}

	var userName string
	_ = h.db.QueryRow(`SELECT name FROM users WHERE id = $1`, userID).Scan(&userName)

	svc := &engine.DailyRecapService{DB: h.db}
	now := time.Now()
	blocks := svc.BuildMessage(r.Context(), userID, userName, now, s)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ts, err := engine.SendSlackRecap(ctx, conn.BotToken, s.RecapSendTo, s.RecapChannelID, conn.WorkspaceID, userID, blocks)
	if err != nil {
		http.Error(w, "send error: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "slack_message_ts": ts})
}
