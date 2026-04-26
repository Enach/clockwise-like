import type {
  AuditEntry,
  AuthStatus,
  CalendarEvent,
  CompressionResult,
  FocusBlock,
  FocusRunResult,
  MoveProposal,
  ParseResult,
  Settings,
  SuggestedSlot,
} from "./types";

// ---------- Mock state ----------
// Used as a fallback when the Go backend at /api is not reachable
// (e.g. inside the Lovable preview). Mocks are mutated in-memory so
// the UI feels alive across interactions.

const PROVIDER_KEY = "clockwise:mock-mode";

let usingMocks = false;
const mockListeners = new Set<(v: boolean) => void>();

export function isUsingMocks() {
  return usingMocks;
}
export function subscribeMockMode(cb: (v: boolean) => void) {
  mockListeners.add(cb);
  cb(usingMocks);
  return () => {
    mockListeners.delete(cb);
  };
}
function setMockMode(v: boolean) {
  if (usingMocks === v) return;
  usingMocks = v;
  try {
    sessionStorage.setItem(PROVIDER_KEY, v ? "1" : "0");
  } catch {}
  mockListeners.forEach((cb) => cb(v));
}
try {
  usingMocks = sessionStorage.getItem(PROVIDER_KEY) === "1";
} catch {}

const DEFAULT_SETTINGS: Settings = {
  work_start: "09:00",
  work_end: "18:00",
  timezone: "Europe/Paris",
  focus_min_block_minutes: 60,
  focus_max_block_minutes: 180,
  focus_daily_target_minutes: 180,
  focus_label: "Focus Time",
  focus_color: "#7C3AED",
  lunch_start: "12:30",
  lunch_end: "13:30",
  protect_lunch: true,
  buffer_before_minutes: 5,
  buffer_after_minutes: 5,
  compression_enabled: true,
  auto_schedule_enabled: true,
  auto_schedule_cron: "0 7 * * 1-5",
  llm_provider: "openai",
  llm_model: "gpt-4o-mini",
  llm_api_key: "",
  llm_base_url: "",
  calendar_id: "primary",
};

const mockState = {
  settings: { ...DEFAULT_SETTINGS },
  auth: { connected: true, email: "you@example.com" } as AuthStatus,
  events: [] as CalendarEvent[],
  focusBlocks: [] as FocusBlock[],
  audit: [] as AuditEntry[],
  nextEventId: 1000,
  nextFocusId: 1,
  nextAuditId: 1,
};

function pad(n: number) {
  return n < 10 ? `0${n}` : `${n}`;
}
function isoAt(date: Date, h: number, m: number) {
  const d = new Date(date);
  d.setHours(h, m, 0, 0);
  return d.toISOString();
}
function dateOnly(d: Date) {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}
function startOfWeek(d: Date) {
  const x = new Date(d);
  const day = x.getDay();
  const diff = day === 0 ? -6 : 1 - day; // Monday
  x.setDate(x.getDate() + diff);
  x.setHours(0, 0, 0, 0);
  return x;
}
function addMinutes(iso: string, m: number) {
  return new Date(new Date(iso).getTime() + m * 60_000).toISOString();
}
function logAudit(action: string, details: string) {
  mockState.audit.unshift({
    id: mockState.nextAuditId++,
    action,
    details,
    created_at: new Date().toISOString(),
  });
  mockState.audit = mockState.audit.slice(0, 50);
}

