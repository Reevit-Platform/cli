# Frontend migration: Next.js 16 → TanStack Start

**Status:** Phases 0–3 and the core Phase 4 migration are COMPLETE (2026-07-18) — foundation plus dashboard, platform, auth, cli, pay, onboarding, all 25 apex marketing page templates, 10 MDX guides, and the SEO/crawl surface are ported (`tsc` 0 errors, vitest 638/638 green; production build boots; browser and HTTP smoke checks cover the main marketing, feature, guide, auth, dashboard, platform, and pay surfaces). Remaining work is deployment-provider integration, marketing image/Lighthouse parity, full production crawl/E2E, subdomain/apex cutovers, and Next.js decommissioning.
**Started:** 2026-07-18
**Owner:** Kenneth
**Source app:** `frontend/` (Next 16.2, App Router, React 19, Tailwind v4, TanStack Query v5)
**Target:** TanStack Start v1 (Vite), same React/Tailwind/Query versions — no version bumps needed.

---

## Why this is feasible (inventory summary, measured 2026-07-18)

| Signal | Value | Implication |
|---|---|---|
| `"use client"` files | 395 | App is client-first; components port as-is |
| Server actions | 0 | Nothing to rewrite |
| ISR / revalidate / next-cache | 0 | No caching model to replicate |
| Async RSC pages | ~3 (MDX guides + a redirect stub) | Port to loaders or keep static |
| React Query usage | 80 files | Moves verbatim; Start is built around it |
| Real routes | 99 pages (~28 are dev-only previews) | ~70 production routes to port |
| API route handlers | 7 (6 thin proxies + 1 AI streaming) | Port to Start server routes |

The cost concentrates in: `proxy.ts` (subdomain rewrites + session gate),
`lib/api/create-proxy-handler.ts` (cross-subdomain Set-Cookie surgery), and
framework services (Sentry, next/image ×31, next/font, metadata/robots/sitemap,
CSP headers, PostHog `/ingest` rewrite).

## Strategy: strangler by subdomain, not big-bang

`dashboard.reevit.io`, `platform.reevit.io`, and the apex are separable apps
behind DNS/CDN. Migration order:

1. **platform.** — internal users, smallest blast radius
2. **dashboard.** — the bulk of the app
3. **apex (marketing + guides + /pay)** — last, after SEO and performance parity are proven

The old Next app stays deployed throughout migration = instant rollback per
subdomain. During transition both apps run side by side; the session cookie is
already `Domain=.reevit.io`, so auth works across both frameworks for free. The
two-app state is temporary: successful apex cutover is followed by complete
Next.js decommissioning.

## Key design decisions

