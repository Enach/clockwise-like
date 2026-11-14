package storage

// PAC-82 (contracts/features/PAC-82.md section 4): the transaction-scoped
// advisory keys that order a public booking's save against every other save it
// could conflict with. Two overlapping slots share an instant, hence a 15-minute
// UTC bucket, and two conflicting bookings share a host, so they share a key and
// serialize; a use-limited link adds one key for its use count.

import (
	"context"
	"database/sql"
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// The advisory lock classes reserved for PAC-82 (two-integer form, pg_locks
// objsubid = 2). Disjoint from PAC-84's class 8400.
const (
	BookingLockClassHost   int32 = 8201 // host-wide: shared (fine save) or exclusive (coarse save)
	BookingLockClassBucket int32 = 8202 // (host, 15-minute UTC bucket): exclusive
	BookingLockClassUses   int32 = 8203 // a use-limited link's use count: exclusive
)

// BookingBucket is the length of one bucket of the (host, bucket) keys.
const BookingBucket = 15 * time.Minute

// BookingKeyCap bounds the (host, bucket) keys of one save (contract section
// 4.3): a save with more lock hosts times buckets than this is coarse and takes
// one exclusive host-wide key per host instead. A package variable only so that
// tests can shorten it; nothing else assigns it.
var BookingKeyCap = 64

// BookingAdvisoryKey is one key a save takes, in the two-integer form.
type BookingAdvisoryKey struct {
	Class  int32
	ObjID  int32
	Shared bool
}

// bookingH32 is the first 4 bytes of SHA-256 of s, big-endian, as a signed
// int32. Fixed by the contract so that every replica and version agree.
func bookingH32(s string) int32 {
	sum := sha256.Sum256([]byte(s))
	// Reinterpreted as two's complement: the contract's derivation (md 4.1).
	return int32(binary.BigEndian.Uint32(sum[:4]))
}

// bookingBucketRange is the first and last bucket index the slot [start, end)
// touches: floor(start / 900 s) .. floor((end - 1 µs) / 900 s) on the UTC Unix
// time in microseconds.
func bookingBucketRange(start, end time.Time) (first, last int64) {
	width := BookingBucket.Microseconds()
	first = floorDiv(start.UnixMicro(), width)
	last = floorDiv(end.UnixMicro()-1, width)
	if last < first {
		last = first
	}
	return first, last
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// BookingAdvisoryKeys are the keys a save of [start, end) on link linkID takes,
// for the lock host set lockHosts (owner and accepted co-hosts), sorted
// ascending by (class, objid) with duplicates removed (contract section 4).
// Fine when len(hosts) x buckets <= BookingKeyCap: per host the host-wide key
// shared and one exclusive key per bucket; coarse otherwise: per host the
// host-wide key exclusive. A use-limited link adds its exclusive uses key.
func BookingAdvisoryKeys(lockHosts []uuid.UUID, linkID uuid.UUID, useLimited bool, start, end time.Time) []BookingAdvisoryKey {
	seen := map[uuid.UUID]bool{}
	var hosts []uuid.UUID
	for _, h := range lockHosts {
		if !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	first, last := bookingBucketRange(start, end)
	buckets := last - first + 1
	fine := int64(len(hosts))*buckets <= int64(BookingKeyCap)
	var keys []BookingAdvisoryKey
	for _, h := range hosts {
		hs := h.String()
		keys = append(keys, BookingAdvisoryKey{Class: BookingLockClassHost, ObjID: bookingH32("pac82:host:" + hs), Shared: fine})
		if !fine {
			continue
		}
		for b := first; b <= last; b++ {
			keys = append(keys, BookingAdvisoryKey{Class: BookingLockClassBucket, ObjID: bookingH32("pac82:bucket:" + hs + ":" + strconv.FormatInt(b, 10))})
		}
	}
	if useLimited {
		keys = append(keys, BookingAdvisoryKey{Class: BookingLockClassUses, ObjID: bookingH32("pac82:uses:" + linkID.String())})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Class != keys[j].Class {
			return keys[i].Class < keys[j].Class
		}
		return keys[i].ObjID < keys[j].ObjID
	})
	// One key is taken once; a collision of a shared and an exclusive request
	// within one save keeps the exclusive one (it covers both).
	out := keys[:0]
	for _, k := range keys {
		if n := len(out); n > 0 && out[n-1].Class == k.Class && out[n-1].ObjID == k.ObjID {
			out[n-1].Shared = out[n-1].Shared && k.Shared
			continue
		}
		out = append(out, k)
	}
	return out
}

// RowQuerier is the one method ResolveIdempotencyKey needs: a pool, a
// transaction or a reserved connection.
type RowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
