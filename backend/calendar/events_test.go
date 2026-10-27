package calendar

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
)

// PAC-29, D-9: only Google's 404 and 410 mean the event is gone.
func TestIsEventGone(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"404", &googleapi.Error{Code: http.StatusNotFound}, true},
		{"410", &googleapi.Error{Code: http.StatusGone}, true},
		{"wrapped 404", fmt.Errorf("get: %w", &googleapi.Error{Code: http.StatusNotFound}), true},
		{"403", &googleapi.Error{Code: http.StatusForbidden}, false},
		{"500", &googleapi.Error{Code: http.StatusInternalServerError}, false},
		{"transport", errors.New("dial tcp: connection refused"), false},
		{"nil", nil, false},
	} {
		if got := IsEventGone(tc.err); got != tc.want {
			t.Errorf("%s: IsEventGone = %v, want %v", tc.name, got, tc.want)
		}
	}
}
