# Plan 004: Normalized decline taxonomy + class-aware recovery policy

> **Executor instructions**: Follow this plan step by step. Run every verification and honour the
> STOP conditions — this changes retry behaviour on live money. When done, update the status row in
> `plans/2026-h2-recovery/README.md`.
>
> **Drift check (run first)**: in `backend/`,
> `git diff --stat 26641116..HEAD -- internal/domain/payment/provider_error.go adapters/psp internal/usecase/payments internal/domain/subscription`.

## Status

- **Priority**: P2
- **Effort**: L
- **Risk**: HIGH (changes when and whether real payments are retried)
- **Depends on**: 003 (soft) — the arm and receipt data make per-class lift measurable, so land 003's
  measurement first or this plan cannot prove it helped
- **Category**: product / recovery engine
- **Repos**: `backend/`

## Why this matters

The audit's core product ask is "decline-aware, provably incremental recovery". Plan 003 supplies the
provably-incremental half. This plan supplies the decline-aware half.

The current state is genuinely mixed, and the distinction matters:

- **In-request failover *is* failure-class aware.** It stops when `ProviderError.Retryable` is false
  (`internal/usecase/payments/errors.go:74-79`, consumed at `service.go:2032-2033`), and adapters set
  `Retryable: false` on hard declines. That part works.
- **Delayed retries are blind.** `scheduleRetry` (`internal/usecase/payments/schedule_retry.go:14-41`)
  applies a delay schedule with no reference to `IsRetriable`, the error category, or the fault party
   — all three of which are already persisted on the payment (`service.go:2073-2082`).
- **The subscription ladder is blind.** `subscription.DefaultRetryPolicy()`
  (`internal/domain/subscription/retry_policy.go:13-22`) is a flat 1h / 6h / 24h / 72h, max 4, applied
  identically to an insufficient-funds decline and a provider timeout. Those two failures want
  opposite treatment: the first wants a delay aligned to when the customer is likely funded, the
  second wants an immediate reroute.

Underneath, the normalization layer is thinner than it looks. There are six real categories —
`declined`, `invalid`, `unavailable`, `transient`, `fraud`, `unsupported`
(`internal/domain/payment/provider_error.go:8-18`) — but `ProviderError.Code` is a **free string**,
and each adapter invents its own. The union across adapters already contains near-duplicates
(`suspected_fraud` vs `fraud_suspected`, `currency_unsupported` vs `unsupported_currency`,
`provider_timeout` vs `timeout`, `invalid_cvc` vs `invalid_security_code`). Moolre emits essentially
nothing (`moolre_rejected`, `provider_record_not_found`). Six categories are too coarse to drive a
recovery decision; free-string codes are too inconsistent to aggregate.

## Current state

**Exists**
- `ErrorCategory` enum, 6 values — `internal/domain/payment/provider_error.go:8-18`
- `ProviderError{Code, ProviderMsg, Retryable, Category}` with the raw provider message preserved —
  `:29-35`, constructor `:101-109`
- Derived fault party (`customer` / `processor` / `merchant_config` / `platform`) — `:46-88`
- Persisted per payment: `DeclineReason`, `DeclineCode`, `ProviderErrorCategory`, `IsRetriable`,
  `FaultParty` — `service.go:2073-2082`
- Per-adapter mappers, quality varying widely:

  | Adapter | Mapper | Coverage |
  |---|---|---|
  | Paystack | `mapPaystackGatewayResponse`, `mapPaystackError` | broadest |
  | Stripe | `mapStripeError`, `mapStripeDecline` | typed errors + decline codes |
  | Flutterwave | `mapFlutterwaveProcessorResponse`, `mapFlutterwaveError` | modest |
  | Hubtel | `mapHubtelError(WithStatus)` | small |
  | M-Pesa | `mapMpesaResultCode`, `mapMpesaError` | result-code based |
  | Monnify | `mapMonnifyResponseCode`, `mapMonnifyError` | small |
  | PawaPay | `mapPawaPayError` | small |
  | Moolre | — | effectively none |
  | Opay / Interswitch | stubs | n/a |

