# Spec — PAC-91: booking page in a frame — links leave the frame, framing decided on the path the browser sees

- **Linear**: https://linear.app/paceday/issue/PAC-91/booking-page-in-a-frame-links-must-leave-the-frame-target-top-or-other
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**: none. `docs/factory/api-audit.md` has no finding on
  framing; no API endpoint or `x-uncertain` marker is involved. The
  `docs/security.md` B1 row (line 55) describes this gap as open and must be
  updated when it closes.
- **Binding owner decision**: PAC-81 Q-O2 (2026-11-01,
  `.claude/factory/owner-decisions.md:127-130`): the public booking page may
  be framed by any site; **every other route stays unframeable**.
- **Repos**: Enach/clockwise-like (nginx, nginx tests, e2e, `docs/security.md`)
  and Enach/smart-calendar-flow (booking page, app shell, vitest).
- Surveyed at clockwise-like `origin/main` 4f798cb and smart-calendar-flow
  `origin/main` ae8b0d4.

## 1. Problem

Q-O2 allows one page of Paceday to be shown inside other websites: the public
booking page. Every other page must refuse to appear inside another site,
because a page shown in a hostile site's frame can be overlaid and its
clicks hijacked (clickjacking).

Today the refusal is only checked by the browser **when a document is
loaded**, and the check is decided on a path that is not always the path the
app ends up showing. That leaves two ways for a page other than the booking
page to appear inside a hostile frame:

1. **Links on the booking page navigate inside the frame.** The booking page
   is part of the single-page app. Its "back to Paceday" links change the
   page inside the app without loading a new document, so the browser never
   re-checks whether the new page may be framed. A visitor who clicks one sees
   the Paceday home page, then the sign-in page, inside someone else's site.
2. **The server and the app read the path differently.** The server decides
   "is this the booking page?" after decoding and resolving the path; the app
   routes on the path as the browser sent it. A crafted address can therefore
   be judged "booking page, framable" by the server and rendered as a
   different page by the app. Reproduced during this survey (section 2.5):
   `/app/team/invite/..%2F..%2F..%2Fbook%2Fx` is served as framable, and the
   app routes it to the team-invite page, which (signed out) redirects
   client-side to the home page — all inside the frame.

The cost is low today: the session cookie is `SameSite=Lax`, so a
cross-site frame carries no session and shows only signed-out pages. But it
contradicts the owner decision, it will become a real hole the day any
cookie or page changes, and a booker who clicks "back to Paceday" in an
embedded widget gets Paceday crammed into the host's widget, which is a poor
experience in its own right.

## 2. Current behaviour

### 2.1 nginx framing headers (clockwise-like)

- `nginx/nginx.conf:41-48` — `map $uri $paceday_framable`: `1` when `$uri`
  matches `~^/book/[^/]+/?$`, else `0`. The comment at 41-44 states the
  owner decision and that `$uri` carries no query string.
- `nginx/nginx.conf:50-53` — `$paceday_frame_options` is `""` (header
  omitted) when framable, else `DENY`.
- `nginx/nginx.conf:84` — `proxy_hide_header X-Frame-Options` (upstream
  cannot set it). `nginx/nginx.conf:89-97` — headers are set once at server
  level with `always`; no location sets `add_header`, so every response,
  including errors, gets them. `X-Frame-Options $paceday_frame_options` at
  line 95, CSP at line 97.
- `nginx/paceday-render.sh:145-146,195-198` — the generated
  `map $paceday_framable $paceday_csp`: one policy with
  `frame-ancestors *` (framable) and one with `frame-ancestors 'none'`,
  otherwise identical.
- `nginx/nginx.conf:138-140` — the SPA location is
  `proxy_pass http://paceday_frontend;` with no URI part, so nginx forwards
  the **raw request URI** to the file server, not `$uri`.

**The decision uses `$uri`, the decoded and normalised path**: `%XX` is
decoded (including `%2F` to `/`) and dot segments are then resolved. The PAC-81
review accepted this deliberately: `nginx/test/partc_test.go:42-54` and
`docs/plans/PAC-81-part-c.md:207-210` record that `/book/x/../app` "is the
booking page of slug `app` and stays framable" (test case at
`partc_test.go:53`, expected `true`), and that `/book/%61bc` is framable
(`partc_test.go:54`).

### 2.2 nginx tests

