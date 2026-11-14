package engine

// PAC-40 part B: the booker-erasure job (contracts/features/PAC-40.md §5;
// spec docs/specs/PAC-40.md §3.5). It runs in-process: once as part of
// start-up and then every 24 h (backend/main.go), each run wrapped by cronJob
// so the PAC-81 drain awaits it and no run starts once shutdown has begun.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
)

// bookerErasureLinkTimeout bounds one link's transaction. Each lock wait is
// already bounded by storage.LinkLockTimeout (PAC-101, 10 s); this caps the
// whole unit so a run always ends.
const bookerErasureLinkTimeout = 30 * time.Second

// BookerErasure erases bookers' names and emails on deleted links once due.
// Now, Logf and Report are injectable (AC-26..AC-35): nil means time.Now,
// log.Printf and no error report.
type BookerErasure struct {
	DB   *sql.DB
	Now  func() time.Time
	Logf func(format string, args ...any)
	// Report receives one sanitised error per failure (Sentry in
	// production). It never receives a database error: only the link id and
	// the error class (K4-4).
	Report func(err error)
}

// BookerErasureSummary is one run's counts, as its summary line prints them.
type BookerErasureSummary struct {
	LinksExamined, LinksErased, BookingsErased, Failures int
}

// Run erases every link due at e.Now(). Each link is its own transaction
// (all or nothing); a failure on one link is logged and reported without row
// data and the run moves on. A cancelled ctx (the PAC-81 drain) stops the run
// BETWEEN links: a link already started finishes or rolls back on its own.
// Exactly one summary line is written per run, whatever happened.
func (e *BookerErasure) Run(ctx context.Context) BookerErasureSummary {
	now := e.now()
	var s BookerErasureSummary
	defer func() {
		e.logf("booker-erasure: run at=%s links_examined=%d links_erased=%d bookings_erased=%d failures=%d",
			now.UTC().Format(time.RFC3339), s.LinksExamined, s.LinksErased, s.BookingsErased, s.Failures)
	}()
	due, err := storage.ListBookerErasureDue(ctx, e.DB, now)
	if err != nil {
		s.Failures++
		class := ErasureErrorClass(err)
		e.logf("booker-erasure: scan failed: %s", class)
		e.report(fmt.Errorf("booker-erasure: scan failed: %s", class))
		return s
	}
	for _, id := range due {
		if ctx.Err() != nil {
			return s
		}
		s.LinksExamined++
		n, err := e.eraseLink(ctx, id, now)
		if err != nil {
			s.Failures++
			class := ErasureErrorClass(err)
			e.logf("booker-erasure: link %s failed: %s", id, class)
			e.report(fmt.Errorf("booker-erasure: link %s failed: %s", id, class))
			continue
		}
		if n > 0 {
			s.LinksErased++
			s.BookingsErased += n
		}
	}
	return s
}

// eraseLink runs one link's transaction detached from ctx's cancellation,
// so the drain never cuts a link in half, and under its own cap.
func (e *BookerErasure) eraseLink(ctx context.Context, id uuid.UUID, now time.Time) (int, error) {
	linkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookerErasureLinkTimeout)
	defer cancel()
	return storage.EraseLinkBookers(linkCtx, e.DB, id, now)
}

// ErasureErrorClass names err's class without any row data (contract §5 "Log
// line", K4-4): the SQLSTATE code of a *pgconn.PgError (pgx, the backend's
// driver) or a *pq.Error, taken with errors.As and never through Error()
// (Message and Detail can repeat the booker's name and email); otherwise a
// fixed word for the context and pool errors, or "unclassified". Never
// err.Error() of an unknown error.
func ErasureErrorClass(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return "sqlstate " + sqlstate(pg.Code)
	}
	var pqe *pq.Error
	if errors.As(err, &pqe) {
		return "sqlstate " + sqlstate(string(pqe.Code))
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "context canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context deadline exceeded"
	case errors.Is(err, sql.ErrConnDone):
		return "connection closed"
	case errors.Is(err, sql.ErrTxDone):
		return "transaction done"
	case errors.Is(err, storage.ErrAuditNoSubject):
		return "audit entry has no subject"
	}
	return "unclassified"
}

// sqlstate keeps a SQLSTATE only when it has the standard shape (five
// characters from [0-9A-Z]), so even a forged code cannot carry text.
func sqlstate(code string) string {
	if len(code) != 5 {
		return "invalid"
	}
	for _, c := range code {
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') {
			return "invalid"
		}
	}
	return code
}

func (e *BookerErasure) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *BookerErasure) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (e *BookerErasure) report(err error) {
	if e.Report != nil {
		e.Report(err)
	}
}
