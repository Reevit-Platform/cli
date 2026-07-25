# Slice 1 — `reevit login` browser pairing flow

Goal: `reevit login` (and later `reevit init`) opens the dashboard in a browser,
the user approves the pairing, the backend mints a **test-mode** scoped API key,
and the CLI receives it — no copy-pasting keys. Test-mode only by design; the
CLI ends with an explicit "you're on a test key — swap to live when ready" notice.

Pattern: Stripe-style **poll-based pairing** (no localhost callback server).
Works headless/SSH, no ports, the key never transits a redirect URL.

---

## Flow overview

```
CLI                                Backend                          Browser (dashboard)
 │ POST /v1/cli/auth                 │                                  │
 │  {device_name}                    │ create pairing session           │
 │ ← {id, pairing_code,              │  (pending, TTL 10m)              │
 │    poll_secret, browser_url}      │                                  │
 │ print code, open browser ─────────┼─────────────────────────────────▶│ /cli/confirm?code=XXXX-XXXX
 │                                   │                                  │ (session auth via existing
 │ poll GET /v1/cli/auth/{id} ······▶│                                  │  /auth?redirect= round trip)
 │ ← {status:"pending"} (every 5s)   │                                  │
 │                                   │◀── POST confirm (session+CSRF) ──│ user checks code matches,
 │                                   │    status → approved,            │ picks org, clicks Approve
 │                                   │    org_id + user_id recorded     │
 │ poll GET /v1/cli/auth/{id} ······▶│ mint test key NOW (at claim),    │ "Return to your terminal"
 │ ← {status:"approved",             │ status → consumed                │
 │    api_key:{raw,…}, org:{…}}      │                                  │
 │ save config, print success        │                                  │
 │ + test-mode notice                │                                  │
```

**Key design decision — mint at claim, not at confirm.** The confirm endpoint
only records approval (org, user, scopes). The API key is minted inside the
*poll/claim* handler the moment the CLI collects it. Consequences:

- The raw key is **never persisted anywhere** — not even vault-encrypted. It
  exists only in the single HTTP response to the CLI. (Mint-at-confirm would
  force us to store the raw key encrypted until the CLI picks it up.)
- The browser **never sees the key at all** — better than the dashboard's
  one-time reveal. The confirm page just says "return to your terminal".
- Failure mode: if the claim response is lost mid-flight the session is already
  consumed; the user just reruns `reevit login`. Acceptable for v1.

**Anti-phishing:** the pairing code is shown in the terminal AND on the confirm
page; copy explicitly asks "does this match your terminal?". A victim of a
forwarded link sees a code that doesn't match anything they typed.

---

## 1. Backend (PR against `dev`)

Follow the api-keys feature shape exactly (handler → ports → usecase → repo →
router.Options → cmd/api/main.go). All references below verified against the
current tree.

### 1.1 Migration — `db/migrations/000139_create_cli_auth_sessions.sql`

Latest migration is `000138_add_dunning_claimed_at.sql`; phantom-goose rule
(≥000130) satisfied. goose Up/Down annotations mandatory.

```sql
CREATE TABLE cli_auth_sessions (
    id               TEXT PRIMARY KEY DEFAULT ulid_generate(),
    pairing_code     TEXT NOT NULL UNIQUE,        -- e.g. GX7M-4KP9 (Crockford base32, no 0/1/O/I)
    poll_secret_hash TEXT NOT NULL,               -- SHA-256 hex of the CLI's poll secret
    status           TEXT NOT NULL DEFAULT 'pending',  -- pending|approved|consumed|denied|expired
    device_name      TEXT NOT NULL DEFAULT '',    -- CLI-supplied hostname, for key naming
    requested_scopes TEXT[] NOT NULL DEFAULT '{}',
    org_id           TEXT REFERENCES organizations(id),  -- set on approve
    approved_by      TEXT,                        -- user id, set on approve (audit actor)
    api_key_id       TEXT,                        -- set on claim
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,        -- created_at + 10 min
    approved_at      TIMESTAMPTZ,
    claimed_at       TIMESTAMPTZ
);
CREATE INDEX idx_cli_auth_sessions_pairing_code ON cli_auth_sessions (pairing_code);
```

