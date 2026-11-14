/**
 * PAC-41: a room picked in the event editor is a room the stored event holds
 * (AC-1), and, until PAC-102 adds release-or-keep, an earlier room stays
 * booked (AC-14). Through the Google stand-in: the backend's PATCH, client
 * library and credential are real; the assertions read what the provider
 * stored (e2e/fake.ts `fake.events`) and what the user saw.
 *
 * The stand-in already keeps a resource attendee's flag across the backend's
 * Get + Update (spec OQ-4; e2e/fake-google/rooms_test.go), and its calendar
 * list answers every calendar it knows, so a room is a calendar this journey
 * defines under a run-unique resource address and removes afterwards.
 *
 * Web test ids (smart-calendar-flow impl/pac-41-web, src/components/LocationPicker.tsx):
 * location-mode-room, location-mode-text, location-text-input,
 * room-search-input, room-option (with data-room-id = the room's id),
 * room-selected, room-booking-notice.
 *
 * The journeys own CONNECTED.rooms (seed/ids.ts): its calendar is read and
 * written by nothing else (README rule 6). They also run in parallel with
 * each other, so each one seeds its event at its own hour (never overlapping
 * on the week grid), names its rooms and title with a random run id, and
 * deletes its event and room calendars in finally.
 */

import { randomUUID } from 'node:crypto';
import type { Page } from '@playwright/test';
import { test, expect, type ApiClient } from '../fixtures';
import { atUtc, weekDay } from '../lib/dates';
import { fake, type FakeEvent } from '../fake';
import { CONNECTED } from '../seed/ids';

const ROOMS_USER = CONNECTED.rooms;

/** Each journey's own hour (UTC), so their events never overlap on the grid. */
const HOUR = { ac1: '09:00', ac14a: '12:00', ac14b: '15:00' } as const;

type StoredAttendee = { email: string; resource?: boolean };

function roomAddress(slug: string, run: string): string {
  return `c_e2e-${slug}-${run}@resource.calendar.google.com`;
}

/** A day of this week that is not today, so the week grid shows the event once. */
function dayThisWeek(): Date {
  const todayIdx = (new Date().getUTCDay() + 6) % 7; // 0 = Monday
  return weekDay(0, [1, 2, 3].find((d) => d !== todayIdx)!);
}

/** Thirty minutes from `hhmm` (UTC) on this week's day. */
async function seedEvent(title: string, hhmm: string, extra: Partial<FakeEvent> = {}): Promise<string> {
  const start = atUtc(dayThisWeek(), hhmm);
  const end = new Date(Date.parse(start) + 30 * 60_000).toISOString();
  const [stored] = await fake.seedEvents(ROOMS_USER.email, [
    { summary: title, start: { dateTime: start }, end: { dateTime: end }, ...extra },
  ]);
  return stored!.id;
}

/**
 * Remove what a journey created: its event (through the API, so the stand-in
 * deletes it as Google would) and its room calendars. The status check is
 * soft, so a cleanup problem is reported without hiding the journey's own
 * failure.
 */
async function cleanUp(api: ApiClient, eventId: string | undefined, rooms: string[]): Promise<void> {
  if (eventId) {
    const res = await api.delete(`/api/events/${eventId}`);
    expect.soft(res.status(), `cleanup: delete event ${eventId}`).toBe(204);
  }
  for (const room of rooms) await fake.clearCalendar(room);
}

async function storedRooms(eventId: string): Promise<{ rooms: string[]; location?: string }> {
  const ev = (await fake.events(ROOMS_USER.email)).find((e) => e.id === eventId);
  expect(ev, `event ${eventId} on the stand-in`).toBeTruthy();
  const attendees = (ev as { attendees?: StoredAttendee[] }).attendees ?? [];
  return {
    rooms: attendees.filter((a) => a.resource === true).map((a) => a.email).sort(),
    location: (ev as { location?: string }).location,
  };
}

/** Open the event from the week grid and switch its drawer to the editor. */
async function openEditor(page: Page, title: string): Promise<void> {
  await page.goto('/app');
  await page.getByText(title, { exact: true }).click();
  await page.getByRole('button', { name: 'Edit event' }).click();
}

