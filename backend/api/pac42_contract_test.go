package api

// PAC-42 contract criteria AC-5, AC-9, AC-10 and AC-13, read from the committed
// bundle with the PAC-21 test parser (parseBundle).

import (
	"regexp"
	"strings"
	"testing"
)

func pac42Scalar(n *yamlNode) string {
	if n == nil {
		return ""
	}
	return strings.Trim(n.scalar, `'"`)
}

// AC-5: week_start has one format on both read schemas, and neither operation
// says the two differ.
func TestPAC42ContractOneWeekStartFormat(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	for _, s := range []string{"AnalyticsWeekWithBreakdown", "AnalyticsWeekRow"} {
		ws := root.get("components", "schemas", s, "properties", "week_start")
		if ws == nil {
			t.Fatalf("%s.week_start missing", s)
		}
		if f := pac42Scalar(ws.get("format")); f != "date" {
			t.Errorf("%s.week_start format = %q, want date", s, f)
		}
	}
	differ := regexp.MustCompile(`(?i)DIFFERENT|RFC3339 timestamp here|NOT the "\d{4}`)
	for _, p := range []string{"/api/analytics/week", "/api/analytics/trends"} {
		d := pac42Scalar(root.get("paths", p, "get", "description"))
		if d == "" {
			t.Fatalf("%s: no description", p)
		}
		if differ.MatchString(d) {
			t.Errorf("%s description still says the week starts differ: %q", p, differ.FindString(d))
		}
	}
}

// AC-9: the single-week read has no 404 and no x-uncertain anywhere under it.
func TestPAC42ContractWeekHasNo404(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	op := root.get("paths", "/api/analytics/week", "get")
	if op == nil {
		t.Fatal("getAnalyticsWeek missing")
	}
	if op.get("responses", "404") != nil || op.get("responses", "'404'") != nil {
		t.Error("getAnalyticsWeek still documents a 404")
	}
	var walk func(n *yamlNode, where string)
	walk = func(n *yamlNode, where string) {
		for _, k := range n.order {
			if k == "x-uncertain" {
				t.Errorf("x-uncertain at %s", where)
			}
			walk(n.children[k], where+"/"+k)
		}
	}
	walk(op, "getAnalyticsWeek")
}

// AC-10: buffer_minutes carries no x-uncertain, says it is always 0 and links API-057.
func TestPAC42ContractBufferMinutesStatedAsFact(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	bm := root.get("components", "schemas", "AnalyticsWeekWithBreakdown", "properties", "buffer_minutes")
	if bm == nil {
		t.Fatal("buffer_minutes missing")
	}
	if bm.get("x-uncertain") != nil {
		t.Error("buffer_minutes still carries x-uncertain")
	}
	d := pac42Scalar(bm.get("description"))
	if !strings.Contains(d, "Always 0") || !strings.Contains(d, "API-057") {
		t.Errorf("buffer_minutes description = %q, want it to state always 0 and link API-057", d)
	}
}

// AC-13: every proportion states the percentage scale, none calls itself a 0..1
// fraction; the rate and focus score are bounded 0..100, the breakdown percentage
// has no maximum.
func TestPAC42ContractEveryRateIsAPercentage(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	fraction := regexp.MustCompile(`(?i)0\.\.1|fraction`)
	check := func(where string, n *yamlNode, bounded bool) {
		if n == nil {
			t.Errorf("%s missing", where)
			return
		}
		d := pac42Scalar(n.get("description"))
		if !strings.Contains(d, "percentage where 100 means") {
			t.Errorf("%s: description does not state the percentage scale: %q", where, d)
		}
		if fraction.MatchString(d) {
			t.Errorf("%s: description calls itself a fraction: %q", where, fraction.FindString(d))
		}
		lo, hi := pac42Scalar(n.get("minimum")), pac42Scalar(n.get("maximum"))
		if bounded && (lo != "0" || hi != "100") {
			t.Errorf("%s: bounds %q..%q, want 0..100", where, lo, hi)
		}
		if !bounded && hi != "" {
			t.Errorf("%s: declares maximum %q it does not honour", where, hi)
		}
	}
	for _, s := range []string{"AnalyticsWeekWithBreakdown", "AnalyticsWeekRow"} {
		props := root.get("components", "schemas", s, "properties")
		check(s+".habit_completion_rate", props.get("habit_completion_rate"), true)
		check(s+".focus_score", props.get("focus_score"), true)
	}
	check("AnalyticsBreakdownEntry.percentage", root.get("components", "schemas", "AnalyticsBreakdownEntry", "properties", "percentage"), false)
}
