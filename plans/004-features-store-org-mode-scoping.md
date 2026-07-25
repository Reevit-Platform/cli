# Plan 004: Stop the features store leaking flags across org/mode switches

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: in `frontend/`, run
> `git diff --stat 5bd5ced..HEAD -- stores/features-store.ts contexts/`
> On any in-scope change, re-verify the "Current state" excerpts first.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED
- **Depends on**: none
- **Category**: bug
- **Planned at**: frontend commit `5bd5ced`, 2026-07-07

## Why this matters

`stores/features-store.ts` persists the merchant's enabled dashboard features to localStorage under a single global key, with no org or mode dimension. Feature flags gate the sidebar, the ⌘K command palette, and page access (`useFeatureGate`). When a user switches organization (or the test/live mode toggle), the store rehydrates the **previous org's flags** until the API refetch lands — and if that fetch fails, the stale flags persist indefinitely. A merchant with two orgs of different maturity sees the wrong navigation set, and gated pages may briefly admit (then bounce) or hide surfaces the current org actually has. The backend is authoritative, so this is a consistency/UX bug, not a security hole — flags only control UI visibility.

## Current state

Paths relative to `frontend/`.

- `stores/features-store.ts` — zustand store with `persist`. The persist config (~lines 138-146):
  ```ts
  {
    name: "dashboard-features-storage",
    // Only persist the enabled features and timestamp
    partialize: (state) => ({
      enabledFeatures: state.enabledFeatures,
      maturityRevealedFeatures: state.maturityRevealedFeatures,
      lastFetched: state.lastFetched,
    }),
  },
  ```
  The store has a `reset()` action (~line 130) restoring `getDefaultFeatures()` and clearing `isLoaded`/`lastFetched`. `setFeatures` overwrites state whenever the API list loads (documented in `frontend/CLAUDE.md` v1.9.16: "the store's `setFeatures` silently clobbers those local flips whenever the API list loads" — the API is the source of truth).
- Where org/mode context changes: look in `contexts/` for the org/auth provider and the mode provider (`grep -rn "setMode\|currentOrg\|activeOrg" contexts/ | head`). The mode cookie helper is `lib/auth/mode.ts` (`rv_app_mode` cookie). The scope-keying convention used by queries is `useAuthScope()` → `scopeKey` (see `hooks/use-customers.ts:59` for the pattern) — features should follow the same scoping idea.
- Who reads the store: `components/app-sidebar.tsx`, `components/command-palette-entries.ts` consumers, `useIsFeatureEnabled` / `useFeatureGate` (find via `grep -rn "useFeaturesStore\|useIsFeatureEnabled\|useFeatureGate" --include="*.ts*" -l | grep -v test`).
- Where features are fetched from the API and pushed into the store: `grep -rn "setFeatures" --include="*.ts*"` — expect a hook or provider effect.
- Existing tests: `grep -rln "features-store" --include="*.test.*"` and `config/dashboard-features.test.ts`.

Repo conventions: zustand v5, tests with vitest; typecheck gate `npx tsc --noEmit` mandatory before commit.

## Commands you will need

Run from `frontend/`.

| Purpose | Command | Expected |
|---|---|---|
| Typecheck | `npx tsc --noEmit` | 0 errors |
| Tests | `npm run test` | all pass |
| Targeted tests | `npx vitest run stores` | pass |
| Lint | `npx eslint .` | 0 errors |

## Scope

**In scope**:
- `stores/features-store.ts`
- The org/mode provider(s) in `contexts/` that will trigger the scope change (wiring only)
- The features-fetch hook/provider that calls `setFeatures` (if it needs the scope)
- New/extended tests for the store
- `frontend/CLAUDE.md` changelog entry