async function bookRoom(page: Page, roomId: string, search: string): Promise<void> {
  await page.getByTestId('location-mode-room').click();
  await expect(page.getByTestId('room-booking-notice')).toBeVisible();
  await page.getByTestId('room-search-input').fill(search);
  await page.locator(`[data-testid="room-option"][data-room-id="${roomId}"]`).click();
  await expect(page.getByTestId('room-selected')).toBeVisible();
}

async function save(page: Page, eventId: string): Promise<void> {
  const patched = page.waitForResponse(
    (r) => r.request().method() === 'PATCH' && r.url().endsWith(`/api/events/${eventId}`),
  );
  await page.getByRole('button', { name: 'Save changes' }).click();
  expect((await patched).status()).toBe(200);
}

test.describe('PAC-41 room booking (through the stand-in)', () => {
  test('AC-1: a room picked in the editor is a resource attendee of the stored event', async ({
    apiAs,
    pageAs,
    assertNotDemo,
  }) => {
    const run = randomUUID();
    const helsinki = roomAddress('helsinki', run);
    let id: string | undefined;
    await fake.defineCalendar(helsinki, 'Helsinki');
    try {
      const title = `E2E room ${run}`;
      id = await seedEvent(title, HOUR.ac1);

      const page = await pageAs(ROOMS_USER);
      await openEditor(page, title);
      await bookRoom(page, helsinki, `helsinki-${run}`);
      await save(page, id);

      expect(await storedRooms(id)).toMatchObject({ rooms: [helsinki] });
      await assertNotDemo(page);
    } finally {
      await cleanUp(apiAs(ROOMS_USER), id, [helsinki]);
    }
  });

  test('AC-14 (a): picking another room keeps the earlier one booked', async ({ apiAs, pageAs }) => {
    const run = randomUUID();
    const helsinki = roomAddress('helsinki', run);
    const oslo = roomAddress('oslo', run);
    let id: string | undefined;
    await fake.defineCalendar(helsinki, 'Helsinki');
    await fake.defineCalendar(oslo, 'Oslo');
    try {
      const title = `E2E room swap ${run}`;
      id = await seedEvent(title, HOUR.ac14a);
      // Helsinki booked through Paceday.
      const booked = await apiAs(ROOMS_USER).patch(`/api/events/${id}`, { room_resource_email: helsinki });
      expect(booked.status(), await booked.text()).toBe(200);
      expect((await storedRooms(id)).rooms).toEqual([helsinki]);

      const page = await pageAs(ROOMS_USER);
      await openEditor(page, title);
      await bookRoom(page, oslo, `oslo-${run}`);
      await save(page, id);

      expect((await storedRooms(id)).rooms).toEqual([helsinki, oslo].sort());
    } finally {
      await cleanUp(apiAs(ROOMS_USER), id, [helsinki, oslo]);
    }
  });

  test('AC-14 (b): a free-text location keeps the earlier room booked', async ({ apiAs, pageAs }) => {
    const run = randomUUID();
    const helsinki = roomAddress('helsinki', run);
    let id: string | undefined;
    await fake.defineCalendar(helsinki, 'Helsinki');
    try {
      const title = `E2E room text ${run}`;
      id = await seedEvent(title, HOUR.ac14b);
      const booked = await apiAs(ROOMS_USER).patch(`/api/events/${id}`, { room_resource_email: helsinki });
      expect(booked.status(), await booked.text()).toBe(200);

      const page = await pageAs(ROOMS_USER);
      await openEditor(page, title);
      // A loaded event does not carry its room, so the editor opens in free text.
      await page.getByTestId('location-mode-text').click();
      await page.getByTestId('location-text-input').fill('Café');
      await save(page, id);

      expect(await storedRooms(id)).toEqual({ rooms: [helsinki], location: 'Café' });
    } finally {
      await cleanUp(apiAs(ROOMS_USER), id, [helsinki]);
    }
  });
});
