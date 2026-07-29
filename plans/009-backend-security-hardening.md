# Plan 009: Backend security hardening (OWASP sweep 2026-07-28)

> **Executor instructions**: Follow this plan task by task, in order. Run every
> verification command and confirm the expected result before moving on. Each
> task ends in a commit — do not batch tasks into one commit. If anything in
> "STOP conditions" occurs, stop and report — do not improvise. Do **not**
> update `plans/README.md`; the orchestrator maintains the index.
>
> **Drift check (run first)**: in `backend/`, run
> `git diff --stat 251107e6..HEAD -- adapters/http/handlers_delivery_webhooks.go adapters/webhook/flutterwave.go adapters/webhook/paystack.go adapters/webhook/monnify.go adapters/http/handlers_billing_paystack.go internal/usecase/payments/service.go internal/usecase/payments/service_hubtel.go internal/usecase/payments/checkout_sessions.go internal/usecase/payments/service_dispute_webhook.go adapters/http/router.go adapters/http/handlers_webhooks.go adapters/http/middleware_2fa_required.go adapters/http/middleware_rate_limit.go internal/services/auth/rate_limiter.go internal/services/auth/redis_rate_limiter.go internal/services/auth/users.go internal/usecase/exports/service.go internal/infra/config/config.go internal/ports/auth.go go.mod`
> If any in-scope file changed, compare the "Current state" excerpts against the
> live code; on a mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: LOW (each task is small, gated by full test suite)
- **Depends on**: none. Plan 008 (secrets rotation) runs in parallel operationally.
- **Category**: security
- **Planned at**: backend commit `251107e6`, 2026-07-28

## Why this matters

The 2026-07-28 OWASP sweep verified 15 backend findings against code: one HIGH
fail-open webhook, a cluster of non-constant-time secret comparisons, a
deterministic payment `client_secret`, a merchant-reachable ledger-integrity
override, and eight LOWs (access-control gaps, fail-open defaults, log
hygiene), plus a reachable gRPC CVE. All fixes here are small and individually
committable; the gates (`golangci-lint run ./...` = 0 issues, `go test ./...`
green) run after every task.

## Current state

Verified 2026-07-28 at `251107e6`. Key excerpts are inside each task.

## Tasks

### Task 1: Fail closed on the Sail Rides webhook secret

**Files:**
- Modify: `adapters/http/handlers_delivery_webhooks.go:90-100`
- Modify: `internal/infra/config/config.go:445-508` (`Validate()`)
- Test: `adapters/http/handlers_delivery_webhooks_test.go` (create if absent)
- Test: `internal/infra/config/config_test.go`

Current state (`handlers_delivery_webhooks.go:90-100`) — verification only runs
when the secret is set, and `SAIL_RIDES_WEBHOOK_SECRET` defaults to `""`
(`config.go:182`, `envDefault:""`):

```go
if h.webhookSecret != "" {
	sig := r.Header.Get("X-Sail-Signature")
	ts := r.Header.Get("X-Sail-Timestamp")
	if err := verifySailSignature(body, sig, ts, h.webhookSecret); err != nil {
		...
	}
}
```

- [ ] **Step 1: failing tests** — handler test: construct `NewDeliveryWebhookHandler`
  with an empty secret and POST a syntactically valid payload; expect `401
  webhook_auth_not_configured` (not a delivery update). Config test mirroring the
  existing `METRICS_AUTH_TOKEN` cases: `AppEnv: "production"` +
  `SailRidesWebhookSecret: ""` → `Validate()` error contains
  `SAIL_RIDES_WEBHOOK_SECRET`.

