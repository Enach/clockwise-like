package api

// PAC-134 at the HTTP boundary (spec AC-5b, AC-6; contract
// contracts/features/PAC-134.md §1.1): after an operator erasure, the live
// bookings read presents an erased booking as erased, and a keyed public
// booking retry of it gets "key reused".

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
)

// pac134Erase runs the operator's erasure of addr on the test database.
func pac134Erase(t *testing.T, e *pac82Env, addr string) *storage.EraseAddressResult {
	t.Helper()
	res, err := storage.EraseAddress(context.Background(), e.db, storage.EraseAddressOptions{
		Address: addr, MaxDuration: 20 * time.Second, Run: storage.OperatorRun{Operator: "op", Reason: "PAC-134 test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// AC-5b and AC-6.
func TestPAC134_AC5b_AC6_ErasedLiveBooking(t *testing.T) {
	e := newPAC82Env(t)
	owner := e.host("pac134")
	l := e.link(owner, "reusable", nil, []int{30}, true)
	x := "pac134-" + e.sfx + "@ext.example"
	key := pac82Key("pac134")
	q := pac82Req{name: "Pat Person", email: strings.ToUpper(x[:1]) + x[1:], notes: "call me", start: pac82At(10, 0), key: key}
	e.mustStatus(e.post(l.Slug, q), http.StatusCreated, "keyed booking by X")
	e.mustStatus(e.post(l.Slug, pac82Req{name: "Other", email: "other-" + e.sfx + "@ext.example", notes: "kept", start: pac82At(11, 0)}), http.StatusCreated, "other booking")
	e.settle()

	// Before: the answer as the stored booking encoded it before PAC-134.
	w := e.do(http.MethodGet, "/api/scheduling-links/"+l.ID.String()+"/bookings", owner, "")
	e.mustStatus(w, http.StatusOK, "list before")
	stored, err := storage.GetBookingsByLink(e.db, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(stored)
	if !jsonEqual(t, w.Body.Bytes(), legacy) || strings.Contains(w.Body.String(), "booker_erased_at") {
		t.Fatalf("an answer with no erased booking differs from the pre-PAC-134 encoding:\n%s\n%s", w.Body.String(), legacy)
	}
	rows := e.bookingCount(l.ID)

	if res := pac134Erase(t, e, x); res.Places[8].Erased != 1 {
		t.Fatalf("bookings erased %d", res.Places[8].Erased)
	}

	// AC-6: strict decode into the generated record; X's record is erased.
	w = e.do(http.MethodGet, "/api/scheduling-links/"+l.ID.String()+"/bookings", owner, "")
	e.mustStatus(w, http.StatusOK, "list after")
	dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	dec.DisallowUnknownFields()
	var recs []gen.SchedulingLinkBookingRecord
	if err := dec.Decode(&recs); err != nil {
		t.Fatalf("strict decode: %v\n%s", err, w.Body.String())
	}
	var raw []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	if len(recs) != 2 || len(raw) != 2 {
		t.Fatalf("%d records", len(recs))
	}
	erased := 0
	for i, r := range recs {
		_, hasKey := raw[i]["booker_erased_at"]
		nameV, nameKey := raw[i]["booker_name"]
		emailV, emailKey := raw[i]["booker_email"]
		if !nameKey || !emailKey {
			t.Errorf("record %d lacks booker_name or booker_email", i)
		}
		if r.BookerErasedAt == nil {
			if hasKey || r.BookerName == nil || *r.BookerName != "Other" || r.Notes != "kept" {
				t.Errorf("record %d not erased: %s", i, w.Body.String())
			}
			continue
		}
		erased++
		// The web rejects an erased record with a name, an email or notes.
		if nameV != nil || emailV != nil || r.Notes != "" || !strings.HasSuffix(raw[i]["booker_erased_at"].(string), "Z") {
			t.Errorf("erased record %d: %s", i, w.Body.String())
		}
	}
	if erased != 1 || strings.Contains(strings.ToLower(w.Body.String()), x) || strings.Contains(w.Body.String(), "Pat Person") {
		t.Errorf("after the erasure: %s", w.Body.String())
	}

	// AC-5b: X's keyed booking retried with the same key and details: key
	// reused, nothing booked, use count unchanged.
	again := e.post(l.Slug, q)
	if !pac82IsReused(again) {
		t.Errorf("retry: %d %s; want 409 key reused", again.Code, again.Body.String())
	}
	e.settle()
	if e.bookingCount(l.ID) != rows {
		t.Errorf("the retry booked: %d -> %d", rows, e.bookingCount(l.ID))
	}
	var uses int
	if err := e.db.QueryRow(`SELECT count(*) FROM bookings WHERE link_id = $1`, l.ID).Scan(&uses); err != nil || uses != rows {
		t.Errorf("uses %d, want %d (%v)", uses, rows, err)
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}
