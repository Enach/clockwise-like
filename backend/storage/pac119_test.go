package storage

// PAC-119 (contracts/features/PAC-119.md) at the storage layer: migration
// 040, the canonical digest v1, the keyed create transaction (key claimed
// before the host work, storage-owned outcome encoding, unique violations by
// constraint name), the resolution's precedence, the cleanup (AC-12) and the
// PAC-80 seams.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func pac119Key() string { return "pac119-" + strings.ReplaceAll(uuid.NewString(), "-", "") }

func pac119Link(owner uuid.UUID, title string) *SchedulingLink {
	return &SchedulingLink{OwnerUserID: owner, Slug: "pac119-s-" + uuid.NewString()[:8], Title: title,
		DurationOptions: []int{30}, DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00",
		UsageType: "reusable", BookingHorizonDays: DefaultBookingHorizonDays, RefusalMessage: RefusalGeneric}
}

// pac119Create is one keyed create through the production path: R1 computes
// the digest, then the create transaction claims the key.
func pac119Create(t *testing.T, db *sql.DB, l *SchedulingLink, emails []string, key string) (*SchedulingLink, *CoHostChanges, []byte) {
	t.Helper()
	pre := *l
	pre.Slug = ""
	a := &LinkCreateAttempt{Key: key, Link: &pre, CoHostEmails: emails}
	res, err := ResolveLinkCreateKeyCtx(context.Background(), db, l.OwnerUserID, a)
	if err != nil || res.Outcome != LinkCreateKeyNotStored {
		t.Fatalf("R1 before the create: %+v %v", res, err)
	}
	created, changes, _, err := CreateSchedulingLinkWithCoHostsKeyedCtx(context.Background(), db, l, emails, key, a.Digest)
	if err != nil {
		t.Fatalf("keyed create: %v", err)
	}
	return created, changes, a.Digest
}

func pac119Resolve(t *testing.T, db *sql.DB, owner uuid.UUID, l *SchedulingLink, emails []string, key string) *LinkCreateKeyResolution {
	t.Helper()
	pre := *l
	pre.Slug = ""
	res, err := ResolveLinkCreateKeyCtx(context.Background(), db, owner, &LinkCreateAttempt{Key: key, Link: &pre, CoHostEmails: emails})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return res
}

// Contract md §5: migration 040 applies, its CHECKs hold, the down file drops
// only the key table (links and hosts survive), and it applies again.
func TestPAC119_Migration040UpDownUp(t *testing.T) {
	db, to := migrationTestDB(t)
	to(39)
	u := seedMigrationUser(t, db, "m40@example.com", time.Now())
	var link uuid.UUID
	if err := db.QueryRow(`INSERT INTO scheduling_links (owner_user_id, slug, title, duration_options, days_of_week,
		window_start_time, window_end_time) VALUES ($1, 'm40', 'M40', '{30}', '{1}', '09:00', '17:00') RETURNING id`, u).Scan(&link); err != nil {
		t.Fatal(err)
	}
	to(40)
	digest := make([]byte, 32)
	for _, bad := range []struct {
		what string
		key  string
		dig  []byte
		json any
	}{
		{"a 15-character key", "abcdefghijklmno", digest, nil},
		{"a key outside the set", "abcdefghijklmnop!", digest, nil},
		{"a 31-byte digest", "abcdefghijklmnopq", make([]byte, 31), nil},
		{"outcomes without a digest", "abcdefghijklmnopq", nil, `{"addresses":[]}`},
	} {
		if _, err := db.Exec(`INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, request_sha256, co_host_changes)
			VALUES ($1, $2, $3, $4, $5)`, u, bad.key, link, bad.dig, bad.json); err == nil {
			t.Errorf("%s accepted", bad.what)
		}
	}
	if _, err := db.Exec(`INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, request_sha256)
		VALUES ($1, 'abcdefghijklmnop', $2, $3)`, u, link, digest); err != nil {
		t.Fatalf("a well-formed row refused: %v", err)
	}
	to(39)
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.scheduling_link_create_keys') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("key table still there after down (%v)", err)
	}
	var links int
	if err := db.QueryRow(`SELECT count(*) FROM scheduling_links WHERE id = $1`, link).Scan(&links); err != nil || links != 1 {
		t.Fatalf("down 040 lost the link (%d, %v)", links, err)
	}
	to(40)
}

