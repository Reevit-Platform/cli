# reevit CLI

Command-line tool for [Reevit](https://reevit.io): test payments locally,
drive the sandbox simulator, and inspect your account — with a scoped API key.

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

```bash
reevit login                                        # stores the key with 0600 perms
reevit payments list --status failed
reevit trigger payment.succeeded
```

## Commands

| Command | What it does |
| --- | --- |
| `reevit login [--key rk_test_...]` | Verify + store a scoped API key (`~/.config/reevit/config.json`, 0600) |
| `reevit payments list [--status s] [--limit n]` | Recent payments in the current mode |
| `reevit trigger <event>` | Fire a test event by creating a **real** sandbox payment through the simulator |
| `reevit listen --forward-to <url>` | Stream live test-mode events to a local endpoint, signed like production |

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

## Configuration

Env vars override the config file: `REEVIT_API_KEY`, `REEVIT_API_URL`
(default `https://api.reevit.io`), `REEVIT_MODE` (`test`|`live`, default
`test`), `REEVIT_CONFIG` (config file path).

### `listen`

```bash
reevit listen --forward-to http://localhost:3000/webhooks
```

Subscribes to your account's live test-mode event stream and POSTs each event
to your endpoint with the production header set and a real signature
(`X-Reevit-Signature: sha256=<hex HMAC-SHA256 of the raw body>`), so your
verification code runs unchanged. The signing secret comes from your webhook
config when the key has `webhooks:read`; otherwise an ephemeral secret is
generated and printed. Reconnects with backoff; test mode only.

## Releasing

Tag `v*` → GitHub Actions runs GoReleaser: builds all platforms, publishes the
GitHub release, and updates the Homebrew tap (needs the `HOMEBREW_TAP_TOKEN`
repo secret — a fine-grained PAT with `contents:write` on
`Reevit-Platform/homebrew-tap`). The npm package (`npm/`) is published
manually: bump `npm/package.json` to the tag version, then
`npm publish --access public` from `npm/`.
