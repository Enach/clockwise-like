package calendar

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTimeSlot(t *testing.T) {
	now := time.Now()
	end := now.Add(time.Hour)
	ts := TimeSlot{Start: now, End: end}
	if !ts.Start.Equal(now) {
		t.Error("Start time mismatch")
	}
	if !ts.End.Equal(end) {
		t.Error("End time mismatch")
	}
}

func TestNewClient(t *testing.T) {
	token := &oauth2.Token{AccessToken: "fake-token"}
	ts := oauth2.StaticTokenSource(token)
	client, err := NewClient(context.Background(), ts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client == nil {
		t.Error("client should not be nil")
	}
	if client.CalendarID != "primary" {
		t.Errorf("CalendarID = %q, want primary", client.CalendarID)
	}
}
