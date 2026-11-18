package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func buildServer(c *backendClient) *server.MCPServer {
	s := server.NewMCPServer("Paceday", "1.0.0")

	// ── Calendar ──────────────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("clockwise_list_events",
		mcp.WithDescription("List Google Calendar events for a date range"),
		mcp.WithString("start", mcp.Required(), mcp.Description("Range start in RFC3339 format")),
		mcp.WithString("end", mcp.Required(), mcp.Description("Range end in RFC3339 format")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := url.Values{}
		q.Set("start", strArg(req, "start"))
		q.Set("end", strArg(req, "end"))
		res, err := c.get(ctx, "/api/calendar/events", q)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_create_event",
		mcp.WithDescription(createEventDescription),
		mcp.WithString("title", mcp.Required(), mcp.Description("Meeting title")),
		mcp.WithString("attendees", mcp.Required(), mcp.Description("Comma-separated attendee emails")),
		mcp.WithString("duration", mcp.Description("Duration in minutes (default 30)")),
		mcp.WithString("preferred_date", mcp.Description("Preferred date in YYYY-MM-DD format")),
		mcp.WithString("location", mcp.Description("Meeting location or conferencing URL")),
		mcp.WithString("start", mcp.Description("Book exactly this start (RFC3339), as given in a previous answer; it is checked again before booking")),
		mcp.WithString("accept_unknown", mcp.Description(`"true" to book past the attendees named in accepted_unknown, whose availability could not be checked`)),
		mcp.WithString("accepted_unknown", mcp.Description("Comma-separated attendees whose unknown availability is accepted, as listed in the previous answer")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return createEvent(ctx, c, req)
	})

	s.AddTool(mcp.NewTool("clockwise_update_event",
		mcp.WithDescription("Partially update an existing calendar event by ID"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Google Calendar event ID")),
		mcp.WithString("title", mcp.Description("New title")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("location", mcp.Description("New location")),
		mcp.WithString("start", mcp.Description("New start time in RFC3339")),
		mcp.WithString("end", mcp.Description("New end time in RFC3339")),
		mcp.WithString("attendees", mcp.Description("Comma-separated attendee emails")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := strArg(req, "id")
		payload := map[string]any{}
		if v := strArg(req, "title"); v != "" {
			payload["title"] = v
		}
		if v := strArg(req, "description"); v != "" {
			payload["description"] = v
		}
		if v := strArg(req, "location"); v != "" {
			payload["location"] = v
		}
		if v := strArg(req, "start"); v != "" {
			payload["start"] = v
		}
		if v := strArg(req, "end"); v != "" {
			payload["end"] = v
		}
		if v := strArg(req, "attendees"); v != "" {
			payload["attendees"] = splitEmails(v)
		}
		res, err := c.patch(ctx, "/api/events/"+id, payload)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_delete_event",
		mcp.WithDescription("Delete a calendar event by ID"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Google Calendar event ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := strArg(req, "id")
		err := c.delete(ctx, "/api/events/"+id)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error: %v", err)), nil
		}
		return mcp.NewToolResultText("Event deleted"), nil
	})

	s.AddTool(mcp.NewTool("clockwise_find_free_slots",
		mcp.WithDescription(findFreeSlotsDescription),
		mcp.WithString("start", mcp.Required(), mcp.Description("Range start in RFC3339")),
		mcp.WithString("end", mcp.Required(), mcp.Description("Range end in RFC3339")),
		mcp.WithString("attendees", mcp.Description("Comma-separated attendee emails (optional); the user's own calendar is always read")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := url.Values{}
		q.Set("start", strArg(req, "start"))
		q.Set("end", strArg(req, "end"))
		// PAC-133 §6.1: the named attendees pass through; the user's own
		// calendar is never named here, the backend always adds it.
		if attendees := splitEmails(strArg(req, "attendees")); len(attendees) > 0 {
			q.Set("attendees", strings.Join(attendees, ","))
		}
		res, err := c.get(ctx, "/api/calendar/freebusy", q)
		return jsonResult(res, err)
	})

	// ── Focus ─────────────────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("clockwise_run_focus_engine",
		mcp.WithDescription("Run the focus time engine to schedule focus blocks based on current settings"),
	), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.post(ctx, "/api/focus/run", nil)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_list_focus_blocks",
		mcp.WithDescription("List all currently scheduled focus time blocks"),
	), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.get(ctx, "/api/focus/blocks", nil)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_clear_focus_blocks",
		mcp.WithDescription("Delete all focus time blocks from the calendar"),
	), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		err := c.delete(ctx, "/api/focus/blocks")
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error: %v", err)), nil
		}
		return mcp.NewToolResultText("Focus blocks cleared"), nil
	})

	// ── Scheduling ────────────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("clockwise_compress_schedule",
		mcp.WithDescription("Analyse the calendar and produce a compression plan that moves meetings to create larger free blocks"),
		mcp.WithString("date", mcp.Description("Date to compress in YYYY-MM-DD format (default: today)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		payload := map[string]any{}
		if v := strArg(req, "date"); v != "" {
			payload["date"] = v
		}
		res, err := c.post(ctx, "/api/schedule/compress", payload)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_apply_compression",
		mcp.WithDescription("Apply a compression plan returned by clockwise_compress_schedule"),
		mcp.WithString("plan", mcp.Required(), mcp.Description("JSON compression plan as returned by clockwise_compress_schedule")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		proposals, err := decodeCompressionProposals(strArg(req, "plan"))
		if err != nil {
			return jsonResult(nil, err)
		}
		res, err := c.post(ctx, "/api/schedule/compress/apply", map[string]any{
			"proposals": proposals,
		})
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_parse_command",
		mcp.WithDescription("Parse a natural-language scheduling command and return a structured action plan"),
		mcp.WithString("command", mcp.Required(), mcp.Description("Natural-language command, e.g. \"schedule a 30-min call with alice@example.com next Tuesday\"")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.post(ctx, "/api/nlp/parse", map[string]any{
			"text": strArg(req, "command"),
		})
		return jsonResult(res, err)
	})

	// ── Rooms & Attendees ─────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("clockwise_search_rooms",
		mcp.WithDescription("Search for bookable conference rooms visible in Google Calendar"),
		mcp.WithString("q", mcp.Description("Optional name prefix to filter rooms")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := url.Values{}
		if v := strArg(req, "q"); v != "" {
			q.Set("q", v)
		}
		res, err := c.get(ctx, "/api/rooms", q)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_suggest_attendees",
		mcp.WithDescription("Suggest attendees based on recent calendar history"),
		mcp.WithString("q", mcp.Description("Optional email/name prefix to filter suggestions")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := url.Values{}
		if v := strArg(req, "q"); v != "" {
			q.Set("q", v)
		}
		res, err := c.get(ctx, "/api/attendees/suggest", q)
		return jsonResult(res, err)
	})

	// ── Settings & Status ─────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("clockwise_get_settings",
		mcp.WithDescription("Retrieve the current user settings (work hours, focus preferences, conferencing provider, etc.)"),
	), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.get(ctx, "/api/settings/", nil)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_calendar_status",
		mcp.WithDescription("Check which calendar providers are connected (Google, Microsoft, etc.)"),
	), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.get(ctx, "/api/auth/status", nil)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_suggest_meeting",
		mcp.WithDescription(suggestMeetingDescription),
		mcp.WithString("title", mcp.Required(), mcp.Description("Meeting title")),
		mcp.WithString("attendees", mcp.Required(), mcp.Description("Comma-separated attendee emails")),
		mcp.WithString("duration", mcp.Description("Duration in minutes (default 30)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		duration := parseDuration(strArg(req, "duration"), 30)
		rangeStart, rangeEnd, err := schedulingRange("")
		if err != nil {
			return jsonResult(nil, err)
		}
		payload := map[string]any{
			"title":            strArg(req, "title"),
			"attendees":        splitEmails(strArg(req, "attendees")),
			"duration_minutes": duration,
			"range_start":      rangeStart,
			"range_end":        rangeEnd,
		}
		res, err := c.post(ctx, "/api/schedule/suggest", payload)
		return jsonResult(res, err)
	})

	return s
}

