package engine

import (
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// ── inferCadence ──────────────────────────────────────────────────────────────

func TestInferCadence_Weekly(t *testing.T) {
	if got := inferCadence("RRULE:FREQ=WEEKLY;BYDAY=MO"); got != "weekly" {
		t.Errorf("expected weekly, got %q", got)
	}
}

func TestInferCadence_Biweekly(t *testing.T) {
	if got := inferCadence("RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=WE"); got != "biweekly" {
		t.Errorf("expected biweekly, got %q", got)
	}
}

func TestInferCadence_Monthly(t *testing.T) {
	if got := inferCadence("RRULE:FREQ=MONTHLY;BYDAY=1MO"); got != "monthly" {
		t.Errorf("expected monthly, got %q", got)
	}
}

func TestInferCadence_None(t *testing.T) {
	if got := inferCadence("RRULE:FREQ=DAILY"); got != "none" {
		t.Errorf("expected none, got %q", got)
	}
}

func TestInferCadence_CaseInsensitive(t *testing.T) {
	if got := inferCadence("rrule:freq=weekly;byday=tu"); got != "weekly" {
		t.Errorf("expected weekly, got %q", got)
	}
}

// ── nextExpectedDate ──────────────────────────────────────────────────────────

func newMemberWithCadence(cadence string, lastAt *time.Time, customDays *int) *storage.ManagerTeamMember {
	return &storage.ManagerTeamMember{
		Cadence:           cadence,
		LastOneOnOneAt:    lastAt,
		CadenceCustomDays: customDays,
	}
}

func TestNextExpectedDate_Weekly(t *testing.T) {
	last := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	m := newMemberWithCadence("weekly", &last, nil)
	e := &ManagerEngine{}
	got := e.nextExpectedDate(m)
	want := last.AddDate(0, 0, 7)
	if !got.Equal(want) {
		t.Errorf("weekly: got %v, want %v", got, want)
	}
}

func TestNextExpectedDate_Biweekly(t *testing.T) {
	last := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	m := newMemberWithCadence("biweekly", &last, nil)
	e := &ManagerEngine{}
	got := e.nextExpectedDate(m)
	want := last.AddDate(0, 0, 14)
	if !got.Equal(want) {
		t.Errorf("biweekly: got %v, want %v", got, want)
	}
}

func TestNextExpectedDate_Monthly(t *testing.T) {
	last := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	m := newMemberWithCadence("monthly", &last, nil)
	e := &ManagerEngine{}
	got := e.nextExpectedDate(m)
	want := last.AddDate(0, 1, 0)
	if !got.Equal(want) {
		t.Errorf("monthly: got %v, want %v", got, want)
	}
}

func TestNextExpectedDate_Custom(t *testing.T) {
	last := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	days := 21
	m := newMemberWithCadence("custom", &last, &days)
	e := &ManagerEngine{}
	got := e.nextExpectedDate(m)
	want := last.AddDate(0, 0, 21)
	if !got.Equal(want) {
		t.Errorf("custom: got %v, want %v", got, want)
	}
}

func TestNextExpectedDate_CustomZeroDays(t *testing.T) {
	last := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	days := 0
	m := newMemberWithCadence("custom", &last, &days)
	e := &ManagerEngine{}
	got := e.nextExpectedDate(m)
	if !got.IsZero() {
		t.Errorf("custom with 0 days should return zero time, got %v", got)
	}
}

func TestNextExpectedDate_NoLastDate(t *testing.T) {
	m := newMemberWithCadence("weekly", nil, nil)
	e := &ManagerEngine{}
	before := time.Now().UTC()
	got := e.nextExpectedDate(m)
	after := time.Now().UTC()
	// base is now(), so expected is now()+7d; just check it's roughly 7 days out
	minExpected := before.AddDate(0, 0, 7)
	maxExpected := after.AddDate(0, 0, 7)
	if got.Before(minExpected) || got.After(maxExpected) {
		t.Errorf("no last date weekly: got %v, expected roughly %v", got, minExpected)
	}
}

func TestNextExpectedDate_None(t *testing.T) {
	m := newMemberWithCadence("none", nil, nil)
	e := &ManagerEngine{}
	got := e.nextExpectedDate(m)
	if !got.IsZero() {
		t.Errorf("cadence=none should return zero time, got %v", got)
	}
}

// ── TrendPct ─────────────────────────────────────────────────────────────────

func TestTrendPct_Zero(t *testing.T) {
	if got := TrendPct(0, 0); got != 0 {
		t.Errorf("TrendPct(0,0) = %v, want 0", got)
	}
}

func TestTrendPct_NoPrior(t *testing.T) {
	if got := TrendPct(100, 0); got != 200 {
		t.Errorf("TrendPct(100,0) = %v, want 200", got)
	}
}

func TestTrendPct_Increase(t *testing.T) {
	got := TrendPct(120, 100)
	if got != 20.0 {
		t.Errorf("TrendPct(120,100) = %v, want 20.0", got)
	}
}

func TestTrendPct_Decrease(t *testing.T) {
	got := TrendPct(80, 100)
	if got != -20.0 {
		t.Errorf("TrendPct(80,100) = %v, want -20.0", got)
	}
}

func TestTrendPct_CapPositive(t *testing.T) {
	if got := TrendPct(10000, 1); got != 200 {
		t.Errorf("TrendPct cap positive: got %v, want 200", got)
	}
}

func TestTrendPct_MaxDecrease(t *testing.T) {
	// current=0, prior=100: -100% decrease
	got := TrendPct(0, 100)
	if got != -100.0 {
		t.Errorf("TrendPct(0,100) = %v, want -100.0", got)
	}
}

func TestTrendPct_Rounding(t *testing.T) {
	// 33.333... should round to 33.3
	got := TrendPct(133, 100)
	if got != 33.0 {
		t.Errorf("TrendPct(133,100) = %v, want 33.0", got)
	}
}

// ── calcEventDurationMinutes ──────────────────────────────────────────────────

func TestCalcEventDurationMinutes_Valid(t *testing.T) {
	start := "2026-04-20T10:00:00Z"
	end := "2026-04-20T10:30:00Z"
	if got := calcEventDurationMinutes(start, end); got != 30 {
		t.Errorf("calcEventDurationMinutes = %d, want 30", got)
	}
}

func TestCalcEventDurationMinutes_Invalid(t *testing.T) {
	if got := calcEventDurationMinutes("bad", "data"); got != 0 {
		t.Errorf("calcEventDurationMinutes with bad input = %d, want 0", got)
	}
}

func TestCalcEventDurationMinutes_Hour(t *testing.T) {
	start := "2026-04-20T09:00:00Z"
	end := "2026-04-20T10:00:00Z"
	if got := calcEventDurationMinutes(start, end); got != 60 {
		t.Errorf("calcEventDurationMinutes 60min = %d, want 60", got)
	}
}

// TestNextExpectedDate_Monthly_CalendarMonth pins down the fix for the
// "30 days != 1 month" bug. With a fixed clock at 2026-01-31, monthly cadence
// should return 2026-02-28 (or 02-29 in leap years), not 2026-03-02.
func TestNextExpectedDate_Monthly_CalendarMonth(t *testing.T) {
	last := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	m := newMemberWithCadence("monthly", &last, nil)
	e := &ManagerEngine{Clock: FixedClock{T: last}}
	got := e.nextExpectedDate(m)
	want := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC) // 2026 is not a leap year
	if !got.Equal(want) {
		t.Errorf("monthly cadence from 2026-01-31: got %v, want %v", got, want)
	}
}

