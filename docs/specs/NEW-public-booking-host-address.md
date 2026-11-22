# Spec — NEW-public-booking-host-address: the public booking page's two unauthenticated answers carry every accepted host's email address

- **Linear**: none yet. The highest issue in the PAC team on 2026-11-22 is **PAC-162** (`list_issues`, team PAC, ordered by creation, archived included), so the next free number is **PAC-163** — and it is **not claimed here**. `NEW-request-field-length-limits.md` (branch `codeden/spec-enforce-contract-validation-on-aea8`, revision 2 the same day) names the same PAC-163 as next free and also declines to claim it, so two sessions would file the same number. The factory ticket id is `NEW-public-booking-host-address`, following the existing `NEW-*` specs in this directory (`NEW-booking-rate-limit.md` §Linear is the precedent for the convention). **A PAC issue must be opened at intake and this file renamed or linked from it.** Because there is no issue, nothing was posted to Linear.
- **Status**: draft, revision 1 (awaiting spec-challenger). **The work it describes is blocked until the owner approves it.** No handler, contract, generated file or test was changed by the session that wrote it.
- **Author**: spec-author (factory), in a CodeDen session on branch `codeden/spec-stop-returning-host-addresses-48d4`.
- **Verified against**: clockwise-like `origin/main` `308ac21` ("fix(security): guard the webcal feed fetch against SSRF, on write and at egress (#331)"), which is the tip after both halves of the 2026-11-22 audit (#331, #332) merged. Every backend, contract, test, e2e, docs-site and MCP citation below was read at that SHA.
  - **smart-calendar-flow was NOT reachable from this worktree.** Every claim about what the web app reads or renders is marked **[unverified-web]** and names the file a reader must open in that repo to settle it. There are **six** such claims and no more (§2.4, W-1 to W-6).
- **Repositories touched**: clockwise-like (contract fragment, bundle, handler, backend tests, e2e tests, `MIGRATION.md` notes, `docs/security.md`, `docs/factory/api-audit.md`). smart-calendar-flow stops reading the field and regenerates `src/api/generated` — and the **order of those two repos is reversed from the factory's usual stage-4 order** (D-7, §3.8).
- **Process**: full. A documented response field is removed from two live, unauthenticated operations; a second field changes from optional to always-present; the contract bundle changes, so the cross-repo types gate is in play.
- **Inherited scope** (`docs/factory/README.md` §7 rule 1, "touch it, fix it"). This work modifies the response half of `getPublicLinkInfo` and `createPublicBooking` and the `publicHostDTO` builder they share, so the open findings on that code are in scope:
  - **No audit row exists for this disclosure itself.** `docs/factory/api-audit.md` has no row for `getPublicLinkInfo`, and `docs/factory/silent-failures.md` has none either. The only written record is one clause inside `docs/security.md:92` — "the route that answers with every accepted host's address" — attached to the *rate-limit* entry, which is struck through as fixed. **Resolution (D-9): a row is opened at intake**, so the disclosure is tracked as its own finding rather than as a subordinate clause of a closed one, and `docs/security.md:92` gains its own entry (AC-14).
  - **SF-027** (`silent-failures.md:170`, **Fixed, PAC-122 batch 1**): a failed roster read on `getPublicLinkInfo` is the documented 500, never `hosts: []`. **Behaviour preserved by §6**, and its test's baseline assertion is one of the two this work must rewrite (§2.7).
  - **SF-028** (`silent-failures.md:171`, **Fixed, PAC-122 batch 1**): the same for the 201 confirmation. **Behaviour preserved by §6.**
  - **API-006** (`api-audit.md:276`, **Fixed, PAC-25**): the authenticated link read returns the full roster with addresses, and is now owner-and-accepted-co-hosts only. That read is the *legitimate* place a host address appears and is **named out of scope** in §5 — removing it would take from the owner something they are entitled to.
  - `x-uncertain`: **none** on either operation in scope. The two markers in `contracts/openapi/paths/booking.yaml` are at `:520` and `:1840` (neither on these two operations) — checked by reading every `x-uncertain` in `contracts/openapi/paths/` at `308ac21`.
  - **Read and set aside** (Fixed, behaviour preserved by §6): API-051, API-073, API-094, API-096, SF-005, SF-066, SF-067.

Decision marks: **[F]** = factory decision, recorded here and open to challenge. **[OQ]** = open for the owner, with the default the spec is written to.

---

## 0. The brief's premises, checked

| Premise | Verdict |
|---|---|
| `GET /api/book/{slug}` returns every accepted host's address in `publicHostDTO.Email`, unauthenticated. | **Confirmed.** `handlers_booking.go:45-49` declares `publicHostDTO` with `Email string json:"email"`; `publicLink` fills it from `user.Email` at `:112`; `routes.go:49` mounts the route under the "Public booking routes — no JWT required" comment (`:47`) and the operation declares `security: []` (`booking.yaml:46`). |
| PR #332 made `allowPublicBooking` `getLinkInfo`'s first statement. | **Confirmed.** `handlers_booking.go:164-166`: `if !allowPublicBooking(w, r, bookingSlotsLimiter) { return }` is the function's first statement, on the slot-listing budget. |
| PR #332 made `GenerateSlug` append 8 hex characters of `crypto/rand`. | **Confirmed**, with the caveat the code itself records: `engine/booking.go:94-109`, format `{first-name}-{duration}min-{8 hex}`, 32 bits. The comment at `:85-92` states that **this is generation only — no stored slug is rewritten and every slug already handed out keeps resolving**, explicit owner-chosen slugs included. So for every link created before 2026-11-22 the slug is still `{first-name}-{duration}min` and still guessable, and the rate limit is the only control on those links. |
| A rate limit is a throttle, not an end to harvesting. | **Confirmed and understated.** The limiter is 120 requests per client per fixed 1-minute window shared with `getPublicBookingSlots`, per process, keyed on one exact address with no IPv6 prefix grouping (`docs/security.md:92`; `ratelimit_booking.go:132`). It stops nothing distributed, and against a known slug it is irrelevant: one request returns every accepted host's address. |
| `GET /api/book/{slug}` is the exposure. | **Incomplete — there are two.** `publicHostDTO` is also the `hosts` array of the 201 `bookingConfirmationDTO` (`handlers_booking.go:77`, filled at `:149` by `confirmation`, written by `writeBooked` at `:622-629`). `POST /api/book/{slug}` is unauthenticated too. It is a weaker path (it needs a successful booking, and is limited to 20 per client per 10 minutes) but it is the same leak through the same schema, so the spec covers both. |
| smart-calendar-flow #207 stopped rendering host addresses, so the most likely consumer may no longer need the field. | **Cannot be checked from here** and is not assumed. It is W-1 in §2.4 and the single most load-bearing unverified claim in this spec. |
| `hostsLabel` printed `h.email.split("@")[0]` for a nameless host. | **Cannot be checked from here** (W-3). But the *shape* of the bug is checkable, and §2.6 reports what this repository does. |
| "Stop returning the field" and "stop rendering the field" are separate problems. | **Confirmed, and §3.3 is the consequence**: the reason a renderer reached for the local part is that `name` is *absent* for a nameless host (`name,omitempty`, `handlers_booking.go:46`). Removing `email` without fixing that leaves the next renderer with the same empty hole and nothing legitimate to fill it with. |

**One premise this spec adds.** Removing the address is worth doing and does **not**, on its own, make the public read privacy-safe. The same response still carries each accepted host's display name and avatar URL to anyone with the slug, and `coverage` still reports how many of them hold a Google token. §2.8 says so plainly and §8 OQ-1 puts the avatar in front of the owner rather than quietly widening scope.

## 1. Problem

Two unauthenticated operations answer with the email address of every person who has accepted to host a scheduling link. A booking page's slug is public by design — that is the whole point of the feature — so the address is disclosed to everyone the link is shared with, and to anyone who can guess or find a slug.

Who pays, concretely:

- **Someone who accepted a co-host invitation.** Accepting is consent to be *shown on the page* (the contract says so: `SchedulingLinkHost`'s description, `booking.yaml:3279`, reads "accepting is the invitee's consent to be shown"). It is not consent to have one's work address published to every recipient of a URL the link's owner pastes into an email signature, a Slack status or a website contact form (`docs/src/content/docs/scheduling/creating-a-link.mdx:33` recommends exactly that). The co-host does not control where the link goes and is never told.
- **Every owner of a link created before 2026-11-22.** Their slug is permanently `{first-name}-{duration}min` (`engine/booking.go:85-92`). A first-name dictionary sweep still resolves it; only the 120-per-minute limiter slows the sweep down, and that limiter is per process and per exact address.
- **Anyone who can read one slug.** The slug is not a secret the holder is asked to keep. One request, inside every rate limit, returns the roster. The fix shipped on 2026-11-22 raised the cost of *finding* a slug and capped the rate of *trying*; it changed nothing about what a single legitimate-looking request gets.
- **The next renderer.** A field that is returned will eventually be rendered, derived from, logged or put in a URL by a component nobody has written yet. The web sweep that found `hostsLabel` printing an address's local part on a page that had just stopped showing addresses is the evidence: not returning the field is the only control that does not depend on every future consumer being careful.

What makes this a spec rather than a patch: the premise is "no consumer needs this field", and that premise has to be **established**, not assumed. It cannot be fully established from inside this repository — one of the two consumers lives in another repo this session could not read. §2.3 and §2.4 report exactly how far the check got, and §3.8 and D-7 are shaped so that the one unverified consumer cannot be broken by the merge order.

## 2. Current behaviour

### 2.0 Names used in this spec

| Name | What it is today |
|---|---|
| **The public host entry** | `publicHostDTO`, `backend/api/handlers_booking.go:45-49`: `email` (always sent), `name` (`omitempty`), `avatar_url` (`omitempty`). Contract schema `PublicHost`, fragment `contracts/openapi/paths/booking.yaml:2992-3007`, bundle `contracts/openapi/openapi.yaml:15033-15047`. |
| **The two public reads** | `getPublicLinkInfo` — `GET /api/book/{slug}`, `handlers_booking.go:164-194`, answering `publicLinkDTO` (`:50-62`) whose `hosts` is `[]publicHostDTO`. And `createPublicBooking` — `POST /api/book/{slug}`, whose 201 is `bookingConfirmationDTO` (`:68-79`), likewise. Both `security: []`. |
| **The roster builders** | `publicLink` (`:94-116`) and `confirmation` (`:138-154`). Both read `storage.GetAcceptedHosts`, then `hostUser` (`:121-130`) per host, then append `publicHostDTO{Email: user.Email, Name: user.Name, AvatarURL: user.AvatarURL}` (`:112`, `:149`). A failed read of either is the operation's 500, never a degraded 200 (PAC-122 SF-027/SF-028). |
| **The owner-facing roster** | `schedulingHostDTO`, `handlers_scheduling_links.go:65-72`, on `GET /api/scheduling-links/{id}` and its family. Carries `email` too, and is readable only by the owner and accepted co-hosts (PAC-25, API-006). **Not touched by this work.** |
| **The coverage count** | `coverageDTO` (`handlers_booking.go:64-67`), `PublicLinkCoverage` in the contract. `total` already counts every accepted host; `checked` counts those holding a Google token row. Public today, by owner decision PAC-67 D-14. |

### 2.1 Who can read each exposure, and at what cost

| Operation | Auth | Rate limit at `308ac21` | What it costs an attacker | What they get |
|---|---|---|---|---|
| `GET /api/book/{slug}` | none (`booking.yaml:46`) | 120 / client / 1 min, shared with the slot listing, counted first (`handlers_booking.go:165`) | one request per slug | every accepted host's address, name, avatar; `coverage`; the link's title, durations, zone and horizon |
| `POST /api/book/{slug}` | none | 20 / client / 10 min (`NEW-booking-rate-limit`) | a *successful booking* — an offered slot, a valid address, a real calendar write on every host (PAC-72's offerable-start rule, `booking_rule.go`) | the same host roster, in the 201 |

The second is loud and self-limiting; the first is silent and cheap. Both are closed by the same change because both render the same schema.

### 2.2 What the contract says, and what removal means for the generated code

```yaml
PublicHost:
  type: object
  description: A host shown on the public booking page. Only accepted hosts appear.
  required: [email]
  additionalProperties: false
  properties:
    email: { type: string, format: email }
    name: { type: string, description: 'Omitted when the user record has no name (json `name,omitempty`).' }
    avatar_url: { type: string, description: 'Omitted when empty (json `avatar_url,omitempty`).' }
```
(`contracts/openapi/paths/booking.yaml:2992-3007`; identical in the bundle at `openapi.yaml:15033-15047`.)

Referenced from exactly two places: `PublicLinkInfo.hosts` (`booking.yaml:3055`, bundle `:15002`) and `PublicBookingConfirmation.hosts` (`booking.yaml:3259`, bundle `:15100`). No third reference anywhere in the bundle.

`email` is **required**, `type: string`, `format: email`, **not nullable**, and the object is `additionalProperties: false`.

- **Generated Go.** `backend/api/gen/paceday.gen.go:3184-3191` declares `PublicHost` with `Email openapi_types.Email` non-pointer and `AvatarUrl`/`Name` as `*string`. **Nothing in the backend uses `gen.PublicHost`** — grep for `gen.PublicHost` across `backend/` returns nothing; both operations are `handwritten` in `MIGRATION.md` and build their own `publicHostDTO`. So regenerating is mechanical and changes no behaviour; the behavioural change is in `publicHostDTO`.
- **Generated web types.** `smart-calendar-flow/src/api/generated`, produced from this bundle; `make openapi-check-web` (`Makefile:127-130`) fails when the two disagree. `OPENAPI_CROSS_REPO=skip` exists (`Makefile:106-112`) precisely because the two mains disagree for the minutes between a paired merge pair. **This is why D-7 and §3.8 specify the merge order.**
- **Is removal breaking for a client that reads the field?** Yes, and in the worst of the two possible ways. Removing a `required` property makes the key **absent**, not empty. A TypeScript client compiled against the old generated types reads `host.email` as `undefined`; `host.email.split("@")[0]` — the exact expression the web sweep found — **throws a `TypeError` and takes the component down**, where an empty string would merely have rendered blank. A client that validates the response against the old schema fails validation on `required`. A Go client with `openapi_types.Email` gets the zero value and no error, because `encoding/json` does not enforce `required`.
- **`required: []` is not legal.** With `email` gone, `required` would hold nothing, and OpenAPI forbids an empty `required` array — it must be omitted. Combined with `additionalProperties: false` and two `omitempty` properties, the schema would then permit `{}` as a valid host. §3.3 closes that by making `name` required, which is also the fix for the nameless-host hole.

### 2.3 Consumers in this repository: what was checked, and what was found

| Consumer | Reads a host address from a public booking answer? | Evidence | Verified |
|---|---|---|---|
| **The MCP server** (`mcp/`) | **No. It calls no booking endpoint at all.** | Every literal API path in `mcp/` (non-test): `/api/attendees/suggest`, `/api/auth/status`, `/api/calendar/events`, `/api/calendar/freebusy`, `/api/events/`, `/api/focus/blocks`, `/api/focus/run`, `/api/nlp/parse`, `/api/rooms`, `/api/schedule/compress`, `/api/schedule/compress/apply`, `/api/schedule/create`, `/api/schedule/suggest`, `/api/settings/` — all in `mcp/tools.go`. No `/api/book` anywhere in `mcp/`. Paths are passed as literals into `backendClient.get`/`post`/`patch` (`mcp/client.go:36-66`); none is assembled from a variable. `hosts` does not appear in any non-test Go file in `mcp/`. | **yes** |
| **The e2e suite** (`e2e/`) — two assertions break | **Yes, two.** `public-booking.spec.ts:71-74` asserts `body.hosts[0]` matches `{ email: BOOKING_HOSTS.ui.email, name: BOOKING_HOSTS.ui.name }` on the `GET`. `public-booking.spec.ts:170-172` asserts the 201's `conf.hosts` equals `[objectContaining({ email: BOOKING_HOSTS.api.email })]`. Both must become name-based; the seeded hosts have distinct names (`e2e/seed/ids.ts:136-152`), so the rewrite loses no discrimination. | **yes** |
| **The e2e suite** — survives unchanged | `pac88-zones.spec.ts:77-82` checks only `hosts` **length**. `pac40-lifecycle.spec.ts:89, :116-118, :337-345` check status and error bodies only. `framing.spec.ts:207-208` checks headers only. No e2e test asserts a host address is **visible on the rendered page**; the only `getByText` on an address is the *booker's own* (`public-booking.spec.ts:270`). | **yes** |
| **Backend tests** — two break | `handlers_booking_test.go:138` asserts `got.Hosts[0].Email != "public-link-owner@example.com"` on the `GET`. `pac122_b1_test.go:58` uses `strings.Contains(w.Body.String(), "-accepted@example.com")` as its *baseline* that the roster is present before it breaks the read — an SF-027 regression guard that happens to key on the address. Both must be rewritten; neither criterion they protect changes. | **yes** |
| **Backend tests** — survive unchanged | `pac67_surfaces_test.go:334, :363` compare the whole link-info body before and after a credential loss and never name an address. `security_audit_test.go:271-308` (the new rate-limit tests) assert status codes. Grepping `Hosts` across `backend/api/*_test.go` returns no other assertion on a public host address. | **yes** |
| **The docs site** (`docs/src/content`) | **No.** No page mentions a host address on the booking page. `what-are-scheduling-links.mdx:8` describes the page as "showing only the times you've made available"; `creating-a-link.mdx:35` describes what the *booker* types. `collective-scheduling.mdx:14` is about typing a co-host's address into the editor, which is unchanged. **No docs-site change is needed**, which is itself a useful fact: nothing was ever promised. | **yes** |
| **Operator tooling, nginx, compose tests** | **No.** `/api/book` appears in `nginx/test/partc_test.go:38-39` (publication-surface assertions), `nginx/nginx.conf:69` (a comment naming the web app), and `compose-test/compose_test.go:281, :322` (a GET of an unknown slug, held behind a table lock). None reads a response body's hosts. `scripts/` and `tools/` contain no reference to the route. | **yes** |
| **A webhook, a published API, a third-party integration** | **None exists.** There is no webhook anywhere in `backend/`, `mcp/` or `contracts/` (grep for `webhook`: no match in any Go or YAML file). The bundle is not published by the docs site — `docs/astro.config.mjs` and `docs/package.json` reference no OpenAPI or Swagger integration, and `docs/src/content/docs/reference` holds no API page. The only programmatic bearer-token client of the backend is the MCP server, which ships from this same repository (`docker-compose.mcp.yml`) and is covered above. | **yes, to the limit of this repository** |

**Can consumers be enumerated?** For this repository, yes, and the enumeration above is complete. For the product as a whole, **no with one named gap**: smart-calendar-flow (§2.4). There is no mechanism by which a third party could hold a client — no API key issuance, no OAuth app registration, no published contract — so the gap is one repository, not an unknown population. **That is what makes this a removal rather than a deprecation** (D-6): a deprecation window exists to protect clients you cannot deploy alongside the server, and there are none.

### 2.4 The web app — what a reader must open `[unverified-web]`

smart-calendar-flow was not reachable from this worktree. Six claims are therefore unverified, each with the file to open. All are on `origin/main` of `Enach/smart-calendar-flow`.

| id | Claim to settle | File a reader must open |
|---|---|---|
| **W-1** | After #207, nothing on the public booking page reads `host.email` — not for display, not for a label, not for a `key`, not for a `mailto:`, not for an avatar fallback. | `src/pages/PublicBooking.tsx`. Cited as the public booking page by `NEW-booking-rate-limit.md:146, :617, :638, :646` and `docs/plans/PAC-81-part-c.md:30` (`<Route path="/book/:slug" element={<PublicBooking />} />`). Grep the file for `email`. |
| **W-2** | The transport layer that fetches both public operations does not read, log or cache a host address. | `src/api/schedulingLinks.ts`, named as the only web caller of the public routes by `NEW-booking-rate-limit.md:654-656`. |
| **W-3** | `hostsLabel`'s `h.email.split("@")[0]` fallback — the local-part leak the web sweep found — is gone, and no other derivation from a host address remains. | Whatever builds the host label. The sweep reported it inside `PublicBooking`; if it has been extracted, grep the repo for `hostsLabel` and for `split('@')` / `split("@")`. |
| **W-4** | `PublicHost` in the generated types is referenced only where #207 left it, so regenerating without `email` is a compile-clean change. | `src/api/generated` (the directory `make openapi-check-web` compares, per `.github/workflows/release.yml:20, :163`), plus every importer of the `PublicHost` type. |
| **W-5** | Nothing on the page renders a *nameless* host by falling back to anything derived from an address, and the page has a placeholder for a host with no display name. | `src/pages/PublicBooking.tsx`, the host-list render. This is what §3.3 exists to make unnecessary, and the reader should confirm the placeholder rather than assume one. |
| **W-6** | The page still has **no mapped behaviour for the 429** that PR #332 added to `getPublicLinkInfo`, so a rate-limited visitor gets the generic branch's raw server string and a failure toast. Reported here because it is the live example of the failure this spec's §3.9 exists to prevent, and because it is on the same page and the same operation. | `src/pages/PublicBooking.tsx` — the status mapping around `:165-183` as cited by `NEW-booking-rate-limit.md:638, :646-650`. **Not this spec's to fix** (§5, FU-3), but it must be filed. |

**If W-1 or W-3 turns out false** — the field is still read somewhere — the work is not blocked, but §3.8's order becomes mandatory rather than merely prudent: the web read must go first, in its own unpaired PR, before the contract change. D-7 is written to hold either way.

### 2.5 Does anything legitimately need the address? No client does

The booking flow is complete server-side. Nothing a client does requires it to know a host's address.

- **The hosts' calendar events** are created by the server with the server's own stored credential, one event per host, each carrying the other hosts listed so far plus the booker as attendees: `engine/booking.go:719-772` (`completeBooking`), attendees built at `:734-739`, inserted at `:762`. The address comes from `storage.GetUserByIDNoOrg` inside the engine (`:727`), never from a request.
- **The booker's confirmation email and ICS** are composed and sent by the server: `engine/booking.go:782` → `sendBookingEmail` (`:828-836`) → `sendConfirmationEmail` (`:872-941`) and `generateICS` (`:955-979`). The recipient is `booking.BookerEmail` — the address the booker typed. **Host addresses do not appear in the confirmation at all**: what goes into the ICS `DESCRIPTION` and the mail body is `hostNames`, the join of `u.Name` (`:733`, `:782`), not addresses.
- **The booking request** carries only `name`, `email`, `start`, `notes` and an optional duration (`handlers_booking.go:408-440`). It names no host. Which hosts are involved is derived server-side from the slug.

So the field is **decorative on both responses**: it exists only to be displayed. There is no flow in which removing it makes a booking fail, and no flow in which a client must supply or echo it. If some flow in the web app does need it, that flow is W-1's business and would be the reason the field exists — but no server-side flow does.

### 2.6 `hostsLabel`'s local-part leak: is there an analogue in this repository?

**The pattern exists; the leak does not.** Grepping every `Split`/`SplitN`/`Index`/`Cut` on `"@"` in non-generated Go across `backend/` and `mcp/` gives nine hits. Seven are production code; two are tests.

| Site | What it does | Is it a leak? |
|---|---|---|
| `api/handlers_manager.go:347` (add a 1:1 member), `:460` (patch one) | `displayName = strings.Split(email, "@")[0]` when the caller sent no `display_name` | **No.** The address is the one **the caller themselves put in this request**. The derived label is returned only to that caller, on an authenticated route. Nothing is disclosed that the reader did not supply. |
| `api/handlers_manager.go:717`, `:742`, `:814` | the same fallback for a formal team member, an added person, and a formal-member match | **No.** Authenticated manager routes; the addresses come from the viewer's own team roster or organisation list, which the same viewer can already read (PAC-130 D-1, PAC-129). |
| `domain/domain.go:31`, `engine/freebusy_service.go:289-294` | extract the **domain**, not the local part, for org matching and free/busy provider selection | **No.** Not a display value, and the domain is not the identifying half. |
| `api/pac150_test.go:47`, `api/pac122_b3_test.go:142`, `api/pac148_test.go:423` | test helpers asserting that an address, or its local part, **did not** reach an answer or a log | **No — the opposite.** `pac150AssertNoPersonalData` (`pac150_test.go:43-58`) is a guard of exactly the kind this spec wants. |

**Conclusion: the analogue is out of scope, and that is a finding, not an omission.** The distinguishing property is who learns something: a label derived from an address the reader already holds discloses nothing, and every derivation in this backend is of that kind. The web's `hostsLabel` was different in exactly one respect — it rendered a local part derived from an address its *reader* had not supplied, on an unauthenticated page. Removing the field removes the only input that made that possible on the backend side.

One place is worth naming because this work changes its weight: **`users.name` is never request input.** It is written only at sign-in, from the identity provider's profile, and is fitted to one line there by `safetext.Label` (`storage/signin.go:99-108`, written at `:207` and `:272`). `UPDATE users SET name` appears nowhere else in non-test Go. After this work `name` becomes the only host-identifying string in the public answer, so §3.7 states what is and is not done to it on the way out.

### 2.7 Tests that pin today's behaviour

Four assertions encode the address into a passing test today. None of them is *about* the address; each uses it as a convenient witness.

| Test | Line | What it asserts today | Why it must change |
|---|---|---|---|
| `backend/api/handlers_booking_test.go` `TestGetLinkInfo…` | `:138` | `len(got.Durations) != 2 \|\| len(got.Hosts) != 1 \|\| got.Hosts[0].Email != "public-link-owner@example.com"` | the field is gone; the host must be identified by `Name` |
| `backend/api/pac122_b1_test.go` `TestPAC122_SF027_PublicLinkRosterFailureIs500` | `:58` | baseline: the 200 body contains `-accepted@example.com`, proving the roster is present before the table is hidden | same, and note that **`createTestUser` gives every test user the identical name `"Test User"`** (`handlers_teams_test.go:57-60`), so a name-based baseline needs the fixture to set a distinct name — the `pac25LinkFixture` already sets a per-user `avatar_url` (`handlers_scheduling_links_test.go:610-614`) and should do the same for `name` |
| `e2e/tests/public-booking.spec.ts` | `:71-74` | `body.hosts[0]` matches `{ email, name }` of `BOOKING_HOSTS.ui` | seeded hosts have distinct names (`e2e/seed/ids.ts:141, :149`), so `{ name }` alone discriminates |
| `e2e/tests/public-booking.spec.ts` | `:170-172` | the 201's `conf.hosts` equals `[objectContaining({ email })]` | same |

**No test anywhere asserts that a host address is *absent* from a public answer.** That absence is the gap this work closes, and AC-1 and AC-2 are written to fail today for that reason.

### 2.8 Residual exposure this spec does not close, stated plainly

1. **Name and avatar stay public.** The same unauthenticated answer still carries every accepted host's display name and `avatar_url`. For a Google account the avatar URL is a `*.googleusercontent.com` URL containing a stable per-account identifier (the CSP already allow-lists that host for images, `docs/security.md:55`). A name plus a stable image identifier is still personal data on an unauthenticated route. **In scope for a decision, not for a change: OQ-1.** The default this spec is written to is that both stay, because showing who you are about to meet is the page's purpose.
2. **`coverage` stays public.** `total` is a count of accepted hosts and `checked` says how many hold a Google token. Owner decision PAC-67 D-14 made that deliberate (counting only *usable* tokens would have told every visitor that a host's connection was dead). Unchanged, and §6 protects it.
3. **A booker who completes a booking may still learn host addresses — through Google, not through Paceday.** `completeBooking` puts each host and the booker on the host's calendar event as attendees (`engine/booking.go:734-739`). The insert sets no `sendUpdates` (`:753-761`), so Google sends no invitation mail; whether the event, with its attendee list, nonetheless appears on the booker's own calendar depends on Google's delivery behaviour and the booker's settings. **Not verified end-to-end by this session**, and not fixable in this API: it is a property of inviting someone to a calendar event. This work defends against *harvesting without booking*, which is the cheap attack; it does not and cannot hide a host from someone who actually books a meeting with them.
4. **Pre-2026-11-22 slugs stay guessable.** `engine/booking.go:85-92` is explicit that no stored slug was rewritten. Those links keep `{first-name}-{duration}min`. Re-slugging existing links is a different ticket with a broken-link cost; **named out of scope in §5.**

## 3. Desired behaviour

Written from the point of view of a person who accepted a co-host invitation, and of the visitor who opens the page.

### 3.1 Neither public answer carries a host's email address [F, D-1]

The host entry of `getPublicLinkInfo`'s 200 and of `createPublicBooking`'s 201 carries no email address, in any field, under any name, in any encoding. Not as `email`; not hashed; not as a local part; not as an initial; not inside a label.

This holds for every host, every status that reaches the array (only `accepted` does, unchanged), every link, and both operations. It holds on the replay 201 of an idempotent retry as well as on a fresh save.

### 3.2 Nothing is added in its place [F, D-2]

The page already has everything it needs to say something true about the hosts:

- **who** — `name`, already present;
- **what they look like** — `avatar_url`, already present;
- **how many** — `coverage.total`, already present and documented as counting every accepted host.

So: **no count field, no opaque per-link host identifier, no placeholder address.** The three alternatives the brief named were weighed and are recorded in D-2 with the reason each was declined. The shortest defensible change is the one that cannot leak, and adding a new field to an unauthenticated response in a spec whose purpose is to narrow one is the wrong direction.

### 3.3 A host with no display name is answered honestly, and the key is always there [F, D-3]

`name` becomes **required and always present**, possibly the empty string. Today it is `omitempty` (`handlers_booking.go:46`), so a host whose account carries no name — `name` is optional in OIDC, and `slugBase`'s comment at `engine/booking.go:120-124` records that this really happens — appears as a host entry with **no name key at all**.

That absence is why a renderer reached for the address's local part. Three things follow:

- the key is always present, so a consumer never has to invent a value;
- the contract states, in so many words, that an empty `name` means the host has no display name and that the client must render **its own** placeholder — and that there is no longer any field it could derive one from;
- `PublicHost` keeps a non-empty `required` list, so the schema does not degrade to permitting `{}` (§2.2).

### 3.4 The host array's order becomes deterministic [F, D-4]

`storage.GetAcceptedHosts` (`storage/scheduling_links.go:402-420`) has **no `ORDER BY`**, so the order of `hosts` is unspecified today and PostgreSQL may change it between reads. That was tolerable while every entry carried a unique address a client could key on. Removing the address removes that key, so the order becomes the only thing a client can rely on, and it must therefore be stable: the array is ordered deterministically, the same for two reads of an unchanged link.

This is inherited scope in its strictest sense — the removal is what makes it matter — and it is cheap. It is also observable, so AC-5 can fail.

### 3.5 The owner's view of their own link is unchanged [F, D-5]

Everything an owner or accepted co-host can see today about who hosts a link, they can still see, addresses included: `GET /api/scheduling-links/{id}` and its family keep `schedulingHostDTO.email` exactly as PAC-25 and PAC-149 left it. This work takes nothing from the people entitled to the information; it stops publishing it to people who are not.

### 3.6 The refusal, the rate limit and every failure answer are untouched

No status code, no error body, no evaluation order and no limiter changes. In particular `getLinkInfo`'s limiter stays its first statement, the 404/410/429/500 bodies are byte-identical, and a failed roster read is still the 500 rather than a 200 with an emptied or short `hosts` (PAC-122 SF-027/SF-028). The *only* observable difference is which keys appear inside each host entry.

### 3.7 No sanitisation is added on the response path [F, D-8]

The brief asks whether `safetext.Label`, which PR #332 put on the mail path, should also apply on a response path. **No, and the reasoning matters because `name` becomes the only host-identifying string in the answer:**

- `users.name` is **already** `safetext.Label`'d, at the one place it is written — sign-in (`storage/signin.go:108`, with the reason recorded in the comment at `:100-107`). So a value written since 2026-11-22 carries no CR, LF or NUL and is at most 200 runes.
- `encoding/json` escapes control characters when encoding, so a JSON response cannot be line-injected regardless of what is stored. The composers that *do* need it are line-oriented (mail headers, ICS content lines); a JSON response is not, which is exactly the distinction `safetext`'s package comment draws (`backend/internal/safetext/safetext.go:1-27`).
- A name stored **before** that change was never fitted to a line and is returned as-is. In JSON that is harmless; in a renderer that puts it into a line-oriented context it would not be. That is the consumer's boundary, and §6 protects the composers that already guard it.

So: nothing is added at the response boundary, and §6 pins that no response value is silently repaired or truncated. The one thing that **does** change is §3.3's always-present `name`, which means an empty string now appears where a key used to be absent.

### 3.8 Compatibility, and the order the two repositories must merge in [F, D-7]

Removal makes the key **absent**, which is the harsher of the two failure modes for an unprepared client (§2.2). The factory's own mechanism decides how to handle that, not a preference:

- `make openapi-check-web` requires the web's `src/api/generated` to match this bundle, so **the web cannot regenerate its types before the contract changes** — a web-first regeneration fails its own gate.
- Paired halves carry the same `pac-N` branch token and merge minutes apart (`Makefile:106-112`, `docs/factory/README.md` §Paired branches), so there *is* a window on `main` where one half is ahead.
- The factory's usual stage-4 order is backend first, then frontend (`README.md` §Stage 4, and `frontend-implementer` "only once the backend stream is merged"). **For a response-field removal that order is backwards**: between the two merges the web on `main` would be reading an absent key.

The sequence is therefore:

1. **Web, unpaired, first.** A PR on a branch carrying **no `pac-N` token** (so `scripts/ci/pair-branch.sh` rule 1 pairs it against api `main`) removes every read of `host.email` from hand-written web code. The generated types are **not** touched, the bundle is unchanged, and `openapi-check-web` passes. Against the current API this is a no-op — the field is still sent, just unread. **If #207 already did this, this step is already done and the reader confirms it via W-1 and W-3 rather than redoing it.**
2. **Both repos, paired, second.** One `factory/pac-N-…` branch in each repo: api removes the field from the fragment, the bundle, `publicHostDTO` and the tests; web regenerates `src/api/generated`. Both PRs carry the token so CI pairs them; the web half compiles either way because step 1 left nothing reading the field.

**No `deprecated: true` phase, no two-release window, no empty-string interim** (D-6). PAC-130 D-4's deprecation precedent (`docs/specs/PAC-130.md:497`; the contract text at `openapi.yaml:13908-13923`) applies when the deprecated value is **already valid under the existing type** — `member_user_id` was required *and nullable*, so `null` broke nobody. Here `""` is invalid under `format: email`, so an interim empty string needs a type change of its own, and it would keep the address's *place* in the response while the thing it exists to prevent — a consumer rendering it — would be unaffected. It would be ceremony, not protection. PAC-24's precedent is the applicable one: it removed `PUT /api/settings` outright rather than retaining a deprecated alias (`openapi.yaml:9346`, `paths/scheduling.yaml:1772`).

**Both operations stay `handwritten` in `MIGRATION.md`**, with a note added. Moving `getPublicLinkInfo` to the generated wrapper would put the generated binder ahead of the limiter that PR #332 deliberately made the handler's first statement, which is the same reason `getPublicBookingSlots` is recorded as kept handwritten (`MIGRATION.md`, that row's note). The `generated` count must not move.

**The web must have a mapped behaviour before the api half merges** [F, D-11]. Step 1 is not only "stop reading `email`" — it is "render the hosts correctly from what the API will send". That means a named host renders as today, and a **nameless** host renders the page's own placeholder (§3.3) rather than a blank row, a missing `key` or a crash. The window between the two merges is minutes on `main` (`Makefile:106-112`) and real visitors are in it, so a behaviour that is only correct after the second merge is not acceptable. The precedent is immediate: PR #332 added a documented **429 with a `Retry-After` header** to `getPublicLinkInfo` (`contracts/openapi/paths/booking.yaml:70-71`, `$ref: '#/components/responses/BookingSlotsRateLimited'`), which is part of the operation's shape and therefore reached the generated types; the web page has no mapping for it, so a rate-limited visitor falls to the generic branch that prints the raw server string (`NEW-booking-rate-limit.md:646-650`, `src/pages/PublicBooking.tsx:181-183` **[unverified-web]**, W-6). A status code nobody mentioned is a status code nobody handled, and the same is true of a key nobody mentioned going absent.

**What the page shows instead.** Less information reaches the page, so this spec says what it should show rather than only what the API stops sending: each accepted host as `name` plus `avatar_url`, in the order the API returns them (§3.4); for a host whose `name` is the empty string, a neutral placeholder chosen by the page in its own copy and locale — **never** a value derived from an identifier, because after this change there is none to derive from. The count of hosts is `coverage.total`, as today. Nothing else about the page changes.

### 3.9 The full downstream shape of this change

Removing a property from a response is a **shape change**, so the prose above is not the whole of it. This section is the enumeration the implementing session **must reproduce in its PR description**, item by item. Reasoning about where values live is how the two mistakes of 2026-11-22 happened: #331 was described as "two doc comments" and was five hunks across three files, and #332 was described as not reaching the generated types while it added a status code that did. **The honest way to confirm this list is to run `make openapi` and diff the bundle, and to regenerate both repos' types and diff those — not to reason about it.**

**Schemas touched**

| Schema | Change | `required` before → after |
|---|---|---|
| `PublicHost` (`paths/booking.yaml:2992-3007`, bundle `openapi.yaml:15033-15047`) | `email` property **removed** (`type: string`, `format: email`); `name` loses its "Omitted when…" description and becomes required; the schema's own `description` is rewritten | `[email]` → `[name]`. An empty `required` list is illegal, which is one of the reasons `name` becomes required (§3.3). |
| No other schema | — | — |

`PublicHost` is referenced from exactly two places and **gains and loses no reference**: `PublicLinkInfo.hosts` (`booking.yaml:3055`, bundle `:15002`) and `PublicBookingConfirmation.hosts` (`booking.yaml:3259`, bundle `:15100`). Neither parent schema's own `required` list changes — `hosts` stays required on both.

**Operations whose response shape changes**

| Operation | Response | Change |
|---|---|---|
| `getPublicLinkInfo` `GET /api/book/{slug}` | 200 | each `hosts[]` entry loses `email` and always carries `name` |
| `createPublicBooking` `POST /api/book/{slug}` | 201 (fresh **and** idempotent replay) | the same |

**No other operation's response shape changes.** In particular the authenticated scheduling-link family keeps `schedulingHostDTO.email` (§3.5), and its contract prose about host addresses **must not be touched**: `booking.yaml:962`, `:1071`, `:1430`, `:1434`, `:1456`, `:1603`, `:1652` all describe `SchedulingLink.hosts`, not `PublicHost`. A blanket find-and-replace over "host's email" would silently rewrite seven true statements; this list exists so that it does not.

**Status codes, response headers, referenced components**

- **None added. None removed.** Both operations keep exactly their current sets: `getPublicLinkInfo` 200 / 404 / 410 / 429 / 500; `createPublicBooking` its current set unchanged. The 429's `Retry-After` and the `BookingSlotsRateLimited` / `BookingError` component references are untouched.
- No request shape, parameter, header or `security` block changes anywhere.
- If a bundle diff shows a status code, header or component reference appearing or disappearing, the change has gone wrong and the PR must stop.

**Prose hunks in the contract, enumerated**

1. `paths/booking.yaml:38-42` — `getPublicLinkInfo`'s description currently states "This operation answers with every accepted host's address". **Must change**; it becomes the record of what it used to answer and what it answers now.
2. `paths/booking.yaml:2994` — `PublicHost`'s own description.
3. `paths/booking.yaml:3001-3003` — `name`'s "Omitted when the user record has no name (json `name,omitempty`)", which stops being true.
4. `paths/booking.yaml:826` (bundle `openapi.yaml:8115`) — `createSchedulingLink`'s description, recording that the pre-audit sweep "harvested every host's address". Historically true and should stay, **amended** to say the address is no longer in that answer; left as-is, a reader concludes the field is still returned.
5. Each edited fragment regenerates its bundle counterpart via `make openapi`, so **the bundle diff is larger than the fragment diff** and both must be in the PR.

**Checked and deliberately not changed** (they would look like candidates to a reader sweeping for the word): `paths/auth.yaml:1237` and `:2705` (bundle `openapi.yaml:2485`, `:12352`) say the current-user picture is also `avatar_url` in `SchedulingLinkHost` and `PublicHost` — still true, because `avatar_url` is kept. The `getPublicLinkInfo` 500 description's "never a 200 with `hosts` emptied, a host left out or `coverage.total` undercounted" names no address and stays verbatim.

**Generated code**

- **Go** (`backend/api/gen/paceday.gen.go:3184-3191`): `PublicHost.Email openapi_types.Email` disappears; `Name *string` becomes `Name string`; `AvatarUrl *string` is unchanged. **No behaviour follows**, because nothing in `backend/` uses `gen.PublicHost` (§2.2). `format: email` appears 31 times in the bundle and `openapi_types.Email` 26 times in the generated file, so the import survives; a diff showing the import dropped means something else was removed too.
- **Web** (`smart-calendar-flow/src/api/generated`): the `PublicHost` type loses `email` and `name` stops being optional. Every importer of that type is a compile-time site; W-4 is the list a reader must walk.
- **No generated file may be hand-edited** (`docs/factory/README.md` §Paired branches, rule 3).

**Field ordering, and the honest byte-level answer**

`publicHostDTO`'s JSON keys are emitted in Go struct declaration order, so today each host entry begins `{"email":…,"name":…,"avatar_url":…}` and after this change begins `{"name":…,"avatar_url":…}`. **The first key of every host entry changes.** Anything that compares a public answer byte-for-byte, hashes it, or snapshots it, changes — which is why AC-8's byte-identity claim is scoped to the fields *outside* `hosts`, and why the four assertions of §2.7 are rewritten rather than adjusted. No other field's position moves, and no parent object's key order changes.

**What a client reading the field sees after removal**

- **TypeScript compiled against the new types**: a compile error at every read site. This is the good case and is what step 1 of §3.8 converts into a deliberate change.
- **TypeScript compiled against the old types, running against the new server**: `host.email` is `undefined`. A render of it shows nothing; `host.email.split("@")[0]` — the expression the web sweep found — throws a `TypeError` and takes the component down. **This is the case the merge window creates and D-7 and D-11 exist to close.**
- **A client validating responses against the old schema**: fails on `required: [email]`.
- **A Go client using the old generated type**: `Email` is the zero value and no error is raised, because `encoding/json` does not enforce `required`. It fails silently, which is worse than loudly.

## 4. Acceptance criteria

Layers: `[contract]` a Go test against the HTTP handler and the bundle's shapes; `[unit]` a Go test below HTTP; `[e2e]` Playwright on the compose stack; `[doc]` a document assertion; `[web]` a test in smart-calendar-flow.

**Tests before code.** The suite is a new `backend/api/public_host_address_test.go`, plus rewrites of the four assertions in §2.7. Stage 3 names the file per criterion. No criterion below may be proven by a test that exists today — §2.7 establishes that none does.

**Fixtures.** **P** is a live reusable link with two accepted hosts, **H1** (name "Alpha Host", avatar set) and **H2** (name "Beta Host", avatar set). **N** is a live link whose single accepted host **H0** has an account with `name = ''`. **O** owns P and N. **B** is an anonymous booker.

**The address is gone, from both answers**

AC-1. Given P, when B requests `GET /api/book/{slug}` without credentials, then the response is 200 and **no byte of the body matches H1's or H2's address, that address's local part, or that local part with the `@` percent- or unicode-escaped** — asserted over the raw body, not over a decoded struct. *Fails today: `handlers_booking.go:112` sends both addresses.* [contract]

AC-2. Given P and an offered slot, when B books it, then the 201 body satisfies AC-1's assertion; and given the same request replayed with the same `Idempotency-Key`, the replay 201 satisfies it too. *Fails today: `handlers_booking.go:149`, both on the fresh save and on the replay path through `writeBooked`.* [contract]

AC-3. Given P, the 200 body's `hosts` entries have exactly the keys the contract declares and no other — asserted by decoding into a map and comparing key sets, so a field renamed rather than removed fails. The same for the 201. *Fails today.* [contract]

AC-4. The assembled bundle's `PublicHost` has no `email` property, is referenced by exactly `PublicLinkInfo.hosts` and `PublicBookingConfirmation.hosts`, declares a non-empty `required` list, and keeps `additionalProperties: false`. No other schema in the bundle gains or loses a property. *Fails today.* [contract]

**What replaces it, and the nameless host**

AC-5. Given P read twice with no intervening write, the two 200 bodies are byte-identical, `hosts` order included; and given P with H2 removed and re-accepted, the order is still deterministic for two further reads. *Fails today: `GetAcceptedHosts` has no `ORDER BY` (`storage/scheduling_links.go:402-406`), so this criterion is the only thing that would detect a reordering.* [contract]

AC-6. Given N, the 200's single host entry **carries a `name` key whose value is the empty string** — present, not omitted. *Fails today: `name,omitempty` drops the key entirely.* [contract]

AC-7. Given P, the 200 still carries `coverage: {total: 2, checked: …}` and each host's `name` and `avatar_url` exactly as today. *Regression guard; passes today except for the `email` key's absence, which AC-3 owns.* [contract]

**Nothing else about these two operations changes**

AC-8. For P, the 200's `slug`, `title`, `durations`, `min_notice_minutes`, `usage_type`, `time_zone` and `booking_horizon_days` are byte-identical to today's. For the 201, every field other than `hosts` is byte-identical. *Regression guard.* [contract]

AC-9. `getLinkInfo` still refuses before it looks a slug up: a client at the slot-listing limit requesting an unknown slug gets 429, not 404. `security_audit_test.go:301-308` passes untouched. *Regression guard.* [contract]

AC-10. Given P and a hidden `scheduling_link_hosts` table, the 200 becomes the documented 500 `{"error":"internal error"}` with no part of the roster in the body and **no address in the log line or the Sentry report**; the same with `users` hidden; and the same on the 201 path, for a fresh save and for a replay. `TestPAC122_SF027_*` and `TestPAC122_SF028_*` pass, with their **baseline assertions rewritten to key on the host's name, not its address** — and the `pac25LinkFixture` gives its accepted host a distinct name so that baseline discriminates. *Regression guard whose test must change without its criterion changing.* [contract]

AC-11. `MIGRATION.md`'s `generated` count is unchanged, and `getPublicLinkInfo` and `createPublicBooking` are still `handwritten`, each with a note naming this work. *Fails today only in the note.* [doc]

AC-11a. **The shape enumeration is reproduced and is true.** The implementation PR's description carries §3.9's enumeration, and each line of it is checkable against the diff in the same PR: the fragment diff, the regenerated bundle diff, and both repos' regenerated types. Specifically: exactly one schema changed; exactly two operations' response shapes changed; **zero** status codes, response headers and component references added or removed; the four prose hunks of §3.9 present and the seven authenticated-roster statements (`booking.yaml:962`, `:1071`, `:1430`, `:1434`, `:1456`, `:1603`, `:1652`) untouched; the `openapi_types` import still present in the regenerated Go. A claim in that description that the diff does not support fails this criterion. *Fails today: no such description exists.* [doc]

AC-11b. No status code's behaviour or documentation on either operation is altered as a side effect: `getPublicLinkInfo` still declares exactly 200, 404, 410, 429 and 500, the 429 still `$ref`s `BookingSlotsRateLimited` with its `Retry-After`, and `createPublicBooking`'s set is unchanged. Asserted against the assembled bundle, so a stray `$ref` edit fails it. *Regression guard; the reason it exists is PR #332's unannounced 429 (§3.8, W-6).* [contract]

**The whole stack, and the web**

AC-12. The e2e public-booking journey passes: an anonymous visitor opens `/book/{slug}`, sees the hosts, picks a slot, books it, and sees the confirmation. The two assertions at `public-booking.spec.ts:71-74` and `:170-172` are rewritten to identify the host by `name`, and the suite additionally asserts that neither answer's raw body contains the host's seeded address. *The last part fails today.* [e2e]

AC-13. The rendered public booking page shows no host address and no value derived from one, for a named host and for a nameless one; a nameless host renders the page's own placeholder. `[web]` **[unverified-web]** — the criterion is stated here, and whether it passes today depends on W-1, W-3 and W-5. The reader settles it in `src/pages/PublicBooking.tsx`.

AC-13a. **The web renders correctly against both contracts, before the api half merges.** With a mocked response that carries `email` (today's server) and with one that does not (after the api half), the page renders the same host list; and for a host whose `name` is the empty string it renders the page's placeholder, with a stable list key, and logs no error. This is the criterion that makes the merge window safe, and it must be green on the step-1 web PR — not on the paired pair. `[web]` **[unverified-web]**

**The record**

AC-14. `docs/security.md` carries this disclosure as **its own entry**, not as a clause of the struck-through rate-limit entry at `:92`, stating what was returned, what is returned now, and the two residuals of §2.8 (name and avatar stay public; a completed booking still puts hosts on a calendar event with the booker). `docs/factory/api-audit.md` carries the row D-9 opens, marked Fixed by this work. The B7 row of the threat table (`docs/security.md:61`) is corrected where it describes the public booking surface. [doc]

## 5. Explicitly out of scope

- **`schedulingHostDTO.email`** on the authenticated link reads (`handlers_scheduling_links.go:67`). The owner and accepted co-hosts are entitled to it, PAC-25 gated it and PAC-149 narrowed what a *pending* or *declined* entry shows. Nothing here touches it (§3.5, AC-7 of nothing — it has no criterion because it must not change).
- **`avatar_url` and `name` on the public answer.** Both stay. They are the page's purpose, and narrowing them is a product decision, not a security fix with an obvious shape. Raised as **OQ-1**, with "keep" as the default this spec is written to.
- **`coverage`.** Owner decision PAC-67 D-14 made its present meaning deliberate.
- **Re-slugging links created before 2026-11-22.** Their slugs are permanently guessable (§2.8 item 4) and the rate limiter is their only control. Rewriting them breaks every link already handed out; it is a separate ticket with a user-visible cost. Follow-up **FU-1**.
- **Tightening the rate limit, or adding an nginx-level one.** `docs/security.md:92` lists the open limiter caveats (per process, one exact address, no IPv6 prefix grouping, distributed floods). Unchanged. Follow-up **FU-2**.
- **Giving the public booking page a mapped behaviour for the 429** PR #332 added to `getPublicLinkInfo` (W-6). It is a real defect on the same page and the same operation, found while checking this spec's premises, and it is left out deliberately: it is a web-only change to a status mapping, with no contract or backend component, so folding it in would mean this work's api half gates a fix that does not need it. Follow-up **FU-3**, and it should be filed at intake beside this spec's own issue. §3.9 cites it as the precedent, which is a different thing from fixing it.
- **The local-part derivations in `handlers_manager.go`** (`:347`, `:460`, `:717`, `:742`, `:814`). §2.6 establishes they disclose nothing to a reader who did not supply the address. Deliberately passed over, with the reason recorded, rather than left unexamined.
- **Any response-path sanitiser** (§3.7, D-8).
- **Migrating either operation to the generated strict wrapper.** It would put the generated binder ahead of the limiter PR #332 made first (§3.8). `MIGRATION.md`'s `generated` count must not move (AC-11).
- **Any migration or stored-data change.** None is needed: the field is derived at read time from `users.email`, which is untouched.
- **A deprecation window of any kind** (D-6).

## 6. What must not change

- **Every status and body of both operations**: the 200's non-`hosts` fields, the 201's non-`hosts` fields, the 404 `{"error":"not found"}`, the 410 `{"error":"link is no longer accepting bookings"}`, the 429 with its `Retry-After`, the 500 `{"error":"internal error"}`. Pinned by `handlers_booking_test.go`, `pac40_lifecycle_test.go`, `pac72_rev2_test.go`, `pac81_paths_test.go`, `pac82_test.go`, `pac88_listing_test.go`, `ratelimit_booking_test.go`, `security_audit_test.go`, and e2e `public-booking.spec.ts`, `pac40-lifecycle.spec.ts`, `pac88-zones.spec.ts`, `framing.spec.ts`.
- **The order refusals are evaluated in.** The limiter stays `getLinkInfo`'s first statement (`handlers_booking.go:165`); `createPublicBooking` keeps 429 → 404 → 410 → 400 → 409. `TestGetLinkInfoRefusalPrecedesTheLookup` must pass untouched.
- **The strict roster read.** A failed read of the accepted hosts or of a host's user record stays the 500 on both operations — never `hosts: []`, never a host left out, never an undercounted `coverage.total` (PAC-122 SF-027, SF-028; `handlers_booking.go:94-154`).
- **`coverage`'s meaning**, including that a host with a dead Google credential still counts as `checked` (PAC-67 D-14).
- **Everything the booking flow does server-side**: the per-host calendar events and their attendee lists (`engine/booking.go:719-772`), the booker's confirmation mail and ICS and their `safetext` guards (`:901-902`, `:956-957`), and the fact that host *names*, never addresses, go into them.
- **`users.name`'s single write path** and its `safetext.Label` (`storage/signin.go:108`).
- **`GenerateSlug`'s 32-bit suffix** and the rule that no stored slug is rewritten (`engine/booking.go:85-109`).
- **`MIGRATION.md`'s `generated` count** (AC-11).
- **The docs site.** No page promises a host address on the booking page (§2.3), so no page needs editing. A change there would be a sign the removal was mis-scoped.

## 7. Rollback

Revert the paired PRs of step 2 (§3.8). The field is answered again, exactly as at `308ac21`; the web regenerates its types from the reverted bundle. **There is no migration and no stored data is touched**, so rollback loses nothing and needs no restore.

The step-1 web PR need not be reverted and should not be: a page that does not read a field it is sent is correct under both contracts. That asymmetry is deliberate — it means the rollback is a single revert of a paired pair, with no cross-repo ordering to get right under pressure.

The one thing a rollback does **not** undo: any address disclosed before the removal was already disclosed. This work stops a flow; it cannot retract what the flow produced. If the owner needs to know the exposure window, it is the lifetime of the field, which predates every spec in this directory.

## 8. Open questions

- **OQ-1 (owner).** `name` and `avatar_url` stay on the public answer, so an unauthenticated reader with a slug still gets every accepted host's display name and a `*.googleusercontent.com` avatar URL carrying a stable per-account identifier. Is that acceptable, or should the public entry narrow further — names only, or a count? **Default this spec is written to: both stay.** Showing who you are about to meet is the page's purpose, and a face is the ordinary way to do it. If the answer is "narrow further", the change is in the same loop and should be folded into this work rather than filed behind it.
- **OQ-2 (owner).** §2.8 item 3: a booker who completes a booking is added as an attendee to each host's calendar event, which carries the other attendees. Should that change — for example, one event per host with only that host and the booker? It is a real narrowing and it changes what the hosts see of each other, which is a product decision. Not decided here.
- **OQ-3 (reader of smart-calendar-flow).** W-1 and W-3: is `host.email` genuinely unread after #207, and is `hostsLabel`'s local-part fallback gone? This decides only whether step 1 of §3.8 is work or a confirmation; it does not change anything else in the spec.
- **OQ-4 (contract stage).** §3.4 requires a deterministic order but does not say which. The candidates are the host row's creation time (which puts the owner first on a link they created, and reads naturally) and the host's user id (stable but arbitrary). The contract should state one and document it, so a client may rely on it.

## 9. Decisions

| # | Decision | Mark |
|---|---|---|
| D-1 | **Remove the address from both public answers**, not just the `GET`. `publicHostDTO` is the `hosts` item of `PublicLinkInfo` **and** of `PublicBookingConfirmation`, both unauthenticated. Fixing one and not the other would leave the same leak through the same schema behind a slightly higher bar. | [F] |
| D-2 | **Nothing replaces it.** `name` (who), `avatar_url` (what they look like) and `coverage.total` (how many) are already there. The four alternatives were weighed: **nothing at all** — chosen; **a count** — already exists as `coverage.total`, so a second one would be redundant; **display names only** — that *is* the outcome, since names already exist, so it needs no new field; **an opaque per-link identifier** — declined, because its only use is as a client-side list key, §3.4 gives the client a deterministic order to key on instead, and the honest implementation (a keyed hash of link-plus-host under a server secret) is real work added to a response whose surface this spec exists to shrink. A non-keyed identifier would be a cross-link correlator of exactly the kind PAC-149 D-1 removed from pending host entries. | [F] |
| D-3 | **`name` becomes required and always present, possibly empty**, and the contract tells the client to render its own placeholder for an empty one. This is the actual fix for the nameless-host hole that drove the local-part fallback, it stops `PublicHost`'s `required` list becoming empty (which OpenAPI forbids) and so stops `{}` becoming a valid host, and it is the one change here that makes a *new* value appear rather than one disappear. | [F] |
| D-4 | **The host array gains a deterministic order.** `GetAcceptedHosts` has no `ORDER BY` today (`storage/scheduling_links.go:402-406`); removing the address removes the only per-entry key a client had, so order becomes load-bearing. Inherited scope in the strict sense: this work is what makes it matter. Which order, OQ-4. | [F] |
| D-5 | **The owner-facing roster keeps its addresses.** `schedulingHostDTO.email` is unchanged. The problem is publication to people with no entitlement, not the existence of the information. | [F] |
| D-6 | **Straight removal. No `deprecated: true`, no two-release window, no empty-string interim.** Three reasons, in order of weight: (a) every consumer is inside the two repositories plus the MCP server, all deployed together, and there is no mechanism by which a third party could hold a client — no API keys, no OAuth app registration, no published contract (§2.3), so there is nothing a deprecation window would protect; (b) PAC-130 D-4's precedent applies only when the deprecated value is already valid under the existing type, and `""` is not valid under `format: email`, so an interim needs a type change of its own; (c) an interim empty string would keep the field's place while leaving a consumer that renders it rendering a blank — ceremony, not protection. PAC-24's outright removal of `PUT /api/settings` is the applicable precedent. | [F] |
| D-7 | **The merge order is web-first, then paired** (§3.8), which inverts the factory's usual stage-4 order. A response-field *removal* reverses the dependency direction: the client must stop reading before the server stops sending. Step 1 is an unpaired web PR with **no `pac-N` token** (so it pairs against api `main` and its `openapi-check-web` passes against the unchanged bundle); step 2 is one `factory/pac-N-…` branch in each repo. Both halves of step 2 **must** carry the token or the two repos deadlock against each other's `main`. | [F] |
| D-8 | **No sanitisation on the response path.** `users.name` is already `safetext.Label`'d at its single write (`storage/signin.go:108`), `encoding/json` escapes control characters so a JSON body cannot be line-injected, and the line-oriented composers that genuinely need the guard already have it. A name stored before 2026-11-22 is returned unfitted; in JSON that is harmless, and what a consumer does with it is the consumer's boundary. | [F] |
| D-9 | **A register row is opened at intake.** The disclosure has no row in `api-audit.md` or `silent-failures.md` today — its only written trace is one clause inside the struck-through rate-limit entry at `docs/security.md:92`. A finding recorded as a subordinate clause of a closed finding is a finding nobody will find again. AC-14 makes the record its own. | [F] |
| D-10 | **The local-part derivations in `handlers_manager.go` are out of scope, with the reason recorded** (§2.6). All five derive a label from an address the caller supplied, on an authenticated route, and return it only to that caller. The web's `hostsLabel` differed in exactly one respect — its reader had not supplied the address — and that is the property this work removes. Passing it over deliberately, rather than silently, is the point. | [F] |
| D-11 | **The change is specified as a shape, enumerated in §3.9, and the implementing session must reproduce that enumeration in its PR description and have the diff support it** (AC-11a, AC-11b). Removing a required property from a public response is a shape change, and a shape change described only in prose has been under-reported twice on 2026-11-22: #331 as "two doc comments" against five hunks in three files, and #332 as "not reaching the generated types" while it added a 429 with a `Retry-After` to this very operation — a status code the web still does not map (W-6). The enumeration is confirmed by diffing the regenerated bundle and both repos' regenerated types, never by reasoning about where values live. Its direct consequence is D-7's merge order plus a web behaviour that is correct under **both** contracts before the api half merges (AC-13a), because api merges first by design and real visitors are in the window. | [F] |
