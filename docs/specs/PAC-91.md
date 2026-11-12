# Spec — PAC-91: booking page in a frame — links leave the frame, framing decided on the path the browser sees

- **Linear**: https://linear.app/paceday/issue/PAC-91/booking-page-in-a-frame-links-must-leave-the-frame-target-top-or-other
- **Status**: revision 2 — challenged 2026-11-12 (spec-challenger,
  accept-with-changes; changes applied in place, see "Challenge log" at the
  end)
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
   client-side to the home page — all inside the frame. The same happens
   without any encoding: nginx merges repeated slashes, so `/book//abc` is
   framable while the app shows its not-found page (challenge, section 2.5).

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
  page was blocked (83).
- Lines 89-98: header assertions for `/book/<slug>`, `/app`, `/api/book/<slug>`.
- `e2e/tests/edge.spec.ts:68-82` also asserts the booking page's headers
  (`frame-ancestors *`, no `X-Frame-Options`, with and without
  `X-Forwarded-Proto`).
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

- **Re-run by the challenger (2026-11-12) against what ships**: the shipped
  `nginx/nginx.conf` and `nginx/paceday-render.sh` in the image pinned by
  `docker-compose.prod.yml:179` (`nginx:1.31.8-alpine`, not 1.27), through
  the `nginx/test` harness (Go client, path sent unnormalised, stub
  upstream recording the URI nginx forwards). Every row above reproduces.
  Additional rows, all **framable today**, all `200` through `location /`,
  all forwarded to the file server with the raw URI unchanged:

  | Request path (raw) | App route (React Router 7.18.2 `matchRoutes`) |
  |---|---|
  | `/xyz/..%2fbook%2fabc` (lower-case `%2f`) | `*` (not found) |
  | `/xyz/%2E%2E%2Fbook%2Fabc` | `*` |
  | `/%2Fbook/x` | `*` |
  | `/book%2Fx` | `*` |
  | `/book//abc` (no encoding: nginx `merge_slashes`) | `*` |
  | `//book/abc` | `*` |
  | `/book/x/../app` | browsers send `/book/app` (booking page) |

  `/book//abc` is the cheapest variant: a browser sends it as written
  (the URL standard keeps empty segments), so a hostile page needs no
  encoding at all. Double encoding (`/xyz/..%252Fbook%252Fabc`) is **not** a
  gap: `$uri` keeps `%2F` and is refused. `%2e%2e` written as a whole
  segment (`/xyz/%2e%2e/book/abc`) is resolved by the browser before
  sending, so the request is `/book/abc` and the app agrees.

- File server (`docker/fileserver/main.go` at 4f798cb, via `httptest`):
  `/app/team/invite/..%2F..%2F..%2Fbook%2Fx` and `/settings/..%2Fbook/x`
  answer **200 with the index**, no redirect; the literal
  `/app/team/invite/../../../book/x` answers 301 to `/book/x`.
- Browsers keep `%2F` in a path (it is not a separator and `..%2F..` is not a
  dot segment under the URL standard), so a frame `src` with such a path
  loads it as-is.
- The app (smart-calendar-flow `src/App.tsx:102`, `<Route path="/book/:slug">`)
  routes on the raw path: React Router 7.18.2 decodes each segment once but
  keeps an encoded slash inside its segment (verified with `matchRoutes`),
  and matches **case-insensitively** (`/BOOK/abc` is the booking page to the
  app; nginx refuses it, which is the safe direction). Routes are exact (`src/App.tsx:38-103`), with two
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

Shell-level behaviour every route shares (challenge): `AuthProvider`
(`src/App.tsx:36`, wrapping every route including the booking page) probes
`GET /api/auth/me` on mount (`src/contexts/AuthContext.tsx:43,66-67`).
Two routes act on mount: `/logout` calls `logout()` then navigates
(`src/pages/Logout.tsx:9-13`), `/auth/callback` refreshes and navigates
(`src/pages/AuthCallback.tsx:21-41`). `RequireAuth`'s redirect is a
`<Navigate>`, which fires in an effect of the child, i.e. before any effect
of an ancestor component runs.

On PAC-88's branch (`impl/pac-88-web` 35c18cd) the three links are at
`PublicBooking.tsx:355`, `:383` and `:487`; PAC-88 adds no anchor, link or
navigation call to the booking page (its `TimeZoneSwitcher` and `ics.ts`
were grepped).

### 2.7 Existing tests on the web side