```go
func TestDeliveryWebhook_FailsClosedWhenSecretUnset(t *testing.T) {
	h := NewDeliveryWebhookHandler(fakeDeliveryRepo(), nil, "", testLogger(t))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/deliveries/sailrides",
		strings.NewReader(`{"event":"status","data":{"deliveryId":"sd_1","status":"delivered"}}`))
	rec := httptest.NewRecorder()
	h.handle(rec, req) // adjust if handle is wrapped; call the exported route handler in that case
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: run, expect fail** — `go test ./adapters/http/ -run TestDeliveryWebhook_FailsClosed -v`
  Expected: FAIL (today the request passes through with no auth).
- [ ] **Step 3: implement** — replace the conditional with fail-closed:

```go
if h.webhookSecret == "" {
	// Fail closed: an unauthenticated webhook endpoint must never mutate state.
	h.log("webhook rejected: SAIL_RIDES_WEBHOOK_SECRET not configured")
	writeError(w, http.StatusUnauthorized, "webhook_auth_not_configured", "webhook authentication is not configured")
	return
}

sig := r.Header.Get("X-Sail-Signature")
ts := r.Header.Get("X-Sail-Timestamp")

if err := verifySailSignature(body, sig, ts, h.webhookSecret); err != nil {
	h.log("signature verification failed", "error", err)
	writeError(w, http.StatusUnauthorized, "invalid_signature", "webhook signature verification failed")
	return
}
```

  and in `config.go` `Validate()`, after the `MetricsAuthToken` block:

```go
	// The Sail Rides inbound webhook fails closed when unconfigured; requiring
	// the secret in production makes that an explicit boot failure instead of a
	// silent 401 for every delivery update.
	if strings.TrimSpace(c.SailRidesWebhookSecret) == "" {
		missing = append(missing, "SAIL_RIDES_WEBHOOK_SECRET")
	}
```

- [ ] **Step 4: run, expect pass** — same two test commands; then `go test ./adapters/http/ ./internal/infra/config/`.
- [ ] **Step 5: ops note** — append to `backend/.env.example` near the webhook
  section (if a `SAIL_RIDES_WEBHOOK_SECRET=` line already exists there, skip):
  `SAIL_RIDES_WEBHOOK_SECRET=  # required in production — webhook 401s without it`.
- [ ] **Step 6: commit** — `git add -A && git commit -m "fix(backend): fail closed on unset Sail Rides webhook secret"`

### Task 2: Constant-time Flutterwave webhook secret compare

**Files:**
- Modify: `adapters/webhook/flutterwave.go:156-163`
- Test: `adapters/webhook/flutterwave_test.go`

Current state (`flutterwave.go:161`): `if header != secret` on the reusable
static `verif-hash`.

- [ ] **Step 1: failing test** — table test asserting valid header accepted,
  wrong header rejected (guard against behavior drift from the compare swap):

```go
func TestFlutterwaveVerify_RejectsMismatchedSecret(t *testing.T) {
	err := verifyFlutterwaveSignature("wrong-hash", map[string]any{"secret_hash": "real-hash"})
	if err == nil {
		t.Fatal("want mismatch error")
	}
}
```

  (Match the real helper name/signature — read it at `flutterwave.go:~150`.)
- [ ] **Step 2: run, expect pass** (behavior-preserving change; test pins it).
- [ ] **Step 3: implement** — ensure `crypto/hmac` is imported, then:

```go
	if !hmac.Equal([]byte(header), []byte(secret)) {
		return fmt.Errorf("flutterwave signature mismatch")
	}
```

- [ ] **Step 4: gates** — `go test ./adapters/webhook/ && golangci-lint run ./adapters/webhook/`.
- [ ] **Step 5: commit** — `git commit -am "fix(backend): constant-time compare for Flutterwave webhook secret"`

### Task 3: Replace `EqualFold` HMAC compares (paystack, monnify, billing)

**Files:**
- Modify: `adapters/webhook/paystack.go:265-271`
- Modify: `adapters/webhook/monnify.go:~138`
- Modify: `adapters/http/handlers_billing_paystack.go:~451`
- Test: `adapters/webhook/paystack_test.go`, `adapters/webhook/monnify_test.go`

Current state (`paystack.go:265-271`):

