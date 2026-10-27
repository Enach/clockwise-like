package api

// PAC-26: the six wire structs that had no json tags emit the contract's
// snake_case/lowercase keys, and the manager/team writes answer with the row
// read back (or the operation's existing 500), never a zero row or `null`.
// AC numbers refer to contracts/features/PAC-26.yaml.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// keysOf returns the sorted key set of one JSON object.
func keysOf(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("not a JSON object: %s (%v)", raw, err)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func assertKeySet(t *testing.T, what string, raw json.RawMessage, want ...string) {
	t.Helper()
	got := keysOf(t, raw)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(got, sorted) {
		t.Fatalf("%s key set = %v, want exactly %v; body %s", what, got, sorted, raw)
	}
}

// onlyElement decodes a JSON array and returns its only element.
func onlyElement(t *testing.T, what string, raw []byte) json.RawMessage {
	t.Helper()
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("%s: not a JSON array: %s (%v)", what, raw, err)
	}
	if len(arr) != 1 {
		t.Fatalf("%s: %d elements, want 1; body %s", what, len(arr), raw)
	}
	return arr[0]
}

// AC-9: every exported field of the six structs carries a non-empty json tag,
// so a field added later without one fails here rather than on the wire.
func TestPAC26WireStructsAreFullyTagged(t *testing.T) {
	for _, v := range []any{
		storage.FocusBlock{},
		engine.FocusBlock{},
		calendar.TimeSlot{},
		calendar.GenericEvent{},
		storage.UserProfile{},
		storage.ManagerTeamMember{},
	} {
		typ := reflect.TypeOf(v)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				t.Errorf("%s.%s has no json key (tag %q)", typ, f.Name, f.Tag.Get("json"))
			}
			if strings.Contains(f.Tag.Get("json"), "omitempty") {
				t.Errorf("%s.%s is omitempty; the contract marks every key required", typ, f.Name)
			}
		}
	}
}

