package storage

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// PAC-71 migration rule (b): an unbound account created before PAC-71 binds
// on authoritative evidence only when it is EMPTY (spec docs/specs/PAC-71.md
// §3 rule (b), §2 "What an account can carry today"; contract
// contracts/features/PAC-71.md §5 D1–D14 and §6).

// accountDataCheck is one kind of account data, checked by an EXISTS query on
// the account id ($1).
type accountDataCheck struct {
	kind  string
	query string
}

// accountDataChecks lists every kind of spec §2's inventory except the
// settings row (checked column by column, settingsKind), in the contract's
// D-order. Its foreign-key columns are classifiedUserRefs below.
var accountDataChecks = []accountDataCheck{
	{"google_calendar_credential", `SELECT EXISTS (SELECT 1 FROM oauth_tokens WHERE user_id = $1)`},              // D1
	{"workspace_connection", `SELECT EXISTS (SELECT 1 FROM workspace_connections WHERE user_id = $1)`},           // D6
	{"personal_calendar", `SELECT EXISTS (SELECT 1 FROM personal_calendars WHERE user_id = $1)`},                 // D7
	{"credential_loss", `SELECT EXISTS (SELECT 1 FROM credential_losses WHERE user_id = $1)`},                    // D8
	{"team_created", `SELECT EXISTS (SELECT 1 FROM teams WHERE created_by = $1)`},                                // D9
	{"team_membership", `SELECT EXISTS (SELECT 1 FROM team_members WHERE user_id = $1)`},                         // D9
	{"team_invite_sent", `SELECT EXISTS (SELECT 1 FROM team_invites WHERE invited_by = $1)`},                     // D9
	{"scheduling_link_owned", `SELECT EXISTS (SELECT 1 FROM scheduling_links WHERE owner_user_id = $1)`},         // D10
	{"scheduling_link_host", `SELECT EXISTS (SELECT 1 FROM scheduling_link_hosts WHERE user_id = $1)`},           // D10
	{"booking", `SELECT EXISTS (SELECT 1 FROM bookings b JOIN scheduling_links l ON l.id = b.link_id WHERE l.owner_user_id = $1)`}, // D11
	{"booking_event", `SELECT EXISTS (SELECT 1 FROM booking_events WHERE user_id = $1)`},                         // D11
	// user_profiles is created on read with defaults; a row at its defaults
	// says nothing about use (spec §2), so only a changed profile counts.
	{"manager_profile", `SELECT EXISTS (SELECT 1 FROM user_profiles WHERE user_id = $1
		AND (is_manager OR detected_at IS NOT NULL OR NOT analytics_shared_with_manager))`}, // D12
	{"manager_roster", `SELECT EXISTS (SELECT 1 FROM manager_team_members WHERE manager_user_id = $1)`},            // D12
	{"roster_entry_pointing_at_account", `SELECT EXISTS (SELECT 1 FROM manager_team_members WHERE member_user_id = $1)`}, // D12
	{"one_on_one_record", `SELECT EXISTS (SELECT 1 FROM one_on_one_occurrences WHERE manager_user_id = $1)`},       // D12
	{"team_analytics_record", `SELECT EXISTS (SELECT 1 FROM team_analytics_cache WHERE manager_user_id = $1)`},     // D12
	{"manager_assignment", `SELECT EXISTS (SELECT 1 FROM manager_team_member_assignments WHERE manager_user_id = $1)`}, // D12
	{"focus_block", `SELECT EXISTS (SELECT 1 FROM focus_blocks WHERE user_id = $1)`},                             // D13
	{"habit", `SELECT EXISTS (SELECT 1 FROM habits WHERE user_id = $1)`},                                         // D13
	{"analytics_week", `SELECT EXISTS (SELECT 1 FROM analytics_weeks WHERE user_id = $1)`},                       // D13
	{"meeting_brief", `SELECT EXISTS (SELECT 1 FROM meeting_briefs WHERE user_id = $1)`},                         // D13
	{"recap_record", `SELECT EXISTS (SELECT 1 FROM recap_sends WHERE user_id = $1)`},                             // D13
	{"activity_log_entry", `SELECT EXISTS (SELECT 1 FROM audit_log WHERE user_id = $1)`},                         // D13
	{"stepup_grant", `SELECT EXISTS (SELECT 1 FROM sso_stepup_grants WHERE user_id = $1)`},                       // D14
	{"sso_event", `SELECT EXISTS (SELECT 1 FROM sso_events WHERE user_id = $1)`},                                 // D14
}

