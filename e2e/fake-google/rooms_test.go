package main

import (
	"context"
	"testing"

	googlecalendar "google.golang.org/api/calendar/v3"
)

// PAC-41 OQ-4: the stand-in keeps a room resource attendee, flag included,
// across the backend's write path (Events.Get, then Events.Update with the
// merged attendees), and answers it on the next read, so AC-1 and AC-14 can
// be observed on the stored event without a stand-in change.
func TestRoomResourceAttendee_SurvivesUpdateAndNextRead(t *testing.T) {
	store, srv := newTestServer(t)
	svc := client(t, srv.URL, "tok-a")
	ctx := context.Background()
	const helsinki = "c_1888helsinki@resource.calendar.google.com"
	const oslo = "c_1888oslo@resource.calendar.google.com"

	created, err := svc.Events.Insert("primary", ev("sync", "2030-01-07T10:00:00Z", "2030-01-07T11:00:00Z")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Book Helsinki, as PATCH /api/events/{id} does: read, add, write back.
	got, err := svc.Events.Get("primary", created.Id).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got.Attendees = append(got.Attendees, &googlecalendar.EventAttendee{Email: helsinki, Resource: true})
	if _, err := svc.Events.Update("primary", created.Id, got).Context(ctx).Do(); err != nil {
		t.Fatalf("update: %v", err)
	}

	// A later edit reads Helsinki back with its flag and adds Oslo beside it.
	again, err := svc.Events.Get("primary", created.Id).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if len(again.Attendees) != 1 || again.Attendees[0].Email != helsinki || !again.Attendees[0].Resource {
		t.Fatalf("next read attendees = %+v, want Helsinki as a resource", again.Attendees)
	}
	again.Attendees = append(again.Attendees, &googlecalendar.EventAttendee{Email: oslo, Resource: true})
	again.Location = "Café"
	if _, err := svc.Events.Update("primary", created.Id, again).Context(ctx).Do(); err != nil {
		t.Fatalf("update again: %v", err)
	}

	// What the control plane answers (e2e/fake.ts fake.events) carries both.
	stored, ok := store.Get("a@example.test", created.Id)
	if !ok {
		t.Fatal("event gone")
	}
	rooms := map[string]bool{}
	for _, a := range stored.Attendees {
		if a.Resource {
			rooms[a.Email] = true
		}
	}
	if !rooms[helsinki] || !rooms[oslo] || len(rooms) != 2 || stored.Location != "Café" {
		t.Fatalf("stored event attendees %+v location %q, want Helsinki and Oslo as resources and Café", stored.Attendees, stored.Location)
	}
}
