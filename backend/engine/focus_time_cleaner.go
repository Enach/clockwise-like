package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Enach/paceday/backend/auth"
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

	var errs []string
	for _, b := range blocks {
		if err := client.deleteEvent(ctx, b.GoogleEventID); err != nil {
			errs = append(errs, fmt.Sprintf("delete event %s: %v", b.GoogleEventID, err))
		}
		if err := storage.DeleteFocusBlock(e.DB, userID, b.GoogleEventID); err != nil {
			errs = append(errs, fmt.Sprintf("delete db block %s: %v", b.GoogleEventID, err))
		}
	}

	detail, _ := json.Marshal(map[string]interface{}{
		"week_start": monday.Format("2006-01-02"),
		"cleared":    len(blocks),
		"errors":     errs,
	})
	storage.WriteAuditLog(e.DB, userID, "focus_cleared", string(detail))

	if len(errs) > 0 {
		return len(blocks), fmt.Errorf("partial errors clearing week: %v", errs)
	}
	return len(blocks), nil
}
