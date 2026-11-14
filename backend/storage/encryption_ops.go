package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// PAC-84 contract section 8: the batch passes (conversion, data-key
// rotation, decrypt-all), the lost-key reset, the status report, the schema
// downgrade and the operator audit row.

// identityType is the SQL type of each row-identity column (contract 3.3).
var identityType = map[string]map[string]string{
	"oauth_tokens":          {"user_id": "uuid", "calendar_id": "text"},
	"settings":              {"user_id": "uuid"},
	"workspace_connections": {"user_id": "uuid", "provider": "text"},
	"org_ai_configurations": {"org_id": "uuid"},
	"sso_providers":         {"id": "uuid"},
	"personal_calendars":    {"id": "bigint"},
}

// PassKind selects a batch pass.
type PassKind int

const (
	PassConvert PassKind = iota
	PassRotate
	PassDecrypt
)

// PassOptions configures a batch pass.
type PassOptions struct {
	Kind  PassKind
	Batch int // rows per batch, 1..1000 (default 100)
	// Pause between batches (the backend's automatic conversion: 50 ms).
	Pause time.Duration
	// Keyring opens values (rotate, decrypt) and, for convert and rotate,
	// seals with its active data key. Seal, when set, replaces the
	// keyring's seal (the backend passes secretbox.Seal, which applies the
	// lease).
	Keyring *secretbox.Keyring
	Seal    func(secretbox.Binding, string) (string, error)
	// Retire is the data key whose values a rotation re-seals.
	Retire int32
	// EmptyUndecryptable: decrypt-all gives a value it cannot decrypt the
	// lost-key removal (contract 8.2).
	EmptyUndecryptable bool
	// MaxSweeps bounds the passes over each column (default 3).
	MaxSweeps int
}

// Failure is one value a pass could not process.
type Failure struct {
	Column string // table.column
	Row    string // RowString
	DEK    string
	Reason string
}

// PassResult is what a pass did.
type PassResult struct {
	// Counts per "table.column" and verb (encrypted, reencrypted,
	// decrypted, emptied, skipped).
	Counts   map[string]map[string]int64
	Failures []Failure
	// LossesCleared counts recorded credential losses ended by a removal
	// (reset-lost-key, decrypt-all --empty-undecryptable; contract 8.4).
	LossesCleared int64
	// Remaining is set when the pass ended partial: values still to do.
	Remaining int64
	Partial   bool
}

func (r *PassResult) add(col, verb string, n int64) {
	if n == 0 {
		return
	}
	if r.Counts == nil {
		r.Counts = map[string]map[string]int64{}
	}
	if r.Counts[col] == nil {
		r.Counts[col] = map[string]int64{}
	}
	r.Counts[col][verb] += n
}

// Total sums a verb over every column.
func (r *PassResult) Total(verb string) int64 {
	var n int64
	for _, m := range r.Counts {
		n += m[verb]
	}
	return n
}

// sealedPrefix selects values starting with the marker.
const sealedPrefix = `left(%s, 6) = 'pdenc:'`

// DamagedSQL is secretbox.Classify's damaged-marker rule in SQL (review #5):
// the first six characters differ from "pdenc:" in exactly one place and the
// rest is "<digits>:<digits>:<base64url>". Such a value is neither plain
// nor convertible: it is counted "malformed", is never converted, and blocks
// decrypt-all's key removal and the downgrade like a sealed value.
func DamagedSQL(c string) string {
	return fmt.Sprintf(`(left(%[1]s, 6) <> 'pdenc:' AND octet_length(%[1]s) >= 11 AND substr(%[1]s, 7) ~ '^[0-9]+:[0-9]+:[A-Za-z0-9_-]+$' AND ((substr(%[1]s, 1, 1) <> 'p')::int + (substr(%[1]s, 2, 1) <> 'd')::int + (substr(%[1]s, 3, 1) <> 'e')::int + (substr(%[1]s, 4, 1) <> 'n')::int + (substr(%[1]s, 5, 1) <> 'c')::int + (substr(%[1]s, 6, 1) <> ':')::int) = 1)`, c)
}