`src/pages/PublicBooking.test.tsx` and `src/pages/PublicBooking.pac83.test.tsx`
cover refusal messages and rendering; none asserts anything about the links'
targets or framing. No test covers the app shell under a frame.

### 2.8 Overlap with open work

Enach/smart-calendar-flow#144 (PAC-88, open, branch `impl/pac-88-web`)
changes `src/pages/PublicBooking.tsx` (imports, slot queries, moves the ICS
helper to `src/components/booking/ics.ts`) and both `PublicBooking*.test.tsx`
files. It does not touch the three `<Link to="/">` lines. **Order (OQ-3,
settled):** PAC-88 merges first; the PAC-91 web branch is cut from (or
rebased onto) `main` after #144 merges, so it edits the post-PAC-88 file and
its unit tests sit beside `PublicBooking.pac88.test.tsx`. The clockwise-like
half (nginx, e2e, doc) has no overlap with PAC-88 and need not wait, except
that AC-4 (e2e) needs the PAC-91 web image; AC-16 is decided by nginx
alone.

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
  else — no app page, no redirect, no API request at all (not even the
  shell's session probe), no on-mount action such as sign-out. The notice is
  decided before the first render: the route's own component never
  renders, not even for one frame.
- The notice's link and the booking page's links use the same target
  (D-2): whatever OQ-1 decides applies to all four.
- When the booking page is **not** framed, following one of its links is a
  full page load of the home page instead of an in-app transition. That is
  the accepted cost of the top-level target.
- If the embedding site sandboxes its frame without allowing top-level
  navigation (or pop-ups, for a new tab), the browser blocks the link; it
  must still never navigate inside the frame.
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

AC-3. Given the booking page rendered in any of the states of AC-1/AC-2
(loaded, slot chosen, booked, generic error, 404, 410), when every anchor in
it is enumerated, then each one whose destination is a Paceday page carries
the D-2 target, and activating it does not change the app's client-side
location (the router stays on `/book/<slug>`). Today activating the header
link changes the location to `/`. [unit]

AC-4. Given another origin frames `/book/<slug>`, when the visitor clicks the
"paceday" header link inside the frame, then the **top-level** page's URL
becomes Paceday's home page (`BASE_URL/`), and no frame on the embedding page
ends up showing a non-booking Paceday page. Today the frame's URL becomes
`BASE_URL/` and the top-level URL stays the embedder's. (If OQ-1 is answered
"new tab": a new page opens at `BASE_URL/`, the embedder's URL is
unchanged, and the booking frame still shows `/book/<slug>`.) [e2e]

AC-5. Given the booking page is opened directly (not framed), when the
visitor clicks the header link, then they land on Paceday's home page.
guard (passes today; must still pass). [e2e] — a unit test cannot observe it
once the link is a document navigation (jsdom does not navigate).

### B. The app refuses to render framed off the booking page

AC-6. Given the app starts inside a frame (top-level window is not the app's
window) on any route other than the booking page — at least `/`, `/signin`,
`/signup`, `/app`, `/settings`, `/app/team/invite/<token>`, `/logout`,
`/auth/callback` and an unknown path — then the only thing rendered is the
frame notice (test id `frame-notice`); that route's own component is never
rendered, not even once before the notice (asserted by a render counter on
the route component, not only by the final DOM). [unit]

AC-7. Given AC-6's situation, then the notice has exactly one link (test id
`frame-notice-open`) to Paceday's home page carrying the same target as the
booking page's links (D-2). [unit]

AC-8. Given AC-6's situation with the session probe answering "signed out"
(so that, unframed, `RequireAuth` would redirect), then the app performs no
client-side redirect (the address stays the one that was loaded) and sends
no request to `/api/` at all — no session probe, no sign-out on `/logout`,
no refresh on `/auth/callback` (D-7). Today `/app` framed redirects to
`/?redirect=…` and probes `/api/auth/me`. [unit]

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

