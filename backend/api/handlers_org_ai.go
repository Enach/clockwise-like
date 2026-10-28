package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-73 Part 2 (spec docs/specs/NEW-llm-bring-your-own-part2.md §3.2-§3.4;
// contract revision 3.1): the organisation's AI configuration, read by every
// member, changed by a proven administrator naming the version they were
// shown, and confirmed by each member. The three operations are served
// through the generated strict wrapper (factory §4; MIGRATION.md).

// orgAIStore is every storage access of the three operations. It exists so a
// test can apply another administrator's change between a request's checks
// and its write (P2-AC-40) or fail the write (P2-AC-5).
type orgAIStore interface {
	read(ctx context.Context, orgID uuid.UUID) (storage.OrgAIConfig, error)
	write(ctx context.Context, w storage.OrgAIWrite) (storage.OrgAIWriteOutcome, error)
	confirm(ctx context.Context, userID, orgID uuid.UUID, version int64, now time.Time) (storage.OrgAIConfirmStatus, storage.OrgAIConfig, error)
}

type dbOrgAIStore struct{ db *sql.DB }

func (s dbOrgAIStore) read(ctx context.Context, orgID uuid.UUID) (storage.OrgAIConfig, error) {
	return storage.GetOrgAIConfig(ctx, s.db, orgID)
}

func (s dbOrgAIStore) write(ctx context.Context, w storage.OrgAIWrite) (storage.OrgAIWriteOutcome, error) {
	return storage.WriteOrgAIConfig(ctx, s.db, w)
}

func (s dbOrgAIStore) confirm(ctx context.Context, userID, orgID uuid.UUID, version int64, now time.Time) (storage.OrgAIConfirmStatus, storage.OrgAIConfig, error) {
	return storage.ConfirmOrgAI(ctx, s.db, userID, orgID, version, now)
}

// orgAIHandlers serves /api/organization/ai. sso supplies PAC-50's clock,
// break-glass list and step-up scopes.
type orgAIHandlers struct {
	db     *sql.DB
	sso    *ssoHandlers
	store  orgAIStore
	source nlp.AISource
	// hooks reach the write transaction (tests only; nil in production).
	hooks *storage.OrgAIWriteHooks
}

func newOrgAIHandlers(db *sql.DB, sso *ssoHandlers) *orgAIHandlers {
	return &orgAIHandlers{db: db, sso: sso, store: dbOrgAIStore{db: db}}
}

// mount mounts /api/organization/ai behind the session.
func (h *orgAIHandlers) mount(r chi.Router) {
	r.Get("/", h.get)
	r.Patch("/", h.patch)
	r.Post("/consent", h.confirm)
}

func (h *orgAIHandlers) strict() gen.ServerInterface {
	return gen.NewStrictHandlerWithOptions(&generatedServer{db: h.db, orgAI: h},
		[]gen.StrictMiddlewareFunc{withRequest}, strictOptions)
}

func (h *orgAIHandlers) get(w http.ResponseWriter, r *http.Request) {
	h.strict().GetOrganizationAI(w, r)
}

func (h *orgAIHandlers) patch(w http.ResponseWriter, r *http.Request) {
	h.strict().PatchOrganizationAI(w, withRawBody(r))
}

func (h *orgAIHandlers) confirm(w http.ResponseWriter, r *http.Request) {
	h.strict().ConfirmOrganizationAI(w, withRawBody(r))
}

type rawBodyKey struct{}

// withRawBody keeps the request body for the operation and hands the
// generated wrapper "{}" to decode. The contract requires decoding the raw
// body (patchOrganizationAI: an absent configurationVersion is never read as
// 0, which the generated int cannot tell; each malformed body has its own
// message), so the operation, not the wrapper, decides every 400.
func withRawBody(r *http.Request) *http.Request {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		raw = nil
	}
	r = r.WithContext(context.WithValue(r.Context(), rawBodyKey{}, raw))
	r.Body = io.NopCloser(strings.NewReader("{}"))
	return r
}

