package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Enach/clockwise-like/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
	"golang.org/x/oauth2"
)

type FocusBlock struct {
	GoogleEventID string
	Start         time.Time
	End           time.Time
	Date          string
}

type FocusRunResult struct {
	WeekStart     time.Time    `json:"weekStart"`
	CreatedBlocks []FocusBlock `json:"createdBlocks"`
	SkippedDays   []string     `json:"skippedDays"`
	TotalMinutes  int          `json:"totalMinutes"`
	Errors        []string     `json:"errors"`
}

type interval struct {
	start time.Time
	end   time.Time
}

type scoredSlot struct {
	iv    interval
	score int
}

type FocusTimeEngine struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	calOps      calendarOps
}

func (e *FocusTimeEngine) calClient(ctx context.Context) (calendarOps, error) {
	if e.calOps != nil {
		return e.calOps, nil
	}
	return newCalOps(ctx, e.DB, e.OAuthConfig)
}

func (e *FocusTimeEngine) Run(ctx context.Context, targetWeek time.Time) (*FocusRunResult, error) {
	s, err := storage.GetSettings(e.DB)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}

	client, err := e.calClient(ctx)
	if err != nil {
		return nil, err
	}

	monday := startOfWeek(targetWeek)
	result := &FocusRunResult{WeekStart: monday}

	for i := 0; i < 5; i++ {
		day := monday.AddDate(0, 0, i)
		dateStr := day.Format("2006-01-02")

		if err := e.processDay(ctx, client, s, day, dateStr, result); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", dateStr, err))
		}
	}

	detail, _ := json.Marshal(map[string]interface{}{
		"week_start":    monday.Format("2006-01-02"),
		"created":       len(result.CreatedBlocks),
		"total_minutes": result.TotalMinutes,
	})
	storage.WriteAuditLog(e.DB, "focus_created", string(detail))

	return result, nil
}

func (e *FocusTimeEngine) processDay(ctx context.Context, client calendarOps, s *storage.Settings, day time.Time, dateStr string, result *FocusRunResult) error {
	existingMinutes, _ := storage.FocusMinutesForDay(e.DB, dateStr)
	if existingMinutes >= s.FocusDailyTargetMinutes {
		result.SkippedDays = append(result.SkippedDays, dateStr)
		return nil
	}
	remaining := s.FocusDailyTargetMinutes - existingMinutes

	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}

	workStart := parseHHMM(s.WorkStart, day, loc)
	workEnd := parseHHMM(s.WorkEnd, day, loc)

	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	events, err := client.listEvents(ctx, dayStart, dayEnd)
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}

	busy := buildBusy(events, workStart, workEnd, s, day, loc)
	free := subtractIntervals(interval{workStart, workEnd}, busy)

	var candidates []scoredSlot
	for _, slot := range free {
		dur := int(slot.end.Sub(slot.start).Minutes())
		if dur < s.FocusMinBlockMinutes {
			continue
		}
		ss := scoredSlot{iv: slot}
		ss.score = scoreSlot(ss, day)
		candidates = append(candidates, ss)
	}

	sortByScore(candidates)

	for _, cs := range candidates {
		if remaining <= 0 {
			break
		}

		blockDur := int(cs.iv.end.Sub(cs.iv.start).Minutes())
		if blockDur > s.FocusMaxBlockMinutes {
			blockDur = s.FocusMaxBlockMinutes
		}

		blockStart := cs.iv.start.Add(time.Duration(s.BufferBeforeMinutes) * time.Minute)
		blockEnd := blockStart.Add(time.Duration(blockDur) * time.Minute)
		if blockEnd.After(cs.iv.end.Add(-time.Duration(s.BufferAfterMinutes) * time.Minute)) {
			blockEnd = cs.iv.end.Add(-time.Duration(s.BufferAfterMinutes) * time.Minute)
		}
		if int(blockEnd.Sub(blockStart).Minutes()) < s.FocusMinBlockMinutes {
			continue
		}

		event := &googlecalendar.Event{
			Summary:      s.FocusLabel,
			Description:  "Scheduled by Clockwise-like",
			Transparency: "transparent",
			Visibility:   "public",
			ColorId:      colorIDFromHex(s.FocusColor),
			Start:        &googlecalendar.EventDateTime{DateTime: blockStart.Format(time.RFC3339)},
			End:          &googlecalendar.EventDateTime{DateTime: blockEnd.Format(time.RFC3339)},
		}

		created, err := client.createEvent(ctx, event)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("create event %s: %v", dateStr, err))
			continue
		}

		if err := storage.SaveFocusBlock(e.DB, created.Id, dateStr, blockStart, blockEnd); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("save block %s: %v", dateStr, err))
		}

		actualDur := int(blockEnd.Sub(blockStart).Minutes())
		result.CreatedBlocks = append(result.CreatedBlocks, FocusBlock{
			GoogleEventID: created.Id,
			Start:         blockStart,
			End:           blockEnd,
			Date:          dateStr,
		})
		result.TotalMinutes += actualDur
		remaining -= actualDur
	}

	return nil
}

