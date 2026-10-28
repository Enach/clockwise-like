package nlp

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// AISource is every storage access a decision makes (PAC-73 Part 2): the
// caller's settings, then one consistent read of their organisation, its AI
// configuration and their confirmation. It is an interface so a test can
// apply an administrator's change at each access a request makes
// (P2-AC-37).
type AISource interface {
	Settings(ctx context.Context, userID uuid.UUID) (*storage.Settings, error)
	OrgAI(ctx context.Context, userID uuid.UUID) (storage.OrgAIState, error)
}

// DBSource reads from the database.
type DBSource struct{ DB *sql.DB }

// Settings is the caller's settings row, created on first read.
func (s DBSource) Settings(ctx context.Context, userID uuid.UUID) (*storage.Settings, error) {
	return storage.GetOrCreateSettingsByUserCtx(ctx, s.DB, userID)
}

// OrgAI is storage.ReadOrgAIState.
func (s DBSource) OrgAI(ctx context.Context, userID uuid.UUID) (storage.OrgAIState, error) {
	return storage.ReadOrgAIState(ctx, s.DB, userID)
}

// LoadAIState reads everything a decision needs. Any failure is an error:
// the request is a server error and nothing is written, fetched or sent
// (spec §3.1).
func LoadAIState(ctx context.Context, src AISource, userID uuid.UUID) (AIState, error) {
	s, err := src.Settings(ctx, userID)
	if err != nil {
		return AIState{}, fmt.Errorf("load settings: %w", err)
	}
	org, err := src.OrgAI(ctx, userID)
	if err != nil {
		return AIState{}, fmt.Errorf("load organisation AI: %w", err)
	}
	return AIState{Settings: s, Org: org}, nil
}
