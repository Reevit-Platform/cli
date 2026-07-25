# 2026 H2 — Recovery, Proof and Trust

**Planning window:** 25 July – 24 December 2026
**Planned at:** root `675baed`, backend `26641116` (dev), frontend `2469201`, mintlify-docs `f27155e` (main)
**Baseline refreshed 25 July:** backend `dev` fast-forwarded to `0db0a596`; frontend `dev` created at
`7aafb3a`. No cited evidence changed — verified with
`git diff --stat 26641116..0db0a596` over every file these plans reference (empty).
**Input:** the 24–25 July competitive audit of the public site, plus a first-party read of
`backend/`, `frontend/` and `mintlify-docs/`.

> **Repo scope note.** Three repos are in scope: `backend/`, `frontend/`, `mintlify-docs/`.
> The `docs/` submodule (docs-v2) is **deprecated — do not edit it, do not plan against it.**
> `mintlify-docs/` is the documentation source of truth.

## 0. Working branches and a submodule trap

**Branches for this work:**

| Repo | Branch | Base | Note |
|---|---|---|---|
| `backend/` | `dev` | — | long-lived integration branch; at `0db0a596`. Cut feature branches from it per each plan's Git workflow section. |
| `frontend/` | `dev` | `origin/main` @ `7aafb3a` | created 25 July; no `dev` existed before. No upstream set yet. |
| `mintlify-docs/` | `main` | — | branch per plan as needed. |

**Submodule trap — read this before executing any frontend plan.**

The `frontend/` directory is a checkout of **`Reevit-Platform/frontend-start`** (the TanStack Start
app), *not* `Reevit-Platform/frontend`. `.gitmodules` still declares:

```
submodule.frontend.url       = git@github.com:Reevit-Platform/frontend.git      # stale
submodule.frontend-start.url = git@github.com:Reevit-Platform/frontend-start.git
```

and the root index still tracks **both** `frontend` (`29f57da`, from the old repo) and
`frontend-start` (`a2240b0`), while the `frontend-start/` directory has been deleted from disk. This
is why root `git status` shows ` M frontend` and ` D frontend-start`.

Consequences for anyone executing these plans:

- Every `frontend/…` path in plans 001, 002, 003 and 006 refers to the **TanStack Start app**, which
  is what is actually on disk and actually deployed. The evidence was read there. It is correct.
- A fresh `git submodule update --init` at the root will clone the **wrong** repo into `frontend/`.
- The root superproject cannot record frontend submodule pointers correctly until `.gitmodules` and
  the index are reconciled.

Reconciling this is **not** part of any plan below — it is a repo-hygiene task that needs an explicit
decision (rename the submodule path, repoint the URL, and drop the dead `frontend-start` entry).
It should be done before anyone else clones this monorepo.

---

## 1. Why this plan differs from the audit

The audit is an outside-in read of the public site. It is accurate about trust, proof and
positioning, and materially wrong about product state, because most of what it says is missing
is already merged. Reconciling it against the code changes the sequencing substantially.

| Audit claim | Code reality | Evidence |
|---|---|---|
| "No productized, automated reconciliation" | Shipped. Nightly worker, pure matcher, 4 mismatch kinds, resolve-with-audited-note, manual run-now, settlement CSV export, dashboard queue. | `backend/internal/usecase/recon/`, `internal/domain/recon/recon.go`, `internal/worker/reconstatement/`, migration `000129` |
| "No payout operations" | Shipped. Guarded state machine, beneficiary book, bulk (100/batch), provider webhooks, balance, name-enquiry, cancel. | migrations `000126`/`000127`, `worker/payoutreconcile` |
| "Routing A/B tests exist but ROI is unproven" | Wired into the live router. Deterministic bucketing, guardrail auto-pause, promote-winner. | `internal/usecase/payments/payment_router.go:97-107`, `service_checkout.go:323-408`, `internal/usecase/routingabtests/` |
| "No provider-agnostic credential portability" | Partially shipped. Portable (MoMo MSISDN+network, bank) vs `psp_locked` (card token), `chargeable_providers` per method, typed `ErrTokenNotPortable`. | `internal/domain/paymentmethod/portability.go` |
| "Basic failure categories are not enough" | A 6-value `ErrorCategory` enum plus `FaultParty` and `IsRetriable` are persisted per payment; 8 adapters map raw codes. | `internal/domain/payment/provider_error.go:8-18`, `usecase/payments/service.go:2073-2082` |
| Privacy policy names a different legal entity | The repo is consistent — `Connectivity Support Systems LTD` in all 7 occurrences, zero occurrences of "Reevit Technologies". **The live site is serving a stale build.** | `frontend/src/routes/__root.tsx:71`, `components/landing/PrivacyPage.tsx:267`, `components/landing-v3/footer.tsx:248` |

