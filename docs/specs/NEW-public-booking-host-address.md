# Spec — NEW-public-booking-host-address: the public booking page's two unauthenticated answers carry every accepted host's email address

- **Linear**: none yet. The highest issue in the PAC team on 2026-11-22 is **PAC-162** (`list_issues`, team PAC, ordered by creation, archived included), so the next free number is **PAC-163** — and it is **not claimed here**. `NEW-request-field-length-limits.md` (branch `codeden/spec-enforce-contract-validation-on-aea8`, revision 2 the same day) names the same PAC-163 as next free and also declines to claim it, so two sessions would file the same number. The factory ticket id is `NEW-public-booking-host-address`, following the existing `NEW-*` specs in this directory (`NEW-booking-rate-limit.md` §Linear is the precedent for the convention). **A PAC issue must be opened at intake and this file renamed or linked from it.** Because there is no issue, nothing was posted to Linear.
- **Status**: draft, **revision 3** (awaiting spec-challenger). **The work it describes is blocked until the owner approves it.** No handler, contract, generated file or test was changed by the session that wrote it.
  - **Revision 2 (2026-11-22)** corrects revision 1's central conclusion against a cross-repo check this session could not run. **W-1 is settled and splits in two:** #207 stopped the booking *page* rendering host addresses — confirmed, and the premise held — but nothing stopped the API sending them, so the enumeration still yields addresses from the response body today. **And there is a consumer after all**: smart-calendar-flow's ICS builder reads `PublicBookingConfirmation.hosts[].email` into the `ORGANIZER`/`ATTENDEE` lines of the `.ics` a booker downloads after confirming. So "remove, no replacement" **does not hold for the confirmation half**. Three changes: the change splits by operation (D-1 revised, D-12, §3.1–§3.2), the confirmation half gets a named replacement (new §3.10, D-13), and §3.9's shape enumeration is rewritten for a split `PublicHost` component. Revision 1's conclusion on `PublicLinkInfo` is unchanged and is now the half that can land alone.
  - **Revision 3 (2026-11-22)** is driven by a web-side read of the actual call sites, and **retracts revision 2's split**. Three consumers exist, not one; **both DTOs must move together** because the web's booking mapper has a *second source* for `hosts` — a cached `PublicLinkInfo` — so removing the field from the 201 alone would leave the downloaded ICS still carrying addresses while a response-body inspection showed the field gone (D-12, rewritten; §3.1b). Two of the three consumers are incidental and are being fixed on the web side, which collapses this spec's blocker to **one contract question**: what the ICS organiser becomes (§3.10, OQ-5). **And §2.4a is new and unflattering**: the enumeration was **short, not merely unverified** — two of the three consumers were named by file in this repository's own `docs/specs/`, and the sweep that produced revision 1's claims could not have found them. The method is now stated so a reader can judge the sweep's shape instead of trusting its result.
  - Revision 2 corrected revision 1's central conclusion for the confirmation half and proposed splitting the change. The correction stands; the split does not.
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
| **C-1. The ICS builder** — `src/components/booking/ics.ts:57, :60` | **YES, and it genuinely needs a real address.** The `ORGANIZER` (`:57`) and `ATTENDEE` (`:60`) `mailto:` URIs of the `.ics` a booker downloads. | **This is the consumer §3.10 exists for**, and the only one of the three that needs an address rather than a discriminator. Their own present-day bug, theirs to fix: `:57` is guarded, `:60` calls `icsAddress(h.email)` **unguarded**, so an absent key throws rather than degrading — which is revision 1's §2.2 "`undefined` is harsher than empty" prediction landing on a real line. | **yes (web read)** |
| **C-2. Host avatars** — `src/components/links/HostAvatars.tsx:66, :71` | **YES, but incidentally.** `key={h.email + i}` and `paletteFor(h.email)`. | It wants a **stable per-host discriminator**, not an address: a React list key and a colour seed. Both reads sit **outside** the `audience` gate #207 added, so they run on the public path. **Being fixed on the web side now**, which is the right fix — the address was never the thing it needed. This is what §3.4's deterministic order and §3.3's always-present `name` are for. | **yes (web read)** |
| **C-3. The link-info cache** — `src/api/schedulingLinks.ts:219, :641, :707` | **YES, but incidentally, and it needs nothing.** A module-level cache that happens to hold whole link-info objects. | It stops holding addresses when the field goes. No change needed on the web side at all. | **yes (web read)** |
| **The booking page** (`src/pages/PublicBooking.tsx`) | **No, since #207** | #207 stopped the *page* rendering host addresses. It did **not** stop the API sending them, and it is not the code of C-1, C-2 or C-3. | **yes (web read)** |
| **A webhook, a published API, a third-party integration** | **None exists.** There is no webhook anywhere in `backend/`, `mcp/` or `contracts/` (grep for `webhook`: no match in any Go or YAML file). The bundle is not published by the docs site — `docs/astro.config.mjs` and `docs/package.json` reference no OpenAPI or Swagger integration, and `docs/src/content/docs/reference` holds no API page. The only programmatic bearer-token client of the backend is the MCP server, which ships from this same repository (`docker-compose.mcp.yml`) and is covered above. | **yes, to the limit of this repository** |

