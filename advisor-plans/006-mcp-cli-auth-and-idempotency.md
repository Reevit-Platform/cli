# Plan 006: Secure MCP/CLI transports and make money operations retry-safe

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git diff --stat a450e8a..HEAD -- mcp cli`

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: HIGH
- **Depends on**: none
- **Category**: security | bug | tests
- **Planned at**: root `a450e8a`, 2026-07-11

## Why this matters

MCP HTTP mode accepts requests without authenticating the caller and buffers arbitrary bodies. MCP and CLI money operations generate a new idempotency key on every invocation, so retries after ambiguous failures can duplicate operations.

## Current state

- `mcp/src/http.ts:29-67` listens on the configured port without endpoint authentication or a body-size cap.
- `mcp/src/client.ts:45-50,79` generates `randomUUID()` for idempotent calls.
- `cli/internal/api/client.go:47-53,87-89` does the same for CLI requests.

## Scope

In scope: MCP HTTP authentication/binding/body limits, MCP request options/tools, CLI request options/trigger retry behavior, and tests.

Out of scope: changing backend API-key scopes or making live destructive actions bypass confirmation.

## Steps

### Step 1: Authenticate and bound MCP HTTP

Bind localhost by default, support an explicit bearer/shared-secret boundary for remote use, reject unauthorized requests, and cap request bytes before JSON parsing.

**Verify**: HTTP tests cover unauthorized, authorized, oversized, malformed, and valid requests.

### Step 2: Introduce stable idempotency options

Allow callers to provide an idempotency key and reuse it for retries. For CLI commands, expose a flag or derive one stable invocation key; never silently replace a caller key.

**Verify**: repeated calls with the same operation key emit identical `Idempotency-Key` headers; independent operations remain distinct.

### Step 3: Preserve safety gates

Keep scoped backend authorization and live refund confirmation requirements unchanged. Add negative tests for read-only keys and missing live confirmation.

**Verify**: `npm test` in `mcp` and `go test ./...` in `cli` → exit 0.

## Done criteria

- [ ] MCP HTTP is authenticated and size-bounded.
- [ ] MCP and CLI retries reuse stable keys.
- [ ] Scope and live confirmation tests remain green.

## STOP conditions

- Remote HTTP authentication cannot be added without exposing the configured API key.
- A retry design requires storing secret values in logs or plaintext files.
- Existing CLI command contracts would break without a compatibility decision.

## Maintenance notes

Document whether forwarding is at-least-once and how operators supply stable keys for external retries.
