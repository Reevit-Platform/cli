# Plan 005: Backend pre-commit gate + fix stale roadmap/doc drift

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: in `backend/`, run
> `git diff --stat 61332193..HEAD -- Makefile CONTRIBUTING.md` and at the monorepo
> root `git diff --stat a450e8a..HEAD -- ROADMAP.md`. Reconcile any changes
> against "Current state" before proceeding.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: dx / docs
- **Planned at**: root `a450e8a`, backend `61332193`, frontend `5bd5ced`, 2026-07-07

## Why this matters

Two cheap fixes. (1) `backend/CLAUDE.md` mandates `golangci-lint run ./...` (0 issues) and `go test ./...` before **every** commit, but nothing enforces it — the frontend has husky + lint-staged, the backend has no hooks at all, so the gate depends on discipline and failures surface in CI a push later. (2) Two docs are actively wrong, which is worse than missing: the root `ROADMAP.md` says the routing A/B tests are "not integrated into the router", but they are — `internal/usecase/payments/payment_router.go:95` consults `resolveABTest` during route resolution and stamps `ABTestID/Variant/RuleID` on the route result; and `frontend/CLAUDE.md` still points the changelog process at `components/landing/ChangelogPage.tsx` when the changelog data now lives in `frontend/content/changelog.ts`. Stale docs actively mislead the agents and humans who plan against them (this very audit initially accepted the roadmap claim).

## Current state

- `backend/` has no `.githooks/` directory and no `core.hooksPath` configuration; `backend/Makefile` exists with dev targets (`make test`, `make run`, migration helpers). `backend/CONTRIBUTING.md` exists.
- `backend/CLAUDE.md` (top): "Before **every** commit and push — **always** run both: `golangci-lint run ./...` … `go test ./...`".
- Full `go test ./...` requires the test DB (`TEST_DATABASE_URL=postgres://reevit:reevit@localhost:5433/reevit_test?sslmode=disable`) — a pre-commit hook must not hard-require the DB or it will be skipped with `--no-verify` forever. Check whether DB-dependent tests guard themselves (run `go test ./internal/domain/... ./internal/usecase/payments/ -count=1` without the env var and observe: skip vs fail) and scope the hook accordingly.
- Evidence for the A/B wiring, `backend/internal/usecase/payments/payment_router.go`:
  ```go
  // :61  resolveABTest func(ctx context.Context, orgID, country, method, currency string, prefer []string, amount int64, allowedProviders []string) ([]ports.ConnectionDetails, string, string, string, error)
  // :95  routes, abTestID, abTestVariant, abTestRuleID, err := r.resolveABTest(...)
  // :98-100  result.ABTestID / result.ABTestVariant / result.ABTestRuleID = ...
  ```
  Find the stale claim: `grep -n "not integrated into the router" /Users/nanayaw/Developer/Devs/fullstack/primeflow/ROADMAP.md` (near the C1 section, ~line 191). Note the frontend shipped the promote-winner flow end-to-end (frontend CLAUDE.md v1.9.17: "closes roadmap C1 end-to-end").
- Frontend doc drift: `grep -n "ChangelogPage" frontend/CLAUDE.md` — the "Changelog Process" section step 2 references `components/landing/ChangelogPage.tsx`; the shared data source is now `frontend/content/changelog.ts` (consumed by the marketing page and the sidebar "What's new" stack).

## Commands you will need

| Purpose | Where | Command | Expected |
|---|---|---|---|
| Lint | backend/ | `golangci-lint run ./...` | 0 issues |
| Unit tests (no DB) | backend/ | `go test ./... ` with DB-needing pkgs skipped/short — determined in Step 1 | pass/skip cleanly |
| Hook install | backend/ | `git config core.hooksPath .githooks` | exit 0 |
| Hook smoke test | backend/ | `git commit --allow-empty -m test` on a scratch branch | hook runs, commit succeeds |

## Scope

**In scope**:
- `backend/.githooks/pre-commit` (create), `backend/Makefile` (add `hooks` target), `backend/CONTRIBUTING.md` + `backend/CLAUDE.md` (document installation; changelog entry)
- Monorepo root `ROADMAP.md` (correct the C1 router-integration sentence only)
- `frontend/CLAUDE.md` ("Changelog Process" section pointer only)

**Out of scope** (do NOT touch):
- CI workflows (`.github/`) — CI already runs the gates.
- Any Go source code.
- The rest of ROADMAP.md — no re-planning, no status changes beyond the one false sentence.
- Frontend husky config (already working).

## Git workflow

