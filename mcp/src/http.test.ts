import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { AddressInfo } from "node:net";
import { request as httpRequest, type createServer } from "node:http";

import { serveHttp } from "./http.js";
import type { ReevitConfig } from "./client.js";

const config: ReevitConfig = {
  apiKey: "pfk_test_fake",
  baseUrl: "http://localhost:8080",
  mode: "test",
};

const MCP_HEADERS = {
  "content-type": "application/json",
  accept: "application/json, text/event-stream",
};

describe("serveHttp", () => {
  let server: ReturnType<typeof createServer> | undefined;
  let baseUrl = "";

  beforeEach(async () => {
    server = await serveHttp(config, 0, { authToken: "test-token-123" });
    const addr = server.address() as AddressInfo;
    baseUrl = `http://127.0.0.1:${addr.port}`;
  });

  afterEach(async () => {
    await new Promise<void>((resolve) => {
      if (server) {
        server.close(() => resolve());
      } else {
        resolve();
      }
    });
    server = undefined;
  });

  it("binds to loopback by default", () => {
    const addr = server!.address() as AddressInfo;
    expect(addr.address).toBe("127.0.0.1");
  });

  it("requires a token (or explicit insecure opt-out) at startup", async () => {
    await expect(serveHttp(config, 0)).rejects.toThrow(/REEVIT_MCP_HTTP_TOKEN/);
  });

  it("rejects requests without Authorization", async () => {
    const res = await fetch(`${baseUrl}/mcp`, {
      method: "POST",
      headers: MCP_HEADERS,
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }),
    });

    expect(res.status).toBe(401);
  });

  it("rejects requests with a wrong token", async () => {
    const res = await fetch(`${baseUrl}/mcp`, {
      method: "POST",
      headers: { ...MCP_HEADERS, authorization: "Bearer wrong-token-xx" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }),
    });

    expect(res.status).toBe(401);
  });

  it("rejects non-loopback Host headers (DNS rebinding)", async () => {
    // fetch() silently drops a user-set Host header, so drive this one with
    // raw http to actually send the spoofed value.
    const addr = server!.address() as AddressInfo;
    const status = await new Promise<number>((resolve, reject) => {
      const req = httpRequest(
        {
          host: "127.0.0.1",
          port: addr.port,
          path: "/mcp",
          method: "POST",
          headers: {
            ...MCP_HEADERS,
            authorization: "Bearer test-token-123",
            host: "evil.example",
          },
        },
        (res) => {
          res.resume();
          res.on("end", () => resolve(res.statusCode ?? 0));
        },
      );
      req.on("error", reject);
      req.end(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }));
    });

    expect(status).toBe(403);
  });

  it("admits a correctly authorized request past the auth gates", async () => {
    const res = await fetch(`${baseUrl}/mcp`, {
      method: "POST",
      headers: { ...MCP_HEADERS, authorization: "Bearer test-token-123" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "initialize",
        params: {
          protocolVersion: "2024-11-05",
          capabilities: {},
          clientInfo: { name: "test", version: "0.0.1" },
        },
      }),
    });

    // Whatever the MCP transport returns for the session-less initialize, the
    // request must have passed the 401/403 gates.
    expect(res.status).not.toBe(401);
    expect(res.status).not.toBe(403);
  });

  it("rejects oversized bodies with 413", async () => {
    const res = await fetch(`${baseUrl}/mcp`, {
      method: "POST",
      headers: { ...MCP_HEADERS, authorization: "Bearer test-token-123" },
      body: "x".repeat(1_500_000),
    });

    expect(res.status).toBe(413);
  });
});
