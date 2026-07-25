# Plan 006: Split the remaining backend god-files (handlers_payments, handlers_auth, auth_repo)

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: in `backend/`, run
> `git diff --stat 61332193..HEAD -- adapters/http/handlers_payments.go adapters/http/handlers_auth.go adapters/repo/auth_repo.go`
> If these files changed materially (new/removed functions), re-derive the
> method groupings in Step 1 from the live code — do not trust this plan's
> illustrative group lists blindly.

## Status

- **Priority**: P3
- **Effort**: L
- **Risk**: MED
- **Depends on**: none (but coordinate: no other plan may have in-flight edits to these files — check `plans/README.md` statuses first)
- **Category**: tech-debt
- **Planned at**: backend commit `61332193`, 2026-07-07

## Why this matters

Three files remain an order of magnitude above the repo's norm: `adapters/http/handlers_payments.go` (2,458 lines), `adapters/repo/auth_repo.go` (2,402 lines), `adapters/http/handlers_auth.go` (1,794 lines). Every payments or auth change wades through unrelated concerns, reviews are noisy, and merge conflicts concentrate here (the repo explicitly documents this checkout as shared and clobber-prone). The repo already proved the remedy on a worse case: `internal/usecase/payments/service.go` was split 5,726 → ~3,080 lines into topic files (`service_checkout.go`, `service_refunds.go`, `service_events.go`, …) — same package, methods on the same receiver, **pure file moves, zero behavior change** (see `backend/CLAUDE.md`, Jun 2, 2026 entry). This plan applies the identical recipe to the three remaining files.

## Current state

Paths relative to `backend/`.

- `adapters/http/handlers_payments.go` — 2,458 lines. One handler struct serving payments CRUD/list, refunds, capture/confirm/retry, bulk ops, stats/analytics, exports, fee insights, scheduled payments. Enumerate the actual methods with `grep -n "^func " adapters/http/handlers_payments.go`.
- `adapters/http/handlers_auth.go` — 1,794 lines. Signup, magic-link, `/auth/verify` + 2FA/TOTP challenge (`handleTOTPChallenge`), sessions, recovery, WebAuthn/OAuth glue. Enumerate with the same grep.
- `adapters/repo/auth_repo.go` — 2,402 lines. sqlc-backed repo methods for users, sessions, magic links, TOTP/backup codes, passkeys, recovery, org membership. Enumerate likewise.
- The proven exemplar of the split style: `internal/usecase/payments/service_refunds.go` et al. — same package, no new types, each file opens with the shared imports it needs; original file keeps the struct definition + constructor.
- Repo conventions (`backend/CLAUDE.md`): `golangci-lint run ./...` must show 0 issues (watch `wsl_v5` blank-line rules when moving code); full `go test ./...` with `TEST_DATABASE_URL=postgres://reevit:reevit@localhost:5433/reevit_test?sslmode=disable` must pass. **Shared-checkout hazard** (documented): before committing, re-grep your changes in `adapters/http/router.go` and `cmd/` mains to make sure stale buffers didn't clobber hot files; never stash in this checkout.

## Commands you will need

Run from `backend/`.

| Purpose | Command | Expected |
|---|---|---|
| Build | `go build ./...` | exit 0 |
| Lint | `golangci-lint run ./...` | 0 issues |
| Tests | `TEST_DATABASE_URL=postgres://reevit:reevit@localhost:5433/reevit_test?sslmode=disable go test ./...` | all pass |
| Behavior diff | `git diff --stat` | only file adds + shrunken originals; no query/logic hunks |

## Scope

**In scope** (file moves within the same packages only):
- `adapters/http/handlers_payments.go` → plus new `adapters/http/handlers_payments_{refunds,stats,exports,scheduled,bulk}.go` (final grouping decided in Step 1)
- `adapters/http/handlers_auth.go` → plus new `adapters/http/handlers_auth_{magiclink,twofactor,sessions,recovery}.go` (ditto)
- `adapters/repo/auth_repo.go` → plus new `adapters/repo/auth_repo_{sessions,totp,passkeys,recovery}.go` (ditto)

