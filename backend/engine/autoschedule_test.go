package engine

import (
	"testing"
	"time"
)

// PAC-49 §4.3 item 9: the write and the scheduled loop share one five-field
// parser, so nothing the write accepts is skipped by the loop and nothing the
// loop runs was refused by the write.
func TestParseAutoScheduleCron(t *testing.T) {
	for _, ok := range []string{"0 8 * * *", "0 7 * * 1-5", "*/15 6-9 * * MON-FRI", "30 2 1 1 *"} {
		if _, err := ParseAutoScheduleCron(ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "@daily", "@every 1h", "every tuesday", "0 7 * *", "0 0 7 * * *",
		"CRON_TZ=Europe/Paris 0 7 * * *", "TZ=UTC 0 7 * * *", "61 7 * * *",
	} {
		if _, err := ParseAutoScheduleCron(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestParseAutoScheduleCron_EvaluatesInTheZoneOfItsInput(t *testing.T) {
	s, err := ParseAutoScheduleCron("0 7 * * *")
	if err != nil {
		t.Fatal(err)
	}
	paris, _ := time.LoadLocation("Europe/Paris")
	from := time.Date(2026, 7, 1, 6, 30, 0, 0, paris)
	got := s.Next(from)
	if want := time.Date(2026, 7, 1, 7, 0, 0, 0, paris); !got.Equal(want) {
		t.Errorf("next = %v, want %v", got, want)
	}
}
