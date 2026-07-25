# Plan 002: Single-source plan entitlements + delete the dead landing tree

> **Executor instructions**: Follow this plan step by step. Run every verification command and
> confirm the expected result before moving on. If anything in "STOP conditions" occurs, stop and
> report — do not improvise. When done, update the status row in
> `plans/2026-h2-recovery/README.md`.
>
> **Drift check (run first)**: in `frontend/`,
> `git diff --stat 2469201..HEAD -- src/components/landing src/components/landing-v3 src/routes/pricing src/routes/dashboard/billing`;
> in `backend/`, `git log --oneline 26641116..HEAD -- db/migrations | head`. Reconcile against
> "Current state".

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MEDIUM (deleting components; a missed import breaks the marketing build)
- **Depends on**: none
- **Category**: product truth / tech debt
- **Repos**: `frontend/`, `backend/` (read-mostly), `mintlify-docs/`

## Why this matters

The audit found the Free plan advertising 5,000 webhook attempts in one place and 10,000 in another
and concluded pricing contradicts itself. It does, and the cause is precise: **plan entitlements are
hand-typed into at least five separate places**, four of which are React components, two of which
are in a dead component tree that is no longer rendered by any route but is still edited.

This is not a copywriting problem. It is a missing source of truth, and it will regenerate the same
class of bug every time pricing moves. The backend already has the real source — the `plans` table,
seeded in migration `000034` and amended by `000044`/`000059`/`000060`/`000061`/`000062`, with a
`features` jsonb carrying `smart_routing`, `fraud_rules`, `team_seats` and `data_retention_days`.
Nothing on the marketing surface reads it.

## Current state

### The contradiction, exactly

| Location | Claim |
|---|---|
| `frontend/src/components/landing-v3/pricing-plans.tsx:47` | `"10,000 webhook attempts / mo"` |
| `frontend/src/routes/dashboard/billing/-components/billing-ui.ts:23` | `"5,000 webhook attempts a month"` |
| `frontend/src/components/landing/Pricing.tsx:247` | `"5,000 webhook attempts / mo"` |
| `frontend/src/content/changelog.ts:366` | "Free tier webhook attempts limit updated from 10,000 to 5,000/mo **across all pricing displays**" |

The changelog records the intended value as **5,000** and claims the change landed everywhere. It
did not: `landing-v3/pricing-plans.tsx` — the component the live `/pricing` route actually renders —
was missed. **The public pricing page is the stale one.**

### The dead tree

