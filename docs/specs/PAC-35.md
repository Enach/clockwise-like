# Spec — PAC-35: Sign-in and sign-out flows that fail, mislead or log the user out

- **Linear**: https://linear.app/paceday/issue/PAC-35
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**:
  - Audit findings (all in `docs/factory/api-audit.md`, batched there as item
    D5): **API-010** (SSO redirect never followed), **API-011** (silent session
    issue failure in all three sign-in callbacks), **API-012** (Microsoft
    callback failure path), **API-061** (calendar disconnect ends the session),
    **API-063** (logout status and body), **API-064** (`login_hint` ignored),
    **API-065** (three names for the user's picture).
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
    "if jwtSecret is empty … the user lands logged-out"), and the logout `200`
    description ("a 204 would be the honest status").

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
- **Disconnecting a calendar disconnects the calendar.** After a user
  disconnects their calendar they are still signed in, on the same page, and the
  calendar shows as not connected.
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
to the password step.  [unit] (web) — and [e2e] up to the point the browser
leaves for the IdP, if OQ-2 resolves that way.

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
remain signed in and are not sent to the landing page).  [e2e] and [unit]
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
200-with-empty-body.  [contract]

AC-16. Given the final commits in both repos, when `api-gate`, `web-gate` and
`e2e-gate` run, then each prints `rc=0`.  [gate]

## 5. Explicitly out of scope

- **Other callback failure branches** (state mismatch, token exchange, Google
  profile fetch, upsert, token save, OIDC exchange, domain mismatch) still end on
  a server error page on the API origin. They are not silent-success defects,
  so they are not API-011/012; routing them to the failure state is a sensible
  follow-up but widens this change across every branch of three handlers. See
  OQ-1.
- **Disconnect for Outlook and webcal users** does not remove their calendar
  link today (§2.4). Fixing what disconnect *removes* is a separate defect and
  a separate behaviour change; this spec only stops it from removing the
  session. To be filed as its own issue (OQ-5).
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
  (`TestAuthDisconnect` asserts only the status code today).

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
  data. Assumed by the factory, pending owner confirmation.
- **D-4 — Hint is advisory only.** The login hint is forwarded only when it is a
  syntactically valid email, and it never influences which Paceday account is
  signed in. Assumed by the factory, pending owner confirmation.