// sealedOrDamaged selects every value Go classifies KindSealed.
func sealedOrDamaged(c string) string {
	return "(" + fmt.Sprintf(sealedPrefix, c) + " OR " + DamagedSQL(c) + ")"
}

func (c SecretColumn) selector(kind PassKind) string {
	switch kind {
	case PassConvert:
		return fmt.Sprintf(`%[1]s IS NOT NULL AND %[1]s <> '' AND left(%[1]s, 6) <> 'pdenc:' AND NOT %[2]s`, c.Column, DamagedSQL(c.Column))
	case PassRotate:
		return fmt.Sprintf(sealedPrefix+` AND split_part(%[1]s, ':', 3) = $%d`, c.Column, len(c.Identity)+2)
	default:
		return sealedOrDamaged(c.Column)
	}
}

func (c SecretColumn) identityEq(start int) string {
	parts := make([]string, len(c.Identity))
	for i, n := range c.Identity {
		parts[i] = fmt.Sprintf("%s = $%d::%s", n, start+i, identityType[c.Table][n])
	}
	return strings.Join(parts, " AND ")
}

// batchQuery selects the next batch after the keyset (identity) cursor.
func (c SecretColumn) batchQuery(kind PassKind, first bool) string {
	ids := strings.Join(c.Identity, ", ")
	casts := make([]string, len(c.Identity))
	params := make([]string, len(c.Identity))
	for i, n := range c.Identity {
		casts[i] = n + "::text"
		params[i] = fmt.Sprintf("$%d::%s", i+1, identityType[c.Table][n])
	}
	where := c.selector(kind)
	if !first {
		where += fmt.Sprintf(" AND (%s) > (%s)", ids, strings.Join(params, ", "))
	} else {
		// Keep the parameter numbering the same: the cursor params are
		// still sent, and compared to nothing.
		where += fmt.Sprintf(" AND (%s) IS NOT NULL", strings.Join(params, ", "))
	}
	return fmt.Sprintf(`SELECT %s, %s, xmin::text FROM %s WHERE %s ORDER BY %s LIMIT $%d`,
		strings.Join(casts, ", "), c.Column, c.Table, where, ids, len(c.Identity)+1)
}

// passHook, when set by a test, runs between a batch's SELECT and its
// UPDATEs (AC-13, AC-14).
var passHook func(col SecretColumn, rows int)

type passRow struct {
	ids   []string
	value string
	xmin  string
}

// RunPass runs a batch pass over every inventory column (contract 8.3).
// Each batch is one READ COMMITTED transaction: its SELECT and the
// compare-and-set UPDATEs on xmin; only the secret column is in the SET; no
// plaintext is sent by convert or rotate (AC-19c).
func RunPass(ctx context.Context, db *sql.DB, opt PassOptions) (*PassResult, error) {
	if opt.Batch <= 0 {
		opt.Batch = 100
	}
	if opt.MaxSweeps <= 0 {
		opt.MaxSweeps = 3
	}
	if opt.Seal == nil && opt.Keyring != nil {
		k := opt.Keyring
		opt.Seal = k.Seal
	}
	res := &PassResult{}
	for _, col := range Inventory {
		if err := runColumn(ctx, db, col, opt, res); err != nil {
			return res, err
		}
	}
	return res, nil
}

func runColumn(ctx context.Context, db *sql.DB, col SecretColumn, opt PassOptions, res *PassResult) error {
	for sweep := 1; ; sweep++ {
		updated, concurrent, err := sweepColumn(ctx, db, col, opt, res)
		if err != nil {
			return err
		}
		if updated == 0 && concurrent == 0 {
			return nil
		}
		if sweep >= opt.MaxSweeps {
			n, err := countRemaining(ctx, db, col, opt)
			if err != nil {
				return err
			}
			if n > 0 {
				res.Partial = true
				res.Remaining += n
			}
			return nil
		}
	}
}