- `nginx/test/partc_test.go:17-90` `TestFramingOnlyBookingPage` — table of
  paths with expected framability; asserts `frame-ancestors` and
  `X-Frame-Options` per case and that every other CSP directive is identical
  across routes. Encoded/dot cases at lines 42-54. No case has an encoded
  slash **combined with** a dot segment, which is the case that diverges
  (section 2.5).
- `nginx/test/nginx_test.go:127` `TestSecurityHeadersOnEveryResponse`
  (lines 156-162 check `DENY` / `frame-ancestors 'none'` on non-booking
  responses including errors) and `nginx_test.go:171` `TestCSPDirectives`.
- The suite runs the nginx image pinned in `docker-compose.prod.yml`
  (`nginx/test/harness_test.go:105-115`).

### 2.3 The file server (clockwise-like `docker/fileserver/main.go`)

- `main.go:112-126` — `/` handler: `/` and `/index.html` get the index; any
  other path is cleaned with `filepath.Clean` and, **if no file exists,
  the index is served with 200** (SPA fallback, line 119-122). It looks only
  at the decoded `r.URL.Path`.
- `main.go:104` — Go's `ServeMux`. A path with literal dot segments
  (`/a/../book/x`) gets a 301 to the cleaned path; a path whose dot segments
  are hidden behind encoded slashes (`/a/..%2Fbook%2Fx`) does not, and gets
  the index with 200 (probe in section 2.5). The browser's address therefore
  stays the raw path.
- The file server sets no framing headers; nginx owns them. No change to the
  file server is expected by this spec.

### 2.4 The PAC-81 Part C framing e2e (clockwise-like `e2e/tests/framing.spec.ts`)

- Lines 42-87: a page served from another origin (`127.0.0.1`, OS port,
  lines 25-37) frames `/book/<slug>` and `/app`. Asserts the booking frame
  renders the link title (60-64) at the exact booking URL (65), `/app` is
  refused by a `frame-ancestors` CSP violation (69-73), `#root` is present
  only in the booking frame (78-79), and nothing else on the framed booking
  page was blocked (85).
- Lines 89-98: header assertions for `/book/<slug>`, `/app`, `/api/book/<slug>`.
- Nothing clicks inside the framed booking page, and nothing frames a crafted
  path. Plan: `docs/plans/PAC-81-part-c.md:145` (test map),
  `:211-214` (this gap noted, fix filed separately).

### 2.5 Raw path vs normalised path — reproduced

Probes run during this survey (scratch only, nothing committed):

- nginx (stock `nginx:1.27-alpine`, same `map` regex as `nginx.conf:45-48`),
  request sent with the path as-is:

  | Request path (raw) | `$uri` | framable |
  |---|---|---|
  | `/app/team/invite/..%2F..%2F..%2Fbook%2Fx` | `/book/x` | **1** |
  | `/settings/..%2Fbook/x` | `/book/x` | **1** |
  | `/xyz/..%2Fbook%2Fabc` | `/book/abc` | **1** |
  | `/book/x%2F..%2F..%2Fapp` | `/app` | 0 |

- File server (`docker/fileserver/main.go` at 4f798cb, via `httptest`):
  `/app/team/invite/..%2F..%2F..%2Fbook%2Fx` and `/settings/..%2Fbook/x`
  answer **200 with the index**, no redirect; the literal
  `/app/team/invite/../../../book/x` answers 301 to `/book/x`.
- Browsers keep `%2F` in a path (it is not a separator and `..%2F..` is not a
  dot segment under the URL standard), so a frame `src` with such a path
  loads it as-is.
- The app (smart-calendar-flow `src/App.tsx:102`, `<Route path="/book/:slug">`)
  routes on the raw path. Routes are exact (`src/App.tsx:38-103`), with two
  parameterised routes: `/book/:slug` and `/app/team/invite/:token`
  (`App.tsx:75-82`), plus the catch-all `*` → `NotFound` (`App.tsx:103`).
  So `/app/team/invite/..%2F..%2F..%2Fbook%2Fx` matches the team-invite
  route (token `..%2F..%2F..%2Fbook%2Fx`); signed out, `RequireAuth`
  (`src/components/RequireAuth.tsx:53-59`) navigates client-side to
  `/?redirect=…`, the home page, inside the frame. `/settings/..%2Fbook/x`
  matches `*` and renders `NotFound`.

