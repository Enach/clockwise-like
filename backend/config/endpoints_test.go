package config

import (
	"strconv"
	"strings"
	"testing"
)

func mustDecide(t *testing.T, url, allow string) CalendarEndpoint {
	t.Helper()
	d, err := DecideCalendarEndpoint(url, allow)
	if err != nil {
		t.Fatalf("DecideCalendarEndpoint(%q, %q): unexpected error: %v", url, allow, err)
	}
	return d
}

func mustRefuse(t *testing.T, url, allow string, mustName ...string) {
	t.Helper()
	d, err := DecideCalendarEndpoint(url, allow)
	if err == nil {
		t.Fatalf("DecideCalendarEndpoint(%q, %q) accepted (%+v), want refused", url, allow, d)
	}
	if d != (CalendarEndpoint{}) {
		t.Errorf("refusal returned a partial decision %+v", d)
	}
	for _, s := range mustName {
		// The value may be named verbatim or Go-quoted (control characters
		// escaped so they cannot corrupt the log line); both name it.
		if !strings.Contains(err.Error(), s) && !strings.Contains(err.Error(), strconv.Quote(s)) {
			t.Errorf("refusal %q does not name %q", err.Error(), s)
		}
	}
}

func assertProduction(t *testing.T, d CalendarEndpoint) {
	t.Helper()
	if d.BaseURL != PublishedCalendarEndpoint {
		t.Errorf("BaseURL = %q, want the published address %q", d.BaseURL, PublishedCalendarEndpoint)
	}
	if d.Guard {
		t.Error("guard is on in the production decision")
	}
	if d.Notice != "" {
		t.Errorf("production decision carries a notice: %q", d.Notice)
	}
}

// AC-1.
func TestCalendarEndpoint_BothUnsetIsProductionDecision(t *testing.T) {
	assertProduction(t, mustDecide(t, "", ""))
	if !strings.HasSuffix(PublishedCalendarEndpoint, CalendarAPIRoot) {
		t.Errorf("published address %q does not end in the API root %q", PublishedCalendarEndpoint, CalendarAPIRoot)
	}
}

// AC-2: declaring test mode grants nothing by itself.
func TestCalendarEndpoint_DeclarationAloneIsInert(t *testing.T) {
	for _, allow := range []string{"true", "1", "TRUE", "false", "garbage"} {
		t.Run(allow, func(t *testing.T) { assertProduction(t, mustDecide(t, "", allow)) })
	}
}

// AC-4: an address without the declaration is refused, naming both settings.
func TestCalendarEndpoint_AddressWithoutDeclarationIsRefused(t *testing.T) {
	for _, allow := range []string{"", "false", "0", "no", "yes-please"} {
		t.Run("allow="+allow, func(t *testing.T) {
			mustRefuse(t, "http://fake-google:8080/calendar/v3/", allow, EnvCalendarBaseURL, EnvAllowStandins)
		})
	}
}

// AC-5: unintelligible values are refused, naming the value, never partially
// applied. User information is refused, never stripped.
func TestCalendarEndpoint_RejectsUnintelligibleValue(t *testing.T) {
	cases := []struct{ name, url string }{
		{"unparseable", "http://fake-google:80:80/calendar/v3/"},
		{"control character", "http://fake-google\x7f/calendar/v3/"},
		{"empty host", "http:///calendar/v3/"},
		{"no scheme or host", "fake-google/calendar/v3/"},
		{"blank", " "},
		{"ftp scheme", "ftp://fake-google:8080/calendar/v3/"},
		{"userinfo", "http://fake-google@evil.example.com/calendar/v3/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustRefuse(t, tc.url, "true", tc.url)
		})
	}
	t.Run("userinfo password is not echoed", func(t *testing.T) {
		_, err := DecideCalendarEndpoint("http://user:s3cret@fake-google:8080/calendar/v3/", "true")
		if err == nil {
			t.Fatal("accepted a value carrying user information")
		}
		if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("refusal echoes the password: %v", err)
		}
		if !strings.Contains(err.Error(), "fake-google:8080") {
			t.Errorf("refusal does not name the value: %v", err)
		}
	})
}

