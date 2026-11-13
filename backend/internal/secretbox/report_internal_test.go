package secretbox

import (
	"bytes"
	"log"
	"testing"
	"time"
)

// Review #4: one failing secret is logged and reported once per window per
// process, not on every read; a different row is reported on its own; after
// the window it is reported again.
func TestPAC84_ReportFailureDedupe(t *testing.T) {
	ResetReported()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	now := time.Now()
	reportNow = func() time.Time { return now }
	defer func() { reportNow = time.Now }()
	var sent int
	SetReporter(func(string) { sent++ })
	defer SetReporter(nil)

	e := &DecryptError{Table: "settings", Column: "webcal_url", Row: "user_id=u", DEK: 3, Reason: ReasonAuth}
	for i := 0; i < 5; i++ {
		ReportFailure(e, "u")
	}
	if sent != 1 || bytes.Count(buf.Bytes(), []byte("cannot decrypt")) != 1 {
		t.Fatalf("5 reads reported %d times, logged %d", sent, bytes.Count(buf.Bytes(), []byte("cannot decrypt")))
	}
	other := *e
	other.Row = "user_id=v"
	ReportFailure(&other, "v")
	if sent != 2 {
		t.Fatal("another row not reported")
	}
	now = now.Add(ReportWindow + time.Second)
	ReportFailure(e, "u")
	if sent != 3 {
		t.Fatal("not reported again after the window")
	}
}
