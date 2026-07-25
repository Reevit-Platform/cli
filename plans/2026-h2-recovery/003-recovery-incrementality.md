# Plan 003: Recovery incrementality — holdout, recovered value, honest labelling

> **Executor instructions**: This plan has a mandatory design gate (Step 0). Do not write code until
> it closes. Follow the remaining steps in order, run every verification, and honour the STOP
> conditions — this plan touches the live payment path and the bar is higher than the others. When
> done, update the status row in `plans/2026-h2-recovery/README.md`.
>
> **Drift check (run first)**: in `backend/`,
> `git diff --stat 26641116..HEAD -- internal/usecase/routingabtests internal/usecase/payments adapters/repo/payment_repo_analytics.go internal/domain/routing`;
> in `frontend/`, `git diff --stat 2469201..HEAD -- src/components/analytics src/lib/utils/ab-test-stats.ts`.

## Status

- **Priority**: P1 — this is the strategic bet of the window
- **Effort**: L
- **Risk**: HIGH (deliberately suppresses recovery for a slice of real payments)
- **Depends on**: 001 (the page must add up before it carries a revenue claim)
- **Blocks**: any public case study, ROI calculator, or "recovered GHS" marketing claim
- **Category**: measurement / product
- **Repos**: `backend/`, `frontend/`

## Why this matters

Reevit's whole pitch reduces to one number: how much revenue would have been lost without it. Today
that number does not exist. What exists is a proxy that flatters:

`backend/adapters/repo/payment_repo_analytics.go:204-221` defines recovered as

```
jsonb_array_length(route) > 1
AND route->0->>'provider' != route->-1->>'provider'
AND status = 'succeeded'
```

Every reroute that eventually succeeded is booked as recovered revenue. Some of those payments would
have succeeded on a plain same-provider retry, or on the customer's own second attempt, or were
transient failures that resolved themselves. A repo-wide search for holdout, control, counterfactual
or incrementality returns nothing relevant. `CompareResults` in
`backend/internal/domain/routing/abtest.go:143-186` has a sample-size heuristic (≥100 tx/variant),
not an experiment framework.

The audit is right that this must be labelled "observed" until proven. But labelling alone leaves
Reevit with no defensible number, and the competitor set — Payrails, Primer, Gr4vy — all sell
measured lift. Incrementality is the one thing on that list that cannot be bought or out-funded:
it requires Ghana-route failure data with a control arm, which only Reevit is positioned to collect.

**And it is cheaper than it looks.** The A/B test system already does deterministic per-customer
bucketing, variant assignment, guardrail auto-pause and outcome recording *inside the live router*
(`internal/usecase/payments/payment_router.go:97-107` → `service_checkout.go:323-408`, service in
`internal/usecase/routingabtests/`). The frontend already has significance maths and an
"Insufficient Data" state (`src/lib/utils/ab-test-stats.ts`, `StatisticalSignificanceCard` in
`routes/dashboard/ab-tests/-components/ab-test-results.tsx`) that nothing in
`src/components/analytics/` imports. Much of this plan is wiring existing parts together.

## Current state

**What exists and is reusable**

| Capability | Where |
|---|---|
| Deterministic bucketing + weighted variant selection | `internal/domain/routing/abtest.go` (`ShouldIncludeInTest`, `SelectVariant`) |
| Live-router experiment hook | `internal/usecase/payments/payment_router.go:97-107`; `resolveABTestRoutes` in `service_checkout.go:323-408` |
| Outcome recording | `maybeRecordABTestResult`, `service_checkout.go:411-438`; `routingabtests.RecordResult:333-349` |
| Guardrail auto-pause | `internal/usecase/routingabtests/` |
| Route trace per payment | `payments.route` jsonb (ordered attempts with provider) |
| Failure class on the payment | `DeclineReason`, `DeclineCode`, `ProviderErrorCategory`, `IsRetriable`, `FaultParty` — `service.go:2073-2082` |
| Significance maths + insufficient-data UI | `frontend/src/lib/utils/ab-test-stats.ts`, `ab-test-results.tsx:54-208` |

