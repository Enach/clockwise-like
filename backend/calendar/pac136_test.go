package calendar

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
)

// PAC-136: IsDuplicate is Google's 409 with the reason "duplicate" (an
// insert whose id is taken), also when wrapped; another 409, a 404/410 or a
// transport error is not.
func TestPAC136_IsDuplicate(t *testing.T) {
	dup := &googleapi.Error{Code: http.StatusConflict, Errors: []googleapi.ErrorItem{{Reason: "duplicate"}}}
	cases := map[string]struct {
		err  error
		want bool
	}{
		"409":       {dup, true},
		"409 other": {&googleapi.Error{Code: http.StatusConflict, Errors: []googleapi.ErrorItem{{Reason: "conflict"}}}, false},
		"409 bare":  {&googleapi.Error{Code: http.StatusConflict}, false},
		"wrapped":   {fmt.Errorf("insert: %w", dup), true},
		"404":       {&googleapi.Error{Code: http.StatusNotFound}, false},
		"410":       {&googleapi.Error{Code: http.StatusGone}, false},
		"transport": {errors.New("connection reset"), false},
		"nil":       {nil, false},
	}
	for name, c := range cases {
		if got := IsDuplicate(c.err); got != c.want {
			t.Errorf("%s: IsDuplicate = %v, want %v", name, got, c.want)
		}
	}
}

// PAC-136 review N1: IsPermissionDenied is a 403 for a permission reason,
// also when wrapped; a rate-limit or quota 403, a bare 403 or a 404 is not.
func TestPAC136_IsPermissionDenied(t *testing.T) {
	forbidden := func(reason string) error {
		return &googleapi.Error{Code: http.StatusForbidden, Errors: []googleapi.ErrorItem{{Reason: reason}}}
	}
	for _, reason := range []string{"forbidden", "forbiddenForNonOrganizer", "requiredAccessLevel"} {
		if !IsPermissionDenied(fmt.Errorf("update: %w", forbidden(reason))) {
			t.Errorf("403 %s is not a permission denial", reason)
		}
	}
	for _, reason := range []string{"rateLimitExceeded", "userRateLimitExceeded", "quotaExceeded", ""} {
		if IsPermissionDenied(forbidden(reason)) {
			t.Errorf("403 %q is a permission denial", reason)
		}
	}
	if IsPermissionDenied(&googleapi.Error{Code: http.StatusNotFound, Errors: []googleapi.ErrorItem{{Reason: "forbidden"}}}) ||
		IsPermissionDenied(errors.New("x")) || IsPermissionDenied(nil) {
		t.Error("a non-403 is a permission denial")
	}
}
