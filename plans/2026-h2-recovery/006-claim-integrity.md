# Plan 006: Claim integrity — deploy freshness, documentation contradictions, trust surface

> **Executor instructions**: Step 1 is a diagnosis step and must run before anything else in this
> plan — its result determines how much of the rest is real work versus a deploy. Follow the steps
> in order, honour the STOP conditions, and update the status row in
> `plans/2026-h2-recovery/README.md` when done.
>
> **Drift check (run first)**: in `frontend/`,
> `git diff --stat 2469201..HEAD -- src/components/landing/PrivacyPage.tsx src/components/landing/TermsPage.tsx src/components/landing-v3/footer.tsx src/routes/compare src/routes/developers`;
> in `mintlify-docs/`, `git diff --stat f27155e..HEAD -- security.mdx sdks`.

## Status

- **Priority**: P1
- **Effort**: M (code) + external dependencies (counsel, audit) that this plan can only initiate
- **Risk**: LOW technically; HIGH commercially if left undone
- **Depends on**: none. Step 1 informs 002.
- **Category**: trust / product truth / ops
- **Repos**: `frontend/`, `mintlify-docs/`, plus non-code coordination

## Why this matters

For payment infrastructure, a contradiction on the public surface is not a copy defect — it is a
signal about operational discipline, and it is read that way by exactly the technical buyer Reevit
wants. The audit found six of them.

But the most important finding is not on the list, because it only becomes visible when you compare
the site against the repo: **the audit reports the privacy policy naming "Reevit Technologies Inc."
while the footer names "Connectivity Support Systems LTD". The repository contains
`Connectivity Support Systems LTD` in all seven places and zero occurrences of "Reevit Technologies".**

```
frontend/src/routes/__root.tsx:71                        author meta
frontend/src/components/landing-v3/trust-strip.tsx:17,29,77
frontend/src/components/landing-v3/footer.tsx:248        © 2026
frontend/src/components/landing/TermsPage.tsx:184        Company
frontend/src/components/landing/PrivacyPage.tsx:267      Company
```

Either production is serving a stale build, or a separately-hosted legal page is not in this repo.
Both are worse than a copy error, because both mean **the public surface is not reliably derived
from the tree the team edits** — and every other fix in this plan set would be published into that
same gap. That is why Step 1 comes first.

## Current state

### Confirmed in-repo contradictions

| Issue | Location | Note |
|---|---|---|
| Rate-limit tiers named Free / **Pro** / Enterprise | `mintlify-docs/security.mdx:186-188` | No "Pro" plan exists; real ids are free / starter / growth / enterprise |
| Retention 7 years (payments) / 24 months (audit) vs plan copy 7-day / 30-day / 1-year | `mintlify-docs/security.mdx:215, 249-250` | Reconcilable — different record classes — but nowhere stated, so it reads as a contradiction |
| Webhook attempts 5,000 vs 10,000 | see plan 002 | Owned by 002, not repeated here |

### Compliance language exceeding visible evidence

`mintlify-docs/security.mdx` describes HSM protection, 24/7 monitoring, data residency options
(default EU-West Ireland, `:239`) and SAQ A eligibility. There is no Attestation of Compliance, no
ISO certificate, no SOC 2 report, no independent penetration-test summary and no trust centre. "PCI
DSS Level 1 service provider" and "designed to help you achieve SAQ A eligibility" are entirely
different claims, and a procurement reviewer will treat conflating them as disqualifying.

Note the residency claim specifically: default processing in EU-West for a Ghana-first product
invites a data-protection question the site does not answer.

### SDK claim vs published reality

The audit found the developer page claiming eight SDKs, the docs landing highlighting four server
SDKs, five JavaScript packages on the npm profile, and an empty PyPI publisher profile. The repo has
nine SDK submodules: `core`, `typescript`, `react`, `vue`, `svelte`, `go`, `python`, `php`, `rust`.
The recent root commits are almost entirely SDK release automation (`56b9299` explicit version bumps,
`53042c2` release preflight, `71eec36` recover failed releases, `6278733` stale release tags), which
suggests the publishing pipeline has been unreliable — consistent with what the audit observed
externally.