- No raw key column — see mint-at-claim decision.
- Validate on a **fresh DB** via compose migrate (per memory: shared test DB
  silently skips; use `--entrypoint` so `-e` lands).
- Queries in `db/queries/cli_auth.sql`, regenerate sqlc.

### 1.2 Ports — `internal/ports/cliauth.go` (new)

`CLIAuthSession` struct mirroring the table; `CLIAuthStore` interface
(Create, GetByID, GetByPairingCode, Approve, MarkConsumed, Deny, ExpireStale);
sentinel errors (`ErrPairingNotFound`, `ErrPairingExpired`,
`ErrPairingAlreadyUsed`, `ErrPollSecretMismatch`).

### 1.3 Usecase — `internal/usecase/cliauth/service.go` (new)

Constructor takes `CLIAuthStore`, `ports.APIKeyService` (the existing
`internal/services/auth` Service satisfies it), and a clock.

- `Start(ctx, deviceName, scopes)` → generates pairing code (8 chars Crockford
  base32, grouped `XXXX-XXXX`, uniqueness-retried) + 32-byte poll secret
  (returned raw once; only SHA-256 stored). Returns id, code, secret, expiry.
- `Approve(ctx, pairingCode, orgID, userID)` → guards status==pending and not
  expired; records org/user/approved_at. Idempotent re-approve by the same
  session is a no-op success (supports idempotency middleware retries).
- `Claim(ctx, id, pollSecret)` → constant-time hash compare
  (`crypto/subtle`); status pending → return pending; approved → call
  `apiKeys.IssueAPIKey(ctx, orgID, name, scopes, "test")`
  (`internal/services/auth/api_keys.go:87` — test keys skip the KYC gate,
  live requires approved KYC, which is exactly the test-only rationale),
  set api_key_id + claimed_at, status → consumed, return the `IssuedAPIKey`
  (raw = `pfk_test_<hex>.<secret>`). consumed/denied/expired → terminal error.
- Key name: `CLI (<device_name>)`, truncated sanely.
- Default scopes (when CLI sends none): the set the CLI commands need today —
  `payments:read`, `payments:write`, `webhooks:read`, plus the events-stream
  scope `reevit listen` uses. Confirm exact slugs against the `SCOPES` list in
  frontend `types/api-keys.ts` / backend scope constants during implementation.

### 1.4 Handlers — `adapters/http/handlers_cli_auth.go` (new)

**Public (CLI-facing), imitate the `/auth/*` block at router.go:410-430 —
no auth, wrapped in `authRateLimit` (`NewPublicAuthRateLimiter`):**

- `POST /v1/cli/auth` — body `{device_name, scopes?}`. 201 →
  `{id, pairing_code, poll_secret, browser_url, expires_in, interval}` where
  `browser_url` = `<dashboard base>/cli/confirm?code=<pairing_code>` (dashboard
  base from router config/env) and `interval` = 5 (seconds).
