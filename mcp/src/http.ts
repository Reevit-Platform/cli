import { createServer } from "node:http";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";

import { ReevitClient, type ReevitConfig } from "./client.js";
import { buildTools } from "./tools.js";

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

/**
 * Streamable HTTP transport, stateless mode: each POST /mcp gets a fresh
 * server+transport pair, so there is no session state to leak between
 * callers. Auth stays the configured scoped API key (this is a self-hosted
 * endpoint — put it behind your own network boundary).
 */
export async function serveHttp(config: ReevitConfig, port: number): Promise<void> {
  const httpServer = createServer(async (req, res) => {
    if (!req.url?.startsWith("/mcp")) {
      res.writeHead(404).end();
      return;
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
      for await (const chunk of req) chunks.push(chunk as Buffer);
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

  await new Promise<void>((resolve) => httpServer.listen(port, resolve));
  console.error(`reevit-mcp listening on http://localhost:${port}/mcp (${config.mode} mode)`);
}