```go
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !strings.EqualFold(sig, expected) {
		return fmt.Errorf("paystack signature mismatch")
	}
```

- [ ] **Step 1: pin behavior with tests** — valid signature accepted; one-char-off
  hex rejected (both cases already covered if tests exist — extend the table).
- [ ] **Step 2: implement all three sites** identically (mirror
  `adapters/webhook/paystack_dispute.go:144`, already correct):

```go
	if len(sig) != len(expected) || !hmac.Equal([]byte(strings.ToLower(sig)), []byte(expected)) {
		return fmt.Errorf("paystack signature mismatch")
	}
```

  Use the same shape at `monnify.go:138` and `handlers_billing_paystack.go:451`
  (adjust the error text to each provider).
- [ ] **Step 3: gates** — `go test ./adapters/webhook/ ./adapters/http/ && golangci-lint run ./adapters/...`.
- [ ] **Step 4: commit** — `git commit -am "fix(backend): constant-time HMAC compare for paystack/monnify/billing webhooks"`

### Task 4: Random payment `client_secret` (both generation sites)

**Files:**
- Modify: `internal/usecase/payments/service.go:1135` and `:1221-1224`
- Test: `internal/usecase/payments/service_test.go` (or the intent-creation test file)

Current state — TWO sites mint the secret from the ID (`"pf_"` is also the
tx_ref convention for Monnify/Flutterwave — do not reuse it):

```go
// :1135 (draft payment)
ClientSecret:   "pf_" + paymentID,
// :1221-1224 (fallback when the PSP returns none)
clientSecret := providerResp.ClientSecret
if clientSecret == "" {
	clientSecret = "pf_" + paymentID
}
```

- [ ] **Step 1: failing test** — create two intents; assert each `ClientSecret`
  is non-empty, does NOT contain the payment ID, differs between payments, and
  is at least 24 chars of random material after any prefix.
- [ ] **Step 2: run, expect fail** — `go test ./internal/usecase/payments/ -run ClientSecret -v`.
- [ ] **Step 3: implement** — add to `service.go` (imports `crypto/rand`, `encoding/hex`):

```go
// newClientSecret mints an unguessable bearer secret for the public payment
// endpoints. It must stay independent of the payment ID, which flows through
// URLs, webhook payloads, and logs.
func newClientSecret() (string, error) {
	b := make([]byte, 16) // 128 bits
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mint client secret: %w", err)
	}

	return "pfsec_" + hex.EncodeToString(b), nil
}
```

  At `:1135` mint before building `draftPayment` (`secret, err := newClientSecret()`;
  propagate err) and set `ClientSecret: secret`. At `:1221` keep the provider
  secret when present, else `clientSecret, err = newClientSecret()`. Verify the
  fallback value is persisted where the payment row is updated (read the
  surrounding update call — if the fallback is returned but never stored, store
  it before returning; the public endpoints compare against the stored value).
- [ ] **Step 4: gates** — `go test ./internal/usecase/payments/ && golangci-lint run ./internal/usecase/payments/`.
- [ ] **Step 5: commit** — `git commit -am "fix(backend): mint random payment client secrets independent of payment ID"`

### Task 5: Constant-time `client_secret` validation (3 sites)

**Files:**
- Modify: `internal/usecase/payments/service.go:2273`
- Modify: `internal/usecase/payments/service_hubtel.go:41`
- Modify: `internal/usecase/payments/checkout_sessions.go:52`
- Test: extend the confirm/checkout tests with a same-length wrong secret case

- [ ] **Step 1: implement** (import `crypto/subtle` in each file):

```go
	if subtle.ConstantTimeCompare([]byte(pmt.ClientSecret), []byte(clientSecret)) != 1 {
		return payment.Payment{}, ErrInvalidClientSecret
	}
```

  Same shape in `service_hubtel.go:41` (`return nil, ErrInvalidClientSecret`)
  and `checkout_sessions.go:52` (`return CheckoutSession{}, ErrInvalidClientSecret`).
