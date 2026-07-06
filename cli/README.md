# reevit CLI

Command-line tool for [Reevit](https://reevit.io): test payments locally,
drive the sandbox simulator, and inspect your account — with a scoped API key.

```bash
go install github.com/Reevit-Platform/cli@latest   # binary: cli → alias as reevit
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

## Roadmap

- `reevit listen --forward-to localhost:3000/webhooks` — stream test-mode
  events to a local endpoint with valid `X-Reevit-Signature`s
- Homebrew tap + prebuilt binaries
