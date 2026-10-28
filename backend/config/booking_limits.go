package config

import (
	"fmt"
	"strconv"
	"strings"
)

// NEW-booking-rate-limit, spec Q1: the public booking limits (20 bookings per
// 10 minutes, 120 slot listings per minute, per client) are fixed in
// production. The e2e stack sends all its traffic as one client behind nginx
// and may keep its backend between runs (E2E_KEEP_STACK=1), so it may raise
// the counts. The override is honoured only together with the test-mode
// declaration EnvAllowStandins, which no production deployment sets; the
// windows are never configurable.
const (
	EnvPublicBookingLimit = "PACEDAY_PUBLIC_BOOKING_LIMIT"
	EnvPublicSlotsLimit   = "PACEDAY_PUBLIC_SLOTS_LIMIT"
)

// DecideBookingRateLimits returns the request counts to use for public booking
// and slot listing; 0 means "keep the production value". It never fails: a
// value without the test-mode declaration, or one that is not a positive
// integer, is ignored with a notice for the operator.
func DecideBookingRateLimits(getenv func(string) string) (create, slots int, notices []string) {
	notices = make([]string, 0)
	rawCreate := strings.TrimSpace(getenv(EnvPublicBookingLimit))
	rawSlots := strings.TrimSpace(getenv(EnvPublicSlotsLimit))
	if rawCreate == "" && rawSlots == "" {
		return 0, 0, notices
	}
	testMode, _ := strconv.ParseBool(strings.TrimSpace(getenv(EnvAllowStandins)))
	if !testMode {
		notices = append(notices, fmt.Sprintf("%s/%s ignored: public booking limits can be raised only in test mode (%s=true); production limits apply", EnvPublicBookingLimit, EnvPublicSlotsLimit, EnvAllowStandins))
		return 0, 0, notices
	}
	parse := func(name, raw string) int {
		if raw == "" {
			return 0
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			notices = append(notices, fmt.Sprintf("%s=%q is not a positive integer; the production limit applies", name, raw))
			return 0
		}
		notices = append(notices, fmt.Sprintf("%s=%d: public booking limit raised for test mode; never set this in production", name, n))
		return n
	}
	return parse(EnvPublicBookingLimit, rawCreate), parse(EnvPublicSlotsLimit, rawSlots), notices
}
