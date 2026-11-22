package api

// PAC-126: two creates whose generated slugs collide. GenerateSlug checks that
// its candidate is free and the insert comes later, so two concurrent creates
// by owners who share a first name (and first duration) both pick the same
// "<first name>-30min-<suffix>" (the suffix pinned, as it is random since the
// 2026-11-22 security audit). The second insert waits on the slug's unique
// index for the first transaction and then violates it. Before PAC-126 that was
// 500 "internal error" (PAC-119 spec §5, the follow-up it named); a CI e2e
// run hit it (pac112-cohosts.spec.ts AC-3 public slots, every e2e user's first
// name being "E2E"). A generated slug is the server's choice, so the server
// picks another one and the create succeeds.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// pac126Twins are two accounts with the same, run-unique first name, so their
// creates generate the same base slug. The third value is the stem both
// generate from, without the suffix.
//
// A generated slug also carries 32 bits of entropy since the 2026-11-22
// security audit (engine.GenerateSlug), which on its own would make two
// creates collide about once in four billion. The first two draws are pinned
// to the same suffix so the collision this file is about stays reachable —
// exactly what two owners sharing a first name hit before the audit — and
// later draws differ, so the loser's retry finds a slug of its own.
func (e *pac119Env) pac126Twins() (uuid.UUID, uuid.UUID, string) {
	e.t.Helper()
	first := "p126" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	a, b := e.owner("p126-a"), e.owner("p126-b")
	execT(e.t, `UPDATE users SET name = $2 WHERE id = $1`, a, first+" Alpha")
	execT(e.t, `UPDATE users SET name = $2 WHERE id = $1`, b, first+" Beta")
	pinnedSlugSuffixes(e.t, pac126PinnedSuffix, 2)
	return a, b, first + "-30min"
}

// pac126PinnedSuffix is the suffix both twins' first create draws.
const pac126PinnedSuffix = "deadbeef"

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
			a, b, stem := e.pac126Twins()
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
			if want := stem + "-" + pac126PinnedSuffix; sa.Slug != want {
				t.Errorf("A's slug %q; want the slug both generated, %q", sa.Slug, want)
			}
			if sb.Slug == sa.Slug || !strings.HasPrefix(sb.Slug, stem+"-") {
				t.Errorf("B's slug %q; want %q with a suffix of its own", sb.Slug, stem)
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

// pac126LoseSlug makes the first n create attempts lose their slug: the
// owner-host seam runs inside the create transaction right after the link
// insert, and answering it with the slug's conflict error rolls the attempt
// back exactly as a concurrent winner's index entry would. It returns how
// many attempts reached the seam.
func pac126LoseSlug(t *testing.T, n int) *int {
	t.Helper()
	calls := 0
	storage.InstallCreateOwnerHost(t, func(uuid.UUID) error {
		calls++
		if calls <= n {
			return &storage.LinkCreateConflictError{Constraint: storage.LinkSlugConstraint, Err: errors.New("pac126: slug taken")}
		}
		return nil
	})
	return &calls
}

// Two lost generated slugs: the third insert succeeds, under a slug of its own.
func TestPAC126_ThirdGeneratedSlugInsertSucceeds(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("p126-third")
	calls := pac126LoseSlug(t, 2)
	s := pac112Saved(t, e.send(owner, "", `{"title":"PAC-126 third"}`, ""), http.StatusCreated)
	if *calls != 3 {
		t.Errorf("%d inserts; want 3", *calls)
	}
	if n := e.linkCount(owner); n != 1 || s.Slug == "" {
		t.Errorf("%d links, slug %q; want 1 link", n, s.Slug)
	}
}

// Every generated slug lost: the third loss is the 500, after exactly three
// inserts, and nothing is left behind.
func TestPAC126_ThirdGeneratedSlugCollisionIs500(t *testing.T) {
	for _, c := range []struct {
		name  string
		keyed bool
	}{{"keyless", false}, {"keyed", true}} {
		t.Run(c.name, func(t *testing.T) {
			e := newPAC119Env(t)
			owner := e.owner("p126-500")
			key := ""
			if c.keyed {
				key = pac119NewKey()
			}
			calls := pac126LoseSlug(t, 1<<30)
			pac112Body(t, e.send(owner, key, `{"title":"PAC-126 lost"}`, ""), http.StatusInternalServerError, `{"error":"internal error"}`, "third loss")
			if *calls != 3 {
				t.Errorf("%d inserts; want 3", *calls)
			}
			if n, k := e.linkCount(owner), e.keyRows(owner); n != 0 || k != 0 {
				t.Errorf("%d links, %d key rows; want none", n, k)
			}
		})
	}
}
