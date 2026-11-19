package api

import (
	"errors"
	"strconv"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
)

// PAC-142: `if v, err := target(); err == nil { ... }` with no else and no
// later use of err. Flagged: the failure takes the "absent" path.
func swallows(q storage.Querier, s storage.Store) string {
	name := ""
	if n, err := storage.GetUser(q, 1); err == nil {
		name = n
	}
	if v, err := s.Get(2); nil == err { // reversed comparison
		name += v
	}
	if t, err := auth.LoadToken(3); (err == nil) && t != nil { // leftmost of &&
		name += *t
	}
	var err error
	var m string
	if m, err = storage.GetUser(q, 4); err == nil { // `=`, err never read later
		name += m
	}
	// A later write is not a read: err is only assigned after the if.
	var werr error
	var w string
	if w, werr = storage.GetUser(q, 6); werr == nil {
		name += w
	}
	werr = storage.Save(q, name)
	// An else-if that never reads err does not handle it.
	if n, _, err := storage.Count(q); err == nil {
		name += strconv.Itoa(n)
	} else if name == "" {
		name = "none"
	}
	return name
}

// Not flagged: the failure is handled, seen, or the call is not targeted.
func handled(q storage.Querier) (string, error) {
	name := ""
	if n, err := storage.GetUser(q, 1); err == nil { // has an else
		name = n
	} else {
		return "", err
	}
	if n, err := storage.GetUser(q, 2); err != nil { // the checked shape
		return "", err
	} else {
		name += n
	}
	if n, err := strconv.Atoi("5"); err == nil { // not a targeted call
		name += strconv.Itoa(n)
	}
	var err error
	var m string
	if m, err = storage.GetUser(q, 3); err == nil { // `=`, err read after the if
		name += m
	}
	if err != nil {
		return "", err
	}
	if t, err := auth.LoadToken(4); err != nil || t == nil { // || is not the shape
		return name, nil
	}
	if _, err := storage.GetUser(q, 6); err == nil { // the else-if reads err
		name += "known"
	} else if !errors.Is(err, errGone) {
		return "", err
	}
	n, err := storage.GetUser(q, 5) // separate line: out of this shape's scope
	if err == nil {
		name += n
	}
	return name, nil
}

var errGone = errors.New("gone")
