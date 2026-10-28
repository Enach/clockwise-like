# PAC-71 — provider verification (merge condition 2): what to run and record

**Status: OPEN.** No provider endpoint was called for this document or for the implementation
PR. An agent has no development tenant, no Workspace test account and no consent to give. This
page is the procedure for the owner (or someone the owner names) to close contract merge
condition 2 (`contracts/features/PAC-71.yaml`, `mergeConditions[1]`).

Rules for the run:

- Use **development tenants and test accounts only**, never production users.
- Use **exactly today's sign-in scopes** for items (i) and (ii): Google `calendar` +
  `userinfo.email` (no `openid`); Microsoft `Calendars.ReadWrite` + `User.Read` +
  `offline_access` (no `openid`). `TestPAC50_SignInAuthorizeURLsCarryNoNewScope` pins them.
- Record each answer **redacted**: keep field names, types and the values compared; replace
  tokens, names and addresses that are not needed with `<redacted>`. Paste the redacted answers
  into this file under "Recorded answers" with the date and who ran them.
- If any answer differs from the expectation below, **stop**: contract §3 says the contract is
  revised before stage 4, and the implementation must not work around it.

## The five items

| # | What | Expected (contract §3) | Why it matters in the code |
|---|---|---|---|
| (i) | One Graph `GET https://graph.microsoft.com/v1.0/organization?$select=id,verifiedDomains` with a **work** account's sign-in access token, and one with a **personal** (outlook.com/hotmail) account's | Work: `200`, `value` holds exactly one entry, `id` is a GUID (the tenant), `verifiedDomains[].name` lists the tenant's domains. Personal: not a usable 200 (an error status or an empty `value`) | `handlers_microsoft_auth.go` `classifyMicrosoftWork`: a work account needs exactly that shape; anything else is the refusal `/?error=signin_refused` (spec AC-5, AC-11b, AC-11c). If the work call fails under `User.Read` alone, **every** Microsoft sign-in is refused. |
| (ii) | One Google `GET https://www.googleapis.com/oauth2/v2/userinfo` with a **Workspace** account's sign-in token (scopes as above) | Fields `id` (decimal string), `email`, `verified_email` (boolean `true`), `hd` (the Workspace primary domain) | `handlers_auth.go` `googleEvidence`: `verified_email` + `hd` decide AUTHORITATIVE. If `hd` is absent under `userinfo.email` alone, Workspace users get MAILBOX evidence: new accounts are created *unconfirmed* and rule (b) refuses (safe direction, release note). |
| (iii) | For the **same** Workspace account as (ii): the PAC-50 step-up ID token's `sub` (`/api/admin/sso/stepup/start?provider=google`, decode the verified ID token) and the v2 userinfo `id` | `sub == id`, byte for byte | Step-up check 4b (`handlers_sso_stepup.go`, `stepupIdentityMatches`) compares the bound Google subject (userinfo `id`, recorded at sign-in) with the step-up `sub`. If they differ, every bound Google administrator gets `identity_mismatch` and cannot change SSO configuration except through break-glass. **Hard.** |
| (iv) | For the **same** Microsoft work account as (i): the step-up ID token's `oid` and Graph `GET /v1.0/me?$select=id` | `oid == /me id`, compared as GUIDs (case-insensitive) | Same check 4b for Microsoft: the bound object id is `/me` `id`. **Hard.** |
| (v) | Whether an **unmanaged ("viral") Entra directory** can list a domain in `verifiedDomains`, and whether today's answers can tell it apart | Documentation or a recorded answer | Spec OQ-3 item 4. Until answered, Microsoft tenant evidence is treated as authoritative (release-noted residual). If such a directory can list the domain and cannot be told apart, it becomes the owner question the spec describes. |

## How to run each item

A throw-away script outside the repository is enough; nothing here needs Paceday running except
(iii) and (iv), which use Paceday's own step-up so that the token is exactly what check 4b sees.

### (i) Microsoft `/organization`

1. Register (or reuse) a **development** multi-tenant app with redirect URI
   `http://localhost:8080/api/auth/microsoft/callback`, delegated permissions
   `Calendars.ReadWrite`, `User.Read`, `offline_access` only.
2. Run the local stack with that client (`MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET`,
   `MICROSOFT_REDIRECT_URL`) and a debugger or a temporary log line printing the access token in
   `microsoftCallback` **on a local branch that is never pushed**; or use the Graph Explorer
   signed in with the same test account, restricting consent to `User.Read`.
