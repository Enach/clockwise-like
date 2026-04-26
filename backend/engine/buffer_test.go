package engine

import (
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
)

func makeEvent(start, end time.Time, transparent bool) *googlecalendar.Event {
	ev := &googlecalendar.Event{
		Start: &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:   &googlecalendar.EventDateTime{DateTime: end.Format(time.RFC3339)},
	}
	if transparent {
		ev.Transparency = "transparent"
	}
	return ev
}

func TestComputeBufferBlocks_Disabled(t *testing.T) {
	s := &storage.Settings{BufferEnabled: false, BufferBeforeMinutes: 15, BufferAfterMinutes: 15}
	workStart := time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(2024, 1, 2, 17, 0, 0, 0, time.UTC)
	meetStart := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	meetEnd := time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC)
	events := []*googlecalendar.Event{makeEvent(meetStart, meetEnd, false)}

	got := ComputeBufferBlocks(events, s, workStart, workEnd)
	if len(got) != 0 {
		t.Errorf("expected 0 buffers when disabled, got %d", len(got))
	}
}

func TestComputeBufferBlocks_NoBufferMinutes(t *testing.T) {
	s := &storage.Settings{BufferEnabled: true, BufferBeforeMinutes: 0, BufferAfterMinutes: 0}
	workStart := time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(2024, 1, 2, 17, 0, 0, 0, time.UTC)
	meetStart := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	meetEnd := time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC)
	events := []*googlecalendar.Event{makeEvent(meetStart, meetEnd, false)}

	got := ComputeBufferBlocks(events, s, workStart, workEnd)
	if len(got) != 0 {
		t.Errorf("expected 0 buffers when both minutes=0, got %d", len(got))
	}
}

func TestComputeBufferBlocks_BeforeAndAfter(t *testing.T) {
	s := &storage.Settings{
		BufferEnabled:           true,
		BufferBeforeMinutes:     15,
		BufferAfterMinutes:      15,
		BufferMinMeetingMinutes: 30,
	}
	workStart := time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(2024, 1, 2, 17, 0, 0, 0, time.UTC)
	meetStart := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	meetEnd := time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC)
	events := []*googlecalendar.Event{makeEvent(meetStart, meetEnd, false)}

	got := ComputeBufferBlocks(events, s, workStart, workEnd)
	if len(got) != 2 {
		t.Fatalf("expected 2 buffers (before+after), got %d", len(got))
	}
	// Before buffer: [09:45, 10:00]
	wantBeforeStart := time.Date(2024, 1, 2, 9, 45, 0, 0, time.UTC)
	if !got[0].start.Equal(wantBeforeStart) || !got[0].end.Equal(meetStart) {
		t.Errorf("before buffer = [%v, %v], want [%v, %v]", got[0].start, got[0].end, wantBeforeStart, meetStart)
	}
	// After buffer: [11:00, 11:15]
	wantAfterEnd := time.Date(2024, 1, 2, 11, 15, 0, 0, time.UTC)
	if !got[1].start.Equal(meetEnd) || !got[1].end.Equal(wantAfterEnd) {
		t.Errorf("after buffer = [%v, %v], want [%v, %v]", got[1].start, got[1].end, meetEnd, wantAfterEnd)
	}
}

func TestComputeBufferBlocks_MeetingTooShort(t *testing.T) {
	s := &storage.Settings{
		BufferEnabled:           true,
		BufferBeforeMinutes:     15,
		BufferAfterMinutes:      15,
		BufferMinMeetingMinutes: 60,
	}
	workStart := time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(2024, 1, 2, 17, 0, 0, 0, time.UTC)
	// 30-minute meeting — below the 60-minute threshold
	meetStart := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	meetEnd := time.Date(2024, 1, 2, 10, 30, 0, 0, time.UTC)
	events := []*googlecalendar.Event{makeEvent(meetStart, meetEnd, false)}

	got := ComputeBufferBlocks(events, s, workStart, workEnd)
	if len(got) != 0 {
		t.Errorf("expected 0 buffers for short meeting, got %d", len(got))
	}
}

func TestComputeBufferBlocks_TransparentEventIgnored(t *testing.T) {
	s := &storage.Settings{
		BufferEnabled:           true,
		BufferBeforeMinutes:     15,
		BufferAfterMinutes:      15,
		BufferMinMeetingMinutes: 30,
	}
	workStart := time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(2024, 1, 2, 17, 0, 0, 0, time.UTC)
	meetStart := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	meetEnd := time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC)
	events := []*googlecalendar.Event{makeEvent(meetStart, meetEnd, true)} // transparent

	got := ComputeBufferBlocks(events, s, workStart, workEnd)
	if len(got) != 0 {
		t.Errorf("expected 0 buffers for transparent event, got %d", len(got))
	}
}