// Contract md §2.3: the canonical bytes v1, field order and values pinned.
func TestPAC119_CanonicalFormV1(t *testing.T) {
	l := &SchedulingLink{Title: "Intro", Slug: "", DurationOptions: []int{30, 15, 30}, DaysOfWeek: []int{5, 1},
		WindowStart: "09:00", WindowEnd: "17:00", BufferBefore: 5, UsageType: "reusable",
		BookingHorizonDays: 60, RefusalMessage: "generic"}
	got := string(linkCreateCanonical(l, []string{"g@x.com", "b@x.com", "b@x.com"}))
	want := `{"v":1,"title":"Intro","slug":"","duration_options":[30,15,30],"days_of_week":[5,1],` +
		`"window_start_time":"09:00","window_end_time":"17:00","buffer_before":5,"buffer_after":0,` +
		`"min_notice_minutes":0,"usage_type":"reusable","max_uses":null,"booking_horizon_days":60,` +
		`"refusal_message":"generic","co_host_emails":["b@x.com","g@x.com"]}`
	if got != want {
		t.Fatalf("canonical v1 =\n%s\nwant\n%s", got, want)
	}
	// Defaults applied before hashing: an unset usage, horizon and refusal
	// message are reusable, 60 and generic.
	bare := *l
	bare.UsageType, bare.BookingHorizonDays, bare.RefusalMessage = "", 0, ""
	if string(linkCreateCanonical(&bare, []string{"b@x.com", "g@x.com"})) != want {
		t.Error("defaults are not applied before hashing")
	}
	// No list is null; an empty list is [] and differs from it.
	noList := string(linkCreateCanonical(l, nil))
	empty := string(linkCreateCanonical(l, []string{}))
	if !strings.HasSuffix(noList, `"co_host_emails":null}`) || !strings.HasSuffix(empty, `"co_host_emails":[]}`) {
		t.Fatalf("no list %s; empty list %s", noList, empty)
	}
	// Every stored field moves the bytes.
	max := 3
	for name, mutate := range map[string]func(c *SchedulingLink){
		"title":      func(c *SchedulingLink) { c.Title = "Intro 2" },
		"slug":       func(c *SchedulingLink) { c.Slug = "intro" },
		"durations":  func(c *SchedulingLink) { c.DurationOptions = []int{30, 15} },
		"days":       func(c *SchedulingLink) { c.DaysOfWeek = []int{1, 5} },
		"start":      func(c *SchedulingLink) { c.WindowStart = "09:30" },
		"end":        func(c *SchedulingLink) { c.WindowEnd = "16:00" },
		"buffer":     func(c *SchedulingLink) { c.BufferAfter = 1 },
		"notice":     func(c *SchedulingLink) { c.MinNoticeMinutes = 1 },
		"usage":      func(c *SchedulingLink) { c.UsageType = "recurring"; c.MaxUses = &max },
		"horizon":    func(c *SchedulingLink) { c.BookingHorizonDays = 61 },
		"refusal":    func(c *SchedulingLink) { c.RefusalMessage = "specific" },
		"title-trim": func(c *SchedulingLink) { c.Title = "Intro " },
	} {
		c := *l
		mutate(&c)
		if string(linkCreateCanonical(&c, []string{"b@x.com", "g@x.com"})) == want {
			t.Errorf("changing %s leaves the canonical bytes unchanged", name)
		}
	}
}

// Contract md §2.3: entries trimmed of ECMA \s, lowered by the database's
// lower() (lowerAll), deduplicated and sorted; [] differs from no list.
func TestPAC119_DigestNormalisesTheList(t *testing.T) {
	db := openTestDB(t)
	l := pac119Link(uuid.New(), "Intro")
	d := func(emails []string) string {
		t.Helper()
		b, err := linkCreateDigest(db, l, emails)
		if err != nil || len(b) != sha256.Size {
			t.Fatalf("digest: %x %v", b, err)
		}
		return hex.EncodeToString(b)
	}
	base := d([]string{"b@x.com", "g@x.com"})
	if got := d([]string{" G@X.com", "b@x.com", "B@x.com"}); got != base {
		t.Error("a reordered, re-cased, duplicated list does not give the same digest")
	}
	if got := d([]string{"\u00a0b@x.com\ufeff", "g@x.com"}); got != base {
		t.Error("ECMA-262 \\s around an entry changes the digest")
	}
	if d(nil) == d([]string{}) {
		t.Error("no list and an empty list give the same digest")
	}
	if d([]string{"b@x.com", "h@x.com"}) == base {
		t.Error("another address gives the same digest")
	}
	want := sha256.Sum256(linkCreateCanonical(l, []string{"b@x.com", "g@x.com"}))
	if base != hex.EncodeToString(want[:]) {
		t.Error("the digest is not SHA-256 of the canonical bytes")
	}
}

