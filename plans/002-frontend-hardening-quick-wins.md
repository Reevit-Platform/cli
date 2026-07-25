# Plan 002: Frontend hardening & DX quick wins (redirect scheme guard, color validation, proxy error bodies, vitest excludes, .env.example)

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: in `frontend/`, run
> `git diff --stat 5bd5ced..HEAD -- "app/pay/[code]/checkout-view.tsx" "app/(sass)/dashboard/payment-links/components/create-payment-link-sheet.tsx" lib/api/create-proxy-handler.ts vitest.config.ts`
> If any in-scope file changed, compare the "Current state" excerpts against
> the live code before proceeding; on a mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: security / dx
- **Planned at**: frontend commit `5bd5ced`, 2026-07-07

## Why this matters

Five small, independent fixes, each verified against the code. (a) The buyer checkout assigns a merchant-stored `redirect_url` to `window.location.href`; the backend already rejects non-http(s) schemes (`internal/infra/security/ssrf.go` `ValidateURL`), so this is **defense-in-depth**, but the frontend's zod `.url()` accepts `javascript:` URIs and the client should not rely on a server it doesn't own the deploy timeline of. (b) Branding color fields accept arbitrary strings that flow into inline `style` props — safe today, a latent hazard on refactor. (c) The API proxy silently discards non-JSON error bodies, blinding debugging of backend 5xx/nginx HTML errors. (d) vitest sweeps stale `.claude/worktrees/agent-*` specs — 43 noisy "failed files" every full run. (e) There is no `.env.example`, so onboarding requires reading code or copying someone's real `.env`.

## Current state

All paths relative to `frontend/`.

1. **Redirect sink** — `app/pay/[code]/checkout-view.tsx:559-561`:
   ```tsx
   if (link.redirect_url) {
     setTimeout(() => (window.location.href = link.redirect_url!), 3000);
   }
   ```
   (There is also a message render at line 230: `{link.redirect_url && <p>Taking you back in a moment…</p>}`.)

2. **Zod schemas** — `app/(sass)/dashboard/payment-links/components/create-payment-link-sheet.tsx` (~lines 51-64):
   ```ts
   redirect_url: z.string().url().optional().or(z.literal("")),
   webhook_url: z.string().url().optional().or(z.literal("")),
   ...
   background_color: z.string().optional(),
   text_color: z.string().optional(),
   primary_color: z.string().optional(),
   logo_url: z.string().url("Enter a valid logo URL").optional().or(z.literal("")),
   ```
   `z.string().url()` accepts any parseable URL including `javascript:` schemes. The edit path (`payment-link-detail-sheet.tsx:181`) submits `editForm.redirect_url` with no schema at all (plain state form).
   The shared branding editor `app/(sass)/dashboard/payment-links/components/checkout-branding.tsx` also carries color fields — check it for the same missing color validation.

3. **Proxy JSON swallow** — `lib/api/create-proxy-handler.ts` (~lines 234-240):
   ```ts
   } else if (responseContentType?.includes("application/json")) {
     try {
       const data = await response.json();
       nextResponse = NextResponse.json(data, { status: response.status });
     } catch {
       nextResponse = new NextResponse(null, { status: response.status });
     }
   } else {
     const body = await response.text();
     ...
   ```
   On JSON parse failure the original body is lost. Note: `response.json()` consumes the body stream, so the fix must read `response.text()` **first** and `JSON.parse` it, falling back to returning the raw text.

4. **vitest excludes** — `vitest.config.ts:15`:
   ```ts
   exclude: ["e2e/**", "node_modules/**", ".next/**"],
   ```
   Missing `.claude/**` (stale agent worktrees under `.claude/worktrees/agent-*` contain copies of specs).

5. **No `.env.example`** — `.gitignore` line 35 ignores `.env*`; a real `.env` and `.env.local` exist locally (never committed) containing live keys (AI gateway, Sentry auth token, Google GenAI, webhook secret — **never copy the values**).