func rawBodyFrom(ctx context.Context) []byte {
	b, _ := ctx.Value(rawBodyKey{}).([]byte)
	return b
}

// orgCaller is membership (gate step 1): the caller's organisation, or the
// 403 error value.
func orgCaller(db *sql.DB, ctx context.Context) (*storage.User, *storage.Org, gen.SsoWriteMembershipRequiredError) {
	user, err := storage.GetUserByID(db, auth.UserIDFromContext(ctx))
	if err != nil || user == nil || user.OrgID == nil {
		return nil, nil, gen.OrgMembershipRequired
	}
	org, err := storage.GetOrgByID(db, *user.OrgID)
	if err != nil || org == nil {
		return nil, nil, gen.OrgNotFound
	}
	return user, org, ""
}

// orgAIView is the configuration as every member reads it: no key, and
// nothing that depends on the caller (P2-AC-7).
func orgAIView(c storage.OrgAIConfig) gen.OrganizationAIConfiguration {
	out := gen.OrganizationAIConfiguration{
		Configured:           c.Complete(),
		KeySet:               c.Provider != "" && c.APIKey != "",
		PersonalKeysAllowed:  c.PersonalKeysAllowed,
		ConfigurationVersion: int(c.Version),
		UpdatedAt:            c.UpdatedAt,
		ChangeableBy:         gen.OrganizationAIConfigurationChangeableByOrganizationAdmin,
	}
	if c.Provider == "" {
		return out
	}
	p := gen.OrganizationAIConfigurationProvider(c.Provider)
	out.Provider = &p
	str := func(v string) *string { return &v }
	switch c.Provider {
	case nlp.ProviderOpenAI, nlp.ProviderAnthropic:
		out.Model, out.BaseUrl = str(c.Model), str(c.BaseURL)
	case nlp.ProviderAzureOpenAI:
		out.AzureEndpoint, out.AzureDeployment, out.AzureApiVersion = str(c.AzureEndpoint), str(c.AzureDeployment), str(c.AzureAPIVersion)
	case nlp.ProviderBedrock:
		out.Model, out.AwsRegion = str(c.Model), str(c.AWSRegion)
	case nlp.ProviderVertex:
		out.Model = str(c.Model)
	}
	return out
}

// --- GET /api/organization/ai --------------------------------------------------

// GetOrganizationAI is getOrganizationAI: membership (403), then the
// configuration without the key (200). Read-only.
func (s *generatedServer) GetOrganizationAI(ctx context.Context, _ gen.GetOrganizationAIRequestObject) (gen.GetOrganizationAIResponseObject, error) {
	_, org, denied := orgCaller(s.db, ctx)
	if denied != "" {
		return gen.GetOrganizationAI403JSONResponse{Error: denied}, nil
	}
	c, err := s.orgAI.store.read(ctx, org.ID)
	if err != nil {
		return gen.GetOrganizationAI500JSONResponse{Error: serverFailure(ctx, "getOrganizationAI", err, http.StatusInternalServerError)}, nil
	}
	return gen.GetOrganizationAI200JSONResponse(orgAIView(c)), nil
}

// --- PATCH /api/organization/ai ------------------------------------------------

// orgAIBody is a patchOrganizationAI body after its shape checks.
type orgAIBody struct {
	version int64
	patch   storage.OrgAIPatch
	order   []string // the properties to change, in body order
}

// orgAIStringProps are the string properties of the body.
var orgAIStringProps = map[string]bool{
	storage.OrgAIPropProvider: true, storage.OrgAIPropModel: true, storage.OrgAIPropAPIKey: true,
	storage.OrgAIPropBaseURL: true, storage.OrgAIPropAzureEndpoint: true, storage.OrgAIPropAzureDeployment: true,
	storage.OrgAIPropAzureAPIVersion: true, storage.OrgAIPropAWSRegion: true,
}

const orgAIVersionProp = "configurationVersion"

