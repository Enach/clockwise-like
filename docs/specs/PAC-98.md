# Spec — PAC-98: Sign-in password step posts to /api/auth/login and /register, which don't exist

- **Linear**: https://linear.app/paceday/issue/PAC-98/sign-in-password-step-posts-to-apiauthlogin-and-register-which-dont
- **Status**: accepted with changes (spec-challenger, 2026-11-13; changes applied in the body, Challenge log at the end). Owner questions OQ-1..OQ-4 still open; the factory defaults apply.
- **Author**: spec-author
- **Priority**: High
- **Inherited scope**:
  - `contracts/openapi/paths/auth.yaml` carries no `x-uncertain` marker on the detection operation (`auth.yaml:833-885`) or its answer schema (`auth.yaml:2697-2740`).
  - API-062 (`docs/factory/api-audit.md:320`, per-replica detect rate limit): still open, not touched here (this ticket changes neither the limiter nor its key; same reasoning as PAC-97 §5).
  - API-092 (`api-audit.md:350`): every detection outcome changed here gets a Go assertion through the real router (AC-6).
  - No audit row covers the web's password step; the gap was found in the PAC-97 challenge (B-1, `docs/specs/PAC-97.md:665-674`).
- **Verified against**: `clockwise-like` `origin/main` at `68540f6` (PAC-97 backend merged as `b0c0d7a`); `smart-calendar-flow` `origin/main` at `ae72939` (PAC-97 web merged as `aea164c`), read with `git show`, no checkout. No code was run.

