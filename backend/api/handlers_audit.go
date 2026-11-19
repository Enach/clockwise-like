package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// auditLimitMessage is the one body every refusal of GET /api/audit carries
// (PAC-53, contract listAuditEntries 400). It never contains the submitted
// text; its range is the parameter's `minimum`/`maximum`.
const auditLimitMessage = "limit must be an integer from 1 to 500"

// auditLimit reads `limit` from the raw query by the PAC-53 rule (C-1..C-10,
// contracts/openapi/paths/calendar.yaml, listAuditEntries). ok is false for
// every request the rule refuses.
func auditLimit(rawQuery string) (limit int, ok bool) {
	// C-9 first, on the raw query: an unencoded ';' or an undecodable '%'
	// anywhere, under any name. r.URL.Query() would drop such a pair
	// silently, and the dropped pair may have been `limit` (`%6Cimit`).
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return 0, false
	}
	// `limit` by its decoded, case-sensitive name (C-10: `LIMIT` is ignored).
	values, present := q["limit"]
	if !present {
		return storage.AuditDefaultLimit, true // C-1
	}
	if len(values) != 1 {
		return 0, false // C-8, identical values included
	}
	// C-2 (empty or bare), C-3 (not base-10: ParseInt with base 10 refuses
	// spaces, decimals, exponents, 0x and underscores) and the overflowing
	// part of C-5/C-7 (ErrRange); C-4: one optional sign, leading zeros.
	n, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || n < 1 || n > storage.AuditMaxLimit { // C-5, C-7
		return 0, false
	}
	return int(n), true // C-6
}

func auditHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := userIDFromCtx(r.Context())
		if userID == uuid.Nil {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		limit, ok := auditLimit(r.URL.RawQuery)
		if !ok {
			writeError(w, auditLimitMessage, http.StatusBadRequest)
			return
		}
		entries, err := storage.ListAuditLog(db, userID, limit)
		if err != nil {
			writeServerError(w, r, "audit", err, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}
}
