package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-23: GET /api/audit returns the caller's own entries and nothing else.
// Every request here goes through requireAuth, as the route does in routes.go.

const auditTestSecret = "audit-test-secret"

func auditServer(t *testing.T) http.Handler {
	return requireAuth(auditTestSecret)(auditHandler(openTestDB(t)))
}

func getAudit(t *testing.T, user uuid.UUID, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/audit"+query, nil)
	if user != uuid.Nil {
		tok, err := auth.GenerateToken(user, "audit@example.com", auditTestSecret)
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		req.AddCookie(&http.Cookie{Name: "auth_token", Value: tok})
	}
	w := httptest.NewRecorder()
	auditServer(t).ServeHTTP(w, req)
	return w
}

func decodeAudit(t *testing.T, w *httptest.ResponseRecorder) []storage.AuditEntry {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	var entries []storage.AuditEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return entries
}

// seedAuditRows inserts n entries for user, newest first by one second each.
func seedAuditRows(t *testing.T, user uuid.UUID, n int) {
	t.Helper()
	if _, err := openTestDB(t).Exec(`INSERT INTO audit_log (user_id, action, details, created_at)
		SELECT $1, 'focus_created', '{}', NOW() - make_interval(secs => g) FROM generate_series(1, $2::int) g`, user, n); err != nil {
		t.Fatalf("seed audit rows: %v", err)
	}
}

func TestListAuditEntries_ReturnsOnlyCallersEntries(t *testing.T) {
	db := openTestDB(t)
	a := createTestUser(t, "audit-http-a@example.com")
	b := createTestUser(t, "audit-http-b@example.com")
	storage.WriteAuditLog(db, a, "focus_created", `{"marker":"A-1"}`)
	storage.WriteAuditLog(db, a, "meeting_scheduled", `{"marker":"A-2"}`)
	storage.WriteAuditLog(db, b, "meeting_scheduled", `{"marker":"B-1"}`)

	for _, q := range []string{"", "?limit=500"} {
		entries := decodeAudit(t, getAudit(t, a, q))
		if len(entries) != 2 {
			t.Errorf("%q: A got %d entries, want 2: %+v", q, len(entries), entries)
		}
		for _, e := range entries {
			if !strings.Contains(e.Details, "A-") {
				t.Errorf("%q: A received an entry that is not A's: %+v", q, e)
			}
		}
	}
}

func TestListAuditEntries_EmptyLogIsEmptyArrayNotError(t *testing.T) {
	db := openTestDB(t)
	quiet := createTestUser(t, "audit-http-quiet@example.com")
	busy := createTestUser(t, "audit-http-busy@example.com")
	storage.WriteAuditLog(db, busy, "focus_created", `{}`)

	w := getAudit(t, quiet, "?limit=50")
	if body := strings.TrimSpace(w.Body.String()); w.Code != http.StatusOK || body != "[]" {
		t.Errorf("status %d body %q, want 200 []", w.Code, body)
	}
}

// AC-3: another user's meeting title and typed prompt appear in no response,
// whatever limit is sent.
func TestListAuditEntries_ForeignTitlesAndPromptsNeverAppear(t *testing.T) {
	db := openTestDB(t)
	reader := createTestUser(t, "audit-http-reader@example.com")
	victim := createTestUser(t, "audit-http-victim@example.com")
	const title = "1:1 re: PIP (audit-http)"
	const prompt = "call with the recruiter at Competitor (audit-http)"
	storage.WriteAuditLog(db, victim, "meeting_scheduled", `{"event_id":"v1","title":"`+title+`"}`)
	storage.WriteAuditLog(db, victim, "nlp_parsed", `{"text":"`+prompt+`","intent":"schedule_meeting"}`)
	storage.WriteAuditLog(db, reader, "focus_created", `{}`)

	for _, q := range []string{"", "?limit=1", "?limit=500", "?limit=10000", "?limit=-1", "?limit=abc", "?limit=10&limit=500"} {
		w := getAudit(t, reader, q)
		body := w.Body.String()
		if w.Code != http.StatusOK {
			t.Errorf("%q: status %d", q, w.Code)
		}
		if strings.Contains(body, title) || strings.Contains(body, prompt) || strings.Contains(body, "audit-http)") {
			t.Errorf("%q: response carries another user's text: %s", q, body)
		}
	}
}

// AC-4: the identical request as two users returns two different results.
func TestListAuditEntries_ResultDependsOnCaller(t *testing.T) {
	db := openTestDB(t)
	a := createTestUser(t, "audit-http-same-a@example.com")
	b := createTestUser(t, "audit-http-same-b@example.com")
	storage.WriteAuditLog(db, a, "focus_created", `{"who":"a"}`)
	storage.WriteAuditLog(db, b, "focus_cleared", `{"who":"b"}`)

	wa, wb := getAudit(t, a, "?limit=50"), getAudit(t, b, "?limit=50")
	if wa.Body.String() == wb.Body.String() {
		t.Errorf("both callers received %s", wa.Body.String())
	}
	if ea := decodeAudit(t, wa); len(ea) != 1 || ea[0].Action != "focus_created" {
		t.Errorf("A got %+v", ea)
	}
	if eb := decodeAudit(t, wb); len(eb) != 1 || eb[0].Action != "focus_cleared" {
		t.Errorf("B got %+v", eb)
	}
}

