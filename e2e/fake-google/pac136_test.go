package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
)

// PAC-136: an insert with a caller-supplied id that is already taken, by a
// live event or by one that was deleted, is answered 409 duplicate, as the
// real service does; an update of a deleted event restores it.
func TestPAC136_InsertWithTakenIDIsDuplicate(t *testing.T) {
	_, srv := newTestServer(t)
	svc := client(t, srv.URL, "tok-a")
	ctx := context.Background()
	ev := func() *googlecalendar.Event {
		return &googlecalendar.Event{
			Id:      "pac136habit0001",
			Summary: "habit",
			Start:   &googlecalendar.EventDateTime{DateTime: "2030-01-07T07:00:00Z"},
			End:     &googlecalendar.EventDateTime{DateTime: "2030-01-07T07:30:00Z"},
		}
	}
	isDuplicate := func(err error) bool {
		var gerr *googleapi.Error
		if !errors.As(err, &gerr) || gerr.Code != http.StatusConflict {
			return false
		}
		for _, item := range gerr.Errors {
			if item.Reason == "duplicate" {
				return true
			}
		}
		return false
	}
	listed := func() []*googlecalendar.Event {
		t.Helper()
		list, err := svc.Events.List("primary").Context(ctx).
			TimeMin("2030-01-07T00:00:00Z").TimeMax("2030-01-08T00:00:00Z").Do()
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		return list.Items
	}

	created, err := svc.Events.Insert("primary", ev()).Context(ctx).Do()
	if err != nil || created.Id != "pac136habit0001" {
		t.Fatalf("first insert = %+v, %v; want the supplied id kept", created, err)
	}
	if _, err := svc.Events.Insert("primary", ev()).Context(ctx).Do(); !isDuplicate(err) {
		t.Fatalf("second insert error = %v, want 409 duplicate", err)
	}
	if n := len(listed()); n != 1 {
		t.Fatalf("%d events after a refused insert, want 1", n)
	}

	if err := svc.Events.Delete("primary", "pac136habit0001").Context(ctx).Do(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Events.Insert("primary", ev()).Context(ctx).Do(); !isDuplicate(err) {
		t.Fatalf("insert after delete error = %v, want 409 duplicate (the id stays taken)", err)
	}
	if n := len(listed()); n != 0 {
		t.Fatalf("%d events after delete, want 0", n)
	}

	restored := ev()
	restored.Status = "confirmed"
	restored.Start.DateTime, restored.End.DateTime = "2030-01-07T08:00:00Z", "2030-01-07T08:30:00Z"
	got, err := svc.Events.Update("primary", "pac136habit0001", restored).Context(ctx).Do()
	if err != nil || got.Id != "pac136habit0001" || got.Status != "confirmed" {
		t.Fatalf("update of the deleted event = %+v, %v; want it restored", got, err)
	}
	items := listed()
	if len(items) != 1 || items[0].Start.DateTime != "2030-01-07T08:00:00Z" {
		t.Fatalf("listed %+v, want the one restored event at 08:00", items)
	}

	// An insert without an id is never a duplicate.
	free := ev()
	free.Id = ""
	if _, err := svc.Events.Insert("primary", free).Context(ctx).Do(); err != nil {
		t.Fatalf("insert without id: %v", err)
	}
}
