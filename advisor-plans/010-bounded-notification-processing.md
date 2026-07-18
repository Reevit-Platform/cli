# Plan 010: Bound event notification processing and preserve mode context

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git -C backend diff --stat 9de2f570..HEAD -- cmd/api/main.go internal/eventstream internal/usecase/notifications`

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED
- **Depends on**: advisor-plans/003-backend-mode-isolation-gaps.md
- **Category**: perf | reliability | bug
- **Planned at**: backend `9de2f570`, 2026-07-11

## Why this matters

Each published event starts an unbounded goroutine with `context.Background()` for notification creation. Bursts can exhaust resources, shutdown can drop work, and live events lose their mode context during customer enrichment.

## Current state

- `backend/cmd/api/main.go:725-728` starts one goroutine per event.
- `backend/internal/eventstream/broker.go:131-175` invokes hooks synchronously after fan-out.
- Notification creation performs repository lookups at `backend/internal/usecase/notifications/service.go:75-114`.

## Scope

In scope: event-to-notification dispatch, bounded worker/queue lifecycle, mode/org context propagation, retries, and load/shutdown tests.

Out of scope: changing SSE delivery semantics or event payload schemas.

## Steps

### Step 1: Introduce bounded dispatch

Use an existing durable queue where appropriate, or a bounded worker pool with explicit queue capacity and shutdown draining. Record failures for retry rather than dropping silently.

**Verify**: burst test caps concurrent notification work and shutdown test drains or reports queued work.

### Step 2: Propagate event identity context

Build a principal/context from the event's trusted org and mode before customer/repository enrichment.

**Verify**: live/sandbox fixtures enrich only their own customer records.

### Step 3: Run reliability tests

Run focused notification/event tests and the backend suite with required services.

**Verify**: `go test ./internal/eventstream ./internal/usecase/notifications ./cmd/api` → exit 0.

## Done criteria

- [ ] Notification concurrency is bounded.
- [ ] Shutdown and retry behavior is explicit.
- [ ] Event mode/org context is preserved.

## STOP conditions

- The chosen queue cannot provide the required retry or shutdown semantics.
- A fix would block the event broker's SSE delivery path.

## Maintenance notes

Document queue capacity, retry policy, and the metric used to detect dropped notifications.