**What does not exist**
- Any holdout, control arm or counterfactual.
- Recovered *value* — `StatsRouting` reports counts and a `recovered_amounts[]` by currency, but no
  added provider cost and therefore no net figure.
- Any distinction in the API or the UI between observed reroute success and incremental lift.
- Retries participating in experiments at all — `resolveRetryRoutes` (`service.go:1665-1713`)
  explicitly bypasses A/B routing so failure-biased retry traffic cannot pollute experiments
  (comment at `:1671-1672`). **This is directly load-bearing for the design: the holdout must
  cover the retry path, which is exactly the path currently excluded.**

## Step 0 preparation (25 Jul) — what the code answers, and what it cannot

The gate below still has to be closed by the operator. This section removes everything that could
be settled from the codebase so the remaining questions are only the ones that genuinely need a
human decision.

### Settled from code

**Q2 (unit of randomization) — use the customer, and the machinery exists.**
`internal/domain/routing/abtest.go` already implements deterministic per-customer bucketing
(`ShouldIncludeInTest`, `SelectVariant`) and it is already consulted on the live path via
`resolveABTestRoutes`. Per-payment randomization would need a *new* mechanism; per-customer reuses
a tested one. Accept the smaller effective sample in exchange for not building a second bucketing
system — the plan's own STOP condition forbids two.

**Q3 (what the control arm suppresses) — it must cover the delayed retry path, and that is the
hard part.** `resolveRetryRoutes` (`service.go:1665-1713`) *explicitly bypasses A/B routing* so
that failure-biased retry traffic cannot pollute experiments (comment at `:1671-1672`). That
exclusion is load-bearing for the existing feature and directly in the way of this one: a holdout
that only covers in-request failover measures a fraction of recovery. Whoever implements this must
decide how retries participate without corrupting the routing experiments that already run.

**Q5 (eligibility) — the signal already exists per payment.** `ProviderError.Retryable` gates
in-request failover (`errors.go:74-79`), and `IsRetriable`, `ProviderErrorCategory` and
`FaultParty` are persisted on every payment (`service.go:2073-2082`). Eligibility can be written
against stored fields rather than requiring new instrumentation. Exclude at minimum: `fraud`
category, `Retryable == false`, and Moolre (retries are hard-blocked at `service.go:1435-1439`).

**Safety guards are already in place and must be re-verified, not rebuilt:** `ClaimRetry`
compare-and-set (`payment_repo_sqlc.go:474-491`), pending-stops-failover
(`attempt_executor.go:31-32`), and required idempotency keys on money-moving mutations.

### Still open — operator only

**Q1 (volume) is the gating question and I cannot answer it.** It needs production data I have no
access to. Until someone runs the numbers, note the arithmetic: detecting a 2-percentage-point
absolute lift at 80% power needs on the order of several thousand eligible *failed* payments per
arm. The audit's sample screen showed 49 payments total in a period. If pilot volume is anywhere
near that, a per-merchant holdout will never reach significance inside this window and the plan
must be rescoped to shadow-mode plus pooled cross-merchant estimation **before** any UI is built.

**Q4 (consent) is a policy and contract question**, not an engineering one. The control arm
deliberately lets recoverable payments fail.

### Recommended sequence given the above

Build Step 1 (persist the arm) and Step 4 (shadow mode) first regardless of how Q1 resolves —
both are safe, neither suppresses anything, and shadow data is what actually answers Q1 with real
numbers instead of an estimate. Hold Step 3 (suppression) until Q1 and Q4 are both closed.

## Step 0 — Design gate (MANDATORY, no code)

Close all five questions in writing and get operator sign-off. Append the answers to this file.

