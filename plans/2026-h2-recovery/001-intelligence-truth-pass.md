# Plan 001: Intelligence page truth pass

> **Executor instructions**: Follow this plan step by step. Run every verification command and
> confirm the expected result before moving on. If anything in "STOP conditions" occurs, stop and
> report — do not improvise. When done, update the status row in
> `plans/2026-h2-recovery/README.md`.
>
> **Drift check (run first)**: in `frontend/`, `git diff --stat 2469201..HEAD -- src/components/analytics src/lib/utils/wow-computation.ts src/lib/api/payments.ts`;
> in `backend/`, `git diff --stat 26641116..HEAD -- adapters/http/handlers_payments.go adapters/repo/payment_repo_analytics.go`.
> Reconcile any changes against "Current state" before proceeding.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: LOW (read-path only; no money movement touched)
- **Depends on**: none
- **Blocks**: 003 (the incrementality panels attach to this page; fix the arithmetic before adding claims to it)
- **Category**: correctness / product truth
- **Repos**: `frontend/`, `backend/`

## Why this matters

The Payments Intelligence page is the single best asset Reevit has for sales, and it is currently
not safe to show a prospect. The audit found the headline arithmetic does not close (85.7% of 49
payments succeeded, 6 failed — 42 + 6 = 48, not 49), the same payment method appears twice under
two names, a 345% change is displayed on a 49-payment sample with no absolute number beside it, and
a Fees column renders dashes. Every one of those is real and every one of them is located below.

None of these are hard. They matter because this page is about to become the proof surface for the
recovery claim (plan 003), and a page that cannot add up its own totals cannot be used to assert
recovered revenue.

## Current state

**Page:** `frontend/src/routes/dashboard/analytics/index.tsx` (`AnalyticsPage`), route
`/dashboard/analytics/`, sidebar label "Intelligence". Components live in
`frontend/src/components/analytics/` and `.../analytics/charts/`. Data via TanStack Query, charts
via Recharts 3.10, UI shadcn + Tailwind v4.

### Defect 1 — totals do not close, and success rate falls when payments are refunded

**This is worse than the audit's arithmetic complaint and needs a product decision, not just a bug fix.**

There are **twelve** payment statuses (`backend/internal/domain/payment/payment.go:9-22`):

```
pending  requires_action  authorized  succeeded  partially_captured  failed
canceled  disputed  refunded  partially_refunded  abandoned  expired
```

The stats query (`backend/internal/infra/sqlc/payments_stats.go:29-43`) buckets three of them:

```sql
COUNT(*) AS total_count,
COALESCE(SUM(amount) FILTER (WHERE status = 'succeeded'), 0) AS total_amount,
COUNT(*) FILTER (WHERE status = 'succeeded')                 AS succeeded_count,
COUNT(*) FILTER (WHERE status = 'failed')                    AS failed_count,
COUNT(*) FILTER (WHERE status IN ('refunded','partially_refunded')) AS refunded_count
```

`paymentStatsTotals` (`backend/adapters/http/handlers_payments.go:866-871`) surfaces exactly those.
**Eight of twelve statuses land in `total_count` and in no bucket at all**: `pending`,
`requires_action`, `authorized`, `partially_captured`, `canceled`, `disputed`, `abandoned`,
`expired`. That alone explains the audit's 42 + 6 ≠ 49.

The deeper problem is that **status is a single mutable value, and refund moves it off `succeeded`.**
`hardening.go:75-78` permits `succeeded → refunded | partially_refunded | disputed`. So:

- A merchant with 100 authorized payments who then refunds 10 sees `succeeded_count` fall to 90
  against an unchanged `total_count` of 100 — **success rate reads 90% for a period in which every
  single payment succeeded.** A refund is a post-authorization event, not an authorization failure.
- `total_amount` filters on `status = 'succeeded'`, so refunded and disputed payments drop out of
  volume entirely. Gross volume understates by the refunded amount, with no way to see it.