- **Project location:** `frontend-start/` in the primeflow root (sibling of `frontend/`), now its own Git repository.
- **Routing:** file-based TanStack Router (`src/routes/`). Route groups `(sass)`/`(web)` map to Router's pathless layout routes.
- **Subdomain rewriting:** NOT re-implemented as invisible rewrites inside the app. Each deployment gets a `BASE_SURFACE` env (`dashboard` | `platform` | `web`) and mounts the matching route subtree at `/`. Local dev keeps all surfaces on one server under path prefixes, matching today's localhost behavior. This deletes the rewrite magic instead of porting it — and kills the `usePathname`-vs-rewrite trap class of bugs permanently.
- **Session gate:** global server middleware checks `reevit_session` cookie presence (same presence-only check; Go backend stays the security authority). Public checkout allowlist (`PUBLIC_API_PATTERNS`) carried over verbatim and kept mirrored with `adapters/http/router.go`.
- **API proxy:** ported to standard Fetch `Request`/`Response` in a framework-agnostic `src/server/proxy.ts` + thin catch-all server routes. Cookie-rewrite logic (`Domain=.reevit.io`, `SameSite=None`, SSE passthrough, redirect handling) copied byte-for-byte in behavior; existing `proxy.test.ts` cases ported to vitest against plain Requests.
- **Images:** dashboard/platform use the existing plain `<img>` compatibility layer. Phase 4 must give above-the-fold marketing media explicit dimensions, responsive sources, preload/fetch priority where justified, and CDN image resizing so apex LCP/CLS does not regress from `next/image`.
- **Fonts:** `next/font` → Vite static font files + `@font-face` in CSS (gratoClassic + Manrope + mono). Keep the "mono is data" rule.
- **Sentry:** `@sentry/nextjs` → `@sentry/react` (+ `@sentry/vite-plugin` for sourcemaps).
- **PostHog:** `instrumentation-client.ts` → plain init in the client entry; `/ingest` rewrite → a Start server route proxy (same-origin preserved).
- **SEO/metadata:** `metadata` exports (26) + `robots.ts`/`sitemap.ts` → Router `head()` on routes + static/generated files. Only matters for apex; dashboard/platform need only titles.
- **typedRoutes/typedEnv:** replaced by TanStack Router's native route typing (stronger than Next's) and a small zod-validated env module.
- **nuqs (URL state):** keep via its TanStack Router adapter initially; fold into native typed `validateSearch` opportunistically later. Do NOT mass-rewrite filter hooks in the migration.
- **CSP/headers:** move from `next.config.ts` `headers()` to server middleware (and/or CDN config at deploy time).

## Phases

