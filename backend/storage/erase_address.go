package storage

// PAC-134 (spec docs/specs/PAC-134.md; contract contracts/features/PAC-134.md
// sections 3, 6, 7, 8 and 10): the transaction of paceday-operator
// erase-address. One READ COMMITTED transaction finds every stored copy of one
// address in other people's data, inside the database (no value of a row that
// does not match ever reaches this process), erases each copy (delete, or
// anonymise a booking), keeps an account's own data and the earlier operator
// records, and writes one address-free operator_audit record.
//
// The address reaches PostgreSQL only as a bind parameter, never in statement
// text: every statement below is a constant built from fixed fragments, and
// the caller opens its pool with OpenNoMigrateDescribeExec (contract md
// §2.2a), so pgx never interpolates a bind client-side.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/stdlib"
)

// EraseAddressApp is the command's application_name and PAC-116 caller
// (PAC-84 contract 4.3).
const EraseAddressApp = "paceday-operator erase-address"

// Erase-address audit actions (migration 045).
const (
	EraseAddressAction       = "erase_address"
	EraseAddressDryRunAction = "erase_address_dry_run"
)

var (
	// ErrEraseAccountHeld: an account's address matches and the erasure was
	// not acknowledged (spec §3.2). Nothing changed and no record was written;
	// the result names the accounts.
	ErrEraseAccountHeld = errors.New("an account holds this address")
	// ErrEraseStatementLogging: the server's settings could write the bound
	// address to its log (contract md §2.3). Nothing was read from a table.
	ErrEraseStatementLogging = errors.New("statement logging could record the address")
	// ErrEraseBoundExceeded: the run reached its duration bound (contract md
	// §7) and was rolled back.
	ErrEraseBoundExceeded = errors.New("duration bound exceeded")
)

// EraseAddressOptions is one run. Address is the operator's input already
// trimmed of the PAC-129 set (TrimAddressSpace) and valid under ValidAddress,
// with its letter case kept (contract md §2.1, bind :t).
type EraseAddressOptions struct {
	Address                 string
	DryRun                  bool
	AccountHeldAcknowledged bool
	// MaxDuration bounds the whole transaction, measured from the call
	// (contract md §7). The command accepts 1 s to 8 s; tests pass shorter or
	// longer ones directly (md §10).
	MaxDuration time.Duration
	Run         OperatorRun
}

// ErasePlace is one place's count: Erased is "would erase" on a dry run.
type ErasePlace struct {
	Name           string
	Erased         int64
	KeptAccountOwn int64
}

// EraseAddressResult reports a run. Places is filled once discovery counted
// (also for a dry run that exits 3); AuditID is set when a record committed.
type EraseAddressResult struct {
	DryRun              bool
	Accounts            []uuid.UUID
	Acknowledged        bool
	Counted             bool
	Places              []ErasePlace
	KeptOperatorRecords int64
	AuditID             uuid.UUID
	// LockPhase is how long the erasure phase held its locks, from the
	// statement before lock_teams to the commit (AC-22b evidence); zero on a
	// dry run.
	LockPhase time.Duration
	// Total is the run's duration, from the call to the commit.
	Total time.Duration
}

// ErasePlaceNames are the places of contract md §2.5, in output order.
var ErasePlaceNames = []string{
	"team_added_members", "team_invites", "manager_team_members",
	"manager_team_member_assignments", "one_on_one_occurrences", "team_analytics_cache",
	"scheduling_link_hosts", "scheduling_link_create_keys", "bookings",
}

// erasePlaceOfKey maps the found table's place keys (and t4) to the places.
var erasePlaceOfKey = map[string]string{
	"t1": "team_added_members", "t2": "team_invites", "t3": "manager_team_members",
	"t4": "manager_team_member_assignments", "t5": "one_on_one_occurrences",
	"t6": "team_analytics_cache", "t7": "scheduling_link_hosts",
	"t8": "scheduling_link_create_keys", "t9": "bookings",
}