Conclusion: today a hostile site can show the home page, the not-found page
and (one more client-side click) the sign-in page inside its frame without
the booking page being involved at all.

### 2.6 Navigation on the booking page (smart-calendar-flow `src/pages/PublicBooking.tsx`)

| Where | Element | Navigates inside a frame? |
|---|---|---|
| `:249-251` (link failed to load, generic error state) | `<Link to="/">← Back to Paceday` | **yes**, client-side to `/` |
| `:277-279` (404/410/other error state) | `<Link to="/">← Back to Paceday` | **yes**, client-side to `/` |
| `:360-365` (normal page header) | `<Link to="/">← paceday` | **yes**, client-side to `/` |
| `:560-575` booking form | `<form onSubmit>` with `e.preventDefault()` then a mutation | no — no navigation, stays on the booking page |
| `:97-108` `downloadIcs` | temporary `<a href=blob: download>` | no — a download, not a navigation |
| buttons (retry, change, slot picks, calendar nav `src/components/ui/calendar.tsx`) | `<Button>` / state changes | no |
| `src/components/ApiErrorNotice.tsx`, `CoveragePill`, `StillWorking`, `links/HostAvatars` | — | no links, anchors, forms or navigation (grep) |
| `src/api/client.ts` | builds request URLs from `window.location.origin` (`:519`); no `location` assignment | no |

The issue's "about lines 233 and 315" are now 249, 277 and 360: three links,
not two.

Pages reachable from those links, for context: `src/pages/Index.tsx` uses
`navigate("/app")` (`:37`), `navigate("/")` (`:40`) and `<Link to="/">`
(`:46`); `src/pages/NotFound.tsx:16` uses a plain `<a href="/app">` (a full
document load, which nginx's header refuses in a frame — correct today).

No code in the web app detects being framed (`git grep` for
`window.top`, `window.self`, `frameElement`, `<iframe`: none).

### 2.7 Existing tests on the web side

`src/pages/PublicBooking.test.tsx` and `src/pages/PublicBooking.pac83.test.tsx`
cover refusal messages and rendering; none asserts anything about the links'
targets or framing. No test covers the app shell under a frame.

### 2.8 Overlap with open work

Enach/smart-calendar-flow#144 (PAC-88, open, branch `impl/pac-88-web`)
changes `src/pages/PublicBooking.tsx` (imports, slot queries, moves the ICS
helper to `src/components/booking/ics.ts`) and both `PublicBooking*.test.tsx`
files. It does not touch the three `<Link to="/">` lines. Expect a textual
rebase on the booking page file only; whichever merges second rebases.

## 3. Desired behaviour

- When the booking page is shown inside another site, every link on it that
  leads elsewhere in Paceday opens Paceday **at the top level of the browser
  tab**, not inside the frame. The booker leaves the embedding site, as the
  issue proposes (factory-assumed D-1; see OQ-1).
- When the booking page is not framed, those links still take the visitor to
  the Paceday home page, as today.
- No page of Paceday other than the booking page ever renders its content
  inside a frame, however it was reached (a link, a crafted address, a future
  link someone adds). If the app finds itself framed on any other page, it
  shows a short notice with a way to open Paceday in its own tab, and nothing
  else — no app page, no redirect, no data request on behalf of that page.
- The server says "may be framed" only for addresses that the app will
  actually show as the booking page. An address that the server would
  normalise to the booking page but that the browser and app see as something
  else is refused like any other page.
- The booking page itself, framed or not, behaves exactly as today:
  loading, choosing a slot, booking, error states, the calendar download.

## 4. Acceptance criteria

Test levels: **[nginx]** = Go integration test in `nginx/test` against the
real nginx image; **[unit]** = vitest in smart-calendar-flow; **[e2e]** =
Playwright through docker compose in clockwise-like `e2e/`. Each criterion
fails on the surveyed `origin/main` unless noted "guard (passes today)".

### A. Links on the booking page leave the frame

AC-1. Given the booking page has loaded a link, when it renders, then every
element on it that navigates to another Paceday page navigates the top-level
browsing context (not the frame it is in). Today the header link
(`PublicBooking.tsx:360`) is a client-side in-frame navigation. [unit]

AC-2. Given the booking page's link failed to load (generic error, 404 and
410 states), when it renders its "back to Paceday" link, then that link too
targets the top-level browsing context. Covers both error-state links
(`:249`, `:277`). [unit]

