# reevit CLI

Command-line tool for [Reevit](https://reevit.io): set up Reevit in your
project in one command, test payments locally, drive the sandbox simulator,
and inspect your account.

## Install

```bash
# Homebrew (macOS / Linux)
brew install reevit-platform/tap/reevit

# npm
npm install -g @reevit/cli

# Go
go install github.com/Reevit-Platform/cli@latest   # binary: cli → alias as reevit
```

Prebuilt binaries for every platform are on the
[releases page](https://github.com/Reevit-Platform/cli/releases).

## Quickstart

```bash
cd your-project
reevit init
```

`init` opens your browser to log in (no keys to copy), detects your stack,
installs the matching SDK, wires your env, and writes working integration
code. Then prove the whole setup:

```bash
reevit doctor --webhook-url http://localhost:3000/api/webhooks/reevit
```

## Commands

| Command | What it does |
| --- | --- |
| `reevit login` | Opens the dashboard in your browser to authorize the CLI — a **test-mode** key is created for you and stored locally (0600). `--key pfk_...` pastes a key manually; `--no-browser` prints the URL instead of opening it |
| `reevit init` | Sets Reevit up in the current project: detects the stack + package manager, installs the SDK, wires env vars (including the browser-exposed `NEXT_PUBLIC_`/`VITE_` key for checkout), scaffolds a webhook handler / checkout component / server client, and can insert checkout into an existing page. `--target`, `-y`, `--dry-run`, `--checkout-page`, `--checkout-fields`, `--checkout-metadata`, custom output paths, `--register-webhook <url>` |
| `reevit doctor [--webhook-url url] [--e2e]` | Verifies the setup: key against the API, env wiring, SDK installed, signed + tampered webhook round-trip — and with `--e2e`, a **real** sandbox payment through the simulator delivered to your handler |
| `reevit payments list [--status s] [--limit n]` | Recent payments in the current mode |
| `reevit trigger <event>` | Fire a test event by creating a **real** sandbox payment through the simulator |
| `reevit listen --forward-to <url>` | Stream live test-mode events to a local endpoint, signed like production |

### `login`

`reevit login` starts a pairing session, prints a code, and opens
`dashboard.reevit.io/cli/confirm`. Check the code matches your terminal, pick
your organization, and approve — the CLI receives a freshly minted
**test-mode** API key scoped to what the CLI actually needs (payments,
webhooks:read). The key is delivered exactly once and never shown in the
browser. When you're ready for live traffic, create a live key in
Dashboard → Developers → API keys and run `reevit login --key <live_key>`.

### `init`

Detection covers Next.js, React, Vue, Svelte, Node/Express, Go, PHP, and
Python — including TypeScript-vs-JS, `src/` layouts, and every JS package
manager with a lockfile (multi-lockfile repos get the SDK installed with all
of them so frozen installs stay consistent). What it can scaffold depends on
the stack:

- **Webhook handler** — signature-verified receiver using the SDK's verify
  helper (inlined for Go, whose SDK ships no inbound verifier)
- **Checkout component** — `ReevitCheckout` drop-in for React/Next/Vue/Svelte.
  In an interactive run, the CLI asks which existing page should receive the
  button, which standard fields to collect (amount, name, email, phone, and
  payment reference), and which custom payment metadata fields to add.
- **Server client** — SDK client wired to `REEVIT_API_KEY` + `REEVIT_ORG_ID`
  with a payment-intent example

Env wiring is stack-aware: server code reads plain `REEVIT_*` vars, and when
you scaffold checkout the browser-exposed key is written under the framework's
own convention — `NEXT_PUBLIC_REEVIT_KEY` on Next.js, `VITE_REEVIT_KEY` on
Vite-based stacks (React/Vue/Svelte). Test-mode keys only in the client bundle.

Place code wherever you like with `--webhook-path`, `--checkout-path`, and
`--client-path`. With a webhook target, `--register-webhook https://…` (or the
interactive prompt) registers the production endpoint in your dashboard —
needs a key with `webhooks:write` (fresh `reevit login` keys have it).

Checkout can also be fully scripted:

```bash
reevit init --target checkout \
  --checkout-page src/app/cart/page.tsx \
  --checkout-fields price,name,email,phone,reference \
  --checkout-metadata order_id,product_sku
```

The page update is marker-based and idempotent. Standard customer fields are
stored under `customer_name`, `customer_email`, and `customer_phone`, matching
workflow variables. Custom keys are attached unchanged and are available to
workflow templates as `metadata_<key>` (for example,
`metadata_order_id`). Enter `-` when the interactive page question appears to
keep the component standalone.

Generated integration files and env values are never overwritten. The selected
checkout page is updated only with a clearly marked, idempotent block, so
re-running is safe.

### `doctor`

```bash
reevit doctor --webhook-url http://localhost:3000/api/webhooks/reevit
```

Checks, in order: CLI key present and accepted by the API; project + SDK
detected; `REEVIT_API_KEY` / `REEVIT_ORG_ID` / `REEVIT_WEBHOOK_SECRET` in the
env file; webhook handler present. With `--webhook-url` and your dev server
running, it signs a synthetic `payment.succeeded` with your
`REEVIT_WEBHOOK_SECRET` and POSTs it — your handler must accept it — then
sends the same payload with a tampered signature — your handler must reject
it. Exits non-zero when anything fails, so it works in CI.

Add `--e2e` for the strongest check: doctor fires a **real** sandbox payment
through the simulator, waits for the platform-generated event on your
account's stream, and delivers it to `--webhook-url` with the production
envelope and signature — proving the entire pipeline, not just the signature
contract. Test mode only.

### `trigger` events → simulator magic amounts

Triggering doesn't fake events — it creates a real test-mode payment against
the simulator connection (auto-created on first use) with the documented
magic amount, so webhooks/notifications/SSE all come from the production
pipeline and match production schemas by construction.

| Event | Amount | Behavior |
| --- | --- | --- |
| `payment.succeeded` | 4000 | immediate success |
| `payment.failed` | 4001 | hard decline (no failover) |
| `payment.insufficient_funds` | 4002 | decline (no failover) |
| `payment.timeout` | 4003 | transient — exercises routing failover |
| `payment.provider_downtime` | 4004 | provider down — exercises failover |

`trigger` refuses to run in live mode.

### `listen`

```bash
reevit listen --forward-to http://localhost:3000/api/webhooks/reevit
```

Subscribes to your account's live test-mode event stream and POSTs each event
to your endpoint with the production header set and a real signature
(`X-Reevit-Signature: sha256=<hex HMAC-SHA256 of the raw body>`), so your
verification code runs unchanged. The signing secret comes from your webhook
config when the key has `webhooks:read`; otherwise an ephemeral secret is
generated and printed — put it in `REEVIT_WEBHOOK_SECRET`. Reconnects with
backoff; test mode only.

## Telemetry

The CLI reports one **anonymous** usage event per command run (command name,
CLI version, OS/arch, detected stack and chosen targets for `init`,
success/failure, duration, org id when logged in, and a random per-install
UUID). Never collected: file contents, paths, keys, or hostnames. Events go
to the Reevit API only — no third-party analytics keys ship in the binary.

Opt out any time:

```bash
export REEVIT_TELEMETRY=0   # or the cross-tool convention: DO_NOT_TRACK=1
```

A one-time notice is printed on first use.

## Configuration

Env vars override the config file: `REEVIT_API_KEY`, `REEVIT_API_URL`
(default `https://api.reevit.io`), `REEVIT_MODE` (`test`|`live`, default
`test`), `REEVIT_CONFIG` (config file path).

## Releasing

Tag `v*` → GitHub Actions runs GoReleaser: builds all platforms, publishes the
GitHub release, and updates the Homebrew tap (needs the `HOMEBREW_TAP_TOKEN`
repo secret — a fine-grained PAT with `contents:write` on
`Reevit-Platform/homebrew-tap`). The npm wrapper (`npm/`) publishes from the
same workflow via npm trusted publishing (configure the repo + `release.yml`
as a trusted publisher on the `@reevit/cli` package settings); bump
`npm/package.json` to the tag version before tagging.