- Backend branch from `origin/dev`: `chore/precommit-hooks`. Root repo + frontend doc edits: on their respective current branches (docs-only; ask the operator where to land root-repo changes if unclear — root repo has no documented PR convention).
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Determine the no-DB test scope

In `backend/`, unset `TEST_DATABASE_URL`/`DATABASE_URL` and run `go test ./internal/... ./adapters/... 2>&1 | tail -30`. Record which packages fail (vs skip) without the DB. The hook will run `golangci-lint run ./...` plus `go test` over the packages that pass DB-less, OR `go test ./... -short` if the repo's DB tests honor `testing.Short()` (check: `grep -rn "testing.Short()" --include="*_test.go" | head`).

**Verify**: you have a concrete test command that exits 0 on a clean tree without a DB, in under ~2 minutes.

### Step 2: Create the hook

`backend/.githooks/pre-commit`:
```sh
#!/bin/sh
set -e
echo "pre-commit: golangci-lint"
golangci-lint run ./...
echo "pre-commit: go test (no-DB scope)"
<the command from Step 1>
```
`chmod +x .githooks/pre-commit`. Add a Makefile target:
```make
hooks: ## install git hooks
	git config core.hooksPath .githooks
```

**Verify**: `make hooks && git commit --allow-empty -m "hook smoke test"` on a scratch branch → hook output visible, commit succeeds; then drop the scratch commit (`git reset --hard HEAD~1`).

### Step 3: Document

- `CONTRIBUTING.md`: a "Git hooks" section — run `make hooks` once after clone; the hook runs lint + no-DB tests; full `go test ./...` (with the test-DB URL) still required before push per CLAUDE.md.
- `backend/CLAUDE.md`: changelog entry; add one line under the pre-commit checklist noting `make hooks` installs enforcement.

**Verify**: `grep -n "make hooks" CONTRIBUTING.md backend/CLAUDE.md` → both match. `golangci-lint run ./...` still 0 issues (no Go changes expected, sanity only).

### Step 4: Fix the ROADMAP C1 drift

At the monorepo root, edit the false sentence (~line 191) to state that A/B experiments ARE consulted by the payment router (`internal/usecase/payments/payment_router.go` — `resolveABTest` stamps `ABTestID/Variant/RuleID` on route results) and that the promote-winner flow shipped end-to-end (frontend v1.9.17). Leave the section's remaining open items (e.g. guardrails/benchmarks) untouched unless they are also contradicted by code you verify.

**Verify**: `grep -n "not integrated into the router" ROADMAP.md` → no matches.

### Step 5: Fix the frontend CLAUDE.md changelog pointer

Update the "Changelog Process" step referencing `components/landing/ChangelogPage.tsx` to point at `content/changelog.ts` (shared by the marketing changelog page and the sidebar "What's new" stack). First confirm: `ls frontend/content/changelog.ts` and `grep -rn "changelog" frontend/components/landing/ChangelogPage.tsx 2>/dev/null | head -3` — if `ChangelogPage.tsx` still exists and imports from `content/changelog.ts`, say both (data in `content/changelog.ts`, rendered by the page).

**Verify**: `grep -n "content/changelog.ts" frontend/CLAUDE.md` → match present.

## Test plan

No Go/TS tests — the hook smoke test (Step 2) and the grep-based done criteria are the verification. The hook itself is the test infrastructure.

## Done criteria

- [ ] `backend/.githooks/pre-commit` exists, executable; `make hooks` configures `core.hooksPath`
- [ ] Empty-commit smoke test showed the hook running lint + tests
- [ ] `grep -rn "not integrated into the router" ROADMAP.md` → no matches
- [ ] `frontend/CLAUDE.md` changelog process names `content/changelog.ts`
- [ ] `golangci-lint run ./...` in backend → 0 issues (unchanged)
- [ ] Only in-scope files modified across the three repos (`git status` in each)
- [ ] `plans/README.md` status row updated

## STOP conditions

- Step 1 finds no test subset that passes DB-less in reasonable time → ship the hook with lint-only + a loud comment, and report that the test half needs a `testing.Short()` adoption pass (separate work).
- `.githooks/` or a hooks mechanism already exists in backend (drift) → reconcile with it instead of adding a second mechanism.
- The ROADMAP C1 section has been rewritten since planning such that the quoted sentence is gone → verify the current text against `payment_router.go` and only fix what is still false.

## Maintenance notes

- If DB-dependent packages later adopt `testing.Short()`, upgrade the hook to `go test ./... -short`.
- The hook is opt-in per clone (`make hooks`); CI remains the hard gate. Do not make the hook mandatory via bootstrap scripts without team buy-in.
- Reviewer: check the hook doesn't slow commits beyond ~2 min — developers bypass slow hooks, which is worse than no hook.