- [ ] **Step 2: gates** — `go test ./internal/usecase/payments/ && golangci-lint run ./internal/usecase/payments/`.
- [ ] **Step 3: commit** — `git commit -am "fix(backend): constant-time client_secret comparison on public payment endpoints"`

### Task 6: Close the bulk status override

**Files:**
- Modify: `internal/usecase/payments/service.go:3489-3517`
- Test: `internal/usecase/payments/service_test.go`

Current state: `BulkStatusUpdate` runs with `AllowedFrom: nil`, so
`pending/requires_action → succeeded` is permitted via any `payments:write`
credential (`router.go:1173-1174`).

- [ ] **Step 1: failing test** — bulk-update a `pending` payment to `succeeded`;
  expect a per-payment error, no transition, no `payment.updated` webhook.
- [ ] **Step 2: run, expect fail**.
- [ ] **Step 3: implement** — add at the top of `BulkStatusUpdate`:

```go
// bulkStatusGuardStatuses are success-family states a bulk override may never
// set: reaching them must require provider confirmation, not a credential.
var errBulkStatusGuard = errors.New("bulk status override cannot set success-family states (succeeded/authorized/captured/partially_captured)")
```

  and inside the loop, before `applyPaymentTransition`:

```go
		switch status {
		case payment.StatusSucceeded, payment.StatusAuthorized,
			payment.StatusCaptured, payment.StatusPartiallyCaptured:
			results[paymentID] = errBulkStatusGuard

			continue
		}
```

  (Confirm the exact `payment.Status*` identifiers in `internal/domain/payment`
  — adjust names to the domain constants.) Map the sentinel in the HTTP handler
  to `422 status_guard` if handlers translate errors per-type; otherwise the
  default error mapping is acceptable.
- [ ] **Step 4: gates** — payments package tests + lint.
- [ ] **Step 5: commit** — `git commit -am "fix(backend): block success-family states in bulk status override"`

### Task 7: Scope-check `/v1/notifications/*`

**Files:**
- Modify: `adapters/http/router.go:1484-1493`
- Test: `adapters/http/router_test.go` (route/middleware table if one exists)

- [ ] **Step 1: implement** — mirror the neighboring `RequireScopes` pattern:

```go
				gated.Route("/notifications", func(n chi.Router) {
					n.With(RequireScopes(scopePaymentsRead)).Get("/", notificationHandler.list)
					n.With(RequireScopes(scopePaymentsRead)).Get("/grouped", notificationHandler.listGrouped)
					n.With(RequireScopes(scopePaymentsRead)).Get("/unread-count", notificationHandler.getUnreadCount)
					n.With(RequireScopes(scopePaymentsRead)).Get("/export", notificationHandler.export)
					n.With(RequireScopes(scopePaymentsWrite)).Post("/{id}/read", notificationHandler.markRead)
					n.With(RequireScopes(scopePaymentsWrite)).Post("/bulk-read", notificationHandler.bulkMarkRead)
					n.With(RequireScopes(scopePaymentsWrite)).Post("/{id}/archive", notificationHandler.archive)
					n.With(RequireScopes(scopePaymentsWrite)).Post("/bulk-archive", notificationHandler.bulkArchive)
				})
```

  (Session-authenticated dashboard users pass scope checks by design — verify
  `RequireScopes` admits sessions; read its middleware code first. If it does
  not, use `RequireAnyScopes` consistent with how other session+key routes work.)
- [ ] **Step 2: verify** — build + any router test; manual: an API key without
  `payments:read` gets 403 on `GET /v1/notifications`.
- [ ] **Step 3: commit** — `git commit -am "fix(backend): require payment scopes on notification routes"`

### Task 8: Uniform dispute-webhook responses (close existence oracle)

**Files:**
- Modify: `adapters/http/handlers_webhooks.go:482-489`
- Test: `adapters/http/handlers_webhooks_test.go`

