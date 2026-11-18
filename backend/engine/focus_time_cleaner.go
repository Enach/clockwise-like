package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// ClearWeek deletes the focus blocks of the week for the user in ctx, from
// their calendar and from storage. Other users' blocks are not touched.
func (e *FocusTimeEngine) ClearWeek(ctx context.Context, targetWeek time.Time) (int, error) {
	userID := auth.UserIDFromContext(ctx)
	if userID == uuid.Nil {
		return 0, fmt.Errorf("FocusTimeEngine.ClearWeek: userID is required")
	}
	client, err := e.calClient(ctx)
	if err != nil {
		return 0, err
	}

	monday := startOfWeek(targetWeek)
	blocks, err := storage.ListFocusBlocksForUserWeek(e.DB, userID, monday)
	if err != nil {
		return 0, fmt.Errorf("list focus blocks: %w", err)
	}

	// PAC-122 SF-018: a block's row is deleted only once its calendar event
	// is gone (deleted now, or already absent: Google's 404/410). When the
	// event delete fails the row stays, so the block is still tracked and a
	// retry deletes it, instead of answering {deleted:0} over an event left
	// on the calendar.
	var errs []string
	for _, b := range blocks {
		if err := client.deleteEvent(ctx, b.GoogleEventID); err != nil && !calendar.IsEventGone(err) {
			errs = append(errs, fmt.Sprintf("delete event %s: %v", b.GoogleEventID, err))
			continue
		}
		if err := storage.DeleteFocusBlockCtx(ctx, e.DB, userID, b.GoogleEventID); err != nil {
			errs = append(errs, fmt.Sprintf("delete db block %s: %v", b.GoogleEventID, err))
		}
	}

	detail, _ := json.Marshal(map[string]interface{}{
		"week_start": monday.Format("2006-01-02"),
		"cleared":    len(blocks),
		"errors":     errs,
	})
	storage.WriteAuditLogCtx(ctx, e.DB, userID, "focus_cleared", string(detail))

	if len(errs) > 0 {
		return len(blocks), fmt.Errorf("partial errors clearing week: %v", errs)
	}
	return len(blocks), nil
}