function seedMocks() {
  if (mockState.events.length > 0) return;
  const monday = startOfWeek(new Date());
  const sample: Array<{ day: number; sh: number; sm: number; eh: number; em: number; title: string; color: string; att?: string[] }> = [
    { day: 0, sh: 9, sm: 30, eh: 10, em: 0, title: "Standup", color: "#3B82F6", att: ["alice@co.com", "bob@co.com"] },
    { day: 0, sh: 14, sm: 0, eh: 15, em: 0, title: "1:1 with Alice", color: "#10B981", att: ["alice@co.com"] },
    { day: 1, sh: 11, sm: 0, eh: 12, em: 0, title: "Product review", color: "#F59E0B", att: ["pm@co.com"] },
    { day: 1, sh: 16, sm: 0, eh: 17, em: 0, title: "Design sync", color: "#EC4899" },
    { day: 2, sh: 10, sm: 0, eh: 10, em: 30, title: "Standup", color: "#3B82F6" },
    { day: 2, sh: 15, sm: 30, eh: 16, em: 30, title: "Customer call", color: "#06B6D4", att: ["client@acme.com"] },
    { day: 3, sh: 9, sm: 30, eh: 10, em: 30, title: "Sprint planning", color: "#8B5CF6", att: ["team@co.com"] },
    { day: 3, sh: 14, sm: 0, eh: 14, em: 30, title: "Coffee with Sam", color: "#10B981" },
    { day: 4, sh: 11, sm: 0, eh: 12, em: 0, title: "Demo prep", color: "#F59E0B" },
  ];
  sample.forEach((s) => {
    const d = new Date(monday);
    d.setDate(d.getDate() + s.day);
    mockState.events.push({
      id: String(mockState.nextEventId++),
      title: s.title,
      start: isoAt(d, s.sh, s.sm),
      end: isoAt(d, s.eh, s.em),
      color: s.color,
      attendees: s.att,
    });
  });

  // A couple of pre-existing focus blocks
  const focusDays = [1, 3];
  focusDays.forEach((day) => {
    const d = new Date(monday);
    d.setDate(d.getDate() + day);
    const start = isoAt(d, 8, 0);
    const end = isoAt(d, 9, 0);
    const id = mockState.nextFocusId++;
    mockState.focusBlocks.push({
      id,
      google_event_id: `gcal-focus-${id}`,
      start_time: start,
      end_time: end,
      date: dateOnly(d),
    });
    mockState.events.push({
      id: `focus-${id}`,
      title: mockState.settings.focus_label,
      start,
      end,
      color: mockState.settings.focus_color,
      is_focus_block: true,
    });
  });

  logAudit("seed", "Initialised mock workspace");
}
seedMocks();

// ---------- Tiny fetch wrapper with mock fallback ----------

const API_BASE = "/api";
const NETWORK_TIMEOUT_MS = 4000;