- `GET /v1/cli/auth/{id}` — poll secret via `X-Reevit-Poll-Secret` header
  (never a query param — keeps it out of access logs). Responses:
  `{status:"pending"}` · `{status:"approved", api_key:{id,raw,name,scopes,
  mode:"test"}, org:{id,name}}` (single claim; see usecase) ·
  `{status:"denied"|"expired"}` · 429 with `Retry-After` when polled faster
  than `interval`. 404 on unknown id / bad secret (don't distinguish).

**Dashboard (session-facing), imitate the `/api-keys/session` chain at
router.go:669-679 — `SessionAuth → OrgScope → RequireAdmin → CSRFMiddleware`:**

- `GET /v1/cli/auth/session/{pairing_code}` — details for the confirm page:
  `{pairing_code, device_name, requested_scopes, created_at, expires_at,
  status}`. Session + membership is enough for read; keep the full admin chain
  anyway for simplicity and symmetry.
- `POST /v1/cli/auth/session/{pairing_code}/confirm` — with
  `idempotencyMiddleware`. Approves for the org in `X-Org-Id` (OrgScope) with
  actor from session. 200 → `{status:"approved"}`.
- `POST /v1/cli/auth/session/{pairing_code}/deny` — courtesy endpoint.

**Audit/events:** the mint happens outside `handlers_api_keys.go`, so the
`api_key.created` audit row + outbound webhook do NOT fire automatically.
Extract/replicate `recordAuditAndEvent` (handlers_api_keys.go:764-809 —
fire-and-forget, `context.Background()` + 5s timeout, never fails the op) so
the claim path emits `api_key.created` with actor = `approved_by` user id and
metadata `{source:"cli_pairing", device_name}`. Optionally also a
`cli.pairing_approved` audit action on confirm.

### 1.5 Repo + wiring

- `adapters/repo/cli_auth_repo.go` mapping ports ↔ sqlc.
- `RouterOptions`: add `CLIAuth CLIAuthService` + `DashboardBaseURL string`
  (or reuse an existing frontend-origin config if one exists); conditional
  registration like the other features.
- `cmd/api/main.go`: construct repo + usecase (inject the existing
  `apiKeyService`), pass in options. **Shared-checkout warning applies:
  re-grep router.go and cmd mains for stale buffers before committing.**
- Expiry: `Claim`/`Approve` check `expires_at` inline (lazy expiry); no
  worker/cron needed for v1. Optional: a periodic cleanup delete for hygiene.

### 1.6 Tests & gates

- Usecase tests: full status machine (pending→approved→consumed, deny, expiry,
  wrong secret constant-time path, double-claim, re-approve idempotency).
- Handler tests: rate-limit contract, 404-on-bad-secret, poll cadence 429.
- `golangci-lint run ./...` (0 issues) + `go test ./...` with
  `TEST_DATABASE_URL=postgres://reevit:reevit@localhost:5433/reevit_test?sslmode=disable`.
- Mocks via mockery per `.mockery.yaml` for the new ports.

---

## 2. Frontend (PR against `dev`)

### 2.1 Page — `app/(sass)/dashboard/cli/confirm/page.tsx`

- Lives under `/dashboard` so the middleware (`proxy.ts:50-57`) gives the
  server-side session gate + `/auth?redirect=<url>` round trip for free —
  magic-link login returns the user to the confirm page with the `code` query
  param intact (`getSafeRedirect` already allows it).
- **Keep `OnboardingGuard`** (do NOT bypass): the backend requires an org +
  admin membership to approve, so an un-onboarded user *should* be bounced to
  `/onboarding?redirect=<confirm url>` and returned after — the guard's
  existing behavior is exactly the right UX.
- Avoid the sidebar/header shell: nested route group
  `app/(sass)/dashboard/(standalone)/cli/confirm/` with a lightweight
  `layout.tsx` (centered flex + logo, modeled on `app/(sass)/auth/layout.tsx`),
  or extend the workflow-builder special-case in `dashboard/layout.tsx:52-60`.
  Prefer the route group — no regex additions to the shared layout.

### 2.2 Content (centered `PanelCard`, imitate `auth/login/page.tsx`)

1. Header: "Authorize the Reevit CLI".
2. The pairing code, huge, **`font-mono tabular-nums`** (gratoClassic digits
   are illegible otherwise), with copy: *"Check that this code matches the one
   shown in your terminal."*
3. Device name + requested-at from
   `GET /api/v1/cli/auth/session/{code}`.
4. Org picker fed by `useAuthContext().memberships` filtered to
   `role ∈ {admin, owner}`, defaulting to `activeOrganization`. Selecting sets
   the `X-Org-Id` header on confirm (do NOT switch the global org cookie).
5. A quiet test-mode badge: "This creates a **test-mode** API key. Live keys
   are created in Developers → API keys."
6. Approve / Deny buttons → success state: "Done — return to your terminal."
   The key is never displayed in the browser.
7. Error states: expired (offer "run `reevit login` again"), already used,
   not found.

### 2.3 Client plumbing

- `lib/api/cli.ts`: axios client cloned from `lib/api/api-keys.ts`
  (`baseURL:"/api/v1"`, `withCredentials`, `attachCSRF` interceptor —
  supplies `X-CSRF-Token` + `X-Reevit-Mode`), `X-Org-Id` from the picker,
  `Idempotency-Key` on confirm.
- `hooks/use-cli-pairing.ts`: `useQuery` for session details,
  `useMutation` for confirm/deny.
- Verification: local Playwright is broken — verify by source cross-check +
  push and watch CI (per memory). Manual check via the dev server + magic-link
  (Resend key works locally; recreate api/worker if .env changes).

---

## 3. CLI (PR on Reevit-Platform/cli)

### 3.1 `reevit login` — browser flow becomes the default

New file `cli/cmd/login_browser.go` (keep `login.go`'s `--key` path as
fallback; add `--no-browser` to print the URL instead of opening it).

1. Unauthenticated client call (the current `api.Client` always sends
   `X-Reevit-Key`; add a small public-request helper or make the header
   conditional) → `POST /v1/cli/auth` with `device_name = os.Hostname()`.
2. Print:
   ```
   Your pairing code is  GX7M-4KP9
   Press Enter to open   https://dashboard.reevit.io/cli/confirm?code=GX7M-4KP9
   ```
   Open via `open`/`xdg-open`/`rundll32` per GOOS; on failure just print the URL.
3. Poll `GET /v1/cli/auth/{id}` with `X-Reevit-Poll-Secret`, honoring
   `interval` and `Retry-After` on 429, spinner in between, ctx-cancellable
   (Ctrl-C aborts cleanly). Timeout at session expiry with a clear message.
4. On approved: `config.Save({APIKey: raw, Mode: "test"})` (BaseURL untouched;
   `REEVIT_API_URL` env override already exists for dev). Reuse `login.go`'s
   verify-probe against `/payments` for a sanity check.
5. Success output — **the agreed test-mode notice**:
   ```
   ✔ Logged in to <Org Name> as CLI (<hostname>)
   ✔ Saved to ~/.config/reevit/config.json (test mode)

   Heads up: this is a TEST-MODE key — perfect for reevit listen / trigger and
   integrating safely. When you're ready for live traffic, create a live key in
   Dashboard → Developers → API keys, then run:  reevit login --key <live_key>
   ```

### 3.2 Nice follow-through

`root.go`'s `client()` "no API key configured" error should now suggest the
browser flow first: ``run `reevit login` (opens your browser) or set
REEVIT_API_KEY``.

---

## 4. Security summary

- Poll secret: 32 random bytes, stored only as SHA-256, compared with
  `crypto/subtle`; transmitted only in a header, never a URL.
- Raw API key: never persisted, never shown in the browser, delivered exactly
  once to the poll holder.
- Pairing session: 10-min TTL, single-use, IP rate-limited creation + polling,
  404 indistinguishable between bad id and bad secret.
- Code-matching UX defends against forwarded-link phishing.
- Test-mode only ⇒ no KYC bypass surface, minimal blast radius; scopes limited
  to what the CLI commands actually use.
- CSRF + admin role + idempotency on the approving endpoint (existing chain).

## 5. Ship order

1. Backend PR (migration + endpoints) — mergeable alone, nothing user-visible.
2. Frontend confirm page — mergeable once backend is on dev.
3. CLI release (goreleaser tag flow) — ship after 1+2 are deployed; old CLIs
   keep working via `--key`.

All PRs base `dev`, not `main`.
