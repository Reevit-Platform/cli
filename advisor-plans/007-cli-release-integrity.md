# Plan 007: Verify CLI release binaries before execution

> **Executor instructions**: Follow this plan step by step. Stop on any STOP condition. Preserve unrelated dirty changes.
>
> **Drift check (run first)**: `git diff --stat a450e8a..HEAD -- cli/npm/install.js cli/.goreleaser.yaml cli/npm/package.json`

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: HIGH
- **Depends on**: none
- **Category**: security | tests
- **Planned at**: root `a450e8a`, 2026-07-11

## Why this matters

The npm wrapper downloads a release archive and executes the extracted binary, while GoReleaser publishes checksums that are never verified. This leaves a supply-chain gap at installation time.

## Current state

- `cli/npm/install.js:21-30,48-52` fetches and extracts the archive without checking a digest or signature.
- `cli/.goreleaser.yaml:27-28` emits `checksums.txt`.

## Scope

In scope: npm installer download and verification code, package scripts, release configuration, and installer tests.

Out of scope: changing GitHub release permissions or replacing GoReleaser.

## Steps

### Step 1: Verify the archive digest

Fetch the matching checksum manifest, parse the platform asset entry, compute SHA-256, and fail closed on mismatch or missing manifest. Keep redirect and platform validation.

**Verify**: installer tests cover valid, missing, malformed, and mismatched checksums without executing arbitrary content.

### Step 2: Add install smoke coverage

Mock release responses and assert only a verified binary is written with executable permissions.

**Verify**: `npm test` in `cli/npm` or the repository's installer test command → exit 0.

## Done criteria

- [ ] No release binary is written unless checksum verification succeeds.
- [ ] Tests cover failure modes.
- [ ] No secret or token values are introduced.

## STOP conditions

- The release checksum format differs from the assumed GoReleaser format.
- Verification would require trusting an unsigned manifest from an unrelated host.

## Maintenance notes

If signing is later introduced, prefer signature verification over adding another checksum transport.
