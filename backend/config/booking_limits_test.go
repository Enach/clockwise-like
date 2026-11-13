package config

import (
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// NEW-booking-rate-limit, spec Q1: the e2e stack may raise the public booking
// limits; a production deployment (no test-mode declaration) never changes
// them.
func TestDecideBookingRateLimits(t *testing.T) {
	cases := []struct {
		name          string
		env           map[string]string
		create, slots int
		noticeHas     string
	}{
		{name: "unset keeps production", env: map[string]string{}, create: 0, slots: 0},
		{name: "override without test mode is ignored", env: map[string]string{EnvPublicBookingLimit: "500", EnvPublicSlotsLimit: "2000"}, create: 0, slots: 0, noticeHas: "ignored"},
		{name: "override with test mode false is ignored", env: map[string]string{EnvAllowStandins: "false", EnvPublicBookingLimit: "500"}, create: 0, slots: 0, noticeHas: "ignored"},
		{name: "test mode applies both", env: map[string]string{EnvAllowStandins: "true", EnvPublicBookingLimit: "500", EnvPublicSlotsLimit: "2000"}, create: 500, slots: 2000, noticeHas: "test mode"},
		{name: "test mode applies one", env: map[string]string{EnvAllowStandins: "1", EnvPublicSlotsLimit: " 2000 "}, create: 0, slots: 2000, noticeHas: "test mode"},
		{name: "malformed value ignored", env: map[string]string{EnvAllowStandins: "true", EnvPublicBookingLimit: "lots", EnvPublicSlotsLimit: "0"}, create: 0, slots: 0, noticeHas: "not a positive integer"},
		{name: "negative value ignored", env: map[string]string{EnvAllowStandins: "true", EnvPublicBookingLimit: "-5"}, create: 0, slots: 0, noticeHas: "not a positive integer"},
		{name: "test mode alone changes nothing", env: map[string]string{EnvAllowStandins: "true"}, create: 0, slots: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			create, slots, notices := DecideBookingRateLimits(envMap(tc.env))
			if create != tc.create || slots != tc.slots {
				t.Fatalf("limits = %d/%d, want %d/%d", create, slots, tc.create, tc.slots)
			}
			joined := strings.Join(notices, "\n")
			if tc.noticeHas == "" && joined != "" {
				t.Fatalf("unexpected notices: %q", joined)
			}
			if tc.noticeHas != "" && !strings.Contains(joined, tc.noticeHas) {
				t.Fatalf("notices = %q, want one containing %q", joined, tc.noticeHas)
			}
		})
	}
}
