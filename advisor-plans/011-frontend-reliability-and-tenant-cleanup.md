# Plan 011: Fix frontend analytics, SSE, tenant cleanup, and mutation pressure

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition. Preserve unrelated dirty frontend changes.
>
> **Drift check (run first)**: `git -C frontend diff --stat 22f7c58..HEAD -- app/(sass)/dashboard/analytics/page.tsx hooks/use-event-source.ts contexts/auth-context.tsx hooks/use-org-id.ts lib/api/customers.ts hooks/use-payments.ts hooks/use-payouts.ts`

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED
- **Depends on**: none
- **Category**: bug | perf | tests
- **Planned at**: frontend `22f7c58`, 2026-07-11

## Why this matters

Analytics queries can reuse the wrong period or fall back to month data before custom dates exist. SSE reconnect timers can survive cleanup, tenant state remains after memberships disappear, and bulk deletion launches unbounded requests. These failures produce stale data, excess traffic, or wrong-tenant UI state.

## Current state

- `frontend/app/(sass)/dashboard/analytics/page.tsx:241-287` branches on `isCustom` but omits it from keys and readiness gating.
- `frontend/hooks/use-event-source.ts:51-63,104-113` schedules reconnects without a generation/current-source guard.
- `frontend/contexts/auth-context.tsx:68-72` returns early when memberships are empty.
- `frontend/lib/api/customers.ts:184-204` uses `Promise.allSettled(ids.map(deleteCustomer))`.
- Money mutation hooks in `hooks/use-payments.ts:175-351` and `hooks/use-payouts.ts:35-153` lack focused hook/integration coverage.

## Scope

In scope: the listed analytics/SSE/auth/customer files, bounded deletion or bulk API integration, and tests for mode/org switches and mutation invalidation.

Out of scope: unrelated animation changes, backend API redesign unless a small bulk endpoint is required and separately approved, and visual redesign.

## Steps

### Step 1: Correct analytics keys and enablement

Include `isCustom`, start, and end in affected query keys. Disable custom queries until both dates exist and add tests for custom/rolling toggles.

**Verify**: frontend unit tests demonstrate distinct cache entries and no month fallback.

### Step 2: Make SSE cleanup authoritative

Guard reconnect callbacks by active generation/current source and cancel timers during cleanup, visibility changes, and scope changes.

**Verify**: hook tests assert no EventSource is created after unmount or hidden-tab cleanup.

### Step 3: Clear stale tenant state

Clear active organization and persisted org scope when memberships disappear or authentication becomes anonymous. Add an auth-to-anonymous-to-login test.

**Verify**: test confirms old org ID cannot keep scope-enabled queries active.

### Step 4: Bound customer deletion and mutation coverage

Use bounded concurrency/chunking or an approved bulk endpoint. Add characterization tests for deletion progress, partial failures, idempotency, and representative payout/refund/API-key mutations.

**Verify**: `pnpm test`, `pnpm typecheck`, and `pnpm lint` → exit 0; lint warnings may remain but no errors.

## Done criteria

- [ ] Analytics periods never reuse incompatible cache entries.
- [ ] SSE cannot reconnect after cleanup.
- [ ] Tenant state clears on membership loss.
- [ ] Bulk deletion is bounded and money mutations have regression coverage.

## STOP conditions

- Fixing tenant cleanup would change the public auth contract.
- A bulk endpoint is required but backend scope/contract is not approved.
- Existing animation dirty changes conflict with in-scope files.

## Maintenance notes

Keep query keys and auth scope keys explicit; add a regression test whenever a new mode- or org-dependent query is introduced.