So "build reconciliation, prove ROI, add portability" is largely a **deployment, provider-coverage
and marketing** problem. The build gap is narrower — and the one part that genuinely does not
exist is cheaper than expected, because the machinery for it already ships.

## 2. The actual gap

**There is no holdout, control group, counterfactual or incrementality logic anywhere in the codebase.**

"Recovered by smart routing" is computed in `backend/adapters/repo/payment_repo_analytics.go:204-221`
as: `jsonb_array_length(route) > 1` AND `route->0->>'provider' != route->-1->>'provider'` AND
`status = 'succeeded'`. Every reroute that eventually worked is claimed as recovered revenue. There
is no attempt to establish what would have happened without Reevit.

That is exactly the metric the strategy names as its north star, and today it is unfalsifiable.
It is also the only item on the competitor list that cannot be bought, copied or out-funded —
MoneyHash can add connectors faster, Gr4vy can certify faster, but nobody else has Ghana-route
failure data with a credible counterfactual attached.

**Why it is cheap:** the A/B test system already performs deterministic per-customer bucketing,
variant assignment, guardrail auto-pause and outcome recording *inside the live router*. A recovery
holdout is that same machinery with a "recovery suppressed" variant. On the frontend,
`src/lib/utils/ab-test-stats.ts` already implements significance, confidence intervals and a
`minSamples = 100` floor, and `StatisticalSignificanceCard` already renders an "Insufficient Data"
state. Nothing in `src/components/analytics/` imports either. Most of plan 003 is wiring, not
invention.

## 3. Position

Unchanged from the audit, and the code supports it:

> **The most trusted way for Ghanaian and West African digital businesses to measure, recover and
> operate payments across the provider accounts they already own.**

- **Primary buyer:** founder / CTO / Head of Payments / finance-ops lead at a Ghanaian or West
  African digital business already running two or more PSPs.
- **Primary user:** developer or payment-operations manager.
- **Not the priority this window:** single-payment-link micro-merchants with no reliability problem.

## 4. Workstreams

Six plans. 001, 002 and 006 are credibility debt and should land first because they are cheap and
they gate the ability to use the product as its own proof. 003 is the strategic bet. 004 deepens
it. 005 removes the ceiling on 001–003 being sellable beyond Paystack.

| Plan | Title | Repos | Priority | Effort | Depends on | Status |
|------|-------|-------|----------|--------|------------|--------|
| 001 | Intelligence page truth pass | frontend, backend | P1 | M | — | **DONE** — backend `3aeb5f5f`/`33f18b77`, frontend `62f6c09`/`345dbfe`. Follow-up: per-connection deep link needs a route/URL param on `/dashboard/connections`. |
| 002 | Single-source plan entitlements + delete the dead landing tree | frontend, backend | P1 | M | — | **PARTIAL** — frontend `9a1da2a`, mintlify `3a4b8d4`, backend `493cdc0e`: one rate table, GHS base + default, Free webhook limit, copy test, docs rate-limit + retention, Free audit logs, entitlements pinned by test, PRICING.md. **Open:** (a) `smart_routing` is true on *every* plan incl. Free but marketed as Growth-only — pricing decision, not a copy fix; (b) enterprise `monthly_price_cents` reads as ₵1,000 under the GHS base; (c) webhook/workflow allowances still not modelled in `plans.features`. |
| 003 | Recovery incrementality: holdout, recovered value, honest labelling | backend, frontend | P1 | L | 001 | **PARTIAL** — Steps 2, 5, 6, 8 done (backend `93eaf1f8`/`d0a2b92b`, frontend `5cc9e7d`): observed/incremental split shipped in the API and the panel, recovered value net of fees, eligibility rule. **Remaining:** Step 1 (persist arm), 4 (shadow), 7 (route receipt), and Step 3 suppression — still gated on **Q1 volume** and **Q4 consent**. |
| 004 | Normalized decline taxonomy + class-aware recovery policy | backend | P2 | L | 003 (shared attribution) | **PARTIAL** — Steps 1–4 done (`0733c9d5`): 12-class registry mapping all 31 emitted codes with alias collapse, behavior-neutral and tested; per-provider unknown-share coverage metric naming the codes to map next. **Remaining:** Steps 5–6 (policy table, shadow run, class-aware enforcement) need the production export and a shadow review. |
| 005 | Provider coverage depth: statements + payouts beyond Paystack | backend | P2 | L | — | **BLOCKED** — Step 1 contract note done. Steps 2/3/5/6 need Hubtel, Flutterwave and PawaPay **sandbox credentials**; the plan forbids shipping payout adapters on unit tests alone. |
| 006 | Claim integrity: deploy freshness, docs contradictions, trust surface | frontend, mintlify-docs, ops | P1 | M | — | **PARTIAL** — Steps 1, 2, 4, 5 done. **Production is current and two audit findings are retired** (legal entity, SDK publishing). Docs rate-limits + retention corrected (`3a4b8d4`), generated SDK matrix (`7f80839` + root `fa75302`), honest sourced comparison page (`f71a9d3`). **Remaining:** Step 3 (compliance status pass), 6 (Trust Center), 7 (counsel engagements). **Escalation:** the live privacy policy misstates retention in a legal document. |

