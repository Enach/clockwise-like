package main

import (
	"context"
	"fmt"
	"net/url"

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
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := url.Values{}
		q.Set("start", strArg(req, "start"))
		q.Set("end", strArg(req, "end"))
		res, err := c.get("/api/calendar/events", q)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_create_event",
		mcp.WithDescription("Schedule a meeting using the smart scheduler (finds a free slot and creates the event)"),
		mcp.WithString("title", mcp.Required(), mcp.Description("Meeting title")),
		mcp.WithString("attendees", mcp.Required(), mcp.Description("Comma-separated attendee emails")),
		mcp.WithString("duration", mcp.Description("Duration in minutes (default 30)")),
		mcp.WithString("preferred_date", mcp.Description("Preferred date in YYYY-MM-DD format")),
		mcp.WithString("location", mcp.Description("Meeting location or conferencing URL")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		payload := map[string]any{
			"title":     strArg(req, "title"),
			"attendees": strArg(req, "attendees"),
		}
		if v := strArg(req, "duration"); v != "" {
			payload["duration"] = v
		}
		if v := strArg(req, "preferred_date"); v != "" {
			payload["preferredDate"] = v
		}
		if v := strArg(req, "location"); v != "" {
			payload["location"] = v
		}
		res, err := c.post("/api/schedule/create", payload)
		return jsonResult(res, err)
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
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
			payload["attendees"] = v
		}
		res, err := c.patch("/api/events/"+id, payload)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_delete_event",
		mcp.WithDescription("Delete a calendar event by ID"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Google Calendar event ID")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := strArg(req, "id")
		err := c.delete("/api/events/" + id)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error: %v", err)), nil
		}
		return mcp.NewToolResultText("Event deleted"), nil
	})

	s.AddTool(mcp.NewTool("clockwise_find_free_slots",
		mcp.WithDescription("Find free/busy windows in the calendar for a date range"),
		mcp.WithString("start", mcp.Required(), mcp.Description("Range start in RFC3339")),
		mcp.WithString("end", mcp.Required(), mcp.Description("Range end in RFC3339")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := url.Values{}
		q.Set("start", strArg(req, "start"))
		q.Set("end", strArg(req, "end"))
		res, err := c.get("/api/calendar/freebusy", q)
		return jsonResult(res, err)
	})

	// ── Focus ─────────────────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("clockwise_run_focus_engine",
		mcp.WithDescription("Run the focus time engine to schedule focus blocks based on current settings"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.post("/api/focus/run", nil)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_list_focus_blocks",
		mcp.WithDescription("List all currently scheduled focus time blocks"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.get("/api/focus/blocks", nil)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_clear_focus_blocks",
		mcp.WithDescription("Delete all focus time blocks from the calendar"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		err := c.delete("/api/focus/blocks")
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error: %v", err)), nil
		}
		return mcp.NewToolResultText("Focus blocks cleared"), nil
	})

	// ── Scheduling ────────────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("clockwise_compress_schedule",
		mcp.WithDescription("Analyse the calendar and produce a compression plan that moves meetings to create larger free blocks"),
		mcp.WithString("date", mcp.Description("Date to compress in YYYY-MM-DD format (default: today)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		payload := map[string]any{}
		if v := strArg(req, "date"); v != "" {
			payload["date"] = v
		}
		res, err := c.post("/api/schedule/compress", payload)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_apply_compression",
		mcp.WithDescription("Apply a compression plan returned by clockwise_compress_schedule"),
		mcp.WithString("plan", mcp.Required(), mcp.Description("JSON compression plan as returned by clockwise_compress_schedule")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.post("/api/schedule/compress/apply", map[string]any{
			"plan": strArg(req, "plan"),
		})
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_parse_command",
		mcp.WithDescription("Parse a natural-language scheduling command and return a structured action plan"),
		mcp.WithString("command", mcp.Required(), mcp.Description("Natural-language command, e.g. \"schedule a 30-min call with alice@example.com next Tuesday\"")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.post("/api/nlp/parse", map[string]any{
			"command": strArg(req, "command"),
		})
		return jsonResult(res, err)
	})

	// ── Rooms & Attendees ─────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("clockwise_search_rooms",
		mcp.WithDescription("Search for bookable conference rooms visible in Google Calendar"),
		mcp.WithString("q", mcp.Description("Optional name prefix to filter rooms")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := url.Values{}
		if v := strArg(req, "q"); v != "" {
			q.Set("q", v)
		}
		res, err := c.get("/api/rooms", q)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_suggest_attendees",
		mcp.WithDescription("Suggest attendees based on recent calendar history"),
		mcp.WithString("q", mcp.Description("Optional email/name prefix to filter suggestions")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := url.Values{}
		if v := strArg(req, "q"); v != "" {
			q.Set("q", v)
		}
		res, err := c.get("/api/attendees/suggest", q)
		return jsonResult(res, err)
	})

	// ── Settings & Status ─────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("clockwise_get_settings",
		mcp.WithDescription("Retrieve the current user settings (work hours, focus preferences, conferencing provider, etc.)"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.get("/api/settings/", nil)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_calendar_status",
		mcp.WithDescription("Check which calendar providers are connected (Google, Microsoft, etc.)"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := c.get("/api/auth/status", nil)
		return jsonResult(res, err)
	})

	s.AddTool(mcp.NewTool("clockwise_suggest_meeting",
		mcp.WithDescription("Find the best meeting slot for a set of attendees without creating the event"),
		mcp.WithString("title", mcp.Required(), mcp.Description("Meeting title")),
		mcp.WithString("attendees", mcp.Required(), mcp.Description("Comma-separated attendee emails")),
		mcp.WithString("duration", mcp.Description("Duration in minutes (default 30)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		payload := map[string]any{
			"title":     strArg(req, "title"),
			"attendees": strArg(req, "attendees"),
		}
		if v := strArg(req, "duration"); v != "" {
			payload["duration"] = v
		}
		res, err := c.post("/api/schedule/suggest", payload)
		return jsonResult(res, err)
	})

	return s
}

func strArg(req mcp.CallToolRequest, key string) string {
	if v, ok := req.Params.Arguments[key].(string); ok {
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
