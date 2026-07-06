#!/usr/bin/env node
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";

import { configFromEnv, ReevitClient } from "./client.js";
import { buildTools } from "./tools.js";

async function main() {
  const config = configFromEnv();
  const client = new ReevitClient(config);

  const server = new McpServer({ name: "reevit", version: "0.1.0" });

  for (const tool of buildTools(client)) {
    server.registerTool(
      tool.name,
      { description: tool.description, inputSchema: tool.inputSchema },
      tool.handler as never,
    );
  }

  await server.connect(new StdioServerTransport());

  // Mode is worth announcing once on stderr (stdout is the MCP transport).
  console.error(`reevit-mcp ready (${config.mode} mode, ${config.baseUrl})`);
}

main().catch((error) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
});