- The same distortion hits every breakdown — `paymentStatsByProviderQuery:45-62` and
  `paymentStatsByMethodQuery:64+` repeat the identical FILTER set, so provider and method success
  rates are wrong in the same direction.

The frontend then computes `success_rate = succeeded / count`
(`frontend/src/components/analytics/intelligence-hero.tsx:33`, same in `intelligence-verdict.tsx:23`)
and renders `failed` beside it. The frontend formula is a faithful reading of a broken contract.

**The decision this forces:** "success rate" currently conflates two different questions — *where is
this payment now* (lifecycle state) and *did the charge authorize* (outcome). They need separating:

- **Lifecycle buckets** — every status gets one, and they sum to `count`. This makes the page add up.
- **Authorization outcome** — `succeeded`, `partially_captured`, `refunded`, `partially_refunded`
  and `disputed` all authorized at some point. `failed`, `canceled`, `abandoned` and `expired` did
  not. `pending`, `requires_action` and `authorized` are undecided and belong in neither numerator
  nor denominator.

Under that model success rate becomes `authorized_ever / (authorized_ever + did_not_authorize)`, and
crucially it **stops moving when a refund is issued**. Refunds get their own line, which is what a
finance lead actually wants to see anyway.

Note this makes plan 003 harder to get right too: recovery attribution counts succeeded reroutes, and
a rerouted payment that is later refunded currently silently leaves the recovered set.

### Defect 2 — payment method aliases are not normalized

`frontend/src/components/analytics/method-table.tsx:7-16`:

```ts
const METHOD_LABELS: Record<string, string> = {
  card: "Card",
  mobile_money: "Mobile money",
  bank_transfer: "Bank transfer",
  // ... no `momo`
};
```

`prettyKey` (`:18-20`) falls through to `key.replaceAll("_", " ")` + capitalize, so a `momo` key
renders as "Momo" in its own row next to "Mobile money". Rows are keyed on the raw API `m.key`
(`:75-76`), so there is no merge. `frontend/src/types/subscriptions.ts` already admits both spellings,
which confirms both reach the client. No normalization map exists anywhere in the analytics tree.

### Defect 3 — percentage change with no absolute and no sample floor

`frontend/src/lib/utils/wow-computation.ts:17-24` — `computeWoWChange` returns
`{ value, isPositive }` and nothing else; it returns `null` only when `previous === 0`. There is no
absolute delta in the `WoWTrend` shape and no minimum-sample guard. Rendered percent-only at
`intelligence-hero.tsx:179` and `:226-234`. The same pattern drives the anomaly detector in
`attention-feed.tsx:23-47`, which flags any provider whose amount moved more than 10% — on a
three-payment provider that fires constantly.

### Defect 4 — Fees column renders dashes

`frontend/src/components/analytics/provider-table.tsx:218-225` —
`fees && fees > 0 ? formatMoney(...) : "—"`. Fee rows come from `/payments/fees` keyed by
`row.provider`; when a provider has no fee row, or the sum is zero, the column is a dash for every
row. A dash column reads as "broken", not "not applicable".

### Defect 5 — highlights presented as problems

`attention-feed.tsx` renders volume anomalies and backend recommendations in one "Needs attention"
list. Provider volume *growth* is good news sitting under a warning heading.

### Defect 6 — recommendations are not actionable

`GET /v1/intelligence/recommendations` (`backend/internal/usecase/intelligence/recommendations.go:86-156`)
returns five rule-based tips: degraded provider, failure spike, missing backup, missing routing
rules, missing recovery workflow. They are rendered as text with no target. "Review degraded
provider health" should open that connection; "Investigate failure spike" should open the filtered
payment set.

### Defect 7 — sparse data drawn as continuous curves

`intelligence-hero.tsx:97-168` and `charts/decline-reasons-chart.tsx:116-178` both use Recharts
`Area type="monotone"` with gradient/`fillOpacity` fills. Over four data points a smoothed filled
curve implies continuous behaviour that was never measured.

### Not a defect — the Live/Sandbox "conflict"