Current state: unknown ref → `200 {"status":"ignored"}`; existing ref + bad
signature → `400 invalid_webhook`. The delta reveals payment existence.

- [ ] **Step 1: failing test** — POST a dispute with a valid-shaped but wrong
  signature for an existing transaction ref; expect `200 {"status":"ignored"}`
  and a recorded failure (same external response as unknown-ref).
- [ ] **Step 2: run, expect fail**.
- [ ] **Step 3: implement** — in the `errors.Is(err, usecasepayments.ErrDisputeSignatureInvalid)`
  branch, keep the internal failure recording exactly as-is and change only the
  response:

```go
	case errors.Is(err, usecasepayments.ErrDisputeSignatureInvalid):
		record.Status = ports.WebhookEventStatusFailed
		record.Error = errInvalidWebhookPayload
		record.FinalReason = finalReasonInvalidSignature

		h.metrics.RecordFailed(webhookProviderPaystack)
		h.recordDisputeOutcome(r.Context(), record)
		// Uniform with the unknown-payment response: never reveal whether the
		// disputed transaction ref exists on the platform.
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
```

  Legit Paystack traffic is unaffected (its signatures verify).
- [ ] **Step 4: gates** — http adapter tests + lint.
- [ ] **Step 5: commit** — `git commit -am "fix(backend): uniform dispute webhook responses for invalid signature vs unknown ref"`

### Task 9: Webhook replay — deny org-less events, re-check source IP