// OpenNoMigrateDescribeExec is OpenNoMigrate with every connection on pgx's
// QueryExecModeDescribeExec whatever the DSN says (contract md §2.2a,
// revision 2 B-1): binds always travel as server-side parameters of the
// extended protocol (unnamed statement, so it also works behind a
// transaction-pooling pgbouncer), never interpolated client-side into the
// statement text, as default_query_exec_mode=simple_protocol in a DSN would
// make pgx do. paceday-operator erase-address opens every pool with it, its
// dry run included: the address it binds must never reach statement text
// (the server log of a failed statement, pg_stat_activity.query).
func OpenNoMigrateDescribeExec(dsn, appName string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	cfg.RuntimeParams["application_name"] = appName
	cfg.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	// Review of #326, item 1: when the run's context ends (the deadline, or
	// Ctrl-C/SIGTERM), send PostgreSQL a cancel request at once, so the
	// server ends the in-flight statement (a backend waiting on a lock does
	// not notice a closed socket) and the run's locks go with it. pgx's
	// default handler only closes the socket.
	cfg.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: time.Second}
	}
	db := stdlib.OpenDB(*cfg)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

// EraseAddressMigrationVersion is the version of *_erase_address.up.sql (045).
func EraseAddressMigrationVersion() (uint, error) {
	return embeddedMigrationVersion("_erase_address.up.sql")
}

// The binds of contract md §3.1, as every statement that matches numbers
// them: $1 = :g (Go strings.ToLower of :t), $2 = :d (the database's lower()
// of :t), $3 = :acc (the matching account ids, uuid[]). A statement binds
// exactly the ones it uses.
const (
	eraseBindG   = "$1::text"
	eraseBindD   = "$2::text"
	eraseBindAcc = "$3::uuid[]"
)

// eraseMatch is M(c) of contract md §3.1 for a stored text expression c: the
// Go half through paceday_go_lower (with the sound char_length pre-filter)
// or the database half through lower(), both on values trimmed of the
// PAC-129 set.
func eraseMatch(c string) string {
	t := "paceday_trim_ecma(" + c + ")"
	return "((char_length(" + t + ") = char_length(" + eraseBindG + ") AND paceday_go_lower(" + t + ") = " + eraseBindG +
		") OR lower(" + t + ") = " + eraseBindD + ")"
}

// eraseJSONMatch is J(k) of contract md §3.1: any addresses[].email or
// removed[].email string of the outcomes document k matches; any other
// shape is ignored, never an error.
func eraseJSONMatch(k string) string {
	return "EXISTS (SELECT 1 FROM jsonb_array_elements(" +
		"CASE jsonb_typeof(" + k + "->'addresses') WHEN 'array' THEN " + k + "->'addresses' ELSE '[]'::jsonb END || " +
		"CASE jsonb_typeof(" + k + "->'removed') WHEN 'array' THEN " + k + "->'removed' ELSE '[]'::jsonb END) AS e(v) " +
		"WHERE jsonb_typeof(e.v->'email') = 'string' AND " + eraseMatch("(e.v->>'email')") + ")"
}

