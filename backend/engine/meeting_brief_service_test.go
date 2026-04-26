package engine

import (
	"testing"

	googlecalendar "google.golang.org/api/calendar/v3"
)

func TestBuildSearchTerms_Basic(t *testing.T) {
	event := &googlecalendar.Event{
		Summary: "Q2 Planning",
		Attendees: []*googlecalendar.EventAttendee{
			{DisplayName: "Alice Martin"},
			{DisplayName: "Bob"},
		},
	}
	terms := buildSearchTerms(event)
	if len(terms) == 0 {
		t.Fatal("expected at least one term")
	}
	found := false
	for _, t := range terms {
		if t == "Q2 Planning" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected meeting title in terms, got %v", terms)
	}
}

func TestBuildSearchTerms_Dedup(t *testing.T) {
	event := &googlecalendar.Event{
		Summary: "Review",
		Attendees: []*googlecalendar.EventAttendee{
			{DisplayName: "Review"},
		},
	}
	terms := buildSearchTerms(event)
	count := 0
	for _, term := range terms {
		if term == "Review" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'Review' deduplicated, got count=%d", count)
	}
}

func TestBuildSearchTerms_MaxTerms(t *testing.T) {
	event := &googlecalendar.Event{
		Summary: "Alpha Meeting",
		Attendees: []*googlecalendar.EventAttendee{
			{DisplayName: "Alice Smith"},
			{DisplayName: "Bob Jones"},
			{DisplayName: "Carol White"},
			{DisplayName: "David Brown"},
			{DisplayName: "Eve Davis"},
			{DisplayName: "Frank Miller"},
		},
	}
	terms := buildSearchTerms(event)
	if len(terms) > maxTerms {
		t.Errorf("got %d terms, want <= %d", len(terms), maxTerms)
	}
}

func TestBuildSearchTerms_ShortTermsSkipped(t *testing.T) {
	event := &googlecalendar.Event{
		Summary: "OK",
		Attendees: []*googlecalendar.EventAttendee{
			{DisplayName: "Jo"},
		},
	}
	terms := buildSearchTerms(event)
	if len(terms) != 0 {
		t.Errorf("expected no terms (all too short), got %v", terms)
	}
}

func TestBuildSearchTerms_LongTermTrimmed(t *testing.T) {
	longName := "VeryLongAttendeeNameThatExceedsTheMaximumAllowedTermLengthOfFiftyCharacters"
	event := &googlecalendar.Event{
		Summary:   longName,
		Attendees: []*googlecalendar.EventAttendee{},
	}
	terms := buildSearchTerms(event)
	if len(terms) == 0 {
		t.Fatal("expected one term")
	}
	runes := []rune(terms[0])
	if len(runes) > maxTermLen {
		t.Errorf("term length %d exceeds max %d", len(runes), maxTermLen)
	}
}

func TestBuildBriefPrompt_ContainsTitle(t *testing.T) {
	event := &googlecalendar.Event{
		Summary: "Budget Review",
		Attendees: []*googlecalendar.EventAttendee{
			{DisplayName: "Alice", Email: "alice@co.com"},
		},
		Start: &googlecalendar.EventDateTime{DateTime: "2026-04-28T10:00:00Z"},
	}
	prompt := buildBriefPrompt(event, nil, nil)
	if !briefContains(prompt, "Budget Review") {
		t.Error("prompt missing meeting title")
	}
	if !briefContains(prompt, "Alice") {
		t.Error("prompt missing attendee name")
	}
}

func TestBuildBriefPrompt_NoAttendees(t *testing.T) {
	event := &googlecalendar.Event{
		Summary:   "Solo Work",
		Attendees: []*googlecalendar.EventAttendee{},
	}
	prompt := buildBriefPrompt(event, nil, nil)
	if !briefContains(prompt, "Solo Work") {
		t.Error("prompt missing meeting title")
	}
}

func briefContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
