# PAC-71: provider verification (merge condition 2)

**Status: OPEN.** No agent has called a provider for this check: agents have no development tenant and no test account, and cannot give consent. This page is the procedure for the owner, or someone the owner names, to close merge condition 2 of `contracts/features/PAC-71.yaml` (`mergeConditions[1]`), against contract revision 3.1.

## Rules

- Use **development app registrations, dev tenants and test accounts only**. Never use production users or the production clients.
- **Sign-in scopes** are exactly what `TestPAC50_SignInAuthorizeURLsCarryNoNewScope` pins:
  - Microsoft: `openid profile Calendars.ReadWrite User.Read offline_access`, with a `nonce`.
  - Google: unchanged, `https://www.googleapis.com/auth/calendar https://www.googleapis.com/auth/userinfo.email` (no `openid`).
- **Never log or paste a token or a client secret.** Decode tokens locally only (see "Reading the tokens"). Record only the claims and fields listed below, and redact names and addresses that the comparison does not need.
- **Stop if any answer differs from its expectation.** The contract is revised first; the code must not work around a provider answer.

## The five items

| # | Compare | Expected | If not |
|---|---|---|---|
| (i) | The Microsoft **sign-in** ID token's `tid`, and Graph `GET /v1.0/organization?$select=id,verifiedDomains` with the same sign-in access token. Do this for one **work** account and one **personal** account (outlook.com or hotmail.com). | **Work account:**<br>- `tid` is a GUID, the tenant.<br>- `/organization` answers 200 with one entry whose `id == tid` (case-insensitive), and `verifiedDomains[].name` lists the tenant's domains.<br><br>**Personal account:**<br>- `tid` is `9188040d-6c67-4c5b-b112-36a304b66dad`.<br>- `/organization` is not a usable 200; record what it answers. | `/organization` is **evidence only** (AC-51), so a failure there refuses nobody. It only downgrades the address standing.<br><br>A personal token with a different `tid` is a **blocker**: personal accounts would be classified as work accounts. |
| (ii) | Google `GET https://www.googleapis.com/oauth2/v2/userinfo` with a **Workspace** account's sign-in token. | Fields `id` (decimal string), `email`, `verified_email: true`, and `hd` (the Workspace primary domain). | Without `hd`, Workspace users get mailbox evidence: new accounts are created unconfirmed (the safe direction). Release-note it. |
| (iii) | For the **same** Workspace account: the Google **step-up** ID token's `sub`, and the (ii) userinfo `id`. | `sub == id`, byte for byte. | **Hard.** Every bound Google administrator would get `identity_mismatch` at step-up (`stepupIdentityMatches`). |
| (iv) | For the **same** Microsoft work account: the **sign-in** ID token's `oid`, the **step-up** ID token's `oid`, and Graph `GET /v1.0/me?$select=id`. | All three are equal as GUIDs (case-insensitive). | **Hard.**<br>- If the sign-in `oid` differs from `/me` `id`, every Microsoft sign-in is refused (the callback's cross-check).<br>- If the step-up `oid` differs, every bound Microsoft administrator gets `identity_mismatch`. |
| (v) | Whether an **unmanaged ("viral") Entra directory** can list a domain in `verifiedDomains`, and whether anything in today's answers tells it apart. | Microsoft's current documentation, or one recorded answer from a test tenant. | Spec OQ-3 item 4. Until it is answered, tenant evidence counts as authoritative; this is a release-noted residual. |

## Steps

### 1. Dev app registrations

- **Microsoft (Entra), one multi-tenant app** ("accounts in any organizational directory and personal Microsoft accounts"):
  - Redirect URIs: `http://localhost:8080/api/auth/microsoft/callback` (sign-in) and `http://localhost:8080/api/admin/sso/stepup/callback` (step-up).
  - Delegated permissions: `openid`, `profile`, `User.Read`, `Calendars.ReadWrite`, `offline_access`.
  - Step-up only: add the directory-read permission that `auth/stepup.go` `NewMicrosoftStepup` requests.
- **Google, one OAuth client** (Web application, publishing status "Testing", Workspace test user added):
  - Redirect URIs: `http://localhost:8080/api/auth/callback` and `http://localhost:8080/api/admin/sso/stepup/callback`.
  - Scopes: `calendar` and `userinfo.email` (sign-in), plus the step-up's identity and Directory read-only scopes.

### 2. Run the backend locally against those clients

Set these in `.env`:
- `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL`
- `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET`, `MICROSOFT_REDIRECT_URL`
- `APP_URL=http://localhost:8080`, which the step-up uses.

Leave the `PACEDAY_*_IDP_BASE_URL` stand-ins empty, so the real providers are called.

### 3. Reading the tokens: locally, never logged or pushed

Make a **local branch that is never pushed** and add one temporary line. Remove it when done.

- **Microsoft sign-in:** in `api/handlers_pac71_auth.go` `microsoftCallback`, after `rawID` is read, write the decoded payload only (never `rawID` itself or the access token) to a local file outside the repository:

  ```go
  if p := strings.Split(rawID, "."); len(p) == 3 { b, _ := base64.RawURLEncoding.DecodeString(p[1]); _ = os.WriteFile(os.Getenv("HOME")+"/pac71-idtoken.json", b, 0o600) }
  ```

  Then read `tid` and `oid` from that file. It holds claims, not a credential: the signature is not in it.
- **Graph calls** for (i) and (iv): use Graph Explorer signed in with the same test account. Consent to `User.Read` only, and run:
  - `GET https://graph.microsoft.com/v1.0/organization?$select=id,verifiedDomains`
  - `GET https://graph.microsoft.com/v1.0/me?$select=id`

  Graph Explorer's token is a different client's, but `/me` `id` and `/organization` `id` do not depend on the client.
- **Step-up** for (iii) and (iv):
  1. Sign in to the local stack.
  2. Start `GET /api/admin/sso/stepup?provider=google` (then `provider=microsoft`) and complete it.
  3. Read `sub` or `oid` with the same kind of temporary payload-to-file line in `handlers_sso_stepup.go`, after the step-up ID token is verified.
- **Google userinfo** for (ii): add a temporary line in the Google callback that writes the decoded userinfo JSON (not the token) to a local file. Alternatively, call the endpoint with an access token from the OAuth Playground, using **your own dev client** and the same two scopes.

Afterwards, delete the local branch and the files in your home directory.

### 4. Unmanaged directories (v)

Read Microsoft's current "Admin takeover of an unmanaged directory" and "Self-service sign-up" pages, and record:
1. Whether self-service sign-up can still create an unmanaged directory.
2. Whether its domain appears in `/organization` `verifiedDomains`.
3. Any field that tells it apart (for example `tenantType`, or a domain's `isVerified` or `authenticationType`).

## What to record

Add one block per item under "Recorded answers":
- date, who ran it, the dev tenant or test account (redacted), and the app registration used;
- for (i): `tid`, the `/organization` status, and its `id` and domain names, for each of the two accounts;
- for (ii): the field names and the values of `verified_email` and `hd`; `id` only as needed for (iii);
- for (iii) and (iv): the compared values, or their first and last four characters plus "equal: yes/no";
- for every item: "matches expectation: yes/no".

## Recorded answers

_Not yet recorded._