`frontend/src/routes/pricing/index.tsx` imports from `~/components/landing-v3/*` exclusively
(`PricingPlans`, `Faq`, `FinalCta`, `Footer`, `PillLink`, `Reveal`). Meanwhile
`frontend/src/components/landing/` still contains `Pricing.tsx`, `PricingPage.tsx` and
`PricingPageContent.tsx` — three more pricing surfaces, each with its own hand-typed comparison
table (`PricingPageContent.tsx:107` "Webhook Attempts", `PricingPage.tsx:47` "Sandbox Webhook
Attempts"). Determine per component whether anything still imports it; the ones that nothing
imports are the drift source and should go.

The same duplication exists beyond pricing: `landing/` holds `Navbar`, `FinalCTA`, `TrustStrip`,
`FeaturesPage`, `IntegrationsPage`, `ComparePage`, `AboutPage`, `PrivacyPage`, `TermsPage`,
`ChangelogPage`, `StatusPage`, `FAQPage` — against `landing-v3/` equivalents. **Some of `landing/`
is still live** (e.g. `PrivacyPage.tsx` and `TermsPage.tsx` contain the current legal entity and are
presumably still routed). Do not bulk-delete: prove per file.

### The real source of truth

`backend/db/migrations/000034_create_billing_tables.sql:36-40`:

```sql
INSERT INTO plans (id, name, monthly_price_cents, included_transactions, overage_fee_cents, max_connections, features) VALUES
('free',       'Developer (Free)', 0,      1000,    0, 2,  '{"smart_routing": false, "fraud_rules": false, "team_seats": 1,  "data_retention_days": 7}'),
('starter',    'Starter',          4900,   10000,   5, 4,  '{"smart_routing": false, "fraud_rules": false, "team_seats": 3,  "data_retention_days": 30}'),
('growth',     'Growth',           19900,  100000,  2, -1, '{"smart_routing": true,  "fraud_rules": true,  "team_seats": 10, "data_retention_days": 365}'),
('enterprise', 'Enterprise',       100000, 1000000, 1, -1, '{"smart_routing": true,  "fraud_rules": true,  "team_seats": -1, "data_retention_days": -1, "sso": true}');
```

Note this seed is itself amended by later migrations (`000044` features/meters, `000059` Paystack
codes, `000060` prices, `000061` workflows-for-starter, `000062` smart-routing-for-starter) — so the
**live table**, not the seed, is authoritative, and `max_connections` here (2 on free) already
contradicts the "1 live connection" marketing copy. Reconcile that during Step 1.

Webhook attempts do not appear in `features` at all — that entitlement exists only in prose. It has
to be added to the model, not just to the copy.

### Related contradictions to fold in

- `mintlify-docs/security.mdx:186-188` documents rate-limit tiers named **Free / Pro / Enterprise**.
  There is no "Pro" plan; the tiers are free / starter / growth / enterprise.
- `mintlify-docs/security.mdx:249-250` states payment records retained 7 years, audit logs 24
  months, while plan copy advertises 7-day / 30-day / 1-year retention. These are reconcilable —
  they describe different record classes — but nowhere says so, which reads as a contradiction.

## Scope

**In scope**
- `backend/`: add the missing entitlement keys (webhook attempts, and anything else marketing
  claims that the table does not model) to `plans.features` via a new migration numbered **`000146`
  or higher** (see `backend/CLAUDE.md` — migrations below `000130` are silently skipped on the
  shared DBs; `000145` is taken by `create_cli_projects`); expose a public read endpoint or a
  generated artifact for plan entitlements.
- `frontend/`: one entitlements module consumed by every plan surface; delete provably-dead landing
  components; a copy test that fails when a surface hard-codes a number.
- `mintlify-docs/`: fix the plan names in `security.mdx`; add a retention matrix that distinguishes
  record classes from plan retention.

**Out of scope (do NOT touch)**
- Actual pricing levels, plan structure or willingness-to-pay changes. This plan makes the existing
  numbers consistent; it does not reprice anything. Repricing needs interviews and cost data.
- Billing enforcement logic, the metering worker, the 402 gatekeeper.
- Existing customers' terms.
- `docs/` (deprecated).
- Any `landing/` component you cannot prove is unreferenced.

## Git workflow

- backend: `origin/dev` → `feat/plan-entitlements-source`
- frontend: → `refactor/single-source-entitlements`
- mintlify-docs: `main` → `fix/plan-names-and-retention`
- Backend first (the frontend consumes it).

## Steps

### Step 1 findings (completed 25 Jul — AWAITING SIGN-OFF)

Sources compared: the `plans` table as it stands after migrations `000034` → `000044` → `000060` →
`000061` → `000062`; the live `/pricing` page (`components/landing-v3/pricing-plans.tsx` + the rate
table it imports from `components/landing/Pricing.tsx`); the dashboard
(`routes/dashboard/billing/-components/billing-ui.ts` + `lib/pricing.tsx`); and `backend/PRICING.md`.

**There are four currency/rate tables and none of them is the database.**

#### Agreed — no action needed

Included transactions (1,000 / 10,000 / 100,000), retention days (7 / 30 / 365), team seats
(1 / 3 / 10) and `fraud_rules` all match across DB, marketing and dashboard.

#### Conflicts requiring a decision

| # | Entitlement | `plans` table | Live `/pricing` | Dashboard | `PRICING.md` |
|---|---|---|---|---|---|
| 1 | **Webhook attempts (Free)** | *not modelled* | **10,000** | **5,000** | — |
| 2 | **Starter price** | `19900` | $15 / ₵199 | ₵199 | **$49** |
| 3 | **Growth price** | `64900` | $49 / ₵649 | ₵649 | **$199** |
| ~~4~~ | ~~`smart_routing`~~ | **RESOLVED** — see below | — | — | — |
| 5 | **`audit_logs` on Free** | **false** (`000044`) | **"Audit logs"** listed | **"…and audit logs"** listed | — |
| ~~6~~ | ~~`live_mode` on Free~~ | **NOT A CONFLICT** — `000091` set it `true` | — | — | — |
| ~~7~~ | ~~Free connections~~ | **NOT A CONFLICT** — `000091` set `max_connections = 1` | — | — | — |

> **Correction (25 Jul).** Rows 6 and 7 were wrong in the first pass. They were
> derived by replaying migrations `000034` → `000044` → `000060`–`000062` by hand
> and **missing `000091_enable_free_tier_live_mode`**, which had already set
> `live_mode: true` and `max_connections = 1` for Free. There was never a
> production live-mode outage for Free orgs.
>
> The lesson is the one the plan already states and the first pass ignored: **the
> live table is authoritative, not the seed and not a hand-replay of migrations.**
> The verified state is now locked by
> `adapters/repo/plan_entitlements_test.go`, which reads the real post-migration
> table.

#### Verified post-migration state (dumped from the DB, 25 Jul)

| Plan | price (GHS minor) | txns | max live conns | retention | smart_routing | audit_logs | live_mode |
|---|---|---|---|---|---|---|---|
| free | 0 | 1,000 | 1 | 7d | **true** | false → **true** (`000146`) | true |
| starter | 19900 | 10,000 | 4 | 30d | **true** | **false** | true |
| growth | 64900 | 100,000 | −1 | 365d | true | true | true |
| enterprise | 100000 | 1,000,000 | −1 | −1 | true | true | true |

#### Conflict 4 resolved (25 Jul): `smart_routing` was the wrong lever

Investigating this changed the question. `smart_routing` gates
`/routing-rules`, `/routing/ab-tests` and `/routing/decisions`
(`adapters/http/router.go:987`, `:1003`, `:1026`) — and **does not gate the
router**. Health- and fee-aware selection and in-request failover run on every
payment for every plan; there is no entitlement check near payment creation.

So restricting the flag would not have made routing a paid feature. It would
have left routing running for a merchant while denying them the ability to see
or steer it. Meanwhile the real boundary was already correctly tiered and already
enforced: `max_connections` at 1 / 4 / unlimited **live** connections
(`gatekeeper.go:316`). With one live connection there is nothing to route
between, so the flag is inert on Free regardless of its value.

Sandbox is separately hard-capped at 2 for every plan (`gatekeeper.go:276`), so
a Free user *can* prove two-provider failover before paying — the exact motion
§"Pricing direction" calls for. Turning the flag off on Free would have broken it.

**Resolution shipped** (backend `bed12a79`, frontend `8cc2fb0`):

- `smart_routing` stays on every plan — rules and the decisions log explain
  behavior the merchant is already subject to.
- Routing A/B tests move to a new `routing_experiments` entitlement
  (migration `000147`). They need volume to mean anything, which makes them a
  defensible boundary where the others were not.
- The migration ships `routing_experiments` **equal to each plan's existing
  `smart_routing`**, so nobody is downgraded on landing. Starter has had A/B
  tests since `000062`; removing them silently was the trap. Pulling that lever
  is now a data change plus customer comms.
- Marketing prices *scale* — "Providers you can route between: 1 / 4 /
  unlimited" — instead of the capability.

`basic_failover` is misnamed the same way: it gates `/dunning` and
`/policies/retry`, not failover. **Left alone deliberately** — renaming a live
entitlement key is a migration plus route churn for a cosmetic gain. Worth doing
alongside the next entitlement change, not on its own.

#### The currency problem (worst of the set)

Migration `000060` is commented *"Fix plan prices to match pricing page (in GHS cedis) — Starter:
₵199/mo = 19900 cents"*. So `monthly_price_cents` holds **GHS** for starter and growth.

But `billing-ui.ts:57-59` states the opposite in a comment:

> "the API's `monthly_price_cents` is the USD price, so symbol-swapping it would show the wrong
> number for GHS/NGN/KES"

and its fallback path renders `$${monthly_price_cents / 100}` — which would print **"$199"** for a
plan whose real USD price is **$15**. The fallback only fires for an unrecognised plan id, so it is
latent rather than live, but the two halves of the system disagree in writing about what the column
means.

Worse, the column is not even internally consistent: `enterprise` is `100000`, which matches
`PRICING.md`'s **$1,000** — USD — while starter and growth in the same column are GHS.

**`monthly_price_cents` currently has no single defined currency.** That has to be resolved before
anything is generated from it, and it is a billing-correctness question, not a copy question.

#### Duplication that will regenerate this drift

- `components/landing/Pricing.tsx` exports the rate table consumed by the *live* v3 pricing page —
  so the `landing/` tree is **not** dead and must not be bulk-deleted (Step 6's STOP condition
  applies).
- `lib/pricing.tsx` is a hand-maintained copy of that same rate table for the dashboard, carrying
  the comment *"Keep the rates in sync with the landing page until the apex migrates."* Documented
  manual duplication.
- Feature bullets are typed out separately in `landing-v3/pricing-plans.tsx`, `billing-ui.ts`,
  `landing/PricingPageContent.tsx` and `landing/PricingPage.tsx`.

### Step 1 — Establish the true entitlement set

Query the live `plans` table (not the seed) in each environment and diff it against every marketing
claim. Produce a table: entitlement × plan × (DB value, marketing value, docs value). Take it to the
operator and get one authoritative value per cell. Expect disagreements beyond webhook attempts —
`max_connections` on free is 2 in the seed against "1 live connection" in `/pricing` copy.

**Verify**: a signed-off table exists with no empty cells and no unresolved conflicts. **Do not write
code before this exists** — otherwise you will encode one of the wrong numbers into the new single
source.

### Step 2 — Model the missing entitlements (backend)

Migration `000146+` adding the agreed keys to `plans.features` (webhook attempts per month, and
whatever else Step 1 surfaced). Backfill all four plans. Validate up and down on a **fresh** database
via the compose migrate service — per `backend/CLAUDE.md`, "applied fine locally" is not evidence.

**Verify**: fresh-DB up/down clean; every plan row has every agreed key; `go test ./...` green.

### Step 3 — Expose entitlements

Serve the plan catalogue over a public, cacheable endpoint (no auth — it is public pricing) or
generate a checked-in TS artifact from the table at build time. Prefer the endpoint if the marketing
pages can fetch at build/SSR time; prefer generation if `/pricing` must be fully static. Decide with
the operator and record the decision in this file.

**Verify**: the response/artifact contains every plan and every agreed entitlement, and matches the
Step 1 table exactly.

### Step 4 — One entitlements module (frontend)

Create a single module that owns plan display data — price, included transactions, overage, and the
entitlement list — sourced from Step 3. Every marketing and dashboard plan surface renders from it.
No component may contain a hard-coded plan limit.

Migrate, in order: `landing-v3/pricing-plans.tsx` (live), then
`dashboard/billing/-components/billing-ui.ts`, then any surviving `landing/` surface.

**Verify**: `npx tsc --noEmit` clean; `/pricing` and `/dashboard/billing` show 5,000 webhook attempts
on Free and agree on every other number.

### Step 5 — Copy test

Add a test that fails when a plan number is hard-coded outside the entitlements module. A regex over
`src/components/` and `src/routes/` for digit-grouped numbers adjacent to entitlement words
(`transactions`, `webhook`, `connections`, `seats`, `retention`) with an explicit allowlist is
sufficient and cheap. The point is to fail loudly the next time someone types a number.

**Verify**: the test passes on the cleaned tree, and fails if you temporarily reintroduce
`"10,000 webhook attempts"` into a component.

### Step 6 — Delete the dead tree

For each file in `frontend/src/components/landing/`, prove liveness:
`rg -n "from \"~/components/landing/<Name>\"|components/landing/<Name>" frontend/src`. Delete only
files with zero references outside their own tree. Anything still referenced stays and gets migrated
to the entitlements module instead.

**Verify**: `npx tsc --noEmit` clean, the frontend builds, and every marketing route still renders
(`/`, `/pricing`, `/features`, `/integrations`, `/compare`, `/about`, `/privacy`, `/terms`,
`/changelog`, `/status`, `/faq`). Check each route in a browser, not just the build.

### Step 7 — Docs

`mintlify-docs/security.mdx:186-188` — rename the rate-limit tiers to the real plan ids. If limits
differ per real plan, get the true numbers; do not just relabel Pro → Growth and hope.

Add a retention matrix stating, per record class (payment records, audit logs, webhook events,
analytics/aggregates, PII), the retention period, the legal basis, whether it varies by plan, and the
deletion behaviour. Make explicit that plan "data retention" governs dashboard/API history windows
while payment records are retained 7 years for regulatory reasons — that single sentence dissolves
the apparent contradiction.

**Verify**: no occurrence of a non-existent plan name in `mintlify-docs/`; the matrix covers every
retention figure quoted anywhere on the public surface.

## Test plan

- Backend: migration up/down on a fresh DB; plan-catalogue response test; `go test ./...` +
  `golangci-lint run ./...` 0 issues.
- Frontend: entitlements module unit test against a fixture; the Step 5 copy test; `npx tsc --noEmit`;
  full build; manual pass over every marketing route.
- Docs: `rg -n "\bPro\b" mintlify-docs/` returns no plan-tier usage.

## Done criteria

- [ ] Signed-off entitlement table exists (Step 1) and is recorded in this file or linked from it
- [ ] `plans.features` models every entitlement marketing claims
- [ ] One frontend module owns plan display data; no hard-coded limits remain
- [ ] `/pricing` and `/dashboard/billing` agree on Free webhook attempts (5,000) and on every other number
- [ ] Copy test in place and demonstrated to fail on a reintroduced hard-coded number
- [ ] Dead landing components deleted; every marketing route verified in a browser
- [ ] `mintlify-docs` uses real plan names and carries a retention matrix
- [ ] Backend and frontend gates green; only in-scope files touched
- [ ] README status row updated

## STOP conditions

- Step 1 cannot reach an authoritative value for a cell → stop and escalate. Shipping a
  "single source" seeded with a guess is worse than the current mess, because it launders the guess
  as truth.
- The live `plans` table differs between environments → stop; that is a separate defect and this
  plan's premise (the table is authoritative) fails until it is fixed.
- A `landing/` component looks dead but is dynamically imported or referenced from a route file you
  did not grep → if in any doubt, keep it and migrate it. Deleting a live legal page is the worst
  possible outcome of this plan.
- Deleting a component would remove the only copy of legal text (`PrivacyPage.tsx`,
  `TermsPage.tsx`) → never delete these under this plan regardless of reference count.

## Maintenance notes

- The copy test is the durable part. Everything else is a one-time cleanup that will decay without it.
- Once entitlements are single-sourced, repricing becomes a data change plus a copy review rather
  than a hunt through components. That is the precondition for the pricing rework the audit wants —
  which is deliberately *not* in this plan.
- Reviewer: check Step 1's table before reviewing any code. The code is mechanical; the table is
  where the decisions are.
