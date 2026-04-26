
# Clockwise-like calendar frontend

Build the full frontend at the Lovable project root (`src/`), wired to the Go backend's `/api/*` endpoints, with a graceful mock-data fallback so the Lovable preview is fully interactive.

## Stack & setup
- React 18 + TypeScript + Vite + Tailwind (existing)
- Add: `@fullcalendar/react`, `@fullcalendar/timegrid`, `@fullcalendar/daygrid`, `@fullcalendar/interaction`
- Use existing: react-router-dom v6, @tanstack/react-query, axios, lucide-react
- Custom toast system (no external lib), per spec

## Architecture
- `src/api/client.ts` — axios instance (`baseURL: "/api"`) with a **mock fallback**: each API function tries the real call first; on network failure returns realistic mock data (sample week of events, default settings, fake focus blocks, NLP parses with 3 suggested slots, audit entries). A small dev banner appears when mocks are active.
- `src/api/types.ts` — all interfaces from the spec
- `src/hooks/` — `useToast`, `useSettings`, `useCalendarEvents`, `useFocusBlocks` (react-query wrappers)
- `src/App.tsx` — Router + QueryClientProvider + ToastProvider
- Routes: `/` Dashboard, `/settings` Settings

## Dashboard (`/`)
- **Navbar**: 🗓️ logo + name, current week range with ‹ › arrows, ⚙️ Settings link
- **NLP Command Bar** (full-width, sticky under navbar): input + submit, loading spinner. On `schedule_meeting` → **NLPConfirmModal** with parsed summary + up to 3 slot radio cards (day, date, time range, score bar) → Confirm posts `/api/nlp/confirm`, refreshes calendar, toast. `schedule_focus` → toast + refresh. `unknown` → inline red error.
- **Main grid (2-col desktop, stacked on mobile)**:
  - Left: **FullCalendar** `timeGridWeek`, business hours from settings, 30-min slots. Focus blocks rendered in `settings.focus_color` with 🎯 prefix, non-editable. Click empty slot → pre-fill NLP bar with date/time hint. Click event → popover (title, time, attendees). Week nav synced with navbar. Auto-refresh every 5 min via react-query.
  - Right sidebar:
    - **ConnectionStatus** — green/red dot + email + Connect/Disconnect button (Connect → `window.location.href = "/api/auth/google"`)
    - **TodayAgenda** — today's events chronologically with color dot, time, title, 🎯 for focus; empty state copy
    - **FocusStats** — total focus time this week, progress bar vs `daily_target × 5`, 5 mini bars Mon–Fri
    - **QuickActions** — Run Focus Engine, Clear Focus Blocks (with confirm), Audit Log toggle showing last 10 entries

## Settings (`/settings`)
Cards per section, load from `GET /api/settings`, save per section (Save button + on blur), toast feedback:
- **Working Hours** — start, end, timezone select (13 specified zones)
- **Focus Time** — min/max block, daily target with "X h Y min" preview, label, color picker, auto-schedule toggle, conditional cron input + helper text
- **Lunch Break** — protect toggle + conditional start/end
- **Meeting Buffers** — before / after minutes
- **Meeting Compression** — toggle + description
- **Google Calendar** — ConnectionStatus + calendar ID input (default `primary`)
- **AI / NLP** — provider select (OpenAI/Anthropic/Ollama), model, API key (password), conditional Ollama base URL

## Toast system
`Toast.tsx` + `useToast` hook providing `toast.success/error/info`. Fixed bottom-right, stacked, auto-dismiss 4s, manual close button, green/red/blue variants.

## Design
Notion/Linear minimal aesthetic. White / gray-50 background, indigo-600 primary accent, dynamic focus color from settings (the only allowed inline style). Cards `shadow-sm rounded-xl`, system-ui/Inter font. Tailwind utilities only. Sidebar stacks below calendar at `< lg` breakpoint.

## Files created
```
src/
  App.tsx (replace)
  api/{client.ts, types.ts, mocks.ts}
  pages/{Dashboard.tsx, Settings.tsx}
  components/{Navbar.tsx, NLPBar.tsx, NLPConfirmModal.tsx,
              ConnectionStatus.tsx, TodayAgenda.tsx,
              FocusStats.tsx, QuickActions.tsx, Toast.tsx,
              EventPopover.tsx}
  hooks/{useToast.ts, useSettings.ts, useCalendarEvents.ts, useFocusBlocks.ts}
```
`pages/Index.tsx` will be replaced by `Dashboard.tsx` in routing.

## Out of scope / untouched
- `vite.config.ts`, `index.html`, Dockerfile, nginx config, backend code
- No auth UI beyond connect/disconnect (OAuth handled by backend redirect)

## Note on repo placement
Files are created at `src/` (Lovable project root) so the preview works. When integrating into your repo, copy `src/` and the new package.json deps into your `frontend/` directory.
