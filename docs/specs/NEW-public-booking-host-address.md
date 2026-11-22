# Spec — NEW-public-booking-host-address: the public booking page's two unauthenticated answers carry every accepted host's email address

- **Linear**: none yet. The highest issue in the PAC team on 2026-11-22 is **PAC-162** (`list_issues`, team PAC, ordered by creation, archived included), so the next free number is **PAC-163** — and it is **not claimed here**. `NEW-request-field-length-limits.md` (branch `codeden/spec-enforce-contract-validation-on-aea8`, revision 2 the same day) names the same PAC-163 as next free and also declines to claim it, so two sessions would file the same number. The factory ticket id is `NEW-public-booking-host-address`, following the existing `NEW-*` specs in this directory (`NEW-booking-rate-limit.md` §Linear is the precedent for the convention). **A PAC issue must be opened at intake and this file renamed or linked from it.** Because there is no issue, nothing was posted to Linear.
- **Status**: draft, **revision 2** (awaiting spec-challenger). **The work it describes is blocked until the owner approves it.** No handler, contract, generated file or test was changed by the session that wrote it.
  - **Revision 2 (2026-11-22)** corrects revision 1's central conclusion against a cross-repo check this session could not run. **W-1 is settled and splits in two:** #207 stopped the booking *page* rendering host addresses — confirmed, and the premise held — but nothing stopped the API sending them, so the enumeration still yields addresses from the response body today. **And there is a consumer after all**: smart-calendar-flow's ICS builder reads `PublicBookingConfirmation.hosts[].email` into the `ORGANIZER`/`ATTENDEE` lines of the `.ics` a booker downloads after confirming. So "remove, no replacement" **does not hold for the confirmation half**. Three changes: the change splits by operation (D-1 revised, D-12, §3.1–§3.2), the confirmation half gets a named replacement (new §3.10, D-13), and §3.9's shape enumeration is rewritten for a split `PublicHost` component. Revision 1's conclusion on `PublicLinkInfo` is unchanged and is now the half that can land alone.
  - Revision 1 was the original draft.
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
| smart-calendar-flow #207 stopped rendering host addresses, so the most likely consumer may no longer need the field. | **The first half is confirmed; the inference from it is false** (revision 2, cross-repo check). #207 did stop the *page* rendering host addresses. But nothing stopped the API sending them, so the enumeration still yields every accepted host's address out of the response body today — "stopped rendering" and "stopped returning" came apart exactly as the brief said they would. **And the page was not the only consumer**: the web's ICS builder reads the address out of the 201 (§2.3, C-1). The premise was right about the render and wrong about "no longer needed". |
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
- **Is removal breaking for a client that reads the field?** Yes, and in the worst of the two possible ways. Removing a `required` property makes the key **absent**, not empty. A TypeScript client compiled against the old generated types reads `host.email` as `undefined`; `host.email.split("@")[0]` — the exact expression the web sweep found — **throws a `TypeError` and takes the component down**, where an empty string would merely have rendered blank. **Revision 2: this is no longer hypothetical** — C-1 (§2.3) is a live reader of the 201's copy, which is why part B is gated on a replacement rather than merged on an ordering promise. A client that validates the response against the old schema fails validation on `required`. A Go client with `openapi_types.Email` gets the zero value and no error, because `encoding/json` does not enforce `required`.
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
| **C-1. The web's ICS builder** (`smart-calendar-flow`) | **YES — `PublicBookingConfirmation.hosts[].email`, confirmed.** It populates the `ORGANIZER` / `ATTENDEE` lines of the `.ics` a booker downloads after confirming. | Cross-repo check reported to this session on 2026-11-22, after revision 1 was written. **This is the one consumer revision 1 got wrong**, and it is why the confirmation half cannot be a plain deletion: removing the field does not shrink a response, it **degrades the calendar file a booker gets** — no organiser, no attendee, or a file the client rejects. It reads the **201 only**; nothing reads `PublicLinkInfo.hosts[].email`. | **yes (cross-repo)** |
| **The web's booking page** (`src/pages/PublicBooking.tsx`) | **No, since #207** | #207 stopped the page rendering host addresses. Settles the render half of W-1. It did **not** stop the API sending them, and it is not the same code as C-1. | **yes (cross-repo)** |
| **A webhook, a published API, a third-party integration** | **None exists.** There is no webhook anywhere in `backend/`, `mcp/` or `contracts/` (grep for `webhook`: no match in any Go or YAML file). The bundle is not published by the docs site — `docs/astro.config.mjs` and `docs/package.json` reference no OpenAPI or Swagger integration, and `docs/src/content/docs/reference` holds no API page. The only programmatic bearer-token client of the backend is the MCP server, which ships from this same repository (`docker-compose.mcp.yml`) and is covered above. | **yes, to the limit of this repository** |

**Can consumers be enumerated?** **Yes, and the enumeration is now complete** — for this repository by reading it, and for smart-calendar-flow by the cross-repo check of revision 2. There is no mechanism by which a third party could hold a client: no API key issuance, no OAuth app registration, no published contract. So the population is two repositories plus the MCP server, all deployed together, and it has been fully walked.

**The enumeration found one consumer, and it falsifies revision 1's central claim for one of the two operations.** The result is asymmetric and the asymmetry is the whole of this spec's shape:

| Operation | Consumer of its `hosts[].email` | Can the field be deleted outright? |
|---|---|---|
| `getPublicLinkInfo` 200 | **none**, in either repo | **Yes.** Nothing reads it; the page stopped rendering it at #207. |
| `createPublicBooking` 201 | **C-1, the web's ICS builder** | **No.** Deletion degrades a file a booker downloads (§3.10). |

**That is still what makes this a removal rather than a deprecation** (D-6): a deprecation window protects clients you cannot deploy alongside the server, and there are none. What C-1 changes is not the *mechanism* but the *scope* — one half needs a replacement before its field can go, which is a sequencing problem, not a compatibility one.

### 2.4 The web app — what a reader must open `[unverified-web]`

smart-calendar-flow was not reachable from this worktree. Revision 2's cross-repo check **settled W-1 and W-3 and found C-1**; the rest stay open. All are on `origin/main` of `Enach/smart-calendar-flow`.