// eraseSettingsSQL is the first statement (contract md §2.3, §3.3
// [settings]; revision 2 note 3; stage-4 amendment N-3): the transaction-local
// bounds, and whether statement logging could record a bind. Every condition
// is evaluated in SQL on pg_settings.setting, which is unitless (ms for the
// durations), never by parsing current_setting's "2s" in Go; a custom
// auto_explain.log_min_duration that is absent (module not loaded) is NULL and
// does not refuse, a numeric one refuses when >= 0, and any other text (a
// placeholder that cannot be read as a number) refuses. Review of #326,
// item 2: debug_print_parse, debug_print_rewritten and debug_print_plan
// log query trees whose constants hold the bound address, so any of them on
// refuses too.
const eraseSettingsSQL = `
SELECT set_config('statement_timeout', $1, true), set_config('lock_timeout', $1, true),
       set_config('idle_in_transaction_session_timeout', $2, true),
       COALESCE((SELECT s.setting::bigint <> 0 FROM pg_settings s WHERE s.name = 'log_parameter_max_length_on_error'), true)
    OR (COALESCE((SELECT s.setting::bigint <> 0 FROM pg_settings s WHERE s.name = 'log_parameter_max_length'), true)
        AND (COALESCE((SELECT s.setting IN ('mod', 'all') FROM pg_settings s WHERE s.name = 'log_statement'), true)
          OR COALESCE((SELECT s.setting::bigint >= 0 FROM pg_settings s WHERE s.name = 'log_min_duration_statement'), true)
          OR COALESCE((SELECT s.setting::bigint >= 0 FROM pg_settings s WHERE s.name = 'log_min_duration_sample'), true)
          OR COALESCE((SELECT s.setting::float8 > 0 FROM pg_settings s WHERE s.name = 'log_transaction_sample_rate'), true)))
    OR COALESCE((SELECT CASE WHEN s.setting ~ '^-?[0-9]+$' THEN s.setting::bigint >= 0 ELSE true END
                 FROM pg_settings s WHERE s.name = 'auto_explain.log_min_duration'), false)
    OR COALESCE((SELECT bool_or(s.setting <> 'off') FROM pg_settings s
                 WHERE s.name IN ('debug_print_parse', 'debug_print_rewritten', 'debug_print_plan')), false)`

// eraseBudgetSQL re-arms the server-side bounds before every statement of
// the lock phase (stage-4 amendment N-1): statement_timeout and lock_timeout
// restart with each statement, so each lock-phase statement gets the time
// left until the run's deadline, and ends by it server-side even when the
// client is gone.
// eraseBudgetMargin is taken off each re-armed budget (N-1).
const eraseBudgetMargin = 20 * time.Millisecond

const eraseBudgetSQL = `SELECT set_config('statement_timeout', $1, true), set_config('lock_timeout', $1, true)`

