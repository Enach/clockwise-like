package api

import (
	"testing"

	"github.com/Enach/paceday/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
)

func TestSchedulingLinkUsagePolicyContract(t *testing.T) {
	reusable := &storage.SchedulingLink{UsageType: "reusable", UsesCount: 100}
	if linkExhausted(reusable) {
		t.Fatal("reusable links must remain available")
	}
	single := &storage.SchedulingLink{UsageType: "single_use", UsesCount: 1}
	if !linkExhausted(single) {
		t.Fatal("single-use links must exhaust after one booking")
	}
	max := 2
	recurring := &storage.SchedulingLink{UsageType: "recurring", UsesCount: 2, MaxUses: &max}
	if !linkExhausted(recurring) {
		t.Fatal("recurring links must exhaust at max_uses")
	}
	link := &storage.SchedulingLink{DurationOptions: []int{15, 30}}
	if durationAllowed(link, 45) || !durationAllowed(link, 30) {
		t.Fatal("duration policy did not enforce allowed options")
	}
}

func TestConferenceMetadataRoundTripContract(t *testing.T) {
	event := &googlecalendar.Event{Summary: "Planning"}
	setExternalConference(event, "custom", "https://meet.example.test/planning")
	provider, url := conferenceFromEvent(event)
	if provider != "custom" || url != "https://meet.example.test/planning" {
		t.Fatalf("conference metadata = %q %q", provider, url)
	}
	dto := toCalendarEventDTO(event)
	if dto.Conference == nil || dto.Conference.Provider != "custom" || dto.Conference.URL != url {
		t.Fatalf("calendar DTO conference = %+v", dto.Conference)
	}
	if !removeExternalConference(event) {
		t.Fatal("expected external conference metadata to be removed")
	}
	if _, url := conferenceFromEvent(event); url != "" {
		t.Fatalf("conference URL remained after removal: %q", url)
	}
}