func countRemaining(ctx context.Context, db *sql.DB, col SecretColumn, opt PassOptions) (int64, error) {
	q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s`, col.Table, col.selector(opt.Kind))
	q = strings.ReplaceAll(q, fmt.Sprintf("$%d", len(col.Identity)+2), "$1")
	var n int64
	var err error
	if opt.Kind == PassRotate {
		err = db.QueryRowContext(ctx, q, strconv.Itoa(int(opt.Retire))).Scan(&n)
	} else {
		err = db.QueryRowContext(ctx, q).Scan(&n)
	}
	return n, err
}

// sweepColumn goes once over col; it returns the rows updated and the rows
// skipped because they changed in between (compare-and-set lost).
func sweepColumn(ctx context.Context, db *sql.DB, col SecretColumn, opt PassOptions, res *PassResult) (updated, concurrent int64, err error) {
	cursor := make([]string, len(col.Identity))
	for i, n := range col.Identity {
		switch identityType[col.Table][n] {
		case "uuid":
			cursor[i] = "00000000-0000-0000-0000-000000000000"
		case "bigint":
			cursor[i] = "0"
		default:
			cursor[i] = ""
		}
	}
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return updated, concurrent, err
		}
		rows, u, c, err := runBatch(ctx, db, col, opt, res, cursor, first)
		updated += u
		concurrent += c
		if err != nil {
			return updated, concurrent, err
		}
		if len(rows) < opt.Batch {
			return updated, concurrent, nil
		}
		cursor = rows[len(rows)-1].ids
		first = false
		if opt.Pause > 0 {
			select {
			case <-ctx.Done():
				return updated, concurrent, ctx.Err()
			case <-time.After(opt.Pause):
			}
		}
	}
}

func runBatch(ctx context.Context, db *sql.DB, col SecretColumn, opt PassOptions, res *PassResult, cursor []string, first bool) ([]passRow, int64, int64, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	args := make([]any, 0, len(cursor)+2)
	for _, v := range cursor {
		args = append(args, v)
	}
	args = append(args, opt.Batch)
	if opt.Kind == PassRotate {
		args = append(args, strconv.Itoa(int(opt.Retire)))
	}
	q := col.batchQuery(opt.Kind, first)
	rs, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%s: select batch: %w", col.Name(), err)
	}
	var rows []passRow
	for rs.Next() {
		r := passRow{ids: make([]string, len(col.Identity))}
		dest := make([]any, 0, len(col.Identity)+2)
		for i := range r.ids {
			dest = append(dest, &r.ids[i])
		}
		var v sql.NullString
		dest = append(dest, &v, &r.xmin)
		if err := rs.Scan(dest...); err != nil {
			rs.Close()
			return nil, 0, 0, err
		}
		r.value = v.String
		rows = append(rows, r)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, 0, 0, err
	}
	if passHook != nil {
		passHook(col, len(rows))
	}
	update := fmt.Sprintf(`UPDATE %s SET %s = $1 WHERE %s AND xmin::text = $%d`,
		col.Table, col.Column, col.identityEq(2), len(col.Identity)+2)
	var updated, concurrent int64
	for _, r := range rows {
		b := col.Binding(r.ids...)
		var newValue string
		verb := ""
		switch opt.Kind {
		case PassConvert:
			if secretbox.Classify(r.value) != secretbox.KindPlain {
				res.fail(Failure{Column: col.Name(), Row: b.RowString(), DEK: "-", Reason: string(secretbox.ReasonMalformed)})
				continue
			}
			if newValue, err = opt.Seal(b, r.value); err != nil {
				return rows, updated, concurrent, fmt.Errorf("%s: seal: %w", col.Name(), err)
			}
			verb = "encrypted"
		case PassRotate, PassDecrypt:
			plain, oerr := opt.Keyring.Open(b, r.value)
			if oerr != nil {
				f := Failure{Column: col.Name(), Row: b.RowString(), DEK: "-", Reason: "malformed"}
				if de, ok := secretbox.AsDecryptError(oerr); ok {
					f.Reason = string(de.Reason)
					if de.DEK > 0 {
						f.DEK = strconv.Itoa(int(de.DEK))
					}
				}
				if opt.Kind == PassDecrypt && opt.EmptyUndecryptable {
					n, err := removeSealedValues(ctx, tx, col, col.identityEq(1), &res.LossesCleared, argsOf(r.ids)...)
					if err != nil {
						return rows, updated, concurrent, err
					}
					res.add(col.Name(), "emptied", n)
					continue
				}
				res.fail(f)
				continue
			}
			if opt.Kind == PassDecrypt {
				newValue, verb = plain, "decrypted" // the only pass that writes plaintext (AC-19c)
			} else {
				if newValue, err = opt.Seal(b, plain); err != nil {
					return rows, updated, concurrent, fmt.Errorf("%s: seal: %w", col.Name(), err)
				}
				verb = "reencrypted"
			}
		}
		uargs := append([]any{newValue}, argsOf(r.ids)...)
		uargs = append(uargs, r.xmin)
		out, err := tx.ExecContext(ctx, update, uargs...)
		if err != nil {
			return rows, updated, concurrent, fmt.Errorf("%s: update: %w", col.Name(), err)
		}
		if n, _ := out.RowsAffected(); n == 1 {
			updated++
			res.add(col.Name(), verb, 1)
		} else {
			concurrent++
		}
	}
	if err := tx.Commit(); err != nil {
		return rows, 0, 0, err
	}
	return rows, updated, concurrent, nil
}

func argsOf(ids []string) []any {
	out := make([]any, len(ids))
	for i, v := range ids {
		out[i] = v
	}
	return out
}

// removeSealedValues gives the sealed values of col in the rows matching
// where (args from $1) the owner's-removal end state (AC-23), without any
// provider call, and returns the rows changed. Plain values are untouched.
func removeSealedValues(ctx context.Context, tx *sql.Tx, col SecretColumn, where string, losses *int64, args ...any) (int64, error) {
	sealed := sealedOrDamaged(col.Column)
	exec := func(q string) (int64, error) {
		r, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return 0, fmt.Errorf("%s: reset: %w", col.Name(), err)
		}
		return r.RowsAffected()
	}
	clearLosses := func(q string) error {
		n, err := exec(q)
		if err == nil && losses != nil {
			*losses += n
		}
		return err
	}
	cond := sealed
	if where != "" {
		cond = where + " AND " + sealed
	}
	switch col.Table {
	case "oauth_tokens":
		// An emptied row would still read as connected: delete it, and end
		// the recorded loss of the removed credential.
		if err := clearLosses(`DELETE FROM credential_losses cl USING oauth_tokens t WHERE ` + qualify(cond, "t") + ` AND cl.user_id = t.user_id AND cl.provider = 'google'`); err != nil {
			return 0, err
		}
		return exec(`DELETE FROM oauth_tokens WHERE ` + cond)
	case "workspace_connections":
		return exec(`DELETE FROM workspace_connections WHERE ` + cond)
	case "settings":
		switch col.Column {
		case "microsoft_tokens":
			if err := clearLosses(`DELETE FROM credential_losses cl USING settings t WHERE ` + qualify(cond, "t") + ` AND cl.user_id = t.user_id AND cl.provider = 'outlook'`); err != nil {
				return 0, err
			}
			return exec(`UPDATE settings SET microsoft_tokens = '',
				conferencing_provider = CASE WHEN conferencing_provider = 'teams' THEN 'meet' ELSE conferencing_provider END
				WHERE ` + cond)
		case "zoom_tokens":
			if err := clearLosses(`DELETE FROM credential_losses cl USING settings t WHERE ` + qualify(cond, "t") + ` AND cl.user_id = t.user_id AND cl.provider = 'zoom'`); err != nil {
				return 0, err
			}
			return exec(`UPDATE settings SET zoom_tokens = '', conferencing_provider = 'meet' WHERE ` + cond)
		}
		return exec(fmt.Sprintf(`UPDATE settings SET %s = '' WHERE %s`, col.Column, cond))
	case "personal_calendars":
		return exec(fmt.Sprintf(`UPDATE personal_calendars SET %s = '', enabled = FALSE WHERE %s`, col.Column, cond))
	default: // org_ai_configurations, sso_providers
		return exec(fmt.Sprintf(`UPDATE %s SET %s = '' WHERE %s`, col.Table, col.Column, cond))
	}
}

var bareColumn = regexp.MustCompile(`\b(user_id|calendar_id|provider|org_id|id|access_token|refresh_token|microsoft_tokens|zoom_tokens)\b`)

// qualify prefixes the bare column names of cond with alias.
func qualify(cond, alias string) string {
	return bareColumn.ReplaceAllStringFunc(cond, func(s string) string { return alias + "." + s })
}

// ResetLostKey is reset-lost-key's work (AC-23) in one transaction: every
// sealed value gets its owner's-removal end state, every data key is
// deleted, and one new active data key is created under cfg.Active.
func ResetLostKey(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig) (*PassResult, []int32, int32, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := keySetupLock(ctx, tx); err != nil {
		return nil, nil, 0, err
	}
	res := &PassResult{}
	for _, col := range Inventory {
		n, err := removeSealedValues(ctx, tx, col, "", &res.LossesCleared)
		if err != nil {
			return nil, nil, 0, err
		}
		verb := "emptied"
		switch col.Table {
		case "oauth_tokens", "workspace_connections":
			verb = "deleted"
		case "personal_calendars":
			verb = "disabled"
		}
		res.add(col.Name(), verb, n)
	}
	removed, err := deleteDataKeys(ctx, tx)
	if err != nil {
		return nil, nil, 0, err
	}
	w, err := insertDataKey(ctx, tx, cfg.Active)
	if err != nil {
		return nil, nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, 0, err
	}
	return res, removed, w.ID, nil
}

func deleteDataKeys(ctx context.Context, q DBTXQuery) ([]int32, error) {
	rows, err := q.QueryContext(ctx, `DELETE FROM encryption_data_keys RETURNING id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int32, 0)
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, rows.Err()
}

