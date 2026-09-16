# `backend/api/gen` — generated, committed, not editable

`paceday.gen.go` is produced by `oapi-codegen` from
`contracts/openapi/openapi.yaml`. It is committed so that a consumer never needs
the generator and so drift is a diff rather than a discovery
(`docs/factory/README.md` §4).

- regenerate: `make openapi`
- prove it is in sync: `make openapi-check` (also run by `make verify` and by
  the pre-commit hook whenever `contracts/` or this directory changes)

Do not edit anything here. If the generated code is wrong, the contract is
wrong — fix `contracts/openapi/paths/<domain>.yaml` and regenerate
(`docs/factory/README.md` §3, rule 3).

Which handlers actually use this package is tracked, one row per operation, in
`contracts/openapi/MIGRATION.md`. The package is generated in full from day one;
handlers adopt `StrictServerInterface` one endpoint at a time.