func TestComputeBufferBlocks_ClampedToWorkHours(t *testing.T) {
	s := &storage.Settings{
		BufferEnabled:           true,
		BufferBeforeMinutes:     30,
		BufferAfterMinutes:      30,
		BufferMinMeetingMinutes: 30,
	}
	workStart := time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(2024, 1, 2, 17, 0, 0, 0, time.UTC)
	// Meeting starts at 09:10 — before-buffer would go before workStart
	meetStart := time.Date(2024, 1, 2, 9, 10, 0, 0, time.UTC)
	meetEnd := time.Date(2024, 1, 2, 10, 10, 0, 0, time.UTC)
	events := []*googlecalendar.Event{makeEvent(meetStart, meetEnd, false)}

	got := ComputeBufferBlocks(events, s, workStart, workEnd)
	// Before buffer clamped to workStart: [09:00, 09:10]
	for _, buf := range got {
		if buf.start.Before(workStart) {
			t.Errorf("buffer start %v is before workStart %v", buf.start, workStart)
		}
		if buf.end.After(workEnd) {
			t.Errorf("buffer end %v is after workEnd %v", buf.end, workEnd)
		}
	}
}

func TestComputeBufferBlocks_SkipBackToBack(t *testing.T) {
	s := &storage.Settings{
		BufferEnabled:           true,
		BufferBeforeMinutes:     15,
		BufferAfterMinutes:      15,
		BufferMinMeetingMinutes: 30,
		BufferSkipBackToBack:    true,
	}
	workStart := time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(2024, 1, 2, 17, 0, 0, 0, time.UTC)

	// Two back-to-back meetings
	meet1Start := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	meet1End := time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC)
	meet2Start := time.Date(2024, 1, 2, 11, 5, 0, 0, time.UTC) // 5 min gap
	meet2End := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)

	events := []*googlecalendar.Event{
		makeEvent(meet1Start, meet1End, false),
		makeEvent(meet2Start, meet2End, false),
	}

	got := ComputeBufferBlocks(events, s, workStart, workEnd)
	// The after-buffer for meeting 1 [11:00, 11:15] overlaps meeting 2 [11:05, 12:00] — should be excluded.
	// The before-buffer for meeting 2 [10:50, 11:05] overlaps meeting 1 [10:00, 11:00] — should be excluded.
	// Should only have: before-buffer for meeting 1 [09:45, 10:00] and after-buffer for meeting 2 [12:00, 12:15].
	for _, buf := range got {
		// No buffer should overlap either meeting
		for _, ev := range events {
			start, _ := time.Parse(time.RFC3339, ev.Start.DateTime)
			end, _ := time.Parse(time.RFC3339, ev.End.DateTime)
			if buf.start.Before(end) && buf.end.After(start) {
				t.Errorf("buffer [%v, %v] overlaps meeting [%v, %v]", buf.start, buf.end, start, end)
			}
		}
	}
}

func TestComputeBufferBlocks_NoBackToBackSkip(t *testing.T) {
	s := &storage.Settings{
		BufferEnabled:           true,
		BufferBeforeMinutes:     15,
		BufferAfterMinutes:      15,
		BufferMinMeetingMinutes: 30,
		BufferSkipBackToBack:    false,
	}
	workStart := time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(2024, 1, 2, 17, 0, 0, 0, time.UTC)

	meet1Start := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	meet1End := time.Date(2024, 1, 2, 11, 0, 0, 0, time.UTC)
	meet2Start := time.Date(2024, 1, 2, 11, 30, 0, 0, time.UTC)
	meet2End := time.Date(2024, 1, 2, 12, 30, 0, 0, time.UTC)

	events := []*googlecalendar.Event{
		makeEvent(meet1Start, meet1End, false),
		makeEvent(meet2Start, meet2End, false),
	}

	got := ComputeBufferBlocks(events, s, workStart, workEnd)
	// Expect 4 buffers: before1, after1, before2, after2
	if len(got) != 4 {
		t.Errorf("expected 4 buffers, got %d", len(got))
	}
}

func TestComputeBufferBlocks_NoEvents(t *testing.T) {
	s := &storage.Settings{BufferEnabled: true, BufferBeforeMinutes: 15, BufferAfterMinutes: 15}
	workStart := time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(2024, 1, 2, 17, 0, 0, 0, time.UTC)

	got := ComputeBufferBlocks(nil, s, workStart, workEnd)
	if len(got) != 0 {
		t.Errorf("expected 0 buffers with no events, got %d", len(got))
	}
}