- `failure_events` table + recorder — but that is operational observability (source/severity/error
  type), **not** a payment decline taxonomy. Do not conflate them.

**Does not exist**
- A shared code registry every adapter maps into.
- Any per-class recovery policy.
- Failure-class awareness in `scheduleRetry` or the subscription ladder.
- Issuer / BIN / operator signal in routing (BIN appears only in fraud and blocklist checks,
  `service.go:~3230`).
- Per-merchant rolling success rate as a router input (health score is per-connection, computed in
  5-minute buckets — `internal/usecase/connections/health.go:138-176`).

## Scope

**In scope**
- A canonical decline-code registry as a first-class Go type, with an explicit `unknown`.
- Adapter mappers rewritten to return registry values; raw provider code and message preserved
  alongside, always.
- A coverage metric: share of live failures landing on a real class vs `unknown`, per provider.
- A recovery policy keyed on the canonical class: retry same provider / fail over / wait N /
  request customer action / offer another method / stop.
- `scheduleRetry` and the subscription ladder consult the policy instead of a flat schedule.
- Retry budgets per class, and safety rules that cannot be overridden by policy.

**Out of scope (do NOT touch)**
- In-request failover's stop condition. `Retryable` already works; do not rewrite it, map onto it.
- Router selection logic. Issuer/BIN/operator routing is a later plan; this one changes *whether and
  when* to retry, not *how the router picks*.
- ML or learned policy. Deterministic rules only. Rolling Bayesian estimates are permitted for
  reporting, not for decisions, in this plan.
- Any change to `failure_events`.
- Fraud policy, blocklists, velocity caps.
- `docs/` (deprecated).

## Git workflow

- backend: `origin/dev` → `feat/decline-taxonomy` then `feat/class-aware-recovery` as a second branch.
  The taxonomy is behaviour-neutral and should land and bake on its own before any policy change.

## Steps

### Step 1 partial (25 Jul) — code-derived inventory, still needs the data pass

The registry must be validated against real failures (below), but the *emitted* vocabulary can be
read straight from the adapters, and it already shows the shape of the problem.

**Per-adapter coverage** — distinct codes passed to `NewProviderError`:

| Adapter | Distinct codes |
|---|---|
| paystack | 18 |
| mpesa | 10 |
| stripe | 9 |
| flutterwave | 9 |
| hubtel | 9 |
| monnify | 8 |
| pawapay | 7 |
| **moolre** | **0** |

**Moolre classifies nothing.** It is a GHS mobile-money adapter on the primary target rail, and
every failure it produces is unclassified. That is the single largest hole in the taxonomy and it
is original adapter work, not a mapping exercise — it needs Moolre's documented response codes.

**Emitted vocabulary — 31 free strings, no shared enum.** `provider_error` is the most frequent
code in the codebase (12 call sites), i.e. the catch-all is the biggest bucket.

Confirmed alias collisions, all of which must collapse to one class:

| Duplicated concept | Spellings in use |
|---|---|
| Fraud | `suspected_fraud` (4), `fraud_suspected` (1) |
| Unsupported currency | `unsupported_currency` (2), `currency_unsupported` (1) |
| Timeout | `timeout` (6), `provider_timeout` (1) |
| Card security code | `invalid_cvc` (2), `invalid_security_code` (1) |
| Bad card | `invalid_card` (1), `invalid_card_number` (1) |

**Draft class list**, derived from the union above and grouped by the recovery action each implies.
Validate the shape against the 90-day export before committing to it:

| Class | Emitted codes that map to it | Recovery implication |
|---|---|---|
| `provider_unavailable` | `provider_unavailable`, `system_error` | reroute immediately |
| `provider_timeout` | `timeout`, `provider_timeout` | reroute; outcome may be unknown — do not double-charge |
| `rate_limited` | `rate_limited` | back off, same provider |
| `merchant_config` | `provider_not_configured`, `auth_failed`, `idempotency_error`, `provider_not_implemented` | stop; alert the merchant, no retry will help |
| `invalid_request` | `invalid_amount`, `invalid_email`, `invalid_phone`, `invalid_account`, `unsupported_currency`, `currency_unsupported` | stop; the request itself is wrong |
| `customer_action_required` | `authentication_required`, `invalid_pin` | prompt the customer, do not silently retry |
| `customer_canceled` | `user_canceled` | stop |
| `insufficient_funds` | `insufficient_funds` | delayed retry only, timed to likely funding — never an immediate reroute |
| `issuer_declined` | `card_declined`, `card_expired`, `invalid_card`, `invalid_card_number`, `invalid_cvc`, `invalid_security_code` | hard decline; reroute rarely helps |
| `limit_exceeded` | `limit_exceeded` | delayed retry, possibly a different method |
| `fraud_block` | `suspected_fraud`, `fraud_suspected` | stop; never retry |
| `unknown` | `provider_error`, everything unmapped | measure it; a high share here means the registry is wrong |

Two things this inventory cannot tell us, which is why the data pass in the step below still stands:

1. **Real-world distribution.** Twelve call sites for `provider_error` says nothing about how often
   it actually fires. The unknown-share metric (Step 4) is the number that matters.
2. **What Ghana rails actually emit.** This vocabulary is skewed toward Paystack and Stripe — the
   two card-heavy adapters. A taxonomy validated only against them will under-serve MoMo, which is
   the wedge.

### Step 1 — Draft the registry from real data, not from first principles

Export the last 90 days of raw provider error codes and messages from every environment with data,
plus any anonymized history consenting design partners will share. Cluster them. The registry must be
derived from what Ghana rails actually emit, not from a card-centric taxonomy borrowed from a US
processor.

Target shape (validate against the data before committing to it): provider unavailable, timeout,
rate limited, invalid request / merchant config, customer action required, insufficient funds,
issuer soft decline, issuer hard decline, fraud block, duplicate, and `unknown`.

Every class needs a written definition and at least one real observed example. A class with no
observed instance in the export does not go in.

**Verify**: a coverage estimate — what share of the 90-day export maps to a non-`unknown` class under
the draft. If it is below the target agreed with the operator, the registry is wrong, not the data.

### Step 2 — Land the registry, behaviour-neutral

Introduce the canonical class as a real type (not a string) next to the existing `ErrorCategory`, and
persist it alongside the existing decline fields. Migration numbered **`000146+`** per
`backend/CLAUDE.md`; validate up/down on a fresh DB.

**Do not remove `ErrorCategory` or `Retryable`.** They are load-bearing for in-request failover.
The canonical class is additive.

**Verify**: `go test ./...` + `golangci-lint run ./...` green. No behaviour change — the same payments
retry the same way. Assert that with a regression run over the existing payments suite.

### Step 3 — Migrate the adapters, best-covered first

Paystack, then Stripe, then Flutterwave, Hubtel, M-Pesa, Monnify, PawaPay. Each mapper returns a
canonical class **and** keeps the raw code and message. Resolve the known near-duplicates
(`suspected_fraud`/`fraud_suspected`, `currency_unsupported`/`unsupported_currency`,
`provider_timeout`/`timeout`, `invalid_cvc`/`invalid_security_code`) by mapping both spellings to one
class — do not rename the raw values.

Moolre needs original work: today it emits `moolre_rejected` for nearly everything. That is a gap in
the adapter, not a mapping problem, and it will need the provider's documented response codes.

**Verify**: per-adapter table test mapping every known raw code to its class. Raw code and message
survive on every path — assert that explicitly; losing the raw error is the classic regression here.

### Step 4 — Coverage metric

Expose the share of failures per provider landing on `unknown`, over a window. This is both an
internal quality signal and a genuine product surface — a merchant seeing "12% of Hubtel failures are
unclassified" learns something real.

**Verify**: the metric matches a hand-count on a seeded fixture. Wire it into the existing Prometheus
metrics rather than a new mechanism.

### Step 5 — Policy table, shadow first

Define the per-class recovery policy as data, not as a switch statement, so it can be tuned without a
deploy and audited. For each class: action, delay, max attempts, whether reroute is permitted,
whether a different method may be offered.

Run it in shadow: log the decision the policy *would* make alongside the decision the current flat
schedule actually made. Let it run until you can see, per class, how often they disagree and what the
outcome was in each case.

