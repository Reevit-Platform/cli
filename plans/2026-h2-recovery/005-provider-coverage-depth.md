# Plan 005: Provider coverage depth — statements and payouts beyond Paystack

> **Executor instructions**: Follow this plan step by step. Run every verification and honour the
> STOP conditions. When done, update the status row in `plans/2026-h2-recovery/README.md`.
>
> **Drift check (run first)**: in `backend/`,
> `git diff --stat 26641116..HEAD -- adapters/psp internal/usecase/recon internal/worker/reconstatement cmd/api/main.go cmd/worker/main.go`.

## Status

- **Priority**: P2
- **Effort**: L
- **Risk**: MEDIUM (payout adapters move real money; statement fetchers are read-only)
- **Depends on**: none — runs in parallel with 003
- **Category**: coverage / finance operations
- **Repos**: `backend/`

## Why this matters

The audit concluded Reevit needs more providers. It does not. It needs the providers it already has
to support the features that are already built.

Reconciliation and payouts are both **complete engines wired to exactly one provider**:

- **Statement fetchers: Paystack only.** `cmd/api/main.go:657-662` and `cmd/worker/main.go:584-586`
  register a single `ports.StatementFetcher`, implemented in `adapters/psp/paystack/statements.go`.
  A merchant running Hubtel and Flutterwave alongside Paystack gets a reconciliation run that
  silently covers a third of their money.
- **Payout adapters: Paystack Transfers only.** The payout domain, guarded state machine, beneficiary
  book, bulk disbursement, webhook finalization, balance and name-enquiry are all built and
  provider-agnostic. One adapter exists.

This is the highest ratio of merchant value to engineering effort available in the window: the hard
part — engine, state machine, matcher, dashboard, audit — is done and battle-tested. Each additional
provider is an adapter against a stable internal contract.

It also gates the commercial story. "Automated reconciliation" is not a credible claim to a Ghanaian
merchant whose primary rail is Hubtel or MoMo. And plans 001–003 all produce numbers that a merchant
will want to reconcile against their actual settlements.

## Current state

**Reconciliation engine (complete, Paystack-wired)**
- Domain matcher: `internal/domain/recon/recon.go` — pairs by provider ref; amount, currency and
  status must agree (`:104-169`)
- Mismatch kinds (`:20-34`): `amount_mismatch`, `status_mismatch`, `missing_local`, `missing_remote`
- Service: `internal/usecase/recon/service.go`
- Nightly sweep at 02:30 with per-connection graceful degradation:
  `internal/worker/reconstatement/`
- Idempotent re-runs (UNIQUE run per connection+day; resolved mismatches survive)
- API: runs, mismatches, resolve-with-audited-note, manual run-now
- Optional `reconciliation.mismatch` outbound webhook
- Dashboard: `StatementReconPanel` on the Operations page
- Settlement CSV export grouped by settlement date + currency, minor units
- Migration `000129`

**Payouts (complete, Paystack-wired)**
- Migrations `000126` (payouts), `000127` (beneficiaries)
- Guarded state machine, CAS transitions, audit row per transition
- `POST/GET /v1/payouts`, `/{id}`, `/confirm`, `/cancel`, `/bulk` (100 items, per-item isolation)
- Beneficiary book, destination-deduped per org+mode, snapshot-on-payout
- Reconcile worker (`worker/payoutreconcile`) as backstop; Paystack `transfer.*` webhooks finalize
  in near-real-time
- `GET /v1/payouts/balance`, `POST /v1/payouts/resolve-account`
- Scopes `payouts:read` / `payouts:write`; idempotency keys required

**Missing**
- Statement fetchers for Hubtel, Flutterwave, and any other provider a target merchant runs
- Payout adapters for Flutterwave and PawaPay (both named in the roadmap as launch targets)
- Payout SDK coverage and Mintlify docs (roadmap B1 lists these as remaining)

## Scope

**In scope**
- Statement fetchers for Hubtel and Flutterwave against the existing `ports.StatementFetcher`
  contract, registered in both `cmd/api` and `cmd/worker`.
- Payout adapters for Flutterwave and PawaPay against the existing capability contract.
- Per-provider reconciliation coverage reporting — which connections were covered by last night's run
  and which were skipped for want of a fetcher.