**Files:**
- Modify: `adapters/http/handlers_webhooks.go:586-590` (replay)
- Modify: `adapters/http/webhook_ip_allowlist.go` (export what's needed)
- Test: `adapters/http/handlers_webhooks_test.go`

Current state (`handlers_webhooks.go:586-590`): ownership check only fires when
`rec.OrgID != ""`; org-less events replay cross-tenant, and replay never
re-runs `webhookSourceAllowed`.

- [ ] **Step 1: failing tests** — (a) replay of an event with empty `OrgID` →
  404 `event_not_found`; (b) replay of an unsigned-provider event from a
  disallowed source IP → 403.
- [ ] **Step 2: run, expect fail** (a passes 200 today).
- [ ] **Step 3: implement**:

```go
	orgID := OrgID(r.Context())
	// Org-less events (persisted before org resolution, e.g. failed parses) are
	// never replayable: their IDs are computable, so any tenant could drive them.
	if rec.OrgID == "" || !strings.EqualFold(rec.OrgID, orgID) {
		writeError(w, http.StatusNotFound, "event_not_found", "webhook event not found")
		return
	}

	if allowed, reason := h.webhookSourceAllowed(r, strings.ToLower(rec.Provider)); !allowed {
		h.metrics.RecordFailed(rec.Provider)
		writeError(w, http.StatusForbidden, "webhook_source_forbidden", reason)
		return
	}
```

  (Match the real signature of `webhookSourceAllowed` in
  `webhook_ip_allowlist.go` — it currently takes the provider from the route in
  `handle`; factor a shared helper if needed and use it from both sites.)
- [ ] **Step 4: gates** — http adapter tests + lint.
- [ ] **Step 5: commit** — `git commit -am "fix(backend): deny org-less webhook replay and re-check source IP allowlist"`

### Task 10: Require docs auth in production

**Files:**
- Modify: `internal/infra/config/config.go:445-508` (`Validate()`)
- Test: `internal/infra/config/config_test.go`

Current state: `DocsBasicAuthUsername/Password` default `""` (`config.go:123-124`);
when unset, `router.go:350-359` serves `/docs`, `/openapi.yaml`, `/openapi.json`
publicly.

- [ ] **Step 1: failing test** — production config with empty docs creds AND
  empty `DOCS_PUBLIC` → `Validate()` error containing `DOCS_BASIC_AUTH`.
- [ ] **Step 2: run, expect fail**.
- [ ] **Step 3: implement** — add config field near line 124:

```go
	DocsPublic                   bool          `env:"DOCS_PUBLIC" envDefault:"false"`
```

  and in `Validate()` after the metrics block:

```go
	// The OpenAPI surface (every endpoint, scope, and payload shape) is public
	// unless basic auth is set; require creds or an explicit opt-out.
	if !c.DocsPublic && (strings.TrimSpace(c.DocsBasicAuthUsername) == "" || strings.TrimSpace(c.DocsBasicAuthPassword) == "") {
		missing = append(missing, "DOCS_BASIC_AUTH_USERNAME/DOCS_BASIC_AUTH_PASSWORD (or DOCS_PUBLIC=true)")
	}
```

  Also add `DOCS_PUBLIC=` to `backend/.env.example` under the docs lines.
- [ ] **Step 4: gates** — config tests + lint.
- [ ] **Step 5: commit** — `git commit -am "fix(backend): require docs basic auth or explicit DOCS_PUBLIC in production"`

### Task 11: Redis-backed public auth rate limiting

**Files:**
- Modify: `adapters/http/middleware_rate_limit.go` (`PublicAuthRateLimit`, ~line 80+)
- Modify: `adapters/http/router.go:424-429` (construction)
- Modify: `cmd/api/main.go` (wire Redis client into `RouterOptions`)
- Test: `adapters/http/middleware_rate_limit_test.go` (miniredis, already a dependency per renewalscheduler tests)

Current state: `NewPublicAuthRateLimiter(config)` builds in-memory
`RateLimiter`s per branch — per-pod budgets multiply by replica count. A
`RedisRateLimiter` (`internal/services/auth/redis_rate_limiter.go`,
`RateLimiterInterface`) already exists.

- [ ] **Step 1: read** the remainder of `middleware_rate_limit.go` (the
  `PublicAuthRateLimit` middleware and `NewPublicAuthRateLimiter`) to see how
  per-branch limiters are stored.
- [ ] **Step 2: implement** — extend `PublicAuthRateLimiterConfig` handling so
  the middleware accepts a `RateLimiterInterface` factory:

```go
// In router.go where authRateLimit is built:
var authRateLimit func(http.Handler) http.Handler
if opts.RateLimiterBackend != nil { // *redis.Client injected from cmd/api
	authRateLimit = NewPublicAuthRateLimiterRedis(authRateLimiterConfig, opts.RateLimiterBackend)
} else {
	authRateLimit = NewPublicAuthRateLimiter(authRateLimiterConfig)
}
```

  `NewPublicAuthRateLimiterRedis` mirrors the in-memory constructor but creates
  `authservice.NewRedisRateLimiter(client, "auth:ratelimit:"+branch+":", window, maxReqs)`
  per branch; the middleware's `Allow(key)` call sites switch to
  `Allow(r.Context(), key)` (interface already matches). Production boots require
  `REDIS_ADDR` already, so no fallback logic is needed there; keep in-memory as
  the non-Redis default for tests/dev. Apply the same backend to
  `CLIAuthRateLimit` (same file) via a shared option.
- [ ] **Step 3: tests** — miniredis-backed test: 4th magic-link request in 5m
  (limit 3) returns 429; a second limiter instance sharing the Redis sees the
  same budget (proves cross-pod sharing).
- [ ] **Step 4: gates** — `go test ./adapters/http/ -run RateLimit -v && golangci-lint run ./adapters/http/`.
- [ ] **Step 5: commit** — `git commit -am "fix(backend): Redis-backed rate limiting for public auth endpoints"`

### Task 12: CSV formula-injection sanitization

**Files:**
- Modify: `internal/usecase/exports/service.go:368-386` (payments) + the
  customers/refunds/disputes row loops in the same file
- Test: `internal/usecase/exports/service_test.go`

- [ ] **Step 1: failing table test**:

```go
func TestSanitizeCSVCell(t *testing.T) {
	cases := map[string]string{
		"plain":            "plain",
		"=SUM(A1)":         "'=SUM(A1)",
		"+1-2":             "'+1-2",
		"-5":               "'-5",
		"@cmd":             "'@cmd",
		"\t=x":             "'\t=x",
		"=HYPERLINK(\"http://evil\",\"x\")": "'=HYPERLINK(\"http://evil\",\"x\")",
		"":                 "",
	}
	for in, want := range cases {
		if got := sanitizeCSVCell(in); got != want {
			t.Errorf("sanitizeCSVCell(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: run, expect fail** (undefined func).
- [ ] **Step 3: implement**:

```go
// sanitizeCSVCell neutralizes spreadsheet formula injection: cells whose first
// byte is a formula trigger are prefixed with a single quote so Excel/Sheets
// render them as text.
func sanitizeCSVCell(s string) string {
	if s == "" {
		return s
	}

	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}

	return s
}
```

  Apply to every attacker-influenced string cell in all export loops: metadata
  (`formatMetadata(p.Metadata)` output), customer names/emails, and any
  free-text columns — wrap at row-build time, e.g.
  `sanitizeCSVCell(formatMetadata(p.Metadata))`.
- [ ] **Step 4: gates** — exports package tests + lint.
- [ ] **Step 5: commit** — `git commit -am "fix(backend): sanitize formula-trigger prefixes in CSV exports"`

### Task 13: Bind the admin 2FA gate to the current session

**Files:**
- Create: `db/migrations/000150_sessions_two_factor_verified_at.sql` (next free
  number — max today is `000149`; never reuse 000118-000125/000129)
- Modify: `db/queries/sessions.sql` (scan/insert updates + new
  `SetSessionTwoFactorVerified` query) — regenerate sqlc after editing
- Modify: `internal/ports/auth.go:361-380` (`Session` struct)
- Modify: `internal/services/auth/handlers_auth.go` (`handleTOTPChallenge` success path)
- Modify: `adapters/http/middleware_2fa_required.go:29-38`
- Test: `internal/services/auth/` + `adapters/http/` tests

Current state: `Require2FA` checks only `IsTOTPEnabled(userID)` — any session
created before 2FA enrollment (or a hijacked cookie) passes.

- [ ] **Step 1: migration** — `ALTER TABLE sessions ADD COLUMN two_factor_verified_at timestamptz;`
  (with `-- +goose Up/Down`; Down drops the column). Validate up+down on a
  fresh DB via the compose migrate service (`docker compose run --entrypoint`
  override per backend/CLAUDE.md).
- [ ] **Step 2: query + regen** — add to `db/queries/sessions.sql`:

```sql
-- name: SetSessionTwoFactorVerified :exec
UPDATE sessions SET two_factor_verified_at = now() WHERE id = $1;
```

  Add `two_factor_verified_at` to the session SELECT scans, run
  `sqlc generate`, and add `TwoFactorVerifiedAt *time.Time` to `ports.Session`.
- [ ] **Step 3: set on challenge success** — in `handleTOTPChallenge`, where the
  TOTP/backup-code check succeeds and the session is issued/confirmed, call the
  new repo method for that session ID. Failing test first: completing 2FA sets
  the marker; a session created before enrollment has NULL.
- [ ] **Step 4: assert in middleware** — extend `TOTPChecker` with
  `SessionTwoFactorVerified(ctx context.Context, sessionID string) (bool, error)`
  and require it after the `IsTOTPEnabled` check:

```go
			verified, err := totpChecker.SessionTwoFactorVerified(r.Context(), SessionID(r.Context()))
			if err != nil {
				writeError(w, http.StatusInternalServerError, "2fa_check_failed", "unable to verify 2FA status")
				return
			}

			if !verified {
				writeError(w, http.StatusForbidden, "2fa_required", "this session has not completed two-factor authentication")
				return
			}
