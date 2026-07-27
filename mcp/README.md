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
claude mcp add reevit -e REEVIT_API_KEY=pfk_test_... -- npx -y @reevit/mcp
```

### Claude Desktop

Add to `claude_desktop_config.json` (Settings → Developer → Edit Config):

```json
{
  "mcpServers": {
    "reevit": {
      "command": "npx",
      "args": ["-y", "@reevit/mcp"],
      "env": { "REEVIT_API_KEY": "pfk_test_..." }
    }
  }
}
```

### Cursor

Add to `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` in your project:

```json
{
  "mcpServers": {
    "reevit": {
      "command": "npx",
      "args": ["-y", "@reevit/mcp"],
      "env": { "REEVIT_API_KEY": "pfk_test_..." }
    }
  }
}
```

### VS Code (Copilot agent mode)

Add to `.vscode/mcp.json` in your workspace:

```json
{
  "servers": {
    "reevit": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@reevit/mcp"],
      "env": { "REEVIT_API_KEY": "pfk_test_..." }
    }
  }
}
```

### Windsurf

Add to `~/.codeium/windsurf/mcp_config.json`:

```json
{
  "mcpServers": {
    "reevit": {
      "command": "npx",
      "args": ["-y", "@reevit/mcp"],
      "env": { "REEVIT_API_KEY": "pfk_test_..." }
    }
  }
}
```

### Cline

Open Cline → MCP Servers → Configure, and add the same `mcpServers` entry as
Cursor/Windsurf above to `cline_mcp_settings.json`.

### Zed

Add to Zed `settings.json`:

```json
{
  "context_servers": {
    "reevit": {
      "command": { "path": "npx", "args": ["-y", "@reevit/mcp"] },
      "settings": {}
    }
  }
}
```

Set `REEVIT_API_KEY` in the environment Zed is launched from.

### Codex CLI

Add to `~/.codex/config.toml`:

```toml
[mcp_servers.reevit]
command = "npx"
args = ["-y", "@reevit/mcp"]
env = { REEVIT_API_KEY = "pfk_test_..." }
```

### Gemini CLI

Add to `~/.gemini/settings.json`:

```json
{
  "mcpServers": {
    "reevit": {
      "command": "npx",
      "args": ["-y", "@reevit/mcp"],
      "env": { "REEVIT_API_KEY": "pfk_test_..." }
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
| `REEVIT_MCP_HOST` | `127.0.0.1` | HTTP mode only. Bind host; non-loopback values require `REEVIT_MCP_AUTH_TOKEN` |
| `REEVIT_MCP_AUTH_TOKEN` | — (optional on loopback) | HTTP mode only. Bearer token required in the `Authorization` header |
| `REEVIT_MCP_ALLOWED_ORIGINS` | — (none) | HTTP mode only. Comma-separated browser `Origin` allowlist; any other `Origin` is rejected |

## Safety model

- **Scopes, not trust**: authorization lives in the backend key scopes.
- **Live-money gate**: `create_refund` in live mode refuses to run unless the
  call includes `confirm: true`, so an agent must surface the decision to a
  human first.
- **Idempotency**: money-moving calls send an `Idempotency-Key`, so a retried
  tool call can never double-refund or double-create.
- Amounts are minor units everywhere (GHS 50.00 = `5000`).

## Streamable HTTP mode

For remote/agent-platform use, run the same server over streamable HTTP
(stateless; each request gets a fresh server, no session leakage):

```bash
REEVIT_API_KEY=pfk_test_... npx -y @reevit/mcp --http --port 8788
# endpoint: http://127.0.0.1:8788/mcp
```

HTTP mode binds to **loopback (`127.0.0.1`) by default** and enforces:

- **Bind host**: set `REEVIT_MCP_HOST` only if you genuinely need to expose
  this beyond the local machine, and put a real network boundary
  (firewall/VPN) in front of it. A non-loopback `REEVIT_MCP_HOST` requires
  `REEVIT_MCP_AUTH_TOKEN` to be set — the server refuses to start otherwise.
- **Bearer auth**: set `REEVIT_MCP_AUTH_TOKEN` and send
  `Authorization: Bearer <token>`. Required whenever the bind host isn't
  loopback; optional (but still enforced if set) on the loopback default,
  since anything reaching a loopback-bound port is already local to the
  machine.
- **Origin allowlist**: requests carrying a browser `Origin` header are
  rejected unless that origin is listed in `REEVIT_MCP_ALLOWED_ORIGINS`. A
  normal MCP client never sends `Origin`; this is what stops a malicious web
  page from driving the server via DNS rebinding or a direct
  `127.0.0.1:<port>` request.
- **Content-Type**: only `application/json` bodies are accepted, which
  closes off the CORS-"simple request" bypass (e.g. `text/plain` fetches that
  skip preflight).
- **Body size**: requests over 1 MiB are rejected with `413` instead of being
  buffered without limit.

## Development

```bash
npm install
npm test        # vitest — gate + request-shaping tests
npm run build   # tsc → dist/
REEVIT_API_KEY=pfk_test_... npm run dev
```
