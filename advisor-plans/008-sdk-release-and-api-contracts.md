# Plan 008: Make SDK releases and API documentation contract-driven

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git diff --stat a450e8a..HEAD -- cli mcp sdks`; `git -C backend diff --stat 9de2f570..HEAD -- adapters/http/router.go internal/docs/openapi.yaml`; `git -C docs diff --stat bd92363..HEAD -- content/docs/reevit`

## Status

- **Priority**: P1
- **Effort**: L
- **Risk**: HIGH
- **Depends on**: advisor-plans/003-backend-mode-isolation-gaps.md, advisor-plans/006-mcp-cli-auth-and-idempotency.md
- **Category**: docs | tests | dependencies | dx
- **Planned at**: root `a450e8a`, backend `9de2f570`, docs `bd92363`, 2026-07-11

## Why this matters

The shipped backend exposes payouts and reconciliation, but tracked OpenAPI specs omit them. Auth examples use legacy key shapes and the PHP constructor example is wrong. SDK release workflows also swallow test/audit failures and browser SDKs lack behavior tests.

## Current state

- Payout/reconciliation routes exist at `backend/adapters/http/router.go:1017-1048`, but no matching paths exist in `backend/internal/docs/openapi.yaml` or the docs copy.
- Legacy key examples appear at `backend/internal/docs/openapi.yaml:22-27`.
- PHP docs call `new Reevit(apiKey, baseUrl)` while implementation expects `(apiKey, orgId, baseUrl)`.
- `sdks/php/.github/workflows/ci.yml:56-60` and Python CI `:54-55` swallow failures; browser SDK CI builds without checkout behavior tests.

## Scope

In scope: OpenAPI source/public copies, PHP/Python/TS SDK examples, SDK CI workflows, browser SDK component/bridge tests, and lockfile consistency checks.

Out of scope: adding new API resources, changing public response schemas, or publishing packages.

## Steps

### Step 1: Regenerate and validate API contracts

Update the canonical OpenAPI source and generated copies for payouts/reconciliation and current key formats. Correct the PHP constructor example and add a compile/smoke check for each language.

**Verify**: route/spec parity check finds every shipped route; language examples compile or execute in their existing CI.

### Step 2: Make release CI fail actionable

Remove unconditional `|| true` around tests and high/critical audits. If a package has no tests, detect that explicitly rather than masking failures. Prefer frozen installs and validate manifest/lock versions.

**Verify**: intentionally failing test fixture fails CI locally; clean package runs pass.

### Step 3: Add browser SDK behavior coverage

Add focused React/Svelte/Vue tests for success, failure, cancel, callback, bridge, and idempotency behavior. Run them in CI before dist verification.

**Verify**: each browser SDK CI job runs its behavior tests and build/typecheck.

## Done criteria

- [ ] OpenAPI and SDK docs expose payout/reconciliation and current auth.
- [ ] PHP/Python/TypeScript examples are executable or compile-checked.
- [ ] Release CI fails on real test and audit failures.
- [ ] Browser SDK behavior tests run before publish.

## STOP conditions

- No canonical OpenAPI source can be identified; stop rather than editing generated copies only.
- A package's publishing contract requires keeping a known broken example.
- Lockfile repair changes unrelated dependency trees without review.

## Maintenance notes

Keep route/spec parity and example compilation as release gates, not one-off cleanup scripts.