```

  (Confirm the context accessor name for the session ID — `SessionID(r.Context())`
  or read it from the session stored by SessionAuth middleware.) A session
  without the marker gets the same 403 shape as "2FA not enabled", so the
  frontend re-challenge flow handles both.
- [ ] **Step 5: gates** — auth services + http adapter tests, lint, migration
  validated on fresh DB.
- [ ] **Step 6: commit** — `git commit -am "fix(backend): bind admin 2FA requirement to the current session"`

### Task 14: Stop logging magic-link token bytes

**Files:**
- Modify: `internal/services/auth/users.go:402`
- Test: `internal/services/auth/users_test.go` (log-output assertion if the
  logger is injectable — it is, `s.logger`)

- [ ] **Step 1: implement** (imports `crypto/sha256`, `encoding/hex`):

```go
		if s.logger != nil {
			fp := sha256.Sum256([]byte(token))
			s.logger.Printf("auth: verify_magic_link failed: invalid token error=%v token_fingerprint=%s", err, hex.EncodeToString(fp[:])[:12])
		}
```

- [ ] **Step 2: test** — failed verify logs `token_fingerprint=` and never any
  prefix of the raw token.
- [ ] **Step 3: gates + commit** — `go test ./internal/services/auth/ && git commit -am "fix(backend): log magic-link token fingerprint instead of token prefix"`

### Task 15: Bump gRPC past GO-2026-6061 + changelog

**Files:**
- Modify: `go.mod` (line 36: `google.golang.org/grpc v1.79.2`)
- Modify: `backend/CLAUDE.md` (changelog section)

- [ ] **Step 1: bump**:

```bash
cd backend
go get google.golang.org/grpc@v1.82.1
go mod tidy
go build ./...
```

- [ ] **Step 2: verify the advisory is gone**:

```bash
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

  Expected: `Your code is affected by 0 vulnerabilities` in symbol results
  (module-level advisories for non-imported packages may still print — the
  symbol-level section is the gate).
