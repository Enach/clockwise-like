/**
 * PAC-83 AC-30, e2e half (owner OQ-1 (c), contract
 * contracts/features/PAC-83.md §4): the demo exists only in non-production
 * builds. The e2e stack serves the production image
 * (docker/frontend.dockerfile, `npm run build`, mode production), so its
 * sign-in screen must offer no demo entry control.
 *
 * The bundle half (make demo-check: the production build contains none of
 * the demo markers, the development build contains all of them) runs in the
 * web repo's gate.
 */

import { test, expect } from '../fixtures';

test('the sign-in screen of the production image has no demo entry', async ({ page }) => {
  await page.goto('/signin');
  // The sign-in dialog's initial view is the one place a demo build renders
  // the entry (§4.4); it must be on screen for the absence to mean anything.
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button').first()).toBeVisible();

  await expect(page.getByTestId('demo-entry')).toHaveCount(0);
  await expect(page.getByText(/explore a demo|made-up data/i)).toHaveCount(0);
  await expect(page.getByTestId('demo-label')).toHaveCount(0);
});
