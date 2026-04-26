package engine

import (
	"testing"
	"time"

	"github.com/Enach/paceday/backend/calendar"
)

func TestEmailDomain(t *testing.T) {
	cases := []struct {
		email string
		want  string
	}{
		{"alice@company.com", "company.com"},
		{"bob@gmail.com", "gmail.com"},
		{"noatsign", ""},
		{"multi@at@signs.com", "signs.com"},
	}
	for _, tc := range cases {
		if got := emailDomain(tc.email); got != tc.want {
			t.Errorf("emailDomain(%q) = %q, want %q", tc.email, got, tc.want)
		}
	}
}

func TestPersonalDomainBlocklist(t *testing.T) {
	blocked := []string{
		"gmail.com", "googlemail.com", "outlook.com", "hotmail.com",
		"yahoo.com", "icloud.com", "live.com", "msn.com",
		"proton.me", "protonmail.com",
	}
	for _, d := range blocked {
		if !personalDomains[d] {
			t.Errorf("expected %q to be in personalDomains blocklist", d)
		}
	}
}

func TestFreeBusyCache(t *testing.T) {
	svc := NewFreeBusyService(nil, nil)

	// Empty cache misses.
	if _, ok := svc.getCached("missing"); ok {
		t.Error("expected cache miss for unknown key")
	}

	slots := []calendar.TimeSlot{
		{Start: time.Now(), End: time.Now().Add(time.Hour)},
	}
	svc.setCached("key1", cachedResult{
		slots:     slots,
		coverage:  "known",
		expiresAt: time.Now().Add(15 * time.Minute),
	})

	got, ok := svc.getCached("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.coverage != "known" {
		t.Errorf("coverage = %q, want known", got.coverage)
	}
	if len(got.slots) != 1 {
		t.Errorf("slots len = %d, want 1", len(got.slots))
	}
}

func TestFreeBusyCacheExpiry(t *testing.T) {
	svc := NewFreeBusyService(nil, nil)

	svc.setCached("expiredKey", cachedResult{
		slots:     nil,
		coverage:  "known",
		expiresAt: time.Now().Add(-time.Second), // already expired
	})

	if _, ok := svc.getCached("expiredKey"); ok {
		t.Error("expected expired cache entry to miss")
	}
}

func TestNewFreeBusyService(t *testing.T) {
	svc := NewFreeBusyService(nil, nil)
	// Verify cache is pre-allocated by storing and retrieving an entry.
	svc.setCached("init-check", cachedResult{
		coverage:  "known",
		expiresAt: time.Now().Add(time.Minute),
	})
	if _, ok := svc.getCached("init-check"); !ok {
		t.Error("expected cache to be initialized and functional")
	}
}