- [ ] **Step 3: changelog** — append a `### Jul 28, 2026` entry to
  `backend/CLAUDE.md` summarizing this plan's batch (fail-closed Sail Rides
  webhook, constant-time compares ×5, random client secrets, bulk-status guard,
  notification scopes, dispute oracle, replay hardening, docs auth, Redis auth
  rate limits, CSV sanitization, session-bound 2FA, token log fingerprint, gRPC
  bump), following the existing entry style.
- [ ] **Step 4: full gates** — `golangci-lint run ./...` (0 issues) and
  `go test ./...` (DB-backed: `TEST_DATABASE_URL='postgres://reevit:reevit@localhost:5433/reevit_test?sslmode=disable'`).
- [ ] **Step 5: commit** — `git commit -am "chore(backend): bump grpc to v1.82.1 (GO-2026-6061) and record security batch changelog"`

## STOP conditions

- Any task's "Current state" excerpt no longer matches the live file → re-audit
  that task against the drifted code before editing; if the finding is already
  fixed, skip the task and note it.
- `newClientSecret` reveals existing stored payments still carry `pf_<id>`
  secrets → expected and acceptable (they age out with payment expiry); do NOT
  mass-rotate stored secrets inside this plan.
- The sqlc regeneration in Task 13 produces unrelated diffs → STOP and report
  (someone edited queries without regenerating).
- Any gate failure you cannot attribute to your change (baseline failures
  `TestPayoutRepo_ListUnsettled`, `TestPlatformInvoiceRepo_Collection` are
  pre-existing per backend/CLAUDE.md) → verify against a clean baseline, then
  report.

## Verification (end of plan)

- `golangci-lint run ./...` → 0 issues.
- `go test ./...` → green (same pre-existing exceptions as above).
- `govulncheck ./...` → no symbol-level findings.
- Manual spot-checks: Sail Rides webhook 401s without secret; bulk status
  `pending→succeeded` rejected; `GET /v1/notifications` 403 with scopeless key;
  `/docs` requires auth in production config.
