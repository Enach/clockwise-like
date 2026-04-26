package nlp

import (
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
)

var testNow = time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC) // Tuesday

func baseSettings(tz string) *storage.Settings {
	return &storage.Settings{
		Timezone:  tz,
		WorkStart: "09:00",
		WorkEnd:   "18:00",
	}
}

func TestBuildSchedulingPromptData_SingleTimezone(t *testing.T) {
	s := baseSettings("Europe/Paris")
	p := []calendar.ParticipantInfo{
		{Email: "alice@co.com", Timezone: "Europe/Paris", WorkStart: "09:00", WorkEnd: "18:00"},
	}
	data := BuildSchedulingPromptData(s, p, testNow)

	if len(data.Participants) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(data.Participants))
	}
	ps := data.Participants[0]
	if ps.Email != "alice@co.com" {
		t.Errorf("Email = %q, want alice@co.com", ps.Email)
	}
	if ps.Timezone != "Europe/Paris" {
		t.Errorf("Timezone = %q, want Europe/Paris", ps.Timezone)
	}
	if ps.WorkStart != "09:00" || ps.WorkEnd != "18:00" {
		t.Errorf("WorkStart/WorkEnd = %q/%q", ps.WorkStart, ps.WorkEnd)
	}
	if ps.LocalNow == "" {
		t.Error("LocalNow should be set for known timezone")
	}
}

func TestBuildSchedulingPromptData_MultipleTimezones(t *testing.T) {
	s := baseSettings("Europe/Paris")
	p := []calendar.ParticipantInfo{
		{Email: "alice@co.com", Timezone: "Europe/Paris"},
		{Email: "bob@co.com", Timezone: "America/New_York"},
	}
	data := BuildSchedulingPromptData(s, p, testNow)

	if len(data.Participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(data.Participants))
	}
	// Both should have LocalNow set
	for _, ps := range data.Participants {
		if ps.LocalNow == "" {
			t.Errorf("%s: LocalNow should be set for known timezone", ps.Email)
		}
	}
	// Paris and New York are different → LocalNow should differ
	if data.Participants[0].LocalNow == data.Participants[1].LocalNow {
		t.Error("LocalNow for Paris and New York should differ")
	}
}

func TestBuildSchedulingPromptData_UnknownTimezone(t *testing.T) {
	s := baseSettings("UTC")
	p := []calendar.ParticipantInfo{
		{Email: "charlie@co.com"}, // no timezone
	}
	data := BuildSchedulingPromptData(s, p, testNow)

	ps := data.Participants[0]
	if ps.Timezone != "" {
		t.Errorf("Timezone should be empty for unknown participant, got %q", ps.Timezone)
	}
	if ps.LocalNow != "" {
		t.Errorf("LocalNow should be empty for unknown timezone, got %q", ps.LocalNow)
	}
}

func TestBuildSchedulingPromptData_FarTimezone(t *testing.T) {
	s := baseSettings("Europe/Paris")
	p := []calendar.ParticipantInfo{
		{Email: "tokyo@co.com", Timezone: "Asia/Tokyo"}, // UTC+9, Paris is UTC+2 → 7h gap
	}
	data := BuildSchedulingPromptData(s, p, testNow)

	if len(data.Participants) != 1 {
		t.Fatalf("expected 1 participant")
	}
	ps := data.Participants[0]
	if ps.Timezone != "Asia/Tokyo" {
		t.Errorf("Timezone = %q, want Asia/Tokyo", ps.Timezone)
	}
	// LocalNow should be set and different from Paris time
	if ps.LocalNow == "" {
		t.Error("LocalNow should be set for Asia/Tokyo")
	}
}

func TestSchedulingPromptTemplate_Renders(t *testing.T) {
	s := baseSettings("UTC")
	p := []calendar.ParticipantInfo{
		{Email: "alice@co.com", Timezone: "America/New_York", WorkStart: "09:00", WorkEnd: "17:00"},
	}
	data := BuildSchedulingPromptData(s, p, testNow)

	rendered, err := renderSchedulingPrompt(data)
	if err != nil {
		t.Fatalf("renderSchedulingPrompt: %v", err)
	}

	checks := []string{
		"calendar scheduling assistant",
		"alice@co.com",
		"America/New_York",
		"09:00–17:00",
		data.TodayISO,
		"schedule_meeting",
		"preferred_times",
	}
	for _, check := range checks {
		if !strings.Contains(rendered, check) {
			t.Errorf("rendered prompt missing %q", check)
		}
	}
}
