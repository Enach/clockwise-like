/**
 * A typed client for the calendar stand-in's control plane (e2e/fake-google,
 * /__fixture/) and the recording sink's log (e2e/fake-google/sink).
 *
 * This is NOT a mock of the backend. The backend talks to the stand-in exactly
 * as it talks to Google — same client library, same requests, same bearer
 * credential — because GOOGLE_CALENDAR_API_BASE_URL points it there
 * (PAC-43). What this file drives is the *external provider's* state: which
 * events a calendar holds, which credentials it accepts, and what it was
 * asked. The tests then assert what the backend and the UI did with it.
 *
 * Isolation: specs run in parallel against ONE stand-in. A scenario owns its
 * calendars (by id) and reads the log by its own credential label after a
 * mark it took; nothing here resets shared state except `reset`, which only
 * global setup calls.
 */

import { ACCEPTED_CREDENTIALS } from './seed/ids';

export const FAKE_GOOGLE_URL = process.env.E2E_FAKE_GOOGLE_URL ?? 'http://127.0.0.1:18090';
export const SINK_URL = process.env.E2E_SINK_URL ?? 'http://127.0.0.1:18091';

/**
 * Every host name the backend container maps to the sink
 * (docker-compose.e2e.yml backend.extra_hosts). Keep the two lists identical.
 */
export const SINK_HOSTS = [
  'www.googleapis.com',
  'oauth2.googleapis.com',
  'accounts.google.com',
  'admin.googleapis.com',
  'graph.microsoft.com',
  'login.microsoftonline.com',
  'slack.com',
  'api.notion.com',
  'zoom.us',
] as const;

/** The sink's fixed address on the pinned network. */
export const SINK_ADDRESS = '10.143.43.20';

/** A calendar#event as the stand-in stores it (the subset the suite uses). */
export interface FakeEvent {
  id?: string;
  summary?: string;
  description?: string;
  status?: string;
  transparency?: string;
  recurringEventId?: string;
  recurrence?: string[];
  start: { dateTime?: string; date?: string };
  end: { dateTime?: string; date?: string };
  attendees?: Array<{
    email: string;
    displayName?: string;
    responseStatus?: string;
    self?: boolean;
    organizer?: boolean;
  }>;
}

export interface FakeRequest {
  seq: number;
  at: string;
  method: string;
  path: string;
  operation: string;
  calendar_id?: string;
  event_id?: string;
  time_min?: string;
  time_max?: string;
  freebusy_ids?: string[];
  /** The title an insert or update carried. */
  summary?: string;
  /** The label of the presented credential; "" for none, "rejected" if unknown. */
  credential: string;
  status: number;
}

export interface SinkConnection {
  at: string;
  listener: string;
  peer: string;
  sni?: string;
  method?: string;
  host?: string;
  path?: string;
  credential_presented: boolean;
}

async function call(method: string, url: string, body?: unknown): Promise<Response> {
  const res = await fetch(url, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(10_000),
  });
  if (!res.ok) {
    throw new Error(`${method} ${url} -> ${res.status} ${await res.text()}`);
  }
  return res;
}

const enc = encodeURIComponent;