// classifiedUserRefs is every foreign-key column to users(id) that the checks
// above (or the settings check, or the identity itself) classify. A foreign
// key to users that is not listed here is UNCLASSIFIED: FirstNonEmptyKind
// counts a row there as non-empty (fail closed, spec §3 rule (b)), and
// TestPAC71_EveryUserForeignKeyIsClassified fails until it is classified.
var classifiedUserRefs = map[string]bool{
	"oauth_tokens.user_id":                            true,
	"settings.user_id":                                true,
	"workspace_connections.user_id":                   true,
	"personal_calendars.user_id":                      true,
	"credential_losses.user_id":                       true,
	"teams.created_by":                                true,
	"team_members.user_id":                            true,
	"team_invites.invited_by":                         true,
	"scheduling_links.owner_user_id":                  true,
	"scheduling_link_hosts.user_id":                   true,
	"booking_events.user_id":                          true,
	"user_profiles.user_id":                           true,
	"manager_team_members.manager_user_id":            true,
	"manager_team_members.member_user_id":             true,
	"one_on_one_occurrences.manager_user_id":          true,
	"team_analytics_cache.manager_user_id":            true,
	"manager_team_member_assignments.manager_user_id": true,
	"focus_blocks.user_id":                            true,
	"habits.user_id":                                  true,
	"analytics_weeks.user_id":                         true,
	"meeting_briefs.user_id":                          true,
	"recap_sends.user_id":                             true,
	"audit_log.user_id":                               true,
	"sso_stepup_grants.user_id":                       true,
	"sso_events.user_id":                              true,
	"user_identities.user_id":                         true,
}