3. `curl -s -H "Authorization: Bearer $AT" 'https://graph.microsoft.com/v1.0/organization?$select=id,verifiedDomains'`
   for a work account and for a personal account. Record status code and redacted body.

### (ii) Google userinfo

1. Development OAuth client, scopes `https://www.googleapis.com/auth/calendar` and
   `https://www.googleapis.com/auth/userinfo.email` only.
2. Obtain an access token for a **Workspace** test account (OAuth Playground with your own
   client, or the local stack as in (i)).
3. `curl -s -H "Authorization: Bearer $AT" https://www.googleapis.com/oauth2/v2/userinfo`.
   Record the field names and `verified_email`, `hd`, and `id`.

### (iii) Google `sub` vs userinfo `id`

1. Same Workspace account as (ii), with a Paceday organisation for its domain and PAC-50's
   Google step-up configured (`SSO_STEPUP_GOOGLE_*`).
2. Sign in to the local stack with that account, start the step-up
   (`GET /api/admin/sso/stepup/start?provider=google`) and complete it.
3. Read the verified ID token's `sub` (a temporary log line on an unpushed local branch, or the
   OAuth Playground with `openid` *only for this step-up client*) and compare with (ii)'s `id`.

### (iv) Microsoft `oid` vs `/me` `id`

As (iii) with the Microsoft step-up and the work account of (i): record the ID token's `oid` and
`GET /v1.0/me?$select=id` with the sign-in token; compare case-insensitively.

### (v) Unmanaged directories

Read Microsoft's current documentation on "unmanaged directory" / "admin takeover" /
"self-service sign-up" and record what it says about (1) whether self-service (email-verified)
sign-up can still create an unmanaged directory, (2) whether a domain in such a directory
appears in `verifiedDomains` of `/organization`, and (3) any field that distinguishes it
(for example `tenantType`, or `isVerified`/`authenticationType` on the domain). If a test
tenant is available, record one answer.

## What the documentation says (from memory, not re-read for this page)

Confidence labels: **high** = I am confident the documentation states this; **medium** = I
believe it is documented but the details may have changed; **low** = I am not sure it is
documented at all. None of these replaces the recorded answers above.

| Claim | Source I recall | Confidence |
|---|---|---|
| `GET /organization` lists the signed-in user's organization; least-privileged delegated permission `User.Read`; under `User.Read` only a restricted set of properties is readable, including `id`, `displayName`, `verifiedDomains` | Microsoft Graph API reference, "Get organization" / "List organization", Permissions table | medium |
| Personal Microsoft accounts are not supported for `/organization` | Same page's permissions table ("Delegated (personal Microsoft account): Not supported") | medium |
| Graph `/me` `id` is the directory object id of a work/school user; the ID token `oid` is "the immutable identifier for the requestor … the object ID of the user", the same across apps in a tenant; `sub` differs per app | Microsoft identity platform, "ID token claims reference" (`oid`, `sub`, `tid`) | high for `oid`/`sub` semantics; medium that `/me id == oid` is stated in so many words |
| Access tokens for Graph are opaque to clients; do not parse them | Microsoft identity platform, "Access tokens" | high |
| Google v2 userinfo returns `id`, `email`, `verified_email`, `picture`, and `hd` for Workspace accounts | Google OAuth 2.0 / userinfo (v2 `oauth2.userinfo.get`) reference | medium (`hd` under `userinfo.email` alone is the open part) |
| Google's OpenID `sub` equals the v2 userinfo `id` for the same account | Google Identity "OpenID Connect" docs (the `sub` claim is the Google account's unique id, "same as" the legacy userinfo id) | medium |
| `hd` is the Workspace primary domain only; secondary/alias domains do not appear | Google Identity "OpenID Connect", `hd` claim | medium |
| Entra "unmanaged directories" are created by self-service sign-up with email verification; Microsoft has restricted creating them, and an admin can take over the domain; whether the domain shows in `verifiedDomains` for the unmanaged tenant is not something I can state | Entra ID docs, "Admin takeover of an unmanaged directory", "Self-service sign-up" | low |

## Recorded answers

_Not yet recorded. Add one block per item: date, who ran it, environment (dev tenant / test
account), request, redacted response, and "matches expectation: yes/no"._
