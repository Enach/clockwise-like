package api

// PAC-41: GET /api/rooms as the web now relies on it. Booking a room sends the
// listed room's `id` unchanged as room_resource_email, so the 200 body's key
// set and the meaning of `id` are pinned here (AC-15), and the 401 the web
// reads as "no connected Google calendar" is pinned to mean exactly that: a
// stored Google credential that cannot be used is the 500, never the 401
// (contract listRooms, revisions 3 and 4; spec AC-16).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const pac41Helsinki = "c_1888helsinki@resource.calendar.google.com"

// pac41CalendarList stands in for Google Calendar's calendar list: the
// caller's own calendar, one room resource calendar named "Helsinki", and a
// shared calendar that is not a room.
func pac41CalendarList(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/users/me/calendarList") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind": "calendar#calendarList",
			"items": []map[string]any{
				{"kind": "calendar#calendarListEntry", "id": "pac41-rooms@example.com", "summary": "Me", "primary": true, "accessRole": "owner"},
				{"kind": "calendar#calendarListEntry", "id": pac41Helsinki, "summary": "Helsinki", "accessRole": "reader",
					"description": "6 seats", "location": "HQ, floor 3", "timeZone": "Europe/Helsinki"},
				{"kind": "calendar#calendarListEntry", "id": "team@group.calendar.google.com", "summary": "Team", "accessRole": "reader"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
}

// AC-15: the 200 answer is a JSON array whose Helsinki element has exactly the
// two keys the contract names (CalendarRoom: id, name), an id equal to the
// room's resource calendar address, and the name "Helsinki". Whatever else
// Google says about the room (description, location, time zone) never reaches
// the wire.
func TestPAC41_ListRoomsAnswersExactlyIDAndName(t *testing.T) {
	pac41CalendarList(t)
	db := openTestDB(t)
	user := createTestUser(t, "pac41-rooms@example.com")
	if err := auth.UpsertUserToken(db, user, &oauth2.Token{AccessToken: "pac41-a", RefreshToken: "pac41-r", Expiry: time.Now().Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	h := &eventHandlers{db: db, oauthConfig: &oauth2.Config{}}

	for _, target := range []string{"/api/rooms", "/api/rooms?q=hel", "/api/rooms?q=1888HELSINKI"} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.listRooms(rec, withUser(httptest.NewRequest(http.MethodGet, target, nil), user))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("Content-Type = %q", ct)
			}
			item := onlyElement(t, "listRooms", rec.Body.Bytes())
			assertKeySet(t, "CalendarRoom", item, "id", "name")
			var room struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := json.Unmarshal(item, &room); err != nil {
				t.Fatal(err)
			}
			if room.ID != pac41Helsinki || room.Name != "Helsinki" {
				t.Fatalf("room = %+v, want id %q (the resource calendar address) and name Helsinki", room, pac41Helsinki)
			}
			// The id is what PATCH /api/events/{id} accepts as room_resource_email.
			if !roomResourceEmail.MatchString(room.ID) {
				t.Fatalf("id %q does not match the room_resource_email pattern", room.ID)
			}
		})
	}

	// No match is an empty array, never null.
	rec := httptest.NewRecorder()
	h.listRooms(rec, withUser(httptest.NewRequest(http.MethodGet, "/api/rooms?q=oslo", nil), user))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("no match = %d %s, want 200 []", rec.Code, rec.Body.String())
	}
}

// AC-16 / contract listRooms: the web shows "room booking needs a connected
// Google calendar" only for the 401 calendar not connected, so that 401 must
// mean "no stored Google credential" and nothing else. A stored Google
// credential that cannot be used answers the 500.
func TestPAC41_ListRoomsUnusableGoogleCredentialIsThe500NotThe401(t *testing.T) {
	cases := []struct {
		name  string
		setup func(w *pac67World, u uuid.UUID)
		lost  bool
	}{
		{"lost: the provider permanently refused to renew it (PAC-67)", func(w *pac67World, u uuid.UUID) {
			w.d.Answer("pac41-dead", oauthfake.InvalidGrant)
			w.seedGoogle(u, "g-old", "pac41-dead", pac67Past)
		}, true},
		{"undecryptable (PAC-84)", func(w *pac67World, u uuid.UUID) {
			w.seedGoogle(u, "g", "pac41-r", pac67Future)
			w.exec(`UPDATE oauth_tokens SET refresh_token = $2 WHERE user_id = $1`, u, undecryptable)
		}, false},
		{"expired with no refresh token", func(w *pac67World, u uuid.UUID) {
			w.seedGoogle(u, "g-old", "", pac67Past)
		}, false},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newPAC67World(t)
			w.r.Get("/api/rooms", (&eventHandlers{db: w.db, oauthConfig: w.google}).listRooms)
			u := w.user(fmt.Sprintf("pac41-rooms-unusable-%d@example.com", i))
			c.setup(w, u)
			before := len(w.d.Calls(""))

			// The first read, and a second one after anything it recorded.
			for _, attempt := range []string{"first", "again"} {
				rec := w.do(u, http.MethodGet, "/api/rooms?q=hel", "")
				if rec.Code == http.StatusUnauthorized {
					t.Fatalf("%s read: 401 %s; an unusable stored Google credential must not read as calendar not connected", attempt, rec.Body.String())
				}
				assertBody(t, rec, http.StatusInternalServerError, internalError)
			}
			if c.lost {
				if !w.lost(u, storage.CredentialGoogle) {
					t.Error("the refused credential was not recorded as lost")
				}
			} else if n := len(w.d.Calls("")); n != before {
				t.Errorf("%d token requests made for a credential that cannot be used", n-before)
			}
		})
	}

	// And the 401 is exactly "no stored Google credential".
	t.Run("no stored Google credential", func(t *testing.T) {
		w := newPAC67World(t)
		w.r.Get("/api/rooms", (&eventHandlers{db: w.db, oauthConfig: w.google}).listRooms)
		u := w.user("pac41-rooms-none@example.com")
		assertBody(t, w.do(u, http.MethodGet, "/api/rooms", ""), http.StatusUnauthorized, `{"error":"calendar not connected"}`)
	})
}
