# Plan 009: Add database-backed concurrency gates for money and auth flows

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git -C backend diff --stat 9de2f570..HEAD -- internal/testing adapters/repo internal/usecase db/migrations`

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: HIGH
- **Depends on**: advisor-plans/002-public-payment-mode-and-secret-redaction.md, advisor-plans/003-backend-mode-isolation-gaps.md, advisor-plans/004-payout-auth-and-quota-guardrails.md
- **Category**: tests | security
- **Planned at**: backend `9de2f570`, 2026-07-11

## Why this matters

The backend review plan explicitly leaves PostgreSQL tests unchecked for stale dunning claims, refund CAS races, session rotation, live quota reservation, and payment-link races. These are database-atomicity properties that mocks cannot prove.

## Current state

- Unchecked test tasks are recorded in `backend/docs/superpowers/plans/backend-review-fixes-2026-07-10.md:20-22,31-33,41-43,64-78`.
- Existing integration tests use shared setup in `backend/internal/testing/db.go` and skip when database services are unavailable.

## Scope

In scope: PostgreSQL test fixtures and tests for the listed race/atomicity paths, CI service configuration, and one documented command.

Out of scope: changing production transaction semantics unless a test demonstrates a defect.

## Steps

### Step 1: Build a reproducible PostgreSQL test command

Provide a single command that starts or targets PostgreSQL/Redis, applies migrations, and runs the integration suite without silently skipping required tests.

**Verify**: the command fails clearly when services are absent and runs the selected suite when present.

### Step 2: Add concurrency tests

Cover recent-vs-stale dunning claims, concurrent refund transitions, refresh-token replay, last-unit quota reservation, and payment-link max-use races. Assert row counts and authoritative outcomes.

**Verify**: each test passes repeatedly under `-race` where applicable and fails against the pre-fix behavior in a fixture or focused regression check.

### Step 3: Wire CI

Run the integration command in backend CI with PostgreSQL/Redis services and prohibit `t.Skip` for required race tests.

**Verify**: CI-equivalent local command reports all new tests executed, not skipped.

## Done criteria

- [ ] All five critical race classes have PostgreSQL-backed tests.
- [ ] A one-command fixture is documented and used in CI.
- [ ] Missing services cause an explicit failure, not a green skip.

## STOP conditions

- A test requires production credentials or live PSP calls.
- The database fixture cannot isolate test/live/org data.
- Existing tests rely on silent skips that cannot be separated safely.

## Maintenance notes

Keep race tests close to repository/usecase code and retain a small deterministic fixture dataset.