AC-3. Given the booking page rendered in any of the states of AC-1/AC-2,
then it contains no other element that would navigate the frame to another
page of the app (no unmarked internal link, no client-side navigation call).
Asserted by enumerating anchors/links in each rendered state: all internal
ones target the top level. [unit]

AC-4. Given another origin frames `/book/<slug>`, when the visitor clicks the
"paceday" header link inside the frame, then the **top-level** page's URL
becomes Paceday's home page (`BASE_URL/`), and no frame on the embedding page
ends up showing a non-booking Paceday page. [e2e]

AC-5. Given the booking page is opened directly (not framed), when the
visitor clicks the header link, then they land on Paceday's home page.
guard (passes today; must still pass). [unit or e2e]

### B. The app refuses to render framed off the booking page

AC-6. Given the app starts inside a frame (top-level window is not the app's
window) on any route other than the booking page — at least `/`, `/signin`,
`/app`, `/settings`, `/app/team/invite/<token>` and an unknown path — then
the only thing rendered is the "open Paceday in its own tab" notice; none of
that route's own content is rendered (e.g. no sign-in button on `/signin`, no
404 text on an unknown path). [unit]

AC-7. Given AC-6's situation, then the notice's link opens Paceday's home
page at the top level (not inside the frame). [unit]

AC-8. Given AC-6's situation on a route that requires sign-in, then the app
performs no client-side redirect (the address stays the one that was
loaded); in particular the sign-in redirect of `RequireAuth` does not run.
[unit]

AC-9. Given the app starts inside a frame on the booking page `/book/<slug>`,
then the booking page renders normally (same content as unframed). guard
(passes today). [unit]

AC-10. Given the app is framed on the booking page and something navigates
the app client-side to another route (simulated in the test), then the
notice replaces the page — the guard holds after navigation, not only on
first load. [unit]

AC-11. Given the app is not framed, on every route of AC-6, then it renders
exactly as today (no notice). guard (passes today). [unit]

### C. The server decides framability on the path the browser and app see

AC-12. Given a request whose path **as sent** is not exactly `/book/` plus
one segment (optionally followed by `/`), when nginx answers, then the
response carries `frame-ancestors 'none'` and `X-Frame-Options: DENY`, even
when decoding and normalising the path would yield a booking-page path. At
least these raw paths (sent as-is, no client normalisation), all framable
today:
`/app/team/invite/..%2F..%2F..%2Fbook%2Fx`, `/settings/..%2Fbook/x`,
`/xyz/..%2Fbook%2Fabc`, `/%2Fbook/x` (if nginx accepts it),
`/book/x/../app`, `/book%2Fx`. [nginx]

AC-13. Given a raw path `/book/<segment>` or `/book/<segment>/` (with or
without a query string) where the segment contains an encoded slash or
backslash (`%2F`, `%2f`, `%5C`, `%5c`), or is itself a dot segment in any
encoding (`.`, `..`, `%2E`, `%2e%2E`, …), then it is not framable. [nginx]

AC-14. Given the booking page addresses that are framable today and are
what browsers actually request — `/book/abc123`, `/book/abc123/`,
`/book/abc123?utm=x`, and a real seeded slug — then they remain framable
(`frame-ancestors *`, no `X-Frame-Options`) and every other CSP directive is
unchanged. guard (passes today). [nginx]

AC-15. Given every non-framable case already in
`TestFramingOnlyBookingPage` and `TestSecurityHeadersOnEveryResponse`
(including error responses), they stay non-framable. guard (passes today).
[nginx]

AC-16. Given another origin frames `BASE_URL/app/team/invite/..%2F..%2F..%2Fbook%2Fx`
next to the real booking page, when the page loads, then the browser
reports a `frame-ancestors` violation for that frame and the frame contains
no `#root`, while the booking frame still renders (existing control). [e2e]

### D. Documentation

AC-17. `docs/security.md` (B1 row) no longer describes the in-frame
navigation as open; it states both mitigations (top-level links, the app's
frame refusal) and that framability is decided on the raw path. Checked in
review. [review]

## 5. Explicitly out of scope

- **Changing which page may be framed.** Q-O2 is binding; no other route
  becomes framable and the booking page does not become unframeable.
- **Restricting who may frame the booking page** (an allow-list of
  embedding sites). Q-O2 says any site.