// Shape messages of patchOrganizationAI's 400 (contract revision 3.1).
const (
	msgOrgAIInvalidJSON      = "invalid JSON"
	msgOrgAIVersionRequired  = "configurationVersion is required: send the version you were shown"
	msgOrgAIVersionNull      = "configurationVersion must not be null"
	msgOrgAIVersionType      = "invalid JSON: configurationVersion has the wrong type"
	msgOrgAIVersionNegative  = "configurationVersion must be 0 or more"
	msgOrgAINothingToChange  = "send at least one property to change"
	msgOrgAIProviderInvalid  = `provider must be one of: "", openai, anthropic, azure_openai, bedrock, vertex`
	msgOrgAIRemovalAlone     = `provider "" removes the whole configuration; send no other property with it`
	msgOrgAIKeyOnChange      = "apiKey is required when provider changes"
	msgOrgAIConfirmBadBody   = `invalid request: send {"configurationVersion": <the version you were shown>}`
	msgOrgAIChanged          = "the organisation's AI configuration changed since you were shown it; review the current configuration"
	msgOrgAINotConfiguredYet = "your organisation's AI configuration is not complete, so there is nothing to confirm"
)

// objectFields decodes a JSON object's members in body order. ok is false
// for anything that is not exactly one JSON object.
func objectFields(raw []byte) (names []string, values map[string]json.RawMessage, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, nil, false
	}
	values = map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, false
		}
		name, isString := tok.(string)
		if !isString {
			return nil, nil, false
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, false
		}
		if _, seen := values[name]; !seen {
			names = append(names, name)
		}
		values[name] = v
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, nil, false
	}
	return names, values, true
}

// wholeNumber parses a JSON integer literal (no fraction, no exponent).
func wholeNumber(v json.RawMessage) (int64, bool) {
	n, err := strconv.ParseInt(string(bytes.TrimSpace(v)), 10, 64)
	return n, err == nil
}

func isNull(v json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }

// parseOrgAIBody runs step 2 of patchOrganizationAI, the shape checks, in
// the contract's order; it returns the first failing check's message.
func parseOrgAIBody(raw []byte) (orgAIBody, string) {
	var b orgAIBody
	names, values, ok := objectFields(raw)
	if !ok {
		return b, msgOrgAIInvalidJSON // (1)
	}
	for _, n := range names { // (2)
		if n != orgAIVersionProp && n != storage.OrgAIPropPersonalKeysAllowed && !orgAIStringProps[n] {
			return b, "unknown property: " + n
		}
	}
	v, present := values[orgAIVersionProp] // (3)
	switch {
	case !present:
		return b, msgOrgAIVersionRequired
	case isNull(v):
		return b, msgOrgAIVersionNull
	}
	version, ok := wholeNumber(v)
	if !ok {
		return b, msgOrgAIVersionType
	}
	if version < 0 {
		return b, msgOrgAIVersionNegative
	}
	b.version = version
	for _, n := range names { // (4): one property at a time, in body order
		if n == orgAIVersionProp {
			continue
		}
		raw := values[n]
		if isNull(raw) {
			return b, n + " must not be null"
		}
		if n == storage.OrgAIPropPersonalKeysAllowed {
			var flag bool
			if json.Unmarshal(raw, &flag) != nil {
				return b, "invalid JSON: " + n + " has the wrong type"
			}
			b.patch.PersonalKeysAllowed = &flag
		} else {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				return b, "invalid JSON: " + n + " has the wrong type"
			}
			*orgAIField(&b.patch, n) = &s
		}
		b.order = append(b.order, n)
	}
	if len(b.order) == 0 { // (5)
		return b, msgOrgAINothingToChange
	}
	if p := b.patch.Provider; p != nil && *p != "" && !nlp.IsKeyProvider(*p) { // (6)
		return b, msgOrgAIProviderInvalid
	}
	if p := b.patch.Provider; p != nil && *p == "" && len(b.order) > 1 { // (7)
		return b, msgOrgAIRemovalAlone
	}
	if e := b.patch.AzureEndpoint; e != nil { // (8)
		if msg := nlp.OrgAzureEndpointShapeProblem(*e); msg != "" {
			return b, msg
		}
	}
	return b, ""
}

