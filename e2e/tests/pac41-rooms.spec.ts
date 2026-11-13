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
 * BLOCKED by PAC-103: every seeded user with a Google credential already owns
 * one journey's calendar (README rule 6: specs share one stand-in, in
 * parallel), and seeding a new one edits e2e/seed, which is outside PAC-41's
 * manifest allowedPaths. Both journeys are written in full; PAC-103 seeds
 * CONNECTED.rooms, points ROOMS_USER at it and turns test.fixme into test.
 */

import type { Page } from '@playwright/test';
import { test, expect } from '../fixtures';
import { atUtc, weekDay } from '../lib/dates';
import { fake, type FakeEvent } from '../fake';
import { CONNECTED } from '../seed/ids';

// PAC-103 replaces this with the journey's own seeded user (CONNECTED.rooms).
const ROOMS_USER = CONNECTED.calendar;

type StoredAttendee = { email: string; resource?: boolean };

function roomAddress(slug: string, run: number): string {
  return `c_e2e-${slug}-${run}@resource.calendar.google.com`;
}

/** A day of this week that is not today, so the week grid shows the event once. */
function dayThisWeek(): Date {
  const todayIdx = (new Date().getUTCDay() + 6) % 7; // 0 = Monday
  return weekDay(0, [1, 2, 3].find((d) => d !== todayIdx)!);
}

async function seedEvent(title: string, extra: Partial<FakeEvent> = {}): Promise<string> {
  const day = dayThisWeek();
  const [stored] = await fake.seedEvents(ROOMS_USER.email, [
    { summary: title, start: { dateTime: atUtc(day, '10:00') }, end: { dateTime: atUtc(day, '10:30') }, ...extra },
  ]);
  return stored!.id;
}

async function storedRooms(eventId: string): Promise<{ rooms: string[]; location?: string }> {
  const ev = (await fake.events(ROOMS_USER.email)).find((e) => e.id === eventId);
  expect(ev, `event ${eventId} on the stand-in`).toBeTruthy();
  const attendees = ((ev as { attendees?: StoredAttendee[] }).attendees ?? []);
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
  // eslint-disable-next-line playwright/no-skipped-test -- PAC-103
  test.fixme('AC-1: a room picked in the editor is a resource attendee of the stored event', async ({
    pageAs,
    assertNotDemo,
  }) => {
    // Blocked by PAC-103 (a seeded connected user this journey owns).
    const run = Date.now();
    const helsinki = roomAddress('helsinki', run);
    await fake.defineCalendar(helsinki, 'Helsinki');
    try {
      const title = `E2E room ${run}`;
      const id = await seedEvent(title);

      const page = await pageAs(ROOMS_USER);
      await openEditor(page, title);
      await bookRoom(page, helsinki, `helsinki-${run}`);
      await save(page, id);

      expect(await storedRooms(id)).toMatchObject({ rooms: [helsinki] });
      await assertNotDemo(page);
    } finally {
      await fake.clearCalendar(helsinki);
    }
  });

  // eslint-disable-next-line playwright/no-skipped-test -- PAC-103
  test.fixme('AC-14 (a): picking another room keeps the earlier one booked', async ({ apiAs, pageAs }) => {
    // Blocked by PAC-103 (a seeded connected user this journey owns).
    const run = Date.now();
    const helsinki = roomAddress('helsinki', run);
    const oslo = roomAddress('oslo', run);
    await fake.defineCalendar(helsinki, 'Helsinki');
    await fake.defineCalendar(oslo, 'Oslo');
    try {
      const title = `E2E room swap ${run}`;
      const id = await seedEvent(title);
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
      await fake.clearCalendar(helsinki);
      await fake.clearCalendar(oslo);
    }
  });

  // eslint-disable-next-line playwright/no-skipped-test -- PAC-103
  test.fixme('AC-14 (b): a free-text location keeps the earlier room booked', async ({ apiAs, pageAs }) => {
    // Blocked by PAC-103 (a seeded connected user this journey owns).
    const run = Date.now();
    const helsinki = roomAddress('helsinki', run);
    await fake.defineCalendar(helsinki, 'Helsinki');
    try {
      const title = `E2E room text ${run}`;
      const id = await seedEvent(title);
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
      await fake.clearCalendar(helsinki);
    }
  });
});