**Out of scope** (do NOT touch):
- `adapters/http/router.go` — route registration stays exactly where it is.
- Any function BODY: no renames, no signature changes, no error-handling "improvements", no import cleanups beyond what the move mechanically requires. If you see a bug while moving, note it in the final report; do not fix it.
- `internal/usecase/payments/service.go` (already split; still 3,200 lines — further decomposition is a separate judgment call, deliberately excluded).
- Test files: move a `_test.go` alongside only if the compiler forces it (same-package tests don't require moves).

## Git workflow

- Branch in `backend/` from `origin/dev`: `refactor/split-god-files`.
- **One commit per file split** (3 commits): `refactor(http): split handlers_payments.go into topic files — pure file move`, etc. This makes each reviewable with `git diff --color-moved`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Derive the groupings

For each of the three files run `grep -n "^func " <file>` and cluster methods by topic (refunds together, stats together, …). Write the grouping as a table in the commit message body. Target: no new file over ~800 lines; the original keeps the struct, constructor, and shared helpers used across groups.

**Verify**: every function from the grep appears in exactly one group.

### Step 2: Split `handlers_payments.go`

Move each group into its new file (same package `http`... confirm the actual package name from the file header first). Keep the original file's remaining content compiling at every point: move one group, build, move the next.

**Verify** after each group and at the end: `go build ./...` → exit 0. Then `golangci-lint run ./...` → 0 issues; full test suite green. `git diff --color-moved=zebra` shows only moved blocks. Commit.

### Step 3: Split `handlers_auth.go`

Same procedure. Note `handleTOTPChallenge` + the `LoginAttemptTracker` wiring (security fix #327) is one cohesive group — keep it intact in one file.

**Verify**: same gates. Commit.

### Step 4: Split `auth_repo.go`

Same procedure. sqlc-generated code is elsewhere (`internal/infra/sqlc`) — you're only moving the repo wrapper methods.

**Verify**: same gates. Commit.

### Step 5: Post-split audit

Run the shared-checkout clobber check: `git diff origin/dev...HEAD -- adapters/http/router.go cmd/` → must be empty. Confirm total behavior neutrality: `git diff origin/dev...HEAD --stat` shows only the six-or-so new files plus shrunken originals; spot-check that `grep -c "^func " ` totals per package are unchanged vs Step 1's enumeration.

**Verify**: all checks above hold; `backend/CLAUDE.md` changelog entry added (match the Jun 2 "Refactor — split the payments god-file" entry's style).

## Test plan

No new tests — this is a behavior-preserving move verified by the full existing suite (the same standard the service.go split used). The per-step `go build` + full `go test ./...` + lint gates are the safety net.

## Done criteria

- [ ] `wc -l adapters/http/handlers_payments.go adapters/http/handlers_auth.go adapters/repo/auth_repo.go` — each original ≤ ~900 lines
- [ ] No new file exceeds ~800 lines
- [ ] `go build ./...`, `golangci-lint run ./...` (0 issues), full `go test ./...` green
- [ ] `git diff origin/dev...HEAD -- adapters/http/router.go cmd/` → empty
- [ ] Function count per package unchanged (Step 1 enumeration vs post-split)
- [ ] Three commits, each a pure file move
- [ ] `plans/README.md` status row updated

## STOP conditions

- Any test failure that isn't a compile error from a half-moved group — a "pure move" must never change test outcomes; if one does, revert the group and report.
- You find yourself wanting to rename, de-duplicate, or "fix" anything mid-move — that's scope creep by definition here; note it, don't do it.
- The drift check shows these files were significantly restructured since `61332193` — re-derive groupings; if another branch has an in-flight split, abandon and report.
- Merge conflicts against `origin/dev` mid-work in these files — stop and ask the operator whether to rebase or wait (this checkout is shared).

## Maintenance notes

- Future handler additions should land in the topic file, not the rump original — a reviewer should reject additions to the original files.
- The `internal/usecase/payments/service.go` rump (3,200 lines) is the next candidate if this split proves its worth; same recipe.
- Reviewer: use `git diff --color-moved=zebra` — any non-moved (changed) line in the diff is a defect in this PR.
