// Command fake-google is the e2e suite's stand-in for the Google Calendar API
// (PAC-43). The backend reaches it through GOOGLE_CALENDAR_API_BASE_URL, with
// the same requests, the same bearer credential and the same client library it
// uses against the real service. It is a test fixture, never a deployment
// component: it holds state in memory, trusts its control plane completely and
// has no TLS.
//
// Two surfaces share one listener:
//
//	/calendar/v3/...  the product surface: the seven operations the backend calls
//	/__fixture/...    the control plane the tests drive (fixture.go)
//
// Every product request is recorded (store.go LogEntry) with the label of the
// credential it presented, so a test can attribute an arrival to the user and
// the path that caused it.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const apiRoot = "/calendar/v3/"

func main() {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	// The image is FROM scratch, so the compose healthcheck runs the binary
	// itself: `fake-google -healthcheck`.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck("http://127.0.0.1" + addr + "/__fixture/health"))
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           NewServer(NewStore()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("fake-google listening on %s (product surface %s, control plane /__fixture/)", addr, apiRoot)
	log.Fatal(srv.ListenAndServe())
}

func healthcheck(url string) int {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// Server is the stand-in's HTTP surface.
type Server struct {
	store *Store
	mux   *http.ServeMux
}

// NewServer wires both surfaces onto one handler.
func NewServer(store *Store) *Server {
	s := &Server{store: store, mux: http.NewServeMux()}
	s.mux.HandleFunc(apiRoot, s.product)
	s.registerFixture()
	s.registerIdP() // PAC-50: the SSO admin step-up's identity provider (idp.go)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// call is one product request in flight. Handlers fill in what they learn
// (identifiers in a body, for instance) so the log entry is complete.
type call struct {
	entry      LogEntry
	credential Credential
	calendarID string
	eventID    string
}

// primary resolves the "primary" alias the way the real service does: to the
// calendar of the credential's owner.
func (c *call) resolve(id string) string {
	if id == "primary" {
		return c.credential.Calendar
	}
	return id
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) product(w http.ResponseWriter, r *http.Request) {
	op, calID, eventID, ok := route(r)
	c := &call{entry: LogEntry{
		At:     time.Now().UTC(),
		Method: r.Method,
		Path:   r.URL.Path,
	}}
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		c.entry.Status = sw.status
		s.store.Record(c.entry)
	}()

	c.entry.Operation = op
	c.entry.TimeMin = r.URL.Query().Get("timeMin")
	c.entry.TimeMax = r.URL.Query().Get("timeMax")

	// The credential is checked before the route, as the real service does:
	// an unauthenticated request learns nothing, not even "no such path".
	bearer, hasBearer := bearerOf(r)
	cred, accepted := s.store.Lookup(bearer)
	switch {
	case !hasBearer:
		c.entry.Credential = ""
		writeGoogleError(sw, http.StatusUnauthorized, "UNAUTHENTICATED", "authError",
			"Request is missing required authentication credential.")
		return
	case !accepted:
		c.entry.Credential = "rejected"
		writeGoogleError(sw, http.StatusUnauthorized, "UNAUTHENTICATED", "authError",
			"Request had invalid authentication credentials.")
		return
	}
	c.credential = cred
	c.entry.Credential = cred.Label

	if !ok {
		writeGoogleError(sw, http.StatusNotFound, "NOT_FOUND", "notFound", "Not Found")
		return
	}
	c.calendarID = c.resolve(calID)
	c.eventID = eventID
	c.entry.CalendarID = c.calendarID
	c.entry.EventID = eventID

	// Redirect mode (AC-11): answer this one request with a 302 and nothing
	// else. The backend must not follow it.
	if loc, armed := s.store.TakeRedirect(cred.Label); armed {
		sw.Header().Set("Location", loc)
		sw.WriteHeader(http.StatusFound)
		return
	}

	switch op {
	case "events.list":
		s.listEvents(sw, r, c)
	case "events.insert":
		s.insertEvent(sw, r, c)
	case "events.get":
		s.getEvent(sw, r, c)
	case "events.update":
		s.updateEvent(sw, r, c)
	case "events.delete":
		s.deleteEvent(sw, r, c)
	case "freebusy.query":
		s.freeBusy(sw, r, c)
	case "calendarList.list":
		s.calendarList(sw, r, c)
	}
}

// route maps a product request to an operation. It answers ok=false for any
// path or method the backend does not use, so an unexpected call is a visible
// 404 in the log rather than a silent success.
func route(r *http.Request) (op, calendarID, eventID string, ok bool) {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), strings.TrimSuffix(apiRoot, "/"))
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	for i, p := range parts {
		if u, err := url.PathUnescape(p); err == nil {
			parts[i] = u
		}
	}
	switch {
	case len(parts) == 1 && parts[0] == "freeBusy" && r.Method == http.MethodPost:
		return "freebusy.query", "", "", true
	case len(parts) == 3 && parts[0] == "users" && parts[1] == "me" && parts[2] == "calendarList" && r.Method == http.MethodGet:
		return "calendarList.list", "", "", true
	case len(parts) == 3 && parts[0] == "calendars" && parts[2] == "events":
		switch r.Method {
		case http.MethodGet:
			return "events.list", parts[1], "", true
		case http.MethodPost:
			return "events.insert", parts[1], "", true
		}
	case len(parts) == 4 && parts[0] == "calendars" && parts[2] == "events":
		switch r.Method {
		case http.MethodGet:
			return "events.get", parts[1], parts[3], true
		case http.MethodPut:
			return "events.update", parts[1], parts[3], true
		case http.MethodDelete:
			return "events.delete", parts[1], parts[3], true
		}
	}
	return "unknown", "", "", false
}

func bearerOf(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// writeGoogleError answers with the real service's error envelope, so the
// client library produces the same *googleapi.Error it would in production.
func writeGoogleError(w http.ResponseWriter, code int, status, reason, message string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"status":  status,
			"errors": []map[string]string{{
				"message": message,
				"domain":  "global",
				"reason":  reason,
			}},
		},
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
