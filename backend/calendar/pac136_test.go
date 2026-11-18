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

// PAC-136 review M1: IsForbidden is a 403, also when wrapped.
func TestPAC136_IsForbidden(t *testing.T) {
	if !IsForbidden(fmt.Errorf("update: %w", &googleapi.Error{Code: http.StatusForbidden})) {
		t.Error("a wrapped 403 is not forbidden")
	}
	if IsForbidden(&googleapi.Error{Code: http.StatusNotFound}) || IsForbidden(errors.New("x")) || IsForbidden(nil) {
		t.Error("a non-403 is forbidden")
	}
}