Status values: TODO | IN PROGRESS | DONE | BLOCKED (one-line reason) | REJECTED (one-line rationale)

### Suggested sequencing across the window

- **Aug** — 001, 002, 006. All three are contained, and together they mean nothing on the public
  surface or the Intelligence page contradicts anything else. 003 design spike runs in parallel.
- **Sep–Oct** — 003. Holdout behind a flag, shadow first, then controlled traffic. 005 starts in
  parallel (independent, different files).
- **Oct–Nov** — 004, once 003 has produced enough labelled outcome data to justify per-class
  policies rather than guessing at them.
- **Nov–Dec** — measurement readout, case studies from real holdout numbers, 006 phase 2
  (trust centre with evidence).

## 5. Non-goals for this window

Recording these explicitly so they do not get re-litigated:

- **No new PSP connectors.** The gap is depth on the eight that exist, not breadth. See 005.
- **No card vault / network tokens.** It changes PCI scope and audit obligations. Partner
  evaluation only, no build. The existing portability model already covers the MoMo case, which
  is the one that matters on Ghana rails.
- **No ML routing.** 003 must generate clean labelled data with a control arm first. Deterministic
  rules plus rolling Bayesian estimates until offline evaluation shows stable lift.
- **No Shopify.** Revisit only if target-account interviews demand it; WooCommerce first if any.
- **No general-purpose workflow expansion.** Workflows serve recovery, reconciliation and customer
  comms only.
- **Do not lead marketing with MCP.** Keep it as a developer differentiator.

## 6. Scorecard

**North star:** incremental recovered GMV per live merchant, measured against a holdout.
Explicitly *not* total processed volume, and *not* every successful fallback.

Product:
- eligible payment attempts
- canonical failure-code coverage (share of provider failures mapping to a real class vs `unknown`)
- incremental authorization lift (holdout vs treatment, with CI)
- recovered GMV and added provider cost per recovered GHS
- duplicate-debit rate (must stay 0), false-reroute rate
- P50/P95 orchestration overhead
- reconciliation auto-match rate, per provider

Trust:
- documentation contradiction count (target 0; 001/002/006 drive this)
- prod-vs-`main` drift (target: no stale public claims)
- security questionnaire turnaround
- SDK install smoke-test pass rate

## 7. Open questions to resolve before 003 builds

1. **Is production running current `main`?** The stale privacy-policy entity strongly suggests not.
   Resolve in 006 step 1 — it determines how much of the audit is a real defect versus a deploy gap.
2. **Is there enough volume for a holdout to reach significance inside this window?** The audit's
   sample screen showed 49 payments in a period. At that scale a 5–10% holdout never reaches
   significance. If pilot volume cannot support it, 003 changes shape: shadow-mode counterfactual
   and pooled cross-merchant estimation, with per-merchant numbers reported as observed-only until
   the sample supports more. **Decide this before writing the holdout code, not after.**
3. **Who owns product truth** across site, docs, API and billing? 002 and 006 both assume a single
   owner exists; without one the drift returns within a quarter.
