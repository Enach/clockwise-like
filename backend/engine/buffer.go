package engine

import (
	"time"

	"github.com/Enach/paceday/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// ComputeBufferBlocks returns intervals that should be treated as busy due to
// buffer time rules around qualifying meetings. workStart/workEnd clamp the
// buffers so they don't spill outside the user's working day.
func ComputeBufferBlocks(events []*googlecalendar.Event, s *storage.Settings, workStart, workEnd time.Time) []interval {
	if !s.BufferEnabled {
		return nil
	}
	if s.BufferBeforeMinutes == 0 && s.BufferAfterMinutes == 0 {
		return nil
	}

	minDur := s.BufferMinMeetingMinutes
	if minDur <= 0 {
		minDur = 1
	}

	// Collect qualifying meetings (opaque events long enough to warrant a buffer).
	var meetings []interval
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
		if int(end.Sub(start).Minutes()) < minDur {
			continue
		}
		meetings = append(meetings, interval{start, end})
	}

	if len(meetings) == 0 {
		return nil
	}

	before := time.Duration(s.BufferBeforeMinutes) * time.Minute
	after := time.Duration(s.BufferAfterMinutes) * time.Minute

	var buffers []interval
	for _, m := range meetings {
		if before > 0 {
			bStart := m.start.Add(-before)
			if bStart.Before(workStart) {
				bStart = workStart
			}
			if bStart.Before(m.start) {
				buffers = append(buffers, interval{bStart, m.start})
			}
		}
		if after > 0 {
			bEnd := m.end.Add(after)
			if bEnd.After(workEnd) {
				bEnd = workEnd
			}
			if bEnd.After(m.end) {
				buffers = append(buffers, interval{m.end, bEnd})
			}
		}
	}

	if !s.BufferSkipBackToBack {
		return buffers
	}

	// When BufferSkipBackToBack is true, drop any buffer that would overlap
	// another qualifying meeting — those gaps are already occupied.
	var result []interval
	for _, buf := range buffers {
		overlaps := false
		for _, m := range meetings {
			if buf.start.Before(m.end) && buf.end.After(m.start) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			result = append(result, buf)
		}
	}
	return result
}
