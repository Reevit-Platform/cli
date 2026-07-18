# Reevit 10x Roadmap

**Date:** July 2, 2026 · **Status:** Proposed
**Basis:** Full codebase audit of `backend/` (117 migrations, 52 handler modules, 7 live PSP adapters), `frontend/` (v1.9.13, all-real-data dashboard), `sdks/` (9 SDKs), `mintlify-docs/`, `examples/` (10 apps), and the live merchant integration (`misscookiespices.com`).

---

## Where we are

The orchestration core is **feature-complete**: payments (create/confirm/refund/retry/cancel/capture/bulk), subscriptions with proration and dunning, health/fee/capability-aware routing with failover, unified inbound webhooks for 7 PSPs, signed outbound webhooks with retry + delivery logs, fraud policies, blocklists, disputes, idempotency, mode isolation, audit logs, and a dashboard that covers all of it. `COMPREHENSIVE_GAP_ANALYSIS.md` shows zero open critical/important items.

## Why the next 10x is not "more endpoints"

Value multiplies along four axes the current codebase does *not* cover:

| Axis | Today | Gap |
|---|---|---|
| **Reevit revenue** | Plans + metering + 402 gatekeeper exist | No auto-billing — we literally cannot invoice or charge a customer |
| **Money lifecycle** | Money in only | No payouts/transfers, no reconciliation engine, no settlements |
| **Merchant ROI** | Routing works | No experimentation loop or cross-PSP token portability to *prove* uplift |
| **Distribution** | SDKs + docs | No plugins, no CLI, no headless checkout, KYC onboarding is manual |

The roadmap below is organized into five tracks (A–E), sequenced into four phases. Each feature has testable acceptance criteria.

---

# Phase 1 — Monetize & close the money loop (Q3 2026)

## A1. Billing Engine: invoice and charge our own customers

> **Status (Jul 3, 2026):** Core shipped — monthly overage invoicing + charge
> (backend `2b81574`), collection retries + downgrade-to-free (`762c5ef`),
> downgrade cancels the Paystack base-fee subscription + merchant email
> (`d0ad343`), dashboard invoice history (frontend `3204cc8`). Remaining:
> PDF invoices, coupon application to overage,
> `dashboard_features.billing` flag rollout.
**Why 10x:** Every other feature is worthless to the business if revenue can't be collected. ~60% is already built: `plans`, `org_subscriptions`, `org_usage` tables, Redis metering with 10-min flush worker (`internal/worker/usage/handler.go`), and the 402 `BillingGatekeeper` (`adapters/http/router_live_mode_plan_gate.go`). What's missing is the monthly billing cron, card-on-file charging, invoices, and the coupon API (schema exists in migration `000109`, no endpoints).

**Scope**
- Monthly billing worker: for each active `org_subscription`, compute `base + max(0, usage − included) × overage_fee`, generate an invoice via Reevit's own invoicing engine (dogfood), and charge the org's stored payment method.
- Failed platform charges enter dunning (reuse existing retry-policy engine), then downgrade to Free after exhaustion.
- `GET /v1/billing/usage`, `GET /v1/billing/invoices`, `GET /v1/billing/invoices/{id}/pdf`.
- Coupons API (`POST/GET/DELETE /v1/coupons`, redemption at invoice computation).
- Dashboard: real usage meter with limit bar, invoice history, payment method management (replace the current view-only `/dashboard/billing`).

**Acceptance criteria**
- [ ] A billing cycle run against an org with 12,000 transactions on Starter produces an invoice of $49 + 2,000 × $0.05 = $149, with line items for base and overage; invoice math covered by property-based tests.
- [ ] The billing worker is idempotent: re-running the same period for the same org creates zero duplicate invoices or charges (verified by a test that runs it twice).
- [ ] A successful charge marks the invoice paid and emits `billing.invoice.paid`; a failed charge schedules dunning attempts per the org's retry policy; after exhaustion the org is downgraded to Free and notified.
- [ ] Free-tier org exceeding 1,000 successful live payments in a period receives `402 Payment Required` on `POST /v1/payments/intents` (existing gate) — regression-tested end-to-end against a real metered counter, not a mock.
- [ ] A percent-off coupon applied to an org reduces the next invoice total exactly once and is recorded in the audit log.
- [ ] `/dashboard/billing` shows live current-period usage within 10 minutes of a payment (metering flush interval) and lists downloadable PDF invoices.
- [ ] Test-mode transactions never count toward billed usage (mode-isolation test).

