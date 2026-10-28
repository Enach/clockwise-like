# Running Paceday behind a TLS-terminating proxy

Paceday's production stack (`docker-compose.prod.yml`) speaks **plain HTTP
only** and never redirects to HTTPS. TLS, HSTS and the public certificate are
the job of a proxy you run in front of it (Caddy, Traefik, an nginx of your
own, a cloud load balancer). This page lists what that proxy must do. It is
part of PAC-81 (spec `NEW-deploy-hardening`, AC-D2).

Passages marked **added by Part A/B/C** describe PAC-81 work that is not on
`main` yet; until it ships, the "today" sentence next to them applies.

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
- The other browser security headers (CSP, `X-Frame-Options`,
  `Referrer-Policy`, `nosniff`) are set by Paceday's nginx — **added by
  Part B** (AC-B10). Today nginx sets none; do not add a CSP at the proxy, it
  would conflict with Part B's.

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

- **Added by Part B** (AC-B5..B7): an nginx setting holding the trusted proxy
  addresses (empty by default: nothing is trusted). nginx then takes the
  right-most address in `X-Forwarded-For` that is not in the list, and passes
  it to the backend as `X-Real-IP`.
- **Today**, nginx sends `X-Real-IP: $remote_addr`, i.e. your proxy's address.
  Every visitor therefore shares one rate-limit bucket (the public booking
  limits count per client). This is safe (nothing can be spoofed) but means
  one busy visitor can exhaust the limit for everyone until Part B ships.

What to put in the list:

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

- The 90/120/130 s values are **added by Part A and Part B**. Today the backend
  has no timeouts and nginx uses its 60 s default; a proxy timeout above 130 s
  is correct both today and afterwards.
- Example: an AWS Application Load Balancer's default idle timeout is 60 s —
  too short; raise it to at least 135 s. Cloudflare's proxied-request timeout
  (100 s on most plans) is also too short for the longest legitimate requests.
- Keep-alive to nginx: any value is fine; Paceday's backend keeps idle
  connections 120 s (added by Part A).

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

- **Readiness** — `GET /api/ready` style endpoint through nginx, answering
  503 while the backend is starting (migrations) or shutting down: **added by
  Part A and Part B** (AC-A11, AC-B11). Once present, use it: during a deploy
  it turns 503 a few seconds before the backend stops, so the proxy stops
  sending traffic first.
- **Today**, use `GET /api/health` through nginx: it answers `200
  {"status":"ok",…}` whenever the backend process is up (liveness only; it does
  not check the database). Interval 5–10 s, 2–3 failures to mark down.

## Checklist

1. TLS at the proxy; HSTS `max-age=31536000; includeSubDomains` (no `preload`
   unless chosen); HTTP→HTTPS redirect at the proxy.
2. `X-Forwarded-For` appended and `X-Forwarded-Proto: https` sent.
3. nginx's trusted list = your proxy's address — the bridge gateway when the
   proxy is on the same host (setting added by Part B).
4. Proxy idle/read timeout > 130 s.
5. nginx's port reachable only from the proxy (`127.0.0.1` bind on the same
   host); MCP's 3001 not exposed.
6. Proxy health check through nginx: readiness (Part A/B), `/api/health` until
   then.