### Comparison page

`frontend/src/routes/compare/index.tsx` makes unsourced absolute claims about "other orchestrators".
Stitch, MoneyHash and Gr4vy publicly offer merchant-controlled provider relationships and agnostic
vaulting; the page as written is falsifiable in about two minutes by the exact buyer it targets.

### External dependencies this plan can only start

- Ghana payments counsel on Bank of Ghana licence classification. BoG licence categories explicitly
  include "switching & routing of payment transactions and instructions" under a PSP Scheme licence.
  Not holding funds does not settle the question.
- Data Protection Commission registration and obligations under Ghana's Data Protection Act.
- Independent penetration test.
- Whichever certification path (PCI, SOC 2, ISO 27001) the company chooses.

**These are engagements, not tickets.** This plan initiates and tracks them; it cannot complete them.

## Scope

**In scope**
- Diagnose and close the prod-versus-`main` gap (Step 1).
- Fix the in-repo documentation contradictions.
- Replace overreaching compliance language with exact status per claim.
- Publish an SDK support matrix generated from release reality, not from intent.
- Rewrite the comparison page as named, sourced comparison plus a decision guide.
- Trust Center v0: architecture, data flow, encryption, subprocessors, current compliance status,
  incident reporting, vulnerability disclosure.
- Initiate and track the external engagements above.

**Out of scope (do NOT touch)**
- Writing legal text. Terms, Privacy and the DPA need Ghana-qualified counsel. This plan gathers
  requirements and publishes what counsel returns; it does not draft.
- Asserting a regulatory position. The output of the counsel engagement is a determination, not a
  conclusion this plan may reach on its own.
- Claiming any certification not held.
- Plan entitlement numbers — owned by 002.
- `docs/` (deprecated).

## Git workflow

- frontend: → `fix/claim-integrity`
- mintlify-docs: `main` → `fix/compliance-language-and-sdk-matrix`
- Trust Center content: agree the surface with the operator before building — it may belong in
  `mintlify-docs/` rather than the marketing app.

## Steps

### Step 1 findings (completed 25 Jul) — **production is current; the audit was wrong twice**

Fetched the live site and every SDK registry and diffed against the repo.

#### (a) Is production stale? **No.**

`https://reevit.io/pricing` renders exactly the repo's pre-fix state: "10,000 webhook
attempts / mo", Starter `$15/mo`, Growth `$49/mo`, USD default, "1 live connection" on Free.
Every one of those matches `main` before the 25 Jul changes. **Production is deploying current
`main`.** There is no deploy-freshness problem, and the fixes in this plan set will reach users
once merged.

#### (b) Legal entity — **the audit's finding does not exist**

`https://reevit.io/privacy` states **"Company — Connectivity Support Systems LTD"**, matching the
repo's seven occurrences. There is no "Reevit Technologies Inc." anywhere on the live site or in
the tree. Audit item #2 (§"Legal identity is inconsistent") is **retired**.

#### (c) SDK publishing — **the audit's finding is also wrong**

The audit reported an empty PyPI publisher profile and only five JavaScript packages. Every
surface is in fact published:

| Package | Registry | Version | Last release |
|---|---|---|---|
| `@reevit/core` | npm | 0.9.0 | 2026-05-15 |
| `@reevit/react` | npm | 0.10.2 | 2026-07-18 |
| `@reevit/vue` | npm | 0.10.3 | 2026-07-18 |
| `@reevit/svelte` | npm | 0.10.2 | 2026-07-18 |
| `@reevit/node` | npm | 0.9.0 | 2026-07-13 |
| `@reevit/cli` | npm | 0.7.0 | 2026-07-25 |
| `@reevit/mcp` | npm | 0.1.1 | 2026-07-06 |
| `reevit` | PyPI | 0.9.1 | — |
| `reevit/reevit-php` | Packagist | published | — |
| `go-sdk` | Go proxy | available | — |
| `reevit` | crates.io | 0.1.0 | — |