// UserForeignKeys lists every foreign-key column that references users(id),
// as "table.column".
func UserForeignKeys(q Querier) ([]string, error) {
	rows, err := q.Query(`
		SELECT c.conrelid::regclass::text, a.attname
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.contype = 'f' AND c.confrelid = 'users'::regclass
		ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, len(classifiedUserRefs))
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			return nil, err
		}
		out = append(out, table+"."+col)
	}
	return out, rows.Err()
}

// FirstNonEmptyKind returns the first kind of data or relationship the
// account carries, in the contract's D-order, or "" when the account is empty
// in the sense of rule (b). It is read inside the binding transaction, under
// the account lock, as well as before it.
func FirstNonEmptyKind(q Querier, userID uuid.UUID) (string, error) {
	// D1 first, then the settings row (D2–D5), then D6–D14.
	var exists bool
	if err := q.QueryRow(accountDataChecks[0].query, userID).Scan(&exists); err != nil {
		return "", fmt.Errorf("%s: %w", accountDataChecks[0].kind, err)
	}
	if exists {
		return accountDataChecks[0].kind, nil
	}
	if kind, err := settingsKind(q, userID); err != nil || kind != "" {
		return kind, err
	}
	for _, c := range accountDataChecks[1:] {
		if err := q.QueryRow(c.query, userID).Scan(&exists); err != nil {
			return "", fmt.Errorf("%s: %w", c.kind, err)
		}
		if exists {
			return c.kind, nil
		}
	}
	// Fail closed on a kind a later migration adds and nobody classified.
	refs, err := UserForeignKeys(q)
	if err != nil {
		return "", err
	}
	for _, ref := range refs {
		if classifiedUserRefs[ref] {
			continue
		}
		table, col, _ := strings.Cut(ref, ".")
		query := `SELECT EXISTS (SELECT 1 FROM ` + table + ` WHERE ` + quoteIdent(col) + ` = $1)`
		if err := q.QueryRow(query, userID).Scan(&exists); err != nil {
			return "", fmt.Errorf("unclassified %s: %w", ref, err)
		}
		if exists {
			return "unclassified:" + ref, nil
		}
	}
	return "", nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Settings classification (contract §6). Every column of the settings table
// is in exactly one of these sets; a column in none counts as changed.
var (
	// SettingsBookkeeping is never compared (spec §3 rule (b)).
	SettingsBookkeeping = []string{"id", "user_id", "updated_at", "auto_schedule_last_run"}

	// settingsMustBeEmpty are credentials, keys and settings that point outside
	// Paceday (D2–D4): empty string or NULL, else the account is not empty.
	settingsMustBeEmpty = []struct{ column, kind string }{
		{"microsoft_tokens", "microsoft_credential"},
		{"zoom_tokens", "zoom_credential"},
		{"llm_api_key", "ai_key"},
		{"llm_api_key_openai", "ai_key"},
		{"llm_api_key_anthropic", "ai_key"},
		{"llm_api_key_azure_openai", "ai_key"},
		{"llm_api_key_bedrock", "ai_key"},
		{"llm_api_key_vertex", "ai_key"},
		{"llm_base_url", "ai_destination"},
		{"llm_model", "ai_destination"},
		{"webcal_url", "external_setting"},
		{"calendar_email", "external_setting"},
		{"recap_channel_id", "external_setting"},
	}

	// SettingsDefaults is every preference's default: the value a settings row
	// created by `INSERT INTO settings (user_id)` holds (contract §6), as
	// to_jsonb renders it. working_hours, lunch_breaks and recap_send_time are
	// compared in normal form (settingsNormalForm).
	SettingsDefaults = map[string]any{
		"work_start":                         "09:00",
		"work_end":                           "18:00",
		"timezone":                           "UTC",
		"focus_min_block_minutes":            25,
		"focus_max_block_minutes":            120,
		"focus_daily_target_minutes":         240,
		"out_of_hours_meetings_per_week":     0,
		"auto_decline_outside_working_hours": false,
		"focus_label":                        "Focus Time",
		"focus_color":                        "#4F46E5",
		"lunch_start":                        "12:00",
		"lunch_end":                          "13:00",
		"protect_lunch":                      true,
		"buffer_before_minutes":              5,
		"buffer_after_minutes":               5,
		"buffer_enabled":                     false,
		"buffer_min_meeting_minutes":         30,
		"buffer_skip_back_to_back":           true,
		"compression_enabled":                false,
		"auto_schedule_enabled":              false,
		"auto_schedule_cron":                 "0 8 * * *",
		"working_hours":                      `{"mode":"all_days","default":{"enabled":true,"start":"09:00","end":"18:00"},"days":{}}`,
		"lunch_breaks":                       `{}`,
		"time_format":                        "12h",
		"calendar_provider":                  "google",
		"conferencing_provider":              "meet",
		"recap_enabled":                      false,
		"recap_send_time":                    "08:00",
		"recap_include_briefs":               true,
		"recap_include_focus":                true,
		"recap_include_habits":               true,
		"recap_send_to":                      "dm",
		// Migration 029's default. A row created before 029 holds '' or
		// 'ollama' and counts as changed (contract §6, fail closed).
		"llm_provider":      "organization",
		"aws_region":        "us-east-1",
		"bedrock_model":     "anthropic.claude-sonnet-4-5-20251001-v1:0",
		"azure_endpoint":    "",
		"azure_deployment":  "",
		"azure_api_version": "2024-02-01",
		"gcp_project":       "",
		"gcp_location":      "us-central1",
		"vertex_model":      "gemini-2.0-flash-001",
		"aws_profile":       "",
		"ollama_base_url":   "http://localhost:11434",
		"ollama_model":      "llama3",
	}
)

// settingsKind compares userID's settings row with the defaults. "" means
// every column is at its default (or no row exists).
func settingsKind(q Querier, userID uuid.UUID) (string, error) {
	var raw []byte
	err := q.QueryRow(`SELECT to_jsonb(s) FROM settings s WHERE user_id = $1`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("settings: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	row := map[string]any{}
	if err := dec.Decode(&row); err != nil {
		return "", fmt.Errorf("settings: %w", err)
	}
	seen := map[string]bool{}
	for _, c := range SettingsBookkeeping {
		seen[c] = true
	}
	for _, c := range settingsMustBeEmpty {
		seen[c.column] = true
		if v, ok := row[c.column]; ok && v != nil && v != "" {
			return c.kind, nil
		}
	}
	cols := make([]string, 0, len(row))
	for c := range row {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	for _, c := range cols {
		if seen[c] {
			continue
		}
		def, ok := SettingsDefaults[c]
		if !ok {
			return "unclassified_setting:" + c, nil
		}
		if !settingEqualsDefault(c, row[c], def, row) {
			if c == "llm_provider" {
				return "ai_destination", nil
			}
			if c == "recap_send_to" {
				return "external_setting", nil
			}
			return "changed_preference:" + c, nil
		}
	}
	return "", nil
}

func settingEqualsDefault(col string, v, def any, row map[string]any) bool {
	switch col {
	case "working_hours", "lunch_breaks":
		return schedulesEqual(col, v, def.(string), row)
	case "recap_send_time":
		s, ok := v.(string)
		return ok && len(s) >= 5 && s[:5] == def
	}
	switch d := def.(type) {
	case int:
		n, ok := v.(json.Number)
		return ok && n.String() == fmt.Sprint(d)
	default:
		return v == def
	}
}

// schedulesEqual compares a schedule column with its default after the
// normalisation the code applies on every read (normalizeSchedules), as
// decoded structures (contract §6).
func schedulesEqual(col string, v any, def string, row map[string]any) bool {
	stored, err := json.Marshal(v)
	if err != nil {
		return false
	}
	ws, _ := row["work_start"].(string)
	we, _ := row["work_end"].(string)
	norm := func(b []byte) (any, bool) {
		s := Settings{WorkStart: ws, WorkEnd: we}
		if col == "working_hours" {
			if string(b) != "null" {
				if err := json.Unmarshal(b, &s.WorkingHours); err != nil {
					return nil, false
				}
			}
			s.normalizeSchedules()
			return s.WorkingHours, true
		}
		if string(b) != "null" {
			if err := json.Unmarshal(b, &s.LunchBreaks); err != nil {
				return nil, false
			}
		}
		s.normalizeSchedules()
		return s.LunchBreaks, true
	}
	got, ok := norm(stored)
	if !ok {
		return false
	}
	want, ok := norm([]byte(def))
	return ok && reflect.DeepEqual(got, want)
}
