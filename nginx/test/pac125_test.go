package nginxtest

// PAC-125: the access log carries no path parameter that is an email address
// or an invite token, in any spelling the client can send.

import (
	"strings"
	"testing"
)

func TestPAC125AccessLogHasNoEmailOrInviteToken(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	const email = "pac125.member"
	const token = "PAC125-SECRET-TOKEN"
	cases := []struct {
		method, uri, want string
	}{
		{"DELETE", "/api/manager/team/members/" + email + "%40example.test", "/api/manager/team/members/{email}"},
		{"PATCH", "/api/manager/team/members/" + email + "@example.test", "/api/manager/team/members/{email}"},
		{"POST", "/api/manager/team/members/" + email + "%40example.test/schedule", "/api/manager/team/members/{email}/schedule"},
		// An encoded slash inside the address: $uri decodes it, and nothing
		// after the prefix is copied.
		{"DELETE", "/api/manager/team/members/a%2F" + email + "%40example.test", "/api/manager/team/members/{email}"},
		{"GET", "/api/teams/invites/" + token, "/api/teams/invites/{token}"},
		{"POST", "/api/teams/invites/" + token + "/accept", "/api/teams/invites/{token}/accept"},
		// Case, doubled slashes and dot segments: nginx normalises $uri.
		{"GET", "/API/Teams/Invites/" + token, "/api/teams/invites/{token}"},
		{"GET", "/api//teams/./invites/" + token, "/api/teams/invites/{token}"},
		// The web app's invite page, served through the same edge.
		{"GET", "/app/team/invite/" + token, "/app/team/invite/{token}"},
	}
	for _, c := range cases {
		sendOne(t, "", "http://"+n+c.uri, "-method", c.method)
	}
	// Control: an ordinary path is still logged as received.
	sendOne(t, "", "http://"+n+"/api/pac125-control")
	logs := nginxLogs(t, n)
	if !strings.Contains(logs, "GET /api/pac125-control ") {
		t.Fatalf("control request not logged as received:\n%s", logs)
	}
	for _, secret := range []string{email, "example.test", token, strings.ToLower(token)} {
		if strings.Contains(logs, secret) {
			t.Errorf("access log carries %q:\n%s", secret, logs)
		}
	}
	for _, c := range cases {
		if want := c.method + " " + c.want + " "; !strings.Contains(strings.ToLower(logs), strings.ToLower(want)) {
			t.Errorf("%s %s: no %q line in the access log:\n%s", c.method, c.uri, want, logs)
		}
	}
}