// AC-8: an entry with no subject (recorded before migration 026) is returned
// to nobody.
func TestListAuditEntries_PreMigrationRowsReturnedToNobody(t *testing.T) {
	db := openTestDB(t)
	u := createTestUser(t, "audit-http-premigration@example.com")
	const marker = "pre-migration-row (audit-http)"
	// Migration 026 refuses a new row with no subject, so the row is written
	// the way history arrives: constraint dropped, row inserted, constraint
	// re-added NOT VALID, in one transaction.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`ALTER TABLE audit_log DROP CONSTRAINT audit_log_user_id_present`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO audit_log (action, details, created_at) VALUES ('nlp_parsed', $1, NOW())`, marker); err != nil {
		t.Fatalf("seed unattributed row: %v", err)
	}
	if _, err := tx.Exec(`ALTER TABLE audit_log ADD CONSTRAINT audit_log_user_id_present CHECK (user_id IS NOT NULL) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var stored int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE details = $1 AND user_id IS NULL`, marker).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("unattributed row not stored (count %d, err %v)", stored, err)
	}
	for _, q := range []string{"", "?limit=500"} {
		if body := getAudit(t, u, q).Body.String(); strings.Contains(body, marker) {
			t.Errorf("%q: pre-migration row returned: %s", q, body)
		}
	}
}

// AC-10: no limit -> the contract's default, 50 (it was 100).
func TestListAuditEntries_DefaultLimitMatchesContract(t *testing.T) {
	u := createTestUser(t, "audit-http-default@example.com")
	seedAuditRows(t, u, 120)
	for _, q := range []string{"", "?limit=", "?limit=abc", "?limit=0", "?limit=-5", "?limit=+50"} {
		if got := len(decodeAudit(t, getAudit(t, u, q))); got != 50 {
			t.Errorf("%q: got %d entries, want 50", q, got)
		}
	}
	if got := len(decodeAudit(t, getAudit(t, u, "?limit=10"))); got != 10 {
		t.Errorf("?limit=10: got %d entries, want 10", got)
	}
}

// AC-11: above the maximum -> the maximum, 500, never a smaller number;
// including a value that overflows int64.
func TestListAuditEntries_OverMaximumLimitCaps(t *testing.T) {
	u := createTestUser(t, "audit-http-cap@example.com")
	seedAuditRows(t, u, 520)
	for _, q := range []string{"?limit=501", "?limit=10000", "?limit=99999999999999999999"} {
		if got := len(decodeAudit(t, getAudit(t, u, q))); got != 500 {
			t.Errorf("%q: got %d entries, want 500", q, got)
		}
	}
}

// AC-12: refused exactly as today, by the middleware.
func TestListAuditEntries_UnauthenticatedIsRefused(t *testing.T) {
	w := getAudit(t, uuid.Nil, "?limit=50")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if w.Body.String() != "{\"error\":\"unauthorized\"}\n" {
		t.Errorf("body = %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", w.Header().Get("X-Content-Type-Options"))
	}
}

// The handler's own guard, for a context with no user (unreachable behind
// requireAuth today; kept so a routing change cannot turn it into a read of
// nobody's — or everybody's — log).
func TestListAuditEntries_NoUserInContextIsRefused(t *testing.T) {
	w := httptest.NewRecorder()
	auditHandler(openTestDB(t)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
	if w.Code != http.StatusUnauthorized || w.Body.String() != "{\"error\":\"unauthorized\"}\n" {
		t.Errorf("status %d body %q, want the middleware's 401", w.Code, w.Body.String())
	}
}

// AC-15: details that are not valid JSON (a title with a double quote, as the
// concatenating writers produce) still serialise and come back verbatim.
func TestListAuditEntries_DetailsContainingQuoteRoundTrips(t *testing.T) {
	db := openTestDB(t)
	u := createTestUser(t, "audit-http-quote@example.com")
	details := `{"event_id":"e1","title":"say "hi" \ now"}`
	storage.WriteAuditLog(db, u, "meeting_created", details)

	entries := decodeAudit(t, getAudit(t, u, "?limit=50"))
	if len(entries) != 1 || entries[0].Details != details {
		t.Errorf("entries = %+v, want details %q", entries, details)
	}
	if entries[0].CreatedAt.IsZero() || time.Since(entries[0].CreatedAt) > time.Hour {
		t.Errorf("created_at = %v", entries[0].CreatedAt)
	}
}