// DeleteAllDataKeysIfUnused deletes every data key when no sealed value
// remains in the inventory (decrypt-all's last step). It reports the ids
// removed, or ok false (nothing deleted) when a sealed value remains.
func DeleteAllDataKeysIfUnused(ctx context.Context, db *sql.DB) ([]int32, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := keySetupLock(ctx, tx); err != nil {
		return nil, false, err
	}
	n, err := countSealed(ctx, tx)
	if err != nil {
		return nil, false, err
	}
	if n > 0 {
		return nil, false, nil
	}
	ids, err := deleteDataKeys(ctx, tx)
	if err != nil {
		return nil, false, err
	}
	return ids, true, tx.Commit()
}

// countSealed counts sealed values over the inventory.
func countSealed(ctx context.Context, q DBTXQuery) (int64, error) {
	var total int64
	for _, col := range Inventory {
		var n int64
		if err := q.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s`, col.Table, sealedOrDamaged(col.Column))).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// ColumnStatus is one inventory column in the status report.
type ColumnStatus struct {
	Name                    string
	Empty, Plain, Encrypted int64
	// Malformed: damaged-marker values (review #5), neither plain nor
	// decryptable.
	Malformed int64
	ByDEK     map[string]int64
}

// DataKeyStatus is one data key in the status report.
type DataKeyStatus struct {
	ID          int32
	State       string
	KEKID       string
	CreatedAt   time.Time
	WrappedAt   time.Time
	RetiredAt   *time.Time
	SealedCount int64
	Values      int64
}

// RotateDue is status's advisory flag (contract 4.1): an active data key
// older than 365 days or past 2^29 seals.
func (d DataKeyStatus) RotateDue(now time.Time) bool {
	return d.State == "active" && (now.Sub(d.CreatedAt) > 365*24*time.Hour || d.SealedCount >= SealAdvisory)
}

// EncryptionStatus is the status report (AC-20).
type EncryptionStatus struct {
	Columns    []ColumnStatus
	DataKeys   []DataKeyStatus
	Heartbeats []Heartbeat
}

// PlainTotal counts plain values over the inventory.
func (s *EncryptionStatus) PlainTotal() int64 {
	var n int64
	for _, c := range s.Columns {
		n += c.Plain
	}
	return n
}

// ReadEncryptionStatus builds the status report. Read-only; no key needed.
func ReadEncryptionStatus(ctx context.Context, db *sql.DB) (*EncryptionStatus, error) {
	st := &EncryptionStatus{}
	perDEK := map[string]int64{}
	for _, col := range Inventory {
		cs := ColumnStatus{Name: col.Name(), ByDEK: map[string]int64{}}
		c := col.Column
		err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT
			count(*) FILTER (WHERE %[1]s IS NULL OR %[1]s = ''),
			count(*) FILTER (WHERE %[1]s <> '' AND left(%[1]s, 6) <> 'pdenc:' AND NOT %[3]s),
			count(*) FILTER (WHERE %[3]s),
			count(*) FILTER (WHERE left(%[1]s, 6) = 'pdenc:')
			FROM %[2]s`, c, col.Table, DamagedSQL(c))).Scan(&cs.Empty, &cs.Plain, &cs.Malformed, &cs.Encrypted)
		if err != nil {
			return nil, err
		}
		rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT split_part(%[1]s, ':', 3), count(*) FROM %[2]s
			WHERE left(%[1]s, 6) = 'pdenc:' GROUP BY 1 ORDER BY 1`, c, col.Table))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var dek string
			var n int64
			if err := rows.Scan(&dek, &n); err != nil {
				rows.Close()
				return nil, err
			}
			cs.ByDEK[dek] = n
			perDEK[dek] += n
		}
		rows.Close()
		st.Columns = append(st.Columns, cs)
	}
	rows, err := db.QueryContext(ctx, `SELECT id, state, kek_id, created_at, wrapped_at, retired_at, sealed_count
		FROM encryption_data_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d DataKeyStatus
		var retired sql.NullTime
		if err := rows.Scan(&d.ID, &d.State, &d.KEKID, &d.CreatedAt, &d.WrappedAt, &retired, &d.SealedCount); err != nil {
			rows.Close()
			return nil, err
		}
		if retired.Valid {
			t := retired.Time
			d.RetiredAt = &t
		}
		d.Values = perDEK[strconv.Itoa(int(d.ID))]
		st.DataKeys = append(st.DataKeys, d)
	}
	rows.Close()
	if st.Heartbeats, err = LiveHeartbeats(ctx, db); err != nil {
		return nil, err
	}
	return st, nil
}

