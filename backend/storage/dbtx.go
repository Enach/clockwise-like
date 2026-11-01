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

// BindConn returns a DBTX that runs every call on one reserved connection
// with ctx. PAC-81 AC-A19: the booking insert's connection is acquired while
// the request budget still bounds the wait, then used without cancellation.
func BindConn(ctx context.Context, conn *sql.Conn) DBTX { return &boundConn{ctx: ctx, conn: conn} }

type boundConn struct {
	ctx  context.Context
	conn *sql.Conn
}

func (b *boundConn) pick(ctx context.Context) context.Context {
	if ctx == nil || ctx == context.Background() || ctx == context.TODO() {
		return b.ctx
	}
	return ctx
}

func (b *boundConn) Exec(q string, args ...any) (sql.Result, error) {
	return b.conn.ExecContext(b.ctx, q, args...)
}
func (b *boundConn) Query(q string, args ...any) (*sql.Rows, error) {
	return b.conn.QueryContext(b.ctx, q, args...)
}
func (b *boundConn) QueryRow(q string, args ...any) *sql.Row {
	return b.conn.QueryRowContext(b.ctx, q, args...)
}
func (b *boundConn) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return b.conn.ExecContext(b.pick(ctx), q, args...)
}
func (b *boundConn) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return b.conn.QueryContext(b.pick(ctx), q, args...)
}
func (b *boundConn) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return b.conn.QueryRowContext(b.pick(ctx), q, args...)
}
func (b *boundConn) Begin() (*sql.Tx, error) { return b.conn.BeginTx(b.ctx, nil) }
func (b *boundConn) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return b.conn.BeginTx(b.pick(ctx), opts)
}

type reservedKey struct{}

// WithReserved carries a reserved connection (BindConn) to a store that
// would otherwise use the pool (PAC-81: the step-up record).
func WithReserved(ctx context.Context, db DBTX) context.Context {
	return context.WithValue(ctx, reservedKey{}, db)
}

// Reserved returns the connection reserved on ctx, or fallback.
func Reserved(ctx context.Context, fallback DBTX) DBTX {
	if db, ok := ctx.Value(reservedKey{}).(DBTX); ok && db != nil {
		return db
	}
	return fallback
}
