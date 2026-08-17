# Paceday

An AI-powered calendar management app that protects your focus time, compresses meetings, and lets you control your schedule with natural language.

## Setup

1. Copy `.env.example` to `.env` and fill in your Google OAuth credentials.
2. Start all services:

```bash
docker compose up --build
```

3. Visit [http://localhost](http://localhost) to access the app.
4. The API health check is at [http://localhost/api/health](http://localhost/api/health).

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

Start the backend first (`cd backend && go run .` from this repo) and configure the frontend API base URL so `/api` calls reach it.

### Building the production frontend image locally

`docker-compose.yml` defaults to the published image `enach/paceday-frontend:latest`. To build from a local clone instead:

```bash
git clone git@github.com:Enach/smart-calendar-flow.git ../smart-calendar-flow
docker compose -f docker-compose.yml -f docker-compose.dev.yml build frontend
docker compose -f docker-compose.yml -f docker-compose.dev.yml up
```

## MCP Server (Claude Desktop / Cursor)

Paceday exposes 16 calendar tools via the [Model Context Protocol](https://modelcontextprotocol.io).

**SSE mode** (running via docker-compose, port 3001):
```json
{
  "mcpServers": {
    "paceday": {
      "url": "http://localhost:3001/sse"
    }
  }
}
```

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
