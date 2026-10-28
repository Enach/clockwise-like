package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/domain"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-71: sign-in by identity, not by address; the signed-in connect step
// (spec docs/specs/PAC-71.md §3; contract contracts/features/PAC-71.md §2–§4
// and the googleOAuthCallback / microsoftOAuthCallback / ssoOidcCallback /
// startGoogleConnect / startMicrosoftConnect operations; plan
// docs/plans/PAC-71.md §5).

const (
	// signInRefusedPath is the one refusal state of every sign-in callback
	// (spec §3 "Refusal", D-6): fixed, the same for every cause.
	signInRefusedPath = "/?error=signin_refused"
	// connectNotCreditedPath is a connect credited to nobody.
	connectNotCreditedPath = "/?connect=not_credited"

	googleConnectCookie = "google_connect_state"
	msConnectCookie     = "ms_connect_state"
	connectStateTTL     = 300 * time.Second

	connectProviderGoogle  = "google"
	connectProviderOutlook = "outlook"
)

// Test seams. connectBeforeStoreHook runs between the connect's generation
// check (step 3) and its credential store (step 6), the interleaving point of
// spec AC-47; connectNow is the clock the connect state is stamped with.
var (
	connectBeforeStoreHook func()
	connectNow             = time.Now
	// microsoftGraphOrganizationURL is the tenant source (contract §3 item 1).
	microsoftGraphOrganizationURL = "https://graph.microsoft.com/v1.0/organization?$select=id,verifiedDomains"
)