export const fake = {
  /** Wipe all stand-in state. Global setup only — never from a test. */
  async reset(): Promise<void> {
    await call('POST', `${FAKE_GOOGLE_URL}/__fixture/reset`);
  },

  /** Replace the accepted credential set. Global setup only. */
  async setAcceptedTokens(
    creds: ReadonlyArray<{ token: string; label: string; calendar: string }>,
  ): Promise<void> {
    await call('PUT', `${FAKE_GOOGLE_URL}/__fixture/tokens`, creds);
  },

  /** Forget every event of one calendar the calling scenario owns. */
  async clearCalendar(calendarId: string): Promise<void> {
    await call('DELETE', `${FAKE_GOOGLE_URL}/__fixture/calendars/${enc(calendarId)}`);
  },

  /** Register a calendar's summary (rooms, for the calendar list). */
  async defineCalendar(calendarId: string, summary: string): Promise<void> {
    await call('PUT', `${FAKE_GOOGLE_URL}/__fixture/calendars/${enc(calendarId)}`, { summary });
  },

  /** Seed events on a calendar; answers them as stored, ids included. */
  async seedEvents(
    calendarId: string,
    events: FakeEvent[],
  ): Promise<Array<FakeEvent & { id: string }>> {
    const res = await call(
      'POST',
      `${FAKE_GOOGLE_URL}/__fixture/calendars/${enc(calendarId)}/events`,
      events,
    );
    return (await res.json()) as Array<FakeEvent & { id: string }>;
  },

  /** Every event a calendar holds now, unfiltered. */
  async events(calendarId: string): Promise<Array<FakeEvent & { id: string }>> {
    const res = await call('GET', `${FAKE_GOOGLE_URL}/__fixture/calendars/${enc(calendarId)}/events`);
    return (await res.json()) as Array<FakeEvent & { id: string }>;
  },

  /** The newest log sequence number: take it before acting, read after it. */
  async mark(): Promise<number> {
    const res = await call('GET', `${FAKE_GOOGLE_URL}/__fixture/requests?since=${Number.MAX_SAFE_INTEGER}`);
    return ((await res.json()) as { last_seq: number }).last_seq;
  },

  /** Product requests after `since`, optionally only those under one label. */
  async requests(since = 0, credentialLabel?: string): Promise<FakeRequest[]> {
    const res = await call('GET', `${FAKE_GOOGLE_URL}/__fixture/requests?since=${since}`);
    const all = ((await res.json()) as { requests: FakeRequest[] }).requests;
    return credentialLabel === undefined ? all : all.filter((r) => r.credential === credentialLabel);
  },

  /** Arm a 302 for the next `count` (default 1) product requests carrying `label`. */
  async redirectNext(label: string, location: string, count = 1): Promise<void> {
    await call('POST', `${FAKE_GOOGLE_URL}/__fixture/redirect-next`, { label, location, count });
  },

  /** Disarm any redirect still armed for `label`. */
  async disarmRedirect(label: string): Promise<void> {
    await call('POST', `${FAKE_GOOGLE_URL}/__fixture/redirect-next`, { label, count: 0 });
  },
};

export const sink = {
  /** Every connection the sink has been offered since the stack started. */
  async connections(): Promise<SinkConnection[]> {
    const res = await call('GET', `${SINK_URL}/connections`);
    return (await res.json()) as SinkConnection[];
  },
};

/**
 * The sink connections NOT explained by the one known escape: GET
 * /api/auth/status, for a user with a stored credential, looks the user up at
 * https://www.googleapis.com/oauth2/v2/userinfo (backend/api/handlers_auth.go
 * fetchUserEmail, called from status()). That host is not redirected (spec
 * §V2-3 "the identity lookup"), so every page load by a connected user offers
 * the sink one TLS connection with SNI www.googleapis.com, which the sink
 * refuses before any credential is sent. It is the defect that keeps AC-23's
 * strict form fixme'd (tests/hermeticity.spec.ts).
 *
 * Anything else — plain HTTP, a credential presented, any other host (token
 * refresh goes to oauth2.googleapis.com) — is unexplained.
 */
export function unexplainedSinkConnections(conns: SinkConnection[]): SinkConnection[] {
  return conns.filter(
    (c) =>
      !(
        c.listener === ':443' &&
        c.sni === 'www.googleapis.com' &&
        !c.credential_presented &&
        c.method === undefined
      ),
  );
}

/**
 * Global-setup hook: a clean stand-in that accepts exactly the seeded
 * credentials (seed/ids.ts ACCEPTED_CREDENTIALS, the single source).
 */
export async function prepareStandIn(): Promise<void> {
  await fake.reset();
  await fake.setAcceptedTokens(ACCEPTED_CREDENTIALS);
}