- Payout SDK surface (TypeScript, Python, Go, PHP) and Mintlify docs, per the repo's standing rule
  that a new API resource ships with scopes, SDKs, docs, exports, SSE and metrics together.

**Out of scope (do NOT touch)**
- New PSP *collection* adapters. Eight is enough for this window.
- Interswitch and Opay stubs — parked pending demand, explicitly.
- The recon matcher, mismatch taxonomy, worker schedule, or dashboard. Adapters only.
- The payout state machine or its guards.
- M-Pesa / Monnify / Moolre / Stripe statement fetchers unless a target account requires one — add
  on demand, not speculatively.
- `docs/` (deprecated).

## Git workflow

- backend: one branch per adapter — `feat/hubtel-statements`, `feat/flutterwave-statements`,
  `feat/flutterwave-payouts`, `feat/pawapay-payouts`. Independent, reviewable, individually
  revertable. Do not stack them.

## Steps

### Step 1 partial (25 Jul) — the contract, read from code

**Statement fetchers registered: Paystack only.** Confirmed at `cmd/api/main.go:657-662` and
`cmd/worker/main.go:584-586`. Both registrations exist, which is the trap: wiring a new fetcher
into only one of them yields a fetcher that works on manual run-now and never runs nightly.

**The matcher pairs on the provider reference** (`internal/domain/recon/recon.go:104-169`), then
requires amount, currency and status to agree. Mismatch kinds are `amount_mismatch`,
`status_mismatch`, `missing_local`, `missing_remote` (`:20-34`).

**The reference field is where this breaks silently.** If a provider's statement reference is not
the value stored on the payment, every row lands as `missing_local` **and** `missing_remote` — a
run that looks catastrophic while being merely misconfigured. Establish the mapping per provider
before writing the fetcher, and never loosen the matcher to compensate: a loose matcher produces
false matches, which is worse than an unmatched row.

**Payout adapters: Paystack only.** The domain, guarded state machine, beneficiary book, bulk
disbursement, webhook finalization, balance and name-enquiry are all provider-agnostic and done.

**Blocked on credentials.** Steps 2, 3, 5 and 6 require Hubtel, Flutterwave and PawaPay sandbox
access, and this plan's STOP condition is explicit: "do not ship on unit tests alone — every payout
path in this repo has been live-verified; that bar holds." Nothing further can be done here without
sandbox credentials.

### Step 1 — Read the contract before reading any provider docs

Study `adapters/psp/paystack/statements.go` against `ports.StatementFetcher`, and the Paystack
Transfers adapter against the payout capability. Write down the contract's expectations: pagination,
time-window semantics, currency and minor-unit handling, the provider-reference field the matcher
pairs on (`internal/domain/recon/recon.go:104-169`), and how partial/failed fetches degrade.

The reference field is the one that silently breaks reconciliation: if a provider's statement
reference is not the same value stored on the payment, every row lands as `missing_local` +
`missing_remote` and the run looks catastrophic while being merely misconfigured.

**Verify**: a written contract note covering pagination, windowing, minor units, the reference field
and the degradation path. Get it reviewed before implementing.

### Step 2 — Hubtel statement fetcher

Implement, register in **both** `cmd/api/main.go` and `cmd/worker/main.go` (the existing Paystack
registration appears in both — missing one gives a fetcher that works on manual run-now and never
runs nightly, which is a confusing bug to chase).

**Verify**: against a real Hubtel sandbox, a seeded day reconciles with the expected classification.
Construct at least one deliberate mismatch of each kind and confirm the matcher classifies it
correctly. Re-run the same day and confirm idempotency — no duplicate rows, resolved mismatches
survive.

### Step 3 — Flutterwave statement fetcher

As Step 2.

**Verify**: same bar. Additionally, run a reconciliation over an org with **three** connections
(Paystack, Hubtel, Flutterwave) and confirm per-connection graceful degradation still holds when one
provider's fetch fails — the run must be marked partial with the failing connection named, not fail
wholesale.

### Step 4 — Coverage reporting

Surface, per reconciliation run, which connections were covered and which were skipped for want of a
fetcher. A merchant must be able to tell at a glance that "reconciled" means all their money, not
some of it.