Eleven published surfaces. The "eight SDKs" claim on the developer page is, if anything,
conservative. Step 4 becomes "generate the matrix from these registries", not "explain a gap".

#### (d) A real finding the audit missed: the privacy policy misstates retention

`https://reevit.io/privacy` — last updated **15 December 2024**, ~19 months stale — states:

> "Transaction records are kept 7 years for compliance; operational logs for 90 days."
> "Transaction records — retained for 7 years for compliance"

The retention cleanup service archives and deletes payments after the plan's
`data_retention_days` — **7 days on Free**. This is the same false claim corrected in
`mintlify-docs/security.mdx`, but here it sits in a **legal document**, which makes it
materially more serious.

Source: `frontend/src/components/landing/PrivacyPage.tsx:152-153` and `:283`.

**Deliberately not fixed here.** This plan's STOP condition is "any step requires drafting or
amending legal text → stop; counsel drafts, this plan publishes." The 7-year figure may reflect
advice or intent rather than an error, and replacing it with "7 days" could create its own
problem. **Escalate to counsel with the engineering facts attached.**

### Step 1 — Close the prod/main gap (DO THIS FIRST)

Fetch the live `/privacy`, `/terms`, `/pricing` and the footer, and diff the rendered claims against
what the repo produces at `main`. Determine which of these is true:

(a) production is serving a stale build → find out how stale, and why the deploy did not run;
(b) the live legal pages are served from somewhere outside this repo → identify where and bring them
under version control or document the split explicitly;
(c) the audit read a cached or archived copy → verify and record it, then move on.

Then check the same for `docs.reevit.io` against `mintlify-docs/`, and the SDK registries against the
submodule tags.

**Verify**: a written finding per surface (marketing, docs, each SDK registry) stating live version,
repo version, and the gap. If (a), a root cause for the deploy failure and a fix.

**This step's output changes the rest of the plan set.** If production is materially behind `main`,
some of the audit's other findings may already be fixed, and every subsequent fix will land into the
same gap unless it is closed first.

### Step 2 — Documentation contradictions

`mintlify-docs/security.mdx:186-188` — real plan ids, real per-plan limits. If limits genuinely
differ per plan, get the true numbers from the rate-limit config; do not relabel Pro → Growth on
assumption.

Retention: add a matrix by record class (payment records, audit logs, webhook events, analytics
aggregates, PII) giving period, legal basis, plan variance and deletion behaviour, with an explicit
sentence distinguishing plan "data retention" (dashboard/API history window) from statutory payment
record retention. Cross-check against what the deletion jobs actually do — **a retention matrix that
does not match the jobs is a worse liability than no matrix**, because it becomes a representation.

**Verify**: no non-existent plan name anywhere in `mintlify-docs/`; every retention figure quoted on
any public surface appears in the matrix; each matrix row confirmed against a real job or an explicit
"manual" note.

### Step 3 — Exact compliance status

For each claim currently on the security page, assign one of: **validated** (evidence available on
request), **in progress** (with a target), **inherited from a named vendor**, or **merchant
responsibility**. Rewrite each claim to state its status inline. Remove anything that fits none of
the four.

Handle the EU-West default residency explicitly, since it is a live question for a Ghana-first
product: state where data is processed, why, and what options exist.

**Verify**: every claim on the page carries a status. An adversarial read finds nothing that implies
a certification not held. Have someone outside the team read it cold and try to find one.

### Step 4 — SDK support matrix

Generate from release reality: package name, registry, current published version, last release date,
supported API version, maturity, CI status, compatibility policy. **Generate it — do not hand-write
it**, or it will be wrong within a month. Only mark a surface "official" once install, smoke and
webhook-verification tests pass in CI.

Reconcile the count claimed on the developer page with the matrix. If the honest number is five, say
five.

**Verify**: the matrix matches the registries at generation time. The developer page states the
matrix's number. Any SDK failing its smoke test is labelled accordingly rather than omitted.