AC-12. Given a request whose **raw path** (the request target up to its
first `?`, before any decoding, slash merging or dot-segment resolution) is
not exactly `/book/`, one plain segment, and an optional `/` — a plain
segment being one or more characters, none of them `/`, containing no
encoded slash or backslash and not a dot segment (AC-13) — when nginx
answers, then the response carries `frame-ancestors 'none'` and
`X-Frame-Options: DENY`, even when decoding and normalising the path would
yield a booking-page path. At least these raw paths (sent as-is, no client
normalisation), all framable today (reproduced, section 2.5), all answered
`200` through the SPA location (OQ-2):
`/app/team/invite/..%2F..%2F..%2Fbook%2Fx`, `/settings/..%2Fbook/x`,
`/xyz/..%2Fbook%2Fabc`, `/xyz/..%2fbook%2fabc`, `/xyz/%2E%2E%2Fbook%2Fabc`,
`/%2Fbook/x`, `/book%2Fx`, `/book%2fx`, `/book//abc`, `//book/abc`,
`/book/x/../app`, `/book/./abc`, `/book/abc/.`. [nginx]

AC-13. Given a raw path `/book/<segment>` or `/book/<segment>/` (with or
without a query string) where the segment contains an encoded slash or
backslash (`%2F`, `%2f`, `%5C`, `%5c`), or is itself a dot segment in any
encoding (`.`, `..`, `%2E`, `%2e%2E`, `.%2e`, …), then it is not framable.
Cases at least: `/book/x%2F`, `/book/x%2f`, `/book/x%5C..%5Capp`,
`/book/x%5c..%5capp`, `/book/..`, `/book/.`, `/book/%2e`, `/book/%2E%2e/`,
`/book/.%2e?x`. The backslash cases are framable today and the app would
show them as the booking page; they are refused anyway because a proxy in
front may treat `\` as a separator (fail closed). [nginx]

AC-14. Given the booking page addresses that are framable today and are
what browsers actually request — `/book/abc123`, `/book/abc123/`,
`/book/abc123?utm=x`, `/book/my-slug-30` (slugs are lower-case
alphanumerics and dashes), a query that itself holds encoded slashes and
dot segments (`/book/abc123?ref=https%3A%2F%2Fexample.com%2F..%2Fapp`,
`/book/abc123/?a=/../app`), a segment that merely contains dots
(`/book/..x`), a percent-encoded segment (`/book/%61bc`,
`/book/caf%C3%A9`, `/book/caf%c3%a9`) — then they remain framable
(`frame-ancestors *`, no `X-Frame-Options`) and every other CSP directive is
unchanged. guard (passes today). [nginx]

AC-15. Given every non-framable case already in
`TestFramingOnlyBookingPage` and `TestSecurityHeadersOnEveryResponse`
(including error responses), they stay non-framable. guard (passes today).
[nginx]

AC-16. Given another origin frames `BASE_URL/app/team/invite/..%2F..%2F..%2Fbook%2Fx`
and `BASE_URL/book//<slug>` next to the real booking page, when the page
loads, then the browser reports a `frame-ancestors` violation for each of
those two frames and neither contains `#root`, while the booking frame still
renders (existing control). Today both render the app (team-invite redirect
to home; not-found). [e2e]

### D. Documentation

AC-17. `docs/security.md` (B1 row) no longer describes the in-frame
navigation as open; it states both mitigations (top-level links, the app's
frame refusal) and that framability is decided on the raw path. Checked in
review. [review] — not test-falsifiable by design; it is a documentation
criterion, and the reviewer checks it against the merged nginx config.

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
- **Upper-case or otherwise non-canonical booking addresses** (`/BOOK/x`).
  The app's router matches them case-insensitively, nginx refuses them;
  that is the safe direction and no Paceday link produces them.
- **Embedders that sandbox the frame** without allowing top-level
  navigation: the link is blocked by the browser (section 3); no fallback
  is required.
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
| e2e: no CSP violation on the framed booking page other than the refused frames | `framing.spec.ts:83` |
| Booking page headers through the full stack | `e2e/tests/edge.spec.ts:68-82` |

`partc_test.go:53` (`/book/x/../app` expected framable) is the one existing
expectation this spec **reverses** (AC-12, decision D-3). Browsers resolve
literal dot segments before sending, so no real visitor requests that path.
`partc_test.go:54` (`/book/%61bc` framable) is kept (AC-14).

