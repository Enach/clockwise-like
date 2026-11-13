package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// AC-44 (spec revision 5.1, unconditional; owner OQ-6 yes): after the
// release's "advance every account" step, every session issued before it
// gets requireAuth's 401 on its next request, and no account, credential or
// setting changes. Run on its own database: the step advances every account.
func TestPAC71_AC44_ReleaseStepEndsEveryPreDeploySession(t *testing.T) {
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); _ = cleanup() })

	users := make([]uuid.UUID, 3)
	for i := range users {
		if err := db.QueryRow(`INSERT INTO users (email, name) VALUES ($1, 'U') RETURNING id`, uuid.NewString()+"@example.com").Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO settings (user_id, timezone) VALUES ($1, 'Europe/Paris')`, users[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expiry, calendar_id) VALUES ($1, 'a', 'r', NOW(), 'primary')`, users[i]); err != nil {
			t.Fatal(err)
		}
	}
	r := pac35Router(db, pac71Secret)
	me := func(id uuid.UUID, gen int64) int {
		return pac35Do(r, http.MethodGet, "/api/auth/me", sessionCookie(t, id, gen)).Code
	}
	for _, u := range users {
		if me(u, 0) != http.StatusOK {
			t.Fatal("pre-deploy session not valid before the step")
		}
	}
	var before string
	_ = db.QueryRow(`SELECT string_agg(u.id::text || u.email || s.timezone || o.access_token, ',' ORDER BY u.id)
		FROM users u JOIN settings s ON s.user_id = u.id JOIN oauth_tokens o ON o.user_id = u.id`).Scan(&before)

	n, err := storage.AdvanceAllGenerations(context.Background(), db, storage.OperatorRun{Operator: "release", Reason: "PAC-71 deploy (OQ-6)"})
	if err != nil || n != int64(len(users)) {
		t.Fatal(n, err)
	}
	for _, u := range users {
		if c := me(u, 0); c != http.StatusUnauthorized {
			t.Fatalf("pre-deploy session = %d, want 401", c)
		}
		if c := me(u, 1); c != http.StatusOK {
			t.Fatalf("a session issued after the step = %d", c)
		}
	}
	var after string
	_ = db.QueryRow(`SELECT string_agg(u.id::text || u.email || s.timezone || o.access_token, ',' ORDER BY u.id)
		FROM users u JOIN settings s ON s.user_id = u.id JOIN oauth_tokens o ON o.user_id = u.id`).Scan(&after)
	if after != before {
		t.Fatal("the step changed an account, credential or setting")
	}
}
