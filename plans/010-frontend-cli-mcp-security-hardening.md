# Plan 010: Frontend, CLI & MCP security hardening (OWASP sweep 2026-07-28)

> **Executor instructions**: Follow this plan task by task, in order. Run every
> verification command and confirm the expected result before moving on. Each
> task ends in a commit. If anything in "STOP conditions" occurs, stop and
> report — do not improvise. Do **not** update `plans/README.md`; the
> orchestrator maintains the index.
>
> **Drift check (run first)**:
> `git -C frontend diff --stat ce2df9c..HEAD -- server.mjs src/routes/platform/kyc/-components/kyc-review-drawer.tsx src/routes/platform/organizations/-components/org-drawer.tsx "src/routes/dashboard/payment-links/-components/payment-link-detail-sheet.tsx" "src/routes/pay/\$code/-checkout-view.tsx" src/server/proxy.ts src/lib/api/csrf.ts src/lib/auth/client.ts`
> and `git diff --stat 8b3c9e856..HEAD -- cli/internal/scaffold/env.go cli/cmd/init.go cli/cmd/login.go cli/npm/install.js mcp/src/http.ts mcp/src/tools.ts mcp/package.json` from the repo root.
> On any mismatch with a "Current state" excerpt, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: LOW
- **Depends on**: plan 002 owns the payment-link redirect schema guard (Task 5
  here only hardens the sink and must not duplicate 002). Task 8's backend half
  lives in this plan (plan 009 does not cover it).
- **Category**: security
- **Planned at**: frontend `ce2df9c`, root `8b3c9e856` (cli/, mcp/), 2026-07-28

## Why this matters

The sweep verified two HIGHs on the client/tooling surface — the MCP `--http`
transport is unauthenticated on all interfaces, and `reevit init` can inline a
**live** secret key into browser bundles — plus MEDIUMs with real exploit paths
(no security headers anywhere, `javascript:` hrefs in the admin console, an
unvalidated post-payment redirect, a weakened CSRF cookie architecture,
unverified release-binary downloads) and four LOWs. Gates: `npx tsc --noEmit` +
`pnpm test` (frontend), `go test ./...` + `golangci-lint run ./...` (cli),
`tsc -p .` + package test script (mcp).

## Tasks

### Task 1: Lock down the MCP HTTP transport (HIGH)