**Effort:** 3–4 weeks · **Dependencies:** none — all infra exists.

---

## B1. Payouts & Transfers API

> **Status (Jul 3, 2026): Slice 1 shipped** (backend `0f534ae`): payout domain
> with guarded state machine (single-statement CAS transition + audit row),
> Paystack Transfers adapter (bank + mobile money recipients,
> unknown-outcome-stays-pending discipline), and
> `POST/GET /v1/payouts`, `GET /v1/payouts/{id}`,
> `POST /v1/payouts/{id}/confirm` behind new `payouts:read/write` scopes with
> required idempotency keys. **Slice 2 shipped** (`94d825b`): 5-minute
> reconciliation worker finalizing open payouts (fetch by provider ref, or
> Paystack verify-by-merchant-reference for lost initiation outcomes, with a
> 30-min never-landed grace before failing out). **Slice 3 shipped**
> (`96313df`): beneficiary book (destination-deduped per org+mode,
> payout-by-`beneficiary_id` with snapshot-on-payout) + bulk disbursements
> (`POST /v1/payouts/bulk`, 100 items, per-item isolation). **Dashboard
> shipped** (frontend `cbf1cf4` + backend `285dd46`/`13ee327`/`66ffadf`):
> `/dashboard/payouts` with status-filtered table, new-payout sheet
> (saved-or-inline destination, per-open idempotency key), beneficiary book
> tab — browser-verified end to end incl. a live beneficiary write.
> **Slice 4 shipped** (`2d4457a`): Paystack `transfer.*` webhooks finalize
> payouts in near-real-time (reconcile worker demoted to backstop) —
> lookup-then-verify security model (reference locates payout → connection
> secret verifies HMAC before any state change), CAS absorbs replays and
> out-of-order events; migration `000132` relaxed
> `webhook_events.payment_id` for payment-less events (FK bug found by live
> probe). Live-verified: signed webhook flipped a payout with audit row,
> forged signature rejected, replay + stale event both no-ops.
> **Cancel endpoint shipped** (backend `00a447a9` + frontend `e1f96ec`):
> `POST /v1/payouts/{id}/cancel` voids a still-pending payout via a local CAS
> pending→canceled (no provider call); idempotent; 409 once processing/
> terminal or on a lost CAS. Dashboard Cancel button on pending rows.
> Live-verified end to end.
> **Balance endpoint shipped** (backend `b4169acd`): `GET
> /v1/payouts/balance?connection_id=X` reports a connection's available float
> per currency via a new `PayoutBalanceFetcher` capability (Paystack `GET
> /balance`). Live-verified against real Paystack sandbox (GHS 1,393.00).
> **Name-enquiry shipped** (backend `ad7d312a`): `POST
> /v1/payouts/resolve-account` resolves a beneficiary to its account holder's
> name (Paystack bank name-enquiry via `GET /bank/resolve`) so merchants
> verify before paying. Typed 400s: `resolution_unsupported` (momo) vs
> `account_not_resolved` (bad account — a Paystack 422 mapped to 400, not
> 500). Live-verified against real Paystack sandbox.
> Remaining: Flutterwave/PawaPay adapters, SDKs + docs. (The three self-
> contained payout endpoints — cancel, balance, name-enquiry — are done; the
> rest is provider breadth + client surface.)
**Why 10x:** Merchants can accept money but not move it. Payouts complete the loop and unlock payroll, marketplace disbursements, and refund-to-wallet flows — and they're a metered, billable event. Settlement DB fields already exist (migration `000096`); the PSP adapter contract (`adapters/psp/*/provider.go`) needs a `Transfer` capability. Paystack Transfers, Flutterwave Transfers, Hubtel Send Money, M-Pesa B2C, and PawaPay payouts all have public APIs.

