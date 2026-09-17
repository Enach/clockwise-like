/**
 * Programmatic sign-in.
 *
 * ── What the backend actually accepts ────────────────────────────────────────
 * backend/api/middleware.go requireAuth() takes the token from, in order:
 *   1. the `auth_token` cookie
 *   2. an `Authorization: Bearer <token>` header
 * and then calls auth.ValidateToken(token, JWT_SECRET) — backend/auth/jwt.go —
 * which requires HS256 (it rejects any other alg explicitly) and reads two
 * claims:
 *   sub   -> the user UUID, which must parse as a uuid
 *   email -> copied into the request context
 * plus the standard exp/iat. Nothing else is checked: there is no server-side
 * session store, no token revocation list, no nonce. A correctly signed JWT IS
 * a session.
 *
 * So the honest programmatic login is: mint the same JWT the backend's own
 * issueJWT() would mint (backend/api/handlers_auth.go) and set it as the same
 * cookie, with the same attributes. That is not a mock — the token goes through
 * the real middleware, the real signature check and the real claim parsing.
 *
 * We deliberately do NOT drive the Google consent screen. Doing so would need a
 * real Google account, a real client secret and network egress, and would test
 * Google rather than Paceday. The one thing this approach does not cover is the
 * OAuth callback handler itself; that gap is named in README.md.
 *
 * The JWT is signed with node:crypto rather than a JWT library on purpose —
 * HS256 is an HMAC over two base64url segments, and hand-rolling it keeps the
 * harness free of a dependency whose version would need pinning and auditing.
 */

import { createHmac } from 'node:crypto';
import type { BrowserContext, Cookie } from '@playwright/test';

import { USERS } from './seed/ids';

/** Must match JWT_SECRET in docker-compose.e2e.yml. */
export const JWT_SECRET =
  process.env.E2E_JWT_SECRET ?? 'e2e-only-jwt-secret-not-for-any-real-deployment';

export const BASE_URL = process.env.E2E_BASE_URL ?? 'http://localhost:8088';

function b64url(input: Buffer | string): string {
  return Buffer.from(input)
    .toString('base64')
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}

export interface SessionUser {
  id: string;
  email: string;
  name: string;
}

/**
 * Mint a session token for `user`.
 *
 * @param ttlSeconds defaults to 7 days, matching auth.GenerateToken.
 */
export function mintSessionToken(user: SessionUser, ttlSeconds = 7 * 24 * 3600): string {
  const now = Math.floor(Date.now() / 1000);
  const header = b64url(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
  // Field order mirrors backend/auth/jwt.go Claims. Note that `sub` carries the
  // user id: Claims.UserID has the `sub` json tag and shadows the embedded
  // RegisteredClaims.Subject, because Go's encoding/json prefers the shallower
  // field on a tag collision.
  const payload = b64url(
    JSON.stringify({
      sub: user.id,
      email: user.email,
      exp: now + ttlSeconds,
      iat: now,
    }),
  );
  const signature = b64url(
    createHmac('sha256', JWT_SECRET).update(`${header}.${payload}`).digest(),
  );
  return `${header}.${payload}.${signature}`;
}

/** An already-expired token, for asserting that the guard actually guards. */
export function mintExpiredSessionToken(user: SessionUser): string {
  return mintSessionToken(user, -3600);
}

function sessionCookie(token: string): Cookie {
  const url = new URL(BASE_URL);
  return {
    name: 'auth_token',
    value: token,
    domain: url.hostname,
    path: '/',
    expires: Math.floor(Date.now() / 1000) + 7 * 24 * 3600,
    // Mirrors backend/api/handlers_auth.go issueJWT() exactly.
    httpOnly: true,
    secure: false,
    sameSite: 'Lax',
  };
}

/** Attach a session to a live browser context. */
export async function signIn(context: BrowserContext, user: SessionUser): Promise<void> {
  await context.addCookies([sessionCookie(mintSessionToken(user))]);
}

/**
 * The client-side state the app itself writes once a user has been through
 * onboarding, reproduced here so authenticated navigations land where a
 * returning user would land.
 *
 * This is NOT a mock of server data — it is genuine per-browser preference
 * state that the frontend keeps in localStorage and never fetches:
 *   - `paceday:manager:v1`.profile.is_manager is read synchronously by
 *     src/pages/Team.tsx, which renders "Manager mode is off" when it is false,
 *     regardless of what GET /api/manager/profile says.
 *   - `onboarding_profile_selected` gates components/RequireAuth.tsx. The
 *     backend has no such field; src/api/manager.ts derives it from
 *     `detected_at` OR this stored value. seed.sql sets detected_at too, so
 *     this is belt-and-braces rather than the only thing holding it up.
 *
 * Both are listed in TESTIDS-REQUIRED.md as things the backend should own.
 * The `members` array is left EMPTY on purpose: src/api/manager.ts ships demo
 * members ("Sarah Chen", ...) and seeding them here would let a roster test
 * pass against local state instead of the API.
 */
export function onboardedLocalStorage(isManager: boolean) {
  return [
    {
      name: 'paceday:manager:v1',
      value: JSON.stringify({
        profile: { is_manager: isManager, onboarding_profile_selected: true },
        members: [],
      }),
    },
  ];
}

/**
 * A complete Playwright storageState for `user`, built without launching a
 * browser. Used by playwright.config.ts projects and by fixtures.ts.
 */
export function storageStateFor(user: SessionUser, opts: { isManager?: boolean } = {}) {
  const url = new URL(BASE_URL);
  return {
    cookies: [sessionCookie(mintSessionToken(user))],
    origins: [
      {
        origin: url.origin,
        localStorage: onboardedLocalStorage(opts.isManager ?? false),
      },
    ],
  };
}

export const PRIMARY_SESSION = () => storageStateFor(USERS.primary, { isManager: true });
export const SECOND_SESSION = () => storageStateFor(USERS.second, { isManager: false });
