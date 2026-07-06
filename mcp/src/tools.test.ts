import { describe, expect, it, vi } from "vitest";

import { ReevitClient, configFromEnv } from "./client.js";
import { buildTools } from "./tools.js";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function makeClient(mode: string, fetchImpl: ReturnType<typeof vi.fn>) {
  return new ReevitClient(
    { apiKey: "rk_test_123", baseUrl: "https://api.example.com", mode },
    fetchImpl as never,
  );
}

function tool(client: ReevitClient, name: string) {
  const found = buildTools(client).find((t) => t.name === name);
  if (!found) throw new Error(`tool ${name} not registered`);

  return found;
}

describe("configFromEnv", () => {
  it("requires an API key and validates mode", () => {
    expect(() => configFromEnv({} as NodeJS.ProcessEnv)).toThrow(/REEVIT_API_KEY/);
    expect(() =>
      configFromEnv({ REEVIT_API_KEY: "rk", REEVIT_MODE: "prod" } as NodeJS.ProcessEnv),
    ).toThrow(/test.*live/);

    const config = configFromEnv({ REEVIT_API_KEY: "rk" } as NodeJS.ProcessEnv);

    expect(config.mode).toBe("test");
    expect(config.baseUrl).toBe("https://api.reevit.io");
  });
});

describe("create_refund live-money gate", () => {
  it("refuses live refunds without confirm: true and never calls the API", async () => {
    const fetchImpl = vi.fn();
    const result = await tool(makeClient("live", fetchImpl), "create_refund").handler({
      payment_id: "pay_1",
    });

    expect(result.isError).toBe(true);
    expect(result.content[0].text).toMatch(/confirm: true/);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("allows live refunds with confirm: true, sending an idempotency key", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(200, { id: "ref_1" }));
    const result = await tool(makeClient("live", fetchImpl), "create_refund").handler({
      payment_id: "pay_1",
      confirm: true,
    });

    expect(result.isError).toBeUndefined();

    const [url, init] = fetchImpl.mock.calls[0];

    expect(url).toBe("https://api.example.com/v1/payments/pay_1/refund");
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBeTruthy();
    expect((init.headers as Record<string, string>)["X-Reevit-Mode"]).toBe("live");
  });

  it("allows test-mode refunds without confirmation", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(200, { id: "ref_1" }));
    const result = await tool(makeClient("test", fetchImpl), "create_refund").handler({
      payment_id: "pay_1",
    });

    expect(result.isError).toBeUndefined();
    expect(fetchImpl).toHaveBeenCalledOnce();
  });
});

describe("scope errors surface to the agent", () => {
  it("maps a 403 to a readable scope message", async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(jsonResponse(403, { code: "insufficient_scope", message: "missing payments:write" }));
    const result = await tool(makeClient("test", fetchImpl), "create_payment_link").handler({
      name: "Test link",
      currency: "GHS",
      amount: 5000,
    });

    expect(result.isError).toBe(true);
    expect(result.content[0].text).toMatch(/403/);
    expect(result.content[0].text).toMatch(/scope/);
  });
});

describe("request shaping", () => {
  it("create_payment_link posts the documented payload with auth headers", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(201, { id: "plink_1" }));

    await tool(makeClient("test", fetchImpl), "create_payment_link").handler({
      name: "Cookies",
      currency: "ghs",
      amount: 5000,
    });

    const [url, init] = fetchImpl.mock.calls[0];
    const body = JSON.parse(init.body as string);
    const headers = init.headers as Record<string, string>;

    expect(url).toBe("https://api.example.com/v1/payment-links");
    expect(body.currency).toBe("GHS");
    expect(body.metadata.created_via).toBe("mcp");
    expect(headers["X-Reevit-Key"]).toBe("rk_test_123");
    expect(headers["Idempotency-Key"]).toBeTruthy();
  });

  it("list_payments passes filters as query params", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(200, { payments: [] }));

    await tool(makeClient("test", fetchImpl), "list_payments").handler({
      status: "failed",
      limit: 5,
    });

    const [url] = fetchImpl.mock.calls[0];

    expect(url).toContain("/v1/payments?");
    expect(url).toContain("status=failed");
    expect(url).toContain("limit=5");
  });
});
