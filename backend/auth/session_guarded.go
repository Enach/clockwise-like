package auth

import (
	"context"
	"database/sql"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-71 in-request re-check for the calendar credentials (contract §5): the
// load runs in the same transaction as the session-generation re-check when
// ctx carries the request's session guard (storage.WithSessionGeneration).

// LoadUserTokenCtx is LoadUserToken (D1) for the request in ctx.
func LoadUserTokenCtx(ctx context.Context, db *sql.DB, userID uuid.UUID) (t *oauth2.Token, err error) {
	if userID == uuid.Nil {
		return nil, ErrNoUser
	}
	err = storage.WithSessionGeneration(ctx, db, func(q storage.Querier) error {
		t, err = LoadUserToken(q, userID)
		return err
	})
	return t, err
}

// LoadMicrosoftTokenCtx is LoadMicrosoftToken (D2) for the request in ctx.
func LoadMicrosoftTokenCtx(ctx context.Context, db *sql.DB, userID uuid.UUID) (t *oauth2.Token, err error) {
	err = storage.WithSessionGeneration(ctx, db, func(q storage.Querier) error {
		t, err = LoadMicrosoftToken(q, userID)
		return err
	})
	return t, err
}