**Q1. Is there enough volume?** The audit's sample screen showed 49 payments in a period. A 5%
holdout on 49 payments is two payments; it will never reach significance. Pull actual eligible-failure
volume per pilot merchant for the last 90 days and compute the detectable effect size at the
available sample. If per-merchant significance is out of reach inside this window, the design
changes: pooled cross-merchant estimation as the headline, per-merchant numbers reported as
observed-only with an explicit "insufficient data for incremental" state. **Decide now.** Building
per-merchant holdout UI that can never populate is the main failure mode of this plan.

**Q2. What is the unit of randomization?** Per payment is simplest and gives the most samples, but
leaks: a customer who fails, is held out, and retries manually contaminates the arm. Per customer is
cleaner and matches the existing deterministic bucketing, but reduces effective sample size. Choose
one and write down the contamination you are accepting.

**Q3. What exactly does the control arm suppress?** Candidates: (a) in-request failover only,
(b) delayed retry only, (c) both. Each measures a different product claim. (a) is the narrowest and
safest; (c) matches the marketing claim but withholds the most value from real merchants.

**Q4. Consent and ethics.** The control arm deliberately lets some real payments fail that Reevit
could have recovered. That is defensible for measurement, and indefensible if undisclosed. Required:
explicit per-merchant opt-in, a hard cap on holdout share, a documented kill switch, and language in
the contract or dashboard that says plainly what the holdout does. **A merchant must never discover
this from a support ticket.** Get operator and, if available, counsel sign-off before Step 1.

**Q5. Eligibility.** Which failures enter the experiment at all? At minimum exclude: fraud blocks,
hard declines where no route could succeed, duplicate-suspect attempts, and anything where the
non-retryable path already applies (`ProviderError.Retryable == false`, `errors.go:74-79`). Holding
out a payment that was never recoverable adds noise and costs the merchant nothing — but it also
biases the estimate if the eligibility rule differs between arms. **The rule must be evaluated
identically in both arms, before assignment.**

**Gate**: a written answer to each, operator sign-off recorded, and an agreed target effect size and
run length. Without Q1's answer this plan does not start.

## Scope

**In scope**

Backend:
- A recovery-holdout experiment type reusing `routingabtests` rather than a parallel system.
- Eligibility evaluation, assignment and suppression on both the in-request failover path
  (`service.go:1822-2055`) and the delayed retry path (`resolveRetryRoutes`, `service.go:1665-1713`).
- Arm assignment persisted per payment so analytics can partition after the fact.
- Recovered value: GHS recovered, added provider cost, net recovered.
- Analytics split into `observed` and `incremental` blocks with sample size and CI.
- Route receipt per payment: why this provider, what failed, why the next was tried, latency, outcome.
- Kill switch, holdout cap, and audit rows on every assignment.

Frontend:
- Rework the "Recovered by smart routing" panel (`src/components/analytics/charts/retry-reroute-analytics.tsx`)
  into observed vs incremental, with the existing significance card driving an explicit
  insufficient-data state.
- Recovered GHS, added cost and net, not just counts.
- Route receipt in the payment detail sheet.
- Merchant-facing holdout control: on/off, current share, what it does, in plain language.

**Out of scope (do NOT touch)**
- Any change to routing *decisions* outside the suppression itself. This plan measures the current
  router; it does not improve it. Improving it is plan 004.
- ML or contextual bandits. Deterministic suppression and frequentist readout only.
- The existing routing A/B test feature's semantics for merchants already using it.
- Pricing on recovered GMV. Outcome-aligned pricing is a later decision and needs defensible
  attribution first — which is what this plan builds.
- `docs/` (deprecated).

## Git workflow

- backend: `origin/dev` → `feat/recovery-holdout` (expect several stacked branches; this is not one PR)
- frontend: → `feat/incremental-recovery-panels`
- Every backend slice ships behind a feature flag, default **off**.

## Steps

### Step 1 — Persist the arm

Add holdout arm (`treatment` | `control` | `not_enrolled`) and the experiment id to the payment
record, written at assignment time inside the existing transition machinery so it lands atomically
with the payment state and its audit row (follow the `applyPaymentTransition` pattern in
`internal/usecase/payments/hardening.go`). Migration numbered **`000146+`** per `backend/CLAUDE.md`;
validate up/down on a fresh DB.

