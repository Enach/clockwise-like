package scheduler

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"golang.org/x/oauth2"
)

// FocusCron schedules per-user auto focus-time runs. Reload queries
// storage.ListUsersWithAutoSchedule and registers one cron entry per user.
type FocusCron struct {
	cron        *cron.Cron
	db          *sql.DB
	oauthConfig *oauth2.Config
	entryIDs    map[uuid.UUID]cron.EntryID
}

func NewFocusCron(db *sql.DB, oauthConfig *oauth2.Config) *FocusCron {
	return &FocusCron{
		cron:        cron.New(),
		db:          db,
		oauthConfig: oauthConfig,
		entryIDs:    make(map[uuid.UUID]cron.EntryID),
	}
}

// Reload re-registers cron entries from the current set of users with
// auto-schedule enabled. Safe to call repeatedly (e.g. after a settings PUT).
func (fc *FocusCron) Reload() {
	configs, err := storage.ListUsersWithAutoSchedule(fc.db)
	if err != nil {
		log.Printf("cron reload: failed to list auto-schedule users: %v", err)
		return
	}

	// Remove any existing entries — simplest correctness model.
	for userID, id := range fc.entryIDs {
		fc.cron.Remove(id)
		delete(fc.entryIDs, userID)
	}

	eng := &engine.FocusTimeEngine{DB: fc.db, OAuthConfig: fc.oauthConfig}
	for _, cfg := range configs {
		userID := cfg.UserID // capture per-iteration
		cronExpr := cfg.CronExpr
		id, err := fc.cron.AddFunc(cronExpr, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			result, err := eng.RunForUser(ctx, userID, time.Now())
			if err != nil {
				log.Printf("cron focus run for user %s: %v", userID, err)
				return
			}
			log.Printf("cron focus run for user %s: created %d blocks (%d min)",
				userID, len(result.CreatedBlocks), result.TotalMinutes)
		})
		if err != nil {
			log.Printf("cron: invalid schedule %q for user %s: %v", cronExpr, userID, err)
			continue
		}
		fc.entryIDs[userID] = id
		log.Printf("cron: focus time scheduled for user %s: %s", userID, cronExpr)
	}
}

func (fc *FocusCron) Start() {
	fc.Reload()
	fc.cron.Start()
}

func (fc *FocusCron) Stop() {
	fc.cron.Stop()
}
