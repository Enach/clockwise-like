# Running Paceday behind a TLS-terminating proxy

Paceday's production stack (`docker-compose.prod.yml`) speaks **plain HTTP
only** and never redirects to HTTPS. TLS, HSTS and the public certificate are
the job of a proxy you run in front of it (Caddy, Traefik, an nginx of your
own, a cloud load balancer). This page lists what that proxy must do. It is
part of PAC-81 (spec `NEW-deploy-hardening`, AC-D2).

PAC-81 Part A (backend timeouts, request budget, graceful shutdown,
readiness) and Part B (the nginx edge: headers, CSP, trusted proxy, timeouts,
`/nginx-health`) have shipped. Passages marked **added by Part C** describe
PAC-81 work that is not on `main` yet; until it ships, the "today" sentence
next to them applies.

```
browser ──HTTPS──▶ your proxy ──HTTP──▶ nginx (compose, :80) ──▶ backend :8080
                                                          └──▶ frontend :80
```

## 1. Terminate TLS and set HSTS

- The proxy holds the certificate and terminates TLS. Paceday mounts no
  certificate and listens on no 443 that matters (the `443:443` mapping and
  `TLS_CERT_DIR` mount in today's compose file are unused; Part C removes
  them, AC-C1).
- The proxy sets **`Strict-Transport-Security: max-age=31536000; includeSubDomains`**
  on every HTTPS response. Do not add `preload` unless you have decided to
  submit the domain to the browsers' preload list: it is hard to undo.
- The proxy redirects HTTP to HTTPS itself. Paceday's session cookies carry
  `Secure` (leave `PACEDAY_INSECURE_COOKIES` empty), so sign-in does not work
  over plain HTTP anyway.
- The other browser security headers (`Content-Security-Policy`,
  `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`,
  `X-Content-Type-Options: nosniff`) are set by Paceday's nginx on every
  response (PAC-81 Part B). Do not set them, or a second CSP, at the proxy: a
  second policy can only narrow the first and breaks the app in ways that
  are hard to trace. nginx never sends HSTS. The CSP's operator settings are
  `NGINX_CSP_SENTRY_ORIGIN` and `NGINX_CSP_CONNECT_ORIGINS` (`.env.example`).

## 2. Forward the client address and scheme

The proxy must send, on every request to nginx:

- `X-Forwarded-For` — **appending** the client address to any value already
  present (Caddy and Traefik do this by default; in nginx,
  `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`).
- `X-Forwarded-Proto: https`.

The backend never reads `X-Forwarded-For` itself. It reads `X-Real-IP`, and
only from the addresses in `TRUSTED_PROXIES` (`.env.example`); in the
production compose file those default to Docker's private ranges, so the
backend believes Paceday's nginx and nothing else.

## 3. Tell nginx which proxy to trust

Paceday's nginx must learn the real client from your proxy's
`X-Forwarded-For`, and believe it only when the request comes from your proxy.

- Set **`NGINX_TRUSTED_PROXIES`** (`.env.example`): the IPs/CIDRs, comma or
  space separated, whose `X-Forwarded-For` and `X-Forwarded-Proto` nginx
  believes. nginx then takes the right-most address in `X-Forwarded-For` that
  is not in the list and passes it to the backend as `X-Real-IP`. An invalid
  entry (or `/0`, which would trust everyone) stops nginx at start.
- **Left empty** (the default), nothing is trusted: every request's client is
  the socket peer, i.e. your proxy. That is safe (nothing can be spoofed), but
  every visitor then shares one rate-limit bucket (the public booking limits
  count per client), so one busy visitor can exhaust the limit for everyone.
  Set it in production.

What to put in it:

| Topology | nginx sees requests coming from | Trust |
|---|---|---|
| Proxy on another host or a cloud load balancer | the proxy's address (or its subnet, for a load balancer with several addresses) | that address/CIDR |
| **Proxy on the same host**, connecting to nginx's published port | the **Docker bridge gateway** of the compose network, not `127.0.0.1` | the gateway address |

For the same-host case, find the gateway with:

```bash
docker network inspect "$(docker compose -f docker-compose.prod.yml ps -q nginx | xargs docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}')" \
  -f '{{(index .IPAM.Config 0).Gateway}}'
```