Assignment happens **once**, at first eligible failure, and is immutable thereafter — a payment must
not switch arms between its first failure and its delayed retry.

**Verify**: a payment that fails, is retried and fails again carries one arm value throughout.
Migration clean on a fresh DB. `go test ./...` + `golangci-lint run ./...` green.

### Step 2 — Eligibility, evaluated identically in both arms

Implement the Q5 rule as a pure function over the failure context, with its own unit tests. It runs
**before** assignment, so the same payments are eligible regardless of arm. Log the eligibility
verdict and reason.

**Verify**: table-driven test covering every exclusion; a fraud-blocked payment is never enrolled;
a `Retryable == false` provider error is never enrolled.

### Step 3 — Suppression, behind a flag

In the treatment arm nothing changes. In the control arm, suppress whatever Q3 selected. The
suppressed payment must reach a normal terminal failed state indistinguishable to the merchant's
integration — same webhooks, same status, same error shape. **A control-arm payment must never be
observably different to the payer or to the merchant's code.**

Cap the holdout share in config with a hard ceiling, independent of per-merchant settings. Add a
global kill switch that returns everything to treatment immediately without a deploy.

**Verify**: with the flag off, byte-identical routing behaviour to today (regression test over the
existing routing test suite). With the flag on at 0%, likewise. At a non-zero share, control-arm
payments skip the suppressed path and emit the identical event/webhook sequence a genuine failure
emits. Kill switch tested.

### Step 4 — Shadow mode first

Before suppressing anything on live traffic, run assignment and eligibility in shadow: record what
*would* have been held out, suppress nothing. Let it run long enough to validate arm balance,
eligibility rates and volume projections against Q1's estimate.

**Verify**: shadow arm balance within tolerance of the configured split; measured eligible volume
matches the Q1 projection within a stated margin. If it does not, return to Step 0 Q1 — the run
length was wrong.

### Step 5 — Recovered value and cost

Extend the routing stats to report, per currency: recovered payment count, recovered amount, added
provider cost attributable to the extra attempts, and net. Fee data already exists per provider
(the `/payments/fees` path feeding `provider-table.tsx`); reuse it rather than inventing a second
cost model. Where cost cannot be attributed, return an explicit unknown — never zero.

**Verify**: a seeded scenario with known fees produces the arithmetic by hand-check and by unit test.
Unknown-cost cases surface as unknown, not as 0.

### Step 6 — Split observed from incremental in the API

The routing stats response gains two clearly separated blocks:

- `observed` — today's numbers, unchanged, explicitly labelled as reroute outcomes, not attribution.
- `incremental` — treatment vs control success rate, absolute lift, CI, sample size per arm, and a
  status enum covering at least `insufficient_data`, `not_enrolled`, `available`.

Never emit an incremental figure without its sample size and interval attached in the same object.
Make it structurally impossible for a client to render lift without confidence.

**Verify**: contract test asserting `incremental` is either a full object with all fields or an
explicit status; there is no partial shape.

### Step 7 — Route receipt

Per payment, expose the decision trace: connections considered, why the first was chosen (health,
fee, rule, chain), what failed with which normalized class, why the next was attempted, per-attempt
latency, final outcome, and the arm. Most of this is already in `payments.route` plus the decline
fields; this is largely a read model over existing data.

**Verify**: a rerouted payment's receipt reconstructs its full journey and matches the `route` jsonb
and the recorded decline fields.

### Step 8 — Frontend: honest recovery panel

Rework `retry-reroute-analytics.tsx`. Two clearly distinct areas:

- **Observed** — retried share, retry success, reroute count and success, provider paths. Labelled as
  observed outcomes, not attribution. Same numbers as today, honest framing.
