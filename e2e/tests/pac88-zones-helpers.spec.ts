/**
 * PAC-88 AC-19 helper checks (no browser, no stack): the DST views are
 * visited in calendar order per display zone whichever zone changes first.
 * Before this, the journey visited New York's change days first, and the
 * calendar could only move forwards, so it broke whenever Paris changed
 * before New York (about mid-March to late October).
 */

import { test, expect } from '@playwright/test';

import { dstViews, nextTransition } from '../lib/zones';

const ZONES = ['America/New_York', 'Europe/Paris'];

for (const [run, nyChange, parisChange, nyDays, parisDays] of [
  [
    '2027-03-20T12:00:00Z',
    '2027-11-07T06:00:00.000Z',
    '2027-03-28T01:00:00.000Z',
    ['2027-03-26', '2027-03-27', '2027-03-28', '2027-11-06', '2027-11-07', '2027-11-08'],
    ['2027-03-27', '2027-03-28', '2027-03-29', '2027-11-06', '2027-11-07', '2027-11-08'],
  ],
  [
    '2027-05-01T12:00:00Z',
    '2027-11-07T06:00:00.000Z',
    '2027-10-31T01:00:00.000Z',
    ['2027-10-29', '2027-10-30', '2027-10-31', '2027-11-06', '2027-11-07', '2027-11-08'],
    ['2027-10-30', '2027-10-31', '2027-11-01', '2027-11-06', '2027-11-07', '2027-11-08'],
  ],
  [
    '2027-10-28T12:00:00Z',
    '2027-11-07T06:00:00.000Z',
    '2027-10-31T01:00:00.000Z',
    ['2027-10-29', '2027-10-30', '2027-10-31', '2027-11-06', '2027-11-07', '2027-11-08'],
    ['2027-10-30', '2027-10-31', '2027-11-01', '2027-11-06', '2027-11-07', '2027-11-08'],
  ],
] as Array<[string, string, string, string[], string[]]>) {
  test(`AC-19 views run on ${run.slice(0, 10)}: each zone's days in calendar order`, () => {
    const now = new Date(run);
    // Paris changes first on all three dates: the case that broke.
    expect(nextTransition('America/New_York', 2, now).toISOString()).toBe(nyChange);
    expect(nextTransition('Europe/Paris', 2, now).toISOString()).toBe(parisChange);

    const views = dstViews(ZONES, 2, now);
    expect(views.filter((v) => v.zone === 'America/New_York').map((v) => v.day)).toEqual(nyDays);
    expect(views.filter((v) => v.zone === 'Europe/Paris').map((v) => v.day)).toEqual(parisDays);
    // Zone by zone, in the zones' order, never going back within a zone.
    expect(views.map((v) => v.zone)).toEqual([...Array(6).fill(ZONES[0]), ...Array(6).fill(ZONES[1])]);
  });
}