type compressionProposal struct {
	EventID       string `json:"event_id"`
	ProposedStart string `json:"proposed_start"`
	ProposedEnd   string `json:"proposed_end"`
}

func parseDuration(value string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 {
		return n
	}
	return fallback
}

func splitEmails(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if email := strings.TrimSpace(part); email != "" {
			out = append(out, email)
		}
	}
	return out
}

func schedulingRange(preferredDate string) (time.Time, time.Time, error) {
	if preferredDate != "" {
		day, err := time.Parse("2006-01-02", preferredDate)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("preferred_date must be YYYY-MM-DD: %w", err)
		}
		return day, day.Add(24 * time.Hour), nil
	}
	start := time.Now().UTC()
	return start, start.Add(7 * 24 * time.Hour), nil
}

func decodeCompressionProposals(raw string) ([]compressionProposal, error) {
	var direct struct {
		Proposals []compressionProposal `json:"proposals"`
	}
	if err := json.Unmarshal([]byte(raw), &direct); err == nil && direct.Proposals != nil {
		return direct.Proposals, nil
	}
	var results []struct {
		Proposals []compressionProposal `json:"proposals"`
	}
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		return nil, fmt.Errorf("plan must be compression JSON: %w", err)
	}
	var proposals []compressionProposal
	for _, result := range results {
		proposals = append(proposals, result.Proposals...)
	}
	if proposals == nil {
		return nil, fmt.Errorf("plan does not contain proposals")
	}
	return proposals, nil
}