- **Incremental** — lift with CI, recovered GHS, added cost, net, sample per arm. When the API
  returns `insufficient_data`, render the existing `StatisticalSignificanceCard` insufficient state
  rather than a number. Import from `src/lib/utils/ab-test-stats.ts`; do not reimplement.

Nowhere on this page may a percentage appear without either a sample size or an absolute count
beside it (this is the plan 001 rule, applied to the highest-stakes numbers on the page).

**Verify**: with a small sample the page shows insufficient-data and no lift figure. With a
sufficient sample it shows lift with CI. `npx tsc --noEmit` clean.

### Step 9 — Merchant-facing control

A settings surface where the merchant sees the holdout is on, what share, what it does in plain
language, the measured lift to date, and can turn it off. Default **off**; enabling is an explicit,
audit-logged action.

**Verify**: enabling and disabling both write audit rows; disabling takes effect on the next payment
without a deploy; the explanatory copy has been read and approved by the operator.

## Test plan

- Unit: eligibility table, assignment determinism and immutability, suppression path, value/cost
  arithmetic, incremental-response contract shape.
- Regression: full existing routing and payments suites must be unchanged with the flag off. This is
  the most important test in the plan.
- Integration: a control-arm payment emits the identical webhook/event sequence as a natural failure.
- Load/shape: arm balance within tolerance over a simulated run.
- Frontend: insufficient-data rendering, lift rendering, no bare percentages.
- Gates: `golangci-lint run ./...` 0 issues, `go test ./...` green, `npx tsc --noEmit` clean.

## Done criteria

- [ ] Step 0 answered in writing with operator sign-off appended to this file
- [ ] Arm persisted immutably per payment, atomically with its audit row
- [ ] Eligibility is a tested pure function evaluated identically in both arms
- [ ] Flag off ⇒ provably identical behaviour to today
- [ ] Holdout share capped in config; kill switch tested
- [ ] Shadow mode run and reconciled against the Q1 volume projection before any live suppression
- [ ] Recovered GHS, added cost and net reported; unattributable cost reported as unknown
- [ ] API separates `observed` from `incremental`; incremental always carries sample size and CI
- [ ] Route receipt reconstructs a rerouted payment end to end
- [ ] Dashboard shows insufficient-data instead of a lift number on small samples
- [ ] Merchant-facing control exists, defaults off, audit-logged
- [ ] Zero duplicate-debit incidents attributable to this work
- [ ] README status row updated

## STOP conditions

- **Q1 shows the available volume cannot reach significance in this window** → stop, return to the
  operator, and re-scope to shadow-mode counterfactual plus pooled estimation. Do not build
  per-merchant holdout UI that will never populate.
- **No merchant consent mechanism is agreed** → do not suppress a single live payment. Shadow mode
  only.
- **The flag-off regression suite is not byte-identical** → stop. A measurement feature that changes
  routing behaviour when disabled has invalidated its own baseline.
- **Any duplicate debit is observed** in shadow or live → kill switch, full stop, root cause before
  anything else. The existing guards (`ClaimRetry` CAS, pending-stops-failover, idempotency) must be
  re-verified against the new path.
- **Suppression turns out to be observable** to the merchant integration (different error shape,
  missing webhook, different timing signature) → fix before proceeding; an observable control arm is
  both a broken experiment and a support incident.
- You find yourself adding a second experiment framework alongside `routingabtests` → stop and
  reconcile. Two bucketing systems in one router is how double-assignment bugs happen.

## Maintenance notes

- The eligibility function is the highest-churn part; keep it pure and keep its tests exhaustive.
- Plan 004 will want to condition recovery policy on the failure class. Design the arm and receipt
  data so a per-class lift breakdown falls out without a schema change.
- Once incremental lift is real and stable, it becomes the case-study input, the ROI-calculator
  input, and the only honest basis for outcome-aligned pricing. Sequence: measure, publish, then
  price — never the reverse.
- Reviewer: the two checks that matter are the flag-off regression and the consent mechanism.
  Everything else can be iterated; those two cannot be undone after the fact.
