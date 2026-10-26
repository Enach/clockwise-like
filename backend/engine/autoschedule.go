package engine

import (
	"fmt"
	"strings"

	"github.com/robfig/cron/v3"
)

// autoScheduleParser accepts exactly five fields — minute, hour, day of month,
// month, day of week — and nothing else: no seconds, no descriptors such as
// @daily, no CRON_TZ= prefix.
var autoScheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ParseAutoScheduleCron is the one parser for a stored auto-schedule
// expression. The settings write refuses what it refuses and the focus
// scheduler skips what it refuses (PAC-49 §4.3 item 9), so the two can never
// disagree. The returned schedule evaluates in the location of the instant it
// is given.
func ParseAutoScheduleCron(expr string) (cron.Schedule, error) {
	if n := len(strings.Fields(expr)); n != 5 {
		return nil, fmt.Errorf("expected exactly 5 fields, found %d: %q", n, expr)
	}
	return autoScheduleParser.Parse(expr)
}