The audit read the org card showing "Live" against a mode toggle showing "Sandbox" as a bug. It is
two different concepts: `frontend/src/components/team-switcher.tsx:117-126` reports KYC +
live-capability status, while `frontend/src/components/mode-toggle.tsx:7-71` reports the active data
environment (`src/contexts/mode-context.tsx`). Both are correct.
**Fix the labels, not the logic** — the org card should read "Live enabled", not "Live".

### Reusable asset

`frontend/src/lib/utils/ab-test-stats.ts` implements `calculateSignificance`,
`hasSufficientSamples` (`minSamples = 100`, `:190-191`), `formatConfidence` and badge helpers.
`frontend/src/routes/dashboard/ab-tests/-components/ab-test-results.tsx` renders
`StatisticalSignificanceCard` (~`:54-208`) with an "Insufficient Data" state. Nothing under
`components/analytics/` imports either. Defect 3 should reuse them, not reimplement.

## Scope

**In scope**

Backend (`backend/`):
- `adapters/http/handlers_payments.go` — extend `paymentStatsTotals` with the missing status buckets.
- `adapters/repo/payment_repo_analytics.go` + the corresponding `db/queries/*.sql` — add the counts.
- Regenerate sqlc if the query files change.
- `internal/usecase/intelligence/recommendations.go` — add a machine-readable target to each recommendation.

Frontend (`frontend/`):
- `src/components/analytics/` — `intelligence-hero.tsx`, `intelligence-verdict.tsx`,
  `method-table.tsx`, `provider-table.tsx`, `attention-feed.tsx`, `charts/decline-reasons-chart.tsx`
- `src/lib/utils/wow-computation.ts` — add absolute delta + sample floor
- `src/lib/api/payments.ts` — `PaymentStatsTotals` type (`:131-137`)
- `src/components/team-switcher.tsx` — label only ("Live" → "Live enabled")
- New: a shared method-normalization map and a shared percent formatter

**Out of scope (do NOT touch)**
- Any payment, routing, retry or money-moving code.
- The recovery/incrementality panels — that is plan 003. This plan does **not** change what
  "recovered" means; it only fixes the surrounding page.
- `docs/` (deprecated).
- The A/B tests page itself — read from it, do not modify it.
- Backend `route` jsonb semantics.

## Git workflow

- backend: branch from `origin/dev` → `fix/analytics-totals-contract`
- frontend: branch from its current base → `fix/intelligence-truth-pass`
- Ship backend first; the frontend change consumes the new fields.
- Do not push or open PRs unless the operator asks.

## Steps

### Step 1 — Close the totals contract (backend)

**Get the operator's decision on the model before writing any SQL.** See Defect 1 — this is not a
matter of adding a `pending` field.

Recommended model, two independent axes over the same rows:

1. **Lifecycle buckets** — one per status, or a small agreed grouping (e.g. `in_flight` =
   `pending` + `requires_action` + `authorized`; `abandoned_expired` = `abandoned` + `expired`).
   Whatever the grouping, the invariant is that the buckets **sum exactly to `count`**, with no
   status unassigned.
2. **Authorization outcome** — `authorized_ever` (`succeeded`, `partially_captured`, `refunded`,
   `partially_refunded`, `disputed`), `did_not_authorize` (`failed`, `canceled`, `abandoned`,
   `expired`), and `undecided` (`pending`, `requires_action`, `authorized`). Success rate is
   `authorized_ever / (authorized_ever + did_not_authorize)` — undecided excluded from both sides.

Also fix `total_amount`: filtering on `status = 'succeeded'` drops refunded and disputed volume.
Report gross authorized volume, and refunded volume as its own figure, rather than netting silently.

Apply the identical change to `paymentStatsByProviderQuery` and `paymentStatsByMethodQuery`
(`payments_stats.go:45-62`, `:64+`) — they carry the same defect, so provider and method success
rates are wrong the same way. A fix that lands only on the headline makes the page *more*
contradictory, not less.