var (
	eraseFoundTableSQL = `CREATE TEMP TABLE pac134_found (place text NOT NULL, own boolean NOT NULL, row_id uuid, team_id uuid,
    link_id uuid, owner_id uuid, row_key text) ON COMMIT DROP`

	eraseFindSQL = []struct{ name, sql string }{
		{"find_t1", `INSERT INTO pac134_found (place, own, team_id, row_key)
SELECT 't1', COALESCE(a.added_by = ANY(` + eraseBindAcc + `), false), a.team_id, a.email
FROM team_added_members a WHERE ` + eraseMatch("a.email")},
		{"find_t2", `INSERT INTO pac134_found (place, own, row_id, team_id)
SELECT 't2', COALESCE(i.invited_by = ANY(` + eraseBindAcc + `), false), i.id, i.team_id
FROM team_invites i WHERE ` + eraseMatch("i.invitee_email")},
		{"find_t3", `INSERT INTO pac134_found (place, own, row_id)
SELECT 't3', COALESCE(r.manager_user_id = ANY(` + eraseBindAcc + `), false), r.id
FROM manager_team_members r WHERE ` + eraseMatch("r.member_email")},
		{"find_t5", `INSERT INTO pac134_found (place, own, row_id)
SELECT 't5', COALESCE(o.manager_user_id = ANY(` + eraseBindAcc + `), false), o.id
FROM one_on_one_occurrences o WHERE ` + eraseMatch("o.member_email")},
		{"find_t6", `INSERT INTO pac134_found (place, own, row_id)
SELECT 't6', COALESCE(c.manager_user_id = ANY(` + eraseBindAcc + `), false), c.id
FROM team_analytics_cache c WHERE ` + eraseMatch("c.member_email")},
		{"find_t7", `INSERT INTO pac134_found (place, own, row_id, link_id)
SELECT 't7', COALESCE(l.owner_user_id = ANY(` + eraseBindAcc + `), false), h.id, h.link_id
FROM scheduling_link_hosts h JOIN scheduling_links l ON l.id = h.link_id
WHERE h.invitee_email IS NOT NULL AND ` + eraseMatch("h.invitee_email")},
		{"find_t9", `INSERT INTO pac134_found (place, own, row_id, link_id)
SELECT 't9', COALESCE(l.owner_user_id = ANY(` + eraseBindAcc + `), false), b.id, b.link_id
FROM bookings b JOIN scheduling_links l ON l.id = b.link_id
WHERE b.booker_erased_at IS NULL AND ` + eraseMatch("b.booker_email")},
		{"find_t8", `INSERT INTO pac134_found (place, own, owner_id, row_key)
SELECT 't8', COALESCE(k.owner_user_id = ANY(` + eraseBindAcc + `), false), k.owner_user_id, k.idempotency_key
FROM scheduling_link_create_keys k
WHERE k.co_host_changes IS NOT NULL AND ` + eraseJSONMatch("k.co_host_changes")},
	}

	eraseCountT10SQL = `SELECT count(*) FROM operator_audit o
WHERE (o.account_email IS NOT NULL AND ` + eraseMatch("o.account_email") + `)
   OR (jsonb_typeof(o.details->'freed_address') = 'string' AND ` + eraseMatch("(o.details->>'freed_address')") + `)`

	eraseCountsSQL = `SELECT f.place, count(*) FILTER (WHERE NOT f.own), count(*) FILTER (WHERE f.own)
FROM pac134_found f GROUP BY f.place
UNION ALL
SELECT 't4', count(*) FILTER (WHERE NOT f.own), count(*) FILTER (WHERE f.own)
FROM manager_team_member_assignments s
  JOIN manager_team_members r ON r.manager_user_id = s.manager_user_id AND r.member_email = s.member_email
  JOIN pac134_found f ON f.place = 't3' AND f.row_id = r.id`

	// eraseAnalyzeSQL (stage-4 amendment N-2): statistics for the joins of the
	// lock phase, without row_key: ANALYZE copies a column's common values into
	// pg_statistic (WAL-logged), and row_key holds the found address variants.
	eraseAnalyzeSQL = `ANALYZE pac134_found (place, own, row_id, team_id, link_id, owner_id)`

	// eraseLockPhaseSQL is the erasure phase of contract md §3.3 in the lock
	// order of md §6.1. binds says whether a statement takes $1-$3.
	eraseLockPhaseSQL = []struct {
		name  string
		binds bool
		sql   string
	}{
		{"lock_teams", false, `SELECT count(*) FROM (
  SELECT tm.id FROM teams tm WHERE tm.id IN (
      SELECT f.team_id FROM pac134_found f WHERE f.place IN ('t1', 't2') AND NOT f.own
      UNION SELECT s.team_id FROM manager_team_member_assignments s
        JOIN manager_team_members r ON r.manager_user_id = s.manager_user_id AND r.member_email = s.member_email
        JOIN pac134_found f ON f.place = 't3' AND NOT f.own AND f.row_id = r.id)
  ORDER BY tm.id FOR NO KEY UPDATE OF tm) x`},
		{"erase_t1", true, `WITH m AS MATERIALIZED (
  SELECT a.team_id, a.email FROM team_added_members a
  JOIN pac134_found f ON f.place = 't1' AND NOT f.own AND f.team_id = a.team_id AND f.row_key = a.email
  WHERE ` + eraseMatch("a.email") + ` AND (a.added_by IS NULL OR a.added_by <> ALL(` + eraseBindAcc + `))
  ORDER BY a.team_id, a.email COLLATE "C" FOR UPDATE OF a),
d AS (DELETE FROM team_added_members a USING m WHERE a.team_id = m.team_id AND a.email = m.email RETURNING 1)
SELECT count(*) FROM d`},
		{"erase_t2", true, `WITH m AS MATERIALIZED (
  SELECT i.id FROM team_invites i JOIN pac134_found f ON f.place = 't2' AND NOT f.own AND f.row_id = i.id
  WHERE ` + eraseMatch("i.invitee_email") + ` AND i.invited_by <> ALL(` + eraseBindAcc + `)
  ORDER BY i.id FOR UPDATE OF i),
d AS (DELETE FROM team_invites i USING m WHERE i.id = m.id RETURNING 1)
SELECT count(*) FROM d`},
		{"lock_roster", true, `SELECT count(*) FROM (
  SELECT r.id FROM manager_team_members r JOIN pac134_found f ON f.place = 't3' AND NOT f.own AND f.row_id = r.id
  WHERE ` + eraseMatch("r.member_email") + ` AND r.manager_user_id <> ALL(` + eraseBindAcc + `)
  ORDER BY r.manager_user_id, r.member_email COLLATE "C" FOR UPDATE OF r) x`},
		{"erase_t4", false, `WITH m AS MATERIALIZED (
  SELECT s.manager_user_id, s.team_id, s.member_email FROM manager_team_member_assignments s
  JOIN manager_team_members r ON r.manager_user_id = s.manager_user_id AND r.member_email = s.member_email
  JOIN pac134_found f ON f.place = 't3' AND NOT f.own AND f.row_id = r.id
  ORDER BY s.manager_user_id, s.team_id, s.member_email COLLATE "C" FOR UPDATE OF s),
d AS (DELETE FROM manager_team_member_assignments s USING m
      WHERE s.manager_user_id = m.manager_user_id AND s.team_id = m.team_id AND s.member_email = m.member_email
      RETURNING 1)
SELECT count(*) FROM d`},
		{"erase_t3", true, `WITH d AS (DELETE FROM manager_team_members r USING pac134_found f
  WHERE f.place = 't3' AND NOT f.own AND f.row_id = r.id
    AND ` + eraseMatch("r.member_email") + ` AND r.manager_user_id <> ALL(` + eraseBindAcc + `) RETURNING 1)
SELECT count(*) FROM d`},
		{"erase_t5", true, `WITH m AS MATERIALIZED (
  SELECT o.id FROM one_on_one_occurrences o JOIN pac134_found f ON f.place = 't5' AND NOT f.own AND f.row_id = o.id
  WHERE ` + eraseMatch("o.member_email") + ` AND o.manager_user_id <> ALL(` + eraseBindAcc + `)
  ORDER BY o.id FOR UPDATE OF o),
d AS (DELETE FROM one_on_one_occurrences o USING m WHERE o.id = m.id RETURNING 1)
SELECT count(*) FROM d`},
		{"erase_t6", true, `WITH m AS MATERIALIZED (
  SELECT c.id FROM team_analytics_cache c JOIN pac134_found f ON f.place = 't6' AND NOT f.own AND f.row_id = c.id
  WHERE ` + eraseMatch("c.member_email") + ` AND c.manager_user_id <> ALL(` + eraseBindAcc + `)
  ORDER BY c.id FOR UPDATE OF c),
d AS (DELETE FROM team_analytics_cache c USING m WHERE c.id = m.id RETURNING 1)
SELECT count(*) FROM d`},
		{"lock_links", false, `SELECT count(*) FROM (
  SELECT l.id FROM scheduling_links l
  WHERE l.id IN (SELECT f.link_id FROM pac134_found f WHERE f.place IN ('t7', 't9') AND NOT f.own)
  ORDER BY l.id FOR SHARE OF l) x`},
		{"erase_t7", true, `WITH m AS MATERIALIZED (
  SELECT h.id FROM scheduling_link_hosts h
  JOIN pac134_found f ON f.place = 't7' AND NOT f.own AND f.row_id = h.id
  JOIN scheduling_links l ON l.id = h.link_id
  WHERE h.invitee_email IS NOT NULL AND ` + eraseMatch("h.invitee_email") + ` AND l.owner_user_id <> ALL(` + eraseBindAcc + `)
  ORDER BY h.id FOR UPDATE OF h),
d AS (DELETE FROM scheduling_link_hosts h USING m WHERE h.id = m.id RETURNING 1)
SELECT count(*) FROM d`},
		{"erase_t9", true, `WITH m AS MATERIALIZED (
  SELECT b.id FROM bookings b
  JOIN pac134_found f ON f.place = 't9' AND NOT f.own AND f.row_id = b.id
  JOIN scheduling_links l ON l.id = b.link_id
  WHERE b.booker_erased_at IS NULL AND ` + eraseMatch("b.booker_email") + ` AND l.owner_user_id <> ALL(` + eraseBindAcc + `)
  ORDER BY b.id FOR NO KEY UPDATE OF b),
u AS (UPDATE bookings b SET booker_name = '', booker_email = '', notes = '', booker_erased_at = now()
      FROM m WHERE b.id = m.id RETURNING 1)
SELECT count(*) FROM u`},
		{"erase_t8", true, `WITH m AS MATERIALIZED (
  SELECT k.owner_user_id, k.idempotency_key FROM scheduling_link_create_keys k
  JOIN pac134_found f ON f.place = 't8' AND NOT f.own
                     AND f.owner_id = k.owner_user_id AND f.row_key = k.idempotency_key
  WHERE k.co_host_changes IS NOT NULL AND k.owner_user_id <> ALL(` + eraseBindAcc + `)
    AND ` + eraseJSONMatch("k.co_host_changes") + `
  ORDER BY k.owner_user_id, k.idempotency_key COLLATE "C" FOR UPDATE OF k),
u AS (UPDATE scheduling_link_create_keys k SET request_sha256 = NULL, co_host_changes = NULL
      FROM m WHERE k.owner_user_id = m.owner_user_id AND k.idempotency_key = m.idempotency_key RETURNING 1)
SELECT count(*) FROM u`},
	}

	// eraseErasedPlace is the place each erase_* statement's count belongs to.
	eraseErasedPlace = map[string]string{
		"erase_t1": "t1", "erase_t2": "t2", "erase_t4": "t4", "erase_t3": "t3",
		"erase_t5": "t5", "erase_t6": "t6", "erase_t7": "t7", "erase_t9": "t9", "erase_t8": "t8",
	}
)

