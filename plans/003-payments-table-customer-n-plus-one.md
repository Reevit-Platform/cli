# Plan 003: Eliminate per-row customer fetches on the payments table

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: in `frontend/`, run
> `git diff --stat 5bd5ced..HEAD -- "app/(sass)/dashboard/payments/components/payments-table-columns.tsx" hooks/use-customers.ts types/` and in `backend/`, `git diff --stat 61332193..HEAD -- internal/usecase/payments/ adapters/http/handlers_payments.go db/queries/`
> On any in-scope change, re-verify the "Current state" excerpts first.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**: none
- **Category**: perf
- **Planned at**: frontend `5bd5ced`, backend `61332193`, 2026-07-07

## Why this matters

The payments table renders a `CustomerNameCell` per row that issues `GET /v1/customers/{id}` whenever the row lacks a preloaded `customer_name`. Regular payment rows usually carry `customer_name` from the list response, but **scheduled-payment rows always fetch** (the cell explicitly passes `undefined` for them), and any regular row with a null name fetches too. At the page's `limit: 100`, a scheduled tab full of rows fires up to ~100 serialized detail requests per render (React Query dedupes identical ids, so N = distinct customers). The fix is to enrich the scheduled-payments list response with `customer_name` server-side — the same enrichment the main payments list already has — and pass it through.

## Current state

**Frontend** (paths relative to `frontend/`):

- `app/(sass)/dashboard/payments/components/payments-table-columns.tsx:102-104`:
  ```tsx
  function CustomerNameCell({ customerId, name }: { customerId?: string; name?: string | null }) {
    const { data: customer, isLoading } = useCustomer(name ? null : customerId || null);
  ```
- Same file, ~lines 240-251 — the cell wiring; scheduled rows never get a name:
  ```tsx
  const preloadedName =
    "rowType" in payment && payment.rowType === "scheduled"
      ? undefined
      : ((payment as Payment).customer_name ?? undefined);
  return <CustomerNameCell customerId={row.getValue("customer_id") as string} name={preloadedName} />;
  ```
- `hooks/use-customers.ts:58-65` — `useCustomer(id: string | null)` is a standard scope-keyed React Query hook; `enabled: !!id`. There is **no batch "customers by ids" endpoint** in `lib/api/customers.ts` (create/update/get/list/delete only).
- The scheduled-payment type lives in `types/` (find it via `grep -rn "rowType" app/\(sass\)/dashboard/payments/` and `grep -rn "ScheduledPayment" types/`).

**Backend** (paths relative to `backend/`):

- Scheduled payments list: `GET /v1/payments/scheduled` — handler in `adapters/http/handlers_payments.go`, usecase in `internal/usecase/payments/` (scheduled-payment methods; find via `grep -rn "scheduled" internal/usecase/payments/*.go | grep -i list`), queries in `db/queries/` (`grep -rn "scheduled_payments" db/queries/`).
- The main payments list already returns `customer_name` — locate how it's enriched (`grep -rn "customer_name" db/queries/payments.sql internal/usecase/payments/`) and mirror that mechanism (usually a LEFT JOIN on `customers` in the sqlc query, or a post-query enrichment pass).
- Backend conventions (from `backend/CLAUDE.md`): before every commit run `golangci-lint run ./...` (0 issues) and `go test ./...`. DB tests need `TEST_DATABASE_URL=postgres://reevit:reevit@localhost:5433/reevit_test?sslmode=disable`. sqlc regeneration: `docker run --rm -v $PWD:/src -w /src sqlc/sqlc:latest generate`. New migrations must be numbered **000135+** (the test DB has phantom goose versions 118–125 and 129; current max is 000134). This change should NOT need a migration — it's a query change.

## Commands you will need

| Purpose | Where | Command | Expected |
|---|---|---|---|
| Backend lint | backend/ | `golangci-lint run ./...` | 0 issues |
| Backend tests | backend/ | `TEST_DATABASE_URL=postgres://reevit:reevit@localhost:5433/reevit_test?sslmode=disable go test ./...` | all pass |
| sqlc regen | backend/ | `docker run --rm -v $PWD:/src -w /src sqlc/sqlc:latest generate` | exit 0, diff only generated files |
| Frontend typecheck | frontend/ | `npx tsc --noEmit` | 0 errors |
| Frontend tests | frontend/ | `npm run test` | all pass |

## Scope

**In scope**:
- backend: the scheduled-payments list query in `db/queries/` + regenerated sqlc output, the scheduled list usecase/repo mapping, the HTTP response DTO in `adapters/http/handlers_payments.go` (add `customer_name`), `openapi` spec if the repo maintains one for this endpoint (`grep -n "scheduled" backend/api/openapi.yaml` — check `docs/OPENAPI.md` for the compatibility process)
- frontend: `types/` scheduled-payment type (+ regenerate `types/api.ts` via `npm run generate:types` if the openapi spec feeds it), `payments-table-columns.tsx` (pass the name for scheduled rows), a colocated test
- `frontend/CLAUDE.md` and `backend/CLAUDE.md` changelog entries