Conventions: after every change run `npx tsc --noEmit` (mandatory, per `frontend/CLAUDE.md`). Tests use vitest + Testing Library; a structural exemplar for schema-level tests is any `*.test.ts` colocated under `app/(sass)/dashboard/` (e.g. `components/command-palette-entries.test.ts`).

## Commands you will need

Run from `frontend/`.

| Purpose | Command | Expected on success |
|---|---|---|
| Typecheck | `npx tsc --noEmit` | 0 errors |
| Tests | `npm run test` | all pass, zero worktree noise after step 4 |
| Lint | `npx eslint .` | 0 errors |
| Format | `npx prettier --check .` | clean |

## Scope

**In scope** (the only files you should modify/create):
- `app/pay/[code]/checkout-view.tsx`
- `app/(sass)/dashboard/payment-links/components/create-payment-link-sheet.tsx`
- `app/(sass)/dashboard/payment-links/components/payment-link-detail-sheet.tsx` (redirect submit path only)
- `app/(sass)/dashboard/payment-links/components/checkout-branding.tsx` (color validation only, if it has its own schema)
- `lib/api/create-proxy-handler.ts`
- `vitest.config.ts`
- `.env.example` (create), `.gitignore` (one line: un-ignore `.env.example`)
- New colocated test files for the above
- `frontend/CLAUDE.md` (changelog entry)

**Out of scope** (do NOT touch):
- The backend's `ssrf.ValidateURL` — already correct.
- The checkout page's payment/SDK logic beyond the redirect guard.
- `app/preview/*` harnesses.
- Any UI/copy redesign of the sheets.

## Git workflow

- Branch in `frontend/` from `origin/dev-frontend`: `fix/hardening-quick-wins`.
- Conventional commits, one per fix: `fix(pay): guard redirect_url to http/https`, `fix(proxy): preserve non-JSON error bodies`, `chore(test): exclude .claude worktrees from vitest`, `chore(dx): add .env.example`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Scheme-guard the redirect sink

In `checkout-view.tsx`, add a module-level helper:
```ts
function safeRedirectURL(raw: string | null | undefined): string | null {
  if (!raw) return null;
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}
```
Compute `const redirectTarget = safeRedirectURL(link.redirect_url)` and use it at both sites (the line-230 message and the line-560 assignment) instead of `link.redirect_url`.

**Verify**: `npx tsc --noEmit` → 0 errors.

### Step 2: Tighten the zod schemas

In `create-payment-link-sheet.tsx`, replace `z.string().url()` for `redirect_url`, `webhook_url`, and `logo_url` with a scheme-checked refinement (zod v4):
```ts
const httpUrl = z
  .string()
  .url()
  .refine((v) => /^https?:\/\//i.test(v), "Must start with http:// or https://");
```
Add hex-color validation to the three color fields: `z.string().regex(/^#[0-9a-fA-F]{3,8}$/, "Use a hex color like #0c1929").optional().or(z.literal(""))` — first grep how presets populate these fields (`PRESET_COLORS`, `checkout-branding.tsx`) and match the format they actually emit; if presets emit `rgb(...)` strings, widen the regex accordingly rather than breaking presets. Apply the same to `checkout-branding.tsx` if it owns a schema. For the detail sheet's edit path (no zod), validate `editForm.redirect_url` with the same `httpUrl` check before submit and surface the existing inline-error pattern used by that form.

**Verify**: `npx tsc --noEmit` → 0 errors; `npm run test` → existing payment-links tests pass.

### Step 3: Preserve error bodies in the proxy

In `create-proxy-handler.ts`, change the JSON branch to read text-first:
```ts
} else if (responseContentType?.includes("application/json")) {
  const raw = await response.text();
  try {
    nextResponse = NextResponse.json(JSON.parse(raw), { status: response.status });
  } catch {
    nextResponse = new NextResponse(raw, {
      status: response.status,
      headers: { "Content-Type": "text/plain" },
    });
  }
}
```