const eraseAuditSQL = `INSERT INTO operator_audit (action, operator, reason, details) VALUES ($1, $2, $3, $4::jsonb)
RETURNING id`

// eraser runs one transaction's statements through the seams.
type eraser struct {
	ctx      context.Context
	tx       *sql.Tx
	deadline time.Time
}

// stmt runs one named statement whose result is one row (scanned into dest),
// or no row when dest is empty.
func (e *eraser) stmt(name, q string, dest []any, args ...any) error {
	fireEraseStatement(name, q, args)
	var err error
	if len(dest) == 0 {
		_, err = e.tx.ExecContext(e.ctx, q, args...)
	} else {
		err = e.tx.QueryRowContext(e.ctx, q, args...).Scan(dest...)
	}
	if err != nil {
		return e.fail(name, err)
	}
	if err := fireEraseAfterStatement(e.ctx, name); err != nil {
		return e.fail(name, err)
	}
	return nil
}

// rows runs one named statement with several result rows.
func (e *eraser) rows(name, q string, each func(*sql.Rows) error, args ...any) error {
	fireEraseStatement(name, q, args)
	rs, err := e.tx.QueryContext(e.ctx, q, args...)
	if err != nil {
		return e.fail(name, err)
	}
	defer rs.Close()
	for rs.Next() {
		if err := each(rs); err != nil {
			return e.fail(name, err)
		}
	}
	if err := rs.Err(); err != nil {
		return e.fail(name, err)
	}
	if err := fireEraseAfterStatement(e.ctx, name); err != nil {
		return e.fail(name, err)
	}
	return nil
}