// CountValuesUnder counts sealed values under data key dek.
func CountValuesUnder(ctx context.Context, db *sql.DB, dek int32) (int64, error) {
	var total int64
	for _, col := range Inventory {
		var n int64
		q := fmt.Sprintf(`SELECT count(*) FROM %[2]s WHERE left(%[1]s, 6) = 'pdenc:' AND split_part(%[1]s, ':', 3) = $1`, col.Column, col.Table)
		if err := db.QueryRowContext(ctx, q, strconv.Itoa(int(dek))).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// AuditDetails is operator_audit.details for an encryption action (8.4).
type AuditDetails struct {
	Outcome       string                      `json:"outcome"`
	KeyIDs        *AuditKeyIDs                `json:"keyIds,omitempty"`
	DataKeys      *AuditDataKeys              `json:"dataKeys,omitempty"`
	Counts        map[string]map[string]int64 `json:"counts,omitempty"`
	LossesCleared *int64                      `json:"lossesCleared,omitempty"`
	StartedAt     string                      `json:"startedAt"`
	FinishedAt    string                      `json:"finishedAt"`
}

// AuditKeyIDs are the configured key ids (never key bytes).
type AuditKeyIDs struct {
	Active   string   `json:"active"`
	Previous []string `json:"previous"`
}

// AuditDataKeys lists the data keys an action created, retired, removed.
type AuditDataKeys struct {
	Created []int32 `json:"created,omitempty"`
	Retired []int32 `json:"retired,omitempty"`
	Removed []int32 `json:"removed,omitempty"`
}

// WriteEncryptionAudit writes one operator_audit row for an encryption
// action (account and identity columns NULL).
func WriteEncryptionAudit(ctx context.Context, db DBTX, action, operator, reason string, d AuditDetails) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO operator_audit (action, operator, reason, details) VALUES ($1, $2, $3, $4)`,
		action, operator, reason, string(raw))
	return err
}

// The encryption audit actions (contract 7.1).
const (
	AuditEncryptionConvert   = "encryption_convert"
	AuditEncryptionRotate    = "encryption_rotate_data_key"
	AuditEncryptionDecrypt   = "encryption_decrypt_all"
	AuditEncryptionResetLost = "encryption_reset_lost_key"
	AuditEncryptionDowngrade = "encryption_downgrade_schema"
)

// EncryptionMigrationVersion is the number of this work's migration pair,
// read from the embedded file name (it may be renumbered at merge time).
func EncryptionMigrationVersion() (uint, error) {
	return embeddedMigrationVersion("_encryption_at_rest.up.sql")
}

// embeddedMigrationVersion is the number of the embedded up migration whose
// file name ends with suffix.
func embeddedMigrationVersion(suffix string) (uint, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, suffix) {
			v, err := strconv.ParseUint(strings.SplitN(n, "_", 2)[0], 10, 32)
			return uint(v), err
		}
	}
	return 0, fmt.Errorf("migration *%s not embedded", suffix)
}

// ErrDowngradeRefused: something is still encrypted (contract 7.2).
var ErrDowngradeRefused = errors.New(`PAC-84 down refused: encrypted values or data keys remain; run "paceday-operator encryption decrypt-all" first (docs/runbook.md, "Encryption key - downgrade")`)

// ErrDowngradeVersion: the database is not at this work's version.
type ErrDowngradeVersion struct {
	Current, Want uint
	Dirty         bool
}

func (e *ErrDowngradeVersion) Error() string {
	if e.Dirty {
		return fmt.Sprintf("the database is at version %d marked dirty; fix it before downgrading", e.Current)
	}
	return fmt.Sprintf("the database is at version %d, not %d: roll back the later release first", e.Current, e.Want)
}

// DowngradeCheck is the Go pre-check of the down step (contract 7.2, K-1).
func DowngradeCheck(ctx context.Context, db *sql.DB) error {
	var keys int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM encryption_data_keys`).Scan(&keys); err != nil {
		return err
	}
	n, err := countSealed(ctx, db)
	if err != nil {
		return err
	}
	if keys > 0 || n > 0 {
		return ErrDowngradeRefused
	}
	return nil
}

