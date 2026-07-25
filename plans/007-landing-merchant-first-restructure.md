# Plan 007: Merchant-first landing restructure + /developers page

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: in `frontend/`, run
> `git diff --stat 22f7c58..HEAD -- components/landing-v3 'app/(web)'`
> If the landing components or (web) routes changed since planning, re-read
> the changed files and reconcile the section inventory below before
> proceeding. Also re-read `frontend/CONTEXT.md` § "Marketing site" — it is
> the language contract for this plan.

## Status

- **Priority**: P1 (conversion surface; also fixes live credibility bugs)
- **Effort**: L (phase 0 alone is S and independently shippable)
- **Risk**: MED (marketing-only; no dashboard/API surface touched)
- **Depends on**: none (phase 1 §1.7 needs content inputs from the maintainer — see "Content prerequisites")
- **Category**: marketing / frontend
- **Planned at**: frontend commit `22f7c58` (branch `feat/marketing-hero-nav-refresh`), 2026-07-11

## Why this matters

The v3 landing page (live at `app/(web)/(home)/page.tsx`) was reviewed as two
personas on 2026-07-11. Verdict: the material is good but mis-sequenced — a
merchant must scroll past ~5,000px of developer content (code wall at section
4) to reach the checkout/payment-links story at section 7, while a developer's
first credibility click (footer GitHub) lands on a **stranger's profile**
(`github.com/reevit` is an unrelated user; the real org is `Reevit-Platform`).
The only product screenshot shows a dead account ("You processed GHS 0",
"0 providers", a "struggling" warning banner). Headline stats (99.9% uptime,
15% fewer failed payments) are **not backed by data** per the maintainer, and
the stats count-up zeroes real server-rendered numbers then trusts a GSAP
tween to restore them — observed frozen at "14.1% platform uptime / 0
providers" under rAF throttling.

Decisions settled in the grill session (recorded in `frontend/CONTEXT.md`
§ "Marketing site"):

1. **Merchant-first homepage + dedicated `/developers` page** (Paystack model).
2. **Hero Promise = reliability**: "when your payment provider has a bad day,
   your customers shouldn't notice."
3. **Homepage arc = pain → rescue → product** (10-section order in §1 below).
4. **No unbacked numbers**: unprovable stats become Mechanism Language; any
   uptime mention links the live `/status` page.
5. **Trust Strip**: named registered company + security-architecture facts +
   public founder story. No certification claims.
6. **Ghana-first Voice**: headlines speak Ghana; NG/KE breadth is stated
   factually in coverage sections (the 7-provider registry in
   `types/connections.ts` genuinely spans GH/NG/KE — the claim is true, the
   voice was the issue).
7. **`/developers` = code-first, CLI second**: five-language code showcase as
   hero; the shipped-but-unmentioned CLI + sandbox simulator
   (`brew install reevit` / npm, `reevit listen`) as the second beat; every
   capability claim deep-links to docs.

## Current state (section inventory at `22f7c58`)

`app/(web)/(home)/page.tsx` renders, in order:
`Hero` → `ProviderTicker` → `FeatureGrid` → `CodeShowcase` →
`RoutingShowcase` → `DashboardShowcase` → `CheckoutShowcase` → `Statement`
(200vh GSAP scrub) → `TrustStats` (count-up) → `Testimonial` → `SdkSection` →
`FinalCta` → `Footer`. All components in `components/landing-v3/`.

Verified facts to build on:

- `@reevit/node@0.3.3` is live on npm; all five `CodeShowcase` tabs carry
  real, distinct snippets (`code-showcase.tsx`).
- `docs.reevit.io` resolves (`/introduction`, `/api-reference`).
- Provider registry (`frontend/types/connections.ts` `PROVIDERS`): paystack
  (GH,NG), hubtel (GH), flutterwave (GH,NG,KE), monnify (NG), mpesa (KE),
  stripe (multi), pawapay (multi) — **7 providers**; the stats slab's "7" is
  accurate but the ticker omits PawaPay and mixes PSPs with rails
  (Visa/Mastercard/AirtelTigo).
- `/status`, `/pricing`, `/contact`, `/changelog` routes exist under
  `app/(web)/`. Pricing facts usable for the teaser: free tier = 1,000 live
  transactions/mo, failed payments never billed, Starter $15/mo.
- CLI exists and is distributed (root repo: goreleaser + Homebrew cask + npm
  wrapper, `reevit listen` forwards signature-valid events; see memory/plan
  D3) — currently absent from all marketing surfaces.

## Phase 0 — unconditional fixes (shippable alone, in one PR)

0.1 **GitHub links** — in `components/landing-v3/footer.tsx` (and any other
    marketing reference: `grep -rn "github.com/reevit" app components`),
    change `https://github.com/reevit` → `https://github.com/Reevit-Platform`.