### Phase 0 — Spike: prove the risky 20% (3–5 days)
Scaffold `frontend-start/` and stand up a walking skeleton:
- [x] Vite + TanStack Start + Tailwind v4 + strict TS builds clean *(2026-07-18: latest-everything — Start 1.168, Router 1.170, Vite 8, TS 7, Vitest 4, Tailwind 4.3; exact pins)*
- [x] `/api/v1/$` catch-all proxy ported (headers, cookie policy, SSE, 503 fallback) + vitest parity tests *(10/10 green; curl-verified: no-session 401, public checkout allowlisted, 503 fallback with backend down)*
- [x] Session-presence gate + `/auth` redirect with `?redirect=` *(curl-verified 307; implemented as a route-level `beforeLoad` server fn — revisit as global middleware when more surfaces exist)*
- [x] Real authed round-trip against the local Go backend *(2026-07-18: minted a magic-link PASETO with the backend's PASETO_KEY, verified via `/api/v1/auth/verify` THROUGH the Start proxy → Set-Cookie surgery correct (Lax, no Domain on http localhost); `/api/v1/auth/me` → 200 full payload; `/dashboard` 200 with session / 307 without)*
- [x] SSE works through the Start proxy *(`/api/v1/events/stream` `: connected` comment arrived immediately via curl -N — unbuffered)*

**API-drift notes (Start 1.168 / unified route API):** server routes are `createFileRoute` with `server.handlers` (no `createServerFileRoute`); request access in server fns is `getRequest()` (not `getWebRequest`); TS 7 removed `baseUrl` (paths are tsconfig-relative); Vite 8 resolves tsconfig paths natively (`resolve.tsconfigPaths: true`, no plugin). Ecosystem included: Query (+ router-ssr-query integration), Table, Form, Virtual, Pacer, unified devtools. `@tanstack/react-db` deliberately skipped (0.1.x sync engine, wrong fit).
**Exit criterion:** authed round-trip `browser → Start → Go backend → UI` with real session cookie. If cross-subdomain auth can't be proven locally, stop and reassess.

### Phase 1 — Foundation parity (week 1)
- [x] Port `lib/`, `hooks/`, `stores/`, `types/`, `config/` (framework-agnostic — mostly file copies + import fixes) *(2026-07-18)*
- [x] Port `components/ui/*` (shadcn + Base UI — no Next imports expected) + all non-marketing `components/**` (Wave A: 95 files scripted, small hand-fix loop)
- [x] Root layout: fonts, theme (next-themes works standalone), Toaster, providers (Query, Auth, Mode) + command palette / keyboard-shortcuts via a `next/dynamic` shim
- [x] `next/link`→`Link`, `useRouter/usePathname/useSearchParams/useParams`→Router equivalents via `~/lib/navigation` shim + codemod script; shim's `Link` splits `href` `?query` into `to`+`search` (TanStack `to` is a bare path) and degrades to `<a>` outside a RouterProvider (unit tests)
- [x] `next/image`→`~/lib/image` (`<img>`), `next/dynamic`→`~/lib/dynamic` (React.lazy) shims
- [x] Env: `NEXT_PUBLIC_*` → `import.meta.env.VITE_*` codemodded at all call sites *(the `VITE_*` values themselves still need a defaults/env module — see below)*
- [x] Vitest harness (63→64 files, 604 passing); Playwright via `frontend/node_modules/playwright` + `channel: "chrome"` (session-cookie-injecting screenshot scripts)
- [ ] Onborda product-tour RUNTIME (overlay) — data layer ported, provider/context stubbed via the source's own `useOptionalOnborda` optional design; real overlay deferred

### Phase 2 — platform.* vertical (week 2)
- [x] Port `(sass)/platform/**` routes (Overview, Logs, KYC, Users, Features, Incidents, Audit, Failures, Traces incl. `$traceId`, Organizations, Coupons, Plans, Subscriptions, Settings, CLI, Waitlists, Refund-cap overrides…) — 19 pages + 27 support files scripted; `(admin)` route group flattened to `/platform/`; layout→`route.tsx` with server-side session `beforeLoad` gate; `admin-provider.tsx`→`-admin-provider.tsx` keeps the client-side platform-admin API check *(live-verified: /platform + /platform/organizations render real backend data as the operator console; logs stats/volume 404 is a backend gap, degrades gracefully)*
- [x] AI generate endpoint (`app/api/ai/generate`) → Start server route (Vercel AI SDK is framework-agnostic) *(2026-07-18: auth, rate limit, 256 KiB body cap, schema validation, structured Gemini streaming, and sanitized failures ported with focused tests)*
- [x] Sentry + PostHog wired (2026-07-18: TanStack Router tracing + privacy-masked replay, validated client/build config, conditional hidden sourcemap upload, and a same-origin streaming PostHog proxy hardened against credential leakage and protocol-relative SSRF)
- [ ] Deploy behind `platform.` on a staging host; E2E on production build
- [ ] **Cutover platform.reevit.io**, Next keeps serving everything else

### Phase 3 — dashboard.* vertical (weeks 3–4)
- [x] Port `(sass)/dashboard/**` (payments, connections, workflows incl. `$workflowId` builder, developers, settings, billing, checkout, links, subscriptions, failures, payouts…) — Wave B: 26 pages + 111 support files scripted, `components/`→`-components/`, `lib/`→`-lib/`, `[param]`→`$param`; dashboard `route.tsx` shell (sidebar/KYC banner/breadcrumbs/⌘K/notifications/network status/session+usage banners) with session `beforeLoad` gate *(live-verified: /dashboard + /dashboard/payments render real backend data)*
- [x] `/cli/confirm` standalone surface (own shell at `routes/cli/route.tsx`; legacy `/dashboard/cli/confirm` → redirect); OAuth login/callback handled by the `/api/v1/$` proxy + `/auth` pages
- [x] `/auth` vertical: layout→`route.tsx` (client session check + grid/logo chrome), `/auth`, `/auth/login`, `/auth/register` *(live-verified unauthenticated: magic-link + Google/GitHub OAuth forms render)*
- [x] `/pay/$code` buyer checkout: `route.tsx` (Toaster) + `index.tsx` page + `-checkout-view.tsx` *(live-verified: bogus code → graceful "Link not found" light-theme card)*
- [x] Command palette, feature-flag store, SSE notifications ported
- [x] Onboarding conversational flow — full authenticated conversational route and supporting flow components ported; production build route smoke-verified
- [ ] Full E2E pass on a production build (currently curl + Playwright screenshot spot-checks)
- [ ] **Cutover dashboard.reevit.io**

### Phase 4 — apex, marketing, and guides (required; week 5+)
- [x] Port the apex shell and all 25 `(web)` pages: home, about, changelog, compare, contact, developers, FAQ, integrations, pricing, privacy, status, terms, the feature index, and all feature detail pages. *(2026-07-18: all page templates and the onboarding route are registered and production-smoke-verified.)*
- [x] Audit all 76 `components/landing*` source files; port the 74 runtime modules plus the animation-contract test, excluding only the unused legacy `GuidesPage` implementation superseded by the current guides page; remove remaining Next-only imports through the existing navigation/image compatibility boundaries or direct TanStack APIs.
- [x] Port all 10 guide MDX documents and the `/guides`, `/guides/$category`, and `/guides/$category/$slug` routes with build-time content discovery, typed frontmatter, syntax highlighting, table of contents, category navigation, and not-found behavior. *(Vite MDX pipeline; 8 categories and 10 compiled guides covered by tests.)*
- [x] Recreate every marketing route's title, description, canonical URL, Open Graph/Twitter metadata, and structured data with TanStack Router `head()`; generate `robots.txt` and `sitemap.xml` from the same canonical route inventory. *(Production endpoints return the correct content types; sitemap contains 40 canonical URLs.)*
- [ ] Replace marketing-critical `next/image` behavior with responsive, dimensioned, CDN-resized assets; run Lighthouse on home, pricing, developers, one feature page, and one guide with no material LCP/CLS/accessibility/SEO regression.
- [ ] Re-verify `/pay/$code` on the apex deployment, including anonymous checkout and every `PUBLIC_API_PATTERNS` path; buyers must never require a session.
- [ ] Verify contact, waitlist, pricing, status, navigation, theme, external SDK/docs links, 404s, redirects, and a crawl for broken internal links on the production build.
- [ ] Deploy the web surface to an apex staging hostname; compare rendered metadata and screenshots against Next, then cut over `reevit.io` and `www.reevit.io` with the Next deployment retained only for rollback during the observation window.

### Phase 5 — decommission
- [ ] After the apex observation window passes, remove the Next deployment and archive/delete `frontend/`; TanStack Start becomes the only frontend implementation.
- [ ] Update CLAUDE.md, README/onboarding docs, launch configs, preview harnesses (`NEXT_DIST_DIR` pattern → Vite equivalent), CI, deploy manifests, monitoring, and rollback runbooks.
- [ ] Remove Next-only dependencies, compatibility shims that no longer have callers, stale DNS/CDN routes, and framework-specific environment variables.

## Risk register

| Risk | Mitigation |
|---|---|
| Cookie-domain regression logs everyone out | Byte-equivalent port + vitest parity suite + staging E2E across two subdomains before any cutover |
| SSE buffering through new proxy | Explicit Phase 0 exit criterion; test with real `EventSource` |
| Turbopack-era CSS workarounds interact badly with Vite | They don't apply (Vite ≠ Turbopack) — but re-verify the arbitrary-class surfaces; can re-simplify CSS after migration |
| Ecosystem gaps (image optimization, metadata, OG images, MDX) | Apex is mandatory but last; require SEO, Lighthouse, crawl, and metadata parity before cutover |
| Long-lived branch drift vs active feature work | Strangler order + porting whole subtrees quickly; freeze redesigns per area while its port is in flight |
| Start API drift (fast-moving v1) | Pin exact versions (same policy as next/axios pins) |

## Verification gates (every phase)
`tsc --noEmit` 0 errors · vitest green · production build boots · Playwright E2E on the built app (`E2E_CHANNEL=chrome`) · manual authed session against local Go backend.