func strArg(req mcp.CallToolRequest, key string) string {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func jsonResult(data []byte, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("Error: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// PAC-133 §6: the tool texts are binding (contracts/features/PAC-133.md).

const findFreeSlotsDescription = `Read free/busy for a date range: the user's own calendar (always) and any attendees given. Each calendar is "known" (its busy windows are complete) or "unknown". Unknown means not checked, never free: the person may be busy at times not listed. Busy windows shown for an unknown calendar are real and still block.`

const createEventDescription = `Schedule a meeting with the smart scheduler. If every attendee's calendar was checked, the first suggested slot is booked. If some attendees' availability is unknown (their calendar could not be checked), nothing is booked: the answer gives the exact proposed start and end and the unknown attendees. To book that slot anyway, call again with the same title, attendees and duration, plus start (the proposed start), accept_unknown="true" and accepted_unknown (the unknown attendees listed). Pass only a start (and the duration) from a previous answer: the slot is checked again before booking, and a start the scheduler would not propose is refused.`

const suggestMeetingDescription = `Find the best meeting slots for a set of attendees without creating the event. The answer lists unknown_attendees: attendees whose calendars could not be checked. Unknown means not checked, never free.`

const (
	refuseCouldNotCheck = "Not booked: that time could not be checked — ask again (call clockwise_create_event without start)."
	refuseNoLongerFree  = "Not booked: that time is no longer available — ask again (call clockwise_create_event without start)."
)

func refuseUnknownGrew(names []string) string {
	return "Not booked: the availability of " + strings.Join(names, ", ") + " could not be checked — ask again (call clockwise_create_event without start)."
}

// firstCallText is the answer that books nothing past unknown attendees: the
// exact slot, the names, and how to accept (PAC-133 §6.2 A.3).
func firstCallText(unknown []string, start, end time.Time) string {
	s, e := start.Format(time.RFC3339), end.Format(time.RFC3339)
	return "Not booked yet: the availability of these attendees could not be checked (unknown, not free): " + strings.Join(unknown, ", ") + ".\n" +
		"Proposed slot: start " + s + ", end " + e + ".\n" +
		`To book this exact slot anyway, call clockwise_create_event again with the same title, attendees and duration, start="` + s +
		`", accept_unknown="true" and accepted_unknown="` + strings.Join(unknown, ",") + `".`
}

// suggestions is the part of POST /api/schedule/suggest's answer the tool reads.
type suggestions struct {
	Slots []struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"slots"`
	UnknownAttendees []string `json:"unknown_attendees"`
}

// createEvent is clockwise_create_event (PAC-133 §6.2). Without start it
// books the first slot only when every attendee was checked; otherwise it
// books nothing and says how to accept. With start it re-checks exactly
// [start, start+duration] through the scheduler and books that slot only if
// the scheduler still offers it first, the unknown attendees are all
// accepted, and accept_unknown is "true".
func createEvent(ctx context.Context, c *backendClient, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title := strArg(req, "title")
	attendees := splitEmails(strArg(req, "attendees"))
	duration := parseDuration(strArg(req, "duration"), 30)
	suggest := func(rangeStart, rangeEnd time.Time) (suggestions, error) {
		var out suggestions
		res, err := c.post(ctx, "/api/schedule/suggest", map[string]any{
			"title":            title,
			"attendees":        attendees,
			"duration_minutes": duration,
			"range_start":      rangeStart,
			"range_end":        rangeEnd,
		})
		if err != nil {
			return out, err
		}
		if err := json.Unmarshal(res, &out); err != nil {
			return out, fmt.Errorf("decode schedule suggestions: %w", err)
		}
		return out, nil
	}
	book := func(start, end time.Time) (*mcp.CallToolResult, error) {
		payload := map[string]any{"title": title, "attendees": attendees, "start": start, "end": end}
		if v := strArg(req, "location"); v != "" {
			payload["location"] = v
		}
		created, err := c.post(ctx, "/api/schedule/create", payload)
		return jsonResult(created, err)
	}

	startArg := strings.TrimSpace(strArg(req, "start"))
	if startArg == "" {
		// A: whatever accept_unknown says, nothing is booked past an unknown
		// attendee without the exact start.
		rangeStart, rangeEnd, err := schedulingRange(strArg(req, "preferred_date"))
		if err != nil {
			return jsonResult(nil, err)
		}
		got, err := suggest(rangeStart, rangeEnd)
		if err != nil {
			return jsonResult(nil, err)
		}
		if len(got.Slots) == 0 {
			return jsonResult(nil, fmt.Errorf("no available slot found"))
		}
		first := got.Slots[0]
		if len(got.UnknownAttendees) > 0 {
			return mcp.NewToolResultText(firstCallText(got.UnknownAttendees, first.Start, first.End)), nil
		}
		return book(first.Start, first.End)
	}

	// B: re-check exactly the slot given.
	start, err := time.Parse(time.RFC3339, startArg)
	if err != nil {
		return jsonResult(nil, fmt.Errorf("start must be RFC3339"))
	}
	end := start.Add(time.Duration(duration) * time.Minute)
	got, err := suggest(start, end)
	if err != nil {
		return mcp.NewToolResultText(refuseCouldNotCheck), nil
	}
	if len(got.Slots) == 0 || !got.Slots[0].Start.Equal(start) {
		return mcp.NewToolResultText(refuseNoLongerFree), nil
	}
	accepted := map[string]bool{}
	for _, a := range splitEmails(strArg(req, "accepted_unknown")) {
		accepted[strings.ToLower(a)] = true
	}
	var notAccepted []string
	for _, u := range got.UnknownAttendees {
		if !accepted[strings.ToLower(u)] {
			notAccepted = append(notAccepted, u)
		}
	}
	if len(notAccepted) > 0 {
		return mcp.NewToolResultText(refuseUnknownGrew(notAccepted)), nil
	}
	if len(got.UnknownAttendees) > 0 && !strings.EqualFold(strings.TrimSpace(strArg(req, "accept_unknown")), "true") {
		return mcp.NewToolResultText(firstCallText(got.UnknownAttendees, start, end)), nil
	}
	return book(start, end)
}