**Out of scope** (do NOT touch):
- `CustomerNameCell`'s fallback fetch — keep it; it covers legacy rows and detail-sheet reuse.
- The main payments list query (already enriched).
- Pagination for the scheduled tab (documented decision: `limit: 100`, no pager).
- Building a batch `GET /customers?ids=` endpoint — rejected as unnecessary once the list is enriched.

## Git workflow

- Backend branch from `origin/dev`: `perf/scheduled-payments-customer-name`; frontend branch from `origin/dev-frontend`: `perf/payments-table-customer-name`. Conventional commits (`feat(scheduled-payments): include customer_name in list response`).
- **Backend caution** (documented repo hazard): the backend checkout is shared — before committing, re-grep your changes in `adapters/http/router.go` and `cmd/` mains to ensure no stale buffers clobbered hot files.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Confirm the gap server-side

Inspect the scheduled list response DTO in `adapters/http/handlers_payments.go` and the sqlc query it uses. Confirm `customer_name` is absent. If it is already present, STOP — the fix is frontend-only (skip to Step 4, sourcing the name from the existing field).

**Verify**: `grep -n "customer_name" <the scheduled list query file and DTO>` → no matches on the scheduled path.

### Step 2: Enrich the query

Mirror the main payments list's enrichment: add a LEFT JOIN to `customers` (match on the same key the main list uses — customer id + org scoping) returning `customer_name` in the scheduled list query. Regenerate sqlc. Thread the field through the repo mapping and usecase DTO to the HTTP response as `customer_name *string` / `omitempty`, matching the main list's JSON shape exactly (check its json tag).

**Verify**: `golangci-lint run ./...` → 0 issues; `go test ./...` (with TEST_DATABASE_URL) → green.

### Step 3: Backend test

Extend the existing scheduled-payments list test (find via `grep -rln "payments/scheduled" adapters/http/ internal/usecase/payments/ | grep test`) with a case seeding a customer and asserting `customer_name` in the list response, plus a null-customer row asserting the field is empty/omitted.

**Verify**: targeted `go test ./... -run <TestName>` → pass; full suite green.

### Step 4: Frontend pass-through

Add `customer_name?: string | null` to the scheduled-payment type (or regenerate `types/api.ts` if openapi-driven — preserve `undefined`, do NOT coerce to `null`; this is a documented repo rule for fields whose backend deploy is decoupled). In `payments-table-columns.tsx`, change the `preloadedName` expression so scheduled rows use their own `customer_name` when present:
```tsx
const preloadedName =
  "rowType" in payment && payment.rowType === "scheduled"
    ? ((payment as ScheduledPaymentRow).customer_name ?? undefined)
    : ((payment as Payment).customer_name ?? undefined);
```

**Verify**: `npx tsc --noEmit` → 0 errors.

### Step 5: Frontend test + changelogs

Add `payments-table-columns.test.tsx` (colocated; model after any existing columns/sections test, e.g. `app/(sass)/dashboard/developers/components/api-keys-sections.test.tsx`): mock `useCustomer` and assert it receives `null` (i.e. no fetch) when a scheduled row carries `customer_name`, and receives the id when the name is absent. Update both CLAUDE.md changelogs.

**Verify**: `npm run test` → all pass including the new file.

## Test plan

Covered in steps 3 and 5: backend list-enrichment happy path + null-customer case; frontend no-fetch-when-preloaded + fallback-fetch-when-missing. Structural patterns: existing scheduled-payments backend list tests; `api-keys-sections.test.tsx` for the frontend.

## Done criteria

- [ ] Backend: scheduled list response includes `customer_name`; lint 0 issues; full `go test ./...` green
- [ ] Frontend: `npx tsc --noEmit` 0 errors; `npm run test` green incl. new test
- [ ] The `useCustomer` mock assertion proves scheduled rows with names issue zero customer fetches
- [ ] `undefined` preserved (no `?? null` coercion) on the new optional field
- [ ] Only in-scope files modified in both repos (`git status` each)
- [ ] `plans/README.md` status row updated

## STOP conditions

- Step 1 finds `customer_name` already in the scheduled response → frontend-only fix; report the reduced scope and continue from Step 4.
- The scheduled list query doesn't have access to a customer identifier that joins cleanly to `customers` (e.g. it stores only a free-text destination) → report; the fallback fetch stays and this plan is REJECTED.
- The openapi compatibility check (`docs/OPENAPI_COMPATIBILITY.md` process) flags the field addition as breaking → report before merging.
- Backend tests fail for reasons unrelated to your change (e.g. test DB not running on :5433) → report the environment issue, do not "fix" unrelated tests.

## Maintenance notes

- If a batch customers endpoint ever lands, `CustomerNameCell`'s per-id fallback should migrate to it; until then the fallback is the safety net for legacy rows.
- Reviewer: confirm the JOIN respects org scoping identically to the main payments list (multi-tenant invariant) and mode isolation.
- Deferred: the same per-row-fetch pattern may exist on other tables (subscriptions/customers columns) — worth a grep for `useCustomer(` in cell components after this lands.