**Out of scope** (do NOT touch):
- The backend features API, `config/dashboard-features.ts` registry, `/platform/features` admin page.
- The sidebar/palette consumers — they must keep working unmodified (that's the point).
- Do NOT convert flags to a React Query cache in this plan — that's a bigger refactor; note it as follow-up if it looks attractive.

## Git workflow

- Branch in `frontend/` from `origin/dev-frontend`: `fix/features-store-scope`.
- Conventional commit: `fix(features): scope persisted flags by org+mode`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Map the switch points

Identify exactly where (a) the active org changes and (b) the mode changes. Record the file:line of each. Confirm whether anything currently calls `featuresStore.reset()` on either switch (`grep -rn "reset()" contexts/ components/providers.tsx`).

**Verify**: you can name both switch points; write them into the PR description/commit body.

### Step 2: Scope the persisted state

Preferred design (smallest blast radius): keep ONE storage key but persist a **map keyed by scope** instead of a flat object:
```ts
// persisted shape
{ byScope: { [`${orgId}:${mode}`]: { enabledFeatures, maturityRevealedFeatures, lastFetched } } }
```
Add a `setScope(orgId, mode)` action that (a) stores the current scope string, (b) hydrates `enabledFeatures` etc. from `byScope[scope]` or defaults, and (c) sets `isLoaded: false` so gates fall back to their pre-load behavior until the API responds. `setFeatures` writes through to `byScope[currentScope]`. Add a zustand `persist` `version` + `migrate` that converts the old flat shape into `byScope` under a `"legacy"` scope (or discards it — flags refetch on load anyway; discarding is acceptable and simpler, choose it unless a test depends on migration).

**Verify**: `npx tsc --noEmit` → 0 errors; `npx vitest run stores` → existing store tests pass (update them for the new shape where they assert persistence internals).

### Step 3: Wire the switch points

Call `setScope(orgId, mode)` from the org provider and mode provider identified in Step 1 (an effect watching the values is fine). Ensure initial mount also sets the scope before the features fetch pushes data.

**Verify**: `npm run test` → green.

### Step 4: Tests + changelog

Write the tests below; add the CLAUDE.md changelog entry.

**Verify**: full gates clean (`tsc`, `eslint`, `vitest`, `prettier --check`).

## Test plan

Extend/replace the store's test file (colocated `stores/features-store.test.ts`; model after existing zustand store tests if present, else plain vitest with `renderHook`):
1. Org A enables feature X → switch scope to org B → `isFeatureEnabled(X)` returns the default, not org A's value.
2. Switch back to org A's scope → org A's flags rehydrate from `byScope`.
3. Mode flip (`org:test` → `org:live`) isolates the same way.
4. `setFeatures` after a scope switch writes to the new scope only.
5. Old persisted flat shape (pre-migration) doesn't crash hydration (whichever migrate strategy you chose).

## Done criteria

- [ ] localStorage entry for `dashboard-features-storage` contains scope-keyed data (assert in a test via the mocked storage)
- [ ] All 5 test cases above exist and pass
- [ ] `npx tsc --noEmit` 0 errors; `npm run test` fully green; `npx eslint .` 0 errors
- [ ] No consumer files (sidebar, palette, gates) modified
- [ ] `plans/README.md` status row updated

## STOP conditions

- Step 1 reveals org/mode switches happen via a full page reload (window.location navigation) rather than in-app state — then stale-flash exposure is a single render and the fix may not be worth the migration complexity; report before proceeding.
- The store already receives scope information you didn't expect (drift since planning) — reconcile first.
- `useFeatureGate` behavior on `isLoaded: false` turns out to deny-by-default (bouncing users off pages during every org switch) — report; the hydration strategy needs a product call (optimistic-allow vs deny).
- More than ~6 test files break on the persisted-shape change — the shape is more load-bearing than audited; report.

## Maintenance notes

- Future: if features move to React Query (scope-keyed like every other resource via `useAuthScope`), this store shrinks to a thin selector layer — that migration supersedes this fix.
- Reviewer: scrutinize the pre-load gate behavior — the documented quirk is "unknown keys default-allow pre-load then deny post-load" (CLAUDE.md v1.9.27); the scope reset must not change that contract.
- The maturity-reveal ledger (`maturityRevealedFeatures`) rides along in the same persist — confirm reveal animations don't re-fire on every org switch (they should be per-scope too).