**Verify**: shadow decisions are recorded for every eligible retry; a disagreement report by class is
produceable. **Do not proceed until the disagreements have been reviewed with the operator** — the
shadow log is where you discover that your "obvious" insufficient-funds delay is wrong for MoMo.

### Step 6 — Enforce, class by class, behind a flag

Switch `scheduleRetry` (`schedule_retry.go:14-41`) and the subscription ladder
(`domain/subscription/retry_policy.go`) to consult the policy. Roll out one class at a time, starting
with the least contentious (provider unavailable / timeout — reroute sooner) and ending with the
most (insufficient funds — wait longer).

Hard safety rules sit **above** the policy and cannot be overridden by it: no retry without the
`ClaimRetry` compare-and-set (`adapters/repo/payment_repo_sqlc.go:474-491`), no failover while an
attempt is pending (`attempt_executor.go:31-32`), Moolre retries remain hard-blocked
(`service.go:1435-1439`), mode isolation preserved, and the global retry budget caps everything
regardless of class.

**Verify**: per-class regression test; the safety rules hold under a policy that (in a test) tries to
violate each one. Duplicate-debit rate stays at zero.

### Step 7 — Prove it helped

Using plan 003's arm and route-receipt data, report recovery rate and incremental lift **by failure
class**. This is the payoff: it turns "we retry smartly" into "on issuer soft declines we recover
X% incrementally, on insufficient funds we recover nothing and stopped wasting your money".

A class where the policy demonstrably does not help is a finding worth publishing, not a failure to
hide.

**Verify**: per-class lift is computable and reconciles with the totals from plan 003.

## Test plan

- Per-adapter mapping tables, including raw-value preservation on every path.
- Policy decision tests per class.
- Safety-rule tests that assert the policy cannot override them.
- Regression: the whole payments and subscriptions suite green at every step, with Step 2 asserted
  behaviour-neutral.
- Gates: `golangci-lint run ./...` 0 issues, `go test ./...` green on the :5433 test DB.

## Done criteria

- [ ] Registry derived from a real 90-day export, every class with a definition and an observed example
- [ ] Canonical class persisted; `ErrorCategory` and `Retryable` untouched and still driving failover
- [ ] All non-stub adapters mapped; raw code and message preserved everywhere
- [ ] Moolre emits real classes, not a single catch-all
- [ ] Unknown-share metric exposed per provider and meeting the agreed target
- [ ] Policy is data, shadow-run, disagreements reviewed with the operator before enforcement
- [ ] `scheduleRetry` and the subscription ladder are class-aware
- [ ] Safety rules provably un-overridable; duplicate-debit rate zero
- [ ] Per-class incremental lift reported
- [ ] README status row updated

## STOP conditions

- Step 1's coverage estimate falls short of the agreed target → the registry is wrong. Return to the
  data. Do not ship a taxonomy that dumps a third of reality into `unknown`.
- Step 2 is not behaviour-neutral → stop. The taxonomy landing must not change a single retry
  decision; if it does, the additive design has been violated somewhere.
- Step 5's shadow log shows the policy disagreeing with current behaviour on a majority of retries →
  do not enforce. Either the policy is wrong or the current behaviour is load-bearing in a way nobody
  documented. Find out which.
- Any duplicate debit, at any step → kill switch, full stop, root cause first.
- You need issuer, BIN or operator data to make a class decision → note it and move on. That is a
  separate plan with its own data-protection review; do not smuggle it in here.
- A raw provider code or message is lost on any path → fix before proceeding. The raw value is the
  only way to debug a bad mapping, and once it stops being written the history is gone.

## Maintenance notes

- The registry will need extension every time a provider is added or changes its codes. Keep it
  additive, keep `unknown` real, and keep the coverage metric visible so drift shows up as a number.
- Once per-class lift exists, the policy becomes tunable against evidence rather than intuition. That
  is the point at which a learned policy could be justified — and not before.
- Reviewer: check Step 2's behaviour-neutrality claim and Step 6's safety rules. Those are the two
  places where this plan can cost a merchant real money.
