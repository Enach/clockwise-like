package api

// PAC-106, the rule half: settingsRefusal is PAC-72's own offerable-start
// rule, applied to a link row read later than the request's. These are pure
// checks on the rule, with no request and no database: what the save decides
// when it is handed a row whose settings moved.

import (
	"errors"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
)

// pac106Link is a link open Monday to Friday, 09:00 to 17:00, 30-minute
// slots, no notice, the default horizon.
func pac106Link() *storage.SchedulingLink {
	return &storage.SchedulingLink{
		DurationOptions: []int{30}, DaysOfWeek: []int{1, 2, 3, 4, 5},
		WindowStart: "09:00", WindowEnd: "17:00", BookingHorizonDays: 60,
	}
}

func TestPAC106_SettingsRefusalIsTheRuleOnTheNewerRow(t *testing.T) {
	// Monday 3 March 2031, and a slot the link above offers on the Wednesday
	// two days later: far enough ahead that a shrunk horizon and a raised
	// notice can both reach it.
	now := time.Date(2031, 3, 3, 8, 0, 0, 0, time.UTC)
	start := time.Date(2031, 3, 5, 10, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)

	for _, tc := range []struct {
		name    string
		change  func(*storage.SchedulingLink)
		reason  gen.BookingRefusalReason
		refused bool
	}{
		{"unchanged", func(*storage.SchedulingLink) {}, "", false},
		{"title only", func(l *storage.SchedulingLink) { l.Title = "renamed" }, "", false},
		{"window widened", func(l *storage.SchedulingLink) { l.WindowStart, l.WindowEnd = "08:00", "18:00" }, "", false},
		{"duration added", func(l *storage.SchedulingLink) { l.DurationOptions = []int{30, 60} }, "", false},
		{"horizon shrunk", func(l *storage.SchedulingLink) { l.BookingHorizonDays = 1 }, gen.BeyondHorizon, true},
		{"hours moved", func(l *storage.SchedulingLink) { l.WindowStart = "13:00" }, gen.OutsideLinkHours, true},
		{"day dropped", func(l *storage.SchedulingLink) { l.DaysOfWeek = []int{1, 2, 4, 5} }, gen.OutsideLinkHours, true},
		{"grid shifted off the start", func(l *storage.SchedulingLink) { l.WindowStart = "09:10" }, gen.OutsideLinkHours, true},
		{"window closes before the end", func(l *storage.SchedulingLink) { l.WindowEnd = "10:15" }, gen.OutsideLinkHours, true},
		{"duration withdrawn", func(l *storage.SchedulingLink) { l.DurationOptions = []int{60} }, gen.OutsideLinkHours, true},
		{"notice raised", func(l *storage.SchedulingLink) { l.MinNoticeMinutes = 7 * 24 * 60 }, gen.TooSoon, true},
		// The contract's precedence survives the duration check: a withdrawn
		// duration never shadows beyond_horizon, and never loses to too_soon.
		{"duration withdrawn and horizon shrunk", func(l *storage.SchedulingLink) {
			l.DurationOptions, l.BookingHorizonDays = []int{60}, 1
		}, gen.BeyondHorizon, true},
		{"duration withdrawn and notice raised", func(l *storage.SchedulingLink) {
			l.DurationOptions, l.MinNoticeMinutes = []int{60}, 7*24*60
		}, gen.OutsideLinkHours, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := pac106Link()
			tc.change(link)
			reason, refused := settingsRefusal(link, time.UTC, start, end, 30, now)
			if refused != tc.refused || reason != tc.reason {
				t.Fatalf("settingsRefusal = %q, %v; want %q, %v", reason, refused, tc.reason, tc.refused)
			}
		})
	}
}

// On settings that did not move, the rule applied at the save is the rule the
// request applied: for every row the request would accept, the save accepts,
// and for every row it would refuse, the save refuses with the same reason.
// The only difference is the duration options, which the request checks just
// before the rule (its own 400) and the save folds into the grid.
func TestPAC106_SettingsRefusalAgreesWithTheRequestsOwnPass(t *testing.T) {
	now := time.Date(2031, 3, 3, 8, 0, 0, 0, time.UTC) // Monday
	for _, start := range []time.Time{
		time.Date(2031, 3, 5, 10, 0, 0, 0, time.UTC),  // offered
		time.Date(2031, 3, 5, 7, 0, 0, 0, time.UTC),   // before the window
		time.Date(2031, 3, 5, 16, 45, 0, 0, time.UTC), // ends after the window
		time.Date(2031, 3, 5, 10, 5, 0, 0, time.UTC),  // off the grid
		time.Date(2031, 3, 8, 10, 0, 0, 0, time.UTC),  // a Saturday
		time.Date(2031, 8, 5, 10, 0, 0, 0, time.UTC),  // beyond the horizon
		time.Date(2031, 3, 2, 10, 0, 0, 0, time.UTC),  // the day before today
	} {
		link := pac106Link()
		end := start.Add(30 * time.Minute)
		wantReason, wantRefused := cheapRefusal(link, time.UTC, start, end, 30, now)
		reason, refused := settingsRefusal(link, time.UTC, start, end, 30, now)
		if refused != wantRefused || reason != wantReason {
			t.Errorf("%s: the save says %q, %v; the request's own pass says %q, %v",
				start.Format(time.RFC3339), reason, refused, wantReason, wantRefused)
		}
	}
}

// The check the handler hands the save reports a refusal the handler can
// answer (it carries the rule's reason) and passes a row that still offers
// the slot.
func TestPAC106_TheCheckCarriesTheRulesReason(t *testing.T) {
	now := time.Date(2031, 3, 3, 8, 0, 0, 0, time.UTC)
	start := time.Date(2031, 3, 5, 10, 0, 0, 0, time.UTC)
	check := bookingSettingsCheck(time.UTC, start, start.Add(30*time.Minute), 30, now)

	if err := check(pac106Link()); err != nil {
		t.Fatalf("a row that still offers the slot: %v, want nil", err)
	}

	moved := pac106Link()
	moved.BookingHorizonDays = 1
	err := check(moved)
	var refusal *settingsRefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("a row that no longer offers the slot: %v, want a settings refusal", err)
	}
	if refusal.reason != gen.BeyondHorizon {
		t.Errorf("reason %q, want %q", refusal.reason, gen.BeyondHorizon)
	}
	if !errors.Is(err, storage.ErrLinkSettingsChanged) {
		t.Error("the refusal does not unwrap to storage.ErrLinkSettingsChanged: the save would read it as a storage failure")
	}
}