func buildBusy(events []*googlecalendar.Event, workStart, workEnd time.Time, s *storage.Settings, day time.Time, loc *time.Location) []interval {
	var busy []interval

	if s.ProtectLunch && s.LunchStart != "" && s.LunchEnd != "" {
		ls := parseHHMM(s.LunchStart, day, loc)
		le := parseHHMM(s.LunchEnd, day, loc)
		busy = append(busy, interval{ls, le})
	}

	for _, ev := range events {
		if ev.Transparency == "transparent" {
			continue
		}
		if ev.Start == nil || ev.End == nil {
			continue
		}
		start, err1 := time.Parse(time.RFC3339, ev.Start.DateTime)
		end, err2 := time.Parse(time.RFC3339, ev.End.DateTime)
		if err1 != nil || err2 != nil {
			continue
		}
		if end.Before(workStart) || start.After(workEnd) {
			continue
		}
		if start.Before(workStart) {
			start = workStart
		}
		if end.After(workEnd) {
			end = workEnd
		}
		busy = append(busy, interval{start, end})
	}

	return mergeIntervals(busy)
}

func subtractIntervals(whole interval, busy []interval) []interval {
	free := []interval{whole}
	for _, b := range busy {
		var next []interval
		for _, f := range free {
			if b.end.Before(f.start) || b.start.After(f.end) {
				next = append(next, f)
				continue
			}
			if f.start.Before(b.start) {
				next = append(next, interval{f.start, b.start})
			}
			if b.end.Before(f.end) {
				next = append(next, interval{b.end, f.end})
			}
		}
		free = next
	}
	return free
}

func mergeIntervals(ivs []interval) []interval {
	if len(ivs) == 0 {
		return nil
	}
	sortIntervals(ivs)
	merged := []interval{ivs[0]}
	for _, iv := range ivs[1:] {
		last := &merged[len(merged)-1]
		if !iv.start.After(last.end) {
			if iv.end.After(last.end) {
				last.end = iv.end
			}
		} else {
			merged = append(merged, iv)
		}
	}
	return merged
}

func sortIntervals(ivs []interval) {
	for i := 1; i < len(ivs); i++ {
		for j := i; j > 0 && ivs[j].start.Before(ivs[j-1].start); j-- {
			ivs[j], ivs[j-1] = ivs[j-1], ivs[j]
		}
	}
}

func sortByScore(slots []scoredSlot) {
	for i := 1; i < len(slots); i++ {
		for j := i; j > 0 && slots[j].score > slots[j-1].score; j-- {
			slots[j], slots[j-1] = slots[j-1], slots[j]
		}
	}
}

func scoreSlot(s scoredSlot, day time.Time) int {
	score := 0
	hour := s.iv.start.Hour()
	if hour < 12 {
		score += 30
	}
	if hour >= 14 && hour < 17 {
		score += 20
	}
	dur := int(s.iv.end.Sub(s.iv.start).Minutes())
	score += (dur / 30) * 10
	if day.Weekday() == time.Monday && hour < 10 {
		score -= 10
	}
	return score
}

func parseHHMM(hhmm string, day time.Time, loc *time.Location) time.Time {
	var h, m int
	fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
}

func startOfWeek(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-wd+1, 0, 0, 0, 0, t.Location())
}

func colorIDFromHex(_ string) string {
	return "7"
}