// orgAIField is the patch field for a string property.
func orgAIField(p *storage.OrgAIPatch, name string) **string {
	switch name {
	case storage.OrgAIPropProvider:
		return &p.Provider
	case storage.OrgAIPropModel:
		return &p.Model
	case storage.OrgAIPropAPIKey:
		return &p.APIKey
	case storage.OrgAIPropBaseURL:
		return &p.BaseURL
	case storage.OrgAIPropAzureEndpoint:
		return &p.AzureEndpoint
	case storage.OrgAIPropAzureDeployment:
		return &p.AzureDeployment
	case storage.OrgAIPropAzureAPIVersion:
		return &p.AzureAPIVersion
	default:
		return &p.AWSRegion
	}
}

// orgAIApplies says whether a model or destination property applies to a
// provider (contract OrganizationAIConfiguration: destination properties
// appear only for the provider they apply to; model not for azure_openai).
func orgAIApplies(provider, prop string) bool {
	switch prop {
	case storage.OrgAIPropModel:
		return provider != nlp.ProviderAzureOpenAI
	case storage.OrgAIPropBaseURL:
		return provider == nlp.ProviderOpenAI || provider == nlp.ProviderAnthropic
	case storage.OrgAIPropAzureEndpoint, storage.OrgAIPropAzureDeployment, storage.OrgAIPropAzureAPIVersion:
		return provider == nlp.ProviderAzureOpenAI
	case storage.OrgAIPropAWSRegion:
		return provider == nlp.ProviderBedrock
	}
	return true
}

// orgAIStateProblem is step 2b: the body against the configuration at the
// version just checked (contract revision 3: after the version check, so a
// stale form is told "changed" rather than refused for a reason only the
// newer configuration explains).
func orgAIStateProblem(b orgAIBody, stored storage.OrgAIConfig) string {
	p := b.patch
	providerChanged := p.Provider != nil && *p.Provider != "" && *p.Provider != stored.Provider
	if providerChanged && (p.APIKey == nil || *p.APIKey == "") { // (9)
		return msgOrgAIKeyOnChange
	}
	effective := stored.Provider
	if p.Provider != nil {
		effective = *p.Provider
	}
	if effective == "" && (p.Provider == nil || *p.Provider != "") { // (10)
		for _, n := range b.order {
			if n == storage.OrgAIPropProvider || n == storage.OrgAIPropPersonalKeysAllowed {
				continue
			}
			if n == storage.OrgAIPropAPIKey && *p.APIKey == "" {
				continue
			}
			return n + " requires a provider"
		}
		return ""
	}
	if effective == "" {
		return "" // the removal, alone (shape (7))
	}
	for _, n := range b.order { // (11)
		if n == storage.OrgAIPropProvider || n == storage.OrgAIPropAPIKey || n == storage.OrgAIPropPersonalKeysAllowed {
			continue
		}
		if !orgAIApplies(effective, n) {
			return n + " does not apply to " + effective
		}
	}
	// (12) Destinations, against what they replace: the stored values, or
	// none after a provider change. An unchanged value is not re-checked.
	baseline := stored
	if providerChanged {
		baseline = storage.OrgAIConfig{}
	}
	for _, n := range b.order {
		var msg string
		switch n {
		case storage.OrgAIPropBaseURL:
			if v := *p.BaseURL; v != baseline.BaseURL {
				msg = nlp.OrgBaseURLWriteProblem(effective, v)
			}
		case storage.OrgAIPropAzureEndpoint:
			if v := *p.AzureEndpoint; v != baseline.AzureEndpoint {
				msg = nlp.AzureEndpointWriteProblem(v)
			}
		case storage.OrgAIPropAWSRegion:
			if v := *p.AWSRegion; v != baseline.AWSRegion {
				msg = nlp.AWSRegionWriteProblem(v)
			}
		}
		if msg != "" {
			return msg
		}
	}
	return ""
}

func patch400(msg string) gen.PatchOrganizationAIResponseObject {
	return gen.PatchOrganizationAI400JSONResponse{Error: msg}
}

