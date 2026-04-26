# Clockwise-like

A Clockwise-inspired calendar management app with focus time scheduling and meeting compression.

## Setup

1. Copy `.env.example` to `.env` and fill in your Google OAuth credentials.
2. Start all services:

```bash
docker compose up --build
```

3. Visit [http://localhost](http://localhost) to access the app.
4. The API health check is at [http://localhost/api/health](http://localhost/api/health).

## Stack

- **Backend**: Go 1.25, chi router, SQLite
- **Frontend**: React 18, TypeScript, Vite 5, Tailwind CSS, shadcn/ui
- **Infrastructure**: Docker Compose, Nginx reverse proxy

## Frontend Development

The frontend source lives in `frontend/src/` and is generated and maintained via **Lovable** at [https://github.com/Enach/smart-calendar-flow](https://github.com/Enach/smart-calendar-flow).

### Pull the latest Lovable changes

```bash
make update-frontend
```

This fetches the latest commits from the Lovable repo, copies the updated `src/` into `frontend/src/`, merges any new dependencies into `frontend/package.json`, and creates a commit automatically.

### Local development

```bash
cd frontend
npm install --legacy-peer-deps
npm run dev   # requires backend running on :8080
```

Vite proxies all `/api/` requests to `http://localhost:8080` in dev mode, so start the Go backend first:

```bash
cd backend
go run .
```

### Rebuild after frontend changes

```bash
docker compose build
docker compose up
```