// budget re-arms statement_timeout and lock_timeout to the time left until
// the deadline (stage-4 amendment N-1).
func (e *eraser) budget() error {
	left := time.Until(e.deadline)
	if left <= 0 {
		return e.fail("budget", context.DeadlineExceeded)
	}
	// The server's timers start when the next statement starts, a round
	// trip after this one: eraseBudgetMargin keeps them ending by the
	// deadline, not a round trip past it.
	ms := (left - eraseBudgetMargin).Milliseconds()
	if ms < 1 {
		ms = 1
	}
	b := fmt.Sprintf("%dms", ms)
	var a, c string
	return e.stmt("budget", eraseBudgetSQL, []any{&a, &c}, b)
}

// fail names the step of err and marks a run past its deadline, or stopped
// by the server's statement_timeout (which is only ever the bound), as
// ErrEraseBoundExceeded. Never err's text in anything the caller prints: the
// command classifies it (contract md §2.6).
func (e *eraser) fail(name string, err error) error {
	switch {
	case errors.Is(e.ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w: %w", name, ErrEraseBoundExceeded, err)
	case errors.Is(e.ctx.Err(), context.Canceled):
		// Interrupted (Ctrl-C/SIGTERM): the server answers the cancel
		// request with 57014; the cause is the cancellation.
		return fmt.Errorf("%s: %w: %w", name, context.Canceled, err)
	case e.ctx.Err() == nil && pgCode(err) == "57014":
		// statement_timeout fired server-side: it is only ever the bound or
		// the budget left until the deadline, so this is the bound too.
		return fmt.Errorf("%s: %w: %w", name, ErrEraseBoundExceeded, err)
	}
	return fmt.Errorf("%s: %w", name, err)
}

// pgCode is err's SQLSTATE, or "".
func pgCode(err error) string {
	type coder interface{ SQLState() string }
	var c coder
	if errors.As(err, &c) {
		return c.SQLState()
	}
	return ""
}

// accArray renders account ids as a uuid[] literal: ids only, never input.
func accArray(ids []uuid.UUID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.String()
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// EraseAddress runs one erasure, or one dry run (contract md §3.3). On an
// error nothing changed and no record was written. ErrEraseAccountHeld comes
// with a result naming the accounts (an erasure refused after the account
// check). A dry run that finds an account and was not acknowledged returns no
// error: it committed its record; the caller exits 3 on res.Accounts.
func EraseAddress(ctx context.Context, db *sql.DB, opts EraseAddressOptions) (*EraseAddressResult, error) {
	start := time.Now()
	if err := opts.Run.validate(); err != nil {
		return nil, err
	}
	t := opts.Address
	g, ok := ValidAddress(t)
	if t == "" || TrimAddressSpace(t) != t || !ok {
		return nil, fmt.Errorf("%w: not a valid address", ErrOperatorInput)
	}
	if opts.MaxDuration <= 0 {
		return nil, fmt.Errorf("%w: the duration bound must be positive", ErrOperatorInput)
	}
	deadline := start.Add(opts.MaxDuration)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	res := &EraseAddressResult{DryRun: opts.DryRun, Acknowledged: opts.AccountHeldAcknowledged}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("begin: %w: %w", ErrEraseBoundExceeded, err)
		}
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	e := &eraser{ctx: ctx, tx: tx, deadline: deadline}

	// [settings]
	bound := fmt.Sprintf("%dms", opts.MaxDuration.Milliseconds())
	idle := "1000ms"
	if eraseAfterStatementInstalled() {
		idle = "0" // a test holds the transaction open between statements (md §10)
	}
	var s1, s2, s3 string
	var unsafe bool
	if err := e.stmt("settings", eraseSettingsSQL, []any{&s1, &s2, &s3, &unsafe}, bound, idle); err != nil {
		return nil, err
	}
	if unsafe {
		return nil, ErrEraseStatementLogging
	}

	// [input_lower]
	var d string
	if err := e.stmt("input_lower", `SELECT lower($1::text)`, []any{&d}, t); err != nil {
		return nil, err
	}
	gd := []any{g, d}

	// [accounts]
	if err := e.rows("accounts", `SELECT u.id FROM users u WHERE `+eraseMatch("u.email")+` ORDER BY u.id`, func(rs *sql.Rows) error {
		var id uuid.UUID
		if err := rs.Scan(&id); err != nil {
			return err
		}
		res.Accounts = append(res.Accounts, id)
		return nil
	}, gd...); err != nil {
		return nil, err
	}
	if res.Accounts == nil {
		res.Accounts = []uuid.UUID{}
	}
	if len(res.Accounts) > 0 && !opts.AccountHeldAcknowledged && !opts.DryRun {
		return res, ErrEraseAccountHeld
	}
	gda := []any{g, d, accArray(res.Accounts)}

	// Discovery: no row lock.
	if err := e.stmt("found_table", eraseFoundTableSQL, nil); err != nil {
		return nil, err
	}
	for _, f := range eraseFindSQL {
		if err := e.stmt(f.name, f.sql, nil, gda...); err != nil {
			return nil, err
		}
	}
	if err := e.stmt("count_t10", eraseCountT10SQL, []any{&res.KeptOperatorRecords}, gd...); err != nil {
		return nil, err
	}
	found := map[string][2]int64{}
	if err := e.rows("counts", eraseCountsSQL, func(rs *sql.Rows) error {
		var place string
		var n, k int64
		if err := rs.Scan(&place, &n, &k); err != nil {
			return err
		}
		found[place] = [2]int64{n, k}
		return nil
	}); err != nil {
		return nil, err
	}
	res.Places = make([]ErasePlace, len(ErasePlaceNames))
	for i, name := range ErasePlaceNames {
		res.Places[i] = ErasePlace{Name: name}
	}
	place := func(key string) *ErasePlace {
		name := erasePlaceOfKey[key]
		for i := range res.Places {
			if res.Places[i].Name == name {
				return &res.Places[i]
			}
		}
		return nil
	}
	for key, c := range found {
		if p := place(key); p != nil {
			p.KeptAccountOwn = c[1]
			if opts.DryRun {
				p.Erased = c[0]
			}
		}
	}
	res.Counted = true

	var lockStart time.Time
	if !opts.DryRun {
		// [analyze_found]
		if err := e.stmt("analyze_found", eraseAnalyzeSQL, nil); err != nil {
			return nil, err
		}
		lockStart = time.Now()
		for _, st := range eraseLockPhaseSQL {
			if err := e.budget(); err != nil {
				return nil, err
			}
			var n int64
			var args []any
			if st.binds {
				args = gda
			}
			if err := e.stmt(st.name, st.sql, []any{&n}, args...); err != nil {
				return nil, err
			}
			if key, ok := eraseErasedPlace[st.name]; ok {
				place(key).Erased = n
			}
		}
		if err := e.budget(); err != nil {
			return nil, err
		}
	}

	// [audit]
	action := EraseAddressAction
	if opts.DryRun {
		action = EraseAddressDryRunAction
	}
	details, err := eraseAuditDetails(res, opts)
	if err != nil {
		return nil, err
	}
	var id uuid.UUID
	if err := e.stmt("audit", eraseAuditSQL, []any{&id}, action, opts.Run.Operator, opts.Run.Reason, details); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, e.fail("commit", err)
	}
	res.AuditID = id
	now := time.Now()
	if !lockStart.IsZero() {
		res.LockPhase = now.Sub(lockStart)
	}
	res.Total = now.Sub(start)
	return res, nil
}