Add tests asserting: (a) buckets sum to `count` across a fixture spanning all twelve statuses;
(b) **refunding a succeeded payment does not change the success rate** — this is the regression that
matters most and the one nobody would think to write.

**Verify**: `cd backend && golangci-lint run ./... && go test ./...` (test DB on :5433, see
`backend/CLAUDE.md`). Both tests pass. `curl` the stats endpoint against a seeded org containing at
least one payment in each status and check the arithmetic closes by hand.

### Step 2 — Make recommendations addressable (backend) — STILL OPEN

**Finding from execution:** recommendations already carry an `href`
(`internal/usecase/intelligence/recommendations.go:32`), so this is narrower than planned — but two
of the five links are duds:

| Rule | Current href | Problem |
|---|---|---|
| degraded provider health | `/dashboard/connections` | the whole page, not the affected connection |
| failure spike | `/dashboard/analytics` | **links back to the page the user is already on** |
| missing backup | `/dashboard/connections` | acceptable |
| missing routing rules | `/dashboard/routing-rules` | acceptable |
| missing recovery workflow | `/dashboard/workflows` | acceptable |

Add a typed target alongside the existing `href`: kind (`connection` | `payment_filter` | `route` |
`none`) plus the identifier or query the dashboard needs. Do not encode more frontend URLs in the
backend — return `{ kind: "connection", connection_id: "..." }` and let the client build the link.
Keep `href` for backwards compatibility.

**Verify**: every one of the five rules emits a target or an explicit `none` with a reason; no rule
targets the page it is displayed on. Unit test per rule.

### Step 3 — Consume the new totals (frontend)

Extend `PaymentStatsTotals` in `src/lib/api/payments.ts` for the new shape. Stop computing the
success rate client-side (`intelligence-hero.tsx:33`, `intelligence-verdict.tsx:23`) — consume the
backend's outcome-based rate instead, so there is exactly one definition of the number.

Render the lifecycle breakdown so the arithmetic is visible rather than implied, and surface refunded
volume separately from authorized volume. Where `undecided` is non-zero, say so in the verdict
sentence rather than letting it silently depress the denominator.

**Verify**: `npx tsc --noEmit` clean. On a seeded org spanning every status, the displayed lifecycle
numbers sum to the total, and no component computes a success rate locally
(`rg -n "succeeded\s*/\s*.*count" src/components/analytics` returns nothing).

### Step 4 — Normalize method labels

Add a canonical method map in a shared util (not inside `method-table.tsx` — plan 003 and the
failures page will need it too). Map `momo` → `mobile_money` before grouping, and merge rows by the
canonical key while summing counts and volume. Recompute success rate from the merged numerator and
denominator, never by averaging the two rows' percentages. If an operator/network breakdown is
available, render it as a sub-row under the canonical method.

**Verify**: a fixture containing both `momo` and `mobile_money` renders one row whose count and
volume equal the sum, and whose success rate equals `total_succeeded / total_count`. Add that as a
unit test.

### Step 5 — Honest deltas

Extend `WoWTrend` with the absolute change. Render "↑ 345% (+38)" rather than "↑ 345%". Add a
sample floor: below it, suppress the percentage and show the absolute change plus a low-sample
marker. Reuse `hasSufficientSamples` from `src/lib/utils/ab-test-stats.ts` rather than inventing a
second threshold; if 100 is wrong for this surface, change it in one place with a comment.

Apply the same floor to `computeVolumeAnomalies` in `attention-feed.tsx:23-47` so tiny providers
stop generating alerts.

**Verify**: `wow-computation` unit tests cover below-floor, at-floor and zero-previous. A 3-payment
provider produces no anomaly.

### Step 6 — Fees column

Two acceptable outcomes; pick one with the operator:
(a) compute effective cost per successful payment from the fee data and label the column that way, or
(b) hide the column entirely when no provider in the current view has fee data.
Do not ship a column of dashes. If (a), a provider with genuinely zero fees must render `0.00`,
distinct from "no data".

**Verify**: with fee data absent the column is gone, not dashed; with fee data present every row has
a number.