// DowngradeSchema runs this work's down step with the embedded migrations,
// only from this work's version, after DowngradeCheck. If the step fails,
// the version is forced back to this work's (the schema is unchanged: the
// step is one transaction) so the database is never left dirty.
func DowngradeSchema(ctx context.Context, db *sql.DB) (from, to uint, err error) {
	want, err := EncryptionMigrationVersion()
	if err != nil {
		return 0, 0, err
	}
	if err := DowngradeCheck(ctx, db); err != nil {
		return 0, 0, err
	}
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return 0, 0, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, 0, err
	}
	driver, err := postgres.WithConnection(ctx, conn, &postgres.Config{})
	if err != nil {
		_ = conn.Close()
		return 0, 0, err
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		_ = driver.Close()
		return 0, 0, err
	}
	defer m.Close()
	cur, dirty, err := m.Version()
	if err != nil {
		return 0, 0, err
	}
	if dirty || cur != want {
		return cur, 0, &ErrDowngradeVersion{Current: cur, Want: want, Dirty: dirty}
	}
	if err := m.Steps(-1); err != nil {
		if ferr := m.Force(int(want)); ferr != nil {
			return cur, 0, fmt.Errorf("down step failed: %v; restoring version %d also failed: %v", err, want, ferr)
		}
		return cur, 0, fmt.Errorf("down step failed (version %d restored, schema unchanged): %w", want, err)
	}
	to, _, err = m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return cur, 0, nil
	}
	return cur, to, err
}

// MalformedTotal counts damaged-marker values over the inventory.
func (s *EncryptionStatus) MalformedTotal() int64 {
	var n int64
	for _, c := range s.Columns {
		n += c.Malformed
	}
	return n
}

// fail records a value the pass could not process, once per row and column
// however many sweeps see it.
func (r *PassResult) fail(f Failure) {
	for _, g := range r.Failures {
		if g.Column == f.Column && g.Row == f.Row {
			return
		}
	}
	r.add(f.Column, "skipped", 1)
	r.Failures = append(r.Failures, f)
}