// AC-1: GET /api/focus/blocks items are FocusBlockRecord.
func TestPAC26ListFocusBlocksKeys(t *testing.T) {
	db := openTestDB(t)
	user := createTestUser(t, "pac26-focus-list@example.com")
	monday := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	if err := storage.SaveFocusBlock(db, user, "pac26-evt", "2026-10-26", monday.Add(8*time.Hour), monday.Add(9*time.Hour+30*time.Minute)); err != nil {
		t.Fatalf("save block: %v", err)
	}
	h := &focusHandlers{eng: &mockFocusEngine{}, db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/focus/blocks?week=2026-10-26", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserIDKey, user))
	w := httptest.NewRecorder()
	h.listBlocks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	item := onlyElement(t, "listFocusBlocks", w.Body.Bytes())
	assertKeySet(t, "FocusBlockRecord", item, "id", "google_event_id", "start_time", "end_time", "date", "created_at")
	var got struct {
		GoogleEventID string    `json:"google_event_id"`
		StartTime     time.Time `json:"start_time"`
		Date          string    `json:"date"`
	}
	_ = json.Unmarshal(item, &got)
	if got.GoogleEventID != "pac26-evt" || !got.StartTime.Equal(monday.Add(8*time.Hour)) || got.Date != "2026-10-26" {
		t.Fatalf("values = %+v", got)
	}
}

// AC-2: POST /api/focus/run createdBlocks items are FocusRunBlock and the
// camelCase wrapper is unchanged.
func TestPAC26RunFocusBlockKeys(t *testing.T) {
	start := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	h := &focusHandlers{db: openTestDB(t), eng: &mockFocusEngine{runResult: &engine.FocusRunResult{
		WeekStart:     time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
		CreatedBlocks: []engine.FocusBlock{{GoogleEventID: "g1", Start: start, End: start.Add(time.Hour), Date: "2026-10-26"}},
		SkippedDays:   []string{},
		TotalMinutes:  60,
		Errors:        []string{},
	}}}
	req := httptest.NewRequest(http.MethodPost, "/api/focus/run", bytes.NewBufferString(`{"week":"2026-10-26"}`))
	w := httptest.NewRecorder()
	h.runFocus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	assertKeySet(t, "FocusRunResult", w.Body.Bytes(), "weekStart", "createdBlocks", "skippedDays", "totalMinutes", "errors")
	var body struct {
		CreatedBlocks []json.RawMessage `json:"createdBlocks"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.CreatedBlocks) != 1 {
		t.Fatalf("createdBlocks = %s", w.Body.String())
	}
	assertKeySet(t, "FocusRunBlock", body.CreatedBlocks[0], "google_event_id", "start_time", "end_time", "date")
}

// fakeGoogleFreeBusy is a Google Calendar stand-in whose freeBusy answers one
// busy window for every requested calendar.
func fakeGoogleFreeBusy(t *testing.T, start, end time.Time) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/freeBusy") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		cals := map[string]any{}
		for _, item := range req.Items {
			cals[item.ID] = map[string]any{"busy": []map[string]string{{
				"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339),
			}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#freeBusy", "calendars": cals})
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
}

// AC-3, GET half: getCalendarFreeBusy windows are {start, end}.
func TestPAC26CalendarFreeBusyWindowKeys(t *testing.T) {
	start := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	fakeGoogleFreeBusy(t, start, start.Add(time.Hour))
	user := userWithGoogleToken(t, "pac26-cal-freebusy@example.com")
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: openTestDB(t)}
	req := httptest.NewRequest(http.MethodGet, "/api/calendar/freebusy?start=2026-10-26T00:00:00Z&end=2026-10-27T00:00:00Z&attendees=colleague@corp.example", nil)
	w := httptest.NewRecorder()
	h.freeBusy(w, withUser(req, user))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	var body map[string][]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body %s", err, w.Body.String())
	}
	windows := body["colleague@corp.example"]
	if len(windows) != 1 {
		t.Fatalf("windows = %s", w.Body.String())
	}
	assertKeySet(t, "RawGoogleBusyWindow", windows[0], "start", "end")
}

// AC-3, POST half: results[0].busy[0] is {start, end} and byte-equal to
// participants[0].busy[0], so the body has one casing throughout.
func TestPAC26QueryFreeBusyResultsKeys(t *testing.T) {
	start := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	fakeGoogleFreeBusy(t, start, start.Add(time.Hour))
	user := userWithGoogleToken(t, "pac26-post-freebusy@example.com")
	h := newFreeBusyHandlers(openTestDB(t), &oauth2.Config{})
	payload, _ := json.Marshal(map[string]any{
		"emails":     []string{"colleague@corp.example"},
		"start_time": "2026-10-26T00:00:00Z",
		"end_time":   "2026-10-27T00:00:00Z",
	})
	w := httptest.NewRecorder()
	h.query(w, withUser(httptest.NewRequest(http.MethodPost, "/api/freebusy", bytes.NewReader(payload)), user))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	var body struct {
		Results []struct {
			Coverage string            `json:"coverage"`
			Busy     []json.RawMessage `json:"busy"`
		} `json:"results"`
		Participants []struct {
			Busy []json.RawMessage `json:"busy"`
		} `json:"participants"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Results) != 1 || body.Results[0].Coverage != "known" || len(body.Results[0].Busy) != 1 ||
		len(body.Participants) != 1 || len(body.Participants[0].Busy) != 1 {
		t.Fatalf("body = %s, want one known participant with one window", w.Body.String())
	}
	assertKeySet(t, "results[0].busy[0]", body.Results[0].Busy[0], "start", "end")
	if !bytes.Equal(body.Results[0].Busy[0], body.Participants[0].Busy[0]) {
		t.Fatalf("results[0].busy[0] = %s, participants[0].busy[0] = %s; want equal", body.Results[0].Busy[0], body.Participants[0].Busy[0])
	}
}

// AC-4: previewPersonalCalendar items are {id, title, start, end}.
func TestPAC26PersonalPreviewKeys(t *testing.T) {
	user := createTestUser(t, "pac26-personal-preview@example.com")
	calID := createPersonalCalendar(t, user, "webcal", "Family", "webcal://example.com/pac26.ics", true)
	start := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	r := setupPersonalRoutes(t, &fakePersonalBlocker{previewEvents: []calendar.GenericEvent{
		{ID: "evt-1", Title: "School pickup", Start: start, End: start.Add(30 * time.Minute)},
	}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, withUser(httptest.NewRequest(http.MethodGet, "/api/personal-calendars/"+strconv.FormatInt(calID, 10)+"/preview", nil), user))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	assertKeySet(t, "PersonalCalendarPreviewEvent", onlyElement(t, "preview", w.Body.Bytes()), "id", "title", "start", "end")
}

// AC-5: setManagerProfile answers UserProfileGoStruct in snake_case.
func TestPAC26ManagerProfileKeys(t *testing.T) {
	user := createTestUser(t, "pac26-profile@example.com")
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	w := managerRequest(t, r, http.MethodPost, "/api/manager/profile", user, map[string]any{"is_manager": false})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	assertKeySet(t, "UserProfileGoStruct", w.Body.Bytes(), "user_id", "is_manager", "detected_at", "analytics_shared_with_manager", "updated_at")
	var got struct {
		UserID uuid.UUID `json:"user_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.UserID != user {
		t.Fatalf("user_id = %s, want %s", got.UserID, user)
	}
}

var pac26MemberKeys = []string{
	"id", "manager_user_id", "member_email", "member_user_id", "display_name", "source",
	"cadence", "cadence_custom_days", "last_one_on_one_at", "created_at", "updated_at",
}

type pac26Member struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
	Cadence     string    `json:"cadence"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func decodePAC26Member(t *testing.T, w *httptest.ResponseRecorder) pac26Member {
	t.Helper()
	assertKeySet(t, "ManagerTeamMemberGoStruct", w.Body.Bytes(), pac26MemberKeys...)
	var m pac26Member
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode member: %v", err)
	}
	return m
}

// AC-6: addManagerTeamMember answers the stored row, without and with team_id.
func TestPAC26AddMemberReturnsStoredRow(t *testing.T) {
	db := openTestDB(t)
	manager := createTestUser(t, "pac26-add-manager@example.com")
	team := createManagerTestTeam(t, manager, "PAC26 Add")
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})

	t.Run("without team_id", func(t *testing.T) {
		w := managerRequest(t, r, http.MethodPost, "/api/manager/team/members", manager, map[string]any{
			"email": "pac26-global@example.com", "display_name": "Global", "cadence": "monthly",
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
		}
		m := decodePAC26Member(t, w)
		stored, err := storage.GetManagerTeamMemberByEmail(db, manager, "pac26-global@example.com")
		if err != nil {
			t.Fatalf("read stored: %v", err)
		}
		assertStoredRow(t, m, stored.ID)
		if m.Cadence != "monthly" {
			t.Fatalf("cadence = %q", m.Cadence)
		}
	})

	t.Run("with team_id", func(t *testing.T) {
		w := managerRequest(t, r, http.MethodPost, "/api/manager/team/members?team_id="+team.ID.String(), manager, map[string]any{
			"email": "pac26-team@example.com", "display_name": "Teamed", "cadence": "biweekly",
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
		}
		m := decodePAC26Member(t, w)
		stored, err := storage.GetManagerTeamMemberByEmailForTeam(db, manager, team.ID, "pac26-team@example.com")
		if err != nil {
			t.Fatalf("read stored: %v", err)
		}
		assertStoredRow(t, m, stored.ID)
		if m.Cadence != stored.Cadence || m.Cadence != "biweekly" {
			t.Fatalf("cadence = %q, assignment cadence %q", m.Cadence, stored.Cadence)
		}
	})

	// D-4 (challenger recommendation 1): with team_id the body is the team
	// view, so an existing assignment override survives a re-add and is what
	// the 201 shows, while the global row takes the new display name.
	t.Run("with team_id keeps the team override", func(t *testing.T) {
		email := "pac26-override@example.com"
		if err := storage.UpsertManagerTeamMemberForTeam(db, team.ID, &storage.ManagerTeamMember{
			ManagerUserID: manager, MemberEmail: email, DisplayName: "Alice", Source: "manual", Cadence: "weekly",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := storage.PatchManagerTeamMemberForTeam(db, manager, team.ID, email, "Ally", "weekly", nil); err != nil {
			t.Fatalf("seed override: %v", err)
		}
		w := managerRequest(t, r, http.MethodPost, "/api/manager/team/members?team_id="+team.ID.String(), manager, map[string]any{
			"email": email, "display_name": "Alice", "cadence": "weekly",
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
		}
		m := decodePAC26Member(t, w)
		if m.DisplayName != "Ally" {
			t.Fatalf("201 display_name = %q, want the team override \"Ally\"", m.DisplayName)
		}
		global, err := storage.GetManagerTeamMemberByEmail(db, manager, email)
		if err != nil {
			t.Fatalf("read global: %v", err)
		}
		if global.DisplayName != "Alice" {
			t.Fatalf("global display_name = %q, want \"Alice\"", global.DisplayName)
		}
	})
}

func assertStoredRow(t *testing.T, m pac26Member, storedID uuid.UUID) {
	t.Helper()
	if m.ID == uuid.Nil || m.ID != storedID {
		t.Fatalf("id = %s, want the stored row's id %s", m.ID, storedID)
	}
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Fatalf("timestamps are zero: created_at %v updated_at %v", m.CreatedAt, m.UpdatedAt)
	}
}

// swapReader replaces a read-back seam for the duration of a test.
func swapReader[T any](t *testing.T, target *T, replacement T) {
	t.Helper()
	prev := *target
	*target = replacement
	t.Cleanup(func() { *target = prev })
}

var errPAC26ReadBack = errors.New("pac26: injected read-back failure")

// D-3 on the add: a failed read-back after the upsert is the existing 500.
func TestPAC26AddMemberReadBackFailureIs500(t *testing.T) {
	manager := createTestUser(t, "pac26-add-fail@example.com")
	team := createManagerTestTeam(t, manager, "PAC26 Add Fail")
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	swapReader(t, &readManagerMember, func(*sql.DB, uuid.UUID, string) (*storage.ManagerTeamMember, error) {
		return nil, errPAC26ReadBack
	})
	swapReader(t, &readManagerMemberForTeam, func(*sql.DB, uuid.UUID, uuid.UUID, string) (*storage.ManagerTeamMember, error) {
		return nil, errPAC26ReadBack
	})
	for _, path := range []string{"/api/manager/team/members", "/api/manager/team/members?team_id=" + team.ID.String()} {
		w := managerRequest(t, r, http.MethodPost, path, manager, map[string]any{"email": "pac26-addfail@example.com"})
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status = %d, want 500; body %s", path, w.Code, w.Body.String())
		}
		exactJSONError(t, w, "db error")
	}
}

// AC-7: the patch answers the re-read row, or 500 db error when it fails.
func TestPAC26PatchMember(t *testing.T) {
	manager := createTestUser(t, "pac26-patch@example.com")
	team := createManagerTestTeam(t, manager, "PAC26 Patch")
	r := setupManagerRoutes(t, &fakeManagerWorkflow{})
	for _, path := range []string{"/api/manager/team/members", "/api/manager/team/members?team_id=" + team.ID.String()} {
		if w := managerRequest(t, r, http.MethodPost, path, manager, map[string]any{"email": "pac26-patchee@example.com", "cadence": "weekly"}); w.Code != http.StatusCreated {
			t.Fatalf("seed %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	patchPath := func(scoped bool) string {
		p := "/api/manager/team/members/pac26-patchee@example.com"
		if scoped {
			p += "?team_id=" + team.ID.String()
		}
		return p
	}

	for _, scoped := range []bool{false, true} {
		w := managerRequest(t, r, http.MethodPatch, patchPath(scoped), manager, map[string]any{"cadence": "monthly"})
		if w.Code != http.StatusOK {
			t.Fatalf("scoped=%v: status = %d; body %s", scoped, w.Code, w.Body.String())
		}
		m := decodePAC26Member(t, w)
		if m.Cadence != "monthly" || m.ID == uuid.Nil {
			t.Fatalf("scoped=%v: member = %+v", scoped, m)
		}
	}

	swapReader(t, &readManagerMember, func(*sql.DB, uuid.UUID, string) (*storage.ManagerTeamMember, error) {
		return nil, errPAC26ReadBack
	})
	swapReader(t, &readManagerMemberForTeam, func(*sql.DB, uuid.UUID, uuid.UUID, string) (*storage.ManagerTeamMember, error) {
		return nil, errPAC26ReadBack
	})
	for _, scoped := range []bool{false, true} {
		w := managerRequest(t, r, http.MethodPatch, patchPath(scoped), manager, map[string]any{"cadence": "weekly"})
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("scoped=%v: status = %d, want 500; body %s", scoped, w.Code, w.Body.String())
		}
		exactJSONError(t, w, "db error")
	}
}

// AC-8: a rename whose re-read fails is 500 internal error, never 200 null.
func TestPAC26RenameTeamReadBackFailureIs500(t *testing.T) {
	owner := createTestUser(t, "pac26-rename@example.com")
	db := openTestDB(t)
	team, err := storage.CreateTeam(db, "PAC26 Old", owner)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	_ = storage.AddTeamMember(db, team.ID, owner, "owner")
	r := setupTeamRoutes(t)
	rename := func(name string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"name": name})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, withUser(httptest.NewRequest(http.MethodPatch, "/api/teams/"+team.ID.String(), bytes.NewReader(body)), owner))
		return w
	}

	ok := rename("PAC26 New")
	if ok.Code != http.StatusOK || strings.TrimSpace(ok.Body.String()) == "null" {
		t.Fatalf("rename: status = %d body %s", ok.Code, ok.Body.String())
	}

	swapReader(t, &readTeam, func(*sql.DB, uuid.UUID) (*storage.Team, error) { return nil, errPAC26ReadBack })
	w := rename("PAC26 Newer")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", w.Code, w.Body.String())
	}
	exactJSONError(t, w, "internal error")
}
