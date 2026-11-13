package api

// PAC-42: GET /api/analytics/week and GET /api/analytics/trends report the week
// start in one format (the calendar date of the Monday, never the zero date) and
// every proportion on one scale (a percentage, 100 = the whole). The habit
// completion rate is still stored as a 0..1 fraction (spec D-3); the ×100 is
// applied when the response is built. Spec: docs/specs/PAC-42.md.

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// pac42Week is Monday 2025-01-06, the spec's "week W".
var pac42Week = time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)

func pac42Router(t *testing.T) *chi.Mux {
	t.Helper()
	h := newAnalyticsHandlers(openTestDB(t), nil)
	r := chi.NewRouter()
	r.Get("/api/analytics/week", h.week)
	r.Get("/api/analytics/trends", h.trends)
	return r
}

// pac42Get answers the raw body and its decoded form.
func pac42Get(t *testing.T, r http.Handler, userID uuid.UUID, target string) (string, any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, withUser(httptest.NewRequest(http.MethodGet, target, nil), userID))
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200; body: %s", target, w.Code, w.Body.String())
	}
	var v any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("GET %s: decode: %v; body: %s", target, err, w.Body.String())
	}
	return w.Body.String(), v
}

func pac42WeekObj(t *testing.T, r http.Handler, userID uuid.UUID, date string) map[string]any {
	t.Helper()
	_, v := pac42Get(t, r, userID, "/api/analytics/week?date="+date)
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("week: not an object: %#v", v)
	}
	return m
}

func pac42Trends(t *testing.T, r http.Handler, userID uuid.UUID, query string) []map[string]any {
	t.Helper()
	_, v := pac42Get(t, r, userID, "/api/analytics/trends"+query)
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("trends: not an array: %#v", v)
	}
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("trends: item not an object: %#v", it)
		}
		out = append(out, m)
	}
	return out
}

// pac42Store writes a week the way the engine does (rate as a 0..1 fraction).
func pac42Store(t *testing.T, userID uuid.UUID, week time.Time, rate float64) *storage.AnalyticsWeek {
	t.Helper()
	a, err := storage.UpsertAnalyticsWeek(openTestDB(t), &storage.AnalyticsWeek{
		UserID: userID, WeekStart: week, TotalWorkingMinutes: 2400,
		MeetingMinutes: 600, FocusMinutes: 300, HabitMinutes: 120, PersonalMinutes: 60, FreeMinutes: 1320,
		MeetingCount: 7, FocusBlockCount: 2, HabitCompletionRate: rate, FocusScore: 33,
	})
	if err != nil || a == nil {
		t.Fatalf("store week %s: %v, %v", week.Format("2006-01-02"), a, err)
	}
	return a
}

// pac42Habits gives the user one habit with `completed` completed and `scheduled`
// scheduled occurrences inside week W (30 minutes each).
func pac42Habits(t *testing.T, userID uuid.UUID, completed, scheduled int) {
	t.Helper()
	db := openTestDB(t)
	h, err := storage.CreateHabit(db, &storage.Habit{
		UserID: userID, Title: "PAC-42 habit", DurationMinutes: 30,
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "12:00", Priority: 1, Color: "#9B7AE0",
	})
	if err != nil || h == nil {
		t.Fatalf("create habit: %v, %v", h, err)
	}
	for i := 0; i < completed+scheduled; i++ {
		day := pac42Week.AddDate(0, 0, i)
		status := "completed"
		if i >= completed {
			status = "scheduled"
		}
		start := day.Add(9 * time.Hour)
		if _, err := storage.UpsertHabitOccurrence(db, &storage.HabitOccurrence{
			HabitID: h.ID, ScheduledDate: day, StartTime: start, EndTime: start.Add(30 * time.Minute), Status: status,
		}); err != nil {
			t.Fatalf("occurrence %d: %v", i, err)
		}
	}
}

func pac42Num(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("%s = %#v, want a number", key, m[key])
	}
	return v
}

func pac42StoredRate(t *testing.T, userID uuid.UUID) float64 {
	t.Helper()
	var rate float64
	if err := openTestDB(t).QueryRow(
		`SELECT habit_completion_rate FROM analytics_weeks WHERE user_id = $1 AND week_start = $2`,
		userID, pac42Week.Format("2006-01-02")).Scan(&rate); err != nil {
		t.Fatalf("read stored rate: %v", err)
	}
	return rate
}

