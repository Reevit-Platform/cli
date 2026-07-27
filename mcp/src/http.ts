import { createHash, timingSafeEqual } from "node:crypto";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";

import { ReevitClient, type ReevitConfig } from "./client.js";
import { buildTools } from "./tools.js";

/** 1 MiB — generous for a JSON-RPC tool call, small enough to bound memory. */
const MAX_BODY_BYTES = 1024 * 1024;

const LOOPBACK_HOSTS = new Set(["127.0.0.1", "::1", "localhost"]);

function isLoopbackHost(host: string): boolean {
  return LOOPBACK_HOSTS.has(host);
}

/** Origins explicitly allowed to call this endpoint from a browser context. Empty by default. */
function allowedOrigins(env: NodeJS.ProcessEnv): Set<string> {
  const raw = env.REEVIT_MCP_ALLOWED_ORIGINS?.trim();
  if (!raw) return new Set();
  return new Set(
    raw
      .split(",")
      .map((o) => o.trim())
      .filter(Boolean),
  );
}

/**
 * Compare two secrets without leaking anything through timing.
 *
 * Hashing first is what makes this honest: `timingSafeEqual` throws outright
 * on a length mismatch, so a naive wrapper has to branch on length and thereby
 * leaks the token's length to an attacker who can time it. SHA-256 maps both
 * inputs to a fixed 32 bytes, so there is exactly one code path regardless of
 * what was submitted.
 */
function constantTimeEquals(a: string, b: string): boolean {
  const digestA = createHash("sha256").update(a, "utf8").digest();
  const digestB = createHash("sha256").update(b, "utf8").digest();

  return timingSafeEqual(digestA, digestB);
}

function newServer(config: ReevitConfig): McpServer {
  const server = new McpServer({ name: "reevit", version: "0.1.1" });

  for (const tool of buildTools(new ReevitClient(config))) {
    server.registerTool(
      tool.name,
      { description: tool.description, inputSchema: tool.inputSchema },
      tool.handler as never,
    );
  }

  return server;
}

function sendJsonRpcError(res: ServerResponse, status: number, message: string): void {
  if (res.headersSent) return;
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(
    JSON.stringify({
      jsonrpc: "2.0",
      error: { code: -32603, message },
      id: null,
    }),
  );
}

/**
 * Streamable HTTP transport, stateless mode: each POST /mcp gets a fresh
 * server+transport pair, so there is no session state to leak between
 * callers.
 *
 * Network exposure: binds to loopback (127.0.0.1) by default — override with
 * REEVIT_MCP_HOST only if you genuinely intend to expose this beyond the
 * local machine, and put a real network boundary (firewall/VPN) in front of
 * it if you do.
 *
 * Auth: requires `Authorization: Bearer <REEVIT_MCP_AUTH_TOKEN>` whenever the
 * server is NOT bound to loopback — a non-loopback bind with no token would
 * hand the configured Reevit API key to anyone who can reach the port. On
 * the loopback default, the token is optional (any process on the same
 * machine can already reach a loopback-bound port, so an unset token there
 * is a much smaller relaxation), but if REEVIT_MCP_AUTH_TOKEN is set it is
 * enforced regardless of bind host.
 *
 * Also rejects any request carrying an `Origin` header that isn't in
 * REEVIT_MCP_ALLOWED_ORIGINS (empty by default) — a legitimate local MCP
 * client never sends `Origin`, only browsers do, so this is the primary
 * defense against DNS-rebinding / drive-by-browser attacks. Bodies must be
 * `application/json` (defeats CORS-simple-request bypasses) and are capped
 * at 1 MiB.
 *
 * Returns the listening server. `index.ts` ignores it and simply keeps the
 * process alive, but returning it is what lets the test suite bind an
 * ephemeral port, exercise the guards above against a real socket, and close
 * cleanly afterwards.
 */
export async function serveHttp(config: ReevitConfig, port: number): Promise<Server> {
  const env = process.env;
  const host = env.REEVIT_MCP_HOST?.trim() || "127.0.0.1";
  const loopback = isLoopbackHost(host);
  const authToken = env.REEVIT_MCP_AUTH_TOKEN?.trim() || undefined;
  const origins = allowedOrigins(env);

  if (!loopback && !authToken) {
    throw new Error(
      "REEVIT_MCP_HOST is set to a non-loopback address but REEVIT_MCP_AUTH_TOKEN is unset. " +
        "Refusing to start: this would expose a live Reevit API key to anyone who can reach the port. " +
        "Set REEVIT_MCP_AUTH_TOKEN, or leave REEVIT_MCP_HOST unset to bind to loopback only.",
    );
  }

  const httpServer = createServer(async (req: IncomingMessage, res: ServerResponse) => {
    if (!req.url?.startsWith("/mcp")) {
      res.writeHead(404).end();
      return;
    }

    try {
      // 1. Origin allowlist — the primary DNS-rebinding / browser defense.
      const origin = req.headers.origin;
      if (origin !== undefined && !origins.has(origin)) {
        sendJsonRpcError(res, 403, `Origin "${origin}" is not allowed to call this server.`);
        return;
      }

      // 2. Bearer auth, required off-loopback and whenever a token is configured.
      if (authToken) {
        const header = req.headers.authorization ?? "";
        const [scheme, token] = header.split(" ", 2);
        if (scheme !== "Bearer" || !token || !constantTimeEquals(token, authToken)) {
          sendJsonRpcError(res, 401, "Missing or invalid bearer token.");
          return;
        }
      }

      // 3. Content-Type — reject anything that isn't JSON (kills the
      // CORS-simple-request / text-plain bypass).
      const contentType = req.headers["content-type"] ?? "";
      const hasBody = req.method !== "GET" && req.method !== "HEAD";
      if (hasBody && !contentType.toLowerCase().startsWith("application/json")) {
        sendJsonRpcError(res, 415, `Unsupported Content-Type "${contentType}"; expected application/json.`);
        return;
      }

      const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
      const server = newServer(config);

      res.on("close", () => {
        void transport.close();
        void server.close();
      });

      await server.connect(transport);

      // 4. Bounded body read — reject with 413 instead of buffering without limit.
      const chunks: Buffer[] = [];
      let total = 0;
      let tooLarge = false;
      for await (const chunk of req as AsyncIterable<Buffer>) {
        total += chunk.length;
        if (total > MAX_BODY_BYTES) {
          tooLarge = true;
          break;
        }
        chunks.push(chunk);
      }

      if (tooLarge) {
        sendJsonRpcError(res, 413, `Request body exceeds the ${MAX_BODY_BYTES}-byte limit.`);
        void transport.close();
        void server.close();
        return;
      }

      const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString()) : undefined;

      await transport.handleRequest(req, res, body);
    } catch (error) {
      sendJsonRpcError(res, 500, error instanceof Error ? error.message : "error");
    }
  });

  await new Promise<void>((resolve) => httpServer.listen(port, host, resolve));

  console.error(
    `reevit-mcp listening on http://${host}:${port}/mcp (${config.mode} mode)` +
      (authToken ? ", bearer auth required" : loopback ? ", no auth (loopback only)" : ""),
  );

  if (!loopback) {
    console.error(
      `WARNING: reevit-mcp is bound to ${host}, not loopback. This endpoint holds a live Reevit ` +
        "API key and is reachable from beyond this machine — confirm REEVIT_MCP_AUTH_TOKEN is set " +
        "(it is) and that you have a real network boundary (firewall/VPN) in front of it.",
    );
  }

  return httpServer;
}