**Can consumers be enumerated?** **Yes, and the enumeration is now complete** — for this repository by reading it, and for smart-calendar-flow by the cross-repo check of revision 2. There is no mechanism by which a third party could hold a client: no API key issuance, no OAuth app registration, no published contract. So the population is two repositories plus the MCP server, all deployed together, and it has been fully walked.

**The enumeration found one consumer, and it falsifies revision 1's central claim for one of the two operations.** The result is asymmetric and the asymmetry is the whole of this spec's shape:

| Consumer | Reads which answer? | Needs an address? | Blocks the removal? |
|---|---|---|---|
| **C-1** ICS builder | the 201 | **yes** — `mailto:` URIs | **yes**, until §3.10 lands |
| **C-2** host avatars | **the 200**, outside #207's gate | no — wants a discriminator | no; being fixed on the web side |
| **C-3** link-info cache | the 200 | no | no; needs nothing |

**Two corrections to revision 2 follow from this table.**

First, **`getPublicLinkInfo` is not consumer-free after all** — C-2 reads its `hosts[].email` on the public path. Revision 2 called that half "consumer-free, lands alone". It was wrong, and the error came from the same place as revision 1's: a claim scoped to `PublicBooking.tsx` rather than to "every reader of this field" (§2.4a).

Second, **once C-2 and C-3 land there is exactly one blocker left, and it is a product question**: what the downloaded ICS's organiser becomes — whether a booker can reply to their host (§3.10, OQ-5). That is the decision worth this spec's weight. Everything else here is plumbing with a gate behind it.

**That is still what makes this a removal rather than a deprecation** (D-6): a deprecation window protects clients you cannot deploy alongside the server, and there are none. The three consumers change the *sequencing*, not the *mechanism*.

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

### 2.4a How revision 1's claims were derived, and why they were short

**The claims were short, not complete-but-unverified. Stating the method is the point of this section**, so a reader can judge the sweep's shape rather than trust its result.

**The method actually used.** smart-calendar-flow was unreachable, so the web claims were not derived from a sweep of that repository. They were derived from **a citation index of *this* repository** — every `src/**.ts(x)` path named anywhere in `docs/`, `contracts/`, `nginx/` and `e2e/` — plus reasoning about what a booking page does. That method has a precise blind spot: **it can only name a file this repo has already mentioned, and it ranks by what the mention was about.** Every mention of the public booking page that revision 1 followed (`NEW-booking-rate-limit.md:146`, `:617`, `:638`, `:646`; `docs/plans/PAC-81-part-c.md:30`) was about the *page*, so the claims came out page-shaped and transport-shaped.

**Two of the three consumers were named by file in this repository, in `docs/specs/`, and the sweep did not reach them:**

- `src/components/links/HostAvatars.tsx` is cited **six times** here — and `NEW-deploy-hardening.md:237` says, in so many words, "`HostAvatars.tsx:58`, **public booking page included**". A spec in this very directory names the component that renders hosts on the public page. Revision 1 did not open it.
- `src/components/booking/ics.ts` is named at `PAC-91.md:254` ("moves the ICS helper to `src/components/booking/ics.ts`") and grepped at `:240`.

So the honest verdict is not "unverified". It is **short, with the pointer to the gap sitting in the directory this spec was written into.**

**One-to-one against revision 1's six claims:**

| Consumer | Which claim covered it | Verdict |
|---|---|---|
| **C-1** `booking/ics.ts` (`mailto:`) | **W-1 partially.** W-1 listed the right *read kind* — "not for a `mailto:`" — but scoped it to `PublicBooking.tsx`. The file was never named. | **missed as a file**; read kind anticipated |
| **C-2** `HostAvatars.tsx` (`key=`, `paletteFor`) | **W-1 partially** ("not for a `key`"); **W-3** adjacent (a derivation from the address, though `paletteFor` is a colour seed, not a label). Scoped to the wrong file again. | **missed as a file**; read kind anticipated |
| **C-3** `schedulingLinks.ts` (cache) | **W-2, fully.** It named the file *and* named caching: "does not read, log or cache a host address." | **anticipated correctly** |
| — | **W-4** (every importer of the generated `PublicHost` type) | **the only file-independent claim, and the one that would have been complete.** Any importer is a compile-time site regardless of what it does with the field; W-4 would have enumerated all three. Revision 1 ranked it fourth and treated it as a types-hygiene check. **It should have led.** |
| — | **W-5** (nameless-host render fallback) | display-shaped; covered none of the three, though it names the same hazard class as C-2 |
| — | **W-6** (the 429 mapping) | unrelated to the field |

