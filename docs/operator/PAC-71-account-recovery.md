# Operator procedure: accounts refused at sign-in (PAC-71)

Since PAC-71 an account belongs to the identity bound to it (Google account id; Microsoft
tenant + object id; SSO issuer + subject), never to an address. Some rightful users are
therefore refused at sign-in with "Paceday could not sign you in with this account" until
an operator acts. This page is the interim procedure (spec `docs/specs/PAC-71.md` §3
"Operator procedure"), until follow-up F2 gives users a product path.

The steps run on the server with `backend/cmd/paceday-operator` (`DATABASE_URL` set). They
have no API route, web page or MCP tool. Every run writes an `operator_audit` row (operator,
time, account, previous and new identity, evidence, what was removed, reason); a rollback of
migration 032 keeps that table.

## 1. Find the account

Every refusal is logged by the API with the prefix `pac71:`. The line names the cause, the
account concerned (when an existing account is involved), the incoming identity
(`google:<id>` or `microsoft:<tenant>:<oid>`) and, for rule (b), the first non-empty kind.
Rule (a) bindings made on mailbox evidence for a non-consumer domain are logged as
`pac71: REVIEW` for the same reason.

## 2. Verify the person outside Paceday

For example through the domain's administrator. Never on the word of the person alone.

## 3a. Free the address (squatter, recycled mailbox, refused legacy account, case collision)

```
paceday-operator free-address -account <uuid> -operator <you> -reason "<why>" [-squatter]
```

The account is not deleted. Its address becomes `freed+<uuid>@paceday.invalid`, its standing
unconfirmed, its sessions and connects in flight end. With `-squatter` every credential,
connection and key on it is removed too. Freeing an address removes that account from every
address-based grant it has not yet accepted. Then ask the person to sign in again: they are
created a new account (rule 5) or bind the legacy account (rule (b)).

## 3b. Unbind and reassign (the data is the person's, the binding is someone else's)

```
paceday-operator reassign -account <uuid> -identity google:<id>|microsoft:<tid>:<oid> \
  -evidence authoritative|mailbox -operator <you> -reason "<why>" \
  [-keep-calendar-credentials] [-remove slack,notion,zoom,ai-keys,external]
```

Take the identity and the evidence level from the refusal log line of the person's own
sign-in. By default every calendar credential (Google, Microsoft, personal calendars) is
removed, because one issued to the previous identity would keep receiving the person's
meetings; keep them only if you confirmed they were issued to the new identity. Remove the
other connections and keys the person says are not theirs. Every session of the account and
every connect in flight ends. The person then signs in and reaches the account; the previous
identity is refused from then on.

## Deploy-time step (owner decision OQ-6)

```
paceday-operator advance-all -operator <you> -reason "PAC-71 deploy"
```

Ends every existing session once (every user signs in again). Run it only if the owner
answered OQ-6 "yes", after both the API and the web app are deployed.
