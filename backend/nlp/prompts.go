package nlp

import (
	"bytes"
	"text/template"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
)

// SchedulingPromptData holds all runtime variables injected into the scheduling system prompt.
type SchedulingPromptData struct {
	Today         string
	TodayISO      string
	Timezone      string
	WorkStart     string
	WorkEnd       string
	WeekStart     string
	WeekEnd       string
	NextWeekStart string
	NextWeekEnd   string
	Tomorrow      string
	Participants  []ParticipantSummary
}

// ParticipantSummary is the per-participant view used in the scheduling prompt.
type ParticipantSummary struct {
	Email     string
	Timezone  string
	WorkStart string
	WorkEnd   string
	LocalNow  string // e.g. "14:30 Tuesday"
}

// SchedulingSystemPromptTmpl is the rich timezone-aware scheduling prompt template.
const SchedulingSystemPromptTmpl = `You are an intelligent calendar scheduling assistant. Your job is to parse natural language scheduling requests and return structured JSON.

## Current context

- Today: {{.Today}} ({{.TodayISO}})
- User's timezone: {{.Timezone}}
- User's working hours: {{.WorkStart}}–{{.WorkEnd}} {{.Timezone}}
- This week: Monday {{.WeekStart}} to Friday {{.WeekEnd}}
- Next week: Monday {{.NextWeekStart}} to Friday {{.NextWeekEnd}}
- Tomorrow: {{.Tomorrow}}

## Participants in this request
{{- if .Participants}}
The following people may be involved. Respect their working hours and timezones when choosing times.
{{range .Participants}}
- {{.Email}}
  - Timezone: {{if .Timezone}}{{.Timezone}}{{else}}unknown (assume similar to user){{end}}
  - Working hours: {{if .WorkStart}}{{.WorkStart}}–{{.WorkEnd}} local time{{else}}unknown (assume 09:00–18:00 local){{end}}
  - Their current local time: {{if .LocalNow}}{{.LocalNow}}{{else}}unknown{{end}}
{{end}}
{{- else}}
No specific participants identified yet.
{{- end}}

## Scheduling rules you must follow

1. Never suggest times outside ANY participant's working hours (use 09:00–18:00 if unknown).
2. Never suggest times before 08:00 or after 20:00 in any participant's local timezone.
3. Prefer times that fall within "golden hours" for ALL participants: 10:00–12:00 or 14:00–16:00 local time.
4. If participants are in very different timezones (>6h apart), find the overlapping window and note the trade-off in constraints.
5. "This week" means {{.WeekStart}} to {{.WeekEnd}}. "Next week" means {{.NextWeekStart}} to {{.NextWeekEnd}}.
6. If no good overlap exists between working hours, set constraints to explain the issue and suggest the best compromise.

## Output format

Return ONLY valid JSON — no prose, no markdown, no explanation.

For a meeting scheduling request:
{
  "intent": "schedule_meeting",
  "title": "<inferred meeting title, or null>",
  "duration_minutes": <integer>,
  "attendees": ["email@example.com"],
  "range_start": "<YYYY-MM-DD>",
  "range_end": "<YYYY-MM-DD>",
  "preferred_times": ["HH:MM-HH:MM"],
  "avoid_times": ["HH:MM-HH:MM"],
  "constraints": "<human-readable note about timezone trade-offs or null>",
  "timezone_notes": "<e.g. 'Alice is UTC-5, a 10am Paris time meeting is 4am for her — avoid' or null>"
}

For a focus time request:
{
  "intent": "schedule_focus",
  "duration_minutes": <integer>,
  "range_start": "<YYYY-MM-DD>",
  "range_end": "<YYYY-MM-DD>"
}

For anything unclear:
{
  "intent": "unknown",
  "error": "<why you could not parse this>"
}`

// BuildSchedulingPromptData constructs the template data from settings and participant info.
func BuildSchedulingPromptData(s *storage.Settings, participants []calendar.ParticipantInfo, now time.Time) *SchedulingPromptData {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}
	local := now.In(loc)
	monday := startOfWeek(local)
	friday := monday.AddDate(0, 0, 4)
	nextMonday := monday.AddDate(0, 0, 7)
	nextFriday := nextMonday.AddDate(0, 0, 4)

	summaries := make([]ParticipantSummary, 0, len(participants))
	for _, p := range participants {
		ps := ParticipantSummary{
			Email:     p.Email,
			Timezone:  p.Timezone,
			WorkStart: p.WorkStart,
			WorkEnd:   p.WorkEnd,
		}
		if p.Timezone != "" {
			pLoc, err := time.LoadLocation(p.Timezone)
			if err == nil {
				pLocal := now.In(pLoc)
				ps.LocalNow = pLocal.Format("15:04 Monday")
			}
		}
		summaries = append(summaries, ps)
	}

	return &SchedulingPromptData{
		Today:         local.Format("Monday, January 2, 2006"),
		TodayISO:      local.Format("2006-01-02"),
		Timezone:      s.Timezone,
		WorkStart:     s.WorkStart,
		WorkEnd:       s.WorkEnd,
		WeekStart:     monday.Format("2006-01-02"),
		WeekEnd:       friday.Format("2006-01-02"),
		NextWeekStart: nextMonday.Format("2006-01-02"),
		NextWeekEnd:   nextFriday.Format("2006-01-02"),
		Tomorrow:      local.AddDate(0, 0, 1).Format("2006-01-02"),
		Participants:  summaries,
	}
}

func renderSchedulingPrompt(data *SchedulingPromptData) (string, error) {
	tmpl, err := template.New("scheduling").Parse(SchedulingSystemPromptTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func startOfWeek(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-wd+1, 0, 0, 0, 0, t.Location())
}
