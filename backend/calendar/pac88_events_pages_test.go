package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// PAC-88 contract section 2.3 / C-10 (unit half): the one read per host is
// complete. ListEventsComplete follows nextPageToken to the last page, sends
// the same window on every page, and a failing page is the error (never a
// partial list). ListEvents keeps its first-page behaviour.

func TestPAC88_ListEventsCompleteFollowsEveryPage(t *testing.T) {
	_, reqs := recordingStandIn(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("pageToken") {
		case "":
			_, _ = w.Write([]byte(`{"items":[{"id":"p1"}],"nextPageToken":"t2"}`))
		case "t2":
			_, _ = w.Write([]byte(`{"items":[{"id":"p2a"},{"id":"p2b"}],"nextPageToken":"t3"}`))
		default:
			_, _ = w.Write([]byte(`{"items":[{"id":"p3"}]}`))
		}
	})
	from := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 44)
	c := newTestClient(t)
	events, err := c.ListEventsComplete(context.Background(), "primary", from, to)
	if err != nil {
		t.Fatalf("ListEventsComplete: %v", err)
	}
	var ids []string
	for _, e := range events {
		ids = append(ids, e.Id)
	}
	if fmt.Sprint(ids) != "[p1 p2a p2b p3]" {
		t.Errorf("events = %v, want every page in order", ids)
	}
	got := reqs()
	if len(got) != 3 {
		t.Fatalf("requests = %d, want 3 pages", len(got))
	}
	for i, r := range got {
		if r.q("timeMin") != from.Format(time.RFC3339) || r.q("timeMax") != to.Format(time.RFC3339) || r.q("singleEvents") != "true" {
			t.Errorf("page %d query = %v, want the same window on every page", i+1, r.query)
		}
	}
	if got[1].q("pageToken") != "t2" || got[2].q("pageToken") != "t3" {
		t.Errorf("page tokens = %q, %q", got[1].q("pageToken"), got[2].q("pageToken"))
	}

	// ListEvents is unchanged: one request, first page only.
	first, err := c.ListEvents(context.Background(), "primary", from, to)
	if err != nil || len(first) != 1 || first[0].Id != "p1" {
		t.Errorf("ListEvents = %d events, %v; want the first page only", len(first), err)
	}
}

func TestPAC88_ListEventsCompletePageFailureIsTheError(t *testing.T) {
	recordingStandIn(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = w.Write([]byte(`{"items":[{"id":"p1"}],"nextPageToken":"t2"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"backend error"}}`))
	})
	from := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	events, err := newTestClient(t).ListEventsComplete(context.Background(), "primary", from, from.AddDate(0, 0, 44))
	if err == nil || events != nil {
		t.Fatalf("ListEventsComplete = %d events, %v; want no list and the page-2 error", len(events), err)
	}
}

func TestPAC88_ListEventsCompleteEndlessPagesIsAnError(t *testing.T) {
	recordingStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[],"nextPageToken":"again"}`))
	})
	from := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	_, err := newTestClient(t).ListEventsComplete(context.Background(), "primary", from, from.Add(time.Hour))
	if !errors.Is(err, ErrEventPagesExceeded) {
		t.Fatalf("err = %v, want ErrEventPagesExceeded", err)
	}
}

func (r recordedRequest) q(key string) string { return url.Values(r.query).Get(key) }
