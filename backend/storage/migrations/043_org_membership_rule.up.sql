-- PAC-148 (docs/specs/PAC-148.md §3.5, contracts/features/PAC-148.md §5.1):
-- end every stored organisation membership that fails the membership rule
-- (domain_confirmed standing, a domain not on the consumer list, and the
-- organisation of the address's own domain), with every organisation-AI
-- confirmation of the accounts concerned (PAC-87's leaving rule).
--
-- One DO block, so the file's work is one statement and one transaction:
--   1. the gate: an SSO provider or an organisation AI configuration on a
--      consumer-list domain aborts the migration before any row changes
--      (the version is left dirty; recovery is docs/runbook.md,
--      "PAC-148 migration gate");
--   2. the detach;
--   3. the counts, by RAISE LOG and RAISE NOTICE.
--
-- The list between the consumer-list markers is domain.GenericDomains() of
-- this release, one sorted, quoted, lower-case entry per line
-- (TestPAC148_AC16_MigrationListIsTheGoList). The domain expression is
-- storage's addrDomainSQL, verbatim. Organisation rows, organisation AI
-- configurations, SSO providers, sso_events, step-up grants and
-- manager_team_members are not touched (spec D-6).
DO $$
DECLARE
    listed text[] := ARRAY[
        -- consumer-list:begin
        'aol.com',
        'fastmail.com',
        'gmail.com',
        'gmx.com',
        'googlemail.com',
        'hotmail.be',
        'hotmail.co.uk',
        'hotmail.com',
        'hotmail.de',
        'hotmail.es',
        'hotmail.fr',
        'hotmail.it',
        'icloud.com',
        'live.be',
        'live.co.uk',
        'live.com',
        'live.de',
        'live.fr',
        'live.it',
        'live.nl',
        'mac.com',
        'mail.com',
        'me.com',
        'msn.com',
        'outlook.com',
        'outlook.de',
        'outlook.es',
        'outlook.fr',
        'outlook.it',
        'proton.me',
        'protonmail.com',
        'windowslive.com',
        'yahoo.co.uk',
        'yahoo.com',
        'yahoo.fr',
        'yandex.com',
        'yandex.ru',
        'zoho.com'
        -- consumer-list:end
    ]::text[];
    blocked text;
    ending uuid[];
    n_confirmations bigint;
BEGIN
    -- The gate (spec §3.5 B-4; the same check as pre-deploy query 2).
    SELECT string_agg(g.kind || ' ' || g.domain, ', ' ORDER BY g.kind, g.domain)
      INTO blocked
      FROM (SELECT 'sso_provider' AS kind, lower(domain) AS domain
              FROM sso_providers WHERE lower(domain) = ANY(listed)
            UNION ALL
            SELECT 'org_ai_configuration', o.domain
              FROM org_ai_configurations c JOIN organizations o ON o.id = c.org_id
             WHERE o.domain = ANY(listed)) g;
    IF blocked IS NOT NULL THEN
        RAISE EXCEPTION 'PAC-148 migration 043 refused: configuration on a consumer-list domain: %', blocked
            USING HINT = 'Nothing was changed. See docs/runbook.md, "PAC-148 migration gate".';
    END IF;

    -- Every stored membership that fails the rule (§2.1), with addrDomainSQL verbatim.
    SELECT coalesce(array_agg(u.id), '{}')
      INTO ending
      FROM users u JOIN organizations o ON o.id = u.org_id
     WHERE NOT (u.address_standing = 'domain_confirmed'
                AND o.domain <> ALL(listed)
                AND o.domain = CASE WHEN strpos(u.email, '@') = 0 THEN ''
                                    ELSE lower(btrim(substr(u.email, strpos(u.email, '@') + 1),
                                         U&'\0020\0009\000A\000B\000C\000D\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'))
                               END);

    DELETE FROM org_ai_confirmations WHERE user_id = ANY(ending);
    GET DIAGNOSTICS n_confirmations = ROW_COUNT;
    UPDATE users SET org_id = NULL WHERE id = ANY(ending);

    RAISE LOG 'PAC-148 migration 043: ended % organisation memberships and % organisation-AI confirmations',
        cardinality(ending), n_confirmations;
    RAISE NOTICE 'PAC-148 migration 043: ended % organisation memberships and % organisation-AI confirmations',
        cardinality(ending), n_confirmations;
END $$;
