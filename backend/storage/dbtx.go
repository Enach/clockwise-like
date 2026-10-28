package storage

import (
	"context"
	"database/sql"
)

// DBTX is the part of *sql.DB the storage functions on the request paths of
// PAC-81 AC-A9 use. *sql.DB satisfies it, so callers that pass the pool are
// unchanged; Bind returns one whose calls wait for a pooled connection only
// until the request's context ends.
type DBTX interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	Begin() (*sql.Tx, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Bind returns db with ctx attached: every call made without a context, or
// with context.Background(), runs on ctx, so the wait for a pooled
// connection and the query itself end when ctx does (PAC-81 AC-A9). A call
// that passes its own non-background context keeps it.
func Bind(ctx context.Context, db DBTX) DBTX {
	if b, ok := db.(*boundDB); ok {
		db = b.db
	}
	return &boundDB{ctx: ctx, db: db}
}

type boundDB struct {
	ctx context.Context
	db  DBTX
}

func (b *boundDB) pick(ctx context.Context) context.Context {
	if ctx == nil || ctx == context.Background() || ctx == context.TODO() {
		return b.ctx
	}
	return ctx
}

func (b *boundDB) Exec(q string, args ...any) (sql.Result, error) {
	return b.db.ExecContext(b.ctx, q, args...)
}
func (b *boundDB) Query(q string, args ...any) (*sql.Rows, error) {
	return b.db.QueryContext(b.ctx, q, args...)
}
func (b *boundDB) QueryRow(q string, args ...any) *sql.Row {
	return b.db.QueryRowContext(b.ctx, q, args...)
}
func (b *boundDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return b.db.ExecContext(b.pick(ctx), q, args...)
}
func (b *boundDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return b.db.QueryContext(b.pick(ctx), q, args...)
}
func (b *boundDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return b.db.QueryRowContext(b.pick(ctx), q, args...)
}
func (b *boundDB) Begin() (*sql.Tx, error) { return b.db.BeginTx(b.ctx, nil) }
func (b *boundDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return b.db.BeginTx(b.pick(ctx), opts)
}