// Contract md §3.1, §4.2: a keyed create stores its key with the digest and
// the storage-owned encoding of the outcomes (wire names); the replay decodes
// it into the same CoHostChanges. A keyless create writes no key row; a keyed
// create without a list stores no outcomes.
func TestPAC119_KeyedCreateStoresKeyDigestAndOutcomes(t *testing.T) {
	db := openTestDB(t)
	owner := pac112StorageUser(t, db, "pac119-own-"+uuid.NewString()[:8]+"@acme.test", true)
	b := "pac119-b-" + uuid.NewString()[:8] + "@acme.test"
	pac112StorageUser(t, db, b, true)
	c := "pac119-c-" + uuid.NewString()[:8] + "@nobody.test"
	key := pac119Key()
	l := pac119Link(owner, "Intro")
	created, changes, digest := pac119Create(t, db, l, []string{b, c}, key)

	var gotDigest []byte
	var stored string
	var link uuid.UUID
	if err := db.QueryRow(`SELECT link_id, request_sha256, co_host_changes::text FROM scheduling_link_create_keys
		WHERE owner_user_id = $1 AND idempotency_key = $2`, owner, key).Scan(&link, &gotDigest, &stored); err != nil {
		t.Fatal(err)
	}
	if link != created.ID || !bytes.Equal(gotDigest, digest) {
		t.Fatalf("key row link %s digest %x; want %s %x", link, gotDigest, created.ID, digest)
	}
	var wire map[string]any
	if err := json.Unmarshal([]byte(stored), &wire); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"addresses", "removed", "invited_count", "removed_count", "not_added_count"} {
		if _, ok := wire[k]; !ok {
			t.Errorf("stored outcomes lack %q: %s", k, stored)
		}
	}
	if !strings.Contains(stored, `"outcome": "invited"`) || !strings.Contains(stored, `"email": "`+b+`"`) {
		t.Errorf("stored outcomes do not use the wire names: %s", stored)
	}

	res := pac119Resolve(t, db, owner, l, []string{c, strings.ToUpper(b)}, key)
	if res.Outcome != LinkCreateKeyReplay || res.Link.ID != created.ID || !reflect.DeepEqual(res.Changes, changes) {
		t.Fatalf("replay = %+v; changes %+v, want %+v", res, res.Changes, changes)
	}

	// Without a list: a key row with no outcomes, and the replay has none.
	key2 := pac119Key()
	l2 := pac119Link(owner, "No list")
	pac119Create(t, db, l2, nil, key2)
	var outcomes sql.NullString
	if err := db.QueryRow(`SELECT co_host_changes::text FROM scheduling_link_create_keys WHERE idempotency_key = $1`, key2).Scan(&outcomes); err != nil || outcomes.Valid {
		t.Fatalf("outcomes stored without a list: %v %v", outcomes, err)
	}
	if res := pac119Resolve(t, db, owner, l2, nil, key2); res.Outcome != LinkCreateKeyReplay || res.Changes != nil {
		t.Fatalf("replay without a list = %+v", res)
	}

	// Keyless: no key row.
	var before, after int
	_ = db.QueryRow(`SELECT count(*) FROM scheduling_link_create_keys`).Scan(&before)
	if _, _, err := CreateSchedulingLinkWithCoHostsCtx(context.Background(), db, pac119Link(owner, "Keyless"), []string{b}); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT count(*) FROM scheduling_link_create_keys`).Scan(&after)
	if after != before {
		t.Errorf("a keyless create wrote %d key rows", after-before)
	}
}

// Contract md §4.2 (challenge m-2): a second create with the same (owner,
// key) is a LinkCreateConflictError on the key constraint, and a slug
// collision one on the slug constraint, both matching ErrLinkCreateConflict;
// neither leaves a link, a host row or a key behind.
func TestPAC119_UniqueViolationsByConstraintName(t *testing.T) {
	db := openTestDB(t)
	owner := pac112StorageUser(t, db, "pac119-uv-"+uuid.NewString()[:8]+"@acme.test", true)
	key := pac119Key()
	first, _, digest := pac119Create(t, db, pac119Link(owner, "Intro"), nil, key)
	count := func() (links, keys int) {
		_ = db.QueryRow(`SELECT count(*) FROM scheduling_links WHERE owner_user_id = $1`, owner).Scan(&links)
		_ = db.QueryRow(`SELECT count(*) FROM scheduling_link_create_keys WHERE owner_user_id = $1`, owner).Scan(&keys)
		return
	}

	_, _, _, err := CreateSchedulingLinkWithCoHostsKeyedCtx(context.Background(), db, pac119Link(owner, "Intro"), nil, key, digest)
	var conflict *LinkCreateConflictError
	if !errors.As(err, &conflict) || conflict.Constraint != LinkCreateKeyConstraint || !errors.Is(err, ErrLinkCreateConflict) {
		t.Fatalf("same key = %v; want the key-constraint conflict", err)
	}
	if !isUniqueViolationOn(err, LinkCreateKeyConstraint) {
		t.Error("the conflict lost the driver error")
	}

	dup := pac119Link(owner, "Intro")
	dup.Slug = first.Slug
	_, _, _, err = CreateSchedulingLinkWithCoHostsKeyedCtx(context.Background(), db, dup, nil, pac119Key(), digest)
	if !errors.As(err, &conflict) || conflict.Constraint != LinkSlugConstraint {
		t.Fatalf("same slug = %v; want the slug-constraint conflict", err)
	}
	if links, keys := count(); links != 1 || keys != 1 {
		t.Fatalf("after two conflicts: %d links, %d keys; want 1 and 1", links, keys)
	}

	// A keyed create needs the 32-byte digest.
	if _, _, _, err := CreateSchedulingLinkWithCoHostsKeyedCtx(context.Background(), db, pac119Link(owner, "x"), nil, pac119Key(), nil); err == nil {
		t.Error("a keyed create without a digest succeeded")
	}
}

// Contract md §1.2: deleted wins over expired, expired over the body
// comparison, and a NULL digest under 24 h is the key-reused refusal.
func TestPAC119_ResolutionPrecedence(t *testing.T) {
	db := openTestDB(t)
	owner := pac112StorageUser(t, db, "pac119-pr-"+uuid.NewString()[:8]+"@acme.test", true)
	l := pac119Link(owner, "Intro")
	key := pac119Key()
	created, _, _ := pac119Create(t, db, l, nil, key)
	other := pac119Link(owner, "Other title")

	if r := pac119Resolve(t, db, owner, other, nil, key); r.Outcome != LinkCreateKeyReused || r.LinkID != created.ID {
		t.Fatalf("different request = %+v", r)
	}
	if r := pac119Resolve(t, db, uuid.New(), l, nil, key); r.Outcome != LinkCreateKeyNotStored {
		t.Fatalf("another owner's identical key = %+v", r)
	}
	mustExec(t, db, `UPDATE scheduling_link_create_keys SET request_sha256 = NULL WHERE idempotency_key = $1`, key)
	if r := pac119Resolve(t, db, owner, l, nil, key); r.Outcome != LinkCreateKeyReused {
		t.Fatalf("NULL digest under 24 h = %+v", r)
	}
	mustExec(t, db, `UPDATE scheduling_link_create_keys SET created_at = now() - interval '24 hours' WHERE idempotency_key = $1`, key)
	if r := pac119Resolve(t, db, owner, l, nil, key); r.Outcome != LinkCreateKeyExpired || r.LinkID != created.ID {
		t.Fatalf("expired = %+v", r)
	}
	if ok, err := DeleteSchedulingLink(db, created.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, req := range []*SchedulingLink{l, other} {
		if r := pac119Resolve(t, db, owner, req, nil, key); r.Outcome != LinkCreateKeyDeleted || r.LinkID != uuid.Nil || r.Link != nil {
			t.Fatalf("deleted = %+v", r)
		}
	}
}

// Contract md §4.1: under a session guard whose generation moved, the
// resolution is ErrStaleSession and nothing of the row; with a current guard
// it resolves.
func TestPAC119_ResolutionRechecksTheSession(t *testing.T) {
	db := openTestDB(t)
	owner := pac112StorageUser(t, db, "pac119-rc-"+uuid.NewString()[:8]+"@acme.test", true)
	l := pac119Link(owner, "Intro")
	key := pac119Key()
	pac119Create(t, db, l, nil, key)
	gen, _, err := CurrentGeneration(db, owner)
	if err != nil {
		t.Fatal(err)
	}
	pre := *l
	pre.Slug = ""
	ctx := WithSessionGuard(context.Background(), &SessionGuard{UserID: owner, Gen: gen})
	if r, err := ResolveLinkCreateKeyCtx(ctx, db, owner, &LinkCreateAttempt{Key: key, Link: &pre}); err != nil || r.Outcome != LinkCreateKeyReplay {
		t.Fatalf("current guard = %+v %v", r, err)
	}
	g := &SessionGuard{UserID: owner, Gen: gen}
	InstallAfterLinkCreateKeyRead(t, func(context.Context, string) {
		mustExec(t, db, `UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, owner)
	})
	r, err := ResolveLinkCreateKeyCtx(WithSessionGuard(context.Background(), g), db, owner, &LinkCreateAttempt{Key: key, Link: &pre})
	if !errors.Is(err, ErrStaleSession) || r != nil || !g.Stale() {
		t.Fatalf("generation moved between the key read and re-check #2 = %+v %v (stale %v)", r, err, g.Stale())
	}
}

