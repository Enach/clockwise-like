# Spec — PAC-35: Sign-in and sign-out flows that fail, mislead or log the user out

- **Linear**: https://linear.app/paceday/issue/PAC-35
- **Status**: challenged (revision 3 answers challenge round 3)
- **Author**: spec-author
- **Inherited scope**:
  - Audit findings (all in `docs/factory/api-audit.md`, batched there as item
    D5): **API-010** (SSO redirect never followed), **API-011** (silent session
    issue failure in all three sign-in callbacks), **API-012** (Microsoft
    callback failure path), **API-061** (calendar disconnect ends the session),
    **API-063** (logout status and body), **API-064** (`login_hint` ignored),
    **API-065** (three names for the user's picture).
  - **API-092** (`api-audit.md:350`, no Go test coverage), **non-admin share
    only**: the five paths `/api/auth/detect`, `/api/auth/sso/{domain}`,
    `/api/auth/callback/oidc/{domain}`, `/api/auth/me`, `/api/auth/logout`. All
    five are touched here. Resolved here by AC-18. The `/api/admin/sso` share is
    claimed by PAC-22 (`docs/specs/PAC-22.md:7-8`). The row split is D-7.
  - **API-060** (`api-audit.md:318`, SAML and empty-issuer SSO rows accepted at
    create time and failing only at the start route). The SSO start route is
    touched here (AC-8 adds a hint). **Position: not resolved here; stays with
    PAC-22**, which claims it (`docs/specs/PAC-22.md:7-8`). The reason README §7
    rule 1 does not pull it into this spec is recorded as D-5. This spec does
    not change the start route's SAML or setup-failure answers, and AC-18 pins
    them in Go.
  - `x-uncertain` markers: one sits on an operation this work touches. The
    current-user response's `provider` property
    (`contracts/openapi/paths/auth.yaml:750`) is marked uncertain ("free text with
    no CHECK constraint found"). API-065 modifies that response, so per README
    §7 rule 1 the contract stage must resolve the marker (close the value set or
    state definitively that it is open). No other auth operation touched here
    carries a marker; the other two markers in `auth.yaml` (`:877`, `:938`) are
    on SSO-provider administration, which is not touched.
  - Open contract notes that this work makes false and must therefore be
    rewritten, not left: the `login_hint` parameter descriptions
    (`auth.yaml:45`, `:132`, which say it is "silently discarded"), the
    Google-callback `Set-Cookie` note (`auth.yaml` under `googleOAuthCallback`,
    "if jwtSecret is empty … the user lands logged-out"), the logout `200`
    description ("a 204 would be the honest status"), and two more operations
    (added in revision 1):
    - `microsoftOAuthCallback` (`auth.yaml:160-213`): the operation description
      ("a failed fetch is a 502 and nothing is saved"), the `302` description
      ("Every failure is a non-302 response below"), the whole `502` response
      (Graph profile unreadable), and the upsert-failure clause of the `500`
      description ("or the user upsert failed"). AC-4 turns both of those
      failures into redirects to the failure state.
    - `disconnectCalendar` (`auth.yaml:414-428`): the summary ("… and clear the
      session cookie"), the description ("this also logs the user out"), and
      the `Set-Cookie` header on the `204`. AC-11 makes all three false.

## 1. Problem

Signing in to Paceday is broken in four ways that a user can hit and cannot
work around:

1. **People whose company uses single sign-on cannot sign in at all.** They type
   their work email, the app asks the server which sign-in method that email
   uses, the server answers "single sign-on, go here", and the app ignores the
   answer and shows them a password step that cannot succeed for them.
2. **A sign-in that fails on the server looks like a success, then quietly
   isn't.** If the server cannot create the session at the very last step, the
   user is still sent to the "signing you in" page, which finds no session and
   drops them back on the landing page as an anonymous visitor with no
   explanation. They will try again, get the same result, and conclude the
   product is broken.
3. **Typing your email does not carry through to the identity provider.** The
   user types their email into Paceday, then is shown Google's or Microsoft's
   "choose an account" screen anyway and has to pick the same address again. For
   people with several accounts (personal and work) this is where the wrong
   account gets picked.
4. **Disconnecting your calendar signs you out.** A user who disconnects their
   calendar (to reconnect a different one, or to stop Paceday reading it) is
   also logged out, which neither the button nor the confirmation suggests.

Two smaller defects ride along because they are in the same flow and the audit
batched them (D5): logging out returns a response shape that one of the app's two
API clients would reject, and the user's profile picture is sent by the server
under one name and read by the app under another (or not at all), so it never
appears.

Who is affected: every user who signs in (items 2, 3), every user at an
organisation with SSO configured (item 1), and every user who disconnects a
calendar (item 4).

## 2. Current behaviour

All citations are to `origin/main` of each repo as of 2026-10-26
(clockwise-like `7de7044`, smart-calendar-flow `4ded854`).

### 2.1 SSO detection (API-010)

- The server's detection answer has three fields: a type, a provider name, and a
  redirect URL, the last two only for SSO
  (`backend/auth/sso_detect.go:18-22`). For an SSO domain the redirect URL is a
  server-relative path to the SSO start route
  (`backend/auth/sso_detect.go:42-46`). There is no `domain` field.
- The route is `POST /api/auth/detect` (`backend/api/routes.go:41`); the audit
  row calls it `GET`, which is wrong — the method is POST.
- The web app declares the answer as `{type, domain?}`
  (`smart-calendar-flow/src/components/auth/AuthDialog.tsx:32-35`) and only
  follows the SSO branch when `res.domain` is present
  (`AuthDialog.tsx:111-114`). It never is, so control falls to the password step
  (`AuthDialog.tsx:115-117`).
- The SSO start route itself works: it looks up the provider for the domain and
  redirects to the IdP (`backend/api/handlers_sso.go:49-87`).

### 2.2 Session issue failure after a successful sign-in (API-011, API-012)

- The session is created by a helper that **returns without doing anything and
  without telling its caller** when the server's signing secret is empty or token
  generation returns an error (`backend/api/handlers_auth.go:146-153`).
- All three sign-in callbacks call that helper and then redirect unconditionally
  to the web app's sign-in completion page:
  - Google: `backend/api/handlers_auth.go:78-79` (no query string)
  - Microsoft: `backend/api/handlers_microsoft_auth.go:79-81` (`?provider=outlook`)
  - OIDC SSO: `backend/api/handlers_sso.go:150-151` (`?provider=sso`)
- How reachable is it? `main.go` refuses to start with an empty secret
  (`backend/main.go:169-172`), and HS256 signing with a non-empty key
  (`backend/auth/jwt.go:17-27`) has no realistic runtime failure. So in a
  correctly started production server this path is near-unreachable; it is
  reachable whenever the handler is built some other way (tests, a future
  wiring change) and the defect is that the failure is *silent*. This is stated
  so the challenger can weigh it; it does not remove the defect.
- **API-012 has partly changed since the audit.** Since PAC-24, a failed
  Microsoft profile fetch or user upsert no longer redirects to the success page:
  it writes a JSON error body with 502 or 500 on the API origin and stops
  (`backend/api/handlers_microsoft_auth.go:62-73`). What remains is that the user
  is left on a raw JSON error page on the API's origin with no way back into
  the app. The same is true of every other failure branch in all three callbacks
  (token exchange, profile fetch, upsert, token save, state mismatch:
  `handlers_auth.go:42-73`, `handlers_microsoft_auth.go:41-77`,
  `handlers_sso.go:94-147`).
- The web app's completion page (`src/pages/AuthCallback.tsx:15-31`) re-reads the
  current user and then navigates to `/app` (or a sanitized `redirect`). The
  re-read does **not** throw on 401: the auth context treats 401 as "signed
  out" and returns normally (`src/contexts/AuthContext.tsx:48-62`). So with no
  session the page still navigates to `/app`, which bounces an anonymous visitor
  to the landing page (protected by e2e
  `e2e/tests/auth-session.spec.ts:75`). The page's own failure branch navigates
  to `/?error=auth_failed` (`AuthCallback.tsx:25`), but **nothing in the web app
  reads that `error` parameter** (no other match for `auth_failed` or
  `error=` routing in `src/`), so even the existing failure path shows nothing.

### 2.3 `login_hint` (API-064)

- The web app appends `?login_hint=<email>` when sending a user to Google or
  Microsoft (`AuthDialog.tsx:104`, `:108`). The SSO branch (were it reached)
  sends no hint (`AuthDialog.tsx:112`).
- Neither start route reads any query parameter: Google
  (`backend/api/handlers_auth.go:24-39`, building the URL at
  `backend/auth/google_oauth.go:24-26`) and Microsoft
  (`backend/api/handlers_microsoft_auth.go:17-38`,
  `backend/auth/microsoft_oauth.go:35-37`). The SSO start route reads only the
  path domain and builds the IdP URL with no hint
  (`backend/api/handlers_sso.go:49-87`, `backend/auth/oidc.go:61-63`).
- The contract already documents the parameter as accepted and discarded
  (`contracts/openapi/paths/auth.yaml:45-54`, and the Microsoft equivalent at
  `:132`).

### 2.4 Calendar disconnect (API-061)

- Disconnect deletes the user's stored Google token and then **also expires the
  session cookie** (`backend/api/handlers_auth.go:135-144`, the cookie at `:142`).
- The web app calls it from two places, both labelled as a calendar action
  ("Disconnected from Google Calendar", "Disconnected"):
  `src/components/ConnectionStatus.tsx:15-26` and
  `src/components/WorkCalendarConnection.tsx:84-95`. Neither expects to be
  signed out; both just invalidate the calendar-status query.
- Found while reading, **not** in the audit: disconnect only deletes the Google
  token row (`backend/auth/token_store.go:53-59`). A user whose calendar is
  Outlook or webcal is logged out but their calendar stays connected. See §5 and
  §8 OQ-5.
- (revision 2) That deletion happens **whatever the user's calendar provider
  is**: the delete is keyed on the user alone
  (`DELETE FROM oauth_tokens WHERE user_id = $1`, `token_store.go:57`), while the
  calendar status picks its branch from the settings provider, not from which
  credentials exist (`backend/api/handlers_auth.go:92-111`). The Microsoft
  credential lives on the settings row instead
  (`backend/auth/microsoft_oauth.go:73-77`) and the webcal link is a settings
  field, so neither is touched. A user can be an Outlook or webcal user *and*
  hold a stored Google token: the Google sign-in callback always stores one
  (`handlers_auth.go:68-71`), and the user can then switch their calendar to
  webcal, or sign in with Microsoft under the same email, which makes Outlook
  their provider. For such a user, disconnect today deletes the Google token and
  nothing of the Outlook or webcal calendar.
- (revision 2) The stored Google token is used beyond the calendar status:
  Google Meet reports as connected only while it exists
  (`backend/api/handlers_conferencing.go:199-204`), and booking and the calendar
  engine load it (`backend/api/handlers_booking.go:88`,
  `backend/engine/cal_iface.go:33`). So disconnect today also turns Google Meet
  to not connected, for Google users and for Outlook/webcal users holding a
  Google token alike.
- (revision 3) Outlook users holding a Google token are the common case, not an
  edge: every Microsoft sign-in sets the calendar provider to Outlook
  (`backend/auth/microsoft_oauth.go:68`) and the Google sign-in never resets it
  (`backend/api/handlers_auth.go:62-79` does not touch settings), so anyone who
  has ever signed in with both is one.
- (revision 3) Which users count as "Google" is decided by the calendar status:
  a blank provider, and any value other than Outlook or webcal, is answered as
  Google (`backend/api/handlers_auth.go:92-95` and the `default` branch of the
  switch at `:97-111`). The settings write accepts only google, outlook and
  webcal (`backend/api/handlers_settings.go:32`, `:272`), and migration 024
  normalises older rows, so no fourth case exists today. The web app decides
  which disconnect UI to show from the provider that status returns.
- (revision 3) How the web app treats a disconnect answer today: its API client
  (`smart-calendar-flow/src/api/client.ts`, `requestApi` at `:375-418`) throws an
  HTTP error for any non-success status, and both callers then show "Failed to
  disconnect". It resolves a no-content success, and both callers show a
  success toast (`ConnectionStatus.tsx:18-19` "Disconnected from Google
  Calendar", `WorkCalendarConnection.tsx:87-88` "Disconnected"). Any other
  success answer without a JSON content type is classified as "server
  unreachable" (`client.ts:414-415`); `withFallback` (`:491-503`) then switches
  the whole session to preview data and runs the mock disconnect (`:952-954`),
  which also ends in the success toast.

### 2.5 Logout (API-063)

- Logout expires the session cookie and writes status 200 with no body and no
  content type (`backend/api/handlers_me.go:31-41`).
- The web app calls it through the lenient wrapper (`AuthContext.tsx:128`),
  which tolerates that. The strict client (`requestApi`) would reject the same
  shape as non-JSON (audit API-063).
- Logout sits behind the auth middleware (`backend/api/routes.go:56-61`), so
  logging out with an expired session returns 401. The web app clears local
  state regardless (`AuthContext.tsx:129-133`).

### 2.6 User picture (API-065)

- The server serialises the user's picture as `avatarUrl`, always present, empty
  for Microsoft and SSO users (`backend/storage/users.go:14`; contract
  `auth.yaml` `CurrentUserResponse`).
- The auth context declares it as `avatar?` (`src/contexts/AuthContext.tsx:16-21`)
  — but **no component reads the current user's picture from the auth context**
  (no match for `user.avatar` / `user?.avatar` in `src/`).
- The scheduling-links module reads the same endpoint as
  `{id, email, name?}` (`src/api/schedulingLinks.ts:56`, used at `:452`, `:510`)
  and builds the owner's host entry without a picture
  (`schedulingLinks.ts:79-81`). Host avatars render a picture when one is present
  (`src/components/links/HostAvatars.tsx:57-58`). So the one place the app could
  show the signed-in user's picture — as owner/host of their own scheduling link
  — never does.

### 2.7 Existing tests in this area

- `backend/api/handlers_auth_test.go:35-53` — disconnect returns 204. It does not
  assert anything about the session cookie, so it neither protects nor exposes
  API-061.
- `handlers_auth_test.go:55`, `:80` — Google start route sets a state cookie and
  redirects; Google callback rejects a bad state.
- `backend/api/ratelimit_detect_test.go` — detection rate limiting.
- `backend/api/pac20_cookies_test.go` — every cookie goes through the secure
  cookie helper.
- `smart-calendar-flow/src/api/contract.test.ts:84-87` — the client's disconnect
  call resolves to nothing and hits the right path.
- e2e `e2e/tests/auth-session.spec.ts` — session boundary, anonymous `/app`
  bounce, logout ends the session (`:101`). The real OAuth callbacks are
  `test.fixme` and listed as a seam gap (`e2e/README.md:152`,
  `e2e/SEAM-REQUIRED.md` item B).
- **No test** covers: SSO detection driving a redirect in the web app; session
  issue failure in any callback; `login_hint` forwarding; disconnect leaving the
  session intact; the user's picture.

## 3. Desired behaviour

- **SSO users reach their identity provider.** When a user types an email whose
  domain has single sign-on configured, the app sends them to their
  organisation's identity provider, using the destination the server's
  detection answer gives, rather than to the password step.
- **The email the user typed travels with them.** Whichever provider the user is
  sent to — Google, Microsoft, or their organisation's IdP — the provider is told
  which account the user said they are, so a provider that supports this can
  preselect or prefill it. The hint is a convenience only: the account the user
  ends up signed in as is still whatever the provider authenticates, never the
  hint. A hint that is not a plausible email address is dropped and sign-in
  continues without it.
- **A sign-in that did not create a session says so.** If the server completes
  the provider exchange but cannot create the session, the user is not sent to
  the "signing you in" page. They land back in the app on a visible sign-in
  failure state that says sign-in did not complete and lets them try again. The
  same failure state is used when a Microsoft sign-in fails because the profile
  could not be read or the account could not be saved (API-012). As a second
  line of defence, if the app's sign-in completion page finds no session, it
  shows that same failure state rather than proceeding as if signed in.
  The failure state shows no server error text.
- **Disconnecting a Google calendar disconnects the calendar.** After a user
  whose calendar is Google disconnects it they are still signed in, on the same
  page, and the calendar shows as not connected.
- **Disconnect no longer signs anyone out, and the app stops claiming a
  disconnect it cannot perform.** For a user whose calendar is Outlook or
  webcal, disconnect today removes nothing of that calendar (§2.4); fixing that
  is OQ-5 and out of scope. After this change such a user stays signed in, their
  calendar connection is unchanged and still shows as connected, and the app
  does not offer them a disconnect that would do nothing: it tells them that
  disconnecting this kind of calendar is not available yet. At no point is a
  "disconnected" confirmation shown over a calendar that still shows as
  connected (D-6). A disconnect request made for such a user anyway deletes
  nothing: any Google sign-in credential they hold is kept, Google Meet stays
  as connected as it was, and the server's answer does not report that a
  disconnect took place (D-8).
  (revision 3) That answer is one the app treats as a failure, never as a
  success and never as "server unreachable". So if the app itself sends a
  disconnect for such a user, for example because its view of the calendar is
  out of date after the provider changed in another tab, neither place that
  offers disconnect shows a "disconnected" confirmation, the app does not switch
  to preview data, and the user is told that disconnecting this kind of
  calendar is not available yet. Which users count as having a Google calendar
  follows the server's calendar status (D-8).
- **Logout answers in a shape both of the app's API clients accept**, with no
  body. Logout still ends the session.
- **The user's picture has one name end to end.** The picture the server already
  sends for the signed-in user is read under that one name everywhere the app
  reads the current user, and appears where the app already shows host pictures
  for the user's own scheduling links.

## 4. Acceptance criteria

AC-1. Given the server cannot create a session at the end of a Google sign-in
(signing secret empty, or token generation fails), when the provider redirects
the user back to the server, then the server does not set a session and does not
send the user to the sign-in completion page; it sends them to the app's sign-in
failure state.  [unit]
*Note (revision 1):* the empty-secret case is inducible as the code stands; a
token-generation failure with a non-empty secret is not (§2.2). AC-1..3 are
proven through the empty-secret case, and through a generation failure only if
the plan provides a seam that can force one. The plan must say which.

AC-2. Given the same failure at the end of a Microsoft sign-in, when the provider
redirects back, then the outcome is as in AC-1.  [unit]

AC-3. Given the same failure at the end of an SSO (OIDC) sign-in, when the IdP
redirects back, then the outcome is as in AC-1.  [unit]

AC-4. Given a Microsoft sign-in where the provider exchange succeeds but the
user's profile cannot be read or the user cannot be saved, when the provider
redirects back, then the user is sent to the app's sign-in failure state, not
left on a server error page and not sent to the completion page.  [unit]

AC-5. Given the user arrives at the app's sign-in failure state, when the page
renders, then a message that sign-in did not complete is visible, a way to try
again is offered, and no server error detail is shown.  [unit] (web)

AC-6. Given the app's sign-in completion page loads and the server reports that
there is no session, when the page finishes checking, then the user sees the
sign-in failure state (AC-5) instead of being sent on to the dashboard (and from
there bounced anonymously to the landing page).  [unit] (web)

AC-7. Given an email domain with an OIDC SSO provider configured, when the user
enters an email at that domain in the sign-in dialog, then the browser is sent
to the SSO start destination that the server's detection answer names, and not
to the password step. The destination is resolved against the API's origin,
not the web app's own origin, so a deployment where the two differ still
reaches the server's SSO start (the test sets them to different origins).
[unit] (web) — and [e2e] up to the point the browser leaves for the IdP, if
OQ-2 resolves that way.

AC-8. Given an email domain with an OIDC SSO provider configured, when the user
enters `alice@<domain>` and the server sends the browser on to the IdP, then the
IdP authorization request carries a login hint equal to `alice@<domain>`.
[unit]

AC-9. Given a user who enters an email that routes to Google, when the server
sends the browser to Google, then Google's authorization request carries a login
hint equal to that email. The same holds for Microsoft.  [unit]

AC-10. Given a sign-in start request whose hint is not a valid email address
(including one carrying extra URL parameters), when the server sends the browser
to the provider, then no hint is forwarded, no extra parameter reaches the
provider, and sign-in still starts.  [unit]

AC-11. Given a signed-in user with a connected Google calendar, when they
disconnect the calendar from the app, then the disconnect succeeds, the
calendar status reads as not connected, and an authenticated request made
immediately afterwards with the same browser session still succeeds (they
remain signed in and are not sent to the landing page). That request must be one
that does not need a calendar (reading the current user, for example), so a
"calendar not connected" refusal cannot be mistaken for a signed-out one. The
e2e journey uses its own seeded user, so removing that user's calendar link
takes nothing away from another journey.  [e2e] and [unit]
(the server response to a disconnect does not expire the session).

AC-12. Given a signed-in user, when they log out, then the response has no body,
is classified by the strict API client as success (does not throw), the session
is ended (a following authenticated request is refused), and the existing e2e
logout journey still passes.  [contract] and [unit] (web)

AC-13. Given a signed-in user whose account has a non-empty picture, when the app
reads the current user, then that picture is available under the single name
the contract defines, in both places the app reads the current user.  [unit]
(web)

AC-14. Given a signed-in user with a non-empty picture who owns a scheduling
link, when the link's hosts are displayed, then the owner's picture is shown
rather than initials.  [unit] (web)

AC-15. Given the touched operations, when the contract gate runs, then no
operation this feature touches carries an unresolved `x-uncertain`
(specifically the uncertainty on the current user's login-provider value), and no contract text still
describes the hint as discarded, the session failure as silent, or logout as
200-with-empty-body. In addition, the contract fails this criterion while either
of the following still holds:
- the Microsoft sign-in return still documents a failed profile read or a failed
  account save as a non-redirect error response (any error status, or any
  description saying every failure is a non-redirect response) rather than as a
  redirect to the failure state;
- calendar disconnect still says, in its summary, description or response
  headers, that it clears the session, logs the user out or sets a cookie.
The check is a contract review against these two lists plus the earlier three,
done by contract-challenger and recorded in the contract PR.  [contract]

AC-16. Given the final commits in both repos, when `api-gate`, `web-gate` and
`e2e-gate` run, then each prints `rc=0`.  [gate]

AC-17. Given a signed-in user whose calendar is Outlook, or webcal, and shows as
connected, when they view their calendar connection in the app, then no
disconnect action is offered for it and the app says that disconnecting this
kind of calendar is not available yet; and when a disconnect request is made on
that user's behalf anyway (bypassing the app), then they remain signed in, their
calendar status still reads connected with the same provider, their stored
calendar connection is unchanged, and no "disconnected" confirmation is shown.
"When they view their calendar connection in the app" means every place the
app offers a calendar disconnect today: the settings page's calendar section
and the dashboard's calendar connection card. Each is asserted separately.
(revision 2) The server half is asserted on a user of each kind (Outlook, and
webcal) who **also holds a stored Google sign-in credential**, seeded before the
request. After the request: that Google credential is still stored and
unchanged; Google Meet's connected state reads exactly as it did before the
request (connected, when Google is configured on the server); the Outlook
credential or webcal link is unchanged; and the server's answer does not report
a successful disconnect (D-8). The test fails today because the Google
credential is deleted, Google Meet turns not connected, and the session is
ended. For the Outlook user, Teams' connected state also reads exactly as it
did before the request.
(revision 3) The web half also covers the app receiving that answer. On each of
the two surfaces, given the app still shows the calendar as Google and offers
disconnect (a stale status), when the user disconnects and the server returns
the D-8 answer, then: the app's API client classifies it as a failure (neither
a success nor "server unreachable"); no success confirmation is shown; the
session does not switch to preview data and no preview disconnect runs; and the
user sees that disconnecting this kind of calendar is not available yet. The
test fails today because both surfaces show their success toast for a success
answer, and a non-JSON success answer switches the session to preview data.
[unit] (server: session kept, status unchanged, Google credential kept, Google
Meet and Teams state unchanged, answer not a success) and [unit] (web: on both
surfaces, no action offered, honest message, no success message; and on both
surfaces, receiving the D-8 answer is a failure with no success message and no
switch to preview data).

AC-18. Given each of the five auth operations named in API-092's non-admin
share (sign-in method detection, SSO start, SSO sign-in return, current-user
read, logout), when the backend tests run, then every response status the
contract documents for those five operations after the contract stage has a
passing Go assertion on its status and body shape. This includes, at least: the
SSO detection answer and the non-SSO answer; the SSO start's redirect (with and
without a valid hint), its unknown-domain answer, its SAML answer and its
setup-failure answer (these last two pin API-060's current behaviour, D-5); the
SSO return's failure redirect (AC-3), state mismatch and success; the current
user's success, signed-out and stale-user answers; logout's success (AC-12) and
signed-out answers. A status that cannot be induced without a test seam is named
in the plan together with the seam that induces it; none is left unasserted.
[unit]

## 5. Explicitly out of scope

- **Other callback failure branches** (state mismatch, token exchange, Google
  profile fetch, upsert, token save, OIDC exchange, domain mismatch) still end on
  a server error page on the API origin. They are not silent-success defects,
  so they are not API-011/012; routing them to the failure state is a sensible
  follow-up but widens this change across every branch of three handlers. See
  OQ-1.
- **Disconnect for Outlook and webcal users** does not remove their calendar
  link today (§2.4). Fixing what disconnect *removes* is a separate defect and
  a separate behaviour change, and it deletes stored credentials; this spec only
  stops disconnect from removing the session and stops the app from claiming a
  disconnect it does not perform (AC-17, D-6). To be filed as its own issue
  (OQ-5).
- **API-060's fix** (refusing SAML and incomplete OIDC rows at create time).
  That is a change to SSO-provider administration, which PAC-22 owns (D-5).
- **API-092's SSO-provider administration share** (PAC-22, D-7).
- **Logout with an expired session** returning 401 (§2.5). The web app already
  clears local state; changing auth on logout is a separate decision.
- **Rendering the user's picture in new places** (header, account menu). This
  spec makes the picture available under one name and fixes the one existing
  display that should already show it (AC-14). New UI is a design decision.
- **SAML SSO.** The server answers "not yet supported" for SAML providers; the
  SSO redirect fix sends such users to that answer, which is no worse than today.
  See OQ-4.
- **Driving the real provider consent screens in e2e.** Still blocked on the
  seam gap the e2e suite already records (item B of its seam list); AC-1..4, 8, 9 are unit-proven.
- **Rate limiting of detection** (API-062) — separate open finding.

## 6. What must not change

- **The account signed in is the one the provider authenticated.** A login hint
  never selects, creates or links an account. The SSO domain check that refuses
  an IdP-asserted email outside the provider's domain stays
  (`backend/api/handlers_sso.go:134-141`, PAC-45). No test today asserts the hint
  is ignored for identity — AC-10 adds the format guard; the identity rule needs
  its own assertion in the plan.
- **State-cookie checks** on all three callbacks stay as they are
  (`TestCallback_InvalidState`, `handlers_auth_test.go:80`; no equivalent test
  exists for the Microsoft and SSO callbacks — a finding).
- **Successful sign-in** still lands on the completion page and then the
  dashboard, with the `provider` query values the Microsoft and SSO callbacks
  send today. No test covers the successful callback end to end (seam gap).
- **Every cookie goes through the secure cookie helper**
  (`pac20_cookies_test.go:134`).
- **Detection rate limiting and its keying** (`ratelimit_detect_test.go`,
  PAC-39).
- **Logout ends the session** (`e2e/tests/auth-session.spec.ts:101`).
- **Anonymous `/app` bounces to the landing page**
  (`auth-session.spec.ts:75`).
- **The demo fallback** in the sign-in dialog when the backend is unreachable
  (`AuthDialog.tsx:118-121`). No test protects it; the plan should name one or
  record that none exists.
- **Disconnect still removes the Google calendar link** for a Google user
  (`TestAuthDisconnect` asserts only the status code today). As today, removing
  it also turns Google Meet to not connected for that user, because Google Meet
  depends on the same stored credential (§2.4); AC-11 does not change that, and
  no test protects it today.

## 7. Rollback

No migration and no stored data change; revert is a code revert in each repo.

- Reverting the backend alone restores: silent session failure, ignored hints,
  disconnect logging out, logout 200. The web app keeps working: it already
  tolerates logout 200 via the lenient wrapper; following the SSO redirect works
  against the current server because the server already sends the redirect
  destination today; the failure-state page simply stops being reached from the
  server.
- Reverting the web app alone restores the SSO dead end and the missing picture;
  the backend fixes still hold.
- Order of deploy is the factory's fixed order (contract → backend → web). The
  only cross-repo coupling to check in the plan is the name of the picture field
  if the contract stage renames it rather than keeping the server's current name:
  a rename makes the web change depend on the backend change and must deploy
  together, as PAC-24 did (`docs/specs/PAC-24.md`, Decisions OQ-3).

## 8. Open questions

- **OQ-1 — Scope of "failure state" across callback branches.** Should every
  post-provider failure branch (§5 first bullet) route to the failure state in
  this change, or only the silent-success ones (API-011) and the Microsoft
  profile/upsert path (API-012)? Owner: product owner. Factory assumption below.
- **OQ-2 — e2e for SSO.** Proving AC-7 end to end needs an SSO provider row in
  the e2e seed and either a fake OIDC issuer (issuer discovery runs on the SSO
  start route, `handlers_sso.go:64-72`) or stopping the browser at the server's
  redirect without following it. Owner: e2e-author. Resolve in the plan; if
  neither is acceptable, AC-7 stays unit-only and the gap is listed in
  `e2e/README.md`.
- **OQ-3 — Where the hint for SSO comes from.** Either the web app passes the
  typed email to the SSO start destination, or the server's detection answer
  already includes it. This is an interface choice; owner: contract-author. The
  spec only requires AC-8.
- **OQ-4 — SAML domains.** With the redirect fixed, a SAML-configured domain
  will now land on the server's "not yet supported" answer instead of the
  password step. Is that acceptable, or should detection not report SAML domains
  as SSO until SAML exists? Owner: product owner.
- **OQ-5 — Disconnect for Outlook/webcal users** (§2.4). Needs its own Linear
  issue. Owner: triage.
- **OQ-6 — Picture field name.** Keep the server's current name and change the
  web app, or rename on the server? The audit and this spec require one name,
  not which one. Owner: contract-author (README §7 rule 2: the contract
  describes what is, so keeping the server's name is the smaller change).

## Decisions

*Assumed by the factory, pending owner confirmation (2026-10-26). These follow
the factory rule of choosing the most conservative option consistent with
PRODUCT.md and earlier owner decisions.*

- **D-1 (OQ-1) — Narrow failure-state scope.** Only the paths that today end in a
  misleading success (all three callbacks when the session cannot be created)
  and the Microsoft profile/upsert failure named by API-012 route to the
  failure state. Other branches keep their current server error responses and
  become a follow-up issue. Assumed by the factory, pending owner confirmation.
- **D-2 — What the failure state is.** The user is returned to the app's public
  entry point with a short, plain message that sign-in did not complete and a
  way to start sign-in again. No provider name, error code or server detail is
  shown (consistent with PAC-21's rule that error detail stays in logs). No new
  page is required by the spec. Assumed by the factory, pending owner
  confirmation.
- **D-3 (OQ-4) — SAML.** Detection behaviour for SAML domains is unchanged by
  this work; such users now reach the server's "not yet supported" answer. This
  is no worse than today (they could not sign in either way) and changes no
  data. Stated plainly (revision 1): that answer is a raw JSON error on the API
  origin with no way back into the app, the same kind of dead end §2.2
  criticises, and this spec accepts it for SAML domains until API-060 is fixed
  (D-5). Assumed by the factory, pending owner confirmation.
- **D-4 — Hint is advisory only.** The login hint is forwarded only when it is a
  syntactically valid email, and it never influences which Paceday account is
  signed in. Assumed by the factory, pending owner confirmation.
- **D-5 (revision 1) — API-060 stays with PAC-22.** API-060's defect is that
  SSO-provider administration accepts rows that cannot work; its fix is a
  validation change on the create operation, which PAC-22 claims and this spec
  does not touch. This spec touches the SSO start route only to forward a hint,
  and changes neither its SAML answer nor its setup-failure answer; AC-18 pins
  both in Go so PAC-22's change to the create side is the only thing that can
  move them. Fixing API-060 here would mean editing an administration operation
  outside this spec's problem and racing PAC-22 on the same code. If PAC-22 is
  abandoned, API-060 returns to triage rather than becoming silently orphaned.
  Assumed by the factory, pending owner confirmation.
- **D-6 (revision 1) — Outlook and webcal users after this change.** They stay
  signed in; their calendar connection is left exactly as it is (nothing of
  theirs is deleted); the app does not offer them a disconnect and says it is
  not available yet for that kind of calendar. This is the most conservative
  outcome: it deletes no data, widens no visibility, and removes the false
  "Disconnected" message the challenge identified. It does not give those users
  a way to stop Paceday reading their calendar, which they also do not have
  today (today they are logged out and the calendar stays connected); that is
  OQ-5. If the owner judges this unacceptable, OQ-5 moves into scope. Assumed by
  the factory, pending owner confirmation.
  *Revision 2*: "nothing of theirs is deleted" holds only together with D-8.
  Today a disconnect request for such a user deletes any Google sign-in
  credential they hold (§2.4); under D-8 it no longer does, so after this change
  a disconnect request deletes nothing of theirs, the Google credential
  included.
- **D-7 (revision 1) — API-092 is split.** The non-admin share (five paths) is
  resolved here by AC-18; the `/api/admin/sso` share stays with PAC-22. The
  backend implementer of PAC-35 marks the row "Partly fixed (PAC-35)" with the
  non-admin paths struck when AC-18's tests merge; PAC-22's implementer closes
  the rest. Owner of the row edit: PAC-35 backend implementer. Assumed by the
  factory, pending owner confirmation.
- **D-8 (revision 2) — A disconnect request for a user whose calendar is not
  Google deletes nothing, the Google credential included.** For a user whose
  calendar provider is Outlook or webcal, a disconnect request keeps any stored
  Google sign-in credential, so Google Meet, booking and the calendar engine see
  the same credential before and after; the session is kept (AC-17); and the
  server's answer tells the caller that no disconnect was performed for this
  kind of calendar rather than reporting success (the shape of that answer is
  the contract stage's choice). Disconnect for a Google-calendar user is
  unchanged apart from keeping the session (AC-11). Options considered: (a) keep
  deleting the Google credential for these users, which is today's behaviour but
  deletes user data the user did not ask to remove (they asked to disconnect an
  Outlook or webcal calendar, which it does not do) and silently turns Google
  Meet off; (b) keep it, chosen. (b) follows the factory's conservative rule
  (never choose an option that deletes user data) and D-6's aim that the app
  stop claiming a disconnect it does not perform. It widens no visibility: the
  credential stays exactly where and as it was. Consequence stated plainly: such
  a user still has no way, through disconnect, to remove a stored Google
  credential; removing credentials for non-Google users is OQ-5's issue.
  Assumed by the factory, pending owner confirmation.
  *Revision 3*: the "not performed" answer must be one the app's API client
  classifies as a failure: not as a success, and not as "server unreachable"
  (which would switch the session to preview data, §2.4). Its exact shape is
  still the contract stage's choice within that constraint, and AC-17's web
  half pins the app's handling of it. "A user whose calendar is Google" means
  what the server's calendar status reports as Google (a blank or unrecognised
  provider included, §2.4), so the server and the app classify every user the
  same way. Assumed by the factory, pending owner confirmation.

## Challenge — 2026-10-26

Reviewed: commit `54c5458` on `factory/pac-35-spec`, against clockwise-like
`origin/main` `7de7044` and smart-calendar-flow `origin/main` `4ded854`, and
the Linear issue PAC-35 text. Reviewed by spec-challenger, which did not write
this spec.

**Verdict**: accept-with-changes

### Blocking

1. **Inherited scope omits two open audit findings on operations this spec
   touches (README §7 rule 1).**
   - **API-092** (`docs/factory/api-audit.md:350`, low, Reported): "No Go test
     coverage" for `/api/auth/detect`, `/api/auth/sso/{domain}`,
     `/api/auth/callback/oidc/{domain}`, `/api/auth/me`, `/api/auth/logout`
     (plus `/api/admin/sso`). Every one of those five non-admin paths is touched
     here (detection is followed, the SSO start gains a hint, the OIDC callback
     gains a failure redirect, the current-user response is reworked by API-065,
     logout changes status). PAC-22 claims only the `/api/admin/sso` share of
     the row (`docs/specs/PAC-22.md:7-8`, `:589-593`), so the non-admin share
     belongs to whoever touches those paths, which is this spec. Today AC-12
     proves logout only through the contract and the web client; no AC requires a
     Go assertion on the logout handler, the SSO start route or `/api/auth/me`.
   - **API-060** (`api-audit.md:318`, medium, Reported): SAML rows are accepted
     and the SSO start route then answers 501; an `oidc` row with an empty issuer
     fails only as a 500 at the start route. The start route is touched (AC-8)
     and D-3 decides SAML behaviour without naming the finding. PAC-22 (still a
     draft, not merged) claims API-060.

   *Resolves it*: list both in "Inherited scope". For API-092, either add
   acceptance criteria that put a passing Go assertion on the statuses this spec
   changes or relies on for the five non-admin paths, or record (with an owner)
   that the row is split and which part stays open. For API-060, state the
   position: either it is resolved here, or it stays with PAC-22 and the reason
   rule 1 does not apply to this touch is recorded under Decisions as assumed by
   the factory, pending owner confirmation.

2. **AC-15 and the "Open contract notes" list leave contract text that this
   spec makes false.** AC-15 checks only that no text still describes the hint
   as discarded, the session failure as silent, or logout as 200. After AC-4 and
   AC-11 the following would still pass AC-15 and be false:
   - `microsoftOAuthCallback`: the `302` description says "Every failure is a
     non-302 response below", the `502` response ("The Graph profile could not be
     read … Added by PAC-24") and the `500` description's upsert-failure clause
     (`contracts/openapi/paths/auth.yaml:178-213`). AC-4 turns those two failures
     into redirects to the failure state. The operation description at
     `auth.yaml:160-168` ("a failed fetch is a 502") is false too.
   - `disconnectCalendar`: summary "Delete the stored calendar OAuth token and
     clear the session cookie", the description "this also logs the user out",
     and the `Set-Cookie` header on the `204` (`auth.yaml:414-428`). AC-11 makes
     all three false.

   README §7 rule 2 says a lying contract is worse than none.
   *Resolves it*: name both operations under "Open contract notes", and extend
   AC-15 so it fails while either of them still describes the old behaviour.

3. **§3 promises a disconnect outcome that the rest of the spec excludes for
   some users, and the change makes those users' result worse than it looks.**
   §3 says, with no qualifier: "After a user disconnects their calendar they are
   still signed in, on the same page, and the calendar shows as not connected."
   §2.4 and §5 say disconnect removes only the Google token row
   (`backend/auth/token_store.go:53-59`), so for an Outlook or webcal user the
   calendar does *not* show as not connected: `GET /api/auth/status` answers from
   the Microsoft token or the webcal URL (`backend/api/handlers_auth.go:97-111`),
   neither of which is removed. The web app shows the Disconnect button to those
   users (`smart-calendar-flow/src/components/WorkCalendarConnection.tsx:125-134`,
   shown whenever `status.connected`) and toasts "Disconnected" (`:88`). Today
   that user is at least logged out. After this change they stay signed in on a
   page that says "Disconnected" while the indicator stays green and Paceday
   keeps reading their calendar. §1 names "to stop Paceday reading it" as a
   reason users disconnect. AC-11 covers only Google, so nothing tests this
   outcome.

   *Resolves it*: scope the §3 statement to Google-calendar users, and state the
   observable outcome for Outlook and webcal users after this change as a
   Decision (assumed by the factory, pending owner confirmation). Add an
   acceptance criterion that pins that outcome, whatever it is, so it is a
   chosen behaviour and not an accident. If the owner judges a "Disconnected"
   message over a still-connected calendar unacceptable, OQ-5 moves into scope.

### Non-blocking

1. **AC-7 does not say which origin the SSO destination is resolved against.**
   The contract says `redirect_url` is a "Relative path `/api/auth/sso/<domain>`"
   (`auth.yaml:808-811`). The web app talks to the API through a base that can
   differ from its own origin (`smart-calendar-flow/src/lib/api.ts:19-22`,
   `VITE_API_URL`). A web test asserting the browser went to exactly
   `redirect_url` would pass while a split-origin deployment sends the user to
   the web app's own `/api/...` path. §7's claim that following the redirect
   "works against the current server" depends on this. AC-7 should say the
   destination is on the API's origin.
2. **AC-1's "or token generation fails" clause cannot be induced as the code
   stands.** HS256 signing with a non-empty key (`backend/auth/jwt.go:17-27`)
   has no failure a unit test can trigger. As written, AC-1 is proven through
   the empty-secret case only. Say that, or say that the second case needs a
   test seam the plan must provide.
3. **AC-6 covers only "the server reports that there is no session".** The
   completion page's re-read also returns normally, and the page navigates on to
   `/app`, when `/api/auth/me` answers 404 "user not found" (`auth.yaml`
   `getCurrentUser` 404; `AuthContext.tsx:64-66` treats it as stale) and on 5xx
   or network failure. The spec should say what the completion page shows in
   those cases. "Same as today" is an acceptable answer if it is written down.
4. **AC-11's "authenticated request made immediately afterwards" needs to be a
   request that does not need a calendar.** Since PAC-21 the calendar-dependent
   operations answer `401 {"error":"calendar not connected"}` for a user with no
   calendar (`e2e/tests/calendar.spec.ts:37-45`). That 401 looks like a signed-out
   401 unless the body is read, which makes a status-only e2e assertion
   ambiguous. Also, the e2e journey deletes a seeded token row
   (`e2e/seed/seed.sql`), so it must not take away a credential another journey
   depends on.
5. **The Google start route forces the consent screen** (`oauth2.ApprovalForce`,
   `backend/auth/google_oauth.go:25`). Forwarding the hint fixes which account
   is picked, which is the harm §1 item 3 names. The user still sees a Google
   screen every time. That is fine, but it should be stated so AC-9 passing is
   not read as "no provider screen".
6. **An SSO hint whose domain differs from the start route's domain** (for
   example `/api/auth/sso/acme.com` with a hint at another domain) is not
   addressed. D-4 and the PAC-45 domain check (`handlers_sso.go:134-141`) keep
   identity safe, so this is advisory only. AC-10 could still state whether such
   a hint is forwarded.
7. **§6 records two protections with no test but no AC**: state checks on the
   Microsoft and SSO callbacks ("a finding") and "the hint never selects an
   account" ("needs its own assertion in the plan"). Things the spec says must
   not change are only gated if an acceptance criterion requires them. Consider
   promoting both to ACs.
8. **D-3 sends SAML users to a raw `501` JSON page on the API origin**
   (`handlers_sso.go:56-58`), the same dead end §2.2 criticises for the
   Microsoft callback. D-3 is a reasonable conservative choice. The spec should
   say plainly that this outcome is accepted.
9. **Bundling.** Seven findings in one spec, and §7 shows they are independently
   revertable. The issue and audit batch D5 ask for one change, and shipping half
   leaves nothing incoherent beyond the picture-name coupling §7 already names.
   No split is requested.

### Criteria I could not falsify

- None outright. Each of AC-1…AC-15 has a test that fails on `7de7044` /
  `4ded854` and can pass afterwards. Exceptions: AC-1's "token generation fails"
  half (non-blocking 2), and AC-16, which is a gate run rather than a
  behavioural criterion.

### Citations I checked and found wrong

- None found. I checked against the two commits named in §2:
  `sso_detect.go:18-22,42-46`, `routes.go:41,56-61`,
  `handlers_auth.go:24-39,42-79,135-153`, `handlers_microsoft_auth.go:17-38,41-81`,
  `handlers_sso.go:49-87,94-151`, `main.go:169-172`, `jwt.go:17-27`,
  `token_store.go:53-59`, `google_oauth.go:24-26`, `microsoft_oauth.go:35-37`,
  `oidc.go:61-63`, `handlers_me.go:31-41`, `users.go:14`,
  `auth.yaml:45-54,132,750,877,938`, `AuthDialog.tsx:32-35,104,108,111-121`,
  `AuthCallback.tsx:15-31` (no reader of `error=auth_failed` in `src/`),
  `AuthContext.tsx:16-21,48-62,128-133`, `ConnectionStatus.tsx:15-26`,
  `WorkCalendarConnection.tsx:84-95`, `schedulingLinks.ts:56,79-81,452,510`,
  `HostAvatars.tsx:57-58`, `contract.test.ts:84-87`,
  `handlers_auth_test.go:35-53,55,80`, `auth-session.spec.ts:75,101`,
  `e2e/README.md:152`, `SEAM-REQUIRED.md` item B. The audit row's `GET` for
  detection is indeed wrong (`api-audit.md:268`), as §2.1 says.

### Consumers this breaks

- clockwise-like `contracts/openapi/paths/auth.yaml:178-213` (and the generated
  `backend/api/gen/paceday.gen.go`, smart-calendar-flow
  `src/api/generated/*`): the Microsoft callback's documented `500`/`502`
  failure responses change to a redirect. See blocking 2.
- smart-calendar-flow `src/components/WorkCalendarConnection.tsx:84-95`: for
  Outlook and webcal users it now shows "Disconnected" and leaves them signed in
  over a still-connected calendar. See blocking 3.
- smart-calendar-flow `src/contexts/AuthContext.tsx:16-21` and
  `src/api/schedulingLinks.ts:56`: these must change together with the backend
  if OQ-6 renames the field. §7 already records this.
- Checked and unaffected: `mcp/tools.go:243` uses only `GET /api/auth/status`,
  which this spec does not change. `backend/api/handlers_auth_test.go:35`
  (`TestAuthDisconnect`) asserts only the 204 status.
  `e2e/tests/auth-session.spec.ts:101` is preserved by AC-12.

## Revision 1 — 2026-10-26

Answers to the round-1 challenge above. Author: spec-author.

### Blocking

1. **API-092 and API-060 missing from inherited scope.** Both are now listed.
   - API-092: the non-admin share (the five paths) is resolved here by the new
     AC-18, which requires a passing Go assertion for every status the contract
     documents on those five operations, naming the ones this spec changes or
     relies on. Some coverage has appeared since the audit was written: the
     OIDC callback's domain check (`backend/api/handlers_sso_test.go:138-172`,
     PAC-45), the detection limiter (`ratelimit_detect_test.go`, PAC-39) and the
     logout cookie's flags (`pac20_cookies_test.go:67`). None of them asserts
     the statuses AC-18 lists, so the row is still open for this share. The row
     split, and who edits the audit row, is D-7.
   - API-060: position stated as D-5. It stays with PAC-22, because its fix is
     on the SSO-provider create operation, which this spec does not modify. This
     spec does not change the start route's 501/500 answers, and AC-18 pins them
     in Go. D-3 now names the SAML dead end and says it is accepted (this also
     answers non-blocking 8).
2. **Contract text left false.** `microsoftOAuthCallback` and
   `disconnectCalendar` are now named under "Open contract notes" with the exact
   sentences, response and header that become false. AC-15 is extended so it
   fails while the Microsoft sign-in return still documents profile or upsert
   failure as a non-redirect response, or while disconnect still describes
   clearing the session or carries the cookie header.
3. **§3 disconnect promise for Outlook and webcal users.** §3 is now limited to
   Google calendar users. A second §3 bullet and D-6 record the outcome for
   Outlook and webcal users: they stay signed in, nothing of theirs is deleted,
   their calendar still shows as connected, no disconnect is offered, and the
   app says it is not available yet. The new AC-17 pins this on both the server
   and the web side, so it is a chosen behaviour and not an accident. OQ-5
   stays out of scope because it deletes stored credentials. D-6 says it moves
   in if the owner rejects this outcome.

### Non-blocking taken in this revision

- **1 (origin)**: AC-7 now requires the destination to be resolved against the
  API's origin, tested with the two origins different.
- **2 (token-generation failure)**: AC-1 carries a note that it is proven
  through the empty-secret case. A generation failure is proven only if the plan
  provides a seam, and the plan must say which.
- **4 (follow-up request)**: AC-11 now requires a request that needs no calendar,
  and an e2e user of its own.
- **8 (SAML dead end)**: answered in D-3, see blocking 1.

### Non-blocking not taken, with reason

- **3 (completion page on 404/5xx)**: recorded as a choice, not widened. The
  auth context treats a stale-user 404, a 5xx and a network failure alike as
  "temporarily unavailable", not as signed out (`AuthContext.tsx:56-66`). AC-6
  covers only the signed-out answer. In those other cases the completion page
  behaves as it does today. The plan may widen this, but this spec does not
  require it.
- **5 (Google forces the consent screen)**: noted here. AC-9 passing means the
  right account is preselected, not that no Google screen appears.
- **6 (SSO hint at another domain)**: advisory. D-4 and the PAC-45 check keep
  identity safe. The contract stage decides whether such a hint is forwarded.
- **7 (promote §6 protections to ACs)**: partly done. AC-18 now asserts the
  SSO return's state mismatch in Go. The Microsoft callback's state check and
  "the hint never selects an account" remain plan obligations (§6).
- **9 (bundling)**: no split requested. None made.

## Challenge — 2026-10-26 (round 2)

Reviewed: commit `52e91ac` on `factory/pac-35-spec` (revision 1), against
clockwise-like `origin/main` `7de7044` and smart-calendar-flow `origin/main`
`4ded854`. Reviewed by spec-challenger, which did not write this spec. Scope:
whether revision 1 resolves the round-1 blocking findings, and the new text it
added (§3 second disconnect bullet, AC-7 origin clause, AC-11 rewrite, AC-15
extension, AC-17, AC-18, D-5, D-6, D-7).

**Verdict**: accept-with-changes

Round-1 blocking 1 (API-092, API-060) and blocking 2 (false contract text) are
resolved: both findings are in "Inherited scope" with a position, and AC-15 now
fails on the exact `microsoftOAuthCallback` and `disconnectCalendar` text I
re-read at `auth.yaml:160-213` and `:414-428`. Round-1 blocking 3 is resolved
for the web side, but the server side of its answer (D-6, AC-17) introduces the
finding below.

### Blocking

1. **D-6 says "nothing of theirs is deleted" for Outlook and webcal users, but
   disconnect deletes their stored Google credential, and AC-17 does not decide
   whether it still may.** Disconnect runs
   `DELETE FROM oauth_tokens WHERE user_id = $1`
   (`backend/auth/token_store.go:53-59`). That table holds the Google OAuth
   token; the Microsoft credential is on the settings row
   (`backend/auth/microsoft_oauth.go:49`, `:75`). The calendar status picks its
   branch from the settings provider, not from which tokens exist
   (`backend/api/handlers_auth.go:92-111`). So a user who signed in with Google
   (the Google callback always stores the token, `handlers_auth.go:68-71`) and
   then set their calendar to webcal (or later signed in with Microsoft under
   the same email, which makes Outlook their provider, `auth.yaml:160-168`)
   reads as a webcal or Outlook user *and* holds a Google token. That token is
   used elsewhere: Google Meet shows as connected only while it exists
   (`backend/api/handlers_conferencing.go:199-204`), and booking and the
   calendar engine load it (`handlers_booking.go:88`, `engine/cal_iface.go:33`).
   A disconnect request made for this user deletes it today.
   - D-6 ("their calendar connection is left exactly as it is (nothing of
     theirs is deleted)") is therefore false unless disconnect's server
     behaviour changes for these users, and the spec names no such change.
   - AC-17 ("their stored calendar connection is unchanged") does not
     distinguish the two outcomes: an implementation that keeps deleting the
     Google token passes it (the webcal or Outlook connection is untouched), and
     so does one that stops. Two implementations that differ in whether user
     data is deleted both pass.
   - §2.4's account ("a user whose calendar is Outlook or webcal is logged out
     but their calendar stays connected") omits this deletion.

   *Resolves it*: state in §2.4 that disconnect deletes the user's stored Google
   credential whatever their calendar provider, and what else depends on it.
   Then choose, as a Decision (assumed by the factory, pending owner
   confirmation), whether a disconnect request for a user whose calendar is not
   Google still deletes that credential, and make AC-17 assert the chosen
   outcome on the stored Google credential and on the Google Meet connected
   state. If the choice is "still deleted", D-6's "nothing of theirs is deleted"
   must be reworded to match.

### Non-blocking

1. **AC-17's web half should name both surfaces.** The app shows a Disconnect
   action in two places: Settings (`src/components/WorkCalendarConnection.tsx:125-134`)
   and the Dashboard card (`src/components/ConnectionStatus.tsx:62-71`, mounted at
   `src/pages/Dashboard.tsx:579`). The Dashboard card reads `provider` nowhere
   and toasts "Disconnected from Google Calendar" (`ConnectionStatus.tsx:19`)
   for every user. "When they view their calendar connection in the app" is
   satisfied by a test of the Settings surface alone, leaving the Dashboard
   card offering the action AC-17 forbids.
2. **AC-11 leaves the Google Meet side effect unstated.** For a Google user,
   disconnect also flips Google Meet to not connected
   (`handlers_conferencing.go:204`), as it does today. That is no change, but
   §3 ("disconnecting a Google calendar disconnects the calendar") reads as if
   only the calendar is affected. One sentence in §6 would fix the reading.
3. **AC-15 is a review, not a test.** It is falsifiable by the
   contract-challenger reading the five listed texts, which is what the AC says;
   noted only so nobody looks for an automated check.

### Criteria I could not falsify

- AC-17 server half, as worded: see blocking 1 (it cannot distinguish
  deleting the Google credential from keeping it).
- AC-16: a gate run, not a behavioural criterion (unchanged from round 1).
- All others (AC-1..15, AC-18) have a test that fails on `7de7044` / `4ded854`
  and can pass afterwards. AC-1's generation-failure half is now explicitly
  seam-dependent, which is acceptable.

### Citations I checked and found wrong

- None found in revision 1's new text. Checked:
  `backend/api/handlers_sso_test.go:138-172` (PAC-45 domain checks),
  `backend/api/pac20_cookies_test.go:67` (logout cookie entry),
  `contracts/openapi/paths/auth.yaml:160-213`, `:414-428`, `:750`,
  `smart-calendar-flow/src/contexts/AuthContext.tsx:56-66` (404, 5xx and
  network failure all set `sessionStale`, only 401/403 sign out), and
  `src/lib/api.ts:93` (the strict path returns `undefined` on 204, so AC-12 is
  reachable). §2.4 is incomplete rather than wrong: see blocking 1.

### Consumers this breaks

- clockwise-like `backend/api/handlers_conferencing.go:199-204`: Google Meet's
  connected state for a webcal or Outlook user depends on whether disconnect
  still deletes their Google token. Unbroken today; undefined under AC-17. See
  blocking 1.
- smart-calendar-flow `src/components/ConnectionStatus.tsx:15-26`, `:62-71`:
  must change for AC-17, and is not named. See non-blocking 1.
- Checked and unaffected: `e2e/tests/auth-session.spec.ts:100-111` asserts the
  post-logout bounce, not the 200, so AC-12's status change does not break it;
  `mcp/` has no caller of disconnect, logout, `/api/auth/me` or detection.

## Revision 2 — 2026-10-26

Answers to the round-2 challenge above. Author: spec-author.

### Blocking

1. **D-6's "nothing of theirs is deleted" was false for Outlook and webcal
   users holding a Google token, and AC-17 did not decide the outcome.**
   Citations re-read and confirmed: `backend/auth/token_store.go:53-59` (delete
   keyed on the user only), `backend/api/handlers_auth.go:92-111` (status
   branches on the settings provider), `:68-71` (Google sign-in always stores the
   token), `backend/api/handlers_conferencing.go:199-204` (Google Meet connected
   only while the token exists), `backend/auth/microsoft_oauth.go:73-77`
   (Microsoft credential on the settings row).
   - §2.4 now states that disconnect deletes the stored Google credential
     whatever the calendar provider, and names what depends on it (Google Meet,
     booking, the calendar engine).
   - New **D-8** (assumed by the factory, pending owner confirmation): for a
     user whose calendar is not Google, a disconnect request no longer deletes
     the Google credential, and the server's answer does not report success.
     Chosen because the factory never picks the option that deletes user data;
     the rejected option (keep deleting) is recorded with its reason.
   - **AC-17** now asserts the outcome on a seeded Outlook user and a seeded
     webcal user who each also hold a Google credential: the credential is
     still stored and unchanged, Google Meet's connected state is unchanged, and
     the answer is not a success. An implementation that keeps deleting the
     token now fails AC-17.
   - **D-6** is not reworded to say the token is deleted, because under D-8 it
     is not. A revision-2 note under D-6 ties "nothing of theirs is deleted" to
     D-8 and says it was false before this change. §3's second disconnect bullet
     states the same outcome in user terms.

### Non-blocking taken in this revision

- **1 (both surfaces)**: AC-17's web half now names both places the app offers
  a disconnect today (the settings calendar section and the dashboard
  connection card) and requires each to be asserted.
- **2 (Google Meet for Google users)**: §6 now says that a Google user's
  disconnect still turns Google Meet to not connected, as today, and that no
  test protects it.

### Non-blocking not taken, with reason

- **3 (AC-15 is a review)**: no change; the AC already says it is a review
  recorded in the contract PR.

## Challenge — 2026-10-26 (round 3)

Reviewed: commit `943cfd4` on `factory/pac-35-spec` (revision 2), against
clockwise-like `origin/main` `7de7044` and smart-calendar-flow `origin/main`
`4ded854` (both still the tips of `main` at review time). Reviewed by
spec-challenger, which did not write this spec. Scope: whether revision 2
resolves the round-2 blocking finding, and the new text it added (§2.4 two
revision-2 bullets, §3 second disconnect bullet, AC-17 rewrite, D-6 note, D-8,
§6 Google Meet line).

**Verdict**: accept-with-changes

Round-2 blocking 1 is resolved. §2.4 now says the Google credential is
deleted whatever the provider. D-8 chooses to keep it, and AC-17 now fails
an implementation that keeps deleting it: the seeded Outlook and webcal users
each hold a Google credential, and the AC asserts on that credential and on
Google Meet's state. Round-2 non-blocking 1 and 2 are taken. One new finding
comes from D-8's new server answer.

### Blocking

1. **D-8 lets the contract choose a "not performed" answer that the app's two
   disconnect callers would show as a success, so §3's "at no point is a
   'disconnected' confirmation shown over a calendar that still shows as
   connected" is not guaranteed.** D-8 leaves the shape to the contract stage.
   It requires only that the answer "does not report that a disconnect took
   place". A `2xx` answer with a body saying nothing was done satisfies that
   wording. Both callers treat any resolved call as success:
   - `smart-calendar-flow/src/components/ConnectionStatus.tsx:18-19` toasts
     "Disconnected from Google Calendar".
   - `src/components/WorkCalendarConnection.tsx:87-88` toasts "Disconnected".

   The client behind them (`src/api/client.ts:949-956`, `requestApi` at
   `:375-418`) treats a response like this:
   - Any non-`2xx` throws `ApiHttpError`, and both callers then show "Failed
     to disconnect".
   - A `204` resolves.
   - Any other `2xx` without a JSON content type throws `ApiUnreachableError`
     (`:414-415`). `withFallback` (`:491-503`) then switches the whole session
     to preview data and runs the mock disconnect (`:952-954`), which also
     ends in the "Disconnected" toast.

   AC-17's web half asserts only that no action is offered. Its "anyway" half
   is server-only ("bypassing the app"). So no criterion covers the app
   receiving the D-8 answer. That can happen whenever the cached status still
   shows Google while the server holds Outlook or webcal, for example after a
   provider change in another tab. The contract stage could pick a `2xx`
   shape that meets D-8 and passes every AC, and the user would still see the
   false confirmation, or be dropped into demo data.

   *Resolves it*: state, in D-8 or AC-17, that the "not performed" answer is
   one the app's API client classifies as a failure and not as success or
   unreachable. Add to AC-17's web half that when either surface receives
   that answer, no success confirmation is shown and the session does not
   switch to preview data. Alternatively, narrow §3's "at no point" sentence
   to what the ACs actually pin, and record the remaining gap as a Decision
   (assumed by the factory, pending owner confirmation).

### Non-blocking

1. **The Outlook-and-Google-credential population is the common case, not an
   edge.** Every Microsoft sign-in sets the provider to Outlook
   (`backend/auth/microsoft_oauth.go:68`). The Google sign-in never resets it
   (`handlers_auth.go:62-79` does not touch settings). So any user who has
   ever signed in with both is an Outlook user holding a Google credential.
   D-8 handles them correctly. This is only worth a sentence in §2.4 so the
   owner can weigh D-8 knowing how many users it covers.
2. **"A user whose calendar is Google" should be defined as the status
   endpoint defines it.** The status endpoint treats a blank provider as
   Google (`handlers_auth.go:92-95`), and the web app decides from the
   `provider` that status returns. D-8 and AC-11/17 say "Google",
   "Outlook or webcal". The settings write accepts only those three values
   (`handlers_settings.go:32`, `:272`), and migration 024 normalises old rows.
   So no fourth case exists today, but the spec should say that D-8's split
   follows the status endpoint's rule. Otherwise the server and the app could
   classify a blank-provider user differently.
3. **For the Outlook user in AC-17, Teams is implied but not stated.** "The
   Outlook credential … is unchanged" implies Teams' connected state is
   unchanged (`handlers_conferencing.go:209-210`). Stating it next to the
   Google Meet assertion would make the server half complete.

### Criteria I could not falsify

- AC-16: a gate run, not a behavioural criterion (unchanged).
- All others have a test that fails on `7de7044` / `4ded854` and can pass
  afterwards. AC-17's server half now fails today, because the Google
  credential is deleted, Google Meet turns not connected, and the session
  cookie is expired (`handlers_auth.go:137`, `:142`). It can pass afterwards.
  AC-17 has no web assertion for receiving the D-8 answer (blocking 1).

### Citations I checked and found wrong

- None. I checked revision 2's new citations against `7de7044`:
  - `token_store.go:57` (delete keyed on `user_id` only)
  - `handlers_auth.go:92-111` (status branches on the settings provider,
    blank means Google)
  - `:68-71` (the Google callback always stores the token)
  - `microsoft_oauth.go:73-77` (the Outlook credential is read from the
    settings row; it is written, and the provider set to `outlook`, at `:68`)
  - `handlers_conferencing.go:199-204` (Google Meet is listed only when Google
    is configured, and is connected while the token exists)
  - `handlers_booking.go:88` (host coverage counts the Google token)
  - `engine/cal_iface.go:33` (the calendar engine loads the Google token)

### Consumers this breaks

- smart-calendar-flow `src/components/ConnectionStatus.tsx:15-26` and
  `src/components/WorkCalendarConnection.tsx:84-95`, through
  `src/api/client.ts:949-956`: both show a success toast for any `2xx`
  disconnect answer. With a non-JSON `2xx`, the session also falls into
  preview mode. See blocking 1.
- smart-calendar-flow `src/api/contract.test.ts:84-87` and the generated
  `src/api/generated/types.ts` (`disconnectCalendar`, 204 only): the new D-8
  answer adds a response to this operation, which the contract stage must
  regenerate and the test must cover. This is expected under AC-15; it is
  listed so it is not missed.
- Checked and unaffected: `ConferencingSection.tsx:226-235` disconnects Zoom
  only, not the calendar. `mcp/` has no caller of disconnect (unchanged from
  round 2).

## Revision 3 — 2026-10-26

Answers to the round-3 challenge above. Author: spec-author.

### Blocking

1. **D-8 allowed a "not performed" answer the app would show as a success, or
   treat as unreachable and switch to preview data.** Citations re-read and
   confirmed on smart-calendar-flow `origin/main` `4ded854`:
   `ConnectionStatus.tsx:18-19`, `WorkCalendarConnection.tsx:87-88`,
   `client.ts:375-418` (non-success throws an HTTP error; no-content resolves;
   other non-JSON success throws "unreachable" at `:414-415`), `:491-503`
   (`withFallback` switches to preview data on "unreachable"), `:952-954` (mock
   disconnect). Resolved with the challenger's first option, not the narrowing:
   - **D-8** now requires the answer to be one the API client classifies as a
     failure, neither success nor unreachable; the shape stays the contract
     stage's choice within that constraint.
   - **AC-17**'s web half now covers each surface receiving the D-8 answer from
     a stale Google status: failure classification, no success confirmation, no
     switch to preview data and no preview disconnect, and the "not available
     yet" message. It fails today on both surfaces.
   - **§3** states the same outcome in user terms, so its "at no point is a
     'disconnected' confirmation shown over a calendar that still shows as
     connected" is now backed by an AC rather than narrowed.
   - **§2.4** records how the client treats each kind of answer today.

### Non-blocking taken in this revision

- **1 (common population)**: §2.4 now says that any user who has signed in with
  both Microsoft and Google is an Outlook user holding a Google credential
  (`microsoft_oauth.go:68`, `handlers_auth.go:62-79`).
- **2 (who counts as Google)**: §2.4 records the status endpoint's rule (blank or
  unrecognised provider means Google, `handlers_auth.go:92-111`); D-8 and §3
  now say the split follows it.
- **3 (Teams)**: AC-17's server half now asserts that the Outlook user's Teams
  connected state is unchanged (`handlers_conferencing.go:208-210`).

### Non-blocking not taken, with reason

- None.