**Files:**
- Modify: `mcp/src/http.ts` (entire file, 68 lines)
- Modify: `mcp/src/index.ts:14-18` (pass host/token through from env/flags)
- Test: `mcp/src/http.test.ts` (create; check `mcp/package.json` for the test runner — if none exists, add `node --test` and wire `"test": "node --test dist/http.test.js"` after build, or colocate a vitest config matching the repo's other TS packages)

Current state (`mcp/src/http.ts:29-67`): `httpServer.listen(port, resolve)`
binds all interfaces; no request authentication; body buffered unbounded
(`:47-49`); log line even claims `localhost` while binding `::`.

- [ ] **Step 1: implement the secured server** — rewrite `serveHttp`:

```ts
const MAX_BODY_BYTES = 1_000_000; // 1MB
const LOOPBACK_HOSTS = new Set(["localhost", "127.0.0.1", "[::1]"]);

export interface ServeHttpOptions {
  host?: string;               // defaults to 127.0.0.1
  authToken?: string;          // required unless allowInsecureNoAuth
  allowInsecureNoAuth?: boolean; // explicit opt-out, logs a loud warning
}

export async function serveHttp(
  config: ReevitConfig,
  port: number,
  opts: ServeHttpOptions = {},
): Promise<void> {
  const host = opts.host ?? "127.0.0.1";
  if (!opts.authToken && !opts.allowInsecureNoAuth) {
    throw new Error(
      "reevit-mcp --http requires REEVIT_MCP_HTTP_TOKEN (or --allow-insecure-no-auth for local-only use)",
    );
  }

  const httpServer = createServer(async (req, res) => {
    if (!req.url?.startsWith("/mcp")) {
      res.writeHead(404).end();
      return;
    }

    // DNS-rebinding guard: only serve requests addressed at loopback.
    const reqHost = (req.headers.host ?? "").toLowerCase();
    const hostname = reqHost.replace(/:\d+$/, "");
    if (!LOOPBACK_HOSTS.has(hostname)) {
      res.writeHead(403, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "forbidden_host" }));
      return;
    }

    if (opts.authToken) {
      const expected = `Bearer ${opts.authToken}`;
      const got = req.headers.authorization ?? "";
      if (got.length !== expected.length || !timingSafeEqual(Buffer.from(got), Buffer.from(expected))) {
        res.writeHead(401, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: "unauthorized" }));
        return;
      }
    }

    try {
      const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
      const server = newServer(config);

      res.on("close", () => {
        void transport.close();
        void server.close();
      });

      await server.connect(transport);

      const chunks: Buffer[] = [];
      let size = 0;
      for await (const chunk of req) {
        size += (chunk as Buffer).length;
        if (size > MAX_BODY_BYTES) {
          res.writeHead(413, { "Content-Type": "application/json" });
          res.end(JSON.stringify({ error: "payload_too_large" }));
          return;
        }
        chunks.push(chunk as Buffer);
      }
      const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString()) : undefined;

      await transport.handleRequest(req, res, body);
    } catch (error) {
      if (!res.headersSent) {
        res.writeHead(500, { "Content-Type": "application/json" });
        res.end(
          JSON.stringify({
            jsonrpc: "2.0",
            error: { code: -32603, message: error instanceof Error ? error.message : "error" },
            id: null,
          }),
        );
      }
    }
  });

  await new Promise<void>((resolve) => httpServer.listen(port, host, resolve));
  console.error(`reevit-mcp listening on http://${host}:${port}/mcp (${config.mode} mode)`);
  if (opts.allowInsecureNoAuth) {
    console.error("WARNING: reevit-mcp --http is running WITHOUT authentication (loopback only).");
  }
}
```

  Add `import { timingSafeEqual } from "node:crypto";` at the top.
- [ ] **Step 2: wire the CLI entry** — in `mcp/src/index.ts` read
  `process.env.REEVIT_MCP_HTTP_TOKEN` and a `--allow-insecure-no-auth` flag and
  pass `{ authToken, allowInsecureNoAuth }` into `serveHttp`.
- [ ] **Step 3: tests** — (a) POST without `Authorization` → 401; (b) wrong
  token → 401; (c) correct token → reaches the MCP handler; (d) `Host:
  evil.example` → 403; (e) >1MB body → 413; (f) server is bound to 127.0.0.1
  (`server.address()`).
- [ ] **Step 4: gates** — `cd mcp && npm run build && npm test`.
- [ ] **Step 5: commit** — `git commit -am "fix(mcp): authenticate and localhost-bind the HTTP transport, cap body size"`

### Task 2: Refuse live keys in browser-exposed env vars (HIGH)

**Files:**
- Modify: `cli/internal/scaffold/env.go:54-83`
- Modify: `cli/cmd/init.go:140` (handle the new error)
- Test: `cli/internal/scaffold/env_test.go`

Current state (`env.go:73-77`): `WriteEnv` writes `cfg.APIKey` into
`NEXT_PUBLIC_REEVIT_KEY`/`VITE_REEVIT_KEY` with no mode check. Reevit API keys
are prefixed `pfk_live` / `pfk_test` (backend
`internal/services/auth/api_keys.go:26-28`; raw key form is `<keyID>.<secret>`,
and the keyID carries the prefix).

- [ ] **Step 1: failing tests** — `WriteEnv` with a `pfk_live_…` key and
  `includeClientKey=true` returns `ErrLiveKeyInClientEnv` and writes NO client
  var; with `pfk_test_…` it writes the var as today.

```go
func TestWriteEnv_RefusesLiveKeyInBrowserVar(t *testing.T) {
	proj := Project{Stack: StackReact, Root: t.TempDir()}
	_, err := WriteEnv(proj, "pfk_live_abc123.secretpart", "org_1", true)
	if !errors.Is(err, ErrLiveKeyInClientEnv) {
		t.Fatalf("want ErrLiveKeyInClientEnv, got %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(proj.Root, ".env.local"))
	if strings.Contains(string(raw), "VITE_REEVIT_KEY=") {
		t.Fatal("live key must never reach a browser-exposed var")
	}
}
```

- [ ] **Step 2: run, expect fail** — `go test ./internal/scaffold/ -run LiveKey -v`.
- [ ] **Step 3: implement** — in `env.go`:

```go
// ErrLiveKeyInClientEnv is returned when a live-mode secret key would be
// written to a browser-exposed env var (NEXT_PUBLIC_*/VITE_*), which the
// bundler inlines into the public JS bundle.
var ErrLiveKeyInClientEnv = errors.New("live API keys must never be written to browser-exposed env vars — use a test-mode key (pfk_test_…) for the client bundle")

// isLiveAPIKey reports whether the key is a live-mode Reevit secret key.
// The raw key form is "<keyID>.<secret>" and the keyID carries the prefix.
func isLiveAPIKey(key string) bool {
	return strings.HasPrefix(key, "pfk_live")
}
```

  and at the top of the `includeClientKey && clientVar != ""` block:

```go
	if includeClientKey && clientVar != "" {
		if isLiveAPIKey(apiKey) {
			return res, ErrLiveKeyInClientEnv
		}
		// ... existing write ...
	}
```

  In `cli/cmd/init.go:140`, on `errors.Is(err, ErrLiveKeyInClientEnv)` print the
  error text + "rerun with a test-mode key (`reevit login` mints one) or drop
  the checkout target" and exit non-zero — do not silently skip.
- [ ] **Step 4: gates** — `go test ./internal/scaffold/ ./cmd/ && golangci-lint run ./...`.
- [ ] **Step 5: commit** — `git commit -am "fix(cli): refuse live API keys in browser-exposed env vars"`

### Task 3: Security headers on `server.mjs` (MEDIUM)

**Files:**
- Modify: `frontend/server.mjs`
- Test: `frontend/e2e` or a node smoke script — a Playwright assertion on
  response headers for `/` and `/healthz` if the e2e setup supports it;
  otherwise a `scripts/check-headers.mjs` smoke script run against `pnpm start`

Current state (`server.mjs:22-26`): bare `srvx` static + SSR fetch, zero
header middleware; no CSP/frame-ancestors/HSTS/nosniff/Referrer-Policy
anywhere in the repo (grep-verified during the sweep).

- [ ] **Step 1: implement** — wrap the app fetch:

```js
const SECURITY_HEADERS = {
  "content-security-policy":
    "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
    "img-src 'self' data: https:; font-src 'self' data:; connect-src 'self' https://api.reevit.io; " +
    "frame-ancestors 'self'; base-uri 'self'; form-action 'self'",
  "x-content-type-options": "nosniff",
  "referrer-policy": "strict-origin-when-cross-origin",
};

function withSecurityHeaders(response, request) {
  const headers = new Headers(response.headers);
  for (const [k, v] of Object.entries(SECURITY_HEADERS)) headers.set(k, v);
  // HSTS only when terminated over HTTPS (behind Caddy/CF in prod).
  if (new URL(request.url).protocol === "https:" ||
      request.headers.get("x-forwarded-proto") === "https") {
    headers.set("strict-transport-security", "max-age=63072000; includeSubDomains; preload");
  }
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
}

function fetchWithHealth(request, ...rest) {
  if (new URL(request.url).pathname === "/healthz") {
    return new Response("ok", { status: 200, headers: { "content-type": "text/plain" } });
  }
  return Promise.resolve(server.fetch(request, ...rest)).then((r) => withSecurityHeaders(r, request));
}
```

  Note in a comment: `script-src 'unsafe-inline'` is required for the inline
  JSON-LD in `src/routes/__root.tsx`; tightening to nonces is a follow-up, not
  this plan.
- [ ] **Step 2: smoke check** — `pnpm build && pnpm start &`, then
  `curl -sI http://localhost:3000/ | grep -iE 'content-security-policy|x-content-type-options|referrer-policy'`
  expecting all three; `curl -sI http://localhost:3000/healthz` still 200.
- [ ] **Step 3: gates** — `npx tsc --noEmit && pnpm test`.
- [ ] **Step 4: commit** — `git commit -am "fix(frontend): add security headers middleware to node server"`

### Task 4: `safeHttpUrl` for user-controlled hrefs (MEDIUM)

**Files:**
- Create: `frontend/src/lib/url/safe-url.ts`
- Test: `frontend/src/lib/url/safe-url.test.ts`
- Modify: `frontend/src/routes/platform/kyc/-components/kyc-review-drawer.tsx` (~:309, ~:367)
- Modify: `frontend/src/routes/platform/organizations/-components/org-drawer.tsx` (~:174, ~:212)
- Modify: `frontend/src/routes/dashboard/payment-links/-components/payment-link-detail-sheet.tsx` (~:577)

- [ ] **Step 1: failing table test**:

```ts
import { describe, expect, it } from "vitest";
import { safeHttpUrl } from "./safe-url";

describe("safeHttpUrl", () => {
  it.each([
    ["https://example.com", "https://example.com"],
    ["http://example.com", "http://example.com"],
    ["  https://example.com  ", "https://example.com"],
    ["javascript:alert(1)", null],
    ["JaVaScRiPt:alert(1)", null],
    ["data:text/html,<script>1</script>", null],
    ["vbscript:x", null],
    ["//evil.com", null],
    ["\\\\evil.com", null],
    ["java\tscript:alert(1)", null],
    ["https://user:pw@example.com", "https://user:pw@example.com"],
    ["", null],
    [null, null],
    [undefined, null],
  ])("safeHttpUrl(%j)", (input, want) => {
    expect(safeHttpUrl(input as string)).toBe(want);
  });
});
```

- [ ] **Step 2: run, expect fail** — `pnpm vitest run src/lib/url/safe-url.test.ts`.
- [ ] **Step 3: implement**:

```ts
/**
 * Returns the trimmed URL only when it uses the http/https scheme, else null.
 * React does not sanitize `href` — user-controlled URLs (KYC website fields,
 * merchant org profiles) must pass through this before rendering as links.
 */
export function safeHttpUrl(input: string | null | undefined): string | null {
  if (!input) return null;
  const trimmed = input.trim();
  if (trimmed === "") return null;
  try {
    const url = new URL(trimmed);
    if (url.protocol !== "https:" && url.protocol !== "http:") return null;
    return trimmed;
  } catch {
    return null;
  }
}
```

- [ ] **Step 4: route the five sinks through it** — pattern per site (example
  from `kyc-review-drawer.tsx`):

```tsx
{submission.website && safeHttpUrl(submission.website) ? (
  <a href={safeHttpUrl(submission.website)!} target="_blank" rel="noopener noreferrer">
    {submission.website}
  </a>
) : submission.website ? (
  <span>{submission.website}</span> // unsafe scheme — render as text, never as a link
) : null}
```

  Compute `const websiteUrl = safeHttpUrl(submission.website)` once per render
  rather than calling twice. Then grep the whole `src/` for other raw
  user-controlled `href={` sites (`rg "href=\{(submission|org|link|customer|merchant)\." src/`)
  and fix any you find the same way.
- [ ] **Step 5: gates** — `npx tsc --noEmit && pnpm test`.
- [ ] **Step 6: commit** — `git commit -am "fix(frontend): scheme-allowlist user-controlled URLs before rendering as links"`

### Task 5: Harden the `/pay/[code]` redirect sink (MEDIUM)

**Files:**
- Modify: `frontend/src/routes/pay/$code/-checkout-view.tsx:277-283`

Current state: `setTimeout(() => (window.location.href = link.redirect_url!), 3000)`.
Plan 002 owns the schema-level guard (zod scheme validation on create/edit) and
is still TODO — this task hardens the sink independently; do not duplicate 002.

- [ ] **Step 1: implement** — reuse Task 4's helper:

```tsx
const completeSuccess = useCallback(() => {
  setCheckoutOutcome("success");
  setIsCheckoutOpen(false);
  const target = safeHttpUrl(link.redirect_url);
  if (target) {
    setTimeout(() => (window.location.href = target), 3000);
  }
  // Unsafe/missing scheme: stay on the success screen — plan 002 owns the
  // create/edit-time validation that prevents such values being stored.
}, [link.redirect_url]);
```

- [ ] **Step 2: gates** — `npx tsc --noEmit && pnpm test`.
- [ ] **Step 3: commit** — `git commit -am "fix(frontend): validate payment-link redirect scheme at the navigation sink"`

### Task 6: Tighten the session/CSRF cookie architecture (MEDIUM)

**Files:**
- Modify: `frontend/src/server/proxy.ts:48-101` (`rewriteCookie`, `deriveCookiePolicy`)
- Test: colocated proxy tests (find the existing proxy test file; if none,
  create `src/server/proxy.test.ts` with the rewrite-cookie cases)

Current state (`proxy.ts:53-64`): every upstream cookie — session AND
`reevit_csrf` — is rewritten to `Domain=.reevit.io; SameSite=None; Secure` on
any `*.reevit.io` host. The CSRF token is JS-readable (`src/lib/api/csrf.ts`
reads `document.cookie`), so one compromised sibling subdomain can read the
token and ride the `SameSite=None` session.

- [ ] **Step 1: read first** — how `deriveCookiePolicy` decides `sharedDomain`,
  and grep the backend for whether any flow genuinely needs cross-site session
  cookies (OAuth callback returns — check `backend/adapters/http` OAuth handler
  cookie expectations and the frontend OAuth callback route). Write down which
  flows need `SameSite=None` before editing.
- [ ] **Step 2: implement the tightening**:

```ts
// In rewriteCookie, split the policy by cookie name:
const isCsrf = /^reevit_csrf=/i.test(cookie);
// CSRF cookie: host-scoped (no Domain attribute) so sibling subdomains cannot
// read the double-submit token.
// Session cookie: SameSite=Lax unless the request path needs cross-site
// (verified in Step 1 — enumerate the exact paths, e.g. OAuth callback).
if (isCsrf) {
  // drop `; Domain=...` entirely
} else if (needsCrossSite(requestPath)) {
  rewritten += "; SameSite=None";
} else {
  rewritten = rewritten.replace(/;\s*samesite=[^;]*/gi, "; SameSite=Lax");
}
```

  (Implement against the real function bodies — keep `Secure` always on in
  production policy.)
- [ ] **Step 3: tests** — session cookie on `/dashboard` → `SameSite=Lax`, no
  relaxation; `reevit_csrf` → no `Domain` attribute; OAuth callback path keeps
  `SameSite=None`; non-reevit.io hosts unchanged.
- [ ] **Step 4: manual verification** — login, magic-link verify, OAuth login,
  dashboard mutation (CSRF header flow) all still work against the dev backend.
- [ ] **Step 5: gates** — `npx tsc --noEmit && pnpm test`.
- [ ] **Step 6: commit** — `git commit -am "fix(frontend): host-scope CSRF cookie and tighten session SameSite policy"`

### Task 7: Verify checksums in the npm CLI installer (MEDIUM)

**Files:**
- Modify: `cli/npm/install.js`
- Test: `cli/npm/install.test.js` if a test runner exists for the npm wrapper
  (check `cli/npm/package.json`); otherwise add a `--selftest` path and run it
  as the verification step

Current state (`install.js:48-53`): downloads the release tarball and writes
the binary `0o755` with no integrity check. Goreleaser already publishes
`checksums.txt` (`cli/.goreleaser.yaml`).

- [ ] **Step 1: implement** — after downloading the archive:

```js
const crypto = require("node:crypto");

async function fetchChecksums(version) {
  const url = `https://github.com/Reevit-Platform/cli/releases/download/v${version}/checksums.txt`;
  const text = (await download(url)).toString("utf8");
  const map = new Map();
  for (const line of text.split("\n")) {
    const m = line.trim().match(/^([0-9a-f]{64})\s+(\S+)$/);
    if (m) map.set(m[2], m[1]);
  }
  return map;
}

// in the install flow, after `const gz = await download(url);`
const artifact = path.basename(url);
const checksums = await fetchChecksums(version);
const expected = checksums.get(artifact);
if (!expected) throw new Error(`no checksum published for ${artifact}`);
const actual = crypto.createHash("sha256").update(gz).digest("hex");
if (actual !== expected) {
  throw new Error(`checksum mismatch for ${artifact}: expected ${expected}, got ${actual} — refusing to install`);
}
```

- [ ] **Step 2: verify** — `npm install` the package from a clean cache
  succeeds; tamper test: point the installer at a local fixture server serving
  a modified tarball and confirm it refuses.
- [ ] **Step 3: commit** — `git commit -am "fix(cli): verify SHA-256 checksums for release binaries in npm installer"`

### Task 8: Move magic-link verification to POST (LOW)

**Files:**
- Modify: `backend/adapters/http/handlers_auth.go` (add `verifyMagicLinkPOST`)
- Modify: `backend/adapters/http/router.go` (route the POST next to the GET)
- Modify: `frontend/src/lib/auth/client.ts:136-149`
- Modify: `frontend/src/routes/auth/index.tsx` (~:331 — strip `?token=` after verify)

Current state (`client.ts:141-147`): `GET /api/v1/auth/verify?token=…` puts the
one-time token in access logs and browser history.

- [ ] **Step 1: backend** — add `POST /auth/verify` accepting
  `{"token": "…", "code": "…", "remember_me": bool}` and calling the same
  `VerifyMagicLink` service path as the GET handler (keep GET for backward
  compat with already-emailed links). Backend test: POST with valid token
  returns the session response; invalid token returns the same error shape as
  GET. Rate limit: confirm the POST path lands inside the existing
  `/auth/verify` rate-limit branch (it matches by path prefix — verify in
  `middleware_rate_limit.go`).
- [ ] **Step 2: frontend client** — switch to `this.api.post("/auth/verify", { token, code, remember_me })`.
- [ ] **Step 3: strip the URL** — after a successful verify in
  `routes/auth/index.tsx`, `router.replace({ to: "/auth", search: {} })` (match
  the route's real navigation API) before redirecting onward, so the token
  doesn't linger in history.
- [ ] **Step 4: gates** — backend: `go test ./adapters/http/ -run Verify -v`;
  frontend: `npx tsc --noEmit && pnpm test`.
- [ ] **Step 5: commits** — two commits: `feat(backend): accept magic-link verification via POST` then
  `fix(frontend): verify magic links via POST and strip token from URL`.

### Task 9: `reevit login --key` without shell-history exposure (LOW)

**Files:**
- Modify: `cli/cmd/login.go:95-97`
- Modify: `cli/cmd/init.go` (~:488 — the hint that says `reevit login --key <live_key>`)
- Test: `cli/cmd/login_test.go`

- [ ] **Step 1: implement** — support `-` as stdin:

```go
loginCmd.Flags().StringVar(&loginKey, "key", "", "API key (use '-' to read from stdin; prefer the browser flow)")
```

  In the run function:

```go
	if loginKey == "-" {
		fmt.Fprint(os.Stderr, "API key: ")
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return fmt.Errorf("read key from stdin: %w", err)
		}
		loginKey = strings.TrimSpace(string(b))
	}
