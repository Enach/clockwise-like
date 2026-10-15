/**
 * PAC-55: each signed-in user chooses a 12- or 24-hour clock; the public
 * booking page always shows 12-hour.
 *
 * Every test uses a fresh user, so no seeded user's settings change under the
 * parallel specs. A fresh browser context (storageStateFor) stands in for
 * "site storage cleared" in AC-10: clearing storage literally would also drop
 * the onboarding marker and land the user in onboarding.
 */

import { randomUUID } from 'node:crypto';
import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { storageStateFor, type SessionUser } from '../auth';
import { query } from '../db';
import { atUtc, nextBookableWeekday } from '../lib/dates';
import { pickBookingDate, slotButtons } from '../lib/ui';

async function freshUser(label: string): Promise<SessionUser> {
  const id = randomUUID();
  const user = { id, email: `e2e.timefmt.${label}.${id.slice(0, 8)}@paceday.test`, name: `E2E Time ${label}` };
  await query('INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $3, NOW())', [user.id, user.email, user.name]);
  return user;
}

/** The week view's hour-axis labels, once the grid has rendered. */
async function hourAxis(page: Page): Promise<string[]> {
  const labels = page.locator('.fc-timegrid-slot-label-cushion');
  await expect(labels.first()).toBeVisible();
  return (await labels.allInnerTexts()).map((t) => t.trim()).filter(Boolean);
}

test.describe('PAC-55 — time format', () => {
  test('AC-8: with 24-hour chosen, the calendar hour axis reads 07:00 … 20:00', async ({ browser, apiAs }) => {
    const user = await freshUser('axis');
    expect((await apiAs(user).patch('/api/settings', { timeFormat: '24h' })).status()).toBe(200);

    const context = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await context.newPage();
    try {
      await page.goto('/app');
      const axis = await hourAxis(page);
      expect(axis[0]).toBe('07:00');
      expect(axis).toContain('20:00');
      expect(axis.join(' ')).not.toMatch(/am|pm/i);
    } finally {
      await context.close();
    }
  });

  test('AC-10: choosing 24-hour applies without a reload and survives a fresh session', async ({ browser }) => {
    const user = await freshUser('persist');
    const first = await browser.newContext({ storageState: storageStateFor(user) });
    const page = await first.newPage();
    try {
      await page.goto('/app');
      expect((await hourAxis(page))[0]).toMatch(/am/i); // 12-hour by default

      await page.goto('/settings');
      await page.getByLabel('Time format').selectOption('24h');
      await page.getByRole('button', { name: /save changes/i }).click();
      await expect(page.getByText(/settings saved/i)).toBeVisible();

      // In-app navigation, no reload.
      await page.getByRole('link', { name: /paceday/i }).first().click();
      expect((await hourAxis(page))[0]).toBe('07:00');
    } finally {
      await first.close();
    }

    const second = await browser.newContext({ storageState: storageStateFor(user) });
    const again = await second.newPage();
    try {
      await again.goto('/settings');
      await expect(again.getByLabel('Time format')).toHaveValue('24h');
      await again.goto('/app');
      expect((await hourAxis(again))[0]).toBe('07:00');
    } finally {
      await second.close();
    }
  });

  test('AC-11: the host sees bookings in 24-hour form; their public page stays 12-hour', async ({ browser, apiAs, api }) => {
    const host = await freshUser('host');
    const hostApi = apiAs(host);
    expect((await hostApi.patch('/api/settings', { timeFormat: '24h' })).status()).toBe(200);

    const suffix = randomUUID().slice(0, 8);
    const title = `E2E Time Format ${suffix}`;
    const created = await hostApi.post('/api/scheduling-links', {
      title,
      slug: `e2e-timefmt-${suffix}`,
      duration_options: [30],
      days_of_week: [1, 2, 3, 4, 5],
      window_start_time: '16:00',
      window_end_time: '17:00',
      min_notice_minutes: 0,
      usage_type: 'reusable',
    });
    expect(created.status(), await created.text()).toBe(201);
    const link = await created.json();

    const date = nextBookableWeekday(15);
    const booked = await api.post(`/api/book/${link.slug}`, {
      name: 'E2E Time Format Booker',
      email: `e2e.booker.timefmt.${suffix}@paceday.test`,
      start: atUtc(date, '16:00'),
      duration_minutes: 30,
    });
    expect(booked.status(), await booked.text()).toBe(201);

    const context = await browser.newContext({ storageState: storageStateFor(host) });
    const page = await context.newPage();
    try {
      await page.goto('/app/links');
      await page.getByRole('button', { name: `Bookings for ${title}` }).click();
      await expect(page.getByText(/16:00 – 16:30/)).toBeVisible();
      await expect(page.getByRole('dialog').getByText(/\b(AM|PM)\b/)).toHaveCount(0);
    } finally {
      await context.close();
    }

    // The same host's public page, opened by an anonymous visitor: 12-hour.
    const visitor = await browser.newContext();
    const pub = await visitor.newPage();
    try {
      await pub.goto(`/book/${link.slug}`);
      await pickBookingDate(pub, nextBookableWeekday(16));
      await expect(slotButtons(pub).first()).toBeVisible();
      await expect(pub.getByRole('button', { name: /^\s*\d{2}:\d{2}\s*$/ })).toHaveCount(0);
    } finally {
      await visitor.close();
    }
  });
});