**Verify**: `npx tsc --noEmit` → 0 errors. Check `lib/api/` for an existing proxy test file (e.g. colocated `*.test.ts` or `proxy.test.ts` at repo root) and run it.

### Step 4: Exclude stale worktrees from vitest

In `vitest.config.ts`, extend excludes: `exclude: ["e2e/**", "node_modules/**", ".next/**", ".claude/**", ".conductor/**"]`.

**Verify**: `npm run test` → zero "failed" files from `.claude/worktrees/`; total passing count unchanged or higher.

### Step 5: Create `.env.example`

List every variable **name** present in `.env` and `.env.local` (run `grep -o '^[A-Z_][A-Z0-9_]*' .env .env.local | sort -u` locally), write `.env.example` with each name set to an empty value plus a one-line comment for the non-obvious ones. **Never copy a value.** Add `!.env.example` below the `.env*` line in `.gitignore`.

**Verify**: `git check-ignore .env` → still ignored; `git check-ignore .env.example` → exit 1 (not ignored); `grep -c '=' .env.example` matches the variable count; no secret values (`grep -E '=(.+)$' .env.example` → only empty or placeholder values).

### Step 6: Tests + changelog

Write the tests in the Test plan; add a `frontend/CLAUDE.md` changelog entry summarizing the five fixes.

**Verify**: full gates — `npx tsc --noEmit`, `npx eslint .`, `npm run test`, `npx prettier --check .` all clean.

## Test plan

- New `app/pay/[code]/checkout-view.test.ts` (or extend an existing colocated test): unit-test `safeRedirectURL` — accepts `https://…`/`http://…`, rejects `javascript:` URIs, protocol-relative strings (URL-parse failure), and empty/undefined. Export the helper for testability.
- Extend the payment-links sheet tests (or add a schema-level test file): `formSchema` rejects a `javascript:` redirect_url and a non-hex color; accepts empty strings and valid values, including every preset color emitted by `PRESET_COLORS`.
- Proxy: a test that a 502 with `content-type: application/json` but HTML body returns the raw body, not an empty response (mock `fetch`; follow the pattern in the existing proxy test if present, else use vitest `vi.stubGlobal`).
- Verification: `npm run test` → all pass including the new tests.

## Done criteria

- [ ] `grep -n "window.location.href = link.redirect_url" "app/pay/[code]/checkout-view.tsx"` → no matches
- [ ] `grep -n 'z.string().url().optional()' "app/(sass)/dashboard/payment-links/components/create-payment-link-sheet.tsx"` → no bare matches for redirect/webhook/logo fields
- [ ] `grep -n 'NextResponse(null, { status: response.status })' lib/api/create-proxy-handler.ts` → no match in the JSON branch
- [ ] `grep -n '.claude' vitest.config.ts` → match present
- [ ] `.env.example` exists, tracked, contains no values
- [ ] `npx tsc --noEmit`, `npx eslint .`, `npm run test`, `npx prettier --check .` all clean
- [ ] Only in-scope files modified (`git status`)
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back if:

- The excerpts in "Current state" don't match the live code (drift).
- Preset colors or existing stored branding values fail the new color regex in tests — widening the regex once is fine; if formats are genuinely heterogeneous, stop and report what formats exist.
- The proxy change breaks the SSE (`text/event-stream`) branch or cookie forwarding tests — those paths must remain byte-identical.
- You find `redirect_url` rendered anywhere as an href/script beyond the two checkout-view sites and the detail sheet's display link — report the extra sink instead of expanding scope.

## Maintenance notes

- If the backend's `ssrf.ValidateURL` policy ever changes (e.g. allowing custom schemes for app deep links), the frontend guard and zod refinement must change in lockstep — they intentionally mirror it.
- Reviewer: check the proxy diff carefully — `response.json()` → text-first parse must not change the happy-path response shape (still `NextResponse.json`).
- Deferred: rate-limiting/abuse controls on `/pay` are a backend concern; bundle analysis for icon/recharts weight was audited as MED-confidence and left unplanned pending a `next build --analyze` measurement.