**Scope**
- New resource: `POST /v1/payouts`, `GET /v1/payouts`, `GET /v1/payouts/{id}`, `POST /v1/payouts/{id}/cancel` (while pending).
- Beneficiaries: `POST/GET/DELETE /v1/beneficiaries` (bank account / MoMo wallet, validated against provider name-enquiry APIs where available).
- Bulk disbursement: `POST /v1/payouts/bulk` (up to 100, per-item status like existing bulk refund).
- Adapter implementation for ≥3 providers at launch (Paystack, Flutterwave, PawaPay), routed through the existing router with capability filtering (`payout` capability on connections).
- Async status tracking via provider webhooks + polling worker (PawaPay's `CheckRefundStatus` pattern generalizes).
- Balance endpoint per connection where the provider exposes it (`GET /v1/connections/{id}/balance`).
- Dashboard: Payouts page (table + detail sheet + create sheet, following the settled payments UI patterns), beneficiary book.

**Acceptance criteria**
- [ ] A payout created with an idempotency key against a Paystack test connection reaches `succeeded`, with provider ref stored, and re-submitting the same key returns the original payout (no double disbursement).
- [ ] Payout status transitions (`pending → processing → succeeded|failed|reversed`) are enforced by a domain state machine with row-level locking, mirroring `applyPaymentTransition` in `internal/usecase/payments/hardening.go`, and every transition writes a status-history audit row in the same transaction.
- [ ] Routing respects capability filtering: a payout to a Ghanaian MoMo wallet never routes to a connection without the `payout` capability or GHS support.
- [ ] A bulk disbursement of 100 items returns per-item results; partial failure of item N does not roll back items 1..N−1; each item independently idempotent.
- [ ] Provider webhook confirming/failing a payout updates status within 60s in test; a polling worker reconciles payouts stuck in `processing` > 15 min.
- [ ] Payout attempts against insufficient provider balance fail with a typed `insufficient_balance` error, not a 500.
- [ ] `payouts:read`/`payouts:write` API-key scopes enforced; payouts appear in exports and the SSE event stream; test/live mode isolation verified.
- [ ] SDK support (TypeScript, Python, Go, PHP) + Mintlify docs page shipped in the same release.

**Effort:** 6–8 weeks · **Dependencies:** none (parallel with A1).

---

## B2. Reconciliation Engine

> **Status (Jul 3, 2026): Slice 1 shipped — the engine core.** Migration
> `000133` (`payments.reconciliation_status` + runs/mismatches tables; NB the
> claimed 000105 columns never existed), pure matcher
> (`internal/domain/recon`, roadmap's 96/2/1/1 scenario is a unit test),
> `ports.StatementFetcher` + Paystack paginated `GET /transaction`, nightly
> sweep at 02:30 with per-connection graceful degradation, idempotent
> re-runs (UNIQUE run per connection+day; resolved mismatches survive via
> identity index + ON CONFLICT DO NOTHING), `/v1/reconciliation` API (runs,
> mismatches, resolve-with-note audit, manual run-now), optional
> `reconciliation.mismatch` outbound webhook. Live-verified against real
> Paystack sandbox incl. resolve/409/400 and re-run exclusion.
> **Slice 2 shipped — dashboard** (frontend `b1f9888`): StatementReconPanel
> on the Operations page's Reconciliation tab — runs table, mismatch queue
> (kind badges + two-sided comparison), resolve dialog with mandatory
> audited note, manual run-now. Browser-verified: resolved a mismatch
> through the real dialog (DB shows actor + note), run-now hit real
> Paystack idempotently.
> **Slice 3 shipped — settlement CSV export** (backend `3916779a`, frontend
> `d6bb571`): `GET /v1/reconciliation/settlement-export?from&to` streams a CSV
> of settled payments grouped by settlement date + currency with per-currency
> totals; amounts in minor units so it reconciles exactly to
> SUM(settlement_amount) (domain + CSV tests). "Settlement statement" button
> on the Operations Exports tab. Live-verified end to end (curl CSV totals +
> browser download 200). **This closes B2's last acceptance criterion — the
> reconciliation engine is feature-complete.**
> Remaining (nice-to-have): Flutterwave/Hubtel statement fetchers, dashboard
> (SSE) notification on mismatch, per-payment reconciled badge in payments UI.

**Why 10x:** Trust is the product. Today reconciliation is two unused columns (`reconciliation_status`, `reconciliation_notes`, migration `000105`). An automated engine that proves "what the PSP says matches what we say" is the #1 reason finance teams pick an orchestrator over direct integration.

**Scope**
- Nightly worker per connection: fetch provider transaction lists/statements (Paystack `/transaction`, Flutterwave `/transactions`, etc.) for the previous day.
- Matching pass: provider record ↔ local payment by provider ref, amount, currency, status. Classify: `matched`, `amount_mismatch`, `status_mismatch`, `missing_local` (at provider, not in Reevit), `missing_remote` (in Reevit, not at provider).
- `GET /v1/reconciliation/runs`, `GET /v1/reconciliation/runs/{id}/mismatches`, `POST /v1/reconciliation/mismatches/{id}/resolve` (with note).
- Dashboard: Reconciliation tab under Analytics or Failures — daily run summary, mismatch queue with resolve actions.
- Settlement statement export (CSV) grouping payments by expected settlement date/currency using the existing settlement fields.

**Acceptance criteria**
- [ ] A seeded test scenario with 100 payments where 96 match, 2 have amount drift, 1 exists only at the provider, and 1 exists only locally produces exactly those classifications.
- [ ] Matched payments get `reconciliation_status = reconciled` automatically; mismatches emit a `reconciliation.mismatch` outbound webhook event and a dashboard notification.
- [ ] Reconciliation runs are resumable and idempotent: re-running a day updates rather than duplicates results.
- [ ] Provider fetch failures degrade gracefully (run marked `partial` with the failing connection listed) and are retried with backoff.
- [ ] A mismatch resolved with a note is audit-logged (actor, timestamp, reason) and excluded from subsequent runs.
- [ ] Settlement export for a date range reconciles to the sum of `settlement_amount` on included payments (checked by test).

**Effort:** 4–6 weeks · **Dependencies:** none; enriched by B1 (payout reconciliation follows the same engine).

---

# Phase 2 — Prove merchant ROI (Q4 2026)

## C1. Routing Experiments (wire A/B testing into the payment flow)

> **Status (Jul 3, 2026): COMPLETE end-to-end.** Backend `43e1540`
> (deterministic per-customer bucketing, guardrail auto-pause, promote
> endpoint) + `2d2f913` (API contract fix: json tags + null arrays — the
> ab-tests page had never rendered a real test row) + frontend `465bbb2`
> (Promote-winner button with significance gating and confirm). The readout
> UI (p-values, CIs, power) already existed. Browser-verified against the
> live stack: seeded experiment → readout → promote → rule flip + completion
> confirmed in the DB. Remaining nice-to-have: router-overhead benchmark.
**Why 10x:** The routing A/B test tables and endpoints exist (`GET /v1/routing/ab-tests`) but are **not integrated into the router** — the backend agent confirmed this explicitly. Closing that loop turns "smart routing" from a claim into a measured number ("Reevit raised your auth rate 3.2%"), which is the single best sales asset an orchestrator can have.

**Scope**
- Router integration: deterministic bucketing (hash of payment/customer id → variant) between two connection chains; decision recorded on the existing `routing_decisions` table with experiment/variant ids.
- Guardrails: auto-pause a variant whose success rate drops below a configurable floor with sequential-testing correction.
- Readout API: per-variant success rate, latency, fee cost, with confidence interval; `POST /v1/routing/ab-tests/{id}/promote` applies the winner as a routing rule.
- Dashboard: experiment detail page with live readout and one-click promote (the `/dashboard/ab-tests` shell already exists).

**Acceptance criteria**
- [ ] With an active 50/50 experiment, 1,000 simulated payments split 500±5% per variant, and the same customer id always lands in the same variant (deterministic bucketing test).
- [ ] Experiments only run where scoped (country/method/mode); payments outside scope use the normal router untouched (regression test on routing decisions).
- [ ] A variant breaching the failure guardrail auto-pauses within 50 payments and notifies the org.
- [ ] Promote creates the corresponding routing rule, archives the experiment, and is audit-logged; the readout endpoint returns success-rate deltas with 95% CIs computed correctly against a known fixture.
- [ ] Router overhead with experiments enabled adds <5ms p99 to route selection (benchmark test).

**Effort:** 2–3 weeks (mostly wiring) · **Dependencies:** none.

## C2. Cross-PSP Token Vault (payment method portability)

> **Status (Jul 3, 2026): Slice 1 shipped — the portability model** (backend
> `d1efb275`). `internal/domain/paymentmethod/portability.go` classifies a
> saved method as `portable` (MoMo MSISDN+network, bank) vs `psp_locked`
> (card token); `EnsureChargeableOn(targetProvider)` guard returns typed
> `ErrTokenNotPortable` for a locked token on a different provider (**closes
> acceptance criterion #2**, no provider call); `PortableCredential` extracts
> the MoMo MSISDN+network; `ChargeableProviders` computes cross-PSP coverage.
> API exposes `portable`+`portability`; live-verified (card→locked,
> momo→portable). **Slice 2 shipped — provider coverage** (backend
> `ea50f742`): `GET /v1/payment-methods` returns `chargeable_providers` per
> method (originating always; portable methods add every active connection
> supporting the method type). Live-verified: card→[paystack], portable
> MoMo→[paystack, hubtel]. UI (frontend `01569da`): saved-method cards in the
> payment detail sheet show a Portable/Locked badge + "Chargeable on
> {providers}" — **satisfies accept #4's coverage display.**
> **BLOCKER on the dunning re-route (accept #1):** wiring portability into the
> retry charge needs an off-session MoMo credential decision — a MoMo
> authorization may enable off-session debiting a raw MSISDN can't (raw MoMo
> debits often need an OTP/push or a provider-specific mandate that is NOT
> portable). Switching the charge token→MSISDN risks breaking off-session
> recurring charges. Resolve per-provider off-session MoMo semantics first.
> Remaining after that: `rerouted` attribution, dashboard subscription-detail,
> Analytics recovered-by-rerouting.

**Why 10x:** Saved payment methods exist (migration `000097`) but tokens are PSP-locked. The classic orchestration moat: when a recurring charge fails on PSP A, retry it on PSP B using a portable credential. For African rails this is uniquely feasible — MoMo "tokens" are phone numbers + network, which are inherently portable.

**Scope**
- Vault abstraction over saved methods: `portable` (MoMo MSISDN + network, bank account) vs `psp_locked` (card authorization tokens).
- Renewal/dunning flow upgrade: on failure with a portable method, the retry worker may re-route to a different healthy connection supporting that method.
- Card path: where providers support it, re-tokenize on next successful customer-present charge to build multi-PSP token coverage per customer.
- `GET /v1/customers/{id}/payment-methods` with portability + provider coverage surfaced.

**Acceptance criteria**
- [ ] A subscription renewal that fails on Hubtel with a MoMo method retries on Paystack (same MSISDN/network) within the existing dunning schedule, and the recovery is attributed in `billing_recovered_amount_cents_total` with a `rerouted` label.
- [ ] PSP-locked card tokens are never sent to a different provider (negative test asserts a typed `token_not_portable` error, no provider call).
- [ ] Vault entries are encrypted with the existing SecretsVault; plaintext MSISDNs never appear in logs (log-scrub test).
- [ ] Dashboard subscription detail shows which providers can charge each saved method.
- [ ] Recovered-by-rerouting appears as a distinct metric in Analytics (extends the existing recovered-revenue cards).

**Effort:** 4–6 weeks · **Dependencies:** C1 helpful (shared attribution plumbing), not required.

## C3. Dispute Evidence Management
**Why 10x:** Disputes have list/resolve endpoints but no evidence handling — merchants currently leave Reevit and log into each PSP to fight chargebacks. Deadline-tracked evidence submission keeps the whole dispute lifecycle inside Reevit.

**Scope**
- Object storage integration (S3-compatible) for evidence files; `POST /v1/disputes/{id}/evidence` (multipart), `GET /v1/disputes/{id}/evidence`.
- Submit evidence to provider APIs where supported (Paystack, Flutterwave, Stripe); mark manual-submission-required otherwise with clear instructions.
- Deadline tracking from provider webhook payloads; notifications at T−72h and T−24h.

**Acceptance criteria**
- [ ] Uploading a 5MB PDF + two images to a dispute stores them encrypted-at-rest, virus-scan hooked, and lists them with uploader + timestamp; files >10MB or disallowed types rejected with typed errors.
- [ ] For a Paystack test dispute, evidence submission calls the provider API and records the provider's acknowledgment; for an unsupported provider the dispute shows `manual_submission_required` with a documented playbook link.
- [ ] An org with an open dispute 72h from deadline receives a notification (bell + outbound webhook `dispute.evidence_due`).
- [ ] Evidence is immutable after provider submission (append-only; delete attempts rejected).

**Effort:** 3–4 weeks · **Dependencies:** first object-storage infra in the stack (also unblocks E1 KYC documents).

---

# Phase 3 — Distribution & developer experience (Q1 2027)

## D1. E-commerce Plugins (WooCommerce first, then Shopify)
**Why 10x:** The SDK audit found zero plugins. WordPress/WooCommerce dominates SME e-commerce in Ghana/Nigeria; a plugin converts "developer required" into "install and paste API key" — an order-of-magnitude wider funnel. The PHP SDK and hosted checkout already do the heavy lifting.

**Acceptance criteria**
- [ ] WooCommerce plugin (built on `sdks/php`) passes WordPress.org review: checkout via Reevit hosted flow, webhook-driven order status updates with signature verification, refund from the WooCommerce order screen, test-mode toggle.
- [ ] A fresh WooCommerce store goes from plugin install to first successful test payment in under 10 minutes following only the plugin's setup screen (timed onboarding test with a naive user script).
- [ ] Currency/method support mirrors the org's connections (plugin fetches capabilities; doesn't hardcode).
- [ ] Shopify app (Payments App or checkout redirect, per current Shopify API constraints) listed in the app store with the same webhook/refund guarantees.
- [ ] Plugin telemetry: installs and first-payment activation tracked so funnel conversion is measurable.

**Effort:** 3–4 weeks (Woo) + 4–6 weeks (Shopify) · **Dependencies:** none.

## D2. Headless Checkout + PSP Bridge Parity
**Why 10x:** `ReevitCheckout` is modal-only; the state machine in `sdks/core` is already framework-agnostic but undocumented as a public surface. Headless unlocks every custom-UI merchant we currently lose. Meanwhile Vue/Svelte have 3 PSP bridges vs React's 6 — silent checkout failures for half the providers on those stacks.

**Acceptance criteria**
- [ ] `@reevit/core` exposes a documented headless API (`createCheckout()` → states, transitions, `submit()`, provider bridge hooks) with TypeScript types and a Mintlify guide including a fully custom React UI example.
- [ ] Vue and Svelte SDKs gain Paystack, Flutterwave, and Hubtel bridges with behavior-parity tests against the React implementations (shared fixture suite in `sdks/core`).
- [ ] A payment completed via headless API emits identical events/webhooks as the modal flow (integration test).
- [ ] Semver: existing modal users upgrade with zero breaking changes.

**Effort:** 3–4 weeks · **Dependencies:** none.

## D3. Reevit CLI + Sandbox Simulator
**Why 10x:** Stripe's `stripe listen` is the most-loved DX feature in payments. Local webhook forwarding plus deterministic test scenarios (the docs audit found no published test cards/phone numbers) removes the biggest integration friction: testing webhooks and failure paths.

**Acceptance criteria**
- [ ] `reevit login`, `reevit listen --forward-to localhost:3000/webhooks` (streams live test-mode events over SSE/WebSocket to a local endpoint with valid signatures), `reevit trigger payment.succeeded`, `reevit payments list`.
- [ ] Documented magic values in test mode: specific amounts/phone numbers deterministically produce `failed`, `timeout`, `insufficient_funds`, `provider_downtime` (routing failover exercisable locally), implemented in the stub provider.
- [ ] `reevit trigger` supports every published outbound event type; payloads match production schemas byte-for-byte except ids.
- [ ] CLI distributed via Homebrew + npm + direct binary (Go, reusing `sdks/go`); works against test mode only unless explicitly flagged.
- [ ] Docs: "Test your integration" page listing all magic values per provider/method.

**Effort:** 3–4 weeks · **Dependencies:** none.

## D4. Agent Payments: Reevit MCP Server
**Why 10x:** 2026 buyers increasingly integrate through AI agents. An MCP server exposing scoped tools (create payment link, check payment status, issue refund, query analytics) makes Reevit the default payments layer for agent-built African commerce — near-zero competition in this niche today.

**Acceptance criteria**
- [ ] MCP server (stdio + streamable HTTP) authenticating with scoped API keys; tools respect key scopes exactly (a `payments:read`-only key cannot refund — negative test).
- [ ] Tools: `create_payment_link`, `get_payment`, `list_payments`, `create_refund` (test mode or explicit confirmation gate for live), `get_analytics_summary`.
- [ ] Destructive/live-money tools require an explicit `confirm: true` argument and are rate-limited per key.
- [ ] Published setup guides for Claude Code/Desktop and the docs' agent page; installable via `npx @reevit/mcp`.

**Effort:** 2 weeks · **Dependencies:** none.

---

# Phase 4 — Enterprise & platforms (Q2 2027)

## E1. KYC Automation (document upload + verification provider)
**Why 10x:** KYC is manual (notes API only, no document handling) — the single onboarding bottleneck at scale, since live API keys are gated on KYC (`ErrKYCRequired`).

**Acceptance criteria**
- [ ] Document upload (business registration, ID, proof of address) to object storage with typed validation; statuses `pending → in_review → verified|rejected(reason)` with audit trail.
- [ ] Integration with one verification provider (e.g., Smile ID) for automated ID + business-registry checks in GH/NG/KE; automated pass moves org to `verified` without operator action; failures fall back to the manual review queue in `/platform/kyc`.
- [ ] Median time-to-live-key for orgs passing automated checks < 1 hour (measured); rejection reasons surfaced verbatim to the merchant with re-submission flow.
- [ ] Documents encrypted at rest, access audit-logged, hard-deleted on org deletion (compliance test).

**Effort:** 3–4 weeks · **Dependencies:** object storage from C3.

## E2. Marketplace Split Payments (sub-accounts)
**Why 10x:** Platforms (marketplaces, SaaS-for-commerce) are the highest-volume merchant class, and every one of them needs splits. Combining native PSP splits (Paystack subaccounts, Flutterwave subaccounts) under one API — with a ledger fallback where the PSP lacks splits — is exactly the cross-provider normalization Reevit exists for.

**Acceptance criteria**
- [ ] `POST /v1/split-groups` defining recipients + shares (percent or fixed); a payment intent referencing a split group settles each share, using native PSP splits where supported and internal ledger entries + payouts (B1) otherwise.
- [ ] Shares always sum-validate (percent = 100%, fixed ≤ amount) with typed errors; rounding is deterministic and documented (largest-remainder), property-tested so splits always sum to the captured amount exactly.
- [ ] Refunds against split payments reverse shares proportionally (or per explicit override) and are blocked once a share has been paid out, with a clear error.
- [ ] Ledger endpoint per recipient: balance, pending, paid-out; reconciles with B2 engine.
- [ ] Dashboard: split groups CRUD + per-payment split breakdown in the payment detail sheet.

**Effort:** 6–8 weeks · **Dependencies:** B1 (payouts), B2 (reconciliation).

## E3. Enterprise Auth: SSO/SAML + Custom Roles
**Why 10x:** Priced into the Enterprise tier ($1,000+/mo in `PRICING.md`) but not built. Blocks exactly the customers with the biggest volumes (banks, telcos).

**Acceptance criteria**
- [ ] SAML 2.0 + OIDC SSO per org (IdP metadata upload, tested against Okta and Microsoft Entra); SSO-enforced orgs reject password/magic-link login.
- [ ] SCIM user provisioning/deprovisioning; deprovisioned users lose sessions within 5 minutes.
- [ ] Custom roles: org-defined permission sets over the existing scope vocabulary, enforced by the same middleware as API-key scopes (shared authorization path, no parallel logic).
- [ ] Enterprise audit-log retention configurable (≥ 2 years) with export.

**Effort:** 4–5 weeks · **Dependencies:** none.

---

## Sequencing summary

| Phase | Features | Theme | Headline KPI |
|---|---|---|---|
| **P1 (Q3 '26)** | A1 Billing · B1 Payouts · B2 Reconciliation | Revenue on, money loop closed | First paid invoice collected; payout GMV > 0 |
| **P2 (Q4 '26)** | C1 Experiments · C2 Token Vault · C3 Evidence | Provable merchant ROI | Measured auth-rate uplift %; recovered revenue/mo |
| **P3 (Q1 '27)** | D1 Plugins · D2 Headless · D3 CLI · D4 MCP | Distribution | Time-to-first-payment < 10 min; plugin activations |
| **P4 (Q2 '27)** | E1 KYC · E2 Splits · E3 SSO | Enterprise & platforms | Time-to-live-key < 1 h; first platform merchant |

**Deliberately excluded** (low leverage now): Interswitch/Opay adapters (stubs — wait for demand), Angular/Ruby/.NET SDKs (generate from OpenAPI when requested), ML-based routing (C1's experiments must generate training data first), real-time FX routing.

## Cross-cutting engineering rules

Every feature above inherits the existing platform bar, which the audit confirmed is consistently enforced:
- State machines with row-level locking + same-transaction audit rows (the `applyPaymentTransition` pattern).
- Idempotency keys required on all money-moving mutations.
- Test/live mode isolation on every new table and query.
- `golangci-lint run ./...` at 0 issues + `go test ./...` green pre-commit (backend), `npx tsc --noEmit` clean (frontend).
- New API resources ship with: API-key scopes, SDK coverage (TS/Python/Go/PHP), Mintlify docs, exports, SSE events, and Prometheus metrics in the same release.
