package engine

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/storage"
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
	// From NLP result — boost/penalize candidate slots accordingly
	PreferredTimes []string // e.g. ["10:00-12:00"]
	AvoidTimes     []string // e.g. ["08:00-09:00"]
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
	calOps      calendarOps
}

func (e *SmartScheduler) calClient(ctx context.Context) (calendarOps, error) {
	if e.calOps != nil {
		return e.calOps, nil
	}
	return newCalOps(ctx, e.DB, e.OAuthConfig)
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

	allEmails := append([]string{client.calendarID()}, req.Attendees...)
	busyMap, err := client.getFreeBusy(ctx, allEmails, req.RangeStart, req.RangeEnd)
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
			score, reasons = adjustScoreForPreferences(score, reasons, t, loc, req.PreferredTimes, req.AvoidTimes)
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

func adjustScoreForPreferences(score int, reasons []string, t time.Time, loc *time.Location, preferred, avoid []string) (int, []string) {
	local := t.In(loc)
	h, m := local.Hour(), local.Minute()
	for _, r := range preferred {
		if timeInHHMMRange(h, m, r) {
			score += 25
			reasons = append(reasons, "Preferred time window")
		}
	}
	for _, r := range avoid {
		if timeInHHMMRange(h, m, r) {
			score -= 40
			reasons = append(reasons, "Avoid time window")
		}
	}
	return score, reasons
}

func timeInHHMMRange(h, m int, hhmmRange string) bool {
	parts := strings.SplitN(hhmmRange, "-", 2)
	if len(parts) != 2 {
		return false
	}
	sh, sm := parseHHMMParts(parts[0])
	eh, em := parseHHMMParts(parts[1])
	cur := h*60 + m
	return cur >= sh*60+sm && cur < eh*60+em
}

func parseHHMMParts(hhmm string) (int, int) {
	hhmm = strings.TrimSpace(hhmm)
	if len(hhmm) < 5 || hhmm[2] != ':' {
		return 0, 0
	}
	h := int(hhmm[0]-'0')*10 + int(hhmm[1]-'0')
	m := int(hhmm[3]-'0')*10 + int(hhmm[4]-'0')
	return h, m
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

	created, err := client.createEvent(ctx, event)
	if err != nil {
		return nil, err
	}

	storage.WriteAuditLog(e.DB, "meeting_scheduled", `{"event_id":"`+created.Id+`","title":"`+req.Title+`"}`)
	return created, nil
}