```

  and update the init.go hint to `reevit login --key -` (paste at the prompt).
- [ ] **Step 2: test** — stdin path populates the key; empty stdin errors.
- [ ] **Step 3: gates** — `go test ./cmd/ && golangci-lint run ./...`.
- [ ] **Step 4: commit** — `git commit -am "fix(cli): support reading login API key from stdin"`

### Task 10: Scaffolded env files created 0600 (LOW)

**Files:**
- Modify: `cli/internal/scaffold/env.go:167-177`
- Test: `cli/internal/scaffold/env_test.go`

- [ ] **Step 1: failing test** — after `WriteEnv`, `stat .env.local` → mode
  `0600` (and a pre-existing `0644` file gets tightened).

```go
	info, err := os.Stat(filepath.Join(proj.Root, ".env.local"))
	if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want 0600, got %o", info.Mode().Perm())
	}
```

- [ ] **Step 2: run, expect fail**.
- [ ] **Step 3: implement**:

```go
func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Tighten pre-existing broader files (e.g. a framework-created 0644 env
	// file) now that they hold a secret.
	if info, statErr := f.Stat(); statErr == nil && info.Mode().Perm() != 0o600 {
		_ = os.Chmod(path, 0o600)
	}

	_, err = f.WriteString(content)

	return err
}
```

- [ ] **Step 4: gates** — `go test ./internal/scaffold/ && golangci-lint run ./...`.
- [ ] **Step 5: commit** — `git commit -am "fix(cli): create scaffolded env files with 0600 permissions"`

### Task 11: Clear the npm advisories (dep)

**Files:**
- Modify: `frontend/package.json` (+ `pnpm-lock.yaml`)

- [ ] **Step 1: identify paths** — `pnpm why brace-expansion @hono/node-server`.
- [ ] **Step 2: fix** — if direct: `pnpm update <pkg>`. If transitive (likely):
  add to `package.json`:

```json
  "pnpm": {
    "overrides": {
      "brace-expansion": "^5.0.1",
      "@hono/node-server": "^1.19.9"
    }
  }
