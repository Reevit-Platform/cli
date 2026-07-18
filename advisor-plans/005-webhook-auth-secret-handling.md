# Plan 005: Harden webhook, auth-token, and example-secret handling

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git -C backend diff --stat 9de2f570..HEAD -- adapters/http/handlers_webhooks.go internal/services/auth/users.go adapters/http/middleware_wide_event.go`; `git diff --stat a450e8a..HEAD -- examples`

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: HIGH
- **Depends on**: none
- **Category**: security | docs | tests
- **Planned at**: backend `9de2f570`, root `a450e8a`, 2026-07-11

## Why this matters

Webhook parse failures bypass the existing header-redaction helper, invalid magic-link attempts log token material, and Go/PHP examples accept unsigned webhooks when secrets are absent. These are credential-handling failures that can affect production operators and copy-paste integrations.

## Current state

- `backend/adapters/http/handlers_webhooks.go:163-180,217-229` persists raw headers directly; `:394-399` contains the intended redaction helper.
- `backend/internal/services/auth/users.go:386-388` logs a token prefix.
- `examples/go+reevit/webhooks.go:60-68` and `examples/php+reevit/src/WebhookController.php:32-39` condition verification on a non-empty secret.

## Scope

In scope: backend webhook failure saves, auth logging, Go/PHP example handlers, and regression tests/docs.

Out of scope: changing webhook signature formats or rotating real credentials.

## Steps

### Step 1: Centralize webhook redaction

Route every failure-path event save through `saveWebhookEvent` or an equivalent redacting helper. Add tests for authorization, cookie, API-key, and signature headers.

**Verify**: failure-path tests assert stored headers contain no credential values.

### Step 2: Remove token content from auth logs

Keep error classification and request correlation, but never log token bytes or prefixes. Add a logger-capture regression test.

**Verify**: `go test ./internal/services/auth` → exit 0 and the test fails if any token fragment appears.

### Step 3: Make examples fail closed

Require the webhook secret in Go/PHP handlers; return a clear configuration error when absent. Document a deliberately separate local test stub if needed.

**Verify**: example tests or language lint checks cover missing-secret rejection and invalid-signature rejection.

## Done criteria

- [ ] All webhook persistence paths redact sensitive headers.
- [ ] Auth logs contain no token material.
- [ ] Examples reject unsigned requests by default.
- [ ] No real secret values appear in source, tests, or plans.

## STOP conditions

- A test requires printing a credential to prove redaction.
- A proposed example bypass would be enabled by default.
- Existing replay/debug workflows require raw secret headers; stop and report.

## Maintenance notes

Keep redaction centralized and apply it before persistence, not only at response serialization.