Not affected (checked): `e2e/tests/time-format.spec.ts:79` clicks a
"paceday" link, but in the signed-in app shell, not on the booking page.

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
| D-2 | Links target the top level (replace the embedding page), as the issue proposes, rather than a new tab. The four links (three on the booking page, one in the frame notice) take their target from **one shared definition**, so answering OQ-1 "new tab" changes one value (`_top` to `_blank`; browsers imply `noopener` for `_blank`) plus AC-4's expected outcome, nothing else. | factory-assumed; see OQ-1 |
| D-3 | nginx decides framability on the **raw path** — the request target as received (`$request_uri`, which keeps the query and every escape), cut at its first `?` — **and** on the normalised `$uri`: both must be `/book/`, one plain segment (AC-12/AC-13), optional `/`. The raw test is case-insensitive about escapes (`%2f` = `%2F`) and ignores the query entirely, so encoded slashes in tracking parameters never refuse a booking page (AC-14). Reverses the PAC-81 review's acceptance of `/book/x/../app` as framable. Feasibility checked by the challenger with a scratch build of the shipped config (section "Challenge log"); the mechanism is the plan's to choose. | factory-assumed |
| D-4 | The frame guard applies to **any** frame, same-origin included. Paceday never frames itself (no `<iframe>` in the web app), and nginx already refuses same-origin framing of other routes. "Framed" means the app's window is not the top-level window, a comparison that works across origins without reading anything from the embedder (reading the embedder's location would throw). It is a fact of the page load, but the **route** is re-evaluated on every client-side location change (AC-10). "The booking page" for the guard is the route the app itself renders as the booking page (`/book/:slug` as the router matches it), not a second string rule. | factory-assumed |
| D-5 | The guard's notice copy and look are the implementer's choice, held to the design gate; it must name Paceday and offer one top-level link. | factory-assumed |
| D-6 | The file server is not changed (section 5). | factory-assumed |
| D-7 | Framed off the booking page the app is **inert**: no API request (including the shell's `/api/auth/me` probe), no on-mount action, no client redirect. The booking page framed keeps today's behaviour (its session probe included). | factory-assumed (challenge) |

### Open questions

- **OQ-1 (owner, product, non-blocking):** when a booker clicks "back to
  Paceday" on a booking page embedded in another site, should Paceday
  **replace** the host's page (default, as the issue says) or open in a
  **new tab**, leaving the host's site in place? Both satisfy Q-O2 equally;
  it is a UX call about the embedding site. The factory proceeds with
  "replace" unless told otherwise; switching later only changes the link
  target and AC-4's assertion.
- **OQ-2 — resolved 2026-11-12 (challenger, by running it):** on the shipped
  config and pinned image `nginx:1.31.8-alpine`, `/%2Fbook/x`, `/book%2Fx`,
  `/book%2fx`, `/book//abc` and `//book/abc` all reach `location /`, answer
  `200` and are forwarded raw to the file server; all are framable today.
  Only a `%00` escape is refused earlier (`400`, already `DENY`). AC-12
  asserts `200` and `DENY` for them.
- **OQ-3 — resolved 2026-11-12:** PAC-88 (Enach/smart-calendar-flow#144)
  merges first; the PAC-91 web PR is based on `main` after it (section 2.8).
  The clockwise-like PR may be reviewed in parallel; its AC-4 e2e needs the
  PAC-91 web image (AC-16 needs only the nginx change).

## Challenge log

### Challenge — 2026-11-12

**Verdict**: accept-with-changes. Every change below is applied in place
(revision 2); nothing blocking remains open. Owner OQ-1 stays open and
non-blocking.

Method: re-read the Linear issue, `docs/factory/README.md`, owner decision
PAC-81 Q-O2 (`.claude/factory/owner-decisions.md`); re-ran the raw-path
probe against the **shipped** `nginx/nginx.conf` + `nginx/paceday-render.sh`
in the pinned `nginx:1.31.8-alpine` via the `nginx/test` harness (scratch
test, not committed); checked every crafted path against React Router
7.18.2 `matchRoutes` (the web repo's installed version) and against the
WHATWG URL parser (Node); built a scratch variant of the config with a
raw-path rule to prove D-3 is implementable without false refusals (not
committed: the mechanism is the plan's).

#### Blocking (resolved in this revision)

1. **AC-12 missed browser-reachable variants with no dot-encoding at all.**
   `/book//abc` and `//book/abc` (nginx `merge_slashes`) and lower-case
   `/xyz/..%2fbook%2fabc` are framable today and render the not-found page
   in the app. A fix keyed only on encoded slashes would have left them.
   Resolved: added to AC-12 and (one of them) to AC-16; section 2.5 table.
2. **D-3 did not define "raw path" or "plain segment".** `$request_uri`
   includes the query; a raw rule applied to it whole would refuse real
   embeds whose tracking parameters carry encoded URLs
   (`?ref=https%3A%2F%2F…`), and a case-sensitive `%2F` test misses `%2f`.
   Resolved: D-3 defines the raw path (request target cut at the first `?`,
   no decoding), case-insensitive escapes, query ignored; AC-12 defines a
   plain segment; AC-14 adds the query-with-escapes, dotted-segment and
   percent-encoded-unicode guards. Prototype result: every AC-12/AC-13 path
   refused, every AC-14 path still framable, `/book/%61bc` kept.
3. **AC-5 could not pass as a unit test after the change.** Once the link
   is a top-level document navigation, jsdom never "lands" anywhere.
   Resolved: AC-5 is [e2e].
4. **"No data request on behalf of that page" was untestable and too weak.**
   `AuthProvider` probes `/api/auth/me` on every route, `/logout` signs the
   user out on mount, `/auth/callback` refreshes on mount, and
   `RequireAuth`'s `<Navigate>` fires in a child effect — before any
   ancestor's effect — so a guard that decides after mount still lets the
   redirect (and sign-out) happen. Resolved: §3 wording, AC-6 (route
   component never rendered, counted; `/logout`, `/auth/callback`,
   `/signup` added), AC-8 (signed-out precondition, no `/api/` request at
   all), new D-7.

#### Non-blocking (resolved in this revision)

1. §3 said the notice opens Paceday "in its own tab" while AC-7 said "at the
   top level": contradictory under OQ-1. Now one shared target for all four
   links (D-2), so OQ-1 "new tab" is a one-value change plus AC-4's
   expectation (AC-4 now states both outcomes).
2. AC-3's "no client-side navigation call" could not be observed by
   enumerating anchors. Now: activating each internal anchor leaves the
   router location on `/book/<slug>` (fails today).
3. D-4 detection made explicit: framed = the app's window is not the
   top-level window, a comparison that works cross-origin (no read of the
   embedder's location, which would throw); route re-evaluated on every
   location change; "booking page" = the router's own `/book/:slug` match.
   The guard cannot affect the PAC-81 Part C e2e: `/app` is refused by the
   browser before any script runs, and the booking frame is allowed by the
   guard; the guard adds no inline script, so no new CSP violation
   (`framing.spec.ts:83` keeps holding).
4. AC-13's backslash cases are not a live gap (the app shows them as the
   booking page); kept as fail-closed with the reason written down.
5. React Router matches case-insensitively (`/BOOK/x` = booking page to the
   app); nginx refuses it. Safe direction; recorded in §2.5 and §5.
6. OQ-2 settled by running it (all five paths reach `location /`, `200`);
   OQ-3 settled (PAC-88 first, section 2.8); PAC-88 line numbers recorded.
7. Unframed, the links become full page loads instead of SPA transitions;
   sandboxed embedders may block the link. Both now stated in §3 / §5.
8. AC-17 is review-only by nature; labelled as such.

#### Criteria I could not falsify

- AC-17 — documentation; no test can distinguish pass from fail. Accepted
  as a review criterion.
- AC-9, AC-11, AC-14, AC-15 are guards (pass today by design); they are
  regression protection, not proof of the change.

#### Citations I checked and found wrong

- §2.5 "stock `nginx:1.27-alpine`" — the suite and production pin
  `nginx:1.31.8-alpine` (`docker-compose.prod.yml:179`,
  `nginx/test/harness_test.go:105-115`). Result unchanged on the right
  image.
- §2.4 / §6 `framing.spec.ts:85` — the "nothing else blocked" assertion is
  at line 83.

Checked and correct: `nginx.conf:41-53, 84, 89-97, 138-140`;
`paceday-render.sh:145-146, 195-198`; `partc_test.go:17-90, 42-54`;
`nginx_test.go:127, 171`; `harness_test.go:105-115`;
`docker/fileserver/main.go:104, 112-126`; `docs/plans/PAC-81-part-c.md:145,
207-214`; `docs/security.md:55`; web `App.tsx:38-103`,
`RequireAuth.tsx:53-59`, `PublicBooking.tsx:249, 277, 360` (main ae8b0d4).

#### Consumers this breaks

- clockwise-like `nginx/test/partc_test.go:53` — `/book/x/../app` flips to
  not framable (intended, D-3).
- None else: `e2e/tests/edge.spec.ts:68-82` and `framing.spec.ts` keep
  passing on real booking URLs; `e2e/tests/time-format.spec.ts:79` clicks a
  "paceday" link in the signed-in shell, not on the booking page; no web
  test asserts the booking links' behaviour (§2.7).
