package storage

import (
	"strconv"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
)

// PAC-84: the inventory of stored secrets (contracts/features/PAC-84.md
// section 2). This list is the registry the conversion, status, rotation,
// decrypt-all and reset iterate; a column joins the inventory only by being
// added here (a later contract).

// SecretColumn is one inventory column and the row identity its values are
// bound to (contract 3.3), in order.
type SecretColumn struct {
	Label    string // S1..S11
	Table    string
	Column   string
	Identity []string
	Nullable bool
}

// Name is "table.column".
func (c SecretColumn) Name() string { return c.Table + "." + c.Column }

// Inventory is the 18 secret columns of contract section 2.
var Inventory = []SecretColumn{
	{"S1", "oauth_tokens", "access_token", []string{"user_id", "calendar_id"}, false},
	{"S1", "oauth_tokens", "refresh_token", []string{"user_id", "calendar_id"}, false},
	{"S2", "settings", "microsoft_tokens", []string{"user_id"}, true},
	{"S3", "settings", "zoom_tokens", []string{"user_id"}, false},
	{"S6", "settings", "webcal_url", []string{"user_id"}, false},
	{"S8", "settings", "llm_api_key", []string{"user_id"}, false},
	{"S8", "settings", "llm_api_key_openai", []string{"user_id"}, false},
	{"S8", "settings", "llm_api_key_anthropic", []string{"user_id"}, false},
	{"S8", "settings", "llm_api_key_azure_openai", []string{"user_id"}, false},
	{"S8", "settings", "llm_api_key_bedrock", []string{"user_id"}, false},
	{"S8", "settings", "llm_api_key_vertex", []string{"user_id"}, false},
	{"S4", "personal_calendars", "credentials_json", []string{"id"}, false},
	{"S5", "personal_calendars", "url", []string{"id"}, false},
	{"S7", "workspace_connections", "access_token", []string{"user_id", "provider"}, false},
	{"S7", "workspace_connections", "bot_token", []string{"user_id", "provider"}, false},
	{"S9", "org_ai_configurations", "api_key", []string{"org_id"}, false},
	{"S10", "sso_providers", "oidc_client_secret", []string{"id"}, false},
	{"S11", "sso_providers", "saml_cert", []string{"id"}, false},
}

// Binding is the binding of a value of c in the row whose identity values
// are vals (canonical text, contract 3.3), in c.Identity's order.
func (c SecretColumn) Binding(vals ...string) secretbox.Binding {
	return bindAt(c.Table, c.Column, c.Identity, vals...)
}

func bindAt(table, column string, names []string, vals ...string) secretbox.Binding {
	row := make([]secretbox.Part, len(names))
	for i, n := range names {
		v := ""
		if i < len(vals) {
			v = vals[i]
		}
		row[i] = secretbox.Part{Name: n, Value: v}
	}
	return secretbox.Binding{Table: table, Column: column, Row: row}
}

// Bindings per table (contract 3.3). UUIDs render lower-case hyphenated
// (uuid.UUID.String), integers base-10.

func bindOAuthToken(column string, userID uuid.UUID, calendarID string) secretbox.Binding {
	return bindAt("oauth_tokens", column, []string{"user_id", "calendar_id"}, userID.String(), calendarID)
}

func bindSettings(column string, userID uuid.UUID) secretbox.Binding {
	return bindAt("settings", column, []string{"user_id"}, userID.String())
}

func bindWorkspace(column string, userID uuid.UUID, provider string) secretbox.Binding {
	return bindAt("workspace_connections", column, []string{"user_id", "provider"}, userID.String(), provider)
}

func bindOrgAI(orgID uuid.UUID) secretbox.Binding {
	return bindAt("org_ai_configurations", "api_key", []string{"org_id"}, orgID.String())
}

func bindSSO(column string, id uuid.UUID) secretbox.Binding {
	return bindAt("sso_providers", column, []string{"id"}, id.String())
}

func bindPersonal(column string, id int64) secretbox.Binding {
	return bindAt("personal_calendars", column, []string{"id"}, strconv.FormatInt(id, 10))
}

// BindOAuthToken is the binding of an oauth_tokens value (auth's token
// store writes and reads them).
func BindOAuthToken(column string, userID uuid.UUID, calendarID string) secretbox.Binding {
	return bindOAuthToken(column, userID, calendarID)
}

// BindSettings is the binding of a settings value.
func BindSettings(column string, userID uuid.UUID) secretbox.Binding {
	return bindSettings(column, userID)
}

// openSecret opens stored for b; a failure is reported with contract 9.2's
// line (user: the owning or acting user, uuid.Nil when not known) and
// returned.
func openSecret(b secretbox.Binding, stored string, user uuid.UUID) (string, error) {
	plain, err := secretbox.Open(b, stored)
	if err != nil {
		secretbox.ReportFailure(err, userText(user))
		return "", err
	}
	return plain, nil
}

// OpenSecret is openSecret for other packages (auth).
func OpenSecret(b secretbox.Binding, stored string, user uuid.UUID) (string, error) {
	return openSecret(b, stored, user)
}

func userText(u uuid.UUID) string {
	if u == uuid.Nil {
		return ""
	}
	return u.String()
}