It is typically `172.18.0.1` or similar, and changes if the network is
recreated with another subnet, so re-check after `docker compose down`.
Trusting `127.0.0.1` does nothing in this topology: Docker's port publishing
makes the peer the gateway.

## 4. Timeouts: longer than nginx's

The proxy's idle and read (response) timeout towards nginx must be
**longer than 130 s**, nginx's upstream read/send timeout. The bounds nest so
that the innermost one fires first and the backend returns its own error:
backend handler budget 90 s < backend write backstop 120 s < nginx 130 s <
your proxy.

- nginx's 130 s upstream read/send timeout (Part B) and the backend's 90 s
  handler budget and 120 s write backstop (Part A) have shipped. A request
  still running at 90 s gets the backend's own
  `503 {"error":"request timed out"}`, well before nginx or your proxy give up.
  The backend also bounds request headers (10 s) and whole requests (30 s).
- Example: an AWS Application Load Balancer's default idle timeout is 60 s —
  too short; raise it to at least 135 s. Cloudflare's proxied-request timeout
  (100 s on most plans) is also too short for the longest legitimate requests.
- Keep-alive to nginx: any value is fine; Paceday's backend keeps idle
  connections 120 s.

The web app aborts its own API calls after 4 s regardless (spec F-W1); these
bounds protect the server, they do not make long calls succeed in the browser.

## 5. Only the proxy may reach nginx

nginx's published port must be reachable **only from your proxy**: anyone who
reaches it directly skips TLS, HSTS and your proxy's own protections.

- Proxy on the same host: bind nginx to loopback, `127.0.0.1:80:80`. The
  setting that makes the bind address configurable is **added by Part C**
  (AC-C9); today edit the `ports:` line of `docker-compose.prod.yml`
  (`"80:80"` → `"127.0.0.1:80:80"`) or block port 80 in the host firewall.
  Remember (§3) that nginx then still sees the bridge gateway as the peer, not
  `127.0.0.1`.
- Proxy on another host: firewall port 80 to the proxy's address only.
- The MCP server: today the production compose file publishes it on port
  3001 of every interface. Firewall 3001, or remove the `mcp-server` service.
  Making it opt-in and loopback-only is **added by Part C** (AC-C4, owner
  default Q-O1); it is never meant to be exposed through the proxy.
- Postgres and the backend publish no port; keep it that way.

## 6. Health checks on the proxy

Configure the proxy's (or load balancer's) health check **through nginx**, so
it checks the whole path:

- **Backend readiness** — `GET /api/ready` (PAC-81 Part A, AC-A11):
  `200 {"status":"ready"}` when the database answers a ping within 2 s, else
  `503 {"error":"not ready"}` within 3 s. It also answers 503 for the first 5 s
  after SIGTERM while the backend is still serving. Concurrent checks share
  one database ping, and you may restrict `/api/ready` to your health
  checker's address at your proxy (docs/security.md, B8). Check it through nginx, so that
  the check covers the whole path: during a deploy it turns 503 a few seconds
  before the backend stops accepting connections, so the proxy stops sending
  traffic first. This is the check that should take the stack out of rotation.
- **Edge liveness** — `GET /nginx-health` (PAC-81 Part B, shipped). nginx
  answers 200 itself, without calling any upstream and without logging, so it
  says only that nginx is up. Use it for the proxy's check of the nginx
  container (or for Part C's container health check), not as the stack's
  readiness.
- **Backend liveness** — `GET /api/health` answers `200 {"status":"ok",…}`
  whenever the backend process is up, without checking the database. Do not
  use it to take the stack out of rotation; use `/api/ready`. Interval 5–10 s,
  2–3 failures to mark down.

## Checklist

1. TLS at the proxy; HSTS `max-age=31536000; includeSubDomains` (no `preload`
   unless chosen); HTTP→HTTPS redirect at the proxy.
2. `X-Forwarded-For` appended and `X-Forwarded-Proto: https` sent.
3. `NGINX_TRUSTED_PROXIES` = your proxy's address — the bridge gateway when
   the proxy is on the same host.
4. Proxy idle/read timeout > 130 s.
5. nginx's port reachable only from the proxy (`127.0.0.1` bind on the same
   host); MCP's 3001 not exposed.
6. Proxy health check through nginx: `GET /api/ready` to take the stack out
   of rotation, and `/nginx-health` for nginx itself.