**What the check changed.** W-1 was the single most load-bearing unverified claim in revision 1, and it resolved **both ways**: the page half held (the render stopped at #207) and the inference failed (the ICS builder still reads the field). This is worth recording as a method note as much as a finding — "the most likely consumer stopped rendering it" was never evidence that nothing read it, and a spec that treats the two as one claim gets exactly this wrong.

| id | Claim to settle | File a reader must open |
|---|---|---|
| ~~**W-1**~~ **SETTLED** | After #207, nothing on the public booking **page** reads `host.email`. **Confirmed.** But the page is not the only reader: the ICS builder is **C-1** in §2.3, and it reads the 201's copy. | `src/pages/PublicBooking.tsx` (the render, settled) and whatever builds the downloadable `.ics` (C-1, now a confirmed consumer rather than a question). |
| **W-2** | The transport layer that fetches both public operations does not read, log or cache a host address. | `src/api/schedulingLinks.ts`, named as the only web caller of the public routes by `NEW-booking-rate-limit.md:654-656`. |
| ~~**W-3**~~ **SETTLED for the page** | `hostsLabel`'s `h.email.split("@")[0]` fallback is gone from the page's render. **One derivation remains and is deliberate**: C-1's ICS builder, which uses the address as an address, not as a label. §3.3 still applies — a nameless host must not send a renderer back to an identifier. | `src/pages/PublicBooking.tsx`; grep the repo for `hostsLabel` and `split('@')` / `split("@")` to confirm no third site. |
| **W-4** (revised) | Every importer of the generated `PublicHost` type, so the **split** of §3.9 can be applied: which sites follow the link-info type (lose `email`) and which follow the confirmation type (keep it until §3.10 lands). The split makes this a two-type question, not a one-type question. | `src/api/generated` (the directory `make openapi-check-web` compares, per `.github/workflows/release.yml:20, :163`), plus every importer of `PublicHost`. |
| **W-5** | Nothing on the page renders a *nameless* host by falling back to anything derived from an address, and the page has a placeholder for a host with no display name. | `src/pages/PublicBooking.tsx`, the host-list render. This is what §3.3 exists to make unnecessary, and the reader should confirm the placeholder rather than assume one. |
| **W-6** | The page still has **no mapped behaviour for the 429** that PR #332 added to `getPublicLinkInfo`, so a rate-limited visitor gets the generic branch's raw server string and a failure toast. Reported here because it is the live example of the failure this spec's §3.9 exists to prevent, and because it is on the same page and the same operation. | `src/pages/PublicBooking.tsx` — the status mapping around `:165-183` as cited by `NEW-booking-rate-limit.md:638, :646-650`. **Not this spec's to fix** (§5, FU-3), but it must be filed. |

**W-1 did turn out false in part, and D-7 held.** Revision 1 wrote the merge order so that the web stops reading before the server stops sending, explicitly so that an unverified consumer could not be broken by the merge order. C-1 is that consumer. The order is now **mandatory, not prudent** — and for the confirmation half the web cannot simply "stop reading", because it needs the address for a real purpose; it must first switch to the replacement of §3.10. That is the only structural change C-1 forces.

### 2.5 Does anything legitimately need the address? No *server* flow does; one client flow does

The booking flow is complete server-side. Nothing a client does requires it to know a host's address.

- **The hosts' calendar events** are created by the server with the server's own stored credential, one event per host, each carrying the other hosts listed so far plus the booker as attendees: `engine/booking.go:719-772` (`completeBooking`), attendees built at `:734-739`, inserted at `:762`. The address comes from `storage.GetUserByIDNoOrg` inside the engine (`:727`), never from a request.
- **The booker's confirmation email and ICS** are composed and sent by the server: `engine/booking.go:782` → `sendBookingEmail` (`:828-836`) → `sendConfirmationEmail` (`:872-941`) and `generateICS` (`:955-979`). The recipient is `booking.BookerEmail` — the address the booker typed. **Host addresses do not appear in the confirmation at all**: what goes into the ICS `DESCRIPTION` and the mail body is `hostNames`, the join of `u.Name` (`:733`, `:782`), not addresses.
- **The booking request** carries only `name`, `email`, `start`, `notes` and an optional duration (`handlers_booking.go:408-440`). It names no host. Which hosts are involved is derived server-side from the slug.

So **no server flow needs it, and no flow needs a client to supply or echo it.** Removing it cannot make a booking fail, cannot stop an invitation being created and cannot stop a confirmation being sent.

**One client flow does need it, and revision 1 was wrong to conclude otherwise.** C-1 — smart-calendar-flow's ICS builder — reads the 201's `hosts[].email` into the `ORGANIZER` / `ATTENDEE` lines of the `.ics` a booker downloads after confirming. That is not decoration: an `.ics` whose `ORGANIZER` is empty is a worse file, and some calendar clients reject a `VEVENT` with a malformed or absent `ORGANIZER` on a `METHOD:REQUEST` object outright. **So on the 201 the field has a use, and deletion without a replacement is a regression in a user-visible artifact, not a narrowing of a response.** §3.10 names the replacement.

**The server already builds the same artifact, and the two have already drifted.** `generateICS` (`engine/booking.go:955-980`) produces a `METHOD:REQUEST` object attached to the confirmation email as `invite.ics` (`:927-928`). Compare it with what C-1 builds:

| iCalendar property | The server's ICS (emailed) | C-1's ICS (downloaded) |
|---|---|---|
| `ORGANIZER` | `ORGANIZER;CN=Paceday:mailto:noreply@paceday.app` — a **hardcoded no-reply address**, never a host (`:975`) | a **real host address**, from the 201 |
| `ATTENDEE` | exactly one: the booker (`:976`) | the hosts, from the 201 |
| the hosts | names only, in `DESCRIPTION:Meeting with …` (`:974`) | addresses |
| `UID` | `<start.UnixNano()>@paceday` (`:971`) | whatever C-1 chooses |

**A booker who confirms therefore receives two different calendar files describing one meeting**, with different organisers, different attendee lists and (unless C-1 happens to match it) different `UID`s — so a client that trusts `UID` may file them as two separate events. That is a defect today, independent of this spec, and it is the single strongest argument for the recommendation in §3.10: the duplication the manager predicted has already happened and is already observable.

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
4. **The 201's address requires a completed booking, and §3.10 does not make it secret.** If OQ-5 is answered (b), the server-built ICS still names a host in `ORGANIZER` and the booker can read it in the downloaded file. What changes is the cost: from one anonymous GET to a stored booking, a calendar write on every host and a confirmation email, at 20 per client per 10 minutes. **This is the same shape as item 3 and should be read the same way** — the work raises the price of an address; it does not hide one from somebody who books. Under the recommended answer (a) the address is not in the file, but that is OQ-5's doing, not the mechanism's.
5. **Pre-2026-11-22 slugs stay guessable.** `engine/booking.go:85-92` is explicit that no stored slug was rewritten. Those links keep `{first-name}-{duration}min`. Re-slugging existing links is a different ticket with a broken-link cost; **named out of scope in §5.**

## 3. Desired behaviour

Written from the point of view of a person who accepted a co-host invitation, and of the visitor who opens the page.

### 3.1 The link-info answer carries no host email address [F, D-1, part A]

`getPublicLinkInfo`'s 200 carries no host email address, in any field, under any name, in any encoding. Not as `email`; not hashed; not as a local part; not as an initial; not inside a label. This holds for every host, every link, and every status that reaches the array (only `accepted` does, unchanged).

**This is the half the enumeration finding actually exploits, and it has no consumer** (§2.3). A dictionary sweep of slugs hits this operation: one unauthenticated GET, inside every rate limit, no trace beyond a log line. **It can and should land on its own.**

### 3.1a The confirmation answer loses it too, but only behind a replacement [F, D-1, part B; D-13]

`createPublicBooking`'s 201 — fresh save and idempotent replay alike — loses the same field, under the same rule, **once §3.10's replacement is in place and C-1 has switched to it.** Not before. Deleting it first degrades a file a booker downloads (§2.5), and this spec will not trade a user-visible regression for a narrowing of a path that already costs a completed booking to reach.

The two halves differ in what they are worth, which is why they are allowed to differ in when they land:

| | `getPublicLinkInfo` 200 | `createPublicBooking` 201 |
|---|---|---|
| cost to reach | one anonymous GET | **completing a booking**: an offered slot, a valid address, a calendar write on every host |
| rate limit | 120 / client / min | 20 / client / 10 min |
| what it leaves behind | a log line | a stored `bookings` row, an event on every host's calendar, a confirmation email |
| consumer | none | **C-1** |
| blocked on | nothing | §3.10 |

The second row is not a side note: harvesting through the 201 means creating a real meeting on a real person's calendar for every address obtained. It is self-limiting, loud, and attributable. **Shipping part A alone closes the cheap attack completely**, which is why coupling it to a product decision about calendar files would be the wrong trade.

### 3.1b The shared component splits, and yes the split is worth paying for [F, D-12]

The field lives on one `PublicHost` component referenced by both operations (`booking.yaml:3055`, `:3259`), so a partial removal means splitting it: a link-info host type without `email`, and a confirmation host type that keeps it until §3.10 is settled. That is a new component and a changed `$ref` on one operation — enumerated in §3.9.

**My view, asked for plainly: split — and it is cleaner, not two risks instead of one.** Three reasons, in order of weight:

1. **The risks are of different kinds, and the split trades a runtime risk for a compile-time one.** What the split can get wrong is a `$ref` pointing at the wrong type — caught by `make openapi`'s drift check, by the bundle assertion (AC-4), and by two generated type systems that will not compile against a mismatch. What *not* splitting gets wrong is either leaving the exploited path open (if both wait) or shipping a degraded calendar file to every booker (if both go). A mistake that fails a gate is not the same class of thing as a mistake that reaches a user.
2. **The shared component was a convenience, never a claim.** Nothing says a host shown on a link page and a host listed on a confirmation must carry identical fields; they were identical because one struct was easy. This codebase has already decided the opposite at finer grain: PAC-149 made what a host entry shows depend on its **status**, so that a `pending` entry carries `email` and `status` and nothing else while an `accepted` one carries `user_id`, `name` and `avatar_url` (`booking.yaml:3274-3290`). Context-dependent host shapes are the house pattern. Splitting by operation is the same idea one level up.
3. **The alternative couples a security fix to a product decision it has nothing to do with.** Part A is consumer-free and closes the cheap attack. §3.10 is a question about calendar files, organisers and reply affordances, with a user-visible trade the owner has to rule on. Holding the first behind the second is how a two-week decision becomes a two-week exposure.

**The honest cost, stated.** Two components mean a future change to "what a host looks like publicly" must be made twice or deliberately in one place, and two similarly-named types are exactly the sort of thing that drifts. And a reader will see the same person's address absent from one public answer and present in another, which looks like an inconsistency unless the contract says why — so the contract must say why, on both components, naming the other.

**The one condition that reverses this.** The split is a **schedule hedge**, and it is only worth its cost if the two halves ship in different releases. **If §3.10 is approved and both halves ship in one release, do not split**: make one change to `PublicHost`, remove the field from both, and skip the new component entirely. Stage 2 should decide this once the owner has ruled on §3.10, and this spec is written so that either answer is a smaller edit than re-deciding. AC-4a covers the split form; AC-4b covers the unsplit form; exactly one applies.

### 3.2 Nothing is added in place of the link-info address [F, D-2]

For the `getPublicLinkInfo` half, the page already has everything it needs to say something true about the hosts:

- **who** — `name`, already present;
- **what they look like** — `avatar_url`, already present;
- **how many** — `coverage.total`, already present and documented as counting every accepted host.

So: **no count field, no opaque per-link host identifier, no placeholder address.** The three alternatives the brief named were weighed and are recorded in D-2 with the reason each was declined. The shortest defensible change is the one that cannot leak, and adding a new field to an unauthenticated response in a spec whose purpose is to narrow one is the wrong direction.

**This section does not apply to the 201.** There the field has a use, so something must take it over; that is §3.10, and it is the one place this spec adds a field rather than removing one.

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

**This is a two-step change and the enumeration is per step.** Step A is §3.1 (`getPublicLinkInfo`), step B is §3.1a + §3.10 (`createPublicBooking`). If both ship in one release, D-12's condition applies and the split collapses — see the last paragraph.

**Schemas touched — step A**

| Schema | Change | `required` before → after |
|---|---|---|
| `PublicLinkHost` (**new component**) | created, as `PublicHost` minus `email`: `name` (required here from the start), `avatar_url` | — → `[name]` |
| `PublicHost` (`paths/booking.yaml:2992-3007`, bundle `openapi.yaml:15033-15047`) | **kept, unchanged in shape**, and re-described as the confirmation host only | `[email]` → `[email]` |
| `PublicLinkInfo.hosts.items` | `$ref` changes `PublicHost` → `PublicLinkHost` | unchanged (`hosts` stays required) |

**Schemas touched — step B**

| Schema | Change | `required` before → after |
|---|---|---|
| `PublicHost` | `email` **removed**; `name` becomes required; re-described | `[email]` → `[name]` |
| `PublicBookingConfirmation` (`booking.yaml:3228-3265`) | **gains** the ICS string property of §3.10, optional (omitted when it cannot be built for a pre-2026-11-22 replay) | unchanged |

After both steps the two components are identical in shape, which is D-12's own argument for skipping the split when both land together.

**Reference graph.** `PublicHost` is referenced from exactly two places today — `PublicLinkInfo.hosts` (`booking.yaml:3055`, bundle `:15002`) and `PublicBookingConfirmation.hosts` (`booking.yaml:3259`, bundle `:15100`). **Step A moves one of those two references and adds no third.** Afterwards `PublicHost` has one referent and `PublicLinkHost` has one. Neither parent schema's own `required` list changes; `hosts` stays required on both. **A bundle diff showing three referents, or `PublicHost` with none, means the `$ref` edit went to the wrong place** — the one mistake the split makes possible, and what AC-4a exists to catch.

**Operations whose response shape changes**

| Step | Operation | Response | Change |
|---|---|---|---|
| A | `getPublicLinkInfo` `GET /api/book/{slug}` | 200 | each `hosts[]` entry loses `email` and always carries `name`; the item type is renamed in both repos' generated code |
| B | `createPublicBooking` `POST /api/book/{slug}` | 201 (fresh **and** replay) | each `hosts[]` entry loses `email` and always carries `name`; the body **gains** the ICS field |

**No other operation's response shape changes in either step.** In particular the authenticated scheduling-link family keeps `schedulingHostDTO.email` (§3.5), and its contract prose about host addresses **must not be touched**: `booking.yaml:962`, `:1071`, `:1430`, `:1434`, `:1456`, `:1603`, `:1652` all describe `SchedulingLink.hosts`, not `PublicHost`. A blanket find-and-replace over "host's email" would silently rewrite seven true statements; this list exists so that it does not.

**Status codes, response headers, referenced components**

- **Status codes: none added, none removed, in either step.** `getPublicLinkInfo` keeps exactly 200 / 404 / 410 / 429 / 500; `createPublicBooking` keeps its current set. The 429's `Retry-After` and the `BookingSlotsRateLimited` / `BookingError` references are untouched. **§3.10 uses a response field rather than a new endpoint partly for this reason**: an endpoint would have added an operation with its own 404, 410, 429 and rate-limit decision — a far larger shape change than the one it replaces.
- **Response headers: none added or removed.** The ICS travels as a JSON string, so there is no new `Content-Type`, no `Content-Disposition` and no non-JSON response anywhere.
- **Referenced components: one added** (`PublicLinkHost`, step A); none removed in either step. No request shape, parameter, header or `security` block changes.
- If a bundle diff shows a status code or a response header appearing or disappearing, the change has gone wrong and the PR must stop.

**Prose hunks in the contract, enumerated**

Step A:
1. `paths/booking.yaml:38-42` — `getPublicLinkInfo`'s description currently states "This operation answers with every accepted host's address". **Must change**; it becomes the record of what it used to answer and what it answers now.
2. `PublicLinkHost`'s new description, which must name `PublicHost` as the confirmation's type and say **why** the two differ — D-12's honest-cost point: left unsaid, the asymmetry reads as an inconsistency.
3. `paths/booking.yaml:826` (bundle `openapi.yaml:8115`) — `createSchedulingLink`'s description, recording that the pre-audit sweep "harvested every host's address". Historically true and should stay, **amended** to say the address is no longer in that answer.

Step B:
4. `paths/booking.yaml:2994` — `PublicHost`'s description.
5. `paths/booking.yaml:3001-3003` — `name`'s "Omitted when the user record has no name (json `name,omitempty`)", which stops being true.
6. The ICS field's description: what it contains, that it is byte-identical to the emailed attachment, its iTIP method, and when it is omitted (§3.10).
7. `PublicBookingConfirmation`'s own description, which points at `bookingConfirmationDTO` and must name the new field.

Every edited fragment regenerates its bundle counterpart via `make openapi`, so **the bundle diff is larger than the fragment diff** and both must be in the PR.

**Checked and deliberately not changed** (they would look like candidates to a reader sweeping for the word): `paths/auth.yaml:1237` and `:2705` (bundle `openapi.yaml:2485`, `:12352`) say the current-user picture is also `avatar_url` in `SchedulingLinkHost` and `PublicHost` — still true, because `avatar_url` is kept on both components. The `getPublicLinkInfo` 500 description's "never a 200 with `hosts` emptied, a host left out or `coverage.total` undercounted" names no address and stays verbatim.

**If the split is skipped** (D-12's condition — both halves in one release): delete the `PublicLinkHost` rows above and step A's prose hunk 2. What remains is one schema changed (`PublicHost`: `email` removed, `name` required), two operations' response shapes changed, one component gaining a property, **zero** components added, and zero status codes or headers touched. AC-4b is the assertion for that form.

**Generated code**

- **Go** (`backend/api/gen/paceday.gen.go:3184-3191`): step A adds a `PublicLinkHost` type; step B drops `PublicHost.Email openapi_types.Email` and turns `Name *string` into `Name string`. **No behaviour follows from either**, because nothing in `backend/` uses `gen.PublicHost` (§2.2) — grep `gen.PublicHost` returns nothing. `format: email` appears 31 times in the bundle and `openapi_types.Email` 26 times in the generated file, so the import survives both steps; a diff showing it dropped means something else was removed too.
- **Hand-written Go.** `publicHostDTO` (`handlers_booking.go:45-49`) splits into two structs, one per response, because the point of the split is that one of them cannot carry the field. Both builders change (`publicLink:112`, `confirmation:149`). If the split is skipped, one struct loses one field and both builders change anyway.
- **Web** (`smart-calendar-flow/src/api/generated`): step A gives the link-info host its own type without `email`; step B removes it from the confirmation type and adds the ICS field. Every importer of `PublicHost` is a compile-time site and must be assigned to one type or the other — that is the revised **W-4**, and **C-1 is the importer that must move to §3.10 before step B**.
- **No generated file may be hand-edited** (`docs/factory/README.md` §Paired branches, rule 3).

**Field ordering, and the honest byte-level answer**

`publicHostDTO`'s JSON keys are emitted in Go struct declaration order, so today each host entry begins `{"email":…,"name":…,"avatar_url":…}` and after this change begins `{"name":…,"avatar_url":…}`. **The first key of every host entry changes.** Anything that compares a public answer byte-for-byte, hashes it, or snapshots it, changes — which is why AC-8's byte-identity claim is scoped to the fields *outside* `hosts`, and why the four assertions of §2.7 are rewritten rather than adjusted. No other field's position moves, and no parent object's key order changes.

**What a client reading the field sees after removal**

- **TypeScript compiled against the new types**: a compile error at every read site. This is the good case and is what step 1 of §3.8 converts into a deliberate change.
- **TypeScript compiled against the old types, running against the new server**: `host.email` is `undefined`. A render of it shows nothing; `host.email.split("@")[0]` — the expression the web sweep found — throws a `TypeError` and takes the component down. **This is the case the merge window creates and D-7 and D-11 exist to close.**
- **A client validating responses against the old schema**: fails on `required: [email]`.
- **A Go client using the old generated type**: `Email` is the zero value and no error is raised, because `encoding/json` does not enforce `required`. It fails silently, which is worse than loudly.

### 3.10 The ICS replacement: the server builds it, and the 201 carries it [F, D-13]

**One recommendation, not a menu: the server builds the ICS and the 201 carries it as a string field. The web stops building one.**

The two candidates were a server-built ICS or a no-reply organiser address. **They are not independent, and checking the premise is what settled it:** the server's existing ICS *already* uses a no-reply organiser — `ORGANIZER;CN=Paceday:mailto:noreply@paceday.app`, hardcoded at `engine/booking.go:975`. So "no-reply organiser" is not an alternative to the server-built ICS; it is what the server-built ICS does today. Choosing it *without* moving the build — the web keeps its builder and substitutes a constant — keeps the duplicated logic **and** loses the reply path, so it is strictly worse than the server option on both axes. There is one real choice here, and it is the server.

**The manager's premise, checked and confirmed — and stronger than stated.** The server does already compose a `METHOD:REQUEST` ICS (`generateICS`, `engine/booking.go:955-980`), attached as `invite.ics` (`:927-928`). And the drift is not hypothetical: §2.5's table shows the emailed and downloaded files **already** disagree on `ORGANIZER`, on the attendee list and probably on `UID`, so a booker who confirms gets two different calendar files for one meeting today. One source of truth is not a tidiness argument here; it is a bug fix.

**The shape: a field, not an endpoint.** The ICS is returned as a string on `PublicBookingConfirmation` — not as a new `GET …/.ics` route. An endpoint would be a **new unauthenticated read surface** needing its own 404/410 semantics, its own rate-limit decision and its own place in the contract; a field needs none of that, is delivered in a response the booker already receives, and cannot be fetched by anyone who has not completed the booking. It also makes the emailed and downloaded files **byte-identical by construction**, `UID` included, which is the drift fix. The replay 201 carries it too, so a retried booking offers the same file rather than a second event.

It cannot realistically fail to build: every input is already validated at entry — `validBookerAddress` and `safetext.HasControl` refuse a bad booker name or address with a 400 before anything is written (`handlers_booking.go:435-442`, `input_control_chars.go:67-82`), and the link title cannot hold control characters either (`handlers_scheduling_links.go:259`). The one residual case is a **replay of a booking stored before 2026-11-22**, whose `booker_email` was never shape-checked; `safetext.Address` would refuse it. The contract must say what happens then, and the answer is that the field is omitted and the 201 still stands — the booking exists, and a missing download is not worth a 500.

#### What this does to "reply to organiser" — the product question, stated explicitly

This is the user-visible trade and it is the owner's to rule on. `METHOD:REQUEST` plus an `ORGANIZER` with a `mailto:` is what makes a calendar client show Accept / Decline / Tentative and an "email organiser" action; the buttons send an iTIP `REPLY` to the `ORGANIZER` address.

| `ORGANIZER` | "Respond to organiser" affordance | What actually happens when the booker uses it |
|---|---|---|
| **A real host address** (what C-1 builds today) | shown, and works | the reply reaches the host's mailbox as a raw iTIP message, which their client usually renders as an RSVP. **Paceday does not process it** — there is no inbound mail path anywhere in the backend — so the response is mail to a person, never a change in Paceday. |
| **`noreply@paceday.app`** (what the server builds today) | **shown, and does nothing** | the reply goes to a no-reply mailbox. If it refuses mail the booker may get a bounce; if it accepts and discards, **the booker gets no indication at all** that their response reached nobody. |

So the honest reading of the status quo is that **the emailed ICS already shows a button that promises something it does not deliver**, and has since it was written. That is a silent failure of exactly the kind this factory tracks, and it has no register row. **D-14 records it and §5 names it as a follow-up** rather than smuggling a fix into this spec.

**The recommendation inside the recommendation:** the downloadable ICS should be `METHOD:PUBLISH` with no `mailto:` `ORGANIZER` — the host's name only. A file the booker *downloaded* is not an invitation somebody sent them, `PUBLISH` is its correct iTIP method, and it removes the false affordance instead of re-pointing it. The cost, plainly: **the booker loses any way to reach the host from their calendar client.** They lose nothing they have today — the one affordance they are offered today already goes nowhere — but they gain nothing either, and if the owner wants a booker to be able to reach a host, that needs a deliberate reply path, not a host address leaking into a calendar file as a side effect of how the file is built. **That is OQ-5.**

**Why the server can decide this at all, and the web could not.** This is the structural gain, and it is the reason to prefer the server beyond tidiness: a server-built ICS **decouples "the calendar file names a host" from "the API hands a host address to a client."** Only the server can put a real host in `ORGANIZER` without the address ever crossing the API boundary. For the web builder the two are the same thing — the file can only contain what the response gave it. So moving the build does not just remove a consumer of the field; it converts the organiser question from a constraint imposed by the API's shape into a free product choice.

#### The residual, stated without overclaiming

**If the owner chooses a real host address as `ORGANIZER` (OQ-5), a server-built ICS does not hide the address from the booker.** They download the file, open it in a text editor, and read `ORGANIZER`. What changes is only this: **the address is no longer obtainable without completing a booking** — which costs an offered slot, a valid address, a stored `bookings` row and a calendar write on every host, at 20 per client per 10 minutes.

That is the **same shape as §2.8 item 3's Google residual**, and it should be read the same way: this work raises the cost of obtaining a host address from "one anonymous GET" to "book a real meeting with them", and it does not make the address secret from someone who books. Under the recommended `PUBLISH`-with-no-`mailto` variant the address is not in the file at all, so that residual disappears — but it disappears because of OQ-5's answer, not because of the mechanism, and the spec should not be read as delivering it either way.

## 4. Acceptance criteria

Layers: `[contract]` a Go test against the HTTP handler and the bundle's shapes; `[unit]` a Go test below HTTP; `[e2e]` Playwright on the compose stack; `[doc]` a document assertion; `[web]` a test in smart-calendar-flow.

**Tests before code.** The suite is a new `backend/api/public_host_address_test.go`, plus rewrites of the four assertions in §2.7. Stage 3 names the file per criterion. No criterion below may be proven by a test that exists today — §2.7 establishes that none does.

**Fixtures.** **P** is a live reusable link with two accepted hosts, **H1** (name "Alpha Host", avatar set) and **H2** (name "Beta Host", avatar set). **N** is a live link whose single accepted host **H0** has an account with `name = ''`. **O** owns P and N. **B** is an anonymous booker.

**The address is gone, from both answers**

**Step A — the link-info answer** (lands alone; §3.1)

AC-1. Given P, when B requests `GET /api/book/{slug}` without credentials, then the response is 200 and **no byte of the body matches H1's or H2's address, that address's local part, or that local part with the `@` percent- or unicode-escaped** — asserted over the raw body, not over a decoded struct. *Fails today: `handlers_booking.go:112` sends both addresses.* [contract]

AC-1a. Step A changes **nothing** about the 201: with step A deployed and step B not, booking an offered slot still answers a 201 whose `hosts[]` entries carry `email` exactly as today, and C-1's downloaded `.ics` is unchanged. *Passes today and must keep passing — this is the criterion that makes the split safe to ship alone, and it fails if an implementer edits the shared component instead of re-pointing one `$ref`.* [contract]

**Step B — the confirmation answer** (blocked on §3.10; §3.1a)

AC-2. Given P and an offered slot, when B books it, then the 201 body satisfies AC-1's assertion; and given the same request replayed with the same `Idempotency-Key`, the replay 201 satisfies it too. *Fails today: `handlers_booking.go:149`, on the fresh save and on the replay path through `writeBooked`.* [contract]

AC-2a. **The replacement precedes the removal.** Step B's PR may not merge while any read of the confirmation's `hosts[].email` remains in smart-calendar-flow. Asserted procedurally, on the web side: no such read exists, and C-1 builds its download from the §3.10 field. *Fails today: C-1 reads it (§2.3).* [web] **[unverified-web]**

AC-3. Given P, the 200 body's `hosts` entries have exactly the keys the contract declares and no other — asserted by decoding into a map and comparing key sets, so a field renamed rather than removed fails. The same for the 201 after step B. *Fails today.* [contract]

AC-4a. **The split form** (D-12 default). The assembled bundle declares `PublicLinkHost` with no `email`, a non-empty `required` list and `additionalProperties: false`; `PublicLinkInfo.hosts.items` `$ref`s it; `PublicBookingConfirmation.hosts.items` still `$ref`s `PublicHost`; **each component has exactly one referent**; and no other schema in the bundle gains or loses a property. *Fails today.* [contract]

AC-4b. **The unsplit form** (D-12's condition met — both halves in one release). `PublicHost` has no `email`, a non-empty `required` list, `additionalProperties: false`, and its two referents unchanged; **no component is added**. *Fails today.* [contract]

**Exactly one of AC-4a and AC-4b applies**, decided at stage 2 after the owner rules on §3.10. The other must not be written as a passing test, because a suite that accepts both shapes asserts nothing about which one shipped.

**What replaces it, and the nameless host**

AC-5. Given P read twice with no intervening write, the two 200 bodies are byte-identical, `hosts` order included; and given P with H2 removed and re-accepted, the order is still deterministic for two further reads. *Fails today: `GetAcceptedHosts` has no `ORDER BY` (`storage/scheduling_links.go:402-406`), so this criterion is the only thing that would detect a reordering.* [contract]

AC-6. Given N, the 200's single host entry **carries a `name` key whose value is the empty string** — present, not omitted. *Fails today: `name,omitempty` drops the key entirely.* [contract]

AC-7. Given P, the 200 still carries `coverage: {total: 2, checked: …}` and each host's `name` and `avatar_url` exactly as today. *Regression guard; passes today except for the `email` key's absence, which AC-3 owns.* [contract]

**The ICS replacement (§3.10)**

AC-7a. Given P and an offered slot, when B books it, then the 201 carries the ICS field, and its bytes are **identical to the ICS attached to the confirmation email for that same booking** — `UID` included. Asserted by capturing both in one test. *Fails today: the field does not exist, and the two artifacts are built by different code in different repositories and already differ in `ORGANIZER` and attendee list (§2.5).* [contract]

AC-7b. The ICS field's `VEVENT` carries no host email address — same assertion as AC-1, over the field's decoded bytes rather than the enclosing body. **Under the recommended OQ-5 answer** (`METHOD:PUBLISH`, no `mailto:` `ORGANIZER`) this passes; under the alternative it is replaced by a criterion naming the host address that is deliberately present. *This criterion is the one the owner's OQ-5 answer rewrites, and stage 2 must not carry both forms.* [contract]

AC-7c. Given a booking whose stored `booker_email` would be refused by `safetext.Address` (a row written before 2026-11-22), when its 201 is produced by a replay, then the **field is omitted and the 201 still stands** — never a 500, never a truncated or repaired address in the file. *Fails today: no such behaviour exists.* [contract]

AC-7d. The downloaded file opens in a calendar client as one event, and booking the same slot twice via the replay path does not produce two events. [e2e]

**Nothing else about these two operations changes**

AC-8. For P, the 200's `slug`, `title`, `durations`, `min_notice_minutes`, `usage_type`, `time_zone` and `booking_horizon_days` are byte-identical to today's. For the 201, every field other than `hosts` is byte-identical. *Regression guard.* [contract]

AC-9. `getLinkInfo` still refuses before it looks a slug up: a client at the slot-listing limit requesting an unknown slug gets 429, not 404. `security_audit_test.go:301-308` passes untouched. *Regression guard.* [contract]

AC-10. Given P and a hidden `scheduling_link_hosts` table, the 200 becomes the documented 500 `{"error":"internal error"}` with no part of the roster in the body and **no address in the log line or the Sentry report**; the same with `users` hidden; and the same on the 201 path, for a fresh save and for a replay. `TestPAC122_SF027_*` and `TestPAC122_SF028_*` pass, with their **baseline assertions rewritten to key on the host's name, not its address** — and the `pac25LinkFixture` gives its accepted host a distinct name so that baseline discriminates. *Regression guard whose test must change without its criterion changing.* [contract]

AC-11. `MIGRATION.md`'s `generated` count is unchanged, and `getPublicLinkInfo` and `createPublicBooking` are still `handwritten`, each with a note naming this work. *Fails today only in the note.* [doc]

AC-11a. **The shape enumeration is reproduced and is true, per step.** Each implementation PR's description carries §3.9's enumeration **for its own step**, and every line is checkable against the diff in that same PR: the fragment diff, the regenerated bundle diff, and both repos' regenerated types. Step A: one component added, one `$ref` moved, each component left with exactly one referent, three prose hunks, **zero** status codes and **zero** response headers touched, and the 201's shape provably unchanged (AC-1a). Step B: `email` removed, `name` required, one property added to `PublicBookingConfirmation`, four prose hunks, **zero** status codes and **zero** response headers touched. Both: the seven authenticated-roster statements (`booking.yaml:962`, `:1071`, `:1430`, `:1434`, `:1456`, `:1603`, `:1652`) untouched, and the `openapi_types` import still present in the regenerated Go. **A claim in either description that its own diff does not support fails this criterion** — and "I reasoned about where the values live" is not support; the bundle and the regenerated types are. *Fails today: no such description exists.* [doc]

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
- **Fixing the dishonest RSVP affordance on the *emailed* ICS** (D-14). The emailed `METHOD:REQUEST` has shown Accept / Decline since it was written while its `ORGANIZER` is `noreply@paceday.app` (`engine/booking.go:975`), so every reply reaches nobody and the sender is told nothing. It is a real silent failure with **no register row**, found while checking this spec's premises. §3.10 fixes it for the **downloadable** file because that file is this spec's to define; the emailed one is not, and changing what Paceday's own invitations promise is a product decision with its own blast radius. Follow-up **FU-4**, to be filed at intake.
- **`generateICS`'s `UID` scheme.** `UID: <start.UnixNano()>@paceday` (`engine/booking.go:971`) is derived from the start instant and **not** from the booking id, so two bookings that start at the same instant on different links share a `UID` and a calendar client may file the second as an update replacing the first. Found while checking §3.10's premise. §3.10 makes the emailed and downloaded files share a `UID`, which is the behaviour it needs, and does **not** repair the scheme. Follow-up **FU-5**.
- **Processing inbound iTIP replies.** There is no inbound mail path in the backend and this spec does not add one. It is what would be needed to make a calendar RSVP mean anything in Paceday, and it is a feature, not a fix.
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
- **The 201, for the whole of step A.** Until step B merges, `createPublicBooking`'s response is byte-for-byte what it is today, `hosts[].email` included, and C-1's downloaded `.ics` is unchanged. This is AC-1a, and it is the single most important thing the split must not get wrong: the temptation is to edit the shared component rather than re-point one `$ref`.
- **The emailed confirmation ICS, and the mail path that carries it.** `generateICS` and `sendConfirmationEmail` keep their current output, `ORGANIZER` and `UID` scheme included (D-14, FU-4, FU-5). §3.10 **reuses** that builder for a new delivery; it does not redefine it. If the emailed attachment's bytes change, something has been fixed that this spec said it would not fix.
- **The docs site.** No page promises a host address on the booking page (§2.3), so no page needs editing. A change there would be a sign the removal was mis-scoped.

## 7. Rollback

**Two independent rollbacks, which is a further argument for the split.** Reverting step A's paired PRs restores `getPublicLinkInfo`'s field and removes `PublicLinkHost`; reverting step B's restores the confirmation's field and the ICS property. Neither revert depends on the other, and reverting step B does not reopen the harvesting path step A closed. In each case the web regenerates its types from the reverted bundle. **There is no migration and no stored data is touched**, so rollback loses nothing and needs no restore.

One ordering constraint on the way back: **step B must not be reverted while C-1 relies on the §3.10 field**, or the download loses its source. The safe revert of step B is therefore the contract and handler change *plus* restoring C-1's own builder — which is the mirror image of AC-2a, and worth writing into step B's PR body rather than discovering under pressure.

The step-1 web PR need not be reverted and should not be: a page that does not read a field it is sent is correct under both contracts. That asymmetry is deliberate — it means the rollback is a single revert of a paired pair, with no cross-repo ordering to get right under pressure.

The one thing a rollback does **not** undo: any address disclosed before the removal was already disclosed. This work stops a flow; it cannot retract what the flow produced. If the owner needs to know the exposure window, it is the lifetime of the field, which predates every spec in this directory.

## 8. Open questions

- **OQ-1 (owner).** `name` and `avatar_url` stay on the public answer, so an unauthenticated reader with a slug still gets every accepted host's display name and a `*.googleusercontent.com` avatar URL carrying a stable per-account identifier. Is that acceptable, or should the public entry narrow further — names only, or a count? **Default this spec is written to: both stay.** Showing who you are about to meet is the page's purpose, and a face is the ordinary way to do it. If the answer is "narrow further", the change is in the same loop and should be folded into this work rather than filed behind it.
- **OQ-2 (owner).** §2.8 item 3: a booker who completes a booking is added as an attendee to each host's calendar event, which carries the other attendees. Should that change — for example, one event per host with only that host and the booker? It is a real narrowing and it changes what the hosts see of each other, which is a product decision. Not decided here.
- **OQ-3 (reader of smart-calendar-flow).** W-1 and W-3: is `host.email` genuinely unread after #207, and is `hostsLabel`'s local-part fallback gone? This decides only whether step 1 of §3.8 is work or a confirmation; it does not change anything else in the spec.
- **OQ-5 (owner) — the product question §3.10 raises, and the one that gates step B.** In the server-built downloadable ICS, should `ORGANIZER` be (a) the host's **name only**, `METHOD:PUBLISH`, no `mailto:` — **the spec's recommendation**: no reply affordance is offered, so none is falsely promised, and the host address is not in the file at all; or (b) a **real host address**, `METHOD:REQUEST` — the booker's calendar client offers "respond to organiser" and a reply reaches the host's mailbox as mail, which Paceday does not process and will not record, and the host address is readable in the file by anyone who completed a booking (§3.10's residual)? **Default this spec is written to: (a).** The booker loses nothing they have today, because the affordance they are offered today already goes nowhere. If the answer is that a booker should be able to reach a host, that deserves a deliberate reply path rather than a host address leaking in as a by-product of how a file is built.
- **OQ-6 (owner, for stage 2).** Does step B ship in the same release as step A? If yes, D-12's condition is met and the component split should be **skipped** — AC-4b instead of AC-4a. If no, the split is what lets step A land while OQ-5 is decided.
- **OQ-4 (contract stage).** §3.4 requires a deterministic order but does not say which. The candidates are the host row's creation time (which puts the owner first on a link they created, and reads naturally) and the host's user id (stable but arbitrary). The contract should state one and document it, so a client may rely on it.

## 9. Decisions

| # | Decision | Mark |
|---|---|---|
| D-1 | **Remove the address from both public answers, in two steps.** Revision 1 made this one decision; the cross-repo check split it. **Part A** (`getPublicLinkInfo`, consumer-free) lands alone — it is the half a dictionary sweep exploits, and nothing reads it. **Part B** (`createPublicBooking`) lands only behind D-13's replacement, because C-1 reads that copy for a real purpose and deleting it degrades a file a booker downloads. Fixing neither until both can go would leave the cheap attack open pending a product decision about calendar files. | [F], revised |
| D-2 | **Nothing replaces it.** `name` (who), `avatar_url` (what they look like) and `coverage.total` (how many) are already there. The four alternatives were weighed: **nothing at all** — chosen; **a count** — already exists as `coverage.total`, so a second one would be redundant; **display names only** — that *is* the outcome, since names already exist, so it needs no new field; **an opaque per-link identifier** — declined, because its only use is as a client-side list key, §3.4 gives the client a deterministic order to key on instead, and the honest implementation (a keyed hash of link-plus-host under a server secret) is real work added to a response whose surface this spec exists to shrink. A non-keyed identifier would be a cross-link correlator of exactly the kind PAC-149 D-1 removed from pending host entries. | [F] |
| D-3 | **`name` becomes required and always present, possibly empty**, and the contract tells the client to render its own placeholder for an empty one. This is the actual fix for the nameless-host hole that drove the local-part fallback, it stops `PublicHost`'s `required` list becoming empty (which OpenAPI forbids) and so stops `{}` becoming a valid host, and it is the one change here that makes a *new* value appear rather than one disappear. | [F] |
| D-4 | **The host array gains a deterministic order.** `GetAcceptedHosts` has no `ORDER BY` today (`storage/scheduling_links.go:402-406`); removing the address removes the only per-entry key a client had, so order becomes load-bearing. Inherited scope in the strict sense: this work is what makes it matter. Which order, OQ-4. | [F] |
| D-5 | **The owner-facing roster keeps its addresses.** `schedulingHostDTO.email` is unchanged. The problem is publication to people with no entitlement, not the existence of the information. | [F] |
| D-6 | **Straight removal. No `deprecated: true`, no two-release window, no empty-string interim.** Three reasons, in order of weight: (a) every consumer is inside the two repositories plus the MCP server, all deployed together, and there is no mechanism by which a third party could hold a client — no API keys, no OAuth app registration, no published contract (§2.3), so there is nothing a deprecation window would protect; (b) PAC-130 D-4's precedent applies only when the deprecated value is already valid under the existing type, and `""` is not valid under `format: email`, so an interim needs a type change of its own; (c) an interim empty string would keep the field's place while leaving a consumer that renders it rendering a blank — ceremony, not protection. PAC-24's outright removal of `PUT /api/settings` is the applicable precedent. | [F] |
| D-7 | **The merge order is web-first, then paired** (§3.8), which inverts the factory's usual stage-4 order. **Revision 2: this held, and C-1 is why it was written.** Revision 1 chose the order so that an unverified consumer could not be broken by it; the cross-repo check then found exactly such a consumer. For part B the web cannot merely "stop reading" — it needs the address for a real purpose — so it must first switch to D-13's field, which is AC-2a. A response-field *removal* reverses the dependency direction: the client must stop reading before the server stops sending. Step 1 is an unpaired web PR with **no `pac-N` token** (so it pairs against api `main` and its `openapi-check-web` passes against the unchanged bundle); step 2 is one `factory/pac-N-…` branch in each repo. Both halves of step 2 **must** carry the token or the two repos deadlock against each other's `main`. | [F] |
| D-8 | **No sanitisation on the response path.** `users.name` is already `safetext.Label`'d at its single write (`storage/signin.go:108`), `encoding/json` escapes control characters so a JSON body cannot be line-injected, and the line-oriented composers that genuinely need the guard already have it. A name stored before 2026-11-22 is returned unfitted; in JSON that is harmless, and what a consumer does with it is the consumer's boundary. | [F] |
| D-9 | **A register row is opened at intake.** The disclosure has no row in `api-audit.md` or `silent-failures.md` today — its only written trace is one clause inside the struck-through rate-limit entry at `docs/security.md:92`. A finding recorded as a subordinate clause of a closed finding is a finding nobody will find again. AC-14 makes the record its own. | [F] |
| D-10 | **The local-part derivations in `handlers_manager.go` are out of scope, with the reason recorded** (§2.6). All five derive a label from an address the caller supplied, on an authenticated route, and return it only to that caller. The web's `hostsLabel` differed in exactly one respect — its reader had not supplied the address — and that is the property this work removes. Passing it over deliberately, rather than silently, is the point. | [F] |
| D-11 | **The change is specified as a shape, enumerated in §3.9, and the implementing session must reproduce that enumeration in its PR description and have the diff support it** (AC-11a, AC-11b). Removing a required property from a public response is a shape change, and a shape change described only in prose has been under-reported twice on 2026-11-22: #331 as "two doc comments" against five hunks in three files, and #332 as "not reaching the generated types" while it added a 429 with a `Retry-After` to this very operation — a status code the web still does not map (W-6). The enumeration is confirmed by diffing the regenerated bundle and both repos' regenerated types, never by reasoning about where values live. Its direct consequence is D-7's merge order plus a web behaviour that is correct under **both** contracts before the api half merges (AC-13a), because api merges first by design and real visitors are in the window. | [F] |
| D-12 | **The shared `PublicHost` component splits, by operation, and the split is cleaner than one bigger change — conditionally.** Asked for a plain view: **split.** What the split can get wrong is a `$ref` pointing at the wrong type, which fails `make openapi`'s drift check, the bundle assertion (AC-4a) and two compilers; what coupling gets wrong reaches a user — either the harvesting path stays open or every booker's `.ics` degrades. Trading a gated compile-time risk for an ungated runtime one is the right direction, not merely a smaller number of risks. The shared component was a convenience and never a claim that the two responses must match; PAC-149 already made host-entry contents depend on context at finer grain (`booking.yaml:3274-3290`). **The honest cost**: two similarly-named components that a future change must touch twice, and an asymmetry that reads as an inconsistency unless the contract says why — so it must. **The condition that reverses it (OQ-6): if both halves ship in one release, skip the split** — it is a schedule hedge, and an unhedged schedule should not pay for one. | [F] |
| D-13 | **The server builds the ICS and the 201 carries it as a string field; the web stops building one.** The two candidates were not independent and checking settled it: the server's ICS **already** uses a no-reply `ORGANIZER` (`engine/booking.go:975`), so "no-reply organiser" is what the server option already does, and choosing it without moving the build keeps the duplicated builder *and* loses the reply path — strictly worse on both axes. The duplication is not theoretical: the emailed and downloaded files already disagree on `ORGANIZER`, on the attendee list and probably on `UID` (§2.5), so one source of truth is a bug fix. **A field, not an endpoint**, because an endpoint adds an operation with its own 404/410/429 and rate-limit decision — a larger shape change than the one it replaces — while a field is delivered in a response only a completed booking receives and makes the two artifacts byte-identical by construction. The structural gain, and the real reason to prefer the server: it **decouples "the file names a host" from "the API hands a client an address"**, which the web builder cannot do because its file can only contain what the response gave it. That turns OQ-5 from a constraint into a choice. | [F] |
| D-14 | **The emailed ICS's RSVP affordance is dishonest today, and this spec records it without fixing it.** `METHOD:REQUEST` plus `ORGANIZER: noreply@paceday.app` means every calendar client shows Accept / Decline, every reply reaches a no-reply mailbox, and the booker is told nothing. No register row exists. §3.10 removes the false affordance from the **downloadable** file, which is this spec's to define; the emailed invitation is not, and FU-4 carries it. Recording a defect found while checking a premise, and declining to widen scope for it, is the point. | [F] |