var (
	googleSubjectFormat = regexp.MustCompile(`^[0-9]{1,255}$`)
	guidFormat          = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// googleRecordedMatches is Google's rule (a) (contract §3 item 6): the
// recorded identifier has Google's format and equals the subject exactly.
func googleRecordedMatches(recorded, subject string) bool {
	return googleSubjectFormat.MatchString(recorded) && recorded == subject
}

// microsoftRecordedMatches is Microsoft's rule (a): the recorded identifier is
// a GUID equal to /me's id, ignoring letter case. The two formats are
// disjoint, so no recorded value can match both providers.
func microsoftRecordedMatches(recorded, subject string) bool {
	return guidFormat.MatchString(recorded) && strings.EqualFold(recorded, subject)
}

// googleConsumerDomains are the domains on which a verified Google address is
// authoritative without a hosted domain (spec §3 evidence table).
var googleConsumerDomains = map[string]bool{"gmail.com": true, "googlemail.com": true}

// googleEvidence is contract googleOAuthCallback step 3.
func googleEvidence(info *googleUserInfo) storage.Evidence {
	if info.VerifiedEmail == nil || !*info.VerifiedEmail {
		return storage.EvidenceNone
	}
	d := domain.ExtractDomain(info.Email)
	if d != "" && (googleConsumerDomains[d] || strings.EqualFold(strings.TrimSpace(info.HD), d)) {
		return storage.EvidenceAuthoritative
	}
	return storage.EvidenceMailbox
}

// issueJWT sets the auth_token session cookie for user, carrying gen, the
// account's current session generation. It returns an error, and sets
// nothing, when the session cannot be issued (PAC-35, API-011).
func (h *authHandlers) issueJWT(w http.ResponseWriter, user *storage.User, gen int64) error {
	if h.jwtSecret == "" {
		return errNoSigningSecret
	}
	jwtToken, err := generateSessionToken(user.ID, user.Email, gen, h.jwtSecret)
	if err != nil {
		return err
	}
	setCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    jwtToken,
		Path:     "/",
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// signInRefused ends a sign-in on the refusal state. The cause and the
// account it concerns go to the server log only; auth_token is neither set
// nor expired, so a session the browser holds is kept.
func (h *authHandlers) signInRefused(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("pac71: %s %s: %v", r.Method, r.URL.Path, err)
	http.Redirect(w, r, h.frontendURL+signInRefusedPath, http.StatusFound)
}

// --- Google ----------------------------------------------------------------

// callback is googleOAuthCallback: the state cookie that matches `state`
// chooses the CONNECT or the SIGN-IN branch; a session never does.
func (h *authHandlers) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if c, err := r.Cookie(googleConnectCookie); err == nil && c.Value != "" && c.Value == state {
		h.connectCallback(w, r, connectFlow{
			provider: connectProviderGoogle, cookie: googleConnectCookie,
			exchange: func(ctx context.Context, code string) (*oauth2.Token, error) {
				return auth.ExchangeCode(ctx, h.oauthConfig, code)
			},
			store: func(q storage.Querier, uid uuid.UUID, tok *oauth2.Token) error {
				return auth.UpsertUserToken(q, uid, tok)
			},
		})
		return
	}
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != state {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	setCookie(w, &http.Cookie{Name: "oauth_state", MaxAge: -1, Path: "/"})

	token, err := auth.ExchangeCode(r.Context(), h.oauthConfig, r.URL.Query().Get("code"))
	if err != nil {
		writeServerError(w, r, "token exchange failed", err, http.StatusInternalServerError)
		return
	}
	info, err := fetchGoogleUserInfo(r.Context(), token, h.oauthConfig)
	if err != nil {
		writeServerError(w, r, "userinfo failed", err, http.StatusInternalServerError)
		return
	}
	res, err := storage.ResolveSignIn(r.Context(), h.db, storage.SignInRequest{
		Identity:      storage.Identity{Provider: storage.IdentityGoogle, Subject: strings.TrimSpace(info.ID)},
		Email:         info.Email,
		Name:          info.Name,
		AvatarURL:     info.Picture,
		Evidence:      googleEvidence(info),
		FirstProvider: "google",
		RuleA:         &storage.RuleA{Matches: googleRecordedMatches, MinEvidence: storage.EvidenceMailbox},
		StoreCredential: func(q storage.Querier, uid uuid.UUID, _ storage.SignInHow) error {
			return auth.UpsertUserToken(q, uid, token)
		},
	})
	var refused *storage.RefusalError
	if errors.As(err, &refused) {
		h.signInRefused(w, r, err)
		return
	}
	if err != nil {
		writeServerError(w, r, "account creation or binding failed", err, http.StatusInternalServerError)
		return
	}
	// PAC-68's organisation rule, evaluated for the account the identity is
	// bound to (never one reached by address), on the address it holds.
	_ = storage.AssociateUserWithOrg(h.db, res.User.ID, res.User.Email)
	if err := h.issueJWT(w, res.User, res.Gen); err != nil {
		h.signInFailed(w, r, "issue session", err)
		return
	}
	http.Redirect(w, r, h.frontendURL+"/auth/callback", http.StatusFound)
}

// --- Microsoft -------------------------------------------------------------

// microsoftCallback is microsoftOAuthCallback.
func (h *authHandlers) microsoftCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if c, err := r.Cookie(msConnectCookie); err == nil && c.Value != "" && c.Value == state {
		cfg := microsoftConfig()
		if cfg == nil {
			expireConnectCookie(w, msConnectCookie)
			writeError(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
			return
		}
		h.connectCallback(w, r, connectFlow{
			provider: connectProviderOutlook, cookie: msConnectCookie,
			exchange: func(ctx context.Context, code string) (*oauth2.Token, error) {
				return auth.ExchangeMicrosoftCode(ctx, cfg, code)
			},
			// Neither /me nor /organization is read; the work calendar is
			// never changed (spec D-15, CONTRACT-7).
			store: func(q storage.Querier, uid uuid.UUID, tok *oauth2.Token) error {
				return auth.StoreMicrosoftCredential(q, uid, tok, false)
			},
		})
		return
	}
	stateCookie, err := r.Cookie("ms_oauth_state")
	if err != nil || stateCookie.Value != state {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	setCookie(w, &http.Cookie{Name: "ms_oauth_state", MaxAge: -1, Path: "/"})

	cfg := microsoftConfig()
	if cfg == nil {
		writeError(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
		return
	}
	token, err := auth.ExchangeMicrosoftCode(r.Context(), cfg, r.URL.Query().Get("code"))
	if err != nil {
		writeServerError(w, r, "token exchange failed", err, http.StatusInternalServerError)
		return
	}
	// (a): an unreadable profile, or one without mail, is the failure state.
	info, err := fetchMicrosoftUserInfo(r.Context(), token)
	if err != nil || info.Email == "" {
		h.signInFailed(w, r, "could not read the Microsoft account", err)
		return
	}
	// Step 3: the tenant fails closed (spec D-23). Anything that is not
	// positively a work account is the refusal, before any lookup or write.
	tenant, verified, err := fetchMicrosoftOrganization(r.Context(), token)
	if err != nil || !guidFormat.MatchString(tenant) || !guidFormat.MatchString(info.ID) {
		h.signInRefused(w, r, fmt.Errorf("sign-in refused: Microsoft tenant unobtainable or account not classifiable as work: %v", err))
		return
	}
	evidence := storage.EvidenceNone
	if d := domain.ExtractDomain(info.Email); d != "" && containsFold(verified, d) {
		evidence = storage.EvidenceAuthoritative
	}
	res, err := storage.ResolveSignIn(r.Context(), h.db, storage.SignInRequest{
		Identity: storage.Identity{
			Provider: storage.IdentityMicrosoft,
			Tenant:   strings.ToLower(tenant),
			Subject:  strings.ToLower(info.ID),
		},
		Email:         info.Email,
		Name:          info.Name,
		Evidence:      evidence,
		FirstProvider: "microsoft",
		RuleA:         &storage.RuleA{Matches: microsoftRecordedMatches, MinEvidence: storage.EvidenceAuthoritative},
		StoreCredential: func(q storage.Querier, uid uuid.UUID, how storage.SignInHow) error {
			// A created account gets Outlook, and a bound sign-in keeps
			// today's reset (PAC-47's to remove); a migration binding never
			// changes the work calendar (spec AC-31, AC-41).
			return auth.StoreMicrosoftCredential(q, uid, token, !how.Bound())
		},
	})
	var refused *storage.RefusalError
	if errors.As(err, &refused) {
		h.signInRefused(w, r, err)
		return
	}
	if err != nil {
		// (b): creating or binding failed; nothing was saved (one transaction).
		h.signInFailed(w, r, "creating or binding the account failed", err)
		return
	}
	_ = storage.AssociateUserWithOrg(h.db, res.User.ID, res.User.Email)
	if err := h.issueJWT(w, res.User, res.Gen); err != nil {
		h.signInFailed(w, r, "issue session", err)
		return
	}
	http.Redirect(w, r, h.frontendURL+"/auth/callback?provider=outlook", http.StatusFound)
}

// fetchMicrosoftOrganization reads Graph /organization with the sign-in token
// (contract §3 item 1). It answers only for a 200 whose value holds exactly
// one entry: the tenant id and its verified domain names.
func fetchMicrosoftOrganization(ctx context.Context, token *oauth2.Token) (string, []string, error) {
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	client.Timeout = 5 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, microsoftGraphOrganizationURL, nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("organization: status %d", resp.StatusCode)
	}
	var body struct {
		Value []struct {
			ID              string `json:"id"`
			VerifiedDomains []struct {
				Name string `json:"name"`
			} `json:"verifiedDomains"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", nil, err
	}
	if len(body.Value) != 1 {
		return "", nil, fmt.Errorf("organization: %d entries", len(body.Value))
	}
	names := make([]string, 0, len(body.Value[0].VerifiedDomains))
	for _, d := range body.Value[0].VerifiedDomains {
		names = append(names, d.Name)
	}
	return body.Value[0].ID, names, nil
}

// --- connect state ---------------------------------------------------------

// connectState is what a connect start binds its state to (contract §4): the
// caller, the session's generation, the provider and an expiry. It is
// carried as base64url(JSON) "." base64url(HMAC-SHA256) under a key derived
// from the session signing secret, so only this server can create or check
// it, and it is tied to the browser by the connect cookie.
type connectState struct {
	User     uuid.UUID `json:"u"`
	Gen      int64     `json:"g"`
	Provider string    `json:"p"`
	Expires  int64     `json:"e"`
	Nonce    string    `json:"n"`
}

func connectStateKey(secret string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("paceday PAC-71 connect state v1"))
	return m.Sum(nil)
}

func signConnectState(secret string, st connectState) (string, error) {
	payload, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(payload)
	m := hmac.New(sha256.New, connectStateKey(secret))
	m.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

// verifyConnectState returns the state when value is one this server issued
// for provider and it has not expired.
func verifyConnectState(secret, value, provider string, now time.Time) (*connectState, bool) {
	if secret == "" {
		return nil, false
	}
	p, sig, ok := strings.Cut(value, ".")
	if !ok {
		return nil, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, false
	}
	m := hmac.New(sha256.New, connectStateKey(secret))
	m.Write([]byte(p))
	if !hmac.Equal(got, m.Sum(nil)) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, false
	}
	var st connectState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, false
	}
	if st.Provider != provider || st.User == uuid.Nil || now.Unix() >= st.Expires {
		return nil, false
	}
	return &st, true
}

func expireConnectCookie(w http.ResponseWriter, name string) {
	setCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// --- connect start (generated strict interface) ----------------------------

func (h *authHandlers) startGoogleConnect(w http.ResponseWriter, r *http.Request) {
	newConnectHandler(h).StartGoogleConnect(w, r)
}

func (h *authHandlers) startMicrosoftConnect(w http.ResponseWriter, r *http.Request) {
	newConnectHandler(h).StartMicrosoftConnect(w, r)
}

func newConnectHandler(h *authHandlers) gen.ServerInterface {
	return gen.NewStrictHandlerWithOptions(&generatedServer{db: h.db, ah: h}, []gen.StrictMiddlewareFunc{withRequest}, strictOptions)
}

// issueConnect builds the state, its cookie and the authorize URL.
func (s *generatedServer) issueConnect(ctx context.Context, provider, cookie string, authURL func(state string) string) (string, string, error) {
	g := storage.SessionGuardFrom(ctx)
	if g == nil || g.UserID == uuid.Nil {
		return "", "", errors.New("connect start without a session")
	}
	if s.ah.jwtSecret == "" {
		return "", "", errNoSigningSecret
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	state, err := signConnectState(s.ah.jwtSecret, connectState{
		User: g.UserID, Gen: g.Gen, Provider: provider,
		Expires: connectNow().Add(connectStateTTL).Unix(), Nonce: hex.EncodeToString(b),
	})
	if err != nil {
		return "", "", err
	}
	header := cookieHeader(&http.Cookie{
		Name: cookie, Value: state, Path: "/", MaxAge: int(connectStateTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	return authURL(state), header, nil
}

// StartGoogleConnect is startGoogleConnect: 200 {authorize_url} and the
// google_connect_state cookie, never a redirect (contract §4).
func (s *generatedServer) StartGoogleConnect(ctx context.Context, _ gen.StartGoogleConnectRequestObject) (gen.StartGoogleConnectResponseObject, error) {
	url, cookie, err := s.issueConnect(ctx, connectProviderGoogle, googleConnectCookie, func(state string) string {
		return auth.GetAuthURL(s.ah.oauthConfig, state, "")
	})
	if err != nil {
		return gen.StartGoogleConnect500JSONResponse{Error: serverFailure(ctx, "start google connect", err, http.StatusInternalServerError)}, nil
	}
	return gen.StartGoogleConnect200JSONResponse{
		Body:    gen.ConnectStartResponse{AuthorizeUrl: url},
		Headers: gen.StartGoogleConnect200ResponseHeaders{SetCookie: cookie},
	}, nil
}

// StartMicrosoftConnect is startMicrosoftConnect; 503 when Microsoft is not
// configured (no cookie).
func (s *generatedServer) StartMicrosoftConnect(ctx context.Context, _ gen.StartMicrosoftConnectRequestObject) (gen.StartMicrosoftConnectResponseObject, error) {
	cfg := microsoftConfig()
	if cfg == nil {
		return gen.StartMicrosoftConnect503JSONResponse{Error: "Microsoft OAuth not configured"}, nil
	}
	url, cookie, err := s.issueConnect(ctx, connectProviderOutlook, msConnectCookie, func(state string) string {
		return auth.GetMicrosoftAuthURL(cfg, state, "")
	})
	if err != nil {
		return gen.StartMicrosoftConnect500JSONResponse{Error: serverFailure(ctx, "start microsoft connect", err, http.StatusInternalServerError)}, nil
	}
	return gen.StartMicrosoftConnect200JSONResponse{
		Body:    gen.ConnectStartResponse{AuthorizeUrl: url},
		Headers: gen.StartMicrosoftConnect200ResponseHeaders{SetCookie: cookie},
	}, nil
}

// --- connect callback ------------------------------------------------------

type connectFlow struct {
	provider string // google | outlook
	cookie   string
	exchange func(ctx context.Context, code string) (*oauth2.Token, error)
	store    func(q storage.Querier, uid uuid.UUID, tok *oauth2.Token) error
}

// connectCallback is the CONNECT branch (contract googleOAuthCallback, steps
// 1–7). It never sets or expires auth_token, binds nothing, issues no session
// and changes nothing but the starting account's credential; every response
// expires the connect cookie, so a state is used at most once.
func (h *authHandlers) connectCallback(w http.ResponseWriter, r *http.Request, f connectFlow) {
	expireConnectCookie(w, f.cookie)
	notCredited := func(why string) {
		log.Printf("pac71: %s connect credited to nobody: %s", f.provider, why)
		http.Redirect(w, r, h.frontendURL+connectNotCreditedPath, http.StatusFound)
	}
	c, _ := r.Cookie(f.cookie)
	st, ok := verifyConnectState(h.jwtSecret, c.Value, f.provider, connectNow())
	if !ok { // step 1
		notCredited("state not issued by this server, expired, or for another provider")
		return
	}
	// Step 2: the browser's session (a provider redirect carries no
	// Authorization header) must be the starting account at the start's
	// generation.
	session, err := r.Cookie("auth_token")
	if err != nil {
		notCredited("no session")
		return
	}
	claims, err := auth.ValidateToken(session.Value, h.jwtSecret)
	if err != nil {
		notCredited("invalid session")
		return
	}
	sessGen, genErr := claims.Generation()
	if genErr != nil {
		notCredited("invalid gen claim")
		return
	}
	if claims.UserID != st.User.String() || sessGen != st.Gen {
		notCredited("the session is not the starting account's at the start's generation")
		return
	}
	// Step 3: the account's current generation, uncached.
	cur, found, err := storage.CurrentGeneration(h.db, st.User)
	if err != nil {
		writeServerError(w, r, "connect: read session generation", err, http.StatusInternalServerError)
		return
	}
	if !found || cur != st.Gen {
		notCredited("the account's generation moved since the start")
		return
	}
	// Step 4: the provider returned without a code (the user cancelled).
	code := r.URL.Query().Get("code")
	if code == "" {
		notCredited("no code (provider error " + r.URL.Query().Get("error") + ")")
		return
	}
	tok, err := f.exchange(r.Context(), code) // step 5
	if err != nil {
		writeServerError(w, r, "connect: token exchange failed", err, http.StatusInternalServerError)
		return
	}
	if connectBeforeStoreHook != nil {
		connectBeforeStoreHook()
	}
	// Step 6: store only while the generation is still the start's.
	err = storage.WithGeneration(r.Context(), h.db, st.User, st.Gen, func(q storage.Querier) error {
		return f.store(q, st.User, tok)
	})
	if errors.Is(err, storage.ErrStaleSession) {
		notCredited("the account's generation moved before the store")
		return
	}
	if err != nil {
		writeServerError(w, r, "connect: save token failed", err, http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, h.frontendURL+"/auth/callback?provider="+f.provider+"&connect=credited", http.StatusFound)
}