func pac42RowCount(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := openTestDB(t).QueryRow(`SELECT COUNT(*) FROM analytics_weeks WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// AC-1: a stored week's trend row reports week_start "2025-01-06", not the zero date.
func TestPAC42TrendsWeekStartIsTheMondayDate(t *testing.T) {
	r := pac42Router(t)
	user := createTestUser(t, "pac42-ac1@example.com")
	pac42Store(t, user, pac42Week, 0.5)

	rows := pac42Trends(t, r, user, "")
	if len(rows) != 1 {
		t.Fatalf("trends: %d rows, want 1", len(rows))
	}
	if got := rows[0]["week_start"]; got != "2025-01-06" {
		t.Fatalf("trends week_start = %#v, want \"2025-01-06\"", got)
	}
}

// AC-2 (computed and stored) and AC-3 (served from cache): week_start "2025-01-06".
func TestPAC42WeekWeekStartComputedAndCached(t *testing.T) {
	r := pac42Router(t)
	user := createTestUser(t, "pac42-ac2@example.com")

	first := pac42WeekObj(t, r, user, "2025-01-08")
	if got := first["week_start"]; got != "2025-01-06" {
		t.Fatalf("computed week_start = %#v, want \"2025-01-06\"", got)
	}
	if n := pac42RowCount(t, user); n != 1 {
		t.Fatalf("after compute: %d stored rows, want 1", n)
	}
	second := pac42WeekObj(t, r, user, "2025-01-10")
	if got := second["week_start"]; got != "2025-01-06" {
		t.Fatalf("cached week_start = %#v, want \"2025-01-06\"", got)
	}
	if first["computed_at"] != second["computed_at"] || first["id"] != second["id"] {
		t.Fatalf("second read was not a cache hit: computed_at %v vs %v, id %v vs %v",
			first["computed_at"], second["computed_at"], first["id"], second["id"])
	}
}

// AC-4 (the ticket's "Go test on both"): the two reads' week_start are byte-equal.
func TestPAC42WeekAndTrendsWeekStartAreByteEqual(t *testing.T) {
	r := pac42Router(t)
	user := createTestUser(t, "pac42-ac4@example.com")
	pac42Store(t, user, pac42Week, 0.25)

	weekBody, _ := pac42Get(t, r, user, "/api/analytics/week?date=2025-01-06")
	trendBody, _ := pac42Get(t, r, user, "/api/analytics/trends")
	var week map[string]json.RawMessage
	var trend []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(weekBody), &week); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(trendBody), &trend); err != nil || len(trend) != 1 {
		t.Fatalf("trends: %v, %d rows", err, len(trend))
	}
	if string(week["week_start"]) != string(trend[0]["week_start"]) {
		t.Fatalf("week_start differs: week %s vs trends %s", week["week_start"], trend[0]["week_start"])
	}
	if string(week["week_start"]) != `"2025-01-06"` {
		t.Fatalf("week_start = %s, want \"2025-01-06\"", week["week_start"])
	}
}

// AC-6, AC-7, AC-14: 3 of 4 occurrences completed -> both reads report 75, the
// breakdown and focus score stay on 0..100, and storage keeps the fraction 0.75.
func TestPAC42HabitCompletionRateIsAPercentageOnBothReads(t *testing.T) {
	r := pac42Router(t)
	user := createTestUser(t, "pac42-ac6@example.com")
	pac42Habits(t, user, 3, 1)

	week := pac42WeekObj(t, r, user, "2025-01-07")
	if got := pac42Num(t, week, "habit_completion_rate"); got != 75 {
		t.Fatalf("week habit_completion_rate = %v, want 75", got)
	}
	// Regression guard: the breakdown and the focus score are not rescaled again.
	total := pac42Num(t, week, "total_working_minutes")
	breakdown, _ := week["breakdown"].([]any)
	if len(breakdown) != 5 {
		t.Fatalf("breakdown: %d entries, want 5", len(breakdown))
	}
	for _, e := range breakdown {
		entry := e.(map[string]any)
		want := pac42Num(t, entry, "minutes") / total * 100
		if got := pac42Num(t, entry, "percentage"); math.Abs(got-want) > 1e-9 {
			t.Errorf("breakdown %v percentage = %v, want minutes/total*100 = %v", entry["category"], got, want)
		}
	}
	if fs := pac42Num(t, week, "focus_score"); fs != 50 {
		t.Errorf("focus_score = %v, want 50 (no focus and no meeting time)", fs)
	}

	rows := pac42Trends(t, r, user, "")
	if len(rows) != 1 {
		t.Fatalf("trends: %d rows, want 1", len(rows))
	}
	if got := pac42Num(t, rows[0], "habit_completion_rate"); got != 75 {
		t.Fatalf("trends habit_completion_rate = %v, want 75 (equal to the single week)", got)
	}
	if got := pac42StoredRate(t, user); got != 0.75 {
		t.Fatalf("stored habit_completion_rate = %v, want the fraction 0.75 (spec D-3)", got)
	}
}

// AC-8: a week stored before the change (fraction 0.5) is reported as 50 on both
// reads (single week from cache), and no row is deleted.
func TestPAC42PreExistingFractionIsReportedAsAPercentage(t *testing.T) {
	r := pac42Router(t)
	user := createTestUser(t, "pac42-ac8@example.com")
	stored := pac42Store(t, user, pac42Week, 0.5)

	week := pac42WeekObj(t, r, user, "2025-01-09")
	if week["id"] != stored.ID.String() {
		t.Fatalf("week id = %v, want the stored row %s (cache hit)", week["id"], stored.ID)
	}
	if got := pac42Num(t, week, "habit_completion_rate"); got != 50 {
		t.Fatalf("week habit_completion_rate = %v, want 50", got)
	}
	rows := pac42Trends(t, r, user, "")
	if len(rows) != 1 || pac42Num(t, rows[0], "habit_completion_rate") != 50 {
		t.Fatalf("trends = %v, want one row with habit_completion_rate 50", rows)
	}
	if n := pac42RowCount(t, user); n != 1 {
		t.Fatalf("%d stored rows, want 1 (nothing deleted or duplicated)", n)
	}
	if got := pac42StoredRate(t, user); got != 0.5 {
		t.Fatalf("stored rate = %v, want 0.5 (not rewritten)", got)
	}
}

// AC-11: no habit occurrences -> rate 0.
func TestPAC42NoHabitsRateIsZero(t *testing.T) {
	r := pac42Router(t)
	user := createTestUser(t, "pac42-ac11@example.com")
	week := pac42WeekObj(t, r, user, "2025-01-06")
	v, present := week["habit_completion_rate"]
	if !present || v != float64(0) {
		t.Fatalf("habit_completion_rate = %#v (present %v), want 0", v, present)
	}
}

// Contract plan requirement (U-01, ON CONFLICT half): a stale stored week is
// recomputed through the upsert's conflict path, keeps its id, and answers 200.
func TestPAC42StaleWeekIsRecomputedInPlace(t *testing.T) {
	r := pac42Router(t)
	user := createTestUser(t, "pac42-stale@example.com")
	stored := pac42Store(t, user, pac42Week, 0.1)
	if _, err := openTestDB(t).Exec(`UPDATE analytics_weeks SET computed_at = NOW() - INTERVAL '48 hours' WHERE id = $1`, stored.ID); err != nil {
		t.Fatal(err)
	}
	pac42Habits(t, user, 1, 1)

	week := pac42WeekObj(t, r, user, "2025-01-08")
	if week["id"] != stored.ID.String() {
		t.Fatalf("id = %v, want the stored row %s (updated, not a second row)", week["id"], stored.ID)
	}
	if week["week_start"] != "2025-01-06" {
		t.Fatalf("week_start = %#v, want \"2025-01-06\"", week["week_start"])
	}
	if got := pac42Num(t, week, "habit_completion_rate"); got != 50 {
		t.Fatalf("habit_completion_rate = %v, want the recomputed 50", got)
	}
	if n := pac42RowCount(t, user); n != 1 {
		t.Fatalf("%d stored rows, want 1", n)
	}
}

// Spec §6 characterisation: the trend read never computes, is newest first and
// falls back to 8 rows outside 1..52; the two reads carry the same fields, the
// single week adding only `breakdown` (five entries, fixed order and colours).
func TestPAC42TrendsOrderLimitAndFieldSet(t *testing.T) {
	r := pac42Router(t)
	user := createTestUser(t, "pac42-shape@example.com")
	if rows := pac42Trends(t, r, user, ""); len(rows) != 0 {
		t.Fatalf("no stored weeks: %d rows, want [] (the trend never computes)", len(rows))
	}
	if n := pac42RowCount(t, user); n != 0 {
		t.Fatalf("the trend read stored %d rows, want 0", n)
	}
	for i := 0; i < 10; i++ {
		pac42Store(t, user, pac42Week.AddDate(0, 0, -7*i), 0.5)
	}
	for _, tc := range []struct {
		query string
		n     int
	}{{"", 8}, {"?weeks=3", 3}, {"?weeks=0", 8}, {"?weeks=53", 8}, {"?weeks=52", 10}} {
		rows := pac42Trends(t, r, user, tc.query)
		if len(rows) != tc.n {
			t.Errorf("trends%s: %d rows, want %d", tc.query, len(rows), tc.n)
			continue
		}
		for i, row := range rows {
			if want := pac42Week.AddDate(0, 0, -7*i).Format("2006-01-02"); row["week_start"] != want {
				t.Errorf("trends%s row %d week_start = %v, want %s (newest first)", tc.query, i, row["week_start"], want)
			}
		}
	}

	trendKeys := pac42Keys(pac42Trends(t, r, user, "?weeks=1")[0])
	week := pac42WeekObj(t, r, user, "2025-01-06")
	weekKeys := pac42Keys(week)
	if want := strings.Join(append(append([]string{}, trendKeys...), "breakdown"), ","); strings.Join(sortedCopy(weekKeys), ",") != strings.Join(sortedCopy(strings.Split(want, ",")), ",") {
		t.Fatalf("week keys %v != trends keys %v + breakdown", weekKeys, trendKeys)
	}
	var cats []string
	for _, e := range week["breakdown"].([]any) {
		entry := e.(map[string]any)
		cats = append(cats, entry["category"].(string)+entry["color"].(string))
	}
	if got := strings.Join(cats, ","); got != "meeting#FF6B6B,focus#5B7FFF,habit#9B7AE0,personal#E9B949,free#B0BEC5" {
		t.Fatalf("breakdown order/colours = %s", got)
	}
	if titles, ok := week["top_meeting_titles"].([]any); !ok || len(titles) != 0 {
		t.Fatalf("top_meeting_titles = %#v, want []", week["top_meeting_titles"])
	}
}

func pac42Keys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedCopy(s []string) []string {
	c := append([]string{}, s...)
	sort.Strings(c)
	return c
}