async function realFetch<T>(method: string, path: string, body?: unknown, query?: Record<string, string | undefined>): Promise<T> {
  const url = new URL(API_BASE + path, window.location.origin);
  if (query) {
    for (const [k, v] of Object.entries(query)) {
      if (v != null && v !== "") url.searchParams.set(k, v);
    }
  }
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), NETWORK_TIMEOUT_MS);
  try {
    const res = await fetch(url.toString(), {
      method,
      signal: ctrl.signal,
      headers: body ? { "Content-Type": "application/json" } : undefined,
      body: body ? JSON.stringify(body) : undefined,
      credentials: "include",
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    if (res.status === 204) return undefined as T;
    const ct = res.headers.get("content-type") || "";
    // Strict: only accept JSON. If the dev server returns HTML
    // (e.g. SPA index.html when no real backend exists), treat as failure
    // so the mock fallback engages.
    if (!ct.includes("application/json")) {
      throw new Error("Non-JSON response");
    }
    return (await res.json()) as T;
  } finally {
    clearTimeout(t);
  }
}

async function withFallback<T>(real: () => Promise<T>, mock: () => T | Promise<T>): Promise<T> {
  if (usingMocks) return mock();
  try {
    const v = await real();
    if (usingMocks) setMockMode(false);
    return v;
  } catch {
    setMockMode(true);
    return mock();
  }
}

// ---------- Mock implementations ----------

function mockSuggestedSlots(durationMin: number, rangeStart?: string, rangeEnd?: string): SuggestedSlot[] {
  const start = rangeStart ? new Date(rangeStart) : new Date();
  const out: SuggestedSlot[] = [];
  let cursor = new Date(start);
  cursor.setMinutes(0, 0, 0);
  if (cursor.getHours() < 9) cursor.setHours(10, 0, 0, 0);
  const end = rangeEnd ? new Date(rangeEnd) : new Date(cursor.getTime() + 5 * 24 * 3600_000);
  let attempts = 0;
  while (out.length < 3 && cursor < end && attempts < 60) {
    attempts++;
    const day = cursor.getDay();
    const hour = cursor.getHours();
    if (day !== 0 && day !== 6 && hour >= 9 && hour < 17) {
      const s = new Date(cursor);
      const e = new Date(cursor.getTime() + durationMin * 60_000);
      const conflict = mockState.events.some(
        (ev) => new Date(ev.start) < e && new Date(ev.end) > s,
      );
      if (!conflict) {
        out.push({
          start: s.toISOString(),
          end: e.toISOString(),
          score: 0.95 - out.length * 0.15,
          reasons: out.length === 0 ? ["Best fit", "No conflicts", "Inside working hours"] : ["No conflicts", "Inside working hours"],
        });
      }
    }
    cursor = new Date(cursor.getTime() + 60 * 60_000);
    if (cursor.getHours() >= 18) {
      cursor.setDate(cursor.getDate() + 1);
      cursor.setHours(10, 0, 0, 0);
    }
  }
  return out;
}

// ---------- Public API ----------

export const api = {
  // Health
  health: () =>
    withFallback(
      () => realFetch<{ status: string; version: string }>("GET", "/health"),
      () => ({ status: "ok", version: "mock-1.0.0" }),
    ),

  // Auth
  authStatus: () =>
    withFallback(
      () => realFetch<AuthStatus>("GET", "/auth/status"),
      () => ({ ...mockState.auth }),
    ),
  authConnectUrl: () => `${API_BASE}/auth/google`,
  authDisconnect: () =>
    withFallback(
      () => realFetch<void>("DELETE", "/auth/disconnect"),
      () => {
        mockState.auth = { connected: false, email: "" };
        logAudit("auth.disconnect", "Disconnected Google Calendar (mock)");
      },
    ),

  // Settings
  getSettings: () =>
    withFallback(
      () => realFetch<Settings>("GET", "/settings"),
      () => ({ ...mockState.settings }),
    ),
  updateSettings: (s: Settings) =>
    withFallback(
      () => realFetch<Settings>("PUT", "/settings", s),
      () => {
        mockState.settings = { ...s };
        // keep focus block colors in sync
        mockState.events.forEach((e) => {
          if (e.is_focus_block) {
            e.color = s.focus_color;
            e.title = s.focus_label;
          }
        });
        logAudit("settings.update", "Updated settings (mock)");
        return { ...mockState.settings };
      },
    ),

  // Calendar
  getEvents: (start: string, end: string) =>
    withFallback(
      () => realFetch<CalendarEvent[]>("GET", "/calendar/events", undefined, { start, end }),
      () => {
        const s = new Date(start).getTime();
        const e = new Date(end).getTime();
        return mockState.events.filter((ev) => {
          const es = new Date(ev.start).getTime();
          const ee = new Date(ev.end).getTime();
          return ee >= s && es <= e;
        });
      },
    ),

  // Focus
  runFocus: (week?: string) =>
    withFallback(
      () => realFetch<FocusRunResult>("POST", "/focus/run", { week }),
      () => {
        const monday = week ? new Date(week) : startOfWeek(new Date());
        const created: FocusBlock[] = [];
        const skipped: string[] = [];
        for (let i = 0; i < 5; i++) {
          const d = new Date(monday);
          d.setDate(d.getDate() + i);
          const date = dateOnly(d);
          // pick an empty 90-min morning slot
          const slotStart = isoAt(d, 8, 0);
          const slotEnd = isoAt(d, 9, 30);
          const conflict = mockState.events.some(
            (ev) =>
              new Date(ev.start) < new Date(slotEnd) &&
              new Date(ev.end) > new Date(slotStart) &&
              !ev.is_focus_block,
          );
          if (conflict) {
            skipped.push(date);
            continue;
          }
          const exists = mockState.focusBlocks.some(
            (b) => b.start_time === slotStart && b.end_time === slotEnd,
          );
          if (exists) continue;
          const id = mockState.nextFocusId++;
          const block: FocusBlock = {
            id,
            google_event_id: `gcal-focus-${id}`,
            start_time: slotStart,
            end_time: slotEnd,
            date,
          };
          mockState.focusBlocks.push(block);
          mockState.events.push({
            id: `focus-${id}`,
            title: mockState.settings.focus_label,
            start: slotStart,
            end: slotEnd,
            color: mockState.settings.focus_color,
            is_focus_block: true,
          });
          created.push(block);
        }
        logAudit("focus.run", `Created ${created.length} focus blocks (mock)`);
        return {
          week_start: dateOnly(monday),
          created_blocks: created,
          skipped_days: skipped,
          total_minutes: created.length * 90,
          errors: [],
        };
      },
    ),
  getFocusBlocks: (week: string) =>
    withFallback(
      () => realFetch<FocusBlock[]>("GET", "/focus/blocks", undefined, { week }),
      () => {
        const monday = startOfWeek(new Date(week));
        const sunday = new Date(monday);
        sunday.setDate(sunday.getDate() + 7);
        return mockState.focusBlocks.filter((b) => {
          const t = new Date(b.start_time);
          return t >= monday && t < sunday;
        });
      },
    ),
  clearFocusBlocks: (week: string) =>
    withFallback(
      () => realFetch<{ deleted: number }>("DELETE", "/focus/blocks", undefined, { week }),
      () => {
        const monday = startOfWeek(new Date(week));
        const sunday = new Date(monday);
        sunday.setDate(sunday.getDate() + 7);
        const before = mockState.focusBlocks.length;
        const ids = new Set<string>();
        mockState.focusBlocks = mockState.focusBlocks.filter((b) => {
          const t = new Date(b.start_time);
          if (t >= monday && t < sunday) {
            ids.add(`focus-${b.id}`);
            return false;
          }
          return true;
        });
        mockState.events = mockState.events.filter((e) => !ids.has(e.id));
        const deleted = before - mockState.focusBlocks.length;
        logAudit("focus.clear", `Cleared ${deleted} focus blocks (mock)`);
        return { deleted };
      },
    ),

  // Schedule
  scheduleSuggest: (body: {
    duration_minutes: number;
    attendees: string[];
    range_start: string;
    range_end: string;
    title: string;
  }) =>
    withFallback(
      () => realFetch<{ slots: SuggestedSlot[] }>("POST", "/schedule/suggest", body),
      () => ({ slots: mockSuggestedSlots(body.duration_minutes, body.range_start, body.range_end) }),
    ),
  scheduleCreate: (body: {
    title: string;
    start: string;
    end: string;
    attendees: string[];
    description?: string;
  }) =>
    withFallback(
      () => realFetch<CalendarEvent>("POST", "/schedule/create", body),
      () => {
        const ev: CalendarEvent = {
          id: String(mockState.nextEventId++),
          title: body.title,
          start: body.start,
          end: body.end,
          attendees: body.attendees,
          color: "#3B82F6",
        };
        mockState.events.push(ev);
        logAudit("schedule.create", `Created "${body.title}" (mock)`);
        return ev;
      },
    ),
  compressionPreview: (body: { date?: string; week?: string }) =>
    withFallback(
      () => realFetch<CompressionResult[]>("POST", "/schedule/compress", body),
      () => [],
    ),
  compressionApply: (body: { proposals: MoveProposal[] }) =>
    withFallback(
      () => realFetch<{ applied: number; failed: number }>("POST", "/schedule/compress/apply", body),
      () => ({ applied: body.proposals.length, failed: 0 }),
    ),

  // NLP
  nlpParse: (text: string) =>
    withFallback(
      () => realFetch<ParseResult>("POST", "/nlp/parse", { text }),
      () => {
        const t = text.trim().toLowerCase();
        if (!t) return { intent: "unknown", error: "Please type a request." } as ParseResult;
        if (t.includes("focus")) {
          return {
            intent: "schedule_focus",
            title: "Focus Time",
            duration_minutes: 90,
          } as ParseResult;
        }
        const durationMatch = t.match(/(\d+)\s*(min|minute|m\b|h\b|hour|hr)/);
        let duration = 30;
        if (durationMatch) {
          const n = parseInt(durationMatch[1], 10);
          duration = /h/.test(durationMatch[2]) ? n * 60 : n;
        }
        const attendees = Array.from(text.matchAll(/[\w.+-]+@[\w-]+\.[\w.-]+/g)).map((m) => m[0]);
        const titleMatch = text.match(/(?:about|for|:)\s*(.+)$/i);
        const title = titleMatch ? titleMatch[1].trim() : attendees.length ? `Meeting with ${attendees[0].split("@")[0]}` : "New meeting";
        const now = new Date();
        const rangeStart = now.toISOString();
        const rangeEnd = new Date(now.getTime() + 7 * 24 * 3600_000).toISOString();
        return {
          intent: "schedule_meeting",
          title,
          duration_minutes: duration,
          attendees,
          range_start: rangeStart,
          range_end: rangeEnd,
          suggested_slots: mockSuggestedSlots(duration, rangeStart, rangeEnd),
        } as ParseResult;
      },
    ),
  nlpConfirm: (parse_result: ParseResult, selected_slot_index: number) =>
    withFallback(
      () => realFetch<CalendarEvent>("POST", "/nlp/confirm", { parse_result, selected_slot_index }),
      () => {
        const slot = parse_result.suggested_slots?.[selected_slot_index];
        if (!slot) throw new Error("No slot selected");
        const ev: CalendarEvent = {
          id: String(mockState.nextEventId++),
          title: parse_result.title || "New meeting",
          start: slot.start,
          end: slot.end,
          attendees: parse_result.attendees,
          color: "#3B82F6",
        };
        mockState.events.push(ev);
        logAudit("nlp.confirm", `Scheduled "${ev.title}" (mock)`);
        return ev;
      },
    ),

  // Audit
  getAudit: () =>
    withFallback(
      () => realFetch<AuditEntry[]>("GET", "/audit"),
      () => [...mockState.audit],
    ),
};

// keep helper for components that want to nudge state
export const _mockHelpers = {
  get: () => mockState,
};