// AC-12 (storage half): two keyed creates listing B and C, aged 25 h and
// 23 h. The cleanup clears the 25 h row down to owner, key, link and time,
// leaves the 23 h row intact and both links; the older key is then the
// expired refusal. The fixture's titles hold no address.
func TestPAC119_AC12_CleanupClearsOnlyTheExpiredData(t *testing.T) {
	db := openTestDB(t)
	owner := pac112StorageUser(t, db, "pac119-cl-"+uuid.NewString()[:8]+"@acme.test", true)
	b := "Pac119-B-" + uuid.NewString()[:8] + "@acme.test"
	pac112StorageUser(t, db, strings.ToLower(b), true)
	c := "pac119-c-" + uuid.NewString()[:8] + "@nobody.test"
	oldKey, newKey := pac119Key(), pac119Key()
	oldLink, _, oldDigest := pac119Create(t, db, pac119Link(owner, "Older"), []string{b, c}, oldKey)
	newLink, _, newDigest := pac119Create(t, db, pac119Link(owner, "Newer"), []string{b, c}, newKey)
	mustExec(t, db, `UPDATE scheduling_link_create_keys SET created_at = now() - interval '25 hours' WHERE idempotency_key = $1`, oldKey)
	mustExec(t, db, `UPDATE scheduling_link_create_keys SET created_at = now() - interval '23 hours' WHERE idempotency_key = $1`, newKey)

	n, err := ClearExpiredLinkCreateKeyData(context.Background(), db)
	if err != nil || n != 1 {
		t.Fatalf("cleanup = %d %v; want 1 row", n, err)
	}
	row := func(key string) (text string, digest []byte, outcomes sql.NullString) {
		t.Helper()
		if err := db.QueryRow(`SELECT k::text, k.request_sha256, k.co_host_changes::text FROM scheduling_link_create_keys k
			WHERE idempotency_key = $1`, key).Scan(&text, &digest, &outcomes); err != nil {
			t.Fatal(err)
		}
		return
	}
	oldText, oldGot, oldOutcomes := row(oldKey)
	if oldGot != nil || oldOutcomes.Valid {
		t.Fatalf("the 25 h row kept its digest or outcomes: %s", oldText)
	}
	// Every column of the cleared row: no address (case-insensitive), no digest.
	low := strings.ToLower(oldText)
	for _, leak := range []string{strings.ToLower(b), strings.ToLower(c), hex.EncodeToString(oldDigest), hex.EncodeToString(newDigest)} {
		if strings.Contains(low, leak) {
			t.Errorf("the cleared row still holds %q: %s", leak, oldText)
		}
	}
	var cols []string
	rows, err := db.Query(`SELECT column_name FROM information_schema.columns WHERE table_name = 'scheduling_link_create_keys'
		AND column_name NOT IN ('request_sha256', 'co_host_changes') ORDER BY column_name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var col string
		_ = rows.Scan(&col)
		cols = append(cols, col)
	}
	rows.Close()
	if strings.Join(cols, ",") != "created_at,idempotency_key,link_id,owner_user_id" {
		t.Errorf("the key store keeps more than owner, key, link and time for ever: %v", cols)
	}
	// The 23 h row is intact.
	_, newGot, newOutcomes := row(newKey)
	if !bytes.Equal(newGot, newDigest) || !newOutcomes.Valid || !strings.Contains(strings.ToLower(newOutcomes.String), strings.ToLower(b)) {
		t.Fatalf("the 23 h row was touched: digest %x outcomes %v", newGot, newOutcomes)
	}
	for _, id := range []uuid.UUID{oldLink.ID, newLink.ID} {
		if l, err := GetSchedulingLinkByID(db, id); err != nil || l == nil {
			t.Fatalf("link %s gone after the cleanup (%v)", id, err)
		}
	}
	if r := pac119Resolve(t, db, owner, pac119Link(owner, "Older"), []string{b, c}, oldKey); r.Outcome != LinkCreateKeyExpired || r.LinkID != oldLink.ID {
		t.Fatalf("retry of the older key = %+v; want expired", r)
	}
	// A second run clears nothing (idempotent).
	if n, err := ClearExpiredLinkCreateKeyData(context.Background(), db); err != nil || n != 0 {
		t.Fatalf("second run = %d %v", n, err)
	}
}

// PAC-80 rules for the PAC-119 seams.
func TestPAC119_SeamsNeverOutliveTheirTest(t *testing.T) {
	t.Run("installs and leaves them", func(t *testing.T) {
		InstallAfterLinkCreateKeyRead(t, func(context.Context, string) {})
		InstallAfterLinkCreateKeyResolved(t, func(context.Context, string) {})
		InstallBeforeLinkCreateCommit(t, func(uuid.UUID) error { return nil })
	})
	if _, ok := afterLinkCreateKeyRead.get(); ok {
		t.Error("key-read seam outlived its test")
	}
	if _, ok := afterLinkCreateKeyResolved.get(); ok {
		t.Error("key-resolved seam outlived its test")
	}
	if _, ok := beforeLinkCreateCommit.get(); ok {
		t.Error("before-commit seam outlived its test")
	}
}

// A non-nil error from beforeLinkCreateCommit rolls the keyed create back:
// no link, no host row, no key (contract md §4.2 step 8, AC-9).
func TestPAC119_BeforeCommitErrorStoresNothing(t *testing.T) {
	db := openTestDB(t)
	owner := pac112StorageUser(t, db, "pac119-bc-"+uuid.NewString()[:8]+"@acme.test", true)
	l := pac119Link(owner, "Intro")
	key := pac119Key()
	pre := *l
	pre.Slug = ""
	a := &LinkCreateAttempt{Key: key, Link: &pre}
	if _, err := ResolveLinkCreateKeyCtx(context.Background(), db, owner, a); err != nil {
		t.Fatal(err)
	}
	InstallBeforeLinkCreateCommit(t, func(uuid.UUID) error { return errors.New("pac119: refused") })
	if _, _, _, err := CreateSchedulingLinkWithCoHostsKeyedCtx(context.Background(), db, l, nil, key, a.Digest); err == nil {
		t.Fatal("create succeeded")
	}
	var links, keys, hosts int
	_ = db.QueryRow(`SELECT count(*) FROM scheduling_links WHERE owner_user_id = $1`, owner).Scan(&links)
	_ = db.QueryRow(`SELECT count(*) FROM scheduling_link_create_keys WHERE owner_user_id = $1`, owner).Scan(&keys)
	_ = db.QueryRow(`SELECT count(*) FROM scheduling_link_hosts WHERE user_id = $1`, owner).Scan(&hosts)
	if links+keys+hosts != 0 {
		t.Fatalf("a rolled-back create left %d links, %d keys, %d host rows", links, keys, hosts)
	}
}