### Step 5 — Honest comparison page

Rewrite `frontend/src/routes/compare/index.tsx` as named, sourced comparisons — MoneyHash, Stitch,
Primer, Payrails, Gr4vy, Yuno — with a link per claim and a "best for" decision guide. Acknowledge
where a competitor is genuinely stronger; on connector count and certifications, several are.

The differentiator to lead with is the one that is true and defensible: BYO-provider with direct
settlement, Ghana-rail depth, and — once plan 003 lands — measured incremental recovery.

**Verify**: every competitor claim carries a source link. A reviewer who knows the market cannot find
a false statement. Nothing on the page depends on a capability plan 003 has not yet delivered.

### Step 6 — Trust Center v0

Publish, with no certification claims: architecture and data flow, encryption at rest and in transit,
the subprocessor list, current compliance status (from Step 3), incident reporting and history,
vulnerability disclosure policy and contact, data residency, and a documented process for requesting
evidence.

A security questionnaire response pack — the standard answers, maintained in one place — belongs here
too. It is the artifact that converts a two-week procurement stall into a same-day reply.

**Verify**: a merchant's security reviewer can answer their standard questionnaire from the Trust
Center without emailing. Test this with a real questionnaire if any prospect will share one.

### Step 7 — Initiate the external engagements

Open and track, with an owner and a date each: Ghana payments counsel (BoG classification), Data
Protection Commission registration, independent penetration test, certification path decision. Track
them where the team actually looks, not in this file.

Publish the honest current status in the Trust Center — "regulatory review in progress with Ghana
payments counsel" is a perfectly respectable public statement. Silence is not, and neither is
implication.

**Verify**: each engagement has an owner, a date and a public status line.

## Test plan

Mostly editorial, but verifiable:

- Automated: no non-existent plan names in `mintlify-docs/`; SDK matrix regenerates and matches
  registries; every marketing route renders after edits; `npx tsc --noEmit` clean.
- Manual: cold adversarial read of the security page and the comparison page by someone who did not
  write them.
- Ongoing: add the prod-vs-repo diff from Step 1 as a recurring check, so this class of gap surfaces
  automatically rather than in the next audit.

## Done criteria

- [ ] Step 1 finding written per surface; if production was stale, root-caused and fixed
- [ ] No non-existent plan names in the docs; retention matrix published and matching the real jobs
- [ ] Every compliance claim carries an exact status; nothing implies an unheld certification
- [ ] Data residency stated and explained
- [ ] Generated SDK support matrix published; the developer page's count matches it
- [ ] Comparison page named and sourced, competitor strengths acknowledged
- [ ] Trust Center v0 live with a security questionnaire response pack
- [ ] All four external engagements opened with owner, date and public status
- [ ] Recurring prod-vs-repo check in place
- [ ] README status row updated

## STOP conditions

- Step 1 finds the live legal pages are not in this repo and nobody knows where they are served from
  → stop everything else in this plan and resolve that first. Publishing corrections into an unknown
  pipeline achieves nothing.
- Any step requires drafting or amending legal text → stop. Counsel drafts; this plan publishes.
- A compliance claim cannot be assigned one of the four statuses → remove it from the page. An
  unclassifiable claim is a claim nobody can stand behind.
- The retention matrix contradicts what the deletion jobs actually do → do not publish the matrix.
  Fix the jobs or state the real behaviour; a published matrix becomes a representation the company
  is held to.
- The SDK matrix reveals a package advertised but never published → say so plainly and unpublish the
  claim the same day. That is the single most damaging kind of finding for a developer product.

## Maintenance notes

- Step 4's generated matrix and Step 1's recurring diff are the durable parts. Everything else is a
  point-in-time cleanup that decays without them.
- The Trust Center is a living surface. Assign the same owner as product truth (README §7 Q3), or it
  becomes stale in exactly the way the current security page did.
- Reviewer: Step 1 first. If the deploy pipeline is not trustworthy, nothing else in this plan set
  reaches a customer.