func orgAIConflict(c storage.OrgAIConfig, reason gen.OrganizationAIConsentConflictReason, msg string) gen.OrganizationAIConsentConflict {
	return gen.OrganizationAIConsentConflict{Error: msg, Reason: reason, Organization: orgAIView(c)}
}

// orgAIStepupRequired is the 403 OrganizationAIWriteStepupRequired: PAC-50's
// step-up, for this purpose.
func (h *orgAIHandlers) stepupRequired() gen.OrganizationAIWriteStepupRequired {
	b := h.sso.stepupRequired()
	out := gen.OrganizationAIWriteStepupRequired{
		Error:         gen.OrganizationAiWriteRequiresAdminStepup,
		StepupUrl:     gen.SsoStepupUrl(b.StepupURL),
		StepupPurpose: gen.OrganizationAIWriteStepupRequiredStepupPurposeLlmOrgKeyWrite,
		WithoutStepup: []string{withoutStepupOrgAIWrites},
	}
	if v, ok := b.StepupScopes[auth.StepupGoogle]; ok {
		out.StepupScopes.Google = &v
	}
	if v, ok := b.StepupScopes[auth.StepupMicrosoft]; ok {
		out.StepupScopes.Microsoft = &v
	}
	return out
}

// patchOrgAI403 is the 403 of patchOrganizationAI. The generated
// PatchOrganizationAI403JSONResponse is a defined type over the oneOf
// OrganizationAIWriteForbiddenResponse and so loses its MarshalJSON (it
// encodes as {}); this writes the one variant the gate chose, which is
// exactly one member of that oneOf.
type patchOrgAI403 struct{ body any }

func (r patchOrgAI403) VisitPatchOrganizationAIResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	return json.NewEncoder(w).Encode(r.body)
}

func patch403(body any) (gen.PatchOrganizationAIResponseObject, error) {
	return patchOrgAI403{body: body}, nil
}

// PatchOrganizationAI is patchOrganizationAI (contract revision 3.1):
// membership (403), the body's shape (400), the version (409), the body
// against what is stored (400), then one transaction that spends the proof
// and stores the change only if the version is still the one named.
func (s *generatedServer) PatchOrganizationAI(ctx context.Context, _ gen.PatchOrganizationAIRequestObject) (gen.PatchOrganizationAIResponseObject, error) {
	h := s.orgAI
	user, org, denied := orgCaller(s.db, ctx) // step 1
	if denied != "" {
		return patch403(gen.SsoWriteMembershipRequired{Error: denied})
	}
	body, msg := parseOrgAIBody(rawBodyFrom(ctx)) // step 2
	if msg != "" {
		return patch400(msg), nil
	}
	stored, err := h.store.read(ctx, org.ID)
	if err != nil {
		return gen.PatchOrganizationAI500JSONResponse{Error: serverFailure(ctx, "patchOrganizationAI", err, http.StatusInternalServerError)}, nil
	}
	if stored.Version != body.version { // step 2a, the early answer
		return gen.PatchOrganizationAI409JSONResponse(orgAIConflict(stored, gen.ConfigurationChanged, msgOrgAIChanged)), nil
	}
	if msg := orgAIStateProblem(body, stored); msg != "" { // step 2b
		return patch400(msg), nil
	}

	now := h.sso.clock.Now()
	iat := sessionIssuedAt(ctx)
	age := now.Sub(iat)
	out, err := h.store.write(ctx, storage.OrgAIWrite{ // steps 3 and 4, one transaction
		UserID: user.ID, OrgID: org.ID, ExpectedVersion: body.version, Patch: body.patch,
		BreakGlass:   h.sso.breakGlass[strings.ToLower(sessionEmail(ctx))],
		SessionFresh: !iat.IsZero() && age < breakGlassFreshness && age > -time.Minute,
		SessionIAT:   iat, Now: now, Hooks: h.hooks,
	})
	if err != nil {
		return gen.PatchOrganizationAI500JSONResponse{Error: serverFailure(ctx, "patchOrganizationAI", err, http.StatusInternalServerError)}, nil
	}
	switch out.Status {
	case storage.OrgAIConflict:
		return gen.PatchOrganizationAI409JSONResponse(orgAIConflict(out.Config, gen.ConfigurationChanged, msgOrgAIChanged)), nil
	case storage.OrgAIStepupRequired:
		return patch403(h.stepupRequired())
	case storage.OrgAIFreshLoginRequired:
		return patch403(gen.OrganizationAIWriteFreshLoginRequired{Error: gen.OrganizationAiWriteRequiresFreshLogin})
	}
	// The re-read: what any member reads right after (P2-AC-7). A failure
	// here is the 500 with the change stored (contract: the client re-reads).
	reread, err := h.store.read(ctx, org.ID)
	if err != nil {
		return gen.PatchOrganizationAI500JSONResponse{Error: serverFailure(ctx, "patchOrganizationAI re-read", err, http.StatusInternalServerError)}, nil
	}
	return gen.PatchOrganizationAI200JSONResponse(orgAIView(reread)), nil
}

