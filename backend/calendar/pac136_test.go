package calendar

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
)

// PAC-136: IsDuplicate is Google's 409 for an insert whose id is taken, also
// when wrapped; a 404/410 or a transport error is not.
func TestPAC136_IsDuplicate(t *testing.T) {
	dup := &googleapi.Error{Code: http.StatusConflict, Errors: []googleapi.ErrorItem{{Reason: "duplicate"}}}
	cases := map[string]struct {
		err  error
		want bool
	}{
		"409":       {dup, true},
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