0.2 **Dashboard screenshot** — regenerate `public/images/feat/snapshot.webp`
    (and check `payments/intelligence/connections.webp` for the same disease)
    from the preview dashboard with seeded thriving data: non-zero GHS volume,
    high success rate, several providers, and the "recovered by routing"
    metric visible. Use the un-gated preview route + isolated dist-dir server
    pattern (`app/preview`, `frontend-preview` launch configs). No amber
    "struggling" banner may appear in a marketing screenshot.

0.3 **Stats count-up stranding** — in `trust-stats.tsx`, remove the
    zero-then-tween pattern. Rule (generalize to any marketing section): JS
    may never overwrite server-rendered real values with a placeholder that
    only a rAF tween repairs. Either drop the count-up entirely (CSS reveal
    only) or animate a cosmetic overlay while the real number stays in the
    DOM. (The slab itself is redesigned in §1.8 — do 0.3 only if phase 1 is
    deferred; otherwise fold into §1.8.)

0.4 **Animation-robustness pass** — sections whose content starts at
    inline-style `opacity: 0` (or 0.08) and relies on a GSAP tween to become
    readable (`statement.tsx` scrub, `failover-trace.tsx`, any
    `gsap.from`-style reveals) must degrade readable: initial state legible,
    animation as enhancement — the `Reveal` component's IO + CSS-class
    pattern is the house-approved shape. Repro for the failure: the in-app
    preview browser (rAF-throttled) rendered everything below the hero black.
    `prefers-reduced-motion` must also read fully.

0.5 **Provider reconciliation** — add PawaPay to `provider-ticker.tsx`;
    audit every numeric provider claim on the page against the registry
    (currently "7" is correct); keep rails/wallets in the ticker only under
    the "providers your customers trust" framing, never counted as PSPs.

## Phase 1 — homepage restructure (pain → rescue → product)

New section order for `app/(web)/(home)/page.tsx`. Reuse components where
noted; copy rewrites follow Ghana-first Voice and Mechanism Language rules.

1.1 **Hero** (edit `hero.tsx`) — keep the orchestration map visual. New
    headline selling the reliability promise (working copy: "When your
    payment provider has a bad day, your customers shouldn't notice." —
    maintainer may tune wording, not the promise). Sub keeps "bring your own
    Paystack, Hubtel, MTN MoMo & Telecel" + settles-with-your-PSP line. CTAs:
    "Get started" + secondary anchor "See how it works" (→ §1.3). "Read the
    docs" moves to §1.9.

1.2 **Provider ticker** — kept (with 0.5 applied).

1.3 **"How Reevit saves a sale"** (rework `routing-showcase.tsx` +
    `failover-trace.tsx`) — the failover trace retold in merchant language:
    "Your customer pays with MoMo → the provider times out → Reevit retries
    on your Paystack in the same request → the sale completes. No code, no
    customer-visible error." Keep the trace UI; humanize labels (drop
    "rerouted by health policy" jargon on the merchant page — it moves to
    /developers). Provider-health percentages may stay as ambient texture.

1.4 **Checkout + payment links** (move `checkout-showcase.tsx` up) — keep
    Kente & Co. demo + the 3-step links story (create/share/get paid,
    WhatsApp/SMS/QR/Instagram bio) intact; it was the strongest merchant
    section as-is.

1.5 **Dashboard** (move `dashboard-showcase.tsx`) — with 0.2's thriving
    screenshots.

1.6 **Feature grid** (trim `feature-grid.tsx`) — merchant-voiced four:
    bring-your-own-keys, subscriptions & dunning, fraud policies, workflow
    automation. "Smart routing" content merges into §1.3; "Unified webhooks"
    moves to /developers.