- **The booking API calls** (`/api/book/...`): they are not documents and
  keep `DENY`, as today.
- **The file server's SPA fallback** (serving the index for any unknown
  path). It is not what makes a path framable, and changing it would ripple
  into every client route; the raw-path rule in nginx and the app's guard
  close the gap without it.
- **`SameSite` or cookie changes.** The existing `Lax` cookie is the
  current mitigation and is not touched.
- **Booking page redesign, including hiding the "paceday" link when
  embedded.** Possible product choice; not asked for.
- **Other web routes' own links** (`Index`, `NotFound`). Once the guard holds,
  they never render framed.
- **PAC-88's time-zone work** on the same file (section 2.8).

## 6. What must not change

| Behaviour | Protected today by |
|---|---|
| Framed booking page renders and works; `/app` refused in a frame | `e2e/tests/framing.spec.ts:42-87` |
| Header values on `/book/<slug>`, `/app`, `/api/book/<slug>` | `framing.spec.ts:89-98` |
| Framability table for ordinary paths; CSP identical except `frame-ancestors` | `nginx/test/partc_test.go:17-90` |
| `DENY` / `frame-ancestors 'none'` on every non-booking response, errors included | `nginx/test/nginx_test.go:127-169` |
| CSP directives per route | `nginx/test/nginx_test.go:171` |
| Booking page rendering, refusal messages | `src/pages/PublicBooking.test.tsx`, `PublicBooking.pac83.test.tsx` (and PAC-88's tests once merged) |
| Unframed navigation from booking page to home | **none** — AC-5 adds it |
| All routes render unframed as today | **none at shell level** — AC-11 adds it |
| e2e: no CSP violation on the framed booking page other than the refused frames | `framing.spec.ts:85` |

`partc_test.go:53` (`/book/x/../app` expected framable) is the one existing
expectation this spec **reverses** (AC-12, decision D-3). Browsers resolve
literal dot segments before sending, so no real visitor requests that path.

## 7. Rollback

No migration, no data. Revert the web PR (links and guard) and the
clockwise-like PR (nginx map and tests, e2e, doc) independently; each half is
useful alone, and reverting either restores today's behaviour for that half.
The two halves have no deploy ordering constraint.

## 8. Decisions and open questions

### Decisions

| ID | Decision | Source |
|---|---|---|
| D-0 | Only the booking page may be framed; every other route stays unframeable. | **owner** (PAC-81 Q-O2) |
| D-1 | Do **both** fixes the issue offers: links on the booking page open at the top level, **and** the app refuses to render framed off the booking page. Links alone leave the raw-path gap (section 2.5) and any future link; the guard alone leaves the booker stuck with a notice inside the host's widget. | factory-assumed |
| D-2 | Links target the top level (replace the embedding page), as the issue proposes, rather than a new tab. | factory-assumed; see OQ-1 |
| D-3 | nginx decides framability on the raw request path as well as the normalised one: both must be the booking page with one plain segment. Reverses the PAC-81 review's acceptance of `/book/x/../app` as framable. | factory-assumed |
| D-4 | The frame guard applies to **any** frame, same-origin included. Paceday never frames itself (no `<iframe>` in the web app), and nginx already refuses same-origin framing of other routes. | factory-assumed |
| D-5 | The guard's notice copy and look are the implementer's choice, held to the design gate; it must name Paceday and offer one top-level link. | factory-assumed |
| D-6 | The file server is not changed (section 5). | factory-assumed |

### Open questions

- **OQ-1 (owner, product, non-blocking):** when a booker clicks "back to
  Paceday" on a booking page embedded in another site, should Paceday
  **replace** the host's page (default, as the issue says) or open in a
  **new tab**, leaving the host's site in place? Both satisfy Q-O2 equally;
  it is a UX call about the embedding site. The factory proceeds with
  "replace" unless told otherwise; switching later only changes the link
  target and AC-4's assertion.
- **OQ-2 (factory, contract stage):** whether AC-12's `/%2Fbook/x` and
  `/book%2Fx` reach the `location /` at all or are rejected earlier by nginx;
  the test records whichever status nginx returns, but the framing headers
  must be `DENY` either way. Owner: contract author, resolved by running the
  nginx test.
- **OQ-3 (factory, plan stage):** sequencing with PAC-88 (#144) on
  `PublicBooking.tsx`; whichever lands second rebases. Owner: planner.
