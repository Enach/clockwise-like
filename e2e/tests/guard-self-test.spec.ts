/**
 * PAC-83 AC-28: the anti-false-pass guard `assertNotDemo` (fixtures.ts) is
 * itself tested. A guard that never fails is worse than none: every UI test
 * that calls it would keep passing against a web that shows a demo, or that
 * cannot reach the backend.
 *
 * No web is needed: each case renders one marker with page.setContent (the
 * markup the web renders, contract contracts/features/PAC-83.md §5.1) and
 * expects the guard to fail; a page with none of them, and a page whose only
 * inline error is a non-"lost" kind, must pass.
 */

import { assertNotDemo, expect, test } from '../fixtures';

const page = (body: string) => `<!doctype html><html><body><main>${body}</main></body></html>`;

const MARKERS: Array<{ name: string; html: string }> = [
  {
    name: 'the demo label',
    html: '<div data-testid="demo-label">Demo — made-up data</div>',
  },
  {
    name: 'the server-unreachable banner',
    html:
      '<div role="alert" data-testid="server-unreachable-banner">Can\'t reach Paceday right now.' +
      '<button data-testid="server-unreachable-retry">Try again</button></div>',
  },
  {
    name: 'an inline lost-answer error',
    html:
      '<div role="alert" data-testid="api-error" data-error-kind="lost" data-effect="read">' +
      "Can't reach Paceday right now. Check your connection and try again." +
      '<button data-testid="api-error-retry">Try again</button></div>',
  },
  {
    name: 'the pre-PAC-83 banner sentence',
    html: '<div>Backend not reachable — showing demo data</div>',
  },
  { name: 'demo roster name Sarah Chen', html: '<ul><li>Sarah Chen</li></ul>' },
  { name: 'demo roster name Miguel Alvarez', html: '<ul><li>Miguel Alvarez</li></ul>' },
  { name: 'demo roster name Priya Nair', html: '<ul><li>Priya Nair</li></ul>' },
];

test.describe('assertNotDemo guard self-test (PAC-83 AC-28)', () => {
  for (const marker of MARKERS) {
    test(`fails on ${marker.name}`, async ({ page: p }) => {
      await p.setContent(page(`<h1>Dashboard</h1>${marker.html}`));
      // The marker really is on the page (so a failure below is the guard's).
      await expect(p.locator('main')).not.toBeEmpty();
      let failure: unknown;
      try {
        await assertNotDemo(p);
      } catch (e) {
        failure = e;
      }
      expect(failure, `assertNotDemo passed on a page showing ${marker.name}`).toBeInstanceOf(Error);
    });
  }

  test('passes on a page with no marker', async ({ page: p }) => {
    await p.setContent(page('<h1>Dashboard</h1><p>E2E Primary Owner</p>'));
    await assertNotDemo(p);
  });

  test('passes on an inline error that is not a lost answer', async ({ page: p }) => {
    // A server answer shown honestly (budget, too-large, http) is the real
    // backend talking; only "lost" means it could not be reached.
    await p.setContent(
      page(
        '<div role="alert" data-testid="api-error" data-error-kind="budget" data-effect="read">' +
          'This took too long and was stopped. Try again.</div>',
      ),
    );
    await assertNotDemo(p);
  });
});
