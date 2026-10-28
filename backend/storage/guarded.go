package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// PAC-71 in-request re-check, storage side (contract §5 "Credential load
// sites", plan §4). Each ...Ctx function is its plain counterpart run through
// WithSessionGeneration: when ctx carries the request's session guard, the
// read or write happens in the same transaction as the generation re-check,
// and returns ErrStaleSession (guard marked stale; requireAuth answers 401)
// instead of anything stored after the account's generation advanced. Without
// a guard (scheduled jobs, public pages, tests that inject a user) it is the
// plain function.

// GetOrCreateSettingsByUserCtx carries D2–D5 for the request.
func GetOrCreateSettingsByUserCtx(ctx context.Context, db *sql.DB, userID uuid.UUID) (s *Settings, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		s, err = GetOrCreateSettingsByUser(q, userID)
		return err
	})
	return s, err
}

// SaveSettingsForUserCtx is SaveSettingsForUser under the re-check (AI keys,
// AI destination and every preference: spec AC-46/AC-47 "sets a personal AI
// key").
func SaveSettingsForUserCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, s *Settings) error {
	return WithSessionGeneration(ctx, db, func(q Querier) error {
		return SaveSettingsForUserAt(q, userID, s, time.Now())
	})
}

// GetWorkspaceConnectionCtx carries D6 (Slack, Notion) for the request.
func GetWorkspaceConnectionCtx(ctx context.Context, db *sql.DB, userID uuid.UUID, provider string) (c *WorkspaceConnection, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		c, err = GetWorkspaceConnection(q, userID, provider)
		return err
	})
	return c, err
}

// UpsertWorkspaceConnectionCtx stores a Slack or Notion connection under the
// re-check (spec AC-46/AC-47 "completes a Slack connection").
func UpsertWorkspaceConnectionCtx(ctx context.Context, db *sql.DB, c *WorkspaceConnection) error {
	return WithSessionGeneration(ctx, db, func(q Querier) error {
		return UpsertWorkspaceConnection(q, c)
	})
}

// ListPersonalCalendarsByUserCtx carries D7 for the request.
func ListPersonalCalendarsByUserCtx(ctx context.Context, db *sql.DB, userID uuid.UUID) (out []PersonalCalendar, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		out, err = ListPersonalCalendarsByUser(q, userID)
		return err
	})
	return out, err
}

// GetPersonalCalendarCtx carries D7 (the calendar's URL and credentials).
func GetPersonalCalendarCtx(ctx context.Context, db *sql.DB, id int64) (c *PersonalCalendar, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		c, err = GetPersonalCalendar(q, id)
		return err
	})
	return c, err
}

// InsertPersonalCalendarCtx stores a personal calendar under the re-check.
func InsertPersonalCalendarCtx(ctx context.Context, db *sql.DB, c *PersonalCalendar) (id int64, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		id, err = InsertPersonalCalendar(q, c)
		return err
	})
	return id, err
}

// LoadZoomCredentialCtx carries the Zoom half of D2.
func LoadZoomCredentialCtx(ctx context.Context, db *sql.DB, userID uuid.UUID) (c *ZoomCredential, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		c, err = LoadZoomCredential(q, userID)
		return err
	})
	return c, err
}

// CreateTeamCtx creates a team under the re-check (spec AC-46/AC-47
// "creates a team").
func CreateTeamCtx(ctx context.Context, db *sql.DB, name string, createdBy uuid.UUID) (t *Team, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		t, err = CreateTeam(q, name, createdBy)
		return err
	})
	return t, err
}

// RespondToHostInviteCtx answers a co-host invitation under the re-check
// (spec AC-46/AC-47 "accepts a co-host invitation").
func RespondToHostInviteCtx(ctx context.Context, db *sql.DB, linkID, userID uuid.UUID, status string) (matched bool, err error) {
	err = WithSessionGeneration(ctx, db, func(q Querier) error {
		matched, err = RespondToHostInvite(q, linkID, userID, status)
		return err
	})
	return matched, err
}