// AC-6 (first half): the outcome per row, exactly as the spec's table lists it.
// No outcome depends on the shape of the host.
func TestCalendarEndpoint_HostOutcomeTable(t *testing.T) {
	names := []string{"2130706433", "0x7f000001", "017700000001", "127.1", "0x7f.0.0.1", "fake-google", "localhost", "evil.example.com"}
	literals := []string{"[::1]", "[0:0:0:0:0:0:0:1]", "[::ffff:127.0.0.1]"}
	refused := []string{"[::1%25lo]", "0.0.0.0", "[::]", "fake-google@evil.example.com"}

	for _, h := range append(append([]string{}, names...), literals...) {
		t.Run("accepted/"+h, func(t *testing.T) {
			u := "http://" + h + ":8080/calendar/v3/"
			d := mustDecide(t, u, "true")
			if d.BaseURL != u || !d.Guard {
				t.Errorf("decision = %+v, want BaseURL %q with the guard on", d, u)
			}
		})
	}
	for _, h := range refused {
		t.Run("refused/"+h, func(t *testing.T) {
			u := "http://" + h + ":8080/calendar/v3/"
			mustRefuse(t, u, "true", u)
		})
	}
}

// AC-15: the demoted startup check. Literals outside the permitted ranges are
// refused; names are accepted whatever they look like, because names are
// judged at connection time by the guard in backend/calendar (plan §1.4). This
// is not the safety control.
func TestCalendarEndpoint_RefusesNonPermittedLiteral(t *testing.T) {
	for _, h := range []string{"8.8.8.8", "169.254.169.254", "0.0.0.0", "[::]", "[fe80::1]", "172.32.0.1"} {
		t.Run("refused/"+h, func(t *testing.T) {
			u := "http://" + h + ":8080/calendar/v3/"
			mustRefuse(t, u, "true", u)
		})
	}
	for _, h := range []string{"127.0.0.1", "127.0.0.2", "[::1]", "[::ffff:127.0.0.1]", "10.0.0.5", "192.168.1.10", "[fd00::5]",
		// Names: accepted here, judged at connect time (AC-15, §1.4).
		"fake-google", "evil.example.com"} {
		t.Run("accepted/"+h, func(t *testing.T) {
			d := mustDecide(t, "http://"+h+":8080/calendar/v3/", "true")
			if !d.Guard {
				t.Error("guard is off for a redirected deployment")
			}
		})
	}
}

// AC-26 (unit): the address must end in the provider's API root, with the
// trailing slash, or requests silently lose the v3 segment.
func TestCalendarEndpoint_RequiresAPIRoot(t *testing.T) {
	for _, u := range []string{
		"http://fake-google:8080",
		"http://fake-google:8080/",
		"http://fake-google:8080/calendar/v3",
		"http://fake-google:8080/other/",
	} {
		t.Run(u, func(t *testing.T) { mustRefuse(t, u, "true", u, "/calendar/v3/") })
	}
	t.Run("https is accepted", func(t *testing.T) {
		mustDecide(t, "https://fake-google:8443/calendar/v3/", "true")
	})
}

// AC-14 (unit support): a valid override carries a notice naming the target
// and disclaiming production.
func TestCalendarEndpoint_NoticeNamesTargetAndDisclaimsProduction(t *testing.T) {
	const u = "http://fake-google:8080/calendar/v3/"
	d := mustDecide(t, u, "true")
	if !strings.Contains(d.Notice, u) {
		t.Errorf("notice %q does not name the target", d.Notice)
	}
	if !strings.Contains(strings.ToLower(d.Notice), "not a production deployment") {
		t.Errorf("notice %q does not say the deployment is not production", d.Notice)
	}
	if !strings.Contains(strings.ToLower(d.Notice), "redirected") {
		t.Errorf("notice %q does not say calendar traffic is redirected", d.Notice)
	}
}
