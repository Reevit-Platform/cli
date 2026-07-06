# @reevit/mcp

MCP server for [Reevit](https://reevit.io) — lets AI agents create payment
links, look up payments, issue refunds, and read analytics through your Reevit
account, using a **scoped API key** you control.

## Tools

| Tool | What it does | Scope needed |
| --- | --- | --- |
| `create_payment_link` | Shareable pay link (fixed or open amount) | `payment_links:write` |
| `get_payment` | One payment by id, with status + route | `payments:read` |
| `list_payments` | Recent payments with filters | `payments:read` |
| `create_refund` | Full/partial refund — **live mode requires `confirm: true`** | `payments:write` |
| `get_analytics_summary` | Volume, counts, success rate | `payments:read` |

Scopes are enforced by the Reevit backend on every call: a `payments:read`-only
key cannot refund, no matter what the model asks for.

## Setup

Create an API key in the Reevit dashboard (**Developers → API keys**) with only
the scopes you want the agent to have.

### Claude Code

```bash
claude mcp add reevit -e REEVIT_API_KEY=rk_test_... -- npx -y @reevit/mcp
```

### Claude Desktop

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "reevit": {
      "command": "npx",
      "args": ["-y", "@reevit/mcp"],
      "env": { "REEVIT_API_KEY": "rk_test_..." }
    }
  }
}
```

## Configuration

| Env var | Default | Notes |
| --- | --- | --- |
| `REEVIT_API_KEY` | — (required) | Scoped API key from the dashboard |
| `REEVIT_MODE` | `test` | `test` or `live`. Live refunds additionally require `confirm: true` per call |
| `REEVIT_API_URL` | `https://api.reevit.io` | Point at a self-hosted / local API |

## Safety model

- **Scopes, not trust**: authorization lives in the backend key scopes.
- **Live-money gate**: `create_refund` in live mode refuses to run unless the
  call includes `confirm: true`, so an agent must surface the decision to a
  human first.
- **Idempotency**: money-moving calls send an `Idempotency-Key`, so a retried
  tool call can never double-refund or double-create.
- Amounts are minor units everywhere (GHS 50.00 = `5000`).

## Development

```bash
npm install
npm test        # vitest — gate + request-shaping tests
npm run build   # tsc → dist/
REEVIT_API_KEY=rk_test_... npm run dev
```