// eraseAuditDetails is the details document of contract md §8: counts,
// account ids and the bound; never the address, a digest of it, a name or an
// erased row's id.
func eraseAuditDetails(res *EraseAddressResult, opts EraseAddressOptions) (string, error) {
	countKey := "erased"
	if opts.DryRun {
		countKey = "would_erase"
	}
	places := map[string]any{}
	var total, kept int64
	for _, p := range res.Places {
		places[p.Name] = map[string]int64{countKey: p.Erased, "kept_account_own": p.KeptAccountOwn}
		total += p.Erased
		kept += p.KeptAccountOwn
	}
	places["operator_audit"] = map[string]int64{"kept": res.KeptOperatorRecords}
	accounts := make([]string, len(res.Accounts))
	for i, a := range res.Accounts {
		accounts[i] = a.String()
	}
	doc := map[string]any{
		"schema":  1,
		"dry_run": opts.DryRun,
		"places":  places,
		"total": map[string]int64{
			countKey: total, "kept_account_own": kept, "kept_operator_records": res.KeptOperatorRecords,
		},
		"accounts":             accounts,
		"account_acknowledged": opts.AccountHeldAcknowledged,
		"max_duration_ms":      opts.MaxDuration.Milliseconds(),
	}
	b, err := json.Marshal(doc)
	return string(b), err
}
