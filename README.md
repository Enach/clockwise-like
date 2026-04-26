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

- **Backend**: Go 1.22, chi router, SQLite
- **Frontend**: React 18, TypeScript, Vite 5, Tailwind CSS
- **Infrastructure**: Docker Compose, Nginx reverse proxy
