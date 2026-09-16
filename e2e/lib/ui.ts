/**
 * UI helpers.
 *
 * SELECTOR POLICY: role or test id only, never a CSS class. The frontend
 * currently ships ZERO `data-testid` attributes (verified by grep across
 * smart-calendar-flow/src — the only hit is inside a unit test), so everything
 * here is role- or accessible-name-based. Each place where that is fragile has
 * a matching entry in TESTIDS-REQUIRED.md, and this file is written so that
 * adding those ids later is a small, local edit.
 */

import { expect, type Locator, type Page } from '@playwright/test';

/**
 * Click a day in the react-day-picker calendar on /book/:slug.
 *
 * The frontend uses react-day-picker v10 (smart-calendar-flow package.json),
 * whose day cells are `<button>` elements inside a `role="grid"`. Two selectors
 * are tried, OR'd together, because the library's default accessible label
 * format has changed between majors:
 *   1. the accessible name containing "<Month> <day>" — v9/v10 default
 *      `labelDayButton`, e.g. "Wednesday, September 23rd, 2026"
 *   2. the button's own visible text being exactly the day number
 * Scoping to `role="grid"` keeps (2) from matching a duration chip or a slot.
 *
 * WANTED: data-testid="booking-day-YYYY-MM-DD" (see TESTIDS-REQUIRED.md #1),
 * which would replace this whole function with one getByTestId call.
 */
export async function pickBookingDate(page: Page, target: Date): Promise<void> {
  const grid = page.getByRole('grid');
  await expect(grid).toBeVisible();

  const day = target.getUTCDate();
  const monthName = target.toLocaleString('en-US', { month: 'long', timeZone: 'UTC' });
  const year = target.getUTCFullYear();

  const byLabel = grid.getByRole('button', {
    name: new RegExp(`${monthName}\\s+${day}(st|nd|rd|th)?,?\\s+${year}`, 'i'),
  });
  const byText = grid.getByRole('button', { name: String(day), exact: true });

  // The picker opens on the current month; step forward until the target month
  // is on screen. Bounded so a broken picker fails rather than spins.
  for (let hop = 0; hop < 3; hop++) {
    const candidate = byLabel.or(byText).first();
    if (await candidate.isVisible().catch(() => false)) {
      await candidate.click();
      return;
    }
    const next = page.getByRole('button', { name: /next month|go to the next month/i });
    if (!(await next.isVisible().catch(() => false))) break;
    await next.click();
  }

  throw new Error(
    `could not find ${monthName} ${day}, ${year} in the booking calendar. ` +
      `react-day-picker's day label format may have changed — see TESTIDS-REQUIRED.md #1.`,
  );
}

/**
 * Every bookable time slot currently rendered.
 *
 * The slot buttons are the only buttons on the page whose entire label is a
 * clock time (PublicBooking.tsx renders `fmtTime(s.start)`), so this does not
 * collide with the "30 min" duration chips or the day numbers. `\s` in
 * JavaScript matches U+202F, which is what modern ICU puts before AM/PM — a
 * plain " AM" literal would silently match nothing.
 */
export function slotButtons(page: Page): Locator {
  return page
    .getByRole('button')
    .filter({ hasText: /^\s*\d{1,2}:\d{2}\s*(AM|PM)\s*$/i });
}

/**
 * Open the account dropdown in the app navbar. The trigger is the only element
 * with this accessible name (components/Navbar.tsx).
 */
export async function openAccountMenu(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Account menu' }).click();
}

/** Switch the active team on /app/team via the header popover. */
export async function switchTeam(page: Page, teamName: string): Promise<void> {
  // The trigger's label is the CURRENT team's name, so it cannot be matched by
  // the target name. It is the only button in the header next to the "My Team"
  // heading that carries a team name or the "No team selected" placeholder.
  const trigger = page
    .getByRole('button')
    .filter({ hasText: /^(E2E Team (Alpha|Beta)|No team selected)$/ })
    .first();
  await trigger.click();
  await page.getByRole('button', { name: new RegExp(`^${teamName}`) }).click();
  await expect(trigger).toHaveText(new RegExp(teamName));
}