### Step 7 — Split highlights from problems

Two sections: "Action required" (degraded providers, failure spikes, missing backup) and
"Highlights" (volume growth, improved success rate). Drive the split off the recommendation
severity/priority the backend already returns, plus the sign of the anomaly.

**Verify**: a provider whose volume grew appears under Highlights and never under Action required.

### Step 8 — Truthful charts for sparse data

Below a data-point threshold (suggest < 10 buckets), switch the hero and decline-reasons charts to
bars or a line with visible dots, and drop the gradient fill. Above it keep the current treatment.

**Verify**: a 4-point series renders discrete marks; a 30-point series is unchanged. Snapshot or
explicit render test.

### Step 9 — Org card label

`src/components/team-switcher.tsx:117-126` → "Live enabled" / "Sandbox only". Logic unchanged.

**Verify**: `grep -n "Live enabled" src/components/team-switcher.tsx` matches; the mode toggle is
untouched.

## Test plan

- Backend: totals invariant test across all statuses; recommendation-target unit test per rule;
  full `go test ./...` + `golangci-lint run ./...` at 0 issues.
- Frontend: unit tests for method merging, WoW absolute + floor, chart mode switching. Extend the
  existing `attention-feed.test.tsx`. `npx tsc --noEmit` clean.
- Manual: load `/dashboard/analytics` against a seeded org containing pending payments, both method
  aliases, a zero-fee provider and a 3-payment provider. Every defect above must be visibly gone.

## Done criteria

- [ ] Lifecycle buckets sum exactly to `count` across all twelve statuses, asserted by a test
- [ ] Refunding a succeeded payment provably does not move the success rate (regression test)
- [ ] `total_amount` no longer drops refunded/disputed volume; refunded volume reported separately
- [ ] Provider and method breakdowns carry the identical fix, not just the headline
- [ ] Success rate is computed once, in the backend; no frontend component recomputes it
- [ ] The page displays the full lifecycle breakdown; no unexplained residual
- [ ] `momo` and `mobile_money` render as one row with correctly recomputed success rate
- [ ] Every percentage delta carries its absolute change; below-floor samples suppress the percentage
- [ ] Small providers no longer generate volume anomalies
- [ ] No dash-only Fees column
- [ ] Highlights and Action required are separate sections
- [ ] Sparse series render as discrete marks
- [ ] Org card reads "Live enabled"
- [ ] Every recommendation carries a target or an explicit `none`
- [ ] `golangci-lint run ./...` 0 issues, `go test ./...` green, `npx tsc --noEmit` clean
- [ ] Only in-scope files touched in both repos
- [ ] README status row updated

## STOP conditions

- Step 1's model changes the meaning of an existing field for an API consumer → stop and report.
  Adding fields is safe. Changing what `succeeded` or `amount` means is a breaking change and needs
  a version decision — and note the SDKs and the public OpenAPI spec both carry this contract.
- The outcome-based success rate turns out to move an existing customer-facing number materially
  (e.g. a merchant's dashboard rate jumps several points overnight) → do not ship silently. It is the
  *correct* number, but it needs a changelog entry and, for anyone quoting it, a heads-up.
- The stats query cannot express the new buckets without a materially slower plan → report the
  measured regression before shipping; a correct-but-slow Intelligence page is still a regression.
- `momo` turns out to be a distinct rail rather than an alias (check with the backend owner and
  against the Moolre/Hubtel adapters) → do not merge the rows; label them distinctly instead.
- You find yourself editing anything under `internal/usecase/payments/` beyond read-path analytics
  → out of scope, stop.

## Maintenance notes

- The canonical method map from Step 4 is the seed of the wider taxonomy work in plan 004. Put it
  somewhere both can import; do not bury it in a component.
- The sample floor introduced in Step 5 is the same concept plan 003 needs for incrementality
  confidence. One threshold, one place.
- Reviewer: the highest-value check is Step 1's invariant test. If the totals close, the page can be
  used in a sales call; if they do not, nothing else here matters.
