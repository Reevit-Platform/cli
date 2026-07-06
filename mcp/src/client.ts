import { randomUUID } from "node:crypto";

/**
 * Minimal Reevit API client for the MCP server. Authentication is a scoped
 * API key — the backend enforces scopes on every route, so a payments:read
 * key simply gets a 403 from write tools; the server never widens access.
 */
export interface ReevitConfig {
  apiKey: string;
  baseUrl: string;
  /** "test" | "live" — the backend normalizes test to sandbox. */
  mode: string;
}

export function configFromEnv(env: NodeJS.ProcessEnv = process.env): ReevitConfig {
  const apiKey = env.REEVIT_API_KEY?.trim();
  if (!apiKey) {
    throw new Error(
      "REEVIT_API_KEY is required. Create a scoped API key in the Reevit dashboard (Developers → API keys).",
    );
  }

  const mode = (env.REEVIT_MODE?.trim() || "test").toLowerCase();
  if (mode !== "test" && mode !== "live") {
    throw new Error(`REEVIT_MODE must be "test" or "live", got "${mode}"`);
  }

  return {
    apiKey,
    baseUrl: (env.REEVIT_API_URL?.trim() || "https://api.reevit.io").replace(/\/+$/, ""),
    mode,
  };
}

export class ReevitApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

export interface RequestOptions {
  method?: string;
  query?: Record<string, string | number | undefined>;
  body?: unknown;
  /** Adds an Idempotency-Key header (required by money-moving routes). */
  idempotent?: boolean;
}

export type FetchLike = (url: string, init: RequestInit) => Promise<Response>;

export class ReevitClient {
  constructor(
    private readonly config: ReevitConfig,
    private readonly fetchImpl: FetchLike = fetch,
  ) {}

  get mode(): string {
    return this.config.mode;
  }

  async request<T = unknown>(path: string, options: RequestOptions = {}): Promise<T> {
    const url = new URL(`${this.config.baseUrl}/v1${path}`);

    for (const [key, value] of Object.entries(options.query ?? {})) {
      if (value !== undefined && value !== "") url.searchParams.set(key, String(value));
    }

    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      "X-Reevit-Key": this.config.apiKey,
      "X-Reevit-Mode": this.config.mode,
      "X-Reevit-Client": "@reevit/mcp",
    };

    if (options.idempotent) headers["Idempotency-Key"] = randomUUID();

    const response = await this.fetchImpl(url.toString(), {
      method: options.method ?? "GET",
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    });

    const text = await response.text();
    let parsed: unknown = undefined;

    try {
      parsed = text ? JSON.parse(text) : undefined;
    } catch {
      // non-JSON body — fall through with the raw text
    }

    if (!response.ok) {
      const err = (parsed ?? {}) as { code?: string; error?: string; message?: string };
      throw new ReevitApiError(
        response.status,
        err.code ?? err.error ?? `http_${response.status}`,
        err.message ?? err.error ?? text ?? `Reevit API error ${response.status}`,
      );
    }

    return parsed as T;
  }
}
