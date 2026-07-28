import { createServer } from "node:http";
import { timingSafeEqual } from "node:crypto";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";

import { ReevitClient, type ReevitConfig } from "./client.js";
import { buildTools } from "./tools.js";

const MAX_BODY_BYTES = 1_000_000; // 1MB
const LOOPBACK_HOSTS = new Set(["localhost", "127.0.0.1", "[::1]"]);

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

export interface ServeHttpOptions {
  /** Bind address. Defaults to 127.0.0.1 — never expose this transport on a public interface. */
  host?: string;
  /** Bearer token required on every request. Generated clients must send Authorization: Bearer <token>. */
  authToken?: string;
  /** Explicit opt-out from auth (loopback-only local use). Logs a loud warning. */
  allowInsecureNoAuth?: boolean;
}

/**
 * Streamable HTTP transport, stateless mode: each POST /mcp gets a fresh
 * server+transport pair, so there is no session state to leak between
 * callers.
 *
 * Security posture: the transport drives every tool with the server's
 * configured API key (including refunds), so it binds loopback by default,
 * requires a bearer token, validates the Host header against DNS rebinding,
 * and caps request bodies.
 */
export async function serveHttp(
  config: ReevitConfig,
  port: number,
  opts: ServeHttpOptions = {},
): Promise<ReturnType<typeof createServer>> {
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

    // DNS-rebinding guard: only serve requests addressed at loopback. Without
    // this, any website the operator visits can POST to the local server.
    const reqHost = (req.headers.host ?? "").toLowerCase();
    const hostname = reqHost.replace(/:\d+$/, "");

    if (!LOOPBACK_HOSTS.has(hostname)) {
      res.writeHead(403, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "forbidden_host" }));

      return;
    }

    if (opts.authToken) {
      const expected = Buffer.from(`Bearer ${opts.authToken}`);
      const got = Buffer.from(req.headers.authorization ?? "");

      if (got.length !== expected.length || !timingSafeEqual(got, expected)) {
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

  return httpServer;
}
