package api

import (
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
	n, err := storage.GetUser(q, 5) // separate line: out of this shape's scope
	if err == nil {
		name += n
	}
	return name, nil
}
