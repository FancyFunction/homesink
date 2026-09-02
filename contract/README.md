# contract — the WP-0 gate

Milestone **M0**: the wire contract is frozen before either side is built, and
both sides are tested against it. See `docs/08-ROADMAP.md` and `docs/02-API.md §7`.

## What is here

| Path | Role |
|---|---|
| `../docs/api/openapi.yaml` | the normative contract (owned by WP-0; a change re-runs both CI suites) |
| `../backend/internal/testutil/testdata/` | the shared fixtures — one request + one response example per operation and status, plus `manifest.json` mapping each file to its operation |
| `cmd/validate` | loads the spec, structurally validates it, and checks every fixture for coverage and schema conformance |
| `../scripts/sync-fixtures.sh` | copies the fixtures verbatim into `client/app/src/test/resources/fixtures/` (a Gradle task takes this over once WP-C1 lands) |
| `../.github/workflows/contract.yml` | three lanes: `openapi-lint`, `backend-contract`, `client-contract` |

`backend-contract` and `client-contract` run the **same validator** over the
**same bytes** — a field renamed on one side fails the other side's lane.

## Run it locally

```bash
cd contract
go test ./...
go run ./cmd/validate -spec ../docs/api/openapi.yaml -fixtures ../backend/internal/testutil/testdata

../scripts/sync-fixtures.sh
go run ./cmd/validate -spec ../docs/api/openapi.yaml -fixtures ../client/app/src/test/resources/fixtures
```

## Adding or changing an endpoint

1. Edit `docs/api/openapi.yaml` (add an `operationId`).
2. Add `<operationId>.request.json` / `<operationId>.response-<status>.json` under
   `backend/internal/testutil/testdata/` for the request and **every** declared status.
3. Register them in `manifest.json`. Use `"kind": "json"` for JSON bodies (validated
   against the resolved schema) or `"kind": "headers"` for binary / `204` responses
   (the fixture pins the significant headers; schema check skipped).
4. `go test ./...` — the validator fails if any status is left uncovered.
