# Contract — PAC-43: calendar-dependent behaviour must be verifiable before release

- **Spec**: `docs/specs/PAC-43.md` (accepted: v3 + V3-7, 2026-10-11)
- **Plan**: `docs/factory/PAC-43-plan.md` (v3)

**No change to the HTTP contract.** PAC-43 changes only how the backend reaches Google Calendar (an outbound client): the configured provider address, a connection-time address guard for test deployments, refused redirects and ignored ambient proxies (spec §V3-2/§V3-3, E1–E4). No operation, schema, status code or field in `contracts/openapi/` is added, removed or altered, so there is no feature manifest and nothing to regenerate. Recorded here so the stage-2 gate has an artifact (plan §0).
