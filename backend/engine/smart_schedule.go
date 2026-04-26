package engine

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Enach/clockwise-like/backend/auth"
	"github.com/Enach/clockwise-like/backend/calendar"
	"github.com/Enach/clockwise-like/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
	"golang.org/x/oauth2"
)

type ScheduleRequest struct {
	DurationMinutes int
	Attendees       []string
	RangeStart      time.Time
	RangeEnd        time.Time
	Title           string
	Description     string
}

type SuggestedSlot struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Score   int       `json:"score"`
	Reasons []string  `json:"reasons"`
}

type ScheduleSuggestions struct {
	Slots []SuggestedSlot `json:"slots"`
}

type SmartScheduler struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
}

func (e *SmartScheduler) calClient(ctx context.Context) (*calendar.CalendarClient, error) {
	token, err := auth.TokenFromDB(e.DB)
	if err != nil || token == nil {
		return nil, fmt.Errorf("not authenticated")
	}
	ts := auth.TokenSource(ctx, e.OAuthConfig, token)
	return calendar.NewClient(ctx, ts)
}

func (e *SmartScheduler) Suggest(ctx context.Context, req ScheduleRequest) (*ScheduleSuggestions, error) {
	s, err := storage.GetSettings(e.DB)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}

	client, err := e.calClient(ctx)
	if err != nil {
		return nil, err
	}

	allEmails := append([]string{client.CalendarID}, req.Attendees...)
	busyMap, err := client.GetFreeBusy(ctx, allEmails, req.RangeStart, req.RangeEnd)
	if err != nil {
		return nil, fmt.Errorf("freebusy: %w", err)
	}

	var allBusy []interval
	for _, slots := range busyMap {
		for _, s := range slots {
			allBusy = append(allBusy, interval{s.Start, s.End})
		}
	}
	allBusy = mergeIntervals(allBusy)

	freeBlocks := subtractIntervals(interval{req.RangeStart, req.RangeEnd}, allBusy)

	dur := time.Duration(req.DurationMinutes) * time.Minute
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}

	focusBlocks, _ := storage.ListFocusBlocksForWeek(e.DB, req.RangeStart)

	var candidates []SuggestedSlot
	for _, free := range freeBlocks {
		t := free.start
		if t.Before(req.RangeStart) {
			t = req.RangeStart
		}
		for !t.Add(dur).After(free.end) {
			end := t.Add(dur)

			workStart := parseHHMM(s.WorkStart, t, loc)
			workEnd := parseHHMM(s.WorkEnd, t, loc)
			if t.Before(workStart) || end.After(workEnd) {
				t = t.Add(30 * time.Minute)
				continue
			}

			score, reasons := scoreCandidate(t, end, focusBlocks, s)
			candidates = append(candidates, SuggestedSlot{
				Start:   t,
				End:     end,
				Score:   score,
				Reasons: reasons,
			})

			t = t.Add(30 * time.Minute)
		}
	}

	sortSlotsByScore(candidates)
	top := pickTopUnique(candidates, 3, time.Hour)

	return &ScheduleSuggestions{Slots: top}, nil
}

func scoreCandidate(start, end time.Time, focusBlocks []storage.FocusBlock, s *storage.Settings) (int, []string) {
	score := 0
	var reasons []string

	hour := start.Hour()
	wd := start.Weekday()

	if hour >= 9 && hour < 12 {
		score += 40
		reasons = append(reasons, "Morning slot preferred for important meetings")
	} else if hour >= 14 && hour < 16 {
		score += 20
		reasons = append(reasons, "Early afternoon slot")
	}

	for _, fb := range focusBlocks {
		if start.Before(fb.EndTime) && end.After(fb.StartTime) {
			score -= 20
			reasons = append(reasons, "Overlaps with focus block")
			break
		}
	}

	if wd == time.Friday && hour >= 14 {
		score -= 10
		reasons = append(reasons, "Friday afternoon — low energy")
	}

	if wd == time.Monday || wd == time.Tuesday || wd == time.Wednesday {
		score += 10
		reasons = append(reasons, "Earlier in week preferred for planning")
	}

	return score, reasons
}

func sortSlotsByScore(slots []SuggestedSlot) {
	for i := 1; i < len(slots); i++ {
		for j := i; j > 0 && slots[j].Score > slots[j-1].Score; j-- {
			slots[j], slots[j-1] = slots[j-1], slots[j]
		}
	}
}

func pickTopUnique(slots []SuggestedSlot, n int, minGap time.Duration) []SuggestedSlot {
	var result []SuggestedSlot
	for _, s := range slots {
		if len(result) >= n {
			break
		}
		tooClose := false
		for _, picked := range result {
			diff := s.Start.Sub(picked.Start)
			if diff < 0 {
				diff = -diff
			}
			if diff < minGap {
				tooClose = true
				break
			}
		}
		if !tooClose {
			result = append(result, s)
		}
	}
	return result
}

func (e *SmartScheduler) CreateMeeting(ctx context.Context, req ScheduleRequest, slot SuggestedSlot) (*googlecalendar.Event, error) {
	client, err := e.calClient(ctx)
	if err != nil {
		return nil, err
	}

	attendees := make([]*googlecalendar.EventAttendee, len(req.Attendees))
	for i, email := range req.Attendees {
		attendees[i] = &googlecalendar.EventAttendee{Email: email}
	}

	event := &googlecalendar.Event{
		Summary:     req.Title,
		Description: req.Description,
		Start:       &googlecalendar.EventDateTime{DateTime: slot.Start.Format(time.RFC3339)},
		End:         &googlecalendar.EventDateTime{DateTime: slot.End.Format(time.RFC3339)},
		Attendees:   attendees,
	}

	created, err := client.CreateEvent(ctx, client.CalendarID, event)
	if err != nil {
		return nil, err
	}

	storage.WriteAuditLog(e.DB, "meeting_scheduled", `{"event_id":"`+created.Id+`","title":"`+req.Title+`"}`)
	return created, nil
}