```

  (Check `pnpm audit` output for the exact patched versions and use those.)
  Then `pnpm install` to re-lock.
- [ ] **Step 3: verify** — `pnpm audit` shows 0 high/critical;
  `npx tsc --noEmit && pnpm test && pnpm build` all green (overrides can shift
  transitive behavior — the build is the gate).
- [ ] **Step 4: commit** — `git commit -am "chore(frontend): override vulnerable transitive deps (brace-expansion, hono node-server)"`

## STOP conditions

- Any "Current state" excerpt mismatches the live file → re-verify that finding
  against the drifted code; if already fixed, skip and note.
- Task 6: if Step 1's read shows a cross-site flow you cannot enumerate with
  confidence → implement ONLY the CSRF-cookie host-scoping half and record the
  SameSite question in the commit message for a follow-up.
- Task 11: if an override breaks `pnpm build` and the fix isn't obvious → drop
  that override, keep the other, and report.
- `frontend/CLAUDE.md` requires `npx tsc --noEmit` clean before every commit —
  a type error from your change is a STOP.

## Verification (end of plan)

- frontend: `npx tsc --noEmit`, `pnpm test`, `pnpm build` green; `pnpm audit`
  0 high/critical; `curl -sI localhost:3000/` shows the new headers.
- cli: `go test ./...`, `golangci-lint run ./...` green; `reevit init` with a
  `pfk_live` key refuses the browser var; scaffolded `.env.local` is 0600.
- mcp: `npm run build && npm test` green; unauthenticated POST to `/mcp` → 401;
  `Host: evil.com` → 403; >1MB → 413; bound to 127.0.0.1.
