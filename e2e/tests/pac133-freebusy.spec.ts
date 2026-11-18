/**
 * PAC-133: a calendar the provider cannot read is unknown, never free.
 *
 *   AC-5   the participant lookup reports an unseeded attendee unknown
 *   AC-14  the raw lookup marks it unknown and always includes the own calendar
 *   AC-21  suggestions name it, and it does not constrain the slots
 *
 * The stand-in (e2e/fake-google) is the external provider; the backend is
 * real. An attendee the stand-in holds no calendar for is answered the way
 * Google answers a calendar it cannot see: notFound, no busy windows. Every
 * attendee here is run-unique, so the participant lookup's 15-minute cache
 * and other journeys cannot answer for it.
 *
 * Requester: CONNECTED.calendar, on a Wednesday ten weeks ahead, a week no
 * other journey seeds. Its own stand-in calendar has no events that day; since
 * AC-37 the stand-in answers a fixture credential's empty calendar readable,
 * as Google does, so suggestions are not the own-calendar 500.
 */

import { test, expect } from '../fixtures';
import { fake } from '../fake';
import { CONNECTED } from '../seed/ids';
import { atUtc, instant, isoDate, weekDay } from '../lib/dates';

const USER = CONNECTED.calendar;
const DAY = () => weekDay(10, 2);

interface Window {
  start: string;
  end: string;
}

/** A seeded attendee S, busy 10:00-11:00 on `day`, and an unseeded one U. */
async function attendees(tag: string, day: Date): Promise<{ s: string; u: string }> {
  const run = `${Date.now()}.${Math.random().toString(36).slice(2, 8)}`;
  const s = `e2e.pac133.${tag}.s.${run}@attendee.e2e.test`;
  const u = `e2e.pac133.${tag}.u.${run}@unseeded.e2e.test`;
  await fake.seedEvents(s, [
    { summary: 'busy', start: { dateTime: atUtc(day, '10:00') }, end: { dateTime: atUtc(day, '11:00') } },
  ]);
  return { s, u };
}

const windows = (ws: Window[] | undefined) => (ws ?? []).map((w) => [instant(w.start), instant(w.end)]);

test.describe('PAC-133 — unknown free/busy is never free', () => {
  test('AC-5: the participant lookup reports an unseeded attendee unknown in every view', async ({
    apiAs,
  }) => {
    const day = DAY();
    const { s, u } = await attendees('ac5', day);
    const res = await apiAs(USER).post('/api/freebusy', {
      emails: [s, u],
      start_time: atUtc(day, '00:00'),
      end_time: atUtc(day, '23:59'),
    });
    expect(res.status(), await res.text()).toBe(200);
    const body = (await res.json()) as {
      participants: Array<{ email: string; status: string; busy: Window[] }>;
      busy: Record<string, Window[]>;
      results: Array<{ email: string; coverage: string; busy: Window[] }>;
    };
    const byEmail = Object.fromEntries(body.participants.map((p) => [p.email, p]));
    expect(byEmail[s]?.status).toBe('known');
    expect(windows(byEmail[s]?.busy)).toEqual([[instant(atUtc(day, '10:00')), instant(atUtc(day, '11:00'))]]);

    expect(byEmail[u]?.status).toBe('unknown');
    expect(byEmail[u]?.busy).toEqual([]);
    expect(body.busy[u]).toEqual([]);
    const legacy = body.results.find((r) => r.email === u);
    expect(legacy?.coverage).toBe('unknown');
    expect(legacy?.busy).toEqual([]);
  });

  test('AC-14: the raw lookup marks an unseeded attendee unknown and includes the own calendar', async ({
    apiAs,
  }) => {
    const day = DAY();
    const { s, u } = await attendees('ac14', day);
    const mark = await fake.mark();
    const res = await apiAs(USER).get('/api/calendar/freebusy', {
      start: `${isoDate(day)}T00:00:00Z`,
      end: `${isoDate(day)}T23:59:00Z`,
      attendees: `${s},${u}`,
    });
    expect(res.status(), await res.text()).toBe(200);
    const body = (await res.json()) as {
      own: { status: string; busy?: Window[] };
      calendars: Array<{ address: string; status: string; busy?: Window[] }>;
    };

    // The own calendar, identified by its key, read and empty that day.
    expect(body.own).toEqual({ status: 'known', busy: [] });

    expect(body.calendars.map((c) => c.address)).toEqual([s, u]);
    const [sc, uc] = body.calendars;
    expect(sc?.status).toBe('known');
    expect(windows(sc?.busy)).toEqual([[instant(atUtc(day, '10:00')), instant(atUtc(day, '11:00'))]]);
    expect(uc?.status).toBe('unknown');
    // No part of the answer gives U an empty busy list.
    expect(uc && 'busy' in uc).toBe(false);

    // The provider was asked about the own calendar too.
    const asked = (await fake.requests(mark, USER.label)).filter((r) => r.operation === 'freebusy.query');
    expect(asked).toHaveLength(1);
    expect(asked[0]!.freebusy_ids).toEqual(['primary', s, u]);
  });

  test('AC-21: suggestions name the unknown attendee, and only reported busy time constrains', async ({
    apiAs,
  }) => {
    const day = DAY();
    const { s: b, u } = await attendees('ac21', day);
    const res = await apiAs(USER).post('/api/schedule/suggest', {
      duration_minutes: 30,
      attendees: [b, u],
      range_start: atUtc(day, '09:00'),
      range_end: atUtc(day, '17:00'),
      title: 'E2E PAC-133 suggest',
    });
    expect(res.status(), await res.text()).toBe(200);
    const body = (await res.json()) as { slots: Window[]; unknown_attendees: string[] };

    expect(body.unknown_attendees).toEqual([u]);
    expect(body.slots.length).toBeGreaterThan(0);
    const busyStart = Date.parse(atUtc(day, '10:00'));
    const busyEnd = Date.parse(atUtc(day, '11:00'));
    for (const slot of body.slots) {
      const overlaps = Date.parse(slot.start) < busyEnd && Date.parse(slot.end) > busyStart;
      expect(overlaps, `slot ${slot.start}-${slot.end} overlaps B's 10:00-11:00`).toBe(false);
    }
  });
});