1.7 **Trust strip** (NEW component `trust-strip.tsx`) + testimonial —
    registered company name + jurisdiction, security-architecture facts
    ("card details never touch Reevit — payments execute on your PSP; keys
    stored encrypted"), founder/"built in Accra" story, link to `/status`
    and `/about`. Merge `testimonial.tsx` (Miss Cookie Spices) into or
    directly after this strip. **Blocked on Content prerequisites below.**

1.8 **Mechanism panel** (replace `trust-stats.tsx` slab) — verifiable facts
    only: "7 payment providers behind one API" (registry-backed), markets
    covered (GH/NG/KE, factual coverage line), "automatic failover on every
    charge" (mechanism), uptime as a **link** ("live status →" → `/status`),
    no percentages. The "One network. Zero missed payments." headline may
    stay — it's a promise, not a statistic. No count-up (see 0.3).

1.9 **Developers band** (NEW, quiet) — one dark band: a single line of code
    (`const payment = await reevit.payments.createIntent(…)`), one sentence
    ("Five SDKs, a CLI with a local sandbox, and docs that assume you've
    been burned before."), CTA → `/developers`.

1.10 **Final CTA** (`final-cta.tsx`) — keep; sub becomes pricing-teaser
     honest: "Free for your first 1,000 payments a month. Failed payments
     are never billed." (facts from `/pricing`).

Cut from the homepage: `code-showcase.tsx` (→ /developers hero),
`sdk-section.tsx` (→ /developers), `statement.tsx` (200vh scrub — its line
"Africa doesn't pay one way. Neither should you." conflicts with Ghana-first
headline voice and it is the worst rAF-fragility offender; retire it or fold
one sentence of it into §1.8's headline area). Metadata: page `title`/
`description` in `page.tsx` currently say "…for Africa" — align to
Ghana-first (e.g. "Reevit | Payments that don't miss — built in Ghana").

## Phase 2 — `/developers` page

New route `app/(web)/developers/page.tsx` (+ `components/landing-v3/dev/`
or reuse where clean):

2.1 **Hero = `code-showcase.tsx`** moved here, headline "Charge in your
    language, not the provider's." kept.
2.2 **CLI + sandbox** (NEW) — install commands (`brew install reevit`, `npm
    i -g @reevit/cli` — verify exact package name against the CLI repo
    before writing copy), `reevit listen` demo block (signature-valid local
    event forwarding), sandbox simulator story: "feel a failover without
    moving real money."
2.3 **"The details are already handled"** (move from `sdk-section.tsx`) —
    every bullet becomes a deep link: idempotency → API reference section,
    signed webhooks → webhook signature docs (`X-Reevit-Signature`,
    sha256=hex HMAC-SHA256 — doc page exists per SDK work), SSE events →
    events guide, test mode → sandbox docs. If a target page doesn't exist
    on docs.reevit.io, note it in the PR description rather than linking to
    the docs root — no unlinked claims, but also no bait links.
2.4 **Failover, shown honestly** — the routing trace from §1.3 in its
    original technical voice PLUS the actual response payload (JSON with
    `route_attempts`) so an engineer sees the API shape, not just an
    animation.
2.5 **SDK grid** — all seven SDKs with links to npm/pypi/pkg.go.dev/
    packagist + the `Reevit-Platform` GitHub org.
2.6 **Nav**: `nav.tsx` gains "Developers" (`/developers`); "Docs" stays,
    pointing at docs.reevit.io. Footer "Developers" column links the new
    page first.

## Phase 3 — voice + metadata sweep

3.1 Ghana-first copy pass over `/features`, `/compare`, `/faq`, `/about`
    where they repeat "Africa" umbrella claims or unbacked numbers (grep:
    `99.9`, `15%`, `Africa pays`). Fix per the same rules; do not redesign
    those pages.
3.2 OG/social metadata + sitemap entry for `/developers`; use the
    fixing-metadata skill checklist.

## Content prerequisites (maintainer supplies before §1.7 ships)

- Registered company legal name + jurisdiction line.
- Founder blurb (name(s), one sentence, optional photo) — "built in Accra by…".
- Sign-off that the security-architecture facts as worded are true today.

If these stall, ship phases 0/1 with §1.7 as testimonial-only and a footer
company line placeholder — do not invent facts.

## Verification

- `npx tsc --noEmit` → 0 errors; eslint + prettier clean (repo gate).
- Preview each phase on an isolated dist-dir webpack server (existing
  `frontend-preview-hero` config, port 4331) — screenshot desktop dark,
  light, and 375px mobile for every new/moved section.
- Animation-robustness check: with DevTools CPU throttling ×20 (or the
  in-app preview browser), every section below the hero must be readable
  without any rAF tick; `prefers-reduced-motion: reduce` must show all
  content and final stat values.
- Link audit: every `<a>` on `/` and `/developers` resolves (re-run the
  href-collection snippet from the review; curl external targets).
- Local Playwright is broken on this machine — verify e2e specs by source
  and let CI run them (documented repo quirk).
- Three-lockfile rule if any dependency is added (bun + pnpm + npm).

## STOP conditions

- The maintainer hasn't supplied §"Content prerequisites" and you are about
  to write a company name, founder fact, or security claim — STOP that
  section, ship the rest.
- You find yourself adding a numeric marketing claim not derivable from the
  provider registry, the pricing page, or a linked live surface — STOP;
  Mechanism Language instead.
- Turbopack CSS corruption symptoms (garbage selectors, missing gradients)
  — switch to `--webpack` dev servers and `rm -rf .next*`; do not ship
  around it.
- Any change that would edit dashboard (`(sass)`) surfaces — out of scope.
