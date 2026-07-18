# Plan 012: Repair configuration and contributor-facing documentation contracts

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git -C backend diff --stat 9de2f570..HEAD -- internal/infra/config/config.go config.yaml config.development.yaml cmd/api/main.go`; `git -C docs diff --stat bd92363..HEAD -- CLAUDE.md content/docs/reevit/security.mdx`; `git -C frontend diff --stat 22f7c58..HEAD -- README.md package.json`

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: MED
- **Depends on**: advisor-plans/001-backend-vulnerability-and-tool-pinning.md, advisor-plans/008-sdk-release-and-api-contracts.md
- **Category**: dx | docs | bug
- **Planned at**: backend `9de2f570`, docs `bd92363`, frontend `22f7c58`, 2026-07-11

## Why this matters

YAML SMTP settings are declared and documented but never mapped into runtime configuration. The docs repository still contains stale OpenFront/Keystone guidance and an invalid curl command, while the frontend README references a missing env template and unsupported Node 18 for Next 16.

## Current state

- `backend/internal/infra/config/config.go:685-759` maps Resend fields but not `Email.SMTP`.
- `backend/config.yaml:100-113` and `backend/cmd/api/main.go:203-218` show the intended SMTP path.
- `docs/CLAUDE.md:1-22` describes a different OpenFront product; `docs/content/docs/reevit/security.mdx:26-29` contains `curl -https://...`.
- `frontend/README.md:56-79` instructs copying a missing `.env.local.example`; `frontend/README.md:38` claims Node 18 while `package.json:79` pins Next 16.

## Scope

In scope: backend YAML-to-config mapping/tests, docs CLAUDE/security page, frontend README/env example, and explicit supported runtime documentation.

Out of scope: real environment files, secret values, product redesign, and generated untracked docs.

## Steps

### Step 1: Map and test SMTP configuration

Populate flat SMTP fields from `FileConfig.Email.SMTP` with environment precedence intact. Add a config test for YAML-only SMTP and environment override behavior.

**Verify**: `go test ./internal/infra/config` → exit 0.

### Step 2: Replace stale or invalid docs

Rewrite `docs/CLAUDE.md` for Reevit/Fumadocs, correct the curl command, and ensure no example contains secret values. Keep docs focused on implemented behavior.

**Verify**: docs build/check command → exit 0; `rg -n 'OpenFront|Keystone|curl -https' docs` → no matches.

### Step 3: Repair frontend fresh-clone instructions

Add a sanitized tracked env example, document the supported Node version for Next 16, and optionally add an `engines` field consistent with CI.

**Verify**: from a clean temporary checkout, the documented copy/install commands resolve; `pnpm typecheck` → exit 0.

## Done criteria

- [ ] YAML SMTP configuration initializes the documented fallback.
- [ ] Docs identify Reevit and contain valid commands.
- [ ] Frontend setup works from a fresh clone without real secrets.

## STOP conditions

- A documentation value could be mistaken for a real credential.
- The supported Node version cannot be established from CI/package constraints.
- Docs build tooling is unavailable; report instead of changing generated output blindly.

## Maintenance notes

Treat README, CLAUDE, OpenAPI, and config examples as contracts and update them in the same release as runtime changes.
