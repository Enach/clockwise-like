package storage

// PAC-82 AC-30 (contract section 4): the advisory key derivation is fixed so
// that every replica agrees; buckets cover a slot's instants exactly; a save
// over the cap is coarse; keys are ascending and de-duplicated.

import (
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPAC82_H32FixedVectors(t *testing.T) {
	for s, want := range map[string]int32{
		"pac82:host:00000000-0000-0000-0000-000000000001":           1265567898,
		"pac82:bucket:00000000-0000-0000-0000-000000000001:2101248": -1953245637,
		"pac82:uses:00000000-0000-0000-0000-000000000002":           -531788754,
	} {
		if got := bookingH32(s); got != want {
			t.Errorf("h32(%q) = %d, want %d", s, got, want)
		}
	}
}

func pac82Bucket(t time.Time) int64 { return floorDiv(t.UnixMicro(), BookingBucket.Microseconds()) }

func TestPAC82_BucketRanges(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2031, 3, 3, h, m, 0, 0, time.UTC) }
	cases := []struct {
		name       string
		start, end time.Time
		buckets    int64
	}{
		{"ends on a bucket edge", at(10, 0), at(10, 15), 1},
		{"half hour on the grid", at(10, 0), at(10, 30), 2},
		{"09:10 grid, 15 min, touches two buckets", at(9, 10), at(9, 25), 2},
		{"09:10 grid, 30 min, touches three buckets", at(9, 10), at(9, 40), 3},
		{"seven minutes inside one bucket", at(9, 1), at(9, 8), 1},
		{"one microsecond past the edge", at(10, 0), at(10, 15).Add(time.Microsecond), 2},
	}
	for _, c := range cases {
		first, last := bookingBucketRange(c.start, c.end)
		if first != pac82Bucket(c.start) || last-first+1 != c.buckets {
			t.Errorf("%s: buckets %d..%d (%d), want %d from %d", c.name, first, last, last-first+1, c.buckets, pac82Bucket(c.start))
		}
	}
	// A 25-hour slot (one daily window on a fall-back day, 00:00 to 00:00 in
	// Europe/Paris on 2031-10-26 is 2031-10-25T22:00Z to 2031-10-26T23:00Z) is
	// 100 buckets: above the cap for one host, so the save is coarse.
	s := time.Date(2031, 10, 25, 22, 0, 0, 0, time.UTC)
	e := s.Add(25 * time.Hour)
	if first, last := bookingBucketRange(s, e); last-first+1 != 100 {
		t.Errorf("25 h slot touches %d buckets, want 100", last-first+1)
	}
	keys := BookingAdvisoryKeys([]uuid.UUID{uuid.New()}, uuid.New(), false, s, e)
	if len(keys) != 1 || keys[0].Class != BookingLockClassHost || keys[0].Shared {
		t.Errorf("25 h one-host slot keys = %+v, want one exclusive host-wide key", keys)
	}
}

func TestPAC82_FineAndCoarse(t *testing.T) {
	h1, h2, link := uuid.New(), uuid.New(), uuid.New()
	s := time.Date(2031, 3, 3, 8, 0, 0, 0, time.UTC)
	count := func(keys []BookingAdvisoryKey) (hostShared, hostExcl, bucket, uses int) {
		for _, k := range keys {
			switch {
			case k.Class == BookingLockClassHost && k.Shared:
				hostShared++
			case k.Class == BookingLockClassHost:
				hostExcl++
			case k.Class == BookingLockClassBucket:
				bucket++
			case k.Class == BookingLockClassUses:
				uses++
			}
		}
		return
	}
	// 2 hosts x 32 buckets = 64: fine.
	hs, he, b, u := count(BookingAdvisoryKeys([]uuid.UUID{h1, h2}, link, true, s, s.Add(8*time.Hour)))
	if hs != 2 || he != 0 || b != 64 || u != 1 {
		t.Errorf("64 keys: shared %d excl %d buckets %d uses %d; want 2 0 64 1", hs, he, b, u)
	}
	// 2 hosts x 33 buckets = 66: coarse.
	hs, he, b, u = count(BookingAdvisoryKeys([]uuid.UUID{h1, h2}, link, false, s, s.Add(8*time.Hour+15*time.Minute)))
	if hs != 0 || he != 2 || b != 0 || u != 0 {
		t.Errorf("66 keys: shared %d excl %d buckets %d uses %d; want 0 2 0 0", hs, he, b, u)
	}
	// The cap is a variable: shortened, a short slot is coarse.
	prev := BookingKeyCap
	BookingKeyCap = 1
	defer func() { BookingKeyCap = prev }()
	if _, he, _, _ = count(BookingAdvisoryKeys([]uuid.UUID{h1}, link, false, s, s.Add(30*time.Minute))); he != 1 {
		t.Errorf("cap 1, 2 buckets: want coarse")
	}
}

func TestPAC82_KeysAscendingAndDeduplicated(t *testing.T) {
	h, link := uuid.New(), uuid.New()
	s := time.Date(2031, 3, 3, 9, 10, 0, 0, time.UTC)
	keys := BookingAdvisoryKeys([]uuid.UUID{h, h, h}, link, true, s, s.Add(50*time.Minute))
	// one host-wide, buckets 09:00 09:15 09:30 09:45 (4), one uses key.
	if len(keys) != 6 {
		t.Fatalf("%d keys, want 6: %+v", len(keys), keys)
	}
	for i := 1; i < len(keys); i++ {
		a, b := keys[i-1], keys[i]
		if a.Class > b.Class || (a.Class == b.Class && a.ObjID >= b.ObjID) {
			t.Fatalf("keys not strictly ascending at %d: %+v", i, keys)
		}
	}
}

// Two slots at least 15 minutes apart (end of one to start of the other)
// never share a bucket key, whatever their alignment and length; two
// overlapping slots always do (contract challenge C-7).
func TestPAC82_BucketsSeparateExactlyAsTheRule(t *testing.T) {
	h, link := uuid.New(), uuid.New()
	rng := rand.New(rand.NewSource(82))
	base := time.Date(2031, 3, 3, 0, 0, 0, 0, time.UTC)
	set := func(s, e time.Time) map[int32]bool {
		m := map[int32]bool{}
		for _, k := range BookingAdvisoryKeys([]uuid.UUID{h}, link, false, s, e) {
			if k.Class == BookingLockClassBucket {
				m[k.ObjID] = true
			}
		}
		return m
	}
	for i := 0; i < 2000; i++ {
		s1 := base.Add(time.Duration(rng.Intn(24*60)) * time.Minute)
		e1 := s1.Add(time.Duration(1+rng.Intn(120)) * time.Minute)
		gap := time.Duration(rng.Intn(60)-30) * time.Minute
		s2 := e1.Add(gap)
		e2 := s2.Add(time.Duration(1+rng.Intn(120)) * time.Minute)
		a, b := set(s1, e1), set(s2, e2)
		shared := false
		for k := range a {
			if b[k] {
				shared = true
			}
		}
		overlap := s1.Before(e2) && s2.Before(e1)
		if gap >= BookingBucket && shared {
			t.Fatalf("[%s,%s) and [%s,%s) are %s apart and share a bucket", s1, e1, s2, e2, gap)
		}
		if overlap && !shared {
			t.Fatalf("[%s,%s) and [%s,%s) overlap and share no bucket", s1, e1, s2, e2)
		}
	}
}