**The re-derived question, and the taxonomy to sweep.** Not *"what does the web display?"* but **"what reads this field?"** — and a field is read in places that never reach rendered HTML. Their own #207 missed C-1 and C-2 despite a test that drives a real booking and asserts on `innerHTML`, because an attribute value, a colour seed and a cache's contents do not appear in it. The sweep a reader should run, and which this spec's W-7 now asks for:

1. **rendered text** — what a display sweep finds, and the only thing #207 and revision 1 both looked for;
2. **attribute and prop values** — `key=`, `id=`, `href`, `title`, `alt`, `data-*`, anything passed into a child's props (C-2's `key=`);
3. **keys, hashes and seeds** — a value used because it is unique, not because it is meaningful: map keys, dedupe sets, colour and avatar seeds (C-2's `paletteFor`);
4. **serialisation into another artefact** — ICS, CSV, clipboard, `mailto:`, query strings, a download (C-1). **This is the category revision 1 had no entry for at all**, and it is where the one genuine consumer lived;
5. **caches and stores** — anything holding a whole response object, including React Query caches and module-level maps (C-3; W-2 did cover this);
6. **logging and analytics** — breadcrumbs, Sentry context, event properties;
7. **types** — every importer of the generated type, which subsumes 1–6 and is the check to run **first** (W-4).

**W-7 (new, and the only one that matters going forward).** A reader must run categories 2–7 above over smart-calendar-flow, not category 1 alone, and report the result. Categories 1–5 are now settled by the web read that produced C-1 to C-3; **6 and 7 are not** — nothing has yet confirmed that no logger, breadcrumb or analytics property carries a host address, and that is exactly the kind of read the first two sweeps would have missed twice.

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

### 3.1 Neither public answer carries a host email address, and they move together [F, D-1]

The host entry of `getPublicLinkInfo`'s 200 and of `createPublicBooking`'s 201 carries no email address, in any field, under any name, in any encoding. Not as `email`; not hashed; not as a local part; not as an initial; not inside a label. This holds for every host, every link, every status that reaches the array (only `accepted` does, unchanged), and on the replay 201 of an idempotent retry as well as on a fresh save.

**One change to one shared component. The two responses are not separable** — see §3.1b, which retracts revision 2's split and is the most important correction in this spec.

### 3.1a What the two answers are worth, which is no longer a sequencing argument

| | `getPublicLinkInfo` 200 | `createPublicBooking` 201 |
|---|---|---|
| cost to reach | one anonymous GET | **completing a booking**: an offered slot, a valid address, a calendar write on every host |
| rate limit | 120 / client / min | 20 / client / 10 min |
| what it leaves behind | a log line | a stored `bookings` row, an event on every host's calendar, a confirmation email |
| consumers | **C-2** (`key=`, colour seed), **C-3** (cache) — both incidental | **C-1** (ICS `mailto:`) — genuine |
| blocked on | nothing, once C-2 and C-3 land | §3.10 |

The asymmetry is real and worth recording — harvesting through the 201 means creating a real meeting on a real person's calendar for every address obtained, so it is self-limiting, loud and attributable, where the 200 is cheap and silent. **But it no longer buys a separate release**, because §3.1b shows the two cannot be separated without the leak surviving the change. The one blocker is §3.10, and once C-2 and C-3 land it is the only one.

### 3.1b The split is retracted: both DTOs move together [F, D-12]

**Revision 2 proposed splitting the shared `PublicHost` component so the link-info half could land first. That is withdrawn, and the reason deserves recording as its own finding, because the split would have looked like it worked and leaked anyway.**

The web's booking mapper resolves the confirmation's hosts as roughly `hosts: raw.hosts ?? link?.hosts ?? []`, where `link` is a **cached `PublicLinkInfo`** (C-3's cache) and `raw` is a **cast, not a strictly-parsed value** — there is no `zod` `strictObject` at that boundary. So the mapper has **two sources** for the host roster, and the second is the link-info response.

The consequence, concretely: remove `email` from the 201 alone and C-1's ICS builder can still be handed host entries that carry an address, because they came from the cached link lookup rather than from the 201. **A response-body inspection of `POST /api/book/{slug}` would show the field gone, every assertion in §4 keyed on that body would pass, and the downloaded `.ics` would still carry host addresses.** Removing the field from the *other* DTO is what actually drains the second source.

**Why this is the useful lesson and not just a bug.** Every criterion revision 2 wrote asserted on a **response body**, because that is where the field is. The leak would have lived in an **artefact built from a cache of a different response** — one join away from everything being measured. It is the same failure shape as §2.4a's: an enumeration that is complete for the thing it looked at and silent about the path that actually carries the value. **A field removal is only verified when every consumer's output is checked, not every response body** — which is why AC-15 asserts on the downloaded file and not only on the JSON.

**The decision, therefore:** one edit to `PublicHost`, both referents lose the field at the same instant, one release. No new component, no `$ref` move, no two-step. That is also strictly less contract surface than the split — revision 2's argument that the split was "cleaner" was reasoning about *risk kinds* while missing that the split's premise, two independently-removable halves, was false.

**What revision 2 got right and is kept:** the merge order (D-7), because C-1 still must switch to §3.10's field before the server stops sending; and the refusal to delete the 201's field without a replacement (D-13).

### 3.2 Nothing is added in place of the address, except the ICS [F, D-2]

The page already has everything it needs to say something true about the hosts:

- **who** — `name`, already present;
- **what they look like** — `avatar_url`, already present;
- **how many** — `coverage.total`, already present and documented as counting every accepted host.

So: **no count field, no opaque per-link host identifier, no placeholder address.** The three alternatives the brief named were weighed and are recorded in D-2 with the reason each was declined. The shortest defensible change is the one that cannot leak, and adding a new field to an unauthenticated response in a spec whose purpose is to narrow one is the wrong direction.

**Two riders.** C-2 wants a **stable per-host discriminator** for a React key and a colour seed, and the address was only ever standing in for one — §3.4's deterministic order and §3.3's always-present `name` are what it should use, and the web is fixing it to. And on the 201 the field has a genuine use, so something must take it over: that is §3.10, **the one place this spec adds a field rather than removing one.**

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

**One change, one release, one shared component** (D-12). Revision 2's two-step enumeration is withdrawn with the split.

**Schemas touched**

| Schema | Change | `required` before → after |
|---|---|---|
| `PublicHost` (`paths/booking.yaml:2992-3007`, bundle `openapi.yaml:15033-15047`) | `email` property **removed** (`type: string`, `format: email`); `name` loses its "Omitted when…" description and becomes required; the schema's own `description` rewritten | `[email]` → `[name]`. An empty `required` list is illegal, which is one reason `name` becomes required (§3.3). |
| `PublicBookingConfirmation` (`booking.yaml:3228-3265`) | **gains** the ICS string property of §3.10, optional (omitted when it cannot be built for a pre-2026-11-22 replay) | unchanged |
| No other schema | — | — |

**No component is added and none is removed.** `PublicHost` keeps **exactly** its two referents — `PublicLinkInfo.hosts` (`booking.yaml:3055`, bundle `:15002`) and `PublicBookingConfirmation.hosts` (`booking.yaml:3259`, bundle `:15100`) — and **both lose the field in the same commit**, which is the whole of D-12. Neither parent schema's own `required` list changes; `hosts` stays required on both. **A bundle diff showing a new host component, or `PublicHost` with one referent instead of two, means the retracted split has crept back in** and the PR must stop.

**Operations whose response shape changes**

| Operation | Response | Change |
|---|---|---|
| `getPublicLinkInfo` `GET /api/book/{slug}` | 200 | each `hosts[]` entry loses `email` and always carries `name` |
| `createPublicBooking` `POST /api/book/{slug}` | 201 (fresh **and** replay) | the same, **plus** the body gains the ICS field |

**No other operation's response shape changes.** In particular the authenticated scheduling-link family keeps `schedulingHostDTO.email` (§3.5), and its contract prose about host addresses **must not be touched**: `booking.yaml:962`, `:1071`, `:1430`, `:1434`, `:1456`, `:1603`, `:1652` all describe `SchedulingLink.hosts`, not `PublicHost`. A blanket find-and-replace over "host's email" would silently rewrite seven true statements; this list exists so that it does not.

**Status codes, response headers, referenced components**

- **Status codes: none added, none removed.** `getPublicLinkInfo` keeps exactly 200 / 404 / 410 / 429 / 500; `createPublicBooking` keeps its current set. The 429's `Retry-After` and the `BookingSlotsRateLimited` / `BookingError` references are untouched. **§3.10 uses a response field rather than a new endpoint partly for this reason**: an endpoint would have added an operation with its own 404, 410, 429 and rate-limit decision — a far larger shape change than the one it replaces.
- **Response headers: none added or removed.** The ICS travels as a JSON string, so there is no new `Content-Type`, no `Content-Disposition` and no non-JSON response anywhere.
- **Referenced components: none added, none removed.** No request shape, parameter, header or `security` block changes.
- If a bundle diff shows a status code, a response header or a component appearing or disappearing, the change has gone wrong and the PR must stop.

**Field ordering.** `publicHostDTO`'s JSON keys are emitted in Go struct declaration order, so each host entry today begins `{"email":…,"name":…,"avatar_url":…}` and afterwards begins `{"name":…,"avatar_url":…}`. **The first key of every host entry changes, in both responses.** Anything comparing a public answer byte-for-byte, hashing it or snapshotting it changes — which is why AC-8's byte-identity claim is scoped to the fields *outside* `hosts`, and why the four assertions of §2.7 are rewritten rather than adjusted. On the 201 the new ICS field is appended, so no existing field's position moves relative to another.

**Prose hunks in the contract, enumerated**

1. `paths/booking.yaml:38-42` — `getPublicLinkInfo`'s description currently states "This operation answers with every accepted host's address". **Must change**; it becomes the record of what it used to answer and what it answers now.
2. `paths/booking.yaml:2994` — `PublicHost`'s description.
3. `paths/booking.yaml:3001-3003` — `name`'s "Omitted when the user record has no name (json `name,omitempty`)", which stops being true.
4. `paths/booking.yaml:826` (bundle `openapi.yaml:8115`) — `createSchedulingLink`'s description, recording that the pre-audit sweep "harvested every host's address". Historically true and should stay, **amended** to say the address is no longer in that answer.
5. The ICS field's description: what it contains, that it is byte-identical to the emailed attachment, its iTIP method, and when it is omitted (§3.10).
6. `PublicBookingConfirmation`'s own description, which points at `bookingConfirmationDTO` and must name the new field.

Every edited fragment regenerates its bundle counterpart via `make openapi`, so **the bundle diff is larger than the fragment diff** and both must be in the PR.

**Checked and deliberately not changed** (they would look like candidates to a reader sweeping for the word): `paths/auth.yaml:1237` and `:2705` (bundle `openapi.yaml:2485`, `:12352`) say the current-user picture is also `avatar_url` in `SchedulingLinkHost` and `PublicHost` — still true, because `avatar_url` is kept. The `getPublicLinkInfo` 500 description's "never a 200 with `hosts` emptied, a host left out or `coverage.total` undercounted" names no address and stays verbatim.

**What the enumeration does not cover, and §3.1b is the reason.** Every line above describes a **response body**. The leak the retracted split would have left behind lived in a **downloaded file built from a cache of a different response** — outside everything this section can assert. So the shape enumeration is necessary and **not sufficient**: it must be read alongside AC-15, which checks each consumer's *output*. Reproducing this list in a PR description proves the contract changed as intended; it does not prove the value stopped flowing.

**Generated code**

- **Go** (`backend/api/gen/paceday.gen.go:3184-3191`): `PublicHost.Email openapi_types.Email` disappears and `Name *string` becomes `Name string`; `AvatarUrl *string` is unchanged. **No behaviour follows**, because nothing in `backend/` uses `gen.PublicHost` (§2.2) — grep `gen.PublicHost` returns nothing. `format: email` appears 31 times in the bundle and `openapi_types.Email` 26 times in the generated file, so the import survives; a diff showing it dropped means something else was removed too.
- **Hand-written Go.** `publicHostDTO` (`handlers_booking.go:45-49`) loses one field; both builders change (`publicLink:112`, `confirmation:149`). It stays **one** struct serving both responses, mirroring the one shared component (D-12).
- **Web** (`smart-calendar-flow/src/api/generated`): `PublicHost` loses `email`, `name` stops being optional, and the confirmation type gains the ICS field. **Every importer is a compile-time site, which is the check that would have found all three consumers** (§2.4a, W-4) — C-1 must move to §3.10's field, C-2 to a discriminator, C-3 needs nothing.
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

AC-1. Given P, when B requests `GET /api/book/{slug}` without credentials, then the response is 200 and **no byte of the body matches H1's or H2's address, that address's local part, or that local part with the `@` percent- or unicode-escaped** — asserted over the raw body, not over a decoded struct. *Fails today: `handlers_booking.go:112` sends both addresses.* [contract]

AC-2. Given P and an offered slot, when B books it, then the 201 body satisfies AC-1's assertion; and given the same request replayed with the same `Idempotency-Key`, the replay 201 satisfies it too. *Fails today: `handlers_booking.go:149`, on the fresh save and on the replay path through `writeBooked`.* [contract]

AC-2a. **Both answers lose it in the same commit** (D-12). A test asserts that `PublicHost` has one definition, that `getPublicLinkInfo`'s 200 and `createPublicBooking`'s 201 produce host entries with **identical key sets**, and that neither carries an address. *Fails today, and would also fail revision 2's retracted split — which is the point: it makes the retraction enforceable rather than a note.* [contract]

AC-2b. **The replacement precedes the removal.** The api PR may not merge while any read of either answer's `hosts[].email` remains in smart-calendar-flow: C-1 builds its download from §3.10's field, C-2 keys on a discriminator, C-3 needs nothing. *Fails today: all three read it (§2.3).* [web]

AC-3. Given P, the 200 body's `hosts` entries have exactly the keys the contract declares and no other — asserted by decoding into a map and comparing key sets, so a field renamed rather than removed fails. The same for the 201 — whose entries must have the **same** key set, AC-2a. *Fails today.* [contract]

AC-4. The assembled bundle's `PublicHost` has no `email` property, declares a non-empty `required` list, keeps `additionalProperties: false`, and is referenced by **exactly** `PublicLinkInfo.hosts` and `PublicBookingConfirmation.hosts` — **two referents, one definition, no added component.** Apart from `PublicBookingConfirmation` gaining the ICS property, no other schema in the bundle gains or loses one. *Fails today.* [contract]

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

AC-11a. **The shape enumeration is reproduced and is true.** The implementation PR's description carries §3.9's enumeration, and every line is checkable against the diff in that same PR: the fragment diff, the regenerated bundle diff, and both repos' regenerated types. Specifically: **one** schema loses a property and **one** gains one; `PublicHost` keeps **two referents and one definition**; **zero** components, status codes and response headers added or removed; six prose hunks present and the seven authenticated-roster statements (`booking.yaml:962`, `:1071`, `:1430`, `:1434`, `:1456`, `:1603`, `:1652`) untouched; the `openapi_types` import still present in the regenerated Go. **A claim the diff does not support fails this criterion** — and "I reasoned about where the values live" is not support; the bundle and the regenerated types are. **Reproducing this list is necessary and not sufficient** — §3.1b's leak lived outside every response body, so AC-15 and AC-16 are the other half. *Fails today: no such description exists.* [doc]

AC-11b. No status code's behaviour or documentation on either operation is altered as a side effect: `getPublicLinkInfo` still declares exactly 200, 404, 410, 429 and 500, the 429 still `$ref`s `BookingSlotsRateLimited` with its `Retry-After`, and `createPublicBooking`'s set is unchanged. Asserted against the assembled bundle, so a stray `$ref` edit fails it. *Regression guard; the reason it exists is PR #332's unannounced 429 (§3.8, W-6).* [contract]

**The whole stack, and the web**

AC-12. The e2e public-booking journey passes: an anonymous visitor opens `/book/{slug}`, sees the hosts, picks a slot, books it, and sees the confirmation. The two assertions at `public-booking.spec.ts:71-74` and `:170-172` are rewritten to identify the host by `name`, and the suite additionally asserts that neither answer's raw body contains the host's seeded address. *The last part fails today.* [e2e]

AC-13. The rendered public booking page shows no host address and no value derived from one, for a named host and for a nameless one; a nameless host renders the page's own placeholder. `[web]` **[unverified-web]** — the criterion is stated here, and whether it passes today depends on W-1, W-3 and W-5. The reader settles it in `src/pages/PublicBooking.tsx`.

AC-13a. **The web renders correctly against both contracts, before the api half merges.** With a mocked response that carries `email` (today's server) and with one that does not (after the api half), the page renders the same host list; and for a host whose `name` is the empty string it renders the page's placeholder, with a stable list key, and logs no error. This is the criterion that makes the merge window safe, and it must be green on the step-1 web PR — not on the paired pair. `[web]` **[unverified-web]**

**The record**

AC-14. `docs/security.md` carries this disclosure as **its own entry**, not as a clause of the struck-through rate-limit entry at `:92`, stating what was returned, what is returned now, and the two residuals of §2.8 (name and avatar stay public; a completed booking still puts hosts on a calendar event with the booker). `docs/factory/api-audit.md` carries the row D-9 opens, marked Fixed by this work. The B7 row of the threat table (`docs/security.md:61`) is corrected where it describes the public booking surface. [doc]

**The leak is checked at each consumer's output, not only in the response body** (§3.1b)

AC-15. **The criterion the retracted split would have passed while leaking.** Given a booker who completes a booking through the real UI on the compose stack, when the downloaded `.ics` is read from disk, then **its bytes contain no accepted host's address** — and this holds when the booking page was loaded first, so the link-info cache is warm and the mapper's fallback source is populated. *Fails today. It would also fail under revision 2's split, with every response-body assertion in this section green, which is why it exists.* [e2e]

AC-16. The same for the other two consumers' outputs: the rendered host list carries no address in any **attribute** value (`key`, `id`, `title`, `alt`, `data-*`) or colour seed (C-2), and the link-info cache's stored objects contain none (C-3). Asserted over the DOM and over the cache, not over rendered text — **rendered text is what both earlier sweeps checked, and it is what missed C-1 and C-2** (§2.4a). [web]

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
- **The one shared `PublicHost` definition** (D-12). It must not be split, duplicated or shadowed by a second host component, and its two referents must both lose the field in the same commit. AC-2a and AC-4 make the retraction enforceable; a diff that reintroduces a per-operation host type has reintroduced §3.1b's leak path.
- **The emailed confirmation ICS, and the mail path that carries it.** `generateICS` and `sendConfirmationEmail` keep their current output, `ORGANIZER` and `UID` scheme included (D-14, FU-4, FU-5). §3.10 **reuses** that builder for a new delivery; it does not redefine it. If the emailed attachment's bytes change, something has been fixed that this spec said it would not fix.
- **The docs site.** No page promises a host address on the booking page (§2.3), so no page needs editing. A change there would be a sign the removal was mis-scoped.

## 7. Rollback

Revert the paired PRs. Both answers carry the field again, exactly as at `308ac21`, and the ICS property goes with them; the web regenerates its types from the reverted bundle. **There is no migration and no stored data is touched**, so rollback loses nothing and needs no restore.

One ordering constraint on the way back: **the api half must not be reverted while C-1 relies on the §3.10 field**, or the download loses its source. A safe revert is therefore the contract and handler change *plus* restoring C-1's own builder — the mirror image of AC-2b, and worth writing into the PR body rather than discovering under pressure. C-2 and C-3 need nothing on the way back: neither wanted an address.

The step-1 web PR need not be reverted and should not be: a page that does not read a field it is sent is correct under both contracts. That asymmetry is deliberate — it means the rollback is a single revert of a paired pair, with no cross-repo ordering to get right under pressure.

The one thing a rollback does **not** undo: any address disclosed before the removal was already disclosed. This work stops a flow; it cannot retract what the flow produced. If the owner needs to know the exposure window, it is the lifetime of the field, which predates every spec in this directory.

## 8. Open questions

- **OQ-1 (owner).** `name` and `avatar_url` stay on the public answer, so an unauthenticated reader with a slug still gets every accepted host's display name and a `*.googleusercontent.com` avatar URL carrying a stable per-account identifier. Is that acceptable, or should the public entry narrow further — names only, or a count? **Default this spec is written to: both stay.** Showing who you are about to meet is the page's purpose, and a face is the ordinary way to do it. If the answer is "narrow further", the change is in the same loop and should be folded into this work rather than filed behind it.
- **OQ-2 (owner).** §2.8 item 3: a booker who completes a booking is added as an attendee to each host's calendar event, which carries the other attendees. Should that change — for example, one event per host with only that host and the booker? It is a real narrowing and it changes what the hosts see of each other, which is a product decision. Not decided here.
- **OQ-3 (reader of smart-calendar-flow).** W-1 and W-3: is `host.email` genuinely unread after #207, and is `hostsLabel`'s local-part fallback gone? This decides only whether step 1 of §3.8 is work or a confirmation; it does not change anything else in the spec.
- **OQ-5 (owner) — the product question §3.10 raises, and now the *only* thing gating this work.** Once C-2 and C-3 land on the web side, this is the single open contract question. In the server-built downloadable ICS, should `ORGANIZER` be (a) the host's **name only**, `METHOD:PUBLISH`, no `mailto:` — **the spec's recommendation**: no reply affordance is offered, so none is falsely promised, and the host address is not in the file at all; or (b) a **real host address**, `METHOD:REQUEST` — the booker's calendar client offers "respond to organiser" and a reply reaches the host's mailbox as mail, which Paceday does not process and will not record, and the host address is readable in the file by anyone who completed a booking (§3.10's residual)? **Default this spec is written to: (a).** The booker loses nothing they have today, because the affordance they are offered today already goes nowhere. If the answer is that a booker should be able to reach a host, that deserves a deliberate reply path rather than a host address leaking in as a by-product of how a file is built.
- **OQ-6 — withdrawn with the split** (D-12). There is no sequencing question left: both answers lose the field in one commit, and the only thing gating the release is OQ-5.
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
| D-7 | **The merge order is web-first, then paired** (§3.8), which inverts the factory's usual stage-4 order. **Revisions 2 and 3: this held, and all three consumers are why it was written.** Revision 1 chose the order so that an unverified consumer could not be broken by it, and three were then found. C-2 and C-3 can simply stop reading (and C-2's fix — a discriminator rather than an address — is better code regardless); **C-1 cannot**, because it needs an address for a real purpose, so it must first switch to D-13's field. That is AC-2b. A response-field *removal* reverses the dependency direction: the client must stop reading before the server stops sending. Step 1 is an unpaired web PR with **no `pac-N` token** (so it pairs against api `main` and its `openapi-check-web` passes against the unchanged bundle); step 2 is one `factory/pac-N-…` branch in each repo. Both halves of step 2 **must** carry the token or the two repos deadlock against each other's `main`. | [F] |
| D-8 | **No sanitisation on the response path.** `users.name` is already `safetext.Label`'d at its single write (`storage/signin.go:108`), `encoding/json` escapes control characters so a JSON body cannot be line-injected, and the line-oriented composers that genuinely need the guard already have it. A name stored before 2026-11-22 is returned unfitted; in JSON that is harmless, and what a consumer does with it is the consumer's boundary. | [F] |
| D-9 | **A register row is opened at intake.** The disclosure has no row in `api-audit.md` or `silent-failures.md` today — its only written trace is one clause inside the struck-through rate-limit entry at `docs/security.md:92`. A finding recorded as a subordinate clause of a closed finding is a finding nobody will find again. AC-14 makes the record its own. | [F] |
| D-10 | **The local-part derivations in `handlers_manager.go` are out of scope, with the reason recorded** (§2.6). All five derive a label from an address the caller supplied, on an authenticated route, and return it only to that caller. The web's `hostsLabel` differed in exactly one respect — its reader had not supplied the address — and that is the property this work removes. Passing it over deliberately, rather than silently, is the point. | [F] |
| D-11 | **The change is specified as a shape, enumerated in §3.9, and the implementing session must reproduce that enumeration in its PR description and have the diff support it** (AC-11a, AC-11b). Removing a required property from a public response is a shape change, and a shape change described only in prose has been under-reported twice on 2026-11-22: #331 as "two doc comments" against five hunks in three files, and #332 as "not reaching the generated types" while it added a 429 with a `Retry-After` to this very operation — a status code the web still does not map (W-6). The enumeration is confirmed by diffing the regenerated bundle and both repos' regenerated types, never by reasoning about where values live. Its direct consequence is D-7's merge order plus a web behaviour that is correct under **both** contracts before the api half merges (AC-13a), because api merges first by design and real visitors are in the window. | [F] |
| D-12 | **Both DTOs move together; the component is not split. Revision 2's split is retracted.** The web's booking mapper resolves the confirmation's hosts with a **second source** — roughly `raw.hosts ?? link?.hosts ?? []`, where `link` is a cached `PublicLinkInfo` and `raw` is a cast, not a strictly-parsed value. So removing `email` from the 201 alone leaves C-1's ICS able to receive host entries sourced from the cached link lookup: **a response-body inspection would show the field gone while the downloaded file still carried addresses**, with every response-keyed criterion green. The split's premise — two independently-removable halves — was false, so revision 2's "cleaner" argument was reasoning carefully about risk *kinds* on top of a wrong assumption. One edit, both referents, one release. **This is the spec's best example of a change that would have looked successful and leaked anyway**, and the lesson generalises: a field removal is verified at each consumer's *output* (AC-15, AC-16), never at the response body alone. | [F], revised |
| D-13 | **The server builds the ICS and the 201 carries it as a string field; the web stops building one.** The two candidates were not independent and checking settled it: the server's ICS **already** uses a no-reply `ORGANIZER` (`engine/booking.go:975`), so "no-reply organiser" is what the server option already does, and choosing it without moving the build keeps the duplicated builder *and* loses the reply path — strictly worse on both axes. The duplication is not theoretical: the emailed and downloaded files already disagree on `ORGANIZER`, on the attendee list and probably on `UID` (§2.5), so one source of truth is a bug fix. **A field, not an endpoint**, because an endpoint adds an operation with its own 404/410/429 and rate-limit decision — a larger shape change than the one it replaces — while a field is delivered in a response only a completed booking receives and makes the two artifacts byte-identical by construction. The structural gain, and the real reason to prefer the server: it **decouples "the file names a host" from "the API hands a client an address"**, which the web builder cannot do because its file can only contain what the response gave it. That turns OQ-5 from a constraint into a choice. | [F] |
| D-14 | **The emailed ICS's RSVP affordance is dishonest today, and this spec records it without fixing it.** `METHOD:REQUEST` plus `ORGANIZER: noreply@paceday.app` means every calendar client shows Accept / Decline, every reply reaches a no-reply mailbox, and the booker is told nothing. No register row exists. §3.10 removes the false affordance from the **downloadable** file, which is this spec's to define; the emailed invitation is not, and FU-4 carries it. Recording a defect found while checking a premise, and declining to widen scope for it, is the point. | [F] |
| D-15 | **The enumeration was short, and the method is recorded rather than the result** (§2.4a). Revision 1's web claims were derived from a citation index of *this* repository plus reasoning about what a booking page displays, so they came out page-shaped and transport-shaped. Two of three consumers were named by file in this repo's own `docs/specs/` — `HostAvatars.tsx` six times, including `NEW-deploy-hardening.md:237`'s "public booking page included", and `booking/ics.ts` at `PAC-91.md:254` — and the sweep still missed them. W-4 (every importer of the generated type) was the only file-independent claim and would have been complete; it was ranked fourth. The re-derived question is **"what reads this field"** across seven categories, of which revision 1 had **no entry at all** for serialisation into another artefact — where the one genuine consumer lived. Categories 6 and 7 (logging/analytics, types) remain unswept: **W-7**. | [F] |
