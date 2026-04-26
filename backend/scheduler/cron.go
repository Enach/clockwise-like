package scheduler

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/Enach/clockwise-like/backend/engine"
	"github.com/Enach/clockwise-like/backend/storage"
	"github.com/robfig/cron/v3"
	"golang.org/x/oauth2"
)

type FocusCron struct {
	cron        *cron.Cron
	db          *sql.DB
	oauthConfig *oauth2.Config
	entryID     cron.EntryID
}

func NewFocusCron(db *sql.DB, oauthConfig *oauth2.Config) *FocusCron {
	return &FocusCron{
		cron:        cron.New(),
		db:          db,
		oauthConfig: oauthConfig,
	}
}

func (fc *FocusCron) Reload() {
	s, err := storage.GetSettings(fc.db)
	if err != nil {
		log.Printf("cron reload: failed to load settings: %v", err)
		return
	}

	if fc.entryID != 0 {
		fc.cron.Remove(fc.entryID)
		fc.entryID = 0
	}

	if !s.AutoScheduleEnabled || s.AutoScheduleCron == "" {
		return
	}

	eng := &engine.FocusTimeEngine{DB: fc.db, OAuthConfig: fc.oauthConfig}
	id, err := fc.cron.AddFunc(s.AutoScheduleCron, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		result, err := eng.Run(ctx, time.Now())
		if err != nil {
			log.Printf("cron focus run error: %v", err)
			return
		}
		log.Printf("cron focus run: created %d blocks (%d min)", len(result.CreatedBlocks), result.TotalMinutes)
	})
	if err != nil {
		log.Printf("cron: invalid schedule %q: %v", s.AutoScheduleCron, err)
		return
	}

	fc.entryID = id
	log.Printf("cron: focus time scheduled: %s", s.AutoScheduleCron)
}

func (fc *FocusCron) Start() {
	fc.Reload()
	fc.cron.Start()
}

func (fc *FocusCron) Stop() {
	fc.cron.Stop()
}
