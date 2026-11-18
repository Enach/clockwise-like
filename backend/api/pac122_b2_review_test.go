package api

// PAC-122 batch 2 review (#297), major 1: SF-069 deletes the provider
// meeting only when the event surely does not carry it.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/google/uuid"
)

func TestPAC122_SF069_NoAnswerFromGoogle(t *testing.T) {
	for _, tc := range []struct {
		name               string
		apply, failGet     bool
		wantDelete, report bool
	}{
		// The update reached Google and was applied; the answer was lost.
		{name: "applied", apply: true, wantDelete: false},
		// The update never took effect: the event carries no join URL.
		{name: "not applied", apply: false, wantDelete: true},
		// The event cannot be re-read: keep the meeting and report it.
		{name: "re-read fails", apply: true, failGet: true, wantDelete: false, report: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := lifecycletest.Record(t)
			w := newPAC67World(t)
			u := w.user("sf069r-" + uuid.NewString()[:6] + "@pac122.test")
			w.seedGoogle(u, "g-live-zoom", "g-r", pac67Future)
			w.seedZoom(u, "z", "z-r", &pac67Future)
			w.cal.mu.Lock()
			w.cal.dropPut, w.cal.applyPut = true, tc.apply
			w.cal.mu.Unlock()
			if tc.failGet {
				// Only the re-read fails: the first read has already happened
				// by the time the update is sent.
				w.cal.mu.Lock()
				w.cal.failGetAfterPut = true
				w.cal.mu.Unlock()
			}

			assertBody(t, w.do(u, http.MethodPost, "/api/events/e1/conference", `{"provider":"zoom"}`), http.StatusInternalServerError, internalError)
			if n := len(w.d.Calls("/api/meetings/81")); (n == 1) != tc.wantDelete || n > 1 {
				t.Fatalf("%d deletes of the meeting, want delete=%v", n, tc.wantDelete)
			}
			got := rec.Matching("conference-meeting-cleanup", "kept")
			if (len(got) == 1) != tc.report || len(got) > 1 {
				t.Fatalf("reports = %+v, want report=%v", rec.All(), tc.report)
			}
			for _, r := range got {
				if !strings.HasPrefix(r.Unit.ID, "zoom/") || strings.Contains(r.Err.Error(), "@") {
					t.Fatalf("report = %+v", r)
				}
			}
		})
	}
}