> ## FACTORY-ASSUMED DEFAULT — the owner may choose otherwise
>
> The issue asks the owner one question: **build password sign-in, or send
> consumer addresses to the provider choice?** The owner has not answered.
> This spec is written on the factory default: **Paceday has no password
> sign-in. The password step is removed. Personal addresses go to a provider
> (Google directly for gmail.com and googlemail.com, otherwise a provider
> choice), carrying the typed email as the login hint.**
>
> **The owner may choose to build password sign-in instead.** That is a
> separate, larger ticket (credential storage and hashing, registration,
> email verification, password reset, brute-force protection, and how a
> password identity fits PAC-71's provider-bound accounts). It is not
> specified here, and nothing in this spec makes it harder later. If the owner
> chooses it, this spec is withdrawn, not amended. See OQ-1.

## 1. Problem

A person who signs in to Paceday with a personal address (gmail.com,
outlook.com, yahoo.com, icloud.com and the rest of a fixed list) is shown a
"Continue with email" form asking for a password, with "Sign in" and "Create
account" tabs. Neither action can work: the web posts to two server routes
that have never existed. Signing in answers "No account found — try creating
one." Creating an account fails with "Something went wrong." Nothing tells the
person what to do instead.

Who is affected: everyone who types a personal address into the email field
rather than pressing a provider button first. For a gmail.com user, Google
sign-in already works and treats a gmail.com address as authoritative. The
dialog just never sends them there. The cost is the first impression: a
sign-up form that cannot sign anyone up.

No one has a Paceday password today. The server has no password column, no
password route, and no password code (§2.3). So removing the form loses no
account and needs no migration.

## 2. Current behaviour

### 2.1 Detection (`backend/auth/sso_detect.go`, `clockwise-like` `68540f6`)

- The answers are constants `google`, `microsoft`, `sso`, `generic`, `choose`
  (`sso_detect.go:20-29`); `DetectResult` is the encoded body (`:31-36`).
- `detect` (`sso_detect.go:165-189`), in order:
  1. No extractable domain, or a domain on the consumer list → `generic`
     (`:166-169`). The SSO table is not read and no DNS query is made.
  2. An SSO provider row for the exact domain → `sso` with provider name and
     `/api/auth/sso/<domain>` (`:171-182`). A database error goes to the
     handler (500).
  3. A domain that is not a multi-label ASCII DNS name → `choose` without a
     query (`:184-187`).
  4. MX records → `google`, `microsoft` or `choose` (PAC-97, `:188`,
     `:191-354`). `*.olc.protection.outlook.com` (Outlook.com custom domains,
     personal Microsoft accounts) is deliberately a non-match, so such a
     domain gets `choose` (`:349-354`).
- The consumer list is `backend/domain/domain.go:5-14`: gmail.com,
  googlemail.com, outlook.com, hotmail.com, live.com, msn.com, yahoo.com,
  yahoo.fr, yahoo.co.uk, icloud.com, me.com, mac.com, protonmail.com,
  proton.me, aol.com, mail.com, gmx.com, yandex.com, yandex.ru, zoho.com,
  fastmail.com. `ExtractDomain` splits on the first `@`, lower-cases and
  trims (`:16-22`); `IsGenericDomain` is an exact lookup (`:24-26`).
- The same list is read by two other features that this ticket must not
  change: organisation matching skips consumer domains
  (`backend/storage/orgs.go:78-89`, `AssociateUserWithOrg`, called after every
  Google, Microsoft and SSO sign-in: `handlers_pac71_auth.go:188,314`,
  `handlers_sso.go:199`), and the PAC-71 review log line skips them
  (`backend/storage/signin.go:277`). **No test pins the organisation skip**:
  `backend/domain/domain_test.go:27-38` checks five list entries through
  `IsGenericDomain`, and no test calls `AssociateUserWithOrg` with a consumer
  address (the gmail.com user in `storage/org_ai_test.go:324` never goes
  through it). AC-21 adds the pin.
- Tests pinning `generic` today: `backend/auth/sso_detect_test.go:10-30`
  (gmail.com and `not-an-email`), `backend/auth/pac97_detect_test.go:210-228`
  (AC-4: gmail.com, outlook.com, `not-an-email`) and `:540-555` (AC-14),
  `backend/api/pac97_detect_test.go:207-215` (gmail.com through the default
  detector), `backend/api/pac35_auth_flows_test.go:890-896` ("200 not sso":
  gmail.com → `generic`).

### 2.2 The contract

- `POST /api/auth/detect`, `detectAuthProvider` (`auth.yaml:833-885`): 200,
  400, 429, 500.
- `SsoDetectResponse.type` (`auth.yaml:2702-2732`): enum
  `[google, microsoft, sso, generic, choose]` (`:2704`). "generic" is
  described as "the address has no domain, or its domain is on the fixed
  consumer list" (`:2707-2709`). "choose" says "it never shows a password
  form for this value" (`:2726-2728`), which implies a password form for
  "generic". An unknown value is to be treated as "choose" (`:2731-2732`).
- No operation for password sign-in or registration exists in `auth.yaml` or
  the bundle. The generated Go (`backend/api/gen/paceday.gen.go`) and the
  web's generated types carry the same enum.

### 2.3 Backend remnants of password auth: none

- No `/api/auth/login` or `/api/auth/register` route, handler or contract
  operation exists anywhere in `clockwise-like` (`grep` over the repository;
  the only matches are the PAC-97 notes at
  `contracts/features/PAC-97.md:134` and `contracts/features/PAC-97.yaml:130`).
- The `users` table has no password column (`backend/storage/migrations/006_auth.up.sql:3-12`);
  no later migration adds one (`grep -i password` over `migrations/` finds nothing).
- No password hashing library is imported (`grep bcrypt|argon` finds
  nothing). The `password` matches under `backend/` are the Zoom meeting
  password (`conference/zoom.go:64,73`), a stored-credential field
  (`api/handlers_credentials.go:249-250`) and log/Sentry redaction
  (`config/endpoints.go:106`, `sentry.go:26`). None of them is about sign-in.

### 2.4 Provider sign-in for personal accounts today

- **Google**: a verified gmail.com or googlemail.com address is
  authoritative evidence without a hosted domain
  (`backend/api/handlers_pac71_auth.go:81-95`). Another verified address is
  mailbox evidence, which may create a new account
  (`backend/storage/signin.go:146-153`). A Google Account registered with a
  yahoo.com or outlook.com address can therefore sign up.
- **Microsoft**: the sign-in start uses the `/common` authority
  (`backend/auth/microsoft_oauth.go:20-22`, `:66-69`), so Microsoft lets a
  personal account authenticate. Paceday then **refuses** it: an ID token
  whose tenant is the consumer tenant (`backend/auth/ms_idtoken.go:34`) is
  sent to the refusal state (`handlers_pac71_auth.go:252-256`, "refused until
  PAC-77"). This is accepted by the owner: "PAC-71 ships without personal
  Microsoft sign-in; PAC-77 restores it later" (owner-decisions, 2026-11-01).
  PAC-77 is in Backlog.
- The refusal notice deliberately names no cause
  (`smart-calendar-flow` `src/components/auth/SignInRefusedNotice.tsx:5-12,23-27`).
  So a personal Microsoft user sent to Microsoft authenticates there, comes back,
  and sees only "Paceday could not sign you in with this account."
- `/api/auth/microsoft` answers 503 when Microsoft is not configured
  (`backend/api/handlers_microsoft_auth.go:16-21`).
- `/api/auth/google` has **no** such check (`backend/api/handlers_auth.go:26-41`):
  on a deployment without Google credentials it still redirects the browser
  to Google with an empty client id, which ends on Google's own error page.
  Availability is published by `GET /api/integrations/availability`
  (`backend/api/integration_availability.go:21`).

### 2.5 Web (`smart-calendar-flow` `origin/main` `ae72939`)

`src/components/auth/AuthDialog.tsx`:

- Steps `discover`, `choose`, `generic` (`:92`). Opened from a failure
  notice with an email remembered in this tab, the dialog starts at `choose`
  (`:91-93`).
- `onEmailContinue` (`:134-187`): validates (`:137-141`), remembers the email
  in `sessionStorage` (`:146`), posts to detection (`:149-152`), then:
  `google` / `microsoft` → navigate at once to that start with
  `login_hint`, **whatever the deployment's availability says**
  (`:154-161`); `sso` with a path → navigate (`:162-167`); **`generic`, or
  `sso` without a path → the password step** (`:168-175`); anything else →
  `choose` (`:176-178`).
- `ChooseStep` (`:391-491`): Google and/or Microsoft per availability, each
  with the hint; "not available on this deployment" when neither
  (`:466-474`); "Use a different email" (`:477-488`). Its copy is written for
  a company: "Paceday can't tell which provider … uses. Pick the one your
  company signs in with." (`:431-437`).
- `GenericStep` (`:495-659`): "Continue with email", "Sign in" / "Create
  account" tabs, a password field (`:616-631`), a 6-character minimum
  (`:514-517`), posts to `/api/auth/login` (`:522-525`) or
  `/api/auth/register` (`:527-530`); 401 → "Incorrect email or password.",
  404 → "No account found — try creating one." (`:540-546`). An unreachable
  API shows the PAC-83 failure view (`:535-538`).
- The discover step's email field is labelled "Work email" with placeholder
  `you@company.com` (`:329-338`). The top provider buttons carry no hint
  (`:300-317`, PAC-71 AC-32).
- After a failed or refused provider sign-in, `src/pages/Index.tsx:564-607`
  opens the dialog with `offerSwitch`. The remembered email then gets the
  company `choose` step with both providers
  (`AuthDialog.pac97.test.tsx:245-254`). That includes a personal address,
  for which Microsoft can only end in the refusal of §2.4.

Tests that touch the password step:

- `src/components/auth/AuthDialog.test.tsx:115-121`: `generic` for
  carol@gmail.com shows the "Password" field.
- `src/components/auth/AuthDialog.pac83.test.tsx:140-155`: PAC-83 AC-14, "a
  failed password sign-in shows an error with a retry, never the demo"
  (posts to `/api/auth/login`).
- `src/lib/api.pac83.test.ts:29`: uses `/api/auth/login` only as a sample
  path for the client timeout (not a password-step test, but it names a route
  that does not exist).
- `src/components/auth/AuthDialog.pac97.test.tsx:114-115,158,182`: assert
  that the choose step has **no** password field (they stay).

### 2.6 End to end (`clockwise-like` `e2e/`)

- No e2e journey reaches the password step or types a consumer address. The
  only sign-in-by-email journey is `e2e/tests/pac97-signin-detect.spec.ts`. It
  uses company domains served by the DNS stand-in, asserts the choose step
  has no password field (`:125-126`), and finds the email field by the
  label "Work email" (`:66`, `:135`).
- The e2e stack configures Google sign-in only. Microsoft is unavailable there
  until PAC-99 (`pac97-signin-detect.spec.ts:146-158`, a `test.fixme`).
- The journey "an Exchange Online domain goes straight to the Microsoft
  start" (`pac97-signin-detect.spec.ts:160-168`) expects the dialog to
  navigate to `/api/auth/microsoft` on that Google-only stack. It passes today
  only because the web navigates on `microsoft` without reading availability.
- The DNS stand-in (`e2e/fake-google/dns.go`) answers MX queries but records
  none and exposes no query log on its control plane, so an e2e test cannot
  observe that a name was **not** queried.
- A consumer domain never reaches the resolver (§2.1 step 1), so an e2e
  journey with gmail.com or yahoo.com needs no DNS fixture.

### 2.7 Lovable docs (`smart-calendar-flow`)

- `AGENTS.md` already says "Do not invent endpoints" (Scope section) and
  `docs/lovable-prompts/00-global-context.md` "Do not create a new API
  endpoint" (Hard boundaries). The password step calls two routes the contract
  never had, so a prose rule alone did not prevent it. The rule that both
  agents obey is the gate (`docs/factory/README.md` §3, design gate: the
  Makefile is the only rule both agents obey).
- No Lovable-facing document mentions password sign-in: `AGENTS.md`,
  `docs/lovable-prompts/*.md`, `.lovable/plan.md`, `PRODUCT.md`,
  `docs/architecture.md`, `docs/security.md` (`git grep -i password` hits only
  API-key fields, `.lovable/plan.md:38`).
- `.lovable/plan.md:62` still says "No auth UI beyond connect/disconnect".
  That is stale, but it is not about passwords.
- Nothing in these documents says that Paceday has no password sign-in. Lovable's
  agent edits the web directly and reads `AGENTS.md`
  (`docs/factory/README.md` §1, §3 design gate), so nothing stops it from
  adding a password form again.

## 3. Desired behaviour

**Factory-assumed (see the banner): no password sign-in.**

When a person types an email and continues:

1. **gmail.com / googlemail.com**: they go straight to Google sign-in with the
   typed address as the login hint, like a Google Workspace company domain
   today.
2. **outlook.com, hotmail.com, live.com, msn.com**: these are personal
   Microsoft accounts, and Paceday refuses them until PAC-77. So Paceday
   does **not** send them to Microsoft. They get the personal-address step
   (point 3). The step says plainly that personal Microsoft accounts cannot
   sign in to Paceday yet. When PAC-77 ships, these four go straight to
   Microsoft with the hint (PAC-77's change, recorded in §5).
3. **Every other personal address on the list** (yahoo, icloud, proton, …),
   and the four Microsoft domains until PAC-77, get a **personal-address
   step**:
   - It shows the address and offers "Continue with Google" (or "Sign up
     with Google") with the address as the hint, when the deployment offers
     Google.
   - It says that personal Microsoft accounts cannot sign in to Paceday yet.
     It offers no Microsoft button for the address until PAC-77. The
     sentence is shown for every personal address, not only the four
     Microsoft domains, because a personal Microsoft account can use any
     address (a yahoo.com one included) as its sign-in name. The copy names
     no date.
   - It offers "Use a different email", which keeps the address.
   - When the deployment offers no provider that accepts a personal account,
     it says so and offers going back.
   - Its wording does not tell the person to pick "the one your company
     signs in with".
4. **No step anywhere shows a password field.** Whatever detection answers,
   including a value the web does not know or an SSO answer without its
   path, the dialog never shows a password form. It never posts to a password
   sign-in or registration route.
5. **A direct answer for a provider the deployment does not offer** (for
   example gmail.com on a Microsoft-only deployment, or an Exchange Online
   company domain on a Google-only deployment) does not navigate to a start
   that cannot work (Microsoft: a 503 page; Google: Google's error page,
   §2.4). The person gets the provider step for that address instead: the
   personal-address step for a personal address, the PAC-97 choose step for a
   company address. While availability is still loading, or its read failed,
   the dialog navigates as today.
6. **After a failed or refused sign-in**, retrying from the notice brings back
   the step that fits the remembered address. A personal address gets the
   personal-address step, never a Microsoft button that can only be refused.
   A company address gets the PAC-97 choice, unchanged. When the kind of the
   remembered address is already known in the tab, no request is made to
   find it again; only an address remembered without its kind (written by an
   older build, or remembered before a detection that failed) costs one
   detection request.
7. Company domains, SSO domains and the rest of PAC-97 behave exactly as
   today.
8. The web repository's agent guidance (read by Lovable) states that
   Paceday's sign-in is provider-only and that no password form may be added
   without an owner decision. The web gate (`make verify`), not the prose,
   is what fails when a password form or a password route comes back (AC-13).

## 4. Acceptance criteria

Test levels: **[unit]** Go unit test with the PAC-97 fake resolver (no live
DNS); **[api]** Go test through the real router; **[contract]** schema and
generated types; **[vitest]** web component test; **[static]** a test or
gate step that reads repository files; **[e2e]** Playwright against the
compose stack.

### Server

- **AC-1** [unit] Given detection for `carol@gmail.com`, `Carol@GMail.com`
  and `dan@googlemail.com`, when it runs, then the answer is google with no
  provider name and no redirect path. The fake resolver records zero queries
  and the SSO row is not read. *(Fails today: generic, `sso_detect.go:167-169`.)*
- **AC-2** [unit] Given detection for each of outlook.com, hotmail.com,
  live.com and msn.com, then the answer is the personal-address answer
  (§9 D-4), **not** microsoft. Zero queries, SSO row not read. The test's
  name or comment says that PAC-77 changes this expectation.
- **AC-3** [unit] Table-driven over **every** entry of the consumer list:
  gmail.com and googlemail.com → google; every other entry → the
  personal-address answer. A new entry added to the list without a row in
  this table fails the test (the table is checked against the list itself:
  the test enumerates the consumer list from the `domain` package, never
  from a hand-written copy).
- **AC-4** [unit] Given an address with no extractable domain (`not-an-email`,
  `alice@`, `@`), when detection runs, then the answer is choose, never the
  personal-address answer, with zero queries. *(Fails today: generic.)*
- **AC-5** [unit] Given an SSO provider row for gmail.com in the test
  database, when detection runs for `carol@gmail.com`, then the answer is
  google: the consumer rule is checked before SSO, as today (`:166-169`).
  *(Pins the existing order with the new answer.)*

### API

- **AC-6** [api] Given the real router, when `alice@gmail.com` is posted,
  then 200 and the body decodes strictly into the generated type as google
  with neither provider name nor redirect path. When `bob@yahoo.com` and
  `carol@outlook.com` are posted, then 200 and the personal-address answer.
  The PAC-35 sub-test "200 not sso" (`pac35_auth_flows_test.go:890-896`) is
  updated to these expectations. 400, 429 (21st request in a minute) and 500
  are unchanged.

### Contract

- **AC-7** [contract] The answer schema's description says:
  - the personal-address answer means a personal address that the user
    signs in to with a provider account, chosen on the client;
  - gmail.com and googlemail.com are answered google;
  - an address without a domain is answered choose;
  - the client never shows a password form for any value, and Paceday has no
    password sign-in.

  The enum is unchanged (§9 D-4). `make openapi-check` passes in both repos
  with the regenerated types. *(Fails today: `auth.yaml:2707-2709,2726-2728`.)*

### Web

- **AC-8** [vitest] Given detection answers the personal-address answer for
  `dave@yahoo.com`, and Google and Microsoft are both available, when the
  email is submitted, then:
  - the dialog shows the personal-address step with the address;
  - it shows a Google button and no Microsoft button;
  - it says personal Microsoft accounts cannot sign in to Paceday yet;
  - no password field is in the document, and nothing navigates;
  - clicking Google navigates to the Google start with
    `login_hint=dave%40yahoo.com`.

  *(Fails today: the password step, `AuthDialog.tsx:168-175`.)*
- **AC-9** [vitest] Same as AC-8 in sign-up mode: the button reads "Sign up
  with Google".
- **AC-10** [vitest] Given the personal-address answer and Google not
  available, then the step says that sign-in for personal addresses is not
  available on this deployment and offers going back. No password field, no
  Microsoft button.
- **AC-11** [vitest] Given the personal-address step, when the user activates
  "Use a different email", then the discover step returns with the email
  still filled in.
- **AC-12** [vitest] Table-driven over every answer: google, microsoft, sso
  with a path, sso **without** a path, the personal-address answer, choose,
  and an unknown `"future"`. For each one, after submit, no element labelled
  password is in the document, and `fetch` was never called with a path
  ending in `/api/auth/login` or `/api/auth/register`. sso without a path
  shows the PAC-97 choose step. *(Fails today for the personal-address answer
  and for sso without a path.)*
- **AC-13** [static] A check that runs inside the web's `make verify` (so a
  Lovable edit meets it in the same gate as everything else) fails when:
  - any non-test file under `src/` contains `auth/login` or `auth/register`
    (any prefix, any quoting, so `apiFetch("/api/auth/login")`, a template
    literal holding `/api/auth/register` and `apiUrl("/auth/login")` all
    fail);
  - any non-test file under `src/components/auth/` or a sign-in page renders
    an input of type `password`, or uses `autoComplete` `current-password`
    or `new-password`. (The API-key fields in `src/pages/Settings.tsx:763`
    and `src/components/settings/OrganizationAISection.tsx:274` are outside
    this scope and stay.)

  The check has a self-test: a fixture containing each forbidden form
  makes it fail, and a fixture with none passes. The `/api/auth/login`
  sample path in the client-timeout test (`api.pac83.test.ts:29`) is replaced
  by a route that exists. A route composed at run time from fragments
  (`/api/auth/${x}`) is not caught statically; AC-12 catches it in the
  dialog at run time. *(Fails today: `AuthDialog.tsx:522,527,622`.)*
- **AC-14** [vitest] Given detection answers google, availability has loaded,
  and Google is **not** available, then the dialog does not navigate. It
  shows the provider step for that address (for gmail.com, the
  personal-address step's "not available" state). The same holds for
  microsoft when Microsoft is unavailable (the PAC-97 choose step). Given
  availability still loading or failed, google navigates as today
  (`AuthDialog.test.tsx:89-94` unchanged). *(Fails today: the dialog
  navigates on google and microsoft without reading availability,
  `AuthDialog.tsx:154-161`.)*
- **AC-15** [vitest] Given the tab remembers `dave@yahoo.com` (and separately
  `carol@outlook.com`), when the dialog is opened from a failure notice, then
  it offers the personal-address step for that address: Google with the
  hint, no Microsoft button. Given a remembered company address, it offers
  the PAC-97 choice with both providers, as today
  (`AuthDialog.pac97.test.tsx:245-254`). Request count, falsifiable:
  - when the tab recorded the detection answer's kind together with the
    email (under PAC-97 D-10's storage rules), opening from the notice makes
    **zero** requests to `/api/auth/detect`;
  - when only the email is remembered (no kind: an older build wrote it, or
    the detection that followed failed), it makes **exactly one**, and that
    request follows PAC-83: an unreachable API shows the failure view with a
    retry, never the demo and never a password field.

  The PAC-97 test at `AuthDialog.pac97.test.tsx:245-254` changes on purpose
  in its setup only (it records the company kind with the email); its
  assertions, both providers and zero detection requests, stay. *(Fails
  today: both providers for every remembered address.)*
- **AC-16** [vitest] PAC-83 AC-14 on the new step: given the personal-address
  step, when Google is clicked while Paceday is unreachable, then the failure
  view with a retry is shown, never the demo. The retry starts Google with the
  same hint. This replaces `AuthDialog.pac83.test.tsx:140-155`, which tests a
  password step that no longer exists.
- **AC-17** [static] The web's agent guidance files that Lovable reads
  (`AGENTS.md` and `docs/lovable-prompts/00-global-context.md`, which
  carries its own parallel list of hard boundaries)
  each contain a rule that says:
  - Paceday sign-in is provider-only (Google, Microsoft, company SSO);
  - the server has no password sign-in;
  - no password form may be added without an owner decision (PAC-98).

  The web gate fails if the rule is missing from either file; the check
  looks for a stable marker (the text `PAC-98` and the word `password` in the
  same rule), not for an exact sentence. *(Fails today: neither file has
  it.)*

### End to end

- **AC-18** [e2e] Given the e2e stack, when a user types
  `<random>@gmail.com` on `/signin` and continues, then detection answers
  google. The browser's next request is `/api/auth/google`, with `login_hint`
  equal to the typed address. The journey stops at the Google stand-in as in
  `pac97-signin-detect.spec.ts`. The run's hermeticity check still passes.
  (That gmail.com is never sent to the resolver is proven by AC-1 with the
  fake resolver: the e2e DNS stand-in keeps no query log, §2.6.)
- **AC-19** [e2e] Given the e2e stack, when a user types `<random>@yahoo.com`
  and continues, then:
  - the personal-address step is visible with the address;
  - the dialog has no password field and no Microsoft button;
  - clicking Google requests `/api/auth/google` with the hint.

  During the whole journey, no request goes to a path ending in
  `/api/auth/login` or `/api/auth/register`. *(Microsoft is unavailable in this
  stack (PAC-99), so the "no Microsoft button although available" half is
  proven by AC-8, not here.)*
- **AC-20** [e2e] D-6 for a company domain, on the Google-only stack: when a
  user types `carol@<fresh>.microsoft-mx.pac97.test` and continues, then
  detection answers microsoft (read through the pass-through, as today), no
  request goes to `/api/auth/microsoft`, and the PAC-97 choose step is
  visible with the address, a Google button and no Microsoft button. This
  replaces the journey at `pac97-signin-detect.spec.ts:160-168` (changes on
  purpose, §6). PAC-99, which configures Microsoft in the stack, re-pins it
  to "navigates to the Microsoft start with the hint".

### Organisation matching

- **AC-21** [integration] Given each entry of the consumer list (enumerated
  from the `domain` package, as in AC-3), when `AssociateUserWithOrg` runs
  for a user with an address on it, then the user's `org_id` stays null and
  no organisation row exists for that domain. Given `alice@acme-pac98.test`,
  the user is linked to an organisation for `acme-pac98.test`. *(Pins
  behaviour that must not change, §6; passes today and must still pass.)*

## 5. Explicitly out of scope

- **Password sign-in.** This is the alternative the owner may choose (banner,
  OQ-1). It would be its own, larger ticket.
- **Personal Microsoft sign-in (PAC-77).** This ticket does not send the four
  Microsoft consumer domains to Microsoft, and it offers no Microsoft button on
  the personal-address step. **Hand-off to PAC-77**: when PAC-77 makes
  personal Microsoft sign-in work, it must
  - answer microsoft for outlook.com, hotmail.com, live.com and msn.com
    (AC-2/AC-3 expectations change);
  - offer Microsoft on the personal-address step and drop the "not yet"
    sentence (AC-8, AC-15 change).

  Until then, the top-of-dialog "Continue with Microsoft" button stays as
  it is (PAC-71 AC-32). A personal Microsoft account that uses it is refused
  as today.
- **Regional and custom-domain Microsoft consumer addresses** (outlook.fr,
  hotmail.co.uk, live.fr, …, and domains whose MX is
  `*.olc.protection.outlook.com`). These are not on the consumer list, so
  detection reads their MX. They get the PAC-97 company choice, and Microsoft
  there ends in the refusal. Extending the consumer list would also change
  organisation matching and the PAC-71 review log (§2.1), so it is not done
  here (OQ-3).
- **Other sign-in providers** (Apple, Yahoo, email magic links). They are not
  requested.
- **The email field's "Work email" label and `you@company.com` placeholder.**
  They are unchanged. The e2e suite finds the field by that label (§2.6), and
  a copy change is a design call (OQ-4).
- **API-062**, as in PAC-97.
- **The stale `.lovable/plan.md:62` line.** It is not about passwords. The
  implementer may correct it while editing the guidance files, but nothing
  depends on it.

## 6. What must not change

| Behaviour | Protected by |
|---|---|
| The consumer list's members, and its use by organisation matching and the PAC-71 review log | `domain.go:5-14`; AC-3 and AC-21 (no test pinned the organisation skip before this ticket, §2.1); the PAC-71 review-log condition (`storage/signin.go:277`) is unchanged code |
| Consumer rule before SSO; SSO row wins for a company domain; answer shape | `sso_detect_test.go:32-…`; `TestPAC35_Detect_EveryDocumentedStatus` (sso, 400, 429, 500); AC-5 |
| Company-domain MX decisions and caching (PAC-97) | `backend/auth/pac97_detect_test.go` (all but AC-4's consumer rows); `backend/api/pac97_detect_test.go` |
| Strong company answers navigate with the hint | `AuthDialog.test.tsx:89-94`; `AuthDialog.pac97.test.tsx:187-195` |
| PAC-97 choose step for company domains, its back link, sign-up labels, availability states | `AuthDialog.pac97.test.tsx:74-195` |
| Remembered email: `sessionStorage` only, cleared on sign-in/out, works when storage throws | `AuthDialog.pac97.test.tsx:225-243,256-283` |
| Top provider buttons start the public sign-in without a hint, never a connect | `AuthDialog.test.tsx:96-113` (PAC-71 AC-32) |
| Failure notices accept fixed values only; refusal names no cause | `Index.authFailed.test.tsx`, `pac71Landing.test.tsx`, `SignInRefusedNotice.tsx` |
| No demo after any sign-in failure (PAC-83) | `AuthDialog.pac83.test.tsx` (other cases), AC-16 |
| Personal Microsoft accounts are refused at the callback until PAC-77 | the PAC-71 callback tests (`backend/api/pac71_*`) |
| The hint is forwarded by the starts only when valid | `handlers_auth.go:68-73` and the PAC-35 login-hint tests |
| Company-domain e2e journeys (Google MX, gateway) | `e2e/tests/pac97-signin-detect.spec.ts:89-136` |

Tests that **change on purpose** (they pin today's password or `generic`
behaviour):
- `sso_detect_test.go:10-30`;
- `pac97_detect_test.go` AC-4 (`:210-228`) and AC-14 (`:540-555`) in
  `backend/auth`;
- `backend/api/pac97_detect_test.go:207-215`;
- `pac35_auth_flows_test.go:890-896`;
- `AuthDialog.test.tsx:115-121`;
- `AuthDialog.pac83.test.tsx:140-155`;
- `api.pac83.test.ts:29`;
- `AuthDialog.pac97.test.tsx:245-254` (setup only, AC-15);
- `e2e/tests/pac97-signin-detect.spec.ts:160-168` (AC-20: on the
  Google-only stack a microsoft answer no longer navigates; PAC-99 re-pins).

## 7. Deploy order, old-web compatibility and rollback

No migration and no stored data: no password was ever stored (§2.3).
Reverting is a code revert in each repo.

The enum is unchanged (§9 D-4), so each half is safe alone:

Web builds: **W0** before PAC-97 (`< aea164c`), **W1** PAC-97 (`aea164c`,
today's main), **W2** this ticket. Backends: **B1** today (`68540f6`),
**B2** this ticket. "Google off" is a deployment without Google credentials.

| Deployed | gmail.com, Google on | gmail.com, Google off | yahoo.com / outlook.com | No-domain address (API clients only) | Company domains |
|---|---|---|---|---|---|
| B1 + W1 (today) | generic → password dead end | password dead end | password dead end | generic → password dead end | PAC-97 |
| **B2 + W1** | google → Google with hint (W1 navigates on google, `AuthDialog.tsx:154-157`) | google → W1 navigates to the Google start, which redirects to Google's error page (§2.4): a different dead end, now off Paceday | generic → password dead end, as today | choose → PAC-97 choose step | unchanged |
| **B2 + W0** | google → Google with hint | Google's error page | generic → password dead end | choose → password dead end (W0 shows it for everything but google, microsoft and sso with a path) | W0 behaviour, unchanged by B2 |
| **B1 + W2** | generic → personal-address step → Google with hint | personal-address step, "not available" state | personal-address step | generic → personal-address step (W2's validation never sends one) | PAC-97, plus D-6 |
| B2 + W2 | Google directly | personal-address step, "not available" state (D-6) | personal-address step | choose step | PAC-97, plus D-6 |

The one cell worse than today is **B2 + W1 (or W0) with Google off**: the
gmail.com user leaves Paceday for Google's error page instead of staying on
the password form. Both are dead ends; the new one shows no Paceday text. A
Google-off deployment is a self-hosted or test configuration, not
production (production offers Google).

- **Order**: none is required for production (Google on). The orchestrator
  may merge and deploy either PR first; on a Google-on deployment neither
  intermediate state is worse than today. Deploying the web first avoids the
  Google-off cell above and is preferred when it costs nothing. A stale tab
  that loaded the old web keeps today's behaviour until it reloads.
- **Meaning of `generic` across builds**: B2 sends `generic` only for a
  personal address other than gmail.com/googlemail.com. W0 and W1 read it as
  "password step", W2 as "personal-address step". No build reads it as a
  provider, so no build navigates anywhere on it.
- **Old web older than PAC-97** (`< aea164c`): it would show the password
  step for `choose` (PAC-97 §7). This ticket answers choose for one more
  case only, an address without a domain, and the web's own validation never
  sends one. No new exposure.
- **Rollback**: either repo, either order. Rolling back only the backend sends
  gmail.com back to the personal-address step on the new web, which still
  reaches Google. Rolling back only the web brings back the password dead end
  for personal addresses other than gmail.com.

## 8. Open questions

- **OQ-1 (owner) — the issue's question: password sign-in, or provider-only?**
  Factory default: **provider-only** (this spec). Reasons:
  - no password account exists to keep (§2.3);
  - PAC-71 binds every account to a provider identity, and a password identity
    would need its own evidence and binding rules;
  - a password system adds work the provider path avoids: credential storage,
    reset email, verification and brute-force protection.

  If the owner chooses password sign-in, this spec is withdrawn and a new,
  larger ticket is filed. Meanwhile the dead end stays.
- **OQ-2 (owner) — personal Microsoft addresses until PAC-77.** Factory
  default: do not send outlook.com, hotmail.com, live.com and msn.com to
  Microsoft. Say "not yet" on the personal-address step and offer Google.
  This follows the owner's 2026-11-01 acceptance of no personal Microsoft
  sign-in until PAC-77. The alternative: send them to Microsoft now, where
  they are refused with a notice that names no cause (§2.4). Is the factory
  default right, or should PAC-77 be prioritised so the shortcut can go to
  Microsoft at once?
- **OQ-3 (owner) — regional and custom-domain Microsoft consumer addresses**
  (§5). Factory default: unchanged, so the PAC-97 company choice applies. A
  follow-up could add the regional domains (outlook.fr, hotmail.co.uk, …) to
  the consumer list and treat `*.olc.protection.outlook.com` MX as personal.
  That also changes organisation matching for those domains. File it?
- **OQ-4 (owner, low) — "Work email" label and `you@company.com`
  placeholder.** Factory default: unchanged (§5). Personal addresses are now
  supported, so a neutral label ("Email") may read better. It is a copy
  change with an e2e selector to update.
- **OQ-5 (orchestrator) — deploy order**: none required (§7). Record which
  PR deployed first in the release notes.

## 9. Decisions

Each is **factory-assumed** unless marked **owner**.

- **D-1 No password sign-in.** **FACTORY-ASSUMED; THE OWNER MAY CHOOSE TO
  BUILD PASSWORD SIGN-IN INSTEAD** (banner, OQ-1). The password step and its
  calls are removed from the web, not hidden. No server work, since nothing
  exists to remove (§2.3).
- **D-2 Google shortcut.** gmail.com and googlemail.com are answered google
  by the server. PAC-71 already treats a verified address on these two
  domains as authoritative (`handlers_pac71_auth.go:81-95`), so the shortcut
  leads to a sign-in that works. Decided on the server, so the old web
  benefits too (§7).
- **D-3 Microsoft consumer shortcut is gated on PAC-77.** outlook.com,
  hotmail.com, live.com and msn.com would go to Microsoft, but personal
  Microsoft sign-in is refused (`handlers_pac71_auth.go:252-256`; owner
  2026-11-01, "PAC-77 restores it later"). Until PAC-77 they get the
  personal-address answer and the "not yet" sentence. PAC-77 flips them
  (§5 hand-off).
- **D-4 `generic` stays and changes meaning; no new value, no removal.** The
  value now means "a personal (consumer) address that is not sent straight
  to a provider: the client offers the providers that accept personal
  accounts". It no longer means "show the password form". An address with no
  domain moves from generic to choose, because it is not a personal address.
  Rejected alternatives:
  - *Remove `generic`* and answer `choose` for personal addresses. The
    PAC-97 web would then show both providers, including Microsoft, which
    can only refuse a personal account. The web also needs to tell a
    personal address from a company one (§3.3, AC-15).
  - *A new value* (for example `personal`). Same result on the deployed
    web, which treats unknown values as `choose`. It also forces web-first
    deployment and a new enum member, for no behaviour that `generic`
    cannot carry.

  Keeping the value means any deploy order works (§7), and an old web
  degrades only to today's behaviour. The contract stage may rename the
  value's description but not the value.
- **D-5 Personal-address step.** It is a separate step from the company
  choice. The two differ in copy (personal, not "your company"), in
  Microsoft (absent until PAC-77) and in the unavailable state (whether
  Google is offered). The step is built on the PAC-97 choose step's parts:
  hint, back link, availability states, focus on the heading, and the PAC-83
  failure view. The plan decides whether it is the same component with a
  variant.
  The "not yet" sentence appears for every personal address (§3.3), since
  a personal Microsoft account may use any address as its sign-in name; it
  names no date, because PAC-77 is in Backlog.
- **D-6 Strong answer for an unavailable provider.** Once availability has
  loaded and says the answered provider is not offered, the dialog shows the
  matching provider step instead of navigating to a start that answers 503
  (Microsoft, §2.4) or ends on Google's error page (Google, §2.4). This
  applies to company domains too, a small widening that removes the same
  dead end there; its cost is one e2e journey re-pinned until PAC-99
  (AC-20). While availability is
  loading or failed, the dialog navigates as today, so a slow availability
  call never blocks sign-in.
- **D-7 Failure-return step.** The step offered after a failed or refused
  return depends on the kind of address remembered (AC-15). The kind is kept
  next to the email under PAC-97 D-10's storage rules (`sessionStorage`
  only, cleared on sign-in and sign-out), so a return costs no request; an
  email remembered without a kind costs exactly one detection request
  (AC-15). The web holds no copy of the consumer list.
- **D-8 Guardrail for Lovable.** Lovable's agent reads `AGENTS.md`, not the
  factory's specs (`docs/factory/README.md` §3), so the "no password form"
  rule goes there and in its prompt-library mirror, and the gate checks it
  (AC-17). The prose is guidance; the guard is AC-13's check inside the web
  gate, which fails on a password route or a password input on the sign-in
  surfaces whoever adds it.
- **D-9 PAC-71 / PAC-77 interplay.** None at the callback: the Google and
  Microsoft callbacks, evidence rules and the personal-Microsoft refusal are
  unchanged. Detection only picks which start to offer. The PAC-77 hand-off
  is in §5.

## Challenge — 2026-11-13

**Verdict**: accept-with-changes (changes applied in the body above by the
factory; the owner questions OQ-1..OQ-4 stay open and the factory defaults
apply). Verified against `clockwise-like` `origin/main` `68540f6` and
`smart-calendar-flow` `origin/main` `ae72939`, read with `git show`.

### Blocking (resolved in the body)

1. **D-6 breaks a PAC-97 e2e journey that §6 declared protected.** The e2e
   stack is Google-only, so once availability has loaded the Exchange Online
   journey (`e2e/tests/pac97-signin-detect.spec.ts:160-168`) would stop at the
   choose step instead of reaching `/api/auth/microsoft`. Resolved: the
   journey is listed as changing on purpose and replaced by AC-20 (choose
   step, Google only, no navigation; PAC-99 re-pins), and the §6 row is
   narrowed to the Google-MX and gateway journeys.
2. **AC-18 was not falsifiable as written.** The DNS stand-in
   (`e2e/fake-google/dns.go`) keeps no query log, so "the stand-in records no
   query for gmail.com" has no observable. Resolved: the clause is removed;
   AC-1 proves zero queries with the fake resolver.
3. **AC-15 contradicted §6.** D-7 allowed re-running detection on a failure
   return, while §6 kept `AuthDialog.pac97.test.tsx:245-254`, which asserts
   zero detection requests. Resolved: AC-15 now fixes the request count
   (zero when the kind is remembered with the email, exactly one when only
   the email is), D-7 says the kind is remembered, and the PAC-97 test is
   listed as changing in setup only.
4. **AC-13's static check was easy to step around.** It matched only the
   literal `/api/auth/login` and `/api/auth/register`, and not the password
   input itself. Resolved: it now matches `auth/login` / `auth/register` with
   any prefix, plus a password input or `current-password` / `new-password`
   autocomplete under the sign-in surfaces (the API-key fields elsewhere
   stay), runs inside `make verify`, and carries a self-test. The residual gap
   (a route composed at run time) is named and covered by AC-12.
5. **The organisation skip had no pinning test.** §6 deferred this to the
   implementer ("if none pins the list, that is a finding"). None does:
   `domain_test.go:27-38` checks five entries, and no test calls
   `AssociateUserWithOrg` with a consumer address. Resolved: AC-21.

### Non-blocking

1. **The `generic` change and older web builds.** The deploy table now covers
   W0 (pre-PAC-97), W1 and W2 against both backends. One cell is worse than
   today: new backend + old web on a **Google-off** deployment sends a
   gmail.com user to the Google start, which has no configured check
   (`handlers_auth.go:26-41`) and ends on Google's error page instead of
   the password form. Both are dead ends, and production offers Google. So
   no order is required, and web-first is preferred when it costs nothing.
2. **gmail.com → google under PAC-71 and organisations: correct.** Detection
   picks only the start, and the top Google button already reaches the same
   start. PAC-71 treats a verified gmail.com/googlemail.com address as
   authoritative (`handlers_pac71_auth.go:81-95`), and `AssociateUserWithOrg`
   still skips the whole consumer list (`orgs.go:78-89`), so no gmail.com user
   joins an organisation. The list itself is unchanged. AC-5 keeps a gmail.com
   SSO row from winning over the consumer rule.
3. **Gating the Microsoft consumer shortcut on PAC-77 is right.** Microsoft
   ends in a refusal (`handlers_pac71_auth.go:252-256`) that names no cause.
   The "not yet" sentence belongs on the step **before** PAC-77 for every
   personal address, not only the four Microsoft domains, because a personal
   Microsoft account can use any address as its sign-in name. It must name no
   date. Recorded in §3.3 and D-5. The §5 hand-off must also be visible on
   PAC-77 itself (orchestrator).
4. **D-6 widening for company domains.** A microsoft answer with Microsoft
   unavailable now gets the PAC-97 choose step, which may offer Google to an
   Exchange company. That is what PAC-97 already does for gateway domains, so
   it adds no new behaviour class. A google answer with Google off no longer
   leaves for Google's error page.
5. **Lovable guardrail.** `AGENTS.md` already forbade inventing endpoints and
   the password step exists anyway, so prose is not the guard. The gate is
   (`docs/factory/README.md` §3). AC-17 now matches a stable marker, and §3.8
   and D-8 say the gate, not the text, is what fails.
6. AC-3 needs the consumer list enumerable from the test. The body now says
   the test reads it from the `domain` package, never from a copy.

### Criteria I could not falsify

- None after the changes. Before them: AC-18's DNS clause (no observable),
  and AC-15's "if the step needs a fresh detection" (no fixed request count).

### Citations I checked and found wrong

- `GenericStep` at `AuthDialog.tsx:493-659`: it starts at `:495`.
- "`docs/lovable-prompts/00-global-context.md`, the mirror of `AGENTS.md`":
  it has its own parallel boundary list and is not a mirror.
- D-6 "a start that answers 503 (§2.4)": only the Microsoft start does. The
  Google start redirects with an empty client id (`handlers_auth.go:26-41`).
  Added to §2.4.
- `pac97-signin-detect.spec.ts:147-159` for the PAC-99 note: the fixme is at
  `:146-158`.
- Everything else checked holds: `sso_detect.go:20-36,165-189,349-354`,
  `domain.go:5-26`, `orgs.go:80`, `signin.go:146-153,277`,
  `handlers_pac71_auth.go:81-95,252-256`, `ms_idtoken.go:34`,
  `microsoft_oauth.go:20-22,66-69`, `handlers_microsoft_auth.go:16-21`,
  `auth.yaml:833-885,2697-2740`, the five backend tests pinning `generic`,
  and `AuthDialog.tsx:91-93,134-187,431-437,466-488,516-546`.

### Consumers this breaks

- `clockwise-like/e2e/tests/pac97-signin-detect.spec.ts:160-168`: no longer
  navigates on the Google-only stack (AC-20).
- `smart-calendar-flow/src/components/auth/AuthDialog.pac97.test.tsx:245-254`:
  setup only (AC-15).
- The backend and web tests listed in §6 as changing on purpose.
- No other consumer of `/api/auth/detect` or `generic` exists. The `generic`
  hits in `backend/storage/scheduling_links.go` and the web's
  `schedulingLinks.ts` are PAC-72's refusal-message setting, which is
  unrelated. `mcp/` has no reference.
