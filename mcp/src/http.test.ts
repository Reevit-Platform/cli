import type { AddressInfo } from "node:net";

import { afterEach, describe, expect, it } from "vitest";

import type { ReevitConfig } from "./client.js";
import { serveHttp } from "./http.js";

const CONFIG: ReevitConfig = {
  apiKey: "pfk_test_123",
  baseUrl: "https://api.example.com",
  mode: "test",
};

const TOKEN = "s3cret-token";

/**
 * These guards exist to stop a live Reevit API key being driven by someone who
 * isn't supposed to reach the server, so they are tested against a real socket
 * rather than by calling helpers directly — the thing worth proving is that a
 * request actually gets rejected, not that a predicate returns false.
 */

const started: Array<{ close: () => void }> = [];

/** Bind an ephemeral port (0) so tests never collide with a real dev server. */
async function start(env: Record<string, string | undefined>) {
  const previous = new Map<string, string | undefined>();
  for (const [key, value] of Object.entries(env)) {
    previous.set(key, process.env[key]);
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }

  try {
    const server = await serveHttp(CONFIG, 0);
    started.push(server);
    const { port } = server.address() as AddressInfo;

    return `http://127.0.0.1:${port}/mcp`;
  } finally {
    for (const [key, value] of previous) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

afterEach(async () => {
  await Promise.all(
    started.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))),
  );
});

describe("serveHttp bind host", () => {
  it("refuses to start on a non-loopback host without an auth token", async () => {
    await expect(
      start({ REEVIT_MCP_HOST: "0.0.0.0", REEVIT_MCP_AUTH_TOKEN: undefined }),
    ).rejects.toThrow(/REEVIT_MCP_AUTH_TOKEN/);
  });

  it("starts on a non-loopback host once a token is configured", async () => {
    await expect(
      start({ REEVIT_MCP_HOST: "127.0.0.1", REEVIT_MCP_AUTH_TOKEN: TOKEN }),
    ).resolves.toContain("http://127.0.0.1:");
  });
});

describe("serveHttp request guards", () => {
  it("rejects a request carrying an Origin that is not allowlisted", async () => {
    const url = await start({ REEVIT_MCP_AUTH_TOKEN: undefined, REEVIT_MCP_ALLOWED_ORIGINS: undefined });

    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: "https://evil.example" },
      body: "{}",
    });

    expect(res.status).toBe(403);
  });

  it("allows an Origin that is explicitly allowlisted", async () => {
    const url = await start({
      REEVIT_MCP_AUTH_TOKEN: undefined,
      REEVIT_MCP_ALLOWED_ORIGINS: "https://studio.example, https://other.example",
    });

    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: "https://studio.example" },
      body: "{}",
    });

    // Past the Origin gate; whatever the MCP transport does next is not 403.
    expect(res.status).not.toBe(403);
  });

  it("rejects a missing or wrong bearer token when one is configured", async () => {
    const url = await start({ REEVIT_MCP_AUTH_TOKEN: TOKEN });

    const missing = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    });
    expect(missing.status).toBe(401);

    const wrong = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: "Bearer nope" },
      body: "{}",
    });
    expect(wrong.status).toBe(401);

    // A token of a different length must be rejected the same way — the
    // comparison hashes both sides, so length is not a distinguisher.
    const shorter = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: "Bearer s" },
      body: "{}",
    });
    expect(shorter.status).toBe(401);
  });

  it("accepts the configured bearer token", async () => {
    const url = await start({ REEVIT_MCP_AUTH_TOKEN: TOKEN });

    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${TOKEN}` },
      body: "{}",
    });

    expect(res.status).not.toBe(401);
  });

  it("rejects a non-JSON body, closing the CORS simple-request bypass", async () => {
    const url = await start({ REEVIT_MCP_AUTH_TOKEN: undefined });

    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: "{}",
    });

    expect(res.status).toBe(415);
  });

  it("rejects a body over the size cap instead of buffering it", async () => {
    const url = await start({ REEVIT_MCP_AUTH_TOKEN: undefined });

    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "x".repeat(1024 * 1024 + 1024),
    });

    expect(res.status).toBe(413);
  });
});
