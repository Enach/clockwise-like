package api

// PAC-126: two creates whose generated slugs collide. GenerateSlug checks that
// its candidate is free and the insert comes later, so two concurrent creates
// by owners who share a first name (and first duration) both pick the same
// "<first name>-30min". The second insert waits on the slug's unique index for
// the first transaction and then violates it. Before PAC-126 that was
// 500 "internal error" (PAC-119 spec §5, the follow-up it named); a CI e2e
// run hit it (pac112-cohosts.spec.ts AC-3 public slots, every e2e user's first
// name being "E2E"). A generated slug is the server's choice, so the server
// picks another one and the create succeeds.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// pac126Twins are two accounts with the same, run-unique first name, so their
// creates generate the same base slug.
func (e *pac119Env) pac126Twins() (uuid.UUID, uuid.UUID, string) {
	e.t.Helper()
	first := "p126" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	a, b := e.owner("p126-a"), e.owner("p126-b")
	execT(e.t, `UPDATE users SET name = $2 WHERE id = $1`, a, first+" Alpha")
	execT(e.t, `UPDATE users SET name = $2 WHERE id = $1`, b, first+" Beta")
	return a, b, first + "-30min"
}

func TestPAC126_ConcurrentCreatesOfTheSameGeneratedSlugBothSucceed(t *testing.T) {
	for _, c := range []struct {
		name  string
		keyed bool
	}{
		{"keyless", false},
		{"keyed, each with its own key", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newPAC119Env(t)
			a, b, base := e.pac126Twins()
			keyA, keyB := "", ""
			if c.keyed {
				keyA, keyB = pac119NewKey(), pac119NewKey()
			}
			// A is held after its insert, before its commit: B then
			// generates the same free base slug and waits on A's index
			// entry.
			reached, release := pac119HoldFirstCommit(t)
			first := pac119Async(func() *httptest.ResponseRecorder {
				return e.send(a, keyA, `{"title":"PAC-126 A"}`, "")
			})
			<-reached
			second := pac119Async(func() *httptest.ResponseRecorder {
				return e.send(b, keyB, `{"title":"PAC-126 B"}`, "")
			})
			e.waitLockWaiters(1)
			release()
			sa := pac112Saved(t, pac119Wait(t, first, "A"), http.StatusCreated)
			sb := pac112Saved(t, pac119Wait(t, second, "B"), http.StatusCreated)
			if sa.Slug != base {
				t.Errorf("A's slug %q; want the base %q", sa.Slug, base)
			}
			if sb.Slug == sa.Slug || !strings.HasPrefix(sb.Slug, base+"-") {
				t.Errorf("B's slug %q; want %q with a suffix", sb.Slug, base)
			}
			for _, o := range []struct {
				who  string
				id   uuid.UUID
				link uuid.UUID
			}{{"A", a, sa.Id}, {"B", b, sb.Id}} {
				if n := e.linkCount(o.id); n != 1 {
					t.Errorf("%s owns %d links; want 1", o.who, n)
				}
				var hosts int
				if err := e.db.QueryRow(`SELECT COUNT(*) FROM scheduling_link_hosts WHERE link_id = $1 AND user_id = $2 AND status = 'accepted'`,
					o.link, o.id).Scan(&hosts); err != nil {
					t.Fatal(err)
				}
				if hosts != 1 {
					t.Errorf("%s: %d accepted owner rows; want 1", o.who, hosts)
				}
				if want := map[bool]int{false: 0, true: 1}[c.keyed]; e.keyRows(o.id) != want {
					t.Errorf("%s: %d key rows; want %d", o.who, e.keyRows(o.id), want)
				}
			}
		})
	}
}

// An explicit slug is the caller's choice: losing it to a concurrent create is
// not retried under another slug (PAC-119 contract md §1.1 step 7, unchanged).
func TestPAC126_ExplicitSlugIsNeverReplaced(t *testing.T) {
	e := newPAC119Env(t)
	a, b, _ := e.pac126Twins()
	slug := fmt.Sprintf("p126-explicit-%s", strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	reached, release := pac119HoldFirstCommit(t)
	first := pac119Async(func() *httptest.ResponseRecorder {
		return e.send(a, "", fmt.Sprintf(`{"title":"PAC-126 A","slug":%q}`, slug), "")
	})
	<-reached
	second := pac119Async(func() *httptest.ResponseRecorder {
		return e.send(b, "", fmt.Sprintf(`{"title":"PAC-126 B","slug":%q}`, slug), "")
	})
	e.waitLockWaiters(1)
	release()
	pac112Saved(t, pac119Wait(t, first, "A"), http.StatusCreated)
	w := pac119Wait(t, second, "B")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("B answered %d %s; want the 500 for a lost explicit slug", w.Code, w.Body.String())
	}
	if n := e.linkCount(b); n != 0 {
		t.Errorf("B owns %d links; want 0", n)
	}
	if _, err := storage.GetSchedulingLinkBySlug(e.db, slug); err != nil {
		t.Fatalf("A's link: %v", err)
	}
}
