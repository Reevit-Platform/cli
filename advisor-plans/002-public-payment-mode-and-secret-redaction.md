# Plan 002: Make public payment flows mode-safe and redact secret-bearing URLs

> **Executor instructions**: Follow this plan step by step. Stop and report on any STOP condition. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git -C backend diff --stat 9de2f570..HEAD -- adapters/http/router.go adapters/http/middleware_wide_event.go adapters/http/handlers_payments.go internal/usecase/payments/checkout_sessions.go internal/usecase/payments/service.go internal/usecase/payments/service_hubtel.go adapters/repo/payment_repo_sqlc.go db/queries/payments.sql`

## Status

- **Priority**: P0
- **Effort**: M
- **Risk**: HIGH
- **Depends on**: none
- **Category**: security | bug
- **Planned at**: backend `9de2f570`, 2026-07-11

## Why this matters

Unauthenticated checkout-session, confirm-intent, and Hubtel public flows call mode-filtered lookups with a raw context, which defaults to sandbox. Live customers therefore cannot complete payment. The public session secret is also embedded in the URL and currently reaches wide-event logging; the secret must not be retained in request paths or traces.

## Current state

- `backend/adapters/http/router.go:401-406` exposes public session and confirm routes without a mode principal.
- `backend/internal/usecase/payments/checkout_sessions.go:37-49,73-78` and `service_hubtel.go:20-45` call `GetByIDPublic` on raw context.
- `backend/db/queries/payments.sql:59-68` requires a mode parameter.
- `backend/adapters/http/middleware_wide_event.go:34-42` records `r.URL.Path`, which contains the session secret.

## Scope

In scope: public checkout-session, confirm-intent, and Hubtel lookup code; payment SQL/query helpers; wide-event path redaction and its tests.

Out of scope: changing public URL shapes, payment response contracts, or unrelated tracing fields.

## Steps

### Step 1: Add a safe client-secret resolver

Implement one resolver that validates the client secret against the payment/session across modes, returns the resolved payment and mode, and never accepts a caller-provided mode as authority. Carry the resolved mode into subsequent confirmation/provider calls.

**Verify**: focused payment and checkout tests cover live and sandbox secrets and reject mismatched secrets → all pass.

### Step 2: Route all public flows through the resolver

Update checkout-session retrieval, confirm-intent, and Hubtel public session creation to use the resolver. Preserve existing error codes and mode isolation.

**Verify**: `go test ./internal/usecase/payments ./adapters/http` → exit 0 with live/sandbox regression tests present.

### Step 3: Redact secret-bearing request paths

Change wide-event serialization to redact known public-secret route segments while preserving route identity and query-free diagnostics. Add a regression test asserting the secret never appears in the event payload.

**Verify**: `go test ./adapters/http -run 'WideEvent|Checkout|Confirm'` → exit 0; `rg -n 'session_secret|client_secret' backend/internal/infra backend/adapters/http/middleware_wide_event.go` shows no raw path logging.

## Done criteria

- [ ] Live public checkout, confirm-intent, and Hubtel flows resolve correctly.
- [ ] Client-secret mismatch cannot cross org or mode boundaries.
- [ ] Wide-event logs contain no checkout/session secret.
- [ ] Tests cover both modes and redaction.

## STOP conditions

- The current public secret format cannot be validated without changing a public contract.
- A resolver change requires trusting a caller-supplied mode.
- Any test or log still exposes a secret value.

## Maintenance notes

Keep the resolver as the single authority for public client-secret flows. New public payment endpoints must use it rather than calling mode-filtered repositories directly.
