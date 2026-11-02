# Paceday

An AI-powered calendar management app that protects your focus time, compresses meetings, and lets you control your schedule with natural language.

## Setup

1. Copy `.env.example` to `.env` and fill in your Google OAuth credentials.
2. Generate a development encryption key (PAC-84). The backend refuses to
   start without one, in development exactly as in production; it lives in
   `encryption.env` beside `.env` (git-ignored, never in `.env`):

```bash
echo "PACEDAY_ENCRYPTION_KEY=$(date -u +%Y%m%d):$(openssl rand -base64 32)" > encryption.env && chmod 600 encryption.env
```

3. Start all services:

```bash
docker compose up --build
```

4. Visit [http://localhost](http://localhost) to access the app.
5. The API health check is at [http://localhost/api/health](http://localhost/api/health).

Losing `encryption.env` makes every stored connection unreadable; for a
development database, generate a new key and follow `docs/runbook.md`,
"Encryption key" (lost-key reset), or start from an empty database.

## Stack

- **Backend**: Go 1.25, chi router, PostgreSQL
- **Frontend**: React 18, TypeScript, Vite 5, Tailwind CSS, shadcn/ui
- **MCP Server**: Go, port 3001 — exposes Paceday tools to Claude Desktop, Cursor, Zed
- **Infrastructure**: Docker Compose, Nginx reverse proxy

## Frontend Development

The frontend lives in a **separate repo**: [Enach/smart-calendar-flow](https://github.com/Enach/smart-calendar-flow). See [ADR-0002](docs/adr/0002-frontend-source-of-truth.md) for the rationale. Run it locally alongside this backend repository.

### Local development

Clone the frontend repo as a sibling and run its dev server:

```bash
git clone git@github.com:Enach/smart-calendar-flow.git ../smart-calendar-flow
cd ../smart-calendar-flow
bun install
VITE_BACKEND_URL=http://localhost:8080 bun run dev -- --port 5173
# frontend: http://localhost:5173, /api proxied to http://localhost:8080
```

Start the backend first and configure the frontend API base URL so `/api` calls reach it. Run from this repo, it needs the encryption key of `encryption.env` (Setup, step 2) in its environment:

```bash
cd backend && set -a && . ../encryption.env && set +a && go run .
```

### Building the production frontend image locally

`docker-compose.yml` defaults to the published image `enach/paceday-frontend:latest`. To build from a local clone instead:

```bash
git clone git@github.com:Enach/smart-calendar-flow.git ../smart-calendar-flow
docker compose -f docker-compose.yml -f docker-compose.dev.yml build frontend
docker compose -f docker-compose.yml -f docker-compose.dev.yml up
```

## MCP Server (Claude Desktop / Cursor)

Paceday exposes 16 calendar tools via the [Model Context Protocol](https://modelcontextprotocol.io).

**SSE mode, production** (Docker Engine 25+ and Docker Compose v2, see the
[runbook](docs/runbook.md#requirements)) — opt-in. The production stack runs
no MCP server until you add `docker-compose.mcp.yml` and set `MCP_AUTH_TOKEN` in `.env`
(generate it with `openssl rand -hex 32`; without it compose refuses to start
the stack, naming the variable):

```bash
docker compose -f docker-compose.prod.yml -f docker-compose.mcp.yml up -d
# or put COMPOSE_FILE=docker-compose.prod.yml:docker-compose.mcp.yml in .env
```

The MCP server publishes no port of its own: nginx serves it on your public
origin at `/mcp/`, behind your TLS proxy (PAC-81, owner decision Q-O1). Every
request must carry the token as a bearer header:

```json
{
  "mcpServers": {
    "paceday": {
      "url": "https://app.example.com/mcp/sse",
      "headers": { "Authorization": "Bearer <MCP_AUTH_TOKEN>" }
    }
  }
}
```

Anyone holding the token can call every tool, so treat it like a password.
The proxy in front must let a long-lived event stream through on `/mcp/`
([proxy requirements](docs/operator/proxy.md) §4).

**SSE mode, development** (`docker compose up` with `docker-compose.yml`):
the same configuration with `"url": "http://localhost:3001/sse"` and the
`Authorization` header carrying the `MCP_AUTH_TOKEN` you set.

**stdio mode** (direct binary):
```json
{
  "mcpServers": {
    "paceday": {
      "command": "/path/to/mcp-server",
      "env": { "BACKEND_URL": "http://localhost:8080" }
    }
  }
}
```
