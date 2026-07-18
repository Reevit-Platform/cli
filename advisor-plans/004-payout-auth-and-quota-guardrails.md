# Plan 004: Restore payout session access and fail closed on live quota reservation

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git -C backend diff --stat 9de2f570..HEAD -- adapters/http/middleware_auth_combined.go adapters/http/router.go internal/usecase/billing/gatekeeper.go internal/usecase/payments/service.go internal/usecase/payments/service_test.go`

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: HIGH
- **Depends on**: none
- **Category**: security | bug | tests
- **Planned at**: backend `9de2f570`, 2026-07-11

## Why this matters

Dashboard sessions receive an all-scopes principal that omits the newly shipped payout scopes, so payout pages are unauthorized despite valid sessions. Separately, a missing reservation capability silently downgrades live quota enforcement to a check-then-act path. Both failures are preventable at wiring and readiness boundaries.

## Current state

- `backend/adapters/http/middleware_auth_combined.go:160-181` enumerates session scopes but omits `payouts:read` and `payouts:write`.
- `backend/adapters/http/router.go:1020-1041` requires those scopes for payout/beneficiary routes.
- `backend/internal/usecase/billing/gatekeeper.go:184-187` returns nil when the reservation interface is absent.
- `backend/internal/usecase/payments/service.go:989-997` falls back to `CanCreatePayment`.

## Scope

In scope: session scope construction, production readiness validation, gatekeeper behavior, and route/quota tests.

Out of scope: changing API-key scope semantics or removing the reservation table.

## Steps

### Step 1: Add payout scopes to trusted dashboard sessions

Include both payout scopes in the all-scopes principal and add authenticated-session tests for read and write payout routes.

**Verify**: route tests assert a session reaches the handler rather than receiving missing-scope 403.

### Step 2: Make reservation capability mandatory for live production

Change the production readiness check to require the reservation capability when live billing is enabled, and make runtime absence return a typed billing-unavailable error rather than nil.

**Verify**: unit tests cover missing capability, reservation failure, and non-production compatibility.

### Step 3: Run billing and auth gates

Run focused tests, then the backend suite with PostgreSQL.

**Verify**: `go test ./adapters/http ./internal/usecase/billing ./internal/usecase/payments` and `go test ./...` → exit 0.

## Done criteria

- [ ] Session-authenticated payout and beneficiary routes work.
- [ ] Production cannot start or process live payments without atomic reservation support.
- [ ] Tests cover both protections.

## STOP conditions

- Adding scopes grants them to API keys unintentionally.
- Existing non-production test doubles cannot be adapted without changing public interfaces.
- A fail-open path remains for live quota enforcement.

## Maintenance notes

Every new money-moving scope must be added to both API-key policy and the trusted dashboard-session principal, with route tests.