**Verify**: an org with a Moolre connection (no fetcher) shows that connection as explicitly
uncovered, not silently absent.

### Step 5 — Flutterwave payouts

Implement the transfer capability. Follow the Paystack adapter's discipline exactly, in particular
**unknown-outcome-stays-pending**: an initiation whose result is ambiguous must leave the payout
pending for the reconcile worker to finalize, never fail out. That rule is why the payout engine has
not double-paid anyone.

Wire provider webhooks for near-real-time finalization if Flutterwave supports them, using the same
lookup-then-verify model (reference locates the payout → connection secret verifies HMAC before any
state change).

**Verify**: against a real Flutterwave sandbox — a payout reaches `succeeded` with the provider ref
stored; the same idempotency key returns the original payout and does not disburse twice; a forged
webhook signature is rejected; a replayed webhook is a no-op; an ambiguous initiation stays pending
and is finalized by the reconcile worker.

### Step 6 — PawaPay payouts

As Step 5. PawaPay's existing `CheckRefundStatus` polling pattern is the template for async status.

**Verify**: same bar as Step 5.

### Step 7 — SDKs and docs

Payout endpoints in the TypeScript, Python, Go and PHP SDKs, plus a Mintlify payouts page covering
create, list, get, confirm, cancel, bulk, balance, resolve-account, beneficiaries, the scopes, the
idempotency requirement, and the async status model.

**Verify**: each SDK's payout surface exercised against sandbox in CI (install + smoke). Docs page
builds and every documented field matches the OpenAPI spec.

## Test plan

- Per-adapter unit tests with recorded provider fixtures.
- Matcher integration: for each new fetcher, a seeded day producing every mismatch kind.
- Idempotency: re-run a reconciled day; re-submit a payout idempotency key.
- Degradation: multi-connection run with one provider failing.
- Payout safety: ambiguous initiation, forged signature, replayed webhook, insufficient balance.
- Gates: `golangci-lint run ./...` 0 issues, `go test ./...` green.

## Done criteria

- [ ] Hubtel and Flutterwave statement fetchers registered in both `cmd/api` and `cmd/worker`
- [ ] Each verified against a real sandbox with every mismatch kind classified correctly
- [ ] Idempotent re-runs confirmed per provider
- [ ] Multi-connection partial degradation still correct
- [ ] Uncovered connections reported explicitly
- [ ] Flutterwave and PawaPay payout adapters live-verified, including ambiguous-initiation handling
- [ ] No double disbursement under repeated idempotency keys
- [ ] Payout SDK coverage in TS/Python/Go/PHP with CI smoke tests
- [ ] Mintlify payouts page shipped and matching the OpenAPI spec
- [ ] README status row updated

## STOP conditions

- A provider's statement reference cannot be matched to the value stored on the payment → stop and
  design the mapping deliberately. Do not "fix" it by loosening the matcher; a loose matcher produces
  false matches, which is far worse than an unmatched row.
- A provider's statement API cannot express the nightly window (no date filtering, or a window that
  does not align to settlement dates) → report before implementing. A fetcher that quietly returns
  the wrong day's data makes reconciliation actively harmful.
- Any payout adapter cannot guarantee unknown-outcome-stays-pending → do not ship it. A payout
  adapter that can fail out on an ambiguous initiation will eventually double-pay.
- Sandbox access for a provider is unavailable → do not ship on unit tests alone. Every payout path
  in this repo has been live-verified; that bar holds.
- You find yourself modifying `internal/domain/recon/recon.go` or the payout state machine → stop.
  Adapters only. If the engine genuinely needs a change, that is a separate plan with a separate
  review.

## Maintenance notes

- Order the work by target-account demand, not by API pleasantness. If pipeline says Hubtel
  reconciliation unblocks a deal and Flutterwave payouts do not, ship Hubtel first regardless of
  which adapter is easier.
- Every new collection provider added later inherits a debt of two adapters — statements and payouts.
  Factor that into any future connector decision; the connector is not "done" when charges work.
- Reviewer: the two highest-risk checks are the reference-field mapping in Step 1 and the
  ambiguous-initiation behaviour in Steps 5–6.
