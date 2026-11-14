package nginxtest

import "testing"

// PAC-115: only daemon-side transients are retried. The 502 case is the
// stderr of CI run 43577068776.
func TestIsTransientDaemonError(t *testing.T) {
	const ci502 = "docker: unknown: failed to copy: httpReadSeeker: failed open: unexpected status from GET request to https://registry-1.docker.io/v2/library/nginx/blobs/sha256:1abafee9: 502 Bad Gateway"
	cases := []struct {
		name   string
		sub    string
		code   int
		stderr string
		want   bool
	}{
		{"CI 502 on implicit pull", "run", 125, ci502, true},
		{"pull 503", "pull", 1, "Error response from daemon: received unexpected HTTP status: 503 Service Unavailable", true},
		{"TLS timeout", "run", 125, "docker: Error response from daemon: Get \"https://registry-1.docker.io/v2/\": net/http: TLS handshake timeout.", true},
		{"name conflict is real", "run", 125, `docker: Error response from daemon: Conflict. The container name "/pnt-1-stub-1" is already in use`, false},
		{"missing network is real", "run", 125, "docker: Error response from daemon: network paceday-nginx-test-x not found.", false},
		{"bad mount is real", "run", 125, "docker: Error response from daemon: invalid mount config for type \"bind\": bind source path does not exist", false},
		{"container command failed", "run", 1, "upstream: i/o timeout", false},
		{"pull exit 125 is not a pull failure", "pull", 125, ci502, false},
		{"success", "run", 0, "", false},
	}
	for _, c := range cases {
		if got := isTransientDaemonError(c.sub, c.code, c.stderr); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