// --- POST /api/organization/ai/consent -----------------------------------------

// parseConsentBody is confirmOrganizationAI's body: exactly
// {"configurationVersion": <integer >= 1>}.
func parseConsentBody(raw []byte) (int64, bool) {
	names, values, ok := objectFields(raw)
	if !ok || len(names) != 1 || names[0] != orgAIVersionProp {
		return 0, false
	}
	v, ok := wholeNumber(values[orgAIVersionProp])
	return v, ok && v >= 1
}

// ConfirmOrganizationAI is confirmOrganizationAI: body (400), membership
// (403), the organisation's current state (409), then the confirmation is
// recorded and the caller's AI status returned (200). The only way a
// confirmation is recorded (spec P2-D14).
func (s *generatedServer) ConfirmOrganizationAI(ctx context.Context, _ gen.ConfirmOrganizationAIRequestObject) (gen.ConfirmOrganizationAIResponseObject, error) {
	h := s.orgAI
	version, ok := parseConsentBody(rawBodyFrom(ctx))
	if !ok {
		return gen.ConfirmOrganizationAI400JSONResponse{Error: msgOrgAIConfirmBadBody}, nil
	}
	user, org, denied := orgCaller(s.db, ctx)
	if denied != "" {
		return gen.ConfirmOrganizationAI403JSONResponse{Error: denied}, nil
	}
	status, current, err := h.store.confirm(ctx, user.ID, org.ID, version, h.sso.clock.Now())
	if err != nil {
		return gen.ConfirmOrganizationAI500JSONResponse{Error: serverFailure(ctx, "confirmOrganizationAI", err, http.StatusInternalServerError)}, nil
	}
	switch status {
	case storage.OrgAIConfirmNotConfigured:
		return gen.ConfirmOrganizationAI409JSONResponse(orgAIConflict(current, gen.OrganizationNotConfigured, msgOrgAINotConfiguredYet)), nil
	case storage.OrgAIConfirmChanged:
		return gen.ConfirmOrganizationAI409JSONResponse(orgAIConflict(current, gen.ConfigurationChanged, msgOrgAIChanged)), nil
	}
	src := h.source
	if src == nil {
		src = nlp.DBSource{DB: s.db}
	}
	st, err := nlp.LoadAIState(ctx, src, user.ID)
	if err != nil {
		return gen.ConfirmOrganizationAI500JSONResponse{Error: serverFailure(ctx, "confirmOrganizationAI status", err, http.StatusInternalServerError)}, nil
	}
	out, err := genAIStatus(nlp.StatusOf(st))
	if err != nil {
		return nil, err
	}
	return gen.ConfirmOrganizationAI200JSONResponse(out), nil
}

// genAIStatus converts the settings read's status to the generated type
// (same JSON).
func genAIStatus(st storage.AIStatus) (gen.AIStatus, error) {
	var out gen.AIStatus
	raw, err := json.Marshal(st)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, errors.New("AI status does not match the contract")
	}
	return out, nil
}
