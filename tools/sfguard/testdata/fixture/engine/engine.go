package engine

import "github.com/Enach/paceday/backend/calendar"

type calendarOps interface {
	listEvents() ([]string, error)
	deleteEvent(id string) error
}

type unrelated interface {
	listEvents() ([]string, error)
}

func Sync(ops calendarOps, u unrelated, p calendar.Provider) int {
	evs, _ := ops.listEvents()   // flagged: calendar-ops seam
	_ = ops.deleteEvent("x")     // flagged
	more, _ := p.ListEvents()    // flagged: calendar method
	other, _ := u.listEvents()   // not a target interface
	c, _ := calendar.NewClient() // not a method: constructors are not operations
	_ = c
	return len(evs) + len(more) + len(other)
}
