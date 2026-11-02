/**
 * PAC-84: the committed, TEST-ONLY encryption keys of the e2e suite
 * (contract section 11). They appear only under e2e/ (and the `#:` line of
 * E2E_ENCRYPTION_KEY in .env.example). The backend and paceday-operator
 * refuse their key bytes, whatever the key id, unless
 * PACEDAY_ENCRYPTION_ALLOW_TEST_KEY is exactly "true" (contract 9.1, AC-30).
 */

/** PACEDAY_ENCRYPTION_KEY of the e2e stack: 32 ASCII bytes "paceday-e2e-test-key-NOT-SECRET!". */
export const E2E_ENCRYPTION_KEY = 'e2e-test:cGFjZWRheS1lMmUtdGVzdC1rZXktTk9ULVNFQ1JFVCE=';

/** A second test key for rotation / wrong-key cases: "paceday-e2e-test-key-2-NOTSECRET". */
export const E2E_ENCRYPTION_KEY_2 = 'e2e-test-2:cGFjZWRheS1lMmUtdGVzdC1rZXktMi1OT1RTRUNSRVQ=';

/** The opt-in that lets a backend start with either key above. */
export const ALLOW_TEST_KEY = 'PACEDAY_ENCRYPTION_ALLOW_TEST_KEY';

/** The runbook section every encryption refusal names. */
export const RUNBOOK_ENCRYPTION_KEY = 'docs/runbook.md, "Encryption key"';
