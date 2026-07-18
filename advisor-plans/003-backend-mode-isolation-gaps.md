# Plan 003: Close backend mode-isolation gaps in webhooks, refunds, status, and analytics

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition and report. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git -C backend diff --stat 9de2f570..HEAD -- adapters/webhook/mpesa.go adapters/http/handlers_webhooks.go adapters/repo/refund_repo_sqlc.go db/queries/refunds.sql adapters/repo/payment_status_history_repo.go adapters/repo/payment_repo_analytics.go internal/usecase/payments`

## Status

- **Priority**: P0
- **Effort**: L
- **Risk**: HIGH
- **Depends on**: advisor-plans/002-public-payment-mode-and-secret-redaction.md
- **Category**: security | bug | tests
- **Planned at**: backend `9de2f570`, 2026-07-11

## Why this matters

Several repository paths rely on `modeFromContext`, but webhook parsing and child-record queries run before a mode principal exists or omit the parent payment mode entirely. This can break live M-Pesa callbacks and expose same-org live data in sandbox views. Analytics latency/error queries have the same omission.

## Current state

- `backend/adapters/webhook/mpesa.go:43-46` looks up by provider reference before mode is resolved.
- `backend/db/queries/refunds.sql:18-27` and `refund_repo_sqlc.go:142-180` omit the parent payment mode.
- Payment status history similarly keys only on payment/org.
- `backend/adapters/repo/payment_repo_analytics.go:12-77` lacks the mode predicate, unlike `:106-124`.

## Scope

In scope: M-Pesa parser/handler mode resolution, refund SQL/repository methods, payment status-history reads, latency/error analytics queries, and PostgreSQL regression tests.

Out of scope: changing mode vocabulary, deleting test data, or weakening org checks.

## Steps

### Step 1: Resolve M-Pesa connection and mode before payment lookup

Use the verified connection/provider context to perform a mode-aware provider-reference lookup, then attach the resolved org/mode principal before applying event side effects.

**Verify**: add live and sandbox M-Pesa callback tests with identical provider references → each updates only its own payment.

### Step 2: Add parent-mode predicates to child records

Update refund and status-history queries to join `payments` and filter the mode from context. Preserve org and identifier predicates.

**Verify**: same-org live/sandbox fixtures return only the active mode in repository integration tests.

### Step 3: Scope analytics queries

Add mode predicates and parameters to latency and error breakdown queries, matching the existing error-series/routing pattern.

**Verify**: analytics integration tests seed both modes and assert counts are isolated.

### Step 4: Run generated-query and full package gates

Regenerate sqlc artifacts only if required by the repository workflow, then run focused and full backend tests with PostgreSQL.

**Verify**: `go test ./adapters/webhook ./adapters/repo ./internal/usecase/payments ./adapters/http` and `go test ./...` → exit 0.

## Done criteria

- [ ] M-Pesa live callbacks resolve live payments.
- [ ] Refund and status-history reads cannot cross modes.
- [ ] Analytics latency/errors are mode-scoped.
- [ ] PostgreSQL tests cover same-org cross-mode fixtures.

## STOP conditions

- A query cannot determine mode from a trusted connection/payment relationship.
- Generated SQL and handwritten queries disagree about mode semantics.
- Any proposed fix removes an org or identifier predicate.

## Maintenance notes

Use the parent payment mode as the source of truth for child resources that do not store their own mode column.
