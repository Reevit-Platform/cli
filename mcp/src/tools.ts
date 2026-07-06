import { z } from "zod";

import { ReevitApiError, type ReevitClient } from "./client.js";

/**
 * Tool definitions for the Reevit MCP server.
 *
 * Authorization model: the server holds ONE scoped API key. The backend
 * enforces key scopes on every route, so scope errors (403) surface to the
 * agent verbatim — a payments:read key cannot refund no matter what the
 * model asks for. The only client-side gate is the live-money confirmation
 * on create_refund.
 */

type ToolResult = { content: { type: "text"; text: string }[]; isError?: boolean };

function ok(data: unknown): ToolResult {
  return { content: [{ type: "text", text: JSON.stringify(data, null, 2) }] };
}

function fail(message: string): ToolResult {
  return { content: [{ type: "text", text: message }], isError: true };
}

async function run(fn: () => Promise<ToolResult>): Promise<ToolResult> {
  try {
    return await fn();
  } catch (error) {
    if (error instanceof ReevitApiError) {
      return fail(
        `Reevit API error ${error.status} (${error.code}): ${error.message}` +
          (error.status === 403
            ? " — the configured API key does not have the scope this tool needs."
            : ""),
      );
    }

    return fail(error instanceof Error ? error.message : String(error));
  }
}

export interface ToolDefinition {
  name: string;
  description: string;
  // Zod raw shape, as the MCP SDK expects for inputSchema.
  inputSchema: z.ZodRawShape;
  handler: (args: Record<string, unknown>) => Promise<ToolResult>;
}

export function buildTools(client: ReevitClient): ToolDefinition[] {
  return [
    {
      name: "create_payment_link",
      description:
        "Create a shareable Reevit payment link a customer can open to pay. Returns the link, including its URL/code. Amounts are in minor units (e.g. GHS 50.00 = 5000).",
      inputSchema: {
        name: z.string().min(1).describe("Merchant-facing name for the link"),
        amount: z
          .number()
          .int()
          .positive()
          .optional()
          .describe("Fixed amount in minor units; omit with allow_custom_amount for open amounts"),
        currency: z.string().min(3).max(3).describe("ISO currency code, e.g. GHS"),
        description: z.string().optional().describe("Shown to the customer on the pay page"),
        allow_custom_amount: z
          .boolean()
          .optional()
          .describe("Let the customer choose the amount"),
        max_uses: z.number().int().positive().optional().describe("Deactivate after N payments"),
        expires_at: z
          .string()
          .optional()
          .describe("RFC3339 expiry timestamp, e.g. 2026-08-01T00:00:00Z"),
      },
      handler: (args) =>
        run(async () =>
          ok(
            await client.request("/payment-links", {
              method: "POST",
              idempotent: true,
              body: {
                name: args.name,
                amount: args.amount,
                currency: (args.currency as string | undefined)?.toUpperCase(),
                description: args.description,
                allow_custom_amount: args.allow_custom_amount ?? false,
                max_uses: args.max_uses,
                expires_at: args.expires_at,
                metadata: { created_via: "mcp" },
              },
            }),
          ),
        ),
    },
    {
      name: "get_payment",
      description: "Fetch one payment by its id (pay_...), including status, amounts, and route.",
      inputSchema: {
        payment_id: z.string().min(1).describe("The payment id, e.g. pay_abc123"),
      },
      handler: (args) =>
        run(async () =>
          ok(await client.request(`/payments/${encodeURIComponent(String(args.payment_id))}`)),
        ),
    },
    {
      name: "list_payments",
      description:
        "List recent payments, optionally filtered by status, customer, provider, or free-text search.",
      inputSchema: {
        status: z
          .string()
          .optional()
          .describe("Filter: succeeded, failed, pending, refunded, ..."),
        customer_id: z.string().optional(),
        provider: z.string().optional().describe("e.g. paystack, hubtel"),
        search: z.string().optional().describe("Free-text search (ids, references)"),
        limit: z.number().int().min(1).max(100).optional().describe("Default 20"),
      },
      handler: (args) =>
        run(async () =>
          ok(
            await client.request("/payments", {
              query: {
                status: args.status as string | undefined,
                customer_id: args.customer_id as string | undefined,
                provider: args.provider as string | undefined,
                search: args.search as string | undefined,
                limit: (args.limit as number | undefined) ?? 20,
              },
            }),
          ),
        ),
    },
    {
      name: "create_refund",
      description:
        "Refund a payment (fully, or partially via amount in minor units). Moves real money in live mode — live refunds require confirm: true.",
      inputSchema: {
        payment_id: z.string().min(1),
        amount: z
          .number()
          .int()
          .positive()
          .optional()
          .describe("Partial refund amount in minor units; omit for a full refund"),
        reason: z.string().optional(),
        confirm: z
          .boolean()
          .optional()
          .describe("REQUIRED (true) in live mode — explicit confirmation that money should move"),
      },
      handler: (args) =>
        run(async () => {
          if (client.mode === "live" && args.confirm !== true) {
            return fail(
              "Refusing to refund in live mode without confirm: true. Re-call create_refund with confirm: true once the user has explicitly approved moving real money.",
            );
          }

          return ok(
            await client.request(
              `/payments/${encodeURIComponent(String(args.payment_id))}/refund`,
              {
                method: "POST",
                idempotent: true,
                body: { amount: args.amount ?? 0, reason: args.reason },
              },
            ),
          );
        }),
    },
    {
      name: "get_analytics_summary",
      description:
        "Payment analytics summary: volume, counts, and success rate for the account (current mode).",
      inputSchema: {
        period: z
          .string()
          .optional()
          .describe("day, week, month, or all (default all)"),
      },
      handler: (args) =>
        run(async () =>
          ok(
            await client.request("/payments/stats", {
              query: { period: args.period as string | undefined },
            }),
          ),
        ),
    },
  ];
}
