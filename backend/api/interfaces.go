package api

import (
	"context"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	googlecalendar "google.golang.org/api/calendar/v3"
)

type FocusEngine interface {
	Run(ctx context.Context, targetWeek time.Time) (*engine.FocusRunResult, error)
	ClearWeek(ctx context.Context, targetWeek time.Time) (int, error)
}

type Compressor interface {
	SuggestForDay(ctx context.Context, date time.Time) (*engine.CompressionResult, error)
	Apply(ctx context.Context, proposals []engine.MoveProposal) (applied []string, failed []string, err error)
}

type Scheduler interface {
	Suggest(ctx context.Context, req engine.ScheduleRequest) (*engine.ScheduleSuggestions, error)
	CreateMeeting(ctx context.Context, req engine.ScheduleRequest, slot engine.SuggestedSlot) (*googlecalendar.Event, error)
}

type NLPParser interface {
	Parse(ctx context.Context, text string) (*nlp.ParseResult, error)
}
