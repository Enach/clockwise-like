package storage

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// PAC-71 identities and address standing (spec docs/specs/PAC-71.md §3
// "Identities", "Evidence and address standing"; migration 031).

// Identity providers, the `provider` column of user_identities.
const (
	IdentityGoogle    = "google"
	IdentityMicrosoft = "microsoft"
	IdentityOIDC      = "oidc"
)

// Address standings, users.address_standing and CurrentUserResponse.addressStanding.
const (
	AddressDomainConfirmed = "domain_confirmed"
	AddressUnconfirmed     = "unconfirmed"
)

// Identity is an authority together with the subject it issues. Identifiers
// are only ever compared together with their authority (spec §3):
//
//	Google:          {google, "", userinfo id}
//	Microsoft work:  {microsoft, lower(tenant id), lower(object id)}
//	Organisation SSO {oidc, verified issuer, sub}
type Identity struct {
	Provider string
	Tenant   string
	Subject  string
}

// String is the identity's log form, provider:tenant:subject.
func (i Identity) String() string {
	if i.Tenant == "" {
		return i.Provider + ":" + i.Subject
	}
	return i.Provider + ":" + i.Tenant + ":" + i.Subject
}

// Evidence is what a sign-in carries for the address it reports (spec §3
// evidence table).
type Evidence int

const (
	EvidenceNone Evidence = iota
	EvidenceMailbox
	EvidenceAuthoritative
)

func (e Evidence) String() string {
	switch e {
	case EvidenceAuthoritative:
		return "authoritative"
	case EvidenceMailbox:
		return "mailbox"
	default:
		return "none"
	}
}

// standing is the address standing a binding made with evidence e gives.
func (e Evidence) standing() string {
	if e == EvidenceAuthoritative {
		return AddressDomainConfirmed
	}
	return AddressUnconfirmed
}

// ParseEvidence reads "authoritative" or "mailbox".
func ParseEvidence(s string) (Evidence, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "authoritative":
		return EvidenceAuthoritative, true
	case "mailbox":
		return EvidenceMailbox, true
	}
	return EvidenceNone, false
}

// GetBoundIdentity returns userID's bound identity, or nil when the account
// is unbound.
func GetBoundIdentity(q Querier, userID uuid.UUID) (*Identity, error) {
	var id Identity
	err := q.QueryRow(`SELECT provider, tenant, subject FROM user_identities WHERE user_id = $1`, userID).
		Scan(&id.Provider, &id.Tenant, &id.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// AccountForIdentity returns the account bound to id, if any.
func AccountForIdentity(q Querier, id Identity) (uuid.UUID, bool, error) {
	var uid uuid.UUID
	err := q.QueryRow(`SELECT user_id FROM user_identities WHERE provider = $1 AND tenant = $2 AND subject = $3`,
		id.Provider, id.Tenant, id.Subject).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return uid, true, nil
}

// Carrier is an account that carries an address, in any letter case.
type Carrier struct {
	ID         uuid.UUID
	Email      string
	Standing   string
	ProviderID string
	Bound      bool
}

// CarriersOf returns every account whose address equals email ignoring
// letter case (spec D-17), in a stable order.
func CarriersOf(q Querier, email string) ([]Carrier, error) {
	rows, err := q.Query(`
		SELECT u.id, u.email, u.address_standing, u.provider_id, (i.user_id IS NOT NULL)
		FROM users u LEFT JOIN user_identities i ON i.user_id = u.id
		WHERE lower(u.email) = lower($1)
		ORDER BY u.created_at, u.id`, strings.TrimSpace(email))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Carrier, 0, 1)
	for rows.Next() {
		var c Carrier
		if err := rows.Scan(&c.ID, &c.Email, &c.Standing, &c.ProviderID, &c.Bound); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConfirmedHolder resolves an address at a GRANTING site (spec §3 "What
// standing allows"): the one account that carries it, compared ignoring
// letter case, when that account holds it domain-confirmed. An unconfirmed
// carrier, a case collision (two carriers) or no carrier resolves to nobody.
func ConfirmedHolder(q Querier, email string) (uuid.UUID, bool, error) {
	carriers, err := CarriersOf(q, email)
	if err != nil {
		return uuid.Nil, false, err
	}
	if len(carriers) != 1 || carriers[0].Standing != AddressDomainConfirmed {
		return uuid.Nil, false, nil
	}
	return carriers[0].ID, true, nil
}

// CarrierIDs resolves addresses at a REVOKING or KEEPING site: every account
// that carries any of them, at any standing, ignoring letter case.
func CarrierIDs(q Querier, emails []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(emails))
	seen := map[uuid.UUID]bool{}
	for _, e := range emails {
		if strings.TrimSpace(e) == "" {
			continue
		}
		carriers, err := CarriersOf(q, e)
		if err != nil {
			return nil, err
		}
		for _, c := range carriers {
			if !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, c.ID)
			}
		}
	}
	return out, nil
}

// HoldsConfirmed reports whether userID's account carries email (ignoring
// letter case) domain-confirmed, and is the only account that carries it.
func HoldsConfirmed(q Querier, userID uuid.UUID, email string) (bool, error) {
	id, ok, err := ConfirmedHolder(q, email)
	if err != nil || !ok {
		return false, err
	}
	return id == userID, nil
}

// AddressStandingOf returns userID's address standing.
func AddressStandingOf(q Querier, userID uuid.UUID) (string, error) {
	var s string
	err := q.QueryRow(`SELECT address_standing FROM users WHERE id = $1`, userID).Scan(&s)
	return s, err
}
